<template>
  <Modal :model-value="modelValue" title="租户资料变更申请" wide @update:model-value="emit('update:modelValue', $event)">
    <div v-if="loading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="requests.length === 0" class="py-8 text-center text-sm text-muted-foreground">
      没有待审核的资料变更申请
    </div>

    <div v-else class="space-y-3">
      <div
        v-for="r in requests"
        :key="r.id"
        class="rounded-xl border border-border p-3 space-y-3"
      >
        <!-- 头部：租户 + 房源 + 提交时间 -->
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="text-sm font-semibold text-foreground truncate">
              {{ r.tenant_name || `租户 #${r.tenant_id}` }}
              <span class="text-xs font-normal text-muted-foreground">· {{ r.room_label || '未关联房源' }}</span>
            </div>
            <div class="mt-0.5 text-[11px] text-muted-foreground">
              账号 {{ r.applicant || '—' }} · 提交于 {{ fmtTime(r.created_at) }}
            </div>
          </div>
          <span class="badge shrink-0" :class="changeStatusClass(r.status)">{{ CHANGE_STATUS_LABEL[r.status] || r.status }}</span>
        </div>

        <!-- 逐项对照：正式值 → 申请值；只显示本次真的改动了的项 -->
        <div class="rounded-xl border border-border divide-y divide-border/60">
          <div
            v-for="row in changedRows(r)"
            :key="row.label"
            class="flex items-start justify-between gap-3 px-3 py-2 text-xs"
          >
            <span class="text-muted-foreground shrink-0">{{ row.label }}</span>
            <span class="text-right min-w-0 break-all">
              <span class="text-muted-foreground line-through">{{ row.current || '（空）' }}</span>
              <span class="mx-1.5 text-muted-foreground">→</span>
              <span class="text-foreground font-semibold">{{ row.next || '（空）' }}</span>
            </span>
          </div>
          <div v-if="changedRows(r).length === 0" class="px-3 py-2 text-xs text-muted-foreground">
            本次只上传了证件照，没有文字信息变更
          </div>
        </div>

        <div v-if="r.note" class="text-[11px] leading-5 text-muted-foreground">
          租户留言：{{ r.note }}
        </div>

        <!-- 本次上传的证件照：缩略图按需取 blob（带台账鉴权，<img src> 直连会 401） -->
        <div v-if="r.photo_urls.length" class="space-y-1.5">
          <div class="text-[11px] font-semibold text-muted-foreground">本次上传证件照（{{ r.photo_urls.length }} 张）</div>
          <div class="flex flex-wrap gap-2">
            <button
              v-for="(url, i) in r.photo_urls"
              :key="url"
              type="button"
              class="relative w-20 h-20 rounded-xl border border-border overflow-hidden bg-muted"
              @click="openPhoto(r.id, i)"
            >
              <img
                v-if="thumbs[`${r.id}-${i}`]"
                :src="thumbs[`${r.id}-${i}`]"
                alt="证件照"
                class="w-full h-full object-cover"
              />
              <span v-else class="text-[10px] text-muted-foreground">加载中</span>
            </button>
          </div>
        </div>

        <!-- 操作：通过 / 驳回（驳回可写理由） -->
        <div class="flex flex-wrap items-center gap-2 pt-1">
          <button
            class="btn-brand !py-2 flex-1 min-w-[120px]"
            :disabled="busyId === r.id"
            @click="approve(r)"
          >
            {{ busyId === r.id ? '处理中…' : '通过并更新资料' }}
          </button>
          <button
            class="btn-ghost !py-2 flex-1 min-w-[100px]"
            :disabled="busyId === r.id"
            @click="askReject(r.id)"
          >
            驳回
          </button>
        </div>
      </div>
    </div>

    <!-- 照片预览 -->
    <Modal v-model="showPhoto" :title="`证件照 ${photoIndex + 1}`" wide>
      <div v-if="photoLoading" class="py-10 text-center text-sm text-muted-foreground">加载中…</div>
      <img v-else-if="photoUrl" :src="photoUrl" alt="证件照" class="w-full rounded-xl border border-border" />
      <div v-else class="py-10 text-center text-sm text-muted-foreground">加载失败</div>
    </Modal>

    <!-- 驳回理由：用应用内弹窗收集，不用浏览器原生 prompt（仓库硬规则） -->
    <Modal v-model="showReject" title="驳回变更申请">
      <div class="space-y-3">
        <p class="text-xs leading-5 text-muted-foreground">
          驳回后正式资料保持不变，租户本次上传的证件照会被删除。可以写一句原因让租户知道怎么改。
        </p>
        <div>
          <label class="block text-xs font-semibold text-muted-foreground mb-1.5">驳回理由（选填，最多 100 字）</label>
          <textarea v-model="rejectNote" class="input-field" rows="3" maxlength="100" placeholder="如：证件照不清晰，请重新上传" />
        </div>
      </div>
      <div class="flex items-center gap-3 pt-4">
        <button class="btn-ghost flex-1" @click="showReject = false">取消</button>
        <button
          class="flex-1 rounded-xl px-4 py-2.5 text-sm font-semibold text-white bg-rose-500 hover:bg-rose-600 transition-colors disabled:opacity-60"
          :disabled="busyId !== null"
          @click="doReject"
        >确定驳回</button>
      </div>
    </Modal>
  </Modal>
</template>

