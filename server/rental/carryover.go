// carryover.go — 少交/多交结转次月（GitHub issue #5 需求 7）。
//
// 用户确认的口径（原话示例）：
//
//	1月 费用500 实缴450           → 结转  50
//	2月 费用550 应缴=550+50=600 实缴700 → 结转-100
//	3月 费用450 应缴=450-100=350 实缴350 → 结转  0
//
// 即：本期应缴 = 本期费用 + 上期结转；本期结转 = 本期应缴 - 实缴。
//
// # 为什么结转额单独存列，绝不折进 total_amount
//
// 全应用有三处欠缴聚合都基于 `SUM(total_amount - paid_amount)`：
// 总览欠缴（stats.go）、统计分析当前欠缴与收缴率（fees.go）、欠缴名单（stats.go）。
// 如果把上期结转折进 total_amount，这些聚合会**把同一笔差额重复计算**：
//
//	上面这个例子（各期都结清）真实净额应为 0，
//	折进去后 Σ(total_amount - paid_amount) = -50 —— 每期历史欠缴都被重复计入一次。
//
// 这类错误不会报错、只会悄悄把欠缴金额算多，房东每天看的就是这个数。
//
// 因此：total_amount 保持「本期费用」语义不变（现有查询零改动、零风险），
// 结转额放 CarryIn，本期应缴放 Payable = TotalAmount + CarryIn。
//
// # 默认关闭、按台账开启
//
// 用户确认默认关闭：老用户的账单金额一个子都不用变，要用的房东自己在台账设置里开。
// 开启后**只影响开启之后新生成的账单**，历史账单不回填、不重算——避免「打开开关
// 当月，历史上所有账单一夜之间变了」。
package rental

import (
	"strings"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// carryoverConfigKey 台账级结转开关（默认关闭）。
const carryoverConfigKey = "rental_carryover_enabled"

func init() {
	database.RegisterModels(&Bill{}) // 仅确保 Bill 的结转列参与 AutoMigrate

	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key:         carryoverConfigKey,
		Scope:       sysconfig.ScopeSystem,
		Type:        sysconfig.TypeBool,
		Default:     "false",
		Group:       "rental",
		Label:       "少交多交结转次月",
		Description: "开启后，上期欠缴自动加进本期应缴、多交自动抵扣本期应缴；默认关闭，只影响开启后新生成的账单",
	})
}

// carryoverEnabled 该台账是否启用结转。
//
// 按台账判定（台账覆盖 → 系统级 → 默认 false）。默认关闭是刻意的：
// 老用户升级后账单金额不变，不会月中突然发现应缴变了。
func carryoverEnabled(db *gorm.DB, ledgerID uint) bool {
	if db == nil {
		return false
	}
	// 复用台账配置的读取链（台账覆盖 → 系统级 → 默认值），与其它租房设置同口径。
	v := ledgerConfigString(db, carryoverConfigKey, ledgerID, "false")
	return strings.EqualFold(strings.TrimSpace(v), "true")
}

// prevBillOfRoom 取该房**账期紧邻的上一张**账单（同台账内）。
//
// 不要求月份连续：季付/半年付/年付房的上一张账单可能在 N 个月前，中间没有账单。
// 首账（没有上一张）返回 ok=false。
func prevBillOfRoom(db *gorm.DB, room *Room, period string) (Bill, bool) {
	var prev Bill
	q := db.Where("room_id = ? AND period < ?", room.ID, period)
	if ledgerColumnReady(db, "bills") {
		q = q.Where("org_id = ?", room.LedgerID)
	}
	if err := q.Order("period DESC").First(&prev).Error; err != nil {
		return Bill{}, false
	}
	return prev, true
}

// carryInFor 算出某房某账期应带入的「上期结转」。
//
// 规则：carry_in = 上一期应缴(payable) - 上一期实缴(paid_amount)。
//
//	正数 = 上期少交，本期应缴要加上；
//	负数 = 上期多交，本期应缴要减去。
//
// 关闭结转、首账、读不到上一期时一律返回 0。
//
// 注意用的是上一期的 Payable 而不是 TotalAmount：上一期自己也可能带了结转，
// 用 Payable 才能一路累加下去（与用户示例的递推一致）。
func carryInFor(db *gorm.DB, room *Room, period string) float64 {
	if !carryoverEnabled(db, room.LedgerID) {
		return 0
	}
	prev, ok := prevBillOfRoom(db, room, period)
	if !ok {
		return 0
	}
	return round2(prev.Payable - prev.PaidAmount)
}

