// stores/ledger.ts — 当前台账（个人台账 / 共同台账）状态。
//
// 0.5.2 起业务数据按台账归属，前端必须回答两个问题：
//   1. 我现在在看哪个台账？（数据范围：请求带 ?ledger_id=）
//   2. 我在这个台账里有什么权限？（菜单/按钮门禁：currentPerms）
//
// 选择结果持久化到 localStorage（SK.currentLedger），刷新后仍停在同一个台账。
// 台账列表里的 status=pending 是**待接受的邀请**，不能选中，只能在切换器里
// 接受或拒绝。
//
// **这是前端唯一的权限口径**：路由守卫的页面门禁、侧栏菜单显隐与页面内按钮
// 显隐全部读这里的 canView/canEdit/canFull——菜单亮着就一定点得动。
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getMyLedgers, type LedgerView } from '../api/ledger'
import type { ModuleKey, ModulePerm, ModulePerms } from '../api/rental'
import { useAuthStore } from './auth'
import { SK } from '../utils/storage-keys'
import { PERM_RANK, levelLabelOf } from '../utils/perm'

/** 从 localStorage 读上次选择的台账；读不到或不可解析时返回空串。 */
function readStored(): string {
  try {
    return localStorage.getItem(SK.currentLedger) || ''
  } catch {
    // 隐私模式下 localStorage 不可用：本次会话内仍可切换，只是不持久化。
    return ''
  }
}

function writeStored(value: string) {
  try {
    if (value) localStorage.setItem(SK.currentLedger, value)
    else localStorage.removeItem(SK.currentLedger)
  } catch {
    /* 忽略 */
  }
}

