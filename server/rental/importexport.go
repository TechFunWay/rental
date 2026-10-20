// importexport.go — CSV 模板下载、账单/房源导出与模板导入。
//
// CSV 统一 UTF-8 BOM（writeCSV 已处理），Excel/WPS 打开中文不乱码；
// 导出列顺序与模板一致，导出文件可直接修改后回导。
package rental

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"smallgo/server/response"
	"smallgo/server/sysconfig"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"golang.org/x/text/encoding/simplifiedchinese"
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
	// 模板与导出是读操作按各自模块（并集口径，数据范围由 handler 按 ledger 收窄）；
	// CSV 导入会整批写库，必须用单台账口径：本次请求指定的台账要达到档位，
	// 否则在 A 台账的 full 会被并集带进 B 台账的导入判定。
	api.GET("/rental/template", requireModule(db, ModuleBills, PermView), handleTemplate(db))
	api.GET("/rental/export", requireModule(db, ModuleBills, PermView), handleBillsExport(db))
	api.GET("/rental/rooms/export", requireModule(db, ModuleRooms, PermView), handleRoomsExport(db))
	api.POST("/rental/import", requireLedgerModule(db, ModuleBills, PermFull), handleImport(db))
	// 房源导入会批量新建/更新房源，归房源管理档（对齐房源删除的 full 档）。
	api.GET("/rental/rooms/template", requireModule(db, ModuleRooms, PermView), handleRoomTemplate(db))
	api.POST("/rental/rooms/import", requireLedgerModule(db, ModuleRooms, PermFull), handleRoomsImport(db))
	// 租客导入会批量登记/更新租户，归租户模块档。
	api.GET("/rental/tenants/template", requireModule(db, ModuleTenants, PermView), handleTenantTemplate(db))
	api.GET("/rental/tenants/export", requireModule(db, ModuleTenants, PermView), handleTenantsExport(db))
	api.POST("/rental/tenants/import", requireLedgerModule(db, ModuleTenants, PermFull), handleTenantsImport(db))
}

// handleTemplate 下载导入模板：表头 + 2 行示例。
func handleTemplate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		water, elec, gas := defaultPrices(db)
		// 示例里的水费模式与金额跟随全局默认，避免示例与实际口径打架。
		waterMode := waterModeMeter
		if v, err := sysconfig.GetConfig(db, "rental_water_mode", 0); err == nil && v == waterModeMonthly {
			waterMode = waterModeMonthly
			water = configFloat(db, "rental_water_monthly_fee", 0, 0)
		}
		rows := [][]string{billColumns}
		rows = append(rows,
			[]string{"101", "张三", time.Now().Format("2006-01"), "1500",
				"100", "112.5", "800", "920", "50", "56",
				formatPrice(water), formatPrice(elec), formatPrice(gas), "10", "30", "0", "示例行，可删除",
				waterModeLabel(waterMode), cycleLabel(payCycleMonthly)},
			[]string{"102", "李四", "", "1200", "", "", "", "", "", "", "", "", "", "", "", "", "", "", ""},
		)
		writeCSV(c, utils.ExportFilename("rental_import_template", "csv"), rows)
	}
}

// handleBillsExport 导出账单 CSV。
//
// 账期筛选支持三种写法（2026-10-11 粉丝反馈"不能按时间段批量导出"）：
//   - period=YYYY-MM          单月（老口径，保持兼容）；
//   - period_from / period_to 区间，两端闭区间，可只给一端（另一端视为不限）；
//   - 都不给                   全部月份。
func handleBillsExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		period := strings.TrimSpace(c.Query("period"))
		periodFrom := strings.TrimSpace(c.Query("period_from"))
		periodTo := strings.TrimSpace(c.Query("period_to"))
		// v0.6.0：导出只含本次请求可见台账的账单（导出等于批量读）。
		query, empty := scopeLedger(db.Model(&Bill{}), c)
		rows := [][]string{billColumns}
		if empty {
			writeCSV(c, utils.ExportFilename("rental_bills", "csv"), rows)
			return
		}
		if period != "" {
			query = query.Where("period = ?", period)
		} else {
			// 区间导出：账期是 YYYY-MM 定长字符串，字典序即时间序，可直接比较。
			if periodFrom != "" {
				query = query.Where("period >= ?", periodFrom)
			}
			if periodTo != "" {
				query = query.Where("period <= ?", periodTo)
			}
		}
		bills := make([]Bill, 0)
		if err := query.Order("period DESC, room_no ASC").Limit(10000).Find(&bills).Error; err != nil {
			response.ErrorInternal(c, "导出失败")
			return
		}

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
		writeCSV(c, utils.ExportFilename("rental_bills", "csv"), rows)
	}
}

