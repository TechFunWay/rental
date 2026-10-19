<template>
  <div class="page-container animate-fade-in">
    <!-- Stat cards -->
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
      <div class="surface rounded-2xl p-5">
        <div class="text-sm text-muted-foreground">用户总数</div>
        <div class="text-2xl font-extrabold text-foreground mt-1">{{ total }}</div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="text-sm text-muted-foreground">系统管理员</div>
        <div class="text-2xl font-extrabold text-foreground mt-1">{{ adminCount }}</div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="text-sm text-muted-foreground">已启用</div>
        <div class="text-2xl font-extrabold text-foreground mt-1">{{ activeCount }}</div>
      </div>
      <div class="surface rounded-2xl p-5">
        <div class="text-sm text-muted-foreground">待审核</div>
        <div class="text-2xl font-extrabold text-amber-600 dark:text-amber-300 mt-1">{{ pendingUsers.length }}</div>
      </div>
    </div>

    <!-- 房东入驻申请（平台化）：申请资料比账号本身更有用——姓名、电话、规模、地区。
         通过 = 账号可用 + 个人台账就绪；驳回 = 账号停用（可逆，保留申请行）。 -->
    <div v-if="landlordApps.length" class="surface rounded-2xl overflow-hidden border-brand-500/30">
      <div class="p-4 sm:p-6 border-b border-border">
        <h2 class="text-lg font-bold text-foreground flex items-center gap-2">
          <span class="w-2 h-2 rounded-full bg-brand-500"></span>
          房东入驻申请（{{ landlordApps.length }}）
        </h2>
        <p class="text-sm text-muted-foreground mt-1">
          房东在注册页填的入驻资料。通过后他能管理自己的房源与租户（数据在自己的个人台账里，
          与他人默认隔离）；驳回会把账号停用，之后想放行可在用户列表里重新启用。
        </p>
      </div>
      <ul class="divide-y divide-border">
        <li v-for="a in landlordApps" :key="a.id" class="px-4 sm:px-6 py-3.5 space-y-2">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="font-semibold text-foreground">
                {{ a.name }}
                <span class="text-xs font-normal text-muted-foreground ml-1.5">@{{ a.username }}</span>
              </div>
              <div class="text-xs text-muted-foreground tabular-nums mt-0.5">
                电话 {{ a.phone }}<template v-if="a.wechat"> · 微信 {{ a.wechat }}</template>
                <template v-if="a.region"> · {{ a.region }}</template>
                <template v-if="a.room_scale"> · 规模 {{ a.room_scale }}</template>
              </div>
              <div v-if="a.intro" class="text-xs text-muted-foreground mt-0.5">说明：{{ a.intro }}</div>
              <div class="text-[11px] text-muted-foreground mt-0.5">提交时间：{{ (a.created_at || '').slice(0, 10) }}</div>
            </div>
            <div class="flex items-center gap-2 shrink-0">
              <button class="btn-brand !py-1.5 !px-3 text-xs" @click="approveLandlord(a)">通过</button>
              <button class="btn-ghost !py-1.5 !px-3 text-xs text-destructive" @click="askRejectLandlord(a)">驳回</button>
            </div>
          </div>
        </li>
      </ul>
    </div>

    <!-- 待审核注册申请（status === 2） -->
    <div v-if="pendingUsers.length" class="surface rounded-2xl overflow-hidden border-amber-500/30">
      <div class="p-4 sm:p-6 border-b border-border">
        <h2 class="text-lg font-bold text-foreground flex items-center gap-2">
          <span class="w-2 h-2 rounded-full bg-amber-500"></span>
          待审核注册申请（{{ pendingUsers.length }}）
        </h2>
        <p class="text-sm text-muted-foreground mt-1">
          待审核账号可以登录，但看不到任何业务数据；通过后即可在自己的个人台账里开始使用，
          要参与别人的共同台账，由那个台账的管理者邀请。
        </p>
      </div>
      <ul class="divide-y divide-border">
        <li v-for="u in pendingUsers" :key="u.id" class="flex flex-wrap items-center justify-between gap-3 px-4 sm:px-6 py-3.5">
          <div class="min-w-0">
            <div class="font-semibold text-foreground truncate">{{ u.username }}</div>
            <div class="text-xs text-muted-foreground">
              注册时间：{{ (u.created_at || '').slice(0, 10) }}
              <template v-if="u.phone"> · 手机号 {{ u.phone }}</template>
            </div>
          </div>
          <div class="flex items-center gap-2">
            <button class="btn-brand !py-1.5 !px-3 text-xs" @click="openApprove(u)">通过</button>
            <button class="btn-ghost !py-1.5 !px-3 text-xs text-destructive" @click="askReject(u)">驳回</button>
          </div>
        </li>
      </ul>
    </div>

    <!-- 用户列表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div class="flex flex-wrap items-center justify-between gap-3 p-5 sm:p-6 border-b border-border">
        <div>
          <h2 class="text-lg font-bold text-foreground">用户列表</h2>
          <p class="text-sm text-muted-foreground">管理账号（启用/禁用、重置密码、删除）；业务权限按台账在各台账的「成员与权限」里维护</p>
        </div>
        <div class="flex items-center gap-2 w-full sm:w-auto">
          <div class="relative flex-1 sm:flex-none">
            <svg class="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-muted-foreground pointer-events-none" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"/></svg>
            <input v-model="search" type="text" placeholder="搜索用户名..." class="input-field !pl-10 sm:w-64" @keyup.enter="reload" />
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
                    <div class="text-xs text-muted-foreground">
                      ID #{{ user.id }}<template v-if="user.phone"> · {{ user.phone }}</template>
                    </div>
                  </div>
                </div>
              </td>
              <td class="py-3.5 px-4">
                <span class="badge" :class="roleBadgeClass(user.role)">{{ roleLabel(user.role) }}</span>
              </td>
              <td class="py-3.5 px-4">
                <span v-if="user.status === 2" class="badge bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300">
                  <span class="w-1.5 h-1.5 rounded-full bg-amber-500"></span>待审核
                </span>
                <span v-else-if="user.status === 1" class="badge bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300">
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
              <td colspan="5" class="py-16 text-center text-sm text-muted-foreground">暂无用户数据</td>
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

    <!-- 重置密码 -->
    <Modal v-model="resetModalVisible" title="重置密码">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground">用户：{{ resetTarget?.username }}</p>
        <div>
          <label class="block text-sm font-medium text-foreground mb-1.5">新密码</label>
          <input v-model="resetNewPassword" type="password" placeholder="请输入新密码" class="input-field" @keyup.enter="confirmResetPassword" />
        </div>
        <div v-if="resetMsg" class="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{{ resetMsg }}</div>
        <div class="flex gap-3 pt-1">
          <button @click="resetModalVisible = false" class="btn-ghost flex-1">取消</button>
          <button @click="confirmResetPassword" :disabled="resetLoading" class="btn-brand flex-1">{{ resetLoading ? '重置中…' : '确认重置' }}</button>
        </div>
      </div>
    </Modal>

    <!-- 通过注册申请 -->
    <Modal v-model="approveOpen" title="通过注册申请">
      <div v-if="approveTarget" class="space-y-4">
        <div class="rounded-xl border border-border bg-muted/50 px-3.5 py-3 space-y-1.5">
          <div class="flex items-center justify-between gap-3 text-sm">
            <span class="text-muted-foreground shrink-0">账号</span>
            <span class="font-semibold text-foreground truncate">{{ approveTarget.username }}</span>
          </div>
          <div class="flex items-center justify-between gap-3 text-sm">
            <span class="text-muted-foreground shrink-0">注册时间</span>
            <span class="text-foreground tabular-nums">{{ (approveTarget.created_at || '').slice(0, 10) }}</span>
          </div>
        </div>
        <p class="text-xs leading-5 text-muted-foreground">
          通过后他会立刻拿到自己的「个人台账」（只有他自己能看），可以开始录房源与账单。
          要让他参与某个共同台账，请到那个台账的「成员与权限」里邀请（对方接受后生效）。
        </p>
        <div v-if="approveError" class="text-sm text-destructive bg-destructive/10 px-3 py-2 rounded-lg">{{ approveError }}</div>
        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="approveOpen = false">取消</button>
          <button class="btn-brand flex-1" :disabled="approving" @click="confirmApprove">{{ approving ? '处理中…' : '通过' }}</button>
        </div>
      </div>
    </Modal>

    <ConfirmDialog
      :model-value="!!deleteTarget"
      title="删除用户"
      :message="`确定删除用户 ${deleteTarget?.username}？他必须没有台账数据、也不是共同台账的管理者，否则会被拒绝。`"
      confirm-type="danger"
      @update:model-value="deleteTarget = null"
      @confirm="doDeleteUser"
    />
    <ConfirmDialog
      :model-value="!!rejectTarget"
      title="驳回注册申请"
      :message="`确定驳回 ${rejectTarget?.username} 的注册申请？账号会被禁用并保留，之后可在本页重新启用。`"
      confirm-type="danger"
      confirm-text="驳回"
      @update:model-value="rejectTarget = null"
      @confirm="doReject"
    />
    <!-- 驳回房东入驻申请：账号停用但保留，平台之后可在用户列表里重新启用 -->
    <ConfirmDialog
      :model-value="!!rejectLandlordTarget"
      title="驳回入驻申请"
      :message="`确定驳回「${rejectLandlordTarget?.name || ''}（@${rejectLandlordTarget?.username || ''}）」的入驻申请？账号会被停用并保留，之后可在用户列表里重新启用。`"
      confirm-type="danger"
      confirm-text="驳回"
      @update:model-value="rejectLandlordTarget = null"
      @confirm="doRejectLandlord"
    />
  </div>
