<template>
  <Modal :model-value="modelValue" title="抄表开票" @update:model-value="emit('update:modelValue', $event)">
    <form class="space-y-4" @submit.prevent="saveReading">
      <!-- 独立表单：自选账期 + 全部在租房间，未建账的保存时自动生成 -->
      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">账期</label>
          <DateField v-model="readingPeriod" type="month" @change="onPeriodChange" />
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">房间（在租）</label>
          <select
            :value="readingBillId || readingRoomId"
            class="input-field"
            :disabled="readingLoading || readingCandidates.length === 0"
            @change="onReadingPick($event)"
          >
            <option v-if="readingCandidates.length === 0" :value="0" disabled>暂无在租房间</option>
            <option v-for="c in readingCandidates" :key="c.bill?.id ?? c.room.id" :value="c.bill?.id ?? c.room.id">
              {{ c.bill ? '✓ ' : '' }}{{ c.room.room_no }} · {{ c.bill?.tenant_name || c.room.current_tenants?.map((t) => t.name).join('、') || '—' }}
            </option>
          </select>
        </div>
      </div>
      <div v-if="readingCandidates.length" class="text-xs text-muted-foreground">
        本月已抄 <strong class="text-foreground tabular-nums">{{ readingBillCount }}</strong> / {{ readingCandidates.length }} 间，保存后自动跳下一间未抄的。
      </div>
      <p v-if="readingSkippedQuarterly > 0" class="text-xs text-muted-foreground bg-muted/60 rounded-lg px-3 py-2">
        {{ readingSkippedQuarterly }} 间季付房本月非账单月，未列入名单（每 3 个月抄表开票一次）；如需临时开票，可先把房源改回月付。
      </p>

      <div v-if="readingLoading" class="py-8 text-center text-sm text-muted-foreground">加载中…</div>

      <div v-else-if="readingCandidates.length === 0" class="py-6 text-center">
        <p class="text-sm text-muted-foreground">没有在租房间。请先到「房源管理」添加房源并登记租户。</p>
      </div>

      <template v-else-if="readingForm">
        <div class="rounded-xl border border-border p-2.5 sm:p-3.5 space-y-2 sm:space-y-3">
          <p class="text-xs font-semibold text-muted-foreground">录入本月读数（上月读数自动带入）</p>
          <!-- 水费：包月房不抄表，只核对包月金额 -->
          <div v-if="readingForm" class="rounded-lg bg-muted/60 px-2.5 py-2">
            <div class="flex items-center justify-between mb-1 gap-2">
              <span class="text-sm font-medium text-foreground flex items-center gap-2">
                水费
                <span class="text-[11px] px-1.5 py-0.5 rounded-md" :class="readingForm.water_mode === 'monthly' ? 'bg-brand-500/10 text-brand-600 dark:text-brand-300' : 'bg-muted text-muted-foreground'">
                  {{ readingForm.water_mode === 'monthly' ? '包月' : '按吨' }}
                </span>
              </span>
              <span class="text-[11px] tabular-nums text-muted-foreground">
                费用 <strong class="text-foreground">{{ fmtMoney(feeExcluded('water') ? 0 : waterPreview(readingForm)) }}</strong> 元
              </span>
            </div>
            <p v-if="feeExcluded('water')" class="text-[11px] text-muted-foreground mb-1">本租户不参与水费收费，出账费用为 0（读数仍会记录）。</p>
            <label v-if="readingForm.water_mode === 'monthly'" class="block">
              <span class="text-[11px] text-muted-foreground block sm:hidden">包月金额（元/月）</span>
              <span class="hidden text-[11px] text-muted-foreground">包月金额（元/月），不抄表</span>
              <input v-model.number="readingForm.water_price" type="number" step="0.01" min="0" class="input-field !px-2 !py-1.5 text-right" />
            </label>
            <div v-else class="grid grid-cols-2 sm:grid-cols-3 gap-1.5 sm:gap-2 items-end">
              <div class="hidden sm:block">
                <span class="text-[11px] text-muted-foreground block">上月读数（吨）</span>
                <div class="input-field !px-2 !py-1.5 text-right bg-muted/80 text-muted-foreground tabular-nums">{{ fmtRead(readingForm.water_last) }}</div>
              </div>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block sm:hidden">本月（吨，上月 {{ fmtRead(readingForm.water_last) }}）</span>
                <span class="hidden text-[11px] text-muted-foreground">本月读数（吨）*</span>
                <input v-model.number="readingForm.water_now" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/吨）</span>
                <input v-model.number="readingForm.water_price" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
            </div>
          </div>
          <div v-for="m in meterBlocks" :key="m.lastKey" class="rounded-lg bg-muted/60 px-2.5 py-2">
            <div class="flex items-center justify-between mb-1">
              <span class="text-sm font-medium text-foreground">{{ m.label }}</span>
              <span class="text-[11px] tabular-nums text-muted-foreground">
                费用 <strong class="text-foreground">{{ fmtMoney(feeExcluded(m.feeKey) ? 0 : previewFee(readingForm[m.lastKey], readingForm[m.nowKey], readingForm[m.priceKey])) }}</strong> 元
              </span>
            </div>
            <p v-if="feeExcluded(m.feeKey)" class="text-[11px] text-muted-foreground mb-1">本租户不参与{{ m.label }}收费，出账费用为 0（读数仍会记录）。</p>
            <!-- 手机端两列（本月读数为主），桌面端三列 -->
            <div class="grid grid-cols-2 sm:grid-cols-3 gap-1.5 sm:gap-2 items-end">
              <div class="hidden sm:block">
                <span class="text-[11px] text-muted-foreground block">上月读数（{{ m.unit }}）</span>
                <div class="input-field !px-2 !py-1.5 text-right bg-muted/80 text-muted-foreground tabular-nums">{{ fmtRead(readingForm[m.lastKey]) }}</div>
              </div>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block sm:hidden">本月（{{ m.unit }}，上月 {{ fmtRead(readingForm[m.lastKey]) }}）</span>
                <span class="hidden text-[11px] text-muted-foreground">本月读数（{{ m.unit }}）*</span>
                <input v-model.number="readingForm[m.nowKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/{{ m.unit }}）</span>
                <input v-model.number="readingForm[m.priceKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" />
              </label>
            </div>
          </div>
        </div>
        <div class="rounded-xl bg-muted p-3 text-sm flex items-center justify-between">
          <span class="text-muted-foreground">应付合计（含租金与其他费用）</span>
          <strong class="text-foreground tabular-nums">{{ fmtMoney(readingTotal()) }} 元</strong>
        </div>
      </template>

      <p v-if="readingError" class="text-sm text-destructive">{{ readingError }}</p>
      <div class="flex gap-3 pt-1">
        <button type="button" class="btn-ghost flex-1" @click="close">取消</button>
        <button type="submit" class="btn-brand flex-1" :disabled="saving || !readingForm">{{ saving ? '保存中…' : '保存读数' }}</button>
      </div>
    </form>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import DateField from './DateField.vue'
