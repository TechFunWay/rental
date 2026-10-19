<template>
  <div class="page-container">
    <PageHeader title="我的账单" description="只显示与你相关的账单，点击可查看明细与单据" />

    <!-- 统计卡：手机端一行四列，桌面端放宽（按 mobile-ui 规范） -->
    <div class="grid grid-cols-4 gap-2 lg:gap-4">
      <div v-for="s in stats" :key="s.label" class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-[11px] lg:text-xs text-muted-foreground truncate">{{ s.label }}</div>
        <!-- 不用 truncate：金额是关键数据，被悄悄截成「1,00…」等于显示不全。
             改为紧凑金额（整数不带 .00）+ 手机端收小字号，实测四列放得下。 -->
        <div class="mt-1 text-[13px] lg:text-2xl font-bold tabular-nums whitespace-nowrap" :class="s.cls">{{ s.value }}</div>
      </div>
    </div>

    <div v-if="loading" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="loadError" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">{{ loadError }}</div>

    <template v-else>
      <div v-if="bills.length === 0" class="surface rounded-2xl p-6 text-center text-sm text-muted-foreground">
        还没有账单
      </div>

      <template v-else>
        <!-- 桌面表格 -->
        <div class="surface rounded-2xl overflow-hidden hidden md:block">
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead class="bg-muted/50">
                <tr class="text-left text-xs text-muted-foreground">
                  <th class="py-3 px-4 font-semibold">账期</th>
                  <th class="py-3 px-4 font-semibold">房号</th>
                  <th class="py-3 px-4 font-semibold text-right">应缴</th>
                  <th class="py-3 px-4 font-semibold text-right">已缴</th>
                  <th class="py-3 px-4 font-semibold text-right">欠缴</th>
                  <th class="py-3 px-4 font-semibold">状态</th>
                  <th class="py-3 px-4 font-semibold">备注</th>
                  <th class="py-3 px-4 font-semibold text-right">操作</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border">
                <tr v-for="b in bills" :key="b.id" class="hover:bg-muted/40 transition-colors">
                  <td class="py-3 px-4 font-medium text-foreground tabular-nums">{{ b.period }}</td>
                  <td class="py-3 px-4 text-muted-foreground">{{ b.room_no || '—' }}</td>
                  <td class="py-3 px-4 text-right tabular-nums">{{ fmtMoney(b.total_amount) }}</td>
                  <td class="py-3 px-4 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(b.paid_amount) }}</td>
                  <td class="py-3 px-4 text-right tabular-nums" :class="arrearsOf(b) > 0 ? 'text-rose-500 font-semibold' : 'text-muted-foreground'">
                    {{ arrearsOf(b) > 0 ? fmtMoney(arrearsOf(b)) : '—' }}
                  </td>
                  <td class="py-3 px-4">
                    <span class="badge" :class="statusClass(b.status)">{{ statusLabel(b.status) }}</span>
                  </td>
                  <td class="py-3 px-4 text-muted-foreground text-xs max-w-[200px] truncate">{{ b.remark || '—' }}</td>
                  <td class="py-3 px-4 text-right">
                    <button class="text-brand-600 dark:text-brand-300 font-semibold text-xs hover:underline" @click="openDetail(b)">明细 / 单据</button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- 手机卡片列表（容器 p-2 space-y-1.5，条目独立卡片） -->
        <div class="md:hidden surface rounded-2xl p-2 space-y-1.5">
          <button
            v-for="b in bills"
            :key="b.id"
            class="w-full text-left rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5"
            :class="arrearsOf(b) > 0 ? 'bg-rose-500/[0.04]' : ''"
            @click="openDetail(b)"
          >
            <div class="flex items-center justify-between gap-3">
              <span class="text-sm font-semibold text-foreground tabular-nums truncate">{{ b.period }}</span>
              <span class="badge shrink-0" :class="statusClass(b.status)">{{ statusLabel(b.status) }}</span>
            </div>
            <div class="flex items-center justify-between gap-3 text-xs">
              <span class="text-muted-foreground truncate">{{ b.room_no || '—' }}</span>
              <span class="text-foreground tabular-nums shrink-0">应缴 {{ fmtMoney(b.total_amount) }}</span>
            </div>
            <div class="flex items-center justify-between gap-3 text-[11px]">
              <span class="text-muted-foreground truncate">{{ b.remark || '查看明细与单据' }}</span>
              <span
                class="tabular-nums shrink-0"
                :class="arrearsOf(b) > 0 ? 'text-rose-500 font-semibold' : 'text-emerald-600 dark:text-emerald-400'"
              >{{ arrearsOf(b) > 0 ? `欠 ${fmtMoney(arrearsOf(b))}` : '已缴清' }}</span>
            </div>
          </button>
        </div>

        <Pagination :page="page" :page-size="pageSize" :total="total" @update:page="changePage" />
      </template>
    </template>

    <!-- 账单明细 + 单据预览 -->
    <Modal v-model="showDetail" :title="`账单明细 · ${detail?.bill?.period || ''}`" wide>
      <div v-if="detailLoading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>
      <div v-else-if="detail" class="space-y-4">
        <!-- 手机端卡片化明细（内容不截断）；桌面端表格 -->
        <div class="tablet-only-card md:hidden space-y-1.5">
          <div
            v-for="it in detail.items"
            :key="it.key"
            class="rounded-xl border border-border px-3 py-2.5 space-y-1"
          >
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs font-semibold text-foreground truncate">{{ it.name }}</span>
              <span class="text-xs font-semibold tabular-nums shrink-0">{{ fmtMoney(it.amount) }}</span>
            </div>
            <div class="flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
              <span class="truncate">{{ it.detail || it.cycle || '—' }}</span>
              <span class="tabular-nums shrink-0">已缴 {{ fmtMoney(it.paid) }}</span>
            </div>
          </div>
        </div>
        <div class="hidden md:block overflow-x-auto">
          <table class="w-full text-sm">
            <thead class="bg-muted/50">
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-2.5 px-3 font-semibold">项目</th>
                <th class="py-2.5 px-3 font-semibold">说明</th>
                <th class="py-2.5 px-3 font-semibold text-right">金额</th>
                <th class="py-2.5 px-3 font-semibold text-right">已缴</th>
                <th class="py-2.5 px-3 font-semibold text-right">欠缴</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="it in detail.items" :key="it.key">
                <td class="py-2.5 px-3 text-foreground">{{ it.name }}</td>
                <td class="py-2.5 px-3 text-muted-foreground text-xs">{{ it.detail || it.cycle || '—' }}</td>
                <td class="py-2.5 px-3 text-right tabular-nums">{{ fmtMoney(it.amount) }}</td>
                <td class="py-2.5 px-3 text-right tabular-nums text-emerald-600 dark:text-emerald-400">{{ fmtMoney(it.paid) }}</td>
                <td class="py-2.5 px-3 text-right tabular-nums" :class="it.arrears > 0 ? 'text-rose-500' : 'text-muted-foreground'">
                  {{ it.arrears > 0 ? fmtMoney(it.arrears) : '—' }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <div class="rounded-xl border border-border p-3 space-y-1.5 text-xs">
          <div class="flex items-center justify-between gap-3">
            <span class="text-muted-foreground">应缴合计</span>
            <span class="text-foreground font-semibold tabular-nums">{{ fmtMoney(detail.bill.total_amount) }} 元</span>
          </div>
          <div class="flex items-center justify-between gap-3">
            <span class="text-muted-foreground">已缴</span>
            <span class="text-emerald-600 dark:text-emerald-400 font-semibold tabular-nums">{{ fmtMoney(detail.bill.paid_amount) }} 元</span>
          </div>
          <div class="flex items-center justify-between gap-3">
            <span class="text-muted-foreground">欠缴</span>
            <span
              class="font-semibold tabular-nums"
              :class="arrearsOf(detail.bill) > 0 ? 'text-rose-500' : 'text-muted-foreground'"
            >{{ arrearsOf(detail.bill) > 0 ? `${fmtMoney(arrearsOf(detail.bill))} 元` : '已缴清' }}</span>
          </div>
          <div v-if="detail.bill.remark" class="pt-1.5 border-t border-border/60 text-muted-foreground">
            备注：{{ detail.bill.remark }}
          </div>
        </div>

        <!-- 收款记录 -->
        <div v-if="detail.payments.length" class="space-y-1.5">
          <div class="text-xs font-semibold text-muted-foreground">收款记录</div>
          <div
            v-for="p in detail.payments"
            :key="p.id"
            class="flex items-center justify-between gap-3 rounded-xl border border-border px-3 py-2 text-xs"
          >
            <span class="text-muted-foreground truncate">{{ fmtTime(p.paid_at) }}<template v-if="p.note"> · {{ p.note }}</template></span>
            <span class="text-foreground font-semibold tabular-nums shrink-0">{{ fmtMoney(p.amount) }} 元</span>
          </div>
        </div>

        <div class="flex items-center gap-2 pt-1">
          <button class="btn-ghost flex-1 !py-2" @click="showDetail = false">关闭</button>
          <button class="btn-brand flex-1 !py-2" @click="openReceipt">查看 / 打印单据</button>
        </div>
      </div>
    </Modal>

    <!-- 单据预览与打印：复用房东侧同一套 RentalReceipt + 纸张选择，
         保证租户看到的单据与房东打印的完全一致（同一组件、同一份后端数据）。 -->
    <Modal v-model="showReceipt" title="收费单据" wide>
      <div v-if="receipt">
        <RentalReceipt :bill="receipt.bill" :property="receipt.property" :items="receipt.items" />
        <div class="flex flex-wrap items-center justify-end gap-3 mt-4 print:hidden">
          <div class="flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
            <span>单据纸张</span>
            <ReceiptPaperPicker v-model:paper="receiptPaper" v-model:custom="customPaper" @update:paper="onPaperChange" @update:custom="onPaperChange" />
          </div>
          <button class="btn-ghost" @click="showReceipt = false">关闭</button>
          <button class="btn-brand" @click="printReceipt">🖨 打印 / 另存 PDF</button>
        </div>
      </div>
    </Modal>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import PageHeader from '../../components/PageHeader.vue'
import Pagination from '../../components/Pagination.vue'
import Modal from '../../components/Modal.vue'
import RentalReceipt from '../../components/RentalReceipt.vue'
import ReceiptPaperPicker from '../../components/ReceiptPaperPicker.vue'
import {
  getPortalBills,
  getPortalBillDetail,
  getPortalBillReceipt,
  type PortalBill,
  type PortalReceipt,
} from '../../api/tenant'
import { toast } from '../../utils/toast'
import { serverErrorMessage } from '../../utils/apiError'
import {
  applyReceiptPaper,
  loadCustomPaper,
  loadReceiptPaper,
  saveCustomPaper,
  saveReceiptPaper,
  type CustomPaper,
  type ReceiptPaper,
} from '../../utils/receiptPaper'

const loading = ref(true)
const loadError = ref('')
const bills = ref<PortalBill[]>([])
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)

