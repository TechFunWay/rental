<template>
  <div class="space-y-2.5">
    <div v-if="part !== 'extra'" class="grid grid-cols-2 gap-2">
      <label class="block">
        <span class="text-[11px] text-white/50">姓名 / 称谓 <span class="text-rose-300">*</span></span>
        <input v-model="form.name" class="input-field !py-2 mt-1 text-sm" placeholder="如 陈房东" />
      </label>
      <label class="block">
        <span class="text-[11px] text-white/50">联系电话 <span class="text-rose-300">*</span></span>
        <input v-model="form.phone" class="input-field !py-2 mt-1 text-sm" inputmode="tel" placeholder="便于平台核实" />
      </label>
    </div>

    <template v-if="part !== 'basic'">
      <div class="grid grid-cols-2 gap-2">
        <label class="block">
          <span class="text-[11px] text-white/50">微信号</span>
          <input v-model="form.wechat" class="input-field !py-2 mt-1 text-sm" placeholder="选填" />
        </label>
        <label class="block">
          <span class="text-[11px] text-white/50">所在地区</span>
          <input v-model="form.region" class="input-field !py-2 mt-1 text-sm" placeholder="如 浙江杭州" />
        </label>
      </div>
      <div>
        <span class="text-[11px] text-white/50">房源规模</span>
        <div class="flex flex-wrap gap-1.5 mt-1">
          <button
            v-for="opt in ROOM_SCALE_OPTIONS"
            :key="opt"
            type="button"
            class="px-2.5 py-1 rounded-lg text-[11px] font-medium border transition-colors"
            :class="form.room_scale === opt
              ? 'bg-brand-500/20 border-brand-400/50 text-brand-200'
              : 'bg-white/[0.04] border-white/10 text-white/60 hover:bg-white/[0.08]'"
            @click="form.room_scale = form.room_scale === opt ? '' : opt"
          >{{ opt }}</button>
        </div>
      </div>
      <label class="block">
        <span class="text-[11px] text-white/50">申请说明</span>
        <input v-model="form.intro" class="input-field !py-2 mt-1 text-sm" placeholder="选填，如 自有几套房 / 代管亲友房源" />
      </label>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

import { ROOM_SCALE_OPTIONS } from '../utils/role'
import type { LandlordApplyInput } from '../api/rental'

// 房东入驻资料字段（注册页与等待审核页共用同一份，避免两处表单漂移）。
//
// part 让注册页把它拆成两段：默认只显示「姓名 / 电话」两项（basic），
// 微信/地区/规模/说明收进「更多资料」（extra）。注册页一屏里塞七个输入框
// 又长又挤（2026-11-25 用户反馈「注册页面太丑」），拆开后默认只占两格；
// 等待审核页仍用默认 'all' 一次显示全部。
const props = withDefaults(defineProps<{ modelValue: LandlordApplyInput; part?: 'basic' | 'extra' | 'all' }>(), {
  part: 'all',
})
const emit = defineEmits<{ 'update:modelValue': [value: LandlordApplyInput] }>()

const form = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
})
</script>
