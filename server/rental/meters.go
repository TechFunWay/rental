// meters.go — 抄表台账：每间房每月的水/电/燃气表读数记录，独立于账单存在。
//
// 账单里的读数是开票快照（结算口径），台账是按月的原始抄表记录：
// 季付房的中间月份、空置房、未开票的月份都可以记。创建账单时若该月
// 没有台账记录，会把账单上的当期读数自动沉淀一条，让现有抄表录入
// 习惯不变、台账自动积累；台账记录与账单互不回写。
package rental

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/logger"
	"smallgo/server/response"
	"smallgo/server/sysconfig"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 表种启停配置键（2026-10-30 粉丝反馈「能不能把用不到的功能关掉（电表 燃气表）」）。
//
// 只用得上水表的场景很常见（自建房、村委统管宿舍往往没有独立燃气表），
// 电表/燃气表留在界面上就是噪音：抄表要多填两格、账单多两列、房源表单多两行。
// 关掉后该表种在**抄表记录、抄表开票、账单列表与编辑、房源表单与详情、收费单据**
// 里都不再出现，且新生成的账单也不再收这一项（历史账单是快照，金额与单据不变）。
// 水表不设开关：它是最基础的计量项，且水费本来就能切「包月」。
const (
	meterElecEnabledKey = "rental_elec_enabled"
	meterGasEnabledKey  = "rental_gas_enabled"
)

// registerMeterConfigs 注册表种开关（系统级默认全开，台账可各自覆盖）。
// 默认 "true" 是刻意的：存量用户升级后行为与升级前一模一样，要用的人自己去关。
func registerMeterConfigs() {
	for _, def := range []struct{ key, label string }{
		{meterElecEnabledKey, "启用电表"},
		{meterGasEnabledKey, "启用燃气表"},
	} {
		sysconfig.RegisterConfig(sysconfig.ConfigDef{
			Key: def.key, Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeBool,
			Default: "true", Group: "billing", Label: def.label,
			Description: "关掉后抄表记录、抄表开票、账单、房源表单都不再出现这个表，" +
				"新生成的账单也不再收这一项（已开出的账单是快照，金额与单据不变）；" +
				"只用得上水表的房东可以关掉",
		})
	}
}

// meterEnabled 某台账某表种是否启用：台账覆盖 → 系统级 → 默认启用。
func meterEnabled(db *gorm.DB, key string, ledgerID uint) bool {
	return ledgerConfigString(db, key, ledgerID, "true") == "true"
}

// meterSwitches 某台账生效的表种开关（前端据此隐藏用不到的表）。
type meterSwitches struct {
	LedgerID    uint `json:"ledger_id"`
	ElecEnabled bool `json:"elec_enabled"`
	GasEnabled  bool `json:"gas_enabled"`
}

func meterSwitchesFor(db *gorm.DB, ledgerID uint) meterSwitches {
	return meterSwitches{
		LedgerID:    ledgerID,
		ElecEnabled: meterEnabled(db, meterElecEnabledKey, ledgerID),
		GasEnabled:  meterEnabled(db, meterGasEnabledKey, ledgerID),
	}
}

