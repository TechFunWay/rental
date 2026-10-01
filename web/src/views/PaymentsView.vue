<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="收款记录" description="每笔收款的时间、项目明细与金额；账单是账单、缴费是缴费，欠缴看明细">
      <template #actions>
        <RouterLink to="/admin/bills" class="btn-ghost">去账单收款</RouterLink>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel :summary="paymentsFilterSummary">
      <div class="flex flex-wrap items-center gap-3">
        <DateField v-model="periodFrom" type="month" class="!py-2 !w-[140px]" @change="load(1)" />
        <span class="text-xs text-muted-foreground shrink-0">至</span>
        <DateField v-model="periodTo" type="month" class="!py-2 !w-[140px]" @change="load(1)" />
        <input v-model="keyword" class="input-field flex-1 min-w-[160px] !py-2" placeholder="搜索房号 / 租户 / 备注…" @keyup.enter="load(1)" />
        <button class="btn-brand !py-2" @click="load(1)">查询</button>
        <div class="sm:ml-auto text-sm text-muted-foreground whitespace-nowrap w-full sm:w-auto">
          本页合计：<strong class="text-emerald-600 dark:text-emerald-400 tabular-nums">{{ fmt(pageSum) }}</strong> 元
        </div>
      </div>
    </FilterPanel>

    <!-- 收款列表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="rows.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        还没有收款记录。到「抄表」模块抄表开票，或在账单里登记收款。
      </div>
      <template v-else>
        <!-- 桌面端：表格 -->
        <div class="hidden md:block overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
                <th class="py-3 px-4 font-medium whitespace-nowrap">收款时间</th>
                <th class="py-3 px-4 font-medium whitespace-nowrap">房号</th>
                <th class="py-3 px-4 font-medium whitespace-nowrap">租户</th>
                <th class="py-3 px-4 font-medium whitespace-nowrap">账期</th>
                <th class="py-3 px-4 font-medium">缴费明细</th>
                <th class="py-3 px-4 font-medium text-right whitespace-nowrap">金额（元）</th>
                <th class="py-3 px-4 font-medium">备注</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="p in rows" :key="p.id" class="hover:bg-muted/30 transition-colors">
                <td class="py-3 px-4 text-muted-foreground tabular-nums whitespace-nowrap">{{ fmtTime(p.paid_at) }}</td>
                <td class="py-3 px-4 font-semibold text-foreground whitespace-nowrap">{{ p.room_no || '—' }}</td>
                <td class="py-3 px-4 text-foreground whitespace-nowrap">{{ p.tenant_name || '—' }}</td>
                <td class="py-3 px-4 text-muted-foreground tabular-nums whitespace-nowrap">{{ p.period || '—' }}</td>
                <td class="py-3 px-4">
                  <div v-if="p.items.length" class="flex flex-wrap gap-1">
                    <span v-for="(it, i) in p.items" :key="i" class="badge !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">
                      {{ it.name }} {{ fmt(it.amount) }} 元
                    </span>
                  </div>
                  <span v-else class="text-xs text-muted-foreground">未按项目分摊（历史收款）</span>
                </td>
                <td class="py-3 px-4 text-right font-bold text-emerald-600 dark:text-emerald-400 tabular-nums whitespace-nowrap">{{ fmt(p.amount) }}</td>
                <td class="py-3 px-4 text-muted-foreground text-xs max-w-[160px] truncate" :title="p.note">{{ p.note || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 手机端：卡片列表 -->
        <div class="md:hidden p-2 space-y-1.5">
          <div v-for="p in rows" :key="p.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5">
            <div class="flex items-start justify-between gap-2">
              <div class="min-w-0">
                <span class="text-sm font-semibold text-foreground">{{ p.room_no || '—' }}</span>
                <span class="text-[11px] text-muted-foreground ml-1.5">{{ p.tenant_name || '' }} · {{ p.period || '—' }}</span>
              </div>
              <div class="text-right shrink-0">
                <div class="text-sm font-bold text-emerald-600 dark:text-emerald-400 tabular-nums">+{{ fmt(p.amount) }}</div>
                <div class="text-[10px] text-muted-foreground tabular-nums">{{ fmtTime(p.paid_at) }}</div>
              </div>
            </div>
            <div v-if="p.items.length" class="flex flex-wrap gap-1">
              <span v-for="(it, i) in p.items" :key="i" class="badge !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">
                {{ it.name }} {{ fmt(it.amount) }} 元
              </span>
            </div>
            <div v-if="p.note" class="text-[11px] text-muted-foreground truncate" :title="p.note">备注 {{ p.note }}</div>
          </div>
        </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import DateField from '../components/DateField.vue'
import FilterPanel from '../components/FilterPanel.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { getPayments, type PaymentRecord } from '../api/rental'

const rows = ref<PaymentRecord[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const periodFrom = ref('')
const periodTo = ref('')
const keyword = ref('')

const paymentsFilterSummary = computed(() =>
  [
    periodFrom.value || '开始不限',
    periodTo.value ? `至 ${periodTo.value}` : null,
    keyword.value ? `“${keyword.value}”` : null,
  ].filter(Boolean).join(' · ') || '全部收款',
)

const pageSum = computed(() => rows.value.reduce((sum, p) => sum + p.amount, 0))

function fmt(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtTime(s: string): string {
  return (s || '').slice(0, 10)
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getPayments({
      page: p, pageSize,
      period_from: periodFrom.value || undefined,
      period_to: periodTo.value || undefined,
      keyword: keyword.value,
    })
    if (res.data?.code === 0) {
      rows.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

onMounted(() => load(1))
</script>
