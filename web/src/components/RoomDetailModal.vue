<template>
  <Modal
    :model-value="modelValue"
    :title="`房源详情 · ${room?.room_no ?? ''}`"
    wide
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div v-if="room" class="space-y-3">
      <!-- 概况：位置、状态、标签 -->
      <div class="rounded-xl border border-border p-3 space-y-2">
        <div class="flex items-start justify-between gap-2 flex-wrap">
          <div class="min-w-0">
            <div class="text-base font-semibold text-foreground flex items-center gap-2 flex-wrap">
              {{ room.room_no }}
              <span v-if="room.tags?.length" class="flex flex-wrap gap-1">
                <span v-for="t in room.tags" :key="t" class="badge !px-1.5 !py-0.5 text-[11px] bg-brand-500/10 text-brand-600 dark:text-brand-300">{{ t }}</span>
              </span>
            </div>
            <div class="text-xs text-muted-foreground mt-0.5">{{ locationText }}</div>
          </div>
          <span
            class="badge shrink-0"
            :class="room.current_tenants?.length ? 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300' : 'bg-muted text-muted-foreground'"
          >{{ room.current_tenants?.length ? '在租' : '空闲' }}</span>
        </div>
        <div class="grid grid-cols-2 sm:grid-cols-3 gap-x-3 gap-y-1.5 text-xs">
          <div v-for="f in roomFacts" :key="f.label" class="min-w-0">
            <div class="text-[11px] text-muted-foreground">{{ f.label }}</div>
            <div class="text-foreground tabular-nums break-words">{{ f.value }}</div>
          </div>
        </div>
        <p v-if="room.notes" class="text-[11px] text-muted-foreground">备注：{{ room.notes }}</p>
      </div>

      <!-- 房间照片：跟着房子走，换租客也在 -->
      <div class="rounded-xl border border-border p-3 space-y-1.5">
        <RoomPhotoSection :room-id="room.id" :can-edit="canEditRooms" @changed="emit('photos-changed')" />
        <p class="text-[11px] text-muted-foreground">
          拍下室内、门牌、水表电表位置等留档，单张 ≤20MB、最多 {{ ROOM_PHOTO_MAX }} 张；点缩略图看大图。
        </p>
      </div>

      <!-- 房间物品台账（issue #5 需求 5）：跟着房子走、不绑租客。
           房东据此分清哪些是自己买的、哪些是租客自带的，退租时照清单核对缺失与损坏。 -->
      <div class="rounded-xl border border-border p-3">
        <RoomItemsCard
          :room-id="room.id"
          :can-edit="canEditRooms"
          :room-label="printRoomLabel"
          :landlord-name="room.landlord_name || ''"
          :tenant-name="tenants.map((t) => t.name).join('、')"
        />
      </div>

      <!-- 在租租户：姓名/手机号/身份证等资料，一次看全 -->
      <div class="rounded-xl border border-border p-3 space-y-2">
        <div class="flex items-center justify-between gap-2">
          <span class="text-sm font-semibold text-foreground">在租租户<span v-if="canViewTenants"> · {{ tenants.length }} 人</span></span>
          <RouterLink
            v-if="canViewTenants"
            to="/admin/tenants"
            class="text-[11px] text-brand-600 dark:text-brand-300 hover:underline shrink-0"
          >到租户管理看合同与附件</RouterLink>
        </div>
        <p v-if="!canViewTenants" class="text-xs text-muted-foreground">
          当前账号没有「租户与合同」模块的查看权限，看不到入住人资料；需要时请台账管理者勾选。
        </p>
        <div v-else-if="tenantsLoading" class="text-xs text-muted-foreground py-2">加载中…</div>
        <div v-else-if="tenantsError" class="text-xs text-destructive">{{ tenantsError }}</div>
        <div v-else-if="tenants.length === 0" class="text-xs text-muted-foreground py-1">这间房当前没有在租租户。</div>
        <div v-else class="space-y-2">
          <div v-for="t in tenants" :key="t.id" class="rounded-lg bg-muted/60 px-3 py-2.5 space-y-1.5">
            <div class="flex items-center justify-between gap-2 flex-wrap">
              <div class="text-sm font-semibold text-foreground">
                {{ t.name }}
                <span v-if="t.phone" class="text-[11px] font-normal text-muted-foreground ml-1 tabular-nums">{{ t.phone }}</span>
              </div>
              <button
                v-if="t.id_card"
                type="button"
                class="text-[11px] text-brand-600 dark:text-brand-300 hover:underline shrink-0"
                @click="copyText(t.id_card, '身份证号')"
              >复制身份证号</button>
            </div>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-4 gap-y-1 text-[11px]">
              <div><span class="text-muted-foreground">身份证号</span> <span class="text-foreground tabular-nums break-all">{{ t.id_card || '—' }}</span></div>
              <div><span class="text-muted-foreground">身份证住址</span> <span class="text-foreground">{{ t.address || '—' }}</span></div>
              <div><span class="text-muted-foreground">紧急联系人</span> <span class="text-foreground">{{ t.emergency_contact || '—' }}<template v-if="t.emergency_phone"> · {{ t.emergency_phone }}</template></span></div>
              <div><span class="text-muted-foreground">入住日期</span> <span class="text-foreground tabular-nums">{{ t.move_in_date || '—' }}</span></div>
              <div><span class="text-muted-foreground">租约到期</span> <span class="text-foreground tabular-nums">{{ t.lease_end_date || '—' }}</span></div>
              <div>
                <span class="text-muted-foreground">押金</span>
                <span class="text-foreground tabular-nums">{{ fmtMoney(t.deposit || 0) }} 元</span>
                <button
                  v-if="t.deposit || t.deposit_photos_count"
                  type="button"
                  class="ml-1 text-brand-600 dark:text-brand-300 hover:underline"
                  @click="openDepositPhotos(t)"
                >押金单{{ t.deposit_photos_count ? ` ${t.deposit_photos_count}` : ' +' }}</button>
              </div>
            </div>
            <div v-if="t.tags?.length" class="flex flex-wrap gap-1">
              <span v-for="tag in t.tags" :key="tag" class="badge !px-1.5 !py-0.5 text-[11px]">{{ tag }}</span>
            </div>
            <p v-if="t.notes" class="text-[11px] text-muted-foreground">备注：{{ t.notes }}</p>
            <!-- 同住成员：挂在租户名下，只登记身份、不参与计费（docs/08）。
                 在住与已搬离都列出来（已搬离的挂灰标签），否则退租登记后就查不到人。 -->
            <div class="pt-1 border-t border-border/60">
              <template v-if="membersOf(t.id).length">
                <div class="text-[11px] text-muted-foreground">
                  同住成员 {{ activeMembersOf(t.id).length }} 人（不参与计费）
                  <span v-if="membersOf(t.id).length > activeMembersOf(t.id).length" class="ml-1">
                    · 另有 {{ membersOf(t.id).length - activeMembersOf(t.id).length }} 人已搬离
                  </span>
                </div>
                <div
                  v-for="m in membersOf(t.id)"
                  :key="m.id"
                  class="text-[11px] text-muted-foreground flex flex-wrap items-center gap-x-1.5 mt-0.5"
                >
                  <span class="text-foreground font-medium">{{ m.name }}</span>
                  <span v-if="m.relation">· {{ m.relation }}</span>
                  <span class="tabular-nums break-all">{{ m.id_card || '未登记证件' }}</span>
                  <span v-if="m.birthday || m.gender" class="tabular-nums">· {{ m.birthday || '—' }} {{ genderLabel(m.gender) }}</span>
                  <span v-if="m.phone" class="tabular-nums">· {{ m.phone }}</span>
                  <span
                    v-if="!m.active"
                    class="badge !px-1.5 !py-0 text-[10px] bg-muted text-muted-foreground"
                  >已搬离<template v-if="m.move_out_date"> {{ m.move_out_date }}</template></span>
                </div>
              </template>
              <div v-else class="text-[11px] text-muted-foreground">无同住成员（只有租户本人）</div>
            </div>
            <p class="text-[11px] text-muted-foreground tabular-nums">合同 {{ t.contracts_count || 0 }} 份 · 资料附件 {{ t.attachments_count || 0 }} 个</p>
          </div>
        </div>

        <!-- 历史租客：默认收起，展开时才按房间拉一次 -->
        <div v-if="canViewTenants" class="pt-1 border-t border-border/60">
          <button
            type="button"
            class="text-[11px] text-muted-foreground hover:text-foreground"
            @click="toggleHistory"
          >{{ historyOpen ? '收起历史租客' : '查看历史租客' }}</button>
          <div v-if="historyOpen" class="mt-1.5 space-y-1">
            <div v-if="historyLoading" class="text-[11px] text-muted-foreground">加载中…</div>
            <div v-else-if="historyTenants.length === 0" class="text-[11px] text-muted-foreground">没有已退租的历史记录。</div>
            <template v-else>
              <div v-for="h in historyTenants" :key="h.id" class="text-[11px] text-muted-foreground">
                {{ h.name }}<span v-if="h.phone"> · {{ h.phone }}</span>
                <span class="tabular-nums"> · {{ h.move_in_date || '—' }} ~ {{ h.move_out_date || '退租日期未填' }}</span>
              </div>
            </template>
          </div>
        </div>
      </div>

      <!-- 押金单照片：从入住人资料里的「押金单」入口打开，复用租户附件能力 -->
      <Modal v-model="showDepositPhotos" title="押金单照片">
        <div v-if="depositTenant" class="space-y-3">
          <p class="text-xs text-muted-foreground leading-relaxed">
            <span class="font-semibold text-foreground">{{ depositTenant.name }}</span>
            · 押金 <span class="font-semibold text-foreground tabular-nums">{{ fmtMoney(depositTenant.deposit || 0) }} 元</span>
            · 拍下押金单实物留档，退租结算与对账时有据可查。
          </p>
          <TenantDepositPhotos
            :tenant-id="depositTenant.id"
            :can-edit="canEditTenants"
            @changed="onDepositPhotosChanged"
          />
          <div class="flex pt-1">
            <button class="btn-ghost flex-1" @click="showDepositPhotos = false">关闭</button>
          </div>
        </div>
      </Modal>

      <div class="flex pt-1">
        <button class="btn-ghost flex-1" @click="emit('update:modelValue', false)">关闭</button>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Modal from './Modal.vue'
