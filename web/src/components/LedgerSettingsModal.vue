<template>
  <Modal v-model="visible" :title="`台账设置${ledger ? '：' + ledger.name : ''}`">
    <div class="space-y-4">
      <!-- 名称（仅管理者可改） -->
      <div>
        <label class="block text-sm font-medium text-foreground mb-1.5">台账名称</label>
        <div class="flex items-center gap-2">
          <input v-model="name" class="input-field flex-1" maxlength="50" :disabled="!isOwner" />
          <button v-if="isOwner" type="button" class="btn-ghost shrink-0" :disabled="savingName" @click="saveName">保存</button>
        </div>
      </div>

      <div v-if="loading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>

      <template v-else>
        <!-- 字段区独立滚动：手机端 11 个设置项一屏放不下，但不能把操作按钮顶出屏幕
             （2026-10-10 手机端实测：原来是整个面板滚动，保存按钮被顶到 1600px 处）。 -->
        <div class="space-y-3 overflow-y-auto -mr-1 pr-1 max-h-[46dvh] sm:max-h-[52dvh]">
        <div class="rounded-2xl bg-muted/60 px-4 py-3 text-xs leading-5 text-muted-foreground">
          水价、电价、气价、缴费周期这些计费口径<strong class="text-foreground">按台账各自一套</strong>：本台账没设置的项跟随系统级默认值。
          {{ isOwner ? '改完点「保存设置」即可。' : '只有台账管理者能修改，你可以看到当前生效值。' }}
        </div>

        <div v-for="group in SETTING_GROUPS" :key="group.title" class="space-y-3">
          <div class="text-xs font-bold text-muted-foreground uppercase tracking-widest">{{ group.title }}</div>
          <div v-for="item in group.items" :key="item.key" class="space-y-1">
            <div class="flex items-center gap-2">
              <label class="text-sm font-medium text-foreground">{{ item.label }}</label>
              <span
                class="badge !px-1.5 !py-0 text-[10px]"
                :class="overrides[item.key] !== undefined ? 'bg-brand-500/15 text-brand-600 dark:text-brand-300' : 'bg-muted text-muted-foreground'"
              >{{ overrides[item.key] !== undefined ? '本台账自定义' : '跟随系统' }}</span>
            </div>
            <select v-if="item.type === 'select'" v-model="form[item.key]" class="input-field" :disabled="!isOwner">
              <option v-for="opt in item.options" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
            </select>
            <input
              v-else
              v-model="form[item.key]"
              class="input-field"
              :type="item.type === 'int' ? 'number' : 'text'"
              :maxlength="item.maxlength"
              :placeholder="item.placeholder"
              :disabled="!isOwner"
            />
            <p class="text-[11px] text-muted-foreground">{{ item.hint }}</p>
          </div>
        </div>

        </div>

        <div v-if="isOwner" class="flex items-center gap-3 pt-3 border-t border-border">
          <button type="button" class="flex-1 px-4 py-2.5 rounded-xl text-sm font-semibold bg-brand-500 text-white hover:bg-brand-600 transition-colors disabled:opacity-60" :disabled="saving" @click="saveSettings">
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
          <button type="button" class="px-4 py-2.5 rounded-xl text-sm font-semibold text-muted-foreground hover:bg-muted transition-colors" :disabled="saving" @click="resetSettings">
            恢复跟随系统
          </button>
        </div>

        <!-- 删除共同台账（个人台账不可删、有数据不可删，后端会拦） -->
        <button
          v-if="isOwner && ledger && ledger.kind === 'shared'"
          type="button"
          class="w-full px-4 py-2.5 rounded-xl text-sm font-semibold text-destructive hover:bg-destructive/10 transition-colors"
          @click="askDelete"
        >
          删除这个共同台账
        </button>
      </template>
    </div>
  </Modal>

  <ConfirmDialog
    v-model="confirmOpen"
    title="删除共同台账？"
    message="删除后成员都会失去访问，且不可恢复；台账里还有房源、租户或账单时无法删除。"
    confirm-text="删除"
    confirm-type="danger"
    @confirm="doDelete"
  />
