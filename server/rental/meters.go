// meters.go — 抄表台账：每间房每月的水/电/燃气表读数记录，独立于账单存在。
//
// 账单里的读数是开票快照（结算口径），台账是按月的原始抄表记录：
// 季付房的中间月份、空置房、未开票的月份都可以记。创建账单时若该月
// 没有台账记录，会把账单上的当期读数自动沉淀一条，让现有抄表录入
// 习惯不变、台账自动积累；台账记录与账单互不回写。
package rental

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// MeterRecord 每房每月的三表读数。(room, period) 唯一，
// 重复录入按覆盖处理（改数即重新保存）。UserID 是「录入人」戳。
type MeterRecord struct {
	ID       uint    `gorm:"primarykey" json:"id"`
	UserID   uint    `gorm:"index" json:"-"`
	LedgerID uint    `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID   uint    `gorm:"index" json:"room_id"`
	Period   string  `gorm:"not null;index" json:"period"` // YYYY-MM
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
			Water  float64 `json:"water"`
			Elec   float64 `json:"elec"`
			Gas    float64 `json:"gas"`
			Note   string  `json:"note"`
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
			if err := db.Save(&rec).Error; err != nil {
				response.ErrorInternal(c, "保存抄表记录失败")
				return
			}
			response.Success(c, rec)
			return
		}
		rec = MeterRecord{
			UserID: currentUserID(c), LedgerID: room.LedgerID, RoomID: room.ID, Period: req.Period, // UserID 录入人戳
			Water: req.Water, Elec: req.Elec, Gas: req.Gas, Note: strings.TrimSpace(req.Note),
		}
		if err := db.Create(&rec).Error; err != nil {
			response.ErrorInternal(c, "保存抄表记录失败")
			return
		}
		response.Success(c, rec)
	}
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
		Water: bill.WaterNow, Elec: bill.ElecNow, Gas: bill.GasNow,
		Note: "随账单生成",
	})
}
