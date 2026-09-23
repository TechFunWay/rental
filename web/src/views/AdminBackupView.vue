<template>
  <div class="page-container animate-fade-in">
    <!-- 自动备份说明 -->
    <div class="surface rounded-2xl p-5 sm:p-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div class="flex items-start gap-3">
          <div class="w-10 h-10 rounded-xl bg-brand-500/10 text-brand-500 dark:text-brand-300 flex items-center justify-center shrink-0">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4"/></svg>
          </div>
          <div>
            <h3 class="text-sm font-bold text-foreground">数据库备份</h3>
            <p class="text-sm text-muted-foreground mt-1 leading-relaxed">
              备份为 SQLite 一致性快照，存放于服务器数据目录
              <code class="px-1.5 py-0.5 rounded bg-muted text-xs">{{ backupDir || 'backups/' }}</code> 下。
              自动备份默认每天 03:00 执行，保留最近 {{ keepCount }} 份；
              可在 <RouterLink to="/admin/configs" class="text-brand-500 dark:text-brand-300 hover:underline">系统配置</RouterLink> 中调整开关与数量。
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <span class="badge" :class="autoEnabled ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300' : 'bg-rose-100 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300'">
            自动备份{{ autoEnabled ? '已开启' : '已关闭' }}
          </span>
          <button class="btn-brand !px-4 !py-2" :disabled="creating" @click="handleCreate">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/></svg>
            {{ creating ? '备份中…' : '立即备份' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 备份列表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-3 p-5 sm:p-6 border-b border-border">
        <div>
          <h2 class="text-lg font-bold text-foreground">备份文件</h2>
          <p class="text-sm text-muted-foreground">共 {{ items.length }} 份 · 新的在前</p>
        </div>
        <button @click="load" class="btn-ghost !px-4 !py-2">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
          刷新
        </button>
      </div>

      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wider text-muted-foreground bg-muted/60">
              <th class="py-3.5 px-6 font-semibold">文件名</th>
              <th class="py-3.5 px-4 font-semibold">大小</th>
              <th class="py-3.5 px-4 font-semibold">创建时间</th>
              <th class="py-3.5 px-6 font-semibold text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="item in items" :key="item.name" class="hover:bg-muted/50 transition-colors">
              <td class="py-3.5 px-6 font-medium text-foreground">
                <div class="flex items-center gap-2.5">
                  <svg class="w-4 h-4 text-brand-500 dark:text-brand-300 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7v8a4 4 0 008 0V7m-12 0h16M5 3h14a2 2 0 012 2v14a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2z"/></svg>
                  <code class="text-xs">{{ item.name }}</code>
                </div>
              </td>
              <td class="py-3.5 px-4 text-muted-foreground whitespace-nowrap">{{ formatSize(item.size) }}</td>
              <td class="py-3.5 px-4 text-muted-foreground whitespace-nowrap">{{ formatTime(item.created_at) }}</td>
              <td class="py-3.5 px-6 text-right whitespace-nowrap">
                <button @click="handleDownload(item)" class="text-sm font-medium text-brand-600 dark:text-brand-300 hover:underline mr-4">下载</button>
                <button @click="confirmRestore = { show: true, name: item.name }" class="text-sm font-medium text-amber-600 dark:text-amber-300 hover:underline mr-4">恢复</button>
                <button @click="handleDelete(item)" class="text-sm font-medium text-destructive hover:underline">删除</button>
              </td>
            </tr>
            <tr v-if="items.length === 0">
              <td colspan="4" class="py-16 text-center">
                <div class="flex flex-col items-center gap-3 text-muted-foreground">
                  <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4"/></svg>
                  <span class="text-sm">还没有备份，点击右上角「立即备份」创建第一份</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <Toast :message="toastMsg" :type="toastType" />

    <ConfirmDialog
      v-model="confirmDelete.show"
      title="删除备份"
      :message="`确定删除备份 ${confirmDelete.name} 吗？此操作不可恢复。`"
      confirm-type="danger"
      @confirm="doDelete"
    />

    <ConfirmDialog
      v-model="confirmRestore.show"
      title="恢复备份"
      :message="`用 ${confirmRestore.name} 覆盖当前全部数据？恢复前会自动做一次安全备份；恢复后应用将自动重启（约半分钟），期间请勿操作。`"
      confirm-type="danger"
      confirm-text="确认恢复"
      @confirm="doRestore"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, nextTick, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Toast from '../components/Toast.vue'
import { listBackups, createBackup, deleteBackup, downloadBackup, restoreBackup, type BackupItem } from '../api/system'

const items = ref<BackupItem[]>([])
const backupDir = ref('')
const autoEnabled = ref(false)
const keepCount = ref(7)
const creating = ref(false)

const toastMsg = ref('')
const toastType = ref<'success' | 'error'>('success')
async function toast(message: string, type: 'success' | 'error' = 'success') {
  toastType.value = type
  toastMsg.value = ''
  await nextTick()
  toastMsg.value = message
}

onMounted(load)

async function load() {
  try {
    const res = await listBackups()
    if (res.data?.code === 0 && res.data.data) {
      items.value = res.data.data.items || []
      backupDir.value = res.data.data.dir || ''
      autoEnabled.value = !!res.data.data.auto_enabled
      keepCount.value = res.data.data.keep_count || 7
    }
  } catch {}
}

async function handleCreate() {
  creating.value = true
  try {
    const res = await createBackup()
    if (res.data?.code === 0) {
      await toast('备份创建成功', 'success')
      await load()
    } else {
      await toast(res.data?.message || '备份失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '备份失败', 'error')
  } finally {
    creating.value = false
  }
}

async function handleDownload(item: BackupItem) {
  try {
    const res = await downloadBackup(item.name)
    const url = window.URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    a.download = item.name
    a.click()
    window.URL.revokeObjectURL(url)
  } catch {
    await toast('下载失败，请重试', 'error')
  }
}

const confirmDelete = ref({ show: false, name: '' })

const confirmRestore = ref({ show: false, name: '' })
const restoring = ref(false)

async function doRestore() {
  const name = confirmRestore.value.name
  restoring.value = true
  try {
    const res = await restoreBackup(name)
    if (res.data?.code === 0) {
      // 服务端完成换库后进程会退出等待自动重启；这里给出明确指引后页面随即失联
      await toast(`已开始恢复，应用正在重启，请约半分钟后刷新页面`, 'success')
    } else {
      await toast(res.data?.message || '恢复失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '恢复失败', 'error')
  } finally {
    restoring.value = false
  }
}

function handleDelete(item: BackupItem) {
  confirmDelete.value = { show: true, name: item.name }
}

async function doDelete() {
  const name = confirmDelete.value.name
  try {
    const res = await deleteBackup(name)
    if (res.data?.code === 0) {
      await toast('备份已删除', 'success')
      await load()
    } else {
      await toast(res.data?.message || '删除失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '删除失败', 'error')
  }
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}

function formatTime(value: string): string {
  const d = new Date(value)
  return isNaN(d.getTime()) ? value : d.toLocaleString('zh-CN', { hour12: false })
}
</script>
