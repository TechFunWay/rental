<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="房源管理" description="房号、默认租金与费用、单价覆盖及抄表底数">
      <template #actions>
        <button class="btn-ghost" @click="exportRooms">导出房源</button>
        <button v-if="ledgerStore.canFull('rooms')" class="btn-ghost" @click="showImport = true">导入</button>
        <button v-if="ledgerStore.canEdit('rooms')" class="btn-brand" @click="openCreate">＋ 新增房源</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="roomsFilterSummary" :active-count="roomsFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <input v-model="keyword" class="input-field flex-1 min-w-[200px] !py-2" placeholder="搜索房号 / 小区 / 标签 / 租户…" @keyup.enter="applyFilters" />
        <select v-model="filterCommunity" class="input-field !py-2 !w-auto max-w-[150px]" @change="onCommunityChange">
          <option value="">全部小区</option>
          <option v-for="c in communityFilterOptions" :key="c" :value="c">{{ c }}</option>
        </select>
        <select v-model="filterBuilding" class="input-field !py-2 !w-auto max-w-[130px]" @change="load(1)">
          <option value="">全部楼栋</option>
          <option v-for="b in buildingFilterOptions" :key="b" :value="b">{{ b }}</option>
        </select>
        <select v-model="filterTag" class="input-field !py-2 !w-auto max-w-[130px]" @change="load(1)">
          <option value="">全部标签</option>
          <option v-for="t in tagFilterOptions" :key="t" :value="t">{{ t }}</option>
        </select>
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
        暂无房源，点击右上角「新增房源」或「导入」批量建档。
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
                <div v-if="r.tags?.length" class="flex flex-wrap gap-1 mt-1">
                  <span v-for="t in r.tags" :key="t" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ t }}</span>
                </div>
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
                <button v-if="ledgerStore.canEdit('rooms')" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openApplyLink(r)">登记链接</button>
                <button v-if="ledgerStore.canEdit('rooms')" class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(r)">编辑</button>
                <button v-if="ledgerStore.canFull('rooms')" class="text-destructive hover:underline" @click="remove(r)">删除</button>
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
              <div v-if="r.tags?.length" class="flex flex-wrap gap-1 mt-1">
                <span v-for="t in r.tags" :key="t" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ t }}</span>
              </div>
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
            <button v-if="ledgerStore.canEdit('rooms')" class="text-brand-600 dark:text-brand-300" @click="openApplyLink(r)">登记链接</button>
            <button v-if="ledgerStore.canEdit('rooms')" class="text-brand-600 dark:text-brand-300" @click="openEdit(r)">编辑</button>
            <button v-if="ledgerStore.canFull('rooms')" class="text-destructive ml-auto" @click="remove(r)">删除</button>
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
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">房源形态</label>
          <div class="flex items-center gap-1.5">
            <button type="button" class="px-3 py-1.5 rounded-full text-xs font-medium border transition-colors"
              :class="houseStyle === 'estate' ? 'bg-brand-500/10 border-brand-500/40 text-brand-600 dark:text-brand-300' : 'border-border text-muted-foreground hover:text-foreground'"
              @click="setHouseStyle('estate')">小区住宅</button>
            <button type="button" class="px-3 py-1.5 rounded-full text-xs font-medium border transition-colors"
              :class="houseStyle === 'rural' ? 'bg-brand-500/10 border-brand-500/40 text-brand-600 dark:text-brand-300' : 'border-border text-muted-foreground hover:text-foreground'"
              @click="setHouseStyle('rural')">自建房 / 民房</button>
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">{{ houseStyle === 'rural' ? '村组 / 地址' : '小区' }}</label>
          <input v-model="form.community" class="input-field" list="community-options" :placeholder="houseStyle === 'rural' ? '如 双联村，可留空' : '选择或输入，单小区可留空'" />
          <datalist id="community-options">
            <option v-for="c in communityOptions" :key="c" :value="c" />
          </datalist>
        </div>
        <div class="grid gap-3" :class="houseStyle === 'rural' ? 'grid-cols-1' : 'grid-cols-2 sm:grid-cols-4'">
          <template v-if="houseStyle === 'estate'">
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
          </template>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">{{ houseStyle === 'rural' ? '房号 / 称呼 *' : '房号 *' }}</label>
            <input v-model="form.room_no" class="input-field" :placeholder="houseStyle === 'rural' ? '如 主屋 / 东厢房 / 1号院' : '如 101'" required />
            <p class="text-[11px] text-muted-foreground mt-1">{{ houseStyle === 'rural' ? '同一村组/地址内不能重复' : '同一小区内房号不能重复' }}</p>
          </div>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">标签</label>
          <TagInput v-model="formTags" :suggestions="tagSuggestions" placeholder="如 物业代管 / 临街 / 精装，回车添加" />
          <p class="text-[11px] text-muted-foreground mt-1">自由打标，方便按标签分组和筛选房源（如物业按小区分组管理）。</p>
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
              <input v-model.number="form.initial_water" type="number" step="1" min="0" class="input-field !py-2" />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">电表（度）</label>
              <input v-model.number="form.initial_elec" type="number" step="1" min="0" class="input-field !py-2" />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">燃气表（方）</label>
              <input v-model.number="form.initial_gas" type="number" step="1" min="0" class="input-field !py-2" />
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

    <!-- 登记链接弹窗：复制发给租客，租客手机提交入住信息，申请进租户管理审核 -->
    <Modal v-model="showApplyLink" title="租客登记链接">
      <div class="space-y-4" v-if="applyRoom">
        <p class="text-sm text-muted-foreground leading-relaxed">
          把链接发给这间房的租客（微信/短信均可）。租客手机打开后拍照身份证（AI 自动识别）、
          填个手机号即可提交；申请会出现在「租户管理 → 入住申请」，确认后一键转入租户。
        </p>
        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-sm">
          <span class="text-muted-foreground">房源：</span>
          <span class="text-foreground font-medium">{{ applyRoom.community }} · {{ applyRoom.room_no }}</span>
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">登记链接</label>
          <div class="flex gap-2">
            <input :value="applyLink" readonly class="input-field !py-2 text-xs flex-1 min-w-0" @focus="($event.target as HTMLInputElement).select()" />
            <button class="btn-brand !py-2 shrink-0" @click="copyApplyLink">{{ copied ? '已复制 ✓' : '复制' }}</button>
          </div>
        </div>
        <p v-if="applyLinkError" class="text-sm text-destructive">{{ applyLinkError }}</p>
        <div class="flex items-center justify-between gap-3">
          <button class="text-xs text-muted-foreground hover:text-foreground underline" @click="regenerateApplyLink">重新生成（旧链接失效）</button>
          <button class="btn-ghost !py-2 !px-4 text-sm" @click="showApplyLink = false">关闭</button>
        </div>
      </div>
    </Modal>

    <!-- 批量导入弹窗 -->
    <Modal v-model="showImport" title="导入房源（CSV 模板）">
      <div class="space-y-4">
        <p class="text-sm text-muted-foreground leading-relaxed">
          下载模板填写后上传（.csv，≤2MB）。同小区同房号已存在时更新（只覆盖填了值的列，不会清空旧数据），不会重复建档。
        </p>
        <button class="btn-ghost !py-1.5 !px-3 text-xs" @click="downloadRoomTemplate">⬇ 下载导入模板</button>
        <FilePick
          accept=".csv"
          :disabled="importing"
          :label="importFileName || '点击选择 CSV 文件'"
          hint="仅支持 .csv（Excel/WPS 编辑后另存为 CSV），≤2MB"
          @change="onPickImportFile"
        />
        <div v-if="importResult" class="rounded-xl border border-border p-4 text-sm space-y-2">
          <div class="flex gap-4">
            <span class="text-emerald-600 dark:text-emerald-400">新建 {{ importResult.created }}</span>
            <span class="text-brand-600 dark:text-brand-300">更新 {{ importResult.updated }}</span>
            <span class="text-destructive">失败 {{ importResult.failed }}</span>
          </div>
          <ul v-if="importResult.errors.length" class="text-xs text-destructive space-y-1 max-h-32 overflow-auto">
            <li v-for="(e, i) in importResult.errors" :key="i">第 {{ e.line }} 行：{{ e.reason }}</li>
          </ul>
        </div>
        <p v-if="importError" class="text-sm text-destructive">{{ importError }}</p>
        <div class="flex gap-3">
          <button class="btn-ghost flex-1" @click="showImport = false">关闭</button>
          <button class="btn-brand flex-1" :disabled="importing" @click="doImport">{{ importing ? '导入中…' : '开始导入' }}</button>
        </div>
      </div>
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
import FilePick from '../components/FilePick.vue'
import FilterPanel from '../components/FilterPanel.vue'
import TagInput from '../components/TagInput.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { useLedgerStore } from '../stores/ledger'
import { toast } from '../utils/toast'
import {
  defaultBilling, fetchBillingDefaults, globalBillingText, numText, roomBillingSummary,
  type BillingDefaults,
} from '../utils/billing'
import {
  createRoom, deleteRoom, downloadRoomTemplate, exportRooms, fmtMoney, getAllRooms, getRoomApplyLink, getRooms, importRooms, updateRoom,
  type ImportResult, type Room, type RoomView,
} from '../api/rental'

