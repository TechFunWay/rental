<template>
  <div class="page-container animate-fade-in">
    <!-- 欢迎横幅：手机端收紧（介绍长文案只在桌面端显示） -->
    <div class="relative overflow-hidden rounded-2xl bg-brand-gradient p-4 sm:p-9 shadow-glow">
      <div class="absolute -top-16 -right-16 w-64 h-64 rounded-full bg-white/10 blur-2xl"></div>
      <div class="absolute -bottom-20 -left-10 w-56 h-56 rounded-full bg-black/10 blur-2xl"></div>
      <div class="relative">
        <p class="text-white/80 text-xs sm:text-sm font-medium">{{ greeting }}，{{ authStore.user?.username || '房东' }} 👋</p>
        <h1 class="text-xl sm:text-3xl font-extrabold text-white mt-1 sm:mt-2">租房管理</h1>
        <p class="hidden sm:block text-white/80 mt-3 max-w-xl">房源、租户与月度抄表账单一站式管理，费用自动计算，欠缴一目了然。</p>
        <div class="flex flex-wrap gap-2 sm:gap-3 mt-3 sm:mt-6">
          <RouterLink to="/admin/bills" class="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-white text-brand-700 text-sm font-semibold hover:bg-white/90 active:scale-[0.98] transition-all">
            去抄表记账
          </RouterLink>
          <RouterLink to="/admin/rooms" class="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-white/15 text-white text-sm font-semibold backdrop-blur hover:bg-white/25 active:scale-[0.98] transition-all">
            管理房源
          </RouterLink>
        </div>
      </div>
    </div>

    <!-- 统计卡：手机两列两行（两位小数金额要完整显示，四列放不下），桌面一行四列大卡 -->
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3 lg:gap-4">
      <div v-for="card in statCards" :key="card.label" class="surface rounded-2xl p-3 lg:p-5 hover:-translate-y-0.5 transition-transform min-w-0">
        <div class="flex items-center justify-between gap-1">
          <span class="text-xs lg:text-sm text-muted-foreground">{{ card.label }}</span>
          <span class="w-9 h-9 rounded-lg items-center justify-center hidden lg:flex" :class="card.bg">
            <span v-html="card.icon" class="w-5 h-5 block"></span>
          </span>
        </div>
        <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums truncate">{{ card.value }}</div>
        <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground truncate">{{ card.sub }}</div>
      </div>
    </div>

    <div class="grid grid-cols-1 xl:grid-cols-5 gap-4">
      <!-- 本月收缴进度 -->
      <div class="surface rounded-2xl p-4 sm:p-6 xl:col-span-3">
        <div class="flex items-center justify-between mb-5">
          <h3 class="text-sm sm:text-base font-bold text-foreground">{{ stats?.month.period ?? currentPeriod() }} 收缴情况</h3>
          <span class="badge bg-brand-500/10 text-brand-600 dark:text-brand-300">应收 {{ fmtMoney(stats?.month.total) }} 元</span>
        </div>
        <div class="space-y-4">
          <div>
            <div class="flex justify-between text-sm mb-1.5">
              <span class="text-muted-foreground">已收</span>
              <span class="font-semibold text-emerald-500 tabular-nums">{{ fmtMoney(stats?.month.paid) }} 元</span>
            </div>
            <div class="h-2.5 rounded-full bg-muted overflow-hidden">
              <div class="h-full rounded-full bg-emerald-500 transition-all duration-500" :style="{ width: paidPct + '%' }"></div>
            </div>
          </div>
          <div>
            <div class="flex justify-between text-sm mb-1.5">
              <span class="text-muted-foreground">欠缴（{{ stats?.month.arrears_count ?? 0 }} 户）</span>
              <span class="font-semibold text-rose-500 tabular-nums">{{ fmtMoney(stats?.month.outstanding) }} 元</span>
            </div>
            <div class="h-2.5 rounded-full bg-muted overflow-hidden">
              <div class="h-full rounded-full bg-rose-500 transition-all duration-500" :style="{ width: arrearsPct + '%' }"></div>
            </div>
          </div>
        </div>
        <div class="mt-5 pt-4 border-t border-border flex items-center justify-between text-sm">
          <span class="text-muted-foreground">历史累计欠缴</span>
          <span class="font-bold text-rose-500 tabular-nums">
            {{ fmtMoney(stats?.overall.outstanding) }} 元（{{ stats?.overall.arrears_count ?? 0 }} 张账单）
          </span>
        </div>
      </div>

      <!-- 房源概况 -->
      <div class="surface rounded-2xl p-4 sm:p-6 xl:col-span-2">
        <h3 class="text-sm sm:text-base font-bold text-foreground mb-4 sm:mb-5">房源概况</h3>
        <div class="space-y-4">
          <div class="flex items-center justify-between p-3.5 rounded-xl bg-muted">
            <span class="text-sm text-muted-foreground">房源总数</span>
            <span class="text-lg font-bold text-foreground tabular-nums">{{ stats?.rooms_total ?? 0 }}</span>
          </div>
          <div class="flex items-center justify-between p-3.5 rounded-xl bg-emerald-500/10">
            <span class="text-sm text-muted-foreground">在租</span>
            <span class="text-lg font-bold text-emerald-500 tabular-nums">{{ stats?.rooms_occupied ?? 0 }}</span>
          </div>
          <div class="flex items-center justify-between p-3.5 rounded-xl bg-muted">
            <span class="text-sm text-muted-foreground">空闲</span>
            <span class="text-lg font-bold text-foreground tabular-nums">{{ stats?.rooms_vacant ?? 0 }}</span>
          </div>
          <div class="flex items-center justify-between p-3.5 rounded-xl bg-muted">
            <span class="text-sm text-muted-foreground">在租租户</span>
            <span class="text-lg font-bold text-foreground tabular-nums">{{ stats?.tenants_active ?? 0 }}</span>
          </div>
        </div>
      </div>
    </div>

    <!-- 到期提醒 + 缴费提醒 + 欠缴名单：不用翻列表就知道该办什么 -->
    <div class="grid grid-cols-1 xl:grid-cols-3 gap-4">
      <!-- 缴费提醒（按房间设置的缴费日，窗口内或已逾期） -->
      <div class="surface rounded-2xl p-4 sm:p-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm sm:text-base font-bold text-foreground">缴费提醒</h3>
          <span class="badge shrink-0" :class="paymentDue.length ? 'bg-amber-500/10 text-amber-600 dark:text-amber-300' : 'bg-muted text-muted-foreground'">
            {{ paymentDue.length ? `${paymentDue.length} 笔` : '暂无' }}
          </span>
        </div>
        <div v-if="!paymentDue.length" class="text-sm text-muted-foreground py-6 text-center">
          暂无临期或逾期的缴费。到「租户管理」给租户设置缴费日即可开启提醒。
        </div>
        <div v-else class="divide-y divide-border/60">
          <RouterLink
            v-for="p in paymentDue"
            :key="p.room_id"
            to="/admin/bills"
            class="flex items-center justify-between py-2 group"
            :title="p.due_date"
          >
            <div class="min-w-0">
              <div class="flex items-baseline gap-2">
                <span class="text-sm font-semibold text-foreground">{{ p.room_no }}</span>
                <span class="text-xs text-muted-foreground truncate">{{ p.tenant_name || '—' }}</span>
              </div>
              <div class="text-[11px] text-muted-foreground mt-0.5 tabular-nums">
                {{ cycleLabel(p.pay_cycle) }} · {{ coveredPeriodText(p.period, p.pay_cycle) }} · 每月 {{ p.pay_day }} 号收 · 预计 {{ fmtMoney(p.expected_amount) }} 元
              </div>
            </div>
            <span class="text-xs font-medium tabular-nums shrink-0 ml-2" :class="p.days_left < 0 ? 'text-rose-500' : 'text-amber-500'">
              {{ payDueText(p) }}
            </span>
          </RouterLink>
        </div>
      </div>

      <!-- 租约到期（60 天内含已过期） -->
      <div class="surface rounded-2xl p-4 sm:p-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm sm:text-base font-bold text-foreground">租约到期提醒</h3>
          <span class="badge shrink-0" :class="leaseDue.length ? 'bg-amber-500/10 text-amber-600 dark:text-amber-300' : 'bg-muted text-muted-foreground'">
            {{ leaseDue.length ? `${leaseDue.length} 户` : '暂无' }}
          </span>
        </div>
        <div v-if="!leaseDue.length" class="text-sm text-muted-foreground py-6 text-center">60 天内没有到期的租约。</div>
        <div v-else class="divide-y divide-border/60">
          <RouterLink
            v-for="t in leaseDue"
            :key="t.id"
            to="/admin/tenants"
            class="flex items-center justify-between py-2 group"
          >
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ t.name }}</span>
              <span class="text-xs text-muted-foreground ml-2">{{ t.room_no }}</span>
            </div>
            <span class="text-xs font-medium tabular-nums shrink-0" :class="t.days_left < 0 ? 'text-rose-500' : 'text-amber-500'">
              {{ t.days_left < 0 ? `已过期 ${-t.days_left} 天` : `${t.days_left} 天后到期` }}
            </span>
          </RouterLink>
        </div>
      </div>

      <!-- 本月欠缴户 -->
      <div class="surface rounded-2xl p-4 sm:p-6">
        <div class="flex items-center justify-between mb-4">
          <h3 class="text-sm sm:text-base font-bold text-foreground">本月欠缴（{{ stats?.month.period ?? currentPeriod() }}）</h3>
          <span class="badge shrink-0" :class="arrearsList.length ? 'bg-rose-500/10 text-rose-500' : 'bg-muted text-muted-foreground'">
            {{ arrearsList.length ? `${arrearsList.length} 户` : '已收齐' }}
          </span>
        </div>
        <div v-if="!arrearsList.length" class="text-sm text-muted-foreground py-6 text-center">本月账单已全部收齐。</div>
        <div v-else class="divide-y divide-border/60">
          <RouterLink
            v-for="a in arrearsList"
            :key="a.id"
            to="/admin/bills"
            class="flex items-center justify-between py-2 group"
          >
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ a.room_no }}</span>
              <span class="text-xs text-muted-foreground ml-2 truncate">{{ a.tenant_name || '—' }}</span>
            </div>
            <span class="text-sm font-bold text-rose-500 tabular-nums shrink-0">{{ fmtMoney(a.arrears) }} 元</span>
          </RouterLink>
        </div>
      </div>
    </div>

    <!-- 最近账单 -->
    <div class="surface rounded-2xl p-4 sm:p-6">
      <div class="flex items-center justify-between mb-4">
        <h3 class="text-sm sm:text-base font-bold text-foreground">最近账单</h3>
        <RouterLink to="/admin/bills" class="text-sm text-brand-600 dark:text-brand-300 hover:underline">查看全部</RouterLink>
      </div>
      <div v-if="!stats?.recent_bills.length" class="text-sm text-muted-foreground py-8 text-center">
        还没有账单，去「抄表账单」抄表建账吧。
      </div>
      <template v-else>
      <!-- 桌面端：表格 -->
      <div class="hidden sm:block overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border">
              <th class="py-2.5 pr-4 font-medium">月份</th>
              <th class="py-2.5 pr-4 font-medium">房号</th>
              <th class="py-2.5 pr-4 font-medium">租户</th>
              <th class="py-2.5 pr-4 font-medium text-right">应付</th>
              <th class="py-2.5 pr-4 font-medium text-right">已收</th>
              <th class="py-2.5 pr-4 font-medium">状态</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="b in stats!.recent_bills" :key="b.id">
              <td class="py-2.5 pr-4 text-muted-foreground tabular-nums">{{ b.period }}</td>
              <td class="py-2.5 pr-4 font-semibold text-foreground">{{ b.room_no }}</td>
              <td class="py-2.5 pr-4 text-foreground">{{ b.tenant_name || '—' }}</td>
              <td class="py-2.5 pr-4 text-right tabular-nums">{{ fmtMoney(b.total_amount) }}</td>
              <td class="py-2.5 pr-4 text-right tabular-nums">{{ fmtMoney(b.paid_amount) }}</td>
              <td class="py-2.5 pr-4">
                <span class="badge" :class="statusClass(b)">{{ statusText(b) }}</span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
      <!-- 手机端：紧凑卡片列表 -->
      <div class="sm:hidden -mx-2 space-y-1.5">
        <div v-for="b in stats!.recent_bills" :key="b.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2 flex items-center justify-between gap-3">
          <div class="min-w-0">
            <div class="text-sm font-semibold text-foreground truncate">{{ b.room_no }} <span class="text-xs text-muted-foreground font-normal">{{ b.tenant_name || '—' }}</span></div>
            <div class="text-[11px] text-muted-foreground tabular-nums mt-0.5">{{ b.period }} · 应付 {{ fmtMoney(b.total_amount) }} 元 · 已收 {{ fmtMoney(b.paid_amount) }} 元</div>
          </div>
          <span class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]" :class="statusClass(b)">{{ statusText(b) }}</span>
        </div>
      </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { currentPeriod, fmtMoney, getStats, isArrears, type PaymentDueItem, type RentalStats } from '../api/rental'
