<template>
  <div class="min-h-screen flex items-center justify-center p-3 sm:p-4 bg-background text-foreground">
    <div class="w-full max-w-lg surface rounded-2xl p-4 sm:p-7 animate-scale-in">
      <div class="flex flex-col items-center text-center">
        <div class="w-11 h-11 sm:w-12 sm:h-12 rounded-xl bg-brand-500/15 text-brand-500 dark:text-brand-300 flex items-center justify-center">
          <svg class="w-5 h-5 sm:w-6 sm:h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z" />
          </svg>
        </div>
        <h1 class="mt-3 text-base sm:text-lg font-bold text-foreground">选择你的身份</h1>
        <p class="mt-1.5 text-xs sm:text-sm text-muted-foreground leading-5">
          账号已创建。选一个最贴近你的用途，之后还能随时切换或叠加。
        </p>
      </div>

      <!-- 三个身份：卡片选择，和注册页一样一行三项，手机上也不挤 -->
      <div class="mt-4 grid grid-cols-3 gap-2">
        <button
          v-for="opt in roleOptions"
          :key="opt.value"
          type="button"
          class="rounded-xl border px-2 py-2.5 text-left transition-colors"
          :class="picked === opt.value
            ? 'border-brand-500/60 bg-brand-500/10 text-foreground'
            : 'border-border bg-muted/40 text-muted-foreground hover:bg-muted'"
          @click="picked = opt.value"
        >
          <div class="text-[13px] font-semibold leading-5">{{ opt.label }}</div>
          <div class="mt-0.5 text-[10px] leading-4">{{ opt.hint }}</div>
        </button>
      </div>

      <!-- 房东：可当场提交入驻资料，也可稍后补 -->
      <div v-if="picked === 'landlord'" class="mt-4 space-y-2.5">
        <div class="rounded-xl border border-border bg-muted/40 p-3 space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-foreground">平台入驻资料</span>
            <span class="text-[11px] text-muted-foreground">选填，可稍后补</span>
          </div>
          <LandlordApplyFields v-model="applyForm" part="basic" />
          <button type="button" class="text-[11px] font-medium text-brand-600 dark:text-brand-300" @click="applyMore = !applyMore">
            {{ applyMore ? '收起更多资料' : '更多资料（微信 / 地区 / 房源规模）' }}
          </button>
          <LandlordApplyFields v-if="applyMore" v-model="applyForm" part="extra" />
          <p class="text-[11px] text-muted-foreground leading-5">
            提交后由平台审核；审核期间可以登录，但业务数据要等通过后才能用。
          </p>
        </div>
        <button class="btn-brand w-full min-h-11" :disabled="busy" @click="submitApplyAndEnter">
          {{ busy ? '提交中…' : '提交入驻资料' }}
        </button>
        <button class="btn-ghost w-full min-h-11" :disabled="busy" @click="enterConsole">
          暂不填写，先进入控制台
        </button>
      </div>

      <!-- 业务员 / 协作成员（2026-11-25 用户纠正）：不是管理员，是被房东邀请进
           台账、只拿到部分模块权限的那种人（抄表员、收费员、前台…）。文案必须
           说清「权限由房东勾、可能只有部分功能」，别让人误以为自己是管理员。 -->
      <div v-else-if="picked === 'manager'" class="mt-4 space-y-2.5">
        <div class="rounded-xl border border-border bg-muted/40 p-3 space-y-1">
          <p class="text-xs text-foreground font-semibold">把用户名告诉房东</p>
          <p class="text-[11px] text-muted-foreground leading-5">
            你是业务员 / 协作成员，不是管理员。房东在「台账成员」里按用户名邀请你，
            并按模块勾选你能用的功能（如只让抄表、只让收款），没勾的模块你看不到也用不了。
          </p>
          <p class="text-[11px] text-muted-foreground">
            你的用户名：<span class="font-semibold text-foreground">{{ authStore.user?.username || '—' }}</span>
          </p>
        </div>
        <button class="btn-brand w-full min-h-11" @click="enterConsole">进入控制台</button>
      </div>

      <!-- 租户（多角色，2026-11-25 用户定调）：账号**不切换角色**——同一个账号
           可以同时是房东和租户。把用户名告诉房东，房东在「租户管理」里把档案关联
           给你，侧栏就会出现「租客门户」入口，随时来回切。 -->
      <div v-else class="mt-4 space-y-2.5">
        <div class="rounded-xl border border-border bg-muted/40 p-3 space-y-1">
          <p class="text-xs text-foreground font-semibold">我也要当租户</p>
          <p class="text-[11px] text-muted-foreground leading-5">
            账号不用改，照样能管自己的房源。把用户名
            <span class="font-semibold text-foreground">{{ authStore.user?.username || '—' }}</span>
            告诉房东，房东在「租户管理」里关联你的档案后，侧栏会出现「租客门户」，
            点进去就能看自己的账单、合同与缴费记录。
          </p>
          <p class="text-[11px] text-muted-foreground">一个账号可以同时是房东和租户，两边互不影响。</p>
        </div>
        <button class="btn-brand w-full min-h-11" @click="enterConsole">明白，进入控制台</button>
      </div>

      <p v-if="errorMsg" class="mt-3 text-xs text-destructive">{{ errorMsg }}</p>
      <p v-if="successMsg" class="mt-3 text-xs text-emerald-600 dark:text-emerald-400">{{ successMsg }}</p>

      <div class="mt-4 text-center">
        <button class="text-[11px] text-muted-foreground hover:text-foreground" @click="logout">退出登录</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '../stores/auth'