import RoomItemsCard from './RoomItemsCard.vue'
import RoomPhotoSection from './RoomPhotoSection.vue'
import TenantDepositPhotos from './TenantDepositPhotos.vue'
import { genderLabel } from '../utils/id-card'
import { roomReferralText } from '../utils/referral'
import { useLedgerStore } from '../stores/ledger'
import { toast } from '../utils/toast'
import { defaultBilling, fetchBillingDefaults, numText, roomBillingSummary, type BillingDefaults } from '../utils/billing'
import { fmtMoney, getAllTenants, getTenantMembers, type RoomView, type Tenant, type TenantMember } from '../api/rental'

// 房源详情（2026-10-13 粉丝反馈）：一间房的基本信息 + 房间照片 + 入住人的身份资料。
//
// 数据来源刻意分成三路，各按各的模块权限把关：
//   - 房源信息与房间照片 → 房源模块（本组件直接吃列表行数据 + RoomPhotoSection）；
//   - 在租/历史租户资料 → 租户模块，只有拿到「租户与合同」查看权限才去拉，
//     没权限时显示一句说明而不是报错（403 不该在界面上变成红字）。
const ROOM_PHOTO_MAX = 30

// 入住人资料：身份证住址与紧急联系人是随「租户门户」一起加上的字段（v0.6.10），
// 这里统一当成可选——后端还没这列时读不到就显示「—」，不让房源详情依赖那批
// 数据先落地（用 Omit 重声明，两种字段形状下都能通过类型检查）。
type RoomOccupant = Omit<Tenant, 'address' | 'emergency_contact' | 'emergency_phone'> & {
  address?: string
  emergency_contact?: string
  emergency_phone?: string
}

