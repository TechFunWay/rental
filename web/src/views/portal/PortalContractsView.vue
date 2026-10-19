<template>
  <div class="page-container">
    <PageHeader title="我的合同" description="房东上传的租赁合同，可在线预览或下载留存" />

    <div v-if="loading" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">加载中…</div>
    <div v-else-if="loadError" class="surface rounded-2xl p-4 sm:p-6 text-center text-sm text-muted-foreground">{{ loadError }}</div>

    <div v-else-if="contracts.length === 0" class="surface rounded-2xl p-6 text-center text-sm text-muted-foreground">
      房东还没有上传合同。如需查看，请联系房东上传。
    </div>

    <template v-else>
      <!-- 手机卡片列表（容器 p-2 space-y-1.5，条目独立卡片） -->
      <div class="md:hidden surface rounded-2xl p-2 space-y-1.5">
        <button
          v-for="c in contracts"
          :key="c.id"
          class="w-full text-left rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5"
          @click="preview(c)"
        >
          <div class="flex items-center gap-2.5">
            <span
              class="w-8 h-8 rounded-lg flex items-center justify-center shrink-0 text-[10px] font-bold"
              :class="c.mime_type === 'application/pdf'
                ? 'bg-rose-500/10 text-rose-600 dark:text-rose-300'
                : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            >{{ c.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</span>
            <span class="text-xs font-semibold text-foreground truncate flex-1 min-w-0">{{ c.file_name }}</span>
          </div>
          <div class="flex items-center justify-between gap-3 text-[11px] text-muted-foreground">
            <span>{{ fmtSize(c.file_size) }}</span>
            <span class="tabular-nums">{{ fmtDate(c.created_at) }}</span>
          </div>
        </button>
      </div>

      <!-- 桌面表格 -->
      <div class="surface rounded-2xl overflow-hidden hidden md:block">
        <div class="overflow-x-auto">
          <table class="w-full text-sm">
            <thead class="bg-muted/50">
              <tr class="text-left text-xs text-muted-foreground">
                <th class="py-3 px-4 font-semibold">文件</th>
                <th class="py-3 px-4 font-semibold">类型</th>
                <th class="py-3 px-4 font-semibold">大小</th>
                <th class="py-3 px-4 font-semibold">上传时间</th>
                <th class="py-3 px-4 font-semibold text-right">操作</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="c in contracts" :key="c.id" class="hover:bg-muted/40 transition-colors">
                <td class="py-3 px-4 text-foreground max-w-[320px] truncate">{{ c.file_name }}</td>
                <td class="py-3 px-4 text-muted-foreground">{{ c.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</td>
                <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ fmtSize(c.file_size) }}</td>
                <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ fmtDate(c.created_at) }}</td>
                <td class="py-3 px-4 text-right whitespace-nowrap">
                  <button class="text-brand-600 dark:text-brand-300 font-semibold text-xs hover:underline mr-3" @click="preview(c)">预览</button>
                  <button
                    class="text-muted-foreground font-semibold text-xs hover:underline"
                    @click="download(c)"
                  >下载</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <!-- 合同预览：图片直接显示，PDF 用 iframe（浏览器内置查看器） -->
    <Modal v-model="showPreview" :title="previewing?.file_name || '合同预览'" wide>
      <div v-if="previewing" class="space-y-3">
        <div v-if="previewLoading" class="py-10 text-center text-sm text-muted-foreground">加载中…</div>
        <template v-else-if="previewBlobUrl">
          <img
            v-if="previewing.mime_type !== 'application/pdf'"
            :src="previewBlobUrl"
            :alt="previewing.file_name"
            class="w-full rounded-xl border border-border"
          />
          <iframe
            v-else
            :src="previewBlobUrl"
            class="w-full h-[70vh] rounded-xl border border-border"
            title="合同预览"
          ></iframe>
        </template>
        <div v-else class="py-10 text-center text-sm text-muted-foreground">预览失败</div>
        <div class="flex items-center justify-end gap-2">
          <button class="btn-ghost !py-2" @click="download(previewing)">下载</button>
          <button class="btn-brand !py-2" @click="showPreview = false">关闭</button>
        </div>
      </div>
    </Modal>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import PageHeader from '../../components/PageHeader.vue'
import Modal from '../../components/Modal.vue'
import {
  getPortalContracts,
  fetchPortalContractBlob,
  downloadPortalContract,
  type PortalContract,
} from '../../api/tenant'
import { serverErrorMessage } from '../../utils/apiError'
import { toast } from '../../utils/toast'

const loading = ref(true)
const loadError = ref('')
const contracts = ref<PortalContract[]>([])
const showPreview = ref(false)
const previewing = ref<PortalContract | null>(null)
const previewLoading = ref(false)
// 预览用 objectURL：合同文件的读取要带登录态，<img>/<iframe> 直连会 401，
// 所以先取 blob 再转 objectURL；关闭时 revoke，避免内存泄漏。
const previewBlobUrl = ref('')

onMounted(load)

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const res = await getPortalContracts()
    if (res.data?.code === 0) {
      contracts.value = Array.isArray(res.data.data) ? res.data.data : []
    } else {
      loadError.value = res.data?.message || '加载失败'
    }
  } catch (e) {
    loadError.value = serverErrorMessage(e, '加载失败，请稍后再试')
  } finally {
    loading.value = false
  }
}

async function preview(c: PortalContract) {
  releasePreviewUrl()
  previewing.value = c
  showPreview.value = true
  previewLoading.value = true
  try {
    const blob = await fetchPortalContractBlob(c.id)
    previewBlobUrl.value = URL.createObjectURL(blob)
  } catch (e) {
    toast(serverErrorMessage(e, '预览失败，请稍后重试'), 'error')
  } finally {
    previewLoading.value = false
  }
}

async function download(c: PortalContract) {
  try {
    await downloadPortalContract(c.id, c.file_name)
  } catch (e) {
    toast(serverErrorMessage(e, '下载失败，请稍后重试'), 'error')
  }
}

function releasePreviewUrl() {
  if (previewBlobUrl.value) {
    URL.revokeObjectURL(previewBlobUrl.value)
    previewBlobUrl.value = ''
  }
}

onBeforeUnmount(releasePreviewUrl)

function fmtSize(bytes: number): string {
  const n = Number(bytes || 0)
  if (n <= 0) return '—'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(2)} MB`
}

function fmtDate(raw: string): string {
  if (!raw) return '—'
  const d = new Date(raw)
  if (Number.isNaN(d.getTime())) return String(raw).slice(0, 10)
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}
</script>
