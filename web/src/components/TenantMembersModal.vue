<template>
  <Modal :model-value="modelValue" :title="`住户成员 · ${tenant?.name || ''}`" @update:model-value="emit('update:modelValue', $event)">
    <div class="space-y-3">
      <div class="rounded-xl border border-brand-500/30 bg-brand-500/[0.06] px-3 py-2.5 text-[11px] leading-5 text-muted-foreground">
        住户成员是与
        <span class="font-semibold text-foreground">{{ tenant?.name || '该租户' }}</span>
        同住的人（配偶、子女、父母、同事…）。
        <span class="text-foreground font-medium">费用只跟租户有关</span>——账单、抄表、收款都只按租户算，
        成员只登记身份信息，用于人员登记与安全排查。
      </div>

      <!-- 表单态：新增 / 编辑 -->
      <template v-if="editing">
        <div class="rounded-xl border border-border p-3 space-y-3">
          <div class="text-xs font-semibold text-foreground">{{ editingID ? '编辑成员' : '新增成员' }}</div>
          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label class="block">
              <span class="text-[11px] text-muted-foreground">姓名 *</span>
              <input v-model="form.name" class="input-field !py-2 mt-1" placeholder="如 李四" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">手机号</span>
              <input v-model="form.phone" class="input-field !py-2 mt-1" inputmode="tel" placeholder="选填" />
            </label>
          </div>

          <div>
            <span class="text-[11px] text-muted-foreground">与租户关系</span>
            <div class="flex flex-wrap gap-1.5 mt-1">
              <button
                v-for="opt in RELATIONS"
                :key="opt"
                type="button"
                class="px-2.5 py-1 rounded-lg text-xs font-medium border transition-colors"
                :class="form.relation === opt
                  ? 'bg-brand-500/15 border-brand-500/40 text-brand-600 dark:text-brand-300'
                  : 'bg-surface/70 border-border text-muted-foreground hover:bg-muted'"
                @click="form.relation = form.relation === opt ? '' : opt"
              >{{ opt }}</button>
              <input v-model="form.relation" class="input-field !py-1 !w-[110px] text-xs" placeholder="或自己填" />
            </div>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label class="block">
              <span class="text-[11px] text-muted-foreground">身份证号</span>
              <input v-model="form.id_card" class="input-field !py-2 mt-1 tabular-nums" placeholder="选填，18 位或 15 位" />
              <span v-if="derivedHint" class="text-[11px] text-muted-foreground mt-1 block">{{ derivedHint }}</span>
            </label>
            <div>
              <span class="text-[11px] text-muted-foreground">在住状态</span>
              <div class="flex items-center gap-1.5 mt-1">
                <button
                  type="button"
                  class="px-3 py-1.5 rounded-lg text-xs font-medium border transition-colors"
                  :class="form.active ? 'bg-emerald-500/15 border-emerald-500/40 text-emerald-600 dark:text-emerald-300' : 'bg-surface/70 border-border text-muted-foreground hover:bg-muted'"
                  @click="form.active = true"
                >在住</button>
                <button
                  type="button"
                  class="px-3 py-1.5 rounded-lg text-xs font-medium border transition-colors"
                  :class="!form.active ? 'bg-muted border-border text-foreground' : 'bg-surface/70 border-border text-muted-foreground hover:bg-muted'"
                  @click="form.active = false"
                >已搬离</button>
              </div>
            </div>
          </div>

          <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
            <label class="block">
              <span class="text-[11px] text-muted-foreground">入住日期</span>
              <DateField v-model="form.move_in_date" class="mt-1" placeholder="选填" />
            </label>
            <label class="block">
              <span class="text-[11px] text-muted-foreground">搬离日期</span>
              <DateField v-model="form.move_out_date" class="mt-1" placeholder="选填，填了自动置为已搬离" />
            </label>
          </div>

          <label class="block">
            <span class="text-[11px] text-muted-foreground">备注</span>
            <input v-model="form.notes" class="input-field !py-2 mt-1" placeholder="选填，如 常住 / 寒暑假回来" />
          </label>

          <p v-if="formError" class="text-xs text-destructive">{{ formError }}</p>
          <div class="flex gap-2 pt-0.5">
            <button type="button" class="btn-ghost flex-1 !py-2" :disabled="saving" @click="cancelEdit">取消</button>
            <button type="button" class="btn-brand flex-1 !py-2" :disabled="saving" @click="save">
              {{ saving ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </template>

      <!-- 列表态 -->
      <template v-else>
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs text-muted-foreground">
            共 {{ members.length }} 人<span v-if="activeCount">（在住 {{ activeCount }}）</span>
          </span>
          <button v-if="canEdit" type="button" class="btn-brand !py-1.5 !px-3 text-xs" @click="startCreate">＋ 添加成员</button>
        </div>

        <div v-if="loading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="members.length === 0" class="py-6 text-center text-sm text-muted-foreground">
          还没登记同住成员。只住租户本人时不必添加；有家属或合住人时登记一下，安全排查与人员统计都用得上。
        </div>
        <div v-else class="space-y-2">
          <div v-for="m in members" :key="m.id" class="rounded-xl border border-border px-3 py-2.5 space-y-1.5">
            <div class="flex items-center justify-between gap-2 flex-wrap">
              <div class="flex items-center gap-1.5 min-w-0 flex-wrap">
                <span class="text-sm font-semibold text-foreground">{{ m.name }}</span>
                <span v-if="m.relation" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ m.relation }}</span>
                <span
                  class="badge !px-1.5 !py-0.5 text-[11px]"
                  :class="m.active ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300' : 'bg-muted text-muted-foreground'"
                >{{ m.active ? '在住' : '已搬离' }}</span>
              </div>
              <div class="flex items-center gap-2.5 text-xs shrink-0">
                <button v-if="m.id_card" type="button" class="text-brand-600 dark:text-brand-300 hover:underline" @click="copyText(m.id_card)">复制身份证</button>
                <button v-if="canEdit" type="button" class="text-brand-600 dark:text-brand-300 hover:underline" @click="startEdit(m)">编辑</button>
                <button v-if="canFull" type="button" class="text-destructive hover:underline" @click="askRemove(m)">删除</button>
              </div>
            </div>
            <div class="text-[11px] text-muted-foreground leading-5 space-y-0.5">
              <div class="break-all">
                身份证号 <span class="text-foreground tabular-nums">{{ m.id_card || '—' }}</span>
                <span v-if="m.birthday || m.gender" class="ml-1">
                  · 出生 {{ m.birthday || '—' }} · {{ genderLabel(m.gender) }}
                </span>
              </div>
              <div>手机号 <span class="text-foreground tabular-nums">{{ m.phone || '—' }}</span></div>
              <div class="tabular-nums">
                入住 {{ m.move_in_date || '—' }} · 搬离 {{ m.move_out_date || '—' }}
              </div>
              <div v-if="m.notes">备注 {{ m.notes }}</div>
            </div>
          </div>
        </div>

        <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="emit('update:modelValue', false)">关闭</button>
        </div>
      </template>
    </div>

    <!-- 删除确认：不弹原生 confirm（项目硬规则） -->
    <ConfirmDialog
      v-model="confirmRemove.show"
      title="删除住户成员"
      :message="confirmRemove.message"
      confirm-type="danger"
      @confirm="confirmRemove.action?.()"
    />
  </Modal>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'

