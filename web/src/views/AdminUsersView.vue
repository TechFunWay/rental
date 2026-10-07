<template>
  <div class="page-container animate-fade-in">
    <!-- Stat cards -->
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <div class="surface rounded-2xl p-5">
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm text-muted-foreground">用户总数</div>
            <div class="text-2xl font-extrabold text-foreground mt-1">{{ total }}</div>
          </div>
          <div class="w-11 h-11 rounded-xl bg-brand-50 dark:bg-brand-500/15 text-brand-600 dark:text-brand-300 flex items-center justify-center">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-3.13a4 4 0 10-4-4 4 4 0 004 4z"/></svg>
          </div>
        </div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm text-muted-foreground">管理员</div>
            <div class="text-2xl font-extrabold text-foreground mt-1">{{ adminCount }}</div>
          </div>
          <div class="w-11 h-11 rounded-xl bg-violet-50 dark:bg-violet-500/15 text-violet-600 dark:text-violet-300 flex items-center justify-center">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 12l2 2 4-4m5.618-4.016A11.955 11.955 0 0112 2.944a11.955 11.955 0 01-8.618 3.04A12.02 12.02 0 003 9c0 5.591 3.824 10.29 9 11.622 5.176-1.332 9-6.03 9-11.622 0-1.042-.133-2.052-.382-3.016z"/></svg>
          </div>
        </div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm text-muted-foreground">已启用</div>
            <div class="text-2xl font-extrabold text-foreground mt-1">{{ activeCount }}</div>
          </div>
          <div class="w-11 h-11 rounded-xl bg-emerald-50 dark:bg-emerald-500/15 text-emerald-600 dark:text-emerald-300 flex items-center justify-center">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
          </div>
        </div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm text-muted-foreground">已禁用</div>
            <div class="text-2xl font-extrabold text-foreground mt-1">{{ Math.max(total - activeCount, 0) }}</div>
          </div>
          <div class="w-11 h-11 rounded-xl bg-rose-50 dark:bg-rose-500/15 text-rose-600 dark:text-rose-300 flex items-center justify-center">
            <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636"/></svg>
          </div>
        </div>
      </div>
    </div>

    <!-- Main panel -->
    <div class="surface rounded-2xl overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-3 p-5 sm:p-6 border-b border-border">
        <div>
          <h2 class="text-lg font-bold text-foreground">用户列表</h2>
          <p class="text-sm text-muted-foreground">管理账户并按人勾选模块权限（查看 / 操作 / 删除，管理员默认全部）</p>
        </div>
        <div class="flex items-center gap-2 w-full sm:w-auto">
          <div class="relative flex-1 sm:flex-none">
            <svg class="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/></svg>
            <input
              v-model="search"
              type="text"
              placeholder="搜索用户名..."
              class="input-field !pl-10 sm:w-64"
              @keyup.enter="reload"
            />
          </div>
          <button @click="reload" class="btn-brand whitespace-nowrap">搜索</button>
        </div>
      </div>

      <div class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-xs uppercase tracking-wider text-muted-foreground bg-muted/60">
              <th class="py-3.5 px-4 font-semibold">用户</th>
              <th class="py-3.5 px-4 font-semibold">角色</th>
              <th class="py-3.5 px-4 font-semibold">业务权限</th>
              <th class="py-3.5 px-4 font-semibold">状态</th>
              <th class="py-3.5 px-4 font-semibold">创建时间</th>
              <th class="py-3.5 px-6 font-semibold text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="user in users" :key="user.id" class="hover:bg-muted/50 transition-colors">
              <td class="py-3.5 px-6">
                <div class="flex items-center gap-3">
                  <div class="w-9 h-9 rounded-full flex items-center justify-center text-white font-bold text-xs shrink-0" :class="avatarClass(user.username)">
                    {{ user.username.charAt(0).toUpperCase() }}
                  </div>
                  <div>
                    <div class="font-semibold text-foreground">{{ user.username }}</div>
                    <div class="text-xs text-muted-foreground">ID #{{ user.id }}</div>
                  </div>
                </div>
              </td>
              <td class="py-3.5 px-4">
                <span v-if="user.role === 'admin'" class="badge bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300">
                  <svg class="w-3 h-3" fill="currentColor" viewBox="0 0 20 20"><path d="M10 1l2.39 4.84L18 6.66l-4 3.9.94 5.5L10 13.5l-4.94 2.56.94-5.5-4-3.9 5.61-.82L10 1z"/></svg>
                  管理员
                </span>
                <span v-else class="badge bg-muted text-muted-foreground">普通用户</span>
              </td>
              <td class="py-3.5 px-4">
                <!-- 业务权限按模块勾选；管理员恒为全部，不弹窗 -->
                <span v-if="user.role === 'admin'" class="text-xs text-muted-foreground whitespace-nowrap">全部</span>
                <button
                  v-else
                  class="text-xs font-semibold text-brand-600 dark:text-brand-300 hover:underline whitespace-nowrap"
                  title="按模块勾选：查看 / 操作 / 删除"
                  @click="openPerms(user)"
                >{{ permsLabel(user) }}</button>
              </td>
              <td class="py-3.5 px-4">
                <span v-if="user.status === 1" class="badge bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
                  <span class="w-1.5 h-1.5 rounded-full bg-emerald-500"></span>启用
                </span>
                <span v-else class="badge bg-rose-100 text-rose-700 dark:bg-rose-500/15 dark:text-rose-300">
                  <span class="w-1.5 h-1.5 rounded-full bg-rose-500"></span>禁用
                </span>
              </td>
              <td class="py-3.5 px-4 text-muted-foreground whitespace-nowrap">{{ user.created_at }}</td>
              <td class="py-3.5 px-6">
                <div class="flex items-center justify-end gap-1">
                  <button @click="handleToggleStatus(user)" :title="user.status === 1 ? '禁用' : '启用'" class="icon-btn hover:text-amber-600 hover:bg-amber-50 dark:hover:bg-amber-500/10">
                    <svg v-if="user.status === 1" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728L5.636 5.636"/></svg>
                    <svg v-else class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7"/></svg>
                  </button>
                  <button @click="handleResetPassword(user)" title="重置密码" class="icon-btn hover:text-brand-600 hover:bg-brand-50 dark:hover:bg-brand-500/10">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"/></svg>
                  </button>
                  <button @click="handleDelete(user)" title="删除" class="icon-btn hover:text-rose-600 hover:bg-rose-50 dark:hover:bg-rose-500/10">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="users.length === 0">
              <td colspan="6" class="py-16 text-center">
                <div class="flex flex-col items-center gap-3 text-muted-foreground">
                  <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-3.13a4 4 0 10-4-4 4 4 0 004 4z"/></svg>
                  <span class="text-sm">暂无用户数据</span>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="flex flex-wrap items-center justify-between gap-3 p-5 sm:px-6 border-t border-border">
        <div class="text-sm text-muted-foreground">
          共 <span class="font-semibold text-foreground">{{ total }}</span> 条 · 第 {{ page }} / {{ totalPages }} 页
        </div>
        <div class="flex items-center gap-2">
          <button @click="prevPage" :disabled="page <= 1" class="btn-ghost !px-3 !py-2">上一页</button>
          <button @click="nextPage" :disabled="page >= totalPages" class="btn-ghost !px-3 !py-2">下一页</button>
        </div>
      </div>
    </div>

    <!-- Reset password modal -->
    <transition enter-active-class="transition-opacity duration-200" enter-from-class="opacity-0" leave-active-class="transition-opacity duration-150" leave-to-class="opacity-0">
      <div v-if="resetModalVisible" class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-foreground/50 backdrop-blur-sm" @click.self="resetModalVisible = false">
        <div class="surface rounded-2xl shadow-2xl p-6 w-full max-w-md animate-scale-in">
          <div class="flex items-center gap-3 mb-5">
            <div class="w-11 h-11 rounded-xl bg-brand-50 dark:bg-brand-500/15 text-brand-600 dark:text-brand-300 flex items-center justify-center">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 7a2 2 0 012 2m4 0a6 6 0 01-7.743 5.743L11 17H9v2H7v2H4a1 1 0 01-1-1v-2.586a1 1 0 01.293-.707l5.964-5.964A6 6 0 1121 9z"/></svg>
            </div>
            <div>
              <h3 class="text-lg font-bold text-foreground">重置密码</h3>
              <p class="text-sm text-muted-foreground">用户：{{ resetTarget?.username }}</p>
            </div>
          </div>
          <div class="space-y-4">
            <div>
              <label class="block text-sm font-medium text-foreground mb-1.5">新密码</label>
              <input v-model="resetNewPassword" type="password" placeholder="请输入新密码" class="input-field" @keyup.enter="confirmResetPassword" />
            </div>
            <div v-if="resetMsg" class="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{{ resetMsg }}</div>
            <div class="flex gap-3 pt-1">
              <button @click="resetModalVisible = false" class="btn-ghost flex-1">取消</button>
              <button @click="confirmResetPassword" :disabled="resetLoading" class="btn-brand flex-1">{{ resetLoading ? '重置中...' : '确认重置' }}</button>
            </div>
          </div>
        </div>
      </div>
    </transition>

    <!-- 模块权限勾选弹窗：勾"操作"自动包含查看，勾"删除"自动包含操作 -->
    <Modal v-model="permsModal" title="设置业务权限">
      <div v-if="permsTarget" class="space-y-4">
        <p class="text-xs text-muted-foreground leading-relaxed">
          给 <span class="font-semibold text-foreground">{{ permsTarget.username }}</span> 按模块勾选权限：
          勾「操作」自动包含查看，勾「删除」自动包含操作与 CSV 导入；收款登记在账单模块内。
        </p>
        <div class="flex flex-wrap gap-2">
          <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="applyPreset('view')">预设：只读</button>
          <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="applyPreset('edit')">预设：录入</button>
          <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="applyPreset('full')">预设：完全</button>
          <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="applyPreset(null)">全部清空</button>
        </div>
        <div class="overflow-x-auto rounded-xl border border-border">
          <table class="w-full text-sm">
            <thead>
              <tr class="bg-muted/60 text-xs text-muted-foreground">
                <th class="py-2.5 px-4 text-left font-semibold">模块</th>
                <th class="py-2.5 px-3 text-center font-semibold">查看</th>
                <th class="py-2.5 px-3 text-center font-semibold">操作</th>
                <th class="py-2.5 px-3 text-center font-semibold">删除</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="m in ALL_MODULES" :key="m" class="border-t border-border">
                <td class="py-2.5 px-4 font-medium text-foreground">{{ MODULE_LABEL[m] }}</td>
                <td class="py-2.5 px-3 text-center">
                  <input type="checkbox" class="w-4 h-4 accent-brand-600"
                         :checked="boxGet(m, 'view')" @change="boxSet(m, 'view', ($event.target as HTMLInputElement).checked)" />
                </td>
                <td class="py-2.5 px-3 text-center">
                  <input type="checkbox" class="w-4 h-4 accent-brand-600"
                         :checked="boxGet(m, 'edit')" @change="boxSet(m, 'edit', ($event.target as HTMLInputElement).checked)" />
                </td>
                <td class="py-2.5 px-3 text-center">
                  <input type="checkbox" class="w-4 h-4 accent-brand-600"
                         :checked="boxGet(m, 'full')" @change="boxSet(m, 'full', ($event.target as HTMLInputElement).checked)" />
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
      <div class="flex gap-3 pt-1">
        <button class="btn-ghost flex-1" @click="permsModal = false">取消</button>
        <button class="btn-brand flex-1" :disabled="savingAccess !== null" @click="savePerms">
          {{ savingAccess !== null ? '保存中…' : '保存' }}
        </button>
      </div>
    </Modal>

    <ConfirmDialog
      :model-value="!!deleteTarget"
      title="删除用户"
      :message="`确定删除用户 ${deleteTarget?.username}？`"
      confirm-type="danger"
      @update:model-value="deleteTarget = null"
      @confirm="doDeleteUser"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Modal from '../components/Modal.vue'
