<template>
  <div class="page-container animate-fade-in">
    <PageHeader title="租户管理" description="入住登记与退租管理，宿舍场景支持一房多名租户">
      <template #actions>
        <button class="btn-brand" @click="openCreate">＋ 登记租户</button>
      </template>
    </PageHeader>

    <!-- 筛选：桌面平铺，手机收成一行摘要 -->
    <FilterPanel ref="filterRef" :summary="tenantsFilterSummary" :active-count="tenantsFilterCount">
      <div class="flex flex-wrap items-center gap-3">
        <input v-model="keyword" class="input-field flex-1 min-w-[200px] !py-2" placeholder="搜索姓名或电话…" @keyup.enter="applyFilters" />
        <select v-model="active" class="input-field !py-2 !w-auto" @change="load(1)">
          <option value="">全部</option>
          <option value="1">在租</option>
          <option value="0">已退租</option>
        </select>
        <select v-model="roomId" class="input-field !py-2 !w-auto" @change="load(1)">
          <option :value="0">全部房源</option>
          <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ r.room_no }}</option>
        </select>
        <button class="btn-brand !py-2" @click="applyFilters">搜索</button>
      </div>
    </FilterPanel>

    <div class="surface rounded-2xl overflow-hidden">
      <div v-if="loading" class="text-sm text-muted-foreground py-12 text-center">加载中…</div>
      <div v-else-if="tenants.length === 0" class="text-sm text-muted-foreground py-12 text-center">
        暂无租户，点击右上角「登记租户」开始。
      </div>
      <template v-else>
      <!-- 桌面端：表格（列多，最小宽度防挤压竖排，窄桌面走容器横向滚动） -->
      <div class="hidden md:block overflow-x-auto">
        <table class="w-full text-sm min-w-[1080px]">
          <thead>
            <tr class="text-left text-muted-foreground border-b border-border bg-muted/40">
              <th class="py-3 px-4 font-medium whitespace-nowrap">姓名</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">电话</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">房源</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">计费与缴费</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">入住日期</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">到期时间</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">退租日期</th>
              <th class="py-3 px-4 font-medium text-right whitespace-nowrap">押金（元）</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">状态</th>
              <th class="py-3 px-4 font-medium whitespace-nowrap">备注</th>
              <th class="py-3 px-4 font-medium text-right whitespace-nowrap">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr v-for="t in tenants" :key="t.id" class="hover:bg-muted/30 transition-colors">
              <td class="py-3 px-4 font-semibold text-foreground">{{ t.name }}</td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.phone || '—' }}</td>
              <td class="py-3 px-4 text-foreground" :title="t.room_label">{{ t.room_label || t.room_no || '—' }}</td>
              <td class="py-3 px-4 text-muted-foreground text-xs">{{ tenantBillingSummary(t, billingDefaults) }}<span v-if="tenantExcludedText(t)">{{ tenantExcludedText(t) }}</span></td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_in_date || '—' }}</td>
              <td class="py-3 px-4 tabular-nums">
                <template v-if="t.lease_end_date">
                  <div class="text-muted-foreground">{{ t.lease_end_date }}</div>
                  <div v-if="leaseStatus(t)" class="text-xs font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</div>
                </template>
                <span v-else class="text-muted-foreground">—</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground tabular-nums">{{ t.move_out_date || '—' }}</td>
              <td class="py-3 px-4 text-right tabular-nums" :class="t.deposit ? 'text-foreground' : 'text-muted-foreground'">{{ t.deposit ? fmtDeposit(t.deposit) : '—' }}</td>
              <td class="py-3 px-4">
                <span v-if="t.active" class="badge bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
                <span v-else class="badge bg-muted text-muted-foreground">已退租</span>
              </td>
              <td class="py-3 px-4 text-muted-foreground max-w-[180px] truncate" :title="t.notes">{{ t.notes || '—' }}</td>
              <td class="py-3 px-4 text-right whitespace-nowrap">
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openContracts(t)">
                  合同<span v-if="t.contracts_count" class="ml-0.5 tabular-nums">{{ t.contracts_count }}</span>
                </button>
                <button v-if="t.active" class="text-amber-600 dark:text-amber-300 hover:underline mr-3" @click="checkout(t)">退租</button>
                <button class="text-brand-600 dark:text-brand-300 hover:underline mr-3" @click="openEdit(t)">编辑</button>
                <button class="text-destructive hover:underline" @click="remove(t)">删除</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 手机端：卡片列表（紧凑版） -->
      <div class="md:hidden p-2 space-y-1.5">
        <div v-for="t in tenants" :key="t.id" class="rounded-xl border border-border bg-surface/80 px-3 py-2.5 space-y-1.5">
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <span class="text-sm font-semibold text-foreground">{{ t.name }}</span>
              <span class="text-[11px] text-muted-foreground ml-1.5 tabular-nums">{{ t.phone || '' }}</span>
            </div>
            <span v-if="t.active" class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-emerald-500/10 text-emerald-600 dark:text-emerald-300">在租</span>
            <span v-else class="badge shrink-0 !px-1.5 !py-0.5 text-[11px] bg-muted text-muted-foreground">已退租</span>
          </div>

          <div class="text-[11px] leading-5 text-muted-foreground">
            <div>房源 <span class="text-foreground">{{ t.room_label || t.room_no || '—' }}</span><template v-if="t.deposit"> · 押金 <span class="text-foreground tabular-nums">{{ fmtDeposit(t.deposit) }} 元</span></template></div>
            <div>计费 {{ tenantBillingSummary(t, billingDefaults) }}<span v-if="tenantExcludedText(t)">{{ tenantExcludedText(t) }}</span></div>
            <div class="tabular-nums">入住 {{ t.move_in_date || '—' }} · 到期 {{ t.lease_end_date || '—' }}<template v-if="t.move_out_date"> · 退租 {{ t.move_out_date }}</template>
              <span v-if="leaseStatus(t)" class="ml-1 font-medium" :class="leaseStatus(t)!.cls">{{ leaseStatus(t)!.text }}</span>
            </div>
            <div v-if="t.notes" class="truncate" :title="t.notes">{{ t.notes }}</div>
          </div>

          <div class="flex items-center gap-3 pt-0.5 border-t border-border/60 text-xs">
            <button class="text-brand-600 dark:text-brand-300" @click="openContracts(t)">
              合同<span v-if="t.contracts_count" class="ml-0.5 tabular-nums">{{ t.contracts_count }}</span>
            </button>
            <button v-if="t.active" class="text-amber-600 dark:text-amber-300" @click="checkout(t)">退租</button>
            <button class="text-brand-600 dark:text-brand-300" @click="openEdit(t)">编辑</button>
            <button class="text-destructive ml-auto" @click="remove(t)">删除</button>
          </div>
        </div>
      </div>
      </template>
      <Pagination v-if="total > pageSize" :total="total" :page="page" :page-size="pageSize" @change="load" />
    </div>

    <!-- 新增/编辑弹窗 -->
    <Modal v-model="showModal" :title="editing ? '编辑租户' : '登记租户'">
      <form class="space-y-4" @submit.prevent="save">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">姓名 *</label>
            <input v-model="form.name" class="input-field" placeholder="租户姓名" required />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">电话</label>
            <input v-model="form.phone" class="input-field" placeholder="选填" />
          </div>
        </div>
        <!-- 身份证号独占一行，选填 -->
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">身份证号</label>
          <input v-model="form.id_card" class="input-field" placeholder="选填" />
        </div>
        <div>
          <label class="text-xs font-semibold text-muted-foreground block mb-1.5">所属房源 *</label>
          <select v-model.number="form.room_id" class="input-field" required>
            <option v-for="r in allRooms" :key="r.id" :value="r.id">{{ roomLabelOf(r) }}</option>
          </select>
          <p v-if="allRooms.length === 0" class="text-xs text-destructive mt-1">请先在「房源管理」中添加房源</p>
        </div>
        <!-- 计费与缴费：每个租户可以不一样，未设置时跟随偏好设置里的全局默认 -->
        <div class="rounded-xl border border-border p-3.5 space-y-3">
          <div>
            <p class="text-xs font-semibold text-muted-foreground">计费与缴费（按租户设置，留空跟随全局默认）</p>
            <p class="text-[11px] text-muted-foreground mt-1">当前全局默认：{{ globalBillingText(billingDefaults) }}</p>
          </div>
          <!-- 收费项目勾选：勾选 = 参与该租户的账单结算，未勾选项目出账费用为 0 -->
          <div>
            <p class="text-xs text-muted-foreground block mb-1.5">收费项目（勾选 = 参与本租户的账单结算）</p>
            <div class="flex flex-wrap gap-1.5">
              <label
                v-for="fi in billableFeeItems" :key="fi.key"
                class="flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs cursor-pointer select-none transition-colors"
                :class="feeChecked(fi.key) ? 'border-brand-500/60 bg-brand-500/10 text-foreground' : 'border-border text-muted-foreground'"
              >
                <input type="checkbox" class="w-3.5 h-3.5 accent-indigo-500 shrink-0" :checked="feeChecked(fi.key)" @change="toggleFee(fi.key)" />
                {{ fi.name }}
              </label>
            </div>
            <p v-if="excludedFeeNames" class="text-[11px] text-muted-foreground mt-1.5">
              不参与：{{ excludedFeeNames }}，出账时这些项目费用为 0；之后新增的收费项目默认参与，需排除再回来取消勾选。
            </p>
          </div>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">水费计费方式</label>
              <select v-model="form.water_mode" class="input-field !py-2" @change="onWaterModeChange">
                <option value="">跟随全局</option>
                <option value="meter">按吨计价</option>
                <option value="monthly">包月</option>
              </select>
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">
                水费{{ formWaterMode === 'monthly' ? '包月金额（元/月）' : '单价（元/吨）' }}
              </label>
              <!-- 跟随全局时只展示全局默认值（灰色只读）；显式选择后可改，清空或 0 = 跟随全局 -->
              <input
                v-if="formWaterFollows"
                :value="formWaterGlobalAmount.toFixed(2)"
                type="number" class="input-field !py-2 bg-muted/60 text-muted-foreground" disabled
              />
              <input
                v-else-if="formWaterMode === 'monthly'"
                v-model.number="form.water_monthly_fee" type="number" step="0.01" min="0"
                class="input-field !py-2" placeholder="如 40，0 = 跟随全局"
              />
              <input
                v-else
                v-model.number="form.water_price" type="number" step="0.01" min="0"
                class="input-field !py-2" placeholder="如 5，0 = 跟随全局"
              />
            </div>
          </div>
          <!-- 手机端两列换行排布（第三项落到下一行），桌面端保持一行三列 -->
          <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">缴费周期</label>
              <select v-model="form.pay_cycle" class="input-field !py-2">
                <option value="">跟随全局</option>
                <option value="monthly">月付</option>
                <option value="quarterly">季付</option>
              </select>
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">缴费日（几号）</label>
              <input
                v-model="payDayInput" type="number" min="0" max="28" class="input-field !py-2"
                placeholder="跟随全局"
              />
            </div>
            <div>
              <label class="text-xs text-muted-foreground block mb-1.5">提前提醒天数</label>
              <input
                v-model="remindDaysInput" type="number" min="0" max="30" class="input-field !py-2"
                placeholder="跟随全局"
              />
            </div>
          </div>
          <p class="text-[11px] text-muted-foreground">
            当前全局：{{ waterModeLabel(billingDefaults.waterMode) }} {{ formWaterGlobalAmount.toFixed(2) }} 元 · {{ cycleLabel(billingDefaults.payCycle) }} · {{ payDayText(billingDefaults.payDay) }} · 提前 {{ billingDefaults.remindDays }} 天提醒。
            {{ formWaterFollows
              ? ''
              : (formWaterMode === 'monthly'
                ? '水费按每月固定金额计，不抄表；金额留空或 0 = 跟随全局默认'
                : '水费按用量 × 单价计；单价留空或 0 = 跟随全局默认') }}
            缴费周期决定多久出一张账单（季付租金等费用按 3 个月计）；缴费日留空或 0 表示不提醒，1-28 号按日提醒。
          </p>
        </div>
        <!-- 手机端两列换行排布，桌面端一行三列 -->
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-3">
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">入住日期</label>
            <DateField v-model="form.move_in_date" type="date" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">到期时间</label>
            <DateField v-model="form.lease_end_date" type="date" />
          </div>
          <div>
            <label class="text-xs font-semibold text-muted-foreground block mb-1.5">押金（元）</label>
            <input v-model.number="form.deposit" type="number" step="0.01" min="0" class="input-field" placeholder="0" />
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

    <!-- 合同存档弹窗 -->
    <Modal v-model="showContracts" title="租赁合同">
      <div class="space-y-4">
        <div class="rounded-xl bg-muted/60 px-3 py-2.5 text-xs text-muted-foreground leading-relaxed">
          <span class="font-semibold text-foreground">{{ contractTenantLabel }}</span> ·
          上传合同拍照、扫描件或 PDF（单个 ≤20MB，每租户最多 20 份），退租后记录保留。
        </div>
        <div>
          <span class="text-xs font-semibold text-muted-foreground block mb-1.5">上传合同文件</span>
          <FilePick
            accept="image/*,.pdf"
            multiple
            :disabled="contractUploading"
            :label="contractUploading ? '上传中…' : '点击选择合同文件'"
            hint="支持拍照、图片或 PDF，可多选；单个 ≤20MB，每租户最多 20 份"
            @change="uploadContractFiles"
          />
        </div>

        <div v-if="contractLoading" class="py-6 text-center text-sm text-muted-foreground">加载中…</div>
        <div v-else-if="contracts.length === 0" class="py-6 text-center text-sm text-muted-foreground">
          还没有上传合同。用手机拍下签好的合同页，或上传扫描件 / PDF。
        </div>
        <ul v-else class="space-y-1.5">
          <li v-for="ct in contracts" :key="ct.id" class="flex items-center gap-2 rounded-lg bg-muted/60 px-2.5 py-2">
            <span
              class="badge shrink-0 !px-1.5 !py-0.5 text-[11px]"
              :class="ct.mime_type === 'application/pdf' ? 'bg-rose-500/10 text-rose-600 dark:text-rose-300' : 'bg-sky-500/10 text-sky-600 dark:text-sky-300'"
            >{{ ct.mime_type === 'application/pdf' ? 'PDF' : '图片' }}</span>
            <div class="min-w-0 flex-1">
              <div class="text-xs font-medium text-foreground truncate" :title="ct.file_name">{{ ct.file_name }}</div>
              <div class="text-[11px] text-muted-foreground tabular-nums">{{ fmtSize(ct.file_size) }} · {{ fmtDate(ct.created_at) }}</div>
            </div>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="previewContract(ct)">预览</button>
            <button class="text-brand-600 dark:text-brand-300 text-xs shrink-0" @click="download(ct)">下载</button>
            <button class="text-destructive text-xs shrink-0" @click="removeContract(ct)">删除</button>
          </li>
        </ul>

        <p v-if="contractError" class="text-sm text-destructive">{{ contractError }}</p>

        <!-- 应用内预览：图片直接渲染，PDF 内嵌 -->
        <div v-if="previewing && previewUrl" class="space-y-2">
          <div class="flex items-center justify-between">
            <span class="text-xs font-semibold text-foreground truncate">预览：{{ previewing.file_name }}</span>
            <button class="text-muted-foreground hover:text-foreground text-xs" @click="closePreview">收起预览</button>
          </div>
          <iframe
            v-if="previewing.mime_type === 'application/pdf'"
            :src="previewUrl"
            class="w-full h-[420px] rounded-xl border border-border bg-white"
            title="合同预览"
          ></iframe>
          <img v-else :src="previewUrl" class="w-full max-h-[420px] object-contain rounded-xl border border-border bg-muted/40" alt="合同预览" />
        </div>

        <div class="flex gap-3 pt-1">
          <button class="btn-ghost flex-1" @click="closeContracts">关闭</button>
        </div>
      </div>
    </Modal>

    <ConfirmDialog
      v-model="confirmState.show"
      :title="confirmState.title"
      :message="confirmState.message"
      confirm-type="danger"
      @confirm="onConfirm"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DateField from '../components/DateField.vue'
