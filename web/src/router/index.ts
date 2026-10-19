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
      // 免登录的**房源登记**（村委/中介场景）：凭台账级 token 打开，村民自己登记房源。
      // 与 /apply/:token（房源级 token 的租客登记）是两条并存的免登录链路。
      path: '/room-apply/:token',
      name: 'RoomApply',
      component: () => import('../views/RoomApplyView.vue'),
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
      // 租户（role === 'tenant'）待关联页（v0.6.10）：租户注册后 status=2，
      // 但**与房东的「待审核」不是一回事**——他要等房东在租户管理里把账号关联
      // 到某条租户档案。语义不同所以页面独立，免得租户看到「等待管理员审核」
      // 却不知道该找谁做关联。
      path: '/portal/pending',
      name: 'PortalPending',
      component: () => import('../views/portal/PortalPendingView.vue'),
      meta: { requiresAuth: true, role: 'tenant' }
    },
    {
      // 租户门户（v0.6.10）：只读自己的登记资料、账单、缴费记录与合同，
      // 以及提交资料变更申请。独立布局（TenantLayout），与房东的 /admin 完全分开。
      path: '/portal',
      component: () => import('../layouts/TenantLayout.vue'),
      meta: { requiresAuth: true, role: 'tenant' },
      children: [
        { path: '', name: 'PortalHome', component: () => import('../views/portal/PortalHomeView.vue') },
        { path: 'bills', name: 'PortalBills', component: () => import('../views/portal/PortalBillsView.vue') },
        { path: 'payments', name: 'PortalPayments', component: () => import('../views/portal/PortalPaymentsView.vue') },
        { path: 'contracts', name: 'PortalContracts', component: () => import('../views/portal/PortalContractsView.vue') },
      ]
    },
    {
      // 后台路由统一前缀为 /admin，需要登录。业务页面还要业务权限
      // （requiresAccess：无权限的用户只能留在个人资料/偏好设置）。
      // v0.6.10：整体带 requiresLandlord —— 租户角色一律被送回门户。
      path: '/admin',
      component: () => import('../layouts/MainLayout.vue'),
      meta: { requiresAuth: true, requiresLandlord: true },
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
  //
  // **租户不拉**：租户不是任何台账的成员，列表恒为空，拉它只是白白多一次
  // 请求（还要过一遍限流）。租户分支在下面独立处理。
  const ledgerStore = useLedgerStore()
  if (authStore.isAuthenticated && !authStore.isTenant) {
    await ledgerStore.ensureLoaded()
  }

  // ---- 租户（role === 'tenant'）门禁（v0.6.10） ----
  //
  // 租户与出租人是**两套界面**：租户只能进 /portal/**，出租人只能进 /admin/**。
  // 放在待审核判定**之前**：租户注册后同样是 status=2，但语义是「等房东关联」
  // 而不是「等管理员审核」，先按角色分流才能把他送到正确的等待页。
  if (authStore.isAuthenticated && authStore.isTenant) {
    const inPortal = to.meta.role === 'tenant'
    // 待关联（status === 2）：唯一能进的是「等待房东关联」页。
    if (authStore.isPendingUser) {
      if (to.name !== 'PortalPending') {
        next({ name: 'PortalPending' })
        return
      }
      next()
      return
    }
    // 已关联却停在等待页 → 放回门户首页。
    if (to.name === 'PortalPending') {
      next({ name: 'PortalHome' })
      return
    }
    // 已关联：只放行门户内的页面，其余（含 /admin/**、/login）一律送回门户。
    if (!inPortal) {
      next({ name: 'PortalHome' })
      return
    }
    next()
    return
  }

  // ---- 待审核（status === 2）门禁 ----
  //
  // 判定依据是登录态里的 user.status：登录/注册响应带它，/api/auth/check
  // 回填的 user 也带它（框架 users.status 字段），/rental/users/me/approval
  // 在等待页里还会再自证一次。**不能**靠业务接口的 403 反推——403 同时意味着
  // 「模块无权限」，会把正常用户误判成待审核。
  //
  // 走到这里的一定不是租户（上面已按角色分流）：待审核用户唯一能进的页面是
  // /pending（该页自己也会再自证一次状态）；其余任何路由（含 /admin/**）
  // 一律重定向过去。管理员恒不受此限制。
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
  // 租户的等待页不该被非租户打开（直接输 URL 的情形）→ 回业务区。
  if (to.name === 'PortalPending') {
    next({ name: 'Home' })
    return
  }

  if (to.meta.requiresAdmin && !authStore.isAdmin) {
    next({ name: 'Home' })
  } else if (to.meta.requiresLandlord && !authStore.isLandlord) {
    // 纵深防御：租户已在上面被分流，这里防的是「将来新增角色忘了纳管」。
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
