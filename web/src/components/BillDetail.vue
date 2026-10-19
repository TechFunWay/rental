<template>
  <div class="space-y-4">
    <!-- 账单概要 -->
    <div class="rounded-xl bg-muted/60 px-3.5 py-3 text-sm space-y-2">
      <div class="flex items-center justify-between gap-3">
        <div class="flex items-center gap-2 min-w-0">
          <span class="text-base font-bold text-foreground">{{ bill.room_no }}</span>
          <span v-if="cycleMonths(bill.pay_cycle) > 1" class="badge !px-1.5 !py-0.5 text-[11px] bg-indigo-500/10 text-indigo-600 dark:text-indigo-300" :title="cycleBadgeTitle">{{ cycleLabel(bill.pay_cycle) }}</span>
        </div>
        <span class="badge shrink-0" :class="statusClass">{{ statusText }}</span>
      </div>
      <div class="grid grid-cols-2 gap-x-4 gap-y-1 text-xs">
        <div class="flex justify-between gap-2"><span class="text-muted-foreground shrink-0">租户</span><span class="text-foreground truncate" :title="bill.tenant_name">{{ bill.tenant_name || '—' }}</span></div>
        <div class="flex justify-between gap-2"><span class="text-muted-foreground shrink-0">账期</span><span class="text-foreground tabular-nums">{{ bill.period }}</span></div>
        <div class="flex justify-between gap-2"><span class="text-muted-foreground shrink-0">缴费周期</span><span class="text-foreground">{{ cycleText }}</span></div>
        <div class="flex justify-between gap-2"><span class="text-muted-foreground shrink-0">单据编号</span><span class="text-foreground tabular-nums truncate">{{ bill.receipt_no || '—' }}</span></div>
        <div v-if="bill.remark" class="flex justify-between gap-2 col-span-2"><span class="text-muted-foreground shrink-0">备注</span><span class="text-foreground truncate" :title="bill.remark">{{ bill.remark }}</span></div>
      </div>
    </div>

    <!-- 抄表照片：本期读数凭证（只看不传，上传在抄表入口）。没拍过就不占地方。 -->
    <div class="rounded-xl border border-border p-3">
      <MeterPhotoGallery :room-id="bill.room_id" :period="bill.period" :water-mixed="bill.water_mode === 'monthly'" />
    </div>

    <!-- 费用项目：桌面表格 / 手机卡片 -->
    <div>
      <p class="text-xs font-semibold text-muted-foreground mb-1.5">费用项目（{{ items.length }} 项）</p>
      <table class="hidden sm:table w-full text-sm border border-border rounded-xl overflow-hidden">
        <thead>
          <tr class="bg-muted/60 text-muted-foreground text-xs">
            <th class="py-2 px-2.5 text-left font-medium whitespace-nowrap">项目</th>
            <th class="py-2 px-2.5 text-left font-medium">计费说明</th>
            <th class="py-2 px-2.5 text-right font-medium whitespace-nowrap">周期</th>
            <th class="py-2 px-2.5 text-right font-medium whitespace-nowrap">金额（元）</th>
            <th class="py-2 px-2.5 text-right font-medium whitespace-nowrap">已收（元）</th>
            <th class="py-2 px-2.5 text-right font-medium whitespace-nowrap">欠缴（元）</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border/70">
          <tr v-for="it in items" :key="it.key" class="text-foreground">
            <td class="py-2 px-2.5 font-medium whitespace-nowrap">{{ it.name }}</td>
            <td class="py-2 px-2.5 text-xs text-muted-foreground">{{ it.detail || '—' }}</td>
            <td class="py-2 px-2.5 text-right text-muted-foreground whitespace-nowrap">{{ cycleShort(it.cycle) }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums">{{ fmtMoney(it.amount) }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(it.paid) }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums" :class="it.arrears > 0.005 ? 'text-rose-500 font-semibold' : 'text-muted-foreground'">{{ fmtMoney(it.arrears) }}</td>
          </tr>
        </tbody>
        <tfoot>
          <!-- 有上期结转时把差额单列一行，再给「本期应缴」合计；
               没有结转（未开启或首账）时只剩一行，与改造前完全一致。 -->
          <tr v-if="carryInOf(bill)" class="text-amber-600 dark:text-amber-300">
            <td class="py-1.5 px-2.5" colspan="3">{{ carryInOf(bill) > 0 ? '加：上期少交结转' : '减：上期多交结转' }}</td>
            <td class="py-1.5 px-2.5 text-right tabular-nums">{{ carryInOf(bill) > 0 ? '+' : '-' }}{{ fmtMoney(Math.abs(carryInOf(bill))) }}</td>
            <td class="py-1.5 px-2.5"></td>
            <td class="py-1.5 px-2.5"></td>
          </tr>
          <tr class="border-t-2 border-border font-bold bg-muted/40">
            <td class="py-2 px-2.5" colspan="3">{{ carryInOf(bill) ? '本期应缴' : '应付合计' }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums">{{ fmtMoney(payableOf(bill)) }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(bill.paid_amount) }}</td>
            <td class="py-2 px-2.5 text-right tabular-nums" :class="isArrears(bill) ? 'text-rose-500' : 'text-muted-foreground'">{{ fmtMoney(arrearsOf(bill)) }}</td>
          </tr>
        </tfoot>
      </table>
      <!-- 手机端卡片化 -->
      <div class="sm:hidden rounded-xl border border-border overflow-hidden">
        <div class="divide-y divide-border/70">
          <div v-for="it in items" :key="it.key" class="px-3 py-2 space-y-1">
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="text-sm font-medium text-foreground">{{ it.name }}<span class="text-[11px] text-muted-foreground ml-1.5">{{ cycleShort(it.cycle) }}</span></div>
                <div v-if="it.detail" class="text-[11px] text-muted-foreground tabular-nums mt-0.5">{{ it.detail }}</div>
              </div>
              <div class="text-sm font-semibold text-foreground tabular-nums shrink-0">{{ fmtMoney(it.amount) }}</div>
            </div>
            <div class="flex gap-3 text-[11px] tabular-nums">
              <span class="text-emerald-600 dark:text-emerald-400">已收 {{ fmtMoney(it.paid) }}</span>
              <span v-if="it.arrears > 0.005" class="text-rose-500 font-semibold">欠缴 {{ fmtMoney(it.arrears) }}</span>
            </div>
          </div>
        </div>
        <div class="border-t-2 border-border px-3 py-2 space-y-1 text-sm bg-muted/40">
          <div class="flex justify-between gap-3 font-bold text-foreground">
            <span>应付合计</span><span class="tabular-nums">{{ fmtMoney(bill.total_amount) }}</span>
          </div>
          <div class="flex justify-between gap-3 text-xs">
            <span class="text-muted-foreground">已收 / 欠缴</span>
            <span class="tabular-nums">
              <span class="text-emerald-600 dark:text-emerald-400">{{ fmtMoney(bill.paid_amount) }}</span>
              <span class="text-muted-foreground"> / </span>
              <span :class="isArrears(bill) ? 'text-rose-500 font-semibold' : 'text-muted-foreground'">{{ fmtMoney(arrearsOf(bill)) }}</span>
            </span>
          </div>
        </div>
      </div>
      <!-- 排除项目说明：出账时按租户勾选跳过的项目 -->
      <p v-if="excludedNames" class="text-[11px] text-muted-foreground mt-1.5">
        本租户不参与：{{ excludedNames }}（出账时费用为 0，不在上表中）。
      </p>
    </div>

    <!-- 收款流水 -->
    <div>
      <p class="text-xs font-semibold text-muted-foreground mb-1.5">收款流水（{{ payments.length }} 笔）</p>
      <div v-if="payments.length === 0" class="text-xs text-muted-foreground py-3 text-center rounded-xl bg-muted/40">暂无收款记录</div>
      <div v-else class="rounded-xl border border-border divide-y divide-border/70">
        <div v-for="p in payments" :key="p.id" class="px-3 py-2 space-y-1">
          <div class="flex items-center justify-between gap-3 text-sm">
            <span class="text-foreground tabular-nums">{{ fmtDate(p.paid_at) }}</span>
            <span class="font-semibold text-emerald-600 dark:text-emerald-400 tabular-nums">{{ fmtMoney(p.amount) }} 元</span>
          </div>
          <div v-if="p.items && p.items.length" class="text-[11px] text-muted-foreground tabular-nums">
            {{ p.items.map((s) => `${s.name} ${fmtMoney(s.amount)} 元`).join(' · ') }}
          </div>
          <div v-if="p.note" class="text-[11px] text-muted-foreground">{{ p.note }}</div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import MeterPhotoGallery from './MeterPhotoGallery.vue'
import { arrearsOf, carryInOf, fmtMoney, isArrears, payableOf, type Bill, type BillItemDetail, type FeeItem } from '../api/rental'
import { coveredPeriodText, cycleLabel, cycleMonths, cycleShort as cycleShortFn } from '../utils/cycle'

// 账单详情：全部费用项目（含自定义项目）逐项列出金额/已收/欠缴，
// 附收款流水。数据来自 /api/rental/bills/:id（服务端算好分摊）。
const props = defineProps<{
  bill: Bill
  /** 账单费用明细（内置 + 自定义收费项目，金额为 0 的项目不生成） */
  items: BillItemDetail[]
  /** 收款流水（按时间倒序） */
  payments: { id: number; paid_at: string; amount: number; note: string; items: { key: string; name: string; amount: number }[] }[]
  /** 收费项目定义：把账单快照的排除 key 翻译成名称 */
  feeItems?: FeeItem[]
}>()

const statusText = computed(() => {
  if (props.bill.status === 'paid') return '已缴清'
  if (props.bill.status === 'partial') return '部分已缴'
  return '未缴纳'
})
const statusClass = computed(() => {
  if (props.bill.status === 'paid') return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  if (props.bill.status === 'partial') return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
})

const coveredText = computed(() => coveredPeriodText(props.bill.period, props.bill.pay_cycle))
// 角标 title：显示文字只有周期名，而年付房在非到期月出的水电账单同样带「年付」
// 角标，所以 title 要说清「周期只管固定费，本单水电仍按月」，避免被误读成整单
// 收了 N 个月租金。
const cycleBadgeTitle = computed(() => {
  const months = cycleMonths(props.bill.pay_cycle)
  return `${cycleLabel(props.bill.pay_cycle)}：固定费每 ${months} 个月收一次，本单水电按月 · 覆盖 ${coveredText.value}`
})
// 缴费周期文案：多月周期写清覆盖区间与月数（季付 3 / 半年付 6 / 年付 12），
// 月付直接显示周期名——不再把「非月付」一律说成季付。
const cycleText = computed(() => {
  const months = cycleMonths(props.bill.pay_cycle)
  const label = cycleLabel(props.bill.pay_cycle)
  return months > 1 ? `${label}（${coveredText.value}，共 ${months} 个月）` : label
})

// 明细行的周期缩写统一走 cycleShort（月/季/半/年）。
const cycleShort = cycleShortFn

// 排除项目名称（租户收费项目勾选中未勾选的部分，账单快照）
const excludedNames = computed(() =>
  (props.bill.excluded_fees || [])
    .map((k) => props.feeItems?.find((f) => f.key === k)?.name ?? k)
    .join('、'),
)

function fmtDate(s: string): string {
  return (s || '').slice(0, 10)
}
</script>