import FilePick from '../components/FilePick.vue'
import FilterPanel from '../components/FilterPanel.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import Pagination from '../components/Pagination.vue'
import { toast } from '../utils/toast'
import {
  checkoutTenant, createTenant, deleteContract, deleteTenant, downloadContract,
  fetchContractBlob, getContracts, getFeeItems, getRooms, getTenants, updateTenant, uploadContracts,
  type Contract, type FeeItem, type RoomView, type Tenant,
} from '../api/rental'
import {
  defaultBilling, fetchBillingDefaults, globalBillingText, payDayText,
  tenantBillingSummary, waterModeLabel, type BillingDefaults, type WaterMode,
} from '../utils/billing'
import { cycleLabel } from '../utils/cycle'

const tenants = ref<Tenant[]>([])
const allRooms = ref<RoomView[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const loading = ref(false)
const keyword = ref('')
const active = ref('')
const roomId = ref(0)

// 手机端筛选摘要行与非默认条件数（徽标）
const tenantsFilterSummary = computed(() =>
  [
    keyword.value ? `“${keyword.value}”` : '未搜索',
    active.value === '1' ? '在租' : active.value === '0' ? '已退租' : '全部状态',
    roomId.value ? (allRooms.value.find((r) => r.id === roomId.value)?.room_no || '指定房源') : '全部房源',
  ].join(' · '),
)
const tenantsFilterCount = computed(() => (keyword.value ? 1 : 0) + (active.value ? 1 : 0) + (roomId.value ? 1 : 0))

const filterRef = ref<InstanceType<typeof FilterPanel> | null>(null)

function applyFilters() {
  load(1)
  filterRef.value?.collapse()
}

// 全局计费与缴费默认（偏好设置 → 租房设置）：租户表单的预填值与"跟随全局"提示
const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })

