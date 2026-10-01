// importexport.go — CSV 模板下载、账单/房源导出与模板导入。
//
// CSV 统一 UTF-8 BOM（utils.WriteCSV 已处理），Excel/WPS 打开中文不乱码；
// 导出列顺序与模板一致，导出文件可直接修改后回导。
package rental

import (
	"encoding/csv"
	"io"
	"strconv"
	"strings"
	"time"

	"smallgo/server/response"
	"smallgo/server/sysconfig"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// billColumns 是模板/导出共用的 19 列表头。前 17 列与历史模板一致，
// 第 18 列"水费模式"、第 19 列"缴费周期"是后加的，旧的 17 列文件仍可导入。
var billColumns = []string{
	"房号", "租户名", "月份", "租金",
	"上月水表", "本月水表", "上月电表", "本月电表", "上月燃气表", "本月燃气表",
	"水费单价", "电费单价", "燃气单价", "卫生费", "管理费", "已收金额", "备注",
	"水费模式", "缴费周期",
}

// legacyBillColumns 是新增"水费模式"列之前的表头，导入时用它兼容旧文件。
var legacyBillColumns = billColumns[:17]

const maxImportBytes = 2 << 20 // 2MB

func setupDataRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/template", handleTemplate(db))
	api.GET("/rental/export", handleBillsExport(db))
	api.GET("/rental/rooms/export", handleRoomsExport(db))
	api.POST("/rental/import", handleImport(db))
}

// handleTemplate 下载导入模板：表头 + 2 行示例。
func handleTemplate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := currentUserID(c)
		water, elec, gas := defaultPrices(db, userID)
		// 示例里的水费模式与金额跟随用户的全局默认，避免示例与实际口径打架。
		waterMode := waterModeMeter
		if v, err := sysconfig.GetConfig(db, "rental_water_mode", userID); err == nil && v == waterModeMonthly {
			waterMode = waterModeMonthly
			water = configFloat(db, "rental_water_monthly_fee", userID, 0)
		}
		rows := [][]string{billColumns}
		rows = append(rows,
			[]string{"101", "张三", time.Now().Format("2006-01"), "1500",
				"100", "112.5", "800", "920", "50", "56",
				formatPrice(water), formatPrice(elec), formatPrice(gas), "10", "30", "0", "示例行，可删除",
				waterModeLabel(waterMode), cycleLabel(payCycleMonthly)},
			[]string{"102", "李四", "", "1200", "", "", "", "", "", "", "", "", "", "", "", "", "", "", ""},
		)
		utils.WriteCSV(c, utils.ExportFilename("rental_import_template", "csv"), rows)
	}
}

// handleBillsExport 导出账单（period 空 = 全部月份）。
func handleBillsExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		period := strings.TrimSpace(c.Query("period"))
		query := db.Model(&Bill{}).Where("user_id = ?", currentUserID(c))
		if period != "" {
			query = query.Where("period = ?", period)
		}
		bills := make([]Bill, 0)
		if err := query.Order("period DESC, room_no ASC").Limit(10000).Find(&bills).Error; err != nil {
			response.ErrorInternal(c, "导出失败")
			return
		}

		rows := [][]string{billColumns}
		for _, b := range bills {
			rows = append(rows, []string{
				b.RoomNo, b.TenantName, b.Period, formatPrice(b.Rent),
				formatPrice(b.WaterLast), formatPrice(b.WaterNow),
				formatPrice(b.ElecLast), formatPrice(b.ElecNow),
				formatPrice(b.GasLast), formatPrice(b.GasNow),
				formatPrice(b.WaterPrice), formatPrice(b.ElecPrice), formatPrice(b.GasPrice),
				formatPrice(b.SanitationFee), formatPrice(b.ManagementFee),
				formatPrice(b.PaidAmount), b.Remark,
				waterModeLabel(b.WaterMode), cycleLabel(b.PayCycle),
			})
		}
		utils.WriteCSV(c, utils.ExportFilename("rental_bills", "csv"), rows)
	}
}

