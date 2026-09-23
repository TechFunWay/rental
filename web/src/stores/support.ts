import { defineStore } from 'pinia'
import { ref } from 'vue'
import { getVersion } from '../api/config'
import { checkUpdate, donateSupport, type UpdateInfo } from '../api/system'

// 赞赏支持与新版本提示的全局状态。
//
// 赞赏提示频次（与家族应用一致）：
// - 应用每次启动后，管理员首次进入控制台 1.5s 后弹一次；
// - 点【暂不支持】/关闭后，同版本同次运行内不再弹，应用重启后再次提示；
// - 【已支持】上报成功后本版本内不再提示。
// 「本次运行」由后端 /api/version 返回的 startedAt 区分：刷新页面不重置，
// 应用重启后 startedAt 变化，弹窗会再次出现。
//
// 关键约束：关闭抑制记录必须同时绑定版本号。升级换版本后，旧版本里记下的
// 关闭记录一律失效——弹窗与横幅必定再次弹出，是否赞赏由用户自由选择；
// 点【暂不支持】只表示「本版本本次运行内不再打扰」。
//
// 按版本提示：支持记录按版本号记忆（donate_supported_version）。
// 应用升级到新版本后，即使旧版本支持过，也会重新出现横幅并再次弹窗提醒，
// 每个新版本都重来一次；「已支持」上报成功后本版本内停止提示。
//
// 赞赏横幅常驻展示（关闭不持久化）：点击关闭后刷新浏览器会再次出现，
// 直到用户支持过当前版本（donate_supported_version === 当前版本号）才隐藏。
//
// 新版本提示条：管理员可见，每个最新版本号只提示一次（localStorage 按版本记录）。
//
// 赞赏完全自愿：不赞赏不影响任何功能。

const KEY_SUPPORTED_VERSION = 'donate_supported_version'
const KEY_DISMISSED_START = 'donate_dismissed_start'
const KEY_DISMISSED_VERSION = 'donate_dismissed_version'
const KEY_UPDATE_DISMISSED = 'update_banner_dismissed'

function lsGet(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function lsSet(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    /* 隐私模式下不可用，忽略 */
  }
}

/** 当前版本是否已支持过（支持记录按版本号记忆）。 */
function isSupportedFor(appVersion: string, storedVersion: string | null): boolean {
  if (!storedVersion || !appVersion) return false
  return storedVersion === appVersion
}

export const useSupportStore = defineStore('support', () => {
  // ---- 赞赏弹窗 ----
  const show = ref(false)
  const sending = ref(false)
  const errorText = ref('')

  // ---- 服务端运行标识 ----
  const appVersion = ref('')
  const serverStartedAt = ref('')

  // 记忆的版本号：最近一次「已支持」上报成功时的应用版本
  const supportedVersion = ref(lsGet(KEY_SUPPORTED_VERSION) || '')
  // 当前版本是否已支持
  const donateSupported = ref(false)

  // ---- 赞赏横幅（常驻展示，关闭不持久化）----
  const bannerVisible = ref(false)

  // ---- 新版本提示条 ----
  const updateInfo = ref<UpdateInfo | null>(null)
  const updateBannerVisible = ref(false)

  /** 登录后调用：拉取版本信息并调度提示（仅管理员）。 */
  async function init(isAdmin: boolean) {
    if (!isAdmin) return
    try {
      const res = await getVersion()
      if (res.data?.code === 0 && res.data.data) {
        appVersion.value = res.data.data.version || ''
        serverStartedAt.value = res.data.data.startedAt || ''
      }
    } catch {
      /* 版本接口失败不影响使用 */
    }

    refreshSupported()

    if (!donateSupported.value) {
      // 延迟 1.5s 弹出；若安全问题引导弹窗仍开着，MainLayout 会让 SupportModal
      // 保持不渲染，安全弹窗关闭后自然出现。
      setTimeout(scheduleModal, 1500)
      maybeShowBanner()
    }
    maybeCheckUpdate()
  }

  /** 版本或支持记录变化后重新计算「当前版本是否已支持」。 */
  function refreshSupported() {
    donateSupported.value = isSupportedFor(appVersion.value, supportedVersion.value || null)
  }

  function scheduleModal() {
    if (donateSupported.value) return
    // 抑制记录必须「同版本 + 同次运行」才有效：升级换版本后旧记录失效，
    // 弹窗重新出现；未拿到 startedAt 时按「本次运行」降级处理。
    const dismissedStart = lsGet(KEY_DISMISSED_START)
    const dismissedVersion = lsGet(KEY_DISMISSED_VERSION)
    const sameRun = !!serverStartedAt.value && dismissedStart === serverStartedAt.value
    const sameVersion = !!appVersion.value && dismissedVersion === appVersion.value
    if (sameRun && sameVersion) return
    if (sameRun && !appVersion.value) return
    show.value = true
  }

  function maybeShowBanner() {
    // 横幅常驻展示：未支持当前版本前每次进入都显示；关闭不记忆
    if (donateSupported.value) return
    bannerVisible.value = true
  }

  /** 关闭横幅：本次会话隐藏，刷新浏览器后再次出现
   *  （支持过当前版本后不再出现，版本升级后再次出现）。 */
  function dismissBanner() {
    bannerVisible.value = false
  }

  async function maybeCheckUpdate() {
    try {
      const res = await checkUpdate()
      if (res.data?.code === 0 && res.data.data?.has_update) {
        updateInfo.value = res.data.data
        if (lsGet(KEY_UPDATE_DISMISSED) !== res.data.data.latest) {
          updateBannerVisible.value = true
        }
      }
    } catch {
      /* 检查失败静默忽略 */
    }
  }

  function dismissUpdateBanner() {
    updateBannerVisible.value = false
    if (updateInfo.value?.latest) lsSet(KEY_UPDATE_DISMISSED, updateInfo.value.latest)
  }

  /** 【暂不支持】/ 关闭弹窗：同版本同次运行内不再弹出。
   *  同时记录版本号：升级后版本变化使旧抑制记录失效，弹窗再次弹出。 */
  function dismiss() {
    show.value = false
    errorText.value = ''
    if (serverStartedAt.value) lsSet(KEY_DISMISSED_START, serverStartedAt.value)
    if (appVersion.value) lsSet(KEY_DISMISSED_VERSION, appVersion.value)
  }

  /** 【已支持】上报匿名支持计数，成功才记住当前版本。
   *  应用升级到新版本后 supportedVersion 与当前版本不一致，横幅与
   *  启动弹窗会再次出现，再次确认即刷新记录。 */
  async function confirmSupported(): Promise<boolean> {
    sending.value = true
    errorText.value = ''
    let ok = false
    try {
      const res = await donateSupport()
      ok = res.status === 200 && res.data?.code === 0 && res.data?.data?.ok === true
    } catch {
      ok = false
    }
    sending.value = false
    if (ok) {
      supportedVersion.value = appVersion.value
      lsSet(KEY_SUPPORTED_VERSION, appVersion.value)
      refreshSupported()
      show.value = false
      bannerVisible.value = false
    } else {
      errorText.value = '发送失败，请稍后重试（不影响你的支持 ❤）'
    }
    return ok
  }

  /** 设置页「支持开发者」卡片入口：手动打开弹窗。 */
  function open() {
    errorText.value = ''
    show.value = true
  }

  return {
    show,
    sending,
    errorText,
    donateSupported,
    appVersion,
    serverStartedAt,
    supportedVersion,
    bannerVisible,
    updateInfo,
    updateBannerVisible,
    init,
    dismiss,
    dismissBanner,
    dismissUpdateBanner,
    confirmSupported,
    open,
  }
})
