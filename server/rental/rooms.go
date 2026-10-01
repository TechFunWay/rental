// rooms.go — 房源 CRUD 与删除保护。
package rental

import (
	"strconv"
	"strings"

	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type tenantBrief struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// roomView 是房源的出参视图：附加当前在租租户、是否已有账单与最近一张
// 账单的账期（前端据此判断季付房本月是否应出账），以及该房当前生效的
// 计费与缴费设置（来自在租租户 → 全局默认，见 billing.go）。
type roomView struct {
	Room
	CurrentTenants []tenantBrief `json:"current_tenants"`
	HasBills       bool          `json:"has_bills"`
	LastBillPeriod string        `json:"last_bill_period"`
	Billing        billingView   `json:"billing"`
}

// billingView 是房间生效计费设置的出参形态。两个"该方式下的金额"都给出，
// 便于前端在账单里临时切换计费方式时按配置金额预填（与 waterAmountForMode 同口径）。
// ExcludedFees 是该房生效的"不参与计费项目"（租户勾选），前端据此在
// 开票/编辑界面禁用对应项目并按 0 预估费用。
type billingView struct {
	WaterMode       string   `json:"water_mode"`        // meter 按吨 / monthly 包月
	WaterAmount     float64  `json:"water_amount"`      // 当前生效方式下的金额：按吨=元/吨；包月=元/月
	WaterMeterPrice float64  `json:"water_meter_price"` // 按吨口径的金额（租户 → 全局默认）
	WaterMonthlyFee float64  `json:"water_monthly_fee"` // 包月口径的金额（租户 → 全局默认）
	PayCycle        string   `json:"pay_cycle"`         // monthly 月付 / quarterly 季付
	PayDay          int      `json:"pay_day"`           // 1-28；0=不提醒
	RemindDays      int      `json:"remind_days"`       // 0-30
	ExcludedFees    []string `json:"excluded_fees"`     // 不参与计费的项目 key（空=全部参与）
}

type roomRequest struct {
	Community            string  `json:"community"` // 小区（单小区可留空）
	RoomNo               string  `json:"room_no" binding:"required"`
	Building             string  `json:"building"`
	Unit                 string  `json:"unit"`
	Floor                string  `json:"floor"`
	DefaultRent          float64 `json:"default_rent"`
	DefaultSanitationFee float64 `json:"default_sanitation_fee"`
	DefaultManagementFee float64 `json:"default_management_fee"`
	ElecPrice            float64 `json:"elec_price"`
	GasPrice             float64 `json:"gas_price"`
	InitialWater         float64 `json:"initial_water"`
	InitialElec          float64 `json:"initial_elec"`
	InitialGas           float64 `json:"initial_gas"`
	Notes                string  `json:"notes"`
}

func setupRoomRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/rooms", handleRoomList(db))
	api.POST("/rental/rooms", handleRoomCreate(db))
	api.PUT("/rental/rooms/:id", handleRoomUpdate(db))
	api.DELETE("/rental/rooms/:id", handleRoomDelete(db))
}

func currentUserID(c *gin.Context) uint {
	return c.GetUint("userID")
}

func handleRoomList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		keyword := strings.TrimSpace(c.Query("keyword"))
		status := c.Query("status")

		query := db.Model(&Room{}).Where("user_id = ?", currentUserID(c))
		if keyword != "" {
			like := "%" + keyword + "%"
			// 关键字同时匹配位置信息（房号/小区/楼栋/单元）与该房在租租户姓名。
			query = query.Where(
				"room_no LIKE ? OR community LIKE ? OR building LIKE ? OR unit LIKE ? OR id IN (SELECT room_id FROM tenants WHERE user_id = ? AND active = ? AND name LIKE ?)",
				like, like, like, like, currentUserID(c), true, like)
		}
		if status == "occupied" || status == "vacant" {
			op := "IN"
			if status == "vacant" {
				op = "NOT IN"
			}
			query = query.Where("id "+op+" (SELECT room_id FROM tenants WHERE user_id = ? AND active = ?)", currentUserID(c), true)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		rooms := make([]Room, 0)
		if err := query.Order("room_no ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		items := make([]roomView, 0, len(rooms))
		for _, r := range rooms {
			items = append(items, buildRoomView(db, r))
		}
		response.SuccessPage(c, items, total, page, pageSize)
	}
}

