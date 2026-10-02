// fees.go — 自定义收费项目、账单费用明细与收款流水。
//
// 模型关系：
//   FeeItem  收费项目定义（内置租金/水/电/燃气/卫生/管理 + 用户自定义，如宽带费），
//            每个项目有自己的收费周期（fixed 类 monthly/quarterly；读数类恒月付）。
//   BillItem 账单费用明细快照：出账时把账单各项（内置六项 + 启用的自定义项目）
//            落成明细行，带名称/金额/覆盖区间说明；账单编辑后重建。
//   Payment  收款流水：每次收款一条，items JSON 按项目分摊（账单是账单、缴费是
//            缴费——总费用、已付、欠缴以及"欠哪些项目"由明细与流水算出）。
//
// 兼容策略：bills 既有固定列（rent/water/...）继续承担内置项目计费与读数；
// 自定义项目金额汇总进 bills.extra_amount；TotalAmount = 六项 + extra。
package rental

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 收费项目种类：fixed = 固定金额按周期收（租金/卫生/管理/宽带…）；
// meter = 按抄表读数计费（水/电/燃气，恒月付）。
const (
	feeKindFixed = "fixed"
	feeKindMeter = "meter"

	// 内置项目 key（与 bills 固定列一一对应）。
	feeKeyRent        = "rent"
	feeKeyWater       = "water"
	feeKeyElec        = "elec"
	feeKeyGas         = "gas"
	feeKeySanitation  = "sanitation"
	feeKeyManagement  = "management"
	feeKeyUnallocated = "_unallocated" // 升级前历史收款：未按项目分摊
)

// FeeItem 收费项目定义。BuiltIn 项目不可删除（bills 固定列依赖），可改名、
// 改周期（fixed 类）、停用；自定义项目由用户添加（宽带费、停车费等）。
// 数据共享后收费项目全员一套：UserID 是「录入人」戳，内置项目种子为 0。
type FeeItem struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	UserID        uint      `gorm:"index" json:"-"`
	Key           string    `gorm:"not null;index" json:"key"`
	Name          string    `gorm:"not null" json:"name"`
	Kind          string    `gorm:"not null" json:"kind"` // fixed / meter
	Unit          string    `json:"unit"`                 // meter 类计量单位（吨/度/方），fixed 为空
	Cycle         string    `gorm:"default:monthly" json:"cycle"` // fixed 类收费周期；meter 类恒月付
	DefaultAmount float64   `gorm:"default:0" json:"default_amount"` // fixed 类默认金额（元/期）
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	Sort          int       `json:"sort"`
	BuiltIn       bool      `gorm:"default:false" json:"built_in"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// BillItem 账单费用明细快照。账单是账单、缴费是缴费：应付按行看，已收从
// Payment.Items 按 key 汇总，差额即"该项目还差多少"。
type BillItem struct {
	ID      uint    `gorm:"primarykey" json:"id"`
	UserID  uint    `gorm:"index" json:"-"`
	BillID  uint    `gorm:"index;uniqueIndex:idx_rental_bill_item_key" json:"bill_id"`
	FeeKey  string  `gorm:"not null;uniqueIndex:idx_rental_bill_item_key" json:"fee_key"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Amount  float64 `json:"amount"`
	Detail  string  `json:"detail"` // 计费说明快照：读数/单价/覆盖区间
	Cycle   string  `json:"cycle"`  // 该项目本次收费周期快照
}

// Payment 收款流水。Items 是 []PaymentItem 的 JSON：某次收款缴了哪些项目、
// 各缴了多少；空数组表示升级前的历史收款（未按项目分摊）。
type Payment struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`
	BillID    uint      `gorm:"index" json:"bill_id"`
	PaidAt    time.Time `json:"paid_at"`
	Amount    float64   `json:"amount"`
	Items     string    `gorm:"default:[]" json:"-"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// PaymentItem 收款分摊行（JSON 存储）。
type PaymentItem struct {
	Key    string  `json:"key"`
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
}

// paymentItems / paymentItemsJSON 序列化互转，坏数据按空处理。
func paymentItems(raw string) []PaymentItem {
	items := make([]PaymentItem, 0)
	if strings.TrimSpace(raw) == "" {
		return items
	}
	_ = json.Unmarshal([]byte(raw), &items)
	return items
}

func paymentItemsJSON(items []PaymentItem) string {
	data, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(data)
}

