<template>
  <button
    type="button"
    v-bind="$attrs"
    class="input-field flex items-center justify-between gap-2 text-left"
    :class="disabled ? 'opacity-60 pointer-events-none' : ''"
    :disabled="disabled"
    @click="openPanel"
  >
    <span class="truncate tabular-nums" :class="modelValue ? 'text-foreground' : 'text-muted-foreground'">
      {{ modelValue || placeholder }}
    </span>
    <svg class="w-4 h-4 shrink-0 text-muted-foreground" fill="none" stroke="currentColor" viewBox="0 0 24 24">
      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7V3m8 4V3m-9 8h10M5 21h14a2 2 0 002-2V7a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
    </svg>
  </button>

  <Teleport to="body">
    <Transition name="datefield">
      <div v-if="panelOpen" class="fixed inset-0 z-[70] flex items-end sm:items-center justify-center sm:px-4" @click.self="cancel">
        <div class="absolute inset-0 bg-black bg-opacity-50" @click="cancel"></div>
        <!-- 手机端贴底滑出（bottom sheet），桌面端居中小面板 -->
        <div class="relative datefield-panel w-full sm:max-w-[320px] surface rounded-t-2xl sm:rounded-2xl shadow-xl p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] sm:pb-4">
          <div class="flex items-center justify-between mb-3">
            <h3 class="text-sm font-bold text-foreground">{{ type === 'month' ? '选择月份' : '选择日期' }}</h3>
            <span class="text-xs text-muted-foreground tabular-nums">{{ preview }}</span>
          </div>
          <div class="grid gap-2.5" :class="type === 'month' ? 'grid-cols-2' : 'grid-cols-3'">
            <div v-for="col in columns" :key="col.key" class="space-y-1.5">
              <span class="block text-center text-[11px] font-semibold text-muted-foreground">{{ col.label }}</span>
              <button type="button" class="stepper-btn" :aria-label="`${col.label}加一`" @click="step(col.key, 1)">＋</button>
              <input
                v-model.number="draft[col.key]"
                type="number"
                class="stepper-value"
                :min="col.min"
                :max="col.max"
                :aria-label="col.label"
                @change="clampAll"
              />
              <button type="button" class="stepper-btn" :aria-label="`${col.label}减一`" @click="step(col.key, -1)">－</button>
            </div>
          </div>
          <div class="flex gap-2 mt-4">
            <button type="button" class="btn-ghost flex-1 !py-2 text-sm" @click="cancel">取消</button>
            <button v-if="clearable && modelValue" type="button" class="btn-ghost flex-1 !py-2 text-sm !text-destructive" @click="clear">清除</button>
            <button type="button" class="btn-brand flex-1 !py-2 text-sm" @click="confirm">设置</button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch, onBeforeUnmount } from 'vue'
import { lockBodyScroll, unlockBodyScroll } from '../utils/scroll-lock'

// 自定义日期/月份选择器：替换原生 date/month 输入。
// 原生弹窗（WebView）无法控制按钮顺序与配色，这里统一为应用内弹层，
// 底部按钮为 取消 / 清除 / 设置，设置固定在最右。
const props = withDefaults(defineProps<{
  modelValue?: string
  type?: 'date' | 'month'
  placeholder?: string
  disabled?: boolean
  clearable?: boolean
}>(), {
  modelValue: '',
  type: 'date',
  placeholder: '',
  disabled: false,
  clearable: true,
})

const emit = defineEmits<{
  'update:modelValue': [value: string]
  change: [value: string]
}>()

defineOptions({ inheritAttrs: false })

const panelOpen = ref(false)
const draft = reactive({ y: 2026, m: 1, d: 1 })

const MIN_YEAR = 1970
const MAX_YEAR = 2099

const placeholder = computed(() => props.placeholder || (props.type === 'month' ? '选择月份' : '选择日期'))

const daysInMonth = computed(() => new Date(draft.y, draft.m, 0).getDate())

