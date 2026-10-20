// print-kit — 合同打印 / 物品清单打印的 A4 辅助层，包在 utils/print-root.ts 外面。
//
// 为什么单独包一层而不动 print-root.ts：
// print-root 的 appendPrintPage 能力其实**够用**——每调一次就往 #print-root 追加
// 一个 .print-page（正常文档流、可自然跨页分片），多张图片分页只要循环调用 N 次
// 即可，不需要改原文件；PDF 走隐藏 iframe 的 contentWindow.print()，那是 iframe
// 自己文档的打印，完全不经过 print-root 管线，原文件同样不需要动。
// 这里只提供三件事：A4 @page 的动态注入与撤除、图片逐张分页暂存、PDF iframe 打印。
//
// 为什么固定 A4 竖版、不复用 receiptPaper.ts 的纸张设置：
// receiptPaper 服务的是**收费单据**——针式打印纸、58/80mm 卷纸那套「设备属性」，
// 存在本机 localStorage 的键也是单据专用的。合同与物品交接清单是规范 A4 文书
// （签字、附表、存档都按 A4 纸张设计），把它们的出纸尺寸与收费单据纸张联动，
// 会让「换了卷纸打单据」顺带把合同也打成 58mm——是错误耦合。因此本模块只写
// A4 portrait，@page 用独立 style 标签（id 与 receipt-page-style 不同），
// 两套单据互不覆盖；合同弹窗关闭时 removeA4Portrait() 撤掉标签，避免之后
// 收费单据打印被这里的规则反向压住。
//
// 深色主题：打印根节点本身由 print-root 强制白底（body background #fff），
// 本模块生成的页容器与组件的 .cp-paper/.ip-paper 一律用固定的黑字白底配色
// （不消费主题变量），因此深色主题下打印天然就是白底黑字，无需额外覆盖。

import { appendPrintPage } from './print-root'

const A4_STYLE_ID = 'print-kit-a4-style'
// 页边距 14mm：比收费单据（12mm）略宽，合同与清单正文行距大，装订/签字更从容。
const A4_CSS = '@media print { @page { size: A4 portrait; margin: 14mm; } }'

/** 注入/刷新 A4 @page 规则（print-root 只负责页面隔离，纸张归调用方）。 */
export function applyA4Portrait(): void {
  let tag = document.getElementById(A4_STYLE_ID) as HTMLStyleElement | null
  if (!tag) {
    tag = document.createElement('style')
    tag.id = A4_STYLE_ID
    document.head.appendChild(tag)
  }
  // 始终重写并挪到 <head> 末尾：@page 冲突按文档顺序后写者胜，
  // 打开合同弹窗就意味着用户此刻要的是 A4 输出。
  tag.textContent = A4_CSS
  tag.remove()
  document.head.appendChild(tag)
}

/** 撤除 A4 规则：弹窗卸载时调用，把 @page 主导权还给其它打印模块。 */
export function removeA4Portrait(): void {
  document.getElementById(A4_STYLE_ID)?.remove()
}

/** 写入 A4 纸张并立即打印当前 print-root 里暂存的所有页。 */
export function printA4(): void {
  applyA4Portrait()
  window.print()
}

/**
 * afterprint 收尾（带兜底时限）。
 * window.print() 在多数引擎里同步阻塞到对话框关闭，但 Safari/部分移动端会立即
 * 返回——不能在 print() 之后直接清理，否则预览还没拿到内容。统一等 afterprint
 * 事件，兜底一个足够长的时限，事件丢了也不会永久占住打印根。
 */
function whenPrintSettled(fn: () => void): void {
  let done = false
  const cleanup = () => {
    if (done) return
    done = true
    window.removeEventListener('afterprint', cleanup)
    fn()
  }
  window.addEventListener('afterprint', cleanup)
  window.setTimeout(cleanup, 5 * 60 * 1000)
}

/** A4 内容高度上限（297mm - 上下边距 14mm×2，留 3mm 余量防溢出成两页）。 */
const IMG_MAX_HEIGHT = '264mm'

