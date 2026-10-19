// tenant_export.go — 租户批量导出 CSV（派出所场景：提供「当前住在这片的人是谁」）。
//
// 两条与别处不同的口径，都在这里定死（设计见 docs/14-租户导出-物品台账-结转设计.md
// 第一节）：
//
//  1. **列 = 租客导入模板 tenantColumns 的 15 列原样 + 末尾 4 列只读信息**
//     （房号、在租状态、出生日期、性别）。追加列必须放在**末尾**：CSV 导入按
//     列数截断，既有 15 列文件与「导出→改→再导入」的回导能力因此不受影响。
//     派出所真正要的身份证号本就在前 15 列里，不需要另立一套专用模板
//     ——专用模板会与导入模板分叉，日后改模板要改两处、用户手里有两个格式。
//
//  2. **筛选条件与 handleTenantList 逐条一致**（keyword / active / room_id /
//     community / tag），做到「列表页看到什么，导出就是什么」——这是最容易
//     产生争议的地方。改列表筛选时必须同步改这里（反之亦然）。
//
// 权限与范围与租户列表同档：租户模块**查看**档（导出是纯只读操作，不该要 full），
// 数据范围按 visibleLedgerIDs(c)，跨台账只见自己可见的台账。
package rental

import (
	"strconv"
	"strings"
	"time"

	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// tenantExportExtraColumns 是 15 列导入模板之后追加的只读列。
//
// 列名刻意不重复：早先这里放了一个重复的「房号」（与第 2 列同名），派出所拿到
// 两张同名表头的 CSV 没法直接导入 Excel 做透视（表头重名）。房号本来就在前
// 15 列里，追加列不再重复它。
//
// 「年龄」由出生日期现算（用户明确要「出生日期、年龄、性别」三项）：年龄是随
// 时间变的派生值，必须按导出当天算，不能存成固定字段。
//
// 导入按列数截断（见 importTenantsCSV 的长度校验），追加列变化不影响回导。
var tenantExportExtraColumns = [...]string{"在租状态", "出生日期", "年龄", "性别"}

// tenantExportMaxRows 单次导出的上限，与账单/房源导出一致（防超大库把内存拉爆）。
const tenantExportMaxRows = 10000

// tenantExportColumns 导出的完整表头：导入模板 15 列 + 末尾追加列。
func tenantExportColumns() []string {
	cols := make([]string, 0, len(tenantColumns)+len(tenantExportExtraColumns))
	cols = append(cols, tenantColumns[:]...)
	cols = append(cols, tenantExportExtraColumns[:]...)
	return cols
}

// handleTenantsExport 导出租户 CSV（表头 + 匹配筛选条件的租户行）。
//
// 空结果也输出表头：派出所拿到的空表要能看出查询条件（列名就是口径）。
func handleTenantsExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 以下 5 个筛选参数与 handleTenantList（tenants.go）逐条对齐。
		keyword := strings.TrimSpace(c.Query("keyword"))
		active := c.Query("active") // "1" 在租 / "0" 已退租 / 空=全部
		roomID := utils.Atoi(c.Query("room_id"), 0)
		community := strings.TrimSpace(c.Query("community"))
		tag := strings.TrimSpace(c.Query("tag"))

		rows := [][]string{tenantExportColumns()}

		// v0.6.0：只导本次请求可见台账内的租户；没有可见台账时只剩表头。
		query, empty := scopeLedger(db.Model(&Tenant{}), c)
		if empty {
			writeCSV(c, utils.ExportFilename("rental_tenants", "csv"), rows)
			return
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("name LIKE ? OR phone LIKE ? OR tags LIKE ?", like, like, like)
		}
		if tag != "" {
			// 标签存的是 JSON 数组，按 "标签" 带引号精确命中，避免「优质」命中
			// 「优质长租」的子串误配（与列表同一处理）。
			query = query.Where("tags LIKE ?", "%\""+tag+"\"%")
		}
		if active == "1" {
			query = query.Where("active = ?", true)
		} else if active == "0" {
			query = query.Where("active = ?", false)
		}
		if roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		if community != "" {
			// 按小区/村组筛租户：房源限定在可见台账内，避免跨台账同名小区串数据。
			query = query.Where("room_id IN (SELECT id FROM rooms WHERE community = ? AND org_id IN ?)",
				community, visibleLedgerIDs(c))
		}

		tenants := make([]Tenant, 0)
		// 与列表同序（在租在前、新登记的在前），导出的行序与用户刚才看到的列表一致。
		if err := query.Order("active DESC, id DESC").Limit(tenantExportMaxRows).Find(&tenants).Error; err != nil {
			response.ErrorInternal(c, "导出失败")
			return
		}

		rooms := tenantExportRooms(db, c, tenants)
		for i := range tenants {
			t := &tenants[i]
			room := rooms[t.RoomID]
			cfg := effectiveTenantBilling(db, t)
			rows = append(rows, tenantExportRow(t, room, cfg))
		}
		writeCSV(c, utils.ExportFilename("rental_tenants", "csv"), rows)
	}
}