export const useLedgerStore = defineStore('ledger', () => {
  const authStore = useAuthStore()

  const ledgers = ref<LedgerView[]>([])
  /** '' = 尚未选择（首屏拉取前），其余为台账 id 的十进制字符串 */
  const selection = ref<string>(readStored())
  const loading = ref(false)
  const loadedFor = ref<number | null>(null)
  let loadingPromise: Promise<void> | null = null

  /** 我能实际使用的台账（正式成员；待接受邀请不算）。 */
  const activeLedgers = computed(() => ledgers.value.filter((l) => l.status === 'active'))

  /** 待处理的邀请（别人请我加入的共同台账）。 */
  const pendingInvites = computed(() => ledgers.value.filter((l) => l.status === 'pending'))

  /** 当前选中的台账；没有可用台账时为 null。 */
  const current = computed<LedgerView | null>(() => {
    const id = Number(selection.value)
    if (!selection.value || !Number.isInteger(id)) return null
    return ledgers.value.find((l) => l.id === id) ?? null
  })

  const currentLedgerId = computed<number | null>(() => current.value?.id ?? null)

  /** 当前台账是不是我自己的个人台账。 */
  const isPersonal = computed(() => current.value?.kind === 'personal')

  /** 我是不是当前台账的管理者（共同台账的创建者 / 个人台账的本人）。 */
  const isOwner = computed(() => current.value?.role === 'owner')

  /** 我在当前台账里的模块权限（管理者由后端直接给全部 full）。 */
  const currentPerms = computed<ModulePerms>(() => current.value?.perms ?? {})

  /** 进业务区的底线：有任何一个能用的台账。 */
  const canRead = computed(() => activeLedgers.value.length > 0)

  /** 无任何台账归属（异常数据）——前端据此提示「等待分配」。 */
  const hasNoLedger = computed(() => ledgers.value.length === 0)

  /** 用户卡上的档位短语：按**当前台账**给（管理者恒为「完全」）。 */
  const levelLabel = computed(() => levelLabelOf(currentPerms.value))

  // ---- 权限判定（前端唯一口径：当前台账） ----

  /** tier(m)：当前台账里该模块的档位；none = 未勾选。 */
  function tier(m: ModuleKey): ModulePerm | 'none' {
    return currentPerms.value[m] ?? 'none'
  }

  function atLeast(m: ModuleKey, p: ModulePerm): boolean {
    const cur = currentPerms.value[m]
    return (PERM_RANK[cur ?? ('none' as ModulePerm)] ?? 0) >= PERM_RANK[p]
  }

  const canView = (m: ModuleKey) => atLeast(m, 'view')
  const canEdit = (m: ModuleKey) => atLeast(m, 'edit')
  const canFull = (m: ModuleKey) => atLeast(m, 'full')

  /**
   * 把 selection 收敛到本次可用的台账里：
   *   - 只有一个可用台账 → 直接锁定它；
   *   - 存的选择还有效 → 保留（显式选择优先）；
   *   - 否则落**个人台账**（新模型下每个人都有自己的台账，默认就该在这里），
   *     没有个人台账再退到第一个可用台账；
   *   - 一个可用台账都没有（只有待接受邀请）→ 清空选择。
   */
  function normalizeSelection() {
    const list = activeLedgers.value
    if (list.length === 0) {
      if (selection.value) {
        selection.value = ''
        writeStored('')
      }
      return
    }
    if (list.length === 1) {
      const only = String(list[0].id)
      if (selection.value !== only) {
        selection.value = only
        writeStored(only)
      }
      return
    }
    const id = Number(selection.value)
    if (selection.value && list.some((l) => l.id === id)) return
    const fallback = list.find((l) => l.kind === 'personal') ?? list[0]
    const next = String(fallback.id)
    if (selection.value !== next) {
      selection.value = next
      writeStored(next)
    }
  }

  /**
   * 拉取我的台账列表（按当前用户缓存一次，换账号自动重拉）。
   * 待审核用户也能调（该接口在白名单里），返回他的个人台账。
   */
  async function load(force = false): Promise<void> {
    const uid = Number(authStore.user?.id) || 0
    if (!force && loadedFor.value !== null && loadedFor.value === uid) return
    if (loadingPromise) {
      await loadingPromise
      return
    }
    loading.value = true
    loadingPromise = (async () => {
      try {
        const res = await getMyLedgers()
        if (res.data?.code === 0) {
          ledgers.value = Array.isArray(res.data.data) ? res.data.data : []
          normalizeSelection()
        }
      } catch {
        // 拉取失败保持原样：业务接口的 403 仍是兜底，不在前端把用户挡死。
      } finally {
        loadedFor.value = uid
        loading.value = false
      }
    })()
    await loadingPromise
    loadingPromise = null
  }

  /** 确保已加载（路由守卫与首屏调用，重复调用只发一次请求）。 */
  async function ensureLoaded(): Promise<void> {
    await load()
  }

  /**
   * 切换台账：只改前端状态与持久化值，页面自己在 watch 到变化后重新拉数据
   * （MainLayout 把 RouterView 的 key 绑到了 selection 上，会整体重挂载）。
   */
  function switchLedger(id: number | string | null) {
    if (id === null) return
    const next = String(id)
    if (next === selection.value) return
    selection.value = next
    writeStored(next)
  }

  /** 退出登录时清掉选择，避免下一个账号继承上一个账号的台账。 */
  function reset() {
    ledgers.value = []
    loadedFor.value = null
    loadingPromise = null
    selection.value = ''
    writeStored('')
  }

  return {
    // 状态
    ledgers,
    selection,
    loading,
    // 派生
    activeLedgers,
    pendingInvites,
    current,
    currentLedgerId,
    currentPerms,
    isPersonal,
    isOwner,
    canRead,
    hasNoLedger,
    levelLabel,
    // 权限
    tier,
    canView,
    canEdit,
    canFull,
    // 动作
    load,
    ensureLoaded,
    switchLedger,
    reset,
  }
})

// ---------- 供 api 层使用（不带 store 实例，避免模块循环引用） ----------

/**
 * currentLedgerQuery 返回业务请求应当附带的 ledger_id。
 *
 * 为什么放在 store 外面：api/rental.ts 的请求函数要用它，而 store 又 import
 * 了 api/ledger.ts。若 api/rental.ts 直接 import store，就会形成
 * api/rental → stores/ledger → api/ledger → api/rental 的模块循环，Vite 在
 * 开发态下会给出未初始化的绑定。这里只依赖 localStorage 与固定的键，
 * 读的是 ledger store 写入的同一份值，语义完全一致。
 *
 * 返回 null 表示「还没选好台账」（首屏极短窗口/没有归属）——后端此时按
 * 我的全部台账并集查询、写入落个人台账；正常情况下前端一定会带上它。
 */
export function currentLedgerQuery(): number | null {
  const raw = readStored()
  if (!raw) return null
  const n = Number(raw)
  return Number.isInteger(n) && n >= 0 ? n : null
}
