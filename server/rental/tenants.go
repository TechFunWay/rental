// tenants.go — 租户 CRUD 与退租登记。
package rental

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"smallgo/server/logger"
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
	RoomID uint   `json:"room_id" binding:"required"`
	Name   string `json:"name" binding:"required"`
	Phone  string `json:"phone"`
	IDCard string `json:"id_card"`
	// Address / EmergencyContact / EmergencyPhone 是 v0.6.10 补上的租户档案字段：
	// 住址原先只存在于登记申请（转入登记后丢失），紧急联系人由租户在门户里申请、
	// 房东审核后写入。房东在登记/编辑弹窗里也直接可填可改（保留最终决定权）。
	Address          string   `json:"address"`
	EmergencyContact string   `json:"emergency_contact"`
	EmergencyPhone   string   `json:"emergency_phone"`
	MoveInDate       string   `json:"move_in_date"`
	LeaseEndDate     string   `json:"lease_end_date"` // 租约到期时间
	Deposit          float64  `json:"deposit"`        // 押金（元）
	WaterMode        string   `json:"water_mode"`
	WaterPrice       float64  `json:"water_price"`
	WaterMonthlyFee  float64  `json:"water_monthly_fee"`
	PayCycle         string   `json:"pay_cycle"`
	PayDay           *int     `json:"pay_day"`
	RemindDays       *int     `json:"remind_days"`
	ExcludedFees     []string `json:"excluded_fees"`
	Tags             []string `json:"tags"`
	// ApplicationID 选填：由「租客自助登记」转入登记时带上，租户创建成功后
	// 服务端把申请标记为已入库并转移身份证照片到资料附件。
	ApplicationID uint   `json:"application_id"`
	Notes         string `json:"notes"`
	// Members 住户成员（选填）：登记/编辑租户表单里「入住成员」那一列，与租户
	// **一起保存**（一个事务）。带 id 的改、不带 id 的新增、数组里没有的删除；
	// 字段不出现时不动成员（老客户端与接口调用方不受影响）。
	Members []memberRequest `json:"members"`
}

// tenantRow 是租户列表的出参视图：附加所属房源的位置标签
// （RoomNo/RoomLabel 由代码补齐，非表字段），用于区分不同小区的同房号房源；
// ContractsCount / AttachmentsCount 为该租户名下的合同与资料附件数量
// （各一条 GROUP BY 批量补齐）。
type tenantRow struct {
	Tenant
	RoomNo           string `gorm:"-" json:"room_no"`
	RoomLabel        string `gorm:"-" json:"room_label"`
	ContractsCount   int64  `gorm:"-" json:"contracts_count"`
	AttachmentsCount int64  `gorm:"-" json:"attachments_count"`
	// MembersCount 该租户名下**在住**的住户成员数（已搬离的不计），列表页据此
	// 显示「成员 n」入口；成员弹窗里才完整列出（含已搬离）。
	MembersCount int64 `gorm:"-" json:"members_count"`
	// DepositPhotosCount 该租户名下「押金单」附件的张数。押金单是实物凭证，
	// 列表与房源详情据此显示押金单入口（标签约定见 attachments.go）。
	DepositPhotosCount int64 `gorm:"-" json:"deposit_photos_count"`
}

func setupTenantRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 读并集、写单台账（详见 rooms.go 的同名说明）。
	view := requireModule(db, ModuleTenants, PermView)
	edit := requireLedgerModule(db, ModuleTenants, PermEdit)
	full := requireLedgerModule(db, ModuleTenants, PermFull)
	api.GET("/rental/tenants", view, handleTenantList(db))
	api.POST("/rental/tenants", edit, handleTenantCreate(db))
	api.PUT("/rental/tenants/:id", edit, handleTenantUpdate(db))
	api.POST("/rental/tenants/:id/checkout", edit, handleTenantCheckout(db))
	api.DELETE("/rental/tenants/:id", full, handleTenantDelete(db))
	// 粘贴识别（外网不通时的录入路径，见 tenant_parse.go）：解析不写库、只读档；
	// 批量入库会整批写库，按 CSV 导入同口径的单台账完全档。
	api.POST("/rental/tenants/parse-text", view, handleTenantTextParse())
	api.POST("/rental/tenants/batch", requireLedgerModule(db, ModuleTenants, PermFull), handleTenantsBatch(db))
}

