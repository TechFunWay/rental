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
	// 读（列表/详情/单据）用并集口径；写（开票/生成/编辑/收款/删除）用单台账
	// 口径——开票与收款必须落在本次请求指定的台账里并达到档位（详见 rooms.go）。
	view := requireModule(db, ModuleBills, PermView)
	edit := requireLedgerModule(db, ModuleBills, PermEdit)
	full := requireLedgerModule(db, ModuleBills, PermFull)
	api.GET("/rental/bills", view, handleBillList(db))
	api.GET("/rental/bills/periods", view, handleBillPeriods(db))
	api.GET("/rental/bills/receipts", view, handleBillReceipts(db))
	api.GET("/rental/bills/:id", view, handleBillDetail(db))
	api.POST("/rental/bills", edit, handleBillCreate(db))
	api.POST("/rental/bills/generate", edit, handleBillGenerate(db))
	api.PUT("/rental/bills/:id", edit, handleBillUpdate(db))
	api.POST("/rental/bills/:id/pay", edit, handleBillPay(db))
	api.DELETE("/rental/bills/:id", full, handleBillDelete(db))
}

// billRequest 是编辑账单的入参。费用三项（water_fee 等）与合计由服务端
// 按读数与单价重算，客户端传入值一律忽略，保证口径唯一。
//
// **字段一律用指针，「未传」= 保留原值**（2026-10-08 改）：
// 编辑账单是**部分更新**，客户端只想改读数时不该连带清掉别的字段。
// 此前用值类型，「未传」会被解析成零值直接覆盖，曾导致：
//   - 只改读数 → 水价/电价被清成 0（水费凭空归零）；
//   - 只改读数 → 已收金额被清成 0，账单变「未收」，**但收款流水还在**——
//     钱收了、流水记着、账单却显示欠款，欠缴统计凭空多出欠款。
//
// 指针能区分「显式传 0」与「没传」，两种意图都表达得出来。
//
// 特例：RoomID / Period 是定位与账期，必须显式给（binding:required）。
type billRequest struct {
	RoomID        uint     `json:"room_id" binding:"required"`
	Period        string   `json:"period" binding:"required"`
	TenantName    *string  `json:"tenant_name"`
	Rent          *float64 `json:"rent"`
	WaterLast     *float64 `json:"water_last"`
	WaterNow      *float64 `json:"water_now"`
	ElecLast      *float64 `json:"elec_last"`
	ElecNow       *float64 `json:"elec_now"`
	GasLast       *float64 `json:"gas_last"`
	GasNow        *float64 `json:"gas_now"`
	WaterMode     string   `json:"water_mode"`
	WaterPrice    *float64 `json:"water_price"`
	ElecPrice     *float64 `json:"elec_price"`
	GasPrice      *float64 `json:"gas_price"`
	SanitationFee *float64 `json:"sanitation_fee"`
	ManagementFee *float64 `json:"management_fee"`
	// DepositFee 押金：只在首张账单上有值。传了才覆盖，不传原样保留
	// ——否则「只改读数」的编辑会把押金清零。
	DepositFee *float64 `json:"deposit_fee"`
	PaidAmount *float64 `json:"paid_amount"`
	Remark     *string  `json:"remark"`
}