const showModal = ref(false)
const editing = ref<Tenant | null>(null)
const saving = ref(false)
const formError = ref('')
const form = ref<Partial<Tenant>>({})

// ---------- 收费项目勾选 ----------

// 收费项目定义（内置六项 + 用户自定义），表单勾选与"不含 X"摘要共用
const feeItems = ref<FeeItem[]>([])
// 表单里未勾选（不参与计费）的项目 key，与接口的 excluded_fees 同口径
const excludedInput = ref<string[]>([])

// 参与勾选的项目：启用的内置 + 自定义（停用项目本就不出账，不展示）
const billableFeeItems = computed(() => feeItems.value.filter((f) => f.enabled))

function feeChecked(key: string): boolean {
  return !excludedInput.value.includes(key)
}

function toggleFee(key: string) {
  const set = new Set(excludedInput.value)
  if (set.has(key)) set.delete(key)
  else set.add(key)
  excludedInput.value = [...set]
}

function feeName(key: string): string {
  return feeItems.value.find((f) => f.key === key)?.name ?? key
}

// "不参与：X、Y"摘要文案（只列当前可勾选的项目，停用/已删项目不出账无需提示）
const excludedFeeNames = computed(() =>
  excludedInput.value.filter((k) => billableFeeItems.value.some((f) => f.key === k)).map(feeName).join('、'),
)

