// fees.go — 自定义收费项目、账单费用明细与收款流水。
//
// 模型关系：
//
//	FeeItem  收费项目定义（内置租金/水/电/燃气/卫生/管理 + 用户自定义，如宽带费），
//	         每个项目有自己的收费周期（fixed 类 monthly/quarterly/semiannual/annual；读数类恒月付）。
//	BillItem 账单费用明细快照：出账时把账单各项（内置六项 + 启用的自定义项目）
//	         落成明细行，带名称/金额/覆盖区间说明；账单编辑后重建。
//	Payment  收款流水：每次收款一条，items JSON 按项目分摊（账单是账单、缴费是
//	         缴费——总费用、已付、欠缴以及"欠哪些项目"由明细与流水算出）。
//
// 兼容策略：bills 既有固定列（rent/water/...）继续承担内置项目计费与读数；
// 自定义项目金额汇总进 bills.extra_amount；TotalAmount = 六项 + extra。
package rental

import (
	"encoding/json"
	"fmt"
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
	feeKeyDeposit     = "deposit"      // 押金：只在租户首张账单上收一次
)

// FeeItem 收费项目定义。BuiltIn 项目不可删除（bills 固定列依赖），可改名、
// 改周期（fixed 类）、停用；自定义项目由用户添加（宽带费、停车费等）。
// 数据共享后收费项目全员一套：UserID 是「录入人」戳，内置项目种子为 0。
type FeeItem struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	UserID        uint      `gorm:"index" json:"-"`
	LedgerID      uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	Key           string    `gorm:"not null;index" json:"key"`
	Name          string    `gorm:"not null" json:"name"`
	Kind          string    `gorm:"not null" json:"kind"`            // fixed / meter
	Unit          string    `json:"unit"`                            // meter 类计量单位（吨/度/方），fixed 为空
	Cycle         string    `gorm:"default:monthly" json:"cycle"`    // fixed 类收费周期；meter 类恒月付
	DefaultAmount float64   `gorm:"default:0" json:"default_amount"` // fixed 类默认金额（元/期）
	// 生效月份窗口（1-12，0=不限）——「取暖费只在供暖季收」这类按季节出账的项目用它
	// （GitHub issue #5 需求 6）。
	//
	// 语义：只在**账期的月份**落在 [SeasonFromMonth, SeasonToMonth] 内时才计入该费用。
	//   - 两个都 0 → 全年都收，与改造前完全一致（存量项目零影响）；
	//   - 只填 From → 该月及之后都收；只填 To → 该月及之前都收；
	//   - **支持跨年**：From=11、To=3 表示 11/12/1/2/3 月都收（北方供暖季典型）；
	//   - From == To → 只有那一个月收。
	//
	// 它与 Cycle 是**两条独立的规则**：窗口决定「这个月收不收」，
	// Cycle 决定「收的时候是一个月还是 N 个月的钱」。例如
	//   Cycle=月付 + 11~3 月 → 这几个月每月收一次；
	//   Cycle=年付 + 11~3 月 → 窗口内按年付锚点收，不重复。
	SeasonFromMonth int `gorm:"default:0" json:"season_from_month"`
	SeasonToMonth   int `gorm:"default:0" json:"season_to_month"`
	// Enabled 启用/停用。**停用对内置项目同样生效**（2026-10-19 粉丝反馈修复）：
	// 停用后新生成/新编辑的账单不再收这一项（读数仍照常记录），单据上自然不再出现。
	// 历史账单是按开票时的快照计费的，停用不会改写已开出的账单——要隐藏历史账单上
	// 的那一行用 ReceiptHidden。
	Enabled bool `gorm:"default:true" json:"enabled"`
	// ReceiptHidden 单据不单列该项目（2026-10-19 粉丝反馈「单据能不能自定义显示项目」）。
	//
	// 语义：打印/预览收费单据时这一行不显示；若本期它仍有金额，则合并成一行
	// 「其他费用」显示（不列具体名称），保证**单据明细之和仍等于合计**。
	// 它只影响单据展示，账单详情、统计分析、收款分摊一律照常可见可算。
	ReceiptHidden bool      `gorm:"default:false" json:"receipt_hidden"`
	Sort          int       `json:"sort"`
	BuiltIn       bool      `gorm:"default:false" json:"built_in"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// feeItemInSeason 判断收费项目在 period 这个账期是否处于生效月份内。
//
// 两个边界都是 0 表示不限 → 恒 true（存量项目与不开窗口的项目行为完全不变）。
// 跨年写法（From > To，如 11 → 3）按「冬季环绕」处理。
func feeItemInSeason(fi FeeItem, period string) bool {
	from, to := fi.SeasonFromMonth, fi.SeasonToMonth
	if from == 0 && to == 0 {
		return true
	}
	// 解析失败时放行（按不限处理）：宁可照旧收，也不因脏账期静默漏收费用。
	t, err := time.ParseInLocation("2006-01", strings.TrimSpace(period), time.Local)
	if err != nil {
		return true
	}
	month := int(t.Month())
	switch {
	case from == 0:
		return month <= to // 只填结束月：该月及之前
	case to == 0:
		return month >= from // 只填起始月：该月及之后
	case from <= to:
		return month >= from && month <= to
	default:
		// 跨年：11 → 3 表示 11、12、1、2、3 月
		return month >= from || month <= to
	}
}

// validSeasonMonth 校验生效月份：只允许 0（不限）或 1-12。
func validSeasonMonth(v int) bool {
	return v == 0 || (v >= 1 && v <= 12)
}

// BillItem 账单费用明细快照。账单是账单、缴费是缴费：应付按行看，已收从
// Payment.Items 按 key 汇总，差额即"该项目还差多少"。
type BillItem struct {
	ID       uint    `gorm:"primarykey" json:"id"`
	UserID   uint    `gorm:"index" json:"-"`
	LedgerID uint    `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	BillID   uint    `gorm:"index;uniqueIndex:idx_rental_bill_item_key" json:"bill_id"`
	FeeKey   string  `gorm:"not null;uniqueIndex:idx_rental_bill_item_key" json:"fee_key"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	Amount   float64 `json:"amount"`
	Detail   string  `json:"detail"` // 计费说明快照：读数/单价/覆盖区间
	Cycle    string  `json:"cycle"`  // 该项目本次收费周期快照
}

// Payment 收款流水。Items 是 []PaymentItem 的 JSON：某次收款缴了哪些项目、
// 各缴了多少；空数组表示升级前的历史收款（未按项目分摊）。
type Payment struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
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

// defaultFeeItems 内置收费项目模板（每台账一份）。LedgerID 由 seed 处按目标台账填充。
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

// seedDefaultFeeItems 把内置六项写入指定台账。
func seedDefaultFeeItems(db *gorm.DB, ledgerID uint) {
	items := defaultFeeItems()
	for i := range items {
		items[i].LedgerID = ledgerID
	}
	db.Create(&items)
}

// ensureFeeItems 惰性 seed：指定台账内还没有任何收费项目定义时写入内置六项。
//
// v0.6.0 起收费项目按台账各一套：各台账的启用项目与自定义项目互不干扰。
// 老库在 ledger_id 列补齐前退回旧口径（按整表是否为空判断），避免启动期报
// 「no such column: ledger_id」。
func ensureFeeItems(db *gorm.DB, ledgerID uint) {
	var count int64
	q := db.Model(&FeeItem{})
	if !ledgerColumnReady(db, "fee_items") {
		q.Count(&count)
		if count > 0 {
			return
		}
		seedDefaultFeeItems(db, ledgerID)
		return
	}
	q.Where("org_id = ?", ledgerID).Count(&count)
	if count > 0 {
		return
	}
	seedDefaultFeeItems(db, ledgerID)
}

// enabledCustomFeeItems 指定台账启用的自定义（fixed 类）收费项目，出账并入账单。
func enabledCustomFeeItems(db *gorm.DB, ledgerID uint) []FeeItem {
	items := make([]FeeItem, 0)
	db.Where("enabled = ? AND kind = ? AND built_in = ? AND org_id = ?",
		true, feeKindFixed, false, ledgerID).Order("sort ASC, id ASC").Find(&items)
	return items
}

// disabledBuiltInFeeKeys 该台账里被**停用**的内置收费项目 key（新账单不再收这些项）。
//
// 为什么需要它（2026-10-19 粉丝反馈修复）：「停用」此前只对自定义项目生效——
// enabledCustomFeeItems 只查 built_in = false 的自定义项，而内置六项由
// billBuiltInItems 直接从 bills 固定列生成，**从不读 FeeItem.Enabled**。
// 于是房东停用燃气费后，账单照旧收、单据照旧印，停用开关形同虚设。
//
// 口径：内置项目的启停是**台账级计费开关**，开票时快照进账单（Bill.DisabledFees），
// 由 recalc 清零；历史账单按快照走，停用不会改写已开出的账单金额。
func disabledBuiltInFeeKeys(db *gorm.DB, ledgerID uint) []string {
	keys := make([]string, 0, 6)
	if db == nil {
		return keys
	}
	db.Model(&FeeItem{}).
		Where("built_in = ? AND enabled = ? AND org_id = ?", true, false, ledgerID).
		Order("sort ASC, id ASC").Pluck("key", &keys)
	return keys
}

// hiddenReceiptFeeKeys 该台账里被设为「单据不显示」的收费项目 key。
//
// 只影响单据展示（打印/预览）：单据上不单列这些项目；若本期它们仍有金额，
// 前端合并成一行「其他费用」，保证单据明细之和仍等于合计。
// 账单详情、统计分析、收款分摊一律不受影响。
func hiddenReceiptFeeKeys(db *gorm.DB, ledgerID uint) []string {
	keys := make([]string, 0, 6)
	if db == nil {
		return keys
	}
	db.Model(&FeeItem{}).
		Where("receipt_hidden = ? AND org_id = ?", true, ledgerID).
		Order("sort ASC, id ASC").Pluck("key", &keys)
	return keys
}

// fixedItemDue 判断固定费项目（租金/卫生费/管理费）在 period 这个月是否
// 到期应收。口径统一按**该房生效缴费周期的月数**判锚点：
//   - 月付：每个月都是锚点月，恒到期；
//   - 季付/半年付/年付：与最近一张"含固定费"的账期整差 months（3/6/12）个月
//     及以上才算到期（错过锚点也能补收），给更早月份补账时视为到期
//     （尊重开票自由度）。
//
// 关键：判据来自 effectiveRoomBilling（该房在租租户 → 台账 → 全局默认），
// 而不是写死的 quarterly —— 否则半年付/年付房会每月重复收 N 个月租金。
func fixedItemDue(db *gorm.DB, room *Room, period string) bool {
	months := cycleMonths(effectiveRoomBilling(db, room).PayCycle)
	if months <= 1 {
		// 月付：每月都应收固定费（此前漏了这一分支，导致月付租户连续
		// 生成账单时第二个月起租金/卫生费/管理费被清零）。
		return true
	}
	return cycleAnchorDue(db, room, period, months)
}

// cycleAnchorDue 锚点到期判断：与最近一张"含固定费"的账期整差 months 个月
// 及以上才算到期。months 由调用方传入——房间固定费按该房生效的缴费周期取值
// （见 fixedItemDue）。锚点只在同台账内找：别台账的同房号账单不能当作本台账的
// 收费锚点。
//
// 边界语义：
//   - 没有历史含固定费账单               → 首账月，到期；
//   - diff <= 0（给更早/同期月份补账）   → 显式开票，固定费照收；
//   - diff >= months                     → 已跨过一个完整周期，到期；
//   - 0 < diff < months                  → 周期内，本期固定费为 0。
func cycleAnchorDue(db *gorm.DB, room *Room, period string, months int) bool {
	if months <= 1 {
		return true // 月付：每月都是锚点
	}
	var last Bill
	q := db.Where(
		"room_id = ? AND (rent > ? OR sanitation_fee > ? OR management_fee > ? OR extra_amount > ?)",
		room.ID, 0, 0, 0, 0,
	)
	if ledgerColumnReady(db, "bills") {
		q = q.Where("org_id = ?", room.LedgerID)
	}
	err := q.Order("period DESC").First(&last).Error
	if err != nil {
		return true // 首账月
	}
	return anchorIntervalDue(last.Period, period, months)
}

// customItemAnchorDue 自定义收费项目的到期判断：锚点必须落在**该项目自己**
// 最近一次出现的账期上，不能借用"最近一张含固定费账单"。
//
// 为什么必须按项目单独找锚点（2026-10-12，issue #4 需求 2 实测）：月付房的
// 租金账单每月都含固定费，若共用房间级锚点，上一次固定费账单永远只差 1 个月，
// 半年付/年付的自定义项目（宽带费等）会**首月收过一次后再也不到期**。
// 按项目 key 找"最近一次收过它的账期"，N 个月一到就重新到期，与项目自身
// 周期严格对齐。
//
// 找不到历史（项目刚启用，或明细缺失）时按首账月到期处理，宁可先收一次，
// 也不让房东漏收；diff <= 0（补历史账）同样视为到期。
//
// lastByKey 由 customItemLastPeriods 一次性取好（避免每个项目一次查询的 N+1）。
func customItemAnchorDue(lastByKey map[string]string, feeKey, period string, months int) bool {
	if months <= 1 {
		return true // 月付项目每月到期
	}
	lastPeriod, ok := lastByKey[feeKey]
	if !ok || strings.TrimSpace(lastPeriod) == "" {
		return true // 该项目还没在账单里出现过：首账月到期
	}
	return anchorIntervalDue(lastPeriod, period, months)
}

// customItemLastPeriods 一次查出该房各收费项目**最近一次**出现在账单里的账期
// （key → period）。给 customBillItems 判锚点用，避免逐项目查库的 N+1。
// 明细随账单继承台账，再按账单台账收窄，杜绝跨台账孤儿明细充当锚点。
func customItemLastPeriods(db *gorm.DB, room *Room) map[string]string {
	type row struct {
		FeeKey string
		Period string
	}
	var rows []row
	q := db.Table("bill_items").
		Select("bill_items.fee_key AS fee_key, MAX(bills.period) AS period").
		Joins("JOIN bills ON bills.id = bill_items.bill_id").
		Where("bills.room_id = ?", room.ID).
		Group("bill_items.fee_key")
	if ledgerColumnReady(db, "bills") {
		q = q.Where("bills.org_id = ?", room.LedgerID)
	}
	if err := q.Scan(&rows).Error; err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.FeeKey] = r.Period
	}
	return out
}

// anchorIntervalDue 锚点区间判断：给定"上一次收费账期"与本期账期，判断是否
// 又跨过一个完整周期。diff <= 0（补历史账）一律到期，尊重开票自由度。
func anchorIntervalDue(lastPeriod, period string, months int) bool {
	diff := monthsBetween(lastPeriod, period)
	if diff <= 0 {
		return true
	}
	return diff >= months
}

// setupFeeRoutes 收费项目 / 收款流水 / 统计分析的接口。
func setupFeeRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 收费项在账单弹窗与偏好设置里用，归「账单」模块；收款流水与统计
	// 分析是独立的只读模块。
	// 读（收费项列表、收款流水、统计分析）用并集口径；写（新增/编辑/删除
	// 收费项）用单台账口径（详见 rooms.go）。
	billView := requireModule(db, ModuleBills, PermView)
	billEdit := requireLedgerModule(db, ModuleBills, PermEdit)
	billFull := requireLedgerModule(db, ModuleBills, PermFull)
	api.GET("/rental/fee-items", billView, handleFeeItemList(db))
	api.POST("/rental/fee-items", billEdit, handleFeeItemCreate(db))
	api.PUT("/rental/fee-items/:id", billEdit, handleFeeItemUpdate(db))
	api.DELETE("/rental/fee-items/:id", billFull, handleFeeItemDelete(db))
	api.GET("/rental/payments", requireModule(db, ModulePayments, PermView), handlePaymentList(db))
	api.GET("/rental/analytics", requireModule(db, ModuleAnalytics, PermView), handleAnalytics(db))
}

func handleFeeItemList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 收费项目按台账各一套：列表只给本次请求可见台账的项目。
		// 无一台账可见时返回空数组，不 seed（避免建出「无主」的内置项目）。
		ledgerIDs := visibleLedgerIDs(c)
		if len(ledgerIDs) == 0 {
			response.Success(c, []FeeItem{})
			return
		}
		// 惰性 seed 默认落在写入目标台账（未指定 ledger_id 时为公共台账）。
		seedLedger := ledgerIDs[0]
		if writeLedger, ok := writeLedgerID(c); ok {
			seedLedger = writeLedger
		}
		ensureFeeItems(db, seedLedger)
		items := make([]FeeItem, 0)
		q, _ := scopeLedger(db.Order("built_in DESC, sort ASC, id ASC"), c)
		q.Find(&items)
		response.Success(c, items)
	}
}

// feeItemRequest 新增/编辑入参。key 由服务端生成（内置项目 key 固定不可改）。
type feeItemRequest struct {
	Name          string  `json:"name" binding:"required"`
	Cycle         string  `json:"cycle"`
	DefaultAmount float64 `json:"default_amount"`
	Enabled       *bool   `json:"enabled"`
	// ReceiptHidden 单据不单列该项目（只影响单据展示，不影响账单金额与账单详情）。
	// 指针区分「不传」（编辑时不动）与「显式取消隐藏」。
	ReceiptHidden *bool `json:"receipt_hidden"`
	Sort          *int  `json:"sort"`
	// 生效月份窗口（issue #5 需求 6）。用指针区分「不传」与「传 0」：
	// 不传 = 保持原值（编辑时不动），传 0 = 显式改回不限。
	SeasonFromMonth *int `json:"season_from_month"`
	SeasonToMonth   *int `json:"season_to_month"`
}

// applySeason 把请求里的生效月份写进项目，并校验取值范围。
// 返回错误文案（空串=通过）。两个边界都不传时保持原值不变。
func applySeason(item *FeeItem, req *feeItemRequest) string {
	if req.SeasonFromMonth != nil {
		if !validSeasonMonth(*req.SeasonFromMonth) {
			return "供暖季起始月份应为 1-12，0 表示不限"
		}
		item.SeasonFromMonth = *req.SeasonFromMonth
	}
	if req.SeasonToMonth != nil {
		if !validSeasonMonth(*req.SeasonToMonth) {
			return "供暖季结束月份应为 1-12，0 表示不限"
		}
		item.SeasonToMonth = *req.SeasonToMonth
	}
	return ""
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
		// 新增项目落在写入目标台账；该台账的内置项目先补齐（幂等）。
		writeLedger, ok := writeLedgerID(c)
		if !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			return
		}
		ensureFeeItems(db, writeLedger)

		item := FeeItem{
			UserID:        currentUserID(c), // 录入人戳
			LedgerID:      writeLedger,      // 归属本次写入的台账
			Key:           "custom_" + strconv.FormatInt(time.Now().UnixNano(), 36),
			Name:          name,
			Kind:          feeKindFixed,
			Cycle:         cycle,
			DefaultAmount: round2(req.DefaultAmount),
			Enabled:       req.Enabled == nil || *req.Enabled,
			ReceiptHidden: req.ReceiptHidden != nil && *req.ReceiptHidden,
			Sort:          100,
		}
		if req.Sort != nil {
			item.Sort = *req.Sort
		}
		if msg := applySeason(&item, &req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
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
		// 收费项目按 ID 直取后必须校验台账归属：别的台账的项目不能改。
		if err := db.First(&item, id).Error; err != nil || !inLedgerScope(c, item.LedgerID) {
			response.ErrorNotFound(c, "收费项目不存在")
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleBills, PermEdit) {
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
		if req.ReceiptHidden != nil {
			item.ReceiptHidden = *req.ReceiptHidden
		}
		if req.Sort != nil {
			item.Sort = *req.Sort
		}
		if msg := applySeason(&item, &req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
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
		// 同更新：越权项目一律按「不存在」处理（404 不泄漏存在性）。
		if err := db.First(&item, id).Error; err != nil || !inLedgerScope(c, item.LedgerID) {
			response.ErrorNotFound(c, "收费项目不存在")
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleBills, PermFull) {
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
	// 明细重建只清本账单 + 本台账的旧行，不误删跨台账孤儿明细。
	dq := db.Where("bill_id = ?", bill.ID)
	if ledgerColumnReady(db, "bill_items") {
		dq = dq.Where("org_id = ?", bill.LedgerID)
	}
	dq.Delete(&BillItem{})
	items := billBuiltInItems(*bill)
	items = append(items, customs...)
	for i := range items {
		items[i].UserID = bill.UserID
		items[i].BillID = bill.ID
		// 明细继承账单台账：账单在哪个台账，明细就落在哪个台账。
		items[i].LedgerID = bill.LedgerID
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
	// 押金：只在首张账单上出现一次（判定见 firstBillDeposit），明细里单列一项，
	// 让房东一眼看出"这笔钱是押金、要退"，而不是混在租金里。
	if b.DepositFee > 0 {
		items = append(items, BillItem{FeeKey: feeKeyDeposit, Name: "押金", Kind: feeKindFixed,
			Amount: b.DepositFee, Cycle: payCycleMonthly,
			Detail: "入住押金，退租时结算退还"})
	}
	return items
}

// coveredItemDetail 固定费项目的覆盖区间说明（月付=单月，多月=覆盖月份区间）。
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

// customBillItems 按启用的自定义 fixed 项目生成账单明细行（金额按项目自身
// 周期与到期情况计：周期覆盖 N 个月的项目到期时收 N 个月，月付每月收）。
// 项目只在账单所属台账内取：别台账的自定义项目不能并入本台账的账单。
// 租户勾选排除的项目（该房生效的"不参与计费项目"）不并入。
//
// 周期取值：项目自身 Cycle 经 normalizePayCycle 归一（写库时已 parse 过），
// 四周期 monthly/quarterly/semiannual/annual 全部支持，未知值兜底为月付。
// 锚点按**项目自己**的收费历史判断（customItemAnchorDue），不借用房间固定费
// 的锚点：房间可能月付，而项目是半年付/年付。
func customBillItems(db *gorm.DB, room *Room, period string) ([]BillItem, float64) {
	items := make([]BillItem, 0)
	excluded := excludedFeeSet(effectiveRoomExcludedFees(db, room))
	// 各项目最近一次收费账期一次取好（判锚点用，避免 N+1）；全是月付项目时
	// 用不上，仍查一次也无妨（单房多条明细，代价可控）。
	lastByKey := customItemLastPeriods(db, room)
	var extra float64
	for _, fi := range enabledCustomFeeItems(db, room.LedgerID) {
		if excluded[fi.Key] {
			continue // 租户不参与该项目，本期不收
		}
		// 生效月份窗口（issue #5 需求 6）：窗口外本期不收，例如取暖费只在供暖季出账。
		//
		// **必须在判周期锚点之前跳过**：窗口外的月份不算"这个月收过该项目"，
		// 否则锚点会被这些月份推进，窗口一开反而要多等一个周期才收得上来。
		if !feeItemInSeason(fi, period) {
			continue
		}
		months := cycleMonths(fi.Cycle) // 月付 1、季付 3、半年付 6、年付 12
		if months > 1 && !customItemAnchorDue(lastByKey, fi.Key, period, months) {
			// 多月项目按自身周期锚点判断到期（月付房里不会因每月都有
			// 租金账单而每月重复收 N 个月，也不会收了首期后再无下期）。
			continue // 未到期，本期不收
		}
		amount := round2(fi.DefaultAmount * float64(months))
		if amount <= 0 {
			continue
		}
		covered := coveredPeriodText(period, fi.Cycle)
		detail := coveredItemDetail(covered, months, "自定义项目")
		// 有生效月份窗口的项目，把「几月到几月」写进明细，房东核对账时不用再去翻项目设置。
		if season := seasonRangeText(fi); season != "" {
			detail += " · 仅 " + season + " 收取"
		}
		items = append(items, BillItem{
			FeeKey: fi.Key, Name: fi.Name, Kind: feeKindFixed,
			Amount: amount, Cycle: fi.Cycle,
			Detail: detail,
		})
		extra = round2(extra + amount)
	}
	return items, extra
}

// seasonRangeText 把生效月份窗口渲染成「11–3 月」这样的短文案；不限时返回空串。
func seasonRangeText(fi FeeItem) string {
	from, to := fi.SeasonFromMonth, fi.SeasonToMonth
	switch {
	case from == 0 && to == 0:
		return ""
	case from == 0:
		return fmt.Sprintf("1–%d 月", to)
	case to == 0:
		return fmt.Sprintf("%d–12 月", from)
	case from == to:
		return fmt.Sprintf("%d 月", from)
	default:
		return fmt.Sprintf("%d–%d 月", from, to)
	}
}

// handlePaymentList 收款流水（时间倒序 + 月份区间筛选），带账单快照字段。
func handlePaymentList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		from := strings.TrimSpace(c.Query("period_from"))
		to := strings.TrimSpace(c.Query("period_to"))
		keyword := strings.TrimSpace(c.Query("keyword"))
		roomID := utils.Atoi(c.Query("room_id"), 0)
		community := strings.TrimSpace(c.Query("community"))

		type paymentRow struct {
			Payment
			RoomNo     string `json:"room_no"`
			TenantName string `json:"tenant_name"`
			Period     string `json:"period"`
		}
		query := db.Table("payments").
			Select("payments.*, bills.room_no as room_no, bills.tenant_name as tenant_name, bills.period as period").
			Joins("LEFT JOIN bills ON bills.id = payments.bill_id")
		// v0.6.0：流水按台账隔离。收款自身有 ledger_id；账单快照列再限定一次，
		// 免得孤儿流水（历史脏数据）把别台账的房号/租户名带出来。
		ledgerIDs := visibleLedgerIDs(c)
		if len(ledgerIDs) == 0 {
			response.SuccessPage(c, []gin.H{}, 0, page, pageSize)
			return
		}
		query = query.Where("payments.org_id IN ?", ledgerIDs).
			Where("(payments.bill_id = 0 OR bills.org_id IN ?)", ledgerIDs)
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
		if roomID > 0 {
			// 直收款（bill_id=0）不挂房源，按房源筛选时自然排除
			query = query.Where("bills.room_id = ?", roomID)
		}
		if community != "" {
			// 按小区/村组筛收款：账单的房源限定在可见台账内，避免跨台账同名小区串数据
			query = query.Where("bills.room_id IN (SELECT id FROM rooms WHERE community = ? AND org_id IN ?)",
				community, ledgerIDs)
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
				"items":   paymentItems(row.Items),
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
		start := addMonths(currentPeriodStr(), -(months-1)) + "-01"

		// v0.6.0：统计分析按台账隔离，所有聚合都限定在本次请求的可见台账内。
		ledgerIDs := visibleLedgerIDs(c)
		if len(ledgerIDs) == 0 {
			response.Success(c, gin.H{
				"series": []gin.H{}, "items": []gin.H{},
				"summary": gin.H{"billed": 0, "received": 0, "arrears": 0, "rate": 0},
			})
			return
		}

		// 月度应收（按账期）与实收（按收款时间）。
		type monthRow struct {
			Period string  `json:"period"`
			Billed float64 `json:"billed"`
		}
		billedRows := make([]monthRow, 0)
		db.Model(&Bill{}).Select("period, SUM(total_amount) as billed").
			Where("period >= ?", start[:7]).
			Where("org_id IN ?", ledgerIDs).
			Group("period").Order("period ASC").Scan(&billedRows)

		type paidRow struct {
			Month string  `json:"month"`
			Paid  float64 `json:"paid"`
		}
		paidRows := make([]paidRow, 0)
		db.Model(&Payment{}).Select("strftime('%Y-%m', paid_at) as month, SUM(amount) as paid").
			Where("paid_at >= ?", start).
			Where("org_id IN ?", ledgerIDs).
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
			Where("bill_id IN (SELECT id FROM bills WHERE period >= ? AND org_id IN ?)", start[:7], ledgerIDs).
			Group("name").Order("amount DESC").Scan(&itemRows)

		// 当前欠缴总额（可见台账内的未结净额）。
		//
		// 口径与总览一致（见 stats.go 的说明）：对**全部账单**求和再相减，
		// 让「多交顺延」的超收能冲减前面的欠缴。若按 status<>paid 过滤，
		// 多收的那张单会被排除，导致明明已抵扣完还显示欠款。
		//
		// 未开启结转的台账没有超收，两种算法等价，对老用户零影响。
		var billedAll, paidAll float64
		db.Model(&Bill{}).Where("org_id IN ?", ledgerIDs).
			Select("COALESCE(SUM(total_amount), 0)").Scan(&billedAll)
		db.Model(&Bill{}).Where("org_id IN ?", ledgerIDs).
			Select("COALESCE(SUM(paid_amount), 0)").Scan(&paidAll)
		arrears := round2(billedAll - paidAll)
		if arrears < 0 {
			arrears = 0 // 净多收：欠缴额最小为 0，不显示负数欠款
		}

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

	// 0.6.14：收费项目的「单据显示」开关，以及内置项目停用真正生效（0.6.13 粉丝反馈）。
	//
	// 只做加法：
	//   - fee_items 补 receipt_hidden 列（AutoMigrate 已加），默认 false = 照旧显示；
	//   - bills 补 disabled_fees 列（同样是 AutoMigrate 加），默认空 = 没有任何停用项。
	//
	// **存量数据一行不动**：老账单的金额、明细与单据显示与升级前完全一致。
	// 新列的回填没有业务含义（false / 空 JSON 就是升级前的口径），因此这里只做
	// NEW 列的空值兜底，避免老库读出 NULL 让 JSON 反序列化失败。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.6.14",
		Name:    "rental_fee_item_receipt_hidden",
		Upgrade: func(db *gorm.DB) error {
			if err := db.Exec("UPDATE fee_items SET receipt_hidden = 0 WHERE receipt_hidden IS NULL").Error; err != nil {
				return err
			}
			if err := db.Exec("UPDATE bills SET disabled_fees = '[]' WHERE disabled_fees IS NULL").Error; err != nil {
				return err
			}
			// 内置项目的 enabled 列在更早的版本里没有 default 兜底，老库可能读出 NULL；
			// NULL 会被 GORM 当成 false，等于把全部内置项目判成「已停用」——
			// 那样升级后新账单会一分钱不收。这里统一按 1（启用）兜底，与升级前一致。
			return db.Exec("UPDATE fee_items SET enabled = 1 WHERE enabled IS NULL").Error
		},
	})
}

func init() {
	registerFeeModels()
}
