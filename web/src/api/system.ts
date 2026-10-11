import request from './request'

// 系统级通用接口：版本检查、赞赏支持、数据库备份管理。

export interface FpkAsset {
  name: string
  url: string
}

export interface UpdateInfo {
  current: string
  latest: string
  download_url: string
  has_update: boolean
  /** 命中的发布源：gitee（国内可达，优先）/ github（兜底） */
  source?: string
  /** 两平台发行版列表页（恒返回，前端双入口） */
  gitee_url?: string
  github_url?: string
  /** 飞牛安装包（.fpk）附件直链，x86/arm64 各一个 */
  fpk_assets?: FpkAsset[]
}

export function checkUpdate() {
  return request.get<{ code: number; message: string; data: UpdateInfo }>('/api/version/check')

}

// ---------- 一键自更新（管理员） ----------

export type UpdateApplyStage =
  | 'idle' | 'resolving' | 'downloading' | 'verifying' | 'replacing' | 'restarting' | 'failed'

export interface UpdateApplyStatus {
  stage: UpdateApplyStage
  message: string
  version: string
}

/** 启动一键更新：下载发行包 → 校验 → 替换自身 → 原地重启 */
export function applyUpdate() {
  return request.post<{ code: number; message: string; data: UpdateApplyStatus }>('/api/update/apply')
}

/** 轮询更新任务阶段（重启瞬间连接会断，前端据此切到等待恢复） */
export function getUpdateApplyStatus() {
  return request.get<{ code: number; message: string; data: UpdateApplyStatus }>('/api/update/apply/status')
}

/** 上报一次匿名赞赏支持计数；amount 为赞赏金额（元），缺省 0（仅统计，不做支付核验） */
export function donateSupport(amount?: number) {
  return request.post<{ code: number; message: string; data: { ok: boolean } }>('/api/donate/support', {
    amount: amount && amount > 0 ? amount : 0,
  })
}

export interface BackupItem {
  name: string
  size: number
  created_at: string
}

export interface BackupList {
  items: BackupItem[]
  dir: string
  auto_enabled: boolean
  keep_count: number
}

export function listBackups() {
  return request.get<{ code: number; message: string; data: BackupList }>('/api/backups')
}

export function createBackup() {
  return request.post<{ code: number; message: string; data: { name: string } }>('/api/backups')
}

export function deleteBackup(name: string) {
  return request.delete<{ code: number; message: string; data: { ok: boolean } }>(`/api/backups/${name}`)
}

// 下载备份需要 Authorization 头，所以像审计 CSV 导出一样取回 blob，
// 由调用方用 object URL 触发浏览器保存。
export function downloadBackup(name: string) {
  return request.get<Blob>(`/api/backups/${name}/download`, { responseType: 'blob' })
}

// 恢复备份：服务端先做恢复前快照再覆盖主库，随后进程退出等待自动重启；
// 成功响应里 pre_backup 是恢复前快照的文件名（万一恢复出错可再用它救回）。
export function restoreBackup(name: string) {
  return request.post<{ code: number; message: string; data: { ok: boolean; pre_backup: string; restarting: boolean } }>(`/api/backups/${name}/restore`)
}
