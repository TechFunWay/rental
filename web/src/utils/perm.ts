// utils/perm.ts — 模块权限的共用词表与纯函数（无 store、无副作用）。
//
// v0.6.0 起业务数据按「台账」隔离，模块权限挂在 (用户, 台账) 关系上，
// 所以前端**唯一**的权限口径是「当前台账的权限」（stores/ledger.ts 的
// currentPerms）：路由守卫的页面门禁、侧栏/底部菜单显隐、页面内按钮显隐
// 全部取它。
//
// 抽成独立文件的理由：这些常量与判定原先住在 stores/rentalAccess.ts（跨台账
// 并集的旧口径）。两套口径并存会让「菜单亮着、点进去却被守卫弹回个人资料页」
// 这类问题反复出现，故统一为一套；词表放在这里，避免 org store 与旧 store
// 互相 import 形成循环。
import type { ModuleKey, ModulePerm, ModulePerms } from '../api/rental'

/** 档位排序：数字越大权限越高；缺省（未勾选）为 0。 */
export const PERM_RANK: Record<ModulePerm, number> = { view: 1, edit: 2, full: 3 }

/** 模块中文名（文案用，与后端 moduleNames 一致）。 */
export const MODULE_LABEL: Record<ModuleKey, string> = {
  rooms: '房源',
  tenants: '租户与合同',
  meters: '抄表',
  bills: '账单',
  payments: '收款记录',
  analytics: '统计分析',
}

/** 模块稳定顺序（与后端 moduleKeys 一致）。 */
export const ALL_MODULES: ModuleKey[] = ['rooms', 'tenants', 'meters', 'bills', 'payments', 'analytics']

/** 全部模块完全权限（管理员恒用这一份）。 */
export const FULL_PERMS: ModulePerms = Object.fromEntries(
  ALL_MODULES.map((m) => [m, 'full']),
) as ModulePerms

/**
 * 权限权重：各模块档位之和。0 表示这个台账里一个模块都看不到，
 * 用于「挑一个能用的默认台账」与「这个台账到底有没有权限」的判断。
 */
export function permScore(perms: ModulePerms | undefined | null): number {
  let score = 0
  for (const tier of Object.values(perms ?? {})) score += PERM_RANK[tier] ?? 0
  return score
}

/**
 * 台账档位短语：常见勾选给可读名（完全/录入/只读/抄表），
 * 混合勾选叫「自定义」，一个都没勾叫「无权限」。
 */
export function levelLabelOf(perms: ModulePerms | undefined | null): string {
  const p = perms ?? {}
  const entries = Object.entries(p) as [ModuleKey, ModulePerm][]
  if (entries.length === 0) return '无权限'
  const isPreset = (tier: ModulePerm) => ALL_MODULES.every((m) => p[m] === tier)
  if (isPreset('full')) return '完全'
  if (isPreset('edit')) return '录入'
  if (isPreset('view')) return '只读'
  // 旧版「抄表专用档」换算出来的组合：抄表可录 + 房源可看。
  if (entries.length === 2 && p.meters === 'edit' && p.rooms === 'view') return '抄表'
  return '自定义'
}
