// role.ts — 平台三个角色的中文口径（内部值保持 admin / user / tenant，不改动后端与历史数据）。
//
// 用户 2026-10-13 定调：把应用当平台做，角色叫「系统管理员 / 房东 / 租户」。
//   - admin  → 系统管理员：平台方，负责房东入驻审核、系统配置与备份；
//              **对业务数据没有特权**（不是台账成员就读不到别人的数据）。
//   - user   → 房东（出租人）：注册即拥有一个只属于自己的个人台账，可建共同台账协作。
//   - tenant → 租户：只读自己的登记资料、账单、缴费与合同（门户）。
export type UserRole = 'admin' | 'user' | 'tenant'

/** 角色的中文名；未知值原样返回，避免界面出现空白。 */
export function roleLabel(role: string): string {
  switch (role) {
    case 'admin':
      return '系统管理员'
    case 'user':
      return '房东'
    case 'tenant':
      return '租户'
    default:
      return role || '—'
  }
}

/** 角色徽标配色（与既有 admin 徽标一致的紫/蓝/绿系）。 */
export function roleBadgeClass(role: string): string {
  switch (role) {
    case 'admin':
      return 'bg-violet-100 text-violet-700 dark:bg-violet-500/15 dark:text-violet-300'
    case 'tenant':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-500/15 dark:text-emerald-300'
    default:
      return 'bg-sky-100 text-sky-700 dark:bg-sky-500/15 dark:text-sky-300'
  }
}

/** 房源规模的常用选项（入驻申请与房东资料共用）。 */
export const ROOM_SCALE_OPTIONS = ['1-5 间', '6-20 间', '20 间以上', '暂无，先试用']
