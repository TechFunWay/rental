// bills.go — 月度账单：开票预填、一键生成、编辑重算、收款与单据数据。
package rental

import (
	"strconv"
	"strings"
	"time"

	"smallgo/server/response"
	"smallgo/server/sysconfig"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setupBillRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/bills", handleBillList(db))
	api.GET("/rental/bills/periods", handleBillPeriods(db))
	api.GET("/rental/bills/:id", handleBillDetail(db))
	api.POST("/rental/bills", handleBillCreate(db))
	api.POST("/rental/bills/generate", handleBillGenerate(db))
	api.PUT("/rental/bills/:id", handleBillUpdate(db))
	api.POST("/rental/bills/:id/pay", handleBillPay(db))
	api.DELETE("/rental/bills/:id", handleBillDelete(db))
}

// billRequest 是编辑账单的入参。费用三项（water_fee 等）与合计由服务端
// 按读数与单价重算，客户端传入值一律忽略，保证口径唯一。
// water_mode 为空表示保持账单现有计费方式（按吨 / 包月）。
type billRequest struct {
	RoomID        uint    `json:"room_id" binding:"required"`
	Period        string  `json:"period" binding:"required"`
	TenantName    string  `json:"tenant_name"`
	Rent          float64 `json:"rent"`
	WaterLast     float64 `json:"water_last"`
	WaterNow      float64 `json:"water_now"`
	ElecLast      float64 `json:"elec_last"`
	ElecNow       float64 `json:"elec_now"`
	GasLast       float64 `json:"gas_last"`
	GasNow        float64 `json:"gas_now"`
	WaterMode     string  `json:"water_mode"`
	WaterPrice    float64 `json:"water_price"`
	ElecPrice     float64 `json:"elec_price"`
	GasPrice      float64 `json:"gas_price"`
	SanitationFee float64 `json:"sanitation_fee"`
	ManagementFee float64 `json:"management_fee"`
	PaidAmount    float64 `json:"paid_amount"`
	Remark        string  `json:"remark"`
}

// recalc 按统一口径重算费用与合计，并联动缴纳状态。PaidAt 由调用方维护。
//
// ExcludedFees 是开票时快照的"不参与计费项目"：被排除的内置项目一律清零
// （固定费直接清，读数类算完再清），读数照常保留只是不参与收费；合计按
// 清零后的值算。开票、编辑、收款三条路径都经过这里，口径唯一。
func (b *Bill) recalc() {
	excluded := excludedFeeSet(b.ExcludedFees)
	b.Rent = excludedAmount(b.Rent, excluded[feeKeyRent])
	b.SanitationFee = excludedAmount(b.SanitationFee, excluded[feeKeySanitation])
	b.ManagementFee = excludedAmount(b.ManagementFee, excluded[feeKeyManagement])
	b.WaterFee = excludedAmount(b.waterFee(), excluded[feeKeyWater])
	b.ElecFee = excludedAmount(meterFee(b.ElecLast, b.ElecNow, b.ElecPrice), excluded[feeKeyElec])
	b.GasFee = excludedAmount(meterFee(b.GasLast, b.GasNow, b.GasPrice), excluded[feeKeyGas])
	b.TotalAmount = round2(b.Rent + b.WaterFee + b.ElecFee + b.GasFee + b.SanitationFee + b.ManagementFee + b.ExtraAmount)
	b.Status = billStatus(b.TotalAmount, b.PaidAmount)
}

// excludedAmount 参与计费返回原值，被排除返回 0。
func excludedAmount(v float64, isExcluded bool) float64 {
	if isExcluded {
		return 0
	}
	return v
}

// fixedItemsDue 本期固定费项目（租金/卫生/管理）是否到期应收。
// 到期规则见 fixedItemDue：季付租户与最近一张含固定费的账期整差 3 个月及以上，
// 月付恒到期；补历史账单视为到期。
func (b *Bill) fixedItemsDue(db *gorm.DB, room *Room) bool {
	return fixedItemDue(db, room, b.Period)
}