const props = defineProps<{
  modelValue: boolean
  room: RoomView | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 照片增删后通知列表刷新照片数 */
  'photos-changed': []
}>()

const ledgerStore = useLedgerStore()
const canEditRooms = computed(() => ledgerStore.canEdit('rooms'))
const canViewTenants = computed(() => ledgerStore.canView('tenants'))
const canEditTenants = computed(() => ledgerStore.canEdit('tenants'))

const billingDefaults = ref<BillingDefaults>({ ...defaultBilling })
fetchBillingDefaults().then((d) => { billingDefaults.value = d })

const locationText = computed(() => {
  const r = props.room
  if (!r) return ''
  return [r.community, r.building, r.unit, r.floor ? r.floor + '层' : ''].filter(Boolean).join(' ') || '—'
})

/** 物品清单打印上的房屋名：位置 + 房号 + 定位地址（地址是 2026-10-20 新增字段）。 */
const printRoomLabel = computed(() => {
  const r = props.room
  if (!r) return ''
  const label = [locationText.value, r.room_no].filter(Boolean).join(' ')
  return r.address ? `${label}（${r.address}）` : label
})

// 概况里的字段表：手机端两列、桌面三列，金额与单价都带单位，避免"裸数字"
const roomFacts = computed(() => {
  const r = props.room
  if (!r) return []
  const elec = (r.elec_price || 0) > 0 ? r.elec_price : billingDefaults.value.elecPrice
  const gas = (r.gas_price || 0) > 0 ? r.gas_price : billingDefaults.value.gasPrice
  return [
    { label: '月租金', value: `${fmtMoney(r.default_rent)} 元` },
    // 面积与定位地址在 2026-10-20 新增；未填时不占位置（老房源不用补）。
    ...(Number(r.area_size || 0) > 0 ? [{ label: '建筑面积', value: `${numText(r.area_size)} ㎡` }] : []),
    ...(r.address ? [{ label: '定位地址', value: r.address }] : []),
    { label: '卫生费', value: `${fmtMoney(r.default_sanitation_fee)} 元/月` },
    { label: '管理费（物业费）', value: `${fmtMoney(r.default_management_fee)} 元/月` },
    { label: '电单价', value: `${numText(elec)} 元/度` },
    { label: '燃气单价', value: `${numText(gas)} 元/方` },
    { label: '计费与缴费（随租户）', value: roomBillingSummary(r.billing) },
    // 房东信息与介绍费都只在有值时才显示（选填字段不留空行）
    ...(r.landlord_name ? [{ label: '房东', value: r.landlord_name }] : []),
    ...(r.landlord_phone ? [{ label: '联系电话', value: r.landlord_phone }] : []),
    ...(roomReferralText(r) ? [{ label: '介绍费', value: roomReferralText(r) }] : []),
    { label: '抄表底数（水/电/燃气）', value: `${numText(r.initial_water)} / ${numText(r.initial_elec)} / ${numText(r.initial_gas)}` },
  ]
})

