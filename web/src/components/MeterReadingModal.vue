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
            :value="currentCandidateValue"
            class="input-field"
            :disabled="readingLoading || readingCandidates.length === 0"
            @change="onReadingPick($event)"
          >
            <option v-if="readingCandidates.length === 0" :value="0" disabled>暂无在租房间</option>
            <!--
              value 必须带类型前缀（bill:5 / room:5）：原先是 `c.bill?.id ?? c.room.id`，
              把「账单 id」和「房间 id」塞进了同一个数值空间。两者是各自递增的两套 ID，
              必然重叠——比如账单 5 属于 12 号房，而 5 号房还没建账，两项 value 都是 5。
              用户选「5 号房」时 onReadingPick 会先按账单 id 命中 12 号房的账单，
              于是带出**别的房间**的租户与费用（粉丝报的正是这个现象）。
              加前缀后两种取值不可能相等，配对不再有歧义。
            -->
            <option v-for="c in readingCandidates" :key="c.bill?.id ?? c.room.id" :value="candidateValue(c)">
              {{ c.bill ? '✓ ' : '' }}{{ c.room.room_no }} · {{ c.bill?.tenant_name || c.room.current_tenants?.map((t) => t.name).join('、') || '—' }}
            </option>
          </select>
        </div>
      </div>
      <div v-if="readingCandidates.length" class="text-xs text-muted-foreground">
        本月已抄 <strong class="text-foreground tabular-nums">{{ readingBillCount }}</strong> / {{ readingCandidates.length }} 间，保存后自动跳下一间未抄的。
      </div>
      <p class="text-xs text-muted-foreground bg-muted/60 rounded-lg px-3 py-2">
        水/电/燃气每月抄；租金等固定费按缴费周期在到期月一次收。所有在租房间都在名单里。
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
            <p v-if="feeExcluded('water')" class="text-[11px] text-muted-foreground mb-1">
              {{ feeDisabledByLedger('water') ? '「水费」已在收费项目里停用' : '本租户不参与水费收费' }}，出账费用为 0（读数仍会记录）。
            </p>
            <label v-if="readingForm.water_mode === 'monthly'" class="block">
              <span class="text-[11px] text-muted-foreground block sm:hidden">包月金额（元/月）</span>
              <span class="hidden text-[11px] text-muted-foreground">包月金额（元/月），不抄表</span>
              <input v-model.number="readingForm.water_price" type="number" step="0.01" min="0" class="input-field !px-2 !py-1.5 text-right" @input="markEdited" />
            </label>
            <div v-else class="grid grid-cols-2 sm:grid-cols-3 gap-1.5 sm:gap-2 items-end">
              <div class="hidden sm:block">
                <span class="text-[11px] text-muted-foreground block">上月读数（吨）</span>
                <div class="input-field !px-2 !py-1.5 text-right bg-muted/80 text-muted-foreground tabular-nums">{{ fmtRead(readingForm.water_last) }}</div>
              </div>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block sm:hidden">本月（吨，上月 {{ fmtRead(readingForm.water_last) }}）</span>
                <span class="hidden text-[11px] text-muted-foreground">本月读数（吨）*</span>
                <input v-model.number="readingForm.water_now" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/吨）</span>
                <input v-model.number="readingForm.water_price" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" @input="markEdited" />
              </label>
            </div>
            <!-- 表盘照片：按「房间 + 账期」归属，与读数分开存（照片是现场凭证，
                 开票前后都能补拍；包月水不抄表就不显示水表照片入口） -->
            <div v-if="readingForm.water_mode !== 'monthly'" class="mt-1.5">
              <MeterPhotoPicker
                :room-id="readingForm.room_id"
                :period="readingForm.period"
                meter="water"
                label="水表"
                :can-edit="canEditMeterPhotos"
              />
            </div>
          </div>
          <div v-for="m in meterBlocks" :key="m.lastKey" class="rounded-lg bg-muted/60 px-2.5 py-2">
            <div class="flex items-center justify-between mb-1">
              <span class="text-sm font-medium text-foreground">{{ m.label }}</span>
              <span class="text-[11px] tabular-nums text-muted-foreground">
                费用 <strong class="text-foreground">{{ fmtMoney(feeExcluded(m.feeKey) ? 0 : previewFee(readingForm[m.lastKey], readingForm[m.nowKey], readingForm[m.priceKey])) }}</strong> 元
              </span>
            </div>
            <p v-if="feeExcluded(m.feeKey)" class="text-[11px] text-muted-foreground mb-1">
              {{ feeDisabledByLedger(m.feeKey) ? `「${m.label}」已在收费项目里停用` : `本租户不参与${m.label}收费` }}，出账费用为 0（读数仍会记录）。
            </p>
            <!-- 手机端两列（本月读数为主），桌面端三列 -->
            <div class="grid grid-cols-2 sm:grid-cols-3 gap-1.5 sm:gap-2 items-end">
              <div class="hidden sm:block">
                <span class="text-[11px] text-muted-foreground block">上月读数（{{ m.unit }}）</span>
                <div class="input-field !px-2 !py-1.5 text-right bg-muted/80 text-muted-foreground tabular-nums">{{ fmtRead(readingForm[m.lastKey]) }}</div>
              </div>
              <label class="block">
                <span class="text-[11px] text-muted-foreground block sm:hidden">本月（{{ m.unit }}，上月 {{ fmtRead(readingForm[m.lastKey]) }}）</span>
                <span class="hidden text-[11px] text-muted-foreground">本月读数（{{ m.unit }}）*</span>
                <input v-model.number="readingForm[m.nowKey]" type="number" step="1" class="input-field !px-2 !py-1.5 text-right" />
              </label>
              <label class="block">
                <span class="text-[11px] text-muted-foreground">单价（元/{{ m.unit }}）</span>
                <input v-model.number="readingForm[m.priceKey]" type="number" step="0.01" class="input-field !px-2 !py-1.5 text-right" @input="markEdited" />
              </label>
            </div>
            <div class="mt-1.5">
              <MeterPhotoPicker
                :room-id="readingForm.room_id"
                :period="readingForm.period"
                :meter="m.meter"
                :label="m.meterLabel"
                :can-edit="canEditMeterPhotos"
              />
            </div>
          </div>
        </div>
        <!-- 租金与备注可改：租金对应"本次覆盖的租期"（多月周期一次收 N 个月），
             备注用来记下租期口径，抄表开票时就能核对，不必事后回编辑账单页改 -->
        <div class="rounded-xl border border-border p-2.5 sm:p-3.5 space-y-2">
          <div class="flex items-start justify-between gap-2">
            <label class="text-xs font-semibold text-muted-foreground" for="mr-rent">
              租金（元/{{ readingCycleMonths > 1 ? '期' : '月' }}）
            </label>
            <span v-if="readingCycleMonths > 1" class="text-[11px] text-muted-foreground shrink-0">
              本次覆盖 {{ readingCycleMonths }} 个月（{{ cycleLabel(readingForm?.pay_cycle) }}）
            </span>
          </div>
          <input
            id="mr-rent"
            v-model.number="readingForm.rent"
            type="number" step="0.01" min="0"
            class="input-field !px-2 !py-1.5 text-right"
            :disabled="feeExcluded('rent')"
            @input="markEdited"
          />
          <p v-if="feeExcluded('rent')" class="text-[11px] text-muted-foreground">
            {{ feeDisabledByLedger('rent') ? '「租金」已在收费项目里停用' : '本租户不参与租金收费' }}，出账费用为 0。
          </p>
          <label class="block" for="mr-remark">
            <span class="text-[11px] text-muted-foreground">备注</span>
            <input
              id="mr-remark"
              v-model="readingForm.remark"
              class="input-field !px-2 !py-1.5 mt-1"
              placeholder="如：租金对应租期 2026-01 ~ 2026-06"
            />
          </label>
        </div>
        <!-- 自定义收费项目 / 押金 / 上期结转：开票时一并显示并计入合计。
             此前弹窗只算内置六项，保存后账单却含宽带费等自定义项目与押金，
             用户看到的「应付合计」比真实账单少（2026-10-17 粉丝反馈）。 -->
        <div
          v-if="additionalRows.length || previewCarry || previewLoading || previewError"
          class="rounded-xl border border-border p-2.5 sm:p-3 space-y-1.5"
        >
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-semibold text-muted-foreground">其他费用（并入本期账单）</span>
            <span v-if="previewLoading" class="text-[11px] text-muted-foreground">计算中…</span>
          </div>
          <div v-for="row in additionalRows" :key="row.key" class="flex items-start justify-between gap-3">
            <div class="min-w-0">
              <div class="text-xs text-foreground">{{ row.name }}</div>
              <div v-if="row.detail" class="text-[11px] text-muted-foreground">{{ row.detail }}</div>
            </div>
            <span class="text-xs text-foreground tabular-nums shrink-0">{{ fmtMoney(row.amount) }} 元</span>
          </div>
          <p v-if="previewError" class="text-[11px] text-amber-600 dark:text-amber-300">{{ previewError }}</p>
          <div v-if="previewCarry" class="flex items-center justify-between gap-3 border-t border-border/60 pt-1.5">
            <span class="text-xs text-muted-foreground">{{ previewCarry > 0 ? '加：上期少交结转' : '减：上期多交结转' }}</span>
            <span class="text-xs text-foreground tabular-nums">{{ previewCarry > 0 ? '+' : '-' }}{{ fmtMoney(Math.abs(previewCarry)) }} 元</span>
          </div>
        </div>
        <div class="rounded-xl bg-muted p-3 text-sm flex items-center justify-between">
          <span class="text-muted-foreground">本期应缴（含租金与其他费用）</span>
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
import MeterPhotoPicker from './MeterPhotoPicker.vue'
import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import { useLedgerStore } from '../stores/ledger'
import { defaultBilling, fetchBillingDefaults, meterUnit, type BillingDefaults } from '../utils/billing'
import { cycleLabel, cycleMonths, normalizeCycle } from '../utils/cycle'
import {
  createBill, fmtMoney, getAllBills, getAllRooms, previewBill, updateBill,
  type Bill, type BillItemDetail, type RoomView,
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

// 抄表照片归「抄表」模块：能录读数（meters 操作档）才显示拍照/删除入口，
// 只有查看档时照片照常可见（组件内部按 canEdit 区分）。
const ledgerStore = useLedgerStore()
const canEditMeterPhotos = computed(() => ledgerStore.canEdit('meters'))

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

// ---------- 开票预览（自定义收费项目 / 押金 / 上期结转） ----------
// 服务端按真实建账口径算（不写库），前端只把「非内置项目」抽出来展示；
// 内置的水电等仍按弹窗里的输入实时算，所以改读数时合计立刻跟着变。
const BUILTIN_FEE_KEYS = new Set(['rent', 'water', 'elec', 'gas', 'sanitation', 'management'])
const previewItems = ref<BillItemDetail[]>([])
const previewCarryIn = ref(0)
const previewLoaded = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
let previewToken = 0
// 用户是否手动改过租金/单价：预览返回前就改过的字段不能被服务端预填值覆盖。
const manualEdit = ref(false)

function markEdited() {
  manualEdit.value = true
}

interface AdditionalRow { key: string; name: string; detail: string; amount: number }

const additionalRows = computed<AdditionalRow[]>(() => {
  const rows = previewItems.value
    .filter((it) => !BUILTIN_FEE_KEYS.has(it.key))
    .map((it) => ({ key: it.key, name: it.name, detail: it.detail, amount: it.amount }))
  if (rows.length > 0) return rows
  // 预览失败或老账单明细缺失时的兜底：用账单自己的自定义合计与押金字段，
  // 金额仍然准确（只是不再逐项列出自定义项目名称）。
  const f = readingForm.value
  if (!f || readingIsNew.value) return rows
  if ((f.extra_amount || 0) > 0) {
    rows.push({ key: 'custom', name: '其他费用（自定义收费项目）', detail: '自定义收费项目合计', amount: f.extra_amount })
  }
  if ((f.deposit_fee || 0) > 0) {
    rows.push({ key: 'deposit', name: '押金', detail: '入住押金，退租时结算退还', amount: f.deposit_fee })
  }
  return rows
})

// 上期结转：预览成功后以服务端为准；预览失败时用已有账单快照兜底。
const previewCarry = computed(() => {
  if (previewLoaded.value) return previewCarryIn.value
  return readingForm.value?.carry_in || 0
})

// applyServerPrefill 用服务端预览账单的固定费/单价覆盖本地预填值。
// 只动金额与周期，不动用户已经录入的读数；手动改过金额时不覆盖。
function applyServerPrefill(bill: Bill, roomId: number, period: string) {
  const f = readingForm.value
  if (!f || !readingIsNew.value) return
  if (f.room_id !== roomId || f.period !== period) return
  f.rent = bill.rent
  f.water_price = bill.water_price
  f.elec_price = bill.elec_price
  f.gas_price = bill.gas_price
  f.sanitation_fee = bill.sanitation_fee
  f.management_fee = bill.management_fee
  f.water_mode = bill.water_mode || f.water_mode
  f.pay_cycle = bill.pay_cycle || f.pay_cycle
}

async function loadPreview(roomId: number, period: string) {
  const token = ++previewToken
  previewItems.value = []
  previewCarryIn.value = 0
  previewLoaded.value = false
  previewError.value = ''
  if (!roomId || !period) return
  previewLoading.value = true
  try {
    const res = await previewBill(roomId, period)
    if (token !== previewToken) return
    if (res.data?.code === 0) {
      const data = res.data.data as { exists: boolean; bill: Bill; items: BillItemDetail[] }
      previewItems.value = data.items ?? []
      previewCarryIn.value = Number(data.bill?.carry_in || 0)
      previewLoaded.value = true
      // 待生成账单：用服务端按周期锚点算好的固定费（租金/卫生/管理）与单价
      // 覆盖弹窗本地预填值。否则非锚点月会被本地预填成整期租金，预览与落库
      // 金额对不上（服务端 newBillFromRoom 在非锚点月固定费为 0）。
      if (!data.exists && !manualEdit.value) applyServerPrefill(data.bill, roomId, period)
    } else {
      previewError.value = res.data?.message || '费用预览加载失败'
    }
  } catch {
    if (token !== previewToken) return
    previewError.value = '费用预览加载失败，保存后以账单为准'
  } finally {
    if (token === previewToken) previewLoading.value = false
  }
}

// meterBlocks 电/燃气抄表块（水费单独成块，因为可按吨/包月切换）
// feeKey 用于租户收费项目排除判断（被排除项目费用按 0 预估，读数照常录入）
// meter / meterLabel 是抄表照片的表种键与文案（表种白名单与后端一致）
const meterBlocks = [
  { label: '电费', meterLabel: '电表', meter: 'elec', feeKey: 'elec', unit: meterUnit('elec'), lastKey: 'elec_last', nowKey: 'elec_now', priceKey: 'elec_price' },
  { label: '燃气费', meterLabel: '燃气表', meter: 'gas', feeKey: 'gas', unit: meterUnit('gas'), lastKey: 'gas_last', nowKey: 'gas_now', priceKey: 'gas_price' },
] as const

function previewFee(last: number, now: number, price: number): number {
  const usage = now > last ? now - last : 0
  return Math.round(usage * price * 100) / 100
}

// 本租户不参与计费的项目（已有账单取账单快照，新账单取房间生效设置，
// 两处都在表单的 excluded_fees 上）
//
// 2026-10-19 起还要并上台账**停用**的项目（disabled_fees）：停用是整台账级别的
// 计费开关，服务端 recalc 会把它清零。这里若不合并，弹窗的「本期应缴」仍会把
// 停用的燃气费算进去，保存后账单却少一截（粉丝反馈的正是这种"设置没生效"的观感）。
function feeDisabledByLedger(key: string): boolean {
  return (readingForm.value?.disabled_fees ?? []).includes(key)
}

function feeExcluded(key: string): boolean {
  return (readingForm.value?.excluded_fees ?? []).includes(key) || feeDisabledByLedger(key)
}

// waterPreview 水费预估：包月取每月固定金额（water_price 存的就是元/月），
// **不乘缴费周期月数**——服务端 waterFee 对包月也只收单月金额（见 bills.go 的
// waterFee 注释），此前这里 ×N 会让季付/半年付/年付房的预览比落库账单大。
// 按吨按用量 × 单价（与服务端同一口径）。
function waterPreview(f: Pick<Bill, 'water_mode' | 'water_last' | 'water_now' | 'water_price' | 'pay_cycle'> | null): number {
  if (!f) return 0
  if (f.water_mode === 'monthly') {
    return Math.round((f.water_price || 0) * 100) / 100
  }
  return previewFee(f.water_last, f.water_now, f.water_price)
}

// 当前表单的缴费周期覆盖月数（月付 1 / 季付 3 / 半年付 6 / 年付 12）：
// 租金输入框用它标注"本期覆盖 N 个月"，文案不再写死季付 ×3。
const readingCycleMonths = computed(() => cycleMonths(readingForm.value?.pay_cycle))

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

// 抄表候选：**全部在租房间**，不再按缴费周期过滤。
//
// 为什么：账单口径改为「固定费只在到期月按 N 个月一次收、水电燃气恒按月收」
// 后，账单每个月都会产生，水电也要月月抄——季付/半年付/年付房在非到期月
// 同样需要抄表。此前的 onBillSchedule 过滤（utils/cycle.ts 现已恒 true）会把
// 这些房间整月剔除，导致「年付房没法抄水电」。已有本月账单的房间照旧出现，
// 便于继续编辑；其余房间标注该账期是否已有账单及对应账单 id。
interface ReadingCandidate {
  room: RoomView
  bill: Bill | null
}

const readingCandidates = computed<ReadingCandidate[]>(() => {
  const byRoom = new Map<number, Bill>()
  for (const b of readingBills.value) byRoom.set(b.room_id, b)
  return readingRooms.value.map((room) => ({ room, bill: byRoom.get(room.id) ?? null }))
})

// 该账期"已有账单的房间数"，用于空态提示（一个账单都没有才提示先建账/直接抄表均可）
const readingBillCount = computed(() => readingBills.value.length)

async function loadReadingData() {
  readingLoading.value = true
  readingError.value = ''
  try {
    // 同时取本账期与上一账期的账单：后者用于未建账房间预览"上月读数"（与后端 prefillReadings 同口径）。
    // 用 getAllBills / getAllRooms 跨页拉全：服务端 pageSize 上限是 100，
    // 直接写 pageSize: 200 会被静默截断，房间多时列表缺房、配对串房。
    const [bills, prevBills, rooms] = await Promise.all([
      getAllBills({ period: readingPeriod.value }),
      getAllBills({ period: prevPeriod(readingPeriod.value) }),
      getAllRooms({ status: 'occupied' }),
    ])
    readingBills.value = bills
    readingPrevBills.value = prevBills
    readingRooms.value = rooms
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
  manualEdit.value = false
  if (!c) {
    readingForm.value = null
    readingIsNew.value = false
    previewItems.value = []
    previewCarryIn.value = 0
    previewLoaded.value = false
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
    // 缴费周期直接取房间生效设置并用 normalizeCycle 归一：原先写成
    // `=== 'quarterly' ? 'quarterly' : 'monthly'`，半年付/年付会被静默吞成
    // 月付，预填租金与包月水费都只按 1 个月算（少收）。normalizeCycle 认全
    // 四个合法周期，与后端 normalizePayCycle、utils/cycle.ts 同一张词表。
    const payCycle = normalizeCycle(billing?.pay_cycle)
    // 一个账单周期覆盖 N 个月（月付 1 / 季付 3 / 半年付 6 / 年付 12）：
    // 租金/卫生/管理与包月水费按月数预填（与服务端同一口径）
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
  // 自定义收费项目、押金与上期结转由服务端按真实建账口径预览（不写库），
  // 与弹窗里按输入实时算的内置项目合并成「本期应缴」。
  void loadPreview(c.room.id, readingPeriod.value)
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

// candidateValue 生成下拉选项的取值：带类型前缀，避免「账单 id」与「房间 id」
// 数值空间重叠导致串房（详见模板里的注释）。
function candidateValue(c: ReadingCandidate): string {
  return c.bill ? `bill:${c.bill.id}` : `room:${c.room.id}`
}

// 下拉当前选中项的 value，口径与 option 的 :value 必须完全一致——
// 原先这里也是 `readingBillId || readingRoomId`，与选项同一个坑：
// 选「未建账的 5 号房」时，若恰好存在账单 5，下拉会高亮成 12 号房。
const currentCandidateValue = computed(() => {
  if (readingBillId.value > 0) return `bill:${readingBillId.value}`
  if (readingRoomId.value > 0) return `room:${readingRoomId.value}`
  return ''
})

// 下拉按前缀精确分发：bill: → 账单 id，room: → 房间 id。
// 兼容解析纯数字（老值/外部调用），此时先按账单 id 找、再按房间 id 找。
function onReadingPick(e: Event) {
  const raw = (e.target as HTMLSelectElement).value
  let c: ReadingCandidate | undefined
  if (raw.startsWith('bill:')) {
    const id = Number(raw.slice(5))
    c = readingCandidates.value.find((x) => x.bill?.id === id)
  } else if (raw.startsWith('room:')) {
    const id = Number(raw.slice(5))
    c = readingCandidates.value.find((x) => !x.bill && x.room.id === id)
  } else {
    const id = Number(raw)
    c = readingCandidates.value.find((x) => (x.bill ? x.bill.id === id : x.room.id === id))
  }
  selectReadingCandidate(c ?? null)
  readingError.value = ''
}

function readingTotal(): number {
  const f = readingForm.value
  if (!f) return 0
  // 与服务端 recalc 同口径：被排除的项目按 0 计。
  const builtin =
    (feeExcluded('rent') ? 0 : (f.rent || 0)) +
    (feeExcluded('water') ? 0 : waterPreview(f)) +
    (feeExcluded('elec') ? 0 : previewFee(f.elec_last, f.elec_now, f.elec_price)) +
    (feeExcluded('gas') ? 0 : previewFee(f.gas_last, f.gas_now, f.gas_price)) +
    (feeExcluded('sanitation') ? 0 : (f.sanitation_fee || 0)) +
    (feeExcluded('management') ? 0 : (f.management_fee || 0))
  // 自定义收费项目/押金（逐项或兜底汇总）+ 上期结转，均来自服务端预览。
  const extra = additionalRows.value.reduce((sum, row) => sum + (row.amount || 0), 0)
  const t = builtin + extra + previewCarry.value
  return Math.round(t * 100) / 100
}

async function saveReading() {
  const f = readingForm.value
  if (!f) return
  // v-model.number 在输入框被清空时留下的是空串（不是数字）。直接提交会让后端
  // 的 *float64 绑定失败、只回一句「参数错误」；这里先按各自的语义归一：
  // 数字类字段清空按 0 处理，描述类字段清空按空串处理。
  const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)
  const text = (v: unknown): string => (typeof v === 'string' ? v : '')
  f.rent = num(f.rent)
  f.water_price = num(f.water_price)
  f.elec_price = num(f.elec_price)
  f.gas_price = num(f.gas_price)
  f.water_now = num(f.water_now)
  f.water_last = num(f.water_last)
  f.elec_now = num(f.elec_now)
  f.elec_last = num(f.elec_last)
  f.gas_now = num(f.gas_now)
  f.gas_last = num(f.gas_last)
  f.remark = text(f.remark)
  // 包月水费不看抄表，读数倒挂不拦
  if ((f.water_mode !== 'monthly' && (f.water_now ?? 0) < (f.water_last ?? 0)) ||
    (f.elec_now ?? 0) < (f.elec_last ?? 0) || (f.gas_now ?? 0) < (f.gas_last ?? 0)) {
    readingError.value = '本月读数不能小于上月读数，请核对后再保存'
    return
  }
  saving.value = true
  readingError.value = ''
  try {
    // 无账单：带读数直建账单，租金与备注一并提交（remark 是 createBill 新增参数，
    // 用来记录"租金对应哪个租期"）；有账单：更新读数、租金与备注。
    const res = readingIsNew.value
      ? await createBill(f.room_id, f.period, {
          water_now: f.water_now, elec_now: f.elec_now, gas_now: f.gas_now,
          water_mode: f.water_mode === 'monthly' ? 'monthly' : 'meter',
          water_price: f.water_price, elec_price: f.elec_price, gas_price: f.gas_price,
          rent: f.rent, remark: f.remark,
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