const ledgerStore = useLedgerStore()
const rooms = ref<RoomView[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const keyword = ref('')
const status = ref('')
// 筛选下拉：小区/村组 + 楼栋（楼栋选项随所选小区收窄；与表单里的
// communityOptions 区分开，那一份是新增/编辑弹窗 datalist 用的）
const filterCommunity = ref('')
const filterBuilding = ref('')
const filterTag = ref('')
const communityFilterOptions = computed(() => [...new Set(allRooms.value.map((r) => r.community).filter(Boolean))])
const buildingFilterOptions = computed(() => {
  const pool = filterCommunity.value ? allRooms.value.filter((r) => r.community === filterCommunity.value) : allRooms.value
  return [...new Set(pool.map((r) => r.building).filter(Boolean))]
})
// 标签筛选候选与表单建议（同源：全部房源的已用标签）
const tagFilterOptions = computed(() => [...new Set(allRooms.value.flatMap((r) => r.tags ?? []))].sort())
const tagSuggestions = tagFilterOptions

function onCommunityChange() {
  if (filterBuilding.value && !buildingFilterOptions.value.includes(filterBuilding.value)) filterBuilding.value = ''
  load(1)
}

// 手机端筛选摘要行与非默认条件数（徽标）
const roomsFilterSummary = computed(() =>
  [
    keyword.value ? `“${keyword.value}”` : '未搜索',
    filterCommunity.value || '全部小区',
    filterBuilding.value || '全部楼栋',
    filterTag.value ? `#${filterTag.value}` : '全部标签',
    status.value === 'occupied' ? '在租' : status.value === 'vacant' ? '空闲' : '全部状态',
  ].join(' · '),
)
const roomsFilterCount = computed(() => (keyword.value ? 1 : 0) + (filterCommunity.value ? 1 : 0) + (filterBuilding.value ? 1 : 0) + (filterTag.value ? 1 : 0) + (status.value ? 1 : 0))

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

// 房源形态：小区住宅（楼栋/单元/楼层）/ 自建房·民房（村组地址 + 房屋称呼）。
// 只是录入辅助、不落库——两种形态提交的字段完全一致。编辑时按「楼栋/单元/楼层
// 是否全空」推断形态（数据模型里没存这个标记），因此只填了小区+房号的普通住宅
// 也会以民房形态打开；这只是文案与可见字段的差别，不影响数据。
//
// 切换形态**不再立刻清空**三列（2026-10-11 审计：误点一下「自建房/民房」就会
// 丢掉已输入的楼栋单元楼层）。清空改到提交前按最终形态处理，避免隐藏字段带旧值。
const houseStyle = ref<'estate' | 'rural'>('estate')

function setHouseStyle(s: 'estate' | 'rural') {
  houseStyle.value = s
}

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
  houseStyle.value = 'estate'
  form.value = {
    community: '', room_no: '', building: '', unit: '', floor: '',
    default_rent: 0, default_sanitation_fee: 0, default_management_fee: 0,
    elec_price: 0, gas_price: 0,
    initial_water: 0, initial_elec: 0, initial_gas: 0,
    tags: [],
    notes: '',
  }
  formError.value = ''
  showModal.value = true
}