// defaultFeeItems 内置收费项目（首次访问时惰性 seed，全局一份）。
// 租金/卫生/管理是 fixed 类，默认跟随租户缴费周期；水/电/燃气是 meter 类恒月付。
func defaultFeeItems() []FeeItem {
	return []FeeItem{
		{Key: feeKeyRent, Name: "租金", Kind: feeKindFixed, Cycle: payCycleMonthly, Sort: 1, BuiltIn: true, Enabled: true},
		{Key: feeKeyWater, Name: "水费", Kind: feeKindMeter, Unit: "吨", Cycle: payCycleMonthly, Sort: 2, BuiltIn: true, Enabled: true},
		{Key: feeKeyElec, Name: "电费", Kind: feeKindMeter, Unit: "度", Cycle: payCycleMonthly, Sort: 3, BuiltIn: true, Enabled: true},
		{Key: feeKeyGas, Name: "燃气费", Kind: feeKindMeter, Unit: "方", Cycle: payCycleMonthly, Sort: 4, BuiltIn: true, Enabled: true},
		{Key: feeKeySanitation, Name: "卫生费", Kind: feeKindFixed, Cycle: payCycleMonthly, Sort: 5, BuiltIn: true, Enabled: true},
		{Key: feeKeyManagement, Name: "管理费", Kind: feeKindFixed, Cycle: payCycleMonthly, Sort: 6, BuiltIn: true, Enabled: true},
	}
}

// ensureFeeItems 惰性 seed：还没有任何收费项目定义时写入内置六项。
func ensureFeeItems(db *gorm.DB) {
	var count int64
	db.Model(&FeeItem{}).Count(&count)
	if count > 0 {
		return
	}
	items := defaultFeeItems()
	db.Create(&items)
}

// enabledCustomFeeItems 启用的自定义（fixed 类）收费项目，出账并入账单。
func enabledCustomFeeItems(db *gorm.DB) []FeeItem {
	items := make([]FeeItem, 0)
	db.Where("enabled = ? AND kind = ? AND built_in = ?",
		true, feeKindFixed, false).Order("sort ASC, id ASC").Find(&items)
	return items
}

// fixedItemDue 判断某个 fixed 类收费项目在 period 这个月是否到期应收。
// 月付租户每月都是固定费账单月，恒到期；季付租户与最近一张"含固定费"的
// 账期整差 3 个月及以上才到期（错过锚点也能补收），给更早月份补账时视为
// 到期（尊重开票自由度）。
func fixedItemDue(db *gorm.DB, room *Room, period string) bool {
	// 月付：每张账单都应收固定费（此前漏了这一分支，导致月付租户连续
	// 生成账单时第二个月起租金/卫生费/管理费被清零）。
	if effectiveRoomBilling(db, room).PayCycle != payCycleQuarterly {
		return true
	}
	return quarterAnchorDue(db, room, period)
}

// quarterAnchorDue 季付锚点到期判断：与最近一张"含固定费"的账期整差
// 3 个月及以上才算到期。季付自定义收费项目（无论房间缴费周期）共用此口径。
func quarterAnchorDue(db *gorm.DB, room *Room, period string) bool {
	var last Bill
	err := db.Where(
		"room_id = ? AND (rent > ? OR sanitation_fee > ? OR management_fee > ? OR extra_amount > ?)",
		room.ID, 0, 0, 0, 0,
	).Order("period DESC").First(&last).Error
	if err != nil {
		return true // 首账月
	}
	diff := monthsBetween(last.Period, period)
	if diff <= 0 {
		return true // 补历史账单：显式开票，固定费照收
	}
	return diff >= 3
}

// setupFeeRoutes 收费项目 / 收款流水 / 统计分析的接口。
func setupFeeRoutes(api *gin.RouterGroup, db *gorm.DB) {
	read := requireAccess(db, AccessReadonly)
	edit := requireAccess(db, AccessEdit)
	full := requireAccess(db, AccessFull)
	api.GET("/rental/fee-items", read, handleFeeItemList(db))
	api.POST("/rental/fee-items", edit, handleFeeItemCreate(db))
	api.PUT("/rental/fee-items/:id", edit, handleFeeItemUpdate(db))
	api.DELETE("/rental/fee-items/:id", full, handleFeeItemDelete(db))
	api.GET("/rental/payments", read, handlePaymentList(db))
	api.GET("/rental/analytics", read, handleAnalytics(db))
}

func handleFeeItemList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ensureFeeItems(db)
		items := make([]FeeItem, 0)
		db.Order("built_in DESC, sort ASC, id ASC").Find(&items)
		response.Success(c, items)
	}
}

// feeItemRequest 新增/编辑入参。key 由服务端生成（内置项目 key 固定不可改）。
type feeItemRequest struct {
	Name          string  `json:"name" binding:"required"`
	Cycle         string  `json:"cycle"`
	DefaultAmount float64 `json:"default_amount"`
	Enabled       *bool   `json:"enabled"`
	Sort          *int    `json:"sort"`
}

func handleFeeItemCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req feeItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写项目名称")
			return
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			response.ErrorBadRequest(c, "项目名称不能为空")
			return
		}
		cycle := normalizePayCycle(req.Cycle)
		ensureFeeItems(db)

		item := FeeItem{
			UserID:        currentUserID(c), // 录入人戳
			Key:           "custom_" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Name:          name,
			Kind:          feeKindFixed,
			Cycle:         cycle,
			DefaultAmount: round2(req.DefaultAmount),
			Enabled:       req.Enabled == nil || *req.Enabled,
			Sort:          100,
		}
		if req.Sort != nil {
			item.Sort = *req.Sort
		}
		if err := db.Create(&item).Error; err != nil {
			response.ErrorInternal(c, "创建收费项目失败")
			return
		}
		response.Success(c, item)
	}
}

func handleFeeItemUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		var item FeeItem
		if err := db.First(&item, id).Error; err != nil {
			response.ErrorNotFound(c, "收费项目不存在")
			return
		}
		var req feeItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "参数错误")
			return
		}
		if name := strings.TrimSpace(req.Name); name != "" {
			item.Name = name
		}
		item.Cycle = normalizePayCycle(req.Cycle)
		item.DefaultAmount = round2(req.DefaultAmount)
		if req.Enabled != nil {
			item.Enabled = *req.Enabled
		}
		if req.Sort != nil {
			item.Sort = *req.Sort
		}
		if err := db.Save(&item).Error; err != nil {
			response.ErrorInternal(c, "保存收费项目失败")
			return
		}
		response.Success(c, item)
	}
}

func handleFeeItemDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, _ := strconv.Atoi(c.Param("id"))
		var item FeeItem
		if err := db.First(&item, id).Error; err != nil {
			response.ErrorNotFound(c, "收费项目不存在")
			return
		}
		if item.BuiltIn {
			response.ErrorBadRequest(c, "内置项目不可删除，可停用")
			return
		}
		if err := db.Delete(&item).Error; err != nil {
			response.ErrorInternal(c, "删除收费项目失败")
			return
		}
		response.Success(c, nil)
	}
}

// syncBillItems 把账单各项落成明细快照（先删后插，账单编辑后调用）。
// 内置六项从 bills 固定列生成；自定义 fixed 项目金额已在 ExtraAmount，
// 明细从传 入的 custom 行生成。
func syncBillItems(db *gorm.DB, bill *Bill, customs []BillItem) {
	db.Where("bill_id = ?", bill.ID).Delete(&BillItem{})
	items := billBuiltInItems(*bill)
	items = append(items, customs...)
	for i := range items {
		items[i].UserID = bill.UserID
		items[i].BillID = bill.ID
	}
	if len(items) > 0 {
		db.Create(&items)
	}
}

// billBuiltInItems 由 bills 固定列生成内置六项明细（金额为 0 的项不生成，
// 让"该账单收哪些项目"一目了然）。
func billBuiltInItems(b Bill) []BillItem {
	months := cycleMonths(b.PayCycle)
	covered := coveredPeriodText(b.Period, b.PayCycle)
	items := make([]BillItem, 0, 6)
	if b.Rent > 0 {
		items = append(items, BillItem{FeeKey: feeKeyRent, Name: "租金", Kind: feeKindFixed,
			Amount: b.Rent, Cycle: b.PayCycle,
			Detail: coveredItemDetail(covered, months, "")})
	}
	if normalizeWaterMode(b.WaterMode) == waterModeMonthly {
		if b.WaterFee > 0 {
			items = append(items, BillItem{FeeKey: feeKeyWater, Name: "水费（包月）", Kind: feeKindFixed,
				Amount: b.WaterFee, Cycle: payCycleMonthly,
				Detail: "包月 " + fmtAmount(b.WaterPrice) + " 元/月"})
		}
	} else if b.WaterFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeyWater, Name: "水费", Kind: feeKindMeter,
			Amount: b.WaterFee, Cycle: payCycleMonthly,
			Detail: "读数 " + fmtAmount(b.WaterLast) + " → " + fmtAmount(b.WaterNow) + " 吨 · 单价 " + fmtAmount(b.WaterPrice) + " 元/吨"})
	}
	if b.ElecFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeyElec, Name: "电费", Kind: feeKindMeter,
			Amount: b.ElecFee, Cycle: payCycleMonthly,
			Detail: "读数 " + fmtAmount(b.ElecLast) + " → " + fmtAmount(b.ElecNow) + " 度 · 单价 " + fmtAmount(b.ElecPrice) + " 元/度"})
	}
	if b.GasFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeyGas, Name: "燃气费", Kind: feeKindMeter,
			Amount: b.GasFee, Cycle: payCycleMonthly,
			Detail: "读数 " + fmtAmount(b.GasLast) + " → " + fmtAmount(b.GasNow) + " 方 · 单价 " + fmtAmount(b.GasPrice) + " 元/方"})
	}
	if b.SanitationFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeySanitation, Name: "卫生费", Kind: feeKindFixed,
			Amount: b.SanitationFee, Cycle: b.PayCycle,
			Detail: coveredItemDetail(covered, months, "")})
	}
	if b.ManagementFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeyManagement, Name: "管理费", Kind: feeKindFixed,
			Amount: b.ManagementFee, Cycle: b.PayCycle,
			Detail: coveredItemDetail(covered, months, "")})
	}
	return items
}

