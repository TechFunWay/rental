<template>
  <div class="min-h-screen flex">
    <!-- Ambient theme-aware background -->
    <div class="app-bg" aria-hidden="true">
      <div class="glow glow-1"></div>
      <div class="glow glow-2"></div>
      <div class="glow glow-3"></div>
      <div class="app-grid"></div>
    </div>

    <!-- Sidebar -->
    <aside
      :class="[
        'fixed inset-y-0 left-0 z-40 w-72 p-3 pr-0 pb-[calc(0.75rem+env(safe-area-inset-bottom))] lg:pr-3 transform transition-transform duration-300 ease-out lg:translate-x-0',
        sidebarOpen ? 'translate-x-0' : '-translate-x-full'
      ]"
    >
      <!-- 边距由 aside 的 padding 承担：内层 h-full + 自身 margin 会超出视口，手机上底部被顶出屏幕 -->
      <div class="h-full rounded-2xl surface flex flex-col overflow-hidden">
        <!-- Brand -->
        <div class="flex items-center gap-3 h-20 px-6 border-b border-border">
          <div class="w-11 h-11 rounded-xl bg-brand-gradient flex items-center justify-center shadow-glow shrink-0">
            <span class="text-white font-display font-extrabold text-lg">{{ siteInitial }}</span>
          </div>
          <div class="min-w-0">
            <h1 class="text-base font-display font-bold text-foreground truncate">{{ authStore.siteTitle }}</h1>
            <p class="text-xs text-muted-foreground">管理控制台</p>
          </div>
        </div>

        <!-- Nav -->
        <nav class="flex-1 overflow-y-auto px-4 py-5 space-y-1">
          <RouterLink
            v-for="item in mainNav"
            :key="item.to"
            :to="item.to"
            class="nav-link group"
            :class="isActive(item.to) ? 'nav-link-active' : 'nav-link-idle'"
            @click="sidebarOpen = false"
          >
            <span
              class="nav-icon"