import { coveredPeriodText, cycleLabel } from '../utils/cycle'

const authStore = useAuthStore()
const stats = ref<RentalStats | null>(null)

const greeting = computed(() => {
  const h = new Date().getHours()
  if (h < 6) return '凌晨好'
  if (h < 12) return '早上好'
  if (h < 14) return '中午好'
  if (h < 18) return '下午好'
  return '晚上好'
})

// 全应用数字统一保留两位小数，统计卡金额直接用 fmtMoney（fixed 2 位）
const statCards = computed(() => [
  {
    label: '房源总数',
    value: String(stats.value?.rooms_total ?? 0),
    sub: `在租 ${stats.value?.rooms_occupied ?? 0} · 空闲 ${stats.value?.rooms_vacant ?? 0}`,
    bg: 'bg-brand-500/10 text-brand-600 dark:text-brand-300',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>',
  },
  {
    label: '在租租户',
    value: String(stats.value?.tenants_active ?? 0),
    sub: '当前在住人数',
    bg: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2M9 7a4 4 0 104 0zm7 14v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75"/></svg>',
  },
  {
    label: '本月实收',
    value: fmtMoney(stats.value?.month.paid),
    sub: `${stats.value?.month.bill_count ?? 0} 张账单`,
    bg: 'bg-sky-500/10 text-sky-600 dark:text-sky-300',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>',
  },
  {
    label: '本月欠缴',
    value: fmtMoney(stats.value?.month.outstanding),
    sub: `${stats.value?.month.arrears_count ?? 0} 户未缴清`,
    bg: 'bg-rose-500/10 text-rose-600 dark:text-rose-300',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>',
  },
])

