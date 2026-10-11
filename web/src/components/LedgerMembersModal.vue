<template>
  <Modal v-model="visible" :title="`成员与权限${ledger ? '：' + ledger.name : ''}`">
    <div class="space-y-4">
      <div v-if="loading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>

      <template v-else>
        <!-- 邀请（仅管理者） -->
        <div v-if="isOwner" class="rounded-2xl bg-muted/60 p-3">
          <label class="block text-sm font-medium text-foreground mb-1.5">邀请成员</label>
          <div class="flex items-center gap-2">
            <input
              v-model="inviteName"
              class="input-field flex-1"
              placeholder="输入已注册用户的用户名"
              @keyup.enter="invite"
            />
            <button type="button" class="btn-brand !px-4 shrink-0" :disabled="inviting" @click="invite">
              {{ inviting ? '邀请中…' : '发送邀请' }}
            </button>
          </div>
          <p class="mt-1.5 text-xs text-muted-foreground">
            对方接受后才算成员；新成员默认只有查看权限，可在下面按人调整。
          </p>
        </div>

        <!-- 成员列表 -->
        <!-- 成员列表限高：成员多时列表自己滚，保证「发送邀请」与底部「退出」按钮始终在屏内 -->
        <div class="divide-y divide-border rounded-2xl border border-border overflow-hidden max-h-[46dvh] overflow-y-auto">
          <div v-for="m in members" :key="m.user_id" class="px-3 py-3">
            <div class="flex items-center gap-2">
              <div class="min-w-0 flex-1">
                <div class="flex items-center gap-1.5">
                  <span class="text-sm font-semibold text-foreground truncate">{{ m.username }}</span>
                  <span v-if="m.user_id === me" class="badge !px-1.5 !py-0 text-[10px] bg-muted text-muted-foreground">我</span>
                  <span v-if="m.role === 'owner'" class="badge !px-1.5 !py-0 text-[10px] bg-brand-500/15 text-brand-600 dark:text-brand-300">管理者</span>
                  <span v-else-if="m.status === 'pending'" class="badge !px-1.5 !py-0 text-[10px] bg-amber-400/20 text-amber-700 dark:text-amber-300">{{ m.status_label }}</span>
                  <span v-else-if="m.status === 'rejected'" class="badge !px-1.5 !py-0 text-[10px] bg-muted text-muted-foreground">已拒绝</span>
                </div>
                <div class="text-[11px] text-muted-foreground truncate">{{ permSummary(m) }}</div>
              </div>
              <template v-if="isOwner && m.role !== 'owner'">
                <button type="button" class="px-2.5 py-1.5 rounded-lg text-xs font-semibold text-brand-600 dark:text-brand-300 hover:bg-muted transition-colors" @click="toggleEdit(m.user_id)">
                  {{ editing === m.user_id ? '收起' : '权限' }}
                </button>
                <button type="button" class="px-2.5 py-1.5 rounded-lg text-xs font-semibold text-destructive hover:bg-destructive/10 transition-colors" @click="askRemove(m)">
                  移除
                </button>
              </template>
            </div>

            <!-- 按模块勾选权限（展开） -->
            <div v-if="editing === m.user_id" class="mt-3 rounded-xl bg-muted/60 p-3 space-y-2">
              <div v-for="mod in ALL_MODULES" :key="mod" class="flex items-center gap-2">
                <span class="text-xs text-foreground w-24 shrink-0">{{ MODULE_LABEL[mod] }}</span>
                <select v-model="draft[mod]" class="input-field !py-1.5 !text-xs flex-1">
                  <option value="">无权限</option>
                  <option value="view">查看</option>
                  <option value="edit">操作（含查看）</option>
                  <option value="full">完全（含删除与导入）</option>
                </select>
              </div>
              <div class="flex items-center gap-2 pt-1">
                <button type="button" class="btn-brand !py-1.5 !px-3 text-xs" :disabled="saving" @click="savePerms(m.user_id)">保存权限</button>
                <button type="button" class="text-xs text-muted-foreground hover:text-foreground" @click="editing = null">取消</button>
              </div>
            </div>
          </div>
        </div>

        <!-- 非管理者：退出台账 -->
        <button
          v-if="!isOwner && ledger && ledger.kind !== 'personal'"
          type="button"
          class="w-full px-4 py-2.5 rounded-xl text-sm font-semibold text-destructive hover:bg-destructive/10 transition-colors"
          @click="askLeave"
        >
          退出这个共同台账
        </button>

        <p class="text-xs text-muted-foreground">
          {{ isOwner
            ? '数据归台账，不归个人：移除成员只收回权限，他在这个台账里录入过的数据仍然保留。'
            : '只有台账管理者能增删成员；你可以随时退出这个台账。' }}
        </p>
      </template>
    </div>
  </Modal>

  <ConfirmDialog
    v-model="confirmOpen"
    :title="confirmTitle"
    :message="confirmMessage"
    confirm-text="确定"
    confirm-type="danger"
    @confirm="runConfirm"
  />
</template>