// tenantExportRow 拼一行导出数据。
//
// 前 15 列与导入模板一一对应（留空 = 跟随全局默认，回导时语义不变）；
// 后 4 列是派出所要的只读信息，回导时会被按列数截断。
func tenantExportRow(t *Tenant, room Room, cfg billingConfig) []string {
	// 水费金额随最终计费方式只填匹配的那一列（与 handleRoomsExport 同口径：
	// 包月模式填「包月金额」，按吨模式填「单价」，另一列留空）。
	waterPrice, waterMonthlyFee := formatPrice(cfg.WaterAmount), ""
	if cfg.WaterMode == waterModeMonthly {
		waterPrice, waterMonthlyFee = "", formatPrice(cfg.WaterAmount)
	}
	// 缴费日 0 表示不提醒（导出为空，与 handleRoomsExport 一致）。
	payDay := ""
	if cfg.PayDay > 0 {
		payDay = strconv.Itoa(cfg.PayDay)
	}
	// 出生日期与性别由身份证号派生：复用住户成员那套 deriveFromIDCard，
	// 18 位才派生、15 位老号段不猜（见 tenant_members.go）。
	birthday, gender := deriveFromIDCard(strings.TrimSpace(t.IDCard))
	activeLabel := "已退租"
	if t.Active {
		activeLabel = "在租"
	}
	return []string{
		room.Community, room.RoomNo, t.Name, t.Phone, t.IDCard,
		t.MoveInDate, t.LeaseEndDate, formatPrice(t.Deposit),
		waterModeLabel(cfg.WaterMode), waterPrice, waterMonthlyFee,
		cycleLabel(cfg.PayCycle), payDay, strconv.Itoa(cfg.RemindDays), t.Notes,
		activeLabel, birthday, tenantExportAgeLabel(birthday), tenantExportGenderLabel(gender),
	}
}

// tenantExportAgeLabel 由出生日期（YYYY-MM-DD）算周岁，算不出留空。
//
// 生日当天不算长一岁（周岁口径）。出生日期为空（15 位老号段或未填证件号）时
// 返回空——宁可留空，也不拿零值编一个年龄出来。
func tenantExportAgeLabel(birthday string) string {
	if birthday == "" {
		return ""
	}
	b, err := time.ParseInLocation("2006-01-02", birthday, time.Local)
	if err != nil {
		return ""
	}
	now := time.Now()
	age := now.Year() - b.Year()
	// 今年生日还没到 → 减一岁。
	if now.Month() < b.Month() || (now.Month() == b.Month() && now.Day() < b.Day()) {
		age--
	}
	if age < 0 || age > 150 {
		return "" // 明显不合理的出生日期不输出年龄
	}
	return strconv.Itoa(age)
}

// tenantExportGenderLabel 性别展示：male→男 / female→女 / 空→空。
// 与前端 utils/id-card.ts 的 genderText 同口径（那里空值给「性别未填」，CSV 里留空
// 更干净，导入按空处理）。
func tenantExportGenderLabel(gender string) string {
	switch gender {
	case "male":
		return "男"
	case "female":
		return "女"
	default:
		return ""
	}
}

// tenantExportRooms 批量取导出行涉及的房源（一次 IN 查询，避免逐行查）。
// 房源同样限定可见台账：列表里的租户只可能在本台账的房里，这里是为了不让
// 跨台账的同名 ID 脏数据串出别人的房号（与 handleTenantList 同一处理）。
func tenantExportRooms(db *gorm.DB, c *gin.Context, tenants []Tenant) map[uint]Room {
	out := make(map[uint]Room, len(tenants))
	ids := make([]uint, 0, len(tenants))
	seen := make(map[uint]bool, len(tenants))
	for _, t := range tenants {
		if t.RoomID == 0 || seen[t.RoomID] {
			continue
		}
		seen[t.RoomID] = true
		ids = append(ids, t.RoomID)
	}
	if len(ids) == 0 {
		return out
	}
	rooms := make([]Room, 0, len(ids))
	rq, _ := scopeLedger(db.Where("id IN ?", ids), c)
	rq.Find(&rooms)
	for _, r := range rooms {
		out[r.ID] = r
	}
	return out
}

// effectiveTenantBilling 解析**单个租户**生效的计费与缴费设置：
// 租户显式设置 → 台账覆盖 → 系统级 → 代码默认。
//
// 回退链与 effectiveRoomBilling（billing.go）完全一致，只是起点从「房间的
// 在租租户」换成「本行租户本人」——导出一行一个租户，宿舍场景同房多名租户
// 各写各的设置；这也正是租户列表 tenantBillingSummary 的展示口径
// （前端 resolveTenantBilling 同一条链），保证「列表看到什么导出就是什么」。
// 单租户房间下与本房的 effectiveRoomBilling 结果相同。
func effectiveTenantBilling(db *gorm.DB, t *Tenant) billingConfig {
	ledgerID := t.LedgerID

	// 水费计费方式：租户显式设置 → 台账/系统默认（空值与未知值一律按吨）。
	mode := parseWaterMode(t.WaterMode)
	if mode == "" {
		mode = normalizeWaterMode(ledgerConfigString(db, "rental_water_mode", ledgerID, waterModeMeter))
	}
	// 金额随最终方式取：该方式下租户显式金额 → 台账/全局默认金额。
	amount := tenantWaterAmount(*t, mode)
	if amount <= 0 {
		amount = globalWaterAmount(db, mode, ledgerID)
	}

	// 缴费周期：租户显式设置 → 台账/系统默认。
	cycle := parsePayCycle(t.PayCycle)
	if cycle == "" {
		cycle = normalizePayCycle(ledgerConfigString(db, "rental_pay_cycle", ledgerID, payCycleMonthly))
	}

	// 缴费日 / 提前提醒天数：0 及正数是显式设置（0=不提醒 / 当天提醒），
	// -1（tenantFollowGlobal）才回退台账/系统默认。
	payDay := t.PayDay
	if payDay < 0 {
		payDay = ledgerConfigInt(db, "rental_pay_day", ledgerID, 0)
	}
	remind := t.RemindDays
	if remind < 0 {
		remind = ledgerConfigInt(db, "rental_remind_days", ledgerID, defaultRemindDays)
	}

	return billingConfig{
		WaterMode:   mode,
		WaterAmount: amount,
		PayCycle:    cycle,
		PayDay:      clampPayDay(payDay),
		RemindDays:  clampRemindDays(remind),
	}
}