</template>

<script setup lang="ts">
// 用户管理：账号维度的操作（启用/禁用、重置密码、删除）+ 注册审核。
//
// 0.5.2 起业务权限不再是「按人一套全局权限」，而是「按台账一套成员权限」，
// 所以这一页不再有权限矩阵：在哪里能做什么，由各台账的管理者在
// 「成员与权限」里勾选（入口在顶栏的台账切换器里）。
import { ref, computed, onMounted } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Modal from '../components/Modal.vue'
import { getUsers, toggleUserStatus, deleteUser, resetUserPassword } from '../api/user'
import { approveUser, getPendingUsers, rejectUser, type PendingUser } from '../api/ledger'
import {
  approveLandlordApplication, getLandlordApplications, rejectLandlordApplication,
  type LandlordApplication,
} from '../api/rental'
import { roleBadgeClass, roleLabel } from '../utils/role'
import { passwordValidationError } from '../utils/password'
import { toast } from '../utils/toast'

const users = ref<any[]>([])
// 房东入驻申请（平台化）
const landlordApps = ref<LandlordApplication[]>([])
const rejectLandlordTarget = ref<LandlordApplication | null>(null)
const search = ref('')
const page = ref(1)
const pageSize = ref(10)
const total = ref(0)
const totalPages = computed(() => Math.ceil(total.value / pageSize.value) || 1)

