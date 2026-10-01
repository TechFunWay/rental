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
            <tr>
              <td class="receipt-td">租金</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.rent) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">{{ bill.water_mode === 'monthly' ? '水费（包月）' : '水费' }}</td>
              <!-- 包月水费不看抄表：读数与用量列留空，单价即每月金额 -->
              <template v-if="bill.water_mode === 'monthly'">
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right">—</td>
                <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_price) }} 元/月</td>
              </template>
              <template v-else>
                <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_last) }} 吨</td>
                <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_now) }} 吨</td>
                <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.water_last, bill.water_now)) }} 吨</td>
                <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_price) }} 元/吨</td>
              </template>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.water_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">电费</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_last) }} 度</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_now) }} 度</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.elec_last, bill.elec_now)) }} 度</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_price) }} 元/度</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.elec_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">燃气费</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_last) }} 方</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_now) }} 方</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.gas_last, bill.gas_now)) }} 方</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_price) }} 元/方</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.gas_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">卫生费</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.sanitation_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">管理费</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right">—</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.management_fee) }}</td>
            </tr>
          </tbody>
          <tfoot>
            <tr class="border-t-2 border-border font-bold text-foreground">
              <td class="receipt-td" colspan="5">应付合计（含租金与其他费用）</td>
              <td class="receipt-td text-right tabular-nums">￥{{ fmtMoney(bill.total_amount) }}</td>
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

        <!-- 手机端：费用明细卡片化（打印时隐藏，仍输出上面的表格） -->
        <div class="receipt-mobile sm:hidden border border-border rounded-xl mb-4 overflow-hidden">
          <div class="divide-y divide-border/70">
            <div v-for="row in feeRows" :key="row.name" class="px-3 py-2 flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="text-sm font-medium text-foreground">{{ row.name }}</div>
                <div v-if="row.detail" class="text-[11px] text-muted-foreground tabular-nums mt-0.5">{{ row.detail }}</div>
              </div>
              <div class="text-sm font-semibold text-foreground tabular-nums shrink-0">{{ fmtMoney(row.amount) }}</div>
            </div>
          </div>
          <div class="border-t-2 border-border px-3 py-2 space-y-1 text-sm bg-muted/40">
            <div class="flex justify-between gap-3 font-bold text-foreground">
              <span>应付合计（含租金与其他费用）</span>
              <span class="tabular-nums">￥{{ fmtMoney(bill.total_amount) }}</span>
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
import { computed } from 'vue'
import { arrearsOf, fmtMoney, isArrears, type Bill, type PropertyMeta } from '../api/rental'
import { coveredPeriodText, cycleLabel, cycleMonths } from '../utils/cycle'

const props = defineProps<{
  bill: Bill
  property: PropertyMeta
  /** 后端费用明细（含自定义收费项目）；没有时回退由 bills 固定列生成 */
  items?: { key: string; name: string; kind: string; amount: number; detail: string }[]
}>()

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

// 单据明细数据源：优先用后端 bill_items（含自定义收费项目），
// 没有时回退由 bills 固定列生成（老数据兼容）。
const feeRows = computed(() => {
  if (props.items && props.items.length) {
    return props.items.map((it) => ({ name: it.name, detail: it.detail, amount: it.amount }))
  }
  const b = props.bill
  const rows: { name: string; detail: string; amount: number }[] = []
  rows.push({ name: '租金', detail: '', amount: b.rent })
  rows.push(b.water_mode === 'monthly'
    ? { name: '水费（包月）', detail: `单价 ${fmtRead(b.water_price)} 元/月`, amount: b.water_fee }
    : {
        name: '水费',
        detail: `读数 ${fmtRead(b.water_last)} → ${fmtRead(b.water_now)} 吨 · 用量 ${fmtRead(usage(b.water_last, b.water_now))} 吨 · 单价 ${fmtRead(b.water_price)} 元/吨`,
        amount: b.water_fee,
      })
  rows.push({
    name: '电费',
    detail: `读数 ${fmtRead(b.elec_last)} → ${fmtRead(b.elec_now)} 度 · 用量 ${fmtRead(usage(b.elec_last, b.elec_now))} 度 · 单价 ${fmtRead(b.elec_price)} 元/度`,
    amount: b.elec_fee,
  })
  rows.push({
    name: '燃气费',
    detail: `读数 ${fmtRead(b.gas_last)} → ${fmtRead(b.gas_now)} 方 · 用量 ${fmtRead(usage(b.gas_last, b.gas_now))} 方 · 单价 ${fmtRead(b.gas_price)} 元/方`,
    amount: b.gas_fee,
  })
  rows.push({ name: '卫生费', detail: '', amount: b.sanitation_fee })
  rows.push({ name: '管理费', detail: '', amount: b.management_fee })
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

/* 打印：仅显示单据本体 */
@media print {
  body * {
    visibility: hidden;
  }
  .receipt-wrap,
  .receipt-wrap * {
    visibility: visible;
  }
  /* 打印始终输出表格版明细，隐藏手机卡片版 */
  .receipt-table {
    display: table !important;
  }
  .receipt-mobile {
    display: none !important;
  }
  /* 单据铺满打印页：弹窗的滚动/缩放容器全部解除 */
  .receipt-wrap {
    position: absolute !important;
    left: 0 !important;
    top: 0 !important;
    width: 100% !important;
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
