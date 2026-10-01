<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="房源管理" description="房号、默认租金与费用、单价覆盖及抄表底数">
      <template #actions>
        <button class="btn-ghost" @click="exportRooms">导出房源</button>
        <button class="btn-brand" @click="openCreate">＋ 新增房源</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="roomsFilterSummary" :active-count="roomsFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <input v-model="keyword" class="input-field flex-1 min-w-[200px] !py-2" placeholder="搜索房号 / 小区 / 楼栋 / 租户…" @keyup.enter="applyFilters" />
        <select v-model="status" class="input-field !py-2 !w-auto" @change="load(1)">
          <option value="">全部状态</option>
          <option value="occupied">在租</option>
          <option value="vacant">空闲</option>
        </select>
        <button class="btn-brand !py-2" @click="applyFilters">搜索</button>
      </div>
    </FilterPanel>

    <!-- 房源列表 -->
    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="rooms.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        暂无房源，点击右上角「新增房源」开始。
      </div>
      <template v-else>
      <!-- 桌面端：表格 -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th class="py-3 px-4 font-medium">位置</th>
              <th class="py-3 px-4 font-medium text-right">月租金（元）</th>
              <th class="py-3 px-4 font-medium text-right">卫生费（元/月）</th>
              <th class="py-3 px-4 font-medium text-right">管理费（元/月）</th>
              <th class="py-3 px-4 font-medium">电/燃气单价</th>
              <th class="py-3 px-4 font-medium">计费与缴费（随租户）</th>
              <th class="py-3 px-4 font-medium">在租租户</th>
              <th class="py-3 px-4 font-medium text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="r in rooms" :key="r.id" class="hover:bg-muted/30 transition-colors">
              <td class="py-3 px-4">
                <div class="font-semibold text-foreground">{{ r.room_no }}</div>
                <div class="text-xs text-muted-foreground">{{ locationText(r) }}</div>
              </td>
              <td class="py-3 px-4 text-right tabular-nums">{{ fmtMoney(r.default_rent) }}</td>
              <td class="py-3 px-4 text-right tabular-nums">{{ fmtMoney(r.default_sanitation_fee) }}</td>
              <td class="py-3 px-4 text-right tabular-nums">{{ fmtMoney(r.default_management_fee) }}</td>
              <td class="py-3 px-4 text-muted-foreground text-xs whitespace-nowrap">{{ priceText(r) }}</td>
              <td class="py-3 px-4 text-muted-foreground text-xs">{{ roomBillingSummary(r.billing) }}</td>
              <td class="py-3 px-4">
                <template v-if="r.current_tenants.length">
                  <span class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300 mr-1">
                    {{ r.current_tenants.map((t) => t.name).join('、') }}
                  </span>
                </template>
                <span v-else class="badge bg-muted text-muted-foreground">空闲</span>
              </td>
              <td class="py-3 px-4 text-right whitespace-nowrap">
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(r)">编辑</button>
                <button class="text-destructive hover:underline" @click="remove(r)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表 -->
      <div class="md:hidden p-2 space-y-1.5">
        <div v-for="r in rooms" :key="r.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ r.room_no }}</span>
              <span class="text-[11px] text-muted-foreground ml-1.5">{{ locationText(r) }}</span>
            </div>
            <span v-if="r.current_tenants.length" class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">
              {{ r.current_tenants.map((t) => t.name).join('、') }}
            </span>
            <span v-else class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">空闲</span>
          </div>

          <div class="text-[11px] text-muted-foreground tabular-nums rounded-md bg-muted/50 px-2 py-1">
            租金 {{ fmtMoney(r.default_rent) }} 元 · 卫生 {{ fmtMoney(r.default_sanitation_fee) }} 元/月 · 管理 {{ fmtMoney(r.default_management_fee) }} 元/月
            <div class="mt-0.5">单价 {{ priceText(r) }}</div>
            <div class="mt-0.5">计费 {{ roomBillingSummary(r.billing) }}</div>
          </div>

          <div class="flex items-center gap-3 pt-0.5 border-t border-border/60 text-xs">
            <button class="text-brand-600 dark:text-brand-300" @click="openEdit(r)">编辑</button>
            <button class="text-destructive ml-auto" @click="remove(r)">删除</button>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 新增/编辑弹窗 -->
    <Modal v-model="showModal" :title="editing ? '编辑房源' : '新增房源'">
      <form class="space-y-4" @submit.prevent="save">
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">小区</label>
          <input v-model="form.community" class="input-field" list="community-options" placeholder="选择或输入，单小区可留空" />
          <datalist id="community-options">
            <option v-for="c in communityOptions" :key="c" :value="c" />
          </datalist>
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-4 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">楼栋</label>
            <input v-model="form.building" class="input-field" list="building-options" placeholder="如 1栋" />
            <datalist id="building-options">
              <option v-for="b in buildingOptions" :key="b" :value="b" />
            </datalist>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">单元</label>
            <input v-model="form.unit" class="input-field" list="unit-options" placeholder="如 1单元" />
            <datalist id="unit-options">
              <option v-for="u in unitOptions" :key="u" :value="u" />
            </datalist>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">楼层</label>
            <input v-model="form.floor" class="input-field" list="floor-options" placeholder="如 3" />
            <datalist id="floor-options">
              <option v-for="f in floorOptions" :key="f" :value="f" />
            </datalist>
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">房号 *</label>
            <input v-model="form.room_no" class="input-field" placeholder="如 101" required />
            <p class="text-[11px] text-muted-foreground mt-1">同一小区内房号不能重复</p>
          </div>
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">月租金（元）</label>
            <input v-model.number="form.default_rent" type="number" step="0.01" min="0" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">卫生费（元/月）</label>
            <input v-model.number="form.default_sanitation_fee" type="number" step="0.01" min="0" class="input-field" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">管理费（元/月）</label>
            <input v-model.number="form.default_management_fee" type="number" step="0.01" min="0" class="input-field" />
          </div>
        </div>
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <p class="text-xs font-semibold text-muted-foreground">单价覆盖（留空或 0 表示使用偏好设置中的全局默认价）</p>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">电（元/度）</label>
              <input v-model.number="form.elec_price" type="number" step="0.01" min="0" class="input-field !py-2" />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">燃气（元/方）</label>
              <input v-model.number="form.gas_price" type="number" step="0.01" min="0" class="input-field !py-2" />
            </div>
          </div>
          <p class="text-[11px] text-muted-foreground">
            当前全局默认：电 {{ numText(billingDefaults.elecPrice) }} 元/度 · 燃气 {{ numText(billingDefaults.gasPrice) }} 元/方
          </p>
          <p class="text-[11px] text-muted-foreground bg-muted/60 rounded-lg px-2.5 py-2 leading-relaxed">
            水费按吨/包月、水费金额与缴费周期（月付/季付）、缴费日、提前提醒天数都按<strong class="text-foreground">租户</strong>设置：
            到「租户管理」登记或编辑租户时填写，留空则跟随全局默认。当前全局默认：{{ globalBillingText(billingDefaults) }}
          </p>
        </div>
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <p class="text-xs font-semibold text-muted-foreground">抄表底数（首次开账前的表读数）</p>
          <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">水表（吨）</label>
              <input v-model.number="form.initial_water" type="number" step="0.01" min="0" class="input-field !py-2" />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">电表（度）</label>
              <input v-model.number="form.initial_elec" type="number" step="0.01" min="0" class="input-field !py-2" />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">燃气表（方）</label>
              <input v-model.number="form.initial_gas" type="number" step="0.01" min="0" class="input-field !py-2" />
            </div>
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
      title="删除房源"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="confirmState.action?.()"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import FilterPanel from '../components/FilterPanel.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { toast } from '../utils/toast'
