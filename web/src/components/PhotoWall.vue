<template>
  <div>
    <div v-if="label" class="flex items-center justify-between gap-2 mb-1">
      <span class="text-[11px] text-muted-foreground">{{ label }}照片</span>
      <span class="text-[11px] text-muted-foreground tabular-nums">{{ photos.length }}/{{ max }}</span>
    </div>
    <div v-if="loading" class="text-[11px] text-muted-foreground">照片加载中…</div>
    <div v-else class="flex flex-wrap items-center gap-1.5">
      <div v-for="p in photos" :key="p.id" class="relative">
        <button
          type="button"
          class="block w-14 h-14 rounded-lg overflow-hidden border border-border bg-muted active:scale-[0.97] transition-transform"
          :title="p.file_name"
          @click="openViewer(p)"
        >
          <img v-if="thumbs[p.id]" :src="thumbs[p.id]" class="w-full h-full object-cover" :alt="label + '照片'" />
          <span v-else class="w-full h-full flex items-center justify-center text-[10px] text-muted-foreground">…</span>
        </button>
        <!-- 删除走行内确认条（不用原生 confirm）：照片是留档凭证，误删要有一次反悔 -->
        <button
          v-if="canEdit"
          type="button"
          class="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-destructive text-white text-[11px] leading-none flex items-center justify-center shadow"
          title="删除这张照片"
          @click.stop="deleteTarget = p"
        >×</button>
      </div>

      <!-- 紧凑「拍照/相册」方块：同一屏里可能并排放好几组照片（抄表三张表、
           房源详情一栏），FilePick 的大卡片会把表单撑得过高；这里沿用它的
           隐藏原生 input 做法，只换成小方块 -->
      <button
        v-if="canEdit && photos.length < max"
        type="button"
        class="w-14 h-14 rounded-lg border-2 border-dashed border-border hover:border-brand-500/60 bg-muted/40 hover:bg-muted/70 disabled:opacity-60 disabled:pointer-events-none flex flex-col items-center justify-center gap-0.5 text-[10px] text-muted-foreground"
        :disabled="uploading"
        @click="inputEl?.click()"
      >
        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 9a2 2 0 012-2h.93a2 2 0 001.66-.9l.82-1.2A2 2 0 0110.07 4h3.86a2 2 0 011.66.9l.82 1.2a2 2 0 001.66.9H19a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V9z" />
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 13a3 3 0 11-6 0 3 3 0 016 0z" />
        </svg>
        <span>{{ uploading ? '上传中' : '拍照' }}</span>
      </button>
      <span v-else-if="!canEdit && photos.length === 0" class="text-[11px] text-muted-foreground">未上传</span>

      <input ref="inputEl" type="file" accept="image/*" multiple class="hidden" @change="onPickInput" />
    </div>

    <div v-if="deleteTarget" class="mt-1.5 flex items-center gap-2 text-[11px]">
      <span class="text-destructive">删除这张照片？</span>
      <button type="button" class="text-destructive font-medium" @click="confirmDelete">删除</button>
      <button type="button" class="text-muted-foreground" @click="deleteTarget = null">取消</button>
    </div>
    <p v-if="error" class="text-[11px] text-destructive mt-1">{{ error }}</p>

    <!-- 点缩略图看大图：表盘数字、门牌号要能看清，这里给全屏查看
         （挂在 body 上，不受弹窗容器的 overflow 裁剪） -->
    <Teleport to="body">
      <div
        v-if="viewer"
        class="fixed inset-0 z-[60] bg-black/80 flex flex-col items-center justify-center p-4"
        @click.self="viewer = null"
      >
        <img :src="viewer.url" class="max-w-full max-h-[80vh] object-contain rounded-lg" :alt="label + '照片'" />
        <div class="mt-3 flex items-center gap-3 text-xs text-white/85">
          <span class="truncate max-w-[60vw]">{{ viewer.photo.file_name }}</span>
          <button type="button" class="px-3 py-1 rounded-lg bg-white/15 hover:bg-white/25" @click="viewer = null">关闭</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

import type { PhotoWallItem } from '../composables/usePhotoWall'

// 照片墙（纯展示 + 交互）：缩略图、拍照入口、行内删除确认、全屏看大图。
// 取数与上传删除由 usePhotoWall 负责，这里只负责渲染与把用户动作 emit 出去，
// 所以抄表照片与房源照片能共用同一套外观与交互。
const props = withDefaults(defineProps<{
  photos: PhotoWallItem[]
  thumbs: Record<number, string>
  label?: string
  max?: number
  canEdit?: boolean
  loading?: boolean
  uploading?: boolean
  error?: string
}>(), {
  label: '',
  max: 6,
  canEdit: false,
  loading: false,
  uploading: false,
  error: '',
})

const emit = defineEmits<{
  pick: [files: File[]]
  remove: [id: number]
}>()

const inputEl = ref<HTMLInputElement | null>(null)
const deleteTarget = ref<PhotoWallItem | null>(null)
const viewer = ref<{ photo: PhotoWallItem; url: string } | null>(null)

function onPickInput(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = '' // 清空以便重复选同一张
  if (files.length) emit('pick', files)
}

function openViewer(p: PhotoWallItem) {
  const url = props.thumbs[p.id]
  if (!url) return
  viewer.value = { photo: p, url }
}

function confirmDelete() {
  const target = deleteTarget.value
  if (!target) return
  deleteTarget.value = null
  if (viewer.value?.photo.id === target.id) viewer.value = null
  emit('remove', target.id)
}
</script>
