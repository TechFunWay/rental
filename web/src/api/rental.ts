import request from './request'
import { toast } from '../utils/toast'
import { currentLedgerQuery } from '../stores/ledger'
// 缴费周期词表集中在 utils/cycle.ts（月付/季付/半年付/年付）；这里引用其类型，
// 避免各处重复写字面量联合，新增周期时只改一处。
import type { PayCycle } from '../utils/cycle'

// ---------- 台账范围（0.5.2 数据隔离） ----------
//
// 业务数据按台账归属，后端在 resolveLedger 里按 `?ledger_id=` 解析本次请求的范围：
//   - 带 ledger_id：只看/只写该台账（写操作必须带，否则被 requireLedgerModule 挡下）；
//   - 不带 ledger_id：按用户名下全部台账并集查询（写操作会被拒绝）。
//
// 前端**不在业务代码里逐个调用点塞参数**（6 个列表页 + 多个弹窗 + 导出下载
// 共几十处，漏一个就会出现「切了台账但某个页面还是旧数据」这种难查的 bug），
// 而是集中在这里：
//
//   withLedger(params)  —— 给对象型 params 补上 ledger_id（列表/详情 GET）；
//   withLedgerUrl(url)  —— 给已拼好 query 的 URL 补上 ledger_id（导出下载类）。
//
// ledger_id 的来源是 stores/ledger.ts 的 currentLedgerQuery()：它读的是台账 store
// 写入 localStorage 的同一个键，因此这里不需要 import store 实例，也不会
// 形成 api/rental → stores/ledger → api/org → api/rental 的模块循环。
//
// 查询类接口的每个入口都显式调用这两个函数（而不是放在 axios 拦截器里），
// 是为了让「哪些请求带 ledger_id」在代码里一目了然——写路径（POST/PUT/DELETE
// 的 body）刻意不自动附加 ledger_id，由用户在页面上选定的台账决定，且后端只
// 读 query 参数。

/** 给查询参数补上当前台账的 ledger_id；尚未选好台账时不补。 */
function withLedger<T extends Record<string, unknown> | undefined>(params: T): T & { ledger_id?: number } {
  const ledgerId = currentLedgerQuery()
  if (ledgerId === null) return (params ?? {}) as T & { ledger_id?: number }
  return { ...(params ?? ({} as T)), ledger_id: ledgerId }
}

