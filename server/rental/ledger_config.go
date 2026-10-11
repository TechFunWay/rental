// ledger_config.go — 租房设置按台账隔离（v0.6.0）。
//
// 背景：水价、电价、气价、计费方式、缴费周期、缴费日、提醒天数、单据抬头
// 这 11 个 rental_* 键在 v0.3.3 起是**系统级**（全员一套）。数据按台账隔离后
// 各台账的水电单价本就不同，共用一套会互相干扰。
//
// 实现取舍：**不改 sysconfig 框架**（它属 framework-owned，要与 smallgo 基线
// 保持一致），也**不新增表**，而是复用 system_configs 的 (user_id, key) 唯一键，
// 把「台账覆盖值」存在一个保留的 user_id 段里：
//
//	user_id = ledgerConfigBase + ledgerID   （ledgerConfigBase 取一个远大于真实用户 ID
//	                                   的基数，避免和真实用户撞号）
//
// 读取口径（对一个台账 ledgerID 求某 key 的生效值）：
//  1. 若该台账有覆盖行 → 用它；
//  2. 否则回退到系统级（user_id=0）；
//  3. 再否则用代码默认值。
//
// 这样：
//   - 0 号公共台账**不需要**任何覆盖行，自然继续读系统级值 → 老用户零感知；
//   - 每个台账可各自覆盖，互不影响；
//   - 不新增表、不动框架，迁移面最小。
package rental

import (
	"strconv"
	"strings"

	"smallgo/server/database"

	"gorm.io/gorm"
)

// ledgerConfigBase 是「台账覆盖值」在 system_configs.user_id 列上的基数。
//
// 为什么取 1<<40：真实用户 ID 是自增整数，实际部署不可能接近这个量级；
// 用它做加法偏移，既能保持 (user_id, key) 唯一键的天然约束，
// 又能保证永远不会与真实用户的配置行撞上。若将来真有人口爆炸到
// 万亿级用户，请改成单独一张表（那时也更该改表了）。
const ledgerConfigBase uint = 1 << 40

// ledgerConfigUserID 台账覆盖值在 system_configs 里占用的 user_id。
func ledgerConfigUserID(ledgerID uint) uint {
	return ledgerConfigBase + ledgerID
}

// ledgerConfigValue 读某个台账在某 key 上的**覆盖值**；无覆盖时 ok=false。
func ledgerConfigValue(db *gorm.DB, key string, ledgerID uint) (string, bool) {
	var row database.SystemConfig
	err := db.Where("user_id = ? AND `key` = ?", ledgerConfigUserID(ledgerID), key).
		First(&row).Error
	if err != nil {
		return "", false
	}
	return row.Value, true
}

// setLedgerConfigValue 写某个台账的覆盖值（upsert）。
func setLedgerConfigValue(db *gorm.DB, key, value string, ledgerID uint) error {
	uid := ledgerConfigUserID(ledgerID)
	var row database.SystemConfig
	err := db.Where("user_id = ? AND `key` = ?", uid, key).First(&row).Error
	if err == nil {
		return db.Model(&database.SystemConfig{}).Where("id = ?", row.ID).
			Update("value", value).Error
	}
	return db.Create(&database.SystemConfig{
		UserID: uid, Key: key, Value: value,
	}).Error
}

// clearLedgerConfigValue 删除某个台账的覆盖值（回退到系统级）。
func clearLedgerConfigValue(db *gorm.DB, key string, ledgerID uint) error {
	return db.Where("user_id = ? AND `key` = ?", ledgerConfigUserID(ledgerID), key).
		Delete(&database.SystemConfig{}).Error
}

// ledgerConfigString 字符串配置：台账覆盖 → 系统级 → 默认值。
func ledgerConfigString(db *gorm.DB, key string, ledgerID uint, def string) string {
	if v, ok := ledgerConfigValue(db, key, ledgerID); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return configString(db, key, 0, def)
}