// waterFee 按开票时快照的计费方式算水费。水电属于按月收取的项目：
// 包月取每月固定金额（不再随账单季付 ×3，季付只作用于租金等固定费项目），
// 按吨按用量 × 单价（WaterPrice 在包月语义下存的是元/月金额）。
func (b *Bill) waterFee() float64 {
	if normalizeWaterMode(b.WaterMode) == waterModeMonthly {
		return round2(b.WaterPrice)
	}
	return meterFee(b.WaterLast, b.WaterNow, b.WaterPrice)
}

// configFloat 读取用户级数值配置，读不到或非法时返回默认值。
func configFloat(db *gorm.DB, key string, userID uint, def float64) float64 {
	v, err := sysconfig.GetConfig(db, key, userID)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 0 {
		return f
	}
	return def
}

// defaultPrices 读取用户级默认单价（sysconfig，偏好设置中的"租房设置"组）。
func defaultPrices(db *gorm.DB, userID uint) (water, elec, gas float64) {
	return configFloat(db, "rental_water_price", userID, 5.0),
		configFloat(db, "rental_elec_price", userID, 1.2),
		configFloat(db, "rental_gas_price", userID, 3.5)
}

// activeTenantNames 返回房间在租租户姓名（多人顿号连接，作为账单快照）。
func activeTenantNames(db *gorm.DB, roomID uint) string {
	names := make([]string, 0, 4)
	db.Model(&Tenant{}).Where("room_id = ? AND active = ?", roomID, true).
		Order("id ASC").Limit(20).Pluck("name", &names)
	return strings.Join(names, "、")
}

// prefillReadings 计算开票时的"上月读数"：优先取上一期账单的本月读数，
// 无历史账单则取房间底数，再无则为 0。
func prefillReadings(db *gorm.DB, room *Room) (waterLast, elecLast, gasLast float64) {
	var prev Bill
	err := db.Where("room_id = ? AND user_id = ?", room.ID, room.UserID).
		Order("period DESC").First(&prev).Error
	if err == nil {
		return prev.WaterNow, prev.ElecNow, prev.GasNow
	}
	return room.InitialWater, room.InitialElec, room.InitialGas
}

// newBillFromRoom 以房间默认值开票：读数衔接、单价默认值、租户名快照。
// 本月读数默认等于上月读数（用量 0），等抄表后在编辑中录入实际值。
// 水费连同计费方式一起快照进账单：包月房按每月固定金额开票，不依赖抄表。
// 缴费周期一并快照：季付房的租金/卫生费/管理费按 3 个月预填，
// 读数仍与上一张账单衔接（季付房的上一张是 3 个月前，用量跨一个季度）。
//
// 水费计费方式、金额与缴费周期取该房在租租户的生效设置（租户没设置时用
// 偏好设置里的全局默认），见 billing.go。
// newBillFromRoom 以房间默认值开票。收费项目按各自周期出账：
//   - 水电燃气（读数类）恒按月收取，包月水费按每月固定金额收；
//   - 租金/卫生费/管理费等固定费项目按租户缴费周期到期才收
//     （季付租户的中间月账单只含水电等月付项目，锚点月固定费一次收 3 个月）；
//   - 启用的自定义收费项目（宽带费等）按各自周期并入。
func newBillFromRoom(db *gorm.DB, room *Room, period string) *Bill {
	cfg := effectiveRoomBilling(db, room)
	waterLast, elecLast, gasLast := prefillReadings(db, room)
	_, elec, gas := defaultPrices(db, room.UserID)
	if room.ElecPrice > 0 {
		elec = room.ElecPrice
	}
	if room.GasPrice > 0 {
		gas = room.GasPrice
	}
	// 固定费项目（租金/卫生/管理）本期是否到期：到期才按覆盖月数计费。
	months := float64(cycleMonths(cfg.PayCycle))
	fixedDue := fixedItemDue(db, room, period)
	if !fixedDue {
		months = 0
	}
	b := &Bill{
		UserID:        room.UserID,
		RoomID:        room.ID,
		Period:        period,
		RoomNo:        room.RoomNo,
		TenantName:    activeTenantNames(db, room.ID),
		Rent:          round2(room.DefaultRent * months),
		WaterLast:     waterLast,
		WaterNow:      waterLast,
		ElecLast:      elecLast,
		ElecNow:       elecLast,
		GasLast:       gasLast,
		GasNow:        gasLast,
		PayCycle:      cfg.PayCycle,
		WaterMode:     cfg.WaterMode,
		WaterPrice:    cfg.WaterAmount,
		ElecPrice:     elec,
		GasPrice:      gas,
		SanitationFee: round2(room.DefaultSanitationFee * months),
		ManagementFee: round2(room.DefaultManagementFee * months),
		// 不参与计费的项目随账单快照：租户勾选的排除项（被排除的内置项目
		// 在 recalc 清零），历史账单不随租户后续改动而变化。
		ExcludedFees: effectiveRoomExcludedFees(db, room),
	}
	b.recalc()
	b.Status = billStatus(b.TotalAmount, 0)
	return b
}

