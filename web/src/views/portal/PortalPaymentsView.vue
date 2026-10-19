<template>
  <div class="page-container">
    <PageHeader title="缴费记录" description="你的每一次缴款流水，金额与对应转账记录一致" />

    <div class="grid grid-cols-4 gap-2 lg:gap-4">
      <div v-for="s in stats" :key="s.label" class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-[11px] lg:text-xs text-muted-foreground truncate">{{ s.label }}</div>
        <!-- 同账单页：统计值是关键数据，不能靠 truncate 截断；用紧凑写法 + 小字号。 -->
        <div class="mt-1 text-[13px] lg:text-2xl font-bold tabular-nums whitespace-nowrap" :class="s.cls">{{ s.value }}</div>
      </div>
    </div>

    <div v-if="loading" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="loadError" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">{{ loadError }}</div>

    <template v-else>
      <div v-if="payments.length === 0" class="surface rounded-2xl p-6 text-center text-sm text-muted-foreground">
        还没有缴费记录
      </div>

      <template v-else>
        <!-- 桌面表格 -->
        <div class="surface rounded-2xl overflow-hidden hidden md:block">
          <div class="overflow-x-auto">
            <table class="w-full text-sm">
              <thead class="bg-muted/50">
                <tr class="text-left text-xs text-muted-foreground">
                  <th class="py-3 px-4 font-semibold">缴费时间</th>
                  <th class="py-3 px-4 font-semibold text-right">金额（元）</th>
                  <th class="py-3 px-4 font-semibold">对应账单</th>
                  <th class="py-3 px-4 font-semibold">备注</th>
                </tr>
              </thead>
              <tbody class="divide-y divide-border">
                <tr v-for="p in payments" :key="p.id" class="hover:bg-muted/40 transition-colors">
                  <td class="py-3 px-4 text-foreground tabular-nums">{{ fmtTime(p.paid_at) }}</td>
                  <td class="py-3 px-4 text-right tabular-nums font-semibold text-emerald-600 dark:text-emerald-400">{{ fmtMoney(p.amount) }}</td>
                  <td class="py-3 px-4 text-muted-foreground tabular-nums">
                    {{ billPeriodOf(p.bill_id) }}
                  </td>
                  <td class="py-3 px-4 text-muted-foreground text-xs max-w-[240px] truncate">{{ p.note || '—' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <!-- 手机卡片列表 -->
        <div class="md:hidden surface rounded-2xl p-2 space-y-1.5">
          <div
            v-for="p in payments"
            :key="p.id"
            class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5"
          >
            <div class="flex items-center justify-between gap-3">
              <span class="text-xs text-muted-foreground tabular-nums truncate">{{ fmtTime(p.paid_at) }}</span>
              <span class="text-sm font-semibold tabular-nums text-emerald-600 dark:text-emerald-400 shrink-0">
                {{ fmtMoney(p.amount) }} 元
              </span>
            </div>
            <div class="flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
              <span class="truncate">账单 {{ billPeriodOf(p.bill_id) }}</span>
              <span class="truncate">{{ p.note || '—' }}</span>
            </div>
          </div>
        </div>

        <Pagination :page="page" :page-size="pageSize" :total="total" @update:page="changePage" />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import PageHeader from '../../components/PageHeader.vue'
import Pagination from '../../components/Pagination.vue'
import { getPortalPayments, getPortalBills, type PortalPayment } from '../../api/tenant'
import { serverErrorMessage } from '../../utils/apiError'

const loading = ref(true)
const loadError = ref('')
const payments = ref<PortalPayment[]>([])
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)

// 账期映射：缴费流水只带 bill_id，展示时要给出「哪个月」。
// 用「我的账单」列表补一遍映射（租户可见账单不会很多，一次拉全量后本地查表），
// 而不是逐条去请求账单详情——那会是典型的 N+1。
const periodByBill = ref<Record<number, string>>({})

const stats = computed(() => {
  const sum = payments.value.reduce((acc, p) => acc + Number(p.amount || 0), 0)
  const latest = payments.value[0]
  return [
    { label: '流水笔数', value: String(total.value), cls: 'text-foreground' },
    { label: '本页合计', value: fmtMoneyCompact(sum), cls: 'text-emerald-600 dark:text-emerald-400' },
    // 日期在 375px 的四列卡里只放得下「月-日」，年份在缴费记录明细里可见。
    { label: '最近缴款', value: latest ? fmtDate(latest.paid_at).slice(5) : '—', cls: 'text-foreground' },
    { label: '涉及账单', value: String(Object.keys(periodByBill.value).length), cls: 'text-foreground' },
  ]
})

onMounted(async () => {
  await Promise.all([load(), loadBillPeriods()])
})

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getPortalPayments({ page: page.value, pageSize: pageSize.value })
    if (res.data?.code === 0) {
      const d = res.data.data
      payments.value = Array.isArray(d?.items) ? d.items : []
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

async function loadBillPeriods() {
  try {
    // 一次拉足够多的账单（200 条上限足够个人租户的整个租期）建立 id → 账期映射。
    const res = await getPortalBills({ page: 1, pageSize: 200 })
    if (res.data?.code === 0) {
      const items = res.data.data?.items ?? []
      const map: Record<number, string> = {}
      for (const b of items) map[b.id] = b.period
      periodByBill.value = map
    }
  } catch {
    // 映射失败只影响账期展示（退化为 —），不影响缴费流水本身。
  }
}

function changePage(p: number) {
  page.value = p
  void load()
}

function billPeriodOf(billId: number): string {
  return periodByBill.value[billId] || `#${billId}`
}

/** 明细里的金额：完整两位小数（统计卡另用下面的紧凑写法）。 */
function fmtMoney(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/**
 * 统计卡里的金额：整数不带小数（1000 → 「1,000」）。理由同账单页——四列卡
 * 文本宽只有 ~57px，带两位小数会溢出；明细流水里仍是完整两位小数。
 */
function fmtMoneyCompact(v: number): string {
  const n = Number(v || 0)
  return Number.isInteger(n)
    ? n.toLocaleString('zh-CN')
    : n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtDate(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return String(raw).slice(0, 10)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
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