// applyCarryIn 给账单算上结转（开票时调用）。
//
// 账单是快照：算出来写进 CarryIn/Payable 就不再随历史账单变动自动重算，
// 与「开票时固化租户名与单价」的既有约定一致；需要纠正时用显式的重算入口。
func applyCarryIn(db *gorm.DB, b *Bill, room *Room) {
	if !carryoverEnabled(db, b.LedgerID) {
		b.CarryIn = 0
		b.Payable = b.TotalAmount
		return
	}
	b.CarryIn = carryInFor(db, room, b.Period)
	b.Payable = round2(b.TotalAmount + b.CarryIn)
}

// recalc 之后同步 Payable：本期费用变了，应缴要跟着变。
// （CarryIn 是开票快照，不因本期费用变化而变。）
func (b *Bill) syncPayable() {
	b.Payable = round2(b.TotalAmount + b.CarryIn)
}

// recalcCarryoverFrom 从 fromPeriod 起，沿该房后续账单逐期重算结转。
//
// 返回实际更新的账单数。**必须按账期升序处理**：每一期的 CarryIn 依赖上一期
// 重算后的 Payable，顺序错了会连锁算错。
//
// 只动 carry_in / payable / status，**不动本期费用、不动收款流水**。
//
// 两个调用点共用它：
//   - 收款后自动传播（handleBillPay）：本期收款改变了「本期结转」，
//     而本期结转正是下一期的 CarryIn，必须跟着更正；
//   - 手动重算入口（handleCarryoverRecalc）：房东改过历史账单后主动更正后续。
func recalcCarryoverFrom(db *gorm.DB, room *Room, fromPeriod string) (int, error) {
	if !carryoverEnabled(db, room.LedgerID) {
		return 0, nil
	}
	bills := make([]Bill, 0)
	q := db.Where("room_id = ? AND period >= ?", room.ID, fromPeriod)
	if ledgerColumnReady(db, "bills") {
		q = q.Where("org_id = ?", room.LedgerID)
	}
	if err := q.Order("period ASC").Find(&bills).Error; err != nil {
		return 0, err
	}

	updated := 0
	for i := range bills {
		b := &bills[i]
		var carry float64
		if i == 0 {
			// 第一期：按它自己的上一张账单算（可能不在本次范围内）。
			carry = carryInFor(db, room, b.Period)
		} else {
			// 后续期：用上一期**重算后**的 Payable 递推。
			carry = round2(bills[i-1].Payable - bills[i-1].PaidAmount)
		}
		payable := round2(b.TotalAmount + carry)
		if b.CarryIn == carry && b.Payable == payable {
			continue // 无变化，不写库
		}
		status := billStatus(payable, b.PaidAmount)
		if err := db.Model(&Bill{}).Where("id = ?", b.ID).
			Updates(map[string]interface{}{
				"carry_in": carry, "payable": payable, "status": status,
			}).Error; err != nil {
			return updated, err
		}
		b.CarryIn, b.Payable, b.Status = carry, payable, status
		updated++
	}
	return updated, nil
}

// handleCarryoverRecalc 手动重算某房某账期起的结转。
//
// 为什么还需要显式入口：结转额是开票快照，房东**改历史账单的本期费用**之后
// （不是收款，收款已自动传播），后续账期的 CarryIn 不会自动更正。
// 这里给出一次性的「从这期起往后重算」，避免房东被迫逐张手改。
func handleCarryoverRecalc(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			RoomID uint   `json:"room_id" binding:"required"`
			Period string `json:"period" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请选择房间与起始账期")
			return
		}
		room, ok := loadRoom(db, c, req.RoomID)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleBills, PermEdit) {
			return
		}
		// 未启用结转时明确报错，而不是静默「重算成功但什么都没做」——
		// 房东点了按钮却看不到变化，会以为是坏的。
		if !carryoverEnabled(db, room.LedgerID) {
			response.ErrorBadRequest(c, "该台账未启用结转，无需重算")
			return
		}
		updated, err := recalcCarryoverFrom(db, room, req.Period)
		if err != nil {
			response.ErrorInternal(c, "重算结转失败")
			return
		}
		response.Success(c, gin.H{"updated": updated, "room_id": room.ID, "from": req.Period})
	}
}