// roomExportColumns 是房源导出的 22 列表头：房源自身字段列与导入模板同名同序，
// "水费模式/水费单价/水费包月金额"与"缴费周期/缴费日/提前提醒天数" 6 列
// 是该房当前生效的租户级设置（在租租户 → 全局默认），仅用于核对口径，
// 房源导入不接受这 6 列的写入（见 importRoomsCSV）。
// 末尾的"定位地址/面积(㎡)"是 2026-10-20 房东需求新增的两列；追加在末尾、
// 不动既有列顺序，保证用户手里的旧 20 列导出文件仍可回导（见 roomHeaderSlots）。
var roomExportColumns = [...]string{
	"房号", "小区", "楼栋", "单元", "楼层", "默认租金", "默认卫生费", "默认管理费",
	"水费模式", "水费单价", "水费包月金额", "电费单价", "燃气单价", "水表底数", "电表底数", "燃气表底数", "备注",
	"缴费周期", "缴费日", "提前提醒天数",
	"定位地址", "面积(㎡)",
}

// handleRoomsExport 导出房源列表。
//
// 水费模式/金额与缴费周期/缴费日/提醒天数自 v0.2.3 起在租户上设置，这里导出
// 的是每间房**当前生效**的值（在租租户 → 全局默认），便于核对实际口径：
// 水费模式恒为"按吨"或"包月"，两个金额列只填与当前方式匹配的那一列。
func handleRoomsExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// v0.6.0：导出只含本次请求可见台账的房源。
		query, empty := scopeLedger(db.Model(&Room{}), c)
		rows := [][]string{roomExportColumns[:]}
		if empty {
			writeCSV(c, utils.ExportFilename("rental_rooms", "csv"), rows)
			return
		}
		rooms := make([]Room, 0)
		if err := query.Order("room_no ASC").Limit(10000).Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "导出失败")
			return
		}
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
				r.Address, formatArea(r.AreaSize),
			})
		}
		writeCSV(c, utils.ExportFilename("rental_rooms", "csv"), rows)
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

// readCSVRecords 读入上传的 CSV 并解析成记录，对真实办公软件的输出做容错。
//
// 处理三类来自 Excel / WPS 的常见"脏输出"（2026-10-11 粉丝反馈"导入不了"的根因）：
//  1. **编码**：WPS「另存为 CSV」默认可能是 GBK/GB18030，而非 UTF-8。直接按
//     UTF-8 解码会把中文表头变成乱码，报出来却是"表头与模板不一致"，把人引到
//     错误方向。这里先探测编码再解码。
//  2. **BOM**：UTF-8 BOM 会让首列表头多一个不可见字符，必须剥离。
//  3. **尾随空列**：表格软件常在行尾留一个空列（多一个逗号），
//     `FieldsPerRecord` 与严格长度校验都会被它绊倒，这里统一裁掉。
func readCSVRecords(r io.Reader) ([][]string, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, errCSV("读取上传文件失败")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, errCSV("文件为空，请使用系统提供的模板")
	}

	content := decodeCSVBytes(raw)

	reader := csv.NewReader(strings.NewReader(content))
	// 不规范引号也尽量兼容。
	reader.LazyQuotes = true
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, errCSV("CSV 解析失败，请确认文件是逗号分隔的 CSV（不要用 Excel 默认的 xls/xlsx 另存）")
	}
	if len(records) == 0 {
		return nil, errCSV("文件为空，请使用系统提供的模板")
	}
	// 逐行裁掉尾随空列（表格软件常见的多余逗号）。
	for i := range records {
		records[i] = trimTrailingEmpty(records[i])
	}
	return records, nil
}