</template>

<script setup lang="ts">
// 台账设置：改名 + 按台账的租房设置（11 个 rental_* 键）+ 删除共同台账。
// 读=该台账正式成员，写=该台账管理者（后端逐接口判定，这里只是界面门禁）。
import { computed, ref, watch } from 'vue'
import Modal from './Modal.vue'
import ConfirmDialog from './ConfirmDialog.vue'
import {
  deleteLedger, getLedgerSettings, resetLedgerSettings, updateLedger, updateLedgerSettings,
  type LedgerSettingsPayload, type LedgerView,
} from '../api/ledger'
import { toast } from '../utils/toast'
import { cycleMonths, payCycleOptions } from '../utils/cycle'

interface SettingOption { value: string; label: string }
interface SettingDef {
  key: string
  label: string
  type: 'string' | 'int' | 'select'
  hint: string
  options?: SettingOption[]
  maxlength?: number
  placeholder?: string
}
interface SettingGroup { title: string; items: SettingDef[] }

/** 与后端 registerConfigs 的 11 个键、中文标签一一对应，按用途分三组。 */
const SETTING_GROUPS: SettingGroup[] = [
  {
    title: '计费单价',
    items: [
      { key: 'rental_water_price', label: '水费单价（元/吨）', type: 'string', hint: '按吨计费时的默认水价。' },
      {
        key: 'rental_water_mode', label: '水费计费方式', type: 'select',
        hint: '按吨按用量计费，或包月固定金额。',
        options: [
          { value: 'meter', label: '按吨计价（元/吨）' },
          { value: 'monthly', label: '包月（元/月）' },
        ],
      },
      { key: 'rental_water_monthly_fee', label: '水费包月金额（元/月）', type: 'string', hint: '计费方式为包月时的默认金额。' },
      { key: 'rental_elec_price', label: '电费单价（元/度）', type: 'string', hint: '生成账单时的默认电价。' },
      { key: 'rental_gas_price', label: '燃气单价（元/方）', type: 'string', hint: '生成账单时的默认气价。' },
    ],
  },
  {
    title: '缴费与提醒',
    items: [
      {
        key: 'rental_pay_cycle', label: '缴费周期', type: 'select',
        hint: '月付每月一张账单；季付/半年付/年付每 3/6/12 个月收一次固定费，水电梯度仍按月抄。',
        // 选项从 utils/cycle.ts 的 payCycleOptions 派生（与后端 ConfigDef.Options、
        // cycleLabel 同一张词表），新增周期时只改那一处，系统配置与台账配置不会走偏。
        options: payCycleOptions.map((o) => ({ value: o.value, label: `${o.label}（每 ${cycleMonths(o.value)} 个月一张账单）` })),
      },
      { key: 'rental_pay_day', label: '缴费日（每月几号）', type: 'int', hint: '每期收租日期，0 = 不提醒，最大 28。' },
      { key: 'rental_remind_days', label: '缴费提前提醒天数', type: 'int', hint: '缴费日前 N 天开始提醒，0 = 当天提醒。' },
      {
        // 用 select 而不是新增一个 bool 类型：本弹窗的渲染分支只有
        // select / int / text 三种，加 bool 要动它的类型与模板；
        // 而台账设置接口存的就是字符串，select 天然合适、也不引新分支。
        key: 'rental_carryover_enabled', label: '少交多交结转次月', type: 'select',
        // 默认关闭是刻意的（用户确认）：老用户账单金额一个子都不变，
        // 要用的房东自己在这里开；开启后只影响**开启之后新生成**的账单。
        hint: '开启后，上期少交的钱自动加进本期应缴、多交的自动抵扣本期应缴（例如 1 月欠 50，2 月应缴 = 费用 + 50）。默认关闭；开启只影响之后新生成的账单，历史账单不变。',
        options: [
          { value: 'false', label: '关闭（默认，账单金额不变）' },
          { value: 'true', label: '开启（上期差额自动带入本期）' },
        ],
      },
    ],
  },
  {
    title: '单据抬头',
    items: [
      { key: 'rental_property_name', label: '单据抬头（出租方名称）', type: 'string', hint: '打印收费单据时显示的出租方/物业名称。', maxlength: 100, placeholder: '例如：城东公寓管理处' },
      { key: 'rental_contact', label: '单据联系电话', type: 'string', hint: '打印收费单据时显示的联系电话。', maxlength: 50, placeholder: '例如：13800000000' },
      { key: 'rental_receipt_note', label: '单据底部备注', type: 'string', hint: '打印收费单据时显示在底部的提示文字。', maxlength: 200, placeholder: '例如：请于每月 5 日前缴纳' },
    ],
  },
]

