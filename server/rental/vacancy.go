// vacancy.go — 空置分析（GitHub issue #2：房屋空置、租不出去对房主收益的影响）。
//
// 口径（先读这段再看代码，两种「空置」不是同一件事）：
//
//   - **当前空置**：实时、准确。房间本身没有状态列，在租/空置由 tenants.active
//     （同一台账内）派生——与 rooms.go / stats.go 的「已出租」判定同源，不另立口径。
//   - **历史空置**：是**估算**。库里有租户的 move_in_date / move_out_date，但没有
//     状态变更时间戳、日期允许留空、手工登记也不校验格式，所以只能按月重建「这个
//     月这间房有没有人住」。重建规则（与 caveat 文案一一对应）：
//     1. 每段租约占用 [入住月, 退租月]（在租租户的区间开到当前月）；
//     2. **当前月一律取实时口径**（有在租租户即占用），所以趋势图最后一根柱子与
//     汇总卡的「当前空置」永远一致；
//     3. 某房的租约日期缺失或格式非法 → 该房的历史**整体不计入**（既不算占用也
//     不算空置）：日期不齐就无从判断，宁可少报，也不把「不知道」编成「空置」。
//     这类房只在当前月按实时口径参与统计；
//     4. 房源建档月之前不计入（房间还不存在）。
//   - **损失估算**：单房空置一月 = 房源固定费（DefaultRent + DefaultSanitationFee
//   - DefaultManagementFee）+ 包月水费（仅包月口径按月加，金额口径与
//     effectiveRoomBilling 一致）。按吨的水费随用量走，空置期没有用量，不计入。
//
// 为什么用「应得」而不是「实得」租金：空置的机会成本就是本来能收到的钱；实收里
// 混着欠缴、预收与季节性抄表误差，拿它算空置会把两件事搅在一起，说不清「空置让
// 你少收了多少」。欠缴口径另有统计分析页的「当前欠缴 / 收缴率」负责。
//
// 性能：SQLite 连接池被 SetMaxOpenConns(1) 锁到单连接，任何按月相关子查询或
// N+1 都会把全站请求串行化（500 房 × 24 月 = 1.2 万次扫描）。因此这里只做
// **两次业务查询**（① 本台账全部房源；② 这些房源的租户），其余按月判定全在 Go
// 内存里跑；每台账的水费默认值懒加载缓存，每房不再重复读配置。租户查询走
// (org_id, room_id) 复合索引，见 migrate_ledger.go 的 ledgerIndexes。
package rental

