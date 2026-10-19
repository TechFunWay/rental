<template>
  <!-- 收费单据：弹窗内预览，打印时仅保留本节点 -->
  <div ref="receiptEl" class="receipt-wrap">
    <div class="receipt surface rounded-2xl">
      <div class="px-6 py-7">
        <!-- 抬头 -->
        <div class="text-center border-b-2 border-dashed border-border pb-4 mb-5">
          <h2 class="receipt-title text-xl font-bold text-foreground tracking-wide">
            {{ property.name || '租金收费单据' }}
          </h2>
          <p class="text-xs text-muted-foreground mt-1">{{ periodLabel }} 租金及费用收缴凭证</p>
          <p class="text-xs text-muted-foreground mt-0.5 tabular-nums">单据编号：{{ bill.receipt_no || '—' }}</p>
        </div>

        <!-- 基本信息 -->
        <div class="receipt-grid text-xs mb-4">
          <div class="receipt-item"><span class="receipt-label">房号</span><span class="receipt-value font-semibold">{{ bill.room_no }}</span></div>
          <div class="receipt-item"><span class="receipt-label">租户</span><span class="receipt-value font-semibold">{{ bill.tenant_name || '—' }}</span></div>
          <div class="receipt-item"><span class="receipt-label">账期</span><span class="receipt-value tabular-nums">{{ bill.period }}</span></div>
          <div class="receipt-item"><span class="receipt-label">缴费周期</span><span class="receipt-value">{{ cycleText }}</span></div>
          <div class="receipt-item col-span-2"><span class="receipt-label">开票日期</span><span class="receipt-value tabular-nums">{{ today }}</span></div>
        </div>

        <!-- 费用明细表：桌面端/打印用表格，窄屏（<640px）改卡片化明细，避免六列挤成一列字 -->
        <table class="receipt-table hidden sm:table w-full text-sm border border-border mb-4">
          <thead>
            <tr class="bg-muted/60 text-muted-foreground">
              <th class="receipt-th text-left">项目</th>
              <th class="receipt-th text-right">上月读数</th>
              <th class="receipt-th text-right">本月读数</th>
              <th class="receipt-th text-right">用量</th>
              <th class="receipt-th text-right">单价</th>
              <th class="receipt-th text-right">金额（元）</th>
            </tr>
          </thead>
          <tbody>
            <!--
              明细行由 receiptRows 统一驱动，而不是写死六行内置项目。
              原先是硬编码的租金/水/电/燃气/卫生/管理六行，后果是：
              ① 押金、自定义收费项目（宽带费等）在单据上**根本不出现**，
                 但合计里又含它们 —— 桌面单据的明细加起来对不上合计
                 （实测：DJ01 首张账单明细只有 1,500，合计却是 3,530）；
              ② 手机端卡片走的是 feeRows（数据驱动），两端显示还不一致。
              现在两端共用 receiptRows：内置读数项带读数/单价列，
              押金与自定义项只在「项目 + 金额」两列有值（其余列用 — 占位）。
            -->
            <tr v-for="row in receiptRows" :key="row.key">
              <td class="receipt-td">{{ row.name }}</td>
              <template v-if="row.isReading">
                <td class="receipt-td text-right tabular-nums">{{ row.lastText }}</td>
                <td class="receipt-td text-right tabular-nums">{{ row.nowText }}</td>
                <td class="receipt-td text-right tabular-nums">{{ row.usageText }}</td>
                <td class="receipt-td text-right tabular-nums">{{ row.priceText }}</td>
              </template>
              <template v-else>
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right">—</td>
              </template>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(row.amount) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t-2 border-border font-bold text-foreground">
              <td class="receipt-td" colspan="5">本期费用合计（含租金与其他费用）</td>
              <td class="receipt-td text-right tabular-nums">￥{{ fmtMoney(bill.total_amount) }}</td>
            </tr>
            <!-- 结转只在台账开启且确有上期差额时出现；没有这一行时单据与改造前一致，
                 避免给未使用结转的房东平白多出一行「上期结转 ￥0.00」。 -->
            <tr v-if="carryInOf(bill)" class="text-foreground">
              <td class="receipt-td" colspan="5">
                {{ carryInOf(bill) > 0 ? '加：上期少交结转' : '减：上期多交结转' }}
              </td>
              <td class="receipt-td text-right tabular-nums">
                {{ carryInOf(bill) > 0 ? '+' : '-' }}￥{{ fmtMoney(Math.abs(carryInOf(bill))) }}
              </td>
            </tr>
            <tr class="border-t border-border font-bold text-foreground">
              <td class="receipt-td" colspan="5">本期应缴</td>
              <td class="receipt-td text-right tabular-nums">￥{{ fmtMoney(payableOf(bill)) }}</td>
            </tr>
            <tr class="text-foreground">
              <td class="receipt-td" colspan="5">已收金额{{ bill.paid_at ? `（${fmtDate(bill.paid_at)}）` : '' }}</td>
              <td class="receipt-td text-right tabular-nums">￥{{ fmtMoney(bill.paid_amount) }}</td>
            </tr>
            <tr class="font-bold" :class="isArrears(bill) ? 'text-rose-600 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'">
              <td class="receipt-td" colspan="5">{{ isArrears(bill) ? '欠缴金额' : '缴费状态' }}</td>
              <td class="receipt-td text-right tabular-nums">
                {{ isArrears(bill) ? `￥${fmtMoney(arrearsOf(bill))}` : '已缴清' }}
              </td>
            </tr>
          </tfoot>
        </table>

        <!-- 手机端：费用明细卡片化（打印时隐藏，仍输出上面的表格）。
             与桌面表格共用 receiptRows，两端项目与金额完全一致。 -->
        <div class="receipt-mobile sm:hidden border border-border rounded-xl mb-4 overflow-hidden">
          <div class="divide-y divide-border/70">
            <div v-for="row in receiptRows" :key="row.key" class="px-3 py-2 flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="text-sm font-medium text-foreground">{{ row.name }}</div>
                <div v-if="mobileDetail(row)" class="text-[11px] text-muted-foreground tabular-nums mt-0.5">{{ mobileDetail(row) }}</div>
              </div>
              <div class="text-sm font-semibold text-foreground tabular-nums shrink-0">{{ fmtMoney(row.amount) }}</div>
            </div>
          </div>
          <div class="border-t-2 border-border px-3 py-2 space-y-1 text-sm bg-muted/40">
            <div class="flex justify-between gap-3 font-bold text-foreground">
              <span>本期费用合计（含租金与其他费用）</span>
              <span class="tabular-nums">￥{{ fmtMoney(bill.total_amount) }}</span>
            </div>
            <div v-if="carryInOf(bill)" class="flex justify-between gap-3 text-foreground">
              <span>{{ carryInOf(bill) > 0 ? '加：上期少交结转' : '减：上期多交结转' }}</span>
              <span class="tabular-nums">{{ carryInOf(bill) > 0 ? '+' : '-' }}￥{{ fmtMoney(Math.abs(carryInOf(bill))) }}</span>
            </div>
            <div class="flex justify-between gap-3 font-bold text-foreground border-t border-border pt-1">
              <span>本期应缴</span>
              <span class="tabular-nums">￥{{ fmtMoney(payableOf(bill)) }}</span>
            </div>
            <div class="flex justify-between gap-3 text-foreground">
              <span>已收金额{{ bill.paid_at ? `（${fmtDate(bill.paid_at)}）` : '' }}</span>
              <span class="tabular-nums">￥{{ fmtMoney(bill.paid_amount) }}</span>
            </div>
            <div class="flex justify-between gap-3 font-bold" :class="isArrears(bill) ? 'text-rose-600 dark:text-rose-400' : 'text-emerald-600 dark:text-emerald-400'">
              <span>{{ isArrears(bill) ? '欠缴金额' : '缴费状态' }}</span>
              <span class="tabular-nums">{{ isArrears(bill) ? `￥${fmtMoney(arrearsOf(bill))}` : '已缴清' }}</span>
            </div>
          </div>
        </div>

        <!-- 底部 -->
        <div class="text-xs text-muted-foreground space-y-1">
          <p v-if="bill.remark">备注：{{ bill.remark }}</p>
          <p v-if="property.note">{{ property.note }}</p>
          <div class="flex flex-col sm:flex-row sm:justify-between gap-1 pt-2">
            <span>联系电话：{{ property.contact || '—' }}</span>
            <span>收款确认（出租方签字）：＿＿＿＿＿＿</span>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { arrearsOf, carryInOf, fmtMoney, isArrears, payableOf, type Bill, type PropertyMeta } from '../api/rental'