import {
  defaultBilling, fetchBillingDefaults, globalBillingText, numText, roomBillingSummary,
  type BillingDefaults,
} from '../utils/billing'
import {
  createRoom, deleteRoom, exportRooms, fmtMoney, getRooms, updateRoom,
  type Room, type RoomView,
} from '../api/rental'

const rooms = ref<RoomView[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const keyword = ref('')
const status = ref('')

// 手机端筛选摘要行与非默认条件数（徽标）
const roomsFilterSummary = computed(() =>
  [
    keyword.value ? `“${keyword.value}”` : '未搜索',
    status.value === 'occupied' ? '在租' : status.value === 'vacant' ? '空闲' : '全部状态',
  ].join(' · '),
)
const roomsFilterCount = computed(() => (keyword.value ? 1 : 0) + (status.value ? 1 : 0))

const filterRef = ref<InstanceType<typeof FilterPanel> | null>(null)

function applyFilters() {
  load(1)
  filterRef.value?.collapse()
}

const showModal = ref(false)
const editing = ref<RoomView | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref<Partial<Room>>({})

// 全局默认（偏好设置 → 租房设置）：单价与"随租户"计费设置的提示文案
const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })

// 单价列文案：电/燃气取房源覆盖价，未覆盖时用全局默认价，都带单位
function priceText(r: RoomView): string {
  const elec = (r.elec_price || 0) > 0 ? r.elec_price : billingDefaults.value.elecPrice
  const gas = (r.gas_price || 0) > 0 ? r.gas_price : billingDefaults.value.gasPrice
  return `电 ${numText(elec)} 元/度 / 燃气 ${numText(gas)} 元/方`
}

