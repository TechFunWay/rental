// api/tenant.ts — 租户门户与租户账号关联的接口客户端（v0.6.10）。
//
// 两条链路分开：
//   - 租户侧（portalXxx）：只读自己的资料/账单/缴费/合同 + 提交资料变更申请。
//     这些接口**不带 ledger_id**——租户不是任何台账的成员，数据范围由服务端按
//     「当前账号绑定的那条租户档案」推导（见后端 tenant_portal.go 的 portalTenant）。
//   - 房东侧（tenantAccountXxx / changeRequestXxx）：关联登录账号、审核变更申请。
//     这些走业务接口，需要带台账（复用 api/rental.ts 的 withLedger 口径）。
import request from './request'
import { currentLedgerQuery } from '../stores/ledger'
import type { Bill } from './rental'

// ---------- 类型 ----------

/** 门户基础状态（未关联时唯一可读的接口）。 */
export interface PortalStatus {
  role: string
  bound: boolean
  status: number
  username: string
  tenant_name?: string
  ledger_name?: string
  room_label?: string
}

/** 我的登记资料（只读视图；证件号服务端已脱敏）。 */
export interface PortalProfile {
  name: string
  phone: string
  id_card: string
  address: string
  emergency_contact: string
  emergency_phone: string
  room_label: string
  community: string
  move_in_date: string
  lease_end_date: string
  move_out_date: string
  active: boolean
  deposit: number
  billing_summary: string
  pending_change: boolean
}

/** 我的资料附件（元数据；文件流走 portalAttachmentFileUrl）。 */
export interface PortalAttachment {
  id: number
  file_name: string
  file_size: number
  mime_type: string
  label: string
  created_at: string
}

export interface PortalProfilePayload {
  profile: PortalProfile
  attachments: PortalAttachment[]
}

/**
 * 我能看到的账单。
 *
 * 直接复用房东侧的 Bill 类型（同一张表、同一套字段）：RentalReceipt 这类按
 * Bill 建模的组件因此能被门户直接复用，不必维护第二份结构，也就不会出现
 * 「房东打印的单据与租户看到的对不上」。
 */
export interface PortalBill extends Bill {}

export interface PortalBillItem {
  key: string
  name: string
  kind: string
  amount: number
  detail: string
  cycle: string
  paid: number
  arrears: number
}

export interface PortalPaymentShare {
  key: string
  name: string
  amount: number
}

export interface PortalPayment {
  id: number
  bill_id: number
  paid_at: string
  amount: number
  note: string
}

export interface PortalReceipt {
  bill: PortalBill
  items: PortalBillItem[]
  payments: Array<{ id: number; paid_at: string; amount: number; note: string; items: PortalPaymentShare[] }>
  property: { name: string; contact: string; note: string }
}

export interface PortalContract {
  id: number
  file_name: string
  file_size: number
  mime_type: string
  created_at: string
}

/** 我的资料变更申请。 */
export interface ChangeRequest {
  id: number
  ledger_id: number
  tenant_id: number
  phone: string
  id_card: string
  address: string
  emergency_contact: string
  emergency_phone: string
  note: string
  status: 'pending' | 'approved' | 'rejected'
  review_note: string
  reviewed_at: string | null
  created_at: string
}

/** 房东侧审核面板用的申请视图：附正式档案当前值与照片预览地址。 */
export interface ChangeRequestReview extends ChangeRequest {
  tenant_name: string
  room_label: string
  applicant: string
  photo_urls: string[]
  current_phone: string
  current_id_card: string
  current_address: string
  current_emergency_contact: string
  current_emergency_phone: string
}

/** 租户档案的登录账号关联状态。 */
export interface TenantAccount {
  tenant_id: number
  tenant_name: string
  bound: boolean
  user_id: number
  username: string
  phone: string
  account_status: number
}

export interface TenantAccountCandidate {
  id: number
  username: string
  // 刻意没有 phone：接口跨台账（租户账号在被关联前不属于任何台账），
  // 返回手机号等于给人「捞号」的花名册。房东靠租户告知的用户名完成关联。
}

// ---------- 租户侧：门户 ----------

/** 门户状态：未关联时前端据此渲染「等待房东关联」页。 */
export function getPortalStatus() {
  return request.get('/api/rental/portal/status')
}

export function getPortalProfile() {
  return request.get('/api/rental/portal/profile')
}

export function getPortalBills(params: { page?: number; pageSize?: number; period?: string } = {}) {
  return request.get('/api/rental/portal/bills', { params })
}

export function getPortalBillDetail(id: number) {
  return request.get(`/api/rental/portal/bills/${id}`)
}

export function getPortalBillReceipt(id: number) {
  return request.get(`/api/rental/portal/bills/${id}/receipt`)
}

export function getPortalPayments(params: { page?: number; pageSize?: number } = {}) {
  return request.get('/api/rental/portal/payments', { params })
}

export function getPortalContracts() {
  return request.get('/api/rental/portal/contracts')
}

// 文件流统一走 axios blob 中转（与房东侧合同/附件同一口径）：
// 这些接口要求登录态，而 <img src> / <iframe src> / <a href> 不会带上
// Authorization 头，直连会拿到 401。所以一律先取 blob 再 createObjectURL。

