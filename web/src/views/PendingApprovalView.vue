<template>
  <div class="min-h-screen flex items-center justify-center p-3 sm:p-4 bg-background text-foreground">
    <div class="w-full max-w-md surface rounded-2xl p-4 sm:p-8 animate-scale-in">
      <!-- 待审核 -->
      <template v-if="pending">
        <div class="flex flex-col items-center text-center">
          <div class="w-11 h-11 sm:w-14 sm:h-14 rounded-xl sm:rounded-2xl bg-amber-400/15 text-amber-500 dark:text-amber-300 flex items-center justify-center">
            <svg class="w-5 h-5 sm:w-7 sm:h-7" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <h1 class="mt-3 sm:mt-4 text-base sm:text-lg font-bold text-foreground">账号正在等待管理员审核</h1>
          <p class="mt-1.5 sm:mt-2 text-xs sm:text-sm text-muted-foreground leading-5 sm:leading-relaxed">
            您的账号正在等待管理员审核，审核通过后即可使用。
          </p>
          <p class="mt-1.5 sm:mt-2 text-xs text-muted-foreground leading-5 sm:leading-relaxed">
            审核通过后即可在自己的「个人台账」里录房源与账单；
            在此之前业务数据不可访问，这是正常现象，无需重复注册。
          </p>

          <div class="mt-3.5 sm:mt-5 w-full rounded-xl border border-border bg-muted/50 px-3 py-2.5 sm:px-3.5 sm:py-3 text-left space-y-1.5">
            <div class="flex items-center justify-between gap-3 text-xs sm:text-sm">
              <span class="text-muted-foreground shrink-0">账号</span>
              <span class="font-semibold text-foreground truncate">{{ authStore.user?.username || '—' }}</span>
            </div>
            <div class="flex items-center justify-between gap-3 text-xs sm:text-sm">
              <span class="text-muted-foreground shrink-0">当前状态</span>
              <span class="badge bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300">
                <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>待审核
              </span>
            </div>
          </div>
        </div>

        <div class="mt-3.5 sm:mt-5 flex flex-col gap-2 sm:gap-2.5">
          <button class="btn-brand min-h-11 sm:min-h-12 w-full" :disabled="checking" @click="recheck">
            <svg v-if="checking" class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
            </svg>
            {{ checking ? '查询中…' : '刷新审核状态' }}
          </button>
          <button class="btn-ghost min-h-11 sm:min-h-12 w-full" @click="handleLogout">退出登录</button>
        </div>
        <p class="mt-3 text-[11px] text-center text-muted-foreground">
          已联系管理员？点「刷新审核状态」即可继续。
        </p>
      </template>

      <!-- 已通过：给一个明确的去处，不自动跳转（避免管理员刚点通过、用户还在看这一页时页面突然消失） -->
      <template v-else>
        <div class="flex flex-col items-center text-center">
          <div class="w-11 h-11 sm:w-14 sm:h-14 rounded-xl sm:rounded-2xl bg-emerald-500/15 text-emerald-600 dark:text-emerald-300 flex items-center justify-center">
            <svg class="w-5 h-5 sm:w-7 sm:h-7" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <h1 class="mt-3 sm:mt-4 text-base sm:text-lg font-bold text-foreground">审核已通过</h1>
          <p class="mt-1.5 sm:mt-2 text-xs sm:text-sm text-muted-foreground leading-5 sm:leading-relaxed">
            {{ ledgerStore.hasNoLedger
              ? '您的账号已启用，但还没有可用的台账。请联系管理员确认账号状态。'
              : '您的账号已启用，现在可以进入控制台使用出租屋数据了。' }}
          </p>
        </div>
        <div class="mt-3.5 sm:mt-5 flex flex-col gap-2 sm:gap-2.5">
          <button class="btn-brand min-h-11 sm:min-h-12 w-full" @click="goHome">进入控制台</button>
          <button class="btn-ghost min-h-11 sm:min-h-12 w-full" @click="handleLogout">退出登录</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useLedgerStore } from '../stores/ledger'
import { getMyApproval } from '../api/ledger'
import { toast } from '../utils/toast'

// 待审核（status === 2）用户的落地页。
//
// 数据来源有两层，互为兜底：
//   1. 登录/注册响应里的 user.status（authStore.user），路由守卫用它即时判定；
//   2. GET /api/rental/users/me/approval —— 该接口在待审核白名单里，能拿到
//      权威的状态与后端统一的中文提示，用于「刷新审核状态」按钮。
//
// 这里不轮询：用户主动点刷新即可，避免给待审核用户堆无谓的请求。
const router = useRouter()
const authStore = useAuthStore()
const ledgerStore = useLedgerStore()

const checking = ref(false)
const serverPending = ref<boolean | null>(null)

const pending = computed(() => serverPending.value ?? authStore.isPendingUser)

onMounted(() => {
  void refresh()
})

async function refresh() {
  try {
    const res = await getMyApproval()
    if (res.data?.code === 0) {
      const d = res.data.data
      serverPending.value = !!d?.pending
      // 后端是权威来源：把状态回写到 authStore.user，路由守卫与全站判定
      // 都跟着这一份走（否则刷新页面后又会按陈旧的 status 判一次）。
      if (d && authStore.user && authStore.user.status !== d.status) {
        authStore.setUser({ ...authStore.user, status: d.status })
      }
      await ledgerStore.load(true)
    }
  } catch {
    // 接口异常时保持按登录响应里的 status 判定，页面照常可读。
  }
}

async function recheck() {
  checking.value = true
  try {
    await refresh()
    if (serverPending.value === false) {
      toast('审核已通过，欢迎使用')
    } else {
      toast('仍在等待审核，请稍后再试', 'error')
    }
  } finally {
    checking.value = false
  }
}

function goHome() {
  router.push({ name: 'Home' })
}

function handleLogout() {
  void authStore.endSession()
  ledgerStore.reset()
  router.push('/login')
}
</script>
