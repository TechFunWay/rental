import request from './request'
import { toast } from '../utils/toast'
import { currentLedgerQuery } from '../stores/ledger'

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
  notes: string
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
  /** monthly 月付 / quarterly 季付 */
  pay_cycle: 'monthly' | 'quarterly'
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
}

export interface Tenant {
  id: number
  room_id: number
  name: string
  phone: string
  /** 身份证号，选填 */
  id_card: string
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
  /** '' = 跟随全局默认；monthly 月付；quarterly 季付 */
  pay_cycle: '' | 'monthly' | 'quarterly'
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
  /** 开票时快照的缴费周期：monthly 月付 / quarterly 季付（覆盖 3 个月） */
  pay_cycle: 'monthly' | 'quarterly'
  /** 开票时快照的水费计费方式：meter 按吨（用量×单价）/ monthly 包月（固定金额） */
  water_mode: 'meter' | 'monthly'
  /** 开票时快照：不参与计费的项目 key（被排除项目费用恒为 0） */
  excluded_fees: string[]
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
  total_amount: number
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
  pay_cycle: 'monthly' | 'quarterly'
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

export function arrearsOf(b: Bill): number {
  const d = b.total_amount - b.paid_amount
  return d > 0.005 ? Math.round(d * 100) / 100 : 0
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

// ---------- 租户 ----------

export function getTenants(params: { page?: number; pageSize?: number; keyword?: string; active?: string; room_id?: number; community?: string; tag?: string }) {
  return request.get('/api/rental/tenants', { params: withLedger(params) })
}

export function createTenant(data: Partial<Tenant> & { application_id?: number }) {
  return request.post(withLedgerUrl('/api/rental/tenants'), data)
}

export function updateTenant(id: number, data: Partial<Tenant>) {
  return request.put(withLedgerUrl(`/api/rental/tenants/${id}`), data)
}

export function checkoutTenant(id: number, moveOutDate?: string) {
  return request.post(withLedgerUrl(`/api/rental/tenants/${id}/checkout`), { move_out_date: moveOutDate || '' })
}

export function deleteTenant(id: number) {
  return request.delete(withLedgerUrl(`/api/rental/tenants/${id}`))
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

export function getAttachments(tenantId: number) {
  return request.get(`/api/rental/tenants/${tenantId}/attachments`, { params: withLedger({}) })
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

// ---------- 账单 ----------

export function getBills(params: {
  page?: number; pageSize?: number; period?: string
  period_from?: string; period_to?: string
  bill_type?: 'monthly' | 'quarterly'
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

export function createBill(roomId: number, period: string, readings?: {
  water_now?: number; elec_now?: number; gas_now?: number
  water_mode?: 'meter' | 'monthly'
  water_price?: number; elec_price?: number; gas_price?: number; rent?: number
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
  cycle: 'monthly' | 'quarterly'
  default_amount: number
  enabled: boolean
  sort: number
  built_in: boolean
}

export function getFeeItems() {
  return request.get('/api/rental/fee-items', { params: withLedger({}) })
}

export function createFeeItem(data: { name: string; cycle: 'monthly' | 'quarterly'; default_amount: number; enabled?: boolean }) {
  return request.post(withLedgerUrl('/api/rental/fee-items'), data)
}

export function updateFeeItem(id: number, data: { name: string; cycle: 'monthly' | 'quarterly'; default_amount: number; enabled?: boolean }) {
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
  cycle: 'monthly' | 'quarterly'
  paid: number
  arrears: number
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

/** 生成/取回房源的登记 token（?regenerate=1 换新，旧链接失效） */
export function getRoomApplyLink(roomId: number, regenerate = false) {
  return request.post(`/api/rental/rooms/${roomId}/apply-link${regenerate ? '?regenerate=1' : ''}`, {})
}

/** 公开页：按 token 取房源位置信息（无需登录） */
export function getApplyInfo(token: string) {
  return request.get(`/api/rental/apply/${token}`)
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
  pay_cycle: 'monthly' | 'quarterly'
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
