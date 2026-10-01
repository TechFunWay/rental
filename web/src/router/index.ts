import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '../stores/auth'

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
      // 后台路由统一前缀为 /admin，需要登录。
      path: '/admin',
      component: () => import('../layouts/MainLayout.vue'),
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'Home', component: () => import('../views/DashboardView.vue') },
        { path: 'rooms', name: 'RentalRooms', component: () => import('../views/RoomsView.vue') },
        { path: 'tenants', name: 'RentalTenants', component: () => import('../views/TenantsView.vue') },
        { path: 'bills', name: 'RentalBills', component: () => import('../views/BillsView.vue') },
        { path: 'meters', name: 'RentalMeters', component: () => import('../views/MeterRecordsView.vue') },
        { path: 'payments', name: 'RentalPayments', component: () => import('../views/PaymentsView.vue') },
        { path: 'analytics', name: 'RentalAnalytics', component: () => import('../views/AnalyticsView.vue') },
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
  } else {
    next()
  }
})

export default router