import { useLedgerStore } from '../stores/ledger'
import LandlordApplyFields from '../components/LandlordApplyFields.vue'
import { submitLandlordApply, type LandlordApplyInput } from '../api/rental'

// 注册后的「选择身份」页（2026-11-25 用户定调：注册只建普通账号，身份进来再选）。
//
// 三种用途（不是后端角色，只是给新账号指路）：
//   房东   → 可当场提交入驻资料（审核后才进业务区），也可先不填直接进控制台；
//   业务员 → 被房东邀请进台账、按模块勾权限的协作成员（抄表员、收费员…），
//            不需要任何资料，把用户名给房东等他邀请；不是管理员；
//   租户   → 账号保持普通用户（多角色，不切换），把用户名告诉房东等他关联档案；
//            关联后侧栏出现「租客门户」入口，门户里也能一键返回后台。
// 页面本身不做权限判断，路由守卫会处理「已是租户」「待审核」等状态。
const router = useRouter()
const authStore = useAuthStore()
const ledgerStore = useLedgerStore()

type RolePick = 'landlord' | 'manager' | 'tenant'
const picked = ref<RolePick>('landlord')
const roleOptions: { value: RolePick; label: string; hint: string }[] = [
  { value: 'landlord', label: '房东', hint: '管自己的房源' },
  { value: 'manager', label: '业务员', hint: '部分权限协作' },
  { value: 'tenant', label: '租户', hint: '只看自己的' },
]

const applyForm = ref<LandlordApplyInput>({ name: '', phone: '', wechat: '', region: '', room_scale: '', intro: '' })
const applyMore = ref(false)
const busy = ref(false)
const errorMsg = ref('')
const successMsg = ref('')

onMounted(() => {
  // 待审核用户也可能被送到这里；默认选中房东，等他补交资料。
  if (authStore.isTenant) void router.replace({ name: 'PortalPending' })
})

async function enterConsole() {
  errorMsg.value = ''
  await ledgerStore.load(true).catch(() => {})
  if (authStore.isPendingUser) {
    await router.push({ name: 'PendingApproval' })
    return
  }
  await router.push({ name: 'Home' })
}

async function submitApplyAndEnter() {
  const name = applyForm.value.name.trim()
  const phone = (applyForm.value.phone || '').trim()
  if (!name || !phone) {
    errorMsg.value = '请至少填写姓名与联系电话（也可以先选「暂不填写」）'
    return
  }
  busy.value = true
  errorMsg.value = ''
  try {
    const res = await submitLandlordApply({
      name,
      phone,
      wechat: (applyForm.value.wechat || '').trim(),
      region: (applyForm.value.region || '').trim(),
      room_scale: applyForm.value.room_scale || '',
      intro: (applyForm.value.intro || '').trim(),
    })
    if (res.data?.code === 0) {
      successMsg.value = '入驻资料已提交，等待平台审核'
      await enterConsole()
    } else {
      errorMsg.value = res.data?.message || '提交失败，请稍后重试'
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.message || '提交失败，请稍后重试'
  } finally {
    busy.value = false
  }
}

async function logout() {
  await authStore.logout()
  await router.push({ name: 'Login' })
}
</script>
