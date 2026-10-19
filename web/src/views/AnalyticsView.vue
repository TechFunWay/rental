<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="统计分析" description="收入趋势、项目构成与收缴率">
      <template #actions>
        <select v-model="months" class="input-field !py-2 !w-auto" @change="load">
          <option :value="6">近 6 个月</option>
          <option :value="12">近 12 个月</option>
          <option :value="24">近 24 个月</option>
        </select>
      </template>
    </PageHeader>

    <!-- 汇总卡：手机两列两行，桌面一行四列 -->
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3 lg:gap-4">
      <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-xs lg:text-sm text-muted-foreground">应收合计</div>
        <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums">{{ fmt(summary.billed) }}</div>
        <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">区间内账单合计（元）</div>
      </div>
      <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-xs lg:text-sm text-muted-foreground">实收合计</div>
        <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-emerald-600 dark:text-emerald-400 tabular-nums">{{ fmt(summary.received) }}</div>
        <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">按收款时间统计（元）</div>
      </div>
      <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-xs lg:text-sm text-muted-foreground">当前欠缴</div>
        <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-rose-500 tabular-nums">{{ fmt(summary.arrears) }}</div>
        <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">全部未缴清账单（元）</div>
      </div>
      <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
        <div class="text-xs lg:text-sm text-muted-foreground">收缴率</div>
        <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums">{{ fmt(summary.rate) }}%</div>
        <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">实收 / 应收</div>
      </div>
    </div>

    <!-- 收入趋势：SVG 柱状图（应收 vs 实收） -->
    <div class="surface rounded-2xl p-3.5 sm:p-6">
      <div class="flex items-center justify-between mb-4">
        <h3 class="text-sm sm:text-base font-bold text-foreground">收入趋势</h3>
        <div class="flex items-center gap-3 text-[11px] text-muted-foreground">
          <span class="flex items-center gap-1.5"><span class="w-2.5 h-2.5 rounded-sm bg-brand-500 inline-block"></span>应收</span>
          <span class="flex items-center gap-1.5"><span class="w-2.5 h-2.5 rounded-sm bg-emerald-500 inline-block"></span>实收</span>
        </div>
      </div>
      <div v-if="series.length === 0" class="py-10 text-center text-sm text-muted-foreground">暂无数据</div>
      <div v-else ref="trendScroller" class="overflow-x-auto">
        <svg :viewBox="`0 0 ${chartW} ${chartH}`" class="w-full" role="img" aria-label="收入趋势柱状图">
          <!-- 横向网格线 -->
          <line v-for="i in 4" :key="'g' + i" :x1="padL" :x2="chartW - padR"
            :y1="padT + (chartH - padT - padB) * (i / 4)" :y2="padT + (chartH - padT - padB) * (i / 4)"
            stroke="currentColor" class="text-border" stroke-dasharray="3 4" stroke-width="1" />
          <!-- 每月一组：应收 + 实收两根柱 -->
          <g v-for="(m, idx) in series" :key="m.period">
            <rect
              :x="padL + idx * groupW + groupW * 0.14" :y="padT + plotH * (1 - barH(m.billed))"
              :width="barW" :height="plotH * barH(m.billed)" rx="2"
              class="fill-brand-500" :opacity="m.billed ? 0.85 : 0">
            </rect>
            <rect
              :x="padL + idx * groupW + groupW * 0.14 + barW + 2" :y="padT + plotH * (1 - barH(m.received))"
              :width="barW" :height="plotH * barH(m.received)" rx="2"
              class="fill-emerald-500" :opacity="m.received ? 0.9 : 0">
            </rect>
            <!-- 月份标签 -->
            <text :x="padL + idx * groupW + groupW / 2" :y="chartH - padB + 14"
              text-anchor="middle" class="fill-current text-muted-foreground" style="font-size: 9px">
              {{ m.period.slice(2) }}
            </text>
          </g>
          <!-- 纵轴刻度 -->
          <text :x="padL - 4" :y="padT + 4" text-anchor="end" class="fill-current text-muted-foreground" style="font-size: 9px">{{ axisMaxLabel }}</text>
          <text :x="padL - 4" :y="chartH - padB" text-anchor="end" class="fill-current text-muted-foreground" style="font-size: 9px">0</text>
        </svg>
      </div>
    </div>

    <!-- 项目收入构成 -->
    <div class="surface rounded-2xl p-3.5 sm:p-6">
      <h3 class="text-sm sm:text-base font-bold text-foreground mb-4">项目收入构成</h3>
      <div v-if="items.length === 0" class="py-10 text-center text-sm text-muted-foreground">区间内还没有账单明细</div>
      <div v-else class="space-y-3">
        <div v-for="it in items" :key="it.name" class="min-w-0">
          <div class="flex items-baseline justify-between gap-3 text-sm mb-1">
            <span class="text-foreground truncate">{{ it.name }}</span>
            <span class="tabular-nums text-muted-foreground shrink-0">{{ fmt(it.amount) }} 元 · {{ pct(it.amount) }}%</span>
          </div>
          <div class="h-2 rounded-full bg-muted overflow-hidden">
            <div class="h-full rounded-full bg-brand-gradient transition-all duration-500" :style="{ width: pct(it.amount) + '%' }"></div>
          </div>
        </div>
      </div>
    </div>

    <!-- 空置分析（issue #2）：当前空置 + 按月趋势 + 按房源排行。
         与上方共用同一个月份选择器：改 months 时两个接口一起重载。 -->
    <div class="space-y-4 sm:space-y-6">
      <div class="flex items-center justify-between gap-3">
        <h3 class="text-sm sm:text-base font-bold text-foreground">空置分析</h3>
        <!-- 估算口径必须显眼：不能让用户把历史空置当精确值 -->
        <span v-if="vacancy.data_from" class="text-[11px] text-muted-foreground shrink-0">
          估算区间 {{ vacancy.data_from }} 起
        </span>
      </div>

      <!-- 汇总卡：手机一行两列，桌面一行五列（5 张卡在窄屏塞成一行会挤成两条） -->
      <div class="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-5 gap-3 lg:gap-4">
        <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
          <div class="text-xs lg:text-sm text-muted-foreground">当前空置</div>
          <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums">
            {{ vacancy.summary.vacant_rooms }}<span class="text-sm font-normal text-muted-foreground"> 间</span>
          </div>
          <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">在管 {{ vacancy.summary.rooms_total }} 间 · 实时</div>
        </div>
        <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
          <div class="text-xs lg:text-sm text-muted-foreground">当前空置率</div>
          <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold tabular-nums"
            :class="vacancy.summary.vacancy_rate > 0 ? 'text-amber-600 dark:text-amber-400' : 'text-foreground'">
            {{ vacancy.summary.vacancy_rate }}%
          </div>
          <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">空置 / 在管</div>
        </div>
        <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
          <div class="text-xs lg:text-sm text-muted-foreground">区间空置房月</div>
          <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums">
            {{ vacancy.summary.vacant_room_months }}<span class="text-sm font-normal text-muted-foreground"> 房月</span>
          </div>
          <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">按入住/退租日期估算</div>
        </div>
        <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
          <div class="text-xs lg:text-sm text-muted-foreground">空置损失估算</div>
          <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-rose-500 tabular-nums">
            {{ fmt(vacancy.summary.loss_estimate) }}
          </div>
          <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">应得租金口径（元）</div>
        </div>
        <div class="surface rounded-2xl p-3 lg:p-5 min-w-0">
          <div class="text-xs lg:text-sm text-muted-foreground">有日期房间</div>
          <div class="mt-1.5 lg:mt-3 text-xl lg:text-2xl font-extrabold text-foreground tabular-nums">
            {{ vacancy.summary.rooms_dated }}<span class="text-sm font-normal text-muted-foreground"> / {{ vacancy.summary.rooms_total }}</span>
          </div>
          <div class="mt-1 text-[11px] lg:text-xs text-muted-foreground">可回溯历史的房间数</div>
        </div>
      </div>

      <!-- 估算提示：接口有 caveat 时才显示，宁可吵也不能让人误当精确值 -->
      <div v-if="vacancy.caveat"
        class="surface rounded-2xl border border-amber-500/40 bg-amber-500/[0.06] p-3 sm:p-4 flex gap-2.5">
        <svg class="w-4 h-4 shrink-0 mt-0.5 text-amber-600 dark:text-amber-400" viewBox="0 0 24 24" fill="none"
          stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="10" />
          <line x1="12" y1="8" x2="12" y2="12" />
          <line x1="12" y1="16" x2="12.01" y2="16" />
        </svg>
        <p class="text-xs leading-relaxed text-foreground min-w-0">{{ vacancy.caveat }}</p>
      </div>

      <!-- 按月空置趋势：空置率折线 + 空置房数柱（两张图共用同一套纵轴缩放） -->
      <div class="surface rounded-2xl p-3.5 sm:p-6">
        <div class="flex items-center justify-between mb-4">
          <h4 class="text-sm sm:text-base font-bold text-foreground">按月空置趋势</h4>
          <div class="flex items-center gap-3 text-[11px] text-muted-foreground">
            <span class="flex items-center gap-1.5"><span class="w-2.5 h-2.5 rounded-sm bg-amber-500 inline-block"></span>空置房数</span>
            <span class="flex items-center gap-1.5"><span class="w-2.5 h-2.5 rounded-sm bg-rose-500 inline-block"></span>空置率</span>
          </div>
        </div>
        <div v-if="vacancy.series.length === 0" class="py-10 text-center text-sm text-muted-foreground">暂无数据</div>
        <div v-else ref="vacancyScroller" class="overflow-x-auto">
          <svg :viewBox="`0 0 ${chartW} ${chartH}`" class="w-full" role="img" aria-label="按月空置趋势图">
            <line v-for="i in 4" :key="'vg' + i" :x1="padL" :x2="chartW - padR"
              :y1="padT + plotH * (i / 4)" :y2="padT + plotH * (i / 4)"
              stroke="currentColor" class="text-border" stroke-dasharray="3 4" stroke-width="1" />
            <g v-for="(m, idx) in vacancy.series" :key="m.period">
              <!-- 空置房数柱 -->
              <rect
                :x="padL + idx * vGroupW + vGroupW * 0.15" :y="padT + plotH * (1 - vVacantH(m.vacant_rooms))"
                :width="vBarW" :height="plotH * vVacantH(m.vacant_rooms)" rx="2"
                class="fill-amber-500" :opacity="m.vacant_rooms ? 0.85 : 0">
              </rect>
              <!-- 空置率折线点（折线在下方单独画） -->
              <circle :cx="padL + idx * vGroupW + vGroupW * 0.5"
                :cy="padT + plotH * (1 - vRateH(m.vacancy_rate))" r="2.5" class="fill-rose-500" />
              <text :x="padL + idx * vGroupW + vGroupW / 2" :y="chartH - padB + 14"
                text-anchor="middle" class="fill-current text-muted-foreground" style="font-size: 9px">
                {{ m.period.slice(2) }}
              </text>
            </g>
            <!-- 空置率折线 -->
            <polyline :points="vRateLine" fill="none" class="stroke-rose-500" stroke-width="1.5" />
            <text :x="padL - 4" :y="padT + 4" text-anchor="end" class="fill-current text-muted-foreground" style="font-size: 9px">{{ vAxisMax }}</text>
            <text :x="padL - 4" :y="chartH - padB" text-anchor="end" class="fill-current text-muted-foreground" style="font-size: 9px">0</text>
          </svg>
        </div>
      </div>

      <!-- 按房源排行：横向条形，支持损失/月数两种排序 -->
      <div class="surface rounded-2xl p-3.5 sm:p-6">
        <div class="flex flex-wrap items-center justify-between gap-2 mb-4">
          <h4 class="text-sm sm:text-base font-bold text-foreground">空置房源排行</h4>
          <div class="flex items-center gap-1.5">
            <button type="button" class="btn-ghost !py-1.5 !px-3 text-xs"
              :class="vacancySort === 'loss' ? 'ring-1 ring-brand-500' : ''" @click="changeVacancySort('loss')">
              按损失
            </button>
            <button type="button" class="btn-ghost !py-1.5 !px-3 text-xs"
              :class="vacancySort === 'months' ? 'ring-1 ring-brand-500' : ''" @click="changeVacancySort('months')">
              按空置月数
            </button>
          </div>
        </div>

        <div v-if="vacancy.rooms.items.length === 0" class="py-10 text-center text-sm text-muted-foreground">区间内没有空置房源</div>
        <!-- 手机端：卡片列表。面板本身已有 p-3.5 内边距，按 mobile-ui 的
             「嵌在已有内边距面板里的列表」用 -mx-2 抵消、不再叠加 p-2；
             卡片间距 space-y-1.5（6px），条目自撑边界不靠 divide-y。 -->
        <div v-else class="md:hidden -mx-2 space-y-1.5">
          <div v-for="r in vacancy.rooms.items" :key="'vm' + r.room_id"
            class="rounded-xl border border-border bg-surface/80 px-3 py-2">
            <div class="flex items-center gap-2 min-w-0">
              <span class="font-medium text-foreground text-sm truncate min-w-0 flex-1">{{ r.room_label }}</span>
              <span class="shrink-0 text-[11px] px-1.5 py-0.5 rounded-md"
                :class="r.current_status === 'vacant'
                  ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300'
                  : 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'">
                {{ r.current_status === 'vacant' ? '空置中' : '在租' }}
              </span>
            </div>
            <div class="mt-1 flex items-baseline justify-between gap-2 text-xs text-muted-foreground">
              <span class="truncate min-w-0">空置 {{ r.vacant_months }} 房月 · {{ r.last_tenant }}</span>
              <span class="shrink-0 tabular-nums text-rose-500 font-semibold">{{ fmt(r.loss_estimate) }} 元</span>
            </div>
            <div class="mt-1.5 h-1.5 rounded-full bg-muted overflow-hidden">
              <div class="h-full rounded-full bg-amber-500/80" :style="{ width: vacancyBarPct(r) + '%' }"></div>
            </div>
          </div>
        </div>
        <!-- 桌面端：表格 -->
        <div v-if="vacancy.rooms.items.length > 0" class="hidden md:block overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-xs text-muted-foreground border-b border-border">
                <th class="py-2 pr-4 font-medium">房源</th>
                <th class="py-2 pr-4 font-medium">状态</th>
                <th class="py-2 pr-4 font-medium">最近租户</th>
                <th class="py-2 pr-4 font-medium">退租日期</th>
                <th class="py-2 pr-4 font-medium text-right">空置房月</th>
                <th class="py-2 font-medium text-right">损失估算</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="r in vacancy.rooms.items" :key="'vd' + r.room_id">
                <td class="py-2 pr-4 text-foreground">{{ r.room_label }}</td>
                <td class="py-2 pr-4">
                  <span class="text-[11px] px-1.5 py-0.5 rounded-md"
                    :class="r.current_status === 'vacant'
                      ? 'bg-amber-500/15 text-amber-700 dark:text-amber-300'
                      : 'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300'">
                    {{ r.current_status === 'vacant' ? '空置中' : '在租' }}
                  </span>
                </td>
                <td class="py-2 pr-4 text-muted-foreground">{{ r.last_tenant }}</td>
                <td class="py-2 pr-4 text-muted-foreground tabular-nums">{{ r.last_move_out || '—' }}</td>
                <td class="py-2 pr-4 text-right tabular-nums text-foreground">{{ r.vacant_months }}</td>
                <td class="py-2 text-right tabular-nums text-rose-500 font-semibold">{{ fmt(r.loss_estimate) }}</td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 分页：与列表页一致的最小实现（上一页/下一页 + 总数） -->
        <div v-if="vacancy.rooms.total > vacancy.rooms.pageSize" class="flex items-center justify-between gap-3 mt-4">
          <span class="text-xs text-muted-foreground">
            共 {{ vacancy.rooms.total }} 间 · 第 {{ vacancy.rooms.page }} / {{ vacancyPageCount }} 页
          </span>
          <div class="flex items-center gap-2">
            <button type="button" class="btn-ghost !py-1.5 !px-3 text-xs" :disabled="vacancy.rooms.page <= 1"
              @click="changeVacancyPage(vacancy.rooms.page - 1)">上一页</button>
            <button type="button" class="btn-ghost !py-1.5 !px-3 text-xs" :disabled="vacancy.rooms.page >= vacancyPageCount"
              @click="changeVacancyPage(vacancy.rooms.page + 1)">下一页</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import {
  fmtMoney,
  getAnalytics,
  getVacancyAnalytics,
  type Analytics,
  type VacancyAnalytics,
  type VacancyRoomRow,
} from '../api/rental'

