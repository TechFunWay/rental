<template>
  <!-- 单据纸张选择：预设 + 自定义尺寸（针式打印纸/发货单等非标准纸张）。
       三处用到（单张弹窗、批量弹窗、批量打印工具栏），抽成组件避免三份重复。
       选中「自定义尺寸…」时展开宽高输入，改完即时保存并通知父级重新应用 @page。 -->
  <div class="flex flex-wrap items-center gap-1.5">
    <select
      :value="paper"
      class="input-field !py-1.5 !w-auto text-xs"
      @change="onPick(($event.target as HTMLSelectElement).value)"
    >
      <option v-for="p in receiptPapers" :key="p.value" :value="p.value">{{ p.label }}</option>
    </select>

    <template v-if="paper === 'custom'">
      <input
        :value="custom.width"
        type="number"
        :min="CUSTOM_PAPER_LIMITS.min"
        :max="CUSTOM_PAPER_LIMITS.max"
        class="input-field !py-1.5 !w-[72px] text-xs"
        title="纸张宽度（毫米）"
        @change="onSize(($event.target as HTMLInputElement).value, custom.height)"
      />
      <span class="text-[11px] text-muted-foreground">×</span>
      <input
        :value="custom.height"
        type="number"
        :min="CUSTOM_PAPER_LIMITS.min"
        :max="CUSTOM_PAPER_LIMITS.max"
        class="input-field !py-1.5 !w-[72px] text-xs"
        title="纸张高度（毫米）"
        @change="onSize(custom.width, ($event.target as HTMLInputElement).value)"
      />
      <span class="text-[11px] text-muted-foreground">mm</span>
      <label class="text-[11px] text-muted-foreground flex items-center gap-1 cursor-pointer">
        <input type="checkbox" :checked="custom.landscape" class="w-3.5 h-3.5" @change="onLandscape(($event.target as HTMLInputElement).checked)" />
        横向
      </label>
    </template>
  </div>
</template>

<script setup lang="ts">
import {
  CUSTOM_PAPER_LIMITS, isValidCustomPaper, loadCustomPaper, receiptPapers,
  saveCustomPaper, type CustomPaper, type ReceiptPaper,
} from '../utils/receiptPaper'

const props = defineProps<{ paper: ReceiptPaper; custom: CustomPaper }>()
const emit = defineEmits<{
  (e: 'update:paper', v: ReceiptPaper): void
  (e: 'update:custom', v: CustomPaper): void
}>()

function onPick(v: string) {
  emit('update:paper', v as ReceiptPaper)
}

// 宽高只接受 40–1000mm 的合法值，非法输入丢弃（保持原值），避免写出非法 @page。
function onSize(wRaw: string | number, hRaw: string | number) {
  const w = Number(wRaw)
  const h = Number(hRaw)
  const next: CustomPaper = { width: w, height: h, landscape: props.custom.landscape }
  if (!isValidCustomPaper(next)) {
    // 回退到上次保存的合法值，让输入框自己纠回来
    emit('update:custom', { ...loadCustomPaper() })
    return
  }
  saveCustomPaper(next)
  emit('update:custom', next)
}

function onLandscape(v: boolean) {
  const next: CustomPaper = { ...props.custom, landscape: v }
  saveCustomPaper(next)
  emit('update:custom', next)
}
</script>
