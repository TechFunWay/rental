<template>
  <!-- 只读的抄表照片展示：有照片才渲染（没拍就不占地方），点缩略图看大图。
       用在账单详情这类「看凭证」的场景；上传/删除在抄表入口（MeterPhotoPicker）。 -->
  <div v-if="photos.length" class="space-y-2">
    <div class="flex items-center justify-between gap-2">
      <span class="text-xs font-semibold text-muted-foreground">抄表照片（{{ period }}）</span>
      <span class="text-[11px] text-muted-foreground">点缩略图看大图</span>
    </div>
    <div v-for="g in groups" :key="g.meter" class="space-y-0.5">
      <div class="text-[11px] text-muted-foreground">
        {{ g.label }}照片 <span class="tabular-nums">{{ g.items.length }}</span>
      </div>
      <div class="flex flex-wrap gap-1.5">
        <button
          v-for="p in g.items"
          :key="p.id"
          type="button"
          class="block w-14 h-14 rounded-lg overflow-hidden border border-border bg-muted active:scale-[0.97] transition-transform"
          :title="p.file_name"
          @click="openViewer(p)"
        >
          <img v-if="thumbs[p.id]" :src="thumbs[p.id]" class="w-full h-full object-cover" :alt="g.label + '照片'" />
          <span v-else class="w-full h-full flex items-center justify-center text-[10px] text-muted-foreground">…</span>
        </button>
      </div>
    </div>

    <Teleport to="body">
      <div
        v-if="viewer"
        class="fixed inset-0 z-[60] bg-black/80 flex flex-col items-center justify-center p-4"
        @click.self="viewer = null"
      >
        <img :src="viewer.url" class="max-w-full max-h-[80vh] object-contain rounded-lg" alt="表盘照片" />
        <div class="mt-3 flex items-center gap-3 text-xs text-white/85">
          <span class="truncate max-w-[60vw]">{{ viewer.name }}</span>
          <button type="button" class="px-3 py-1 rounded-lg bg-white/15 hover:bg-white/25" @click="viewer = null">关闭</button>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'

import { fetchMeterPhotoBlob, getMeterPhotos, type MeterPhoto } from '../api/rental'

// 抄表照片只读展示（账单详情等）。一次请求取回该房该账期的全部表盘照片，
// 前端按表种归组——不做「每张表一个请求」。
const props = defineProps<{
  roomId: number
  period: string
  /** 包月水不抄表，隐藏水表那一组 */
  waterMixed?: boolean
}>()

const photos = ref<MeterPhoto[]>([])
const thumbs = reactive<Record<number, string>>({})
const viewer = ref<{ url: string; name: string } | null>(null)
let token = 0

const LABELS: Record<string, string> = { water: '水表', elec: '电表', gas: '燃气表' }

const groups = computed(() => {
  const order = ['water', 'elec', 'gas']
  const byMeter = new Map<string, MeterPhoto[]>()
  for (const p of photos.value) {
    const list = byMeter.get(p.meter) ?? []
    list.push(p)
    byMeter.set(p.meter, list)
  }
  return order
    .filter((m) => byMeter.has(m) && !(m === 'water' && props.waterMixed))
    .map((m) => ({ meter: m, label: LABELS[m] ?? m, items: byMeter.get(m) ?? [] }))
})

function release() {
  // 大图可能正显示着某个缩略图的 objectURL：先关掉再撤地址，否则会留下裂图
  viewer.value = null
  for (const key of Object.keys(thumbs)) {
    URL.revokeObjectURL(thumbs[Number(key)])
    delete thumbs[Number(key)]
  }
}

async function load() {
  if (!props.roomId || !props.period) {
    photos.value = []
    return
  }
  const t = ++token
  try {
    const res = await getMeterPhotos({ room_id: props.roomId, period: props.period })
    if (t !== token) return
    photos.value = res.data?.code === 0 ? (res.data.data?.items ?? []) : []
    release()
    await Promise.all(photos.value.map(async (p) => {
      try {
        const blob = await fetchMeterPhotoBlob(p.id)
        if (t !== token) return
        thumbs[p.id] = URL.createObjectURL(blob)
      } catch {
        // 单张失败不影响其它
      }
    }))
  } catch {
    photos.value = []
  }
}

function openViewer(p: MeterPhoto) {
  const url = thumbs[p.id]
  if (!url) return
  viewer.value = { url, name: `${LABELS[p.meter] ?? p.meter} · ${p.file_name}` }
}

watch(() => [props.roomId, props.period] as const, () => { void load() }, { immediate: true })
onBeforeUnmount(() => {
  token++
  release()
})
</script>
