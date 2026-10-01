// utils/billing.ts — 计费与缴费设置的展示口径（v0.2.3 起按租户设置）。
//
// 口径：租户设置（水费计费方式/金额、缴费周期、缴费日、提前提醒天数）→
// 偏好设置里的全局默认。租户用 ''/0/-1 表示"跟随全局默认"，服务端按同一
// 顺序解析（见 server/rental/billing.go）；这里只负责表单默认值、列表文案
// 与单位换算，保证各页面口径一致。

import { getUserConfigMeta } from '../api/config'
import type { RoomBilling } from '../api/rental'
import { cycleLabel, normalizeCycle, type PayCycle } from './cycle'

export type WaterMode = 'meter' | 'monthly'

/** 水费计费方式与缴费周期的中文名 */
export function waterModeLabel(mode: WaterMode | string | undefined | null): string {
  return normalizeWaterMode(mode) === 'monthly' ? '包月' : '按吨'
}

/** 规范化水费计费方式：''/未知一律按"按吨" */
export function normalizeWaterMode(mode: WaterMode | string | undefined | null): WaterMode {
  return mode === 'monthly' ? 'monthly' : 'meter'
}

/** 全局默认（偏好设置 → 租房设置） */
export interface BillingDefaults {
  /** 默认水费计费方式 */
  waterMode: WaterMode
  /** 默认按吨单价（元/吨） */
  waterMeterPrice: number
  /** 默认包月金额（元/月） */
  waterMonthlyFee: number
  /** 默认电费单价（元/度） */
  elecPrice: number
  /** 默认燃气单价（元/方） */
  gasPrice: number
  /** 默认缴费周期 */
  payCycle: PayCycle
  /** 默认缴费日（1-28，0=不提醒） */
  payDay: number
  /** 默认提前提醒天数 */
  remindDays: number
}

export const defaultBilling: BillingDefaults = {
  waterMode: 'meter', waterMeterPrice: 5, waterMonthlyFee: 0,
  elecPrice: 1.2, gasPrice: 3.5,
  payCycle: 'monthly', payDay: 0, remindDays: 3,
}

function toNumber(v: string, fallback: number): number {
  const n = Number(v)
  return Number.isFinite(n) && n >= 0 ? n : fallback
}

// fetchBillingDefaults 读取用户偏好中的全局计费与缴费默认；失败时退回内置默认，
// 不阻塞页面。租户表单用它做"跟随全局默认"的展示值与新建时的预填值。
export async function fetchBillingDefaults(): Promise<BillingDefaults> {
  try {
    const res = await getUserConfigMeta()
    const list = (res.data?.data ?? []) as { key: string; value: string }[]
    const val = (key: string) => list.find((c) => c.key === key)?.value ?? ''
    const payDay = Number(val('rental_pay_day'))
    const remindDays = Number(val('rental_remind_days'))
    return {
      waterMode: normalizeWaterMode(val('rental_water_mode')),
      waterMeterPrice: toNumber(val('rental_water_price'), defaultBilling.waterMeterPrice),
      waterMonthlyFee: toNumber(val('rental_water_monthly_fee'), defaultBilling.waterMonthlyFee),
      elecPrice: toNumber(val('rental_elec_price'), defaultBilling.elecPrice),
      gasPrice: toNumber(val('rental_gas_price'), defaultBilling.gasPrice),
      payCycle: normalizeCycle(val('rental_pay_cycle')),
      payDay: Number.isFinite(payDay) && payDay >= 0 && payDay <= 28 ? payDay : defaultBilling.payDay,
      remindDays: Number.isFinite(remindDays) && remindDays >= 0 ? remindDays : defaultBilling.remindDays,
    }
  } catch {
    return { ...defaultBilling }
  }
}

/** 数字展示：整数省略小数位，否则最多两位 */
export function numText(v: number | undefined | null): string {
  const n = Number(v || 0)
  // 固定两位小数（带千分位），全应用金额与读数统一口径
  return n.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
}

/** 水费金额单位：按吨 元/吨，包月 元/月 */
export function waterUnit(mode: WaterMode | string | undefined | null): string {
  return normalizeWaterMode(mode) === 'monthly' ? '元/月' : '元/吨'
}

/** 水费金额文案（带单位）：如 "5 元/吨"、"40 元/月" */
export function waterAmountText(mode: WaterMode | string | undefined | null, amount: number): string {
  return `${numText(amount)} ${waterUnit(mode)}`
}

/** 水表/电表/燃气表的读数单位 */
export type MeterKind = 'water' | 'elec' | 'gas'

export function meterUnit(kind: MeterKind): string {
  switch (kind) {
    case 'water':
      return '吨'
    case 'elec':
      return '度'
    default:
      return '方'
  }
}

