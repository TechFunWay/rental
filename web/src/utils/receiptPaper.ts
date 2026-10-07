// receiptPaper — 收费单据的打印纸张设置。
//
// 纸张属于打印设备属性（每台打印机/每个场景不一样），因此保存在本机
// localStorage 而非系统级租房设置；打印前调 applyReceiptPaper 动态写入
// @page 规则（组件内静态 @page A5 是兜底默认值，动态规则后插入文档、优先生效）。
export type ReceiptPaper = 'a5' | 'a4' | 'roll80' | 'roll58'

export const receiptPapers: { value: ReceiptPaper; label: string }[] = [
  { value: 'a5', label: 'A5 竖版' },
  { value: 'a4', label: 'A4 竖版' },
  { value: 'roll80', label: '80mm 卷纸' },
  { value: 'roll58', label: '58mm 卷纸' },
]

const STORAGE_KEY = 'rental_receipt_paper'

export function loadReceiptPaper(): ReceiptPaper {
  const v = localStorage.getItem(STORAGE_KEY)
  return receiptPapers.some((p) => p.value === v) ? (v as ReceiptPaper) : 'a5'
}

export function saveReceiptPaper(p: ReceiptPaper) {
  localStorage.setItem(STORAGE_KEY, p)
}

const PAGE_RULES: Record<ReceiptPaper, { size: string; margin: string }> = {
  a5: { size: 'A5 portrait', margin: '12mm' },
  a4: { size: 'A4 portrait', margin: '12mm' },
  roll80: { size: '80mm auto', margin: '4mm 3mm' },
  roll58: { size: '58mm auto', margin: '3mm 2mm' },
}

// applyReceiptPaper 打印前调用：注入/更新全局 @page 规则；卷纸额外收紧
// 单据排版（去 nowrap、缩字号），避免六列明细在窄纸幅上溢出。
export function applyReceiptPaper(p: ReceiptPaper) {
  const cfg = PAGE_RULES[p]
  let css = `@media print { @page { size: ${cfg.size}; margin: ${cfg.margin}; }`
  if (p === 'roll80' || p === 'roll58') {
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