func handleTenantList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		keyword := strings.TrimSpace(c.Query("keyword"))
		active := c.Query("active") // "1" 在租 / "0" 已退租 / 空=全部
		roomID := utils.Atoi(c.Query("room_id"), 0)
		community := strings.TrimSpace(c.Query("community"))

		// v0.6.0：只列本次请求可见台账内的租户。
		query, empty := scopeLedger(db.Model(&Tenant{}), c)
		if empty {
			response.SuccessPage(c, []tenantRow{}, 0, page, pageSize)
			return
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			query = query.Where("name LIKE ? OR phone LIKE ? OR tags LIKE ?", like, like, like)
		}
		if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
			// 标签筛选：tags 存的是 JSON 数组，按 "标签" 带引号精确命中，
			// 避免「优质」命中「优质长租」的子串误配
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
			// 按小区/村组筛租户：房源限定在可见台账内，避免跨台账同名小区串数据
			query = query.Where("room_id IN (SELECT id FROM rooms WHERE community = ? AND org_id IN ?)",
				community, visibleLedgerIDs(c))
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
			// 房源同样限定可见台账：列表里的租户只可能在本台账的房里，
			// 这里是为了不让跨台账的同名 ID 脏数据串出别人的房号。
			rq, _ := scopeLedger(db.Where("id IN ?", roomIDs), c)
			rq.Find(&rooms)
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
			cq, _ := scopeLedger(db.Model(&Contract{}), c)
			cq.Select("tenant_id, COUNT(*) as cnt").
				Where("tenant_id IN ?", tenantIDs).Group("tenant_id").Scan(&counts)
			for _, cr := range counts {
				contractCounts[cr.TenantID] = cr.Cnt
			}
		}
		// 资料附件数量批量补齐（同样一条 GROUP BY）。
		attachmentCounts := map[uint]int64{}
		if len(tenantIDs) > 0 {
			type countRow struct {
				TenantID uint
				Cnt      int64
			}
			counts := make([]countRow, 0, len(tenantIDs))
			aq, _ := scopeLedger(db.Model(&TenantAttachment{}), c)
			aq.Select("tenant_id, COUNT(*) as cnt").
				Where("tenant_id IN ?", tenantIDs).Group("tenant_id").Scan(&counts)
			for _, cr := range counts {
				attachmentCounts[cr.TenantID] = cr.Cnt
			}
		}
		// 在住住户成员数批量补齐（同样一条 GROUP BY；已搬离的不计）。
		memberCounts := map[uint]int64{}
		if len(tenantIDs) > 0 {
			type countRow struct {
				TenantID uint
				Cnt      int64
			}
			counts := make([]countRow, 0, len(tenantIDs))
			mq, _ := scopeLedger(db.Model(&TenantMember{}), c)
			mq.Select("tenant_id, COUNT(*) as cnt").
				Where("tenant_id IN ? AND active = ?", tenantIDs, true).
				Group("tenant_id").Scan(&counts)
			for _, cr := range counts {
				memberCounts[cr.TenantID] = cr.Cnt
			}
		}
		// 押金单照片张数：单独一条 GROUP BY，只数 label=押金单 的附件。
		// 与上面的「资料附件总数」分开，押金单入口要显示的是自己的张数。
		depositPhotoCounts := map[uint]int64{}
		if len(tenantIDs) > 0 {
			type countRow struct {
				TenantID uint
				Cnt      int64
			}
			counts := make([]countRow, 0, len(tenantIDs))
			dpq, _ := scopeLedger(db.Model(&TenantAttachment{}), c)
			dpq.Select("tenant_id, COUNT(*) as cnt").
				Where("tenant_id IN ? AND label = ?", tenantIDs, attachmentLabelDeposit).
				Group("tenant_id").Scan(&counts)
			for _, cr := range counts {
				depositPhotoCounts[cr.TenantID] = cr.Cnt
			}
		}
		for i := range rows {
			rows[i].RoomNo = roomNos[rows[i].RoomID]
			rows[i].RoomLabel = roomLabels[rows[i].RoomID]
			rows[i].ContractsCount = contractCounts[rows[i].ID]
			rows[i].AttachmentsCount = attachmentCounts[rows[i].ID]
			rows[i].MembersCount = memberCounts[rows[i].ID]
			rows[i].DepositPhotosCount = depositPhotoCounts[rows[i].ID]
		}
		response.SuccessPage(c, rows, total, page, pageSize)
	}
}