/** 给已拼好 query 的路径补上 ledger_id（导出/模板下载等直接走 URL 的接口）。 */
function withLedgerUrl(url: string): string {
  const ledgerId = currentLedgerQuery()
  if (ledgerId === null) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}ledger_id=${ledgerId}`
}

/** 当前生效的 ledger_id（写操作/自定义 URL 需要时使用）。 */
export function activeLedgerId(): number | null {
  return currentLedgerQuery()
}

// ---------- 类型 ----------

export interface Room {
  id: number
  community: string
  room_no: string
  building: string
  unit: string
  floor: string
  /** 标签（如 物业代管/临街/精装），自定义分组用 */
  tags: string[]
  default_rent: number
  default_sanitation_fee: number
  default_management_fee: number
  elec_price: number
  gas_price: number
  initial_water: number
  initial_elec: number
  initial_gas: number
  /** 定位地址（2026-10-20 房东需求）：地图定位/门牌之外的补充位置描述，自由文本，≤200 字 */
  address: string
  /** 建筑面积（㎡）：0 = 未填（表单留空即 0，展示按未填处理） */
  area_size: number
  notes: string
  /** 房东姓名与联系电话（选填）：中介/村委核实带看用 */
  landlord_name: string
  landlord_phone: string
  /** 介绍费方式：'' 不提供 / fixed 固定金额（元）/ percent 月租金百分比（%） */
  referral_mode: '' | 'fixed' | 'percent'
  /** 介绍费取值（固定金额元数，或百分比数字） */
  referral_value: number
}

/** 房间当前生效的计费与缴费设置（后端按在租租户 → 全局默认解析，v0.2.3 起）。 */
export interface RoomBilling {
  /** meter 按吨 / monthly 包月 */
  water_mode: 'meter' | 'monthly'
  /** 当前生效方式下的金额：按吨=元/吨；包月=元/月 */
  water_amount: number
  /** 按吨口径的配置金额（元/吨，租户 → 全局默认） */
  water_meter_price: number
  /** 包月口径的配置金额（元/月，租户 → 全局默认） */
  water_monthly_fee: number
  /** 缴费周期：monthly 月付 / quarterly 季付 / semiannual 半年付 / annual 年付 */
  pay_cycle: PayCycle
  /** 1-28 号；0 = 不提醒 */
  pay_day: number
  /** 提前提醒天数 0-30 */
  remind_days: number
  /** 不参与计费的收费项目 key（租户勾选，空 = 全部参与） */
  excluded_fees: string[]
}

export interface TenantBrief {
  id: number
  name: string
  phone: string
}

export interface RoomView extends Room {
  current_tenants: TenantBrief[]
  has_bills: boolean
  /** 最近一张账单的账期 YYYY-MM，无账单为空串 */
  last_bill_period: string
  /** 生效计费与缴费设置：在租租户的设置 → 偏好设置中的全局默认 */
  billing: RoomBilling
  /** 房源照片张数（详情弹窗里的照片入口提示） */
  photo_count: number
}

export interface Tenant {
  id: number
  room_id: number
  name: string
  phone: string
  /** 身份证号，选填 */
  id_card: string
  /** 身份证住址（v0.6.10；租客可在门户申请修改，房东审核后覆盖） */
  address: string
  /** 紧急联系人姓名（v0.6.10） */
  emergency_contact: string
  /** 紧急联系人电话（v0.6.10） */
  emergency_phone: string
  /**
   * 绑定的租户登录账号 id（v0.6.10），0 = 未关联。
   * 与 UserID（录入人/房东）语义不同：这是租客本人登录门户用的账号。
   */
  account_user_id: number
  /** 标签（如 优质/中介/长租），自定义分组用 */
  tags: string[]
  move_in_date: string
  lease_end_date: string
  move_out_date: string
  deposit: number
  /** '' = 跟随全局默认；meter 按吨；monthly 包月 */
  water_mode: '' | 'meter' | 'monthly'
  /** 按吨单价（元/吨），0 = 跟随全局默认 */
  water_price: number
  /** 包月金额（元/月），0 = 跟随全局默认 */
  water_monthly_fee: number
  /** '' = 跟随全局默认；否则为缴费周期（月付/季付/半年付/年付） */
  pay_cycle: '' | PayCycle
  /** 缴费日 1-28 号，0 = 不提醒，-1 = 跟随全局默认 */
  pay_day: number
  /** 提前提醒天数 0-30，-1 = 跟随全局默认 */
  remind_days: number
  /** 不参与计费的收费项目 key 列表（空 = 全部参与；勾选 UI 的反向存储） */
  excluded_fees: string[]
  active: boolean
  notes: string
  room_no?: string
  room_label?: string
  /** 该租户名下的合同文件数（列表接口补充，非表字段） */
  contracts_count?: number
  /** 该租户名下的资料附件数（列表接口补充，非表字段） */
  attachments_count?: number
  /** 该租户名下**在住**的住户成员数（列表接口补充，非表字段；已搬离的不计） */
  members_count?: number
  /** 该租户名下「押金单」照片张数（列表接口补充，非表字段） */
  deposit_photos_count?: number
}

/** 住户成员：挂在租户（计费主体）名下的同住人，只登记身份、不参与计费。 */
export interface TenantMember {
  id: number
  ledger_id: number
  tenant_id: number
  name: string
  /** 与租户关系：配偶/子女/父母…（自由文本，界面给常用选项） */
  relation: string
  phone: string
  id_card: string
  /** 由 18 位身份证号派生：male / female / '' */
  gender: string
  /** 由 18 位身份证号派生：YYYY-MM-DD */
  birthday: string
  move_in_date: string
  move_out_date: string
  /** 是否在住；租户退租时批量置为已搬离 */
  active: boolean
  notes: string
  created_at: string
  updated_at: string
}

export type TenantMemberInput = {
  name: string
  relation?: string
  phone?: string
  id_card?: string
  move_in_date?: string
  move_out_date?: string
  active?: boolean
  notes?: string
}

export interface Contract {
  id: number
  tenant_id: number
  file_name: string
  file_size: number
  mime_type: string
  created_at: string
}

export interface MeterRecord {
  id: number
  room_id: number
  period: string
  water: number
  elec: number
  gas: number
  note: string
  created_at: string
  updated_at: string
  /** 以下由列表接口补充 */
  room_no: string
  prev_water: number
  prev_elec: number
  prev_gas: number
  usage_water: number
  usage_elec: number
  usage_gas: number
  /** 该房该账期的抄表照片张数（水/电/燃气合计），列表页显示「照片 n」入口 */
  photo_count: number
  /** 该房该账期已开票时的账单 id / 本期应收 / 其中的自定义收费合计（未开票为 0） */
  bill_id: number
  bill_total: number
  bill_extra: number
}

export interface Bill {
  id: number
  room_id: number
  period: string
  room_no: string
  tenant_name: string
  rent: number
  water_last: number
  water_now: number
  elec_last: number
  elec_now: number
  gas_last: number
  gas_now: number
  /** 开票时快照的缴费周期（覆盖月数见 utils/cycle.ts 的 cycleMonths） */
  pay_cycle: PayCycle
  /** 开票时快照的水费计费方式：meter 按吨（用量×单价）/ monthly 包月（固定金额） */
  water_mode: 'meter' | 'monthly'
  /** 开票时快照：不参与计费的项目 key（租户级勾选，被排除项目费用恒为 0） */
  excluded_fees: string[]
  /**
   * 开票时快照：台账**停用**的收费项目 key（房东在收费项目里停用了燃气费等）。
   * 与 excluded_fees 同样让本期费用为 0，但来源是台账级计费开关。
   */
  disabled_fees: string[]
  /** 按吨 = 元/吨；包月 = 元/月 */
  water_price: number
  elec_price: number
  gas_price: number
  water_fee: number
  elec_fee: number
  gas_fee: number
  sanitation_fee: number
  management_fee: number
  /** 押金（元）：只在租户首张账单上收一次，与租金一起结清（退租时结算退还） */
  deposit_fee: number
  /** 自定义收费项目（宽带费等）金额合计 */
  extra_amount: number
  /** 本期费用合计（不含结转）——注意不是「应缴」，应缴看 payable */
  total_amount: number
  /**
   * 上期结转（少交为正、多交为负）。
   * 台账未开启结转时恒为 0（issue #5 需求 7，见 docs/14）。
   */
  carry_in: number
  /** 本期应缴 = total_amount + carry_in；界面展示与欠缴计算都应以它为准 */
  payable: number
  paid_amount: number
  status: 'paid' | 'partial' | 'unpaid'
  receipt_no: string
  remark: string
  paid_at: string | null
}

export interface PropertyMeta {
  name: string
  contact: string
  note: string
}

export interface LeaseDueItem {
  id: number
  name: string
  room_no: string
  lease_end_date: string
  days_left: number // 负数 = 已过期 N 天
}

export interface ArrearsItem {
  id: number
  room_no: string
  tenant_name: string
  period: string
  arrears: number
}

export interface PaymentDueItem {
  room_id: number
  room_no: string
  community: string
  tenant_name: string
  pay_cycle: PayCycle
  pay_day: number
  period: string
  months: number
  due_date: string
  days_left: number // 负数 = 已逾期 N 天
  expected_amount: number
  bill_id: number
}

export interface RentalStats {
  rooms_total: number
  rooms_occupied: number
  rooms_vacant: number
  tenants_active: number
  /** 在住住户成员数（只登记身份、不参与计费） */
  members_active: number
  /** 在住人数 = 在租租户 + 在住住户成员 */
  residents_active: number
  month: {
    period: string
    total: number
    paid: number
    outstanding: number
    bill_count: number
    arrears_count: number
  }
  overall: { outstanding: number; arrears_count: number }
  lease_due: LeaseDueItem[]
  payment_due: PaymentDueItem[]
  arrears_list: ArrearsItem[]
  recent_bills: Bill[]
}

export interface Page<T> {
  items: T[]
  total: number
  page: number
  pageSize: number
}

export interface ImportResult {
  created: number
  updated: number
  failed: number
  errors: { line: number; reason: string }[]
}

// ---------- 工具 ----------

export function fmtMoney(v: number | undefined | null): string {
  const n = Number(v || 0)
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/**
 * 账单欠缴额（最小为 0）。
 *
 * 用 `payable`（本期应缴 = 本期费用 + 上期结转）而不是 `total_amount`：
 * 开了结转的台账里，房东在这一栏看到的应当是「这户该交多少」，
 * 少交的部分要能在本期一并收回（issue #5 需求 7）。
 *
 * 兼容老数据：后端未返回 payable 时（例如升级瞬间的旧响应）退回 total_amount，
 * 保证金额不会因为多了一个字段而变成 NaN。
 */
export function arrearsOf(b: Bill): number {
  const payable = typeof b.payable === 'number' && b.payable !== 0 ? b.payable : b.total_amount
  const d = payable - b.paid_amount
  return d > 0.005 ? Math.round(d * 100) / 100 : 0
}

/** 本期应缴（含上期结转）；老数据无 payable 时退回本期费用。 */
export function payableOf(b: Bill): number {
  return typeof b.payable === 'number' && b.payable !== 0 ? b.payable : b.total_amount
}

/** 上期结转额（少交为正、多交为负）；界面据此显示「含上期结转 ±N」。 */
export function carryInOf(b: Bill): number {
  return Number(b.carry_in || 0)
}

export function isArrears(b: Bill): boolean {
  return arrearsOf(b) > 0
}

export function currentPeriod(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

function download(url: string) {
  // 下载类接口需要带 token，走 axios blob 再触发浏览器保存。
  request
    .get(url, { responseType: 'blob' })
    .then((res) => {
      const cd = res.headers['content-disposition'] || ''
      const m = /filename\*=UTF-8''([^;]+)/.exec(cd)
      let name = 'download.csv'
      if (m) {
        name = decodeURIComponent(m[1])
      } else if (/filename="([^"]+)"/.test(cd)) {
        name = /filename="([^"]+)"/.exec(cd)![1]
      }
      const url2 = URL.createObjectURL(res.data)
      const a = document.createElement('a')
      a.href = url2
      a.download = name
      a.click()
      URL.revokeObjectURL(url2)
    })
    .catch(() => toast('下载失败，请重试', 'error'))
}

// ---------- 房源 ----------

export function getRooms(params: { page?: number; pageSize?: number; keyword?: string; status?: string; community?: string; building?: string; tag?: string }) {
  return request.get('/api/rental/rooms', { params: withLedger(params) })
}

export function createRoom(data: Partial<Room>) {
  return request.post(withLedgerUrl('/api/rental/rooms'), data)
}

export function updateRoom(id: number, data: Partial<Room>) {
  return request.put(withLedgerUrl(`/api/rental/rooms/${id}`), data)
}

export function deleteRoom(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/rooms/${id}`))
}

