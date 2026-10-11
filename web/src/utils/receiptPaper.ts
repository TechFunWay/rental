// receiptPaper — 收费单据的打印纸张设置。
//
// 纸张属于打印设备属性（每台打印机/每个场景不一样），因此保存在本机
// localStorage 而非系统级租房设置；打印前调 applyReceiptPaper 动态写入
// @page 规则（组件内静态 @page A5 是兜底默认值，动态规则后插入文档、优先生效）。
export type ReceiptPaper = 'a5' | 'a4' | 'roll80' | 'roll58' | 'custom'

export const receiptPapers: { value: ReceiptPaper; label: string }[] = [
  { value: 'a5', label: 'A5 竖版' },
  { value: 'a4', label: 'A4 竖版' },
  { value: 'roll80', label: '80mm 卷纸' },
  { value: 'roll58', label: '58mm 卷纸' },
  // 自定义尺寸：针式打印纸、发货单、二联单等非标准纸张都用这个
  // （2026-10-11 粉丝反馈"单据尺寸不能自定义"）。
  { value: 'custom', label: '自定义尺寸…' },
]

// 自定义纸张尺寸（毫米）。宽高都必须在合理范围内，避免用户填 0 或离谱数值
// 导致 @page 规则非法、打印直接空白。
export interface CustomPaper {
  width: number
  height: number
  /** 是否横向 */
  landscape: boolean
}

export const CUSTOM_PAPER_LIMITS = { min: 40, max: 1000 }

const STORAGE_KEY = 'rental_receipt_paper'
const CUSTOM_KEY = 'rental_receipt_paper_custom'

export function loadReceiptPaper(): ReceiptPaper {
  const v = localStorage.getItem(STORAGE_KEY)
  return receiptPapers.some((p) => p.value === v) ? (v as ReceiptPaper) : 'a5'
}

export function saveReceiptPaper(p: ReceiptPaper) {
  localStorage.setItem(STORAGE_KEY, p)
}

export function loadCustomPaper(): CustomPaper {
  try {
    const raw = localStorage.getItem(CUSTOM_KEY)
    if (raw) {
      const o = JSON.parse(raw) as CustomPaper
      if (isValidCustomPaper(o)) return o
    }
  } catch { /* 解析失败回退默认 */ }
  // 默认给一个常见的针式打印纸尺寸（241×140 是两联单常用规格）
  return { width: 241, height: 140, landscape: false }
}

export function saveCustomPaper(p: CustomPaper) {
  if (!isValidCustomPaper(p)) return
  localStorage.setItem(CUSTOM_KEY, JSON.stringify(p))
}

export function isValidCustomPaper(p: unknown): p is CustomPaper {
  if (!p || typeof p !== 'object') return false
  const o = p as CustomPaper
  const ok = (n: unknown) => typeof n === 'number' && Number.isFinite(n)
    && n >= CUSTOM_PAPER_LIMITS.min && n <= CUSTOM_PAPER_LIMITS.max
  return ok(o.width) && ok(o.height) && typeof o.landscape === 'boolean'
}

const PAGE_RULES: Record<Exclude<ReceiptPaper, 'custom'>, { size: string; margin: string }> = {
  a5: { size: 'A5 portrait', margin: '12mm' },
  a4: { size: 'A4 portrait', margin: '12mm' },
  roll80: { size: '80mm auto', margin: '4mm 3mm' },
  roll58: { size: '58mm auto', margin: '3mm 2mm' },
}

// 自定义尺寸按"能塞多少内容"给页边距：纸张窄（≤100mm，卷纸量级）时收紧，
// 宽纸（针式打印纸/发货单）给常规边距。
function customMargin(c: CustomPaper): string {
  const shortSide = Math.min(c.width, c.height)
  return shortSide <= 100 ? '4mm 3mm' : '8mm'
}

// applyReceiptPaper 打印前调用：注入/更新全局 @page 规则；卷纸额外收紧
// 单据排版（去 nowrap、缩字号），避免六列明细在窄纸幅上溢出。
export function applyReceiptPaper(p: ReceiptPaper, custom?: CustomPaper) {
  let size: string
  let margin: string
  let narrow = p === 'roll80' || p === 'roll58'
  if (p === 'custom') {
    const c = custom && isValidCustomPaper(custom) ? custom : loadCustomPaper()
    // CSS @page 的 size 支持 "<宽> <高>"；宽高按用户填的**原样**给，
    // 勾了「横向」才交换两者。不要自作主张按大小排序 —— 用户填
    // 210×140（横向单据）时排成 140×210 会变成竖版，打印出来方向是错的。
    const w = c.landscape ? c.height : c.width
    const h = c.landscape ? c.width : c.height
    size = `${w}mm ${h}mm`
    margin = customMargin(c)
    narrow = Math.min(c.width, c.height) <= 100
  } else {
    size = PAGE_RULES[p].size
    margin = PAGE_RULES[p].margin
  }

  let css = `@media print { @page { size: ${size}; margin: ${margin}; }`
  if (narrow) {
    css += `
  .receipt { width: 100% !important; font-size: 12px !important; }
  .receipt-title { font-size: 14px !important; }
  .receipt-table, .receipt-table th, .receipt-table td { white-space: normal !important; padding: 2px 3px !important; }
  .receipt-grid { column-gap: 0.5rem !important; }`
  }
  css += ' }'

  let tag = document.getElementById('receipt-page-style') as HTMLStyleElement | null
  if (!tag) {
    tag = document.createElement('style')
    tag.id = 'receipt-page-style'
    document.head.appendChild(tag)
  }
  tag.textContent = css
}

// paperSummary 给界面用的纸张描述（含自定义尺寸）。
// 宽高按用户填写原样显示，勾了横向才交换，与 applyReceiptPaper 口径一致。
export function paperSummary(p: ReceiptPaper, custom?: CustomPaper): string {
  if (p !== 'custom') return receiptPapers.find((x) => x.value === p)?.label ?? ''
  const c = custom && isValidCustomPaper(custom) ? custom : loadCustomPaper()
  const w = c.landscape ? c.height : c.width
  const h = c.landscape ? c.width : c.height
  return `自定义 ${w}×${h}mm`
}
