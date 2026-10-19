<template>
  <div class="space-y-6">
    <!-- Empty state -->
    <div v-if="groups.length === 0" class="surface rounded-2xl p-16 text-center">
      <div class="flex flex-col items-center gap-3 text-muted-foreground">
        <svg class="w-12 h-12 opacity-40" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="1.5" d="M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z"/></svg>
        <span class="text-sm">暂无配置项</span>
      </div>
    </div>

    <!-- Group panels -->
    <div
      v-for="group in groups"
      :key="group.key"
      class="surface rounded-2xl overflow-hidden"
    >
      <div class="border-b border-border p-5 sm:px-6">
        <h2 class="text-base font-bold text-foreground">{{ group.title }}</h2>
      </div>
      <div class="divide-y divide-border">
        <div
          v-for="item in group.items"
          :key="item.key"
          class="flex flex-col sm:flex-row sm:items-center gap-3 p-5 sm:px-6 hover:bg-muted/40 transition-colors"
        >
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="text-sm font-semibold text-foreground">{{ item.label }}</span>
              <code class="text-xs text-muted-foreground">{{ item.key }}</code>
              <span
                v-if="isDirty(item)"
                class="badge bg-amber-100 text-amber-700 dark:bg-amber-500/15 dark:text-amber-300"
              >未保存</span>
            </div>
            <div class="text-xs text-muted-foreground mt-1">{{ item.description || '—' }}</div>
          </div>
          <div class="flex items-center gap-2 sm:w-80">
            <!-- bool: accessible toggle switch -->
            <button
              v-if="item.type === 'bool'"
              type="button"
              role="switch"
              :aria-checked="getEditValue(item) === 'true'"
              :aria-label="item.label"
              :disabled="getSaving(item)"
              @click="toggleAndSaveBool(item)"
              :class="[
                'w-11 h-6 rounded-full transition-colors shrink-0 focus:outline-none focus:ring-4 focus:ring-brand-500/20',
                getEditValue(item) === 'true' ? 'bg-brand-500' : 'bg-muted',
                getSaving(item) ? 'opacity-60 cursor-not-allowed' : 'cursor-pointer',
              ]"
            >
              <span
                :class="[
                  'inline-block w-5 h-5 rounded-full bg-white shadow transform transition-transform mt-0.5',
                  getEditValue(item) === 'true' ? 'translate-x-5' : 'translate-x-0.5',
                ]"
              ></span>
            </button>

            <!-- select: native styled select -->
            <select
              v-else-if="item.type === 'select'"
              :value="getEditValue(item)"
              :disabled="getSaving(item)"
              @change="onSelectChange(item, ($event.target as HTMLSelectElement).value)"
              @keyup.enter="emitSave(item)"
              class="input-field !py-2 flex-1"
            >
              <option v-for="opt in item.options || []" :key="opt" :value="opt">
                {{ optionLabel(item.key, opt) }}
              </option>
            </select>

            <!-- int: number input -->
            <input
              v-else-if="item.type === 'int'"
              type="number"
              min="0"
              step="1"
              :value="getEditValue(item)"
              :disabled="getSaving(item)"
              @input="setEditValue(item, ($event.target as HTMLInputElement).value)"
              @keyup.enter="emitSave(item)"
              class="input-field !py-2 flex-1"
            />

            <!-- string: text input -->
            <input
              v-else
              type="text"
              :value="getEditValue(item)"
              :disabled="getSaving(item)"
              @input="setEditValue(item, ($event.target as HTMLInputElement).value)"
              @keyup.enter="emitSave(item)"
              class="input-field !py-2 flex-1"
            />

            <button
              v-if="item.type !== 'bool'"
              @click="emitSave(item)"
              :disabled="!isDirty(item) || getSaving(item)"
              class="btn-brand !px-4 !py-2 whitespace-nowrap"
            >
              {{ getSaving(item) ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import type { ConfigMeta } from '../api/config'
import { cycleMonths, payCycleOptions } from '../utils/cycle'

interface RowState {
  editValue: string
  saving: boolean
}

const props = defineProps<{
  items: ConfigMeta[]
}>()

const emit = defineEmits<{
  (e: 'save', key: string, value: string): void
}>()

const groupTitleMap: Record<string, string> = {
  general: '站点设置',
  access: '访问控制',
  appearance: '外观',
  system: '系统维护',
  // 租房管理自己的配置组（server/rental/rental.go 的 registerConfigs）
  billing: '计费默认',
  referral: '介绍费',
  rental: '租客登记与单据',
  export: '数据安全（定时导出）',
}

const selectOptionLabelMap: Record<string, Record<string, string>> = {
  rental_referral_policy: {
    free: '自愿填写（房东自己决定给不给、给多少）',
    min: '最低介绍费（房东必须填，且不低于下面的最低金额）',
    off: '无介绍费（不设这个功能）',
  },
  theme_mode: {
    system: '跟随系统',
    light: '浅色',
    dark: '深色',
  },
  rental_water_mode: {
    meter: '按吨计价（元/吨）',
    monthly: '包月（元/月）',
  },
  // 缴费周期的**选项**来自后端 ConfigDef.Options（monthly/quarterly/semiannual/
  // annual），这个元数据驱动的 select 直接按它渲染；这里只负责把值翻成中文，所以
  // 标签与 utils/cycle.ts 的 payCycleOptions 同源，后端再加周期时不会出现「选项有
  // 了、标签还是英文值」的错位。
  rental_pay_cycle: Object.fromEntries(
    payCycleOptions.map((o) => [o.value, `${o.label}（每 ${cycleMonths(o.value)} 个月一张账单）`]),
  ),
}

function groupTitle(key: string): string {
  return groupTitleMap[key] || key
}

function optionLabel(itemKey: string, opt: string): string {
  return selectOptionLabelMap[itemKey]?.[opt] || opt
}

const rowState = reactive<Record<string, RowState>>({})

// Sync per-row state with the prop:
//  - for new keys, seed editValue from the current value
//  - when an item's value prop changes externally and the row is not in-flight,
//    pull the new value into editValue (parent confirming save or external reload)
watch(
  () => props.items,
  (newItems) => {
    for (const item of newItems) {
      const existing = rowState[item.key]
      if (!existing) {
        rowState[item.key] = { editValue: item.value, saving: false }
      } else if (!existing.saving && existing.editValue !== item.value) {
        existing.editValue = item.value
      }
    }
  },
  { immediate: true, deep: true },
)

function ensureState(item: ConfigMeta) {
  if (!rowState[item.key]) {
    rowState[item.key] = { editValue: item.value, saving: false }
  }
}

function getEditValue(item: ConfigMeta): string {
  return rowState[item.key]?.editValue ?? item.value
}

function getSaving(item: ConfigMeta): boolean {
  return rowState[item.key]?.saving ?? false
}

function setEditValue(item: ConfigMeta, value: string) {
  ensureState(item)
  rowState[item.key].editValue = value
}

function isDirty(item: ConfigMeta): boolean {
  return getEditValue(item) !== item.value
}

function toggleAndSaveBool(item: ConfigMeta) {
  ensureState(item)
  const current = getEditValue(item)
  const next = current === 'true' ? 'false' : 'true'
  rowState[item.key].editValue = next
  if (next !== item.value && !getSaving(item)) {
    rowState[item.key].saving = true
    emit('save', item.key, next)
  }
}

function onSelectChange(item: ConfigMeta, value: string) {
  setEditValue(item, value)
}

function emitSave(item: ConfigMeta) {
  ensureState(item)
  if (!isDirty(item) || getSaving(item)) return
  rowState[item.key].saving = true
  emit('save', item.key, getEditValue(item))
}

// Public methods so the parent can flip saving back off after the request
// resolves — without these, the save button stays disabled until the parent's
// items prop change cascades through the watcher.
defineExpose({
  markSaved(key: string) {
    if (rowState[key]) rowState[key].saving = false
  },
  markFailed(key: string) {
    if (rowState[key]) rowState[key].saving = false
  },
})

// 面板顺序：按用户阅读习惯排（对外的东西在前，算钱的其次，站点/维护靠后）。
// 不在表里的组排在这些之后（按出现顺序）。
const groupOrder: string[] = ['rental', 'billing', 'export', 'access', 'general', 'backup', 'system']

// 组内条目顺序：同组里把「决策类」开关放最前、接着是跟随开关的配置，最后是纯展示项。
// 不在表里的键排在其组内已知键之后（保持后端返回顺序）。
const itemOrder: Record<string, string[]> = {
  rental: [
    'rental_ai_enabled', 'rental_ai_api_key', 'rental_ai_model', 'rental_ai_base_url',
    'rental_apply_base', 'rental_property_name', 'rental_contact', 'rental_receipt_note',
  ],
  billing: [
    'rental_water_mode', 'rental_water_price', 'rental_water_monthly_fee', 'rental_elec_price',
    'rental_gas_price', 'rental_pay_cycle', 'rental_pay_day', 'rental_remind_days',
  ],
  export: ['rental_export_enabled', 'rental_export_interval_hours', 'rental_export_keep_count'],
  access: [
    'allow_register', 'require_login', 'register_need_approval',
    'register_phone_enabled', 'register_phone_required', 'login_expiry_days',
  ],
}

const itemRank = (group: string, key: string): number => {
  const list = itemOrder[group]
  if (!list) return Number.MAX_SAFE_INTEGER
  const i = list.indexOf(key)
  return i === -1 ? Number.MAX_SAFE_INTEGER : i
}

const groups = computed(() => {
  const map = new Map<string, ConfigMeta[]>()
  for (const item of props.items) {
    const g = item.group || 'general'
    if (!map.has(g)) map.set(g, [])
    map.get(g)!.push(item)
  }
  const entries = Array.from(map.entries())
  // 组顺序：groupOrder 优先（按下标），未收录的组保持后端出现顺序排在最后。
  entries.sort((a, b) => {
    const ia = groupOrder.indexOf(a[0])
    const ib = groupOrder.indexOf(b[0])
    if (ia !== -1 && ib !== -1) return ia - ib
    if (ia !== -1) return -1
    if (ib !== -1) return 1
    return 0
  })
  return entries.map(([key, items]) => ({
    key,
    title: groupTitle(key),
    items: [...items].sort((a, b) => itemRank(key, a.key) - itemRank(key, b.key)),
  }))
})
</script>
