// ledger_upgrade_test.go — 0.5.1「组织 + 公共台账」老库升级到
// 0.5.2「个人台账 + 共同台账」的验收测试。
//
// 手工用裸 SQL 造一个 0.5.1 形状的库（physical 名就是 orgs / user_orgs /
// 业务表 org_id，见 ledger.go 的 TableName 说明），再走真实启动顺序
// database.AutoMigrate + database.RunUpgrades(db, "0.5.2", ...)，断言：
//
//  1. 老业务行仍在原台账（org_id 未被改动）；
//  2. 老用户仍是原台账成员，权限原样平移，权限行补上 role/status；
//  3. 每个存量账号都拿到自己的个人台账（owner=自己、全模块 full）；
//  4. 老台账 kind=shared，管理者取「最早的管理员」（created_by 不可考时兜底）；
//  5. 0.5.1 / 0.5.2 都被记录，且 0.5.1 导入的老全局权限进 0 号台账。
package rental

import (
	"path/filepath"
	"testing"

	"smallgo/server/database"

	"gorm.io/gorm/logger"
)

// TestOldLibraryUpgradeToPersonalLedgers 老库（0.5.1 组织模型）升级到
// 0.5.2 个人/共同台账模型后，数据与归属一行不丢。
func TestOldLibraryUpgradeToPersonalLedgers(t *testing.T) {
	dir := t.TempDir()
	db, err := database.InitDB(filepath.Join(dir, "legacy-0.5.1.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	// --- 1) 裸 SQL 造 0.5.1 形状的库 -------------------------------------
	mustExec := func(stmt string) {
		t.Helper()
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
	for _, stmt := range []string{
		// 框架表：users 与 0.5.1 的升级记录表。
		`CREATE TABLE users ( id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, password TEXT NOT NULL, role TEXT DEFAULT 'user', status INTEGER DEFAULT 1, api_key TEXT, auth_version INTEGER NOT NULL DEFAULT 1, fnos_user_id INTEGER, fnos_username TEXT, created_at DATETIME, updated_at DATETIME )`,
		// 0.5.1 的台账/成员表（物理名刻意保留 orgs / user_orgs）。
		`CREATE TABLE orgs ( id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, notes TEXT, created_by INTEGER )`,
		`CREATE TABLE user_orgs ( user_id INTEGER NOT NULL, org_id INTEGER NOT NULL, perms TEXT, is_org_admin BOOLEAN DEFAULT 0, PRIMARY KEY (user_id, org_id) )`,
		// 0.5.x 时代的全局按人授权表（0.6 起不再作为模型，只被迁移读取）。
		`CREATE TABLE rental_accesses ( user_id INTEGER PRIMARY KEY, level TEXT, updated_at DATETIME )`,
		// 业务表：已有 org_id 归属列与数据。
		`CREATE TABLE rooms ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, community TEXT, room_no TEXT NOT NULL, default_rent REAL DEFAULT 0, water_mode TEXT, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE bills ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, room_id INTEGER, period TEXT NOT NULL, room_no TEXT NOT NULL, total_amount REAL DEFAULT 0, paid_amount REAL DEFAULT 0, status TEXT DEFAULT 'unpaid', water_mode TEXT, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE tenants ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, room_id INTEGER, name TEXT NOT NULL, active BOOLEAN DEFAULT 1, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE meter_records ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, room_id INTEGER, period TEXT NOT NULL, water REAL DEFAULT 0, created_at DATETIME, updated_at DATETIME )`,
	} {
		mustExec(stmt)
	}

	// 数据：1 个管理员 + 1 个普通用户；老台账 id=1（created_by=0，不可考）。
	mustExec(`INSERT INTO users (id, username, password, role, status, auth_version) VALUES (1, 'admin1', 'x', 'admin', 1, 1), (2, 'user1', 'x', 'user', 1, 1)`)
	mustExec(`INSERT INTO orgs (id, name, notes, created_by) VALUES (1, 'A房东', '老台账', 0)`)
	mustExec(`INSERT INTO user_orgs (user_id, org_id, perms, is_org_admin) VALUES (1, 1, '{"rooms":"full","bills":"full"}', 1), (2, 1, '{"rooms":"view","bills":"edit"}', 0)`)
	mustExec(`INSERT INTO rental_accesses (user_id, level) VALUES (2, 'readonly')`)

	// 业务数据都归老台账 1。
	mustExec(`INSERT INTO rooms (id, user_id, org_id, community, room_no, default_rent) VALUES (1, 1, 1, 'A区', '101', 1000), (2, 2, 1, 'A区', '102', 800)`)
	mustExec(`INSERT INTO bills (id, user_id, org_id, room_id, period, room_no, total_amount, paid_amount, status) VALUES (1, 1, 1, 1, '2026-09', '101', 1000, 300, 'partial')`)
	mustExec(`INSERT INTO tenants (id, user_id, org_id, room_id, name, active) VALUES (1, 1, 1, 1, '老王', 1)`)
	mustExec(`INSERT INTO meter_records (id, user_id, org_id, room_id, period, water) VALUES (1, 1, 1, 1, '2026-09', 12)`)

	// --- 2) 真实启动顺序：AutoMigrate → RunUpgrades(0.5.2) ---------------
	db.Logger = logger.Default.LogMode(logger.Info)
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// upgrade_records 此刻必须没有 0.5.1 / 0.5.2 的记录（由 AutoMigrate 新建为空表）。
	for _, v := range []string{"0.5.1", "0.5.2"} {
		var n int64
		db.Table("upgrade_records").Where("version = ?", v).Count(&n)
		if n != 0 {
			t.Fatalf("precondition: %s must not be recorded before upgrade", v)
		}
	}
	if err := database.RunUpgrades(db, "0.5.2", database.Upgrades); err != nil {
		t.Fatalf("RunUpgrades: %v", err)
	}

	// --- 3) 断言：老数据仍在原台账 --------------------------------------
	type row struct {
		ID    uint
		OrgID uint
	}
	for _, tc := range []struct {
		table string
		want  map[uint]uint // id -> org_id
	}{
		{"rooms", map[uint]uint{1: 1, 2: 1}},
		{"bills", map[uint]uint{1: 1}},
		{"tenants", map[uint]uint{1: 1}},
		{"meter_records", map[uint]uint{1: 1}},
	} {
		got := make([]row, 0)
		if err := db.Table(tc.table).Select("id, org_id").Scan(&got).Error; err != nil {
			t.Fatalf("read %s: %v", tc.table, err)
		}
		if len(got) != len(tc.want) {
			t.Fatalf("%s rows = %d, want %d", tc.table, len(got), len(tc.want))
		}
		for _, r := range got {
			if want, ok := tc.want[r.ID]; !ok || r.OrgID != want {
				t.Fatalf("%s id=%d org_id=%d, want %d（业务行归属不得被升级改动）",
					tc.table, r.ID, r.OrgID, want)
			}
		}
	}

	// --- 4) 断言：老台账 kind=shared、owner=最早管理员 ------------------
	var old Ledger
	if err := db.First(&old, 1).Error; err != nil {
		t.Fatalf("load legacy ledger: %v", err)
	}
	if old.Kind != LedgerKindShared {
		t.Fatalf("legacy ledger kind = %q, want %q", old.Kind, LedgerKindShared)
	}
	if old.OwnerUserID != 1 {
		t.Fatalf("legacy ledger owner = %d, want 1（created_by 不可考时取最早管理员）", old.OwnerUserID)
	}

	// --- 5) 断言：老用户仍是成员、权限平移、role/status 补齐 ------------
	m1 := memberOf(db, 1, 1)
	if m1 == nil {
		t.Fatal("admin1 必须仍是老台账成员")
	}
	if m1.Role != MemberRoleOwner || m1.Status != MemberStatusActive {
		t.Fatalf("admin1 membership = %+v, want owner/active", m1)
	}
	assertFullModulePerms(t, m1.LedgerPerms(), "admin1 legacy perms")
	m2 := memberOf(db, 2, 1)
	if m2 == nil {
		t.Fatal("user1 必须仍是老台账成员")
	}
	if m2.Role != MemberRoleMember || m2.Status != MemberStatusActive {
		t.Fatalf("user1 membership = %+v, want member/active", m2)
	}
	if p := m2.LedgerPerms(); p[ModuleRooms] != PermView || p[ModuleBills] != PermEdit {
		t.Fatalf("user1 legacy perms = %v, want rooms:view + bills:edit（权限必须平移）", p)
	}

	// 0.5.1 导入的全局权限进 0 号公共台账：普通用户按旧 level 换算。
	var shared Ledger
	if err := db.First(&shared, LegacySharedLedgerID).Error; err != nil {
		t.Fatalf("0 号公共台账必须由 0.5.1 迁移建出: %v", err)
	}
	if shared.Kind != LedgerKindShared || shared.OwnerUserID != 1 {
		t.Fatalf("0 号台账 = %+v, want shared/owner 1", shared)
	}
	sharedMember := memberOf(db, 2, LegacySharedLedgerID)
	if sharedMember == nil {
		t.Fatal("旧全局权限必须平移成 0 号台账的成员关系")
	}
	if p := sharedMember.LedgerPerms(); p[ModuleRooms] != PermView || p[ModuleBills] != PermView {
		t.Fatalf("0 号台账权限 = %v, want readonly → 全模块 view", p)
	}

	// --- 6) 断言：每个老账号都拿到个人台账 ------------------------------
	for _, uid := range []uint{1, 2} {
		pl := personalLedgerOf(db, uid)
		if pl == nil {
			t.Fatalf("user %d 必须拿到个人台账", uid)
		}
		if pl.Kind != LedgerKindPersonal || pl.OwnerUserID != uid {
			t.Fatalf("user %d personal ledger = %+v, want personal/owner self", uid, pl)
		}
		m := activeMemberOf(db, uid, pl.ID)
		if m == nil || m.Role != MemberRoleOwner || m.Status != MemberStatusActive {
			t.Fatalf("user %d personal membership = %+v, want owner/active", uid, m)
		}
		assertFullModulePerms(t, m.LedgerPerms(), "personal ledger perms")
	}
	// 个人台账不能是别人的。
	if personalLedgerOf(db, 1).ID == personalLedgerOf(db, 2).ID {
		t.Fatal("两个账号的个人台账必须各自独立")
	}

	// --- 7) 断言：迁移被记录，且可重复执行（幂等） ----------------------
	for _, v := range []string{"0.5.1", "0.5.2"} {
		var n int64
		db.Model(&database.UpgradeRecord{}).Where("version = ?", v).Count(&n)
		if n != 1 {
			t.Fatalf("upgrade record %s = %d, want 1", v, n)
		}
	}
	if err := database.RunUpgrades(db, "0.5.2", database.Upgrades); err != nil {
		t.Fatalf("re-run upgrades: %v", err)
	}
	var personalCount, memberCount int64
	db.Model(&Ledger{}).Where("kind = ?", LedgerKindPersonal).Count(&personalCount)
	if personalCount != 2 {
		t.Fatalf("personal ledgers after re-run = %d, want 2（迁移必须幂等）", personalCount)
	}
	db.Model(&LedgerMember{}).Where("org_id = ?", 1).Count(&memberCount)
	if memberCount != 2 {
		t.Fatalf("legacy ledger members after re-run = %d, want 2", memberCount)
	}
}
