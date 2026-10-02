// migrate_shared_test.go — 0.3.3 共享化迁移的验收：去重、唯一索引、设置搬家。
package rental

import (
	"path/filepath"
	"testing"

	"smallgo/server/database"
	"smallgo/server/sysconfig"

	"gorm.io/gorm"
)

// runSharedDataUpgrade 找到 0.3.3 迁移并执行。
func runSharedDataUpgrade(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, up := range database.Upgrades {
		if up.Version == "0.3.3" {
			if err := up.Upgrade(db); err != nil {
				t.Fatalf("run 0.3.3 upgrade: %v", err)
			}
			return
		}
	}
	t.Fatal("0.3.3 upgrade not registered")
}

// TestSharedDataMigration 复刻隔离期两个用户各建一套同房号数据的场景：
// 迁移后合并成一套共享台账，引用完整、收款不丢、唯一索引生效、设置进系统级。
func TestSharedDataMigration(t *testing.T) {
	db, err := database.InitDB(filepath.Join(t.TempDir(), "mig.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatal(err)
	}

	// 隔离期数据：用户 1（管理员）与用户 2 各建了 A区/101。
	room1 := Room{UserID: 1, Community: "A区", RoomNo: "101", DefaultRent: 1000}
	room2 := Room{UserID: 2, Community: "A区", RoomNo: "101", DefaultRent: 800}
	if err := db.Create(&room1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&room2).Error; err != nil {
		t.Fatal(err)
	}
	tenant := Tenant{UserID: 2, RoomID: room2.ID, Name: "乙租客", Active: true}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	// 两张同房同月账单：用户 1 的有 100 元收款流水，用户 2 的没收款。
	bill1 := Bill{UserID: 1, RoomID: room1.ID, Period: "2026-09", RoomNo: "101",
		Rent: 1000, TotalAmount: 1000, PaidAmount: 100, Status: billStatusPartial}
	bill2 := Bill{UserID: 2, RoomID: room2.ID, Period: "2026-09", RoomNo: "101",
		Rent: 800, TotalAmount: 800, Status: billStatusUnpaid}
	if err := db.Create(&bill1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&bill2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&Payment{UserID: 1, BillID: bill1.ID, Amount: 100, Items: "[]"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&MeterRecord{UserID: 1, RoomID: room1.ID, Period: "2026-09", Water: 10}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&MeterRecord{UserID: 2, RoomID: room2.ID, Period: "2026-09", Water: 12}).Error; err != nil {
		t.Fatal(err)
	}
	// 隔离期两套内置收费项目种子。
	for _, seed := range defaultFeeItems() {
		row := seed
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	row := defaultFeeItems()[0]
	row.UserID = 2
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	// 用户级租房设置。
	if err := db.Create(&database.SystemConfig{UserID: 1, Key: "rental_water_price", Value: "6.50"}).Error; err != nil {
		t.Fatal(err)
	}
	// 生产启动顺序是 RunUpgrades 在 InitDefaultConfigs 之前，升级那一刻系统级
	// 还没有 rental_* 行；这里删掉测试初始化种下的默认行来复刻该状态。
	if err := db.Where("`key` LIKE 'rental_%' AND user_id = 0").Delete(&database.SystemConfig{}).Error; err != nil {
		t.Fatal(err)
	}

	runSharedDataUpgrade(t, db)

	// 房源合并为一间，租户与账单都指向它。
	var rooms []Room
	if err := db.Find(&rooms).Error; err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || rooms[0].ID != room1.ID {
		t.Fatalf("rooms after merge = %+v, want single room id %d", rooms, room1.ID)
	}
	var tenantCount int64
	db.Model(&Tenant{}).Where("room_id = ?", room1.ID).Count(&tenantCount)
	if tenantCount != 1 {
		t.Fatalf("tenant references = %d, want 1 (repointed)", tenantCount)
	}

	// 账单合并：保留有收款的一张，流水并入、已收按流水重算。
	var bills []Bill
	if err := db.Find(&bills).Error; err != nil {
		t.Fatal(err)
	}
	if len(bills) != 1 {
		t.Fatalf("bills after merge = %d, want 1", len(bills))
	}
	if !almostEqual(bills[0].PaidAmount, 100) {
		t.Fatalf("merged paid = %v, want 100 (payments preserved)", bills[0].PaidAmount)
	}
	var payCount int64
	db.Model(&Payment{}).Count(&payCount)
	if payCount != 1 {
		t.Fatalf("payments after merge = %d, want 1 (no money lost)", payCount)
	}

	// 抄表去重保留最早一条；收费项目只剩一套内置。
	var meterCount, feeCount int64
	db.Model(&MeterRecord{}).Count(&meterCount)
	db.Model(&FeeItem{}).Count(&feeCount)
	if meterCount != 1 {
		t.Fatalf("meter records = %d, want 1", meterCount)
	}
	if feeCount != int64(len(defaultFeeItems())) {
		t.Fatalf("fee items = %d, want %d", feeCount, len(defaultFeeItems()))
	}

	// 设置进系统级：user_id=0 行存在、用户级行清空。
	sysValue, err := sysconfig.GetConfig(db, "rental_water_price", 0)
	if err != nil || sysValue != "6.50" {
		t.Fatalf("system-scope water price = %q err %v, want 6.50", sysValue, err)
	}
	var userScope int64
	db.Model(&database.SystemConfig{}).Where("`key` = ? AND user_id > 0", "rental_water_price").Count(&userScope)
	if userScope != 0 {
		t.Fatalf("user-scope rows = %d, want 0", userScope)
	}

	// 唯一索引生效：重复 (小区, 房号) 与重复 (房间, 账期) 都写不进去。
	if err := db.Create(&Room{UserID: 2, Community: "A区", RoomNo: "101"}).Error; err == nil {
		t.Fatal("duplicate room must be rejected by unique index")
	}
	if err := db.Create(&Bill{UserID: 2, RoomID: room1.ID, Period: "2026-09", RoomNo: "101"}).Error; err == nil {
		t.Fatal("duplicate bill must be rejected by unique index")
	}
}