// MeterRecord 每房每月的三表读数。(room, period) 唯一，
// 重复录入按覆盖处理（改数即重新保存）。UserID 是「录入人」戳。
type MeterRecord struct {
	ID       uint    `gorm:"primarykey" json:"id"`
	UserID   uint    `gorm:"index" json:"-"`
	LedgerID uint    `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID   uint    `gorm:"index" json:"room_id"`
	Period   string  `gorm:"not null;index" json:"period"` // YYYY-MM
	// ReadAt 抄表时间（2026-10-30 粉丝反馈「水表能不能添加上抄时间」）。
	//
	// 账期只说明「这是哪个月的表」，回答不了「几号上门抄的」——房东与租客核对
	// 读数争议（换表、漏抄、代抄）要看时点。存 YYYY-MM-DD（日期粒度足够，
	// 手填成本最低），空串 = 没填（老数据或用户不想记）。
	// **不参与任何计费**，纯粹是台账凭证。
	ReadAt   string  `gorm:"size:10" json:"read_at"`
	Water    float64 `gorm:"default:0" json:"water"`
	Elec     float64 `gorm:"default:0" json:"elec"`
	Gas      float64 `gorm:"default:0" json:"gas"`
	Note     string  `json:"note"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// meterRow 台账出参：附加房号与上期读数/本期用量（服务端算好，跨页也准确）。
type meterRow struct {
	MeterRecord
	RoomNo     string  `gorm:"-" json:"room_no"`
	PrevWater  float64 `gorm:"-" json:"prev_water"`
	PrevElec   float64 `gorm:"-" json:"prev_elec"`
	PrevGas    float64 `gorm:"-" json:"prev_gas"`
	UsageWater float64 `gorm:"-" json:"usage_water"`
	UsageElec  float64 `gorm:"-" json:"usage_elec"`
	UsageGas   float64 `gorm:"-" json:"usage_gas"`
	// PhotoCount 该房该账期的抄表照片数（水/电/燃气合计）。列表页据此显示
	// 「照片 n」入口，不必为每一行再发一次请求；点开才按房+账期取照片。
	PhotoCount int64 `gorm:"-" json:"photo_count"`
	// 本期账单：该房该账期已开票时给出金额，列表页直接显示「账单 ￥x」，
	// 并单列其中的自定义收费合计，避免新增的收费项目在抄表记录里看不出效果
	//（2026-10-17 粉丝反馈）。未开票时三者都是零值。
	BillID    uint    `gorm:"-" json:"bill_id"`
	BillTotal float64 `gorm:"-" json:"bill_total"`
	BillExtra float64 `gorm:"-" json:"bill_extra"`
}

func init() {
	database.RegisterModels(&MeterRecord{})
}

func setupMeterRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 抄表模块按勾选放行：查看看台账，操作录/改读数，管理才能删。
	// 读并集、写单台账（详见 rooms.go 的同名说明）：抄表录入必须落在
	// 本次请求指定的那个台账里，且该台账要达到 edit 档。
	view := requireModule(db, ModuleMeters, PermView)
	edit := requireLedgerModule(db, ModuleMeters, PermEdit)
	full := requireLedgerModule(db, ModuleMeters, PermFull)
	api.GET("/rental/meter-records", view, handleMeterList(db))
	api.POST("/rental/meter-records", edit, handleMeterUpsert(db))
	api.DELETE("/rental/meter-records/:id", full, handleMeterDelete(db))
}

func handleMeterList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		period := strings.TrimSpace(c.Query("period"))
		periodFrom := strings.TrimSpace(c.Query("period_from"))
		periodTo := strings.TrimSpace(c.Query("period_to"))
		roomID := utils.Atoi(c.Query("room_id"), 0)
		community := strings.TrimSpace(c.Query("community"))

		// v0.6.0：只列本次请求可见台账内的抄表记录。
		query, empty := scopeLedger(db.Model(&MeterRecord{}), c)
		if empty {
			response.SuccessPage(c, []meterRow{}, 0, page, pageSize)
			return
		}
		if period != "" {
			query = query.Where("period = ?", period)
		}
		// 账期区间（YYYY-MM，闭区间）：from/to 都可只给一端。
		if periodFrom != "" {
			query = query.Where("period >= ?", periodFrom)
		}
		if periodTo != "" {
			query = query.Where("period <= ?", periodTo)
		}
		if roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		if community != "" {
			// 按小区/村组筛抄表记录：房源限定在可见台账内，避免跨台账同名小区串数据
			query = query.Where("room_id IN (SELECT id FROM rooms WHERE community = ? AND org_id IN ?)",
				community, visibleLedgerIDs(c))
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询抄表记录失败")
			return
		}
		rows := make([]meterRow, 0)
		if err := query.Order("period DESC, room_id ASC, id ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询抄表记录失败")
			return
		}

		fillMeterRows(db, c, rows)
		response.SuccessPage(c, rows, total, page, pageSize)
	}
}

