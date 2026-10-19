// billing.go — 房间生效的计费与缴费设置。
//
// v0.2.3 起水费计费方式（按吨/包月）、水费金额、缴费周期（月付/季付/半年付/年付）、
// 缴费日与提前提醒天数都在租户上配置：每个租户可以不一样。租户没设置的项
// 回退到系统级全局默认（系统配置 → 计费默认，管理员维护、全员共享）。
//
// 账单仍然按房开（(room, period) 唯一），因此房间的生效设置取该房
// 在租租户的设置：同一房间有多名在租租户（宿舍场景）时，逐项取"第一个
// 显式设置"的租户（按登记先后，与账单租户名快照同序）。
package rental

import (
	"sort"
	"strconv"
	"strings"

	"smallgo/server/response"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// billingConfig 房间当前生效的计费与缴费设置（各字段都已解析成确定值）。
type billingConfig struct {
	WaterMode   string  // meter 按吨 / monthly 包月
	WaterAmount float64 // 按吨=元/吨；包月=元/月
	PayCycle    string  // monthly 月付 / quarterly 季付 / semiannual 半年付 / annual 年付
	PayDay      int     // 1-28；0=不提醒
	RemindDays  int     // 0-30
}

// ---------------------------------------------------------------------------
// 台账范围（v0.6.0 数据隔离）下的查询与写入作用域
//
// ledgerScope(c) 给出本次请求「能看哪些台账」与「写哪个台账」，下列辅助函数
// 统一把它落到 SQL 上。每条读取 Room/Tenant/Bill/MeterRecord/Contract/
// FeeItem/BillItem/Payment 的查询都必须经这里过滤，漏一处就是跨台账越权；
// 写入一律从「父记录」继承台账，避免用构造出来的 ID 在别的台账里造孤儿行。
// ---------------------------------------------------------------------------

// visibleLedgerIDsScoped 本次请求的可见台账集合；nil/空表示调用方没有可见台账
// （应返回空结果，而不是退化成不过滤）。ledgerIDs 为 nil 时（后台任务、
// 已由调用方自行收窄的场景）返回 (nil, false) 表示不限台账。
func visibleLedgerIDsScoped(c *gin.Context) ([]uint, bool) {
	ledgerIDs := visibleLedgerIDs(c)
	if len(ledgerIDs) == 0 {
		return nil, true
	}
	return ledgerIDs, true
}

// scopeLedger 给任意查询加上可见台账过滤。empty=true 时调用方必须直接返回空
// 结果（用户不属于任何台账，什么也不该看到）。
func scopeLedger(q *gorm.DB, c *gin.Context) (*gorm.DB, bool) {
	ids := visibleLedgerIDs(c)
	if len(ids) == 0 {
		return q, true
	}
	return q.Where("org_id IN ?", ids), false
}

// scopeRoomQuery / scopeRooms 房源查询过滤。
func scopeRoomQuery(q *gorm.DB, c *gin.Context) (*gorm.DB, bool) { return scopeLedger(q, c) }

func scopeRooms(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&Room{}), c)
	return q
}

// scopeTenants 租户查询过滤。
func scopeTenants(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&Tenant{}), c)
	return q
}

// scopeBills 账单查询过滤。
func scopeBills(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&Bill{}), c)
	return q
}

// scopeMeterRecords 抄表记录查询过滤。
func scopeMeterRecords(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&MeterRecord{}), c)
	return q
}

// scopeContracts 合同查询过滤。
func scopeContracts(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&Contract{}), c)
	return q
}

// scopeFeeItems 收费项目查询过滤。
func scopeFeeItems(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&FeeItem{}), c)
	return q
}

// scopeBillItems 账单费用明细查询过滤。
func scopeBillItems(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&BillItem{}), c)
	return q
}

// scopePayments 收款流水查询过滤。
func scopePayments(db *gorm.DB, c *gin.Context) *gorm.DB {
	q, _ := scopeLedger(db.Model(&Payment{}), c)
	return q
}

