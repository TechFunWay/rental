// cycle.go — 缴费周期(月付/季付)与缴费提醒的口径计算。
//
// 房间按 PayCycle 决定多久出一张账单:月付每月一张,季付每 3 个月一张,
// 账期与该房首张账单的月份对齐。PayDay 是每期收租的"几号",配合
// RemindDays(提前提醒天数)在总览与每日推送中生成缴费提醒。
package rental

import (
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 缴费周期:monthly 月付(默认)、quarterly 季付。
const (
	payCycleMonthly   = "monthly"
	payCycleQuarterly = "quarterly"

	// 缴费日允许的取值范围:1-28 号,0 表示不启用缴费提醒。
	// 不放行 29-31 号,避免大小月带来的"缴费日不存在"歧义。
	payDayMax = 28

	// 房间级提前提醒天数的取值范围;-1 表示跟随全局默认。
	remindDaysMin = -1
	remindDaysMax = 30

	// 全局默认提前提醒天数(偏好设置 → 租房设置 → 缴费提前提醒天数)。
	defaultRemindDays = 3
)

// normalizePayCycle 规范化缴费周期,空值与未知值一律按月付处理。
func normalizePayCycle(cycle string) string {
	if strings.TrimSpace(cycle) == payCycleQuarterly {
		return payCycleQuarterly
	}
	return payCycleMonthly
}

// cycleMonths 一个账单周期覆盖的月数:月付 1、季付 3。
func cycleMonths(cycle string) int {
	if normalizePayCycle(cycle) == payCycleQuarterly {
		return 3
	}
	return 1
}

// cycleLabel 缴费周期的中文名(导出 CSV 与提醒文案用)。
func cycleLabel(cycle string) string {
	if normalizePayCycle(cycle) == payCycleQuarterly {
		return "季付"
	}
	return "月付"
}

// parsePayCycle 解析用户输入("季付"/"quarterly" 均可),
// 未识别或未填写返回空串,由调用方决定缺省口径。
func parsePayCycle(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case payCycleQuarterly, "季付":
		return payCycleQuarterly
	case payCycleMonthly, "月付":
		return payCycleMonthly
	default:
		return ""
	}
}

// coveredPeriodText 季付账单覆盖的月份区间文案："2026-10 ~ 2026-12"；
// 月付返回单月账期本身。
func coveredPeriodText(period string, cycle string) string {
	if normalizePayCycle(cycle) != payCycleQuarterly {
		return period
	}
	return period + " ~ " + addMonths(period, 2)
}

// parsePeriod 把 YYYY-MM 解析成当月 1 号零点。
func parsePeriod(p string) (time.Time, error) {
	return time.ParseInLocation("2006-01", p, time.Local)
}

// addMonths 账期加 n 个月(基于 1 号,不存在月末溢出问题);解析失败返回原值。
func addMonths(p string, n int) string {
	t, err := parsePeriod(p)
	if err != nil {
		return p
	}
	return t.AddDate(0, n, 0).Format("2006-01")
}

// monthsBetween 两个账期相差的月数(b 比 a 大多少个月,可为负)。
// 任一头解析失败返回 0。
func monthsBetween(a, b string) int {
	ta, err := parsePeriod(a)
	if err != nil {
		return 0
	}
	tb, err := parsePeriod(b)
	if err != nil {
		return 0
	}
	return (tb.Year()-ta.Year())*12 + int(tb.Month()) - int(ta.Month())
}

// lastBillPeriod 返回房间最近一张账单的账期;没有账单时 ok=false。
func lastBillPeriod(db *gorm.DB, room *Room) (string, Bill, bool) {
	var last Bill
	if err := db.Where("room_id = ?", room.ID).
		Order("period DESC").First(&last).Error; err != nil {
		return "", Bill{}, false
	}
	return last.Period, last, true
}

// onBillSchedule 判断房间在 period 这个月是否应出账。
// 收费项目按各自周期出账后，读数类（水/电/燃气）恒按月收取，因此只要房间
// 在租，每个月都可能有到期项目（季付房的中间月是"月账单"——只有水电等
// 月付项目；锚点月是"季账单"——固定费一次收 3 个月）。固定费项目自身的
// 到期判断见 fixedItemDue，这里不再按整单过滤。
func onBillSchedule(db *gorm.DB, room *Room, period string) bool {
	// 收费项目周期化后每个月都可能有到期项目，账单按月出（内容随项目到期情况而定）。
	return true
}
func onBillScheduleLegacy(db *gorm.DB, room *Room, period string) bool {
	if effectiveRoomBilling(db, room).PayCycle != payCycleQuarterly {
		return true
	}
	lastPeriod, _, ok := lastBillPeriod(db, room)
	if !ok {
		return true
	}
	diff := monthsBetween(lastPeriod, period)
	return diff > 0 && diff%cycleMonths(payCycleQuarterly) == 0
}