// coveredItemDetail 固定费项目的覆盖区间说明（月付=单月，季付=三个月区间）。
func coveredItemDetail(covered string, months int, extra string) string {
	if months > 1 {
		if extra != "" {
			return covered + " · " + extra
		}
		return covered
	}
	if extra != "" {
		return extra
	}
	return "单月"
}

func fmtAmount(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	return strings.TrimRight(strings.TrimRight(s, "0"), ".")
}

// customBillItems 按启用的自定义 fixed 项目生成账单明细行（金额按项目周期与
// 到期情况计：quarterly 项目到期时收 3 个月，monthly 每月收）。
// 租户勾选排除的项目（该房生效的"不参与计费项目"）不并入。
func customBillItems(db *gorm.DB, room *Room, period string) ([]BillItem, float64) {
	items := make([]BillItem, 0)
	excluded := excludedFeeSet(effectiveRoomExcludedFees(db, room))
	var extra float64
	for _, fi := range enabledCustomFeeItems(db) {
		if excluded[fi.Key] {
			continue // 租户不参与该项目，本期不收
		}
		months := 1
		if fi.Cycle == payCycleQuarterly {
			// 季付自定义项目按自身周期锚点判断到期（月付房里不会因
			// 每月都有租金账单而每月重复收 3 个月）。
			if !quarterAnchorDue(db, room, period) {
				continue // 未到期，本期不收
			}
			months = 3
		}
		amount := round2(fi.DefaultAmount * float64(months))
		if amount <= 0 {
			continue
		}
		covered := coveredPeriodText(period, fi.Cycle)
		items = append(items, BillItem{
			FeeKey: fi.Key, Name: fi.Name, Kind: feeKindFixed,
			Amount: amount, Cycle: fi.Cycle,
			Detail: coveredItemDetail(covered, months, "自定义项目"),
		})
		extra = round2(extra + amount)
	}
	return items, extra
}

// handlePaymentList 收款流水（时间倒序 + 月份区间筛选），带账单快照字段。
func handlePaymentList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		from := strings.TrimSpace(c.Query("period_from"))
		to := strings.TrimSpace(c.Query("period_to"))
		keyword := strings.TrimSpace(c.Query("keyword"))

		type paymentRow struct {
			Payment
			RoomNo     string `json:"room_no"`
			TenantName string `json:"tenant_name"`
			Period     string `json:"period"`
		}
		query := db.Table("payments").
			Select("payments.*, bills.room_no as room_no, bills.tenant_name as tenant_name, bills.period as period").
			Joins("LEFT JOIN bills ON bills.id = payments.bill_id")
		if from != "" {
			query = query.Where("payments.paid_at >= ?", from+"-01 00:00:00")
		}
		if to != "" {
			query = query.Where("payments.paid_at < ?", addMonths(to, 1)+"-01 00:00:00")
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("bills.room_no LIKE ? OR bills.tenant_name LIKE ? OR payments.note LIKE ?", like, like, like)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询收款记录失败")
			return
		}
		rows := make([]paymentRow, 0)
		if err := query.Order("payments.paid_at DESC, payments.id DESC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询收款记录失败")
			return
		}
		// 明细 JSON 解析成结构化 items 供前端直接渲染。
		result := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			result = append(result, gin.H{
				"id": row.ID, "bill_id": row.BillID, "paid_at": row.PaidAt,
				"amount": row.Amount, "note": row.Note,
				"items": paymentItems(row.Items),
				"room_no": row.RoomNo, "tenant_name": row.TenantName, "period": row.Period,
			})
		}
		response.SuccessPage(c, result, total, page, pageSize)
	}
}

