<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="抄表记录" description="每月水/电/燃气表读数台账，独立于账单：空置房、季付房的中间月份也能记">
      <template #actions>
        <button class="btn-ghost" @click="openForm()">录入读数</button>
        <button class="btn-brand" @click="showMetering = true">⚡ 抄表开票</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="metersFilterSummary" :active-count="metersFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <DateField v-model="period" type="month" class="!py-2 !w-[150px]" @change="load(1)" />
        <select v-model="roomId" class="input-field !py-2 !w-auto" @change="load(1)">
          <option :value="0">全部房源</option>
          <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ r.room_no }}</option>
        </select>
        <button class="btn-brand !py-2" @click="applyFilters">查询</button>
        <span class="ml-auto text-xs text-muted-foreground hidden sm:block">开票时会自动沉淀当期读数；读数倒挂（换表）按 0 计用量</span>
      </div>
    </FilterPanel>

    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="records.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        还没有抄表记录。点击右上角「录入读数」开始，抄表开票时也会自动记入这里。
      </div>
      <template v-else>
        <!-- 桌面端：表格 -->
        <div class="hidden md:block overflow-x-auto">
          <table class="w-full text-sm">
            <thead>
              <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
                <th class="py-3 px-4 font-medium">账期</th>
                <th class="py-3 px-4 font-medium">房号</th>
                <th class="py-3 px-4 font-medium">水表（读数 · 用量，吨）</th>
                <th class="py-3 px-4 font-medium">电表（读数 · 用量，度）</th>
                <th class="py-3 px-4 font-medium">燃气表（读数 · 用量，方）</th>
                <th class="py-3 px-4 font-medium">备注</th>
                <th class="py-3 px-4 font-medium text-right">操作</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-border">
              <tr v-for="r in records" :key="r.id" class="hover:bg-muted/30 transition-colors">
                <td class="py-2.5 px-4 text-muted-foreground tabular-nums whitespace-nowrap">{{ r.period }}</td>
                <td class="py-2.5 px-4 font-semibold text-foreground whitespace-nowrap">{{ r.room_no }}</td>
                <td class="py-2.5 px-4 tabular-nums whitespace-nowrap">
                  {{ fmtRead(r.water) }} 吨 <span class="text-xs" :class="usageClass(r, r.water, r.prev_water)">· 用 {{ fmtRead(r.usage_water) }} 吨</span>
                </td>
                <td class="py-2.5 px-4 tabular-nums whitespace-nowrap">
                  {{ fmtRead(r.elec) }} 度 <span class="text-xs" :class="usageClass(r, r.elec, r.prev_elec)">· 用 {{ fmtRead(r.usage_elec) }} 度</span>
                </td>
                <td class="py-2.5 px-4 tabular-nums whitespace-nowrap">
                  {{ fmtRead(r.gas) }} 方 <span class="text-xs" :class="usageClass(r, r.gas, r.prev_gas)">· 用 {{ fmtRead(r.usage_gas) }} 方</span>
                </td>
                <td class="py-2.5 px-4 text-muted-foreground max-w-[200px] truncate" :title="r.note">{{ r.note || '—' }}</td>
                <td class="py-2.5 px-4 text-right whitespace-nowrap">
                  <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openForm(r)">修改</button>
                  <button class="text-destructive hover:underline" @click="remove(r)">删除</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 手机端：卡片列表 -->
        <div class="md:hidden p-2 space-y-1.5">
          <div v-for="r in records" :key="r.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5">
            <div class="flex items-center justify-between gap-2">
              <div class="min-w-0">
                <span class="text-sm font-semibold text-foreground">{{ r.room_no }}</span>
                <span class="text-[11px] text-muted-foreground ml-1.5 tabular-nums">{{ r.period }}</span>
              </div>
              <div class="flex items-center gap-3 text-xs shrink-0">
                <button class="text-brand-600 dark:text-brand-300" @click="openForm(r)">修改</button>
                <button class="text-destructive" @click="remove(r)">删除</button>
              </div>
            </div>
            <div class="text-[11px] leading-5 text-muted-foreground rounded-md bg-muted/50 px-2 py-1.5 tabular-nums">
              <div>水 {{ fmtRead(r.water) }} 吨 <span :class="usageClass(r, r.water, r.prev_water)">· 用 {{ fmtRead(r.usage_water) }} 吨</span></div>
              <div>电 {{ fmtRead(r.elec) }} 度 <span :class="usageClass(r, r.elec, r.prev_elec)">· 用 {{ fmtRead(r.usage_elec) }} 度</span></div>
              <div>燃气 {{ fmtRead(r.gas) }} 方 <span :class="usageClass(r, r.gas, r.prev_gas)">· 用 {{ fmtRead(r.usage_gas) }} 方</span></div>
              <div v-if="r.note" class="truncate" :title="r.note">备注 {{ r.note }}</div>
            </div>
          </div>
        </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 录入/修改弹窗 -->
    <Modal v-model="showForm" :title="form.id ? '修改读数' : '录入读数'">
      <form class="space-y-4" @submit.prevent="save">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">账期 *</label>
            <DateField v-model="form.period" type="month" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">房间 *</label>
            <select v-model.number="form.room_id" class="input-field" required>
              <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ roomLabelOf(r) }}</option>
            </select>
          </div>
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-3" @input="formTouched = true">
          <div v-for="m in meterFields" :key="m.key">
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">{{ m.label }}（{{ m.unit }}）</label>
            <input v-model.number="form[m.key]" type="number" step="0.01" min="0" class="input-field" :placeholder="String(prevOf(m.key))" />
            <p class="text-[11px] text-muted-foreground mt-1 tabular-nums">上期 {{ fmtRead(prevOf(m.key)) }} {{ m.unit }}</p>
          </div>
        </div>
        <p v-if="readingBackwards" class="text-xs text-amber-600 dark:text-amber-300">
          有读数小于上期，如为换表请忽略；用量将按 0 计。
        </p>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注</label>
          <input v-model="form.note" class="input-field" placeholder="选填，如换表/上门抄表" />
        </div>
        <p class="text-[11px] text-muted-foreground">同一房间同一月份重复保存即覆盖原记录。</p>
        <p v-if="formError" class="text-sm text-destructive">{{ formError }}</p>
        <div class="flex gap-3 pt-1">
          <button type="button" class="btn-ghost flex-1" @click="showForm = false">取消</button>
          <button type="submit" class="btn-brand flex-1" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </form>
    </Modal>

    <!-- 抄表开票：录本月读数并建账/开票（与账单列表共用弹窗）；「录入读数」只记台账不建账 -->
    <MeterReadingModal v-model="showMetering" @saved="load(page)" />

    <ConfirmDialog
      v-model="confirmState.show"
      title="删除抄表记录"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="confirmState.action?.()"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DateField from '../components/DateField.vue'
