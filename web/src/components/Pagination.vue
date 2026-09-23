<template>
  <div class="flex items-center justify-between px-4 py-3 text-sm text-muted-foreground border-t border-border">
    <span>共 {{ total }} 条 · 第 {{ page }} / {{ pages }} 页</span>
    <div class="flex items-center gap-2">
      <button class="btn-ghost !px-3 !py-1.5" :disabled="page <= 1" @click="$emit('change', page - 1)">上一页</button>
      <button class="btn-ghost !px-3 !py-1.5" :disabled="page >= pages" @click="$emit('change', page + 1)">下一页</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  total: number
  page: number
  pageSize: number
}>()

defineEmits<{ change: [page: number] }>()

const pages = computed(() => Math.max(1, Math.ceil(props.total / props.pageSize)))
</script>
