import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useLedgerStore } from '../stores/ledger'
import type { ModuleKey } from '../api/rental'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'Login',
      component: () => import('../views/LoginView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/register',
      name: 'Register',
      component: () => import('../views/RegisterView.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/forgot-password',
      name: 'ForgotPassword',
      component: () => import('../views/ForgotPasswordView.vue'),
      meta: { requiresAuth: false }
    },
    {
      // 租客自助登记（公开）：凭房源 token 收集身份证照片与联系方式，
      // 不在 /admin 布局内——租客手机直接打开，无任何业务入口。
      path: '/apply/:token',
      name: 'TenantApply',
      component: () => import('../views/ApplyView.vue'),
      meta: { requiresAuth: false }
    },
    {
      // 待审核（status === 2）用户的落地页：独立于 /admin 布局，没有侧栏与
      // 任何业务入口，只有一句说明与退出登录。登录态仍然成立（pending 用户
      // 可以登录），只是业务接口一律 403。
      path: '/pending',
      name: 'PendingApproval',
      component: () => import('../views/PendingApprovalView.vue'),
      meta: { requiresAuth: true, allowPending: true }
    },
    {
      // 后台路由统一前缀为 /admin，需要登录。业务页面还要业务权限
      // （requiresAccess：无权限的用户只能留在个人资料/偏好设置）。
      path: '/admin',
      component: () => import('../layouts/MainLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'Home', component: () => import('../views/DashboardView.vue'), meta: { requiresAccess: true, module: 'analytics' } },
        { path: 'rooms', name: 'RentalRooms', component: () => import('../views/RoomsView.vue'), meta: { requiresAccess: true, module: 'rooms' } },
        { path: 'tenants', name: 'RentalTenants', component: () => import('../views/TenantsView.vue'), meta: { requiresAccess: true, module: 'tenants' } },
        { path: 'bills', name: 'RentalBills', component: () => import('../views/BillsView.vue'), meta: { requiresAccess: true, module: 'bills' } },
        { path: 'meters', name: 'RentalMeters', component: () => import('../views/MeterRecordsView.vue'), meta: { requiresAccess: true, module: 'meters' } },
        { path: 'payments', name: 'RentalPayments', component: () => import('../views/PaymentsView.vue'), meta: { requiresAccess: true, module: 'payments' } },
        { path: 'analytics', name: 'RentalAnalytics', component: () => import('../views/AnalyticsView.vue'), meta: { requiresAccess: true, module: 'analytics' } },
        { path: 'profile', name: 'Profile', component: () => import('../views/ProfileView.vue') },
        { path: 'settings', name: 'Settings', component: () => import('../views/SettingsView.vue') },
        { path: 'users', name: 'AdminUsers', component: () => import('../views/AdminUsersView.vue'), meta: { requiresAdmin: true } },
        { path: 'configs', name: 'AdminConfigs', component: () => import('../views/AdminConfigView.vue'), meta: { requiresAdmin: true } },
        { path: 'backups', name: 'AdminBackups', component: () => import('../views/AdminBackupView.vue'), meta: { requiresAdmin: true } },
        { path: 'audit', name: 'AdminAudit', component: () => import('../views/AdminAuditView.vue'), meta: { requiresAdmin: true } },
      ]
    },
    {
      // 后期 / 用于免登录的门户或前端页面，当前先重定向到后台首页。
      path: '/',
      redirect: '/admin',
      meta: { requiresAuth: false }
    }
  ]
})

// 权限回落时的落地规则：能看总览先总览；否则落到第一个有「操作」权的
// 模块页（岗位优先，如抄表员直达抄表）；再不行找第一个有查看权的页面；
// 全都没有才回落个人资料。
//
// 判定用 **当前台账** 的权限（ledgerStore），与页面级的 module 门禁同一口径；
// 每个台账各判各的；历史上菜单用跨台账并集判定，会让
// 「列表页显示空数据、菜单却是亮的」，两边必须同口径。
// 侧栏菜单与页面内按钮也读同一个 store（MainLayout / 各 View），
// 所以「菜单亮着就点得动」。
const editRouteOrder: [ModuleKey, string][] = [
  ['meters', 'RentalMeters'],
  ['bills', 'RentalBills'],
  ['rooms', 'RentalRooms'],
  ['tenants', 'RentalTenants'],
  ['payments', 'RentalPayments'],
]
const viewRouteOrder: [ModuleKey, string][] = [
  ['rooms', 'RentalRooms'],
  ['tenants', 'RentalTenants'],
  ['meters', 'RentalMeters'],
  ['bills', 'RentalBills'],
  ['payments', 'RentalPayments'],
]

