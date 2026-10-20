<template>
  <Modal v-model="show" title="续租" @update:model-value="onShowChange">
    <form class="space-y-4" @submit.prevent="save">
      <!-- 现状：租户、房间、当前到期日与续租基准 -->
      <div class="rounded-xl bg-muted/40 p-3 space-y-1">
        <p class="text-sm font-semibold text-foreground">
          {{ tenant?.name }}<span class="text-muted-foreground font-normal"> · {{ roomLabel }}</span>
        </p>
        <p class="text-xs text-muted-foreground">
          当前到期：<span class="text-foreground tabular-nums">{{ tenant?.lease_end_date || '未约定' }}</span>
          　·　续租基准：<span class="text-foreground tabular-nums">{{ baseDate }}</span>
          <span v-if="baseIsToday" class="text-amber-500">（原租期已到期，从今天起算）</span>
        </p>
        <p class="text-xs text-muted-foreground">
          月租金：<span class="text-foreground tabular-nums">{{ fmtMoney(currentRent) }}</span> 元
          <span v-if="tenant?.lease_end_date" class="text-muted-foreground">（房源默认月租，账单按它 × 月数收）</span>
        </p>
      </div>

      <!-- 续租时长：半年/一年最常用，放最前 -->
      <div>
        <label class="text-xs font-semibold text-muted-foreground block mb-1.5">续租时长</label>
        <LeaseTermChips
          v-model="months"
          :base-date="baseDate"
          base-label="续租基准"
          @pick="onPickTerm"
        />
      </div>

      <div class="grid grid-cols-2 gap-3">
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">或填新的到期日</label>
          <DateField v-model="customEnd" type="date" />
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">自定义月数（1~120）</label>
          <input
            v-model.number="months" type="number" min="1" max="120" class="input-field"
            placeholder="如 6"
            @input="customEnd = ''"
          />
        </div>
      </div>

      <!-- 续租预览：口头报价/签合同前要一眼看到新到期日 -->
      <p v-if="previewEnd" class="text-xs text-muted-foreground">
        续租后到期：<span class="text-foreground font-semibold tabular-nums">{{ previewEnd }}</span>
        <span class="text-muted-foreground">（{{ monthsText }}）</span>
      </p>
      <p v-else class="text-xs text-destructive">请选择续租时长或填写早于当前的到期日。</p>

      <!-- 调租金：续租最常见的连带动作，留空不动 -->
      <div class="rounded-xl border border-border p-3 space-y-2">
        <label class="flex items-center gap-2 text-sm text-foreground">
          <input v-model="changeRent" type="checkbox" class="w-4 h-4 accent-indigo-500" />
          顺便调整月租金
        </label>
        <div v-if="changeRent" class="flex items-center gap-2">
          <input v-model.number="newRent" type="number" step="0.01" min="0" class="input-field flex-1" placeholder="新租金" />
          <span class="text-xs text-muted-foreground shrink-0">
            {{ rentDiffText }}
          </span>
        </div>
        <p class="text-[11px] text-muted-foreground leading-4">
          改的是<span class="text-foreground">房源默认月租</span>：后续账单按新租金算，已开出的历史账单金额不变。
        </p>
      </div>

      <div>
        <label class="text-xs font-semibold text-muted-foreground block mb-1.5">备注（选填）</label>
        <input v-model="note" class="input-field" maxlength="200" placeholder="如：续半年、房东让利 100" />
      </div>

      <!-- 续租历史（有才显示） -->
      <div v-if="history.length" class="rounded-xl border border-border p-3">
        <p class="text-xs font-semibold text-muted-foreground mb-1.5">续租记录（{{ history.length }} 次）</p>
        <ul class="space-y-1 max-h-40 overflow-auto">
          <li v-for="h in history" :key="h.id" class="text-[11px] text-muted-foreground flex flex-wrap gap-x-2">
            <span class="tabular-nums text-foreground">{{ h.from_end_date || '—' }} → {{ h.to_end_date }}</span>
            <span>{{ h.months }} 个月</span>
            <span v-if="h.rent_before !== h.rent_after">租金 {{ fmtMoney(h.rent_before) }} → {{ fmtMoney(h.rent_after) }}</span>
            <span v-if="h.note" class="truncate max-w-[10rem]">{{ h.note }}</span>
          </li>
        </ul>
      </div>

      <p v-if="error" class="text-sm text-destructive">{{ error }}</p>
      <div class="flex gap-3 pt-1">
        <button type="button" class="btn-ghost flex-1" @click="show = false">取消</button>
        <button type="submit" class="btn-brand flex-1" :disabled="saving || !previewEnd">
          {{ saving ? '保存中…' : '确认续租' }}
        </button>
      </div>
    </form>
  </Modal>
</template>

<script setup lang="ts">
/**
 * RenewModal — 租约续租弹窗（2026-10-20 粉丝需求）。
 *
 * 为什么单独一个弹窗而不是在租户编辑表单里改到期日：续租是**带历史**的动作
 * （续了几次、每次多长、租金怎么变的），房东对账和年底跟家里交代都要翻得到；
 * 直接在编辑表单里覆盖 lease_end_date 只会丢掉这段历史。因此走
 * POST /rental/tenants/:id/renew：一次写到期日 + 留痕 + 可选调租金。
 *
 * 到期日口径统一走 utils/lease.ts（起租日 + N 月 − 1 天），与后端
 * server/rental/renew.go 的 leaseEndAfter 逐字一致——两边差一天，用户就会
 * 在打印的合同和账单上看到互相矛盾的两个日期。
 */