// ledgerConfigInt 整数配置：台账覆盖 → 系统级 → 默认值。
func ledgerConfigInt(db *gorm.DB, key string, ledgerID uint, def int) int {
	if v, ok := ledgerConfigValue(db, key, ledgerID); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return configInt(db, key, 0, def)
}

// ledgerConfigFloat 浮点配置：台账覆盖 → 系统级 → 默认值。
func ledgerConfigFloat(db *gorm.DB, key string, ledgerID uint, def float64) float64 {
	if v, ok := ledgerConfigValue(db, key, ledgerID); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f >= 0 {
			return f
		}
	}
	return configFloat(db, key, 0, def)
}

// ledgerBillingDefaults 某台账生效的租房设置（供前端「跟随全局默认」预填）。
// 口径与 effectiveRoomBilling 的回退链一致。
type ledgerBillingDefaults struct {
	LedgerID        uint    `json:"ledger_id"`
	WaterMode       string  `json:"water_mode"`
	WaterPrice      float64 `json:"water_price"`
	WaterMonthlyFee float64 `json:"water_monthly_fee"`
	ElecPrice       float64 `json:"elec_price"`
	GasPrice        float64 `json:"gas_price"`
	PayCycle        string  `json:"pay_cycle"`
	PayDay          int     `json:"pay_day"`
	RemindDays      int     `json:"remind_days"`
	HasOverride     bool    `json:"has_override"` // 该台账是否有任何覆盖值
}

// ledgerBillingDefaultsFor 汇总某台账的生效计费默认值。
func ledgerBillingDefaultsFor(db *gorm.DB, ledgerID uint) ledgerBillingDefaults {
	out := ledgerBillingDefaults{
		LedgerID:        ledgerID,
		WaterMode:       normalizeWaterMode(ledgerConfigString(db, "rental_water_mode", ledgerID, waterModeMeter)),
		WaterPrice:      ledgerConfigFloat(db, "rental_water_price", ledgerID, 5.0),
		WaterMonthlyFee: ledgerConfigFloat(db, "rental_water_monthly_fee", ledgerID, 0),
		ElecPrice:       ledgerConfigFloat(db, "rental_elec_price", ledgerID, 1.20),
		GasPrice:        ledgerConfigFloat(db, "rental_gas_price", ledgerID, 3.50),
		PayCycle:        normalizePayCycle(ledgerConfigString(db, "rental_pay_cycle", ledgerID, payCycleMonthly)),
		PayDay:          clampPayDay(ledgerConfigInt(db, "rental_pay_day", ledgerID, 0)),
		RemindDays:      clampRemindDays(ledgerConfigInt(db, "rental_remind_days", ledgerID, defaultRemindDays)),
	}
	for _, key := range rentalConfigKeys {
		if _, ok := ledgerConfigValue(db, key, ledgerID); ok {
			out.HasOverride = true
			break
		}
	}
	return out
}

// rentalConfigKeys 可被台账覆盖的租房设置键（与 registerConfigs 登记的 11 个一致）。
var rentalConfigKeys = []string{
	"rental_water_price", "rental_water_mode", "rental_water_monthly_fee",
	"rental_pay_cycle", "rental_pay_day",
	"rental_elec_price", "rental_gas_price", "rental_remind_days",
	"rental_property_name", "rental_contact", "rental_receipt_note",
}

// receiptHeaderFor 某台账的单据抬头（出租方名称/联系电话/底部备注）。
// 台账无覆盖时回退系统级，保证老库与公共台账的读数不变。
func receiptHeaderFor(db *gorm.DB, ledgerID uint) (name, contact, note string) {
	return ledgerConfigString(db, "rental_property_name", ledgerID, ""),
		ledgerConfigString(db, "rental_contact", ledgerID, ""),
		ledgerConfigString(db, "rental_receipt_note", ledgerID, "")
}