import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import { defaultBilling, fetchBillingDefaults, meterUnit, type BillingDefaults } from '../utils/billing'
import { cycleMonths, onBillSchedule } from '../utils/cycle'
import {
  createBill, fmtMoney, getBills, getRooms, updateBill,
  type Bill, type RoomView,
} from '../api/rental'

// 抄表开票弹窗（账单页与抄表记录页共用）：录本月读数，保存即建账/更新账单，
// 连续抄完一间自动跳下一间未抄的。纯读数台账（不建账）在抄表记录页「录入读数」。
const props = defineProps<{
  modelValue: boolean
  /** 打开时定位到已有账单（账单列表点「抄表」编辑读数）；空 = 从当月自由抄 */
  initialBill?: Bill | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: []
}>()

const saving = ref(false)

// 全局默认（偏好设置 → 租房设置）：抄表预览里电/燃气单价的兜底值
const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })
fetchBillingDefaults().then((d) => { billingDefaults.value = d })

const readingPeriod = ref(currentPeriod())
const readingBills = ref<Bill[]>([])
const readingPrevBills = ref<Bill[]>([])
const readingRooms = ref<RoomView[]>([])
const readingLoading = ref(false)
const readingBillId = ref(0) // >0：已有账单的 id
const readingRoomId = ref(0) // 无账单时选中的房间 id
const readingForm = ref<Bill | null>(null)
const readingIsNew = ref(false) // true = 该房间该月还没有账单，保存走建账
const readingError = ref('')

