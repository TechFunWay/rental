import request from './request'
import { toast } from '../utils/toast'

// ---------- 类型 ----------

export interface Room {
  id: number
  community: string
  room_no: string
  building: string
  unit: string
  floor: string
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

export function getRooms(params: { page?: number; pageSize?: number; keyword?: string; status?: string }) {
  return request.get('/api/rental/rooms', { params })
}

export function createRoom(data: Partial<Room>) {
  return request.post('/api/rental/rooms', data)
}

export function updateRoom(id: number, data: Partial<Room>) {
  return request.put(`/api/rental/rooms/${id}`, data)
}

export function deleteRoom(id: number) {
  return request.delete(`/api/rental/rooms/${id}`)
}

// ---------- 租户 ----------

export function getTenants(params: { page?: number; pageSize?: number; keyword?: string; active?: string; room_id?: number }) {
  return request.get('/api/rental/tenants', { params })
}

export function createTenant(data: Partial<Tenant>) {
  return request.post('/api/rental/tenants', data)
}

export function updateTenant(id: number, data: Partial<Tenant>) {
  return request.put(`/api/rental/tenants/${id}`, data)
}

export function checkoutTenant(id: number, moveOutDate?: string) {
  return request.post(`/api/rental/tenants/${id}/checkout`, { move_out_date: moveOutDate || '' })
}

export function deleteTenant(id: number) {
  return request.delete(`/api/rental/tenants/${id}`)
}

// ---------- 合同存档 ----------

export function getContracts(tenantId: number) {
  return request.get(`/api/rental/tenants/${tenantId}/contracts`)
}

export function uploadContracts(tenantId: number, files: File[]) {
  const form = new FormData()
  for (const f of files) form.append('files', f)
  return request.post(`/api/rental/tenants/${tenantId}/contracts`, form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function deleteContract(id: number) {
  return request.delete(`/api/rental/contracts/${id}`)
}

// 合同文件必须带登录态读取（不走公开的 /uploads），统一用 blob 中转：
// fetchContractBlob 供弹窗内预览（图片直接渲染 / PDF 内嵌）与下载使用。
export async function fetchContractBlob(id: number): Promise<Blob> {
  const res = await request.get(`/api/rental/contracts/${id}/file`, { responseType: 'blob' })
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

// ---------- 抄表台账 ----------

export function getMeterRecords(params: { page?: number; pageSize?: number; period?: string; room_id?: number }) {
  return request.get('/api/rental/meter-records', { params })
}

export function upsertMeterRecord(data: {
  room_id: number
  period: string
  water: number
  elec: number
  gas: number
  note?: string
}) {
  return request.post('/api/rental/meter-records', data)
}

export function deleteMeterRecord(id: number) {
  return request.delete(`/api/rental/meter-records/${id}`)
}

// ---------- 账单 ----------

export function getBills(params: {
  page?: number; pageSize?: number; period?: string
  period_from?: string; period_to?: string
  bill_type?: 'monthly' | 'quarterly'
  status?: string; keyword?: string
}) {
  return request.get('/api/rental/bills', { params })
}

export function getBillPeriods() {
  return request.get('/api/rental/bills/periods')
}

export function getBillDetail(id: number) {
  return request.get(`/api/rental/bills/${id}`)
}

export function createBill(roomId: number, period: string, readings?: {
  water_now?: number; elec_now?: number; gas_now?: number
  water_mode?: 'meter' | 'monthly'
  water_price?: number; elec_price?: number; gas_price?: number; rent?: number
}) {
  return request.post('/api/rental/bills', { room_id: roomId, period, ...readings })
}

export function generateBills(period: string) {
  return request.post('/api/rental/bills/generate', { period })
}

export function updateBill(id: number, data: Partial<Bill>) {
  return request.put(`/api/rental/bills/${id}`, data)
}

export function payBill(
  id: number,
  amount: number,
  note = '',
  items?: { key: string; name: string; amount: number }[],
) {
  return request.post(`/api/rental/bills/${id}/pay`, { amount, note, items })
}

export function deleteBill(id: number) {
  return request.delete(`/api/rental/bills/${id}`)
}

// ---------- 导入导出 ----------

export function downloadTemplate() {
  download('/api/rental/template')
}

export function exportBills(period: string) {
  download(`/api/rental/export${period ? `?period=${encodeURIComponent(period)}` : ''}`)
}

export function exportRooms() {
  download('/api/rental/rooms/export')
}

export function importBills(file: File) {
  const form = new FormData()
  form.append('file', file)
  return request.post('/api/rental/import', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

// ---------- 统计 ----------

export function getStats(period?: string) {
  return request.get('/api/rental/stats', { params: period ? { period } : {} })
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
  return request.get('/api/rental/fee-items')
}

export function createFeeItem(data: { name: string; cycle: 'monthly' | 'quarterly'; default_amount: number; enabled?: boolean }) {
  return request.post('/api/rental/fee-items', data)
}

export function updateFeeItem(id: number, data: { name: string; cycle: 'monthly' | 'quarterly'; default_amount: number; enabled?: boolean }) {
  return request.put(`/api/rental/fee-items/${id}`, data)
}

export function deleteFeeItem(id: number) {
  return request.delete(`/api/rental/fee-items/${id}`)
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

export function getPayments(params: { page?: number; pageSize?: number; period_from?: string; period_to?: string; keyword?: string }) {
  return request.get('/api/rental/payments', { params })
}

/** 统计分析：近 N 月应收/实收序列 + 项目收入构成 + 收缴率 */
export interface Analytics {
  series: { period: string; billed: number; received: number }[]
  items: { name: string; amount: number }[]
  summary: { billed: number; received: number; arrears: number; rate: number }
}

export function getAnalytics(months = 12) {
  return request.get('/api/rental/analytics', { params: { months } })
}