// 租户列表摘要后缀：不参与计费的项目名（空=全项参与不打标）
function tenantExcludedText(t: Tenant): string {
  const names = (t.excluded_fees || [])
    .filter((k) => billableFeeItems.value.some((f) => f.key === k))
    .map(feeName)
    .join('、')
  return names ? `（不含${names}）` : ''
}

// 表单当前生效的水费计费方式：租户显式选择则用它，'' 表示跟随全局默认
const formWaterMode = computed<WaterMode>(() => {
  const m = form.value.water_mode
  if (m === 'monthly' || m === 'meter') return m
  return billingDefaults.value.waterMode
})

// 是否跟随全局默认：未显式选择计费方式（金额输入框只读展示全局值）
const formWaterFollows = computed(() => {
  const m = form.value.water_mode
  return m !== 'monthly' && m !== 'meter'
})

// 跟随全局时展示的全局金额（按当前生效计费方式）
const formWaterGlobalAmount = computed(() => formWaterMode.value === 'monthly'
  ? billingDefaults.value.waterMonthlyFee
  : billingDefaults.value.waterMeterPrice)

// 缴费日 / 提前提醒天数：-1 表示跟随全局默认，输入框留空展示
const payDayInput = computed<string | number>({
  get: () => {
    const v = Number(form.value.pay_day)
    return Number.isFinite(v) && v >= 0 ? v : ''
  },
  set: (v) => { form.value.pay_day = v === '' || v === null || v === undefined ? -1 : Number(v) },
})