import { coveredPeriodText, cycleLabel, cycleMonths } from '../utils/cycle'
import { appendPrintPage } from '../utils/print-root'

const props = defineProps<{
  bill: Bill
  property: PropertyMeta
  /** 后端费用明细（含自定义收费项目）；没有时回退由 bills 固定列生成 */
  items?: {
    key: string; name: string; kind: string; amount: number; detail: string
    /** 单据不单列该项目（台账级「单据显示」设置） */
    receipt_hidden?: boolean
  }[]
}>()

// 打印暂存：挂载即把自身克隆进 body 直属的打印根（#print-root，屏幕上不可见），
// 浏览器菜单 Ctrl+P 不经打印按钮也能只打出单据；卸载时撤下克隆。
const receiptEl = ref<HTMLElement | null>(null)
let removeStaged: (() => void) | null = null
onMounted(() => {
  if (receiptEl.value) removeStaged = appendPrintPage(receiptEl.value)
})
onBeforeUnmount(() => {
  removeStaged?.()
  removeStaged = null
})

const periodLabel = computed(() => {
  const m = props.bill.period.match(/^(\d{4})-(\d{2})$/)
  return m ? `${m[1]} 年 ${Number(m[2])} 月` : props.bill.period
})

// 缴费周期文案：季付账单覆盖 3 个月，写清覆盖区间便于对账
const cycleText = computed(() => {
  const months = cycleMonths(props.bill.pay_cycle)
  const label = cycleLabel(props.bill.pay_cycle)
  return months > 1 ? `${label}（${coveredPeriodText(props.bill.period, props.bill.pay_cycle)}，共 ${months} 个月）` : label
})