import ConfirmDialog from './ConfirmDialog.vue'
import DateField from './DateField.vue'
import Modal from './Modal.vue'
import { useLedgerStore } from '../stores/ledger'
import { ID_CARD_PATTERN, genderLabel, idCardHint } from '../utils/id-card'
import { toast } from '../utils/toast'
import {
  createTenantMember, deleteTenantMember, getTenantMembers, updateTenantMember,
  type Tenant, type TenantMember, type TenantMemberInput,
} from '../api/rental'

// 住户成员弹窗：列出租户名下的同住人，可增可改可删。
// 口径见仓库内 docs/08-租户与住户成员设计.md——**费用只跟租户有关**，
// 成员只登记身份信息（性别与出生日期由身份证号派生，服务端为准）。
const props = defineProps<{
  modelValue: boolean
  tenant: Tenant | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 成员数变化后通知列表刷新「成员 n」 */
  changed: []
}>()

// 常用关系，点一下比打字快；也允许自己填（后端只限长度、不设白名单）。
const RELATIONS = ['配偶', '子女', '父母', '兄弟姐妹', '亲属', '同事', '朋友', '其他']

const ledgerStore = useLedgerStore()
const canEdit = computed(() => ledgerStore.canEdit('tenants'))
const canFull = computed(() => ledgerStore.canFull('tenants'))

