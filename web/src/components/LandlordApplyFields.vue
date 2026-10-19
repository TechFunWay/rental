<template>
  <div class="space-y-3">
    <div class="grid grid-cols-2 gap-3">
      <label class="block">
        <span class="text-[11px] opacity-70">姓名 / 称谓 *</span>
        <input v-model="form.name" class="input-field !py-2 mt-1" placeholder="如 陈房东" />
      </label>
      <label class="block">
        <span class="text-[11px] opacity-70">联系电话 *</span>
        <input v-model="form.phone" class="input-field !py-2 mt-1" inputmode="tel" placeholder="便于平台核实" />
      </label>
    </div>
    <div class="grid grid-cols-2 gap-3">
      <label class="block">
        <span class="text-[11px] opacity-70">微信号</span>
        <input v-model="form.wechat" class="input-field !py-2 mt-1" placeholder="选填" />
      </label>
      <label class="block">
        <span class="text-[11px] opacity-70">所在地区</span>
        <input v-model="form.region" class="input-field !py-2 mt-1" placeholder="如 浙江杭州" />
      </label>
    </div>
    <div>
      <span class="text-[11px] opacity-70">房源规模</span>
      <div class="flex flex-wrap gap-1.5 mt-1">
        <button
          v-for="opt in ROOM_SCALE_OPTIONS"
          :key="opt"
          type="button"
          class="px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors"
          :class="form.room_scale === opt
            ? 'bg-brand-500/15 border-brand-500/40 text-brand-600 dark:text-brand-300'
            : 'bg-surface/70 border-border opacity-80 hover:bg-muted'"
          @click="form.room_scale = form.room_scale === opt ? '' : opt"
        >{{ opt }}</button>
      </div>
    </div>
    <label class="block">
      <span class="text-[11px] opacity-70">申请说明</span>
      <input v-model="form.intro" class="input-field !py-2 mt-1" placeholder="选填，如 自有几套房 / 代管亲友房源" />
    </label>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { ROOM_SCALE_OPTIONS } from '../utils/role'
import type { LandlordApplyInput } from '../api/rental'

// 房东入驻资料字段（注册页与等待审核页共用同一份，避免两处表单漂移）。
// 用法：<LandlordApplyFields v-model="apply" />
const props = defineProps<{ modelValue: LandlordApplyInput }>()
const emit = defineEmits<{ 'update:modelValue': [value: LandlordApplyInput] }>()

const form = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})
</script>
