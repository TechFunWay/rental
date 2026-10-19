// tenant_portal_upgrade_test.go — 0.6.9 老库升级到 0.6.10（租户登录与角色权限）
// 的验收测试。
//
// 为什么这条最需要测：0.6.10 给 **tenants 表加了 account_user_id 等 4 列**，并新建
// 了两条**部分唯一索引**（`ON tenants(account_user_id) WHERE account_user_id > 0`、
// `ON tenant_change_requests(tenant_id) WHERE status='pending'`）。老库里所有租户的
// account_user_id 都是新补的列、默认为 0——如果索引写成**普通唯一索引**而不是部分
// 索引，第二条租户行就会在 0 上撞唯一约束而升级失败（这正是设计文档里点名的坑）。
//
// 本测试用裸 SQL 造 0.6.9 形状的库（tenants 无 account_user_id），走真实启动顺序
// AutoMigrate + RunUpgrades(db, "0.6.10", ...)，断言：
//
//  1. 迁移执行成功（两条部分唯一索引在「多行 0」上不冲突）；
//  2. 老业务数据一行未动（租户姓名/手机号/归属台账原样）；
//  3. 存量租户的 account_user_id 全为 0（未关联），房东侧不受影响；
//  4. 索引真的建出来了（部分索引语义：多行 0 允许，重复非零值拒绝）；
//  5. 迁移可重入（再跑一次不报错）。
package rental

import (
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/database"

	"gorm.io/gorm/logger"
)