// PaymentDueItem 缴费提醒的一行:仪表盘「缴费提醒」卡片与每日推送共用。
type PaymentDueItem struct {
	RoomID         uint    `json:"room_id"`
	RoomNo         string  `json:"room_no"`
	Community      string  `json:"community"`
	TenantName     string  `json:"tenant_name"`
	PayCycle       string  `json:"pay_cycle"`
	PayDay         int     `json:"pay_day"`
	Period         string  `json:"period"`          // 下一个待收账期
	Months         int     `json:"months"`          // 该账期覆盖的月数
	DueDate        string  `json:"due_date"`        // 缴费日 YYYY-MM-DD
	DaysLeft       int     `json:"days_left"`       // 距缴费日天数,负数=已过期
	ExpectedAmount float64 `json:"expected_amount"` // 预计应缴(固定费用×月数,水电按抄表另计)
	BillID         uint    `json:"bill_id"`         // 该账期已有账单 id,0=未开票
}

// nextPaymentDue 计算房间下一次该收租的账期与缴费日;未启用提醒(生效缴费日
// =0)或没有在租租户时返回 nil。cfg 是该房生效的计费与缴费设置。
//
// 口径:取最近一张账单——未收清则它就是待收账期;已收清则下一期是
// 它加一个周期(下一期若也已收清,继续向后推进,兼容提前补账的场景);
// 没有任何账单时以待收账期为当前月。
func nextPaymentDue(db *gorm.DB, room *Room, cfg billingConfig, today time.Time) *PaymentDueItem {
	if cfg.PayDay < 1 || cfg.PayDay > payDayMax {
		return nil
	}
	// 归一到今天零点,让 DaysLeft 是精确的整天差(与租约到期提醒同口径)。
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	var tenants int64
	db.Model(&Tenant{}).Where("room_id = ? AND active = ?", room.ID, true).Count(&tenants)
	if tenants == 0 {
		return nil
	}

	months := cycleMonths(cfg.PayCycle)
	lastPeriod, last, hasLast := lastBillPeriod(db, room)
	period := today.Format("2006-01")
	if hasLast {
		// 最多向后推进 12 个账单,防止脏数据导致死循环。
		for i := 0; i < 12; i++ {
			if last.Status != billStatusPaid {
				period = last.Period
				break
			}
			next := addMonths(last.Period, months)
			var nb Bill
			if err := db.Where("room_id = ? AND period = ?",
				room.ID, next).First(&nb).Error; err != nil {
				period = next
				break
			}
			if nb.Status != billStatusPaid {
				period = next
				break
			}
			last = nb
			lastPeriod = nb.Period
		}
		// 全部已收清(推进 12 次仍未跳出):回到最近账单的下一期。
		if last.Status == billStatusPaid {
			period = addMonths(lastPeriod, months)
		}
	}

	due, err := parsePeriod(period)
	if err != nil {
		return nil
	}
	dueDate := time.Date(due.Year(), due.Month(), cfg.PayDay, 0, 0, 0, 0, time.Local)

	item := &PaymentDueItem{
		RoomID:     room.ID,
		RoomNo:     room.RoomNo,
		Community:  room.Community,
		TenantName: activeTenantNames(db, room.ID),
		PayCycle:   cfg.PayCycle,
		PayDay:     cfg.PayDay,
		Period:     period,
		Months:     months,
		DueDate:    dueDate.Format("2006-01-02"),
		DaysLeft:   int(dueDate.Sub(today).Hours() / 24),
	}
	item.ExpectedAmount = expectedBillAmount(room, cfg)
	var existing Bill
	if err := db.Where("room_id = ? AND period = ?",
		room.ID, period).First(&existing).Error; err == nil {
		item.BillID = existing.ID
	}
	return item
}

// expectedBillAmount 预计应缴:租金/卫生费/管理费等固定费项目按月数放大;
// 水电（含包月水费）属于按月收取的项目，包月只按每月金额计，按吨按抄表另计，
// 都不随季付放大——收费周期按项目划分后水电每期单独收。
func expectedBillAmount(room *Room, cfg billingConfig) float64 {
	months := float64(cycleMonths(cfg.PayCycle))
	amount := (room.DefaultRent + room.DefaultSanitationFee + room.DefaultManagementFee) * months
	if cfg.WaterMode == waterModeMonthly {
		amount += cfg.WaterAmount
	}
	return round2(amount)
}

func clampRemindDays(days int) int {
	if days < 0 {
		return 0
	}
	if days > remindDaysMax {
		return remindDaysMax
	}
	return days
}

// computePaymentDue 汇总全部待提醒的缴费项（数据共享后只有一套台账）:
// 已逾期恒提醒,未逾期只在提前提醒窗口内出现。按缴费日升序,最多 20 条。
// 只有存在在租租户的房间才可能有缴费提醒（缴费设置挂在租户上）。
func computePaymentDue(db *gorm.DB, today time.Time) []PaymentDueItem {
	items := make([]PaymentDueItem, 0)
	var rooms []Room
	if err := db.Where(
		"id IN (SELECT room_id FROM tenants WHERE active = ?)", true).Find(&rooms).Error; err != nil {
		return items
	}
	for i := range rooms {
		cfg := effectiveRoomBilling(db, &rooms[i])
		item := nextPaymentDue(db, &rooms[i], cfg, today)
		if item == nil {
			continue
		}
		if item.DaysLeft > cfg.RemindDays {
			continue
		}
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DueDate != items[j].DueDate {
			return items[i].DueDate < items[j].DueDate
		}
		return items[i].RoomNo < items[j].RoomNo
	})
	if len(items) > 20 {
		items = items[:20]
	}
	return items
}
