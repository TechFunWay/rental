let depth = 0

// 弹窗打开时锁住背景滚动（手机 app 交互惯例）；计数式，弹窗叠开也能正确恢复
export function lockBodyScroll() {
  depth += 1
  if (depth === 1) document.body.style.overflow = 'hidden'
}

export function unlockBodyScroll() {
  depth = Math.max(0, depth - 1)
  if (depth === 0) document.body.style.overflow = ''
}
