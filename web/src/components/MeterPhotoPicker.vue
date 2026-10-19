<template>
  <PhotoWall
    v-if="roomId && period"
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
  deleteMeterPhoto, fetchMeterPhotoBlob, getMeterPhotos, uploadMeterPhotos,
  type MeterKind, type MeterPhoto,
} from '../api/rental'
import { usePhotoWall } from '../composables/usePhotoWall'

// 抄表照片选择器：按「房间 + 账期 + 表种」加载/上传/查看/删除。
// 抄表开票弹窗（每张表一个）与抄表记录页（照片弹窗）共用同一个组件，
// 上传口径与服务端一致（图片、单张 ≤20MB、每表最多 6 张）；
// 取数与交互都在 usePhotoWall + PhotoWall 里，这里只做接口适配。
const props = withDefaults(defineProps<{
  roomId: number
  period: string
  meter: MeterKind
  /** 表名，用于文案，如「水表」 */
  label: string
  canEdit?: boolean
  max?: number
}>(), { canEdit: false, max: 6 })

const emit = defineEmits<{ changed: [] }>()

const { photos, thumbs, loading, uploading, error, load, pick, remove } = usePhotoWall<MeterPhoto>({
  label: props.label,
  max: props.max,
  load: async () => {
    const res = await getMeterPhotos({ room_id: props.roomId, period: props.period, meter: props.meter })
    return { code: res.data?.code, message: res.data?.message, items: res.data?.data?.items ?? [] }
  },
  upload: async (files) => {
    const res = await uploadMeterPhotos(props.roomId, files, props.period, props.meter)
    return { code: res.data?.code, message: res.data?.message, uploaded: res.data?.data?.uploaded }
  },
  remove: async (id) => {
    const res = await deleteMeterPhoto(id)
    return { code: res.data?.code, message: res.data?.message }
  },
  fetchBlob: fetchMeterPhotoBlob,
  onChanged: () => emit('changed'),
})

watch(() => [props.roomId, props.period, props.meter] as const, () => { void load() }, { immediate: true })
</script>
