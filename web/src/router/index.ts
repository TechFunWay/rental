import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { useRentalAccessStore } from '../stores/rentalAccess'
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

function firstViewableRoute(access: ReturnType<typeof useRentalAccessStore>): string | null {
  if (access.canView('analytics')) return 'Home'
  for (const [mod, name] of editRouteOrder) {
    if (access.canEdit(mod)) return name
  }
  for (const [mod, name] of viewRouteOrder) {
    if (access.canView(mod)) return name
  }
  return null
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
  } else if (to.meta.requiresAdmin && !authStore.isAdmin) {
    next({ name: 'Home' })
  } else if (to.meta.requiresAccess && authStore.isAuthenticated) {
    // 业务权限门禁：先拿到勾选再放行（首次会等一次接口），任一模块都没有
    // 查看权的用户回落到个人资料页——登录后看到的第一个页面就是它。
    // 页面带 module 的，还要有该模块的查看权：没有就引到第一个可见的
    // 业务页（多人分工时各人落地各自的岗位页，如抄表员直达抄表）。
    const access = useRentalAccessStore()
    await access.ensureLoaded()
    if (!access.canRead) {
      next({ name: 'Profile' })
      return
    }
    const mod = to.meta.module as ModuleKey | undefined
    if (mod && !access.canView(mod)) {
      const first = firstViewableRoute(access)
      next(first ? { name: first } : { name: 'Profile' })
      return
    }
    next()
  } else {
    next()
  }
})

export default router
