<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="系统配置" description="管理全局系统级配置，仅管理员可见" />

    <div v-if="errorMsg" class="surface rounded-2xl px-5 py-3 text-sm text-destructive">
      {{ errorMsg }}
    </div>

    <!-- 版本与更新：常驻入口（另一处在顶栏版本号按钮）。放在配置列表之前，
         管理员进系统设置第一眼就能看到当前版本、手动检查与一键更新。 -->
    <div class="surface rounded-2xl p-4 sm:p-6">
      <div class="flex items-start justify-between gap-3">
        <div class="min-w-0">
          <h3 class="text-base font-bold text-foreground">版本与更新</h3>
          <p class="mt-1 text-xs sm:text-sm text-muted-foreground">
            当前版本 <span class="font-semibold tabular-nums text-foreground">{{ currentVersion || '—' }}</span>
            <template v-if="latestVersion">
              · 最新版本 <span class="font-semibold tabular-nums text-foreground">{{ latestVersion }}</span>
            </template>
          </p>
          <p v-if="supportStore.updateInfo && !supportStore.updateInfo.has_update" class="mt-1 text-xs text-emerald-600 dark:text-emerald-400">✓ 已经是最新版本</p>
          <p v-else-if="supportStore.updateInfo?.has_update" class="mt-1 text-xs text-brand-600 dark:text-brand-300">发现新版本，可一键更新或手动下载</p>
          <p v-if="supportStore.applyMessage" class="mt-1 text-xs text-muted-foreground">{{ supportStore.applyMessage }}</p>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button class="btn-ghost !py-2 !px-3 text-xs min-h-10" :disabled="supportStore.checkingUpdate" @click="supportStore.openUpdateDialog()">
            {{ supportStore.checkingUpdate ? '检查中…' : '检查更新' }}
          </button>
          <button
            v-if="supportStore.updateInfo?.has_update && supportStore.updateInfo?.fpk_assets?.length"
            class="btn-brand !py-2 !px-3 text-xs min-h-10"
            :disabled="supportStore.applying"
            @click="supportStore.applyNewVersion()"
          >{{ supportStore.applyButtonText }}</button>
        </div>
      </div>
    </div>

    <div v-if="loading" class="space-y-3">
      <div v-for="i in 4" :key="i" class="h-20 rounded-xl bg-muted animate-pulse"></div>
    </div>

    <ConfigGroupList
      v-else
      ref="listRef"
      :items="items"
      @save="handleSave"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import PageHeader from '../components/PageHeader.vue'
import ConfigGroupList from '../components/ConfigGroupList.vue'
import { getSystemConfigMeta, updateConfig } from '../api/config'
import type { ConfigMeta } from '../api/config'
import { useSupportStore } from '../stores/support'

const items = ref<ConfigMeta[]>([])
const loading = ref(false)
const errorMsg = ref('')
const listRef = ref<InstanceType<typeof ConfigGroupList> | null>(null)
const supportStore = useSupportStore()

// 当前运行版本：优先用 support store（登录时已拉取 /api/version），
// 它没拿到时退化为「—」，检查更新按钮仍可用。
// 版本号显示统一补 v：current 是程序版本（0.5.4，无前缀），latest 取自发行版
// tag（形如 v0.5.5，自带前缀），两种情况都显示成 v0.5.x，不能出现 vv0.5.5。
function withV(v?: string): string {
  const s = (v || '').trim()
  if (!s) return ''
  return s.startsWith('v') || s.startsWith('V') ? s : `v${s}`
}
const currentVersion = computed(() => withV(supportStore.updateInfo?.current || supportStore.appVersion))
const latestVersion = computed(() => withV(supportStore.updateInfo?.latest))

onMounted(loadMeta)

async function loadMeta() {
  loading.value = true
  errorMsg.value = ''
  try {
    const res = await getSystemConfigMeta()
    if (res.data?.code === 0) {
      items.value = Array.isArray(res.data.data) ? res.data.data : []
    } else {
      errorMsg.value = res.data?.message || '加载配置失败'
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.message || '网络错误'
  } finally {
    loading.value = false
  }
}

async function handleSave(key: string, value: string) {
  errorMsg.value = ''
  try {
    const res = await updateConfig(key, value)
    if (res.data?.code === 0) {
      const idx = items.value.findIndex((it) => it.key === key)
      if (idx >= 0) {
        items.value[idx] = { ...items.value[idx], value }
      }
      listRef.value?.markSaved(key)
    } else {
      errorMsg.value = res.data?.message || '保存失败'
      listRef.value?.markFailed(key)
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.message || '网络错误'
    listRef.value?.markFailed(key)
  }
}
</script>