const showDetail = ref(false)
const detailLoading = ref(false)
const detail = ref<PortalReceipt | null>(null)

// 单据：与房东侧共用 RentalReceipt 组件与纸张设置（纸张是本机打印设备属性，
// 存在 localStorage，单张与房东侧同一套键）。
const showReceipt = ref(false)
const receipt = ref<PortalReceipt | null>(null)
const receiptPaper = ref<ReceiptPaper>(loadReceiptPaper())
const customPaper = ref<CustomPaper>(loadCustomPaper())

const stats = computed(() => {
  const unpaid = bills.value.filter((b) => arrearsOf(b) > 0)
  const due = unpaid.reduce((sum, b) => sum + arrearsOf(b), 0)
  const paid = bills.value.reduce((sum, b) => sum + Number(b.paid_amount || 0), 0)
  return [
    { label: '账单数', value: String(total.value), cls: 'text-foreground' },
    { label: '欠缴笔数', value: String(unpaid.length), cls: unpaid.length ? 'text-rose-500' : 'text-foreground' },
    { label: '欠缴合计', value: fmtMoneyCompact(due), cls: due > 0 ? 'text-rose-500' : 'text-foreground' },
    { label: '已缴合计', value: fmtMoneyCompact(paid), cls: 'text-emerald-600 dark:text-emerald-400' },
  ]
})