/**
 * 把房源移动到另一个共同台账：连同它的租户与入住成员、抄表记录与照片、账单与收款、
 * 合同与附件、房间照片、待审登记一起迁移（附件文件也会搬到新台账目录）。
 * 权限：源台账「房源」完全档 + 目标台账「房源」操作档，目标必须是共同台账。
 */
export function moveRoom(roomId: number, targetLedgerId: number) {
  return request.post(withLedgerUrl(`/api/rental/rooms/${roomId}/move`), { target_ledger_id: targetLedgerId })
}

// ---------- 房源照片 ----------
// 房源本身的照片（室内、门牌、水表电表位置…），按房间归属、与账期无关；
// 与「抄表照片」（按房间 + 账期 + 表种）是两张表、两套接口。
// 读取同样要带登录态，走 blob 中转。

export interface RoomPhoto {
  id: number
  ledger_id: number
  room_id: number
  file_name: string
  file_size: number
  mime_type: string
  created_at: string
}

export function getRoomPhotos(roomId: number) {
  // 一次取全：服务端每间房上限 30 张，pageSize 给 100 足够。
  return request.get('/api/rental/room-photos', { params: withLedger({ room_id: roomId, pageSize: 100 }) })
}

export function uploadRoomPhotos(roomId: number, files: File[]) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  return request.post(withLedgerUrl(`/api/rental/rooms/${roomId}/photos`), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function deleteRoomPhoto(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/room-photos/${id}`))
}

export async function fetchRoomPhotoBlob(id: number): Promise<Blob> {
  const res = await request.get(withLedgerUrl(`/api/rental/room-photos/${id}/file`), { responseType: 'blob' })
  return res.data as Blob
}

// ---------- 租户 ----------

export function getTenants(params: { page?: number; pageSize?: number; keyword?: string; active?: string; room_id?: number; community?: string; tag?: string }) {
  return request.get('/api/rental/tenants', { params: withLedger(params) })
}

/**
 * 表单里的成员行：带 id = 改这一行、不带 = 新增；**没出现在数组里的成员会被删除**
 * （登记/编辑租户表单的「入住成员」列表即真相）。传 undefined（字段不出现）则不动成员。
 */
export type TenantMemberPayload = TenantMemberInput & { id?: number }

export function createTenant(data: Partial<Tenant> & { application_id?: number; members?: TenantMemberPayload[] }) {
  return request.post(withLedgerUrl('/api/rental/tenants'), data)
}

export function updateTenant(id: number, data: Partial<Tenant> & { members?: TenantMemberPayload[] }) {
  return request.put(withLedgerUrl(`/api/rental/tenants/${id}`), data)
}

export function checkoutTenant(id: number, moveOutDate?: string) {
  return request.post(withLedgerUrl(`/api/rental/tenants/${id}/checkout`), { move_out_date: moveOutDate || '' })
}

export function deleteTenant(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/tenants/${id}`))
}

// ---------- 房东入驻申请（平台化） ----------
// 角色三层：admin 系统管理员 / user 房东 / tenant 租户（内部值不变，界面文案见 utils/role.ts）。
// 房东注册后 status=2 待审核，**在等待审核期间**提交/更新自己的入驻资料；
// 平台管理员在管理后台审核，通过后账号可用并拥有个人台账。

export interface LandlordApplication {
  id: number
  user_id: number
  username: string
  name: string
  phone: string
  wechat: string
  region: string
  room_scale: string
  intro: string
  status: 'pending' | 'approved' | 'rejected'
  review_note: string
  reviewed_at: string | null
  created_at: string
  updated_at: string
  /** 列表接口补充：账号当前状态（1 正常 / 2 待审核 / 0 停用） */
  user_status?: number
}

export interface LandlordApplyInput {
  name: string
  phone: string
  wechat?: string
  region?: string
  room_scale?: string
  intro?: string
}

/** 提交/更新自己的入驻申请（待审核账号也能调，路由在白名单里）。 */
export function submitLandlordApply(data: LandlordApplyInput) {
  return request.post('/api/rental/landlord-apply', data)
}