// 表单标签的 v-model 桥：form.tags 在 Partial<Room> 里可缺省，TagInput 需要确定的数组
const formTags = computed<string[]>({
  get: () => form.value.tags ?? [],
  set: (v) => { form.value.tags = v },
})

// 登记链接：每间房一个 token，链接发给租客自助提交入住信息
const showApplyLink = ref(false)
const applyRoom = ref<RoomView | null>(null)
const applyToken = ref('')
const applyLinkError = ref('')
const copied = ref(false)
// 登记链接必须带应用的基址：飞牛包的前端基址是 /app/techfunway-rental/，
// 写死 origin + '/apply/...' 会落到网关根路径直接 404，房东复制发出的链接
// 租客根本打不开（2026-10-11 审计发现，主分发形态上功能不可用）。
const applyLink = computed(() =>
  new URL(`${import.meta.env.BASE_URL}apply/${applyToken.value}`, location.origin).toString(),
)

async function openApplyLink(r: RoomView) {
  applyRoom.value = r
  applyLinkError.value = ''
  copied.value = false
  showApplyLink.value = true
  try {
    const res = await getRoomApplyLink(r.id)
    if (res.data?.code === 0) applyToken.value = res.data.data.token
    else applyLinkError.value = res.data?.message || '生成登记链接失败'
  } catch {
    applyLinkError.value = '生成登记链接失败，请重试'
  }
}

