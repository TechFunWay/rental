<template>
  <AuthShell :title="pageTitle" :subtitle="pageSubtitle">
    <!-- register disabled -->
    <div v-if="disabled" class="flex flex-col items-center text-center gap-3 rounded-2xl border border-white/10 bg-white/[0.03] px-4 py-6 sm:px-6 sm:py-10">
      <span class="w-10 h-10 sm:w-12 sm:h-12 rounded-full bg-amber-400/15 text-amber-300 flex items-center justify-center">
        <svg class="w-5 h-5 sm:w-6 sm:h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 15v2m-6 4h12a2 2 0 002-2v-6a2 2 0 00-2-2H6a2 2 0 00-2 2v6a2 2 0 002 2zm10-10V7a4 4 0 00-8 0v4h8z"/></svg>
      </span>
      <p class="text-xs sm:text-sm text-white/70">注册功能已关闭，请联系管理员</p>
    </div>

    <form v-else @submit.prevent="handleRegister" class="space-y-3 sm:space-y-5">
      <div v-if="isFnOSBinding && !authStore.setupRequired" class="rounded-xl sm:rounded-2xl border border-brand-400/30 bg-brand-400/10 px-3.5 py-2.5 sm:px-5 sm:py-4 text-xs sm:text-sm leading-5 sm:leading-6 text-white/85">
        当前飞牛 NAS 用户 <span class="font-semibold text-brand-200">{{ fnosUsername || '已登录用户' }}</span> 尚未绑定应用账号。
        <template v-if="fnosMode === 'bind'">请输入当前使用的应用账号密码；绑定后会继续使用该账号中的数据与设置。</template>
        <template v-else>创建后将成为一个数据独立的新账号；如果已有应用数据，请改为绑定已有账号。</template>
      </div>
      <div v-if="authStore.setupRequired" class="flex flex-col gap-2 rounded-xl sm:rounded-2xl border border-brand-400/30 bg-brand-400/10 px-3.5 py-2.5 sm:px-5 sm:py-4 text-xs sm:text-sm">
        <div class="flex items-center gap-2 font-semibold text-brand-300">
          <svg class="w-4 h-4 sm:w-5 sm:h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
          首次使用：使用飞牛 NAS 创建管理员
        </div>
        <p class="text-white/80 leading-5">当前还没有任何用户。创建后该账号将自动成为管理员，并立即绑定当前飞牛 NAS 用户，之后可直接一键登录。</p>
      </div>

      <!-- 身份选择（v0.6.10）：房东与租客是两套完全不同的入口，注册时就要定下来。
           只在「正常注册」时出现——飞牛绑定流程是给已有账号做验证，不涉及选身份；
           首个管理员的初始化也不能是租客。 -->
      <div v-if="showRolePicker" class="space-y-1.5">
        <div class="text-xs font-semibold text-white/70">我是</div>
        <div class="grid grid-cols-2 gap-2">
          <button
            v-for="opt in roleOptions"
            :key="opt.value"
            type="button"
            class="rounded-xl border px-3 py-2.5 text-left transition-colors"
            :class="role === opt.value
              ? 'border-brand-400/60 bg-brand-400/15 text-white'
              : 'border-white/10 bg-white/[0.03] text-white/60 hover:bg-white/[0.06]'"
            @click="role = opt.value"
          >
            <div class="text-xs sm:text-sm font-semibold">{{ opt.label }}</div>
            <div class="mt-0.5 text-[11px] leading-4 text-white/50">{{ opt.hint }}</div>
          </button>
        </div>
        <p v-if="role === 'tenant'" class="text-[11px] leading-5 text-white/55">
          注册后用这个账号登录，房东在「租户管理」里关联你之后，就能查看自己的登记信息、账单与合同。
        </p>
        <!-- 房东入驻资料（平台化，v0.6.10）：申请通过后才能管理房源与租户 -->
        <div v-else class="rounded-xl border border-white/10 bg-white/[0.03] px-3 py-2.5 space-y-2.5">
          <div class="text-xs font-semibold text-white/70">入驻申请</div>
          <p class="text-[11px] leading-5 text-white/50">
            平台需要核实房东身份，填好资料后提交；通过前账号可以登录但看不到业务数据。
          </p>
          <LandlordApplyFields v-model="applyForm" />
        </div>
      </div>

      <AuthField v-model="username" label="用户名" autocomplete="username" required placeholder="请输入用户名" autofocus>
        <template #icon>
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z"/></svg>
        </template>
      </AuthField>

      <AuthField v-model="password" label="密码" type="password" autocomplete="new-password" required placeholder="请输入密码">
        <template #icon>
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><rect x="5" y="11" width="14" height="9" rx="2" stroke-width="2"/><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M8 11V8a4 4 0 118 0v3"/></svg>
        </template>
      </AuthField>

      <AuthField v-if="!isFnOSBinding || fnosMode === 'register'" v-model="confirmPassword" label="确认密码" type="password" autocomplete="new-password" required placeholder="请再次输入密码">
        <template #icon>
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
        </template>
      </AuthField>

      <!-- 手机号：超管可配「是否收集」与「是否必填」（默认收集且必填）。
           用途只是让管理员在用户列表里认出谁是谁——不做登录凭据、不做唯一校验，
           所以提示语要写清，别让用户以为它是账号的一部分。
           刻意**不传 required**：那会变成原生 HTML5 校验，浏览器自己弹气泡并把
           提交拦在 form 之外，我们的错误提示根本没机会显示，风格也不统一。
           必填判断放在 handleRegister 里，用应用内提示条展示。 -->
      <AuthField
        v-if="showPhoneField"
        v-model="phone"
        label="手机号"
        type="tel"
        autocomplete="tel"
        placeholder="请输入手机号（方便管理员识别）"
      >
        <template #icon>
          <svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><rect x="7" y="2.5" width="10" height="19" rx="2.5" stroke-width="2"/><path stroke-linecap="round" stroke-width="2" d="M11 18.5h2"/></svg>
        </template>
      </AuthField>

      <transition enter-active-class="transition duration-200" enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition duration-150" leave-to-class="opacity-0">
        <div v-if="errorMsg" class="flex items-center gap-2 text-xs sm:text-sm text-red-300 bg-red-500/10 border border-red-500/25 rounded-xl px-3 py-2 sm:px-3.5 sm:py-2.5">
          <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01M5.07 19h13.86a2 2 0 001.74-3L13.74 4a2 2 0 00-3.48 0L3.34 16a2 2 0 001.73 3z"/></svg>
          {{ errorMsg }}
        </div>
      </transition>
      <transition enter-active-class="transition duration-200" enter-from-class="opacity-0 -translate-y-1" leave-active-class="transition duration-150" leave-to-class="opacity-0">
        <div v-if="successMsg" class="flex items-center gap-2 text-xs sm:text-sm text-emerald-300 bg-emerald-500/10 border border-emerald-500/25 rounded-xl px-3 py-2 sm:px-3.5 sm:py-2.5">
          <svg class="w-4 h-4 shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z"/></svg>
          {{ successMsg }}
        </div>
      </transition>

      <button type="submit" :disabled="loading" class="btn-premium">
        <svg v-if="loading" class="w-5 h-5 animate-spin" fill="none" viewBox="0 0 24 24"><circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/><path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/></svg>
        {{ loading ? '处理中...' : submitLabel }}
      </button>

      <button v-if="isFnOSBinding && !authStore.setupRequired" type="button" :disabled="loading" @click="fnosMode = fnosMode === 'register' ? 'bind' : 'register'" class="w-full text-xs sm:text-sm font-medium text-brand-300 hover:text-brand-200 transition-colors">
        {{ fnosMode === 'register' ? '已有应用账号？验证并绑定' : '没有应用账号？创建并绑定' }}
      </button>
    </form>

    <template #footer v-if="!authStore.setupRequired">
      已有账号？
      <router-link to="/login" class="font-semibold text-brand-300 hover:text-brand-200 transition-colors">立即登录</router-link>
    </template>
  </AuthShell>