interface ModuleGate {
  canView: (m: ModuleKey) => boolean
  canEdit: (m: ModuleKey) => boolean
}

function firstViewableRoute(gate: ModuleGate): string | null {
  if (gate.canView('analytics')) return 'Home'
  for (const [mod, name] of editRouteOrder) {
    if (gate.canEdit(mod)) return name
  }
  for (const [mod, name] of viewRouteOrder) {
    if (gate.canView(mod)) return name
  }
  return null
}

/**
 * 当前台账里第一个能进的页面（没有则 null → 个人资料页）。
 * 守卫与「切换台账后重新判定当前页面」共用同一份回落规则，避免两处各写一套。
 */
export function landingRouteName(gate: ModuleGate): string | null {
  return firstViewableRoute(gate)
}

router.beforeEach(async (to, from, next) => {
  const authStore = useAuthStore()
  await authStore.init()

  const fnosEnabled = import.meta.env.VITE_FNOS_APP === 'true'
  if (authStore.setupRequired && to.name !== 'Register') {
    next({
      name: 'Register',
      query: fnosEnabled ? { fnos: 'bind', fnos_mode: 'register' } : {},
    })
    return
  }

  if (to.meta.requiresAuth !== false && !authStore.isAuthenticated && authStore.requireLogin) {
    next({ name: 'Login' })
    return
  }

  // 我的台账列表：业务页面门禁、侧栏切换器与「是否有台账可进」都依赖它。
  // 首次会等一次 /rental/ledgers/mine（之后按用户缓存），待审核用户也允许调用
  // （该接口在 pending 白名单里，返回空列表）。放在这里加载，保证下面任何
  // 分支看到的数据都是完整的。
  const ledgerStore = useLedgerStore()
  if (authStore.isAuthenticated) {
    await ledgerStore.ensureLoaded()
  }

  // ---- 待审核（status === 2）门禁 ----
  //
  // 判定依据是登录态里的 user.status：登录/注册响应带它，/api/auth/check
  // 回填的 user 也带它（框架 users.status 字段），/rental/users/me/approval
  // 在等待页里还会再自证一次。**不能**靠业务接口的 403 反推——403 同时意味着
  // 「模块无权限」，会把正常用户误判成待审核。
  //
  // 待审核用户唯一能进的页面是 /pending（该页自己也会再自证一次状态）；
  // 其余任何路由（含 /admin/**）一律重定向过去。管理员恒不受此限制。
  if (authStore.isAuthenticated && !authStore.isAdmin && authStore.isPendingUser) {
    if (to.name !== 'PendingApproval') {
      next({ name: 'PendingApproval' })
      return
    }
    next()
    return
  }
  // 已不是待审核（管理员刚审批通过）却停在 /pending：放回业务区。
  if (to.name === 'PendingApproval' && !authStore.isPendingUser) {
    next({ name: 'Home' })
    return
  }

  if (to.meta.requiresAdmin && !authStore.isAdmin) {
    next({ name: 'Home' })
  } else if (to.meta.requiresAccess && authStore.isAuthenticated) {
    // 业务权限门禁：判定口径 = **当前台账**（ledgerStore，上面已拉好），与侧栏
    // 菜单、页面内按钮完全同源；页面带 module 的还要有该模块的查看权，
    // 没有就引到第一个可见的业务页（多人分工时各人落地各自的岗位页），
    // 一个模块都看不到的用户回落到个人资料页。
    //
    // 门禁按**当前选中台账**的权限判定，切换台账后守卫会重新算一遍；
    // 侧栏菜单历史上用的是跨台账并集
    // （rentalAccess store），会造成「菜单亮着、点进去被弹回个人资料页」，
    // 已统一改读本 store。
    const mod = to.meta.module as ModuleKey | undefined
    if (mod && !ledgerStore.canView(mod)) {
      const first = firstViewableRoute(ledgerStore)
      // 回落目标与当前目标相同时不再跳转（避免守卫里出现无限重定向：
      // 例如用户没有任何模块权限，first 为 null，直接落个人资料页）。
      if (first && first !== to.name) {
        next({ name: first })
        return
      }
      if (to.name !== 'Profile') {
        next({ name: 'Profile' })
        return
      }
    }
    next()
  } else {
    next()
  }
})

export default router