// handleRoomsExport 导出房源列表。
//
// 水费模式/金额与缴费周期/缴费日/提醒天数自 v0.2.3 起在租户上设置，这里导出
// 的是每间房**当前生效**的值（在租租户 → 全局默认），便于核对实际口径：
// 水费模式恒为"按吨"或"包月"，两个金额列只填与当前方式匹配的那一列。
func handleRoomsExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rooms := make([]Room, 0)
		if err := db.Where("user_id = ?", currentUserID(c)).
			Order("room_no ASC").Limit(10000).Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "导出失败")
			return
		}
		rows := [][]string{{
			"房号", "小区", "楼栋", "单元", "楼层", "默认租金", "默认卫生费", "默认管理费",
			"水费模式", "水费单价", "水费包月金额", "电费单价", "燃气单价", "水表底数", "电表底数", "燃气表底数", "备注",
			"缴费周期", "缴费日", "提前提醒天数",
		}}
		for i := range rooms {
			r := &rooms[i]
			cfg := effectiveRoomBilling(db, r)
			waterMode := "按吨"
			waterPrice, waterMonthlyFee := cfg.WaterAmount, 0.0
			if cfg.WaterMode == waterModeMonthly {
				waterMode = "包月"
				waterPrice, waterMonthlyFee = 0, cfg.WaterAmount
			}
			// 缴费日 0 表示不提醒（导出为空）。
			payDay := ""
			if cfg.PayDay > 0 {
				payDay = strconv.Itoa(cfg.PayDay)
			}
			rows = append(rows, []string{
				r.RoomNo, r.Community, r.Building, r.Unit, r.Floor, formatPrice(r.DefaultRent),
				formatPrice(r.DefaultSanitationFee), formatPrice(r.DefaultManagementFee),
				waterMode,
				formatPrice(waterPrice), formatPrice(waterMonthlyFee),
				formatPrice(r.ElecPrice), formatPrice(r.GasPrice),
				formatPrice(r.InitialWater), formatPrice(r.InitialElec), formatPrice(r.InitialGas),
				r.Notes,
				cycleLabel(cfg.PayCycle), payDay, strconv.Itoa(cfg.RemindDays),
			})
		}
		utils.WriteCSV(c, utils.ExportFilename("rental_rooms", "csv"), rows)
	}
}

type importResult struct {
	Created int              `json:"created"`
	Updated int              `json:"updated"`
	Failed  int              `json:"failed"`
	Errors  []importRowError `json:"errors"`
}

type importRowError struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// handleImport 导入账单 CSV（模板格式）。逐行处理：错误行跳过并记录，
// 不影响其他行；房号不存在则自动建房，同房同月已有账单则更新。
func handleImport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			response.ErrorBadRequest(c, "请选择要导入的 CSV 文件")
			return
		}
		if fileHeader.Size > maxImportBytes {
			response.ErrorBadRequest(c, "导入文件不能超过 2MB")
			return
		}
		if !strings.HasSuffix(strings.ToLower(fileHeader.Filename), ".csv") {
			response.ErrorBadRequest(c, "仅支持 .csv 文件")
			return
		}

		f, err := fileHeader.Open()
		if err != nil {
			response.ErrorBadRequest(c, "读取上传文件失败")
			return
		}
		defer f.Close()

		result, err := importCSV(db, c, f)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		response.Success(c, result)
	}
}

func importCSV(db *gorm.DB, c *gin.Context, r io.Reader) (*importResult, error) {
	userID := currentUserID(c)
	_, defElec, defGas := defaultPrices(db, userID)

	// 整体读入（上传已限制 2MB）并剥离 UTF-8 BOM——Excel/WPS 另存的
	// CSV 普遍携带 BOM，若不剥离，首列表头比对会失败。
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, errCSV("读取上传文件失败")
	}
	content := strings.TrimPrefix(string(raw), "\ufeff")

	reader := csv.NewReader(strings.NewReader(content))
	// 不规范引号也尽量兼容。
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, errCSV("CSV 解析失败，请使用系统提供的模板")
	}
	if len(records) < 1 {
		return nil, errCSV("文件为空，请使用系统提供的模板")
	}
	if !matchHeader(records[0]) {
		return nil, errCSV("表头与模板不一致，请下载最新模板后重试")
	}

	result := &importResult{Errors: []importRowError{}}
	thisMonth := time.Now().Format("2006-01")

	for i, rec := range records[1:] {
		lineNo := i + 2 // Excel 行号（含表头）
		if isBlankRow(rec) {
			continue
		}
		created, reason := importRow(db, c, rec, thisMonth, defElec, defGas)
		switch {
		case reason != "":
			result.Failed++
			if len(result.Errors) < 50 {
				result.Errors = append(result.Errors, importRowError{Line: lineNo, Reason: reason})
			}
		case created:
			result.Created++
		default:
			result.Updated++
		}
	}
	return result, nil
}

