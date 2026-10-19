import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { getVersion } from '../api/config'
import {
  applyUpdate as applyUpdateApi, checkUpdate, donateSupport, getUpdateApplyStatus,
  type UpdateApplyStage, type UpdateInfo,
} from '../api/system'
import { onFnOSGatewayOrigin } from '../utils/gateway'

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

  // ---- 版本与更新弹窗（常驻入口：顶栏版本号按钮 / 系统设置卡片）----
  const updateDialogVisible = ref(false)
  const checkingUpdate = ref(false)
  const checkError = ref('')

  // ---- 一键自更新（管理员）：按部署形态下载安装包 → 校验 → 替换 → 原地重启 ----
  //
  // 两种部署形态的前端构建基址不同，一键更新装的包也不同（后端按 -fnos-app 区分）：
  //   - 飞牛安装形态（fpk，前端基址 /app/techfunway-rental/）：装飞牛安装包 .fpk，
  //     解出程序与带网关基址的 ui/；替换前会校验待装前端基址与形态一致，不一致
  //     就失败并保留原前端（避免网关形态装了根基址发行包导致资源 404 白屏）；
  //   - Docker/直连形态（前端基址 /）：装发行包 .tar.gz，内含程序 + 根基址 www/。
  // 前端判定形态优先用后端 /api/version/check 的 fnos_install（与后端选包逻辑
  // 同源，后端按 -fnos-app 判定）；老版本后端没有该字段时退回两个现成信号：
  // 页面是否由飞牛网关 socket 提供，以及构建基址是否非根（fpk 前端基址非根，
  // 发行包 www 基址是 /）。
  const isFnosInstall = computed(() =>
    updateInfo.value?.fnos_install
    ?? (onFnOSGatewayOrigin() || import.meta.env.BASE_URL !== '/'))

  /** 一键更新要下载的产物名称（说明文案用，避免笼统的「替换程序与前端」）。 */
  const applyArtifactLabel = computed(() =>
    isFnosInstall.value ? '飞牛安装包（.fpk）' : '发行包（.tar.gz）')

  const applyStage = ref<UpdateApplyStage>('idle')
  const applyMessage = ref('')
  let applyPollTimer: ReturnType<typeof setInterval> | null = null
  let restartPollTimer: ReturnType<typeof setInterval> | null = null

  const applying = computed(() =>
    ['resolving', 'downloading', 'verifying', 'replacing', 'restarting'].includes(applyStage.value))

  /** 一键更新失败（成功态复用 failed 视觉 + 「已更新/重启完成」文案，排除掉）。 */
  const applyFailed = computed(() =>
    applyStage.value === 'failed' && !/已更新|重启完成/.test(applyMessage.value))

  const applyButtonText = computed(() => {
    switch (applyStage.value) {
      case 'resolving': return '检查中…'
      case 'downloading': return '下载中…'
      case 'verifying': return '校验中…'
      case 'replacing': return '替换文件中…'
      case 'restarting': return '重启中…'
      case 'failed': return '重试更新'
      default: return '⚡ 一键更新'
    }
  })

  function stopApplyPoll() {
    if (applyPollTimer) { clearInterval(applyPollTimer); applyPollTimer = null }
    if (restartPollTimer) { clearInterval(restartPollTimer); restartPollTimer = null }
  }

  /** 轮询任务阶段；服务重启瞬间连接断开即转入"等待服务恢复"。 */
  function pollApplyStatus() {
    stopApplyPoll()
    applyPollTimer = setInterval(async () => {
      try {
        const res = await getUpdateApplyStatus()
        const d = res.data?.data
        if (!d) return
        applyStage.value = d.stage
        applyMessage.value = d.message || ''
        if (d.stage === 'restarting') waitServiceBack()
        if (d.stage === 'failed' || d.stage === 'idle') stopApplyPoll()
      } catch {
        // 连接断开 = 正在重启，等新进程接管
        waitServiceBack()
      }
    }, 1500)
  }

  /** 轮询公开 /api/version 直到新进程接管（版本号变化），成功后刷新页面。
   *
   *  两个关键点（2026-10-11 修）：
   *  1. 地址必须带 BASE_URL：飞牛包的前端基址是 /app/techfunway-rental/，
   *     写死 '/api/version' 会落到网关根目录 404，永远等不到服务恢复；
   *  2. 必须比对版本号：替换二进制到 exec 之间旧进程仍在服务，只要「有响应」
   *     就判定成功会在重启刚开始时误报「已更新到旧版本」，还把新版本号写进
   *     「已提示」记录导致横幅永久消失。只有拿到的版本既不等于当前版本、
   *     又等于目标版本时才认为新进程已接管。 */
  function waitServiceBack() {
    if (restartPollTimer) return
    if (applyPollTimer) { clearInterval(applyPollTimer); applyPollTimer = null }
    const target = updateInfo.value?.latest || ''
    const from = appVersion.value || updateInfo.value?.current || ''
    applyStage.value = 'restarting'
    applyMessage.value = '重启完成，等待服务恢复…'
    let tries = 0
    restartPollTimer = setInterval(async () => {
      tries++
      try {
        const res = await fetch(`${import.meta.env.BASE_URL}api/version`)
        const j = await res.json()
        const v = j?.data?.version || ''
        const replaced = v && v !== from && (!target || v === target)
        if (replaced) {
          stopApplyPoll()
          applyStage.value = 'failed' // 复用 failed 视觉（停止 applying 态），文案给成功
          applyMessage.value = `已更新到 ${v}，页面即将刷新…`
          lsSet(KEY_UPDATE_DISMISSED, v) // 新版本内不再弹同一条横幅
          setTimeout(() => location.reload(), 1500)
        }
      } catch {
        if (tries > 90) {
          stopApplyPoll()
          applyStage.value = 'failed'
          applyMessage.value = '等待服务恢复超时，请手动刷新页面查看版本'
        }
      }
    }, 1000)
  }

  /** 一键更新入口：按部署形态下载对应安装包（飞牛 .fpk / 发行包 .tar.gz）；
   *  失败无损（服务端各阶段都先落暂存再替换，失败保留原程序与原前端）。 */
  async function applyNewVersion() {
    if (applying.value) return
    applyStage.value = 'resolving'
    applyMessage.value = '正在获取最新版本信息…'
    try {
      const res = await applyUpdateApi()
      if (res.data?.code === 0 && res.data.data) {
        applyStage.value = res.data.data.stage
        applyMessage.value = res.data.data.message || ''
        pollApplyStatus()
      } else {
        applyStage.value = 'failed'
        applyMessage.value = res.data?.message || '启动更新失败，可从下方链接手动下载安装包升级'
      }
    } catch {
      applyStage.value = 'failed'
      applyMessage.value = '启动更新失败，请稍后重试；也可以从下方链接手动下载安装包升级'
    }
  }

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
      if (res.data?.code === 0 && res.data.data) {
        // 无论有没有新版本都记下来：常驻入口（顶栏版本号按钮 / 系统设置卡片）
        // 需要据此显示「已经是最新版本」，旧版只在 has_update 时赋值，导致
        // 「已是最新」这种最正常的状态反而什么也不显示。
        updateInfo.value = res.data.data
        if (res.data.data.has_update && lsGet(KEY_UPDATE_DISMISSED) !== res.data.data.latest) {
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

  /** 【已支持】上报匿名支持计数（amount 为赞赏金额，元），成功才记住当前版本。
   *  应用升级到新版本后 supportedVersion 与当前版本不一致，横幅与
   *  启动弹窗会再次出现，再次确认即刷新记录。 */
  async function confirmSupported(amount?: number): Promise<boolean> {
    sending.value = true
    errorText.value = ''
    let ok = false
    try {
      const res = await donateSupport(amount)
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

  /** 手动检查更新：入口常驻（顶栏版本号按钮 / 系统设置「版本与更新」卡片），
   *  不再依赖「恰好检测到新版本才出现提示条」。 */
  async function checkUpdateNow() {
    checkingUpdate.value = true
    checkError.value = ''
    try {
      const res = await checkUpdate()
      if (res.data?.code === 0 && res.data.data) {
        updateInfo.value = res.data.data
        // 手动检查命中有新版本时提示条也一起亮：用户主动点的，看到全部信息
        // 就行，不必再强行弹回已经关过的提示条。
        if (res.data.data.has_update && lsGet(KEY_UPDATE_DISMISSED) !== res.data.data.latest) {
          updateBannerVisible.value = true
        }
      } else {
        checkError.value = res.data?.message || '检查更新失败'
      }
    } catch {
      checkError.value = '检查更新失败，请检查网络或稍后再试'
    } finally {
      checkingUpdate.value = false
    }
  }

  function openUpdateDialog() {
    updateDialogVisible.value = true
    void checkUpdateNow()
  }

  function closeUpdateDialog() {
    updateDialogVisible.value = false
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
    updateDialogVisible,
    checkingUpdate,
    checkError,
    applyStage,
    applyMessage,
    applyFailed,
    applying,
    applyButtonText,
    isFnosInstall,
    applyArtifactLabel,
    applyNewVersion,
    checkUpdateNow,
    openUpdateDialog,
    closeUpdateDialog,
    init,
    dismiss,
    dismissBanner,
    dismissUpdateBanner,
    confirmSupported,
    open,
  }
})
