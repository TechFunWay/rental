// migrate_ledger.go — 台账模型相关的一次性数据迁移。
//
// 两条迁移，各管一个时代：
//
//	0.5.1  rental_ledger_data_isolation
//	       把 v0.5.0 的「全员共享 + 全局按人授权」搬进「0 号公共台账 + 成员权限」。
//	       只对 pre-0.6 老库有意义（判据：rental_accesses 表存在）。
//	0.5.2  rental_personal_ledgers
//	       把「台账」模型换成「个人台账 + 共同台账」：老台账补 kind/owner，
//	       成员行补 role/status，并给每个账号建一个自己的个人台账。
//	       存量数据**一行不动**（业务表归属列保持原值），老用户仍看得到原台账。
//
// 物理表名与归属列名沿用 0.5.1 的 orgs / user_orgs / org_id（见
// Ledger.TableName 的说明）：升级阶梯跑在 AutoMigrate 之后，改物理名会让
// 老库先被建出空的新表新列、再叠一次重命名，容易在唯一索引上撞车。
package rental

import (
	"time"

	"gorm.io/gorm"
)

// ledgerScopedTables 是带归属列（物理名 org_id）的业务表。
var ledgerScopedTables = []string{
	"rooms", "tenants", "contracts", "tenant_attachments", "tenant_applications",
	"meter_records", "bills", "bill_items", "payments", "fee_items",
}

// oldGlobalIndexes 是 v0.3.3 建立的**全局**唯一索引，0.5.1 要换成按台账版本。
var oldGlobalIndexes = []string{
	"idx_rental_room_location",
	"idx_rental_bill_room_period",
	"idx_meter_record_room_period",
	"idx_rental_fee_item_key",
}

// legacyOrgIndexes 是 0.5.1 用物理名建的按台账唯一索引（名字里带 org），
// 0.5.2 换成语义名（idx_*_ledger_*，与模型标签一致）后把它们删掉。
var legacyOrgIndexes = []string{
	"idx_rental_room_org_location",
	"idx_rental_bill_org_period",
	"idx_meter_record_org_period",
	"idx_rental_fee_item_org_key",
}

// ledgerIndexes 是按台账维度的唯一索引：同一台账内仍然唯一，
// 不同台账之间互不干扰（两个房东都可以有 A区/101）。索引名与模型标签一致。
var ledgerIndexes = []string{
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_room_ledger_location ON rooms(org_id, community, room_no)",
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_bill_ledger_period ON bills(org_id, room_id, period)",
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_meter_record_ledger_period ON meter_records(org_id, room_id, period)",
	"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_fee_item_ledger_key ON fee_items(org_id, `key`)",
}

// ---------------------------------------------------------------------------
// 0.5.1：全员共享 → 0 号公共台账
// ---------------------------------------------------------------------------

// legacyTenantDataExists 判断这是不是一个「已经有人用过的老库」。
//
// 0.5.1 迁移只对老库有意义：全新安装启动时还没有任何账号（注册发生在
// 服务可用之后），此时跑它只会在库里留下一张空的「公共台账」。
func legacyTenantDataExists(db *gorm.DB) bool {
	var users int64
	if tableExists(db, "users") {
		db.Table("users").Count(&users)
	}
	return users > 0 || tableExists(db, "rental_accesses")
}

// migrateOrgEraSchema 建 0 号公共台账、给业务表补归属列并回填 0、重建索引。
func migrateOrgEraSchema(db *gorm.DB) error {
	if !legacyTenantDataExists(db) {
		return nil
	}
	if err := ensureLegacySharedLedger(db); err != nil {
		return err
	}

	// 1) 补归属列并回填 0。AutoMigrate 已按新模型建过列，但老库的同名表不会
	//    自动补列，所以这里显式 ALTER；已存在则跳过（SQLite 无 IF NOT EXISTS 列语法）。
	for _, table := range ledgerScopedTables {
		if !tableExists(db, table) {
			continue
		}
		if !columnExists(db, table, "org_id") {
			if err := db.Exec("ALTER TABLE " + table + " ADD COLUMN org_id integer DEFAULT 0").Error; err != nil {
				return err
			}
		}
		if err := db.Exec("UPDATE " + table + " SET org_id = 0 WHERE org_id IS NULL").Error; err != nil {
			return err
		}
	}

	// 2) 唯一索引换成按台账版本。先删全局的，再建带归属列的。
	for _, idx := range oldGlobalIndexes {
		if err := db.Exec("DROP INDEX IF EXISTS " + idx).Error; err != nil {
			return err
		}
	}
	for _, stmt := range ledgerIndexes {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}
	return nil
}