// 同住成员：一次按房间取回该房全部租户的成员（后端一条 JOIN 查询；**含已搬离**，
// 退租后仍能在这间房的详情里查到曾经住过谁），前端按 tenant_id 归组，
// 避免每个租户发一次请求（SQLite 单连接，N+1 代价高）。
const members = ref<TenantMember[]>([])
const membersByTenant = computed(() => {
  const map = new Map<number, TenantMember[]>()
  for (const m of members.value) {
    const list = map.get(m.tenant_id) ?? []
    list.push(m)
    map.set(m.tenant_id, list)
  }
  return map
})

function membersOf(tenantId: number): TenantMember[] {
  return membersByTenant.value.get(tenantId) ?? []
}

/** 该租户还在住的同住成员（已搬离的只作历史展示）。 */
function activeMembersOf(tenantId: number): TenantMember[] {
  return membersOf(tenantId).filter((m) => m.active)
}

// 快速从 101 切到 102 时，101 的响应可能后到并覆盖 102 的数据。每个加载器**各自**
// 一个令牌：三个加载器是并发发起的，共用一个令牌会让后发起的那个把先发起的作废
// （租户与成员会互相丢结果）——所以三个变量分开，各自比对各自的。
let membersToken = 0
let tenantsToken = 0
let historyToken = 0