import (
	"sort"
	"strings"
	"time"

	"smallgo/server/logger"
	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 空置分析的区间与排序口径常量。
const (
	vacancyDefaultMonths = 12 // 默认统计近 12 个月，与 handleAnalytics 一致
	vacancyMinMonths     = 3  // 至少 3 个月：样本太少没有参考意义
	vacancyMaxMonths     = 24 // 上限 24 个月：再长图表与逐月明细都失去可读性
)

// 排行排序方式（白名单，未知值一律回退默认）。
const (
	vacancySortLoss   = "loss"   // 按空置损失降序（默认）
	vacancySortMonths = "months" // 按空置月数降序
)

// VacancySummary 汇总卡。字段名与前端 web/src/api/rental.ts 的 VacancySummary
// 一一对应，改名必须两边同改。
type VacancySummary struct {
	VacantRooms      int     `json:"vacant_rooms"`       // 当前空置房间数（实时）
	RoomsTotal       int     `json:"rooms_total"`        // 台账内全部房间数
	VacancyRate      float64 `json:"vacancy_rate"`       // 当前空置率（%）
	VacantRoomMonths int     `json:"vacant_room_months"` // 区间内空置房月数（估算）
	LossEstimate     float64 `json:"loss_estimate"`      // 区间内空置损失估算（元）
	RoomsDated       int     `json:"rooms_dated"`        // 有入住日期的房间数（可信度参考）
}

// VacancySeriesRow 按月空置趋势的一行。
type VacancySeriesRow struct {
	Period       string  `json:"period"`        // YYYY-MM
	VacantRooms  int     `json:"vacant_rooms"`  // 该月空置房间数
	VacancyRate  float64 `json:"vacancy_rate"`  // 该月空置率（%）
	LossEstimate float64 `json:"loss_estimate"` // 该月空置损失（元）
}

// VacancyRoomRow 按房源排行的一行（内存排序后切片分页的元素）。
type VacancyRoomRow struct {
	RoomID        uint    `json:"room_id"`
	RoomLabel     string  `json:"room_label"`
	Community     string  `json:"community"`
	VacantMonths  int     `json:"vacant_months"`
	LossEstimate  float64 `json:"loss_estimate"`
	CurrentStatus string  `json:"current_status"` // occupied | vacant（实时）
	LastTenant    string  `json:"last_tenant"`    // 最近在租/已退租的租户名
	LastMoveOut   string  `json:"last_move_out"`  // 最近一次退租日期（可能是空串）
}

// setupVacancyRoutes 挂载空置分析路由。
//
// 权限复用 ModuleAnalytics：现有模块键就这 6 个，新增键会让存量成员的权限 JSON
// 缺该键、被 parsePerms 静默判为无权限，等于把功能悄悄收回。
func setupVacancyRoutes(api *gin.RouterGroup, db *gorm.DB) {
	ensureVacancyIndexes(db)
	api.GET("/rental/analytics/vacancy",
		requireModule(db, ModuleAnalytics, PermView), handleVacancyAnalytics(db))
}

// vacancyTenantIndex 是空置分析按房间批量取租户所用的复合索引。
//
// 为什么在启动时**再**建一次（migrate_ledger.go 的 ledgerIndexes 里也有它）：
// 那条 0.5.1 迁移只在**老库**上执行（migrateOrgEraSchema 开头的
// legacyTenantDataExists 判断），全新安装拿不到它。索引本身只是性能优化，
// 缺了不影响正确性，所以这里失败只记日志、不阻断启动。
const vacancyTenantIndex = "CREATE INDEX IF NOT EXISTS idx_rental_tenant_ledger_room ON tenants(org_id, room_id)"

// ensureVacancyIndexes 幂等地保证空置分析要用的索引存在。
func ensureVacancyIndexes(db *gorm.DB) {
	if db == nil {
		return
	}
	if err := db.Exec(vacancyTenantIndex).Error; err != nil {
		// 只降级、不 panic：没有索引时统计仍然算得对，只是慢一点。
		logger.Warn("租房管理：创建租户台账房间索引失败（空置分析会退化为逐房查询）：%v", err)
	}
}

// vacancyPeriods 返回本次请求要统计的月份列表（老→新），最后一期即当前月。
func vacancyPeriods(months int) []string {
	now := currentPeriodStr()
	out := make([]string, 0, months)
	for i := months - 1; i >= 0; i-- {
		out = append(out, addMonths(now, -i))
	}
	return out
}

// vacancyMonthIndex YYYY-MM → 自 0 年 1 月起的月序号；解析失败返回 false。
//
// 判「某月及以前」用字符串前缀也能做，但逐月判定要算月份差与窗口边界，折成整数
// 序号后比较、相减都是一步，不必反复构造 time.Time。
func vacancyMonthIndex(period string) (int, bool) {
	t, err := parsePeriod(period)
	if err != nil {
		return 0, false
	}
	return t.Year()*12 + int(t.Month()) - 1, true
}

// tenancyMonthIndex 把租户日期（YYYY-MM-DD）折成月序号。
//
// 容错是必须的：日期列是字符串、界面允许留空、手工登记与导入都不校验格式。
// 无法识别的一律 ok=false，由调用方按「这段租约不可用」处理——既不能让一个脏
// 日期把整份统计打挂，也不能把它当成有效区间算进空置或占用。
func tenancyMonthIndex(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	// 手工登记常见带时间或带时区的写法，只取日期部分。
	if len(raw) > 10 {
		raw = raw[:10]
	}
	t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
	if err != nil {
		return 0, false
	}
	return t.Year()*12 + int(t.Month()) - 1, true
}

// vacancyWaterDefaults 一个台账生效的水费默认值（每台账只解析一次并缓存）。
type vacancyWaterDefaults struct {
	mode          string  // meter / monthly
	monthlyAmount float64 // 包月口径的默认月费（元/月）
}

// vacancyRoomMetrics 单间房在本次统计区间内的空置情况（内存聚合产物）。
// vacantByMonth / inScopeByMonth 与 vacancyPeriods 一一对齐。
type vacancyRoomMetrics struct {
	room        Room
	monthlyLoss float64 // 单月空置损失（元/月）：房源固定费 + 包月水费
	// vacantByMonth 该月是否计为「空置房月」；inScopeByMonth 该月是否参与统计
	// （逐月空置率的分母：历史不可重建的房、当期还没建档的房都不进分母）。
	vacantByMonth  []bool
	inScopeByMonth []bool
	vacantMonths   int
	lossEstimate   float64
	currentVacant  bool
	historyKnown   bool // 该房历史是否可重建（租约日期完整）
	hasMoveInDate  bool // 是否至少有一个租户填了有效入住日期（rooms_dated 口径）
	lastTenant     string
	lastMoveOut    string
	lastMoveInOrd  int  // 「最近租户」排序键：入住月序号
	lastTenantID   uint // 「最近租户」次级键：同日取 ID 大者（后登记者）
	hasLastTenant  bool
}

// vacancyRoomSnapshot 是 vacancyRoomMetrics 去掉逐月明细后的形态，供排序与分页。
// 逐月明细只在算汇总与趋势时用一次，之后 500 房 × 24 月 = 1.2 万个 bool 没有
// 继续留着的必要。
type vacancyRoomSnapshot struct {
	room          Room
	vacantMonths  int
	lossEstimate  float64
	currentVacant bool
	historyKnown  bool
	lastTenant    string
	lastMoveOut   string
}

// vacancyInputs 承载 handleVacancyAnalytics 聚合所需的一切入参。
type vacancyInputs struct {
	periods        []string
	periodOrdinals []int
	nowOrdinal     int // 当前月月序号（逐月判定的上界）
	windowStart    int // 全局窗口下界月序号：max(最早房源建档月, 区间起点) = data_from
	rooms          []Room
	tenantsByRoom  map[uint][]Tenant
	waterDefaults  map[uint]vacancyWaterDefaults // 台账 → 生效水费默认值（懒加载缓存）
}

// handleVacancyAnalytics 空置分析：当前空置 + 按月趋势 + 按房源排行。
func handleVacancyAnalytics(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		months := utils.Atoi(c.Query("months"), vacancyDefaultMonths)
		if months < vacancyMinMonths {
			months = vacancyMinMonths
		}
		if months > vacancyMaxMonths {
			months = vacancyMaxMonths
		}
		page, pageSize := utils.NormalizePage(
			utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		sortKey := strings.TrimSpace(c.Query("sort"))
		if sortKey != vacancySortMonths {
			sortKey = vacancySortLoss
		}

		periods := vacancyPeriods(months)

		// 台账隔离：无一台账可见时返回零值结构，不报错、更不能退化成不过滤
		// （照 handleAnalytics 的空分支口径：宁可空，也不越权）。
		ledgerIDs := visibleLedgerIDs(c)
		if len(ledgerIDs) == 0 {
			response.Success(c, gin.H{
				"summary":   VacancySummary{},
				"series":    []gin.H{},
				"rooms":     gin.H{"items": []gin.H{}, "total": 0, "page": page, "pageSize": pageSize},
				"data_from": periods[0],
				"caveat":    "",
			})
			return
		}

		in, ok := loadVacancyInputs(db, c, ledgerIDs, periods)
		if !ok {
			return
		}

		metrics := make([]vacancyRoomMetrics, 0, len(in.rooms))
		roomsDated := 0
		hasMissingDates := false
		for _, room := range in.rooms {
			tenants := in.tenantsByRoom[room.ID]
			if vacancyTenantDatesIncomplete(tenants) {
				hasMissingDates = true
			}
			m := buildVacancyRoomMetrics(db, room, tenants, &in)
			if m.hasMoveInDate {
				roomsDated++
			}
			metrics = append(metrics, m)
		}

		summary, series := summarizeVacancy(metrics, periods, len(in.rooms))
		summary.RoomsDated = roomsDated
		items, total := paginateVacancyRooms(metrics, sortKey, page, pageSize)

		response.Success(c, gin.H{
			"summary":   summary,
			"series":    series,
			"rooms":     gin.H{"items": items, "total": total, "page": page, "pageSize": pageSize},
			"data_from": vacancyDataFrom(in.windowStart, periods[0]),
			"caveat":    vacancyCaveat(hasMissingDates),
		})
	}
}

// loadVacancyInputs 取本次统计的全部数据：两次业务查询 + Go 内归桶与窗口计算。
// 出错时已写出响应，返回 ok=false。
func loadVacancyInputs(db *gorm.DB, c *gin.Context, ledgerIDs []uint, periods []string) (vacancyInputs, bool) {
	in := vacancyInputs{
		periods:       periods,
		tenantsByRoom: map[uint][]Tenant{},
		waterDefaults: map[uint]vacancyWaterDefaults{},
	}
	for _, p := range periods {
		ord, _ := vacancyMonthIndex(p)
		in.periodOrdinals = append(in.periodOrdinals, ord)
	}
	// 逐月判定锁在「当前月」：未来月份还没发生，不能算空置损失。
	in.nowOrdinal = in.periodOrdinals[len(in.periodOrdinals)-1]

	// ---- 查询 ①：本次可见台账内的全部房源 ----
	if err := db.Where("org_id IN ?", ledgerIDs).Find(&in.rooms).Error; err != nil {
		response.ErrorInternal(c, "查询房源失败")
		return in, false
	}

	// 全局窗口下界：房源建档月与所查区间起点的较晚者（= data_from）。建档之前
	// 不可能有租约，也就不可能「空置损失」，算进去只会凭空放大估算。
	in.windowStart = in.periodOrdinals[0]
	if len(in.rooms) > 0 {
		earliest := in.rooms[0].CreatedAt
		for _, r := range in.rooms[1:] {
			if !r.CreatedAt.IsZero() && r.CreatedAt.Before(earliest) {
				earliest = r.CreatedAt
			}
		}
		// CreatedAt 为零值（手工灌库的房）时按「无建档信息」处理，不抬高下界。
		if !earliest.IsZero() {
			if ord := earliest.Year()*12 + int(earliest.Month()) - 1; ord > in.windowStart {
				in.windowStart = ord
			}
		}
	}

	// ---- 查询 ②：这些房源的租户（一次取完，Go 内按房间归桶）----
	//
	// 房间为空时直接跳过：不给租户表留全表扫描的机会（空台账/新建台账走这里）。
	if len(in.rooms) == 0 {
		return in, true
	}
	roomIDs := make([]uint, 0, len(in.rooms))
	for _, r := range in.rooms {
		roomIDs = append(roomIDs, r.ID)
	}
	tenants := make([]Tenant, 0, 128)
	if err := db.Where("org_id IN ? AND room_id IN ?", ledgerIDs, roomIDs).
		Order("room_id ASC, id ASC").Find(&tenants).Error; err != nil {
		response.ErrorInternal(c, "查询租户失败")
		return in, false
	}
	for _, t := range tenants {
		// 二次防御：正常数据里租户台账跟随房源，异常孤儿行不得影响本台账判定。
		if !containsLedger(ledgerIDs, t.LedgerID) {
			continue
		}
		in.tenantsByRoom[t.RoomID] = append(in.tenantsByRoom[t.RoomID], t)
	}
	return in, true
}

// buildVacancyRoomMetrics 重建单间房在统计区间内的空置情况（纯内存，零查询）。
//
// 规则见文件头口径说明；要点：当前月取实时口径、日期不全的房历史整体不计入、
// 房源建档月之前不计入。vacantByMonth / inScopeByMonth 已按 in.periods 对齐。
func buildVacancyRoomMetrics(db *gorm.DB, room Room, tenants []Tenant, in *vacancyInputs) vacancyRoomMetrics {
	m := vacancyRoomMetrics{
		room:           room,
		vacantByMonth:  make([]bool, len(in.periods)),
		inScopeByMonth: make([]bool, len(in.periods)),
		// 单月空置损失 = 房源固定费 + 包月水费（按吨水费是空置期不发生的用量费）。
		monthlyLoss:  round2(room.DefaultRent + room.DefaultSanitationFee + room.DefaultManagementFee),
		historyKnown: true,
	}
	m.monthlyLoss = round2(m.monthlyLoss +
		vacancyMonthlyWater(tenants, in.waterDefaultsFor(db, room.LedgerID)))

	type interval struct{ from, to int } // [from, to) 月序号区间
	occupied := make([]interval, 0, len(tenants))
	currentOccupied := false

	for _, t := range tenants {
		moveIn, hasIn := tenancyMonthIndex(t.MoveInDate)
		moveOut, hasOut := tenancyMonthIndex(t.MoveOutDate)
		if hasIn {
			m.hasMoveInDate = true
			// 「最近租户」：入住月更晚者胜；同月按租户 ID 大者胜（后登记者）。
			// 比较一律走整数，不用 moveIn*1000+ID 那种拼接编码——ID 超过 1000
			// 就会溢出到上一个月，排出来的「最近」是错的。
			if !m.hasLastTenant || moveIn > m.lastMoveInOrd ||
				(moveIn == m.lastMoveInOrd && t.ID > m.lastTenantID) {
				m.hasLastTenant = true
				m.lastMoveInOrd = moveIn
				m.lastTenantID = t.ID
				m.lastTenant = strings.TrimSpace(t.Name)
				m.lastMoveOut = strings.TrimSpace(t.MoveOutDate)
			}
		} else if !m.hasLastTenant {
			// 日期缺失时至少留个名字，避免排行里「最近租户」整列空白（它不是估算依据）。
			m.lastTenant = strings.TrimSpace(t.Name)
			m.lastMoveOut = strings.TrimSpace(t.MoveOutDate)
		}

		if t.Active {
			// 在租：当前占用精确成立；历史上从入住月起一直占到现在。
			currentOccupied = true
			if hasIn {
				occupied = append(occupied, interval{moveIn, in.nowOrdinal + 1})
			} else {
				m.historyKnown = false
			}
			continue
		}
		// 已退租：两端日期必须齐全且顺序合理，否则这段租约不可用 → 该房历史不可重建。
		if !hasIn || !hasOut || moveOut < moveIn {
			m.historyKnown = false
			continue
		}
		occupied = append(occupied, interval{moveIn, moveOut + 1})
	}
	m.currentVacant = !currentOccupied

	// 逐月判定的下界：全局窗口起点与本房建档月的较晚者。
	start := in.windowStart
	if !room.CreatedAt.IsZero() {
		if ord := room.CreatedAt.Year()*12 + int(room.CreatedAt.Month()) - 1; ord > start {
			start = ord
		}
	}

	lastIdx := len(in.periodOrdinals) - 1
	for i, ord := range in.periodOrdinals {
		// 当前月一律按实时口径（在租租户），保证趋势最后一根柱子与汇总卡一致。
		if i == lastIdx {
			m.inScopeByMonth[i] = true
			m.vacantByMonth[i] = m.currentVacant
			if m.currentVacant {
				m.vacantMonths++
			}
			continue
		}
		// 历史月：房间尚未建档、或该房历史不可重建 → 该月既不进分子也不进分母。
		if ord < start || ord > in.nowOrdinal || !m.historyKnown {
			continue
		}
		m.inScopeByMonth[i] = true
		hit := false
		for _, iv := range occupied {
			if iv.from <= ord && ord < iv.to {
				hit = true
				break
			}
		}
		if !hit {
			m.vacantByMonth[i] = true
			m.vacantMonths++
		}
	}
	m.lossEstimate = round2(float64(m.vacantMonths) * m.monthlyLoss)
	return m
}

// waterDefaultsFor 取某台账生效的水费默认值（懒加载并缓存：每台账最多读两次配置）。
func (in *vacancyInputs) waterDefaultsFor(db *gorm.DB, ledgerID uint) vacancyWaterDefaults {
	if d, ok := in.waterDefaults[ledgerID]; ok {
		return d
	}
	// 与 effectiveRoomBilling 的回退链一致：台账覆盖 → 系统级 → 代码默认。
	d := vacancyWaterDefaults{
		mode: normalizeWaterMode(ledgerConfigString(db, "rental_water_mode", ledgerID, waterModeMeter)),
	}
	if d.mode == waterModeMonthly {
		d.monthlyAmount = ledgerConfigFloat(db, "rental_water_monthly_fee", ledgerID, 0)
	}
	in.waterDefaults[ledgerID] = d
	return d
}

// vacancyMonthlyWater 该房生效的包月水费（按吨口径返回 0）。
//
// 口径与 effectiveRoomBilling 相同（在租租户的第一个显式设置 → 台账/系统默认），
// 但**不走它**：effectiveRoomBilling 每房都要再查一次租户与配置，500 房就是上千次
// 查询，在 SetMaxOpenConns(1) 下会把全站串行化。这里复用本次已取回的租户与上面
// 缓存的默认值，结果一致、查询数为 0。
func vacancyMonthlyWater(tenants []Tenant, def vacancyWaterDefaults) float64 {
	mode := ""
	for _, t := range tenants {
		if !t.Active {
			continue
		}
		if m := parseWaterMode(t.WaterMode); m != "" {
			mode = m
			break
		}
	}
	if mode == "" {
		mode = def.mode
	}
	if normalizeWaterMode(mode) != waterModeMonthly {
		return 0
	}
	for _, t := range tenants {
		if !t.Active {
			continue
		}
		if v := t.WaterMonthlyFee; v > 0 {
			return round2(v)
		}
	}
	return round2(def.monthlyAmount)
}

// summarizeVacancy 汇总区间指标与按月趋势。
//
// 逐月空置率的分母用「该月参与统计的房间数」而非全量房源：日期不可重建的房、以及
// 当期还没建档的房都不该被算成「本该有租客」。当前月的分母就是全部房间（实时状态
// 对每间房都成立），因此 summary.vacancy_rate 与最后一根柱子天然相等。
func summarizeVacancy(metrics []vacancyRoomMetrics, periods []string, roomsTotal int) (VacancySummary, []VacancySeriesRow) {
	summary := VacancySummary{RoomsTotal: roomsTotal}
	series := make([]VacancySeriesRow, 0, len(periods))
	for i, p := range periods {
		vacantInMonth := 0
		scopeInMonth := 0
		monthLoss := 0.0
		for j := range metrics {
			if metrics[j].inScopeByMonth[i] {
				scopeInMonth++
			}
			if !metrics[j].vacantByMonth[i] {
				continue
			}
			vacantInMonth++
			monthLoss = round2(monthLoss + metrics[j].monthlyLoss)
		}
		summary.VacantRoomMonths += vacantInMonth
		summary.LossEstimate = round2(summary.LossEstimate + monthLoss)
		rate := 0.0
		if scopeInMonth > 0 {
			rate = round1(float64(vacantInMonth) / float64(scopeInMonth) * 100)
		}
		series = append(series, VacancySeriesRow{
			Period: p, VacantRooms: vacantInMonth, VacancyRate: rate, LossEstimate: monthLoss,
		})
	}
	// 当前空置直接取最后一期的判定：它本就是由在租租户集合算出来的实时口径。
	summary.VacantRooms = series[len(series)-1].VacantRooms
	if roomsTotal > 0 {
		summary.VacancyRate = round1(float64(summary.VacantRooms) / float64(roomsTotal) * 100)
	}
	return summary, series
}

// paginateVacancyRooms 内存排序后切片分页（分页上限由 utils.NormalizePage 保证 ≤100）。
func paginateVacancyRooms(metrics []vacancyRoomMetrics, sortKey string, page, pageSize int) ([]VacancyRoomRow, int) {
	rows := make([]vacancyRoomSnapshot, 0, len(metrics))
	for _, m := range metrics {
		rows = append(rows, vacancyRoomSnapshot{
			room: m.room, vacantMonths: m.vacantMonths, lossEstimate: m.lossEstimate,
			currentVacant: m.currentVacant, historyKnown: m.historyKnown,
			lastTenant: m.lastTenant, lastMoveOut: m.lastMoveOut,
		})
	}
	sortVacancySnapshots(rows, sortKey)

	total := len(rows)
	offset := utils.Offset(page, pageSize)
	if offset > total {
		offset = total
	}
	end := offset + pageSize
	if end > total {
		end = total
	}
	items := make([]VacancyRoomRow, 0, end-offset)
	for _, s := range rows[offset:end] {
		status := "occupied"
		if s.currentVacant {
			status = "vacant"
		}
		lastTenant := s.lastTenant
		if lastTenant == "" {
			lastTenant = "—"
		}
		items = append(items, VacancyRoomRow{
			RoomID: s.room.ID, RoomLabel: roomLocationLabel(s.room), Community: s.room.Community,
			VacantMonths: s.vacantMonths, LossEstimate: s.lossEstimate,
			CurrentStatus: status, LastTenant: lastTenant, LastMoveOut: s.lastMoveOut,
		})
	}
	return items, total
}

// sortVacancySnapshots 按口径排序：损失降序 / 空置月数降序；同值时按房号升序，
// 保证分页边界稳定（否则同一份数据两次请求可能重复或漏掉某几间房）。
func sortVacancySnapshots(rows []vacancyRoomSnapshot, sortKey string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if sortKey == vacancySortMonths {
			if a.vacantMonths != b.vacantMonths {
				return a.vacantMonths > b.vacantMonths
			}
		} else if a.lossEstimate != b.lossEstimate {
			return a.lossEstimate > b.lossEstimate
		}
		if a.room.RoomNo != b.room.RoomNo {
			return a.room.RoomNo < b.room.RoomNo
		}
		return a.room.ID < b.room.ID
	})
}

