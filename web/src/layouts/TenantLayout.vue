<template>
  <!-- 租户门户外壳（v0.6.10）：与房东的 MainLayout 完全分开。
       租户不涉及台账/权限/业务录入，所以这里没有台账切换器、没有业务导航、
       没有管理后台入口——手机端底部四个标签，桌面端左侧窄侧栏。 -->
  <div class="min-h-screen flex">
    <div class="app-bg" aria-hidden="true">
      <div class="glow glow-1"></div>
      <div class="glow glow-2"></div>
      <div class="glow glow-3"></div>
      <div class="app-grid"></div>
    </div>

    <!-- 桌面端侧栏（lg 及以上） -->
    <aside class="hidden lg:flex fixed inset-y-0 left-0 z-40 w-64 p-3 pr-0">
      <div class="h-full w-full rounded-2xl surface flex flex-col overflow-hidden">
        <div class="flex items-center gap-3 h-20 px-5 border-b border-border">
          <div class="w-10 h-10 rounded-xl bg-brand-gradient flex items-center justify-center shadow-glow shrink-0">
            <svg class="w-5 h-5 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
            </svg>
          </div>
          <div class="min-w-0">
            <h1 class="text-sm font-display font-bold text-foreground truncate">{{ authStore.siteTitle }}</h1>
            <p class="text-[11px] text-muted-foreground">租客服务</p>
          </div>
        </div>

        <nav class="flex-1 overflow-y-auto px-3 py-4 space-y-1">
          <RouterLink
            v-for="item in navItems"
            :key="item.name"
            :to="{ name: item.name }"
            class="nav-link group"
            :class="isActive(item.name) ? 'nav-link-active' : 'nav-link-idle'"
          >
            <span
              class="nav-icon"
              :class="isActive(item.name) ? 'bg-white/20 text-white' : 'bg-muted text-muted-foreground group-hover:text-brand-600 dark:group-hover:text-brand-300'"
            >
              <span v-html="item.icon" class="w-5 h-5 block"></span>
            </span>
            {{ item.label }}
          </RouterLink>
        </nav>

        <div class="p-3 border-t border-border">
          <div class="flex items-center gap-2.5 p-2.5 rounded-xl bg-muted">
            <div class="w-9 h-9 rounded-full bg-brand-gradient flex items-center justify-center text-white font-bold text-xs shrink-0">
              {{ userInitial }}
            </div>
            <div class="min-w-0 flex-1">
              <div class="text-xs font-semibold text-foreground truncate">{{ authStore.user?.username }}</div>
              <div class="text-[11px] text-muted-foreground truncate">
                租户{{ status?.tenant_name ? ` · ${status.tenant_name}` : '' }}
              </div>
            </div>
            <button
              class="p-2 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
              title="退出登录"
              @click="handleLogout"
            >
              <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
              </svg>
            </button>
          </div>
        </div>
      </div>
    </aside>

    <!-- 主区域 -->
    <div class="flex-1 lg:ml-64 min-h-screen flex flex-col">
      <!-- 手机端顶栏 -->
      <header class="lg:hidden sticky top-0 z-30 surface border-b border-border">
        <div class="flex items-center gap-3 px-3 h-14">
          <div class="w-9 h-9 rounded-xl bg-brand-gradient flex items-center justify-center shrink-0">
            <svg class="w-4.5 h-4.5 text-white" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M3 12l2-2m0 0l7-7 7 7M5 10v10a1 1 0 001 1h3m10-11l2 2m-2-2v10a1 1 0 01-1 1h-3m-6 0a1 1 0 001-1v-4a1 1 0 011-1h2a1 1 0 011 1v4a1 1 0 001 1m-6 0h6" />
            </svg>
          </div>
          <div class="min-w-0 flex-1">
            <div class="text-sm font-semibold text-foreground truncate">{{ authStore.siteTitle }}</div>
            <div class="text-[11px] text-muted-foreground truncate">
              租客服务{{ status?.tenant_name ? ` · ${status.tenant_name}` : '' }}
            </div>
          </div>
          <button
            class="p-2 rounded-lg text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
            title="退出登录"
            @click="handleLogout"
          >
            <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 16l4-4m0 0l-4-4m4 4H7m6 4v1a3 3 0 01-3 3H6a3 3 0 01-3-3V7a3 3 0 013-3h4a3 3 0 013 3v1" />
            </svg>
          </button>
        </div>
      </header>

      <!-- 手机端底部导航（一屏信息量：固定四个标签，留出安全区） -->
      <nav
        class="lg:hidden fixed bottom-0 inset-x-0 z-30 surface border-t border-border"
        style="padding-bottom: env(safe-area-inset-bottom)"
        aria-label="租客导航"
      >
        <div class="grid grid-cols-4">
          <RouterLink
            v-for="item in navItems"
            :key="item.name"
            :to="{ name: item.name }"
            class="flex flex-col items-center justify-center gap-0.5 py-2 text-[11px] font-medium transition-colors"
            :class="isActive(item.name) ? 'text-brand-600 dark:text-brand-300' : 'text-muted-foreground'"
          >
            <span
              class="w-9 h-7 rounded-lg flex items-center justify-center"
              :class="isActive(item.name) ? 'bg-brand-gradient text-white shadow-glow' : ''"
            >
              <span v-html="item.icon" class="w-5 h-5 block"></span>
            </span>
            {{ item.label }}
          </RouterLink>
        </div>
      </nav>

      <main class="flex-1 p-3 sm:p-6 xl:p-8 pb-24 lg:pb-8">
        <RouterView v-slot="{ Component }">
          <transition
            mode="out-in"
            enter-active-class="transition-[opacity,transform] duration-150 ease-out"
            enter-from-class="opacity-0 translate-y-1.5"
            leave-active-class="transition-opacity duration-75"
            leave-to-class="opacity-0"
          >
            <component :is="Component" />
          </transition>
        </RouterView>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useLedgerStore } from '../stores/ledger'