import { getUsers, toggleUserStatus, deleteUser, resetUserPassword } from '../api/user'
import { getAccessList, setAccess, type ModuleKey, type ModulePerm, type ModulePerms } from '../api/rental'
import { ALL_MODULES, MODULE_LABEL } from '../stores/rentalAccess'
import { passwordValidationError } from '../utils/password'
import { toast } from '../utils/toast'

const users = ref<any[]>([])
const search = ref('')
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const totalPages = computed(() => Math.ceil(total.value / pageSize.value) || 1)

// ---- 业务权限授权（数据共享后按人按模块勾选） ----
const PERM_RANK: Record<ModulePerm, number> = { view: 1, edit: 2, full: 3 }
const accessMap = ref<Record<number, ModulePerms>>({})
const savingAccess = ref<number | null>(null)
const permsModal = ref(false)
const permsTarget = ref<any>(null)
const permsDraft = ref<ModulePerms>({})

// 列表里的摘要：常见勾选组合给可读名，混合勾选叫「自定义」。
function permsLabel(user: any): string {
  const p = accessMap.value[user.id] ?? {}
  const entries = Object.entries(p)
  if (entries.length === 0) return '未授权'
  const all = (t: ModulePerm) => ALL_MODULES.every((m) => p[m] === t)
  if (all('full')) return '完全'
  if (all('edit')) return '录入'
  if (all('view')) return '只读'
  if (entries.length === 2 && p.meters === 'edit' && p.rooms === 'view') return '抄表'
  return '自定义'
}