<script setup lang="ts">
// 成员与权限：管理者邀请/设权限/移除；成员只读查看并可以退出。
// 权限沿用项目的「模块 × 档位」模型（view/edit/full），管理者恒为全部模块完全权限。
import { computed, ref, watch } from 'vue'
import Modal from './Modal.vue'
import ConfirmDialog from './ConfirmDialog.vue'
import { ALL_MODULES, MODULE_LABEL, getLedgerMembers, inviteMember, removeMember, setMemberPerms, type LedgerMemberView, type LedgerView } from '../api/ledger'
import type { ModuleKey, ModulePerm, ModulePerms } from '../api/rental'
import { useAuthStore } from '../stores/auth'
import { toast } from '../utils/toast'

const props = defineProps<{
  modelValue: boolean
  ledger: LedgerView | null
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  changed: []
}>()

const authStore = useAuthStore()
const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v })
watch(visible, (v) => emit('update:modelValue', v))

const members = ref<LedgerMemberView[]>([])
const loading = ref(false)
const isOwner = ref(false)
const inviteName = ref('')
const inviting = ref(false)
const editing = ref<number | null>(null)
const draft = ref<Record<string, ModulePerm | ''>>({})
const saving = ref(false)

const me = computed(() => Number(authStore.user?.id) || 0)

const confirmOpen = ref(false)
const confirmTitle = ref('')
const confirmMessage = ref('')
let confirmAction: (() => Promise<void>) | null = null

function permSummary(m: LedgerMemberView): string {
  const keys = ALL_MODULES.filter((k) => m.perms?.[k])
  if (keys.length === 0) return '无权限'
  if (keys.length === ALL_MODULES.length && ALL_MODULES.every((k) => m.perms[k] === 'full')) return '全部模块·完全'
  return keys.map((k) => `${MODULE_LABEL[k]}·${tierLabel(m.perms[k])}`).join('、')
}

function tierLabel(t: ModulePerm | undefined): string {
  return t === 'full' ? '完全' : t === 'edit' ? '操作' : t === 'view' ? '查看' : '无'
}

async function load() {
  if (!props.ledger) return
  loading.value = true
  editing.value = null
  try {
    const res = await getLedgerMembers(props.ledger.id)
    if (res.data?.code === 0) {
      members.value = res.data.data?.members || []
      isOwner.value = !!res.data.data?.is_owner
    } else {
      toast(res.data?.message || '加载成员失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '加载成员失败', 'error')
  } finally {
    loading.value = false
  }
}

watch(() => [props.modelValue, props.ledger?.id], () => {
  if (props.modelValue) void load()
}, { immediate: true })

function toggleEdit(userId: number) {
  if (editing.value === userId) {
    editing.value = null
    return
  }
  const m = members.value.find((x) => x.user_id === userId)
  const next: Record<string, ModulePerm | ''> = {}
  for (const mod of ALL_MODULES) next[mod] = (m?.perms?.[mod] as ModulePerm) || ''
  draft.value = next
  editing.value = userId
}

async function savePerms(userId: number) {
  if (!props.ledger) return
  saving.value = true
  try {
    const perms: ModulePerms = {}
    for (const mod of ALL_MODULES) {
      const t = draft.value[mod]
      if (t) perms[mod as ModuleKey] = t
    }
    const res = await setMemberPerms(props.ledger.id, userId, perms)
    if (res.data?.code === 0) {
      toast('权限已保存')
      editing.value = null
      await load()
      emit('changed')
    } else {
      toast(res.data?.message || '保存失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '保存失败', 'error')
  } finally {
    saving.value = false
  }
}

async function invite() {
  if (!props.ledger) return
  const name = inviteName.value.trim()
  if (!name) {
    toast('请填写用户名', 'error')
    return
  }
  inviting.value = true
  try {
    const res = await inviteMember(props.ledger.id, name)
    if (res.data?.code === 0) {
      toast('邀请已发送')
      inviteName.value = ''
      await load()
      emit('changed')
    } else {
      toast(res.data?.message || '邀请失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '邀请失败', 'error')
  } finally {
    inviting.value = false
  }
}

function askRemove(m: LedgerMemberView) {
  confirmTitle.value = `移除成员「${m.username}」？`
  confirmMessage.value = '移除后他将看不到这个台账的数据（他录入的数据仍保留在台账里）。'
  confirmAction = async () => {
    if (!props.ledger) return
    const res = await removeMember(props.ledger.id, m.user_id)
    if (res.data?.code === 0) {
      toast('成员已移除')
      await load()
      emit('changed')
    } else {
      toast(res.data?.message || '移除失败', 'error')
    }
  }
  confirmOpen.value = true
}

function askLeave() {
  confirmTitle.value = `退出「${props.ledger?.name || ''}」？`
  confirmMessage.value = '退出后你将看不到这个台账的数据；管理者可以再次邀请你。'
  confirmAction = async () => {
    if (!props.ledger) return
    const res = await removeMember(props.ledger.id, me.value)
    if (res.data?.code === 0) {
      toast('已退出该台账')
      visible.value = false
      emit('changed')
    } else {
      toast(res.data?.message || '退出失败', 'error')
    }
  }
  confirmOpen.value = true
}

async function runConfirm() {
  try {
    await confirmAction?.()
  } catch (err: any) {
    toast(err?.response?.data?.message || '操作失败', 'error')
  } finally {
    confirmAction = null
  }
}
</script>