const remindDaysInput = computed<string | number>({
  get: () => {
    const v = Number(form.value.remind_days)
    return Number.isFinite(v) && v >= 0 ? v : ''
  },
  set: (v) => { form.value.remind_days = v === '' || v === null || v === undefined ? -1 : Number(v) },
})

// 切到显式计费方式时，金额留空则按全局默认预填（用户可改，改回 0 仍跟随全局）
function onWaterModeChange() {
  const m = form.value.water_mode
  if (m === 'monthly' && !(Number(form.value.water_monthly_fee) > 0)) {
    form.value.water_monthly_fee = billingDefaults.value.waterMonthlyFee
  }
  if (m === 'meter' && !(Number(form.value.water_price) > 0)) {
    form.value.water_price = billingDefaults.value.waterMeterPrice
  }
}

function openCreate() {
  editing.value = null
  form.value = {
    name: '', phone: '', id_card: '', room_id: allRooms.value[0]?.id,
    move_in_date: '', lease_end_date: '', deposit: 0,
    // 空串/-1 = 跟随全局默认；金额按全局默认预填，切到显式方式时直接可用
    water_mode: '', water_price: billingDefaults.value.waterMeterPrice,
    water_monthly_fee: billingDefaults.value.waterMonthlyFee,
    pay_cycle: '', pay_day: -1, remind_days: -1,
    notes: '',
  }
  // 新租户默认全部收费项目参与
  excludedInput.value = []
  formError.value = ''
  showModal.value = true
}

