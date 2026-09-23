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
  water_price: number
  elec_price: number
  gas_price: number
  initial_water: number
  initial_elec: number
  initial_gas: number
  notes: string
}

export interface TenantBrief {
  id: number
  name: string
  phone: string
}

export interface RoomView extends Room {
  current_tenants: TenantBrief[]
  has_bills: boolean
}

export interface Tenant {
  id: number
  room_id: number
  name: string
  phone: string
  move_in_date: string
  lease_end_date: string
  move_out_date: string
  deposit: number
  active: boolean
  notes: string
  room_no?: string
  room_label?: string
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
  water_price: number
  elec_price: number
  gas_price: number
  water_fee: number
  elec_fee: number
  gas_fee: number
  sanitation_fee: number
  management_fee: number
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

// ---------- 账单 ----------

export function getBills(params: { page?: number; pageSize?: number; period?: string; status?: string; keyword?: string }) {
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

export function payBill(id: number, amount: number, note = '') {
  return request.post(`/api/rental/bills/${id}/pay`, { amount, note })
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