// applyCustomFees 把启用的自定义收费项目并入账单（金额合计进 ExtraAmount），
// 返回明细行供落库。在建账/生成的读数覆盖处理完成后、最终 recalc 前调用。
func applyCustomFees(db *gorm.DB, room *Room, bill *Bill) []BillItem {
	customs, extra := customBillItems(db, room, bill.Period, bill.UserID)
	bill.ExtraAmount = round2(bill.ExtraAmount + extra)
	return customs
}

func handleBillList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		period := strings.TrimSpace(c.Query("period"))
		periodFrom := strings.TrimSpace(c.Query("period_from"))
		periodTo := strings.TrimSpace(c.Query("period_to"))
		billType := strings.TrimSpace(c.Query("bill_type"))
		// status=arrears 是"是否欠缴"的筛选别名（partial+unpaid）。
		status := strings.TrimSpace(c.Query("status"))
		keyword := strings.TrimSpace(c.Query("keyword"))

		query := db.Model(&Bill{}).Where("user_id = ?", currentUserID(c))
		if period != "" {
			query = query.Where("period = ?", period)
		}
		// 时间段选择：from/to 是账期区间（YYYY-MM，闭区间）。
		if periodFrom != "" {
			query = query.Where("period >= ?", periodFrom)
		}
		if periodTo != "" {
			query = query.Where("period <= ?", periodTo)
		}
		// 账单类型：monthly 月账单 / quarterly 季度账单（含季付项目的账单）。
		if billType == "monthly" || billType == "quarterly" {
			query = query.Where("pay_cycle = ?", billType)
		}
		switch status {
		case "paid", "partial", "unpaid":
			query = query.Where("status = ?", status)
		case "arrears":
			query = query.Where("status <> ?", "paid")
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("room_no LIKE ? OR tenant_name LIKE ?", like, like)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询账单失败")
			return
		}

		bills := make([]Bill, 0)
		if err := query.
			Order("period DESC, room_no ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&bills).Error; err != nil {
			response.ErrorInternal(c, "查询账单失败")
			return
		}
		response.SuccessPage(c, bills, total, page, pageSize)
	}
}

// handleBillPeriods 返回已有账单的全部账期（倒序），供月份筛选下拉使用。
func handleBillPeriods(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		periods := make([]string, 0)
		db.Model(&Bill{}).Where("user_id = ?", currentUserID(c)).
			Distinct("period").Order("period DESC").Limit(36).Pluck("period", &periods)
		response.Success(c, periods)
	}
}

