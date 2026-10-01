<template>
  <div>
    <input ref="inputEl" type="file" class="hidden" :accept="accept" :multiple="multiple" :disabled="disabled" @change="onChange" />
    <button
      type="button"
      class="w-full rounded-xl border-2 border-dashed border-border hover:border-brand-500/60 bg-muted/40 hover:bg-muted/70 active:scale-[0.99] transition-all px-4 py-5 flex flex-col items-center justify-center gap-1.5 text-center disabled:opacity-60 disabled:pointer-events-none"
      :disabled="disabled"
      @click="inputEl?.click()"
    >
      <slot>
        <span class="w-10 h-10 rounded-xl bg-brand-500/10 text-brand-600 dark:text-brand-300 flex items-center justify-center">
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 16a4 4 0 01-.88-7.9A5 5 0 1115.9 6H16a5 5 0 011 9.9M15 13l-3-3m0 0l-3 3m3-3v9" />
          </svg>
        </span>
        <span class="text-sm font-semibold text-foreground">{{ label }}</span>
        <span v-if="hint" class="text-[11px] text-muted-foreground leading-relaxed">{{ hint }}</span>
      </slot>
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// 美化的文件选择按钮：隐藏原生 file input（WebView 里是灰底系统按钮），
// 以虚线上传卡样式呈现，选完立即清空 input 以便重复选择同一文件。
withDefaults(defineProps<{
  accept?: string
  multiple?: boolean
  disabled?: boolean
  label?: string
  hint?: string
}>(), {
  accept: '',
  multiple: false,
  disabled: false,
  label: '点击选择文件',
  hint: '',
})

const emit = defineEmits<{ change: [files: File[]] }>()
const inputEl = ref<HTMLInputElement | null>(null)

function onChange(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  if (files.length) emit('change', files)
  input.value = ''
}

defineExpose({ inputEl })
</script>
