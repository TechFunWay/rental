// tenants.go — 租户 CRUD 与退租登记。
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

// tenantRequest 是租户登记/编辑的入参。
//
// 计费与缴费（v0.2.3 起从房源搬到租户）：water_mode / pay_cycle 留空表示
// 跟随全局默认；pay_day / remind_days 用指针区分"没传"与"显式传 0"
// （0 分别是"不提醒"与"当天提醒"），没传时存 -1（跟随全局默认）。
//
// ExcludedFees 是不参与计费的收费项目 key 列表（前端"收费项目勾选"里
// 未勾选的项）；nil/空 = 全部项目参与。IDCard 身份证号选填。
type tenantRequest struct {
	RoomID          uint     `json:"room_id" binding:"required"`
	Name            string   `json:"name" binding:"required"`
	Phone           string   `json:"phone"`
	IDCard          string   `json:"id_card"`
	MoveInDate      string   `json:"move_in_date"`
	LeaseEndDate    string   `json:"lease_end_date"` // 租约到期时间
	Deposit         float64  `json:"deposit"`        // 押金（元）
	WaterMode       string   `json:"water_mode"`
	WaterPrice      float64  `json:"water_price"`
	WaterMonthlyFee float64  `json:"water_monthly_fee"`
	PayCycle        string   `json:"pay_cycle"`
	PayDay          *int     `json:"pay_day"`
	RemindDays      *int     `json:"remind_days"`
	ExcludedFees    []string `json:"excluded_fees"`
	Notes           string   `json:"notes"`
}

// tenantRow 是租户列表的出参视图：附加所属房源的位置标签
// （RoomNo/RoomLabel 由代码补齐，非表字段），用于区分不同小区的同房号房源；
// ContractsCount 为该租户名下的合同文件数（一条 GROUP BY 批量补齐）。
type tenantRow struct {
	Tenant
	RoomNo         string `gorm:"-" json:"room_no"`
	RoomLabel      string `gorm:"-" json:"room_label"`
	ContractsCount int64  `gorm:"-" json:"contracts_count"`
}

func setupTenantRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/tenants", handleTenantList(db))
	api.POST("/rental/tenants", handleTenantCreate(db))
	api.PUT("/rental/tenants/:id", handleTenantUpdate(db))
	api.POST("/rental/tenants/:id/checkout", handleTenantCheckout(db))
	api.DELETE("/rental/tenants/:id", handleTenantDelete(db))
}

func handleTenantList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		keyword := strings.TrimSpace(c.Query("keyword"))
		active := c.Query("active") // "1" 在租 / "0" 已退租 / 空=全部
		roomID := utils.Atoi(c.Query("room_id"), 0)

		query := db.Model(&Tenant{}).Where("user_id = ?", currentUserID(c))
		if keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("name LIKE ? OR phone LIKE ?", like, like)
		}
		if active == "1" {
			query = query.Where("active = ?", true)
		} else if active == "0" {
			query = query.Where("active = ?", false)
		}
		if roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询租户失败")
			return
		}

		rows := make([]tenantRow, 0)
		if err := query.Order("active DESC, id DESC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询租户失败")
			return
		}
		// 房源位置批量补齐：避免 JOIN 自定义投影在不同后端下的映射差异。
		roomIDs := make([]uint, 0, len(rows))
		for _, r := range rows {
			roomIDs = append(roomIDs, r.RoomID)
		}
		roomNos := map[uint]string{}
		roomLabels := map[uint]string{}
		if len(roomIDs) > 0 {
			rooms := make([]Room, 0, len(roomIDs))
			db.Where("id IN ?", roomIDs).Find(&rooms)
			for _, r := range rooms {
				roomNos[r.ID] = r.RoomNo
				roomLabels[r.ID] = roomLocationLabel(r)
			}
		}
		// 合同数量批量补齐（一条 GROUP BY）。
		contractCounts := map[uint]int64{}
		tenantIDs := make([]uint, 0, len(rows))
		for _, r := range rows {
			tenantIDs = append(tenantIDs, r.ID)
		}
		if len(tenantIDs) > 0 {
			type countRow struct {
				TenantID uint
				Cnt      int64
			}
			counts := make([]countRow, 0, len(tenantIDs))
			db.Model(&Contract{}).Select("tenant_id, COUNT(*) as cnt").
				Where("tenant_id IN ?", tenantIDs).Group("tenant_id").Scan(&counts)
			for _, cr := range counts {
				contractCounts[cr.TenantID] = cr.Cnt
			}
		}
		for i := range rows {
			rows[i].RoomNo = roomNos[rows[i].RoomID]
			rows[i].RoomLabel = roomLabels[rows[i].RoomID]
			rows[i].ContractsCount = contractCounts[rows[i].ID]
		}
		response.SuccessPage(c, rows, total, page, pageSize)
	}
}

func handleTenantCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req tenantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写租户姓名并选择房源")
			return
		}
		req.Name = strings.TrimSpace(req.Name)

		// 校验请求体中的房源归属（room_id 在 body 中，不在 URL 参数里）。
		if _, ok := loadRoom(db, c, req.RoomID); !ok {
			return
		}
		if !validateTenantBillingRequest(c, &req) {
			return
		}

		tenant := Tenant{
			UserID:       currentUserID(c),
			RoomID:       req.RoomID,
			Name:         req.Name,
			Phone:        req.Phone,
			IDCard:       strings.TrimSpace(req.IDCard),
			MoveInDate:   req.MoveInDate,
			LeaseEndDate: req.LeaseEndDate,
			Deposit:      req.Deposit,
			Active:       true,
			Notes:        req.Notes,
		}
		applyTenantBilling(&tenant, &req)
		tenant.ExcludedFees = normalizeExcludedFeeKeys(req.ExcludedFees)
		if err := db.Create(&tenant).Error; err != nil {
			response.ErrorInternal(c, "创建租户失败")
			return
		}
		response.Success(c, tenant)
	}
}

func handleTenantUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		var req tenantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写租户姓名并选择房源")
			return
		}
		req.Name = strings.TrimSpace(req.Name)

		if _, ok := loadRoom(db, c, req.RoomID); !ok {
			return
		}
		if !validateTenantBillingRequest(c, &req) {
			return
		}

		tenant.RoomID = req.RoomID
		tenant.Name = req.Name
		tenant.Phone = req.Phone
		tenant.IDCard = strings.TrimSpace(req.IDCard)
		tenant.MoveInDate = req.MoveInDate
		tenant.LeaseEndDate = req.LeaseEndDate
		tenant.Deposit = req.Deposit
		tenant.Notes = req.Notes
		applyTenantBilling(tenant, &req)
		tenant.ExcludedFees = normalizeExcludedFeeKeys(req.ExcludedFees)
		// 已退租的租户编辑资料不自动恢复在租，恢复需另行处理。
		if err := db.Save(tenant).Error; err != nil {
			response.ErrorInternal(c, "保存租户失败")
			return
		}
		response.Success(c, tenant)
	}
}

// validateTenantBillingRequest 校验租户计费与缴费入参，失败时已写出 400 响应。
func validateTenantBillingRequest(c *gin.Context, req *tenantRequest) bool {
	payDay, remindDays := tenantFollowGlobal, tenantFollowGlobal
	if req.PayDay != nil {
		payDay = *req.PayDay
	}
	if req.RemindDays != nil {
		remindDays = *req.RemindDays
	}
	if msg := validateTenantBilling(req.WaterMode, req.WaterPrice, req.WaterMonthlyFee,
		req.PayCycle, payDay, remindDays); msg != "" {
		response.ErrorBadRequest(c, msg)
		return false
	}
	return true
}

// applyTenantBilling 把入参写进租户：计费方式与周期归一为规范值（留空=跟随
// 全局），缴费日/提醒天数没传时存 -1（跟随全局默认）。
func applyTenantBilling(tenant *Tenant, req *tenantRequest) {
	tenant.WaterMode = parseWaterMode(req.WaterMode)
	tenant.WaterPrice = req.WaterPrice
	tenant.WaterMonthlyFee = req.WaterMonthlyFee
	tenant.PayCycle = parsePayCycle(req.PayCycle)
	tenant.PayDay = tenantFollowGlobal
	if req.PayDay != nil {
		tenant.PayDay = *req.PayDay
	}
	tenant.RemindDays = tenantFollowGlobal
	if req.RemindDays != nil {
		tenant.RemindDays = *req.RemindDays
	}
}

// handleTenantCheckout 办理退租：记录退租日期并置为不在租。
func handleTenantCheckout(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		var req struct {
			MoveOutDate string `json:"move_out_date"`
		}
		_ = c.ShouldBindJSON(&req)

		moveOut := strings.TrimSpace(req.MoveOutDate)
		if moveOut == "" {
			moveOut = time.Now().Format("2006-01-02")
		}
		tenant.MoveOutDate = moveOut
		tenant.Active = false
		if err := db.Save(tenant).Error; err != nil {
			response.ErrorInternal(c, "退租登记失败")
			return
		}
		response.Success(c, tenant)
	}
}

func handleTenantDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		// 账单仅快照租户姓名，删除租户不影响历史账单；合同文件随之清理。
		if err := db.Delete(tenant).Error; err != nil {
			response.ErrorInternal(c, "删除租户失败")
			return
		}
		deleteTenantContracts(db, tenant.ID)
		response.Success(c, nil)
	}
}

func findUserTenant(db *gorm.DB, c *gin.Context) (*Tenant, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的租户 ID")
		return nil, false
	}
	var tenant Tenant
	if err := db.Where("id = ? AND user_id = ?", id, currentUserID(c)).First(&tenant).Error; err != nil {
		response.ErrorNotFound(c, "租户不存在")
		return nil, false
	}
	return &tenant, true
}

// roomLocationLabel 拼接房源完整位置：小区 楼栋 单元 N层 房号。
func roomLocationLabel(r Room) string {
	parts := make([]string, 0, 5)
	if r.Community != "" {
		parts = append(parts, r.Community)
	}
	if r.Building != "" {
		parts = append(parts, r.Building)
	}
	if r.Unit != "" {
		parts = append(parts, r.Unit)
	}
	if r.Floor != "" {
		parts = append(parts, r.Floor+"层")
	}
	parts = append(parts, r.RoomNo)
	return strings.Join(parts, " ")
}