// 手机窄屏默认 6 个月：SVG 整体缩放能放下，不用横向滚动找当前月份
const months = ref(window.innerWidth < 640 ? 6 : 12)
const trendScroller = ref<HTMLElement | null>(null)
const series = ref<Analytics['series']>([])
const items = ref<Analytics['items']>([])
const summary = ref<Analytics['summary']>({ billed: 0, received: 0, arrears: 0, rate: 0 })

// 纯 SVG 柱状图：无第三方依赖
const chartW = 720
const chartH = 220
const padL = 46
const padR = 12
const padT = 14
const padB = 26

const plotH = computed(() => chartH - padT - padB)
const groupW = computed(() => (chartW - padL - padR) / Math.max(series.value.length, 1))
const barW = computed(() => Math.min(groupW.value * 0.3, 16))

const axisMax = computed(() => {
  const max = Math.max(1, ...series.value.map((m) => Math.max(m.billed, m.received)))
  const pow = Math.pow(10, Math.floor(Math.log10(max)))
  return Math.ceil(max / pow) * pow
})
const axisMaxLabel = computed(() => {
  const v = axisMax.value
  return v >= 10000 ? `${Math.round(v / 10000)}万` : `${v}`
})

function barH(v: number): number {
  if (v <= 0) return 0
  return Math.min(1, v / axisMax.value)
}

