<template>
  <div class="space-y-1.5">
    <div class="flex flex-wrap gap-1.5">
      <button
        v-for="opt in leaseTermOptions"
        :key="opt.months"
        type="button"
        class="px-2.5 py-1 rounded-full text-xs border transition-colors"
        :class="modelValue === opt.months
          ? 'border-indigo-500 bg-indigo-500/10 text-indigo-500 font-semibold'
          : 'border-border text-muted-foreground hover:border-indigo-400 hover:text-foreground'"
        :disabled="!baseDate"
        @click="pick(opt.months)"
      >
        {{ opt.label }}
      </button>
      <button
        v-if="modelValue"
        type="button"
        class="px-2.5 py-1 rounded-full text-xs border border-border text-muted-foreground hover:text-foreground"
        @click="pick(0)"
      >
        清除
      </button>
    </div>
    <p class="text-[11px] leading-4" :class="baseDate ? 'text-muted-foreground' : 'text-muted-foreground'">
      <template v-if="!baseDate">先填{{ baseLabel }}，再点上面的时长自动算到期日。</template>
      <template v-else-if="modelValue && preview">按{{ baseLabel }} {{ baseDate }} 算，到期日 <span class="text-foreground font-medium">{{ preview }}</span>（已写入左侧日期，可再手改）。</template>
      <template v-else>常用租期一键填充；也可以直接点左侧日历手填任意到期日。</template>
    </p>
  </div>
</template>

<script setup lang="ts">
/**
 * LeaseTermChips — 租期快捷 chip（1 个月 / 3 个月 / 半年 / 一年）。
 *
 * 粉丝反馈（2026-10-20）：「续租周期只到三个月，还会遇到半年和年付」。租户的
 * 合同期常见 1/3/6/12 个月，手点日历很容易算错月末（1 月 31 日续半年到底到
 * 7 月 30 日还是 7 月 31 日）。这里按 unified 口径（起租日 + N 月 − 1 天，
 * 见 utils/lease.ts）一键算出到期日写回父组件。
 *
 * 之所以只发 `update:modelValue` 表示「选中的月数」而不是直接改日期：
 * 父组件既要把日期写进表单，又要保留「用户手改日期后 chip 取消高亮」的真相，
 * 选中状态由父组件按 leaseEndDateStr 反推（见 TenantsView）。
 */
import { computed } from 'vue'
import { leaseEndDateStr, leaseTermOptions } from '../utils/lease'

const props = withDefaults(defineProps<{
  /** 选中的租期月数；0 = 未选（手填到期日） */
  modelValue: number
  /** 起租日 / 基准日（YYYY-MM-DD） */
  baseDate: string
  /** 起算日期在文案里怎么称呼：入住日期 / 当前到期日 */
  baseLabel?: string
}>(), { baseLabel: '入住日期' })

const emit = defineEmits<{
  'update:modelValue': [value: number]
  /** 选中的时长与算出的到期日一起抛出，父组件直接写表单 */
  pick: [value: { months: number; endDate: string }]
}>()

const preview = computed(() =>
  props.modelValue > 0 ? leaseEndDateStr(props.baseDate, props.modelValue) : '',
)

function pick(months: number) {
  const endDate = months > 0 ? leaseEndDateStr(props.baseDate, months) : ''
  emit('update:modelValue', months)
  emit('pick', { months, endDate })
}
</script>