// 房源完整位置：小区 楼栋 单元 N层 房号
function roomLabelOf(r: RoomView): string {
  const parts = [r.community, r.building, r.unit, r.floor ? r.floor + '层' : '', r.room_no]
  return parts.filter(Boolean).join(' ')
}

// 押金显示：带千分位，0 不显示（在列表里以 — 呈现）
function fmtDeposit(v: number): string {
  return Number(v || 0).toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

// 租约到期状态：已过期红 / 30 天内到期橙；未约定或非在租不提示
function leaseStatus(t: Tenant): { text: string; cls: string } | null {
  if (!t.active || !t.lease_end_date) return null
  const days = Math.ceil((new Date(t.lease_end_date + 'T00:00:00').getTime() - Date.now()) / 86400000)
  if (days < 0) return { text: `已过期 ${-days} 天`, cls: 'text-rose-500' }
  if (days <= 30) return { text: `${days} 天后到期`, cls: 'text-amber-500' }
  return null
}

function openEdit(t: Tenant) {
  editing.value = t
  form.value = { ...t }
  excludedInput.value = [...(t.excluded_fees || [])]
  formError.value = ''
  showModal.value = true
}

async function loadRooms() {
  const res = await getRooms({ page: 1, pageSize: 100 })
  if (res.data?.code === 0) allRooms.value = res.data.data.items || []
}

async function load(p = page.value) {
  loading.value = true
  try {
    const res = await getTenants({
      page: p, pageSize, keyword: keyword.value,
      active: active.value, room_id: roomId.value || undefined,
    })
    if (res.data?.code === 0) {
      tenants.value = res.data.data.items || []
      total.value = res.data.data.total || 0
      page.value = p
    }
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!form.value.name?.trim() || !form.value.room_id) {
    formError.value = '请填写姓名并选择房源'
    return
  }
  saving.value = true
  formError.value = ''
  try {
    // 只有与所选计费方式匹配的金额才落库；跟随全局默认（方式为空）时两个金额都存 0，
    // 这样以后改偏好设置里的全局默认，租户会跟着变。
    const mode = form.value.water_mode
    // 未勾选的收费项目（排除项）落库；存量排除里已停用/已删除的项目一并清掉。
    const excluded = billableFeeItems.value.filter((f) => !feeChecked(f.key)).map((f) => f.key)
    const payload: Partial<Tenant> = {
      ...form.value,
      water_price: mode === 'meter' ? Number(form.value.water_price) || 0 : 0,
      water_monthly_fee: mode === 'monthly' ? Number(form.value.water_monthly_fee) || 0 : 0,
      pay_day: Number(form.value.pay_day ?? -1),
      remind_days: Number(form.value.remind_days ?? -1),
      excluded_fees: excluded,
    }
    const res = editing.value
      ? await updateTenant(editing.value.id, payload)
      : await createTenant(payload)
    if (res.data?.code === 0) {
      showModal.value = false
      await load(1)
    } else {
      formError.value = res.data?.message || '保存失败'
    }
  } catch {
    formError.value = '保存失败，请重试'
  } finally {
    saving.value = false
  }
}

const confirmState = ref({ show: false, title: '', message: '', action: null as null | (() => Promise<void>) })

function askConfirm(title: string, message: string, action: () => Promise<void>) {
  confirmState.value = { show: true, title, message, action }
}

async function onConfirm() {
  await confirmState.value.action?.()
}

function checkout(t: Tenant) {
  askConfirm('办理退租', `确认为租户「${t.name}」办理退租？历史账单不受影响。`, async () => {
    const res = await checkoutTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '退租失败', 'error')
      return
    }
    toast('已办理退租')
    await load(page.value)
  })
}

