// billing.go — 房间生效的计费与缴费设置。
//
// v0.2.3 起水费计费方式（按吨/包月）、水费金额、缴费周期（月付/季付）、
// 缴费日与提前提醒天数都在租户上配置：每个租户可以不一样。租户没设置的项
// 回退到用户级全局默认（偏好设置 → 租房设置）。
//
// 账单仍然按房开（(user, room, period) 唯一），因此房间的生效设置取该房
// 在租租户的设置：同一房间有多名在租租户（宿舍场景）时，逐项取"第一个
// 显式设置"的租户（按登记先后，与账单租户名快照同序）。
package rental

import (
	"sort"
	"strconv"
	"strings"

	"smallgo/server/sysconfig"

	"gorm.io/gorm"
)

// billingConfig 房间当前生效的计费与缴费设置（各字段都已解析成确定值）。
type billingConfig struct {
	WaterMode   string  // meter 按吨 / monthly 包月
	WaterAmount float64 // 按吨=元/吨；包月=元/月
	PayCycle    string  // monthly 月付 / quarterly 季付
	PayDay      int     // 1-28；0=不提醒
	RemindDays  int     // 0-30
}

// excludedFeeSet 把排除项目 key 列表转成集合（重算与出账时查命中用）。
func excludedFeeSet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// normalizeExcludedFeeKeys 清洗入参的排除项目 key：去空白、去空项、去重。
func normalizeExcludedFeeKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// effectiveRoomExcludedFees 房间生效的"不参与计费项目"：取第一个设置了排除项
// 的在租租户（与 effectiveRoomBilling 的"第一个显式设置"同规则），key 排序
// 返回。没有租户设置过排除（或房间空置）时返回 nil = 全部项目参与。
func effectiveRoomExcludedFees(db *gorm.DB, room *Room) []string {
	for _, t := range activeRoomTenants(db, room.ID) {
		if len(t.ExcludedFees) > 0 {
			keys := normalizeExcludedFeeKeys(t.ExcludedFees)
			sort.Strings(keys)
			return keys
		}
	}
	return nil
}

// activeRoomTenants 房间的在租租户，按登记先后（id 升序）。
func activeRoomTenants(db *gorm.DB, roomID uint) []Tenant {
	tenants := make([]Tenant, 0, 4)
	db.Where("room_id = ? AND active = ?", roomID, true).Order("id ASC").Limit(20).Find(&tenants)
	return tenants
}

// tenantWaterAmount 租户对指定计费方式显式设置的金额；未设置返回 0。
func tenantWaterAmount(t Tenant, mode string) float64 {
	if normalizeWaterMode(mode) == waterModeMonthly {
		return t.WaterMonthlyFee
	}
	return t.WaterPrice
}

// configString 读取用户级字符串配置，缺失或空白时返回默认值。
func configString(db *gorm.DB, key string, userID uint, def string) string {
	v, err := sysconfig.GetConfig(db, key, userID)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// configInt 读取用户级整数配置，缺失或非法时返回默认值。
func configInt(db *gorm.DB, key string, userID uint, def int) int {
	v, err := sysconfig.GetConfig(db, key, userID)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return def
	}
	return n
}

// globalWaterAmount 全局默认水费金额：按吨读元/吨单价，包月读元/月金额。
func globalWaterAmount(db *gorm.DB, userID uint, mode string) float64 {
	if normalizeWaterMode(mode) == waterModeMonthly {
		return configFloat(db, "rental_water_monthly_fee", userID, 0)
	}
	return configFloat(db, "rental_water_price", userID, 5.0)
}