const pendingUsers = ref<PendingUser[]>([])

const adminCount = computed(() => users.value.filter((u) => u.role === 'admin').length)
const activeCount = computed(() => users.value.filter((u) => u.status === 1).length)

function avatarClass(name: string): string {
  const palette = [
    'bg-rose-500', 'bg-amber-500', 'bg-emerald-500', 'bg-sky-500',
    'bg-violet-500', 'bg-fuchsia-500', 'bg-teal-500', 'bg-indigo-500',
  ]
  let sum = 0
  for (const ch of name) sum += ch.codePointAt(0) || 0
  return palette[sum % palette.length]
}

// ---- 列表 ----

async function loadLandlordApps() {
  try {
    const res = await getLandlordApplications('pending')
    landlordApps.value = res.data?.code === 0 ? (res.data.data ?? []) : []
  } catch {
    landlordApps.value = []
  }
}

async function approveLandlord(a: LandlordApplication) {
  const res = await approveLandlordApplication(a.id)
  if (res.data?.code !== 0) {
    toast(res.data?.message || '通过失败', 'error')
    return
  }
  toast(`已通过「${a.name}」的入驻申请，他现在可以管理自己的房源与租户`)
  await Promise.all([loadLandlordApps(), loadPending(), loadUsers()])
}

// 驳回入驻申请：复用本页既有的「驳回并写明原因」流程（rejectTarget + doReject），
// 只是目标换成申请行——房东拒了之后仍可被平台重新启用，所以保留账号行。
function askRejectLandlord(a: LandlordApplication) {
  rejectLandlordTarget.value = a
}

async function doRejectLandlord() {
  const a = rejectLandlordTarget.value
  if (!a) return
  rejectLandlordTarget.value = null
  try {
    const res = await rejectLandlordApplication(a.id, '')
    if (res.data?.code !== 0) {
      toast(res.data?.message || '驳回失败', 'error')
      return
    }
    toast('已驳回，账号已停用')
    await Promise.all([loadLandlordApps(), loadPending(), loadUsers()])
  } catch (e: any) {
    toast(e?.response?.data?.message || '驳回失败', 'error')
  }
}