async function loadMembers(roomId: number) {
  const token = ++membersToken
  members.value = []
  if (!canViewTenants.value) return
  try {
    const res = await getTenantMembers({ room_id: roomId })
    if (token !== membersToken) return
    if (res.data?.code === 0) members.value = res.data.data ?? []
  } catch {
    if (token !== membersToken) return
    // 成员加载失败不影响详情其余部分（租户与费用信息照常看）
    members.value = []
  }
}

const tenants = ref<RoomOccupant[]>([])
const tenantsLoading = ref(false)
const tenantsError = ref('')

async function loadTenants(roomId: number) {
  const token = ++tenantsToken
  tenants.value = []
  tenantsError.value = ''
  if (!canViewTenants.value) return
  tenantsLoading.value = true
  try {
    // 拉全：跨页合并（pageSize 上限 100，一间房的入住人不止一页的可能性极低，
    // 但沿用 getAllTenants 才与其他页面口径一致）
    const rows = await getAllTenants({ room_id: roomId, active: '1' })
    if (token !== tenantsToken) return
    tenants.value = rows
  } catch {
    if (token !== tenantsToken) return
    tenantsError.value = '加载入住人资料失败，请重试'
  } finally {
    if (token === tenantsToken) tenantsLoading.value = false
  }
}

// 押金单照片：与租户管理里同一份附件数据、同一个组件（label=押金单）。
// 详情页从入住人资料里的「押金单 n」入口打开，看完/补拍后刷新本页张数。
const showDepositPhotos = ref(false)
const depositTenant = ref<RoomOccupant | null>(null)

function openDepositPhotos(t: RoomOccupant) {
  depositTenant.value = t
  showDepositPhotos.value = true
}

function onDepositPhotosChanged() {
  if (props.room) void loadTenants(props.room.id)
}

// 历史租客：折叠着，展开时才拉一次（多数时候用户只看在租的人，不必每次开弹窗都请求）
const historyOpen = ref(false)
const historyLoading = ref(false)
const historyTenants = ref<Tenant[]>([])

async function toggleHistory() {
  historyOpen.value = !historyOpen.value
  if (!historyOpen.value || historyTenants.value.length || !props.room) return
  const token = ++historyToken
  const roomId = props.room.id
  historyLoading.value = true
  try {
    const rows = await getAllTenants({ room_id: roomId, active: '0' })
    if (token !== historyToken || props.room?.id !== roomId) return
    historyTenants.value = rows
  } catch {
    if (token !== historyToken) return
    historyTenants.value = []
  } finally {
    if (token === historyToken) historyLoading.value = false
  }
}

async function copyText(text: string, what: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast(`已复制${what}`)
  } catch {
    // 剪贴板权限被拒（HTTP 部署下常见）：提示手动选中复制
    toast(`复制失败，请长按选中${what}手动复制`, 'error')
  }
}

watch(() => [props.modelValue, props.room?.id] as const, ([open, roomId]) => {
  if (!open || !roomId) return
  historyOpen.value = false
  historyTenants.value = []
  void loadTenants(roomId)
  void loadMembers(roomId)
}, { immediate: true })
</script>
