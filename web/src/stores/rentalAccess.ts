import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getMyAccess, type ModuleKey, type ModulePerm, type ModulePerms } from '../api/rental'
import { useAuthStore } from './auth'

// 业务权限 store：数据共享后，谁能看哪些模块、能做哪些操作由管理员按人
// 勾选（多人分工：抄表员只勾抄表、会计只勾账单收款……）。
// 勾选从 GET /api/rental/access/me 拉一次缓存住；管理员恒为全部模块
// 完全权限，不需要等接口返回就能看到全部菜单。
const PERM_RANK: Record<ModulePerm, number> = { view: 1, edit: 2, full: 3 }
const NONE_RANK = 0

export const MODULE_LABEL: Record<ModuleKey, string> = {
  rooms: '房源',
  tenants: '租户与合同',
  meters: '抄表',
  bills: '账单',
  payments: '收款记录',
  analytics: '统计分析',
}

// 管理员与预设识别共用的全集。
export const ALL_MODULES: ModuleKey[] = ['rooms', 'tenants', 'meters', 'bills', 'payments', 'analytics']
export const FULL_PERMS: ModulePerms = Object.fromEntries(ALL_MODULES.map((m) => [m, 'full'])) as ModulePerms

export const useRentalAccessStore = defineStore('rentalAccess', () => {
  const authStore = useAuthStore()
  const perms = ref<ModulePerms>({})
  const loadedFor = ref<number | null>(null)
  let loading: Promise<void> | null = null

  const effective = computed<ModulePerms>(() => (authStore.isAdmin ? FULL_PERMS : perms.value))

  /** tier(m)：模块当前档位；none = 未勾选 */
  function tier(m: ModuleKey): ModulePerm | 'none' {
    return effective.value[m] ?? 'none'
  }
  function atLeast(m: ModuleKey, p: ModulePerm): boolean {
    return (PERM_RANK[effective.value[m] ?? ('none' as ModulePerm)] ?? NONE_RANK) >= PERM_RANK[p]
  }

  const canView = (m: ModuleKey) => atLeast(m, 'view')
  const canEdit = (m: ModuleKey) => atLeast(m, 'edit')
  const canFull = (m: ModuleKey) => atLeast(m, 'full')
  // 任一模块可看：进业务区（含通知渠道绑定这类个人配置）的底线。
  const canRead = computed(() => ALL_MODULES.some((m) => atLeast(m, 'view')))

  // 用户卡上的档位短语：常见预设给可读名，混合勾选叫「自定义」。
  const levelLabel = computed(() => {
    if (authStore.isAdmin) return '完全'
    const p = perms.value
    const entries = Object.entries(p) as [ModuleKey, ModulePerm][]
    if (entries.length === 0) return '无权限'
    const isPreset = (tier: ModulePerm) => ALL_MODULES.every((m) => p[m] === tier)
    if (isPreset('full')) return '完全'
    if (isPreset('edit')) return '录入'
    if (isPreset('view')) return '只读'
    if (entries.length === 2 && p.meters === 'edit' && p.rooms === 'view') return '抄表'
    return '自定义'
  })

  // ensureLoaded 按当前用户拉一次勾选（换账号自动重拉）；请求失败按全无
  // 处理，后续接口的 403 兜底保证不越权。
  async function ensureLoaded(): Promise<void> {
    const uid = (authStore.user?.id as number | undefined) ?? null
    if (loadedFor.value === uid && loadedFor.value !== null) return
    if (!loading) {
      loading = (async () => {
        try {
          const res = await getMyAccess()
          if (res.data?.code === 0) {
            perms.value = (res.data.data?.perms || {}) as ModulePerms
          }
        } catch { /* 保持全无 */ }
        loadedFor.value = uid
      })()
    }
    await loading
    loading = null
  }

  return { perms, effective, tier, canView, canEdit, canFull, canRead, levelLabel, ensureLoaded }
})