/**
 * printImageBlobs 把若干图片按传入顺序各占一页（每张一个 .print-page，
 * 复用 print-root 的 appendPrintPage），随后拉起 A4 打印。
 * 返回时暂存页尚未撤除（要等打印收尾），撤除与 objectURL 回收由本函数内部
 * 通过 whenPrintSettled 完成；调用方若要重新挂回别的打印页，等自己下一次
 * 交互再挂即可，重复挂载最多造成一次多余的 Ctrl+P 双页，无数据风险。
 */
export async function printImageBlobs(blobs: Blob[]): Promise<void> {
  if (blobs.length === 0) return
  const urls: string[] = []
  const removers: (() => void)[] = []
  try {
    for (const blob of blobs) {
      const url = URL.createObjectURL(blob)
      urls.push(url)
      const img = document.createElement('img')
      img.src = url
      img.style.cssText =
        `display:block;margin:0 auto;max-width:100%;max-height:${IMG_MAX_HEIGHT};width:auto;height:auto;object-fit:contain;`
      try {
        // 先解码再打印：打印快照取的是已光栅化的内容，未加载完的 <img> 会打成空白页。
        await img.decode()
      } catch {
        // decode 失败不阻断（个别格式引擎拒绝 decode 但能渲染），交给浏览器自行处理。
      }
      const holder = document.createElement('div')
      holder.style.cssText = 'width:100%;display:flex;align-items:center;justify-content:center;'
      holder.appendChild(img)
      removers.push(appendPrintPage(holder))
    }
    // 等一帧让克隆节点进入渲染树再打印（同 RentalReceipt 的挂载即暂存时序等价）。
    await new Promise((r) => requestAnimationFrame(() => r(null)))
    printA4()
    whenPrintSettled(() => {
      for (const rm of removers) rm()
      for (const u of urls) URL.revokeObjectURL(u)
    })
  } catch (err) {
    for (const rm of removers) rm()
    for (const u of urls) URL.revokeObjectURL(u)
    throw err
  }
}

/**
 * printPdfBlob 打印一个 PDF：隐藏 iframe 指向 blob url，load 后短暂等内嵌
 * 查看器渲染，再 contentWindow.focus().print()。PDF 自带分页，不经过 print-root。
 * 失败（加载超时 / 引擎不支持）时抛错，由调用方 toast；不留残留 iframe。
 */
export async function printPdfBlob(blob: Blob): Promise<void> {
  const url = URL.createObjectURL(blob)
  const iframe = document.createElement('iframe')
  iframe.setAttribute('aria-hidden', 'true')
  iframe.setAttribute('title', '合同 PDF 打印')
  // 不用 display:none——部分引擎不渲染隐藏插件，打印会出空白页；1px 移出可视区即可。
  iframe.style.cssText =
    'position:fixed;right:0;bottom:0;width:1px;height:1px;border:0;opacity:0;pointer-events:none;'
  document.body.appendChild(iframe)
  const cleanup = () => {
    iframe.remove()
    URL.revokeObjectURL(url)
  }
  try {
    await new Promise<void>((resolve, reject) => {
      const timer = window.setTimeout(() => reject(new Error('PDF 加载超时，请重试')), 30000)
      iframe.onload = () => {
        window.clearTimeout(timer)
        resolve()
      }
      iframe.onerror = () => {
        window.clearTimeout(timer)
        reject(new Error('PDF 加载失败，请重试'))
      }
      iframe.src = url
    })
    // 给内嵌 PDF 查看器一点初始化时间（load 早于首帧渲染，立即 print 可能打空白）。
    await new Promise((r) => window.setTimeout(r, 500))
    const w = iframe.contentWindow
    if (!w || typeof w.print !== 'function') throw new Error('当前浏览器不支持内嵌 PDF 打印，请下载后打开打印')
    w.focus()
    w.print()
    // iframe 打印的收尾事件在 iframe 窗口派发（父窗口收不到），尽力挂上；
    // 挂不上就由兜底时限回收——期间 iframe 保持存在，重复点打印仍可复用。
    try {
      w.addEventListener('afterprint', cleanup, { once: true })
    } catch {
      /* 忽略：跨文档监听失败由下面的兜底时限覆盖 */
    }
    window.setTimeout(cleanup, 5 * 60 * 1000)
  } catch (err) {
    cleanup()
    throw err
  }
}
