// migrate_shared.go — 0.3.3「业务数据全员共享」迁移的两个辅助步骤。
//
// 数据此前按 user_id 隔离，共享化后唯一键不再含 user_id：不同录入人可能
// 建过同小区同房号的房源、同房同月的账单与抄表、同 key 的收费项目，
// 必须先并重复行才能重建唯一索引。金额类合并以收款流水为准，避免丢钱。
package rental

import (
	"time"

	"smallgo/server/database"

	"gorm.io/gorm"
)

// systemScopeConfigKeys 0.3.3 起从用户级偏好升为系统级共享的租房设置。
var systemScopeConfigKeys = []string{
	"rental_water_price", "rental_water_mode", "rental_water_monthly_fee",
	"rental_pay_cycle", "rental_pay_day",
	"rental_elec_price", "rental_gas_price", "rental_remind_days",
	"rental_property_name", "rental_contact", "rental_receipt_note",
}

// migrateConfigsToSystem 把租房设置从用户级升为系统级：每个 key 取登记最早
// 用户（通常是管理员）的值写入 user_id=0，再删除全部用户级行。系统级已有
// 值时（重复升级等）只清用户级，不覆盖。
func migrateConfigsToSystem(db *gorm.DB) error {
	for _, key := range systemScopeConfigKeys {
		var rows []database.SystemConfig
		if err := db.Where("`key` = ? AND user_id > 0", key).Order("user_id ASC").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		var sysCount int64
		if err := db.Model(&database.SystemConfig{}).
			Where("`key` = ? AND user_id = 0", key).Count(&sysCount).Error; err != nil {
			return err
		}
		if sysCount == 0 {
			if err := db.Create(&database.SystemConfig{Key: key, Value: rows[0].Value}).Error; err != nil {
				return err
			}
		}
		if err := db.Where("`key` = ? AND user_id > 0", key).Delete(&database.SystemConfig{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// dedupeSharedData 共享化去重：房源按 (小区, 房号) 并（引用改指向最早一间），
// 账单按 (房间, 账期) 并（收款流水并入保留账单、已收按流水重算），
// 抄表与收费项目按各自业务键保留最早一条。
func dedupeSharedData(db *gorm.DB) error {
	// 房源：同 (community, room_no) 保留最早一间，三类引用改指向后删除其余。
	var roomDups []struct {
		Community string
		RoomNo    string
		KeepID    uint
	}
	if err := db.Raw(`SELECT community, room_no, MIN(id) AS keep_id FROM rooms ` +
		`GROUP BY community, room_no HAVING COUNT(*) > 1`).Scan(&roomDups).Error; err != nil {
		return err
	}
	for _, d := range roomDups {
		var ids []uint
		if err := db.Model(&Room{}).
			Where("community = ? AND room_no = ? AND id <> ?", d.Community, d.RoomNo, d.KeepID).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			for _, stmt := range []string{
				"UPDATE tenants SET room_id = ? WHERE room_id = ?",
				"UPDATE bills SET room_id = ? WHERE room_id = ?",
				"UPDATE meter_records SET room_id = ? WHERE room_id = ?",
			} {
				if err := db.Exec(stmt, d.KeepID, id).Error; err != nil {
					return err
				}
			}
			if err := db.Delete(&Room{}, id).Error; err != nil {
				return err
			}
		}
	}

	// 账单：同 (room_id, period) 保留已收最多的一张，其余账单的收款流水并入
	// 保留账单、明细快照删除，已收金额按流水重算（账单是快照、缴费是流水，
	// 钱以流水为准），状态随之联动。
	var billDups []struct {
		RoomID uint
		Period string
	}
	if err := db.Raw(`SELECT room_id, period FROM bills ` +
		`GROUP BY room_id, period HAVING COUNT(*) > 1`).Scan(&billDups).Error; err != nil {
		return err
	}
	for _, d := range billDups {
		var bills []Bill
		if err := db.Where("room_id = ? AND period = ?", d.RoomID, d.Period).
			Order("id ASC").Find(&bills).Error; err != nil {
			return err
		}
		keep := bills[0]
		for _, b := range bills[1:] {
			if b.PaidAmount > keep.PaidAmount {
				keep = b
			}
		}
		for _, b := range bills {
			if b.ID == keep.ID {
				continue
			}
			if err := db.Model(&Payment{}).Where("bill_id = ?", b.ID).Update("bill_id", keep.ID).Error; err != nil {
				return err
			}
			if err := db.Where("bill_id = ?", b.ID).Delete(&BillItem{}).Error; err != nil {
				return err
			}
			if err := db.Delete(&Bill{}, b.ID).Error; err != nil {
				return err
			}
		}
		var paid float64
		if err := db.Model(&Payment{}).Where("bill_id = ?", keep.ID).
			Select("COALESCE(SUM(amount), 0)").Scan(&paid).Error; err != nil {
			return err
		}
		keep.PaidAmount = round2(paid)
		if keep.PaidAmount > feeTolerance && keep.PaidAt == nil {
			now := time.Now()
			keep.PaidAt = &now
		}
		keep.recalc()
		if err := db.Save(&keep).Error; err != nil {
			return err
		}
		syncBillItems(db, &keep, nil)
	}

	// 抄表：同 (room_id, period) 保留最早一条，其余删除（同房同月的重复读数
	// 本就是冗余录入）。
	var meterDups []struct {
		RoomID uint
		Period string
	}
	if err := db.Raw(`SELECT room_id, period FROM meter_records ` +
		`GROUP BY room_id, period HAVING COUNT(*) > 1`).Scan(&meterDups).Error; err != nil {
		return err
	}
	for _, d := range meterDups {
		var ids []uint
		if err := db.Model(&MeterRecord{}).
			Where("room_id = ? AND period = ?", d.RoomID, d.Period).
			Order("id ASC").Limit(-1).Offset(1).Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := db.Where("id IN ?", ids).Delete(&MeterRecord{}).Error; err != nil {
				return err
			}
		}
	}

	// 收费项目：同 key 保留最早一条（内置项目 key 固定，重复只可能来自
	// 隔离期不同用户的种子）。
	var feeDups []struct {
		Key    string
		KeepID uint
	}
	if err := db.Raw(`SELECT ` + "`key`" + `, MIN(id) AS keep_id FROM fee_items ` +
		`GROUP BY ` + "`key`" + ` HAVING COUNT(*) > 1`).Scan(&feeDups).Error; err != nil {
		return err
	}
	for _, d := range feeDups {
		var ids []uint
		if err := db.Model(&FeeItem{}).Where("`key` = ? AND id <> ?", d.Key, d.KeepID).
			Pluck("id", &ids).Error; err != nil {
			return err
		}
		if len(ids) > 0 {
			if err := db.Where("id IN ?", ids).Delete(&FeeItem{}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}