// applyTenantDerived 从身份证号派生出生日期与性别（issue #5 需求 1）。
//
// 复用入住成员那套 deriveFromIDCard（tenant_members.go），保证主档案与成员
// 口径完全一致：只认 18 位号，解析不出日期就整体留空，15 位老号段不猜。
//
// 身份证号变化即重算；清空证件号时派生字段一并清空——避免「删了证件号、
// 生日性别还挂着」的残留，那种数据会误导导出与派出所核对。
func applyTenantDerived(t *Tenant) {
	t.Birthday, t.Gender = deriveFromIDCard(strings.TrimSpace(t.IDCard))
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
		room, ok := loadRoom(db, c, req.RoomID)
		if !ok {
			return
		}
		// 写入判定按**房源所属台账**：租户的台账跟随房源，若只看「本次请求
		// 选中的台账」，不带 ?ledger_id 时会被回落到个人台账（恒完全权限），
		// 从而往自己只有查看权的台账里塞数据。
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if !validateTenantBillingRequest(c, &req) {
			return
		}
		if msg := validateMemberPayload(req.Members); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}

		tenant := Tenant{
			// UserID 录入人戳，仅溯源；数据归属看 LedgerID。
			UserID: currentUserID(c),
			// 子记录继承父记录的台账：租户必须落在其房源所属台账里，
			// 否则用构造出的 room_id 就能在别的台账里造孤儿租户。
			LedgerID:         room.LedgerID,
			RoomID:           req.RoomID,
			Name:             req.Name,
			Phone:            req.Phone,
			IDCard:           strings.TrimSpace(req.IDCard),
			Address:          strings.TrimSpace(req.Address),
			EmergencyContact: strings.TrimSpace(req.EmergencyContact),
			EmergencyPhone:   strings.TrimSpace(req.EmergencyPhone),
			MoveInDate:       req.MoveInDate,
			LeaseEndDate:     req.LeaseEndDate,
			Deposit:          req.Deposit,
			Active:           true,
			Notes:            req.Notes,
		}
		applyTenantBilling(&tenant, &req)
		tenant.ExcludedFees = normalizeExcludedFeeKeys(req.ExcludedFees)
		tenant.Tags = normalizeTags(req.Tags)
		// 身份证号派生出生日期/性别（issue #5 需求 1）：快照落库，
		// 供租户列表、导出租户信息（派出所用）直接展示。
		applyTenantDerived(&tenant)
		// 租户与「入住成员」一个事务落库：任一步失败就整体回滚，
		// 不会出现「租户建好了、成员没进去」这种半成品档案。
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&tenant).Error; err != nil {
				return err
			}
			if req.Members == nil {
				return nil
			}
			return applyMemberPayload(tx, tenant.LedgerID, tenant.ID, tenant.UserID, req.Members)
		}); err != nil {
			var bad memberPayloadError
			if errors.As(err, &bad) {
				response.ErrorBadRequest(c, bad.msg)
				return
			}
			response.ErrorInternal(c, "创建租户失败")
			return
		}
		// 租客自助登记转入：申请标记已入库，身份证照片挪进该租户附件。
		// **必须校验申请与租户同台账**：申请 id 是全局递增的，若只按
		// id + pending 取，任何一个账号都能在自己台账建租户时带上别台账的
		// application_id，把对方的身份证照片搬进自己的租户附件（2026-10-11
		// 审计复现）。台账不符时按不存在处理，不提示对方台账的存在。
		if req.ApplicationID > 0 {
			var app TenantApplication
			err := db.Where("id = ? AND status = ?", req.ApplicationID, "pending").First(&app).Error
			if err == nil && app.LedgerID == tenant.LedgerID {
				consumeApplication(db, &app, &tenant)
			}
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
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		var req tenantRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写租户姓名并选择房源")
			return
		}
		req.Name = strings.TrimSpace(req.Name)

		room, ok := loadRoom(db, c, req.RoomID)
		if !ok {
			return
		}
		// 换房只允许在同一台账内：此前实现让租户的台账跟随新房间，于是
		// 「在 A、B 两个台账都是成员」的人能把 A 的租户挪到 B 的房间，而 A 里
		// 已开的账单、抄表记录仍留在 A，两边对不上（2026-10-11 审计）。
		if room.LedgerID != tenant.LedgerID {
			response.ErrorBadRequest(c, "只能把租户换到同一台账的房源")
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if !validateTenantBillingRequest(c, &req) {
			return
		}

		tenant.RoomID = req.RoomID
		tenant.Name = req.Name
		tenant.Phone = req.Phone
		tenant.IDCard = strings.TrimSpace(req.IDCard)
		tenant.Address = strings.TrimSpace(req.Address)
		tenant.EmergencyContact = strings.TrimSpace(req.EmergencyContact)
		tenant.EmergencyPhone = strings.TrimSpace(req.EmergencyPhone)
		tenant.MoveInDate = req.MoveInDate
		tenant.LeaseEndDate = req.LeaseEndDate
		tenant.Deposit = req.Deposit
		tenant.Notes = req.Notes
		applyTenantBilling(tenant, &req)
		tenant.ExcludedFees = normalizeExcludedFeeKeys(req.ExcludedFees)
		tenant.Tags = normalizeTags(req.Tags)
		// 改证件号即重算派生字段；清空证件号则一并清空（见 applyTenantDerived）。
		applyTenantDerived(tenant)
		if msg := validateMemberPayload(req.Members); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		// 已退租的租户编辑资料不自动恢复在租，恢复需另行处理。
		// 成员与租户同一事务：表单里删掉的行在这里一并删除（表单即真相）。
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Save(tenant).Error; err != nil {
				return err
			}
			if req.Members == nil {
				return nil
			}
			return applyMemberPayload(tx, tenant.LedgerID, tenant.ID, tenant.UserID, req.Members)
		}); err != nil {
			var bad memberPayloadError
			if errors.As(err, &bad) {
				response.ErrorBadRequest(c, bad.msg)
				return
			}
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
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
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
		// 名下住户成员一并收尾（置为已搬离、补搬离日期，行保留供历史查询）。
		checkoutTenantMembers(db, tenant.ID, tenant.LedgerID, moveOut)
		response.Success(c, tenant)
	}
}

func handleTenantDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermFull) {
			return
		}
		// v0.6.10：档案被删除时，把它绑定的登录账号退回「等待关联」态
		// （账号不删）——否则账号会卡在「已关联」但档案已消失，租户登录后
		// 门户一片空白也说不清原因。解绑失败不阻断删除（房东的删除意图优先），
		// 账号下次尝试进门户时 portalTenant 取不到档案，同样会被引导去找房东。
		if err := releaseTenantAccount(db, tenant); err != nil {
			logger.Error("rental: 删除租户时解绑登录账号失败 tenant=%d: %v", tenant.ID, err)
		}
		// 待审的变更申请随档案一起清掉（档案都没了，审核无意义）；
		// 历史记录一并删，避免留下指向不存在档案的孤儿申请。
		db.Where("tenant_id = ? AND org_id = ?", tenant.ID, tenant.LedgerID).
			Delete(&TenantChangeRequest{})
		// 账单仅快照租户姓名，删除租户不影响历史账单；合同文件与资料附件随之清理。
		if err := db.Delete(tenant).Error; err != nil {
			response.ErrorInternal(c, "删除租户失败")
			return
		}
		deleteTenantContracts(db, tenant.ID, tenant.LedgerID)
		deleteTenantAttachments(db, tenant.ID, tenant.LedgerID)
		// 住户成员随档案一起删除（成员挂在租户下，档案没了就没有归属可言）。
		deleteTenantMembers(db, tenant.ID, tenant.LedgerID)
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
	// 数据共享后按 ID 直取，不再限定录入人；但必须校验台账归属，
	// 否则换个 ID 就能读到别台账的租户档案。
	if err := db.First(&tenant, id).Error; err != nil {
		response.ErrorNotFound(c, "租户不存在")
		return nil, false
	}
	if !inLedgerScope(c, tenant.LedgerID) {
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
