// renew.go — 租约续租：把到期日顺延并留痕（2026-10-20 粉丝需求）。
//
// 为什么单独一个文件、自己 apps.Register 一个 App：
// 续租是「租户 + 房源 + 留痕表」三者的一个新动作，但它的入口只是租户路由上的
// 一个子路径。改 rental.go 的 setupRoutes 会与别人的改动撞车，因此按
// room_items.go 的既有约定——本文件自包含注册路由，在 SetupAuth 里补上
// resolveLedger 与 denyTenantRole 两层中间件，口径与 setupRoutes 逐字一致。
//
// 口径（前后端同一套，前端 web/src/utils/lease.ts 是孪生实现）：
//
//	新到期日 = 基准日 + N 个月 − 1 天
//	基准日   = max(当前到期日, 今天)
//
// 为什么基准日要取「当前到期日与今天的较大者」：
//   - 还没到期就续租（提前一年签）：从原到期日往后顺延，不会把租期缩水；
//   - 已经过期才续租（漏登记）：从今天起算，否则填 3 个月可能得到过去的日期。
//
// 为什么到期日要 −1 天：起租 2026-01-15、租 3 个月，行业口径是 2026-04-14 到期
// （4 月 15 日就已经是下一起租日），与前端租期快捷填充完全一致。
//
// 留痕表为什么单独一张而不是塞进租户备注：续租记录要按月数、租金前后值逐条
// 可查，并且**续租单要能打印**（print-kit 的《续租协议》直接消费本表一行）。
package rental