const today = computed(() => new Date().toISOString().slice(0, 10))

// 单据行：桌面表格与手机卡片共用同一份数据，保证两端一致、且明细加起来等于合计。
// 内置读数项（水/电/燃气）带读数与单价列；押金与自定义项只是一笔金额。
interface ReceiptRow {
  key: string
  name: string
  detail: string
  amount: number
  isReading: boolean
  lastText?: string
  nowText?: string
  usageText?: string
  priceText?: string
}

// 手机卡片的一行副文本：读数项此前只渲染 row.detail，而 detail 在读数项上恒为空
// （读数搬到了 lastText/nowText/usageText/priceText），于是手机上只剩「水费 45.00」，
// 用量与单价全丢（桌面表格正常，2026-10-11 审计发现的回归）。这里把三列读数
// 拼成一行给卡片用，两条渲染路径的数据来源仍是同一份 receiptRows。
function mobileDetail(row: ReceiptRow): string {
  if (!row.isReading) return row.detail
  const parts: string[] = []
  if (row.lastText && row.lastText !== '—') parts.push(`上次 ${row.lastText}`)
  if (row.nowText && row.nowText !== '—') parts.push(`本次 ${row.nowText}`)
  if (row.usageText && row.usageText !== '—') parts.push(`用量 ${row.usageText}`)
  if (row.priceText) parts.push(row.priceText.includes('/月') ? row.priceText : `单价 ${row.priceText}`)
  return parts.join(' · ')
}

