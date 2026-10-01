<template>
  <Teleport to="body">
    <Transition name="modal">
      <div v-if="modelValue" class="fixed inset-0 z-50 flex items-end sm:items-center justify-center" @click.self="handleCancel">
        <div class="absolute inset-0 bg-black bg-opacity-50"></div>
        <!-- 手机端贴底滑出（bottom sheet），桌面端居中卡片 -->
        <div class="modal-panel relative bg-surface text-foreground rounded-t-2xl sm:rounded-2xl shadow-xl p-6 w-full max-w-sm sm:mx-4 pb-[calc(1.5rem+env(safe-area-inset-bottom))] sm:pb-6">
          <div class="flex items-start justify-between gap-3 mb-2">
            <h3 class="text-lg font-bold text-foreground">{{ title }}</h3>
            <button
              @click="handleCancel"
              class="w-9 h-9 -mr-3 -mt-1 shrink-0 rounded-full flex items-center justify-center text-muted-foreground bg-muted/60 hover:bg-muted hover:text-foreground transition-colors"
              aria-label="关闭"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" /></svg>
            </button>
          </div>
          <p class="text-sm text-muted-foreground mb-6">{{ message }}</p>
          <div class="flex space-x-3">
            <button
              @click="handleCancel"
              class="flex-1 px-4 py-2.5 rounded-lg border border-border text-foreground hover:bg-muted transition-colors text-sm font-medium"
            >
              取消
            </button>
            <button
              @click="handleConfirm"
              :class="[
                'flex-1 px-4 py-2.5 rounded-lg text-white text-sm font-medium transition-colors',
                confirmType === 'danger' ? 'bg-destructive hover:brightness-110' : 'bg-primary hover:brightness-110'
              ]"
            >
              {{ confirmText }}
            </button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { watch, onBeforeUnmount } from 'vue'
import { lockBodyScroll, unlockBodyScroll } from '../utils/scroll-lock'

const props = withDefaults(defineProps<{
  modelValue: boolean
  title?: string
  message?: string
  confirmText?: string
  confirmType?: 'primary' | 'danger'
}>(), {
  title: '确认',
  message: '确定要执行此操作吗？',
  confirmText: '确认',
  confirmType: 'primary',
})

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'confirm': []
  'cancel': []
}>()

function handleConfirm() {
  emit('update:modelValue', false)
  emit('confirm')
}

function handleCancel() {
  emit('update:modelValue', false)
  emit('cancel')
}

watch(() => props.modelValue, (open) => {
  if (open) lockBodyScroll()
  else unlockBodyScroll()
})

onBeforeUnmount(() => {
  if (props.modelValue) unlockBodyScroll()
})
</script>

<style scoped>
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