// handleAnalytics 统计分析：近 N 月应收/实收、项目收入构成、收缴率。
func handleAnalytics(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		months := utils.Atoi(c.Query("months"), 12)
		if months < 3 {
			months = 3
		}
		if months > 24 {
			months = 24
		}
		start := addMonths(currentPeriodStr(), -(months - 1)) + "-01"

		// 月度应收（按账期）与实收（按收款时间）。
		type monthRow struct {
			Period string  `json:"period"`
			Billed float64 `json:"billed"`
		}
		billedRows := make([]monthRow, 0)
		db.Model(&Bill{}).Select("period, SUM(total_amount) as billed").
			Where("period >= ?", start[:7]).
			Group("period").Order("period ASC").Scan(&billedRows)

		type paidRow struct {
			Month string  `json:"month"`
			Paid  float64 `json:"paid"`
		}
		paidRows := make([]paidRow, 0)
		db.Model(&Payment{}).Select("strftime('%Y-%m', paid_at) as month, SUM(amount) as paid").
			Where("paid_at >= ?", start).
			Group("month").Order("month ASC").Scan(&paidRows)

		paidByMonth := map[string]float64{}
		for _, r := range paidRows {
			paidByMonth[r.Month] = r.Paid
		}
		billedByMonth := map[string]float64{}
		for _, r := range billedRows {
			billedByMonth[r.Period] = r.Billed
		}

		series := make([]gin.H, 0, months)
		var sumBilled, sumReceived float64
		for i := months - 1; i >= 0; i-- {
			period := addMonths(currentPeriodStr(), -i)
			billed := round2(billedByMonth[period])
			received := round2(paidByMonth[period])
			sumBilled = round2(sumBilled + billed)
			sumReceived = round2(sumReceived + received)
			series = append(series, gin.H{"period": period, "billed": billed, "received": received})
		}

		// 项目收入构成：区间内账单明细按项目汇总。
		type itemRow struct {
			Name   string  `json:"name"`
			Amount float64 `json:"amount"`
		}
		itemRows := make([]itemRow, 0)
		db.Table("bill_items").Select("name, SUM(amount) as amount").
			Where("bill_id IN (SELECT id FROM bills WHERE period >= ?)", start[:7]).
			Group("name").Order("amount DESC").Scan(&itemRows)

		// 当前欠缴总额（全部未缴清账单）。
		var arrears float64
		db.Model(&Bill{}).Where("status <> ?", billStatusPaid).
			Select("COALESCE(SUM(total_amount - paid_amount), 0)").Scan(&arrears)
		arrears = round2(arrears)

		rate := 0.0
		if sumBilled > 0 {
			rate = round2(sumReceived / sumBilled * 100)
		}
		response.Success(c, gin.H{
			"series": series,
			"items":  itemRows,
			"summary": gin.H{
				"billed": sumBilled, "received": sumReceived,
				"arrears": arrears, "rate": rate,
			},
		})
	}
}

// currentPeriodStr 当前月份 YYYY-MM。
func currentPeriodStr() string {
	return time.Now().Format("2006-01")
}

// registerFeeModels 注册新表并执行数据迁移（历史收款/明细回填）。
func registerFeeModels() {
	database.RegisterModels(&FeeItem{}, &BillItem{}, &Payment{})

	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.3.0",
		Name:    "rental_fee_items_payments",
		Upgrade: func(db *gorm.DB) error {
			// 历史收款入流水（只跑一次：升级按版本记录）。升级前收款只有总额，
			// 未按项目分摊，Items 记空数组，账单层面仍正确。
			var paidCount int64
			db.Model(&Bill{}).Where("paid_amount > ?", 0).Count(&paidCount)
			if paidCount > 0 {
				var payCount int64
				db.Model(&Payment{}).Count(&payCount)
				if payCount == 0 {
					bills := make([]Bill, 0)
					db.Where("paid_amount > ?", 0).Find(&bills)
					for i := range bills {
						paidAt := bills[i].CreatedAt
						if bills[i].PaidAt != nil {
							paidAt = *bills[i].PaidAt
						}
						db.Create(&Payment{
							UserID: bills[i].UserID, BillID: bills[i].ID,
							PaidAt: paidAt, Amount: bills[i].PaidAmount,
							Items: "[]", Note: "历史收款（升级前登记）",
						})
					}
				}
			}
			// 存量账单回填费用明细。
			var itemCount int64
			db.Model(&BillItem{}).Count(&itemCount)
			if itemCount == 0 {
				bills := make([]Bill, 0)
				db.Find(&bills)
				for i := range bills {
					syncBillItems(db, &bills[i], nil)
				}
			}
			return nil
		},
	})
}

func init() {
	registerFeeModels()
}