function openPerms(user: any) {
  permsTarget.value = user
  permsDraft.value = { ...(accessMap.value[user.id] ?? {}) }
  permsModal.value = true
}

function tierOf(m: ModuleKey): ModulePerm | null {
  return permsDraft.value[m] ?? null
}
function setTier(m: ModuleKey, tier: ModulePerm | null) {
  const next = { ...permsDraft.value }
  if (tier) next[m] = tier
  else delete next[m]
  permsDraft.value = next
}
function boxGet(m: ModuleKey, tier: ModulePerm): boolean {
  const cur = tierOf(m)
  return cur !== null && PERM_RANK[cur] >= PERM_RANK[tier]
}
function boxSet(m: ModuleKey, tier: ModulePerm, on: boolean) {
  if (on) {
    if (PERM_RANK[tierOf(m) ?? 'view'] < PERM_RANK[tier]) setTier(m, tier)
    return
  }
  // 取消勾选：只降到该档的前一档（勾选包含关系同步收窄）。
  if (PERM_RANK[tierOf(m) ?? 'view'] >= PERM_RANK[tier]) {
    const below: ModulePerm | null = tier === 'full' ? 'edit' : tier === 'edit' ? 'view' : null
    setTier(m, below)
  }
}
function applyPreset(tier: ModulePerm | null) {
  const next: ModulePerms = {}
  if (tier) for (const m of ALL_MODULES) next[m] = tier
  permsDraft.value = next
}

