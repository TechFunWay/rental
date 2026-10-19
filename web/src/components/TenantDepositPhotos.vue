<template>
  <div class="space-y-2">
    <!-- 已保存的租户：押金单照片就是带「押金单」标签的租户附件，直接走附件接口，
         上传/删除即时生效，并复用照片墙的缩略图、全屏查看与行内删除确认。 -->
    <template v-if="tenantId > 0">
      <PhotoWall
        :photos="photos"
        :thumbs="thumbs"
        :label="label"
        :max="max"
        :can-edit="canEdit"
        :loading="loading"
        :uploading="uploading"
        :error="error"
        @pick="pick"
        @remove="remove"
      />
      <p v-if="canEdit && photos.length === 0" class="text-[11px] text-muted-foreground">
        押金单是实物凭证，拍下来留档：退租结算、换房东、对账时都有据可查。
      </p>
    </template>

    <!-- 新租户还没保存、没有租户 ID：先把文件暂存在表单里，保存租户成功后由
         父组件统一上传（避免先建租户再补传的两步操作）。 -->
    <template v-else>
      <div class="flex items-center justify-between gap-2">
        <span class="text-[11px] text-muted-foreground">{{ label }}照片</span>
        <span class="text-[11px] text-muted-foreground tabular-nums">{{ pendingFiles.length }}/{{ max }}</span>
      </div>
      <div v-if="!canEdit" class="text-[11px] text-muted-foreground">保存租户后可补传押金单照片。</div>
      <template v-else>
        <div v-if="pendingFiles.length" class="flex flex-wrap items-center gap-1.5">
          <div v-for="(f, i) in pendingFiles" :key="`${f.name}-${f.size}-${i}`" class="relative">
            <img
              v-if="pendingThumbs[i]"
              :src="pendingThumbs[i]"
              class="w-14 h-14 rounded-lg object-cover border border-border bg-muted"
              :alt="f.name"
            />
            <span v-else class="w-14 h-14 rounded-lg border border-border bg-muted flex items-center justify-center text-[10px] text-muted-foreground">…</span>
            <button
              type="button"
              class="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-destructive text-white text-[11px] leading-none flex items-center justify-center shadow"
              title="移除这张照片"
              @click="removePending(i)"
            >×</button>
          </div>
        </div>
        <FilePick
          v-if="pendingFiles.length < max"
          accept="image/*"
          multiple
          :label="pendingFiles.length ? '继续添加押金单照片' : '拍押金单 / 从相册选'"
          hint="保存租户后自动上传；单张 ≤20MB，最多 6 张"
          @change="onPickPending"
        />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import FilePick from './FilePick.vue'
import PhotoWall from './PhotoWall.vue'
import { usePhotoWall } from '../composables/usePhotoWall'
import {
  deleteAttachment, fetchAttachmentBlob, getAttachments, uploadAttachments, type TenantAttachment,
} from '../api/rental'
import { toast } from '../utils/toast'

// 押金单照片（2026-10-17 粉丝反馈：押金不能只录一个数字，得有实物留档）。
//
// 实现刻意复用租户附件：上传时打「押金单」标签，列表接口按标签过滤。
// 不新开一张表——押金单和身份证、租房协议一样，都是挂在租户名下的留档材料，
// 复用附件能直接继承台账隔离、越权 404、上传大小/类型校验与文件清理。
//
// 两种模式：
//   - tenantId > 0：已保存租户，走附件接口即时上传/删除/预览；
//   - tenantId = 0：新租户表单，文件先暂存在 v-model:pending，保存后由父组件上传。
const props = withDefaults(defineProps<{
  tenantId?: number
  canEdit?: boolean
  label?: string
  max?: number
  pending?: File[]
}>(), { tenantId: 0, canEdit: false, label: '押金单', max: 6, pending: () => [] })

const emit = defineEmits<{
  'update:pending': [files: File[]]
  changed: []
}>()

const pendingFiles = computed(() => props.pending ?? [])

// ---------- 已保存租户：照片墙适配器 ----------
const { photos, thumbs, loading, uploading, error, load, pick, remove } = usePhotoWall<TenantAttachment>({
  label: props.label,
  max: props.max,
  load: async () => {
    const res = await getAttachments(props.tenantId, props.label)
    return { code: res.data?.code, message: res.data?.message, items: res.data?.data ?? [] }
  },
  upload: async (files) => {
    const res = await uploadAttachments(props.tenantId, files, props.label)
    return { code: res.data?.code, message: res.data?.message, uploaded: res.data?.data?.uploaded }
  },
  remove: async (id) => {
    const res = await deleteAttachment(id)
    return { code: res.data?.code, message: res.data?.message }
  },
  fetchBlob: fetchAttachmentBlob,
  onChanged: () => emit('changed'),
})

watch(() => props.tenantId, (id) => {
  if (id > 0) void load()
}, { immediate: true })

// ---------- 新租户：本地暂存 + 预览 ----------
const pendingThumbs = ref<string[]>([])
let thumbUrls: string[] = []

function releasePendingThumbs() {
  for (const url of thumbUrls) URL.revokeObjectURL(url)
  thumbUrls = []
  pendingThumbs.value = []
}

function rebuildPendingThumbs() {
  releasePendingThumbs()
  thumbUrls = pendingFiles.value.map((f) => URL.createObjectURL(f))
  pendingThumbs.value = thumbUrls
}

watch(pendingFiles, rebuildPendingThumbs, { immediate: true })
onBeforeUnmount(releasePendingThumbs)

function onPickPending(files: File[]) {
  if (files.length === 0) return
  const room = Math.max(0, props.max - pendingFiles.value.length)
  if (room === 0) {
    toast(`最多 ${props.max} 张押金单照片`, 'error')
    return
  }
  emit('update:pending', [...pendingFiles.value, ...files.slice(0, room)])
  if (files.length > room) toast(`最多 ${props.max} 张，已保留前 ${room} 张`, 'error')
}

function removePending(index: number) {
  const next = [...pendingFiles.value]
  next.splice(index, 1)
  emit('update:pending', next)
}
</script>