// setIf 传了才覆盖（部分更新的统一写法，避免到处写 nil 判断）。
func setIf[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
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
	b.TotalAmount = round2(b.Rent + b.WaterFee + b.ElecFee + b.GasFee + b.SanitationFee + b.ManagementFee + b.DepositFee + b.ExtraAmount)
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

// configFloat 读取数值配置，读不到或非法时返回默认值。租房设置已升级为
// 系统级共享（user_id=0），调用方一律传 0。
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

// defaultPrices 读取系统级默认单价（系统配置里的"租房设置"组，全员共享）。
func defaultPrices(db *gorm.DB) (water, elec, gas float64) {
	return configFloat(db, "rental_water_price", 0, 5.0),
		configFloat(db, "rental_elec_price", 0, 1.2),
		configFloat(db, "rental_gas_price", 0, 3.5)
}

// activeTenantNames 返回房间在租租户姓名（多人顿号连接，作为账单快照）。
// ledgerID 非 0 时限定同台账，杜绝跨台账孤儿租户进了账单抬头。
func activeTenantNames(db *gorm.DB, roomID uint, ledgerID uint) string {
	names := make([]string, 0, 4)
	q := db.Model(&Tenant{}).Where("room_id = ? AND active = ?", roomID, true)
	if ledgerColumnReady(db, "tenants") {
		q = q.Where("org_id = ?", ledgerID)
	}
	q.Order("id ASC").Limit(20).Pluck("name", &names)
	return strings.Join(names, "、")
}

// prefillReadings 计算开票时的"上月读数"：优先取上一期账单的本月读数，
// 无历史账单则取房间底数，再无则为 0。
// 上一期账单只在同台账内找：别台账的同房号账单不能拿来当读数衔接。
func prefillReadings(db *gorm.DB, room *Room) (waterLast, elecLast, gasLast float64) {
	var prev Bill
	q := db.Where("room_id = ?", room.ID)
	if ledgerColumnReady(db, "bills") {
		q = q.Where("org_id = ?", room.LedgerID)
	}
	err := q.Order("period DESC").First(&prev).Error
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
	_, elec, gas := defaultPrices(db)
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
		UserID: room.UserID,
		// 账单归属随房间：派生账单（一键生成/开票预填）也不会跨台账。
		LedgerID:      room.LedgerID,
		RoomID:        room.ID,
		Period:        period,
		RoomNo:        room.RoomNo,
		TenantName:    activeTenantNames(db, room.ID, room.LedgerID),
		Rent:          round2(room.DefaultRent * months),
		DepositFee:    firstBillDeposit(db, room, period),
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

// firstBillDeposit 返回「本期该不该收押金、收多少」。
//
// 规则（2026-10-11 粉丝需求）：新租户入住第一次，押金要和房租在同一张账单里一起收。
//   - 金额取该房在租租户的 Tenant.Deposit（没填或 0 → 不收）；
//   - **该房在本台账内已有任何账单就不再收**——押金一辈子只收一次。
//
// 为什么判定是「已有任何账单」而不是「已有更早账期」：房东可能先开当月账单、
// 事后又补开上个月的老账期。若只看「更早」，补开的那张会被当成首张，押金再收一遍
// （实测踩到：先开 2026-10，再补 2026-09，两张各收一次 2000）。
// 所以只要该房已有账单记录，后续无论开哪个账期都不再收押金。
//
// 刻意不做的事：不给已存在的历史账单补收押金（历史账单原样不动）。
func firstBillDeposit(db *gorm.DB, room *Room, period string) float64 {
	var existing int64
	q := db.Model(&Bill{}).Where("room_id = ?", room.ID)
	if ledgerColumnReady(db, "bills") {
		q = q.Where("org_id = ?", room.LedgerID)
	}
	if err := q.Count(&existing).Error; err != nil || existing > 0 {
		return 0
	}
	return activeRoomDeposit(db, room)
}

// activeRoomDeposit 该房在租租户的押金：多人同住取第一位的押金（与
// activeTenantNames / effectiveRoomBilling 同口径：按 id 升序取第一个非零值）。
// 押金是"整套房"的钱，不按人头累加。
func activeRoomDeposit(db *gorm.DB, room *Room) float64 {
	tenants := activeRoomTenants(db, room.ID, roomLedgerRef(room))
	for _, t := range tenants {
		if t.Deposit > 0 {
			return round2(t.Deposit)
		}
	}
	return 0
}

// applyCustomFees 把启用的自定义收费项目并入账单（金额合计进 ExtraAmount），
// 返回明细行供落库。在建账/生成的读数覆盖处理完成后、最终 recalc 前调用。
func applyCustomFees(db *gorm.DB, room *Room, bill *Bill) []BillItem {
	customs, extra := customBillItems(db, room, bill.Period)
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
		roomID := utils.Atoi(c.Query("room_id"), 0)
		community := strings.TrimSpace(c.Query("community"))

		// v0.6.0：只列本次请求可见台账内的账单。
		query, empty := scopeLedger(db.Model(&Bill{}), c)
		if empty {
			response.SuccessPage(c, []Bill{}, 0, page, pageSize)
			return
		}
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
		if roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		if community != "" {
			// 按小区/村组筛账单：房源限定在可见台账内，避免跨台账同名小区串数据
			query = query.Where("room_id IN (SELECT id FROM rooms WHERE community = ? AND org_id IN ?)",
				community, visibleLedgerIDs(c))
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
		// 账期下拉按可见台账收窄：别台账的账期不该出现在筛选器里。
		pq, empty := scopeLedger(db.Model(&Bill{}), c)
		if empty {
			response.Success(c, periods)
			return
		}
		pq.Distinct("period").Order("period DESC").Limit(36).Pluck("period", &periods)
		response.Success(c, periods)
	}
}

// handleBillReceipts 按账期区间批量取收费单据打印数据：单据抬头一份，
// 每张账单附费用明细行（与账单详情同口径）。区间两端皆可省略；上限
// 500 张，超出按「账期、房号」排序截断，前端提示分批打印。
func handleBillReceipts(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		from, to := strings.TrimSpace(c.Query("from")), strings.TrimSpace(c.Query("to"))
		if from != "" && !isValidPeriod(from) {
			response.ErrorBadRequest(c, "起始账期格式应为 YYYY-MM")
			return
		}
		if to != "" && !isValidPeriod(to) {
			response.ErrorBadRequest(c, "截止账期格式应为 YYYY-MM")
			return
		}
		query, empty := scopeLedger(db.Model(&Bill{}), c)
		if empty {
			response.Success(c, gin.H{"property": gin.H{"name": "", "contact": "", "note": ""}, "bills": []gin.H{}})
			return
		}
		query = query.Order("period ASC, room_no ASC, id ASC").Limit(500)
		if from != "" {
			query = query.Where("period >= ?", from)
		}
		if to != "" {
			query = query.Where("period <= ?", to)
		}
		bills := make([]Bill, 0)
		if err := query.Find(&bills).Error; err != nil {
			response.ErrorInternal(c, "查询账单失败")
			return
		}

		rows := make([]gin.H, 0, len(bills))
		for i := range bills {
			bill := &bills[i]
			items := make([]BillItem, 0)
			// 明细按账单溯源即可保证归属；再按可见台账收窄是为了不让
			// 历史脏数据（跨台账孤儿明细）出现在单据里。
			iq, _ := scopeLedger(db.Where("bill_id = ?", bill.ID), c)
			iq.Order("id ASC").Find(&items)
			_, paids := billItemChargesAndPaids(db, c, bill)
			itemRows := make([]gin.H, 0, len(items))
			for _, it := range items {
				itemRows = append(itemRows, gin.H{
					"key": it.FeeKey, "name": it.Name, "kind": it.Kind,
					"amount": it.Amount, "detail": it.Detail, "cycle": it.Cycle,
					"paid":    paids[it.FeeKey],
					"arrears": round2(it.Amount - paids[it.FeeKey]),
				})
			}
			rows = append(rows, gin.H{"bill": bill, "items": itemRows})
		}

		// 抬头信息按**台账**取（管理员维护；台账无覆盖时回退系统级），整批共用
		// 一份——不同房东的出租方名称/电话本就不同。整批账单同属当前选中台账，
		// 取第一张的归属即可。
		headerLedger := LegacySharedLedgerID
		if len(bills) > 0 {
			headerLedger = bills[0].LedgerID
		}
		name, contact, note := receiptHeaderFor(db, headerLedger)
		response.Success(c, gin.H{
			"property": gin.H{"name": name, "contact": contact, "note": note},
			"bills":    rows,
		})
	}
}

// handleBillDetail 返回账单详情 + 单据抬头信息（出租方名称/电话/备注）。
func handleBillDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		bill, ok := findUserBill(db, c)
		if !ok {
			return
		}
		// 抬头信息按台账取（无覆盖时回退系统级），与该账单归属一致。
		name, contact, note := receiptHeaderFor(db, bill.LedgerID)

		items := make([]BillItem, 0)
		// 明细与收款都挂在账单下，账单的台账校验已过；仍按可见台账收窄，
		// 避免跨台账的孤儿明细/流水混进详情。
		iq, _ := scopeLedger(db.Where("bill_id = ?", bill.ID), c)
		iq.Order("id ASC").Find(&items)
		charges, paids := billItemChargesAndPaids(db, c, bill)
		itemRows := make([]gin.H, 0, len(items))
		for _, it := range items {
			itemRows = append(itemRows, gin.H{
				"key": it.FeeKey, "name": it.Name, "kind": it.Kind,
				"amount": it.Amount, "detail": it.Detail, "cycle": it.Cycle,
				"paid":    paids[it.FeeKey],
				"arrears": round2(it.Amount - paids[it.FeeKey]),
			})
		}
		_ = charges
		payRows := make([]gin.H, 0)
		payments := make([]Payment, 0)
		pq, _ := scopeLedger(db.Where("bill_id = ?", bill.ID), c)
		pq.Order("paid_at DESC, id DESC").Find(&payments)
		for _, p := range payments {
			payRows = append(payRows, gin.H{
				"id": p.ID, "paid_at": p.PaidAt, "amount": p.Amount,
				"note": p.Note, "items": paymentItems(p.Items),
			})
		}
		response.Success(c, gin.H{
			"bill":     bill,
			"items":    itemRows,
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
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleBills, PermEdit) {
			return
		}
		// 同房同月唯一是「按台账」的唯一：查重限定本次写入的台账，
		// 别的台账有同房同月账单不该挡住建账。
		writeLedger, ok := writeLedgerID(c)
		if !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			return
		}
		// 房间台账必须就是本次写入的台账：否则带 ledger_id=B 开 A 台账房间的票，
		// 会把账单写进 B 台账（或反之），绕过台账边界。
		if room.LedgerID != writeLedger {
			response.ErrorNotFound(c, "房源不存在")
			return
		}
		var existing int64
		db.Model(&Bill{}).Where("room_id = ? AND period = ? AND org_id = ?",
			req.RoomID, req.Period, writeLedger).Count(&existing)
		if existing > 0 {
			response.ErrorBadRequest(c, "该房间本月账单已存在")
			return
		}

		// UserID 录入人戳：谁开的票记谁；LedgerID 继承房间，杜绝跨台账孤儿账单。
		bill := newBillFromRoom(db, room, req.Period)
		bill.UserID = currentUserID(c)
		bill.LedgerID = room.LedgerID
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

		// 只为有在租租户的房间开票；空置房间不产生账单。
		// v0.6.0：房间与在租租户都限定在本次写入的台账内——一键生成只影响
		// 一个台账，不能顺手把别台账的账单也生成出来。
		writeLedger, ok := writeLedgerID(c)
		if !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			return
		}
		rooms := make([]Room, 0)
		if err := db.Where(
			"org_id = ? AND id IN (SELECT room_id FROM tenants WHERE active = ? AND org_id = ?)",
			writeLedger, true, writeLedger).Order("room_no ASC").Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		created, skipped := 0, 0
		for i := range rooms {
			var existing int64
			db.Model(&Bill{}).Where("room_id = ? AND period = ? AND org_id = ?",
				rooms[i].ID, req.Period, writeLedger).Count(&existing)
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
			bill.UserID = currentUserID(c)    // 录入人戳
			bill.LedgerID = rooms[i].LedgerID // 归属继承房间
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
		if !requireLedgerWrite(db, c, bill.LedgerID, ModuleBills, PermEdit) {
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
		if req.PaidAmount != nil && *req.PaidAmount < 0 {
			response.ErrorBadRequest(c, "已收金额不能为负数")
			return
		}

		// 部分更新：只覆盖客户端**显式传入**的字段，未传的一律保留原值。
		bill.Period = req.Period
		if req.TenantName != nil {
			bill.TenantName = strings.TrimSpace(*req.TenantName)
		}
		setIf(&bill.Rent, req.Rent)
		setIf(&bill.WaterLast, req.WaterLast)
		setIf(&bill.WaterNow, req.WaterNow)
		setIf(&bill.ElecLast, req.ElecLast)
		setIf(&bill.ElecNow, req.ElecNow)
		setIf(&bill.GasLast, req.GasLast)
		setIf(&bill.GasNow, req.GasNow)
		setIf(&bill.WaterPrice, req.WaterPrice)
		setIf(&bill.ElecPrice, req.ElecPrice)
		setIf(&bill.GasPrice, req.GasPrice)
		setIf(&bill.SanitationFee, req.SanitationFee)
		setIf(&bill.ManagementFee, req.ManagementFee)
		// 押金：传了才覆盖。不传保留原值，保证「只改读数」不会把押金弄丢。
		setIf(&bill.DepositFee, req.DepositFee)
		if req.Remark != nil {
			bill.Remark = *req.Remark
		}

		// water_mode 未传则保持账单原有计费方式（包月/按吨）；
		// 传了非法值直接拒绝，避免被静默按"按吨"处理。
		if v := strings.TrimSpace(req.WaterMode); v != "" {
			mode := parseWaterMode(v)
			if mode == "" {
				response.ErrorBadRequest(c, "水费计费方式无效，应为按吨（meter）或包月（monthly）")
				return
			}
			// 切换计费方式且没带金额（或带了 0）时，按该房当前生效设置取新方式的
			// 金额：与水费单位匹配，避免包月沿用元/吨单价（或反之）算出离谱水费。
			if mode != normalizeWaterMode(bill.WaterMode) &&
				(req.WaterPrice == nil || *req.WaterPrice <= 0) {
				var room Room
				// 按账单所属台账取房间：别台账的同号房不能决定本账单的水价口径。
				rq := db.Where("id = ?", bill.RoomID)
				if ledgerColumnReady(db, "rooms") {
					rq = rq.Where("org_id = ?", bill.LedgerID)
				}
				if err := rq.First(&room).Error; err == nil {
					bill.WaterPrice = waterAmountForMode(db, &room, mode)
				}
			}
			bill.WaterMode = mode
		}

		// 已收金额只在其被显式传入时改动，且连带维护 PaidAt。
		// 未传时**必须**原样保留：否则「只改读数」会把已收清零，
		// 而收款流水仍在——账单显示欠款、钱其实已收（见 billRequest 注释）。
		if req.PaidAmount != nil {
			bill.PaidAmount = round2(*req.PaidAmount)
			if bill.PaidAmount > feeTolerance {
				if bill.PaidAt == nil {
					now := time.Now()
					bill.PaidAt = &now
				}
			} else {
				bill.PaidAt = nil
			}
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
	cq := db.Where("bill_id = ? AND fee_key LIKE ?", bill.ID, "custom_%")
	if ledgerColumnReady(db, "bill_items") {
		cq = cq.Where("org_id = ?", bill.LedgerID)
	}
	cq.Find(&customs)
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
		if !requireLedgerWrite(db, c, bill.LedgerID, ModuleBills, PermEdit) {
			return
		}
		var req struct {
			Amount float64      `json:"amount" binding:"required"`
			Note   string       `json:"note"`
			Items  []payItemReq `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Amount <= 0 {
			response.ErrorBadRequest(c, "收款金额必须大于 0")
			return
		}

		// 项目分摊：明细行应付 - 已收 = 该项目还差多少；本次每项不得超过。
		shares := make([]PaymentItem, 0, len(req.Items))
		var shareSum float64
		if len(req.Items) > 0 {
			charges, paids := billItemChargesAndPaids(db, c, bill)
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
				UserID: currentUserID(c), BillID: bill.ID, // UserID 收款人戳
				// 收款流水继承账单台账：账单在哪个台账，钱就记在哪个台账。
				LedgerID: bill.LedgerID,
				PaidAt:   now, Amount: round2(req.Amount),
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
func billItemChargesAndPaids(db *gorm.DB, c *gin.Context, bill *Bill) (map[string]float64, map[string]float64) {
	charges := map[string]float64{}
	paids := map[string]float64{}
	items := make([]BillItem, 0)
	iq, _ := scopeLedger(db.Where("bill_id = ?", bill.ID), c)
	iq.Find(&items)
	for _, it := range items {
		charges[it.FeeKey] = round2(charges[it.FeeKey] + it.Amount)
	}
	payments := make([]Payment, 0)
	pq, _ := scopeLedger(db.Where("bill_id = ?", bill.ID), c)
	pq.Order("paid_at ASC, id ASC").Find(&payments)
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
		if !requireLedgerWrite(db, c, bill.LedgerID, ModuleBills, PermFull) {
			return
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Delete(bill).Error; err != nil {
				return err
			}
			// 明细与流水按账单 ID 级联清理，同时限定台账，避免误删跨台账孤儿行。
			iq, _ := scopeLedger(tx.Where("bill_id = ?", bill.ID), c)
			if err := iq.Delete(&BillItem{}).Error; err != nil {
				return err
			}
			pq, _ := scopeLedger(tx.Where("bill_id = ?", bill.ID), c)
			return pq.Delete(&Payment{}).Error
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
	// 数据共享后按 ID 直取，不再限定录入人；但必须校验台账归属，
	// 否则用构造出来的 room_id 就能往别的台账的房源上写子记录。
	if err := db.First(&room, roomID).Error; err != nil {
		response.ErrorNotFound(c, "房源不存在")
		return nil, false
	}
	if !inLedgerScope(c, room.LedgerID) {
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
	// 数据共享后按 ID 直取，不再限定录入人；但必须校验台账归属，
	// 否则换个 ID 就能读到/改到别台账的账单。
	if err := db.First(&bill, id).Error; err != nil {
		response.ErrorNotFound(c, "账单不存在")
		return nil, false
	}
	if !inLedgerScope(c, bill.LedgerID) {
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