const props = defineProps<{
  modelValue: boolean
  ledger: LedgerView | null
}>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  changed: []
}>()

const visible = ref(props.modelValue)
watch(() => props.modelValue, (v) => { visible.value = v })
watch(visible, (v) => emit('update:modelValue', v))

const isOwner = computed(() => props.ledger?.role === 'owner')
const name = ref('')
const form = ref<Record<string, string>>({})
const overrides = ref<Record<string, string>>({})
const loading = ref(false)
const saving = ref(false)
const savingName = ref(false)
const confirmOpen = ref(false)

async function load() {
  if (!props.ledger) return
  name.value = props.ledger.name
  loading.value = true
  try {
    const res = await getLedgerSettings(props.ledger.id)
    if (res.data?.code === 0) {
      const data = res.data.data
      overrides.value = data?.overrides || {}
      const next: Record<string, string> = { ...(data?.effective || {}) }
      // 覆盖过的键用覆盖值（与 effective 相同，这里只是保证一致）
      for (const [k, v] of Object.entries(overrides.value)) next[k] = v
      form.value = next
    } else {
      toast(res.data?.message || '加载设置失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '加载设置失败', 'error')
  } finally {
    loading.value = false
  }
}

watch(() => [props.modelValue, props.ledger?.id], () => {
  if (props.modelValue) void load()
}, { immediate: true })

async function saveName() {
  if (!props.ledger) return
  const trimmed = name.value.trim()
  if (!trimmed) {
    toast('请填写台账名称', 'error')
    return
  }
  savingName.value = true
  try {
    const res = await updateLedger(props.ledger.id, trimmed)
    if (res.data?.code === 0) {
      toast('台账名称已保存')
      emit('changed')
    } else {
      toast(res.data?.message || '保存失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '保存失败', 'error')
  } finally {
    savingName.value = false
  }
}

async function saveSettings() {
  if (!props.ledger) return
  saving.value = true
  try {
    const payload: LedgerSettingsPayload = {}
    for (const group of SETTING_GROUPS) {
      for (const item of group.items) {
        const v = form.value[item.key]
        if (v !== undefined) (payload as Record<string, string>)[item.key] = String(v)
      }
    }
    const res = await updateLedgerSettings(props.ledger.id, payload)
    if (res.data?.code === 0) {
      toast('设置已保存')
      overrides.value = res.data.data?.overrides || {}
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

async function resetSettings() {
  if (!props.ledger) return
  saving.value = true
  try {
    const res = await resetLedgerSettings(props.ledger.id)
    if (res.data?.code === 0) {
      toast('已恢复跟随系统')
      await load()
      emit('changed')
    } else {
      toast(res.data?.message || '重置失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '重置失败', 'error')
  } finally {
    saving.value = false
  }
}

function askDelete() {
  confirmOpen.value = true
}

async function doDelete() {
  if (!props.ledger) return
  try {
    const res = await deleteLedger(props.ledger.id)
    if (res.data?.code === 0) {
      toast('共同台账已删除')
      visible.value = false
      emit('changed')
    } else {
      toast(res.data?.message || '删除失败', 'error')
    }
  } catch (err: any) {
    toast(err?.response?.data?.message || '删除失败', 'error')
  }
}
</script>