onMounted(() => {
  void load()
})

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getPortalBills({ page: page.value, pageSize: pageSize.value })
    if (res.data?.code === 0) {
      const d = res.data.data
      bills.value = Array.isArray(d?.items) ? d.items : []
      total.value = Number(d?.total || 0)
    } else {
      loadError.value = res.data?.message || '加载失败'
    }
  } catch (e) {
    loadError.value = serverErrorMessage(e, '加载失败，请稍后再试')
  } finally {
    loading.value = false
  }
}

function changePage(p: number) {
  page.value = p
  void load()
}

async function openDetail(b: PortalBill) {
  showDetail.value = true
  detailLoading.value = true
  detail.value = null
  try {
    const res = await getPortalBillDetail(b.id)
    if (res.data?.code === 0) {
      detail.value = res.data.data
    } else {
      toast(res.data?.message || '加载明细失败', 'error')
      showDetail.value = false
    }
  } catch (e) {
    toast(serverErrorMessage(e, '加载明细失败'), 'error')
    showDetail.value = false
  } finally {
    detailLoading.value = false
  }
}

// 打开单据：走门户专用的单据接口（与账单详情同结构，含明细与抬头）。
async function openReceipt() {
  const bill = detail.value?.bill
  if (!bill) return
  try {
    const res = await getPortalBillReceipt(bill.id)
    if (res.data?.code === 0) {
      receipt.value = res.data.data
      showReceipt.value = true
      // 打开即落一次 @page：这样用浏览器菜单「打印 / 另存 PDF」走的是同一张纸。
      applyReceiptPaper(receiptPaper.value, customPaper.value)
    } else {
      toast(res.data?.message || '加载单据失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '加载单据失败'), 'error')
  }
}

function printReceipt() {
  applyReceiptPaper(receiptPaper.value, customPaper.value)
  window.print()
}

function onPaperChange() {
  saveReceiptPaper(receiptPaper.value)
  saveCustomPaper(customPaper.value)
  applyReceiptPaper(receiptPaper.value, customPaper.value)
}

function arrearsOf(b: PortalBill): number {
  const diff = Number(b.total_amount || 0) - Number(b.paid_amount || 0)
  return diff > 0.005 ? Math.round(diff * 100) / 100 : 0
}

function statusLabel(s: string): string {
  if (s === 'paid') return '已缴清'
  if (s === 'partial') return '部分缴'
  return '未缴'
}

function statusClass(s: string): string {
  if (s === 'paid') return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  if (s === 'partial') return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
}

function fmtMoney(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/**
 * 统计卡里的金额：整数不带小数（1000 → 「1,000」而不是「1,000.00」）。
 * 统计卡只有 ~57px 文本宽，带两位小数会被压到溢出/截断；概览数字也不需要
 * 分位精度（明细与单据里仍是完整两位小数）。
 */
function fmtMoneyCompact(v: number): string {
  const n = Number(v || 0)
  return Number.isInteger(n)
    ? n.toLocaleString('zh-CN')
    : n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtTime(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return String(raw).slice(0, 16).replace('T', ' ')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}
</script>