// fillMeterRows 批量补齐房号、上期读数与本期用量。
// 上期读数取同房间紧邻的上一条台账；首次记录对房间的抄表底数算用量，
// 读数小于上期（换表倒转）按 0 计。
//
// c 提供可见台账：台账记录与房间读数都只在本台账内取，跨台账的孤儿记录
// 不会出现在用量计算里。
func fillMeterRows(db *gorm.DB, c *gin.Context, rows []meterRow) {
	if len(rows) == 0 {
		return
	}
	roomIDs := make([]uint, 0, len(rows))
	for i := range rows {
		roomIDs = append(roomIDs, rows[i].RoomID)
	}
	roomNos := map[uint]string{}
	initials := map[uint][3]float64{}
	rooms := make([]Room, 0, len(roomIDs))
	rq, _ := scopeLedger(db.Where("id IN ?", roomIDs), c)
	rq.Find(&rooms)
	for _, r := range rooms {
		roomNos[r.ID] = r.RoomNo
		initials[r.ID] = [3]float64{r.InitialWater, r.InitialElec, r.InitialGas}
	}

	// 一次拉回涉及房间的全部台账记录，内存里按房间分组、按账期升序排序，
	// 再倒序找每条记录的上一条——分页跨组也能算准。
	type reading struct {
		period string
		rec    [3]float64
	}
	byRoom := map[uint][]reading{}
	all := make([]MeterRecord, 0)
	mq, _ := scopeLedger(db.Select("id", "room_id", "period", "water", "elec", "gas"), c)
	mq.Where("room_id IN ?", roomIDs).Find(&all)
	for _, m := range all {
		byRoom[m.RoomID] = append(byRoom[m.RoomID], reading{period: m.Period, rec: [3]float64{m.Water, m.Elec, m.Gas}})
	}
	for roomID, list := range byRoom {
		sort.Slice(list, func(i, j int) bool {
			if list[i].period != list[j].period {
				return list[i].period < list[j].period
			}
			return list[i].rec[0] < list[j].rec[0]
		})
		byRoom[roomID] = list
	}

	for i := range rows {
		rows[i].RoomNo = roomNos[rows[i].RoomID]
		cur := [3]float64{rows[i].Water, rows[i].Elec, rows[i].Gas}
		prev := initials[rows[i].RoomID] // 首条记录对抄表底数算用量
		if list := byRoom[rows[i].RoomID]; len(list) > 0 {
			for k := len(list) - 1; k >= 0; k-- {
				if list[k].period < rows[i].Period {
					prev = list[k].rec
					break
				}
			}
		}
		rows[i].PrevWater, rows[i].PrevElec, rows[i].PrevGas = prev[0], prev[1], prev[2]
		rows[i].UsageWater = meterUsage(prev[0], cur[0])
		rows[i].UsageElec = meterUsage(prev[1], cur[1])
		rows[i].UsageGas = meterUsage(prev[2], cur[2])
	}

	// 抄表照片数：一次分组查询取本页涉及房间与账期的合计（水/电/燃气一起算），
	// 列表页据此显示「照片 n」，避免逐行请求（SQLite 单连接，N+1 代价高）。
	periods := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for i := range rows {
		if !seen[rows[i].Period] {
			seen[rows[i].Period] = true
			periods = append(periods, rows[i].Period)
		}
	}
	type photoCount struct {
		RoomID uint
		Period string
		N      int64
	}
	counts := make([]photoCount, 0)
	pq, _ := scopeLedger(db.Model(&MeterPhoto{}), c)
	pq.Select("room_id, period, count(*) AS n").
		Where("room_id IN ? AND period IN ?", roomIDs, periods).
		Group("room_id, period").Scan(&counts)
	countByKey := map[string]int64{}
	for _, pc := range counts {
		countByKey[fmt.Sprintf("%d|%s", pc.RoomID, pc.Period)] = pc.N
	}
	for i := range rows {
		rows[i].PhotoCount = countByKey[fmt.Sprintf("%d|%s", rows[i].RoomID, rows[i].Period)]
	}

	// 本期账单：一次查出本页涉及的 (房间, 账期) 账单，列表页直接展示本期
	// 应收金额与其中的自定义收费合计（未开票的房间保持零值）。
	type billInfo struct {
		ID          uint
		RoomID      uint
		Period      string
		TotalAmount float64
		ExtraAmount float64
	}
	bills := make([]billInfo, 0)
	bq, _ := scopeLedger(db.Model(&Bill{}), c)
	bq.Select("id, room_id, period, total_amount, extra_amount").
		Where("room_id IN ? AND period IN ?", roomIDs, periods).Scan(&bills)
	billByKey := map[string]billInfo{}
	for _, b := range bills {
		billByKey[fmt.Sprintf("%d|%s", b.RoomID, b.Period)] = b
	}
	for i := range rows {
		if b, ok := billByKey[fmt.Sprintf("%d|%s", rows[i].RoomID, rows[i].Period)]; ok {
			rows[i].BillID = b.ID
			rows[i].BillTotal = b.TotalAmount
			rows[i].BillExtra = b.ExtraAmount
		}
	}
}