function remove(t: Tenant) {
  askConfirm('删除租户', `确定删除租户「${t.name}」的登记信息？`, async () => {
    const res = await deleteTenant(t.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    await load(page.value)
  })
}

// ---------- 合同存档 ----------

const showContracts = ref(false)
const contractTenant = ref<Tenant | null>(null)
const contracts = ref<Contract[]>([])
const contractLoading = ref(false)
const contractUploading = ref(false)
const contractError = ref('')
const previewing = ref<Contract | null>(null)
const previewUrl = ref('')

const contractTenantLabel = computed(() => {
  const t = contractTenant.value
  if (!t) return ''
  return `${t.name} · ${t.room_label || t.room_no || ''}`
})

function openContracts(t: Tenant) {
  contractTenant.value = t
  contracts.value = []
  contractError.value = ''
  showContracts.value = true
  loadContracts(t.id)
}

async function loadContracts(tenantId: number) {
  contractLoading.value = true
  try {
    const res = await getContracts(tenantId)
    if (res.data?.code === 0) contracts.value = res.data.data ?? []
  } catch {
    contractError.value = '加载合同失败'
  } finally {
    contractLoading.value = false
  }
}

async function uploadContractFiles(files: File[]) {
  const t = contractTenant.value
  if (!t || files.length === 0) return
  contractUploading.value = true
  contractError.value = ''
  try {
    const res = await uploadContracts(t.id, files)
    if (res.data?.code === 0) {
      toast('合同已上传')
      await loadContracts(t.id)
      await load(page.value)
    } else {
      contractError.value = res.data?.message || '上传失败'
    }
  } catch (err: any) {
    contractError.value = err.response?.data?.message || '上传失败，请重试'
  } finally {
    contractUploading.value = false
  }
}

function previewContract(ct: Contract) {
  contractError.value = ''
  fetchContractBlob(ct.id)
    .then((blob) => {
      releasePreviewUrl()
      previewing.value = ct
      previewUrl.value = URL.createObjectURL(blob)
    })
    .catch(() => toast('预览失败，请重试', 'error'))
}

function closePreview() {
  releasePreviewUrl()
  previewing.value = null
}

function releasePreviewUrl() {
  if (previewUrl.value) {
    URL.revokeObjectURL(previewUrl.value)
    previewUrl.value = ''
  }
}

function closeContracts() {
  closePreview()
  showContracts.value = false
}

async function download(ct: Contract) {
  try {
    await downloadContract(ct.id, ct.file_name)
  } catch {
    toast('下载失败，请重试', 'error')
  }
}

function removeContract(ct: Contract) {
  askConfirm('删除合同', `确定删除「${ct.file_name}」？删除后不可恢复。`, async () => {
    const res = await deleteContract(ct.id)
    if (res.data?.code !== 0) {
      toast(res.data?.message || '删除失败', 'error')
      return
    }
    toast('已删除')
    if (contractTenant.value) await loadContracts(contractTenant.value.id)
    await load(page.value)
  })
}

function fmtSize(v: number): string {
  if (v >= 1 << 20) return `${(v / (1 << 20)).toFixed(1)} MB`
  if (v >= 1 << 10) return `${(v / (1 << 10)).toFixed(0)} KB`
  return `${v} B`
}

function fmtDate(v: string): string {
  return (v || '').slice(0, 10)
}

async function loadFeeItems() {
  try {
    const res = await getFeeItems()
    if (res.data?.code === 0) feeItems.value = res.data.data ?? []
  } catch {
    feeItems.value = [] // 加载失败时表单按"全项参与"处理
  }
}

onMounted(async () => {
  billingDefaults.value = await fetchBillingDefaults()
  await Promise.all([loadRooms(), loadFeeItems()])
  await load(1)
})
</script>
