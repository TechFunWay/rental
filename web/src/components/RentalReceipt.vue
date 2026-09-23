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
        <div class="receipt-grid text-sm mb-4">
          <div class="receipt-item"><span class="receipt-label">房号</span><span class="receipt-value font-semibold">{{ bill.room_no }}</span></div>
          <div class="receipt-item"><span class="receipt-label">租户</span><span class="receipt-value font-semibold">{{ bill.tenant_name || '—' }}</span></div>
          <div class="receipt-item"><span class="receipt-label">账期</span><span class="receipt-value tabular-nums">{{ bill.period }}</span></div>
          <div class="receipt-item"><span class="receipt-label">开票日期</span><span class="receipt-value tabular-nums">{{ today }}</span></div>
        </div>

        <!-- 费用明细表 -->
        <table class="receipt-table w-full text-sm border border-border mb-4">
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
              <td class="receipt-td">水费</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_last) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_now) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.water_last, bill.water_now)) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.water_price) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.water_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">电费</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_last) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_now) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.elec_last, bill.elec_now)) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.elec_price) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtMoney(bill.elec_fee) }}</td>
            </tr>
            <tr>
              <td class="receipt-td">燃气费</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_last) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_now) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(usage(bill.gas_last, bill.gas_now)) }}</td>
              <td class="receipt-td text-right tabular-nums">{{ fmtRead(bill.gas_price) }}</td>
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
              <td class="receipt-td" colspan="5">本月租金（应付合计）</td>
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

        <!-- 底部 -->
        <div class="text-xs text-muted-foreground space-y-1">
          <p v-if="bill.remark">备注：{{ bill.remark }}</p>
          <p v-if="property.note">{{ property.note }}</p>
          <div class="flex justify-between pt-2">
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

const props = defineProps<{
  bill: Bill
  property: PropertyMeta
}>()

const periodLabel = computed(() => {
  const m = props.bill.period.match(/^(\d{4})-(\d{2})$/)
  return m ? `${m[1]} 年 ${Number(m[2])} 月` : props.bill.period
})

const today = computed(() => new Date().toISOString().slice(0, 10))

function usage(last: number, now: number): number {
  return now > last ? Math.round((now - last) * 100) / 100 : 0
}
function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}
function fmtDate(s: string): string {
  return (s || '').slice(0, 10)
}
</script>

<style scoped>
/* 打印：仅显示单据本体 */
@media print {
  body * {
    visibility: hidden;
  }
  .receipt-wrap,
  .receipt-wrap * {
    visibility: visible;
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
