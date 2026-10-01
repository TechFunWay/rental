<template>
  <div class="surface rounded-2xl p-4 sm:p-6">
    <div class="flex items-center justify-between gap-3 mb-4">
      <div>
        <h3 class="text-sm sm:text-base font-bold text-foreground">收费项目</h3>
        <p class="text-xs text-muted-foreground mt-0.5">内置项目不可删除可停用；自定义项目（宽带费、停车费等）按各自周期出账</p>
      </div>
      <button class="btn-brand !px-3 !py-2 text-xs shrink-0" @click="openCreate">＋ 新增项目</button>
    </div>

    <!-- 桌面端：表格 -->
    <div class="hidden sm:block overflow-x-auto">
      <table class="w-full text-sm min-w-[640px]">
        <thead>
          <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
            <th class="py-2.5 px-3 font-medium whitespace-nowrap">项目</th>
            <th class="py-2.5 px-3 font-medium whitespace-nowrap">计费方式</th>
            <th class="py-2.5 px-3 font-medium whitespace-nowrap">收费周期</th>
            <th class="py-2.5 px-3 font-medium text-right whitespace-nowrap">金额（元/期）</th>
            <th class="py-2.5 px-3 font-medium whitespace-nowrap">状态</th>
            <th class="py-2.5 px-3 font-medium text-right whitespace-nowrap">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr v-for="it in items" :key="it.id" class="hover:bg-muted/30 transition-colors">
            <td class="py-2.5 px-3 font-semibold text-foreground whitespace-nowrap">{{ it.name }}<span v-if="it.built_in" class="text-[10px] text-muted-foreground ml-1.5">内置</span></td>
            <td class="py-2.5 px-3 text-muted-foreground whitespace-nowrap">{{ kindText(it) }}</td>
            <td class="py-2.5 px-3 text-muted-foreground whitespace-nowrap">{{ cycleText(it) }}</td>
            <td class="py-2.5 px-3 text-right tabular-nums">{{ it.kind === 'fixed' ? fmt(it.default_amount) : '—' }}</td>
            <td class="py-2.5 px-3">
              <span v-if="it.enabled" class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">启用</span>
              <span v-else class="badge bg-muted text-muted-foreground">已停用</span>
            </td>
            <td class="py-2.5 px-3 text-right whitespace-nowrap">
              <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(it)">编辑</button>
              <button v-if="!it.built_in" class="text-destructive hover:underline" @click="remove(it)">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 手机端：紧凑卡片 -->
    <div class="sm:hidden divide-y divide-border">
      <div v-for="it in items" :key="it.id" class="py-2.5 flex items-center justify-between gap-3">
        <div class="min-w-0">
          <div class="text-sm font-semibold text-foreground">{{ it.name }}<span v-if="it.built_in" class="text-[10px] text-muted-foreground ml-1.5">内置</span></div>
          <div class="text-[11px] text-muted-foreground mt-0.5">{{ kindText(it) }} · {{ cycleText(it) }}<template v-if="it.kind === 'fixed' && it.default_amount > 0"> · {{ fmt(it.default_amount) }} 元/期</template></div>
        </div>
        <div class="flex items-center gap-3 text-xs shrink-0">
          <span v-if="it.enabled" class="badge !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">启用</span>
          <span v-else class="badge !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">停用</span>
          <button class="text-brand-600 dark:text-brand-300" @click="openEdit(it)">编辑</button>
          <button v-if="!it.built_in" class="text-destructive" @click="remove(it)">删除</button>
        </div>
      </div>
    </div>

    <!-- 新增/编辑弹窗 -->
    <Modal v-model="showModal" :title="editing ? '编辑收费项目' : '新增收费项目'">
      <form class="space-y-4" @submit.prevent="save">
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">项目名称 *</label>
          <input v-model="form.name" class="input-field" placeholder="如 宽带费 / 停车费" required />
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">收费周期</label>
            <select v-model="form.cycle" class="input-field">
              <option value="monthly">月付（每月一张）</option>
              <option value="quarterly">季付（每 3 个月一次）</option>
            </select>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">金额（元/期）</label>
            <input v-model.number="form.default_amount" type="number" step="0.01" min="0" class="input-field" placeholder="如 30" />
          </div>
        </div>
        <label class="flex items-center gap-2 text-sm text-foreground">
          <input v-model="form.enabled" type="checkbox" class="w-4 h-4 accent-indigo-500" />
          启用（出账时并入每期账单）
        </label>
        <p class="text-[11px] text-muted-foreground">自定义项目按固定金额 × 周期月数并入账单（季付一次收 3 个月）；水/电/燃气按抄表读数计费，不在此列。</p>
        <p v-if="formError" class="text-sm text-destructive">{{ formError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showModal = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <ConfirmDialog
      v-model="confirmState.show"
      title="删除收费项目"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="onConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ConfirmDialog from './ConfirmDialog.vue'
import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import { createFeeItem, deleteFeeItem, getFeeItems, updateFeeItem, type FeeItem } from '../api/rental'

const items = ref<FeeItem[]>([])
const showModal = ref(false)
const editing = ref<FeeItem | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref({ name: '', cycle: 'monthly' as 'monthly' | 'quarterly', default_amount: 0, enabled: true })

function kindText(it: FeeItem): string {
  if (it.kind === 'meter') return '按抄表读数' + (it.unit ? `（${it.unit}）` : '')
  return '固定金额'
}

function cycleText(it: FeeItem): string {
  if (it.kind === 'meter') return '按月收'
  return it.cycle === 'quarterly' ? '季付' : '月付'
}

function fmt(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function openCreate() {
  editing.value = null
  form.value = { name: '', cycle: 'monthly', default_amount: 0, enabled: true }
  formError.value = ''
  showModal.value = true
}

function openEdit(it: FeeItem) {
  editing.value = it
  form.value = { name: it.name, cycle: it.cycle, default_amount: it.default_amount, enabled: it.enabled }
  formError.value = ''
  showModal.value = true
}

async function load() {
  const res = await getFeeItems()
  if (res.data?.code === 0) items.value = res.data.data || []
}

async function save() {
  if (!form.value.name.trim()) {
    formError.value = '请填写项目名称'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    const payload = { ...form.value, name: form.value.name.trim() }
    const res = editing.value
      ? await updateFeeItem(editing.value.id, payload)
      : await createFeeItem(payload)
    if (res.data?.code === 0) {
      showModal.value = false
      toast('已保存')
      await load()
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch {
    formError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, message: '', action: null as null | (() => Promise<void>) })

function remove(it: FeeItem) {
  confirmState.value = {
    show: true,
    message: `确定删除收费项目「${it.name}」？已有账单中的历史明细不受影响。`,
    action: async () => {
      const res = await deleteFeeItem(it.id)
      if (res.data?.code !== 0) {
        toast(res.data?.message || '删除失败', 'error')
        return
      }
      toast('已删除')
      await load()
    },
  }
}

async function onConfirm() {
  await confirmState.value.action?.()
}

onMounted(load)
</script>
