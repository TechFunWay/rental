import request from './request'

// 系统级通用接口：版本检查、赞赏支持、数据库备份管理。

export interface UpdateInfo {
  current: string
  latest: string
  download_url: string
  has_update: boolean
}

export function checkUpdate() {
  return request.get<{ code: number; message: string; data: UpdateInfo }>('/api/version/check')
}

export function donateSupport() {
  return request.post<{ code: number; message: string; data: { ok: boolean } }>('/api/donate/support')
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