// decodeCSVBytes 把上传的字节解码成字符串：
// 剥离 UTF-8 BOM；若内容不是合法 UTF-8，则按 GB18030 解码（兼容 GBK）。
// 检测顺序很重要——UTF-8 是 GB18030 的子集语义，先判 UTF-8 才不会误伤。
func decodeCSVBytes(raw []byte) string {
	raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) // UTF-8 BOM
	if utf8.Valid(raw) {
		return string(raw)
	}
	// 不是合法 UTF-8 → 按 GB18030 解（覆盖 GBK/GB2312，WPS 另存 CSV 的默认编码）。
	if dec := simplifiedchinese.GB18030.NewDecoder(); dec != nil {
		if out, err := dec.Bytes(raw); err == nil {
			return strings.TrimPrefix(string(out), "\ufeff")
		}
	}
	return string(raw)
}

// trimTrailingEmpty 去掉记录尾部的空字段（只裁尾部，中间的空列保留——
// 中间的列有语义，尾部空列是表格软件加出来的噪音）。
func trimTrailingEmpty(rec []string) []string {
	end := len(rec)
	for end > 0 && strings.TrimSpace(rec[end-1]) == "" {
		end--
	}
	return rec[:end]
}

func importCSV(db *gorm.DB, c *gin.Context, r io.Reader) (*importResult, error) {
	_, defElec, defGas := defaultPrices(db)

	// 统一走 readCSVRecords：处理 BOM、GBK/GB18030 编码与尾随空列。
	records, err := readCSVRecords(r)
	if err != nil {
		return nil, err
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

	// 写入目标台账：账单导入整体落在一个台账内（URL 上的 ?ledger_id）。
	writeLedger, ok := writeLedgerID(c)
	if !ok {
		return false, "请指定要操作的台账（ledger_id）"
	}

	// 水费模式（第 18 列，可空）："包月"/"按吨" 与 monthly/meter 等价，
	// 留空表示未指定：新账单跟房源/全局默认，老账单沿用原快照。
	csvWaterMode := parseWaterMode(get(17))
	// 缴费周期（第 19 列，可空）："月付"/"季付"/"半年付"/"年付" 与
	// monthly/quarterly/semiannual/annual 等价，
	// 留空表示未指定：新账单跟房源设置，老账单沿用原快照。
	csvPayCycle := parsePayCycle(get(18))

	// 房间不存在则按行内数据建档。CSV 只有房号一列（没有小区），匹配按房号
	// 取本次导入台账内最早登记的一间——不会命中别台账的同号房。
	// UserID 是导入人戳。水费/缴费设置自 v0.2.3 起挂在租户上，
	// 房源侧不再写入这些遗留列。
	var room Room
	rq := db.Where("room_no = ?", roomNo)
	if ledgerColumnReady(db, "rooms") {
		rq = rq.Where("org_id = ?", writeLedger)
	}
	err := rq.Order("id ASC").First(&room).Error
	if err != nil {
		room = Room{
			UserID:               currentUserID(c), // 录入人戳
			LedgerID:             writeLedger,      // 归属本次导入的台账
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
	bq := db.Where("room_id = ? AND period = ?", room.ID, period)
	if ledgerColumnReady(db, "bills") {
		bq = bq.Where("org_id = ?", writeLedger)
	}
	err = bq.First(&bill).Error
	isNew := err != nil
	if isNew {
		bill = Bill{
			UserID: currentUserID(c), LedgerID: writeLedger, RoomID: room.ID, Period: period, RoomNo: room.RoomNo, // 录入人戳
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
	// 刻意**不写 DisabledFees**：CSV 导入是用户手里的明确账目（19 列里就有燃气费），
	// 以文件为准。台账停用只作用于系统自动出账，不该把用户导入的历史数悄悄清零。
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
		// 新账单也落一份费用明细，单据/详情与合计同源（此前导入的新账单没有
		// bill_items，单据只能走「固定列汇总」回退分支）。
		syncBillItems(db, &bill, nil)
	} else {
		if err := db.Save(&bill).Error; err != nil {
			return false, "更新账单失败"
		}
		// 覆盖已有账单后必须重建明细：否则单据里还是上次导入/开票的旧金额，
		// 合计却已按新金额重算，出现「明细加起来对不上合计」（2026-10-17 审计修复）。
		syncBillItemsOnUpdate(db, &bill)
	}

	// 导入带租户名且房内无同名在租租户时，补登一名在租租户，便于后续开票。
	// CSV 里显式写了水费模式/缴费周期时一并落到这位新租户上（金额同理），
	// 之后的账单就按租户口径生成。
	if tenantName != "" && !isActiveTenant(db, room.ID, tenantName, writeLedger) {
		tenant := Tenant{
			UserID: currentUserID(c), LedgerID: writeLedger, RoomID: room.ID, Name: tenantName, Active: true, // 录入人戳
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

// isActiveTenant 该房是否已有同名在租租户；限定台账，避免别台账的同名租户
// 让本次导入「以为已经登记过」而漏建。
func isActiveTenant(db *gorm.DB, roomID uint, name string, ledgerID uint) bool {
	var n int64
	q := db.Model(&Tenant{}).Where("room_id = ? AND active = ? AND name = ?", roomID, true, name)
	if ledgerColumnReady(db, "tenants") {
		q = q.Where("org_id = ?", ledgerID)
	}
	q.Count(&n)
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

// formatArea 建筑面积输出：0（未填）导出空单元格而不是 "0"——回导时空值走
// "留空保留原值"合并，与表单里 0=未填 的语义保持一致。
func formatArea(v float64) string {
	if v <= 0 {
		return ""
	}
	return formatPrice(v)
}

// ---------------------------------------------------------------------------
// 房源导入
// ---------------------------------------------------------------------------

// roomColumns 是房源导入模板的 16 列表头，与房源编辑表单字段一一对应。
// 计费与缴费设置（水费模式/单价/包月、缴费周期/缴费日/提醒天数）自 v0.2.3
// 起挂在租户上，不在房源导入范围内，模板刻意不含这些列。
// 末尾的"定位地址/面积(㎡)"是 2026-10-20 房东需求新增的两列；只追加在末尾、
// 不动既有列的顺序与名称，用户手里的旧 14 列文件仍可导入（缺列按留空处理，
// 见 roomHeaderSlots）。
var roomColumns = [...]string{
	"房号", "小区", "楼栋", "单元", "楼层",
	"默认租金", "默认卫生费", "默认管理费",
	"电费单价", "燃气单价",
	"水表底数", "电表底数", "燃气表底数", "备注",
	"定位地址", "面积(㎡)",
}

// handleRoomTemplate 下载房源导入模板：表头 + 2 行示例。
func handleRoomTemplate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows := [][]string{roomColumns[:]}
		rows = append(rows,
			[]string{"101", "阳光小区", "1栋", "1单元", "3", "1500", "10", "30", "1.2", "3.5", "100", "800", "50", "示例行，可删除", "XX路XX号门口便利店旁", "68"},
			[]string{"102", "", "", "", "", "1200", "", "", "", "", "", "", "", "", "", ""},
		)
		writeCSV(c, utils.ExportFilename("rental_rooms_import_template", "csv"), rows)
	}
}

// roomSlots 把房源字段的语义槽位（下标与 roomColumns 一致）映射到 CSV 列
// 下标，用于兼容四种表头：旧 14 列导入模板、新 16 列导入模板、旧 20 列房源
// 导出、新 22 列房源导出（导出 → 修改 → 回导）。导出文件多出的 6 列计费/
// 缴费设置不参与映射，值一律忽略——它们是租户级设置的当前生效值，房源表已
// 不再存储（v0.2.3 迁移搬走）。槽位为 -1 表示该字段在这份表头里没有对应列
// （用户手里的旧文件缺「定位地址 / 面积(㎡)」），读取时按留空处理。
type roomSlots [len(roomColumns)]int

// 新增两列只追加在两种表头的**末尾**，既有列的顺序与名称一律不动，所以旧文件
// 的列数正好是「新列数 - 2」。这两个常量让四种长度组合共用同一套比对逻辑。
const (
	legacyRoomColumns   = len(roomColumns) - 2       // 旧导入模板 14 列
	legacyExportColumns = len(roomExportColumns) - 2 // 旧房源导出 20 列
)

func roomHeaderSlots(header []string) (roomSlots, bool) {
	var slots roomSlots
	for i := range slots {
		slots[i] = -1
	}
	switch len(header) {
	case legacyRoomColumns, len(roomColumns):
		// 导入模板：逐列同名同序；旧 14 列文件少的两列保持 -1（留空）。
		for i := range header {
			if strings.TrimSpace(header[i]) != roomColumns[i] {
				return slots, false
			}
			slots[i] = i
		}
	case legacyExportColumns, len(roomExportColumns):
		// 导出表头：14 个房源字段列中间插了 3 列水费设置，按下标跳过。
		exportFieldCols := [legacyRoomColumns]int{0, 1, 2, 3, 4, 5, 6, 7, 11, 12, 13, 14, 15, 16}
		for i, col := range exportFieldCols {
			if strings.TrimSpace(header[col]) != roomExportColumns[col] {
				return slots, false
			}
			slots[i] = col
		}
		// 新 22 列导出：末尾两列就是定位地址/面积；旧 20 列导出没有，留 -1。
		if len(header) == len(roomExportColumns) {
			for j, col := range []int{legacyExportColumns, legacyExportColumns + 1} {
				name := roomColumns[legacyRoomColumns+j]
				if strings.TrimSpace(header[col]) != name {
					return slots, false
				}
				slots[legacyRoomColumns+j] = col
			}
		}
	default:
		return slots, false
	}
	return slots, true
}

// handleRoomsImport 导入房源 CSV（模板格式，也接受房源导出文件回导）。
// 逐行处理：错误行跳过并记录，不影响其他行。
func handleRoomsImport(db *gorm.DB) gin.HandlerFunc {
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

		result, err := importRoomsCSV(db, c, f)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		response.Success(c, result)
	}
}

func importRoomsCSV(db *gorm.DB, c *gin.Context, r io.Reader) (*importResult, error) {
	// 与账单导入同口径：统一走 readCSVRecords（BOM / GBK 编码 / 尾随空列容错）。
	records, err := readCSVRecords(r)
	if err != nil {
		return nil, err
	}
	slots, ok := roomHeaderSlots(records[0])
	if !ok {
		return nil, errCSV("表头与房源导入模板不一致，请下载最新模板后重试（列名与顺序需与模板一致）")
	}

	result := &importResult{Errors: []importRowError{}}
	// 文件内 (小区, 房号) 去重：同键后出现的行报错跳过，避免两行数据
	// 互相覆盖的歧义（键值大小写与空白已按行内 trim 处理）。
	seen := map[string]int{}

	for i, rec := range records[1:] {
		lineNo := i + 2 // Excel 行号（含表头）
		if isBlankRow(rec) {
			continue
		}
		created, reason := importRoomRow(db, c, rec, slots, seen, lineNo)
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

// importRoomRow 处理单行；返回 (是否新建房源, 错误原因)。
//
// 唯一键是 (小区, 房号)——与房源表唯一索引一致，不同小区允许同房号：
//   - 文件内同键重复：第二次出现报错（seen 记录首次出现行号）；
//   - 库内已存在：按"非空覆盖、空值保留"合并更新（重复导入同一文件是
//     幂等的，只改填了值的列，不会清掉未填的旧数据），计"更新"；
//   - 库内不存在：新建，计"新建"。
//
// 数字列非空时必须能解析为非负数，否则整行报错——房源导入不做账单导入
// 那样的"解析失败按 0"兜底，避免脏数据静默落库。
func importRoomRow(db *gorm.DB, c *gin.Context, rec []string, slots roomSlots, seen map[string]int, lineNo int) (bool, string) {
	get := func(slot int) string {
		col := slots[slot]
		if col >= 0 && col < len(rec) {
			return strings.TrimSpace(rec[col])
		}
		return ""
	}

	roomNo := get(0)
	if roomNo == "" {
		return false, "房号不能为空"
	}
	community := get(1)
	// v0.6.0：房源导入整体落在一个台账内，查重与新建都以它为界。
	writeLedger, okLedger := writeLedgerID(c)
	if !okLedger {
		return false, "请指定要操作的台账（ledger_id）"
	}
	key := community + "\x00" + roomNo
	if first, dup := seen[key]; dup {
		return false, fmt.Sprintf("小区+房号与第 %d 行重复", first)
	}
	seen[key] = lineNo

	// 数字列：默认租金/卫生费/管理费、电/燃气单价、水/电/燃气表底数。
	numNames := [8]string{"默认租金", "默认卫生费", "默认管理费", "电费单价", "燃气单价", "水表底数", "电表底数", "燃气表底数"}
	var nums [8]float64
	var setNums [8]bool
	for j, name := range numNames {
		v, set, msg := parseRoomNumber(name, get(5+j))
		if msg != "" {
			return false, msg
		}
		nums[j], setNums[j] = v, set
	}

	var room Room
	rq := db.Where("community = ? AND room_no = ?", community, roomNo)
	if ledgerColumnReady(db, "rooms") {
		// v0.6.0：唯一键按台账，查重也按台账——别台账有同键房源不参与合并更新。
		rq = rq.Where("org_id = ?", writeLedger)
	}
	err := rq.First(&room).Error
	isNew := err != nil
	if isNew {
		// UserID 是「录入人」戳（谁导入的房源），只作溯源；归属看 LedgerID。
		room = Room{UserID: currentUserID(c), LedgerID: writeLedger, Community: community, RoomNo: roomNo}
	}

	// 文本列与数字列都按"非空覆盖"合并；新建时字段本就是零值，等价于全量写入。
	if v := get(2); v != "" {
		room.Building = v
	}
	if v := get(3); v != "" {
		room.Unit = v
	}
	if v := get(4); v != "" {
		room.Floor = v
	}
	room.DefaultRent = mergeRoomNumber(room.DefaultRent, nums[0], setNums[0], isNew)
	room.DefaultSanitationFee = mergeRoomNumber(room.DefaultSanitationFee, nums[1], setNums[1], isNew)
	room.DefaultManagementFee = mergeRoomNumber(room.DefaultManagementFee, nums[2], setNums[2], isNew)
	room.ElecPrice = mergeRoomNumber(room.ElecPrice, nums[3], setNums[3], isNew)
	room.GasPrice = mergeRoomNumber(room.GasPrice, nums[4], setNums[4], isNew)
	room.InitialWater = mergeRoomNumber(room.InitialWater, nums[5], setNums[5], isNew)
	room.InitialElec = mergeRoomNumber(room.InitialElec, nums[6], setNums[6], isNew)
	room.InitialGas = mergeRoomNumber(room.InitialGas, nums[7], setNums[7], isNew)
	if v := get(13); v != "" {
		room.Notes = v
	}
	// 定位地址与面积(㎡)（2026-10-20 房东需求新增的两列）：旧表头缺列时槽位是 -1、
	// get 返回空串，等价于"留空不覆盖"，与其余列的合并规则一致。
	// 地址超长与面积非法都整行报错（与表单校验同口径，不让脏数据静默落库）。
	if v := get(14); v != "" {
		if len([]rune(v)) > 200 {
			return false, "定位地址不能超过 200 字"
		}
		room.Address = v
	}
	area, setArea, areaMsg := parseRoomNumber("面积(㎡)", get(15))
	if areaMsg != "" {
		return false, areaMsg
	}
	if setArea && area > maxRoomAreaSize {
		return false, "面积(㎡)不能超过 100000"
	}
	room.AreaSize = mergeRoomNumber(room.AreaSize, area, setArea, isNew)
	// 房源上的水费/缴费字段是 0.2.2 及以前的遗留列，此处刻意不动
	//（与 applyRoomRequest 同规则）：编辑房源不会覆盖或清掉老数据。

	if isNew {
		if err := db.Create(&room).Error; err != nil {
			if isDuplicateKey(err) {
				return false, "该小区下房号已存在"
			}
			return false, "创建房源失败"
		}
	} else {
		if err := db.Save(&room).Error; err != nil {
			return false, "更新房源失败"
		}
	}
	return isNew, ""
}

// parseRoomNumber 解析房源 CSV 的数字单元格：空单元格返回 set=false；
// 非数字或负数返回具体原因，由调用方整行报错。
func parseRoomNumber(name, raw string) (float64, bool, string) {
	if raw == "" {
		return 0, false, ""
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false, name + "不是有效数字：" + raw
	}
	if v < 0 {
		return 0, false, name + "不能为负数"
	}
	return v, true, ""
}

// mergeRoomNumber 更新合并规则：单元格填了值才覆盖，空值保留库内原值；
// 新建行一律写入（未填即零值）。
func mergeRoomNumber(old, v float64, set, isNew bool) float64 {
	if isNew || set {
		return v
	}
	return old
}

// ---------------------------------------------------------------------------
// 租客导入
// ---------------------------------------------------------------------------

// tenantColumns 是租客导入模板的 15 列表头。房源定位用「小区+房号」，
// 计费与缴费列（水费模式/金额、缴费周期/缴费日/提醒天数）写进租户本身
// ——这与登记租户表单的字段一一对应，留空表示跟随全局默认。
var tenantColumns = [...]string{
	"小区", "房号", "姓名", "电话", "身份证号", "入住日期", "租约到期日", "押金",
	"水费模式", "水费单价", "水费包月金额", "缴费周期", "缴费日", "提前提醒天数", "备注",
}

// handleTenantTemplate 下载租客导入模板：表头 + 2 行示例。
func handleTenantTemplate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows := [][]string{tenantColumns[:]}
		rows = append(rows,
			[]string{"阳光小区", "101", "张三", "13800000001", "110101199001011234", "2026-01-01", "2026-12-31", "3000",
				"按吨", "5", "", "月付", "5", "3", "示例行，可删除"},
			[]string{"", "102", "李四", "", "", "", "", "", "", "", "", "", "", "", ""},
		)
		writeCSV(c, utils.ExportFilename("rental_tenants_import_template", "csv"), rows)
	}
}

// handleTenantsImport 导入租客 CSV（模板格式）。逐行处理：错误行跳过并记录，
// 不影响其他行。房源必须已存在（请先用房源导入建档），不自动建房。
func handleTenantsImport(db *gorm.DB) gin.HandlerFunc {
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

		result, err := importTenantsCSV(db, c, f)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		response.Success(c, result)
	}
}

func importTenantsCSV(db *gorm.DB, c *gin.Context, r io.Reader) (*importResult, error) {
	// 与房源/账单导入同口径：统一走 readCSVRecords（BOM / GBK 编码 / 尾随空列容错）。
	records, err := readCSVRecords(r)
	if err != nil {
		return nil, err
	}
	header := records[0]
	if len(header) != len(tenantColumns) {
		return nil, errCSV(fmt.Sprintf(
			"表头与租客导入模板不一致：需要 %d 列，实际 %d 列。请下载最新模板后重试（列名与顺序需与模板一致）",
			len(tenantColumns), len(header)))
	}
	for i, col := range tenantColumns {
		if strings.TrimSpace(header[i]) != col {
			return nil, errCSV("表头与租客导入模板不一致，请下载最新模板后重试")
		}
	}

	result := &importResult{Errors: []importRowError{}}
	seen := map[string]int{} // 小区\x00房号\x00姓名 → 首次出现行号

	for i, rec := range records[1:] {
		lineNo := i + 2 // Excel 行号（含表头）
		if isBlankRow(rec) {
			continue
		}
		created, reason := importTenantRow(db, c, rec, seen, lineNo)
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

// importTenantRow 处理单行；返回 (是否新建租户, 错误原因)。
//
// 去重与合并规则与房源导入一致：
//   - 文件内「小区+房号+姓名」重复：第二次出现报错（避免同键两行互相覆盖）；
//   - 库内该房已有同名在租租户：按"非空覆盖、空值保留"合并更新（重复导入
//     幂等），计"更新"；
//   - 其余：新建在租租户，计"新建"。
//
// 缴费日/提醒天数留空 = 跟随全局默认（-1）；更新时空单元格保留原值。
func importTenantRow(db *gorm.DB, c *gin.Context, rec []string, seen map[string]int, lineNo int) (bool, string) {
	get := func(i int) string {
		if i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	community, roomNo, name := get(0), get(1), get(2)
	if name == "" {
		return false, "姓名不能为空"
	}
	if roomNo == "" {
		return false, "房号不能为空（请先在房源管理建档）"
	}

	key := community + "\x00" + roomNo + "\x00" + name
	if first, dup := seen[key]; dup {
		return false, fmt.Sprintf("与第 %d 行的小区+房号+姓名重复", first)
	}
	seen[key] = lineNo

	// v0.6.0：租客导入整体落在一个台账内；房源查找与租户写入都以它为界。
	writeLedger, okLedger := writeLedgerID(c)
	if !okLedger {
		return false, "请指定要操作的台账（ledger_id）"
	}
	// 房源必须已存在；不自动建房，避免从租客表带出缺租金缺底数的脏房源。
	// 按「小区+房号」在本台账内找，别台账的同键房源不会被误用。
	var room Room
	rq := db.Where("community = ? AND room_no = ?", community, roomNo)
	if ledgerColumnReady(db, "rooms") {
		rq = rq.Where("org_id = ?", writeLedger)
	}
	if err := rq.Order("id ASC").First(&room).Error; err != nil {
		return false, "房源不存在：" + community + " " + roomNo + "（请先用房源导入建档）"
	}

	// 日期列：YYYY-MM-DD，留空合法。
	for _, col := range []struct {
		idx   int
		label string
	}{{5, "入住日期"}, {6, "租约到期日"}} {
		if v := get(col.idx); v != "" {
			if _, err := time.Parse("2006-01-02", v); err != nil {
				return false, col.label + "格式应为 YYYY-MM-DD（如 2026-01-31）：" + v
			}
		}
	}

	// 数字列：押金与水费两个金额，非负数字，空 = 未填。
	deposit, depositSet, msg := parseRoomNumber("押金", get(7))
	if msg != "" {
		return false, msg
	}
	waterPrice, waterPriceSet, msg := parseRoomNumber("水费单价", get(9))
	if msg != "" {
		return false, msg
	}
	waterMonthlyFee, monthlyFeeSet, msg := parseRoomNumber("水费包月金额", get(10))
	if msg != "" {
		return false, msg
	}

	// 计费方式与缴费周期：留空 = 跟随全局默认；填了就必须能识别。
	if v := get(8); v != "" && parseWaterMode(v) == "" {
		return false, "水费模式应为 按吨 或 包月：" + v
	}
	if v := get(11); v != "" && parsePayCycle(v) == "" {
		return false, "缴费周期应为 月付 或 季付 或 半年付 或 年付：" + v
	}

	// 缴费日 0-28（0=不提醒）、提前提醒天数 0-30；留空 = -1（跟随全局，
	// CSV 里不需要也不能填 -1）。
	parseBounded := func(label string, raw string, lo, hi int) (int, bool, string) {
		if raw == "" {
			return tenantFollowGlobal, false, ""
		}
		v, err := strconv.Atoi(raw)
		if err != nil || v < lo || v > hi {
			return 0, false, fmt.Sprintf("%s应为 %d-%d 的数字（留空跟随全局）", label, lo, hi)
		}
		return v, true, ""
	}
	payDay, payDaySet, msg := parseBounded("缴费日", get(12), 0, payDayMax)
	if msg != "" {
		return false, msg
	}
	remindDays, remindSet, msg := parseBounded("提前提醒天数", get(13), 0, remindDaysMax)
	if msg != "" {
		return false, msg
	}

	var tenant Tenant
	tq := db.Where("room_id = ? AND active = ? AND name = ?", room.ID, true, name)
	if ledgerColumnReady(db, "tenants") {
		tq = tq.Where("org_id = ?", room.LedgerID)
	}
	err := tq.Order("id ASC").First(&tenant).Error
	isNew := err != nil
	if isNew {
		tenant = Tenant{
			UserID: currentUserID(c), // 录入人戳，仅溯源
			// 租户继承房源台账，与手工登记租户口径一致。
			LedgerID: room.LedgerID, RoomID: room.ID, Name: name, Active: true,
			PayDay: tenantFollowGlobal, RemindDays: tenantFollowGlobal,
		}
	}

	// 文本与数字按"非空覆盖"合并（新建时字段本就是零值，等价全量写入）。
	if v := get(3); v != "" {
		tenant.Phone = v
	}
	if v := get(4); v != "" {
		tenant.IDCard = v
	}
	if v := get(5); v != "" {
		tenant.MoveInDate = v
	}
	if v := get(6); v != "" {
		tenant.LeaseEndDate = v
	}
	tenant.Deposit = mergeRoomNumber(tenant.Deposit, deposit, depositSet, isNew)
	if v := parseWaterMode(get(8)); v != "" {
		tenant.WaterMode = v
	}
	tenant.WaterPrice = mergeRoomNumber(tenant.WaterPrice, waterPrice, waterPriceSet, isNew)
	tenant.WaterMonthlyFee = mergeRoomNumber(tenant.WaterMonthlyFee, waterMonthlyFee, monthlyFeeSet, isNew)
	if v := parsePayCycle(get(11)); v != "" {
		tenant.PayCycle = v
	}
	// 缴费日/提醒天数：填了值才写（0 也是合法显式值），留空更新时保留原值。
	if isNew || payDaySet {
		tenant.PayDay = payDay
	}
	if isNew || remindSet {
		tenant.RemindDays = remindDays
	}
	if v := get(14); v != "" {
		tenant.Notes = v
	}

	if isNew {
		if err := db.Create(&tenant).Error; err != nil {
			return false, "创建租户失败"
		}
	} else {
		if err := db.Save(&tenant).Error; err != nil {
			return false, "更新租户失败"
		}
	}
	return isNew, ""
}
