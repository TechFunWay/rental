// receipt-text.ts — 收费单据的纯文本导出。
//
// 背景（2026-11-18 粉丝需求）：单据现在只有「打印 / 另存 PDF」和批量打印，
// 房东把单据截图发微信给对方时，「图片不打开、不下载就会失效」。文本单据
// 复制即发、粘贴即到，不依赖任何图片链接，也方便直接塞进聊天记录对账。
//
// 口径约束：文本只是**展示层**的一种输出，与 RentalReceipt 的 HTML 单据同源——
// 明细行一律走 bills/:id 返回的 bill_items（内置项由后端 billBuiltInItems 生成，
// 押金单列、自定义项并入），前端不再自己从账单固定列拼一份第二口径；
// 「被隐藏项目合并成一行其他费用」是 RentalReceipt 的渲染规则，后端出参里
// 被隐藏项仍然在 items 里、只是带 receipt_hidden 标记，所以这里也要做同样的
// 合并，两条渲染路径的明细之和才都等于 total_amount（与单据一致性修复同口径）。
//
// 金额、应缴、结转、欠缴全部复用 api/rental.ts 的 payableOf/arrearsOf/carryInOf，
// 不写本地算式。
import {
  arrearsOf,
  carryInOf,
  fmtMoney,
  isArrears,
  payableOf,
  type Bill,
  type BillItemDetail,
  type PropertyMeta,
} from '../api/rental'
import { coveredDateRange } from './cycle'

/** 后端 items 出参形状（单据与账单详情共用，见 bills.go billItemRows）。 */
type ReceiptItems = Pick<BillItemDetail, 'key' | 'name' | 'amount' | 'detail' | 'receipt_hidden'>[] | undefined

function money(v: number): string {
  return fmtMoney(v || 0)
}

/**
 * billToText 生成一张收费单据的纯文本。
 * 版式对齐纸质单据：抬头 → 基本信息 → 明细 → 合计 → 备注/落款，
 * 微信粘贴后无格式也能一眼读完，故只用换行与全角空格，不依赖列对齐。
 */
export function billToText(bill: Bill, items: ReceiptItems, property?: PropertyMeta | null): string {
  const lines: string[] = []
  const head = property?.name?.trim() || '租金收费单据'
  lines.push(head)
  lines.push(`${bill.period} 期租金及费用收缴凭证`)
  if (bill.receipt_no) lines.push(`单据编号：${bill.receipt_no}`)
  lines.push(`房号：${bill.room_no || '—'}　租户：${bill.tenant_name || '—'}`)
  // 计租区间（Gitee issue IKW41H 需求 3）：与 HTML 单据同一口径，写清这笔钱覆盖
  // 哪几天，季付/年付对账时不必再自己数月份。
  const range = coveredDateRange(bill.period, bill.pay_cycle)
  if (range) lines.push(`计租区间：${range}`)

  // 明细直接列后端 items（bill_items 出参，金额>0 才建行，含自定义项与押金，
  // 与合计同源；峰谷分时电费在 detail 里自带「峰/谷 读数 × 单价」全文，
  // 文本单据照抄 detail 即可，不再前端拆行——避免第二套渲染口径）。
  let hiddenTotal = 0
  let hiddenCount = 0
  const shown = (items || []).filter((it) => {
    if (!it.receipt_hidden) return true
    if (it.amount > 0) {
      hiddenTotal += it.amount
      hiddenCount++
    }
    return false
  })
  if (shown.length || hiddenCount) {
    lines.push('')
    lines.push('费用明细')
    for (const it of shown) {
      const detail = it.detail ? `（${it.detail}）` : ''
      lines.push(`　${it.name}　${money(it.amount)} 元${detail}`)
    }
    if (hiddenCount) {
      // 与 RentalReceipt 同规则：被隐藏项目不单列、名目不写出，合并成一行「其他费用」。
      lines.push(`　其他费用　${money(hiddenTotal)} 元（${hiddenCount} 项）`)
    }
  }

  lines.push('')
  lines.push(`合计　${money(bill.total_amount)} 元`)
  const carry = carryInOf(bill)
  if (carry) {
    lines.push(`其中上期结转 ${carry > 0 ? '少收' : '多收'} ${money(Math.abs(carry))} 元`)
  }
  lines.push(`本期应缴　${money(payableOf(bill))} 元`)
  if (bill.paid_amount > 0) {
    lines.push(`已收　${money(bill.paid_amount)} 元${bill.paid_at ? `（${String(bill.paid_at).slice(0, 10)}）` : ''}`)
  }
  if (isArrears(bill)) {
    lines.push(`尚欠　${money(arrearsOf(bill))} 元`)
  } else {
    lines.push('已缴清')
  }

  if (bill.remark) {
    lines.push('')
    lines.push(`备注：${bill.remark}`)
  }
  if (property?.contact?.trim()) lines.push(`联系电话：${property.contact}`)
  if (property?.note?.trim()) lines.push(property.note)
  lines.push('')
  lines.push('收款确认（出租方签字）：________')
  return lines.join('\n')
}

/**
 * billsBundleToText 批量单据文本：与批量打印同数据源（/bills/receipts 的
 * { property, bills:[{bill, items}] }），逐张之间加分隔线，一张一段。
 */
export function billsBundleToText(
  property: PropertyMeta | null | undefined,
  bills: { bill: Bill; items?: ReceiptItems }[],
): string {
  return bills
    .map((e) => billToText(e.bill, e.items, property))
    .join('\n\n' + '─'.repeat(24) + '\n\n')
}

/**
 * downloadTextFile 前端直接产文件下载（与 CSV 导出「服务端产字节流」不同，
 * 文本是纯展示转换，没必要走后端）。加 BOM，避免 Windows 记事本打开乱码。
 */
export function downloadTextFile(filename: string, text: string): void {
  const blob = new Blob(['\uFEFF' + text], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  // 立即 revoke 在部分引擎会截断下载，延迟释放（与打印图片 url 回收同一做法）。
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** 文件名里的账期/房号可能带斜杠等非法字符，统一压平。 */
export function safeTextFilename(prefix: string, period: string, roomNo: string): string {
  const flat = (s: string) => (s || '').replace(/[\\/:*?"<>|\s]+/g, '-')
  return `${prefix}-${flat(period)}${roomNo ? `-${flat(roomNo)}` : ''}.txt`
}
