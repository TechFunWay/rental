// stats.go — 仪表盘统计：房源/租户概况、当月应收实收、欠缴汇总与最近账单。
package rental

import (
	"time"

	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupStatsRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/stats", handleStats(db))
}

func handleStats(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := currentUserID(c)
		period := time.Now().Format("2006-01")
		if p := c.Query("period"); p != "" {
			period = p
		}

		var roomsTotal, tenantsActive, occupied int64
		db.Model(&Room{}).Where("user_id = ?", userID).Count(&roomsTotal)
		db.Model(&Tenant{}).Where("user_id = ? AND active = ?", userID, true).Count(&tenantsActive)
		db.Model(&Room{}).Where(
			"user_id = ? AND id IN (SELECT room_id FROM tenants WHERE user_id = ? AND active = ?)",
			userID, userID, true).Count(&occupied)

		// 指定月应收/实收/欠缴户数——每次聚合都用全新查询链，避免条件串扰。
		var monthTotal, monthPaid float64
		var monthBillCount, monthArrearsCount int64
		db.Model(&Bill{}).Where("user_id = ? AND period = ?", userID, period).
			Select("COALESCE(SUM(total_amount), 0)").Scan(&monthTotal)
		db.Model(&Bill{}).Where("user_id = ? AND period = ?", userID, period).
			Select("COALESCE(SUM(paid_amount), 0)").Scan(&monthPaid)
		db.Model(&Bill{}).Where("user_id = ? AND period = ?", userID, period).Count(&monthBillCount)
		db.Model(&Bill{}).Where("user_id = ? AND period = ? AND status <> ?", userID, period, "paid").
			Count(&monthArrearsCount)

		// 历史累计欠缴（全部月份）。
		var overallArrears float64
		var overallArrearsCount int64
		db.Model(&Bill{}).Where("user_id = ? AND status <> ?", userID, "paid").
			Select("COALESCE(SUM(total_amount - paid_amount), 0)").Scan(&overallArrears)
		db.Model(&Bill{}).Where("user_id = ? AND status <> ?", userID, "paid").
			Count(&overallArrearsCount)

		recent := make([]Bill, 0)
		db.Where("user_id = ?", userID).Order("updated_at DESC").Limit(8).Find(&recent)

		// 租约到期提醒：在租租户中 60 天内到期（含已过期），按到期日升序。
		deadline := time.Now().AddDate(0, 0, 60).Format("2006-01-02")
		leaseDue := make([]gin.H, 0)
		var dueTenants []Tenant
		db.Where("user_id = ? AND active = ? AND lease_end_date <> '' AND lease_end_date <= ?",
			userID, true, deadline).Order("lease_end_date ASC").Limit(20).Find(&dueTenants)
		if len(dueTenants) > 0 {
			roomNoOf := map[uint]string{}
			var rooms []Room
			db.Where("user_id = ?", userID).Find(&rooms)
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
		db.Where("user_id = ? AND period = ? AND status <> ?", userID, period, "paid").
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
		paymentDue := computePaymentDue(db, userID, time.Now())

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