async function loadUsers() {
  try {
    const res = await getUsers(page.value, pageSize.value, search.value || undefined)
    if (res.data?.code === 0) {
      users.value = res.data.data?.list || res.data.data?.items || []
      total.value = res.data.data?.total || 0
    }
  } catch { /* 静默 */ }
}

function reload() {
  page.value = 1
  loadUsers()
}

function prevPage() {
  if (page.value > 1) {
    page.value -= 1
    loadUsers()
  }
}

function nextPage() {
  if (page.value < totalPages.value) {
    page.value += 1
    loadUsers()
  }
}

async function handleToggleStatus(user: any) {
  try {
    const res = await toggleUserStatus(user.id)
    if (res.data?.code === 0) {
      toast(`已${user.status === 1 ? '禁用' : '启用'} ${user.username}`)
      await loadUsers()
    } else {
      toast(res.data?.message || '操作失败', 'error')
    }
  } catch (e: any) {
    toast(e?.response?.data?.message || '操作失败', 'error')
  }
}

const deleteTarget = ref<any>(null)
function handleDelete(user: any) {
  deleteTarget.value = user
}
async function doDeleteUser() {
  const u = deleteTarget.value
  if (!u) return
  try {
    const res = await deleteUser(u.id)
    if (res.data?.code === 0) {
      toast(`已删除 ${u.username}`)
      await loadUsers()
    } else {
      toast(res.data?.message || '删除失败', 'error')
    }
  } catch (e: any) {
    toast(e?.response?.data?.message || '删除失败', 'error')
  }
}

// ---- 重置密码 ----

const resetModalVisible = ref(false)
const resetTarget = ref<any>(null)
const resetNewPassword = ref('')
const resetMsg = ref('')
const resetLoading = ref(false)

function handleResetPassword(user: any) {
  resetTarget.value = user
  resetNewPassword.value = ''
  resetMsg.value = ''
  resetModalVisible.value = true
}

async function confirmResetPassword() {
  const err = passwordValidationError(resetNewPassword.value)
  if (err) {
    resetMsg.value = err
    return
  }
  resetLoading.value = true
  resetMsg.value = ''
  try {
    const res = await resetUserPassword(resetTarget.value.id, resetNewPassword.value)
    if (res.data?.code === 0) {
      toast('密码已重置')
      resetModalVisible.value = false
    } else {
      resetMsg.value = res.data?.message || '重置失败'
    }
  } catch (e: any) {
    resetMsg.value = e?.response?.data?.message || '重置失败'
  } finally {
    resetLoading.value = false
  }
}

// ---- 注册审核 ----

const approveOpen = ref(false)
const approveTarget = ref<PendingUser | null>(null)
const approveError = ref('')
const approving = ref(false)
const rejectTarget = ref<PendingUser | null>(null)

function openApprove(u: PendingUser) {
  approveTarget.value = u
  approveError.value = ''
  approveOpen.value = true
}

async function confirmApprove() {
  const u = approveTarget.value
  if (!u) return
  approving.value = true
  approveError.value = ''
  try {
    const res = await approveUser(u.id)
    if (res.data?.code === 0) {
      toast(`已通过 ${u.username} 的注册申请`)
      approveOpen.value = false
      await Promise.all([loadPending(), loadUsers()])
    } else {
      approveError.value = res.data?.message || '审批失败'
    }
  } catch (e: any) {
    approveError.value = e?.response?.data?.message || '审批失败'
  } finally {
    approving.value = false
  }
}

function askReject(u: PendingUser) {
  rejectTarget.value = u
}

async function doReject() {
  const u = rejectTarget.value
  if (!u) return
  try {
    const res = await rejectUser(u.id)
    if (res.data?.code === 0) {
      toast(res.data.data?.message || '已驳回该注册申请')
      await Promise.all([loadPending(), loadUsers()])
    } else {
      toast(res.data?.message || '驳回失败', 'error')
    }
  } catch (e: any) {
    toast(e?.response?.data?.message || '驳回失败', 'error')
  }
}

async function loadPending() {
  try {
    const res = await getPendingUsers()
    if (res.data?.code === 0) pendingUsers.value = res.data.data || []
  } catch { /* 读取失败不阻塞用户列表 */ }
}

onMounted(() => {
  void loadUsers()
  void loadPending()
  void loadLandlordApps()
})
</script>