</template>

<script setup lang="ts">
import { ref, onMounted, computed } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { register, bindFnOSAccount } from '../api/auth'
import { getPublicConfigs } from '../api/config'
import { useAuthStore } from '../stores/auth'
import { useLedgerStore } from '../stores/ledger'
import AuthShell from '../components/auth/AuthShell.vue'
import AuthField from '../components/auth/AuthField.vue'
import LandlordApplyFields from '../components/LandlordApplyFields.vue'
import { passwordValidationError } from '../utils/password'
import { submitLandlordApply, type LandlordApplyInput } from '../api/rental'

const router = useRouter()
const route = useRoute()
const authStore = useAuthStore()

const username = ref(typeof route.query.fnos_username === 'string' ? route.query.fnos_username : '')
const password = ref('')
const confirmPassword = ref('')
const phone = ref('')
const loading = ref(false)
const errorMsg = ref('')
const successMsg = ref('')
const disabled = ref(false)
const fnosMode = ref<'register' | 'bind'>(route.query.fnos_mode === 'bind' ? 'bind' : 'register')
// 注册身份（v0.6.10）：'' = 房东（出租人，默认）、'tenant' = 租客。
// 默认房东：绝大多数注册者是房东，且默认值必须是「功能最全」的那个，
// 免得老用户升级后随手注册却拿不到业务界面。
const role = ref<'' | 'tenant'>(route.query.role === 'tenant' ? 'tenant' : '')
// 房东入驻资料（平台化）：注册成功后立刻提交；失败时留在本页可重试
const applyForm = ref<LandlordApplyInput>({ name: '', phone: '', wechat: '', region: '', room_scale: '', intro: '' })
const applySaved = ref(false)
// 账号已建好、但入驻资料没提交成功：显示「重试提交」按钮（不用重新注册）
const applyRetry = ref(false)
const roleOptions = [
  // admin / user / tenant 三个角色：平台首个注册账号自动成为系统管理员，
  // 之后的「房东」注册需要提交入驻资料并由平台审核（landlord_applications）
  { value: '' as const, label: '房东', hint: '管理自己的房源、租户与账单' },
  { value: 'tenant' as const, label: '租户', hint: '只看自己的资料与账单' },
]
// 身份选择只在普通注册、且不是「初始化首个管理员」时出现：
// 首个账号必然是管理员（后端强制），让他选租客只会造成「选了没用」的困惑。
const showRolePicker = computed(() =>
  !isFnOSBinding.value && !authStore.setupRequired && authStore.tenantPortalEnabled)