<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import Modal from './Modal.vue'
import {
  getChangeRequests,
  approveChangeRequest,
  rejectChangeRequest,
  fetchChangeRequestPhotoBlob,
  CHANGE_STATUS_LABEL,
  changeStatusClass,
  type ChangeRequestReview,
} from '../api/tenant'
import { toast } from '../utils/toast'
import { serverErrorMessage } from '../utils/apiError'

const props = defineProps<{ modelValue: boolean }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 审核通过/驳回后通知父组件刷新待审红点。 */
  changed: []
}>()

const loading = ref(false)
const requests = ref<ChangeRequestReview[]>([])
const busyId = ref<number | null>(null)

const showPhoto = ref(false)
const photoUrl = ref('')
const photoIndex = ref(0)
const photoLoading = ref(false)
// 缩略图缓存：键 `申请id-序号` → objectURL。审核面板一次最多列 200 条申请，
// 懒加载能避免打开面板就并发拉几百张证件照。
const thumbs = ref<Record<string, string>>({})

const showReject = ref(false)
const rejectNote = ref('')
const rejectTarget = ref<number | null>(null)

watch(() => props.modelValue, (open) => {
  if (open) {
    void load()
  } else {
    releaseThumbs()
    releasePhotoUrl()
  }
})

async function load() {
  loading.value = true
  try {
    const res = await getChangeRequests('pending')
    if (res.data?.code === 0) {
      requests.value = Array.isArray(res.data.data) ? res.data.data : []
      void loadThumbs()
    } else {
      toast(res.data?.message || '加载失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '加载失败'), 'error')
  } finally {
    loading.value = false
  }
}

/** 为每条申请的证件照取缩略图（串行，避免一次打出几十个并发请求）。 */
async function loadThumbs() {
  for (const r of requests.value) {
    for (let i = 0; i < r.photo_urls.length; i++) {
      const key = `${r.id}-${i}`
      if (thumbs.value[key]) continue
      try {
        const blob = await fetchChangeRequestPhotoBlob(r.id, i)
        thumbs.value = { ...thumbs.value, [key]: URL.createObjectURL(blob) }
      } catch {
        // 单张失败不影响其它缩略图；点开预览时还会再试一次并给出提示。
      }
    }
  }
}

function releaseThumbs() {
  for (const url of Object.values(thumbs.value)) URL.revokeObjectURL(url)
  thumbs.value = {}
}

// ownedPhotoUrl 只记录**我们自己创建的**预览地址：命中缩略图缓存时 photoUrl 借用的是
// 缩略图自己的 objectURL，那个地址由 releaseThumbs 负责回收——此前这里无差别 revoke，
// 会把缩略图仍在使用的地址撤掉，再打开面板时缩略图与预览一起裂图。
const ownedPhotoUrl = ref('')

function releasePhotoUrl() {
  if (ownedPhotoUrl.value) {
    URL.revokeObjectURL(ownedPhotoUrl.value)
    ownedPhotoUrl.value = ''
  }
  photoUrl.value = ''
}

onBeforeUnmount(() => {
  releaseThumbs()
  releasePhotoUrl()
})

/** 只列出本次真的改动了的字段（申请留空 = 本次不改，不展示成「改成空」）。 */
function changedRows(r: ChangeRequestReview) {
  const pairs = [
    { label: '手机号', current: r.current_phone, next: r.phone },
    { label: '身份证号', current: r.current_id_card, next: r.id_card },
    { label: '住址', current: r.current_address, next: r.address },
    { label: '紧急联系人', current: r.current_emergency_contact, next: r.emergency_contact },
    { label: '紧急联系电话', current: r.current_emergency_phone, next: r.emergency_phone },
  ]
  return pairs.filter((p) => (p.next || '').trim() !== '')
}

async function openPhoto(requestId: number, index: number) {
  releasePhotoUrl()
  photoIndex.value = index
  showPhoto.value = true
  const cached = thumbs.value[`${requestId}-${index}`]
  if (cached) {
    // 缩略图就是原图（同一个 blob），直接借它的地址显示；**不**登记为自有地址，
    // 免得关预览时把它撤掉
    photoUrl.value = cached
    return
  }
  photoLoading.value = true
  try {
    const blob = await fetchChangeRequestPhotoBlob(requestId, index)
    const url = URL.createObjectURL(blob)
    ownedPhotoUrl.value = url
    photoUrl.value = url
  } catch (e) {
    toast(serverErrorMessage(e, '照片加载失败'), 'error')
  } finally {
    photoLoading.value = false
  }
}

async function approve(r: ChangeRequestReview) {
  busyId.value = r.id
  try {
    const res = await approveChangeRequest(r.id)
    if (res.data?.code === 0) {
      toast('已通过，租户资料已更新')
      requests.value = requests.value.filter((x) => x.id !== r.id)
      emit('changed')
    } else {
      toast(res.data?.message || '通过失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '通过失败'), 'error')
  } finally {
    busyId.value = null
  }
}

function askReject(id: number) {
  rejectTarget.value = id
  rejectNote.value = ''
  showReject.value = true
}

async function doReject() {
  const id = rejectTarget.value
  if (!id) return
  busyId.value = id
  try {
    const res = await rejectChangeRequest(id, rejectNote.value.trim())
    if (res.data?.code === 0) {
      toast('已驳回')
      requests.value = requests.value.filter((x) => x.id !== id)
      showReject.value = false
      emit('changed')
    } else {
      toast(res.data?.message || '驳回失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '驳回失败'), 'error')
  } finally {
    busyId.value = null
  }
}

function fmtTime(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return String(raw).slice(0, 16).replace('T', ' ')
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
</script>