// handleBillDetail 返回账单详情 + 单据抬头信息（出租方名称/电话/备注）。
func handleBillDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bill, ok := findUserBill(db, c)
		if !ok {
			return
		}
		name, _ := sysconfig.GetConfig(db, "rental_property_name", bill.UserID)
		contact, _ := sysconfig.GetConfig(db, "rental_contact", bill.UserID)
		note, _ := sysconfig.GetConfig(db, "rental_receipt_note", bill.UserID)

		items := make([]BillItem, 0)
		db.Where("bill_id = ?", bill.ID).Order("id ASC").Find(&items)
		charges, paids := billItemChargesAndPaids(db, bill)
		itemRows := make([]gin.H, 0, len(items))
		for _, it := range items {
			itemRows = append(itemRows, gin.H{
				"key": it.FeeKey, "name": it.Name, "kind": it.Kind,
				"amount": it.Amount, "detail": it.Detail, "cycle": it.Cycle,
				"paid": paids[it.FeeKey],
				"arrears": round2(it.Amount - paids[it.FeeKey]),
			})
		}
		_ = charges
		payRows := make([]gin.H, 0)
		payments := make([]Payment, 0)
		db.Where("bill_id = ?", bill.ID).Order("paid_at DESC, id DESC").Find(&payments)
		for _, p := range payments {
			payRows = append(payRows, gin.H{
				"id": p.ID, "paid_at": p.PaidAt, "amount": p.Amount,
				"note": p.Note, "items": paymentItems(p.Items),
			})
		}
		response.Success(c, gin.H{
			"bill": bill,
			"items": itemRows,
			"payments": payRows,
			"property": gin.H{
				"name":    name,
				"contact": contact,
				"note":    note,
			},
		})
	}
}

