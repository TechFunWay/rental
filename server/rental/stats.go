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
				"rooms_total": 0, "rooms_occupied": 0, "rooms_vacant": 0, "tenants_active": 0,
				"month": gin.H{
					"period": period, "total": 0, "paid": 0, "outstanding": 0,
					"bill_count": 0, "arrears_count": 0,
				},
				"overall":      gin.H{"outstanding": 0, "arrears_count": 0},
				"recent_bills": []Bill{},
			})
			return
		}

		var roomsTotal, tenantsActive, occupied int64
		db.Model(&Room{}).Where("org_id IN ?", ledgerIDs).Count(&roomsTotal)
		db.Model(&Tenant{}).Where("active = ? AND org_id IN ?", true, ledgerIDs).Count(&tenantsActive)
		// 在租判定同样限定台账：跨台账的孤儿租户不能让本台账的房算作「已出租」。
		db.Model(&Room{}).Where(
			"org_id IN ? AND id IN (SELECT room_id FROM tenants WHERE active = ? AND org_id IN ?)",
			ledgerIDs, true, ledgerIDs).Count(&occupied)

		// 指定月应收/实收/欠缴户数——每次聚合都用全新查询链，避免条件串扰。
		var monthTotal, monthPaid float64
		var monthBillCount, monthArrearsCount int64
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).
			Select("COALESCE(SUM(total_amount), 0)").Scan(&monthTotal)
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).
			Select("COALESCE(SUM(paid_amount), 0)").Scan(&monthPaid)
		db.Model(&Bill{}).Where("period = ? AND org_id IN ?", period, ledgerIDs).Count(&monthBillCount)
		db.Model(&Bill{}).Where("period = ? AND status <> ? AND org_id IN ?", period, "paid", ledgerIDs).
			Count(&monthArrearsCount)

		// 历史累计欠缴（可见台账的全部月份）。
		var overallArrears float64
		var overallArrearsCount int64
		db.Model(&Bill{}).Where("status <> ? AND org_id IN ?", "paid", ledgerIDs).
			Select("COALESCE(SUM(total_amount - paid_amount), 0)").Scan(&overallArrears)
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
		arrearsList := make([]gin.H, 0)
		var arrearsBills []Bill
		db.Where("period = ? AND status <> ? AND org_id IN ?", period, "paid", ledgerIDs).
			Order("total_amount - paid_amount DESC").Limit(50).Find(&arrearsBills)
		for _, b := range arrearsBills {
			arrears := b.TotalAmount - b.PaidAmount
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
