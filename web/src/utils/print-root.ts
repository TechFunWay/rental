// print-root — window.print() 的专用打印根节点。
//
// 旧方案是「visibility 技巧」：body * 全隐藏、单据子树再显示。两处致命伤：
// ① 规则写在 RentalReceipt 的 scoped 样式里，编译成 `body[data-v-xxx] *`，
//    body 上没有 data-v 属性，隐藏规则永远匹配不到 —— 打印输出整张网页，
//    弹窗遮罩、赞赏弹窗全被印出来（2026-10-11 粉丝反馈"打印能显示网页、
//    数据空的乱的"的根因）；
// ② 单据被 position:absolute 提到弹窗面板左上角，绝对定位元素在 Chrome 里
//    不参与跨页分片，超出一页的内容被直接裁掉。
//
// 新方案：单据挂载时把自身克隆进 body 直属的 #print-root（屏幕上 display:none），
// 打印时把 body 其余直属子元素全部 display:none、只显示 #print-root。
// display 隔离让单据回到正常文档流：可自然分页，且与弹窗层级、运行中弹出的
// 引导弹窗（安全引导/赞赏弹窗）完全无关。没暂存单据时（普通 Ctrl+P）不启用
// 隔离，页面照常打印。
const ROOT_ID = 'print-root'
const BODY_FLAG = 'print-stage-active'
let styleTag: HTMLStyleElement | null = null

function ensureStyle() {
  if (styleTag) return
  styleTag = document.createElement('style')
  styleTag.id = 'print-root-style'
  styleTag.textContent = `
#print-root { display: none; }
@media print {
  body.${BODY_FLAG} {
    overflow: visible !important;
    height: auto !important;
    /* body 常驻品牌色径向渐变（style.css），在打印页上会透出一块紫色 */
    background: #fff !important;
  }
  body.${BODY_FLAG} > *:not(#${ROOT_ID}) { display: none !important; }
  body.${BODY_FLAG} #${ROOT_ID} { display: block !important; }
  .print-page { break-after: page; }
  .print-page:last-child { break-after: auto; }
}`
  document.head.appendChild(styleTag)
}

function syncFlag() {
  const el = document.getElementById(ROOT_ID)
  document.body.classList.toggle(BODY_FLAG, !!el && el.childElementCount > 0)
}

function root(): HTMLElement {
  ensureStyle()
  let el = document.getElementById(ROOT_ID)
  if (!el) {
    el = document.createElement('div')
    el.id = ROOT_ID
    document.body.appendChild(el)
  }
  return el
}

// appendPrintPage 把节点克隆为一个独立打印页（.print-page，break-after: page），
// 返回移除函数——供克隆来源（RentalReceipt）卸载时撤下自己的克隆。
// 注意：不要在打印按钮里清空重拷打印根——那会让各组件手里的移除函数指向
// 已被丢弃的旧节点，关闭弹窗后残留陈克隆，之后 Ctrl+P 会打出旧单据。
// 单据数据在打开期间是静态快照，挂载时克隆一次即为最新内容。
export function appendPrintPage(el: Element): () => void {
  const page = document.createElement('div')
  page.className = 'print-page'
  page.appendChild(el.cloneNode(true))
  root().appendChild(page)
  syncFlag()
  return () => {
    page.remove()
    syncFlag()
  }
}