// handleBillCreate 为单个房间开票（同房同月唯一）。开票即预填：
// 读数衔接、单价与费用默认值、租户名快照。抄表录入可直填本月读数：
// 传了读数（指针区分"没传"与"传 0"）就以抄表值建账，费用按实际用量算。
// water_mode 可把该账单切成包月口径（水费 = 每月固定金额，不看读数）。
func handleBillCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			RoomID     uint     `json:"room_id" binding:"required"`
			Period     string   `json:"period" binding:"required"`
			TenantName string   `json:"tenant_name"`
			WaterNow   *float64 `json:"water_now"`
			ElecNow    *float64 `json:"elec_now"`
			GasNow     *float64 `json:"gas_now"`
			WaterMode  *string  `json:"water_mode"`
			WaterPrice *float64 `json:"water_price"`
			ElecPrice  *float64 `json:"elec_price"`
			GasPrice   *float64 `json:"gas_price"`
			Rent       *float64 `json:"rent"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请选择房间与账期")
			return
		}
		req.Period = strings.TrimSpace(req.Period)
		if !isValidPeriod(req.Period) {
			response.ErrorBadRequest(c, "账期格式应为 YYYY-MM")
			return
		}

		room, ok := loadRoom(db, c, req.RoomID)
		if !ok {
			return
		}
		var existing int64
		db.Model(&Bill{}).Where("user_id = ? AND room_id = ? AND period = ?",
			currentUserID(c), req.RoomID, req.Period).Count(&existing)
		if existing > 0 {
			response.ErrorBadRequest(c, "该房间本月账单已存在")
			return
		}

		bill := newBillFromRoom(db, room, req.Period)
		if req.WaterNow != nil {
			bill.WaterNow = *req.WaterNow
		}
		if req.ElecNow != nil {
			bill.ElecNow = *req.ElecNow
		}
		if req.GasNow != nil {
			bill.GasNow = *req.GasNow
		}
		// 计费方式可覆盖：传了非法值直接拒绝；改了方式但没给金额（或给 0）时，
		// 按新方式重新取该房生效设置的金额（在租租户 → 全局默认），避免包月房
		// 沿用元/吨单价（或反之）算出离谱水费。
		baseWaterMode := bill.WaterMode
		if req.WaterMode != nil {
			mode := parseWaterMode(*req.WaterMode)
			if strings.TrimSpace(*req.WaterMode) != "" && mode == "" {
				response.ErrorBadRequest(c, "水费计费方式无效，应为按吨（meter）或包月（monthly）")
				return
			}
			if mode != "" {
				bill.WaterMode = mode
			}
		}
		switch {
		case req.WaterPrice != nil && *req.WaterPrice > 0:
			bill.WaterPrice = *req.WaterPrice
		case bill.WaterMode != baseWaterMode:
			// 方式变了却没给金额：按新方式取该房生效设置的金额。
			bill.WaterPrice = waterAmountForMode(db, room, bill.WaterMode)
		case req.WaterPrice != nil:
			// 同方式下显式传 0：尊重调用方（账单为快照，允许本期不收水费）。
			bill.WaterPrice = *req.WaterPrice
		}
		if req.ElecPrice != nil {
			bill.ElecPrice = *req.ElecPrice
		}
		if req.GasPrice != nil {
			bill.GasPrice = *req.GasPrice
		}
		if req.Rent != nil {
			bill.Rent = *req.Rent
		}
		if strings.TrimSpace(req.TenantName) != "" {
			bill.TenantName = strings.TrimSpace(req.TenantName)
		}
		// 包月水费不看抄表，读数倒挂不再拦；按吨仍要求本月不小于上月。
		if (bill.WaterNow < bill.WaterLast && normalizeWaterMode(bill.WaterMode) == waterModeMeter) ||
			bill.ElecNow < bill.ElecLast || bill.GasNow < bill.GasLast {
			response.ErrorBadRequest(c, "本月读数不能小于上月读数")
			return
		}
		customs := applyCustomFees(db, room, bill)
		bill.recalc()
		if err := db.Create(bill).Error; err != nil {
			response.ErrorInternal(c, "创建账单失败")
			return
		}
		// 单据号依赖账单 ID，落库后补写。
		bill.ReceiptNo = receiptNo(bill.Period, bill.RoomNo, bill.ID)
		db.Model(bill).UpdateColumn("receipt_no", bill.ReceiptNo)
		syncBillItems(db, bill, customs)
		// 当期读数沉淀进抄表台账（该月已有记录时不覆盖）。
		settleMeterRecord(db, bill)
		response.Success(c, bill)
	}
}

// handleBillGenerate 一键生成某月账单：对全部有在租租户且当月尚无账单的
// 房间开票，其余跳过。返回创建/跳过数量供前端提示。
func handleBillGenerate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Period string `json:"period" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请选择账期")
			return
		}
		req.Period = strings.TrimSpace(req.Period)
		if !isValidPeriod(req.Period) {
			response.ErrorBadRequest(c, "账期格式应为 YYYY-MM")
			return
		}

		userID := currentUserID(c)
		// 只为有在租租户的房间开票；空置房间不产生账单。
		rooms := make([]Room, 0)
		if err := db.Where(
			"user_id = ? AND id IN (SELECT room_id FROM tenants WHERE user_id = ? AND active = ?)",
			userID, userID, true).Order("room_no ASC").Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		created, skipped := 0, 0
		for i := range rooms {
			var existing int64
			db.Model(&Bill{}).Where("user_id = ? AND room_id = ? AND period = ?",
				userID, rooms[i].ID, req.Period).Count(&existing)
			if existing > 0 {
				skipped++
				continue
			}
			// 季付房只在账单月出账（与最近一张账期整差 3 个月），
			// 中间月份跳过；手动开票不受此限制，保留自由度。
			if !onBillSchedule(db, &rooms[i], req.Period) {
				skipped++
				continue
			}
			bill := newBillFromRoom(db, &rooms[i], req.Period)
			customs := applyCustomFees(db, &rooms[i], bill)
			bill.recalc()
			if err := db.Create(bill).Error; err != nil {
				response.ErrorInternal(c, "生成账单失败")
				return
			}
			// 单据号依赖账单 ID，落库后补写。
			db.Model(bill).UpdateColumn("receipt_no", receiptNo(bill.Period, bill.RoomNo, bill.ID))
			syncBillItems(db, bill, customs)
			created++
		}
		response.Success(c, gin.H{"created": created, "skipped": skipped})
	}
}