:class="isActive(item.to) ? 'bg-white/20 text-white' : 'bg-muted text-muted-foreground group-hover:text-brand-600 dark:group-hover:text-brand-300'"
              >
                <span v-html="item.icon" class="w-5 h-5 block"></span>
              </span>
              {{ item.label }}
              <span
                v-if="item.to === '/admin/bills' && paymentDueCount"
                class="ml-auto inline-flex items-center justify-center min-w-[18px] h-[18px] px-1 rounded-full text-[11px] font-bold bg-rose-500 text-white tabular-nums"
                title="待收缴费提醒"
              >{{ paymentDueCount > 99 ? '99+' : paymentDueCount }}</span>
            </RouterLink>

            <template v-if="authStore.isAdmin">
              <div class="px-3 pt-6 pb-2">
                <span class="text-[11px] font-bold text-muted-foreground uppercase tracking-widest">系统管理</span>
              </div>
              <RouterLink
                v-for="item in adminNav"
                :key="item.to"
                :to="item.to"
                class="nav-link group"
                :class="isActive(item.to) ? 'nav-link-active' : 'nav-link-idle'"
                @click="sidebarOpen = false"
              >
                <span
                  class="nav-icon"
                  :class="isActive(item.to) ? 'bg-white/20 text-white' : 'bg-muted text-muted-foreground group-hover:text-brand-600 dark:group-hover:text-brand-300'"
              >
                <span v-html="item.icon" class="w-5 h-5 block"></span>
              </span>
              {{ item.label }}
            </RouterLink>
          </template>
        </nav>

        <!-- User card -->
        <div class="p-4 border-t border-border">
          <div class="flex items-center gap-3 p-2.5 rounded-xl bg-muted">
            <div class="w-10 h-10 rounded-full bg-brand-gradient flex items-center justify-center text-white font-bold text-sm shrink-0">
              {{ userInitial }}
            </div>
            <div class="min-w-0 flex-1">
              <div class="text-sm font-semibold text-foreground truncate">{{ authStore.user?.username }}</div>
              <div class="text-xs text-muted-foreground">{{ authStore.isAdmin ? '管理员' : '普通用户' }}</div>
            </div>
            <button
              @click="handleLogout"
              title="退出登录"
              class="p-2 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" /></svg>
            </button>
          </div>
        </div>

        <!-- 支持作者 -->
        <div class="px-4 pb-2">
          <button
            class="w-full flex items-center justify-center gap-2 px-3 py-2.5 rounded-xl text-sm font-semibold text-amber-600 dark:text-amber-300 bg-amber-400/10 hover:bg-amber-400/20 transition-colors"
            @click="openSupport"
          >
            <svg class="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><path d="M12 21.35l-1.45-1.32C5.4 15.36 2 12.28 2 8.5 2 5.42 4.42 3 7.5 3c1.74 0 3.41.81 4.5 2.09C13.09 3.81 14.76 3 16.5 3 19.58 3 22 5.42 22 8.5c0 3.78-3.4 6.86-8.55 11.54L12 21.35z"/></svg>
            支持作者
          </button>
        </div>

        <!-- 版本号已移至顶栏左上角 -->
      </div>
    </aside>

    <!-- Mobile overlay -->
    <transition enter-active-class="transition-opacity duration-200" enter-from-class="opacity-0" leave-active-class="transition-opacity duration-200" leave-to-class="opacity-0">
      <div v-if="sidebarOpen" class="fixed inset-0 z-30 bg-foreground/40 backdrop-blur-sm lg:hidden" @click="sidebarOpen = false"></div>
    </transition>

    <!-- Main column -->
    <div class="flex-1 flex flex-col min-h-screen lg:pl-72 min-w-0">
      <header class="sticky top-0 z-20 px-4 lg:px-8 pt-3">
        <div class="h-16 flex items-center justify-between px-4 lg:px-6 rounded-2xl surface">
          <div class="flex items-center gap-3 min-w-0">
            <button class="lg:hidden p-2 -ml-1 rounded-lg text-foreground hover:bg-muted" @click="sidebarOpen = !sidebarOpen">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16" /></svg>
            </button>
            <div class="min-w-0 flex-1">
              <!-- breadcrumb + 版本号（左上角，只显示 v+版本） -->
              <div class="flex items-center gap-2 text-sm">
                <RouterLink to="/admin" class="text-muted-foreground hover:text-foreground transition-colors truncate">{{ authStore.siteTitle }}</RouterLink>
                <svg class="hidden sm:block w-4 h-4 text-muted-foreground shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7"/></svg>
                <span class="hidden sm:inline text-foreground font-semibold truncate">{{ currentTitle }}</span>
                <span v-if="versionInfo" class="ml-1 inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium tabular-nums bg-muted text-muted-foreground border border-border/50">{{ versionInfo }}</span>
              </div>
            </div>
          </div>
          <div class="flex items-center gap-2 sm:gap-3">
            <UiThemeToggle />

            <!-- user dropdown -->
            <div class="relative" ref="menuRef">
              <button
                @click="menuOpen = !menuOpen"
                class="flex items-center gap-2.5 pl-1 pr-2 py-1 rounded-xl hover:bg-muted transition-colors"
              >
                <div class="w-9 h-9 rounded-full bg-brand-gradient flex items-center justify-center text-white font-bold text-sm">{{ userInitial }}</div>
                <span class="hidden sm:block text-sm font-medium text-foreground">{{ authStore.user?.username }}</span>
                <svg class="hidden sm:block w-4 h-4 text-muted-foreground transition-transform" :class="menuOpen ? 'rotate-180' : ''" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7"/></svg>
              </button>
              <transition enter-active-class="transition duration-150 ease-out" enter-from-class="opacity-0 scale-95 -translate-y-1" leave-active-class="transition duration-100" leave-to-class="opacity-0 scale-95 -translate-y-1">
                <div v-if="menuOpen" class="absolute right-0 mt-2 w-56 rounded-2xl surface shadow-card p-2 origin-top-right">
                  <div class="px-3 py-2.5 mb-1 border-b border-border">
                    <div class="text-sm font-semibold text-foreground truncate">{{ authStore.user?.username }}</div>
                    <div class="text-xs text-muted-foreground">{{ authStore.isAdmin ? '管理员' : '普通用户' }}</div>
                  </div>
                  <RouterLink to="/admin/profile" @click="menuOpen = false" class="menu-item">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
                    个人资料
                  </RouterLink>
                  <button @click="handleLogout" class="menu-item w-full text-destructive hover:bg-destructive/10">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1"/></svg>
                    退出登录
                  </button>
                </div>
              </transition>
            </div>
          </div>
        </div>
      </header>

      <main class="flex-1 p-4 pb-28 sm:p-6 sm:pb-28 lg:pb-8 xl:p-8 overflow-auto">
        <!-- 新版本提示条（管理员，每个最新版本一次） -->
        <div v-if="supportStore.updateBannerVisible" class="mb-4 flex items-center justify-between gap-3 rounded-2xl border border-brand-500/30 bg-brand-500/10 px-4 py-3">
          <span class="text-sm text-foreground">
            🎉 发现新版本 <strong>{{ supportStore.updateInfo?.latest }}</strong>，当前版本 {{ supportStore.updateInfo?.current }}，建议升级以获得最新功能和修复。
            <a :href="supportStore.updateInfo?.download_url" target="_blank" rel="noopener" class="ml-2 font-semibold text-brand-600 dark:text-brand-300 hover:underline">前往下载</a>
          </span>
          <button class="text-muted-foreground hover:text-foreground text-lg leading-none px-1" title="关闭" @click="supportStore.dismissUpdateBanner()">×</button>
        </div>

        <!-- 赞赏横幅（管理员，每个版本一次）；手机端只留一句，长文案桌面端才显示 -->
        <div v-if="supportStore.bannerVisible" class="mb-4 flex items-center justify-between gap-3 rounded-2xl border border-amber-400/40 bg-amber-400/10 px-4 py-3">
          <span class="text-sm text-foreground">
            ❤ <button class="font-semibold text-brand-600 dark:text-brand-300 hover:underline" @click="supportStore.open()">请作者喝杯咖啡</button><span class="hidden sm:inline">（不赞赏不影响任何功能）</span><span class="sm:hidden">，不赞赏不影响功能</span>。
          </span>
          <button class="text-muted-foreground hover:text-foreground text-lg leading-none px-1" title="关闭" @click="supportStore.dismissBanner()">×</button>
        </div>

        <RouterView v-slot="{ Component }">
          <!-- 手机端体验：短淡入即可（out-in 长动画会有"停顿-再出现"的卡顿感） -->
          <transition mode="out-in" enter-active-class="transition-[opacity,transform] duration-150 ease-out" enter-from-class="opacity-0 translate-y-1.5" leave-active-class="transition-opacity duration-75" leave-to-class="opacity-0">
            <component :is="Component" />
          </transition>
        </RouterView>
      </main>
    </div>

    <!-- 手机底部导航（lg 以下显示）：高频业务页一键直达 -->
    <nav
      class="fixed bottom-0 inset-x-0 z-30 lg:hidden surface border-t border-border"
      style="padding-bottom: env(safe-area-inset-bottom)"
      aria-label="手机端主导航"
    >
      <div class="grid grid-cols-5">
        <RouterLink
          v-for="item in mobileNav"
          :key="item.to"
          :to="item.to"
          class="flex flex-col items-center justify-center gap-0.5 py-2.5 text-[11px] font-medium transition-colors"
          :class="isActive(item.to) ? 'text-brand-600 dark:text-brand-300' : 'text-muted-foreground'"
        >
          <span class="relative">
            <span
              class="w-9 h-7 rounded-lg flex items-center justify-center"
              :class="isActive(item.to) ? 'bg-brand-gradient text-white shadow-glow' : ''"
            >
              <span v-html="item.icon" class="w-5 h-5 block"></span>
            </span>
            <span
              v-if="item.to === '/admin/bills' && paymentDueCount"
              class="absolute -top-1 -right-2 min-w-[16px] h-4 px-1 rounded-full text-[10px] font-bold bg-rose-500 text-white flex items-center justify-center tabular-nums"
            >{{ paymentDueCount > 99 ? '99+' : paymentDueCount }}</span>
          </span>
          {{ item.label }}
        </RouterLink>
      </div>
    </nav>

    <!-- 赞赏支持弹窗（安全问题引导优先，让位不渲染） -->
    <SupportModal v-if="!showSecurityModal" />

    <!-- Security question prompt -->
    <Modal v-model="showSecurityModal" title="设置安全问题" :closable="false">
      <div class="flex flex-col items-center text-center gap-4 py-2">
        <div class="w-14 h-14 rounded-2xl bg-amber-400/15 text-amber-400 flex items-center justify-center">
          <svg class="w-7 h-7" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 16.5c-.77.833.192 2.5 1.732 2.5z"/></svg>
        </div>
        <p class="text-sm text-muted-foreground leading-relaxed">
          你尚未设置安全问题。设置后可在忘记密码时通过安全问题找回账号。
        </p>
        <div class="flex items-center gap-3 w-full pt-2">
          <button @click="dismissSecurityPrompt" class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold text-muted-foreground hover:bg-muted transition-colors">
            稍后再说
          </button>
          <button @click="goToSecurityQuestions" class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold bg-brand-500 text-white hover:bg-brand-600 transition-colors">
            去设置
          </button>
        </div>
      </div>
    </Modal>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onBeforeUnmount } from 'vue'