// meterBlocks 电/燃气抄表块（水费单独成块，因为可按吨/包月切换）
// feeKey 用于租户收费项目排除判断（被排除项目费用按 0 预估，读数照常录入）
const meterBlocks = [
  { label: '电费', feeKey: 'elec', unit: meterUnit('elec'), lastKey: 'elec_last', nowKey: 'elec_now', priceKey: 'elec_price' },
  { label: '燃气费', feeKey: 'gas', unit: meterUnit('gas'), lastKey: 'gas_last', nowKey: 'gas_now', priceKey: 'gas_price' },
] as const

function previewFee(last: number, now: number, price: number): number {
  const usage = now > last ? now - last : 0
  return Math.round(usage * price * 100) / 100
}

// 本租户不参与计费的项目（已有账单取账单快照，新账单取房间生效设置，
// 两处都在表单的 excluded_fees 上）
function feeExcluded(key: string): boolean {
  return (readingForm.value?.excluded_fees ?? []).includes(key)
}

// waterPreview 水费预估：包月取每月固定金额（water_price 存的就是元/月）×
// 账单覆盖月数（季付 ×3），按吨按用量 × 单价——与服务端 Bill.waterFee 同一口径。
function waterPreview(f: Pick<Bill, 'water_mode' | 'water_last' | 'water_now' | 'water_price' | 'pay_cycle'> | null): number {
  if (!f) return 0
  if (f.water_mode === 'monthly') {
    return Math.round((f.water_price || 0) * cycleMonths(f.pay_cycle) * 100) / 100
  }
  return previewFee(f.water_last, f.water_now, f.water_price)
}