// handleBillUpdate 编辑账单：读数、单价、各项费用与已收金额可改，
// 费用与合计一律服务端重算，状态联动。
func handleBillUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bill, ok := findUserBill(db, c)
		if !ok {
			return
		}
		var req billRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "参数错误")
			return
		}
		if !isValidPeriod(req.Period) {
			response.ErrorBadRequest(c, "账期格式应为 YYYY-MM")
			return
		}
		if req.PaidAmount < 0 {
			response.ErrorBadRequest(c, "已收金额不能为负数")
			return
		}

		bill.Period = req.Period
		bill.TenantName = strings.TrimSpace(req.TenantName)
		bill.Rent = req.Rent
		bill.WaterLast, bill.WaterNow = req.WaterLast, req.WaterNow
		bill.ElecLast, bill.ElecNow = req.ElecLast, req.ElecNow
		bill.GasLast, bill.GasNow = req.GasLast, req.GasNow
		// water_mode 未传则保持账单原有计费方式（包月/按吨），老客户端不受影响；
		// 传了非法值直接拒绝，避免被静默按"按吨"处理。
		if v := strings.TrimSpace(req.WaterMode); v != "" {
			mode := parseWaterMode(v)
			if mode == "" {
				response.ErrorBadRequest(c, "水费计费方式无效，应为按吨（meter）或包月（monthly）")
				return
			}
			// 切换计费方式且没带金额时，按该房当前生效设置取新方式的金额：
			// 与水费单位匹配，避免包月沿用元/吨单价（或反之）算出离谱水费。
			if mode != normalizeWaterMode(bill.WaterMode) && req.WaterPrice <= 0 {
				var room Room
				if err := db.Where("id = ? AND user_id = ?", bill.RoomID, bill.UserID).First(&room).Error; err == nil {
					req.WaterPrice = waterAmountForMode(db, &room, mode)
				}
			}
			bill.WaterMode = mode
		}
		bill.WaterPrice, bill.ElecPrice, bill.GasPrice = req.WaterPrice, req.ElecPrice, req.GasPrice
		bill.SanitationFee = req.SanitationFee
		bill.ManagementFee = req.ManagementFee
		bill.Remark = req.Remark

		bill.PaidAmount = round2(req.PaidAmount)
		if bill.PaidAmount > feeTolerance {
			if bill.PaidAt == nil {
				now := time.Now()
				bill.PaidAt = &now
			}
		} else {
			bill.PaidAt = nil
		}
		bill.recalc()

		if err := db.Save(bill).Error; err != nil {
			response.ErrorInternal(c, "保存账单失败")
			return
		}
		syncBillItemsOnUpdate(db, bill)
		response.Success(c, bill)
	}
}

// syncBillItemsOnUpdate 编辑保存后重建明细：内置六项按新值重生成，
// 自定义项目明细行保留原金额（编辑表单不含自定义项）。
func syncBillItemsOnUpdate(db *gorm.DB, bill *Bill) {
	customs := make([]BillItem, 0)
	db.Where("bill_id = ? AND fee_key LIKE ?", bill.ID, "custom_%").Find(&customs)
	syncBillItems(db, bill, customs)
}