function fmt(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function pct(amount: number): number {
  const max = items.value[0]?.amount || 0
  if (max <= 0) return 0
  return Math.round((amount / max) * 100)
}

// ---------- 空置分析（issue #2） ----------
//
// 与上方共用 months：一次改月份两个接口一起重载。排序/分页是**服务端**行为
// （rank 在内存排序后切片分页），所以切换时只需带新参数重新请求。
const vacancySort = ref<'loss' | 'months'>('loss')
const vacancyScroller = ref<HTMLElement | null>(null)
const vacancy = ref<VacancyAnalytics>({
  summary: {
    vacant_rooms: 0, rooms_total: 0, vacancy_rate: 0,
    vacant_room_months: 0, loss_estimate: 0, rooms_dated: 0,
  },
  series: [],
  rooms: { items: [], total: 0, page: 1, pageSize: 20 },
  data_from: '',
  caveat: '',
})

const vacancyPageCount = computed(() =>
  Math.max(1, Math.ceil(vacancy.value.rooms.total / Math.max(1, vacancy.value.rooms.pageSize))),
)

// 趋势图缩放：柱高按「最大空置房数」，折线按「最大空置率」各自归一化。
// 两个量纲不同（间 vs %），共用一个坐标轴会让折线永远贴地，失去可比性。
const vGroupW = computed(() => (chartW - padL - padR) / Math.max(vacancy.value.series.length, 1))
const vBarW = computed(() => Math.min(vGroupW.value * 0.4, 18))
const vVacantMax = computed(() => Math.max(1, ...vacancy.value.series.map((r) => r.vacant_rooms)))
const vRateMax = computed(() => {
  const max = Math.max(1, ...vacancy.value.series.map((r) => r.vacancy_rate))
  // 空置率上限取整到 10 的倍数，图上看得出「一半/满员」这种量级。
  return Math.min(100, Math.ceil(max / 10) * 10)
})
const vAxisMax = computed(() => `${vVacantMax.value} 间 / ${vRateMax.value}%`)

function vVacantH(v: number): number {
  if (v <= 0) return 0
  return Math.min(1, v / vVacantMax.value)
}
function vRateH(v: number): number {
  if (v <= 0) return 0
  return Math.min(1, v / vRateMax.value)
}
const vRateLine = computed(() =>
  vacancy.value.series
    .map((m, idx) => `${padL + idx * vGroupW.value + vGroupW.value * 0.5},${padT + plotH.value * (1 - vRateH(m.vacancy_rate))}`)
    .join(' '),
)

// 排行条形的相对长度：按当前页最大值归一，避免绝对值太大时一眼看不出差异。
function vacancyBarPct(r: VacancyRoomRow): number {
  const max = Math.max(1, ...vacancy.value.rooms.items.map((x) => x.loss_estimate))
  return Math.round((r.loss_estimate / max) * 100)
}

async function loadVacancy() {
  try {
    const res = await getVacancyAnalytics({
      months: months.value,
      page: vacancy.value.rooms.page,
      pageSize: vacancy.value.rooms.pageSize,
      sort: vacancySort.value,
    })
    if (res.data?.code === 0) {
      vacancy.value = res.data.data as VacancyAnalytics
      await nextTick()
      setTimeout(() => {
        if (vacancyScroller.value) vacancyScroller.value.scrollLeft = 99999
      }, 150)
    }
    // 非 0 分支保持上一次的数据不动：空置分析失败不该把整页清空，
    // 上方收入统计照常可读（错误由 axios 拦截器统一提示）。
  } catch {
    // 单块失败不影响整页：axios 拦截器已做全局提示，这里静默保留旧数据。
  }
}

/** 切换排序口径：回到第 1 页再请求（否则可能停在越界页看到空列表）。 */
async function changeVacancySort(next: 'loss' | 'months') {
  if (vacancySort.value === next) return
  vacancySort.value = next
  vacancy.value.rooms.page = 1
  await loadVacancy()
}

async function changeVacancyPage(page: number) {
  const target = Math.min(Math.max(1, page), vacancyPageCount.value)
  if (target === vacancy.value.rooms.page) return
  vacancy.value.rooms.page = target
  await loadVacancy()
}

async function load() {
  const res = await getAnalytics(months.value)
  if (res.data?.code === 0) {
    const d = res.data.data as Analytics
    series.value = d.series || []
    items.value = d.items || []
    summary.value = d.summary || { billed: 0, received: 0, arrears: 0, rate: 0 }
    // 时间轴从左到右（老→新），加载后自动滚到最右：当前月份不用横向翻找
    await nextTick()
    setTimeout(() => {
      if (trendScroller.value) trendScroller.value.scrollLeft = 99999
    }, 150)
  }
  // 空置分析跟同一份月份选择器走：改月份时两个接口一起重载。
  await loadVacancy()
}

onMounted(load)
</script>