const receiptRows = computed<ReceiptRow[]>(() => {
  const b = props.bill
  const unit = (k: string) => (k === 'water' ? '吨' : k === 'elec' ? '度' : '方')
  const reading = (key: string, name: string, last: number, now: number, price: number, amount: number, monthly: boolean): ReceiptRow => {
    if (monthly) {
      // 包月水费不看抄表：读数与用量列留空，单价即每月金额
      return { key, name, detail: '', amount, isReading: true,
        lastText: '—', nowText: '—', usageText: '—', priceText: `${fmtRead(price)} 元/月` }
    }
    const u = unit(key)
    return { key, name, detail: '', amount, isReading: true,
      lastText: `${fmtRead(last)} ${u}`, nowText: `${fmtRead(now)} ${u}`,
      usageText: `${fmtRead(usage(last, now))} ${u}`, priceText: `${fmtRead(price)} 元/${u}` }
  }

  // 优先用后端 bill_items（含押金与自定义收费项目），保证与合计同源。
  if (props.items && props.items.length) {
    const rows: ReceiptRow[] = []
    // 被设为「单据不单列」的项目：不单独出现，但金额不能凭空丢掉——
    // 否则单据明细加起来会比合计少一截（正是这次粉丝反馈的问题形态）。
    // 统一合并成一行「其他费用」，合计仍然对得上。
    const hidden: { name: string; amount: number }[] = []
    let hiddenTotal = 0
    for (const it of props.items) {
      if (it.receipt_hidden) {
        if (it.amount > 0) {
          hidden.push({ name: it.name, amount: it.amount })
          hiddenTotal += it.amount
        }
        continue
      }
      if (it.key === 'water') {
        rows.push(reading('water', it.name, b.water_last, b.water_now, b.water_price, it.amount, b.water_mode === 'monthly'))
      } else if (it.key === 'elec') {
        rows.push(reading('elec', it.name, b.elec_last, b.elec_now, b.elec_price, it.amount, false))
      } else if (it.key === 'gas') {
        rows.push(reading('gas', it.name, b.gas_last, b.gas_now, b.gas_price, it.amount, false))
      } else {
        rows.push({ key: it.key, name: it.name, detail: it.detail, amount: it.amount, isReading: false })
      }
    }
    if (hiddenTotal > 0) {
      // 逐项名称刻意不写进明细说明：房东选择「单据不显示」往往就是不想让租户
      // 看到具体名目（例如平台服务费），列出来等于没隐藏。
      rows.push({
        key: 'receipt_hidden_merged',
        name: '其他费用',
        detail: hidden.length > 1 ? '合计' : '',
        amount: Math.round(hiddenTotal * 100) / 100,
        isReading: false,
      })
    }
    return rows
  }

  // 回退：后端没给明细时由 bills 固定列生成（老数据兼容），含押金与自定义合计。
  const rows: ReceiptRow[] = []
  rows.push({ key: 'rent', name: '租金', detail: '', amount: b.rent, isReading: false })
  rows.push(reading('water', b.water_mode === 'monthly' ? '水费（包月）' : '水费',
    b.water_last, b.water_now, b.water_price, b.water_fee, b.water_mode === 'monthly'))
  rows.push(reading('elec', '电费', b.elec_last, b.elec_now, b.elec_price, b.elec_fee, false))
  rows.push(reading('gas', '燃气费', b.gas_last, b.gas_now, b.gas_price, b.gas_fee, false))
  rows.push({ key: 'sanitation', name: '卫生费', detail: '', amount: b.sanitation_fee, isReading: false })
  rows.push({ key: 'management', name: '管理费', detail: '', amount: b.management_fee, isReading: false })
  if (b.deposit_fee > 0) {
    rows.push({ key: 'deposit', name: '押金', detail: '入住押金，退租时结算退还', amount: b.deposit_fee, isReading: false })
  }
  if (b.extra_amount > 0) {
    rows.push({ key: 'custom', name: '其他费用', detail: '自定义收费项目合计', amount: b.extra_amount, isReading: false })
  }
  return rows
})


function usage(last: number, now: number): number {
  return now > last ? Math.round((now - last) * 100) / 100 : 0
}
function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}
function fmtDate(s: string): string {
  return (s || '').slice(0, 10)
}
</script>

<style scoped>
/* 单据内部排版类（此前只写 class 没有定义样式，导致基本信息与表格挤压换行） */
.receipt-grid {
  @apply grid grid-cols-2 gap-x-4 gap-y-1.5;
}
.receipt-item {
  @apply flex items-baseline justify-between gap-2 min-w-0;
}
.receipt-label {
  @apply text-muted-foreground shrink-0;
}
.receipt-value {
  @apply text-right min-w-0 truncate;
}
.receipt-th {
  @apply px-2 py-2 font-medium whitespace-nowrap;
}
.receipt-td {
  @apply px-2 py-2 whitespace-nowrap;
}

/* 打印：页面隔离由 print-root.ts（body 直属 #print-root + display 隔离）负责，
   这里只管单据本体——强制输出表格版明细、隐藏手机卡片、打印配色。
   注意：隐藏页面其余内容的规则不能写在本组件的 scoped 样式里，
   `body *` 会被编译成 `body[data-v-xxx] *` 而永远匹配不到（旧版打印整张网页的根因）。 */
@media print {
  /* 打印始终输出表格版明细，隐藏手机卡片版 */
  .receipt-table {
    display: table !important;
  }
  .receipt-mobile {
    display: none !important;
  }
  .receipt {
    border: none !important;
    box-shadow: none !important;
    background: white !important;
    border-radius: 0 !important;
  }
  .dark .receipt {
    background: white !important;
  }
  .receipt .text-foreground,
  .receipt .receipt-value,
  .receipt .receipt-td {
    color: #111 !important;
  }
  .receipt .text-muted-foreground {
    color: #444 !important;
  }
  @page {
    size: A5 portrait;
    margin: 12mm;
  }
}
</style>
