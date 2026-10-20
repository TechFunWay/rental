// api/renew.ts — 租约续租接口（2026-10-20 粉丝需求）。
//
// 为什么不并进 api/rental.ts：续租是独立模块（后端 server/rental/renew.go），
// 单独一个文件能让路由/类型与其它模块解耦，也避免多个模块同时改同一个大文件。
// axios 实例、台账参数拼接与 api/rental.ts 共用同一套（withLedgerUrl 会把当前
// 台账 ledger_id 带上，否则写入会落到个人台账）。
import request from './request'
import { currentLedgerQuery } from '../stores/ledger'

// withLedgerUrl 在 api/rental.ts 里是模块私有的（未导出），这里按同样口径补一份：
// 写操作必须带当前台账 ledger_id，否则后端会落到个人台账（见 api/rental.ts 顶部说明）。
function withLedgerUrl(url: string): string {
  const ledgerId = currentLedgerQuery()
  if (ledgerId === null) return url
  const sep = url.includes('?') ? '&' : '?'
  return `${url}${sep}ledger_id=${ledgerId}`
}

/** 一次续租的留痕（后端 LeaseRenewal）。金额与到期日都是续租当时的快照。 */
export interface LeaseRenewal {
  id: number
  ledger_id: number
  tenant_id: number
  room_id: number
  /** 续租前的到期日（空=此前未约定） */
  from_end_date: string
  /** 续租后的到期日 */
  to_end_date: string
  /** 本次续租时长（月） */
  months: number
  /** 续租前月租金快照（元） */
  rent_before: number
  /** 续租后月租金快照（元） */
  rent_after: number
  note: string
  created_at: string
}

export interface RenewInput {
  /** 续租月数（1~120），与 to_end_date 二选一；都传时 to_end_date 优先 */
  months?: number
  /** 指定新的到期日 YYYY-MM-DD（必须晚于当前到期日或今天） */
  to_end_date?: string
  /** 新租金（元/月）：不传 = 不变；传 0 表示免租过渡 */
  rent?: number
  note?: string
}

/** 续租：更新租户到期日（可选调租金）并写一条留痕。 */
export function renewTenant(tenantId: number, data: RenewInput) {
  return request.post<{ code: number; message?: string; data: { renewal: LeaseRenewal; tenant: Record<string, unknown> } }>(
    withLedgerUrl(`/api/rental/tenants/${tenantId}/renew`),
    data,
  )
}

/** 某租户的续租历史（倒序，最多 200 条）。 */
export function getTenantRenewals(tenantId: number) {
  return request.get<{ code: number; message?: string; data: { items: LeaseRenewal[] } }>(
    withLedgerUrl(`/api/rental/tenants/${tenantId}/renewals`),
  )
}
