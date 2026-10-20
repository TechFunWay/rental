// utils/lease.ts — 租期换算与快捷填充（2026-10-20 粉丝需求）。
//
// 粉丝原话「续租周期只到三个月，还会遇到半年和年付」落在两件事上：
//   1. 自定义收费项目的周期下拉只放开到季付（见 FeeItemsCard.vue，本文件不涉及）；
//   2. 录入租户/续租时没有「半年 / 一年」的快捷选择，到期日只能手点日历。
// 本文件只管第 2 件的换算口径，页面只负责把结果写进 lease_end_date。
//
// 口径（与后端 server/rental/renew.go 的 leaseEndAfter 逐字一致）：
//
//	到期日 = 起租日 + N 个月 − 1 天
//
// 例：2026-01-15 起租 3 个月 → 2026-04-14；起租 1 年 → 2027-01-14。
// 之所以 −1 天：4 月 15 日就是下一期的起租日，不能两份租期重叠一天。
//
// 为什么日号溢出取目标月最后一天（1/31 + 1 月 = 2/28）：
// JavaScript 的 Date 会自动进位成 3 月 3 日，算出「租期莫名多 3 天」的到期日，
// 所以这里按「目标月最后一天」钳位。后端月份用同样的钳位逻辑，两边必须一致，
// 否则前端显示与后端落库会差一天。

/** 租期快捷选项（顺序由短到长，与常见合同口径一致）。 */
export interface LeaseTermOption {
  /** 月数 */
  months: number
  /** chip 文案 */
  label: string
}

export const leaseTermOptions: LeaseTermOption[] = [
  { months: 1, label: '1 个月' },
  { months: 3, label: '3 个月' },
  { months: 6, label: '半年' },
  { months: 12, label: '一年' },
]

/** 解析 YYYY-MM-DD 为本地零点 Date；非法返回 null。 */
export function parseLeaseDate(v: string | null | undefined): Date | null {
  if (!v) return null
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(v.trim())
  if (!m) return null
  const y = Number(m[1])
  const mo = Number(m[2])
  const d = Number(m[3])
  const date = new Date(y, mo - 1, d)
  // 严格校验：2 月 30 日这类越界日期 Date 会自动进位，回读不一致就是非法。
  if (date.getFullYear() !== y || date.getMonth() !== mo - 1 || date.getDate() !== d) return null
  return date
}

/** Date → YYYY-MM-DD（本地时区，不用 toISOString 以免跨时区差一天）。 */
export function formatLeaseDate(d: Date): string {
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

/**
 * 起租日加 n 个月，日号溢出取目标月最后一天（与后端 addMonthsClamp 同口径）。
 * 基于 1 号构造再回填日号，避免 Date 的自动进位。
 */
export function addMonthsClamp(base: Date, n: number): Date {
  const y = base.getFullYear()
  const m = base.getMonth() + n
  const first = new Date(y, m, 1)
  const lastDay = new Date(first.getFullYear(), first.getMonth() + 1, 0).getDate()
  const day = Math.min(base.getDate(), lastDay)
  return new Date(first.getFullYear(), first.getMonth(), day)
}

/** 到期日 = 起租日 + n 个月 − 1 天（闭区间口径，见文件头）。 */
export function leaseEndDate(moveIn: Date, months: number): Date {
  const end = addMonthsClamp(moveIn, months)
  end.setDate(end.getDate() - 1)
  return end
}

/**
 * 按起租日与月数算到期日字符串（表单快捷 chip 用）。
 * 起租日为空/非法、月数非法时返回空串，由调用方提示先填起租日。
 */
export function leaseEndDateStr(moveIn: string, months: number): string {
  const base = parseLeaseDate(moveIn)
  if (!base || !Number.isFinite(months) || months < 1) return ''
  return formatLeaseDate(leaseEndDate(base, months))
}

/**
 * 租期文案：给「到期时间」旁边的小字提示用。
 * 例：2026-01-15 ~ 2027-01-14（1 年 / 12 个月）。
 * 任一日期缺失返回空串（不显示）。
 */
export function leaseRangeText(moveIn: string, leaseEnd: string): string {
  const start = parseLeaseDate(moveIn)
  const end = parseLeaseDate(leaseEnd)
  if (!start || !end) return ''
  const months = Math.max(
    1,
    Math.round((end.getTime() - start.getTime() + 86400000) / (86400000 * 30.4375)),
  )
  // 月数按自然月反推更准：优先用年月差修正到与 leaseEndDate 互逆的值。
  let exact = months
  for (let guess = Math.max(1, months - 1); guess <= months + 1; guess++) {
    if (formatLeaseDate(leaseEndDate(start, guess)) === formatLeaseDate(end)) {
      exact = guess
      break
    }
  }
  const label = exact >= 12 ? `${(exact / 12).toFixed(exact % 12 === 0 ? 0 : 1)} 年` : `${exact} 个月`
  return `${formatLeaseDate(start)} ~ ${formatLeaseDate(end)}（${label}）`
}

/**
 * 续租基准日 = max(当前到期日, 今天)：提前续从原到期日顺延，过期续从今天起算。
 * 与后端 renewBase 同口径。
 */
export function renewBaseDate(leaseEnd: string, today = new Date()): Date {
  const end = parseLeaseDate(leaseEnd)
  const startOfToday = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  if (!end || end.getTime() < startOfToday.getTime()) return startOfToday
  return end
}

/** 续租预览：给定基准日与月数，返回新的到期日字符串（弹窗里实时展示用）。 */
export function renewalEndDateStr(leaseEnd: string, months: number, today = new Date()): string {
  const base = renewBaseDate(leaseEnd, today)
  return formatLeaseDate(leaseEndDate(base, months))
}
