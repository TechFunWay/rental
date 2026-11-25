// autobill.go — 到期自动生成账单（Gitee issue IKW41H 需求 4）。
//
// 粉丝原话：「租房交租日期到了，不管是否抄表，都生成交租账单。这个很重要，
// 先成账单，然后可以在账单里抄表或者通过集中抄表填写数值之后，账单再重新
// 生成加上水电煤气的账单。」
//
// 口径与手动「一键生成」完全一致：都走 generateBillForRoom 这条唯一建账出口，
// 水电燃气读数留空待抄，之后在账单里点「抄表」或在「抄表」模块集中录入读数，
// 金额直接回算到这张单上（recalc 链路）。因此自动出账不会算出第二套金额，
// 重复执行也不会出重复单（同房同月已有账单就跳过）。
//
// 两种触发方式，房东在「自动出账单」设置里二选一（2026-11-24 用户确认
// 「可以设置，满足上面两者」）：
//
//   - payday  按租户自己的缴费日出账：某租户的缴费日当天，为他的下一期待收
//             账期开单。各房缴费日不同也能各按各的节奏走，这是房东最常用的口径。
//   - monthly 每月固定一天统一出账：到设定的日子，给本次全部在租房间开当月账单。
//   - off     关闭（默认）。升级后老用户行为完全不变，必须房东主动打开。
//
// 台账隔离：模式与出账日都可按台账覆盖（与单价、缴费日同一条配置链），
// 一个台账开了自动出账不会影响别的台账。
package rental

import (
	"time"

	"gorm.io/gorm"

	"smallgo/server/logger"
	"smallgo/server/scheduler"
	"smallgo/server/sysconfig"
)

// 自动出账单配置键。两个键都登记进 rentalConfigKeys（见 ledger_config.go），
// 否则台账级覆盖会被设置接口的白名单挡掉。
const (
	autobillModeConfigKey = "rental_autobill_mode"
	autobillDayConfigKey  = "rental_autobill_day"

	autobillModeOff     = "off"
	autobillModePayDay  = "payday"
	autobillModeMonthly = "monthly"
)

func init() {
	// 每天凌晨跑一次。放在 00:30 而不是 00:00：跨零点瞬间日志与结算任务
	// （日终漏服、对账）都在跑，错开几分钟少一份争抢。
	scheduler.Register(scheduler.Job{
		Name:  "rental-auto-bill",
		Daily: "00:30",
		Run: func() {
			if appDB != nil {
				runAutoBill(appDB, time.Now())
			}
		},
	})
}

// registerAutoBillConfigs 登记自动出账单设置（系统级，可按台台账覆盖）。
func registerAutoBillConfigs() {
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key:     autobillModeConfigKey,
		Scope:   sysconfig.ScopeSystem,
		Type:    sysconfig.TypeSelect,
		Default: autobillModeOff,
		Options: []string{autobillModePayDay, autobillModeMonthly, autobillModeOff},
		Group:   "autobill", Label: "自动出账单",
		Description: "关闭时保持原样；打开后到日子自动开当月账单（读数留空待抄，抄表后金额自动回算），已有账单的月份不会重复开",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key:     autobillDayConfigKey,
		Scope:   sysconfig.ScopeSystem,
		Type:    sysconfig.TypeInt,
		Default: "1",
		Group:   "autobill", Label: "统一出账日（每月几号）",
		Description: "仅「每月固定一天统一出账」用到，1-28 号；选 29-31 时小月没有这一天，那些月份不会自动出账",
	})
}

// runAutoBill 遍历全部台账执行自动出账。按台账读各自的模式与出账日，
// 互不影响；单个台账失败只记录日志，不阻断其余台账。
func runAutoBill(db *gorm.DB, now time.Time) {
	var ledgerIDs []uint
	if err := db.Model(&Room{}).Distinct().Pluck("org_id", &ledgerIDs).Error; err != nil {
		logger.Error("rental: 自动出账读取台账失败: %v", err)
		return
	}
	created, skipped := 0, 0
	for _, ledgerID := range ledgerIDs {
		mode := ledgerConfigString(db, autobillModeConfigKey, ledgerID, autobillModeOff)
		var c, s int
		switch mode {
		case autobillModePayDay:
			c, s = autoBillByPayDay(db, ledgerID, now)
		case autobillModeMonthly:
			day := ledgerConfigInt(db, autobillDayConfigKey, ledgerID, 1)
			c, s = autoBillMonthly(db, ledgerID, day, now)
		default:
			// off：不做事，也不刷日志——多数用户不开这个功能，
			// 每天一条「已跳过」只会把日志刷脏。
			continue
		}
		created += c
		skipped += s
	}
	if created > 0 {
		logger.Info("rental: 自动出账完成 created=%d skipped=%d", created, skipped)
	}
}