const isFnOSBinding = computed(() => import.meta.env.VITE_FNOS_APP === 'true' && route.query.fnos === 'bind')
// 手机号字段只在「注册」流程里出现（绑定流程是在给已有账号做验证，不收集资料）。
// 「初始化首个管理员」时也显示，但**不标必填**——服务端对首个账号豁免必填
// （那时还没有管理员能进后台改配置），前端跟着放宽，免得填不填都心里没底。
const showPhoneField = computed(() =>
  authStore.registerPhoneEnabled && (!isFnOSBinding.value || fnosMode.value === 'register'))
const phoneIsRequired = computed(() =>
  authStore.registerPhoneRequired && !authStore.setupRequired)
const fnosUsername = computed(() => typeof route.query.fnos_username === 'string' ? route.query.fnos_username : '')
const pageTitle = computed(() => authStore.setupRequired
  ? (isFnOSBinding.value ? '使用飞牛 NAS 创建管理员' : '创建管理员账号')
  : (isFnOSBinding.value ? '绑定飞牛 NAS 账号' : '创建账号'))
const pageSubtitle = computed(() => authStore.setupRequired
  ? (isFnOSBinding.value ? '创建首个管理员账号，并绑定当前飞牛 NAS 用户' : '首次使用，请先完成管理员初始化')
  : (isFnOSBinding.value ? '创建或绑定应用账号，之后即可使用飞牛 NAS 一键登录' : '注册一个新账号以访问控制台'))
const submitLabel = computed(() => {
  if (authStore.setupRequired && isFnOSBinding.value) return '创建管理员并绑定飞牛 NAS'
  if (isFnOSBinding.value) return fnosMode.value === 'register' ? '创建并绑定' : '验证并绑定'
  return '注册'
})

onMounted(async () => {
  try {
    const res = await getPublicConfigs()
    if (res.data?.code === 0) {
      disabled.value = res.data.data?.allow_register === 'false' && !authStore.setupRequired && !isFnOSBinding.value
    }
  } catch {}
})

// submitApply 把入驻资料交给平台（待审核账号的接口在白名单里）。返回是否成功。
async function submitApply(): Promise<boolean> {
  applyForm.value.phone = (applyForm.value.phone || phone.value || '').trim()
  if (!applyForm.value.name.trim()) {
    errorMsg.value = '请填写入驻申请的姓名或称谓'
    return false
  }
  if (!/^[0-9+\- ]{5,20}$/.test(applyForm.value.phone)) {
    errorMsg.value = '请填写正确的联系电话'
    return false
  }
  try {
    const res = await submitLandlordApply({
      name: applyForm.value.name.trim(),
      phone: applyForm.value.phone,
      wechat: (applyForm.value.wechat || '').trim(),
      region: (applyForm.value.region || '').trim(),
      room_scale: applyForm.value.room_scale || '',
      intro: (applyForm.value.intro || '').trim(),
    })
    if (res.data?.code === 0) {
      applySaved.value = true
      return true
    }
    errorMsg.value = res.data?.message || '入驻资料提交失败'
    return false
  } catch (err: any) {
    errorMsg.value = err.response?.data?.message || '入驻资料提交失败，请重试'
    return false
  }
}

