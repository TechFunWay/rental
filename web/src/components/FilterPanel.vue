<template>
  <div class="surface rounded-2xl overflow-hidden">
    <!-- 桌面端：筛选控件全部平铺 -->
    <div class="hidden sm:block p-4">
      <slot />
    </div>
    <!-- 手机端：默认收成一行条件摘要，点开才展开，给列表腾位置 -->
    <div class="sm:hidden">
      <button
        type="button"
        class="w-full flex items-center gap-2.5 px-4 py-3 text-left active:bg-muted/50 transition-colors"
        @click="open = !open"
      >
        <span class="w-8 h-8 rounded-lg bg-brand-500/10 text-brand-600 dark:text-brand-300 flex items-center justify-center shrink-0 relative">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
          </svg>
          <span
            v-if="activeCount"
            class="absolute -top-1 -right-1 min-w-[16px] h-4 px-1 rounded-full bg-rose-500 text-white text-[10px] font-bold flex items-center justify-center tabular-nums"
          >{{ activeCount > 9 ? '9+' : activeCount }}</span>
        </span>
        <span class="flex-1 min-w-0 truncate text-sm text-foreground">{{ summary }}</span>
        <svg class="w-4 h-4 text-muted-foreground transition-transform shrink-0" :class="open ? 'rotate-180' : ''" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
        </svg>
      </button>
      <div v-show="open" class="px-4 pb-4 pt-3 border-t border-border/60">
        <slot />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// 列表页统一筛选区：桌面平铺；手机收成一行摘要（漏斗 + 当前条件 + 非默认条件数），
// 点开显示完整筛选控件，查询后调用 collapse() 收起。
withDefaults(defineProps<{
  summary?: string
  activeCount?: number
}>(), {
  summary: '筛选条件',
  activeCount: 0,
})

const open = ref(false)

function collapse() {
  open.value = false
}

defineExpose({ collapse })
</script>
