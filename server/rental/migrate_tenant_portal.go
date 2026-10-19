// migrate_tenant_portal.go — 0.6.10 租户登录与角色权限的结构迁移。
//
// 这条迁移**只加结构、不动数据**：建索引为的是让「一个账号只绑一条租户档案」
// 与「一条档案只允许一条待审申请」这两个约束由数据库兜底（应用层在同一事务里
// 已做双向校验，索引防的是并发绕过）。
//
// 为什么用**部分唯一索引**：未关联的租户档案 account_user_id 全是 0，普通唯一
// 索引会在第二行 0 上立刻冲突——而这正是「多张档案都还没关联账号」的正常状态。
// `WHERE account_user_id > 0` 把唯一性限定在「真的关联了」的那些行。
//
// 可重入：全部 CREATE INDEX IF NOT EXISTS；且 AutoMigrate 已经先把表和列建好
// （见 rental.go 里 &Tenant{} / &TenantChangeRequest{} 的注册），所以这里不需要
// 建表建列，也就能保证在 NoTx（SQLite 的 DDL 不宜包事务）下安全重跑。
package rental

import "gorm.io/gorm"

// migrateTenantPortal 建立租户门户相关的三条索引。
func migrateTenantPortal(db *gorm.DB) error {
	statements := []string{
		// 一个登录账号只能绑定一条租户档案。
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_tenant_account " +
			"ON tenants(account_user_id) WHERE account_user_id > 0",
		// 按台账查关联状态（「这个账号在本台账是不是已关联」）。
		"CREATE INDEX IF NOT EXISTS idx_rental_tenant_ledger_account " +
			"ON tenants(org_id, account_user_id)",
		// 一条租户档案同时只允许一条待审的变更申请。
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_change_pending " +
			"ON tenant_change_requests(tenant_id) WHERE status = 'pending'",
	}
	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}