// autoBillByPayDay 「按缴费日」模式：只看今天（或已过期）该收的房间。
//
// 判定复用 nextPaymentDue——它与总览「缴费提醒」卡片、每日推送完全同一套
// 口径（最近一张账单未收清就是待收账期，已收清则下一期），所以自动出账的
// 账期不会和界面上提醒的账期对不上。DaysLeft<=0 表示缴费日已到或已过。
func autoBillByPayDay(db *gorm.DB, ledgerID uint, now time.Time) (int, int) {
	rooms, err := activeTenantRooms(db, ledgerID)
	if err != nil {
		logger.Error("rental: 自动出账（按缴费日）查询房间失败 ledger=%d: %v", ledgerID, err)
		return 0, 0
	}
	created, skipped := 0, 0
	err = db.Transaction(func(tx *gorm.DB) error {
		for i := range rooms {
			cfg := effectiveRoomBilling(tx, &rooms[i])
			due := nextPaymentDue(tx, &rooms[i], cfg, now)
			// due.BillID != 0：该账期已经开过单，等收钱就行。
			// due.DaysLeft > 0：还没到日子，不提前出账。
			if due == nil || due.BillID != 0 || due.DaysLeft > 0 {
				skipped++
				continue
			}
			if !onBillSchedule(tx, &rooms[i], due.Period) {
				skipped++
				continue
			}
			_, ok, genErr := generateBillForRoom(tx, &rooms[i], due.Period, 0)
			if genErr != nil {
				return genErr
			}
			if ok {
				created++
			} else {
				skipped++
			}
		}
		return nil
	})
	if err != nil {
		logger.Error("rental: 自动出账（按缴费日）失败 ledger=%d: %v", ledgerID, err)
		return 0, 0
	}
	return created, skipped
}

// autoBillMonthly 「每月固定一天统一出账」模式：到日子给全部在租房间开当月账单。
//
// 账单本身按月出（水/电/燃气与包月水费恒按月，房租等固定费只在到期月计入，
// 见 onBillSchedule），所以这里恒用当前月，季付/年付房的中间月就是只含水电
// 的月账单。
func autoBillMonthly(db *gorm.DB, ledgerID uint, day int, now time.Time) (int, int) {
	if day < 1 || day > 28 || now.Day() != day {
		return 0, 0
	}
	period := now.Format("2006-01")
	rooms, err := activeTenantRooms(db, ledgerID)
	if err != nil {
		logger.Error("rental: 自动出账（每月固定日）查询房间失败 ledger=%d: %v", ledgerID, err)
		return 0, 0
	}
	created, skipped := 0, 0
	err = db.Transaction(func(tx *gorm.DB) error {
		for i := range rooms {
			if !onBillSchedule(tx, &rooms[i], period) {
				skipped++
				continue
			}
			_, ok, genErr := generateBillForRoom(tx, &rooms[i], period, 0)
			if genErr != nil {
				return genErr
			}
			if ok {
				created++
			} else {
				skipped++
			}
		}
		return nil
	})
	if err != nil {
		logger.Error("rental: 自动出账（每月固定日）失败 ledger=%d: %v", ledgerID, err)
		return 0, 0
	}
	return created, skipped
}

// activeTenantRooms 某台账下「有在租租户」的房间。空置房不出账——
// 与手动一键生成的取数口径一致（见 handleBillGenerate）。
func activeTenantRooms(db *gorm.DB, ledgerID uint) ([]Room, error) {
	rooms := make([]Room, 0)
	err := db.Where(
		"org_id = ? AND id IN (SELECT room_id FROM tenants WHERE active = ? AND org_id = ?)",
		ledgerID, true, ledgerID).
		Order("room_no ASC").Find(&rooms).Error
	return rooms, err
}
