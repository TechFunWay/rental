// api/ledger.ts — 台账（个人台账 / 共同台账）与成员、邀请、注册审核接口。
//
// 模型（0.5.2）：业务数据按**台账**归属，台账之间不互通。
//   - 个人台账：每个账号注册时自动创建，只有本人能用；不可删除、不可邀请。
//   - 共同台账：任何用户可自建并命名，创建者就是**管理者**；管理者邀请成员、
//     给成员按模块勾选权限、移除成员；被邀请人要自己接受，成员可主动退出。
//
// 权限档位与模块词表沿用 api/rental.ts 的 ModuleKey / ModulePerm / ModulePerms。
import request from './request'
import type { ModuleKey, ModulePerm, ModulePerms } from './rental'

// ---------- 出参类型 ----------

/** 台账类型：个人台账 / 共同台账。 */
export type LedgerKind = 'personal' | 'shared'

/** 我在某台账里的角色与状态。 */
export type LedgerRole = 'owner' | 'member'
export type LedgerMemberStatus = 'pending' | 'active' | 'rejected'

/** 一个可见台账（GET /rental/ledgers/mine 的行）。 */
export interface LedgerView {
  id: number
  name: string
  kind: LedgerKind
  owner_user_id: number
  owner_name: string
  role: LedgerRole
  status: LedgerMemberStatus
  /** 我在这个台账里的模块勾选（管理者恒为全部 full） */
  perms: ModulePerms
  member_count: number
}

/** 台账成员行。 */
export interface LedgerMemberView {
  user_id: number
  username: string
  role: LedgerRole
  status: LedgerMemberStatus
  perms: ModulePerms
  is_self: boolean
  joined_at: string
  status_label: string
}

/** 邀请时按用户名找人的结果行。 */
export interface UserBrief {
  id: number
  username: string
}

/** 待审核用户行。 */
export interface PendingUser {
  id: number
  username: string
  created_at: string
  /** 注册时填写的手机号（只作联系/认人用途，2026-10-11 起在后台可见）。 */
  phone?: string
}

/** 我自己的审核状态（GET /rental/users/me/approval）。 */
export interface MyApproval {
  status: number
  pending: boolean
  message: string
  /**
   * 账号角色（v0.6.10）。待关联的租户同样是 status === 2，前端在
   * /auth/check 认不下待审核会话时靠这个接口恢复登录态——**必须**用服务端
   * 给的角色，否则租户会被恢复成 user、被送去房东的等待页。
   */
  role?: string
}

// ---------- 我的台账 ----------

/** 我可见的台账（个人台账排第一，其次是我参与的共同台账，含待接受邀请）。 */
export function getMyLedgers() {
  return request.get<{ code: number; message?: string; data: LedgerView[] }>('/api/rental/ledgers/mine')
}

/**
 * 新建共同台账。`usernames` 用逗号/空白分隔，可在建台账时直接发出邀请
 * （被邀请人接受后才算成员）；不认识/被禁用/还在待审核的名字会被跳过。
 */
export function createLedger(name: string, usernames = '') {
  return request.post<{ code: number; message?: string; data: { id: number; name: string; invited: number; skipped: string[] } }>(
    '/api/rental/ledgers',
    { name, usernames },
  )
}

/** 改名（仅管理者；个人台账也能改自己的显示名）。 */
export function updateLedger(id: number, name: string) {
  return request.put(`/api/rental/ledgers/${id}`, { name })
}

/** 删除共同台账（仅管理者；个人台账不可删、有数据不可删）。 */
export function deleteLedger(id: number) {
  return request.delete(`/api/rental/ledgers/${id}`)
}

// ---------- 成员与邀请 ----------

/** 成员列表（正式成员可见；管理者还能看到待接受/已拒绝）。 */
export function getLedgerMembers(id: number) {
  return request.get<{ code: number; message?: string; data: { members: LedgerMemberView[]; is_owner: boolean } }>(
    `/api/rental/ledgers/${id}/members`,
  )
}

/** 邀请成员（仅管理者；按已注册用户名邀请）。 */
export function inviteMember(id: number, username: string) {
  return request.post(`/api/rental/ledgers/${id}/members`, { username })
}

/** 设置某成员的模块权限（仅管理者）。 */
export function setMemberPerms(id: number, userId: number, perms: ModulePerms) {
  return request.put(`/api/rental/ledgers/${id}/members/${userId}`, { perms })
}