/** 取我的合同文件 blob（预览用；调用方负责 revokeObjectURL）。 */
export async function fetchPortalContractBlob(id: number): Promise<Blob> {
  const res = await request.get(`/api/rental/portal/contracts/${id}/file`, { responseType: 'blob' })
  return res.data as Blob
}

/** 下载我的合同（浏览器保存）。 */
export async function downloadPortalContract(id: number, fileName: string): Promise<void> {
  const res = await request.get(`/api/rental/portal/contracts/${id}/file`, {
    params: { download: 1 },
    responseType: 'blob',
  })
  saveBlob(res.data as Blob, fileName)
}

/** 取我的资料附件 blob（预览用）。 */
export async function fetchPortalAttachmentBlob(id: number): Promise<Blob> {
  const res = await request.get(`/api/rental/portal/attachments/${id}/file`, { responseType: 'blob' })
  return res.data as Blob
}

/** 取我申请里第 index 张照片的 blob（序号从 0 起）。 */
export async function fetchPortalChangePhotoBlob(id: number, index: number): Promise<Blob> {
  const res = await request.get(`/api/rental/portal/change-requests/${id}/photo/${index}`, { responseType: 'blob' })
  return res.data as Blob
}

/** 取房东侧申请照片 blob（按台账鉴权，需带 ledger_id）。 */
export async function fetchChangeRequestPhotoBlob(id: number, index: number): Promise<Blob> {
  const res = await request.get(`/api/rental/tenant-change-requests/${id}/photo/${index}`, {
    params: withLedger({}),
    responseType: 'blob',
  })
  return res.data as Blob
}

/** 触发浏览器保存一个 blob（下载类接口的公共收尾）。 */
export function saveBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fileName || 'download'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

// ---------- 租户侧：资料变更申请 ----------

export function getMyChangeRequests() {
  return request.get('/api/rental/portal/change-requests')
}

/**
 * 提交资料变更申请。只允许改联系与证件类字段（后端白名单），
 * 照片 0–3 张；留空的字段表示「本次不改」，不会清空正式数据。
 */
export function submitChangeRequest(fields: {
  phone?: string
  id_card?: string
  address?: string
  emergency_contact?: string
  emergency_phone?: string
  note?: string
}, photos: File[] = []) {
  const form = new FormData()
  for (const [k, v] of Object.entries(fields)) {
    if (v) form.append(k, v)
  }
  photos.forEach((f) => form.append('photos', f))
  return request.post('/api/rental/portal/change-requests', form, {
    headers: { 'Content-Type': 'multipart/form-data' },
  })
}

export function withdrawChangeRequest(id: number) {
  return request.delete(`/api/rental/portal/change-requests/${id}`)
}

// ---------- 房东侧：关联登录账号 ----------

export function getTenantAccount(tenantId: number) {
  return request.get(`/api/rental/tenants/${tenantId}/account`, { params: withLedger({}) })
}

/** 候选账号：role=tenant 且未被任何档案绑定；关键字至少 2 字（账号枚举防护）。 */
export function searchTenantAccountCandidates(keyword: string) {
  return request.get('/api/rental/tenant-accounts/candidates', {
    params: withLedger({ keyword }),
  })
}

/** 关联登录账号（关联即审核通过，账号可进门户）。 */
export function bindTenantAccount(tenantId: number, userId: number) {
  return request.post(`/api/rental/tenants/${tenantId}/account`, { user_id: userId }, {
    params: withLedger({}),
  })
}

/** 解除关联：账号退回「等待关联」态，可再关联别的档案。 */
export function unbindTenantAccount(tenantId: number) {
  return request.delete(`/api/rental/tenants/${tenantId}/account`, {
    params: withLedger({}),
  })
}

// ---------- 房东侧：资料变更审核 ----------

export function getChangeRequests(status = 'pending') {
  return request.get('/api/rental/tenant-change-requests', { params: withLedger({ status }) })
}

export function getChangeRequestPendingCount() {
  return request.get('/api/rental/tenant-change-requests/pending-count', { params: withLedger({}) })
}

export function approveChangeRequest(id: number) {
  return request.post(`/api/rental/tenant-change-requests/${id}/approve`, {}, { params: withLedger({}) })
}

export function rejectChangeRequest(id: number, note: string) {
  return request.post(`/api/rental/tenant-change-requests/${id}/reject`, { note }, { params: withLedger({}) })
}

// ---------- 内部 ----------

/** 与 api/rental.ts 的 withLedger 同口径：给房东侧请求补当前台账。 */
function withLedger<T extends Record<string, unknown>>(params: T): T & { ledger_id?: number } {
  const ledgerId = currentLedgerQuery()
  if (ledgerId === null) return params
  return { ...params, ledger_id: ledgerId }
}

// ---------- 展示辅助 ----------

export const CHANGE_STATUS_LABEL: Record<string, string> = {
  pending: '待审核',
  approved: '已通过',
  rejected: '已驳回',
}

/** 变更申请状态的徽标样式（手机端卡片与桌面表格共用）。 */
export function changeStatusClass(status: string): string {
  switch (status) {
    case 'pending':
      return 'bg-amber-500/10 text-amber-600 dark:text-amber-300'
    case 'approved':
      return 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-300'
    default:
      return 'bg-muted text-muted-foreground'
  }
}