// effectiveRoomBilling 解析房间生效的计费与缴费设置：逐项"第一个显式设置的
// 在租租户" → 全局默认。没有在租租户的房间（空置/已退租）直接用全局默认。
func effectiveRoomBilling(db *gorm.DB, room *Room) billingConfig {
	tenants := activeRoomTenants(db, room.ID)
	userID := room.UserID

	// 水费计费方式：租户显式设置 → 全局默认（空值与未知值一律按吨）。
	mode := ""
	for _, t := range tenants {
		if m := parseWaterMode(t.WaterMode); m != "" {
			mode = m
			break
		}
	}
	if mode == "" {
		mode = normalizeWaterMode(configString(db, "rental_water_mode", userID, waterModeMeter))
	}

	// 水费金额随最终方式取：该方式下第一个有显式金额的租户 → 全局默认金额。
	amount := 0.0
	for _, t := range tenants {
		if v := tenantWaterAmount(t, mode); v > 0 {
			amount = v
			break
		}
	}
	if amount <= 0 {
		amount = globalWaterAmount(db, userID, mode)
	}

	// 缴费周期：租户显式设置 → 全局默认。
	cycle := ""
	for _, t := range tenants {
		if c := parsePayCycle(t.PayCycle); c != "" {
			cycle = c
			break
		}
	}
	if cycle == "" {
		cycle = normalizePayCycle(configString(db, "rental_pay_cycle", userID, payCycleMonthly))
	}

	// 缴费日：租户 0-28 的取值都算显式设置（0=不提醒）；-1 才回退全局。
	payDay := tenantFollowGlobal
	for _, t := range tenants {
		if t.PayDay >= 0 {
			payDay = t.PayDay
			break
		}
	}
	if payDay < 0 {
		payDay = clampPayDay(configInt(db, "rental_pay_day", userID, 0))
	}

	// 提前提醒天数：租户 0-30 的取值都算显式设置；-1 回退全局。
	remind := tenantFollowGlobal
	for _, t := range tenants {
		if t.RemindDays >= 0 {
			remind = t.RemindDays
			break
		}
	}
	if remind < 0 {
		remind = clampRemindDays(configInt(db, "rental_remind_days", userID, defaultRemindDays))
	}

	return billingConfig{
		WaterMode:   mode,
		WaterAmount: amount,
		PayCycle:    cycle,
		PayDay:      clampPayDay(payDay),
		RemindDays:  clampRemindDays(remind),
	}
}

// waterAmountForMode 指定计费方式下的水费金额：租户显式设置 → 全局默认。
// 账单里临时切换计费方式时用它取该方式的默认金额，避免沿用另一方式的金额。
func waterAmountForMode(db *gorm.DB, room *Room, mode string) float64 {
	for _, t := range activeRoomTenants(db, room.ID) {
		if v := tenantWaterAmount(t, mode); v > 0 {
			return v
		}
	}
	return globalWaterAmount(db, room.UserID, mode)
}

// clampPayDay 把缴费日收进 0-28（0=不提醒，负数按 0 处理）。
func clampPayDay(day int) int {
	if day < 0 {
		return 0
	}
	if day > payDayMax {
		return payDayMax
	}
	return day
}

// validateTenantBilling 校验租户的计费与缴费入参，返回空串表示通过。
// waterMode / payCycle 允许为空（跟随全局默认）。
func validateTenantBilling(waterMode string, waterPrice, waterMonthlyFee float64, payCycle string, payDay, remindDays int) string {
	if waterMode != "" && parseWaterMode(waterMode) == "" {
		return "水费计费方式无效，应为按吨（meter）或包月（monthly）"
	}
	if waterPrice < 0 || waterMonthlyFee < 0 {
		return "水费金额不能为负数"
	}
	if payCycle != "" && parsePayCycle(payCycle) == "" {
		return "缴费周期无效，应为月付（monthly）或季付（quarterly）"
	}
	if payDay < tenantFollowGlobal || payDay > payDayMax {
		return "缴费日应为 1-28 号，0 表示不提醒，-1 表示跟随全局默认"
	}
	if remindDays < remindDaysMin || remindDays > remindDaysMax {
		return "提前提醒天数应为 0-30，-1 表示跟随全局默认"
	}
	return ""
}
