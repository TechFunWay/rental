<template>
  <div class="surface rounded-2xl p-4 sm:p-6">
    <div class="flex items-center justify-between gap-3 mb-4">
      <div>
        <h3 class="text-sm sm:text-base font-bold text-foreground">收费项目</h3>
        <p class="text-xs text-muted-foreground mt-0.5">内置项目不可删除可停用；自定义项目（宽带费、停车费等）按各自周期出账</p>
      </div>
      <button v-if="ledgerStore.canEdit('bills')" class="btn-brand !px-3 !py-2 text-xs shrink-0" @click="openCreate">＋ 新增项目</button>
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
            <th class="py-2.5 px-3 font-medium whitespace-nowrap" title="单据上是否单列这一项（不影响账单金额）">单据显示</th>
            <th class="py-2.5 px-3 font-medium text-right whitespace-nowrap">操作</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr v-for="it in items" :key="it.id" class="hover:bg-muted/30 transition-colors">
            <td class="py-2.5 px-3 font-semibold text-foreground whitespace-nowrap">{{ it.name }}<span v-if="it.built_in" class="text-[10px] text-muted-foreground ml-1.5">内置</span></td>
            <td class="py-2.5 px-3 text-muted-foreground whitespace-nowrap">{{ kindText(it) }}</td>
            <td class="py-2.5 px-3 text-muted-foreground whitespace-nowrap">
              {{ cycleText(it) }}
              <!-- 有生效月份的项目标出来：否则房东看到「取暖费 300/月」会以为全年都在收 -->
              <span
                v-if="it.season_from_month || it.season_to_month"
                class="badge ml-1.5 !px-1.5 !py-0 text-[10px] bg-amber-500/10 text-amber-600 dark:text-amber-300"
              >{{ seasonText(it.season_from_month, it.season_to_month) }}</span>
            </td>
            <td class="py-2.5 px-3 text-right tabular-nums">{{ it.kind === 'fixed' ? fmt(it.default_amount) : '—' }}</td>
            <td class="py-2.5 px-3">
              <span v-if="it.enabled" class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">启用</span>
              <span v-else class="badge bg-muted text-muted-foreground">已停用</span>
            </td>
            <!-- 单据显示：只管打印/预览，账单金额与账单详情不受影响。
                 停用的项目出账时本来就不收，单据自然不出现；这里管的是
                 「还在收、但不想在单据上单列」的项目。 -->
            <td class="py-2.5 px-3">
              <button
                v-if="ledgerStore.canEdit('bills')"
                type="button"
                class="badge !px-1.5 !py-0.5 text-[11px]"
                :class="it.receipt_hidden
                  ? 'bg-muted text-muted-foreground'
                  : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'"
                :title="it.receipt_hidden ? '单据上不单列（有金额时合并成「其他费用」）。点此改为显示' : '单据上单列这一行。点此改为不显示'"
                @click="toggleReceiptHidden(it)"
              >{{ it.receipt_hidden ? '不显示' : '显示' }}</button>
              <span v-else class="badge !px-1.5 !py-0.5 text-[11px]" :class="it.receipt_hidden ? 'bg-muted text-muted-foreground' : 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'">{{ it.receipt_hidden ? '不显示' : '显示' }}</span>
            </td>
            <td class="py-2.5 px-3 text-right whitespace-nowrap">
              <button v-if="ledgerStore.canEdit('bills')" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(it)">编辑</button>
              <button v-if="ledgerStore.canFull('bills') && !it.built_in" class="text-destructive hover:underline" @click="remove(it)">删除</button>
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
          <div class="text-[11px] text-muted-foreground mt-0.5">
            {{ kindText(it) }} · {{ cycleText(it) }}
            <template v-if="it.kind === 'fixed' && it.default_amount > 0"> · {{ fmt(it.default_amount) }} 元/期</template>
            <!-- 手机端同样标出生效月份，避免以为取暖费全年都收 -->
            <span v-if="it.season_from_month || it.season_to_month" class="text-amber-600 dark:text-amber-300">
              · 仅 {{ seasonText(it.season_from_month, it.season_to_month) }}
            </span>
          </div>
        </div>
        <div class="flex items-center gap-2 text-xs shrink-0">
          <span v-if="it.enabled" class="badge !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">启用</span>
          <span v-else class="badge !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">停用</span>
          <!-- 单据显示（手机端同样可切换）：只管打印，不影响账单金额 -->
          <button
            v-if="ledgerStore.canEdit('bills')"
            type="button"
            class="badge !px-1.5 !py-0.5 text-[11px]"
            :class="it.receipt_hidden ? 'bg-muted text-muted-foreground' : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            @click="toggleReceiptHidden(it)"
          >单据{{ it.receipt_hidden ? '不显' : '显示' }}</button>
          <button v-if="ledgerStore.canEdit('bills')" class="text-brand-600 dark:text-brand-300" @click="openEdit(it)">编辑</button>
          <button v-if="ledgerStore.canFull('bills') && !it.built_in" class="text-destructive" @click="remove(it)">删除</button>
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
            <!-- 自定义收费项目本次仍只放开「月付 / 季付」两项（后端虽已支持四周期，
                 但自定义项目不在本次需求范围内，少放两项可避免用户误配）。
                 文案与提交值都从 utils/cycle.ts 取，不写字面量：季付一次并入
                 3 个月金额，新增周期时这里不会显示错。 -->
            <select v-model="form.cycle" class="input-field">
              <option v-for="o in cycleOptions" :key="o.value" :value="o.value">
                {{ o.label }}（每 {{ cycleMonths(o.value) }} 个月一次）
              </option>
            </select>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">金额（元/期）</label>
            <input v-model.number="form.default_amount" type="number" step="0.01" min="0" class="input-field" placeholder="如 30" />
          </div>
        </div>
        <!-- 生效月份（issue #5 需求 6）：取暖费这类「只在供暖季收」的项目用它。
             两个都选「不限」= 全年都收，与原有项目完全一致。 -->
        <div class="rounded-xl border border-border p-3 space-y-2">
          <div class="flex items-center justify-between gap-2">
            <span class="text-xs font-semibold text-foreground">生效月份</span>
            <span class="text-[11px] text-muted-foreground">默认全年都收</span>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="text-[11px] text-muted-foreground block mb-1">起始月</label>
              <select v-model.number="form.season_from_month" class="input-field !py-2">
                <option :value="0">不限</option>
                <option v-for="m in 12" :key="'f' + m" :value="m">{{ m }} 月</option>
              </select>
            </div>
            <div>
              <label class="text-[11px] text-muted-foreground block mb-1">结束月</label>
              <select v-model.number="form.season_to_month" class="input-field !py-2">
                <option :value="0">不限</option>
                <option v-for="m in 12" :key="'t' + m" :value="m">{{ m }} 月</option>
              </select>
            </div>
          </div>
          <p class="text-[11px] text-muted-foreground leading-4">
            留「不限」= 全年都收。填 <span class="font-medium text-foreground">11 月 → 3 月</span>
            表示跨年，只有这几个月出账（北方供暖季常见）；只填一头表示「该月之后 / 之前」。
            窗外的账期不收这项，也不影响其它费用。
          </p>
          <p v-if="form.season_from_month && form.season_to_month" class="text-[11px] text-brand-600 dark:text-brand-300">
            将在 {{ seasonText(form.season_from_month, form.season_to_month) }} 出账
          </p>
        </div>
        <label class="flex items-center gap-2 text-sm text-foreground">
          <input v-model="form.enabled" type="checkbox" class="w-4 h-4 accent-indigo-500" />
          启用（出账时并入每期账单）
        </label>
        <p class="text-[11px] text-muted-foreground">
          停用后<strong class="text-foreground">新账单不再收这一项</strong>（读数照常可以记录），
          已经开出的历史账单金额不变——要让它不在历史单据上出现，用下面的「单据显示」。
        </p>
        <div class="rounded-xl border border-border p-3 space-y-1.5">
          <label class="flex items-center gap-2 text-sm text-foreground">
            <input v-model="form.receipt_hidden" type="checkbox" class="w-4 h-4 accent-indigo-500" />
            单据上不单列这一项
          </label>
          <p class="text-[11px] text-muted-foreground leading-4">
            只影响打印/预览的收费单据，<strong class="text-foreground">账单金额、账单详情与统计都不变</strong>。
            勾选后单据不出现这一行；若本期它仍有金额，会合并成一行「其他费用」，
            明细相加仍等于合计。
          </p>
        </div>
        <p class="text-[11px] text-muted-foreground">自定义项目按固定金额 × 周期月数并入账单（季付一次收 3 个月）；水/电/燃气按抄表读数计费，不在此列。周期本次仍只提供月付/季付两项。</p>
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
import { computed, onMounted, ref } from 'vue'
import { useLedgerStore } from '../stores/ledger'
import ConfirmDialog from './ConfirmDialog.vue'
import Modal from './Modal.vue'
import { toast } from '../utils/toast'
import { createFeeItem, deleteFeeItem, getFeeItems, updateFeeItem, type FeeItem } from '../api/rental'
import { cycleLabel, cycleMonths, type PayCycle } from '../utils/cycle'

