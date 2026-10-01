package rental

import (
	"testing"

	"smallgo/server/database"
)

// TestWaterModeColumnUpgrade 模拟老库升级：水费计费方式列是后加的，
// 老库通过 AutoMigrate 补列，旧行必须读得出来且沿用原口径（按吨）。
func TestWaterModeColumnUpgrade(t *testing.T) {
	env := setupEnv(t)
	for _, stmt := range []string{
		"ALTER TABLE rooms DROP COLUMN water_mode",
		"ALTER TABLE bills DROP COLUMN water_mode",
	} {
		if err := env.db.Exec(stmt).Error; err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	// 老数据：不带新列写入。
	if err := env.db.Exec(`INSERT INTO rooms (user_id, community, room_no, default_rent, water_price)
		VALUES (1, '', '801', 1000, 5)`).Error; err != nil {
		t.Fatalf("insert old room: %v", err)
	}
	if err := env.db.Exec(`INSERT INTO bills (user_id, room_id, period, room_no, rent, water_last, water_now, water_price, total_amount, status)
		VALUES (1, 1, '2026-08', '801', 1000, 100, 120, 5, 1100, 'paid')`).Error; err != nil {
		t.Fatalf("insert old bill: %v", err)
	}
	if err := database.AutoMigrate(env.db); err != nil {
		t.Fatalf("remigrate: %v", err)
	}

	// 老房源：water_mode 空 = 跟随全局默认。
	var room Room
	if err := env.db.Where("room_no = ?", "801").First(&room).Error; err != nil {
		t.Fatalf("read old room: %v", err)
	}
	if room.WaterMode != "" {
		t.Fatalf("old room water_mode = %q, want 空（跟随全局）", room.WaterMode)
	}

	// 老账单：water_mode 兜底成按吨，水费仍是用量 × 单价（120-100）×5 = 100。
	var bill Bill
	if err := env.db.Where("period = ?", "2026-08").First(&bill).Error; err != nil {
		t.Fatalf("read old bill: %v", err)
	}
	if bill.WaterMode != waterModeMeter {
		t.Fatalf("old bill water_mode = %q, want meter", bill.WaterMode)
	}
	if got := bill.waterFee(); got != 100 {
		t.Fatalf("old bill waterFee = %v, want 100", got)
	}
}

// TestWaterModeBackfillFollowsAppVersion 验证水费计费方式兜底迁移跟随系统版本：
// 迁移版本号 = 发版时的应用版本（随 v0.2.1 发布，VERSION 同步改 0.2.1），只在
// "二进制版本 ≥ 迁移版本"时执行一次；旧版本二进制不会跑它（也不留记录）。
func TestWaterModeBackfillFollowsAppVersion(t *testing.T) {
	env := setupEnv(t)

	// 造一行房、一行账单，再把 water_mode 置 NULL：
	// 这是兜底迁移存在的最坏情况（补列后旧行读出 NULL）。
	if err := env.db.Exec(`INSERT INTO rooms (user_id, community, room_no) VALUES (1, '', '802')`).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	if err := env.db.Exec(`INSERT INTO bills (user_id, room_id, period, room_no, rent, water_mode, total_amount, status)
		VALUES (1, (SELECT id FROM rooms WHERE room_no = '802'), '2026-09', '802', 1000, 'meter', 1000, 'unpaid')`).Error; err != nil {
		t.Fatalf("seed bill: %v", err)
	}
	if err := env.db.Exec("UPDATE bills SET water_mode = NULL").Error; err != nil {
		t.Fatalf("null bill mode: %v", err)
	}
	if err := env.db.Exec("UPDATE rooms SET water_mode = NULL").Error; err != nil {
		t.Fatalf("null room mode: %v", err)
	}

	// 上一版二进制（v0.2.0，库里没有这条迁移）：0.2.1 迁移不执行、不记录。
	if err := database.RunUpgrades(env.db, "0.2.0", database.Upgrades); err != nil {
		t.Fatalf("run upgrades @0.2.0: %v", err)
	}
	var skipped database.UpgradeRecord
	if err := env.db.Where("version = ?", "0.2.1").First(&skipped).Error; err == nil {
		t.Fatal("migration 0.2.1 should not run on binary 0.2.0")
	}

	// 当前系统版本（v0.2.1）：迁移执行一次并留下记录，NULL 全部兜底。
	if err := database.RunUpgrades(env.db, "0.2.1", database.Upgrades); err != nil {
		t.Fatalf("run upgrades @0.2.1: %v", err)
	}
	var applied database.UpgradeRecord
	if err := env.db.Where("version = ?", "0.2.1").First(&applied).Error; err != nil {
		t.Fatalf("migration record missing: %v", err)
	}
	var nullBills, nullRooms int64
	env.db.Raw("SELECT COUNT(*) FROM bills WHERE water_mode IS NULL OR water_mode = ''").Scan(&nullBills)
	env.db.Raw("SELECT COUNT(*) FROM rooms WHERE water_mode IS NULL").Scan(&nullRooms)
	if nullBills != 0 || nullRooms != 0 {
		t.Fatalf("backfill left nulls: bills=%d rooms=%d, want 0/0", nullBills, nullRooms)
	}

	// 幂等：再跑一次不会重复执行，也不会报错。
	if err := database.RunUpgrades(env.db, "0.2.1", database.Upgrades); err != nil {
		t.Fatalf("re-run upgrades: %v", err)
	}
	var count int64
	env.db.Model(&database.UpgradeRecord{}).Where("version = ?", "0.2.1").Count(&count)
	if count != 1 {
		t.Fatalf("migration records = %d, want 1", count)
	}
}
