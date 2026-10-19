<template>
  <PhotoWall
    v-if="roomId"
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
</template>

<script setup lang="ts">
import { watch } from 'vue'

import PhotoWall from './PhotoWall.vue'
import {
  deleteRoomPhoto, fetchRoomPhotoBlob, getRoomPhotos, uploadRoomPhotos, type RoomPhoto,
} from '../api/rental'
import { usePhotoWall } from '../composables/usePhotoWall'

// 房源照片：一间房本身的照片（室内、门牌、表位等），按房间归属、与账期无关。
// 与服务端一致：只收图片、单张 ≤20MB、每间房最多 30 张。
// 房源详情弹窗里用它；列表页只显示张数，点进详情才加载。
const props = withDefaults(defineProps<{
  roomId: number
  /** 文案用的照片名，默认「房间」，如「房间照片 3/30」 */
  label?: string
  canEdit?: boolean
  max?: number
}>(), { label: '房间', canEdit: false, max: 30 })

const emit = defineEmits<{ changed: [] }>()

const { photos, thumbs, loading, uploading, error, load, pick, remove } = usePhotoWall<RoomPhoto>({
  label: props.label,
  max: props.max,
  load: async () => {
    const res = await getRoomPhotos(props.roomId)
    return { code: res.data?.code, message: res.data?.message, items: res.data?.data?.items ?? [] }
  },
  upload: async (files) => {
    const res = await uploadRoomPhotos(props.roomId, files)
    return { code: res.data?.code, message: res.data?.message, uploaded: res.data?.data?.uploaded }
  },
  remove: async (id) => {
    const res = await deleteRoomPhoto(id)
    return { code: res.data?.code, message: res.data?.message }
  },
  fetchBlob: fetchRoomPhotoBlob,
  onChanged: () => emit('changed'),
})

watch(() => props.roomId, () => { void load() }, { immediate: true })
</script>
