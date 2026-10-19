// stats.go — 仪表盘统计：房源/租户概况、当月应收实收、欠缴汇总与最近账单。
package rental

import (
	"time"

	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupStatsRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/stats", requireModule(db, ModuleAnalytics, PermView), handleStats(db))
}

func handleStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		period := time.Now().Format("2006-01")
		if p := c.Query("period"); p != "" {
			period = p
		}

		// v0.6.0：总览所有统计都限定在本次请求的可见台账内。
		// 无一台账可见时直接返回零值结构，避免退化成「看到全部台账」。
		ledgerIDs := visibleLedgerIDs(c)
		if len(ledgerIDs) == 0 {
			response.Success(c, gin.H{
				"lease_due": []gin.H{}, "payment_due": []PaymentDueItem{}, "arrears_list": []gin.H{},
				"rooms_total": 0, "rooms_occupied": 0, "rooms_vacant": 0,
				"tenants_active": 0, "members_active": 0, "residents_active": 0,
				"month": gin.H{
					"period": period, "total": 0, "paid": 0, "outstanding": 0,
					"bill_count": 0, "arrears_count": 0,
				},
				"overall":      gin.H{"outstanding": 0, "arrears_count": 0},
				"recent_bills": []Bill{},
			})
			return
		}

		var roomsTotal, tenantsActive, membersActive, occupied int64
		db.Model(&Room{}).Where("org_id IN ?", ledgerIDs).Count(&roomsTotal)
		db.Model(&Tenant{}).Where("active = ? AND org_id IN ?", true, ledgerIDs).Count(&tenantsActive)
		// 住在这里的人数 = 在租租户 + 在住住户成员。费用口径完全不受影响：
		// 住户成员不进账单、不进抄表、不进收款（见 docs/08 号设计文档第七节）。
		db.Model(&TenantMember{}).Where("active = ? AND org_id IN ?", true, ledgerIDs).Count(&membersActive)
		// 在租判定同样限定台账：跨台账的孤儿租户不能让本台账的房算作「已出租」。
		db.Model(&Room{}).Where(
			"org_id IN ? AND id IN (SELECT room_id FROM tenants WHERE active = ? AND org_id IN ?)",
			ledgerIDs, true, ledgerIDs).Count(&occupied)

		// 指定月应收/实收/欠缴户数——每次聚合都用全新查询链，避免条件串扰。
		//
		// 应收取 `payable`（本期应缴 = 本期费用 + 上期结转）而不是 total_amount：
		// 本卡片是**单月**视角，房东看到的是「这个月租户一共该交多少」，
		// 开了结转的台账应含上期少交的部分。同页的欠缴名单（下方 arrearsBills）
		// 也用 payable，两者必须同口径，否则同一个屏上会自相矛盾。
		//
		// 未开启结转时 payable 恒等于 total_amount，因此对老用户零影响。
		var monthTotal, monthPaid float64
		var monthBillCount, monthArrearsCount int64
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).
			Select("COALESCE(SUM(payable), 0)").Scan(&monthTotal)
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).
			Select("COALESCE(SUM(paid_amount), 0)").Scan(&monthPaid)
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).Count(&monthBillCount)
		db.Model(&Bill{}).Where("period = ? AND status <> ? AND org_id IN ?", period, "paid", ledgerIDs).
			Count(&monthArrearsCount)

		// 历史累计欠缴（可见台账的全部月份）。
		//
		// 口径（2026-10-16 随结转引入修正）：
		//
		//	未结净额 = Σ(total_amount) - Σ(paid_amount)
		//
		// 早先这里是 `SUM(total_amount - paid_amount) WHERE status <> 'paid'`，
		// 即「只把未缴清的账单加起来」。那在**不允许超收**的年代是对的，但现在
		// 允许「多交顺延」（issue #5 需求 7）后它会算错：
		//
		//	1月 应缴500 收450 → partial，欠 50   ← 被统计
		//	2月 应缴600 收700 → paid，多 100     ← 被 status<>paid 排除掉了！
		//
		// 于是总览显示「欠 50」，可实际上 2 月多交的 100 早已把 1 月的 50 抵平，
		// 房东净多收 50。过滤掉多收的那张单，等于永远看不到抵扣。
		//
		// 改成对**全部账单**求和（不再按 status 过滤），多收自然冲减欠缴：
		// 上例 = (500+600+350) - (450+700+350) = -50 → 下方夹到 0（不欠）。
		// 未开启结转的台账两种算法完全等价（没有超收就没有冲减），
		// 因此这个修正对老用户零影响。
		var overallSum float64
		var overallPaidSum float64
		db.Model(&Bill{}).Where("org_id IN ?", ledgerIDs).
			Select("COALESCE(SUM(total_amount), 0)").Scan(&overallSum)
		db.Model(&Bill{}).Where("org_id IN ?", ledgerIDs).
			Select("COALESCE(SUM(paid_amount), 0)").Scan(&overallPaidSum)
		overallArrears := round2(overallSum - overallPaidSum)
		if overallArrears < 0 {
			// 净多收：欠缴额最小为 0（“多收”不在这里体现为负数欠款，
			// 否则总览会出现「欠 -50 元」这种看不懂的显示）。
			overallArrears = 0
		}
		// 欠缴账单数仍按「未缴清」统计——它是“有几张单还没结”，
		// 与金额口径（净额）是两件事，不随上面的改动而变。
		var overallArrearsCount int64
		db.Model(&Bill{}).Where("status <> ? AND org_id IN ?", "paid", ledgerIDs).
			Count(&overallArrearsCount)

		recent := make([]Bill, 0)
		db.Where("org_id IN ?", ledgerIDs).Order("updated_at DESC").Limit(8).Find(&recent)

		// 租约到期提醒：在租租户中 60 天内到期（含已过期），按到期日升序。
		deadline := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
		leaseDue := make([]gin.H, 0)
		var dueTenants []Tenant
		db.Where("active = ? AND lease_end_date <> '' AND lease_end_date <= ? AND org_id IN ?",
			true, deadline, ledgerIDs).Order("lease_end_date ASC").Limit(20).Find(&dueTenants)
		if len(dueTenants) > 0 {
			roomNoOf := map[uint]string{}
			var rooms []Room
			db.Where("org_id IN ?", ledgerIDs).Find(&rooms)
			for _, r := range rooms {
				roomNoOf[r.ID] = r.RoomNo
			}
			today := time.Now().Format("2006-01-02")
			t0, _ := time.Parse("2006-01-02", today)
			for _, t := range dueTenants {
				d, err := time.Parse("2006-01-02", t.LeaseEndDate)
				days := 0
				if err == nil {
					days = int(d.Sub(t0).Hours() / 24)
				}
				leaseDue = append(leaseDue, gin.H{
					"id":             t.ID,
					"name":           t.Name,
					"room_no":        roomNoOf[t.RoomID],
					"lease_end_date": t.LeaseEndDate,
					"days_left":      days,
				})
			}
		}

		// 欠缴户名单：当前统计账期未缴清的账单（房号/租户/欠缴额），按欠缴额降序。
		//
		// 欠缴额用 payable（本期费用 + 上期结转）而不是 total_amount：房东在这一栏
		// 看到的是「这户这个月该交多少」，开了结转的台账应含上期少交的部分。
		// 这里限定单个账期、不跨期累加，因此用 payable 不会重复计算；跨期累加的
		// 那两处（overallArrears 与统计分析）仍必须用 total_amount，
		// 推导见 carryover.go 顶部注释。
		arrearsList := make([]gin.H, 0)
		var arrearsBills []Bill
		db.Where("period = ? AND status <> ? AND org_id IN ?", period, "paid", ledgerIDs).
			Order("payable - paid_amount DESC").Limit(50).Find(&arrearsBills)
		for _, b := range arrearsBills {
			arrears := b.Payable - b.PaidAmount
			if arrears <= 0 {
				continue
			}
			arrearsList = append(arrearsList, gin.H{
				"id":          b.ID,
				"room_no":     b.RoomNo,
				"tenant_name": b.TenantName,
				"period":      b.Period,
				"arrears":     round2(arrears),
			})
		}

		// 缴费提醒：每间设置了缴费日且有在租租户的房，缴费日落在提前提醒
		// 窗口内（或已逾期）的待收项，按缴费日升序。口径见 cycle.go。
		//
		// 必须传本次请求的可见台账（2026-10-11 修）：此前调的是不限台账的
		// computePaymentDue（ledgerIDs 为 nil = 全库），与本页其它字段
		// 「按 visibleLedgerIDs 收窄」的口径不一致——任何已审核账号打开总览，
		// 都能看到全站房东的房号、租户姓名与应缴金额（审计实测复现）。
		paymentDue := computePaymentDueInLedgers(db, time.Now(), visibleLedgerIDs(c))

		response.Success(c, gin.H{
			"lease_due":      leaseDue,
			"payment_due":    paymentDue,
			"arrears_list":   arrearsList,
			"rooms_total":    roomsTotal,
			"rooms_occupied": occupied,
			"rooms_vacant":   roomsTotal - occupied,
			"tenants_active": tenantsActive,
			// members_active 在住住户成员数；residents_active = 租户 + 成员，
			// 即「住在这里的人数」（总览卡片的在住人数）。
			"members_active":   membersActive,
			"residents_active": tenantsActive + membersActive,
			"month": gin.H{
				"period":        period,
				"total":         monthTotal,
				"paid":          monthPaid,
				"outstanding":   monthTotal - monthPaid,
				"bill_count":    monthBillCount,
				"arrears_count": monthArrearsCount,
			},
			"overall": gin.H{
				"outstanding":   overallArrears,
				"arrears_count": overallArrearsCount,
			},
			"recent_bills": recent,
		})
	}
}