async function regenerateApplyLink() {
  if (!applyRoom.value) return
  applyLinkError.value = ''
  copied.value = false
  try {
    const res = await getRoomApplyLink(applyRoom.value.id, true)
    if (res.data?.code === 0) {
      applyToken.value = res.data.data.token
      toast('已重新生成，旧链接已失效')
    } else {
      applyLinkError.value = res.data?.message || '重新生成失败'
    }
  } catch {
    applyLinkError.value = '重新生成失败，请重试'
  }
}

async function copyApplyLink() {
  try {
    await navigator.clipboard.writeText(applyLink.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // 剪贴板权限被拒（HTTP 部署下常见）：退化为选中文本让用户手动复制
    applyLinkError.value = '复制失败，请长按/选中文本手动复制'
  }
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
  // 跨页拉全：服务端 pageSize 硬上限 100，写死 100 会让第 101 间房以后的小区
  // 从筛选下拉里消失（2026-10-11 审计）。
  allRooms.value = await getAllRooms()
}

function openEdit(r: RoomView) {
  editing.value = r
  form.value = { ...r }
  // 楼栋/单元/楼层全空按民房形态打开（自建房录入后再编辑时口径不变）
  houseStyle.value = !r.building && !r.unit && !r.floor ? 'rural' : 'estate'
  formError.value = ''
  showModal.value = true
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getRooms({
      page: p, pageSize, keyword: keyword.value, status: status.value,
      community: filterCommunity.value || undefined, building: filterBuilding.value || undefined,
      tag: filterTag.value || undefined,
    })
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
    // 民房形态下这三列在界面上是隐藏的：提交前清空，避免带着用户早先输入、
    // 现在看不见的旧值入库（清空动作从「切换形态时」挪到「提交前」，误点不再丢数据）。
    if (houseStyle.value === 'rural') {
      form.value.building = ''
      form.value.unit = ''
      form.value.floor = ''
    }
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
      tags: form.value.tags ?? [],
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

// 批量导入：选文件 → 上传 → 展示新建/更新/失败与错误行明细
const showImport = ref(false)
const importing = ref(false)
const importError = ref('')
const importResult = ref<ImportResult | null>(null)
const importFile = ref<File | null>(null)
const importFileName = computed(() => importFile.value?.name || '')

function onPickImportFile(files: File[]) {
  importFile.value = files[0] ?? null
  importResult.value = null
  importError.value = ''
}

async function doImport() {
  const file = importFile.value
  if (!file) {
    importError.value = '请先选择 CSV 文件'
    return
  }
  importing.value = true
  importError.value = ''
  importResult.value = null
  try {
    const res = await importRooms(file)
    const env = res.data as unknown as { code: number; message: string; data?: ImportResult }
    if (env?.code === 0) {
      importResult.value = env.data ?? { created: 0, updated: 0, failed: 0, errors: [] }
      await loadAllRooms()
      await load(1)
    } else {
      importError.value = env?.message || '导入失败'
    }
  } catch {
    importError.value = '导入失败，请检查文件格式'
  } finally {
    importing.value = false
  }
}

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