// inLedgerScope 判断已取出的记录是否落在本次请求的可见台账内。
// 按 ID 单条直取的查询必须再过这一关：否则换个 ID 就能读到别台账的数据。
// 越权一律按「不存在」处理（调用方回 404），不暴露记录是否存在。
func inLedgerScope(c *gin.Context, ledgerID uint) bool {
	ids := visibleLedgerIDs(c)
	if len(ids) == 0 {
		return false
	}
	for _, id := range ids {
		if id == ledgerID {
			return true
		}
	}
	return false
}

// writeLedgerID 本次请求的写入目标台账。resolveLedger 保证写路由上一定能取到
// （取不到时 requireLedgerModule 已挡下）；这里再兜一层，返回 ok=false 时
// 调用方应拒绝写入，而不是悄悄落到 0 号台账。
func writeLedgerID(c *gin.Context) (uint, bool) {
	_, writeLedger, single := ledgerScope(c)
	return writeLedger, single
}

// ledgerScopeColumnExists 探列：老库在 v0.6.0 迁移补齐 org_id 之前可能还没有该列，
// 此时按台账过滤会直接报「no such column」。后台任务与写路径用它在不依赖
// 台账解析的场景下降级为不过滤（与升级前行为一致）。
func ledgerScopeColumnExists(db *gorm.DB, table string) bool {
	var count int64
	if err := db.Raw(
		"SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = 'org_id'", table,
	).Scan(&count).Error; err != nil {
		return false
	}
	return count > 0
}

// ledgerColumnReady 缓存各表的 org_id 列探测结果：列不会在进程运行中消失，
// 探测一次即可，避免每条 SQL 都查一次表结构。
var ledgerColumnCache = map[string]bool{}

func ledgerColumnReady(db *gorm.DB, table string) bool {
	if ready, ok := ledgerColumnCache[table]; ok {
		return ready
	}
	ready := ledgerScopeColumnExists(db, table)
	ledgerColumnCache[table] = ready
	return ready
}

// roomLedgerRef 房间的所属台账引用：计费设置解析用它限定「只看同台账的租户」。
// 恒返回非 nil，空房间（org_id=0）也走同一套语义。
func roomLedgerRef(room *Room) *uint {
	id := room.LedgerID
	return &id
}