// importRow 处理单行；返回 (是否新建账单, 错误原因)。
func importRow(db *gorm.DB, c *gin.Context, rec []string, thisMonth string, defElec, defGas float64) (bool, string) {
	userID := currentUserID(c)
	get := func(i int) string {
		if i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	num := func(i int) float64 {
		v, err := strconv.ParseFloat(get(i), 64)
		if err != nil {
			return 0
		}
		return v
	}

	roomNo := get(0)
	if roomNo == "" {
		return false, "房号不能为空"
	}
	period := get(2)
	if period == "" {
		period = thisMonth
	}
	if !isValidPeriod(period) {
		return false, "月份格式应为 YYYY-MM（如 2026-09）"
	}

	// 水费模式（第 18 列，可空）："包月"/"按吨" 与 monthly/meter 等价，
	// 留空表示未指定：新账单跟房源/全局默认，老账单沿用原快照。
	csvWaterMode := parseWaterMode(get(17))
	// 缴费周期（第 19 列，可空）："月付"/"季付" 与 monthly/quarterly 等价，
	// 留空表示未指定：新账单跟房源设置，老账单沿用原快照。
	csvPayCycle := parsePayCycle(get(18))

	// 房间不存在则按行内数据建档。水费/缴费设置自 v0.2.3 起挂在租户上，
	// 房源侧不再写入这些遗留列。
	var room Room
	err := db.Where("user_id = ? AND room_no = ?", userID, roomNo).First(&room).Error
	if err != nil {
		room = Room{
			UserID:               userID,
			RoomNo:               roomNo,
			DefaultRent:          num(3),
			DefaultSanitationFee: num(13),
			DefaultManagementFee: num(14),
			ElecPrice:            num(11),
			GasPrice:             num(12),
			InitialWater:         num(4),
			InitialElec:          num(6),
			InitialGas:           num(8),
		}
		if err := db.Create(&room).Error; err != nil {
			return false, "自动创建房源失败：" + roomNo
		}
	}

	tenantName := get(1)
	// 该房生效的计费与缴费设置（在租租户 → 全局默认）：CSV 没显式指定时的缺省口径。
	cfg := effectiveRoomBilling(db, &room)

	var bill Bill
	err = db.Where("user_id = ? AND room_id = ? AND period = ?", userID, room.ID, period).First(&bill).Error
	isNew := err != nil
	if isNew {
		bill = Bill{
			UserID: userID, RoomID: room.ID, Period: period, RoomNo: room.RoomNo,
			WaterMode: csvWaterMode,
		}
		if bill.WaterMode == "" {
			bill.WaterMode = cfg.WaterMode
		}
		bill.PayCycle = csvPayCycle
		if bill.PayCycle == "" {
			bill.PayCycle = cfg.PayCycle
		}
	} else {
		if csvWaterMode != "" {
			bill.WaterMode = csvWaterMode
		} else if bill.WaterMode == "" {
			bill.WaterMode = waterModeMeter
		}
		if csvPayCycle != "" {
			bill.PayCycle = csvPayCycle
		}
	}

	// 单价缺省：租户在该方式下的设置 → 全局默认价。包月取元/月金额，按吨取元/吨单价。
	waterPrice := num(10)
	if waterPrice == 0 {
		waterPrice = waterAmountForMode(db, &room, bill.WaterMode)
	}
	elecPrice := num(11)
	if elecPrice == 0 {
		elecPrice = defaultOr(&room, 1, defElec)
	}
	gasPrice := num(12)
	if gasPrice == 0 {
		gasPrice = defaultOr(&room, 2, defGas)
	}

	bill.TenantName = tenantName
	bill.Rent = num(3)
	bill.WaterLast, bill.WaterNow = num(4), num(5)
	bill.ElecLast, bill.ElecNow = num(6), num(7)
	bill.GasLast, bill.GasNow = num(8), num(9)
	bill.WaterPrice, bill.ElecPrice, bill.GasPrice = waterPrice, elecPrice, gasPrice
	bill.SanitationFee = num(13)
	bill.ManagementFee = num(14)
	bill.PaidAmount = round2(num(15))
	bill.Remark = get(16)
	bill.recalc()

	if bill.PaidAmount > feeTolerance && bill.PaidAt == nil {
		now := time.Now()
		bill.PaidAt = &now
	}

	if isNew {
		if err := db.Create(&bill).Error; err != nil {
			return false, "创建账单失败"
		}
		db.Model(&bill).UpdateColumn("receipt_no", receiptNo(bill.Period, bill.RoomNo, bill.ID))
	} else {
		if err := db.Save(&bill).Error; err != nil {
			return false, "更新账单失败"
		}
	}

	// 导入带租户名且房内无同名在租租户时，补登一名在租租户，便于后续开票。
	// CSV 里显式写了水费模式/缴费周期时一并落到这位新租户上（金额同理），
	// 之后的账单就按租户口径生成。
	if tenantName != "" && !isActiveTenant(db, room.ID, tenantName) {
		tenant := Tenant{
			UserID: userID, RoomID: room.ID, Name: tenantName, Active: true,
			WaterMode: csvWaterMode, PayCycle: csvPayCycle,
			PayDay: tenantFollowGlobal, RemindDays: tenantFollowGlobal,
		}
		if csvWaterMode == waterModeMonthly {
			tenant.WaterMonthlyFee = num(10)
		} else if csvWaterMode == waterModeMeter {
			tenant.WaterPrice = num(10)
		}
		db.Create(&tenant)
	}
	return isNew, ""
}

// defaultOr 单价缺省：房源覆盖价（电/燃气）→ 导入时的全局默认价。
// 水费单价已随计费设置搬到租户上，单独由 waterAmountForMode 解析。
func defaultOr(room *Room, which int, def float64) float64 {
	switch which {
	case 1:
		if room.ElecPrice > 0 {
			return room.ElecPrice
		}
	case 2:
		if room.GasPrice > 0 {
			return room.GasPrice
		}
	}
	return def
}

func isActiveTenant(db *gorm.DB, roomID uint, name string) bool {
	var n int64
	db.Model(&Tenant{}).Where("room_id = ? AND active = ? AND name = ?", roomID, true, name).Count(&n)
	return n > 0
}

// matchHeader 校验表头：前 17 列必须与模板一致；第 18 列"水费模式"、
// 第 19 列"缴费周期"是后加的，旧模板（17 列/18 列）仍然接受。
// 新文件可以止步于任一历史版本的列数，但出现过的列名必须逐一相符，
// 且不接受未知的多余列（避免错位文件被误读）。
func matchHeader(header []string) bool {
	if len(header) < len(legacyBillColumns) || len(header) > len(billColumns) {
		return false
	}
	for i, col := range legacyBillColumns {
		if strings.TrimSpace(header[i]) != col {
			return false
		}
	}
	for i := len(legacyBillColumns); i < len(header); i++ {
		if strings.TrimSpace(header[i]) != billColumns[i] {
			return false
		}
	}
	return true
}

func isBlankRow(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func errCSV(msg string) error {
	return &csvError{msg}
}

type csvError struct{ msg string }

func (e *csvError) Error() string { return e.msg }

// formatPrice 金额/读数输出：整数省略小数位，最多两位小数。
func formatPrice(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if i := strings.Index(s, "."); i >= 0 && len(s)-i-1 > 2 {
		s = strconv.FormatFloat(v, 'f', 2, 64)
	}
	return s
}