// meterUsage 本期用量：倒挂（换表）按 0。
func meterUsage(prev, now float64) float64 {
	if now < prev {
		return 0
	}
	return now - prev
}

// handleMeterUpsert 录入/修改一条抄表记录（同房同月覆盖）。
func handleMeterUpsert(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			RoomID uint    `json:"room_id" binding:"required"`
			Period string  `json:"period" binding:"required"`
			// ReadAt 抄表时间（YYYY-MM-DD，选填）；ReadAtSet 让「显式清空」与
			// 「没传」区分开——编辑时用户可能要把填错的日期删掉。
			ReadAt    *string `json:"read_at"`
			Water     float64 `json:"water"`
			Elec      float64 `json:"elec"`
			Gas       float64 `json:"gas"`
			Note      string  `json:"note"`
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
		readAt, ok := normalizeReadAt(c, req.ReadAt)
		if !ok {
			return
		}
		room, ok := loadRoom(db, c, req.RoomID)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleMeters, PermEdit) {
			return
		}

		// 已存在记录按 (房间, 账期) 覆盖；台账记录归属必须与房间同台账，
		// 跨台账的同名记录不参与覆盖（避免写到别的台账上）。
		var rec MeterRecord
		q, _ := scopeLedger(db.Where("room_id = ? AND period = ?", room.ID, req.Period), c)
		err := q.First(&rec).Error
		if err == nil {
			rec.Water, rec.Elec, rec.Gas, rec.Note = req.Water, req.Elec, req.Gas, strings.TrimSpace(req.Note)
			// 只覆盖客户端显式传了的抄表时间：没传就保留原值，
			// 免得「只改读数」把之前记的时间弄丢。
			if req.ReadAt != nil {
				rec.ReadAt = readAt
			}
			if err := db.Save(&rec).Error; err != nil {
				response.ErrorInternal(c, "保存抄表记录失败")
				return
			}
			response.Success(c, rec)
			return
		}
		rec = MeterRecord{
			UserID: currentUserID(c), LedgerID: room.LedgerID, RoomID: room.ID, Period: req.Period, // UserID 录入人戳
			ReadAt: readAt,
			Water:  req.Water, Elec: req.Elec, Gas: req.Gas, Note: strings.TrimSpace(req.Note),
		}
		if err := db.Create(&rec).Error; err != nil {
			response.ErrorInternal(c, "保存抄表记录失败")
			return
		}
		response.Success(c, rec)
	}
}

// normalizeReadAt 校验并归一抄表时间：nil（没传）→ 空串 + 不写库；
// 空串（显式清空）→ 空串；合法 YYYY-MM-DD → 原样。
// 返回 (值, 是否继续)；校验失败时已写好 400，调用方直接 return。
func normalizeReadAt(c *gin.Context, raw *string) (string, bool) {
	if raw == nil {
		return "", true
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return "", true
	}
	// 只收日期：房东手填「几号抄的」，不需要时分秒；带时间的输入截到日期，
	// 让前端直接传 datetime 字符串也能用。
	if len(v) > 10 {
		v = v[:10]
	}
	if _, err := time.ParseInLocation("2006-01-02", v, time.Local); err != nil {
		response.ErrorBadRequest(c, "抄表时间格式应为 YYYY-MM-DD")
		return "", false
	}
	return v, true
}

func handleMeterDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil || id <= 0 {
			response.ErrorBadRequest(c, "无效的记录 ID")
			return
		}
		// 删除前先确认记录在本次请求的可见台账内：越权一律按「不存在」处理，
		// 不泄漏记录是否存在。
		var rec MeterRecord
		if err := db.First(&rec, id).Error; err != nil || !inLedgerScope(c, rec.LedgerID) {
			response.ErrorNotFound(c, "抄表记录不存在")
			return
		}
		if !requireLedgerWrite(db, c, rec.LedgerID, ModuleMeters, PermFull) {
			return
		}
		result := db.Delete(&MeterRecord{}, id)
		if result.Error != nil {
			response.ErrorInternal(c, "删除抄表记录失败")
			return
		}
		if result.RowsAffected == 0 {
			response.ErrorNotFound(c, "抄表记录不存在")
			return
		}
		response.Success(c, gin.H{"deleted": true})
	}
}

// settleMeterRecord 开票后把当期读数沉淀进台账：该月已有记录时不覆盖
// （台账是抄表时点的原始记录），失败不影响开票主流程。
// 记录继承账单的台账（= 房间台账），避免跨台账孤儿记录。
//
// 抄表时间（2026-10-30 新增）按开票当天记：这条记录本来就是「开票时顺手沉淀」的，
// 时点就是此刻。房东后来在台账里补填/改正都能覆盖（upsert 按房+账期）。
func settleMeterRecord(db *gorm.DB, bill *Bill) {
	var n int64
	q := db.Model(&MeterRecord{}).Where("room_id = ? AND period = ?",
		bill.RoomID, bill.Period)
	if ledgerColumnReady(db, "meter_records") {
		q = q.Where("org_id = ?", bill.LedgerID)
	}
	q.Count(&n)
	if n > 0 {
		return
	}
	db.Create(&MeterRecord{
		UserID: bill.UserID, LedgerID: bill.LedgerID, RoomID: bill.RoomID, Period: bill.Period,
		ReadAt: time.Now().Format("2006-01-02"),
		Water:  bill.WaterNow, Elec: bill.ElecNow, Gas: bill.GasNow,
		Note: "随账单生成",
	})
}

// syncMeterRecordFromBill 编辑账单（含抄表弹窗更新读数）后把三表当期读数
// 同步进抄表台账；该月还没有台账记录时按建账口径补一条。
//
// 为什么需要（2026-10-31 粉丝反馈「副号抄的数据界面不显示、账单却有」）：
// 水电表不在一处时，房东先抄水再抄电。第一遍抄表开票给每间房建了账并沉淀
// 台账（此时电还是上月旧值）；第二遍抄电走的是**更新账单**路径，旧代码只在
// 建账时沉淀、更新不回写——账单金额是对的，抄表记录页的电表读数却停在旧值。
// 与 settleMeterRecord 的「已有记录不覆盖」相反，这里是**用户刚刚改过的读数**，
// 账单即最新意图，台账必须跟上；备注等其它字段不动。
// 失败只记日志、不影响账单保存主流程（与 settle 同一容错口径）。
func syncMeterRecordFromBill(db *gorm.DB, bill *Bill) {
	var rec MeterRecord
	q := db.Where("room_id = ? AND period = ?", bill.RoomID, bill.Period)
	if ledgerColumnReady(db, "meter_records") {
		q = q.Where("org_id = ?", bill.LedgerID)
	}
	if err := q.First(&rec).Error; err != nil {
		settleMeterRecord(db, bill)
		return
	}
	changed := false
	if rec.Water != bill.WaterNow {
		rec.Water = bill.WaterNow
		changed = true
	}
	if rec.Elec != bill.ElecNow {
		rec.Elec = bill.ElecNow
		changed = true
	}
	if rec.Gas != bill.GasNow {
		rec.Gas = bill.GasNow
		changed = true
	}
	if !changed {
		return
	}
	if err := db.Save(&rec).Error; err != nil {
		logger.Error("同步抄表台账失败（账单 %d / 房间 %d / %s）: %v", bill.ID, bill.RoomID, bill.Period, err)
	}
}