// handleBillPay 登记一笔收款：写入收款流水（可按项目分摊），累加已收金额并联动状态。
// items 传了时按项目校验分摊（累计不超过该项目应付），没传视为整体收款不分摊。
func handleBillPay(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bill, ok := findUserBill(db, c)
		if !ok {
			return
		}
		var req struct {
			Amount float64       `json:"amount" binding:"required"`
			Note   string        `json:"note"`
			Items  []payItemReq  `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
			response.ErrorBadRequest(c, "收款金额必须大于 0")
			return
		}

		// 项目分摊：明细行应付 - 已收 = 该项目还差多少；本次每项不得超过。
		shares := make([]PaymentItem, 0, len(req.Items))
		var shareSum float64
		if len(req.Items) > 0 {
			charges, paids := billItemChargesAndPaids(db, bill)
			for _, it := range req.Items {
				charge, okCharge := charges[it.Key]
				if !okCharge || charge <= feeTolerance {
					response.ErrorBadRequest(c, "收款项目无效或该项本期不收费")
					return
				}
				share := round2(it.Amount)
				if share <= 0 {
					continue
				}
				if share+paids[it.Key] > charge+feeTolerance {
					response.ErrorBadRequest(c, "「"+it.Name+"」本次收款超过该项目欠缴金额")
					return
				}
				shareSum = round2(shareSum + share)
				shares = append(shares, PaymentItem{Key: it.Key, Name: it.Name, Amount: share})
			}
			if shareSum > req.Amount+feeTolerance {
				response.ErrorBadRequest(c, "分摊合计与收款金额不一致")
				return
			}
		}

		// 收款不能超过账单应付合计。
		if round2(bill.PaidAmount+req.Amount) > bill.TotalAmount+feeTolerance {
			response.ErrorBadRequest(c, "收款金额超过账单应付合计")
			return
		}

		bill.PaidAmount = round2(bill.PaidAmount + req.Amount)
		now := time.Now()
		bill.PaidAt = &now
		bill.recalc()

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Save(bill).Error; err != nil {
				return err
			}
			return tx.Create(&Payment{
				UserID: bill.UserID, BillID: bill.ID,
				PaidAt: now, Amount: round2(req.Amount),
				Items: paymentItemsJSON(shares), Note: strings.TrimSpace(req.Note),
			}).Error
		})
		if err != nil {
			response.ErrorInternal(c, "登记收款失败")
			return
		}
		response.Success(c, bill)
	}
}

// payItemReq 收款分摊入参行。
type payItemReq struct {
	Key    string  `json:"key" binding:"required"`
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
}

// billItemChargesAndPaids 汇总账单各项目应付与已收（已收从收款流水分摊累计）。
func billItemChargesAndPaids(db *gorm.DB, bill *Bill) (map[string]float64, map[string]float64) {
	charges := map[string]float64{}
	paids := map[string]float64{}
	items := make([]BillItem, 0)
	db.Where("bill_id = ?", bill.ID).Find(&items)
	for _, it := range items {
		charges[it.FeeKey] = round2(charges[it.FeeKey] + it.Amount)
	}
	payments := make([]Payment, 0)
	db.Where("bill_id = ?", bill.ID).Order("paid_at ASC, id ASC").Find(&payments)
	for _, p := range payments {
		for _, share := range paymentItems(p.Items) {
			paids[share.Key] = round2(paids[share.Key] + share.Amount)
		}
	}
	return charges, paids
}

func handleBillDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bill, ok := findUserBill(db, c)
		if !ok {
			return
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Delete(bill).Error; err != nil {
				return err
			}
			if err := tx.Where("bill_id = ?", bill.ID).Delete(&BillItem{}).Error; err != nil {
				return err
			}
			return tx.Where("bill_id = ?", bill.ID).Delete(&Payment{}).Error
		})
		if err != nil {
			response.ErrorInternal(c, "删除账单失败")
			return
		}
		response.Success(c, nil)
	}
}

func loadRoom(db *gorm.DB, c *gin.Context, roomID uint) (*Room, bool) {
	var room Room
	if err := db.Where("id = ? AND user_id = ?", roomID, currentUserID(c)).First(&room).Error; err != nil {
		response.ErrorNotFound(c, "房源不存在")
		return nil, false
	}
	return &room, true
}

func findUserBill(db *gorm.DB, c *gin.Context) (*Bill, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的账单 ID")
		return nil, false
	}
	var bill Bill
	if err := db.Where("id = ? AND user_id = ?", id, currentUserID(c)).First(&bill).Error; err != nil {
		response.ErrorNotFound(c, "账单不存在")
		return nil, false
	}
	return &bill, true
}

// isValidPeriod 校验 YYYY-MM 格式。
func isValidPeriod(p string) bool {
	if len(p) != 7 || p[4] != '-' {
		return false
	}
	_, err := time.ParseInLocation("2006-01", p, time.Local)
	return err == nil
}
