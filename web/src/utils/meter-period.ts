// utils/meter-period.ts — 抄表账期的「记住上次」逻辑。
//
// 背景（2026-10-31 粉丝反馈）：水电表常常不在一处，房东先抄完一遍水表、
// 退出弹窗，再进来抄电表。此前抄表开票与录入读数每次打开都把账期重置成
// 系统当前月——跨月抄表（10 月底抄 11 月的表）时每开一次弹窗都要手动把
// 10 月改成 11 月，非常烦。这里把最近一次使用的账期记到 localStorage，
// 弹窗打开时默认落在它上面；改账期或保存读数时更新。
//
// 键必须带应用前缀（见 storage-keys.ts 的说明）：网关域下与兄弟应用共享
// localStorage。隐私模式读写抛异常时静默降级为系统当前月，不影响功能。
import { SK } from './storage-keys'

/** 系统当前月，YYYY-MM。 */
export function currentMonth(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
}

const PERIOD_RE = /^\d{4}-\d{2}$/

/** 最近一次抄表使用的账期；没有记录或值非法时回退系统当前月。 */
export function lastMeterPeriod(): string {
  try {
    const raw = localStorage.getItem(SK.meterPeriod) || ''
    return PERIOD_RE.test(raw) ? raw : currentMonth()
  } catch {
    return currentMonth()
  }
}

/** 记住本次抄表账期（只接受 YYYY-MM）。 */
export function rememberMeterPeriod(period: string): void {
  if (!PERIOD_RE.test(period)) return
  try {
    localStorage.setItem(SK.meterPeriod, period)
  } catch {
    /* 隐私模式忽略 */
  }
}