import { useRouter, useRoute, RouterLink, RouterView } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useThemeStore } from '../stores/theme'
import { getVersion } from '../api/config'
import { getStats } from '../api/rental'
import { useSupportStore } from '../stores/support'
import UiThemeToggle from '../components/ui/ThemeToggle.vue'
import Modal from '../components/Modal.vue'
import SupportModal from '../components/SupportModal.vue'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()
const themeStore = useThemeStore()
const supportStore = useSupportStore()
const sidebarOpen = ref(false)
const menuOpen = ref(false)
const menuRef = ref<HTMLElement | null>(null)
const versionInfo = ref('')

// 缴费提醒角标：待提醒的缴费笔数，挂在「抄表账单」导航项上。
// 轮询 + 路由切换时刷新，失败静默（角标只是提醒，不阻塞任何页面）。
const paymentDueCount = ref(0)
let paymentTimer: number | undefined

async function refreshPaymentBadge() {
  try {
    const res = await getStats()
    if (res.data?.code === 0) {
      paymentDueCount.value = (res.data.data?.payment_due ?? []).length
    }
  } catch { /* 静默 */ }
}

onMounted(async () => {
  try {
    const res = await getVersion()
    if (res.data?.code === 0) {
      const d = res.data.data
      versionInfo.value = `v${d.version}`
    }
  } catch {}
  // 赞赏提示与新版本检查：仅管理员可见（store 内部只对管理员生效）
  await authStore.init()
  supportStore.init(authStore.isAdmin)
  // 缴费提醒角标：立即拉一次，之后每 5 分钟刷新
  refreshPaymentBadge()
  paymentTimer = window.setInterval(refreshPaymentBadge, 5 * 60 * 1000)
})