/** 读自己的入驻申请（等待审核页显示「已提交」用）；没有申请时 data 为 null。 */
export function getMyLandlordApply() {
  return request.get('/api/rental/landlord-apply/me')
}

/** 平台管理员：入驻申请队列（默认待审）。 */
export function getLandlordApplications(status: 'pending' | 'approved' | 'rejected' = 'pending') {
  return request.get('/api/rental/landlord-applications', { params: { status } })
}

export function approveLandlordApplication(id: number, note = '') {
  return request.post(`/api/rental/landlord-applications/${id}/approve`, { note })
}

export function rejectLandlordApplication(id: number, note = '') {
  return request.post(`/api/rental/landlord-applications/${id}/reject`, { note })
}

// ---------- 住户成员 ----------
// 挂在租户（计费主体）名下的同住人：只登记身份信息，不参与任何计费。
// 列表接口返回数组（成员数量有限，不分页）；room_id 用于房源详情一次取该房全部成员。

export function getTenantMembers(params: { tenant_id?: number; room_id?: number; active?: '1' | '0' }) {
  return request.get('/api/rental/tenant-members', { params: withLedger(params) })
}

export function createTenantMember(tenantId: number, data: TenantMemberInput) {
  return request.post(withLedgerUrl(`/api/rental/tenants/${tenantId}/members`), data)
}

export function updateTenantMember(id: number, data: TenantMemberInput) {
  return request.put(withLedgerUrl(`/api/rental/tenant-members/${id}`), data)
}

export function deleteTenantMember(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/tenant-members/${id}`))
}

// ---------- 房源登记（免登录，村委/中介场景） ----------
// 台账级 token：一个台账一个链接（orgs.room_apply_token），村民扫码自己登记房源；
// 提交进「待审房源」队列，房东/村委审核通过才建房源（可同时带一名现有租户）。

export interface RoomApplication {
  id: number
  ledger_id: number
  /** 提交人（房东/村民） */
  name: string
  phone: string
  community: string
  building: string
  unit: string
  floor: string
  room_no: string
  default_rent: number
  notes: string
  /** 登记时挂的介绍费（审核通过写入房源） */
  referral_mode: '' | 'fixed' | 'percent'
  referral_value: number
  /** 选填：随登记一起提交的现有租户 */
  tenant_name: string
  tenant_phone: string
  tenant_id_card: string
  status: 'pending' | 'approved' | 'rejected'
  review_note: string
  room_id: number
  tenant_id: number
  created_at: string
  /** 照片流地址（登录接口取流，需转 blob 渲染） */
  photo_urls: string[]
}

/** 取/生成**当前台账**的房源登记链接（ledger_id 由 withLedgerUrl 带上）。 */
export function getLedgerRoomApplyLink(regenerate = false) {
  return request.post(withLedgerUrl(`/api/rental/room-apply-link${regenerate ? '?regenerate=1' : ''}`))
}

export function getRoomApplications(params: { status?: 'pending' | 'approved' | 'rejected' } = { status: 'pending' }) {
  return request.get('/api/rental/room-applications', { params: withLedger(params) })
}

export function approveRoomApplication(id: number, body: { room_no?: string; default_rent?: number } = {}) {
  return request.post(withLedgerUrl(`/api/rental/room-applications/${id}/approve`), body)
}

export function rejectRoomApplication(id: number, note: string) {
  return request.post(withLedgerUrl(`/api/rental/room-applications/${id}/reject`), { note })
}

export async function fetchRoomApplicationPhotoBlob(url: string): Promise<Blob> {
  const res = await request.get(withLedgerUrl(url), { responseType: 'blob' })
  return res.data as Blob
}

/** 公开页：凭 token 取台账名（免登录）。 */
export function getPublicRoomApplyInfo(token: string) {
  return request.get(`/api/rental/room-apply/${token}`)
}

/** 公开页：提交房源登记（免登录，multipart）。 */
export function submitPublicRoomApply(token: string, form: FormData) {
  return request.post(`/api/rental/room-apply/${token}`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

// ---------- 合同存档 ----------

export function getContracts(tenantId: number) {
  return request.get(`/api/rental/tenants/${tenantId}/contracts`, { params: withLedger({}) })
}

export function uploadContracts(tenantId: number, files: File[]) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  return request.post(withLedgerUrl(`/api/rental/tenants/${tenantId}/contracts`), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function deleteContract(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/contracts/${id}`))
}

// 合同文件必须带登录态读取（不走公开的 /uploads），统一用 blob 中转：
// fetchContractBlob 供弹窗内预览（图片直接渲染 / PDF 内嵌）与下载使用。
export async function fetchContractBlob(id: number): Promise<Blob> {
  const res = await request.get(withLedgerUrl(`/api/rental/contracts/${id}/file`), { responseType: 'blob' })
  return res.data as Blob
}

export async function downloadContract(id: number, fileName: string) {
  const blob = await fetchContractBlob(id)
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName || '合同文件'
  a.click()
  URL.revokeObjectURL(url)
}

// ---------- 拉全量列表（跨页合并） ----------
//
// 为什么需要它：服务端 utils.NormalizePage 把 pageSize 硬上限钳在 100
// （MaxPageSize，框架层常量，各实例一致，不宜按单个应用改）。任何「要一次拿全
// 房间/账单来配对」的场景写 pageSize: 200 都会被**静默截到 100**——房间超过
// 100 间时抄表弹窗就会缺房、串房（2026-10-11 粉丝报的「费用是别的房间」的
// 根因之一）。这里显式翻页合并，把「拿全」这件事做对，而不是赌一个更大的数字。
export async function fetchAllPages<T>(
  fetchPage: (page: number, pageSize: number) => Promise<{ data?: { code?: number; data?: { items?: T[]; total?: number } } }>,
  opts: { pageSize?: number; maxPages?: number } = {},
): Promise<T[]> {
  const pageSize = opts.pageSize ?? 100   // 与 MaxPageSize 对齐
  const maxPages = opts.maxPages ?? 100   // 兜底：最多 100 页（1 万条），防死循环
  const all: T[] = []
  for (let page = 1; page <= maxPages; page++) {
    const res = await fetchPage(page, pageSize)
    if (res.data?.code !== 0) break
    const items = res.data.data?.items ?? []
    all.push(...items)
    const total = res.data.data?.total ?? 0
    if (items.length === 0 || all.length >= total) break
  }
  return all
}

export function getAllRooms(params: { keyword?: string; status?: string } = {}) {
  return fetchAllPages<RoomView>((page, pageSize) => getRooms({ ...params, page, pageSize }))
}

