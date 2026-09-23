import { reactive } from 'vue'

// 全局 Toast 总线：App.vue 挂一个 <Toast> 绑定此状态，任何模块（含 api 层）都能 toast。
export const toastState = reactive({
  message: '',
  type: 'success' as 'success' | 'error',
})

let seq = 0

export function toast(message: string, type: 'success' | 'error' = 'success') {
  // 同名消息连发也要重新触发，用不可见序号前缀保证 message 每次都变化
  toastState.message = `${seq++}${message}`
  toastState.type = type
}

// Toast 组件展示前剥离序号前缀
export function toastText(raw: string) {
  const m = /^\d+([\s\S]*)$/.exec(raw)
  return m ? m[1] : raw
}