const members = ref<TenantMember[]>([])
const loading = ref(false)
const error = ref('')
const activeCount = computed(() => members.value.filter((m) => m.active).length)

const emptyForm = (): TenantMemberInput => ({
  name: '', relation: '', phone: '', id_card: '',
  move_in_date: '', move_out_date: '', active: true, notes: '',
})
const editing = ref(false)
const editingID = ref(0)
const form = reactive<TenantMemberInput>(emptyForm())
const saving = ref(false)
const formError = ref('')

// 身份证号派生的即时提示（落库以服务端为准，这里只是让用户填完立刻看到结果）。
// 规则与文案都在 utils/id-card.ts，租户登记表单里的成员行也用同一份。
const derivedHint = computed(() => idCardHint(form.id_card || ''))

async function load() {
  const tenant = props.tenant
  if (!tenant) return
  loading.value = true
  error.value = ''
  try {
    const res = await getTenantMembers({ tenant_id: tenant.id })
    members.value = res.data?.code === 0 ? (res.data.data ?? []) : []
  } catch {
    error.value = '加载住户成员失败，请重试'
  } finally {
    loading.value = false
  }
}

function startCreate() {
  Object.assign(form, emptyForm())
  editingID.value = 0
  formError.value = ''
  editing.value = true
}

function startEdit(m: TenantMember) {
  Object.assign(form, {
    name: m.name, relation: m.relation, phone: m.phone, id_card: m.id_card,
    move_in_date: m.move_in_date, move_out_date: m.move_out_date,
    active: m.active, notes: m.notes,
  })
  editingID.value = m.id
  formError.value = ''
  editing.value = true
}

function cancelEdit() {
  editing.value = false
  editingID.value = 0
  formError.value = ''
}

async function save() {
  const tenant = props.tenant
  if (!tenant) return
  if (!form.name.trim()) {
    formError.value = '请填写成员姓名'
    return
  }
  const idCard = (form.id_card || '').trim().toUpperCase()
  if (idCard && !ID_CARD_PATTERN.test(idCard)) {
    formError.value = '身份证号应为 18 位（末位可为 X）或 15 位'
    return
  }
  const payload: TenantMemberInput = {
    name: form.name.trim(),
    relation: (form.relation || '').trim(),
    phone: (form.phone || '').trim(),
    id_card: idCard,
    move_in_date: form.move_in_date || '',
    move_out_date: form.move_out_date || '',
    // 填了搬离日期就按已搬离保存，免得出现「已搬离却还在住」的矛盾记录
    active: form.move_out_date ? false : form.active !== false,
    notes: (form.notes || '').trim(),
  }
  saving.value = true
  formError.value = ''
  try {
    const res = editingID.value
      ? await updateTenantMember(editingID.value, payload)
      : await createTenantMember(tenant.id, payload)
    if (res.data?.code === 0) {
      toast(editingID.value ? '已保存成员信息' : '已添加住户成员')
      cancelEdit()
      await load()
      emit('changed')
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch (err: any) {
    formError.value = err.response?.data?.message || '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmRemove = ref({ show: false, message: '', action: null as null | (() => Promise<void>) })

function askRemove(m: TenantMember) {
  confirmRemove.value = {
    show: true,
    message: `确定删除住户成员「${m.name}」？删除后不可恢复（退租的历史同住人建议保留为已搬离）。`,
    action: async () => {
      const res = await deleteTenantMember(m.id)
      if (res.data?.code !== 0) {
        toast(res.data?.message || '删除失败', 'error')
        return
      }
      toast('已删除成员')
      await load()
      emit('changed')
    },
  }
}

async function copyText(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast('已复制身份证号')
  } catch {
    toast('复制失败，请长按选中手动复制', 'error')
  }
}

watch(() => [props.modelValue, props.tenant?.id] as const, ([open]) => {
  if (!open) return
  cancelEdit()
  error.value = ''
  members.value = []
  void load()
}, { immediate: true })
</script>