export function getAllTenants(params: { keyword?: string; active?: string; room_id?: number; community?: string; tag?: string } = {}) {
  return fetchAllPages<Tenant>((page, pageSize) => getTenants({ ...params, page, pageSize }))
}

export function getAllBills(params: { period?: string; status?: string; keyword?: string } = {}) {
  return fetchAllPages<Bill>((page, pageSize) => getBills({ ...params, page, pageSize }))
}

// ---------- 租户资料附件 ----------
// 与「合同存档」分开：合同是签约凭证，附件是房东自用留档（租房协议照片、
// 汽车车牌、租客本人照片、身份证正反面）。两者各自独立入口、独立表。

export interface TenantAttachment {
  id: number
  ledger_id: number
  tenant_id: number
  file_name: string
  file_size: number
  mime_type: string
  label: string
  created_at: string
}

export function getAttachments(tenantId: number, label = '') {
  // label 选填：押金单等专用入口只取该用途的附件，其余附件不白拉一遍。
  return request.get(`/api/rental/tenants/${tenantId}/attachments`, {
    params: withLedger(label ? { label } : {}),
  })
}

// label 是整批共用的用途备注（如「身份证」），选填。
export function uploadAttachments(tenantId: number, files: File[], label = '') {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  if (label) form.append('label', label)
  return request.post(withLedgerUrl(`/api/rental/tenants/${tenantId}/attachments`), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function deleteAttachment(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/attachments/${id}`))
}

// 附件必须带登录态读取（不走公开的 /uploads），统一用 blob 中转：
// 图片直接渲染、PDF 内嵌预览，也可下载。
export async function fetchAttachmentBlob(id: number): Promise<Blob> {
  const res = await request.get(withLedgerUrl(`/api/rental/attachments/${id}/file`), { responseType: 'blob' })
  return res.data as Blob
}

export async function downloadAttachment(id: number, fileName: string) {
  const blob = await fetchAttachmentBlob(id)
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName || '附件'
  a.click()
  URL.revokeObjectURL(url)
}

// ---------- 抄表台账 ----------

export function getMeterRecords(params: { page?: number; pageSize?: number; period?: string; period_from?: string; period_to?: string; room_id?: number; community?: string }) {
  return request.get('/api/rental/meter-records', { params: withLedger(params) })
}

export function upsertMeterRecord(data: {
  room_id: number
  period: string
  water: number
  elec: number
  gas: number
  note?: string
}) {
  return request.post(withLedgerUrl('/api/rental/meter-records'), data)
}

export function deleteMeterRecord(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/meter-records/${id}`))
}

// ---------- 抄表照片 ----------
// 表盘现场照片按「房间 + 账期 + 表种」归属（与抄表台账同口径），抄表开票与
// 只录读数两条入口共用一套照片。读取同样必须带登录态，走 blob 中转。

/** 抄表表种：水 / 电 / 燃气。 */
export type MeterKind = 'water' | 'elec' | 'gas'

export interface MeterPhoto {
  id: number
  ledger_id: number
  room_id: number
  period: string
  meter: MeterKind
  file_name: string
  file_size: number
  mime_type: string
  created_at: string
}

export function getMeterPhotos(params: { room_id?: number; period?: string; meter?: MeterKind }) {
  // 一次取全：同一房同一月的照片最多 18 张（三表各 6），远低于 pageSize 上限。
  return request.get('/api/rental/meter-photos', { params: withLedger({ pageSize: 100, ...params }) })
}

export function uploadMeterPhotos(roomId: number, files: File[], period: string, meter: MeterKind) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  form.append('period', period)
  form.append('meter', meter)
  return request.post(withLedgerUrl(`/api/rental/rooms/${roomId}/meter-photos`), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function deleteMeterPhoto(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/meter-photos/${id}`))
}

export async function fetchMeterPhotoBlob(id: number): Promise<Blob> {
  const res = await request.get(withLedgerUrl(`/api/rental/meter-photos/${id}/file`), { responseType: 'blob' })
  return res.data as Blob
}

// ---------- 账单 ----------

export function getBills(params: {
  page?: number; pageSize?: number; period?: string
  period_from?: string; period_to?: string
  bill_type?: PayCycle
  status?: string; keyword?: string
  room_id?: number; community?: string
}) {
  return request.get('/api/rental/bills', { params: withLedger(params) })
}

export function getBillPeriods() {
  return request.get('/api/rental/bills/periods', { params: withLedger({}) })
}

export function getBillDetail(id: number) {
  return request.get(`/api/rental/bills/${id}`, { params: withLedger({}) })
}

/**
 * 开票预览（不写库）：给定房间与账期，返回账单与费用明细。
 * exists=true 表示该账期已有账单（原样返回），false 表示保存时才会生成；
 * 两种情况下 items 都含自定义收费项目、押金与上期结转，抄表开票弹窗据此
 * 显示与服务端建账一致的金额（2026-10-17 粉丝反馈）。
 */
export interface BillPreview {
  exists: boolean
  bill: Bill
  items: BillItemDetail[]
}

export function previewBill(roomId: number, period: string) {
  return request.get('/api/rental/bills/preview', {
    params: withLedger({ room_id: roomId, period }),
  })
}

export function createBill(roomId: number, period: string, readings?: {
  water_now?: number; elec_now?: number; gas_now?: number
  water_mode?: 'meter' | 'monthly'
  water_price?: number; elec_price?: number; gas_price?: number
  /** 租金金额（元/期）：抄表开票时可改，未传则按房间默认租金与缴费周期预填 */
  rent?: number
  /** 备注：抄表开票时可写（如"租金对应租期 2026-09~2027-08"） */
  remark?: string
}) {
  return request.post(withLedgerUrl('/api/rental/bills'), { room_id: roomId, period, ...readings })
}

export function generateBills(period: string) {
  return request.post(withLedgerUrl('/api/rental/bills/generate'), { period })
}

export function updateBill(id: number, data: Partial<Bill>) {
  return request.put(withLedgerUrl(`/api/rental/bills/${id}`), data)
}

export function payBill(
  id: number,
  amount: number,
  note = '',
  items?: { key: string; name: string; amount: number }[],
) {
  return request.post(withLedgerUrl(`/api/rental/bills/${id}/pay`), { amount, note, items })
}

export function deleteBill(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/bills/${id}`))
}

// ---------- 导入导出 ----------

export function downloadTemplate() {
  download(withLedgerUrl('/api/rental/template'))
}

