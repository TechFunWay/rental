<template>
  <div class="page-container">
    <PageHeader title="我的资料" description="这里是你登记的信息，如需修改请提交申请由房东审核">
      <template #actions>
        <button
          class="btn-brand !py-2"
          :disabled="!profile || profile.pending_change"
          :title="profile?.pending_change ? '已有一条待审核的申请' : ''"
          @click="openChange"
        >
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.172-8.172z" />
          </svg>
          申请修改
        </button>
      </template>
    </PageHeader>

    <!-- 加载中 / 加载失败 -->
    <div v-if="loading" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="loadError" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">
      {{ loadError }}
    </div>

    <template v-else-if="profile">
      <!-- 变更申请状态条：待审 / 被驳回时给出明确说明与撤回入口 -->
      <div
        v-if="activeRequest"
        class="rounded-2xl border px-3.5 py-3 text-xs leading-5"
        :class="activeRequest.status === 'pending'
          ? 'border-amber-400/40 bg-amber-400/10 text-amber-700 dark:text-amber-200'
          : 'border-rose-400/40 bg-rose-400/10 text-rose-700 dark:text-rose-200'"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="font-semibold">
              {{ activeRequest.status === 'pending' ? '变更申请等待房东审核' : '变更申请被驳回' }}
            </div>
            <div class="mt-0.5">
              <template v-if="activeRequest.status === 'pending'">
                提交于 {{ fmtTime(activeRequest.created_at) }}；审核通过后新信息才会生效。
              </template>
              <template v-else>
                {{ activeRequest.review_note || '房东未填写理由' }}；如需再次修改请重新提交。
              </template>
            </div>
          </div>
          <button
            v-if="activeRequest.status === 'pending'"
            class="btn-ghost !py-1.5 !px-2.5 text-[11px] shrink-0"
            :disabled="withdrawing"
            @click="askWithdraw"
          >
            撤回
          </button>
        </div>
      </div>

      <!-- 我的资料（手机端卡片，桌面端两列） -->
      <div class="surface rounded-2xl p-3 sm:p-6">
        <div class="flex items-center gap-3 pb-3 mb-3 border-b border-border">
          <div class="w-11 h-11 rounded-full bg-brand-gradient flex items-center justify-center text-white font-bold shrink-0">
            {{ (profile.name || '?').slice(0, 1) }}
          </div>
          <div class="min-w-0 flex-1">
            <div class="text-sm sm:text-base font-bold text-foreground truncate">{{ profile.name }}</div>
            <div class="text-xs text-muted-foreground truncate">
              {{ profile.room_label || '未关联房源' }}
            </div>
          </div>
          <span
            class="badge shrink-0"
            :class="profile.active
              ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
              : 'bg-muted text-muted-foreground'"
          >{{ profile.active ? '在租' : '已退租' }}</span>
        </div>

        <dl class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2.5">
          <div v-for="row in infoRows" :key="row.label" class="flex items-start justify-between gap-3 text-xs sm:text-sm">
            <dt class="text-muted-foreground shrink-0">{{ row.label }}</dt>
            <dd class="text-foreground text-right min-w-0 break-all">{{ row.value || '—' }}</dd>
          </div>
        </dl>

        <p class="mt-3 pt-3 border-t border-border text-[11px] leading-5 text-muted-foreground">
          证件号已脱敏显示。姓名、房间、租金、租期与押金由房东维护；手机号、证件号、住址与紧急联系人可提交申请修改。
        </p>
      </div>

      <!-- 我的资料附件 -->
      <div class="surface rounded-2xl overflow-hidden">
        <div class="px-4 py-3 sm:px-6 sm:py-4 border-b border-border flex items-center justify-between gap-3">
          <h2 class="text-sm font-semibold text-foreground">我的证件与资料</h2>
          <span class="text-[11px] text-muted-foreground">{{ attachments.length }} 项</span>
        </div>
        <div v-if="attachments.length === 0" class="px-4 py-6 text-center text-xs text-muted-foreground">
          暂无附件
        </div>
        <div v-else class="p-2 space-y-1.5">
          <div
            v-for="a in attachments"
            :key="a.id"
            class="flex items-center gap-3 rounded-xl border border-border bg-surface/80 px-3 py-2.5"
          >
            <span
              class="w-8 h-8 rounded-lg flex items-center justify-center shrink-0 text-[10px] font-bold"
              :class="a.mime_type === 'application/pdf'
                ? 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
                : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            >{{ a.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</span>
            <div class="min-w-0 flex-1">
              <div class="text-xs font-medium text-foreground truncate">{{ a.file_name }}</div>
              <div class="text-[11px] text-muted-foreground truncate">
                {{ a.label || '资料' }} · {{ fmtSize(a.file_size) }}
              </div>
            </div>
            <button
              class="text-xs font-semibold text-brand-600 dark:text-brand-300 hover:underline shrink-0 px-1.5 py-1"
              @click="previewAttachment(a)"
            >查看</button>
          </div>
        </div>
      </div>
    </template>

    <ProfileChangeModal
      v-model="showChange"
      :initial="profile ? {
        phone: profile.phone,
        id_card: profile.id_card,
        address: profile.address,
        emergency_contact: profile.emergency_contact,
        emergency_phone: profile.emergency_phone,
      } : undefined"
      @submitted="load"
    />

    <!-- 附件预览：图片直接显示，PDF 用 iframe（浏览器内置查看器） -->
    <Modal v-model="showAttachment" :title="attachmentPreview?.file_name || '查看资料'" wide>
      <div v-if="attachmentLoading" class="py-10 text-center text-sm text-muted-foreground">加载中…</div>
      <template v-else-if="attachmentBlobUrl">
        <img
          v-if="attachmentPreview?.mime_type !== 'application/pdf'"
          :src="attachmentBlobUrl"
          :alt="attachmentPreview?.file_name"
          class="w-full rounded-xl border border-border"
        />
        <iframe
          v-else
          :src="attachmentBlobUrl"
          class="w-full h-[70vh] rounded-xl border border-border"
          title="资料预览"
        ></iframe>
      </template>
      <div v-else class="py-10 text-center text-sm text-muted-foreground">预览失败</div>
      <div class="flex items-center justify-end gap-2 pt-3">
        <button class="btn-brand !py-2" @click="showAttachment = false">关闭</button>
      </div>
    </Modal>

    <ConfirmDialog
      v-model="showWithdrawConfirm"
      title="撤回变更申请"
      message="撤回后本次上传的证件照会被删除，需要重新提交。确定撤回吗？"
      confirm-text="确定撤回"
      @confirm="doWithdraw"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import PageHeader from '../../components/PageHeader.vue'
import ConfirmDialog from '../../components/ConfirmDialog.vue'
import Modal from '../../components/Modal.vue'
import ProfileChangeModal from '../../components/portal/ProfileChangeModal.vue'
import {
  getPortalProfile,
  getMyChangeRequests,
  withdrawChangeRequest,
  fetchPortalAttachmentBlob,
  type PortalAttachment,
  type PortalProfile,
  type ChangeRequest,
} from '../../api/tenant'
import { toast } from '../../utils/toast'
import { serverErrorMessage } from '../../utils/apiError'

const loading = ref(true)
const loadError = ref('')
const profile = ref<PortalProfile | null>(null)
const attachments = ref<PortalAttachment[]>([])
const requests = ref<ChangeRequest[]>([])
const showChange = ref(false)
const showWithdrawConfirm = ref(false)
const withdrawing = ref(false)

// 附件预览：文件读取要带登录态，<img>/<iframe> 直连会 401，先取 blob 再转
// objectURL；换一张/关闭时 revoke 旧的，避免内存泄漏。
const showAttachment = ref(false)
const attachmentPreview = ref<PortalAttachment | null>(null)
const attachmentBlobUrl = ref('')
const attachmentLoading = ref(false)

// 「当前需要租户关注的申请」：优先待审，否则最近一条被驳回的。
// 已通过的申请只作历史，不必在首页持续提示。
const activeRequest = computed<ChangeRequest | null>(() => {
  const pending = requests.value.find((r) => r.status === 'pending')
  if (pending) return pending
  return requests.value.find((r) => r.status === 'rejected') ?? null
})

const infoRows = computed(() => {
  const p = profile.value
  if (!p) return []
  return [
    { label: '手机号', value: p.phone },
    { label: '身份证号', value: p.id_card },
    { label: '身份证住址', value: p.address },
    { label: '紧急联系人', value: p.emergency_contact },
    { label: '紧急联系电话', value: p.emergency_phone },
    { label: '房源', value: p.room_label },
    { label: '入住日期', value: p.move_in_date },
    { label: '租约到期', value: p.lease_end_date },
    { label: '退租日期', value: p.move_out_date },
    { label: '押金', value: p.deposit ? `${fmtMoney(p.deposit)} 元` : '' },
    { label: '计费方式', value: p.billing_summary },
  ]
})

onMounted(load)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const [profileRes, reqRes] = await Promise.all([getPortalProfile(), getMyChangeRequests()])
    if (profileRes.data?.code === 0) {
      profile.value = profileRes.data.data?.profile ?? null
      attachments.value = profileRes.data.data?.attachments ?? []
    } else {
      loadError.value = profileRes.data?.message || '加载失败'
    }
    if (reqRes.data?.code === 0) {
      requests.value = Array.isArray(reqRes.data.data) ? reqRes.data.data : []
    }
  } catch (e) {
    loadError.value = serverErrorMessage(e, '加载失败，请稍后再试')
  } finally {
    loading.value = false
  }
}