func buildRoomView(db *gorm.DB, r Room) roomView {
	view := roomView{Room: r, CurrentTenants: []tenantBrief{}}
	db.Model(&Tenant{}).
		Where("room_id = ? AND active = ?", r.ID, true).
		Order("id ASC").
		Limit(50).
		Find(&view.CurrentTenants)
	// 生效计费设置：在租租户 → 全局默认（前端房源列表、抄表预览与账单编辑共用）。
	// 两个方式各自的金额都给出来，前端切计费方式时按配置金额预填。
	cfg := effectiveRoomBilling(db, &r)
	view.Billing = billingView{
		WaterMode:       cfg.WaterMode,
		WaterAmount:     cfg.WaterAmount,
		WaterMeterPrice: waterAmountForMode(db, &r, waterModeMeter),
		WaterMonthlyFee: waterAmountForMode(db, &r, waterModeMonthly),
		PayCycle:        cfg.PayCycle,
		PayDay:          cfg.PayDay,
		RemindDays:      cfg.RemindDays,
		ExcludedFees:    effectiveRoomExcludedFees(db, &r),
	}
	var bills int64
	db.Model(&Bill{}).Where("room_id = ?", r.ID).Count(&bills)
	view.HasBills = bills > 0
	if view.HasBills {
		db.Model(&Bill{}).Where("room_id = ?", r.ID).
			Order("period DESC").Limit(1).Pluck("period", &view.LastBillPeriod)
	}
	return view
}

func handleRoomCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req roomRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "房号不能为空")
			return
		}
		if msg := validateRoomRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		req.RoomNo = strings.TrimSpace(req.RoomNo)

		room := Room{UserID: currentUserID(c), Community: strings.TrimSpace(req.Community), RoomNo: req.RoomNo}
		applyRoomRequest(&room, &req)
		if err := db.Create(&room).Error; err != nil {
			if isDuplicateKey(err) {
				response.ErrorBadRequest(c, "房号已存在")
				return
			}
			response.ErrorInternal(c, "创建房源失败")
			return
		}
		response.Success(c, room)
	}
}

func handleRoomUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		var req roomRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "房号不能为空")
			return
		}
		if msg := validateRoomRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		req.RoomNo = strings.TrimSpace(req.RoomNo)

		room.Community = strings.TrimSpace(req.Community)
		room.RoomNo = req.RoomNo
		applyRoomRequest(room, &req)
		if err := db.Save(room).Error; err != nil {
			if isDuplicateKey(err) {
				response.ErrorBadRequest(c, "房号已存在")
				return
			}
			response.ErrorInternal(c, "保存房源失败")
			return
		}
		response.Success(c, room)
	}
}

// handleRoomDelete 删除房源。存在账单时拒绝（保护台账完整性），
// 存在在租租户时提示先退租。
func handleRoomDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		var bills int64
		db.Model(&Bill{}).Where("room_id = ?", room.ID).Count(&bills)
		if bills > 0 {
			response.ErrorBadRequest(c, "该房源存在账单记录，不能删除；如需清理请先删除相关账单")
			return
		}
		var tenants int64
		db.Model(&Tenant{}).Where("room_id = ? AND active = ?", room.ID, true).Count(&tenants)
		if tenants > 0 {
			response.ErrorBadRequest(c, "该房源仍有在租租户，请先办理退租")
			return
		}
		if err := db.Delete(room).Error; err != nil {
			response.ErrorInternal(c, "删除房源失败")
			return
		}
		// 房源删除时顺带清理其历史租户登记（账单不存在才会走到这里）。
		db.Where("room_id = ?", room.ID).Delete(&Tenant{})
		response.Success(c, nil)
	}
}

// validateRoomRequest 校验房源入参，返回空串表示通过。
// 水费计费方式与缴费周期自 v0.2.3 起在租户上设置（见 tenants.go），
// 房源只校验默认租金/费用与抄表底数不能为负。
func validateRoomRequest(req *roomRequest) string {
	if req.DefaultRent < 0 || req.DefaultSanitationFee < 0 || req.DefaultManagementFee < 0 {
		return "默认租金与费用不能为负数"
	}
	if req.ElecPrice < 0 || req.GasPrice < 0 {
		return "电费/燃气单价不能为负数"
	}
	if req.InitialWater < 0 || req.InitialElec < 0 || req.InitialGas < 0 {
		return "抄表底数不能为负数"
	}
	return ""
}

func applyRoomRequest(room *Room, req *roomRequest) {
	room.Building = req.Building
	room.Unit = req.Unit
	room.Floor = req.Floor
	room.DefaultRent = req.DefaultRent
	room.DefaultSanitationFee = req.DefaultSanitationFee
	room.DefaultManagementFee = req.DefaultManagementFee
	room.ElecPrice = req.ElecPrice
	room.GasPrice = req.GasPrice
	room.InitialWater = req.InitialWater
	room.InitialElec = req.InitialElec
	room.InitialGas = req.InitialGas
	room.Notes = req.Notes
	// 房源上的水费/缴费字段是 0.2.2 及以前的遗留列，此处刻意不动：
	// 编辑房源不会覆盖或清掉老数据，迁移已把有效值搬到租户上。
}

func findUserRoom(db *gorm.DB, c *gin.Context) (*Room, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的房源 ID")
		return nil, false
	}
	var room Room
	if err := db.Where("id = ? AND user_id = ?", id, currentUserID(c)).First(&room).Error; err != nil {
		response.ErrorNotFound(c, "房源不存在")
		return nil, false
	}
	return &room, true
}

// isDuplicateKey 识别 SQLite 唯一约束冲突（UNIQUE constraint failed）。
func isDuplicateKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