import { getPortalStatus, type PortalStatus } from '../api/tenant'

// 租户门户外壳。刻意保持极简：租户只读自己的数据，没有台账、权限与业务录入，
// 因此不做 MainLayout 那套台账切换器 / 业务导航 / 管理后台分区 / 赞赏横幅。
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const ledgerStore = useLedgerStore()

const status = ref<PortalStatus | null>(null)

const userInitial = computed(() => (authStore.user?.username || '?').slice(0, 1).toUpperCase())

const navItems = [
  {
    name: 'PortalHome',
    label: '我的资料',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>',
  },
  {
    name: 'PortalBills',
    label: '我的账单',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5H7a2 2 0 00-2 2v12a2 2 0 002 2h10a2 2 0 002-2V7a2 2 0 00-2-2h-2M9 5a2 2 0 002 2h2a2 2 0 002-2M9 5a2 2 0 012-2h2a2 2 0 012 2m-3 7h3m-3 4h3"/></svg>',
  },
  {
    name: 'PortalPayments',
    label: '缴费记录',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 9V7a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2m2 4h10a2 2 0 002-2v-6a2 2 0 00-2-2H9a2 2 0 00-2 2v6a2 2 0 002 2zm7-5a2 2 0 11-4 0 2 2 0 014 0z"/></svg>',
  },
  {
    name: 'PortalContracts',
    label: '我的合同',
    icon: '<svg fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"/></svg>',
  },
]

function isActive(name: string): boolean {
  return route.name === name
}

onMounted(async () => {
  try {
    const res = await getPortalStatus()
    if (res.data?.code === 0) status.value = res.data.data
  } catch {
    // 顶栏的姓名是锦上添花：拉不到不影响门户各页正常渲染。
  }
})

function handleLogout() {
  void authStore.endSession()
  ledgerStore.reset()
  router.push('/login')
}
</script>
