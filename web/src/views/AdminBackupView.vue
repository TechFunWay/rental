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
              换机或异地容灾时，可用「上传备份」把下载到本机的 .db 传回来。
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
          <!-- 上传备份：换机 / 异地容灾时把下载到本机的 .db 传回来。
               严格两步——上传只入库不恢复，恢复仍点列表行的「恢复」。 -->
          <button
            class="btn-ghost !px-4 !py-2"
            :disabled="uploading"
            @click="fileInput?.click()"
            title="从本机上传备份文件（.db），上传后在列表点击「恢复」"
          >
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 16v2a2 2 0 002 2h12a2 2 0 002-2v-2M12 4v12m0-12l-4 4m4-4l4 4"/></svg>
            {{ uploading ? '上传中…' : '上传备份' }}
          </button>
          <input
            ref="fileInput"
            type="file"
            accept=".db,application/octet-stream"
            class="hidden"
            @change="handleUploadFile"
          >
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
                  <span class="text-sm">还没有备份：点击右上角「立即备份」创建一份，或用「上传备份」把本机已有的备份传上来</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- ============ 定时导出存档 ============
         与上面的「数据库备份」是两套完全独立的东西：
         备份是服务器内的快照（防误操作、可回滚，定时点在服务器配置里）；
         导出存档是**给用户下载到本机/网盘留存**的，定时点与保留数在这里配，
         存放在数据目录的 exports/ 下，两者目录与保留数互不影响。 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div class="flex flex-wrap items-start justify-between gap-3 p-4 sm:p-6 border-b border-border">
        <div class="flex items-start gap-3 min-w-0">
          <div class="w-10 h-10 rounded-xl bg-brand-500/10 text-brand-500 dark:text-brand-300 flex items-center justify-center shrink-0">
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
          </div>
          <div class="min-w-0">
            <h2 class="text-base font-bold text-foreground">定时导出存档</h2>
            <p class="text-xs sm:text-sm text-muted-foreground mt-1 leading-relaxed">
              导出存档是给你<strong class="text-foreground">下载到本机/网盘留存</strong>的，升级或换机时可以上传回来恢复。
              它和上面的服务器备份各存各的目录、各按各的保留数清理，<strong class="text-foreground">备份的清理不会删掉这里的存档</strong>。
            </p>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <span class="badge" :class="exportEnabled ? 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300' : 'bg-rose-100 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300'">
            定时导出{{ exportEnabled ? '已开启' : '已关闭' }}
          </span>
          <button class="btn-brand !px-4 !py-2" :disabled="exporting" @click="handleExportNow">
            <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4v16m8-8H4"/></svg>
            {{ exporting ? '导出中…' : '立即导出' }}
          </button>
        </div>
      </div>

      <!-- 配置区：开关 / 间隔 / 保留数。保存走系统配置接口，与系统配置页同一套
           语义（改的是 rental_export_* 三个系统级配置项）。 -->
      <div class="p-4 sm:p-6 border-b border-border space-y-3">
        <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
          <div class="flex-1 min-w-0">
            <div class="text-sm font-semibold text-foreground">定时导出开关</div>
            <div class="text-xs text-muted-foreground mt-0.5">关闭后不再自动导出，仍可手动「立即导出」</div>
          </div>
          <button
            type="button"
            role="switch"
            :aria-checked="exportEnabled"
            aria-label="定时导出开关"
            :disabled="savingConfig === 'rental_export_enabled'"
            @click="toggleExportEnabled"
            :class="[
              'w-11 h-6 rounded-full transition-colors shrink-0 focus:outline-none focus:ring-4 focus:ring-brand-500/20',
              exportEnabled ? 'bg-brand-500' : 'bg-muted',
              savingConfig === 'rental_export_enabled' ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer',
            ]"
          >
            <span :class="[
              'inline-block w-5 h-5 rounded-full bg-white shadow transform transition-transform mt-0.5',
              exportEnabled ? 'translate-x-5' : 'translate-x-0.5',
            ]"></span>
          </button>
        </div>

        <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
          <div class="flex-1 min-w-0">
            <div class="text-sm font-semibold text-foreground">导出间隔（小时）</div>
            <div class="text-xs text-muted-foreground mt-0.5">1–720，默认 24。系统每小时检查一次是否到点</div>
          </div>
          <div class="flex items-center gap-2 sm:w-64 shrink-0">
            <input
              v-model="exportIntervalInput"
              type="number"
              min="1"
              max="720"
              step="1"
              :disabled="savingConfig === 'rental_export_interval_hours'"
              class="input-field !py-2 flex-1 min-w-0"
              @keyup.enter="saveExportConfig('rental_export_interval_hours')"
            />
            <button
              class="btn-brand !px-4 !py-2 whitespace-nowrap"
              :disabled="savingConfig === 'rental_export_interval_hours' || !isIntervalDirty"
              @click="saveExportConfig('rental_export_interval_hours')"
            >
              {{ savingConfig === 'rental_export_interval_hours' ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>

        <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:gap-3">
          <div class="flex-1 min-w-0">
            <div class="text-sm font-semibold text-foreground">存档保留数量（份）</div>
            <div class="text-xs text-muted-foreground mt-0.5">1–500，默认 30。超出后自动清理最旧的，不影响服务器内的备份</div>
          </div>
          <div class="flex items-center gap-2 sm:w-64 shrink-0">
            <input
              v-model="exportKeepInput"
              type="number"
              min="1"
              max="500"
              step="1"
              :disabled="savingConfig === 'rental_export_keep_count'"
              class="input-field !py-2 flex-1 min-w-0"
              @keyup.enter="saveExportConfig('rental_export_keep_count')"
            />
            <button
              class="btn-brand !px-4 !py-2 whitespace-nowrap"
              :disabled="savingConfig === 'rental_export_keep_count' || !isKeepDirty"
              @click="saveExportConfig('rental_export_keep_count')"
            >
              {{ savingConfig === 'rental_export_keep_count' ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </div>

      <!-- 存档列表 -->
      <div class="flex flex-wrap items-center justify-between gap-3 p-4 sm:p-6 border-b border-border">
        <div class="min-w-0">
          <h3 class="text-sm font-bold text-foreground">导出存档</h3>
          <p class="text-xs sm:text-sm text-muted-foreground mt-0.5">
            共 {{ exportItems.length }} 份 · 新的在前 · 应用只保留最近 {{ exportKeepCount }} 份
          </p>
        </div>
        <button @click="loadExports" class="btn-ghost !px-4 !py-2">
          <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"/></svg>
          刷新
        </button>
      </div>

      <!-- 桌面：表格 -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wider text-muted-foreground bg-muted/60">
              <th class="py-3.5 px-6 font-semibold">文件名</th>
              <th class="py-3.5 px-4 font-semibold">大小</th>
              <th class="py-3.5 px-4 font-semibold">导出时间</th>
              <th class="py-3.5 px-6 font-semibold text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="item in exportItems" :key="item.name" class="hover:bg-muted/50 transition-colors">
              <td class="py-3.5 px-6 font-medium text-foreground">
                <div class="flex items-center gap-2.5">
                  <svg class="w-4 h-4 text-brand-500 dark:text-brand-300 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7v8a4 4 0 008 0V7m-12 0h16M5 3h14a2 2 0 012 2v14a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2z"/></svg>
                  <code class="text-xs">{{ item.name }}</code>
                </div>
              </td>
              <td class="py-3.5 px-4 text-muted-foreground whitespace-nowrap">{{ formatSize(item.size) }}</td>
              <td class="py-3.5 px-4 text-muted-foreground whitespace-nowrap">{{ formatTime(item.created_at) }}</td>
              <td class="py-3.5 px-6 text-right whitespace-nowrap">
                <button @click="handleExportDownload(item)" class="text-sm font-medium text-brand-600 dark:text-brand-300 hover:underline mr-4">下载</button>
                <button @click="confirmExportDelete = { show: true, name: item.name }" class="text-sm font-medium text-destructive hover:underline">删除</button>
              </td>
            </tr>
            <tr v-if="exportItems.length === 0">
              <td colspan="4" class="py-12 text-center">
                <div class="flex flex-col items-center gap-3 text-muted-foreground">
                  <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
                  <span class="text-sm">还没有导出存档，点右上角「立即导出」生成第一份</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片化（条目独立成卡，容器 p-2 + space-y-1.5，操作内联可见） -->
      <div class="md:hidden p-2 space-y-1.5">
        <div
          v-for="item in exportItems"
          :key="item.name"
          class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5"
        >
          <div class="flex items-center gap-2">
            <svg class="w-4 h-4 text-brand-500 dark:text-brand-300 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 7v8a4 4 0 008 0V7m-12 0h16M5 3h14a2 2 0 012 2v14a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2z"/></svg>
            <code class="text-xs text-foreground font-medium min-w-0 flex-1 truncate">{{ item.name }}</code>
          </div>
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs text-muted-foreground whitespace-nowrap">
              {{ formatTime(item.created_at) }} · {{ formatSize(item.size) }}
            </span>
            <span class="flex items-center gap-3 shrink-0">
              <button @click="handleExportDownload(item)" class="text-xs font-medium text-brand-600 dark:text-brand-300 min-h-10 px-1">下载</button>
              <button @click="confirmExportDelete = { show: true, name: item.name }" class="text-xs font-medium text-destructive min-h-10 px-1">删除</button>
            </span>
          </div>
        </div>
        <div v-if="exportItems.length === 0" class="py-10 text-center">
          <div class="flex flex-col items-center gap-3 text-muted-foreground">
            <svg class="w-10 h-10 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M12 10v6m0 0l-3-3m3 3l3-3m2 8H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>
            <span class="text-sm">还没有导出存档，点上方「立即导出」生成第一份</span>
          </div>
        </div>
      </div>

      <div class="px-4 sm:px-6 py-3 border-t border-border">
        <p class="text-xs text-muted-foreground leading-relaxed">
          下载后请自行保存到<strong class="text-foreground">本机或网盘</strong>——应用只保留最近 {{ exportKeepCount }} 份，超出后会自动清理最旧的。攒下的存档可在换机时用上方的「上传备份」传回来恢复。
        </p>
      </div>
    </div>

    <Toast :message="toastMsg" :type="toastType" />

    <ConfirmDialog
      v-model="confirmExportDelete.show"
      title="删除导出存档"
      :message="`确定删除导出存档 ${confirmExportDelete.name} 吗？此操作不可恢复。`"
      confirm-type="danger"
      @confirm="doExportDelete"
    />

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
import { ref, computed, nextTick, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Toast from '../components/Toast.vue'
import request from '../api/request'
import { listBackups, createBackup, deleteBackup, downloadBackup, restoreBackup, type BackupItem } from '../api/system'
import { updateConfig } from '../api/config'

const items = ref<BackupItem[]>([])
const backupDir = ref('')
const autoEnabled = ref(false)
const keepCount = ref(7)
const creating = ref(false)
const uploading = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)

// ---------- 定时导出存档 ----------

const exportItems = ref<ExportArchiveItem[]>([])
const exportEnabled = ref(true)
const exportIntervalHours = ref(24)
const exportKeepCount = ref(30)
const exporting = ref(false)
// 配置输入框的本地草稿：数字输入框允许用户先敲再保存，未保存时按钮保持禁用。
const exportIntervalInput = ref('24')
const exportKeepInput = ref('30')
// 正在保存的配置项 key（空串=无）。用它禁用对应的输入与按钮，并显示「保存中…」。
const savingConfig = ref('')

const toastMsg = ref('')
const toastType = ref<'success' | 'error'>('success')
async function toast(message: string, type: 'success' | 'error' = 'success') {
  toastType.value = type
  toastMsg.value = ''
  await nextTick()
  toastMsg.value = message
}

onMounted(() => {
  load()
  loadExports()
})

// ---------------------------------------------------------------------------
// 定时导出存档
//
// 接口挂在 adminGroup（/api/rental/exports），与框架备份 /api/backups 同一层
// 鉴权。因为 api/rental.ts 由他人统一维护，这里就地用共用的 axios 实例调用，
// 待 api/rental.ts 补上 listExports / createExport / deleteExport /
// downloadExport 后可直接替换（签名见交付说明）。
// ---------------------------------------------------------------------------

interface ExportArchiveItem {
  name: string
  size: number
  created_at: string
}

interface ExportListPayload {
  items: ExportArchiveItem[]
  dir: string
  enabled: boolean
  interval_hours: number
  keep_count: number
}

// 导出配置的实际取值由 /api/rental/exports 一并回带（见 loadExports），
// 写入统一走系统配置接口 PUT /api/configs，与系统配置页同一套语义。

async function loadExports() {
  try {
    const res = await request.get<{ code: number; message: string; data: ExportListPayload }>(
      '/api/rental/exports',
    )
    if (res.data?.code === 0 && res.data.data) {
      const d = res.data.data
      exportItems.value = d.items || []
      exportEnabled.value = !!d.enabled
      exportIntervalHours.value = d.interval_hours
      exportKeepCount.value = d.keep_count
      exportIntervalInput.value = String(d.interval_hours)
      exportKeepInput.value = String(d.keep_count)
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '读取导出存档失败', 'error')
  }
}

const isIntervalDirty = computed(
  () => exportIntervalInput.value !== String(exportIntervalHours.value),
)
const isKeepDirty = computed(() => exportKeepInput.value !== String(exportKeepCount.value))

// 保存导出配置。数字输入框的值先按 min/max 夹一次再提交——后端也会夹，
// 这里先夹是为了让用户立刻看到自己填的生效值，而不是提交完又变回去。
async function saveExportConfig(key: 'rental_export_interval_hours' | 'rental_export_keep_count') {
  const isInterval = key === 'rental_export_interval_hours'
  const max = isInterval ? 720 : 500
  const parsed = Number(isInterval ? exportIntervalInput.value : exportKeepInput.value)
  if (!Number.isFinite(parsed)) {
    await toast('请填写一个整数', 'error')
    return
  }
  const n = Math.min(Math.max(Math.trunc(parsed), 1), max)
  savingConfig.value = key
  try {
    const res = await updateConfig(key, String(n))
    if (res.data?.code === 0) {
      if (isInterval) {
        exportIntervalHours.value = n
        exportIntervalInput.value = String(n)
      } else {
        exportKeepCount.value = n
        exportKeepInput.value = String(n)
      }
      await toast('导出设置已保存', 'success')
    } else {
      await toast(res.data?.message || '保存失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '保存失败，请稍后重试', 'error')
  } finally {
    savingConfig.value = ''
  }
}

// 开关是即时生效的（点了就存），不需要额外的保存按钮。
async function toggleExportEnabled() {
  const next = !exportEnabled.value
  savingConfig.value = 'rental_export_enabled'
  try {
    const res = await updateConfig('rental_export_enabled', String(next))
    if (res.data?.code === 0) {
      exportEnabled.value = next
      await toast(next ? '已开启定时导出' : '已关闭定时导出', 'success')
    } else {
      await toast(res.data?.message || '保存失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '保存失败，请稍后重试', 'error')
  } finally {
    savingConfig.value = ''
  }
}

async function handleExportNow() {
  exporting.value = true
  try {
    const res = await request.post<{ code: number; message: string; data: { name: string; removed: number } }>(
      '/api/rental/exports',
    )
    if (res.data?.code === 0) {
      await toast(`已导出 ${res.data.data.name}，记得下载保存到本机或网盘`, 'success')
      await loadExports()
    } else {
      await toast(res.data?.message || '导出失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '导出失败，请稍后重试', 'error')
  } finally {
    exporting.value = false
  }
}

// 下载存档：取回 blob 后用 object URL 触发浏览器保存（同备份下载的既有做法，
// 导出存档需要 Authorization 头，不能直接开新窗口）。
async function handleExportDownload(item: ExportArchiveItem) {
  try {
    const res = await request.get<Blob>(
      `/api/rental/exports/${encodeURIComponent(item.name)}/download`,
      { responseType: 'blob' },
    )
    const url = window.URL.createObjectURL(new Blob([res.data]))
    const a = document.createElement('a')
    a.href = url
    // 服务端在 Content-Disposition 里给出的名字是 rental_<存档名>；
    // 这里用存档名本身落盘，方便用户日后按名字找回来。
    a.download = item.name
    a.click()
    window.URL.revokeObjectURL(url)
    await toast('已下载，请保存到本机或网盘；应用只保留最近 ' + exportKeepCount.value + ' 份', 'success')
  } catch {
    await toast('下载失败，请重试', 'error')
  }
}

const confirmExportDelete = ref({ show: false, name: '' })

async function doExportDelete() {
  const name = confirmExportDelete.value.name
  try {
    const res = await request.delete<{ code: number; message: string; data: { ok: boolean } }>(
      `/api/rental/exports/${encodeURIComponent(name)}`,
    )
    if (res.data?.code === 0) {
      await toast('导出存档已删除', 'success')
      await loadExports()
    } else {
      await toast(res.data?.message || '删除失败', 'error')
    }
  } catch (e: any) {
    await toast(e?.response?.data?.message || '删除失败', 'error')
  }
}

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

// 上传备份走本应用自己的接口（/api/rental/backups/upload，挂在 adminGroup
// 上，见 server/rental/backup_upload.go），因此不复用 api/system.ts 里框架
// backup 包的那组接口，就地用共用的 axios 实例发一次 multipart。
function uploadBackupFile(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post<{ code: number; message: string; data: { name: string } }>(
    '/api/rental/backups/upload',
    form,
    { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 120000 },
  )
}

// 上传本机备份文件：服务端校验（.db 后缀 → SQLite 文件头 → 完整性检查 →
// 必须含 users 表）通过后存为一份备份。
//
// 严格两步：上传不碰当前数据，也不自动恢复；真正恢复仍走列表行的「恢复」
// （带确认弹窗与恢复前快照），让管理员上传后还能核对文件。
// 这里的后缀预检只为省一次无谓的往返，**服务端校验才是权威**——文件名完全
// 由客户端控制，绕过前端很容易。
async function handleUploadFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许连续选同一个文件
  if (!file) return
  if (!file.name.toLowerCase().endsWith('.db')) {
    await toast('只支持 .db 备份文件，请重新选择', 'error')
    return
  }
  if (file.size === 0) {
    await toast('文件是空的，请重新选择', 'error')
    return
  }
  uploading.value = true
  try {
    const res = await uploadBackupFile(file)
    if (res.data?.code === 0) {
      await toast('上传成功，点击该行的「恢复」完成恢复', 'success')
      await load()
    } else {
      await toast(res.data?.message || '上传失败', 'error')
    }
  } catch (e: any) {
    // 服务端的 400 提示已经把原因说清（不是数据库备份 / 缺少 users 表 /
    // 文件损坏），原样透出比前端另编一句更有用。
    await toast(e?.response?.data?.message || '上传失败，请稍后重试', 'error')
  } finally {
    uploading.value = false
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
