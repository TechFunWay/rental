<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="modelValue" class="modal-shell fixed inset-0 z-50 flex items-end sm:items-center justify-center" @click.self="handleClose">
        <div class="absolute inset-0 bg-black bg-opacity-50"></div>
        <!-- 手机端是贴底滑出的 bottom sheet（顶部圆角 + 安全区），桌面端保持居中卡片 -->
        <div class="modal-panel relative bg-surface text-foreground rounded-t-2xl sm:rounded-2xl shadow-xl p-4 sm:p-6 w-full max-w-lg sm:mx-4 max-h-[88dvh] sm:max-h-[90vh] pb-[calc(1rem+env(safe-area-inset-bottom))] sm:pb-6 overflow-auto">
          <div class="flex items-center justify-between gap-3 mb-4">
            <h3 class="text-lg font-bold text-foreground">{{ title }}</h3>
            <!-- closable=false 表示强制引导（如安全问题设置），不渲染叉叉避免"点了没反应"；
                 closable 必须给默认值 true，否则 Vue 的 Boolean casting 把未传当 false，叉叉全消失 -->
            <button
              v-if="closable"
              @click="handleClose"
              class="w-9 h-9 -mr-2 shrink-0 rounded-full flex items-center justify-center text-muted-foreground bg-muted/60 hover:bg-muted hover:text-foreground transition-colors"
              aria-label="关闭"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
          </div>
          <div>
            <slot />
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { watch, onBeforeUnmount } from 'vue'
import { lockBodyScroll, unlockBodyScroll } from '../utils/scroll-lock'

const props = withDefaults(
  defineProps<{
    modelValue: boolean
    title?: string
    closable?: boolean
  }>(),
  { title: '', closable: true },
)

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

function handleClose() {
  if (!props.closable) return
  emit('update:modelValue', false)
}

// 弹窗打开时锁住背景滚动（手机 app 惯例）
watch(() => props.modelValue, (open) => {
  if (open) lockBodyScroll()
  else unlockBodyScroll()
})

onBeforeUnmount(() => {
  if (props.modelValue) unlockBodyScroll()
})
</script>

<style scoped>
/* 手机端：面板从屏幕底部滑入滑出（bottom sheet）；桌面端：居中缩放淡入 */
.modal-panel {
  transition: transform 0.28s cubic-bezier(0.32, 0.72, 0, 1);
}
.modal-enter-active,
.modal-leave-active {
  transition: opacity 0.25s ease;
}
.modal-enter-from,
.modal-leave-to {
  opacity: 0;
}
.modal-enter-from .modal-panel,
.modal-leave-to .modal-panel {
  transform: translateY(100%);
}
@media (min-width: 768px) {
  .modal-enter-from .modal-panel,
  .modal-leave-to .modal-panel {
    transform: scale(0.95);
  }
}
</style>