// ensureLegacySharedLedger 保证 0 号公共台账存在（幂等）。
//
// 注意：**必须用显式 SQL 插入 id=0**。GORM 的 Create 把主键零值当作
// 「未设置」而忽略该列，SQLite 会自增分配 1，导致 0 号台账根本不存在、
// 而迁移写出的成员关系（org_id=0）全部悬空——数据看似迁移成功、
// 实际谁都看不到（2026-10-08 实测踩到）。
func ensureLegacySharedLedger(db *gorm.DB) error {
	var count int64
	if err := db.Model(&Ledger{}).Where("id = ?", LegacySharedLedgerID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	return db.Exec(
		"INSERT INTO orgs (id, name, notes, kind, owner_user_id, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?)",
		LegacySharedLedgerID, LegacySharedLedgerName,
		"0.5.1 时代的全员共享台账，升级后仍是这些成员共同使用",
		LedgerKindShared, firstAdminID(db),
		time.Now(), time.Now()).Error
}

// migrateAccessToLedgers 把 v0.5.x 的全局权限行平移到 0 号台账成员关系：
// 每个用户都加入公共台账，其原 level 作为在该台账的 perms。
//
// 这样老用户升级后：仍只属于 0 号台账、权限不变、看到的数据不变。
func migrateAccessToLedgers(db *gorm.DB) error {
	if !tableExists(db, "users") {
		return nil
	}
	type userRow struct {
		ID   uint
		Role string
	}
	users := make([]userRow, 0)
	if err := db.Table("users").Select("id, role").Find(&users).Error; err != nil {
		return err
	}

	// 原全局权限：user_id → level。表可能因历史原因不存在。
	levels := map[uint]string{}
	if tableExists(db, "rental_accesses") {
		type accessRow struct {
			UserID uint
			Level  string
		}
		rows := make([]accessRow, 0)
		if err := db.Table("rental_accesses").Select("user_id, level").Scan(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			levels[r.UserID] = r.Level
		}
	}

	ownerID := firstAdminID(db)
	for _, u := range users {
		if memberOf(db, u.ID, LegacySharedLedgerID) != nil {
			continue
		}
		perms := ""
		if u.Role != "admin" {
			if raw, ok := levels[u.ID]; ok {
				stored, err := marshalPerms(parsePerms(raw))
				if err != nil {
					return err
				}
				perms = stored
			}
		} else {
			// 管理员在这个老台账里就是管理者：给全部模块完全权限。
			stored, err := marshalPerms(fullPerms())
			if err != nil {
				return err
			}
			perms = stored
		}
		role := MemberRoleMember
		if u.ID == ownerID {
			role = MemberRoleOwner
		}
		if err := db.Create(&LedgerMember{
			LedgerID: LegacySharedLedgerID, UserID: u.ID,
			Role: role, Status: MemberStatusActive, Perms: perms,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 0.5.2：个人台账 + 共同台账
// ---------------------------------------------------------------------------

// migratePersonalLedgers 把「台账」换成「个人台账 + 共同台账」模型：
//
//  1. 老台账（0.5.1 建的公共台账、以及管理员另建的组织）一律标为共同台账，
//     管理者取原创建者，创建者不可考时取最早的管理员；
//  2. 成员行补 role/status（AutoMigrate 建的列有默认值，这里只兜 NULL/空串），
//     并保证管理者在册；
//  3. 给每个账号建一个个人台账（自己是管理者、全部模块完全权限）——
//     新注册用户由注册钩子负责，这里是存量账号的补齐；
//  4. 删掉 0.5.1 用物理名建的按台账唯一索引（AutoMigrate 已按模型标签建好
//     同列的新索引）。
//
// 业务数据一行不动：老用户升级后仍然在自己原来的台账里看到原来的数据。
func migratePersonalLedgers(db *gorm.DB) error {
	if !tableExists(db, "orgs") || !tableExists(db, "user_orgs") {
		return nil
	}

	// 1) 老台账补 kind/owner。
	if err := db.Exec(
		"UPDATE orgs SET kind = ? WHERE kind IS NULL OR kind = ''", LedgerKindShared).Error; err != nil {
		return err
	}
	if err := db.Exec(
		"UPDATE orgs SET owner_user_id = created_by WHERE (owner_user_id IS NULL OR owner_user_id = 0) AND created_by > 0").Error; err != nil {
		return err
	}
	if err := db.Exec(
		"UPDATE orgs SET owner_user_id = ? WHERE owner_user_id IS NULL OR owner_user_id = 0",
		firstAdminID(db)).Error; err != nil {
		return err
	}

	// 2) 成员行补 role/status，并给每个台账的管理者补一行（幂等）。
	if err := db.Exec(
		"UPDATE user_orgs SET role = ? WHERE role IS NULL OR role = ''", MemberRoleMember).Error; err != nil {
		return err
	}
	if err := db.Exec(
		"UPDATE user_orgs SET status = ? WHERE status IS NULL OR status = ''", MemberStatusActive).Error; err != nil {
		return err
	}
	ledgers := make([]Ledger, 0)
	if err := db.Find(&ledgers).Error; err != nil {
		return err
	}
	storedFull, err := marshalPerms(fullPerms())
	if err != nil {
		return err
	}
	for _, l := range ledgers {
		if l.OwnerUserID == 0 {
			continue
		}
		if m := memberOf(db, l.OwnerUserID, l.ID); m == nil {
			if err := db.Create(&LedgerMember{
				LedgerID: l.ID, UserID: l.OwnerUserID,
				Role: MemberRoleOwner, Status: MemberStatusActive, Perms: storedFull,
			}).Error; err != nil {
				return err
			}
			continue
		}
		// 管理者的成员行一并规范成「全部模块完全权限」：权限判定本来就走
		// role=owner 短路，这里把存库值也对齐，避免出现「owner 却只勾了
		// 两个模块」这种自相矛盾的成员行（前端成员列表直接展示这列）。
		if err := db.Exec(
			"UPDATE user_orgs SET role = ?, status = ?, perms = ? WHERE org_id = ? AND user_id = ?",
			MemberRoleOwner, MemberStatusActive, storedFull, l.ID, l.OwnerUserID).Error; err != nil {
			return err
		}
	}

	// 3) 每个存量账号一个个人台账（ensurePersonalLedger 幂等）。
	users := make([]uint, 0)
	if err := db.Table("users").Order("id ASC").Pluck("id", &users).Error; err != nil {
		return err
	}
	for _, uid := range users {
		if _, err := ensurePersonalLedger(db, uid); err != nil {
			return err
		}
	}

	// 4) 删掉 0.5.1 时代的旧索引名（列相同，模型标签已建新名索引）。
	for _, idx := range legacyOrgIndexes {
		if err := db.Exec("DROP INDEX IF EXISTS " + idx).Error; err != nil {
			return err
		}
	}
	return nil
}

// firstAdminID 最早注册的管理员 ID（迁移回填台账管理者用）；没有管理员返回 0。
func firstAdminID(db *gorm.DB) uint {
	var id uint
	_ = db.Table("users").Where("role = ?", "admin").Order("id ASC").
		Limit(1).Pluck("id", &id).Error
	return id
}

// adminIDs 全部管理员的用户 ID（无则返回空切片，避免 IN () 语法问题）。
func adminIDs(db *gorm.DB) []uint {
	ids := make([]uint, 0)
	_ = db.Table("users").Where("role = ?", "admin").Pluck("id", &ids).Error
	if len(ids) == 0 {
		ids = append(ids, 0) // 保证 IN 子句非空，且 0 不匹配任何用户
	}
	return ids
}

// ---------------------------------------------------------------------------
// 共用工具
// ---------------------------------------------------------------------------

// tableExists 判断表是否存在（迁移的可重入基础）。
func tableExists(db *gorm.DB, name string) bool {
	var count int64
	db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&count)
	return count > 0
}

// columnExists 判断列是否存在。
func columnExists(db *gorm.DB, table, column string) bool {
	type col struct {
		Name string
	}
	cols := make([]col, 0)
	if err := db.Raw("PRAGMA table_info(" + table + ")").Scan(&cols).Error; err != nil {
		return false
	}
	for _, c := range cols {
		if c.Name == column {
			return true
		}
	}
	return false
}