// excludedFeeSet 把排除项目 key 列表转成集合（重算与出账时查命中用）。
func excludedFeeSet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// normalizeExcludedFeeKeys 清洗入参的排除项目 key：去空白、去空项、去重。
func normalizeExcludedFeeKeys(keys []string) []string {
	seen := make(map[string]bool, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

// normalizeTags 清洗入参标签：去空白、去空项、去重，并限制单个标签长度
// （标签存成 JSON 数组，超长标签会把列表/筛选项挤变形）。
func normalizeTags(tags []string) []string {
	seen := make(map[string]bool, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if len([]rune(t)) > 20 {
			t = string([]rune(t)[:20])
		}
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// effectiveRoomExcludedFees 房间生效的"不参与计费项目"：取第一个设置了排除项
// 的在租租户（与 effectiveRoomBilling 的"第一个显式设置"同规则），key 排序
// 返回。没有租户设置过排除（或房间空置）时返回 nil = 全部项目参与。
func effectiveRoomExcludedFees(db *gorm.DB, room *Room) []string {
	for _, t := range activeRoomTenants(db, room.ID, roomLedgerRef(room)) {
		if len(t.ExcludedFees) > 0 {
			keys := normalizeExcludedFeeKeys(t.ExcludedFees)
			sort.Strings(keys)
			return keys
		}
	}
	return nil
}

// activeRoomTenants 房间的在租租户，按登记先后（id 升序）。
//
// ledgerID 非 nil 时只取该台账的租户：房间属于某个台账，其计费设置也必须
// 只由同台账的租户决定，别台账的脏数据（或构造出的绑定）不能影响本台账口径。
// 老库在 v0.6.0 迁移跑完前可能还没有 org_id 列，此时退回不过滤，保持
// 升级前行为（否则启动期后台任务会拿到 no such column 报错）。
func activeRoomTenants(db *gorm.DB, roomID uint, ledgerID *uint) []Tenant {
	tenants := make([]Tenant, 0, 4)
	q := db.Where("room_id = ? AND active = ?", roomID, true)
	if ledgerID != nil && ledgerColumnReady(db, "tenants") {
		q = q.Where("org_id = ?", *ledgerID)
	}
	q.Order("id ASC").Limit(20).Find(&tenants)
	return tenants
}

// tenantWaterAmount 租户对指定计费方式显式设置的金额；未设置返回 0。
func tenantWaterAmount(t Tenant, mode string) float64 {
	if normalizeWaterMode(mode) == waterModeMonthly {
		return t.WaterMonthlyFee
	}
	return t.WaterPrice
}

// configString 读取用户级字符串配置，缺失或空白时返回默认值。
func configString(db *gorm.DB, key string, userID uint, def string) string {
	v, err := sysconfig.GetConfig(db, key, userID)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// configInt 读取用户级整数配置，缺失或非法时返回默认值。
func configInt(db *gorm.DB, key string, userID uint, def int) int {
	v, err := sysconfig.GetConfig(db, key, userID)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	n, perr := strconv.Atoi(strings.TrimSpace(v))
	if perr != nil {
		return def
	}
	return n
}

// globalWaterAmount 台账级默认水费金额：按吨读元/吨单价，包月读元/月金额。
//
// v0.6.0：改读**台账**口径（ledgerConfigFloat）：台账有覆盖用覆盖值，
// 没有则回退系统级（user_id=0），所以老库/公共台账读数不变。
func globalWaterAmount(db *gorm.DB, mode string, ledgerID uint) float64 {
	if normalizeWaterMode(mode) == waterModeMonthly {
		return ledgerConfigFloat(db, "rental_water_monthly_fee", ledgerID, 0)
	}
	return ledgerConfigFloat(db, "rental_water_price", ledgerID, 5.0)
}

// effectiveRoomBilling 解析房间生效的计费与缴费设置：逐项"第一个显式设置的
// 在租租户" → 全局默认。没有在租租户的房间（空置/已退租）直接用全局默认。
// 全局默认读系统级共享设置（user_id=0，管理员维护）。
func effectiveRoomBilling(db *gorm.DB, room *Room) billingConfig {
	// 只在「本房所属台账」的租户里找生效设置：跨台账的同名房/孤儿租户
	// 不能影响另一个台账的计费口径。
	tenants := activeRoomTenants(db, room.ID, roomLedgerRef(room))

	// 水费计费方式：租户显式设置 → 全局默认（空值与未知值一律按吨）。
	mode := ""
	for _, t := range tenants {
		if m := parseWaterMode(t.WaterMode); m != "" {
			mode = m
			break
		}
	}
	if mode == "" {
		mode = normalizeWaterMode(ledgerConfigString(db, "rental_water_mode", room.LedgerID, waterModeMeter))
	}

	// 水费金额随最终方式取：该方式下第一个有显式金额的租户 → 全局默认金额。
	amount := 0.0
	for _, t := range tenants {
		if v := tenantWaterAmount(t, mode); v > 0 {
			amount = v
			break
		}
	}
	if amount <= 0 {
		amount = globalWaterAmount(db, mode, room.LedgerID)
	}

	// 缴费周期：租户显式设置 → 全局默认。
	cycle := ""
	for _, t := range tenants {
		if c := parsePayCycle(t.PayCycle); c != "" {
			cycle = c
			break
		}
	}
	if cycle == "" {
		cycle = normalizePayCycle(ledgerConfigString(db, "rental_pay_cycle", room.LedgerID, payCycleMonthly))
	}

	// 缴费日：租户 0-28 的取值都算显式设置（0=不提醒）；-1 才回退全局。
	payDay := tenantFollowGlobal
	for _, t := range tenants {
		if t.PayDay >= 0 {
			payDay = t.PayDay
			break
		}
	}
	if payDay < 0 {
		payDay = clampPayDay(ledgerConfigInt(db, "rental_pay_day", room.LedgerID, 0))
	}

	// 提前提醒天数：租户 0-30 的取值都算显式设置；-1 回退全局。
	remind := tenantFollowGlobal
	for _, t := range tenants {
		if t.RemindDays >= 0 {
			remind = t.RemindDays
			break
		}
	}
	if remind < 0 {
		remind = clampRemindDays(ledgerConfigInt(db, "rental_remind_days", room.LedgerID, defaultRemindDays))
	}

	return billingConfig{
		WaterMode:   mode,
		WaterAmount: amount,
		PayCycle:    cycle,
		PayDay:      clampPayDay(payDay),
		RemindDays:  clampRemindDays(remind),
	}
}

// waterAmountForMode 指定计费方式下的水费金额：租户显式设置 → 全局默认。
// 账单里临时切换计费方式时用它取该方式的默认金额，避免沿用另一方式的金额。
func waterAmountForMode(db *gorm.DB, room *Room, mode string) float64 {
	for _, t := range activeRoomTenants(db, room.ID, roomLedgerRef(room)) {
		if v := tenantWaterAmount(t, mode); v > 0 {
			return v
		}
	}
	return globalWaterAmount(db, mode, room.LedgerID)
}

// handleBillingDefaults 给前端的**当前台账**计费默认值：
// 员工账号看不到系统配置页，抄表/开票弹窗的"跟随全局默认"从这里取。
//
// v0.6.0：按本次请求的台账返回（台账有覆盖用覆盖，没有回退系统级）。
// 不传 ledger_id 且只属于一个台账时自动兜底；多台账时必须显式指定，
// 否则没有"当前台账"可言，回退到系统级值。
func handleBillingDefaults(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ledgerID, _, single := ledgerScope(c)
		target := LegacySharedLedgerID
		if single || len(ledgerID) == 1 {
			target = ledgerID[0]
		}
		d := ledgerBillingDefaultsFor(db, target)
		response.Success(c, gin.H{
			"ledger_id":         d.LedgerID,
			"water_mode":        d.WaterMode,
			"water_price":       d.WaterPrice,
			"water_monthly_fee": d.WaterMonthlyFee,
			"pay_cycle":         d.PayCycle,
			"pay_day":           d.PayDay,
			"elec_price":        d.ElecPrice,
			"gas_price":         d.GasPrice,
			"remind_days":       d.RemindDays,
		})
	}
}

// clampPayDay 把缴费日收进 0-28（0=不提醒，负数按 0 处理）。
func clampPayDay(day int) int {
	if day < 0 {
		return 0
	}
	if day > payDayMax {
		return payDayMax
	}
	return day
}

// validateTenantBilling 校验租户的计费与缴费入参，返回空串表示通过。
// waterMode / payCycle 允许为空（跟随全局默认）。
func validateTenantBilling(waterMode string, waterPrice, waterMonthlyFee float64, payCycle string, payDay, remindDays int) string {
	if waterMode != "" && parseWaterMode(waterMode) == "" {
		return "水费计费方式无效，应为按吨（meter）或包月（monthly）"
	}
	if waterPrice < 0 || waterMonthlyFee < 0 {
		return "水费金额不能为负数"
	}
	if payCycle != "" && parsePayCycle(payCycle) == "" {
		return "缴费周期无效，应为月付（monthly）、季付（quarterly）、半年付（semiannual）或年付（annual）"
	}
	if payDay < tenantFollowGlobal || payDay > payDayMax {
		return "缴费日应为 1-28 号，0 表示不提醒，-1 表示跟随全局默认"
	}
	if remindDays < remindDaysMin || remindDays > remindDaysMax {
		return "提前提醒天数应为 0-30，-1 表示跟随全局默认"
	}
	return ""
}