function openCreate() {
  editing.value = null
  form.value = {
    community: '', room_no: '', building: '', unit: '', floor: '',
    default_rent: 0, default_sanitation_fee: 0, default_management_fee: 0,
    elec_price: 0, gas_price: 0,
    initial_water: 0, initial_elec: 0, initial_gas: 0,
    notes: '',
  }
  formError.value = ''
  showModal.value = true
}

// 位置描述：小区 楼栋 单元 N层（空值跳过）
function locationText(r: RoomView): string {
  const parts = [r.community, r.building, r.unit, r.floor ? r.floor + '层' : '']
  const text = parts.filter(Boolean).join(' ')
  return text || '—'
}

// datalist 候选：从全部房源中提取已用过的位置值
const communityOptions = computed(() => [...new Set(allRooms.value.map((r) => r.community).filter(Boolean))])
const buildingOptions = computed(() => [...new Set(allRooms.value.map((r) => r.building).filter(Boolean))])
const unitOptions = computed(() => [...new Set(allRooms.value.map((r) => r.unit).filter(Boolean))])
const floorOptions = computed(() => [...new Set(allRooms.value.map((r) => r.floor).filter(Boolean))])

async function loadAllRooms() {
  const res = await getRooms({ page: 1, pageSize: 100 })
  if (res.data?.code === 0) allRooms.value = res.data.data.items || []
}

function openEdit(r: RoomView) {
  editing.value = r
  form.value = { ...r }
  formError.value = ''
  showModal.value = true
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getRooms({ page: p, pageSize, keyword: keyword.value, status: status.value })
    if (res.data?.code === 0) {
      rooms.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.room_no?.trim()) {
    formError.value = '房号不能为空'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    // 水费与缴费设置在租户上，房源只提交位置、默认费用、电/燃气单价与抄表底数。
    const payload: Partial<Room> = {
      community: form.value.community,
      room_no: form.value.room_no,
      building: form.value.building,
      unit: form.value.unit,
      floor: form.value.floor,
      default_rent: Number(form.value.default_rent) || 0,
      default_sanitation_fee: Number(form.value.default_sanitation_fee) || 0,
      default_management_fee: Number(form.value.default_management_fee) || 0,
      elec_price: Number(form.value.elec_price) || 0,
      gas_price: Number(form.value.gas_price) || 0,
      initial_water: Number(form.value.initial_water) || 0,
      initial_elec: Number(form.value.initial_elec) || 0,
      initial_gas: Number(form.value.initial_gas) || 0,
      notes: form.value.notes,
    }
    const res = editing.value
      ? await updateRoom(editing.value.id, payload)
      : await createRoom(payload)
    if (res.data?.code === 0) {
      showModal.value = false
      await loadAllRooms()
      await load(editing.value ? page.value : 1)
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch (err: any) {
    formError.value = err.response?.data?.message || '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, message: '', action: null as null | (() => Promise<void>) })

function remove(r: RoomView) {
  confirmState.value = {
    show: true,
    message: r.has_bills
      ? `房源 ${r.room_no} 存在账单记录，系统将拒绝删除。仍要尝试吗？`
      : `确定删除房源 ${r.room_no}？`,
    action: async () => {
      const res = await deleteRoom(r.id)
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
  billingDefaults.value = await fetchBillingDefaults()
  loadAllRooms()
  load(1)
})
</script>
