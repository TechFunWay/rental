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
    <div class="surface rounded-2xl p-4 sm:p-6">
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
    <div class="surface rounded-2xl p-4 sm:p-6">
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
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import { fmtMoney, getAnalytics, type Analytics } from '../api/rental'

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
}

onMounted(load)
</script>