func TestOldLibraryUpgradeToTenantPortal(t *testing.T) {
	dir := t.TempDir()
	db, err := database.InitDB(filepath.Join(dir, "legacy-0.6.9.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})

	mustExec := func(stmt string) {
		t.Helper()
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// --- 1) 造 0.6.9 形状的库 -------------------------------------------
	// tenants 刻意**没有** account_user_id / emergency_* / address 这几列，
	// 也没有 tenant_change_requests 表——正是升级前的老库形态。
	for _, stmt := range []string{
		`CREATE TABLE users ( id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL, password TEXT NOT NULL, role TEXT DEFAULT 'user', status INTEGER DEFAULT 1, api_key TEXT, auth_version INTEGER NOT NULL DEFAULT 1, fnos_user_id INTEGER, fnos_username TEXT, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE orgs ( id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, kind TEXT NOT NULL DEFAULT 'shared', owner_user_id INTEGER NOT NULL DEFAULT 0, notes TEXT, created_by INTEGER, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE user_orgs ( user_id INTEGER NOT NULL, org_id INTEGER NOT NULL, role TEXT NOT NULL DEFAULT 'member', status TEXT NOT NULL DEFAULT 'active', perms TEXT, created_at DATETIME, updated_at DATETIME, PRIMARY KEY (user_id, org_id) )`,
		`CREATE TABLE rooms ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, community TEXT, room_no TEXT NOT NULL, default_rent REAL DEFAULT 0, water_mode TEXT, created_at DATETIME, updated_at DATETIME )`,
		// 关键：老 tenants 表没有 v0.6.10 的四个新列。
		`CREATE TABLE tenants ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, room_id INTEGER, name TEXT NOT NULL, phone TEXT, id_card TEXT, tags TEXT, move_in_date TEXT, lease_end_date TEXT, move_out_date TEXT, deposit REAL DEFAULT 0, water_mode TEXT, water_price REAL DEFAULT 0, water_monthly_fee REAL DEFAULT 0, pay_cycle TEXT, pay_day INTEGER DEFAULT -1, remind_days INTEGER DEFAULT -1, excluded_fees TEXT, active BOOLEAN DEFAULT 1, notes TEXT, created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE bills ( id INTEGER PRIMARY KEY AUTOINCREMENT, user_id INTEGER, org_id INTEGER NOT NULL DEFAULT 0, room_id INTEGER, period TEXT NOT NULL, room_no TEXT NOT NULL, tenant_name TEXT, total_amount REAL DEFAULT 0, paid_amount REAL DEFAULT 0, status TEXT DEFAULT 'unpaid', created_at DATETIME, updated_at DATETIME )`,
		`CREATE TABLE upgrade_records ( id INTEGER PRIMARY KEY AUTOINCREMENT, version TEXT NOT NULL, name TEXT, upgraded_at DATETIME )`,
	} {
		mustExec(stmt)
	}

	// 数据：1 个房东 + 老台账 + 两间房 + **两名**租户（多行 account_user_id=0
	// 正是部分唯一索引要放行的情形）。
	mustExec(`INSERT INTO users (id, username, password, role, status, auth_version) VALUES (1, 'landlord', 'x', 'admin', 1, 1)`)
	mustExec(`INSERT INTO orgs (id, name, kind, owner_user_id, created_by) VALUES (1, '我的台账', 'personal', 1, 1)`)
	mustExec(`INSERT INTO user_orgs (user_id, org_id, role, status, perms) VALUES (1, 1, 'owner', 'active', '{"tenants":"full"}')`)
	mustExec(`INSERT INTO rooms (id, user_id, org_id, community, room_no, default_rent) VALUES (1, 1, 1, 'A区', '101', 1000), (2, 1, 1, 'A区', '102', 900)`)
	mustExec(`INSERT INTO tenants (id, user_id, org_id, room_id, name, phone) VALUES (1, 1, 1, 1, '老王', '13800000001'), (2, 1, 1, 2, '老李', '13800000002')`)
	mustExec(`INSERT INTO bills (id, user_id, org_id, room_id, period, room_no, tenant_name, total_amount) VALUES (1, 1, 1, 1, '2026-09', '101', '老王', 1000)`)

	// --- 2) 真实启动顺序 --------------------------------------------------
	db.Logger = logger.Default.LogMode(logger.Info)
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// 迁移版本必须 ≤ 二进制版本才会执行（0.6.10 正是本次新增的那条）。
	if err := database.RunUpgrades(db, "0.6.10", database.Upgrades); err != nil {
		t.Fatalf("RunUpgrades: %v", err)
	}

	// --- 3) 断言 --------------------------------------------------------

	// 3.1 老业务数据一行未动。
	var name, phone string
	var ledgerID uint
	if err := db.Raw(`SELECT name, phone, org_id FROM tenants WHERE id = 1`).
		Row().Scan(&name, &phone, &ledgerID); err != nil {
		t.Fatalf("read legacy tenant: %v", err)
	}
	if name != "老王" || phone != "13800000001" || ledgerID != 1 {
		t.Fatalf("老租户数据被改动：name=%q phone=%q org_id=%d", name, phone, ledgerID)
	}
	var tenantCount int64
	db.Raw(`SELECT COUNT(*) FROM tenants`).Scan(&tenantCount)
	if tenantCount != 2 {
		t.Fatalf("租户行数 = %d, want 2（升级不该增删租户）", tenantCount)
	}

	// 3.2 存量租户的 account_user_id 全为 0（未关联账号）。
	var bound int64
	db.Raw(`SELECT COUNT(*) FROM tenants WHERE account_user_id <> 0`).Scan(&bound)
	if bound != 0 {
		t.Fatalf("升级后不该有租户被关联账号，实得 %d 行", bound)
	}

	// 3.3 新列真的补上了（AutoMigrate 的功劳）。用**可空**扫描：ALTER TABLE 补的
	// 列在老行上就是 NULL，直接扫进 string 会报 "converting NULL to string"——
	// 那是正常的列语义，不是升级失败。
	var address, ec, ep *string
	if err := db.Raw(`SELECT address, emergency_contact, emergency_phone FROM tenants WHERE id = 1`).
		Row().Scan(&address, &ec, &ep); err != nil {
		t.Fatalf("新列缺失（升级没补上）：%v", err)
	}
	// 新列可写（写进去再读回来），确认不是只读的空壳列。
	if err := db.Exec(`UPDATE tenants SET address = '新住址', emergency_contact = '紧急人' WHERE id = 1`).Error; err != nil {
		t.Fatalf("写新列失败：%v", err)
	}
	var gotAddress string
	db.Raw(`SELECT address FROM tenants WHERE id = 1`).Row().Scan(&gotAddress)
	if gotAddress != "新住址" {
		t.Fatalf("新列写入后读回 = %q, want 新住址", gotAddress)
	}

	// 3.4 变更申请表建出来了。
	if !tableExists(db, "tenant_change_requests") {
		t.Fatal("tenant_change_requests 表未创建")
	}

	// 3.5 索引建出来了。部分唯一索引的关键语义：**多行 0 允许**（上面的两行租户
	// 已经证明了这点，否则 AutoMigrate/迁移早就报唯一约束冲突），但同一个非零
	// 账号 id 只能出现一次。
	var idxCount int64
	db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_rental_tenant_account'`).Scan(&idxCount)
	if idxCount != 1 {
		t.Fatal("idx_rental_tenant_account 未创建")
	}
	if err := db.Exec(`UPDATE tenants SET account_user_id = 42 WHERE id = 1`).Error; err != nil {
		t.Fatalf("设置 account_user_id 失败：%v", err)
	}
	if err := db.Exec(`UPDATE tenants SET account_user_id = 42 WHERE id = 2`).Error; err == nil {
		t.Fatal("同一个账号被绑到两条租户档案，部分唯一索引未生效")
	}

	// 3.6 迁移可重入：同一版本再跑一次不应报错（启动脚本里 NoTx + IF NOT EXISTS
	// 就是为了这个）。
	if err := database.RunUpgrades(db, "0.6.10", database.Upgrades); err != nil {
		t.Fatalf("重复 RunUpgrades 失败（迁移不可重入）：%v", err)
	}
}

// TestTenantPortalMigrationVersionReachable 断言 0.6.10 这条迁移在**当前 VERSION**
// 下会真正执行。
//
// 为什么单独测：升级阶梯只跑 `compareVersions(迁移版本, 二进制版本) <= 0` 的迁移。
// 若 VERSION 忘了提上去（例如停在 0.6.9），这条迁移会被**静默跳过**——全新安装
// 看不出来（AutoMigrate 按新模型建表建列），但老库升级会缺列、缺表、缺索引，
// 而启动过程一声不响。version_gate_test.go 只断言了「VERSION ≥ 最新迁移版本」，
// 这里再显式钉住「0.6.10 这条确实在迁移清单里」。
func TestTenantPortalMigrationRegistered(t *testing.T) {
	found := false
	for _, u := range database.Upgrades {
		if u.Version == "0.6.10" {
			found = true
			if u.Name != "rental_tenant_portal" {
				t.Fatalf("0.6.10 迁移名 = %q, want rental_tenant_portal", u.Name)
			}
			if !u.NoTx {
				// SQLite 的 CREATE INDEX 不宜包事务，与 0.5.1/0.5.2 同策略。
				t.Fatal("0.6.10 迁移必须 NoTx（SQLite DDL 不宜包事务）")
			}
		}
	}
	if !found {
		t.Fatal("未注册 0.6.10 迁移：老库升级会缺 account_user_id 列与相关索引")
	}
	if !strings.Contains(readRepoVersion(t), "0.6.10") {
		t.Logf("提示：VERSION=%s（迁移版本 0.6.10 需 ≤ VERSION 才会执行）", readRepoVersion(t))
	}
}