function fmtRead(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// 抄表读数文案（带单位）：包月水费不看表读数，直接标"包月"；按吨显示 上月→本月 吨

// 上月账期：YYYY-MM → 上个月
function prevPeriod(period: string): string {
  const [y, m] = period.split('-').map(Number)
  if (!y || !m) return period
  const d = new Date(y, m - 2, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

// 抄表候选：全部在租房间；季付房只在账单月出现（与最近一张账期整差 3 个月，
// 已有本月账单的总是出现便于编辑）。每间标注该账期是否已有账单及对应账单 id。
interface ReadingCandidate {
  room: RoomView
  bill: Bill | null
}

const readingCandidates = computed<ReadingCandidate[]>(() => {
  const byRoom = new Map<number, Bill>()
  for (const b of readingBills.value) byRoom.set(b.room_id, b)
  return readingRooms.value
    .map((room) => ({ room, bill: byRoom.get(room.id) ?? null }))
    .filter((c) => c.bill !== null || onBillSchedule(c.room.billing?.pay_cycle, readingPeriod.value, c.room.last_bill_period))
})

// 因季付非账单月而被过滤掉的房间数，用于弹窗里的提示。
const readingSkippedQuarterly = computed(() => {
  const withBill = new Set(readingBills.value.map((b) => b.room_id))
  return readingRooms.value.filter((room) =>
    !withBill.has(room.id) && !onBillSchedule(room.billing?.pay_cycle, readingPeriod.value, room.last_bill_period)).length
})

// 该账期"已有账单的房间数"，用于空态提示（一个账单都没有才提示先建账/直接抄表均可）
const readingBillCount = computed(() => readingBills.value.length)

async function loadReadingData() {
  readingLoading.value = true
  readingError.value = ''
  try {
    // 同时取本账期与上一账期的账单：后者用于未建账房间预览"上月读数"（与后端 prefillReadings 同口径）
    const [billsRes, prevRes, roomsRes] = await Promise.all([
      getBills({ page: 1, pageSize: 200, period: readingPeriod.value }),
      getBills({ page: 1, pageSize: 200, period: prevPeriod(readingPeriod.value) }),
      getRooms({ page: 1, pageSize: 200, status: 'occupied' }),
    ])
    readingBills.value = billsRes.data?.code === 0 ? billsRes.data.data.items || [] : []
    readingPrevBills.value = prevRes.data?.code === 0 ? prevRes.data.data.items || [] : []
    readingRooms.value = roomsRes.data?.code === 0 ? roomsRes.data.data.items || [] : []
  } catch {
    readingBills.value = []
    readingPrevBills.value = []
    readingRooms.value = []
    readingError.value = '加载失败，请重试'
  } finally {
    readingLoading.value = false
  }
}

// 选中某间房间：有账单带出账单；无账单构造预览行（本月读数留空待填，
// 上月读数取上一期账单的本月读数，无历史则房间底数——与后端 prefillReadings 同口径）
function selectReadingCandidate(c: ReadingCandidate | null) {
  if (!c) {
    readingForm.value = null
    readingIsNew.value = false
    return
  }
  if (c.bill) {
    readingBillId.value = c.bill.id
    readingRoomId.value = 0
    readingForm.value = { ...c.bill, water_mode: c.bill.water_mode || 'meter' }
    readingIsNew.value = false
  } else {
    const room = c.room
    const prev = readingPrevBills.value.find((b) => b.room_id === room.id)
    const waterLast = prev?.water_now ?? room.initial_water ?? 0
    const elecLast = prev?.elec_now ?? room.initial_elec ?? 0
    const gasLast = prev?.gas_now ?? room.initial_gas ?? 0
    // 水费计费方式与金额、缴费周期取房间生效设置（在租租户 → 全局默认，
    // 后端 room.billing 已解析好），与服务端 newBillFromRoom 同口径
    const billing = room.billing
    const waterMode = billing?.water_mode === 'monthly' ? 'monthly' : 'meter'
    const waterAmount = billing?.water_amount ?? 0
    const payCycle = billing?.pay_cycle === 'quarterly' ? 'quarterly' : 'monthly'
    // 季付一张账单覆盖 3 个月：租金/卫生/管理与包月水费按月数预填（与服务端一致）
    const months = cycleMonths(payCycle)
    readingBillId.value = 0
    readingRoomId.value = room.id
    readingForm.value = {
      id: 0, room_id: room.id, period: readingPeriod.value, room_no: room.room_no,
      tenant_name: room.current_tenants?.map((t) => t.name).join('、') || '—',
      rent: Math.round((room.default_rent || 0) * months * 100) / 100,
      water_last: waterLast, water_now: waterLast,
      elec_last: elecLast, elec_now: elecLast, gas_last: gasLast, gas_now: gasLast,
      pay_cycle: payCycle,
      water_mode: waterMode, water_price: waterAmount,
      elec_price: (room.elec_price || 0) > 0 ? room.elec_price : billingDefaults.value.elecPrice,
      gas_price: (room.gas_price || 0) > 0 ? room.gas_price : billingDefaults.value.gasPrice,
      sanitation_fee: Math.round((room.default_sanitation_fee || 0) * months * 100) / 100,
      management_fee: Math.round((room.default_management_fee || 0) * months * 100) / 100,
      // 该房生效的"不参与计费项目"（租户勾选），预估费用按 0 计；服务端建账时同口径
      excluded_fees: billing?.excluded_fees ?? [],
    } as Bill
    readingIsNew.value = true
  }
}

// 打开时初始化：传入 initialBill 定位到该账单，否则从当月开始自由抄
watch(() => props.modelValue, (open) => {
  if (!open) return
  readingError.value = ''
  const target = props.initialBill
  if (target) {
    readingPeriod.value = target.period
    loadReadingData().then(() => {
      const c = readingCandidates.value.find((x) => x.room.id === target.room_id) ?? null
      selectReadingCandidate(c)
    })
  } else {
    readingPeriod.value = currentPeriod()
    loadReadingData().then(() => {
      // 默认选第一间：优先已有账单的房间
      const first = readingCandidates.value.find((x) => x.bill) ?? readingCandidates.value[0] ?? null
      selectReadingCandidate(first)
    })
  }
})

// 账期变更：刷新数据后按当前表单的房间重新定位——否则表单停留在旧账期
// （update 态），保存会把读数打到旧账单上。
async function onPeriodChange() {
  const roomId = readingForm.value?.room_id
  await loadReadingData()
  if (roomId) {
    const c = readingCandidates.value.find((x) => x.room.id === roomId) ?? null
    selectReadingCandidate(c)
    return
  }
  const first = readingCandidates.value.find((x) => x.bill) ?? readingCandidates.value[0] ?? null
  selectReadingCandidate(first)
}

// 下拉按 id 分发：先按账单 id 找（已建账），再按房间 id 找（未建账）
function onReadingPick(e: Event) {
  const v = Number((e.target as HTMLSelectElement).value)
  const c = readingCandidates.value.find((x) => (x.bill ? x.bill.id === v : x.room.id === v))
  selectReadingCandidate(c ?? null)
  readingError.value = ''
}

function readingTotal(): number {
  const f = readingForm.value
  if (!f) return 0
  // 与服务端 recalc 同口径：被排除的项目按 0 计。
  const t =
    (feeExcluded('rent') ? 0 : (f.rent || 0)) +
    (feeExcluded('water') ? 0 : waterPreview(f)) +
    (feeExcluded('elec') ? 0 : previewFee(f.elec_last, f.elec_now, f.elec_price)) +
    (feeExcluded('gas') ? 0 : previewFee(f.gas_last, f.gas_now, f.gas_price)) +
    (feeExcluded('sanitation') ? 0 : (f.sanitation_fee || 0)) +
    (feeExcluded('management') ? 0 : (f.management_fee || 0))
  return Math.round(t * 100) / 100
}

async function saveReading() {
  const f = readingForm.value
  if (!f) return
  // 包月水费不看抄表，读数倒挂不拦
  if ((f.water_mode !== 'monthly' && (f.water_now ?? 0) < (f.water_last ?? 0)) ||
    (f.elec_now ?? 0) < (f.elec_last ?? 0) || (f.gas_now ?? 0) < (f.gas_last ?? 0)) {
    readingError.value = '本月读数不能小于上月读数，请核对后再保存'
    return
  }
  saving.value = true
  readingError.value = ''
  try {
    // 无账单：带读数直建账单；有账单：更新读数
    const res = readingIsNew.value
      ? await createBill(f.room_id, f.period, {
          water_now: f.water_now, elec_now: f.elec_now, gas_now: f.gas_now,
          water_mode: f.water_mode === 'monthly' ? 'monthly' : 'meter',
          water_price: f.water_price, elec_price: f.elec_price, gas_price: f.gas_price, rent: f.rent,
        })
      : await updateBill(f.id, f)
    if (res.data?.code === 0) {
      toast(`已保存 ${f.room_no} ${f.period} 抄表读数${readingIsNew.value ? '（已生成账单）' : ''}`)
      emit('saved')
      // 弹窗内连续抄表：刷新该账期名单（当前间标记已抄），自动跳到下一间未抄的；全部抄完才收弹窗
      await loadReadingData()
      const next = readingCandidates.value.find((x) => !x.bill)
      if (next) {
        selectReadingCandidate(next)
      } else {
        close()
        toast(`${readingPeriod.value} 全部房间已抄完`)
      }
    } else {
      readingError.value = res.data?.message || '保存失败'
    }
  } catch {
    readingError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

function close() {
  emit('update:modelValue', false)
}

function currentPeriod(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}
</script>
