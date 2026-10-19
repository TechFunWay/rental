<template>
  <div class="surface rounded-2xl p-4 sm:p-6">
    <!-- 头部：标题 + 件数 + 新增。物品台账是「退租核对」用的，所以标题旁直接说明用途。 -->
    <div class="flex items-start justify-between gap-3 mb-3">
      <div class="min-w-0">
        <h3 class="text-sm sm:text-base font-bold text-foreground">房间物品</h3>
        <p class="text-xs text-muted-foreground mt-0.5">
          记下这间房里有什么（冰箱/洗衣机/机顶盒/路由器/钥匙…），退租时照着核对缺失与损坏；物品跟着房间走，换租客也还在，每间房最多 {{ ROOM_ITEM_MAX }} 件。
        </p>
      </div>
      <button
        v-if="canEdit"
        type="button"
        class="btn-brand !px-3 !py-2 text-xs shrink-0"
        @click="openCreate"
      >＋ 新增物品</button>
    </div>

    <div v-if="loading" class="text-xs text-muted-foreground py-2">物品加载中…</div>
    <p v-else-if="error" class="text-xs text-destructive">{{ error }}</p>

    <template v-else>
      <p v-if="items.length === 0" class="text-xs text-muted-foreground py-2">
        还没有登记物品。点「新增物品」把房东提供的东西记下来，退租时就能对照清单点了。
      </p>

      <!-- 桌面端：表格 -->
      <div v-else class="hidden sm:block overflow-x-auto">
        <table class="w-full text-sm min-w-[560px]">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th class="py-2 px-3 font-medium whitespace-nowrap">物品</th>
              <th class="py-2 px-3 font-medium whitespace-nowrap text-right">数量</th>
              <th class="py-2 px-3 font-medium whitespace-nowrap">状况</th>
              <th class="py-2 px-3 font-medium whitespace-nowrap">归属</th>
              <th class="py-2 px-3 font-medium whitespace-nowrap">照片</th>
              <th v-if="canEdit" class="py-2 px-3 font-medium whitespace-nowrap text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="it in items" :key="it.id" class="hover:bg-muted/30 transition-colors align-top">
              <td class="py-2 px-3">
                <div class="font-semibold text-foreground">{{ it.name }}</div>
                <div v-if="it.note" class="text-[11px] text-muted-foreground mt-0.5 max-w-[22rem]">{{ it.note }}</div>
              </td>
              <td class="py-2 px-3 text-right tabular-nums">{{ it.quantity }}</td>
              <td class="py-2 px-3 whitespace-nowrap">
                <span :class="conditionClass(it.condition)">{{ it.condition || '—' }}</span>
              </td>
              <td class="py-2 px-3 whitespace-nowrap">
                <span :class="it.builtin ? 'badge bg-brand-500/10 text-brand-600 dark:text-brand-300' : 'badge bg-muted text-muted-foreground'">
                  {{ it.builtin ? '房东提供' : '租客自带' }}
                </span>
              </td>
              <td class="py-2 px-3 whitespace-nowrap">
                <button type="button" class="text-brand-600 dark:text-brand-300 hover:underline" @click="openPhotos(it)">
                  照片 {{ it.photo_count }}
                </button>
              </td>
              <td v-if="canEdit" class="py-2 px-3 text-right whitespace-nowrap">
                <button type="button" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(it)">编辑</button>
                <button type="button" class="text-destructive hover:underline" @click="askRemove(it)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：每件物品一张独立卡片（容器 p-2 space-y-1.5，卡间 6px 间隙） -->
      <div v-if="items.length" class="sm:hidden p-2 space-y-1.5">
        <div
          v-for="it in items"
          :key="it.id"
          class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5"
        >
          <div class="flex items-start gap-2">
            <div class="min-w-0 flex-1">
              <div class="text-sm font-semibold text-foreground truncate">{{ it.name }}</div>
              <div v-if="it.note" class="text-[11px] text-muted-foreground mt-0.5 break-words">{{ it.note }}</div>
            </div>
            <span class="shrink-0 text-xs text-muted-foreground tabular-nums">×{{ it.quantity }}</span>
          </div>
          <div class="flex items-center flex-wrap gap-1.5">
            <span :class="it.builtin ? 'badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300' : 'badge !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground'">
              {{ it.builtin ? '房东提供' : '租客自带' }}
            </span>
            <span v-if="it.condition" :class="conditionClass(it.condition, '!px-1.5 !py-0.5 text-[11px]')">{{ it.condition }}</span>
            <button type="button" class="text-[11px] text-brand-600 dark:text-brand-300" @click="openPhotos(it)">
              照片 {{ it.photo_count }}
            </button>
            <template v-if="canEdit">
              <button type="button" class="text-[11px] text-brand-600 dark:text-brand-300 ml-auto" @click="openEdit(it)">编辑</button>
              <button type="button" class="text-[11px] text-destructive" @click="askRemove(it)">删除</button>
            </template>
          </div>
        </div>
      </div>
    </template>

    <!-- 新增/编辑物品 -->
    <Modal v-model="showForm" :title="editing ? '编辑物品' : '新增物品'">
      <form class="space-y-3.5" @submit.prevent="save">
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">物品名称 *</label>
          <input v-model="form.name" class="input-field" :maxlength="60" placeholder="如 路由器 / 洗衣机 / 钥匙" />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">数量</label>
            <input v-model.number="form.quantity" type="number" min="1" max="999" class="input-field" placeholder="如 2" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">状况</label>
            <input v-model="form.condition" class="input-field" :maxlength="50" list="room-item-conditions" placeholder="完好 / 损坏 / 缺失" />
            <datalist id="room-item-conditions">
              <option value="完好" />
              <option value="轻微磨损" />
              <option value="损坏" />
              <option value="缺失" />
            </datalist>
          </div>
        </div>

        <!-- 归属：这直接回答「分不清哪些是自己买的、哪些是租客自带的」 -->
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">这件东西是谁的</label>
          <div class="grid grid-cols-2 gap-2">
            <button
              type="button"
              :class="form.builtin ? 'border-brand-500 bg-brand-500/10 text-foreground' : 'border-border text-muted-foreground'"
              class="rounded-xl border px-3 py-2.5 text-xs font-medium text-left"
              @click="form.builtin = true"
            >
              <span class="block text-sm font-semibold">房东提供</span>
              <span class="text-[11px] text-muted-foreground">退租时要核对缺失/损坏</span>
            </button>
            <button
              type="button"
              :class="!form.builtin ? 'border-brand-500 bg-brand-500/10 text-foreground' : 'border-border text-muted-foreground'"
              class="rounded-xl border px-3 py-2.5 text-xs font-medium text-left"
              @click="form.builtin = false"
            >
              <span class="block text-sm font-semibold">租客自带</span>
              <span class="text-[11px] text-muted-foreground">搬走时跟着租客走</span>
            </button>
          </div>
        </div>

        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <textarea v-model="form.note" rows="2" class="input-field" :maxlength="200" placeholder="如 放在客厅电视柜，遥控器缺电池" />
        </div>

        <p v-if="formError" class="text-sm text-destructive">{{ formError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showForm = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 某件物品的照片：上传、缩略图、删除、看大图 -->
    <Modal v-model="showPhotos" :title="photoTitle">
      <div class="space-y-3">
        <div v-if="photoLoading" class="text-xs text-muted-foreground">照片加载中…</div>
        <div v-else class="flex flex-wrap items-center gap-2">
          <div v-for="p in photoItem?.photos ?? []" :key="p.id" class="relative">
            <button
              type="button"
              class="block w-16 h-16 rounded-lg overflow-hidden border border-border bg-muted"
              :title="p.file_name"
              @click="openViewer(p)"
            >
              <img v-if="thumbs[p.id]" :src="thumbs[p.id]" class="w-full h-full object-cover" :alt="photoItem?.name ?? '物品照片'" />
              <span v-else class="w-full h-full flex items-center justify-center text-[10px] text-muted-foreground">…</span>
            </button>
            <button
              v-if="canEdit"
              type="button"
              class="absolute -top-1.5 -right-1.5 w-5 h-5 rounded-full bg-destructive text-white text-[11px] leading-none flex items-center justify-center shadow"
              title="删除这张照片"
              @click.stop="photoDeleteTarget = p"
            >×</button>
          </div>

          <button
            v-if="canEdit && (photoItem?.photos?.length ?? 0) < ROOM_ITEM_PHOTO_MAX"
            type="button"
            class="w-16 h-16 rounded-lg border-2 border-dashed border-border hover:border-brand-500/60 bg-muted/40 disabled:opacity-60 disabled:pointer-events-none flex flex-col items-center justify-center gap-0.5 text-[10px] text-muted-foreground"
            :disabled="photoUploading"
            @click="photoInput?.click()"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 9a2 2 0 012-2h.93a2 2 0 001.66-.9l.82-1.2A2 2 0 0110.07 4h3.86a2 2 0 011.66.9l.82 1.2a2 2 0 001.66.9H19a2 2 0 012 2v9a2 2 0 01-2 2H5a2 2 0 01-2-2V9z" />
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 13a3 3 0 11-6 0 3 3 0 016 0z" />
            </svg>
            <span>{{ photoUploading ? '上传中' : '拍照' }}</span>
          </button>
          <span v-else-if="!canEdit && (photoItem?.photos?.length ?? 0) === 0" class="text-xs text-muted-foreground">未上传</span>

          <input ref="photoInput" type="file" accept="image/*" multiple class="hidden" @change="onPickPhotos" />
        </div>

        <div v-if="photoDeleteTarget" class="flex items-center gap-3 text-xs">
          <span class="text-destructive">删除这张照片？</span>
          <button type="button" class="text-destructive font-medium" @click="confirmPhotoDelete">删除</button>
          <button type="button" class="text-muted-foreground" @click="photoDeleteTarget = null">取消</button>
        </div>
        <p class="text-[11px] text-muted-foreground">
          单张 ≤20MB、一次最多 {{ ROOM_ITEM_PHOTO_BATCH }} 张、每件物品最多 {{ ROOM_ITEM_PHOTO_MAX }} 张；点缩略图看大图。
        </p>
        <p v-if="photoError" class="text-xs text-destructive">{{ photoError }}</p>
      </div>
    </Modal>

    <!-- 看大图（挂 body，不受弹窗容器裁剪） -->
    <Teleport to="body">
      <div
        v-if="viewer"
        class="fixed inset-0 z-[60] bg-black/80 flex flex-col items-center justify-center p-4"
        @click.self="viewer = null"
      >
        <img :src="viewer.url" class="max-w-full max-h-[80vh] object-contain rounded-lg" :alt="photoItem?.name ?? '物品照片'" />
        <div class="mt-3 flex items-center gap-3 text-xs text-white/85">
          <span class="truncate max-w-[60vw]">{{ viewer.name }}</span>
          <button type="button" class="px-3 py-1 rounded-lg bg-white/15 hover:bg-white/25" @click="viewer = null">关闭</button>
        </div>
      </div>
    </Teleport>

    <ConfirmDialog
      v-model="confirmState.show"
      title="删除物品"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="onConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'

import ConfirmDialog from './ConfirmDialog.vue'
import Modal from './Modal.vue'
import {
  createRoomItem, deleteRoomItem, deleteRoomItemPhoto, fetchRoomItemPhotoBlob,
  getRoomItems, updateRoomItem, uploadRoomItemPhotos,
  type RoomItemInput, type RoomItemPhoto, type RoomItemView,
} from '../api/rental'
import { toast } from '../utils/toast'

// 房间物品台账：一间房里有什么东西（含房东提供/租客自带），退租时照清单核对。
//
// 与服务端口径一致（server/rental/room_items.go）：
//   - 绑房间不绑租户；归属只由 props.roomId 决定；
//   - 每间房最多 60 件、名称 ≤50 字、数量夹到 1–999；
//   - 照片只收图片、单张 ≤20MB、单批 6 张、每件物品最多 10 张。
//
// 权限由外层传 canEdit（房源模块操作档），与 RoomPhotoSection 同一约定：
// 组件不做权限判断，也不直接读 ledger store。
const ROOM_ITEM_MAX = 60
const ROOM_ITEM_PHOTO_MAX = 10
const ROOM_ITEM_PHOTO_BATCH = 6

const props = withDefaults(defineProps<{
  roomId: number
  /** 是否可编辑（房源模块操作档）；false 时只看不改 */
  canEdit?: boolean
}>(), { canEdit: false })

const emit = defineEmits<{ changed: [] }>()

// ---------------------------------------------------------------------------
// 清单
// ---------------------------------------------------------------------------

const items = ref<RoomItemView[]>([])
const loading = ref(false)
const error = ref('')
// 竞态保护：快速切换房间时旧请求不许覆盖新结果。
let loadToken = 0

async function load() {
  if (!props.roomId) {
    items.value = []
    return
  }
  const token = ++loadToken
  loading.value = true
  error.value = ''
  try {
    const res = await getRoomItems(props.roomId)
    if (token !== loadToken) return
    items.value = res.data?.code === 0 ? (res.data?.data?.items ?? []) : []
    if (res.data?.code !== 0) error.value = res.data?.message || '加载物品失败'
  } catch (err: any) {
    if (token === loadToken) error.value = err.response?.data?.message || '加载物品失败'
  } finally {
    if (token === loadToken) loading.value = false
  }
}

watch(() => props.roomId, () => { void load() }, { immediate: true })

/** 状况徽标配色：完好绿、损坏/缺失红、其它中性。 */
function conditionClass(condition: string, extra = ''): string {
  const c = (condition || '').trim()
  let tone = 'bg-muted text-muted-foreground'
  if (c === '完好') tone = 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
  else if (c.includes('损坏') || c.includes('缺失')) tone = 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
  else if (c.includes('磨损') || c.includes('旧')) tone = 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
  return `badge ${extra} ${tone}`
}

// ---------------------------------------------------------------------------
// 新增 / 编辑 / 删除
// ---------------------------------------------------------------------------

const showForm = ref(false)
const editing = ref<RoomItemView | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref<RoomItemInput>({ name: '', quantity: 1, condition: '完好', note: '', builtin: true })

function openCreate() {
  editing.value = null
  form.value = { name: '', quantity: 1, condition: '完好', note: '', builtin: true }
  formError.value = ''
  showForm.value = true
}

function openEdit(it: RoomItemView) {
  editing.value = it
  form.value = {
    name: it.name, quantity: it.quantity,
    condition: it.condition, note: it.note, builtin: it.builtin,
  }
  formError.value = ''
  showForm.value = true
}

async function save() {
  const name = form.value.name.trim()
  if (!name) {
    formError.value = '请填写物品名称'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    const payload: RoomItemInput = {
      name,
      quantity: Number(form.value.quantity) || 1,
      condition: (form.value.condition || '').trim(),
      note: (form.value.note || '').trim(),
      builtin: form.value.builtin,
    }
    const res = editing.value
      ? await updateRoomItem(editing.value.id, payload)
      : await createRoomItem(props.roomId, payload)
    if (res.data?.code === 0) {
      showForm.value = false
      toast('已保存')
      await load()
      emit('changed')
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch (err: any) {
    formError.value = err.response?.data?.message || '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, message: '', action: null as null | (() => Promise<void>) })

function askRemove(it: RoomItemView) {
  const photoTip = it.photo_count > 0 ? `它的 ${it.photo_count} 张照片也会一起删掉。` : ''
  confirmState.value = {
    show: true,
    message: `确定删除「${it.name}」？${photoTip}`,
    action: async () => {
      const res = await deleteRoomItem(it.id)
      if (res.data?.code !== 0) {
        toast(res.data?.message || '删除失败', 'error')
        return
      }
      toast('已删除')
      await load()
      emit('changed')
    },
  }
}

async function onConfirm() {
  await confirmState.value.action?.()
}

// ---------------------------------------------------------------------------
// 物品照片
// ---------------------------------------------------------------------------

const showPhotos = ref(false)
// 照片弹窗里展示的是「列表里那一行的快照」；上传/删除后重新拉列表并用最新行替换，
// 这样列表上的 photo_count 与弹窗里的缩略图始终一致。
const photoItem = ref<RoomItemView | null>(null)
const photoLoading = ref(false)
const photoUploading = ref(false)
const photoError = ref('')
const photoDeleteTarget = ref<RoomItemPhoto | null>(null)
const photoInput = ref<HTMLInputElement | null>(null)
const thumbs = reactive<Record<number, string>>({})
// 看大图：按照片 id 记身份（不用文件名——同一件物品的两张照片可能同名）。
const viewer = ref<{ id: number; url: string; name: string } | null>(null)

const photoTitle = computed(() => `物品照片${photoItem.value ? ' · ' + photoItem.value.name : ''}`)

/** 回收全部缩略图 objectURL（照片必须带登录态读，只能 blob 转 objectURL）。 */
function releaseThumbs() {
  for (const key of Object.keys(thumbs)) {
    URL.revokeObjectURL(thumbs[Number(key)])
    delete thumbs[Number(key)]
  }
}

/** 拉某件物品的照片缩略图，并同步回列表里对应的那一行。 */
async function loadThumbs(row: RoomItemView) {
  releaseThumbs()
  photoLoading.value = true
  try {
    await Promise.all((row.photos ?? []).map(async (p) => {
      try {
        const blob = await fetchRoomItemPhotoBlob(p.id)
        if (photoItem.value?.id !== row.id) return
        thumbs[p.id] = URL.createObjectURL(blob)
      } catch {
        // 单张失败不阻塞其余，也不弹错
      }
    }))
  } finally {
    if (photoItem.value?.id === row.id) photoLoading.value = false
  }
}

function openPhotos(it: RoomItemView) {
  photoItem.value = it
  photoError.value = ''
  photoDeleteTarget.value = null
  showPhotos.value = true
  void loadThumbs(it)
}

/** 上传/删除后刷新：重拉列表，并把弹窗指向最新的那一行。 */
async function refreshAfterPhotoChange() {
  const id = photoItem.value?.id
  await load()
  emit('changed')
  if (!id) return
  const next = items.value.find((x) => x.id === id) ?? null
  photoItem.value = next
  if (next) await loadThumbs(next)
  else {
    releaseThumbs()
    showPhotos.value = false
  }
}

async function onPickPhotos(e: Event) {
  const input = e.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  input.value = '' // 清空以便重复选同一张
  const row = photoItem.value
  if (!row || files.length === 0) return

  const remain = ROOM_ITEM_PHOTO_MAX - (row.photos?.length ?? 0)
  if (remain <= 0) {
    photoError.value = `最多 ${ROOM_ITEM_PHOTO_MAX} 张，请先删除不用的`
    return
  }
  // 按剩余额度与单批上限截断（服务端也各有一道，这里只是少跑一趟）。
  const picked = files.slice(0, Math.min(remain, ROOM_ITEM_PHOTO_BATCH))
  if (picked.length < files.length) {
    toast(`每件物品最多 ${ROOM_ITEM_PHOTO_MAX} 张、一次最多 ${ROOM_ITEM_PHOTO_BATCH} 张，本次只上传前 ${picked.length} 张`, 'error')
  }
  photoUploading.value = true
  photoError.value = ''
  try {
    const res = await uploadRoomItemPhotos(row.id, picked)
    if (res.data?.code === 0) {
      toast(`已上传 ${res.data?.data?.uploaded ?? picked.length} 张照片`)
      await refreshAfterPhotoChange()
    } else {
      photoError.value = res.data?.message || '上传失败'
    }
  } catch (err: any) {
    photoError.value = err.response?.data?.message || '上传失败，请重试'
  } finally {
    photoUploading.value = false
  }
}

function openViewer(p: RoomItemPhoto) {
  const url = thumbs[p.id]
  if (!url) return
  viewer.value = { id: p.id, url, name: p.file_name }
}

async function confirmPhotoDelete() {
  const target = photoDeleteTarget.value
  photoDeleteTarget.value = null
  if (!target) return
  if (viewer.value?.id === target.id) viewer.value = null
  photoError.value = ''
  try {
    const res = await deleteRoomItemPhoto(target.id)
    if (res.data?.code === 0) {
      toast('已删除照片')
      await refreshAfterPhotoChange()
    } else {
      photoError.value = res.data?.message || '删除失败'
    }
  } catch (err: any) {
    photoError.value = err.response?.data?.message || '删除失败，请重试'
  }
}

onBeforeUnmount(() => {
  loadToken++
  releaseThumbs()
})
</script>