const paidPct = computed(() => {
  const total = stats.value?.month.total ?? 0
  if (total <= 0) return 0
  return Math.min(100, Math.round(((stats.value?.month.paid ?? 0) / total) * 100))
})
const arrearsPct = computed(() => (stats.value?.month.total ?? 0) > 0 ? 100 - paidPct.value : 0)

// 租约到期名单（60 天内含已过期）、缴费提醒与本月欠缴户名单，由 stats 接口返回
const leaseDue = computed(() => stats.value?.lease_due ?? [])
const paymentDue = computed<PaymentDueItem[]>(() => stats.value?.payment_due ?? [])
const arrearsList = computed(() => stats.value?.arrears_list ?? [])

// 缴费提醒的到期文案：负数 = 已逾期，0 = 今天，正数 = 剩余天数
function payDueText(p: PaymentDueItem): string {
  if (p.days_left < 0) return `已逾期 ${-p.days_left} 天`
  if (p.days_left === 0) return '今天到期'
  return `${p.days_left} 天后缴费`
}

function statusText(b: { status: string }) {
  if (b.status === 'paid') return '已缴清'
  if (b.status === 'partial') return '部分已缴'
  return '未缴纳'
}
function statusClass(b: { status: string }) {
  if (b.status === 'paid') return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  if (b.status === 'partial') return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
}

onMounted(async () => {
  try {
    const res = await getStats(currentPeriod())
    if (res.data?.code === 0) stats.value = res.data.data
  } catch {
    /* 统计加载失败不阻塞页面 */
  }
})
</script>
