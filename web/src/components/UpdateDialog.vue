<template>
  <!-- 版本与更新弹窗：常驻入口（顶栏版本号按钮 / 系统设置「版本与更新」卡片）。
       一键更新只对管理员开放；普通用户看到的是版本信息与发行页链接。 -->
  <Modal :model-value="supportStore.updateDialogVisible" title="版本与更新" @update:model-value="supportStore.closeUpdateDialog()">
    <div class="space-y-4">
      <div class="rounded-xl border border-border bg-muted/40 px-3.5 py-3 space-y-1.5 text-sm">
        <div class="flex items-center justify-between gap-3">
          <span class="text-muted-foreground">当前版本</span>
          <span class="font-semibold tabular-nums text-foreground">{{ currentVersion || '—' }}</span>
        </div>
        <div class="flex items-center justify-between gap-3">
          <span class="text-muted-foreground">最新版本</span>
          <span class="font-semibold tabular-nums text-foreground">
            {{ supportStore.checkingUpdate ? '检查中…' : latestVersion || '—' }}
          </span>
        </div>
      </div>

      <p v-if="supportStore.checkError" class="text-xs text-destructive leading-5">{{ supportStore.checkError }}</p>

      <template v-if="hasUpdate">
        <div class="rounded-xl border border-brand-500/30 bg-brand-500/10 px-3.5 py-2.5 text-sm leading-5 text-foreground">
          🎉 发现新版本 <strong>{{ latestVersion }}</strong>，建议升级以获得最新功能和修复。
        </div>
        <p
          v-if="supportStore.applyMessage"
          class="text-xs leading-5"
          :class="/已更新|重启完成/.test(supportStore.applyMessage)
            ? 'text-emerald-600 dark:text-emerald-400'
            : /失败|超时/.test(supportStore.applyMessage)
              ? 'text-destructive'
              : 'text-muted-foreground'"
        >
          {{ supportStore.applyMessage }}
        </p>
        <div class="flex flex-col gap-2.5">
          <button
            v-if="authStore.isAdmin && supportStore.updateInfo?.fpk_assets?.length"
            type="button"
            class="btn-brand min-h-11 w-full"
            :disabled="supportStore.applying"
            @click="supportStore.applyNewVersion()"
          >
            {{ supportStore.applyButtonText }}
          </button>
          <div class="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs">
            <a
              v-for="fpk in supportStore.updateInfo?.fpk_assets ?? []"
              :key="fpk.url"
              :href="fpk.url"
              target="_blank"
              rel="noopener"
              class="font-medium text-brand-600 dark:text-brand-300 hover:underline"
            >{{ /arm|aarch/i.test(fpk.name) ? 'ARM' : 'x86' }} 安装包直链</a>
            <a v-if="supportStore.updateInfo?.gitee_url" :href="supportStore.updateInfo.gitee_url" target="_blank" rel="noopener" class="font-medium text-brand-600 dark:text-brand-300 hover:underline">Gitee 发行版</a>
            <a v-if="supportStore.updateInfo?.github_url" :href="supportStore.updateInfo.github_url" target="_blank" rel="noopener" class="font-medium text-brand-600 dark:text-brand-300 hover:underline">GitHub 发行版</a>
          </div>
        </div>
        <p v-if="authStore.isAdmin && !supportStore.updateInfo?.fpk_assets?.length" class="text-[11px] leading-5 text-muted-foreground">
          该版本发行页里没有可一键更新的安装包，请从上面的链接手动下载。
        </p>
      </template>

      <div
        v-else-if="!supportStore.checkingUpdate && supportStore.updateInfo"
        class="rounded-xl border border-emerald-500/30 bg-emerald-500/10 px-3.5 py-2.5 text-sm text-emerald-700 dark:text-emerald-300"
      >
        ✓ 已经是最新版本
      </div>

      <div class="flex items-center gap-2 pt-0.5">
        <button type="button" class="btn-ghost min-h-11 flex-1" :disabled="supportStore.checkingUpdate" @click="supportStore.checkUpdateNow()">
          {{ supportStore.checkingUpdate ? '检查中…' : '重新检查' }}
        </button>
        <button type="button" class="btn-brand min-h-11 flex-1" @click="supportStore.closeUpdateDialog()">关闭</button>
      </div>

      <p class="text-[11px] leading-5 text-muted-foreground">
        一键更新（管理员）会依次下载发行包、校验、原子替换程序与前端并原地重启，数据目录不受影响；
        飞牛应用中心安装的版本也可以点上面的直链下载安装包手动升级。
      </p>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import Modal from './Modal.vue'
import { useAuthStore } from '../stores/auth'
import { useSupportStore } from '../stores/support'

const authStore = useAuthStore()
const supportStore = useSupportStore()

const hasUpdate = computed(() => !!supportStore.updateInfo?.has_update)
// 版本号显示统一补 v：current 是程序版本（0.5.4，无前缀），latest 取自发行版
// tag（形如 v0.5.5，自带前缀），两种情况都要显示成 v0.5.x，不能出现 vv0.5.5。
function withV(v?: string): string {
  const s = (v || '').trim()
  if (!s) return ''
  return s.startsWith('v') || s.startsWith('V') ? s : `v${s}`
}
const currentVersion = computed(() => withV(supportStore.updateInfo?.current || supportStore.appVersion))
const latestVersion = computed(() => withV(supportStore.updateInfo?.latest))
</script>