import { computed, ref, watch } from 'vue'
import Modal from './Modal.vue'
import DateField from './DateField.vue'
import LeaseTermChips from './LeaseTermChips.vue'
import { toast } from '../utils/toast'
import { getTenantRenewals, renewTenant, type LeaseRenewal } from '../api/renew'
import type { Tenant } from '../api/rental'
import { formatLeaseDate, parseLeaseDate, renewBaseDate, renewalEndDateStr } from '../utils/lease'

const props = withDefaults(defineProps<{
  modelValue: boolean
  tenant: Tenant | null
  /** 房间展示文案（如 A栋-101），由父组件按房间数据拼 */
  roomLabel?: string
  /** 房源默认月租 */
  currentRent?: number
}>(), { roomLabel: '', currentRent: 0 })

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 续租成功（父组件刷新列表） */
  renewed: [renewal: LeaseRenewal]
}>()

const show = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit('update:modelValue', v),
})

const months = ref(6)
const customEnd = ref('')
const changeRent = ref(false)
const newRent = ref(0)
const note = ref('')
const saving = ref(false)
const error = ref('')
const history = ref<LeaseRenewal[]>([])

/** 续租基准日 = max(当前到期日, 今天)，与后端 renewBase 同口径。 */
const baseDate = computed(() => {
  const d = renewBaseDate(props.tenant?.lease_end_date ?? '')
  return formatLeaseDate(d)
})

/** 原租期是否已过期（基准日落到今天）——文案要说明为什么从今天起算。 */
const baseIsToday = computed(() => {
  const end = parseLeaseDate(props.tenant?.lease_end_date ?? '')
  const today = new Date()
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  return !end || end.getTime() < start.getTime()
})

/** 预览的新到期日：自定义日期优先，否则按月数推算。 */
const previewEnd = computed(() => {
  const custom = customEnd.value.trim()
  if (custom) {
    const base = parseLeaseDate(baseDate.value)
    const parsed = parseLeaseDate(custom)
    if (!base || !parsed || parsed.getTime() <= base.getTime()) return ''
    return custom
  }
  if (!months.value || months.value < 1) return ''
  return renewalEndDateStr(props.tenant?.lease_end_date ?? '', months.value)
})

const monthsText = computed(() => {
  // 自定义到期日时月数由服务端按自然月回推（monthsBetweenLease），前端不猜测，
  // 只在保存成功后用返回的 months 展示——避免用户看到两个不同的数字。
  if (customEnd.value.trim()) return '按自定义到期日'
  return months.value >= 12 && months.value % 12 === 0
    ? `${months.value} 个月（${months.value / 12} 年）`
    : `${months.value} 个月`
})

const rentDiffText = computed(() => {
  const diff = (newRent.value || 0) - (props.currentRent || 0)
  if (!props.currentRent) return ''
  if (diff === 0) return '与原租金相同'
  const pct = ((diff / props.currentRent) * 100).toFixed(1)
  return diff > 0 ? `涨 ${fmtMoney(diff)} 元（+${pct}%）` : `降 ${fmtMoney(-diff)} 元（${pct}%）`
})

function fmtMoney(v: number | undefined | null) {
  return Number(v || 0).toFixed(2).replace(/\.00$/, '')
}

function onPickTerm(payload: { months: number; endDate: string }) {
  // chip 已经算好到期日：直接清空自定义日期，让预览走月数分支（与后端同口径）。
  if (payload.months > 0) customEnd.value = ''
}

// 打开时重置表单并拉历史：弹窗复用同一实例，不重置会串上一次的输入。
watch(() => props.modelValue, (open) => {
  if (!open) return
  error.value = ''
  saving.value = false
  months.value = 6
  customEnd.value = ''
  changeRent.value = false
  newRent.value = props.currentRent || 0
  note.value = ''
  history.value = []
  if (props.tenant) loadHistory(props.tenant.id)
})

async function loadHistory(tenantId: number) {
  try {
    const res = await getTenantRenewals(tenantId)
    history.value = (res.data?.data?.items ?? []) as LeaseRenewal[]
  } catch {
    // 历史加载失败不影响续租主流程：静默降级，只是不显示记录。
    history.value = []
  }
}

function onShowChange(v: boolean) {
  if (!v) emit('update:modelValue', false)
}

async function save() {
  if (!props.tenant) return
  error.value = ''
  const payload: { months?: number; to_end_date?: string; rent?: number; note?: string } = {}
  const custom = customEnd.value.trim()
  if (custom) {
    payload.to_end_date = custom
  } else {
    if (!months.value || months.value < 1) {
      error.value = '请选择续租时长或填写新的到期日'
      return
    }
    payload.months = months.value
  }
  if (changeRent.value) payload.rent = Number(newRent.value) || 0
  if (note.value.trim()) payload.note = note.value.trim()

  saving.value = true
  try {
    const res = await renewTenant(props.tenant.id, payload)
    const renewal = res.data?.data?.renewal
    if (renewal) {
      toast(`已续租至 ${renewal.to_end_date}`)
      emit('renewed', renewal)
    } else {
      toast('续租成功')
    }
    show.value = false
  } catch (e: unknown) {
    const msg = (e as { response?: { data?: { message?: string } } })?.response?.data?.message
    error.value = msg || '续租失败，请重试'
  } finally {
    saving.value = false
  }
}
</script>