async function loadAccess() {
  try {
    const res = await getAccessList()
    if (res.data?.code === 0) {
      const map: Record<number, ModulePerms> = {}
      for (const row of res.data.data ?? []) {
        map[row.user_id] = (row.perms ?? {}) as ModulePerms
      }
      accessMap.value = map
    }
  } catch { /* 读取失败按未授权展示 */ }
}

async function savePerms() {
  const user = permsTarget.value
  if (!user) return
  savingAccess.value = user.id
  try {
    const res = await setAccess(user.id, permsDraft.value)
    if (res.data?.code === 0) {
      accessMap.value = { ...accessMap.value, [user.id]: permsDraft.value }
      toast(`已保存 ${user.username} 的权限`)
      permsModal.value = false
    } else {
      toast(res.data?.message || '授权失败', 'error')
    }
  } catch (e: any) {
    toast(e?.response?.data?.message || '授权失败', 'error')
  } finally {
    savingAccess.value = null
  }
}

const adminCount = computed(() => users.value.filter((u) => u.role === 'admin').length)
const activeCount = computed(() => users.value.filter((u) => u.status === 1).length)

const resetModalVisible = ref(false)
const resetTarget = ref<any>(null)
const resetNewPassword = ref('')
const resetLoading = ref(false)
const resetMsg = ref('')