// retryApply 账号已建好、只是入驻资料没提交成功时的重试入口
async function retryApply() {
  loading.value = true
  errorMsg.value = ''
  const ok = await submitApply()
  loading.value = false
  if (ok) {
    successMsg.value = '入驻资料已提交，等待平台审核'
    void enterAfterAuth(authStore.token, authStore.user)
  }
}

async function handleRegister() {
  if (!isFnOSBinding.value || fnosMode.value === 'register') {
    const validationError = passwordValidationError(password.value)
    if (validationError) {
      errorMsg.value = validationError
      return
    }
  }
  if ((!isFnOSBinding.value || fnosMode.value === 'register') && password.value !== confirmPassword.value) {
    errorMsg.value = '两次密码不一致'
    return
  }
  // 手机号：必填时不能空；填了就要像手机号（后端会再校验一次，这里只是即时反馈）。
  // 允许用户带空格/短横线书写，后端会规范化。
  if (showPhoneField.value) {
    const digits = phone.value.replace(/[\s\-()·+]/g, '')
    if (phoneIsRequired.value && !digits) {
      errorMsg.value = '请填写手机号'
      return
    }
    if (digits && !/^\d{7,15}$/.test(digits)) {
      errorMsg.value = '手机号格式不正确（请填 7-15 位数字）'
      return
    }
  }
  loading.value = true
  errorMsg.value = ''
  successMsg.value = ''
  try {
    if (isFnOSBinding.value) {
      const res = await bindFnOSAccount(fnosMode.value, username.value, password.value)
      if (res.data?.code === 0) {
        await enterAfterAuth(res.data.data.token, res.data.data.user)
      } else {
        errorMsg.value = res.data?.message || '绑定失败'
      }
      return
    }
    const res = await register(
      username.value, password.value,
      showPhoneField.value ? phone.value : '',
      role.value,
    )
    if (res.data?.code === 0) {
      // 注册即登录：后端直接签发 token，无需再跳登录页
      const token = res.data.data?.token
      const user = res.data.data?.user
      // 房东路径：注册成功后立刻提交入驻资料（这一步失败不影响账号本身，
      // 留在本页重试即可——账号已建好，token 也已生效）
      if (token && user && user.role !== 'tenant') {
        authStore.setToken(token)
        authStore.setUser(user)
        if (!(await submitApply())) {
          applyRetry.value = true
          loading.value = false
          return
        }
      }
      if (token && user) {
        // v0.6.0 / v0.6.10：新注册账号可能是 status === 2，但**含义按角色不同**：
        //   租客 → 等待房东在租户管理里关联；
        //   房东 → 等待管理员审核。
        // 注册后直接落到各自的等待页，而不是进业务区再被 403 弹回来。
        const pending = Number(user.status) === 2
        if (user.role === 'tenant') {
          successMsg.value = pending ? '注册成功，等待房东关联你的账号' : '注册成功，正在进入租客服务'
        } else {
          successMsg.value = pending ? '注册成功，账号正在等待管理员审核' : '注册成功，正在进入控制台'
        }
        setTimeout(() => { void enterAfterAuth(token, user) }, 800)
      } else {
        successMsg.value = ''
        errorMsg.value = '注册成功但无法自动登录，请手动登录'
        setTimeout(() => router.push('/login'), 1500)
      }
    } else {
      errorMsg.value = res.data?.message || '注册失败'
    }
  } catch (err: any) {
    errorMsg.value = err.response?.data?.message || '网络错误'
  } finally {
    loading.value = false
  }
}

// enterAfterAuth 注册/绑定成功后的统一去向：先拉台账列表（ledger store 依赖
// 登录态），再按**角色 + 状态**分流。
//
// 顺序很重要：先判租户。租户与房东的 status === 2 语义不同（前者等房东关联、
// 后者等管理员审核），混在一起会把租户送进房东的等待页。
async function enterAfterAuth(token?: string, user?: any) {
  if (token) authStore.setToken(token)
  if (user) authStore.setUser(user)
  authStore.resetInit()
  if (authStore.isTenant) {
    // 租户没有台账，不必拉列表（后端也不会给他任何台账）。
    router.push({ name: authStore.isPendingUser ? 'PortalPending' : 'PortalHome' })
    return
  }
  const ledgerStore = useLedgerStore()
  await ledgerStore.load(true)
  if (authStore.isPendingUser) {
    router.push({ name: 'PendingApproval' })
    return
  }
  router.push('/admin')
}
</script>