function openChange() {
  showChange.value = true
}

function askWithdraw() {
  showWithdrawConfirm.value = true
}

async function doWithdraw() {
  const req = activeRequest.value
  if (!req) return
  withdrawing.value = true
  try {
    const res = await withdrawChangeRequest(req.id)
    if (res.data?.code === 0) {
      toast('已撤回')
      await load()
    } else {
      toast(res.data?.message || '撤回失败', 'error')
    }
  } catch (e) {
    toast(serverErrorMessage(e, '撤回失败，请稍后再试'), 'error')
  } finally {
    withdrawing.value = false
  }
}

async function previewAttachment(a: PortalAttachment) {
  releaseAttachmentUrl()
  attachmentPreview.value = a
  showAttachment.value = true
  attachmentLoading.value = true
  try {
    const blob = await fetchPortalAttachmentBlob(a.id)
    attachmentBlobUrl.value = URL.createObjectURL(blob)
  } catch (e) {
    toast(serverErrorMessage(e, '预览失败，请稍后重试'), 'error')
  } finally {
    attachmentLoading.value = false
  }
}

function releaseAttachmentUrl() {
  if (attachmentBlobUrl.value) {
    URL.revokeObjectURL(attachmentBlobUrl.value)
    attachmentBlobUrl.value = ''
  }
}

// 关闭弹窗即释放 objectURL（图片/PDF 可能几 MB，攒着会吃内存）。
watch(showAttachment, (open) => {
  if (!open) releaseAttachmentUrl()
})
onBeforeUnmount(releaseAttachmentUrl)

function fmtMoney(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function fmtSize(bytes: number): string {
  const n = Number(bytes || 0)
  if (n <= 0) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function fmtTime(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return raw.slice(0, 16).replace('T', ' ')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}
</script>