/** 计价单位（带"元/"前缀）：水 元/吨、电 元/度、燃气 元/方 */
export function priceUnit(kind: MeterKind): string {
  return `元/${meterUnit(kind)}`
}

/** 缴费日文案：1-28 → "每月N号"，0 → "未设缴费日" */
export function payDayText(payDay: number | undefined | null): string {
  const d = Number(payDay || 0)
  return d >= 1 && d <= 28 ? `每月${d}号` : '未设缴费日'
}

// ---------- 租户级解析 ----------

/** 计费与缴费字段（租户实体 / 房间生效视图通用） */
export interface BillingLike {
  water_mode?: string
  water_price?: number
  water_monthly_fee?: number
  pay_cycle?: string
  pay_day?: number
  remind_days?: number
}

/** 解析后的生效设置（每项都带"是否跟随全局"标记，便于表单提示） */
export interface ResolvedBilling {
  waterMode: WaterMode
  waterAmount: number
  /** 水费方式或金额有一项跟随全局默认 */
  waterFollowsGlobal: boolean
  payCycle: PayCycle
  payCycleFollowsGlobal: boolean
  payDay: number
  payDayFollowsGlobal: boolean
  remindDays: number
  remindDaysFollowsGlobal: boolean
}

/**
 * resolveTenantBilling 解析租户的生效设置：租户显式设置 → 全局默认。
 * 与后端 effectiveRoomBilling 同口径（水费金额随最终计费方式取）。
 */
export function resolveTenantBilling(t: BillingLike | null | undefined, d: BillingDefaults): ResolvedBilling {
  const explicitMode = t?.water_mode === 'meter' || t?.water_mode === 'monthly'
  const waterMode: WaterMode = explicitMode ? (t!.water_mode as WaterMode) : d.waterMode
  let waterAmount = Number((waterMode === 'monthly' ? t?.water_monthly_fee : t?.water_price) || 0)
  const amountFollows = waterAmount <= 0
  if (amountFollows) waterAmount = waterMode === 'monthly' ? d.waterMonthlyFee : d.waterMeterPrice

  const explicitCycle = t?.pay_cycle === 'monthly' || t?.pay_cycle === 'quarterly'
  const payCycle: PayCycle = explicitCycle ? (t!.pay_cycle as PayCycle) : d.payCycle

  const rawPayDay = Number(t?.pay_day)
  const payDayFollowsGlobal = !(Number.isFinite(rawPayDay) && rawPayDay >= 0)
  const payDay = payDayFollowsGlobal ? d.payDay : rawPayDay

  const rawRemind = Number(t?.remind_days)
  const remindFollowsGlobal = !(Number.isFinite(rawRemind) && rawRemind >= 0)
  const remindDays = remindFollowsGlobal ? d.remindDays : rawRemind

  return {
    waterMode,
    waterAmount,
    waterFollowsGlobal: !explicitMode || amountFollows,
    payCycle,
    payCycleFollowsGlobal: !explicitCycle,
    payDay,
    payDayFollowsGlobal,
    remindDays,
    remindDaysFollowsGlobal: remindFollowsGlobal,
  }
}

/** 租户计费与缴费的一句话文案，如 "包月 40 元/月 · 季付 · 每月5号收 · 提前3天提醒" */
export function tenantBillingSummary(t: BillingLike | null | undefined, d: BillingDefaults): string {
  const r = resolveTenantBilling(t, d)
  const parts = [`${waterModeLabel(r.waterMode)} ${waterAmountText(r.waterMode, r.waterAmount)}`]
  parts.push(cycleLabel(r.payCycle))
  if (r.payDay >= 1 && r.payDay <= 28) parts.push(`${payDayText(r.payDay)}收`)
  parts.push(`提前 ${r.remindDays} 天提醒`)
  return parts.join(' · ')
}

/** 房间生效计费设置的一句话文案（后端 room.billing 已是解析后的值） */
export function roomBillingSummary(b: RoomBilling | null | undefined): string {
  if (!b) return '—'
  const parts = [`${waterModeLabel(b.water_mode)} ${waterAmountText(b.water_mode, b.water_amount)}`]
  parts.push(cycleLabel(b.pay_cycle))
  if (b.pay_day >= 1 && b.pay_day <= 28) parts.push(`${payDayText(b.pay_day)}收`)
  return parts.join(' · ')
}

/** 全局默认的一句话描述，用于表单里的"跟随全局默认"提示 */
export function globalBillingText(d: BillingDefaults): string {
  const parts = [
    `水费按吨 ${waterAmountText('meter', d.waterMeterPrice)}`,
    `包月 ${waterAmountText('monthly', d.waterMonthlyFee)}`,
    cycleLabel(d.payCycle),
    d.payDay >= 1 && d.payDay <= 28 ? `${payDayText(d.payDay)}收` : '不设缴费日',
    `提前 ${d.remindDays} 天提醒`,
  ]
  return parts.join(' · ')
}