/**
 * 移除成员（仅管理者）/ 主动退出：`userId` 传自己的 ID 就是退出台账。
 * 管理者不能移除自己（后端会拒绝），个人台账不能退出。
 */
export function removeMember(id: number, userId: number) {
  return request.delete(`/api/rental/ledgers/${id}/members/${userId}`)
}

/** 被邀请人接受或拒绝邀请。 */
export function respondInvitation(id: number, accept: boolean) {
  return request.post(`/api/rental/ledgers/${id}/invitations/respond`, { accept })
}

/** 邀请时按用户名找人。服务端要求关键字至少 2 个字（防账号枚举），
 *  只返回状态正常的前 10 条。当前界面走「直接填用户名邀请」，暂未调用本函数。 */
export function searchUsers(q = '') {
  return request.get<{ code: number; message?: string; data: UserBrief[] }>('/api/rental/users/search', {
    params: { q },
  })
}

// ---------- 注册审核（默认仅管理员；me/approval 任何登录用户） ----------

export function getPendingUsers() {
  return request.get<{ code: number; message?: string; data: PendingUser[] }>('/api/rental/users/pending')
}

/** 审批通过：账号可用（个人台账在注册时就建好了）。 */
export function approveUser(userId: number) {
  return request.post(`/api/rental/users/${userId}/approve`)
}

/** 驳回注册：账号置为已禁用（保留用户行）。 */
export function rejectUser(userId: number) {
  return request.post(`/api/rental/users/${userId}/reject`)
}

/** 自读审核状态（待审核用户也能调，属白名单接口）。 */
export function getMyApproval() {
  return request.get<{ code: number; message?: string; data: MyApproval }>('/api/rental/users/me/approval')
}

// ---------- 按台账的租房设置 ----------

/**
 * 可被台账覆盖的 11 个租房设置键（顺序与后端 rentalConfigKeys 一致）。
 * 未覆盖的键跟随系统级默认值。
 */
export type LedgerSettingKey =
  | 'rental_water_price'
  | 'rental_water_mode'
  | 'rental_water_monthly_fee'
  | 'rental_pay_cycle'
  | 'rental_pay_day'
  | 'rental_elec_price'
  | 'rental_gas_price'
  | 'rental_remind_days'
  | 'rental_property_name'
  | 'rental_contact'
  | 'rental_receipt_note'

/** 一次可提交的设置集合（键受限于 LedgerSettingKey）。 */
export type LedgerSettingsPayload = Partial<Record<LedgerSettingKey, string>>

/** GET/PUT/DELETE /rental/ledgers/:id/settings 的 data 形状。 */
export interface LedgerSettings {
  ledger_id: number
  /** 生效值（台账覆盖 → 系统级 → 代码默认），永远是字符串 */
  effective: Record<string, string>
  /** 只包含本台账显式覆盖过的键（不在其中的表示「跟随系统」） */
  overrides: Record<string, string>
}

export function getLedgerSettings(ledgerId: number) {
  return request.get<{ code: number; message?: string; data: LedgerSettings }>(
    `/api/rental/ledgers/${ledgerId}/settings`,
  )
}

export function updateLedgerSettings(ledgerId: number, settings: LedgerSettingsPayload) {
  return request.put<{ code: number; message?: string; data: LedgerSettings }>(
    `/api/rental/ledgers/${ledgerId}/settings`,
    { settings },
  )
}

/** 清空本台账的全部覆盖值 → 恢复跟随系统级。 */
export function resetLedgerSettings(ledgerId: number) {
  return request.delete<{ code: number; message?: string; data: LedgerSettings }>(
    `/api/rental/ledgers/${ledgerId}/settings`,
  )
}

// ---------- 展示用小工具 ----------

/** 台账类型的展示名。 */
export function ledgerKindLabel(kind: LedgerKind | string): string {
  return kind === 'personal' ? '个人台账' : '共同台账'
}

/** 台账在切换器里的显示名：个人台账固定叫「我的台账」。 */
export function ledgerDisplayName(ledger: { id: number; name: string; kind: LedgerKind | string }): string {
  return ledger.kind === 'personal' ? '我的台账' : ledger.name
}

/** 成员状态的展示名（后端已给 status_label，这里做兜底）。 */
export const MEMBER_STATUS_LABEL: Record<string, string> = {
  active: '已加入',
  pending: '等待接受',
  rejected: '已拒绝',
}

export { MODULE_LABEL, ALL_MODULES } from '../utils/perm'
export type { ModuleKey, ModulePerm, ModulePerms }