import FilterPanel from '../components/FilterPanel.vue'
import MeterReadingModal from '../components/MeterReadingModal.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { toast } from '../utils/toast'
import {
  deleteMeterRecord, getMeterRecords, getRooms, upsertMeterRecord,
  type MeterRecord, type RoomView,
} from '../api/rental'

const records = ref<MeterRecord[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const period = ref('')
const roomId = ref(0)

// 手机端筛选摘要行与非默认条件数（徽标，月份常驻不计入）
const metersFilterSummary = computed(() =>
  [
    period.value || '全部月份',
    roomId.value ? (allRooms.value.find((r) => r.id === roomId.value)?.room_no || '指定房源') : '全部房源',
  ].join(' · '),
)
const metersFilterCount = computed(() => (roomId.value ? 1 : 0))

const filterRef = ref<InstanceType<typeof FilterPanel> | null>(null)

function applyFilters() {
  load(1)
  filterRef.value?.collapse()
}

const showForm = ref(false)
const showMetering = ref(false)
const saving = ref(false)
const formError = ref('')
const form = reactive({
  id: 0, room_id: 0, period: '', water: 0, elec: 0, gas: 0, note: '',
})
// 用户改过读数后才显示倒挂提醒，避免刚打开弹窗就见警告。
const formTouched = ref(false)
// 表单参照：同房间最近一期已加载的记录（仅作提示，保存口径以服务端为准）
const formPrev = ref<{ water: number; elec: number; gas: number } | null>(null)

const meterFields = [
  { key: 'water', label: '水表读数', unit: '吨' },
  { key: 'elec', label: '电表读数', unit: '度' },
  { key: 'gas', label: '燃气表读数', unit: '方' },
] as const

const readingBackwards = computed(() => {
  if (!formTouched.value) return false
  const prevs = [formPrev.value?.water, formPrev.value?.elec, formPrev.value?.gas]
  const nows = [Number(form.water) || 0, Number(form.elec) || 0, Number(form.gas) || 0]
  return prevs.some((p, i) => p !== undefined && nows[i] < p)
})

function prevOf(key: 'water' | 'elec' | 'gas'): number {
  return formPrev.value?.[key] ?? 0
}

function roomLabelOf(r: RoomView): string {
  const parts = [r.community, r.building, r.unit, r.floor ? r.floor + '层' : '', r.room_no]
  return parts.filter(Boolean).join(' ')
}

function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// 用量小字：倒挂（读数小于上期而按 0 计）时给警示色，正常灰色。
function usageClass(r: MeterRecord, now: number, prev: number): string {
  if (now < prev) return 'text-amber-600 dark:text-amber-300'
  return 'text-muted-foreground'
}

function openForm(r?: MeterRecord) {
  formError.value = ''
  formPrev.value = null
  formTouched.value = false
  if (r) {
    form.id = r.id
    form.room_id = r.room_id
    form.period = r.period
    form.water = r.water
    form.elec = r.elec
    form.gas = r.gas
    form.note = r.note
    guessPrev(r.room_id, r.period)
  } else {
    form.id = 0
    form.room_id = allRooms.value[0]?.id ?? 0
    form.period = currentPeriod()
    form.water = 0
    form.elec = 0
    form.gas = 0
    form.note = ''
    if (form.room_id) guessPrev(form.room_id, form.period)
  }
  showForm.value = true
}

// 从当前已加载记录里找同房间、账期早于表单月份的最近一条作参照；
// 找不到则回退房间抄表底数。仅用于表单提示。
function guessPrev(roomIdNum: number, period: string) {
  const candidates = records.value.filter((x) => x.room_id === roomIdNum && x.period < period)
  const nearest = candidates.sort((a, b) => (a.period < b.period ? 1 : -1))[0]
  if (nearest) {
    formPrev.value = { water: nearest.water, elec: nearest.elec, gas: nearest.gas }
    return
  }
  const room = allRooms.value.find((r) => r.id === roomIdNum)
  if (room) {
    formPrev.value = { water: room.initial_water || 0, elec: room.initial_elec || 0, gas: room.initial_gas || 0 }
  }
}

function currentPeriod(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

async function loadRooms() {
  const res = await getRooms({ page: 1, pageSize: 200 })
  if (res.data?.code === 0) allRooms.value = res.data.data.items || []
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getMeterRecords({
      page: p, pageSize, period: period.value || undefined, room_id: roomId.value || undefined,
    })
    if (res.data?.code === 0) {
      records.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.room_id || !form.period) {
    formError.value = '请选择房间与账期'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    const res = await upsertMeterRecord({
      room_id: form.room_id, period: form.period,
      water: Number(form.water) || 0, elec: Number(form.elec) || 0, gas: Number(form.gas) || 0,
      note: form.note,
    })
    if (res.data?.code === 0) {
      showForm.value = false
      toast('已保存抄表记录')
      await load(page.value)
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

function remove(r: MeterRecord) {
  confirmState.value = {
    show: true,
    message: `确定删除 ${r.room_no} ${r.period} 的抄表记录？`,
    action: async () => {
      const res = await deleteMeterRecord(r.id)
      if (res.data?.code !== 0) {
        toast(res.data?.message || '删除失败', 'error')
        return
      }
      toast('已删除')
      await load(page.value)
    },
  }
}

onMounted(async () => {
  await loadRooms()
  await load(1)
})
</script>
