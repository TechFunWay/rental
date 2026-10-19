// utils/cycle.ts — 缴费周期（月付 / 季付 / 半年付 / 年付）的换算与展示口径。
//
// 后端口径：一个缴费周期覆盖 N 个月（月付 1、季付 3、半年付 6、年付 12）。
// 账单仍然每月一张——水/电/燃气（读数类）与包月水费恒按月收，租金/卫生费/
// 管理费等固定费项目只在「到期月」（锚点月）按 N 个月一次收，非锚点月的固定费
// 为 0（到期判断在服务端 fixedItemDue）。缴费周期自 v0.2.3 起在租户上设置
// （未设置跟随台账/全局默认），这里只放周期换算与文案，供房源/租户/账单页共用。

export type PayCycle = 'monthly' | 'quarterly' | 'semiannual' | 'annual'

/** 各缴费周期覆盖的月数（与后端 cycleMonths 同表） */
const CYCLE_MONTHS: Record<PayCycle, number> = {
  monthly: 1,
  quarterly: 3,
  semiannual: 6,
  annual: 12,
}

/** 各缴费周期的中文名（与后端 cycleLabel 同表） */
const CYCLE_LABELS: Record<PayCycle, string> = {
  monthly: '月付',
  quarterly: '季付',
  semiannual: '半年付',
  annual: '年付',
}

/** 下拉/筛选用的周期选项（由短到长） */
export const payCycleOptions: { value: PayCycle; label: string }[] = [
  { value: 'monthly', label: '月付' },
  { value: 'quarterly', label: '季付' },
  { value: 'semiannual', label: '半年付' },
  { value: 'annual', label: '年付' },
]

/** 一个缴费周期覆盖的月数：月付 1、季付 3、半年付 6、年付 12 */
export function cycleMonths(cycle: PayCycle | string | undefined | null): number {
  return CYCLE_MONTHS[normalizeCycle(cycle)]
}

/** 规范化缴费周期：未知值与空值一律按月付处理（与后端 normalizePayCycle 一致） */
export function normalizeCycle(cycle: PayCycle | string | undefined | null): PayCycle {
  return typeof cycle === 'string' && cycle in CYCLE_MONTHS ? (cycle as PayCycle) : 'monthly'
}

export function cycleLabel(cycle: PayCycle | string | undefined | null): string {
  return CYCLE_LABELS[normalizeCycle(cycle)]
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

/** 多月账单的覆盖区间文案：月付 "2026-09"，季付 "2026-09 ~ 2026-11" */
export function coveredPeriodText(period: string, cycle: PayCycle | string | undefined | null): string {
  const months = cycleMonths(cycle)
  if (months <= 1) return period
  return `${period} ~ ${addMonths(period, months - 1)}`
}

/**
 * 某房该月是否应出账。
 *
 * 收费项目周期化后账单**按月**出：水/电/燃气与包月水费恒按月，房租等固定费
 * 只在到期月计入。因此这里恒为 true，与后端 onBillSchedule 保持同一口径——
 * 抄表开票名单不再按缴费周期过滤房间（年付房同样要每月抄水电）。
 */
export function onBillSchedule(_payCycle?: PayCycle | string | null, _period?: string, _lastBillPeriod?: string | null): boolean {
  return true
}

/** 账单类型名：月账单 / 季账单 / 半年账单 / 年账单 */
export function billTypeLabel(cycle: PayCycle | string | undefined | null): string {
  const months = cycleMonths(cycle)
  if (months >= 12) return '年账单'
  if (months >= 6) return '半年账单'
  if (months >= 3) return '季账单'
  return '月账单'
}

/** 角标用的单字缩写：月 / 季 / 半 / 年 */
export function cycleShort(cycle: PayCycle | string | undefined | null): string {
  const months = cycleMonths(cycle)
  if (months >= 12) return '年'
  if (months >= 6) return '半'
  if (months >= 3) return '季'
  return '月'
}
