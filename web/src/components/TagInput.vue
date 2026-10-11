<template>
  <div
    class="input-field !py-1.5 flex flex-wrap items-center gap-1.5 min-h-[42px]"
    @click="focusInput"
  >
    <span
      v-for="(t, i) in modelValue"
      :key="t"
      class="inline-flex items-center gap-1 !px-2 !py-0.5 rounded-full bg-brand-500/10 text-brand-600 dark:text-brand-300 text-xs font-medium max-w-full"
    >
      <span class="truncate">{{ t }}</span>
      <button
        type="button"
        class="shrink-0 hover:text-destructive"
        :aria-label="`移除标签 ${t}`"
        @click.stop="remove(i)"
      >×</button>
    </span>
    <input
      :id="listId"
      ref="inputEl"
      v-model="draft"
      type="text"
      class="flex-1 min-w-[90px] bg-transparent outline-none text-sm"
      aria-label="标签输入"
      :placeholder="modelValue.length ? '' : placeholder"
      @keydown.enter.prevent="commit"
      @keydown="onKeydown"
      @keydown.backspace="onBackspace"
      @blur="commit"
    />
    <datalist :id="listId">
      <option v-for="s in suggestions" :key="s" :value="s" />
    </datalist>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

// 标签录入：回车/逗号（中英文）落一个标签，点 × 移除，退格删末尾。
// 候选用 datalist 提示已用过的标签（浏览器原生气泡，桌面/手机都能用）。
// 日期选择、小区录入都自绘组件是因为要控样式；标签够轻，datalist 够用。
const props = withDefaults(defineProps<{
  modelValue: string[]
  suggestions?: string[]
  placeholder?: string
}>(), {
  suggestions: () => [],
  placeholder: '输入后回车添加',
})

const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()

const draft = ref('')
const inputEl = ref<HTMLInputElement | null>(null)
const listId = `tag-suggest-${Math.random().toString(36).slice(2, 8)}`

function commit() {
  // 中英文逗号、顿号、分号都当分隔符，一次粘贴多个也能拆开
  const parts = draft.value.split(/[,，、;；]/).map((s) => s.trim()).filter(Boolean)
  if (!parts.length) return
  const next = [...props.modelValue]
  for (const p of parts) {
    if (!next.includes(p)) next.push(p)
  }
  emit('update:modelValue', next)
  draft.value = ''
}

function remove(i: number) {
  emit('update:modelValue', props.modelValue.filter((_, idx) => idx !== i))
}

function onBackspace() {
  if (!draft.value && props.modelValue.length) {
    emit('update:modelValue', props.modelValue.slice(0, -1))
  }
}

// datalist 选中候选项不会触发 enter（部分浏览器直接填入），输入法空格确认后
// blur 才落；这里对空格/Tab 不拦截，交给 blur 兜底。
function onKeydown() {}

function focusInput() {
  inputEl.value?.focus()
}
</script>