import (
	"strconv"
	"strings"
	"time"

	"smallgo/server/apps"
	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// LeaseRenewal 一次续租的留痕。
//
// 金额是**快照**：RentBefore/RentAfter 记录续租当时房源的默认月租金
// （租金挂在 rooms.default_rent 上，改它只影响之后新开的账单，历史账单
// 已按开票快照固化，不受影响——与仓储里其它快照口径一致）。
type LeaseRenewal struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	UserID      uint      `gorm:"index" json:"-"`                                          // 经办人，仅溯源
	LedgerID    uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	TenantID    uint      `gorm:"index;not null" json:"tenant_id"`
	RoomID      uint      `gorm:"index;not null" json:"room_id"`
	FromEndDate string    `json:"from_end_date"` // 续租前的到期日（空=此前未约定）
	ToEndDate   string    `json:"to_end_date"`   // 续租后的到期日
	Months      int       `json:"months"`        // 本次续租时长（月）
	RentBefore  float64   `json:"rent_before"`   // 续租前月租金快照
	RentAfter   float64   `json:"rent_after"`    // 续租后月租金快照
	Note        string    `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
}

// 续租参数边界：单次最长 10 年（120 个月）——再长就该走新签合同而不是续租；
// 备注与其它自由文本字段同口径 200 字。
const (
	renewMaxMonths    = 120
	renewNoteMaxRunes = 200
	// 租金上限与房源表单同口径（rooms.go 只校验非负，这里再挡一个明显的脏值：
	// 把「年租金」填进月租金位也会落到千万级，1e8 足够宽）。
	renewMaxRent = 1e8
)

func init() {
	database.RegisterModels(&LeaseRenewal{})

	apps.Register(apps.App{
		Name:        "rental_lease_renewal",
		DisplayName: "租约续租",
		// 与 setupRoutes 同一套前置中间件：resolveLedger 解析本次请求的台账
		// 范围与写入目标，denyTenantRole 一律拒绝租户账号（续租是房东侧动作）。
		SetupAuth: func(auth *gin.RouterGroup, db *gorm.DB) {
			scoped := auth.Group("", resolveLedger(db))
			scoped = scoped.Group("", denyTenantRole())
			scoped.POST("/rental/tenants/:id/renew", requireModule(db, ModuleTenants, PermEdit), handleTenantRenew(db))
			scoped.GET("/rental/tenants/:id/renewals", requireModule(db, ModuleTenants, PermView), handleTenantRenewals(db))
		},
	})
}

// renewRequest 续租入参。Months 与 ToEndDate 二选一，都填时 ToEndDate 优先
// （用户点了「自定义到期日」就是明确指定）。
//
// Rent 用指针：nil = 不调租金；显式传 0 也允许（房东把房子降成免租过渡），
// 用 float64 无法区分「没传」与「填了 0」。
type renewRequest struct {
	Months    int      `json:"months"`
	ToEndDate string   `json:"to_end_date"`
	Rent      *float64 `json:"rent"`
	Note      string   `json:"note"`
}

// addMonthsClamp 在 base 上加 n 个月；日号超过目标月天数时取该月最后一天
// （1 月 31 日 + 1 个月 → 2 月 28/29 日），避免 time.AddDate 的「3 月 3 日」溢出。
func addMonthsClamp(base time.Time, n int) time.Time {
	y, m, d := base.Date()
	first := time.Date(y, time.Month(int(m)+n), 1, 0, 0, 0, 0, base.Location())
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, base.Location()).Day()
	if d > lastDay {
		d = lastDay
	}
	return time.Date(first.Year(), first.Month(), d, 0, 0, 0, 0, base.Location())
}

// leaseEndAfter 新到期日 = base + n 个月 − 1 天（与 web/src/utils/lease.ts 同一口径）。
func leaseEndAfter(base time.Time, n int) time.Time {
	return addMonthsClamp(base, n).AddDate(0, 0, -1)
}

// parseLeaseDate 解析 YYYY-MM-DD 的租期日期（本地时区零点）。
func parseLeaseDate(v string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(v), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// renewBase 续租基准日 = max(当前到期日, 今天)。到期日为空/脏值时按今天。
// 基准日归一到今天零点之前先与今天比较，让「今天到期」仍从今天起算。
func renewBase(tenantLeaseEnd string, today time.Time) time.Time {
	today = time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	end, ok := parseLeaseDate(tenantLeaseEnd)
	if !ok || end.Before(today) {
		return today
	}
	return end
}

// monthsBetweenLease 两个租期日期之间的整月数，口径与 leaseEndAfter 互逆
// （2026-01-15 → 2026-04-14 记 3 个月）。自定义到期日时用它回报「续租了几个月」。
//
// 为什么不直接算年月差：到期日约定是「起算日的前一天」，两处边界会让年月差
// 偏 1——月初对齐（5/1 续 3 个月 → 7/31，年月差是 2）与月末钳位
// （1/31 续 1 个月 → 2/27，年月差是 1 但按天数看不满月）。用 leaseEndAfter
// 反向求值最稳：先取年月差，再按实际推算结果收缩/扩张。
func monthsBetweenLease(from, to time.Time) int {
	months := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month())
	if months < 1 {
		months = 1
	}
	for months > 1 && leaseEndAfter(from, months).After(to) {
		months--
	}
	for leaseEndAfter(from, months).Before(to) {
		months++
	}
	return months
}

// handleTenantRenew 续租：更新租户到期日（可选调租金），并写一行留痕。
func handleTenantRenew(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		// 写权限按**租户所属台账**判定（与 handleTenantUpdate 一致）。
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if !tenant.Active || strings.TrimSpace(tenant.MoveOutDate) != "" {
			response.ErrorBadRequest(c, "已退租的租户不能续租，请先恢复在租状态")
			return
		}
		var req renewRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "参数格式错误")
			return
		}
		if len([]rune(strings.TrimSpace(req.Note))) > renewNoteMaxRunes {
			response.ErrorBadRequest(c, "备注不能超过 200 字")
			return
		}
		if req.Rent != nil && (*req.Rent < 0 || *req.Rent > renewMaxRent) {
			response.ErrorBadRequest(c, "月租金必须是 0~100000000 之间的数字")
			return
		}

		now := time.Now()
		base := renewBase(tenant.LeaseEndDate, now)

		// 目标到期日：自定义日期优先，否则按续租月数推算。
		var toEnd time.Time
		months := 0
		if v := strings.TrimSpace(req.ToEndDate); v != "" {
			parsed, okParse := parseLeaseDate(v)
			if !okParse {
				response.ErrorBadRequest(c, "新的到期日格式应为 YYYY-MM-DD")
				return
			}
			// 必须晚于基准日：等于或早于基准日意味着「没续租」或「把租期改短了」，
			// 后者属于改合同错误向的操作，不在这里做。
			if !parsed.After(base) {
				response.ErrorBadRequest(c, "新的到期日必须晚于当前到期日（或今天）")
				return
			}
			months = monthsBetweenLease(base, parsed)
			if months < 1 {
				months = 1
			}
			if months > renewMaxMonths {
				response.ErrorBadRequest(c, "单次续租不能超过 120 个月")
				return
			}
			toEnd = parsed
		} else {
			if req.Months < 1 || req.Months > renewMaxMonths {
				response.ErrorBadRequest(c, "请选择续租时长（1~120 个月）或填写新的到期日")
				return
			}
			months = req.Months
			toEnd = leaseEndAfter(base, months)
		}

		room, okRoom := loadRoom(db, c, tenant.RoomID)
		if !okRoom {
			return
		}
		rentBefore := room.DefaultRent
		rentAfter := rentBefore
		if req.Rent != nil {
			rentAfter = round2(*req.Rent)
		}

		record := LeaseRenewal{
			UserID:      c.GetUint("userID"),
			LedgerID:    tenant.LedgerID,
			TenantID:    tenant.ID,
			RoomID:      tenant.RoomID,
			FromEndDate: tenant.LeaseEndDate,
			ToEndDate:   toEnd.Format("2006-01-02"),
			Months:      months,
			RentBefore:  rentBefore,
			RentAfter:   rentAfter,
			Note:        strings.TrimSpace(req.Note),
		}

		// 租户到期日与留痕同一事务：不能出现「到期日改了但没记录」的半截状态。
		err := db.Transaction(func(tx *gorm.DB) error {
			tenant.LeaseEndDate = record.ToEndDate
			if err := tx.Save(tenant).Error; err != nil {
				return err
			}
			if req.Rent != nil {
				// 改租金只动房源默认月租：历史账单是开票快照，不会被追改。
				room.DefaultRent = rentAfter
				if err := tx.Model(room).Update("default_rent", rentAfter).Error; err != nil {
					return err
				}
			}
			return tx.Create(&record).Error
		})
		if err != nil {
			response.ErrorInternal(c, "续租失败，请重试")
			return
		}

		response.Success(c, gin.H{"renewal": record, "tenant": tenant})
	}
}

// handleTenantRenewals 续租历史（倒序）。量很小（一年最多几次），不分页，
// 上限 200 条兜底防极端数据拖垮弹窗。
func handleTenantRenewals(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		var list []LeaseRenewal
		if err := db.Where("tenant_id = ? AND org_id = ?", tenant.ID, tenant.LedgerID).
			Order("id DESC").Limit(200).Find(&list).Error; err != nil {
			response.ErrorInternal(c, "续租记录加载失败")
			return
		}
		response.Success(c, gin.H{"items": list, "tenant": tenant})
	}
}

// leaseRenewalByID 仅供测试/打印复用：按 ID 取一条留痕（台账内）。
func leaseRenewalByID(db *gorm.DB, ledgerID, id uint) (*LeaseRenewal, error) {
	var rec LeaseRenewal
	if err := db.Where("id = ? AND org_id = ?", id, ledgerID).First(&rec).Error; err != nil {
		return nil, err
	}
	return &rec, nil
}

// tenantIDParam 解析路径里的租户 ID（供测试复用与调试）。
func tenantIDParam(c *gin.Context) int {
	id, _ := strconv.Atoi(c.Param("id"))
	return id
}