const avatarPalette = [
  'bg-gradient-to-br from-indigo-500 to-purple-500',
  'bg-gradient-to-br from-sky-500 to-blue-600',
  'bg-gradient-to-br from-emerald-500 to-teal-600',
  'bg-gradient-to-br from-amber-500 to-orange-600',
  'bg-gradient-to-br from-pink-500 to-rose-600',
  'bg-gradient-to-br from-violet-500 to-fuchsia-600',
]
function avatarClass(name: string) {
  let h = 0
  for (let i = 0; i < name.length; i++) h = name.charCodeAt(i) + ((h << 5) - h)
  return avatarPalette[Math.abs(h) % avatarPalette.length]
}

onMounted(() => {
  loadUsers()
  loadAccess()
})

async function loadUsers() {
  try {
    const res = await getUsers(page.value, pageSize.value, search.value || undefined)
    if (res.data?.code === 0) {
      users.value = res.data.data?.list || res.data.data?.items || []
      total.value = res.data.data?.total || 0
    }
  } catch {}
}

function reload() {
  page.value = 1
  loadUsers()
}

function prevPage() {
  if (page.value > 1) {
    page.value--
    loadUsers()
  }
}

function nextPage() {
  if (page.value < totalPages.value) {
    page.value++
    loadUsers()
  }
}

async function handleToggleStatus(user: any) {
  try {
    const res = await toggleUserStatus(user.id)
    if (res.data?.code === 0) await loadUsers()
  } catch {}
}

const deleteTarget = ref<any>(null)

function handleDelete(user: any) {
  deleteTarget.value = user
}

async function doDeleteUser() {
  const user = deleteTarget.value
  if (!user) return
  try {
    const res = await deleteUser(user.id)
    if (res.data?.code === 0) {
      toast('用户已删除')
      await loadUsers()
    } else {
      toast(res.data?.message || '删除失败', 'error')
    }
  } catch (e: any) {
    toast(e?.response?.data?.message || '删除失败', 'error')
  }
}

function handleResetPassword(user: any) {
  resetTarget.value = user
  resetNewPassword.value = ''
  resetMsg.value = ''
  resetModalVisible.value = true
}

async function confirmResetPassword() {
  const validationError = passwordValidationError(resetNewPassword.value)
  if (validationError) {
    resetMsg.value = validationError
    return
  }
  resetLoading.value = true
  resetMsg.value = ''
  try {
    const res = await resetUserPassword(resetTarget.value.id, resetNewPassword.value)
    if (res.data?.code === 0) {
      resetModalVisible.value = false
    } else {
      resetMsg.value = res.data?.message || '重置失败'
    }
  } catch (err: any) {
    resetMsg.value = err.response?.data?.message || '网络错误'
  } finally {
    resetLoading.value = false
  }
}
</script>

<style scoped>
.icon-btn {
  @apply p-2 rounded-lg text-muted-foreground transition-colors;
}
</style>
