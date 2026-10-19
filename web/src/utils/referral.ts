// referral.ts — 介绍费的前端展示口径（与服务端 referral.go 同一套规则）。
//
// 房东侧表单要按平台策略显示与校验，所以策略/最低金额走公开配置（非敏感项，
// 服务端已标 Public），前端只做即时提示，**落库判定以服务端为准**。
import type { Room } from '../api/rental'

export type ReferralPolicy = 'off' | 'min' | 'free'
export type ReferralMode = '' | 'fixed' | 'percent'

export interface ReferralSettings {
  policy: ReferralPolicy
  /** 最低介绍费（元），policy === 'min' 时有意义 */
  min: number
}

/** 从公开配置里解析策略（缺失/非法按 free：功能可用优先，与服务端一致）。 */
export function parseReferralSettings(configs: Record<string, string> | undefined): ReferralSettings {
  const raw = (configs?.rental_referral_policy || '').trim()
  const policy: ReferralPolicy = raw === 'off' || raw === 'min' ? raw : 'free'
  const min = Number(configs?.rental_referral_min ?? 200)
  return { policy, min: Number.isFinite(min) && min >= 0 ? min : 200 }
}

/** 折算金额（元）：fixed 原样；percent 按月租金折算。 */
export function referralAmount(mode: ReferralMode, value: number, monthlyRent: number): number {
  if (mode === 'fixed') return Math.max(0, value || 0)
  if (mode === 'percent') return monthlyRent > 0 ? Math.round(((value || 0) * monthlyRent) / 100 * 100) / 100 : 0
  return 0
}

/** 一行展示文案：'' 表示不提供（调用方据此决定显不显示）。 */
export function referralText(mode: string, value: number, monthlyRent: number): string {
  if (mode === 'fixed' && value > 0) return `介绍费 ${trim(value)} 元`
  if (mode === 'percent' && value > 0) {
    const amount = referralAmount('percent', value, monthlyRent)
    return amount > 0 ? `介绍费 月租 ${trim(value)}%（约 ${trim(amount)} 元）` : `介绍费 月租 ${trim(value)}%`
  }
  return ''
}

function trim(v: number): string {
  return String(Math.round(v * 100) / 100)
}

/** 房源是否挂着介绍费（列表徽标/详情用）。 */
export function roomReferralText(room: Pick<Room, 'referral_mode' | 'referral_value' | 'default_rent'>): string {
  return referralText(room.referral_mode || '', room.referral_value || 0, room.default_rent || 0)
}
