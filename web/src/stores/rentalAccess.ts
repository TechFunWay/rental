import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { getMyAccess, type AccessLevel } from '../api/rental'
import { useAuthStore } from './auth'

// 业务权限 store：数据共享后，谁能看、谁能录由管理员按用户授权。
// 等级从 GET /api/rental/access/me 拉一次缓存住；管理员恒为 full，
// 不需要等接口返回就能看到全部菜单。
const RANK: Record<AccessLevel, number> = { none: 0, readonly: 1, edit: 2, full: 3 }

const LEVEL_LABEL: Record<AccessLevel, string> = {
  none: '无权限',
  readonly: '只读',
  edit: '录入',
  full: '完全',
}

export const useRentalAccessStore = defineStore('rentalAccess', () => {
  const authStore = useAuthStore()
  const level = ref<AccessLevel>('none')
  const loadedFor = ref<number | null>(null)
  let loading: Promise<void> | null = null

  const effective = computed<AccessLevel>(() => (authStore.isAdmin ? 'full' : level.value))
  const canRead = computed(() => RANK[effective.value] >= RANK.readonly)
  const canEdit = computed(() => RANK[effective.value] >= RANK.edit)
  const canFull = computed(() => RANK[effective.value] >= RANK.full)
  const levelLabel = computed(() => LEVEL_LABEL[effective.value])

  // ensureLoaded 按当前用户拉一次权限（换账号自动重拉）；请求失败按 none 处理，
  // 后续接口的 403 兜底保证不越权。
  async function ensureLoaded(): Promise<void> {
    const uid = (authStore.user?.id as number | undefined) ?? null
    if (loadedFor.value === uid && loadedFor.value !== null) return
    if (!loading) {
      loading = (async () => {
        try {
          const res = await getMyAccess()
          if (res.data?.code === 0) {
            level.value = (res.data.data?.level || 'none') as AccessLevel
          }
        } catch { /* 保持 none */ }
        loadedFor.value = uid
      })()
    }
    await loading
    loading = null
  }

  return { level, effective, canRead, canEdit, canFull, levelLabel, ensureLoaded }
})