onBeforeUnmount(() => {
  if (paymentTimer) window.clearInterval(paymentTimer)
})

// 路由切换时刷新角标（在账单页收款后回到其它页面时数字能及时更新）。
watch(() => route.path, () => refreshPaymentBadge())

const mainNavIcons = {
  overview: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>',
  rooms: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>',
  tenants: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2M9 11a4 4 0 100-8 4 4 0 000 8zm7 2a4 4 0 014 4v2h-4m-4-9.5a2.5 2.5 0 11-3 4.1"/></svg>',
  bills: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3"/></svg>',
  meters: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/></svg>',
}

const mobileNav = [
  { to: '/admin', label: '总览', icon: mainNavIcons.overview },
  { to: '/admin/rooms', label: '房源', icon: mainNavIcons.rooms },
  { to: '/admin/tenants', label: '租户', icon: mainNavIcons.tenants },
  { to: '/admin/meters', label: '抄表', icon: mainNavIcons.meters },
  { to: '/admin/bills', label: '账单', icon: mainNavIcons.bills },
]

const mainNav = [
  { to: '/admin', label: '总览', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>' },
  { to: '/admin/rooms', label: '房源管理', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6"/></svg>' },
  { to: '/admin/tenants', label: '租户管理', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2M9 11a4 4 0 100-8 4 4 0 000 8zm7 2a4 4 0 014 4v2h-4m-4-9.5a2.5 2.5 0 11-3 4.1"/></svg>' },
  { to: '/admin/meters', label: '抄表记录', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z"/></svg>' },
  { to: '/admin/bills', label: '抄表账单', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3"/></svg>' },
  { to: '/admin/payments', label: '收款记录', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 9V7a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2m2 4h10a2 2 0 002-2v-6a2 2 0 00-2-2H9a2 2 0 00-2 2v6a2 2 0 002 2zm7-5a2 2 0 11-4 0 2 2 0 014 0z"/></svg>' },
  { to: '/admin/analytics', label: '统计分析', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z"/></svg>' },
  { to: '/admin/profile', label: '个人资料', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>' },
  { to: '/admin/settings', label: '偏好设置', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.5 6h9.75M10.5 6a1.5 1.5 0 11-3 0m3 0a1.5 1.5 0 10-3 0M3.75 6H7.5m3 12h9.75m-9.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-3.75 0H7.5m9-6h3.75m-3.75 0a1.5 1.5 0 01-3 0m3 0a1.5 1.5 0 00-3 0m-9.75 0h9.75"/></svg>' },
]

const adminNav = [
  { to: '/admin/users', label: '用户管理', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 4.354a4 4 0 110 5.292M15 21H3v-1a6 6 0 0112 0v1zm0 0h6v-1a6 6 0 00-9-5.197M13 7a4 4 0 11-8 0 4 4 0 018 0z"/></svg>' },
  { to: '/admin/configs', label: '系统配置', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.066 2.573c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.573 1.066c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.066-2.573c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 12a3 3 0 11-6 0 3 3 0 016 0z"/></svg>' },
  { to: '/admin/backups', label: '备份管理', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 8h14M5 8a2 2 0 110-4h14a2 2 0 110 4M5 8v10a2 2 0 002 2h10a2 2 0 002-2V8m-9 4h4"/></svg>' },
  { to: '/admin/audit', label: '操作日志', icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3m-6-4h.01M9 16h.01"/></svg>' },
]

const titleMap: Record<string, string> = {
  '/admin': '总览',
  '/admin/rooms': '房源管理',
  '/admin/tenants': '租户管理',
  '/admin/bills': '抄表账单',
  '/admin/meters': '抄表记录',
  '/admin/payments': '收款记录',
  '/admin/analytics': '统计分析',
  '/admin/profile': '个人资料',
  '/admin/settings': '偏好设置',
  '/admin/users': '用户管理',
  '/admin/configs': '系统配置',
  '/admin/backups': '备份管理',
  '/admin/audit': '操作日志',
}

const currentTitle = computed(() => titleMap[route.path] || authStore.siteTitle)
const siteInitial = computed(() => (authStore.siteTitle || 'S').charAt(0).toUpperCase())
const userInitial = computed(() => (authStore.user?.username || 'U').charAt(0).toUpperCase())

function isActive(to: string) {
  return to === '/admin' ? route.path === '/admin' : route.path.startsWith(to)
}

function handleLogout() {
  menuOpen.value = false
  // endSession 先落服务端「主动登出」抑制标记再清本地（网关域上只清本地
  // 会被网关注入身份重新认回）；本地清理是同步完成的，随后即可跳登录页。
  void authStore.endSession()
  router.push('/login')
}

// "支持作者"入口：赞赏弹窗的常规调度仅对管理员生效，
// 这里对所有登录用户开放手动触发（复用设置页入口的 open()）。
function openSupport() {
  supportStore.open()
}

const showSecurityModal = ref(false)

watch(
  () => authStore.isAuthenticated && !authStore.hasSecurityQuestions && !authStore.securityPromptDismissed,
  (shouldShow) => {
    if (shouldShow) {
      showSecurityModal.value = true
    }
  },
  { immediate: true }
)

function goToSecurityQuestions() {
  showSecurityModal.value = false
  authStore.dismissSecurityPrompt()
  router.push('/admin/profile')
}

function dismissSecurityPrompt() {
  showSecurityModal.value = false
  authStore.dismissSecurityPrompt()
}

function onClickOutside(e: MouseEvent) {
  if (menuRef.value && !menuRef.value.contains(e.target as Node)) menuOpen.value = false
}
onMounted(() => document.addEventListener('click', onClickOutside))
onBeforeUnmount(() => document.removeEventListener('click', onClickOutside))
</script>

<style scoped>
.nav-link {
  @apply flex items-center gap-3 px-3 py-2.5 rounded-xl text-sm font-semibold transition-all duration-200;
}
.nav-link-active {
  @apply bg-brand-gradient text-white shadow-glow;
}
.nav-link-idle {
  @apply text-foreground hover:bg-muted;
}
.nav-icon {
  @apply w-9 h-9 rounded-lg flex items-center justify-center transition-colors shrink-0;
}
.menu-item {
  @apply flex items-center gap-2.5 px-3 py-2.5 rounded-xl text-sm font-medium text-foreground hover:bg-muted transition-colors;
}

/* Ambient background */
.app-bg {
  position: fixed;
  inset: 0;
  z-index: -10;
  overflow: hidden;
  pointer-events: none;
}
.glow {
  position: absolute;
  border-radius: 9999px;
  /* 用径向渐变画柔光，不用 filter: blur —— 大面积高斯模糊在手机 WebView 上渲染贵、重绘更贵 */
}
.glow-1 {
  width: 44rem;
  height: 44rem;
  top: -16rem;
  left: -12rem;
  background: radial-gradient(circle, rgba(99, 102, 241, 0.18) 0%, rgba(99, 102, 241, 0) 65%);
}
.glow-2 {
  width: 40rem;
  height: 40rem;
  top: -10rem;
  right: -14rem;
  background: radial-gradient(circle, rgba(168, 85, 247, 0.14) 0%, rgba(168, 85, 247, 0) 65%);
}
.glow-3 {
  width: 38rem;
  height: 38rem;
  bottom: -18rem;
  left: 28%;
  background: radial-gradient(circle, rgba(34, 211, 238, 0.1) 0%, rgba(34, 211, 238, 0) 65%);
}
.app-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(99, 102, 241, 0.04) 1px, transparent 1px),
    linear-gradient(90deg, rgba(99, 102, 241, 0.04) 1px, transparent 1px);
  background-size: 60px 60px;
  -webkit-mask: radial-gradient(circle at 50% 0%, #000 0%, transparent 70%);
  mask: radial-gradient(circle at 50% 0%, #000 0%, transparent 70%);
}
.dark .glow-1 { background: radial-gradient(circle, rgba(99, 102, 241, 0.3) 0%, rgba(99, 102, 241, 0) 65%); }
.dark .glow-2 { background: radial-gradient(circle, rgba(168, 85, 247, 0.24) 0%, rgba(168, 85, 247, 0) 65%); }
.dark .glow-3 { background: radial-gradient(circle, rgba(34, 211, 238, 0.16) 0%, rgba(34, 211, 238, 0) 65%); }
</style>