export function exportBills(period?: string, periodFrom?: string, periodTo?: string) {
  // 账期筛选三选一：单月（period）优先；否则用区间 period_from / period_to
  // （两端闭区间，可只给一端）；都不给则导出全部月份。
  const qs = new URLSearchParams()
  if (period) qs.set('period', period)
  else {
    if (periodFrom) qs.set('period_from', periodFrom)
    if (periodTo) qs.set('period_to', periodTo)
  }
  const q = qs.toString()
  download(withLedgerUrl(`/api/rental/export${q ? `?${q}` : ''}`))
}

export function exportRooms() {
  download(withLedgerUrl('/api/rental/rooms/export'))
}

export function downloadRoomTemplate() {
  download(withLedgerUrl('/api/rental/rooms/template'))
}

export function importRooms(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(withLedgerUrl('/api/rental/rooms/import'), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function downloadTenantTemplate() {
  download(withLedgerUrl('/api/rental/tenants/template'))
}

/**
 * 导出租户 CSV（派出所场景）：筛选参数与 getTenants 完全一致，
 * 「列表看到什么就导出什么」。后端列 = 租客模板 15 列 + 末尾只读列
 * （房号 / 在租状态 / 出生日期 / 性别），前 15 列可直接回导。
 */
export function exportTenants(params: {
  keyword?: string; active?: string; room_id?: number; community?: string; tag?: string
} = {}) {
  const q = new URLSearchParams()
  if (params.keyword) q.set('keyword', params.keyword)
  if (params.active) q.set('active', params.active)
  if (params.room_id) q.set('room_id', String(params.room_id))
  if (params.community) q.set('community', params.community)
  if (params.tag) q.set('tag', params.tag)
  const qs = q.toString()
  download(withLedgerUrl(`/api/rental/tenants/export${qs ? `?${qs}` : ''}`))
}

// ---------- 租户资料粘贴识别（外网不通时的录入路径） ----------
//
// 租客把资料按「姓名：张三」一行一个字段写成文案，微信发给房东；房东整段
// 粘贴到「租户管理 → 粘贴识别」里解析成草稿，核对/补全后一键入库。
// 多人资料写在同一条消息里，一起识别、一起入库。

/** 一条识别出来的草稿（未入库；warnings 是格式类提示，不阻断入库）。 */
export interface TenantTextDraft {
  name: string
  phone: string
  id_card: string
  community: string
  room_no: string
  move_in_date: string
  lease_end_date: string
  deposit: number
  notes: string
  /** 格式类提示（手机号位数不对、日期认不出…），原样展示给房东核对 */
  warnings: string[]
  /** 该草稿在原文里的行号（从 1 起），方便回头对照 */
  lines: number[]
}

export interface TenantTextParseResult {
  drafts: TenantTextDraft[]
  /** 没能识别的原文行（原样带回，方便看出哪里写岔了） */
  unmatched_lines: string[]
  count: number
}

/** 解析粘贴的租户资料文案（只读，不写库）。 */
export function parseTenantText(text: string) {
  return request.post<{ code: number; message: string; data: TenantTextParseResult }>(
    '/api/rental/tenants/parse-text',
    { text },
  )
}

/** 批量入库核对过的草稿（复用租客 CSV 导入的合并口径）。 */
export function importTenantsBatch(items: Array<Partial<Tenant>>) {
  return request.post<{ code: number; message: string; data: ImportResult }>(
    withLedgerUrl('/api/rental/tenants/batch'),
    { items },
  )
}

export function importTenants(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(withLedgerUrl('/api/rental/tenants/import'), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

// 批量打印单据：按账期区间一次取回全部账单与费用明细（上限 500 张）
export interface ReceiptEntry {
  bill: Bill
  items: BillItemDetail[]
}

export interface ReceiptBundle {
  property: PropertyMeta
  bills: ReceiptEntry[]
}

export function getBillReceipts(from: string, to: string) {
  const params = new URLSearchParams()
  if (from) params.set('from', from)
  if (to) params.set('to', to)
  return request.get(withLedgerUrl(`/api/rental/bills/receipts?${params.toString()}`))
}

export function importBills(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post(withLedgerUrl('/api/rental/import'), form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

// ---------- 统计 ----------

export function getStats(period?: string) {
  return request.get('/api/rental/stats', { params: withLedger(period ? { period } : {}) })
}

// ---------- 缴费通知渠道 ----------

export type NotifyChannel = 'dingtalk' | 'email' | 'sms' | 'qq'

export interface NotifyBindingItem {
  id: number
  target_masked: string
  status: string
}

export interface NotifyChannelStatus {
  channel: NotifyChannel
  label: string
  configured: boolean
  bound: boolean
  status: string
  target_masked: string
  description: string
  bot_link?: string
  bindings?: NotifyBindingItem[]
}

export function getNotifyChannels() {
  return request.get('/api/rental/notify/channels')
}

export function bindNotifyChannel(channel: NotifyChannel, target: string) {
  return request.put(`/api/rental/notify/bind/${channel}`, { target })
}

export function unbindNotifyChannel(channel: NotifyChannel) {
  return request.delete(`/api/rental/notify/bind/${channel}`)
}

export function deleteNotifyBinding(channel: NotifyChannel, id: number) {
  return request.delete(`/api/rental/notify/bind/${channel}/${id}`)
}

export function toggleNotifyChannel(channel: NotifyChannel, enabled: boolean) {
  return request.post(`/api/rental/notify/toggle/${channel}`, { enabled })
}

export function testNotifyChannel(channel: NotifyChannel) {
  return request.post(`/api/rental/notify/test/${channel}`)
}

export function getNotifyProvider(provider: 'email' | 'sms' | 'qq') {
  return request.get(`/api/rental/notify/provider/${provider}`)
}

export function saveNotifyProvider(provider: 'email' | 'sms' | 'qq', values: Record<string, string>) {
  return request.post(`/api/rental/notify/provider/${provider}`, values)
}

export function createQQBindCode() {
  return request.post('/api/rental/notify/qq/bindcode')
}

// ---------- 收费项目 / 收款流水 / 统计分析 ----------

/** 收费项目定义：内置租金/水/电/燃气/卫生/管理 + 自定义（宽带费等） */
export interface FeeItem {
  id: number
  key: string
  name: string
  kind: 'fixed' | 'meter'
  unit: string
  /** fixed 类收费周期；meter 类恒月付 */
  cycle: PayCycle
  default_amount: number
  /**
   * 单据不单列该项目（只影响收费单据的打印/预览）。
   * 隐藏的项目若本期仍有金额，单据合并成一行「其他费用」，明细之和仍等于合计。
   */
  receipt_hidden: boolean
  /** 生效月份窗口（1-12，0=不限）——取暖费只在供暖季收（issue #5 需求 6）。
   *  两个都为 0 = 全年都收（老项目默认值，行为与改造前一致）。
   *  支持跨年：from=11、to=3 表示 11/12/1/2/3 月收。 */
  season_from_month: number
  season_to_month: number
  enabled: boolean
  sort: number
  built_in: boolean
}

export function getFeeItems() {
  return request.get('/api/rental/fee-items', { params: withLedger({}) })
}

/** 收费项目新增/编辑入参。季节窗口不传 = 保持原值（编辑时不动），传 0 = 显式改回不限。 */
export interface FeeItemInput {
  name: string
  cycle: PayCycle
  default_amount: number
  enabled?: boolean
  /** 单据不单列该项目；不传 = 保持原值（编辑时不动） */
  receipt_hidden?: boolean
  /** 1-12；0 或不传 = 不限 */
  season_from_month?: number
  season_to_month?: number
}

export function createFeeItem(data: FeeItemInput) {
  return request.post(withLedgerUrl('/api/rental/fee-items'), data)
}

export function updateFeeItem(id: number, data: FeeItemInput) {
  return request.put(withLedgerUrl(`/api/rental/fee-items/${id}`), data)
}

export function deleteFeeItem(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/fee-items/${id}`))
}

/** 账单费用明细行（详情接口返回，paid/arrears 服务端算好） */
export interface BillItemDetail {
  key: string
  name: string
  kind: 'fixed' | 'meter'
  amount: number
  detail: string
  cycle: PayCycle
  paid: number
  arrears: number
  /**
   * 单据上不单列该项目（台账级「单据显示」设置）。
   * 只在收费单据上生效：账单详情、统计与收款分摊照常显示与计算。
   */
  receipt_hidden?: boolean
}

/** 收款流水：某笔收款缴了哪些项目、各缴多少 */
export interface PaymentRecord {
  id: number
  bill_id: number
  paid_at: string
  amount: number
  note: string
  items: { key: string; name: string; amount: number }[]
  room_no: string
  tenant_name: string
  period: string
}

export function getPayments(params: { page?: number; pageSize?: number; period_from?: string; period_to?: string; keyword?: string; room_id?: number; community?: string }) {
  return request.get('/api/rental/payments', { params: withLedger(params) })
}

// ---------- 租客自助登记（链接收集 → 房东审核入库） ----------

/** 入住申请：租客通过登记链接提交，待房东审核转入租户 */
export interface TenantApplication {
  id: number
  room_id: number
  name: string
  phone: string
  id_card: string
  address: string
  remark: string
  status: 'pending' | 'approved' | 'rejected'
  review_note: string
  tenant_id: number
  reviewed_at: string | null
  created_at: string
  room_label: string
  photo_url: string
}

/** 登记链接出参：link 由服务端拼好——飞牛网关域上会换成应用直连地址
 *（网关路径要先登录 NAS，租客打不开），详见 server/rental/applications.go。 */
export interface RoomApplyLink {
  token: string
  /** 链接基址（末尾无 /）：来自「登记链接基址」配置，或按直连地址推导 */
  base: string
  /** 完整登记链接，直接发给租客 */
  link: string
  /** 这次请求是否从飞牛网关进来（是则 link 已换成直连地址） */
  via_gateway: boolean
  /** 基址是否来自「租客登记链接基址」配置 */
  configured: boolean
  /** 房东有没有开 AI 识别（后台开关 + 已配 Key 才算开） */
  ai_ocr: boolean
}

/** 生成/取回房源的登记 token（?regenerate=1 换新，旧链接失效） */
export function getRoomApplyLink(roomId: number, regenerate = false) {
  return request.post<{ code: number; message: string; data: RoomApplyLink }>(
    `/api/rental/rooms/${roomId}/apply-link${regenerate ? '?regenerate=1' : ''}`,
    {},
  )
}

/**
 * 房源登记链接的二维码图片（PNG）——打印贴房门/公告栏用（issue #5 需求 2）。
 *
 * 内容与 getRoomApplyLink 返回的 link **完全一致**（同一个 token、同一套基址推导），
 * 否则打印出去扫不开。返回 blob URL，用完记得 URL.revokeObjectURL。
 *
 * @param regenerate 换新 token（旧链接与旧二维码一起失效）
 */
export function fetchRoomApplyQRCodeBlob(roomId: number, regenerate = false): Promise<Blob> {
  const q = regenerate ? '?regenerate=1' : '?'
  return request
    .get(withLedgerUrl(`/api/rental/rooms/${roomId}/apply-qrcode${q}`), { responseType: 'blob' })
    .then((res) => res.data as Blob)
}

/** 二维码的直接下载地址（带 download=1，交给浏览器存文件）。 */
export function roomApplyQRCodeDownloadUrl(roomId: number): string {
  return withLedgerUrl(`/api/rental/rooms/${roomId}/apply-qrcode?download=1`)
}

/** 公开登记页的房源信息：ai_ocr 表示房东有没有配 AI 识别（没配就不显示识别按钮） */
export interface ApplyRoomInfo {
  community: string
  room_no: string
  building?: string
  unit?: string
  floor?: string
  ai_ocr: boolean
}

/** 公开页：按 token 取房源位置信息（无需登录） */
export function getApplyInfo(token: string) {
  return request.get<{ code: number; message: string; data: ApplyRoomInfo }>(`/api/rental/apply/${token}`)
}

/** 公开页：身份证照片 AI 识别（multipart: photo） */
export function ocrIdCard(token: string, form: FormData) {
  return request.post(`/api/rental/apply/${token}/ocr`, form)
}

/** 公开页：提交入住申请（multipart: name/phone/id_card/address/remark/photo） */
export function submitApply(token: string, form: FormData) {
  return request.post(`/api/rental/apply/${token}`, form)
}

/** 申请队列（房东侧，status=pending/approved/rejected，空=全部） */
export function getTenantApplications(status?: string) {
  return request.get('/api/rental/tenant-applications', { params: withLedger({ status: status || undefined }) })
}

export function rejectTenantApplication(id: number, note: string) {
  return request.post(`/api/rental/tenant-applications/${id}/reject`, { note })
}

/** 取申请身份证照片（登录态鉴权流），返回 objectURL 供 <img> 使用 */
export async function fetchApplicationPhoto(id: number): Promise<string> {
  const res = await request.get(`/api/rental/tenant-applications/${id}/photo`, { responseType: 'blob' })
  return URL.createObjectURL(res.data as Blob)
}

/** 统计分析：近 N 月应收/实收序列 + 项目收入构成 + 收缴率 */
export interface Analytics {
  series: { period: string; billed: number; received: number }[]
  items: { name: string; amount: number }[]
  summary: { billed: number; received: number; arrears: number; rate: number }
}

export function getAnalytics(months = 12) {
  return request.get('/api/rental/analytics', { params: withLedger({ months }) })
}

// ---------- 空置分析（GitHub issue #2：房屋空置对收益的影响） ----------

/**
 * 空置分析：当前空置清单 + 按月空置趋势 + 按房源排行。
 *
 * 口径说明：
 *   - 「当前空置」由在租租户（active=1）派生，实时准确；
 *   - 「历史空置」按租户入住/退租日期重建，**只是估算**：日期缺失的区间不计入，
 *     接口用 data_from 与 caveat 把估算边界如实返回给界面展示。
 */
export interface VacancySummary {
  /** 当前空置房间数 */
  vacant_rooms: number
  /** 在管房间数（全部房源） */
  rooms_total: number
  /** 当前空置率（%）= vacant_rooms / rooms_total */
  vacancy_rate: number
  /** 区间内空置房月数（按月累计） */
  vacant_room_months: number
  /** 区间内空置损失估算（元） */
  loss_estimate: number
  /** 有完整入住/退租日期的房间数（估算可信度参考） */
  rooms_dated: number
}

export interface VacancySeriesRow {
  period: string
  vacant_rooms: number
  vacancy_rate: number
  loss_estimate: number
}

export interface VacancyRoomRow {
  room_id: number
  room_label: string
  community: string
  vacant_months: number
  loss_estimate: number
  current_status: 'occupied' | 'vacant'
  last_tenant: string
  last_move_out: string
}

export interface VacancyAnalytics {
  summary: VacancySummary
  series: VacancySeriesRow[]
  rooms: { items: VacancyRoomRow[]; total: number; page: number; pageSize: number }
  /** 估算区间起点 YYYY-MM（房源建档月与所查区间的较晚者） */
  data_from: string
  /** 估算口径提示（有日期缺失时非空） */
  caveat: string
}

export function getVacancyAnalytics(params: { months?: number; page?: number; pageSize?: number; sort?: 'loss' | 'months' } = {}) {
  return request.get<{ code: number; data: VacancyAnalytics }>(
    '/api/rental/analytics/vacancy',
    { params: withLedger(params) },
  )
}

// ---------- 业务权限（数据共享后管理员按用户授权） ----------

/** 业务权限按模块勾选：模块 × 档位（查看/操作/管理），与后端 access.go 同词 */
export type ModuleKey = 'rooms' | 'tenants' | 'meters' | 'bills' | 'payments' | 'analytics'
export type ModulePerm = 'view' | 'edit' | 'full'
export type ModulePerms = Partial<Record<ModuleKey, ModulePerm>>

/** 共享计费默认值（系统级租房设置；开票/抄表弹窗"跟随全局默认"用） */
export interface BillingDefaultsPayload {
  water_mode: 'meter' | 'monthly'
  water_price: number
  water_monthly_fee: number
  pay_cycle: PayCycle
  pay_day: number
  elec_price: number
  gas_price: number
  remind_days: number
}

export function getBillingDefaults() {
  return request.get<{ code: number; data: BillingDefaultsPayload }>(
    '/api/rental/billing-defaults',
    { params: withLedger({}) },
  )
}

// ---------- 房间物品台账（issue #5 需求 5） ----------
//
// 物品挂在**房间**上（不绑租户）：房东要分清哪些是自己买的、哪些是租客自带的，
// 以及退租时核对缺失与损坏。一任租客搬走，清单照旧属于这个房间。
// 物品照片单独一张表（一类照片一张表，见 server/rental/room_items.go）。

export interface RoomItemPhoto {
  id: number
  ledger_id: number
  room_item_id: number
  file_name: string
  file_size: number
  mime_type: string
  created_at: string
}

export interface RoomItem {
  id: number
  ledger_id: number
  room_id: number
  name: string
  quantity: number
  /** 状况（完好/损坏/缺失…自由文本） */
  condition: string
  note: string
  /** true = 房东提供；false = 租客自带（用户最关心的区分） */
  builtin: boolean
  sort: number
  created_at: string
  updated_at: string
}

/** 列表项：附带该物品的照片。 */
export interface RoomItemView extends RoomItem {
  photos: RoomItemPhoto[]
  photo_count: number
}

export interface RoomItemInput {
  name: string
  quantity: number
  condition?: string
  note?: string
  builtin?: boolean
  sort?: number
}

/** 某房间的物品清单（含照片）。 */
export function getRoomItems(roomId: number) {
  return request.get<{ code: number; message?: string; data: { items: RoomItemView[]; total: number } }>(
    '/api/rental/room-items',
    { params: withLedger({ room_id: roomId, pageSize: 100 }) },
  )
}

export function createRoomItem(roomId: number, data: RoomItemInput) {
  return request.post<{ code: number; message?: string; data: RoomItemView }>(
    withLedgerUrl(`/api/rental/rooms/${roomId}/items`), data,
  )
}

export function updateRoomItem(id: number, data: RoomItemInput) {
  return request.put<{ code: number; message?: string; data: RoomItemView }>(
    withLedgerUrl(`/api/rental/room-items/${id}`), data,
  )
}

export function deleteRoomItem(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/room-items/${id}`))
}

/** 给某件物品传照片（multipart，字段名 files；20MB/张、单批 6 张、每物品 10 张）。 */
export function uploadRoomItemPhotos(itemId: number, files: File[]) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  return request.post<{ code: number; message?: string; data: { uploaded: number; items: RoomItemPhoto[] } }>(
    withLedgerUrl(`/api/rental/room-items/${itemId}/photos`), form,
    { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 120000 },
  )
}

export function deleteRoomItemPhoto(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/room-item-photos/${id}`))
}

/** 取物品照片内容（带归属校验，越权 404）。 */
export async function fetchRoomItemPhotoBlob(id: number): Promise<Blob> {
  const res = await request.get(withLedgerUrl(`/api/rental/room-item-photos/${id}/file`), { responseType: 'blob' })
  return res.data as Blob
}
