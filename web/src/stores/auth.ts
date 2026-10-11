import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { checkAuth, checkSetupRequired as apiCheckSetupRequired, getCurrentUser, logout as apiLogout } from '../api/auth'
import { getPublicConfigs, getUserConfigMeta } from '../api/config'
import { getSecurityQuestions } from '../api/security'
import { getMyApproval } from '../api/ledger'
import { useThemeStore } from './theme'
import { SK } from '../utils/storage-keys'
import { onFnOSGatewayOrigin } from '../utils/gateway'

export const useAuthStore = defineStore('auth', () => {
  const token = ref(localStorage.getItem(SK.token) || '')
  const user = ref<any>(null)
  const requireLogin = ref(true)
  const allowRegister = ref(true)
  // 注册手机号策略（超管可配）：enabled 控制注册页显不显示输入框，
  // required 控制是否必填。默认都开（管理员要能看出谁是谁）。
  const registerPhoneEnabled = ref(true)
  const registerPhoneRequired = ref(true)
  const setupRequired = ref(false)
  // 站名兜底值：服务端 /api/configs/public 返回前的首屏用它，必须是本应用的中文名，
  // 否则侧栏与浏览器标题会先闪出框架默认的英文名。
  const siteTitle = ref('租房管理')
  // 当前登录态从哪来，由服务端在 checkAuth 里判定（只有服务端知道这次请求是
  // 不是走网关 socket、以及有没有应用自己的凭证）：
  //   gateway —— 仅凭网关注入的 NAS 身份成立，会话实际归 NAS 所有，NAS 那侧
  //              退出后应用必须跟着退出（服务端会拒绝，此时自动登出并回登录页）；
  //   app     —— 应用自己签发的会话（显式登录或直连端口），可以独立退出，
  //              也完全不需要跟随 NAS。
  // 当前只存起来不使用；后续「飞牛侧退出后应用跟随退出」会以它为判据。
  const sessionSource = ref<'app' | 'gateway'>('app')
  watch(siteTitle, (t) => { document.title = t }, { immediate: true })
  const hasSecurityQuestions = ref(true)
  const DISMISS_KEY = SK.securityPromptDismissed
  const DISMISS_TTL = 60 * 60 * 1000 // 1 hour
  const securityPromptDismissed = ref((() => {
    const ts = localStorage.getItem(DISMISS_KEY)
    if (!ts) return false
    return Date.now() - Number(ts) < DISMISS_TTL
  })())
  /** 待审核（status === 2）：可以登录，但业务接口一律 403。
   *  来源是登录/注册响应里的 user.status 与 /api/auth/check 回填的 user，
   *  不依赖业务接口的 403（403 也意味着「模块无权限」，会把正常用户误判）。 */
  const isPendingUser = computed(() => Number(user.value?.status) === 2)

  let initialized = false
  let initPromise: Promise<void> | null = null

  // 网关域下登录态由服务端回填 user：有账密登录种下的会话 cookie 就认 cookie
  // 对应的账号，否则用网关注入的 NAS 身份认「已绑定的应用账号」（本地无 token
  // 也要查），故以 user 为准；直连端口 user 也只在 checkAuth 通过后才设置，
  // 等价于原来的 !!token && !!user。
  const isAuthenticated = computed(() => !!user.value)
  const isAdmin = computed(() => user.value?.role === 'admin')

  function clearSession() {
    token.value = ''
    user.value = null
    localStorage.removeItem(SK.token)
  }

  async function init() {
    if (initialized) return
    if (initPromise) return initPromise

    initPromise = (async () => {
      try {
        const configsRes = await getPublicConfigs()
        if (configsRes.data?.code === 0) {
          requireLogin.value = configsRes.data.data?.require_login !== 'false'
          allowRegister.value = configsRes.data.data?.allow_register !== 'false'
          // 注册手机号：超管可在后台关掉收集、或改成选填（默认收集且必填）。
          // 关闭收集时必填一并作废，否则会出现「输入框不显示却要求填」的死锁。
          const phoneEnabled = configsRes.data.data?.register_phone_enabled !== 'false'
          registerPhoneEnabled.value = phoneEnabled
          registerPhoneRequired.value = phoneEnabled && configsRes.data.data?.register_phone_required !== 'false'
          if (configsRes.data.data?.site_title) {
            siteTitle.value = configsRes.data.data.site_title
          }
        }
      } catch {}

      await checkSetupRequired()

      if (setupRequired.value && token.value) {
        clearSession()
        user.value = null
      }

      // 网关域没有本地令牌也要继续：checkAuth 会带回「网关注入身份对应的应用
      // 账号」，刷新后正是靠它保住登录态（见 utils/gateway.ts）。
      if (token.value || onFnOSGatewayOrigin()) {
        try {
          const res = await checkAuth()
          if (res.data?.code === 0 && res.data.data?.authenticated) {
            user.value = res.data.data.user
            // 服务端只在本应用确实靠网关注入身份登录时才回 gateway；其余情况
            // （直连端口、应用自己的令牌）都算应用会话。
            sessionSource.value = res.data.data?.session_source === 'gateway' ? 'gateway' : 'app'
            try {
              const secRes = await getSecurityQuestions()
              if (secRes.data?.code === 0) {
                hasSecurityQuestions.value = !!secRes.data.data?.has_questions
              }
            } catch {}
            try {
              const metaRes = await getUserConfigMeta()
              if (metaRes.data?.code === 0 && Array.isArray(metaRes.data.data)) {
                const themeItem = metaRes.data.data.find((it: any) => it.key === 'theme_mode')
                if (themeItem && ['system', 'light', 'dark'].includes(themeItem.value)) {
                  const themeStore = useThemeStore()
                  if (themeStore.mode !== themeItem.value) {
                    themeStore.setMode(themeItem.value)
                  }
                }
              }
            } catch {}
          } else if (await restorePendingSession()) {
            // 待审核（status === 2）用户的会话在刷新后的**恢复**路径。
            //
            // 背景：/auth/check 只认 status === 1 的账号（pending 用户在那里
            // 拿到 authenticated:false、user:null），但令牌本身是有效的——
            // /auth/me、/rental/users/me/approval、/rental/ledgers/mine 这些
            // pending 白名单接口仍然返回 200。只按 checkAuth 判就会把待审核
            // 用户直接登出到登录页，永远看不到「等待审核」页（实测踩到）。
            //
            // 所以这里用白名单里的 /rental/users/me/approval 兜一层：它能同时
            // 给出权威的 status 与用户名。仅当服务端确认 pending 时才认这个
            // 会话，其余情况（令牌失效/账号被禁用）照旧清本地。
          } else {
            clearSession()
          }
        } catch {
          clearSession()
        }
      }
      initialized = true
    })()

    return initPromise
  }

  // restorePendingSession 在 checkAuth 未认下会话时，用待审核白名单接口
  // 判断「是不是一个仍然有效的待审核会话」。返回 true 表示已恢复登录态
  // （user 里带 status === 2，路由守卫据此把人送到 /pending）。
  //
  // 网关域上不带应用令牌时不该走这条路（那里的身份由服务端从网关注入头解析），
  // 所以只在确实有本地令牌时尝试。
  async function restorePendingSession(): Promise<boolean> {
    if (!token.value) return false
    try {
      const res = await getMyApproval()
      const d = res.data?.data
      if (res.data?.code !== 0 || !d?.pending) return false
      // 用户名不在该接口的返回里，用 /auth/me（同样在白名单内）补齐；
      // 失败时不阻塞：只要 status 对，页面与守卫就够用了。
      let username = ''
      let id = 0
      try {
        const meRes = await getCurrentUser()
        if (meRes.data?.code === 0) {
          id = Number(meRes.data.data?.id) || 0
          username = String(meRes.data.data?.username || '')
        }
      } catch {}
      user.value = { id, username, role: 'user', status: d.status }
      sessionSource.value = 'app'
      return true
    } catch {
      return false
    }
  }

  function resetInit() {
    initialized = false
    initPromise = null
  }

  function setToken(newToken: string) {
    token.value = newToken
    localStorage.setItem(SK.token, newToken)
  }

  function setUser(newUser: any) {
    user.value = newUser
  }

  // 退出登录（用户点击）。必须先让服务端记下「这位用户主动登出了」，再清本地：
  // 网关域上服务端每个请求都能从 X-Trim-Userid 认出应用账号，只清前端的话
  // 下一个请求就把登录态认回来了，用户看到的正是「退不出去」。请求失败也要
  // 继续本地登出——宁可本地先退出、下次刷新由服务端再纠正，也不能把用户卡在
  // 已登录界面里。
  async function endSession() {
    try {
      await apiLogout()
    } catch {}
    logout()
  }

  // 只清本地登录态（401 拦截器等同步路径用）。用户主动点退出请用 endSession()：
  // 它会先落服务端抑制标记再走到这里。
  function logout() {
    token.value = ''
    user.value = null
    localStorage.removeItem(SK.token)
    localStorage.removeItem(DISMISS_KEY)
    securityPromptDismissed.value = false
    sessionSource.value = 'app'
  }

  async function checkSetupRequired() {
    try {
      const setupRes = await apiCheckSetupRequired()
      if (setupRes.data?.code === 0) {
        setupRequired.value = !!setupRes.data.data?.setup_required
      }
    } catch {}
    return setupRequired.value
  }

  function dismissSecurityPrompt() {
    securityPromptDismissed.value = true
    localStorage.setItem(DISMISS_KEY, String(Date.now()))
  }

  function refreshSecurityQuestions() {
    if (!token.value) return
    getSecurityQuestions()
      .then(res => {
        if (res.data?.code === 0) {
          hasSecurityQuestions.value = !!res.data.data?.has_questions
          if (hasSecurityQuestions.value) {
            localStorage.removeItem(DISMISS_KEY)
          }
        }
      })
      .catch(() => {})
  }

  return { token, user, requireLogin, allowRegister, registerPhoneEnabled, registerPhoneRequired, setupRequired, siteTitle, hasSecurityQuestions, securityPromptDismissed, sessionSource, isAuthenticated, isAdmin, isPendingUser, init, resetInit, setToken, setUser, endSession, logout, checkSetupRequired, dismissSecurityPrompt, refreshSecurityQuestions }
})