// vacancyTenantDatesIncomplete 该房是否存在「日期不足以重建历史」的租户：
// 在租租户没填有效入住日期，或已退租租户缺任一端的有效日期。任一成立即把 caveat
// 打开——界面必须知道哪些数字是低估的。
func vacancyTenantDatesIncomplete(tenants []Tenant) bool {
	for _, t := range tenants {
		if _, ok := tenancyMonthIndex(t.MoveInDate); !ok {
			return true
		}
		if !t.Active {
			if _, ok := tenancyMonthIndex(t.MoveOutDate); !ok {
				return true
			}
		}
	}
	return false
}

// vacancyDataFrom 估算区间起点 YYYY-MM（内部用月序号，出参用账期字符串）。
func vacancyDataFrom(windowStart int, fallback string) string {
	if windowStart <= 0 {
		return fallback
	}
	return time.Date(windowStart/12, time.Month(windowStart%12+1), 1, 0, 0, 0, 0, time.Local).
		Format("2006-01")
}

// round1 百分比保留一位小数。
func round1(v float64) float64 {
	return float64(int(v*10+0.5)) / 10
}

// vacancyCaveat 估算口径提示：只要存在日期不足以重建历史的租户，就必须如实告诉
// 用户「这是估算、且偏低估」。全部租约日期齐全时返回空串（前端据空串不显示提示条，
// 避免把噪音当常态）。
func vacancyCaveat(hasMissingDates bool) string {
	if !hasMissingDates {
		return ""
	}
	return "历史空置按入住/退租日期估算：未填日期或日期格式非法的房间不计入历史区间（只在当前月按实时状态统计），" +
		"因此空置月数与损失是偏低的估算值。"
}
