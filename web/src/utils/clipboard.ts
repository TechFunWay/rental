// clipboard.ts — 复制文本的公共封装。
//
// 为什么单独一个工具（2026-11-18）：应用大量部署在 **HTTP 局域网 / 飞牛网关**
// 下，navigator.clipboard 只在安全上下文可用，直接调会 undefined 报错——
// 现有各处都是各自 try 一下就算了（复制失败静默）。文本单据导出的主战场恰恰
// 是「复制到微信粘贴」，失败必须能兜住：安全上下文走 async API，否则回退
// 隐藏 textarea + execCommand('copy')，两条都失败才由调用方提示改走下载。
export async function copyPlainText(text: string): Promise<boolean> {
  try {
    if (window.isSecureContext && navigator.clipboard) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // 权限被拒等异常继续走回退
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.setAttribute('readonly', '')
    ta.style.cssText = 'position:fixed;top:0;left:0;opacity:0;'
    document.body.appendChild(ta)
    ta.select()
    ta.setSelectionRange(0, text.length)
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  } catch {
    return false
  }
}