// 自定义收费项目本次只提供月付/季付两项（后端已支持四周期，此处刻意不放开）。
// 取值与文案集中在 utils/cycle.ts：既保证表单里的 value 是后端认的周期值，
// 也保证 cycleMonths 换算与「季付 = 3 个月」的展示一致。
const FEE_ITEM_CYCLES: { value: PayCycle; label: string }[] = [
  { value: 'monthly', label: cycleLabel('monthly') },
  { value: 'quarterly', label: cycleLabel('quarterly') },
]

// 下拉选项：默认就是上面两项。但后端 handleFeeItemCreate/Update 对 cycle 用的是
// normalizePayCycle（认四个周期），历史数据或直接调接口可能存进半年付/年付。
// 若原样把它绑进只有两项的 select，浏览器会匹配不到 option 而显示为空白（用户
// 看不到当前周期、一保存还可能被改成月付）。所以遇到两项之外的合法周期时，把
// 当前值**临时补进选项**并保持选中，让"展示"与"已存值"不自相矛盾。
const cycleOptions = computed(() => {
  const current = form.value.cycle
  if (!current || FEE_ITEM_CYCLES.some((o) => o.value === current)) return FEE_ITEM_CYCLES
  return [...FEE_ITEM_CYCLES, { value: current, label: cycleLabel(current) }]
})

