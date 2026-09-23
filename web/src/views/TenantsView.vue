<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="租户管理" description="入住登记与退租管理，宿舍场景支持一房多名租户">
      <template #actions>
        <button class="btn-brand" @click="openCreate">＋ 登记租户</button>
      </template>
    </PageHeader>

    <div class="surface rounded-2xl p-4 flex flex-wrap items-center gap-3">
      <input v-model="keyword" class="input-field flex-1 min-w-[200px] !py-2" placeholder="搜索姓名或电话…" @keyup.enter="load(1)" />
      <select v-model="active" class="input-field !py-2 !w-auto" @change="load(1)">
        <option value="">全部</option>
        <option value="1">在租</option>
        <option value="0">已退租</option>
      </select>
      <select v-model="roomId" class="input-field !py-2 !w-auto" @change="load(1)">
        <option :value="0">全部房源</option>
        <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ r.room_no }}</option>
      </select>
      <button class="btn-brand !py-2" @click="load(1)">搜索</button>
    </div>

    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="tenants.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        暂无租户，点击右上角「登记租户」开始。
      </div>
      <template v-else>
      <!-- 桌面端：表格 -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th class="py-3 px-4 font-medium">姓名</th>
              <th class="py-3 px-4 font-medium">电话</th>
              <th class="py-3 px-4 font-medium">房源</th>
              <th class="py-3 px-4 font-medium">入住日期</th>
              <th class="py-3 px-4 font-medium">到期时间</th>
              <th class="py-3 px-4 font-medium">退租日期</th>
              <th class="py-3 px-4 font-medium text-right">押金</th>
              <th class="py-3 px-4 font-medium">状态</th>
              <th class="py-3 px-4 font-medium">备注</th>
              <th class="py-3 px-4 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="t in tenants" :key="t.id" class="hover:bg-muted/30 transition-colors">
              <td class="py-3 px-4 font-semibold text-foreground">{{ t.name }}</td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.phone || '—' }}</td>
              <td class="py-3 px-4 text-foreground" :title="t.room_label">{{ t.room_label || t.room_no || '—' }}</td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_in_date || '—' }}</td>
              <td class="py-3 px-4 tabular-nums">
                <template v-if="t.lease_end_date">
                  <div class="text-muted-foreground">{{ t.lease_end_date }}</div>
                  <div v-if="leaseStatus(t)" class="text-xs font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</div>
                </template>
                <span v-else class="text-muted-foreground">—</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_out_date || '—' }}</td>
              <td class="py-3 px-4 text-right tabular-nums" :class="t.deposit ? 'text-foreground' : 'text-muted-foreground'">{{ t.deposit ? fmtDeposit(t.deposit) : '—' }}</td>
              <td class="py-3 px-4">
                <span v-if="t.active" class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
                <span v-else class="badge bg-muted text-muted-foreground">已退租</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground max-w-[180px] truncate" :title="t.notes">{{ t.notes || '—' }}</td>
              <td class="py-3 px-4 text-right whitespace-nowrap">
                <button v-if="t.active" class="text-amber-600 dark:text-amber-300 hover:underline mr-3" @click="checkout(t)">退租</button>
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(t)">编辑</button>
                <button class="text-destructive hover:underline" @click="remove(t)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表（紧凑版） -->
      <div class="md:hidden divide-y divide-border">
        <div v-for="t in tenants" :key="t.id" class="px-3 py-2.5 space-y-1.5">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ t.name }}</span>
              <span class="text-[11px] text-muted-foreground ml-1.5 tabular-nums">{{ t.phone || '' }}</span>
            </div>
            <span v-if="t.active" class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
            <span v-else class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">已退租</span>
          </div>

          <div class="text-[11px] leading-5 text-muted-foreground">
            <div>房源 <span class="text-foreground">{{ t.room_label || t.room_no || '—' }}</span><template v-if="t.deposit"> · 押金 <span class="text-foreground tabular-nums">{{ fmtDeposit(t.deposit) }}</span></template></div>
            <div class="tabular-nums">入住 {{ t.move_in_date || '—' }} · 到期 {{ t.lease_end_date || '—' }}<template v-if="t.move_out_date"> · 退租 {{ t.move_out_date }}</template>
              <span v-if="leaseStatus(t)" class="ml-1 font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</span>
            </div>
            <div v-if="t.notes" class="truncate" :title="t.notes">{{ t.notes }}</div>
          </div>

          <div class="flex items-center gap-3 pt-0.5 border-t border-border/60 text-xs">
            <button v-if="t.active" class="text-amber-600 dark:text-amber-300" @click="checkout(t)">退租</button>
            <button class="text-brand-600 dark:text-brand-300" @click="openEdit(t)">编辑</button>
            <button class="text-destructive ml-auto" @click="remove(t)">删除</button>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 新增/编辑弹窗 -->
    <Modal v-model="showModal" :title="editing ? '编辑租户' : '登记租户'">
      <form class="space-y-4" @submit.prevent="save">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">姓名 *</label>
            <input v-model="form.name" class="input-field" placeholder="租户姓名" required />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">电话</label>
            <input v-model="form.phone" class="input-field" placeholder="选填" />
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">所属房源 *</label>
          <select v-model.number="form.room_id" class="input-field" required>
            <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ roomLabelOf(r) }}</option>
          </select>
          <p v-if="allRooms.length === 0" class="text-xs text-destructive mt-1">请先在「房源管理」中添加房源</p>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">入住日期</label>
            <input v-model="form.move_in_date" type="date" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">到期时间</label>
            <input v-model="form.lease_end_date" type="date" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">押金（元）</label>
            <input v-model.number="form.deposit" type="number" step="0.01" min="0" class="input-field" placeholder="0" />
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <input v-model="form.notes" class="input-field" placeholder="选填" />
        </div>
        <p v-if="formError" class="text-sm text-destructive">{{ formError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showModal = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <ConfirmDialog
      v-model="confirmState.show"
      :title="confirmState.title"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="onConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { toast } from '../utils/toast'
import {
  checkoutTenant, createTenant, deleteTenant, getRooms, getTenants, updateTenant,
  type RoomView, type Tenant,
} from '../api/rental'

const tenants = ref<Tenant[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const keyword = ref('')
const active = ref('')
const roomId = ref(0)

const showModal = ref(false)
const editing = ref<Tenant | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref<Partial<Tenant>>({})

function openCreate() {
  editing.value = null
  form.value = { name: '', phone: '', room_id: allRooms.value[0]?.id, move_in_date: '', lease_end_date: '', deposit: 0, notes: '' }
  formError.value = ''
  showModal.value = true
}

// 房源完整位置：小区 楼栋 单元 N层 房号
function roomLabelOf(r: RoomView): string {
  const parts = [r.community, r.building, r.unit, r.floor ? r.floor + '层' : '', r.room_no]
  return parts.filter(Boolean).join(' ')
}

// 押金显示：带千分位，0 不显示（在列表里以 — 呈现）
function fmtDeposit(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { maximumFractionDigits: 2 })
}

// 租约到期状态：已过期红 / 30 天内到期橙；未约定或非在租不提示
function leaseStatus(t: Tenant): { text: string; cls: string } | null {
  if (!t.active || !t.lease_end_date) return null
  const days = Math.ceil((new Date(t.lease_end_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
  if (days < 0) return { text: `已过期 ${-days} 天`, cls: 'text-rose-500' }
  if (days <= 30) return { text: `${days} 天后到期`, cls: 'text-amber-500' }
  return null
}

function openEdit(t: Tenant) {
  editing.value = t
  form.value = { ...t }
  formError.value = ''
  showModal.value = true
}

async function loadRooms() {
  const res = await getRooms({ page: 1, pageSize: 100 })
  if (res.data?.code === 0) allRooms.value = res.data.data.items || []
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getTenants({
      page: p, pageSize, keyword: keyword.value,
      active: active.value, room_id: roomId.value || undefined,
    })
    if (res.data?.code === 0) {
      tenants.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.name?.trim() || !form.value.room_id) {
    formError.value = '请填写姓名并选择房源'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    const res = editing.value
      ? await updateTenant(editing.value.id, form.value)
      : await createTenant(form.value)
    if (res.data?.code === 0) {
      showModal.value = false
      await load(1)
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch {
    formError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, title: '', message: '', action: null as null | (() => Promise<void>) })

function askConfirm(title: string, message: string, action: () => Promise<void>) {
  confirmState.value = { show: true, title, message, action }
}

async function onConfirm() {
  await confirmState.value.action?.()
}

function checkout(t: Tenant) {
  askConfirm('办理退租', `确认为租户「${t.name}」办理退租？历史账单不受影响。`, async () => {
    const res = await checkoutTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '退租失败', 'error')
      return
    }
    toast('已办理退租')
    await load(page.value)
  })
}

function remove(t: Tenant) {
  askConfirm('删除租户', `确定删除租户「${t.name}」的登记信息？`, async () => {
    const res = await deleteTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    await load(page.value)
  })
}

onMounted(async () => {
  await loadRooms()
  await load(1)
})
</script>