const columns = computed(() => {
  const cols: { key: 'y' | 'm' | 'd'; label: string; min: number; max: number }[] = [
    { key: 'y', label: '年', min: MIN_YEAR, max: MAX_YEAR },
    { key: 'm', label: '月', min: 1, max: 12 },
  ]
  if (props.type !== 'month') cols.push({ key: 'd', label: '日', min: 1, max: daysInMonth.value })
  return cols
})

// 月份/年份变化时把日收进当月（如 1-31 切到 2 月变 28/29）
watch(() => [draft.y, draft.m], () => {
  if (draft.d > daysInMonth.value) draft.d = daysInMonth.value
})

const preview = computed(() =>
  props.type === 'month'
    ? `${draft.y}-${pad(draft.m)}`
    : `${draft.y}-${pad(draft.m)}-${pad(draft.d)}`,
)

function pad(v: number): string {
  return String(v).padStart(2, '0')
}

function clampAll() {
  draft.y = Math.min(MAX_YEAR, Math.max(MIN_YEAR, Math.round(Number(draft.y) || MIN_YEAR)))
  draft.m = Math.min(12, Math.max(1, Math.round(Number(draft.m) || 1)))
  draft.d = Math.min(daysInMonth.value, Math.max(1, Math.round(Number(draft.d) || 1)))
}

function step(key: 'y' | 'm' | 'd', delta: number) {
  const limits: Record<'y' | 'm' | 'd', [number, number]> = {
    y: [MIN_YEAR, MAX_YEAR],
    m: [1, 12],
    d: [1, daysInMonth.value],
  }
  const [min, max] = limits[key]
  draft[key] = Math.min(max, Math.max(min, (Number(draft[key]) || 0) + delta))
}

function parseValue(v: string): { y: number; m: number; d: number } | null {
  if (props.type === 'month') {
    const m = /^(\d{4})-(\d{2})$/.exec(v || '')
    return m ? { y: Number(m[1]), m: Number(m[2]), d: 1 } : null
  }
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v || '')
  return m ? { y: Number(m[1]), m: Number(m[2]), d: Number(m[3]) } : null
}

function openPanel() {
  if (props.disabled) return
  const now = new Date()
  const parsed = parseValue(props.modelValue)
  draft.y = parsed?.y ?? now.getFullYear()
  draft.m = parsed?.m ?? now.getMonth() + 1
  draft.d = parsed?.d ?? now.getDate()
  clampAll()
  panelOpen.value = true
}

function confirm() {
  clampAll()
  emit('update:modelValue', preview.value)
  emit('change', preview.value)
  panelOpen.value = false
}

function clear() {
  emit('update:modelValue', '')
  emit('change', '')
  panelOpen.value = false
}

function cancel() {
  panelOpen.value = false
}

// 面板打开时锁住背景滚动
watch(panelOpen, (open) => {
  if (open) lockBodyScroll()
  else unlockBodyScroll()
})

onBeforeUnmount(() => {
  if (panelOpen.value) unlockBodyScroll()
})
</script>

<style scoped>
.stepper-btn {
  @apply w-full h-9 rounded-lg bg-muted text-foreground text-lg font-bold flex items-center justify-center active:scale-95 transition-transform;
}
.stepper-value {
  @apply w-full h-10 text-center text-lg font-bold tabular-nums bg-surface/60 border border-input rounded-lg text-foreground outline-none focus:border-brand-500 focus:ring-4 focus:ring-brand-500/15;
  appearance: textfield;
  -moz-appearance: textfield;
}
.stepper-value::-webkit-outer-spin-button,
.stepper-value::-webkit-inner-spin-button {
  -webkit-appearance: none;
  margin: 0;
}

.datefield-panel {
  transition: transform 0.28s cubic-bezier(0.32, 0.72, 0, 1);
}
.datefield-enter-active,
.datefield-leave-active {
  transition: opacity 0.25s ease;
}
.datefield-enter-from,
.datefield-leave-to {
  opacity: 0;
}
.datefield-enter-from .datefield-panel,
.datefield-leave-to .datefield-panel {
  transform: translateY(100%);
}
@media (min-width: 768px) {
  .datefield-enter-from .datefield-panel,
  .datefield-leave-to .datefield-panel {
    transform: scale(0.95);
  }
}
</style>
