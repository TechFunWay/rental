// utils/cycle.ts — 缴费周期（月付 / 季付）的换算与展示口径。
//
// 后端口径：季付每 3 个月一张账单，租金/卫生费/管理费/包月水费按月数放大，
// 账期与该房首张账单月对齐。缴费周期自 v0.2.3 起在租户上设置（未设置跟随
// 偏好设置里的全局默认），这里只放周期换算与文案，供房源/租户/账单页共用。

export type PayCycle = 'monthly' | 'quarterly'

/** 一个缴费周期覆盖的月数：月付 1、季付 3 */
export function cycleMonths(cycle: PayCycle | string | undefined | null): number {
  return normalizeCycle(cycle) === 'quarterly' ? 3 : 1
}

export function normalizeCycle(cycle: PayCycle | string | undefined | null): PayCycle {
  return cycle === 'quarterly' ? 'quarterly' : 'monthly'
}

export function cycleLabel(cycle: PayCycle | string | undefined | null): string {
  return normalizeCycle(cycle) === 'quarterly' ? '季付' : '月付'
}

/** 账期加 n 个月：YYYY-MM → YYYY-MM（基于 1 号，无月末溢出） */
export function addMonths(period: string, n: number): string {
  const [y, m] = period.split('-').map(Number)
  if (!y || !m) return period
  const d = new Date(y, m - 1 + n, 1)
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

/** 两个账期相差的月数（b 比 a 大多少个月，可为负） */
export function monthsBetween(a: string, b: string): number {
  const [ya, ma] = a.split('-').map(Number)
  const [yb, mb] = b.split('-').map(Number)
  if (!ya || !ma || !yb || !mb) return 0
  return (yb - ya) * 12 + (mb - ma)
}

/** 季付账单的覆盖区间文案：月付 "2026-09"，季付 "2026-09~11月" */
export function coveredPeriodText(period: string, cycle: PayCycle | string | undefined | null): string {
  if (normalizeCycle(cycle) !== 'quarterly') return period
  const [y, m] = period.split('-').map(Number)
  if (!y || !m) return period
  const end = m + 2 > 12 ? `${y + 1}年${String(m + 2 - 12).padStart(2, '0')}月` : `${String(m + 2).padStart(2, '0')}月`
  return `${period}~${end}`
}

/** 季付房该月是否应出账：无历史账单任意月可为首账月，有则整差 3 个月 */
export function onBillSchedule(payCycle: PayCycle | string | undefined | null, period: string, lastBillPeriod: string | null | undefined): boolean {
  if (normalizeCycle(payCycle) !== 'quarterly') return true
  if (!lastBillPeriod) return true
  const diff = monthsBetween(lastBillPeriod, period)
  return diff > 0 && diff % 3 === 0
}
