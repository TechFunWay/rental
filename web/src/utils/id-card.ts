// id-card.ts — 身份证号的两条规则，抄表/房源/租户三处都用得上的一小块逻辑：
// 18 位身份证号第 7-14 位是出生日期、第 17 位的奇偶是性别（奇数男、偶数女）。
//
// 与服务端 `deriveFromIDCard`（server/rental/tenant_members.go）同一口径：
// **落库以服务端为准**，这里只用于界面上的即时提示（填完马上看到派生结果）。
// 15 位老号段编码规则与校验位都不同，一律不猜，只做长度校验。

/** 18 位（末位可为 X）或 15 位老号段。 */
export const ID_CARD_PATTERN = /^(\d{17}[\dX]|\d{15})$/

export interface IdCardDerived {
  /** YYYY-MM-DD */
  birthday: string
  gender: 'male' | 'female'
}

/** 派生出生日期与性别；不是 18 位或日期不合法时返回 null。 */
export function deriveFromIdCard(raw: string): IdCardDerived | null {
  const id = (raw || '').trim().toUpperCase()
  if (id.length !== 18) return null
  const year = id.slice(6, 10)
  const month = id.slice(10, 12)
  const day = id.slice(12, 14)
  const date = new Date(`${year}-${month}-${day}T00:00:00`)
  if (Number.isNaN(date.getTime())) return null
  return {
    birthday: `${year}-${month}-${day}`,
    gender: Number(id[16]) % 2 === 1 ? 'male' : 'female',
  }
}

/** 性别中文名；空值给「性别未填」。 */
export function genderLabel(gender: string): string {
  if (gender === 'male') return '男'
  if (gender === 'female') return '女'
  return '性别未填'
}

/**
 * 界面提示文案：
 * - 18 位 → 「出生日期 1990-01-01 · 男（保存时以服务端派生为准）」
 * - 15 位 → 说明不派生，避免用户以为漏了
 * - 其它 → 空串（不打扰输入过程）
 */
export function idCardHint(raw: string): string {
  const id = (raw || '').trim().toUpperCase()
  if (id.length === 15) return '15 位老号段不派生出生日期与性别'
  const derived = deriveFromIdCard(id)
  if (!derived) return ''
  return `出生日期 ${derived.birthday} · ${genderLabel(derived.gender)}（保存时以服务端派生为准）`
}
