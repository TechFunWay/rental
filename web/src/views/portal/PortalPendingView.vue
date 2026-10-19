<template>
  <div class="min-h-screen flex items-center justify-center p-3 sm:p-4 bg-background text-foreground">
    <div class="w-full max-w-md surface rounded-2xl p-4 sm:p-8 animate-scale-in">
      <template v-if="pending">
        <div class="flex flex-col items-center text-center">
          <div class="w-11 h-11 sm:w-14 sm:h-14 rounded-xl sm:rounded-2xl bg-brand-500/15 text-brand-600 dark:text-brand-300 flex items-center justify-center">
            <svg class="w-5 h-5 sm:w-7 sm:h-7" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13.828 10.172a4 4 0 00-5.656 0l-4 4a4 4 0 105.656 5.656l1.102-1.101m-.758-4.899a4 4 0 005.656 0l4-4a4 4 0 00-5.656-5.656l-1.1 1.1" />
            </svg>
          </div>
          <h1 class="mt-3 sm:mt-4 text-base sm:text-lg font-bold text-foreground">等待房东关联你的账号</h1>
          <p class="mt-1.5 sm:mt-2 text-xs sm:text-sm text-muted-foreground leading-5 sm:leading-relaxed">
            注册成功。还需要房东在「租户管理」里把你的账号关联到他的租户记录，
            关联后你就能查看自己的登记信息、账单与合同了。
          </p>

          <div class="mt-3.5 sm:mt-5 w-full rounded-xl border border-border bg-muted/50 px-3 py-2.5 sm:px-3.5 sm:py-3 text-left space-y-1.5">
            <div class="flex items-center justify-between gap-3 text-xs sm:text-sm">
              <span class="text-muted-foreground shrink-0">登录账号</span>
              <span class="font-semibold text-foreground truncate">{{ authStore.user?.username || '—' }}</span>
            </div>
            <div class="flex items-center justify-between gap-3 text-xs sm:text-sm">
              <span class="text-muted-foreground shrink-0">当前状态</span>
              <span class="badge bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300">
                <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>等待关联
              </span>
            </div>
          </div>

          <p class="mt-3 text-[11px] text-muted-foreground leading-5">
            提示：请把上面这个登录账号告诉房东，方便他找到并关联你的账号。
          </p>
        </div>

        <div class="mt-3.5 sm:mt-5 flex flex-col gap-2 sm:gap-2.5">
          <button class="btn-brand min-h-11 sm:min-h-12 w-full" :disabled="checking" @click="recheck">
            <svg v-if="checking" class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
            </svg>
            {{ checking ? '查询中…' : '刷新关联状态' }}
          </button>
          <button class="btn-ghost min-h-11 sm:min-h-12 w-full" @click="handleLogout">退出登录</button>
        </div>
      </template>

      <!-- 已关联：给一个明确的去处，不自动跳转（避免房东刚点关联、租户还在这页时页面突然消失） -->
      <template v-else>
        <div class="flex flex-col items-center text-center">
          <div class="w-11 h-11 sm:w-14 sm:h-14 rounded-xl sm:rounded-2xl bg-emerald-500/15 text-emerald-600 dark:text-emerald-300 flex items-center justify-center">
            <svg class="w-5 h-5 sm:w-7 sm:h-7" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
          </div>
          <h1 class="mt-3 sm:mt-4 text-base sm:text-lg font-bold text-foreground">关联成功</h1>
          <p class="mt-1.5 sm:mt-2 text-xs sm:text-sm text-muted-foreground leading-5 sm:leading-relaxed">
            房东已经关联了你的账号，现在可以查看自己的登记信息、账单与合同了。
          </p>
        </div>
        <div class="mt-3.5 sm:mt-5 flex flex-col gap-2 sm:gap-2.5">
          <button class="btn-brand min-h-11 sm:min-h-12 w-full" @click="goPortal">进入租客服务</button>
          <button class="btn-ghost min-h-11 sm:min-h-12 w-full" @click="handleLogout">退出登录</button>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '../../stores/auth'
import { useLedgerStore } from '../../stores/ledger'
import { getPortalStatus } from '../../api/tenant'
import { toast } from '../../utils/toast'

// 租户「等待房东关联」页（v0.6.10）。
//
// 与房东的 PendingApprovalView 刻意分开：两者登录态都是 status=2，但语义完全不同
// （房东等管理员审核；租户等房东关联）。混用会让租户看到「等待管理员审核」却不知
// 该找谁做关联——实际要做关联的正是他的房东。
//
// 数据来源：GET /api/rental/portal/status。该接口在待审核白名单里，因此未关联的
// 租户也读得到（这是他能读的唯一接口）。不轮询：用户主动点刷新即可。
const router = useRouter()
const authStore = useAuthStore()
const ledgerStore = useLedgerStore()

const checking = ref(false)
const serverBound = ref<boolean | null>(null)

const pending = computed(() => !(serverBound.value ?? false))

onMounted(() => {
  void refresh()
})

async function refresh() {
  try {
    const res = await getPortalStatus()
    if (res.data?.code === 0) {
      const d = res.data.data
      serverBound.value = !!d?.bound
      // 后端是权威来源：状态变了就回写 authStore（守卫按它分流）。
      if (d && serverBound.value && authStore.user?.status !== 1) {
        authStore.setUser({ ...authStore.user, status: 1 })
      }
    }
  } catch {
    // 接口异常时保持「等待关联」的判定，页面照常可读。
  }
}

async function recheck() {
  checking.value = true
  try {
    await refresh()
    if (serverBound.value) {
      toast('房东已关联你的账号，欢迎使用')
    } else {
      toast('还没有关联，请确认房东已操作', 'error')
    }
  } finally {
    checking.value = false
  }
}

function goPortal() {
  router.push({ name: 'PortalHome' })
}

function handleLogout() {
  void authStore.endSession()
  ledgerStore.reset()
  router.push('/login')
}
</script>