const ledgerStore = useLedgerStore()
const items = ref<FeeItem[]>([])
const showModal = ref(false)
const editing = ref<FeeItem | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref({
  name: '', cycle: 'monthly' as PayCycle, default_amount: 0, enabled: true,
  // 单据不单列这一项（只影响单据展示，见后端 hiddenReceiptFeeKeys）
  receipt_hidden: false,
  // 生效月份窗口：0 = 不限（存量项目都是 0，行为不变）
  season_from_month: 0, season_to_month: 0,
})

function kindText(it: FeeItem): string {
  if (it.kind === 'meter') return '按抄表读数' + (it.unit ? `（${it.unit}）` : '')
  return '固定金额'
}

// 收租周期文案：走 cycleLabel，未知值由 normalizeCycle 归到月付，
// 不再用 `=== 'quarterly' ? 季付 : 月付` 的二元归一（半年付/年付会被吞成月付）。
function cycleText(it: FeeItem): string {
  if (it.kind === 'meter') return '按月收'
  return cycleLabel(it.cycle)
}

// seasonText 把生效月份渲染成「11–3 月」这样的短文案（给表单预览用）。
function seasonText(from: number, to: number): string {
  if (!from && !to) return '全年'
  if (!from) return `1–${to} 月`
  if (!to) return `${from}–12 月`
  if (from === to) return `${from} 月`
  return `${from}–${to} 月`
}

function fmt(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

function openCreate() {
  editing.value = null
  form.value = { name: '', cycle: 'monthly', default_amount: 0, enabled: true, receipt_hidden: false, season_from_month: 0, season_to_month: 0 }
  formError.value = ''
  showModal.value = true
}

function openEdit(it: FeeItem) {
  editing.value = it
  form.value = {
    name: it.name, cycle: it.cycle, default_amount: it.default_amount, enabled: it.enabled,
    receipt_hidden: it.receipt_hidden ?? false,
    season_from_month: it.season_from_month ?? 0, season_to_month: it.season_to_month ?? 0,
  }
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

// toggleReceiptHidden 直接切换「单据显示」，不走弹窗（这是个二元开关，弹窗反而更慢）。
//
// 后端 update 是**整体覆盖** name/cycle/default_amount（不是部分更新），所以这里
// 必须把原值一起带上，否则切一下开关就把项目名与金额清空了。
async function toggleReceiptHidden(it: FeeItem) {
  const next = !it.receipt_hidden
  const res = await updateFeeItem(it.id, {
    name: it.name, cycle: it.cycle, default_amount: it.default_amount,
    enabled: it.enabled, receipt_hidden: next,
    season_from_month: it.season_from_month ?? 0, season_to_month: it.season_to_month ?? 0,
  })
  if (res.data?.code !== 0) {
    toast(res.data?.message || '保存失败', 'error')
    return
  }
  toast(next ? '单据上不再单列这一项' : '单据上会单列这一项')
  await load()
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
