// rooms.go — 房源 CRUD 与删除保护。
package rental

import (
	"strconv"
	"strings"

	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type tenantBrief struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Phone string `json:"phone"`
}

// roomView 是房源的出参视图：附加当前在租租户、是否已有账单与最近一张
// 账单的账期（前端据此判断季付房本月是否应出账），以及该房当前生效的
// 计费与缴费设置（来自在租租户 → 全局默认，见 billing.go）。
type roomView struct {
	Room
	CurrentTenants []tenantBrief `json:"current_tenants"`
	HasBills       bool          `json:"has_bills"`
	LastBillPeriod string        `json:"last_bill_period"`
	Billing        billingView   `json:"billing"`
	// PhotoCount 房源照片张数（列表页显示入口，详情弹窗里再按房间取全量）。
	PhotoCount int64 `gorm:"-" json:"photo_count"`
}

// billingView 是房间生效计费设置的出参形态。两个"该方式下的金额"都给出，
// 便于前端在账单里临时切换计费方式时按配置金额预填（与 waterAmountForMode 同口径）。
// ExcludedFees 是该房生效的"不参与计费项目"（租户勾选），前端据此在
// 开票/编辑界面禁用对应项目并按 0 预估费用。
type billingView struct {
	WaterMode       string   `json:"water_mode"`        // meter 按吨 / monthly 包月
	WaterAmount     float64  `json:"water_amount"`      // 当前生效方式下的金额：按吨=元/吨；包月=元/月
	WaterMeterPrice float64  `json:"water_meter_price"` // 按吨口径的金额（租户 → 全局默认）
	WaterMonthlyFee float64  `json:"water_monthly_fee"` // 包月口径的金额（租户 → 全局默认）
	PayCycle        string   `json:"pay_cycle"`         // monthly 月付 / quarterly 季付
	PayDay          int      `json:"pay_day"`           // 1-28；0=不提醒
	RemindDays      int      `json:"remind_days"`       // 0-30
	ExcludedFees    []string `json:"excluded_fees"`     // 不参与计费的项目 key（空=全部参与）
}

type roomRequest struct {
	Community            string   `json:"community"` // 小区（单小区可留空）
	RoomNo               string   `json:"room_no" binding:"required"`
	Building             string   `json:"building"`
	Unit                 string   `json:"unit"`
	Floor                string   `json:"floor"`
	DefaultRent          float64  `json:"default_rent"`
	DefaultSanitationFee float64  `json:"default_sanitation_fee"`
	DefaultManagementFee float64  `json:"default_management_fee"`
	ElecPrice            float64  `json:"elec_price"`
	GasPrice             float64  `json:"gas_price"`
	InitialWater         float64  `json:"initial_water"`
	InitialElec          float64  `json:"initial_elec"`
	InitialGas           float64  `json:"initial_gas"`
	Tags                 []string `json:"tags"`
	Notes                string   `json:"notes"`
	// 房东姓名与联系电话（选填）
	LandlordName  string `json:"landlord_name"`
	LandlordPhone string `json:"landlord_phone"`
	// 介绍费（见 referral.go）：由 normalizeReferral 按平台策略归一化后落库
	ReferralMode  string  `json:"referral_mode"`
	ReferralValue float64 `json:"referral_value"`
}

func setupRoomRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 读用并集口径（任一台账能看即可进页面，数据范围由 handler 按 ledger 收窄）；
	// 写必须用单台账口径（本次请求指定的那个台账要达到档位），否则在 A 台账的
	// full 会被并集带进 B 台账的写判定，等于跨台账越权。
	view := requireModule(db, ModuleRooms, PermView)
	edit := requireLedgerModule(db, ModuleRooms, PermEdit)
	full := requireLedgerModule(db, ModuleRooms, PermFull)
	api.GET("/rental/rooms", view, handleRoomList(db))
	api.POST("/rental/rooms", edit, handleRoomCreate(db))
	api.PUT("/rental/rooms/:id", edit, handleRoomUpdate(db))
	api.DELETE("/rental/rooms/:id", full, handleRoomDelete(db))
}

func currentUserID(c *gin.Context) uint {
	return c.GetUint("userID")
}

func handleRoomList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		keyword := strings.TrimSpace(c.Query("keyword"))
		status := c.Query("status")
		community := strings.TrimSpace(c.Query("community"))
		building := strings.TrimSpace(c.Query("building"))

		// v0.6.0：只列本次请求可见台账内的房源。
		query, empty := scopeRoomQuery(db.Model(&Room{}), c)
		if empty {
			response.SuccessPage(c, []roomView{}, 0, page, pageSize)
			return
		}
		if keyword != "" {
			like := "%" + keyword + "%"
			// 关键字同时匹配位置信息（房号/小区/楼栋/单元）、标签与该房在租租户姓名。
			// 子查询里的 tenants 也必须限定同一批台账：否则房号能命中别的台账
			// 的在租租户名，从而把本台账的房「漏」出去（列表口径不一致）。
			query = query.Where(
				"room_no LIKE ? OR community LIKE ? OR building LIKE ? OR unit LIKE ? OR tags LIKE ? OR id IN (SELECT room_id FROM tenants WHERE active = ? AND name LIKE ? AND org_id IN ?)",
				like, like, like, like, like, true, like, visibleLedgerIDs(c))
		}
		if tag := strings.TrimSpace(c.Query("tag")); tag != "" {
			// 标签筛选：tags 存的是 JSON 数组，按 "标签" 带引号精确命中，
			// 避免「精装」命中「精装修」的子串误配
			query = query.Where("tags LIKE ?", "%\""+tag+"\"%")
		}
		if community != "" {
			query = query.Where("community = ?", community)
		}
		if building != "" {
			query = query.Where("building = ?", building)
		}
		if status == "occupied" || status == "vacant" {
			op := "IN"
			if status == "vacant" {
				op = "NOT IN"
			}
			query = query.Where("id "+op+" (SELECT room_id FROM tenants WHERE active = ? AND org_id IN ?)",
				true, visibleLedgerIDs(c))
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		rooms := make([]Room, 0)
		if err := query.Order("room_no ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&rooms).Error; err != nil {
			response.ErrorInternal(c, "查询房源失败")
			return
		}

		items := make([]roomView, 0, len(rooms))
		for _, r := range rooms {
			items = append(items, buildRoomView(db, r))
		}
		fillRoomPhotoCounts(db, c, items)
		response.SuccessPage(c, items, total, page, pageSize)
	}
}

// fillRoomPhotoCounts 一次分组查询补上本页每间房的照片张数。
// 放在列表层而不是 buildRoomView 里：后者是逐房间调用（还要查租户/账单/计费），
// 再按房间 count 一次就是典型的 N+1；列表页只需要「有没有照片」这个提示。
func fillRoomPhotoCounts(db *gorm.DB, c *gin.Context, items []roomView) {
	if len(items) == 0 {
		return
	}
	roomIDs := make([]uint, 0, len(items))
	for i := range items {
		roomIDs = append(roomIDs, items[i].ID)
	}
	type photoCount struct {
		RoomID uint
		N      int64
	}
	counts := make([]photoCount, 0)
	pq, _ := scopeLedger(db.Model(&RoomPhoto{}), c)
	pq.Select("room_id, count(*) AS n").Where("room_id IN ?", roomIDs).
		Group("room_id").Scan(&counts)
	byRoom := map[uint]int64{}
	for _, pc := range counts {
		byRoom[pc.RoomID] = pc.N
	}
	for i := range items {
		items[i].PhotoCount = byRoom[items[i].ID]
	}
}

func buildRoomView(db *gorm.DB, r Room) roomView {
	view := roomView{Room: r, CurrentTenants: []tenantBrief{}}
	// 当前在租租户限定在同台账：跨台账的孤儿租户不能出现在本台账房源的详情里。
	tq := db.Model(&Tenant{}).Where("room_id = ? AND active = ?", r.ID, true)
	if ledgerColumnReady(db, "tenants") {
		tq = tq.Where("org_id = ?", r.LedgerID)
	}
	tq.Order("id ASC").Limit(50).Find(&view.CurrentTenants)
	// 生效计费设置：在租租户 → 全局默认（前端房源列表、抄表预览与账单编辑共用）。
	// 两个方式各自的金额都给出来，前端切计费方式时按配置金额预填。
	cfg := effectiveRoomBilling(db, &r)
	view.Billing = billingView{
		WaterMode:       cfg.WaterMode,
		WaterAmount:     cfg.WaterAmount,
		WaterMeterPrice: waterAmountForMode(db, &r, waterModeMeter),
		WaterMonthlyFee: waterAmountForMode(db, &r, waterModeMonthly),
		PayCycle:        cfg.PayCycle,
		PayDay:          cfg.PayDay,
		RemindDays:      cfg.RemindDays,
		ExcludedFees:    effectiveRoomExcludedFees(db, &r),
	}
	var bills int64
	db.Model(&Bill{}).Where("room_id = ?", r.ID).Count(&bills)
	view.HasBills = bills > 0
	if view.HasBills {
		db.Model(&Bill{}).Where("room_id = ?", r.ID).
			Order("period DESC").Limit(1).Pluck("period", &view.LastBillPeriod)
	}
	return view
}

func handleRoomCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req roomRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "房号不能为空")
			return
		}
		if msg := validateRoomRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		referralMode, referralValue, refMsg := normalizeReferral(db, req.ReferralMode, req.ReferralValue, req.DefaultRent)
		if refMsg != "" {
			response.ErrorBadRequest(c, refMsg)
			return
		}
		req.RoomNo = strings.TrimSpace(req.RoomNo)

		// 写入目标台账：由 resolveLedger 依 ?ledger_id 解析（未解析到则写路由的
		// requireLedgerModule 已挡下）。取不到时明确报错，不猜、不落到 0 号台账。
		writeLedger, ok := writeLedgerID(c)
		if !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			return
		}

		// 共享后 (小区, 房号) 在同一台账内唯一，先查重再落库（唯一索引由迁移
		// 管理，不能只依赖约束报错）。查重必须限定本次写入的台账：别的台账
		// 有同名房号不该挡住建档。
		var dup int64
		db.Model(&Room{}).Where("community = ? AND room_no = ? AND org_id = ?",
			strings.TrimSpace(req.Community), req.RoomNo, writeLedger).Count(&dup)
		if dup > 0 {
			response.ErrorBadRequest(c, "房号已存在")
			return
		}

		// UserID 是「录入人」戳（谁创建的房源），只作溯源；数据归属看 LedgerID。
		// 介绍费按平台策略归一化（off 时忽略、min 时校验下限），再统一落库
		req.ReferralMode, req.ReferralValue = referralMode, referralValue
		room := Room{
			UserID: currentUserID(c), LedgerID: writeLedger,
			Community: strings.TrimSpace(req.Community), RoomNo: req.RoomNo,
		}
		applyRoomRequest(&room, &req)
		if err := db.Create(&room).Error; err != nil {
			if isDuplicateKey(err) {
				response.ErrorBadRequest(c, "房号已存在")
				return
			}
			response.ErrorInternal(c, "创建房源失败")
			return
		}
		response.Success(c, room)
	}
}

func handleRoomUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		// 编辑只允许写入本次请求指定的台账：?ledger_id 有值时必须与房源台账一致，
		// 否则「改着 A 台账的房，写请求带的是 B 台账」会绕过台账边界。
		if writeLedger, ok := writeLedgerID(c); !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			return
		} else if room.LedgerID != writeLedger {
			response.ErrorNotFound(c, "房源不存在")
			return
		}
		var req roomRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "房号不能为空")
			return
		}
		if msg := validateRoomRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		referralMode, referralValue, refMsg := normalizeReferral(db, req.ReferralMode, req.ReferralValue, req.DefaultRent)
		if refMsg != "" {
			response.ErrorBadRequest(c, refMsg)
			return
		}
		req.ReferralMode, req.ReferralValue = referralMode, referralValue
		req.RoomNo = strings.TrimSpace(req.RoomNo)

		room.Community = strings.TrimSpace(req.Community)
		room.RoomNo = req.RoomNo
		applyRoomRequest(room, &req)
		if err := db.Save(room).Error; err != nil {
			if isDuplicateKey(err) {
				response.ErrorBadRequest(c, "房号已存在")
				return
			}
			response.ErrorInternal(c, "保存房源失败")
			return
		}
		response.Success(c, room)
	}
}

// handleRoomDelete 删除房源。存在账单时拒绝（保护台账完整性），
// 存在在租租户时提示先退租。
func handleRoomDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermFull) {
			return
		}
		var bills int64
		// 账单按 room_id 统计：room_id 全局唯一，跨台账孤儿行不可能挂到
		// 本房源的 id 上，因此不需要再按台账收窄（写权限已在上面按房源
		// 所属台账判过）。
		db.Model(&Bill{}).Where("room_id = ?", room.ID).Count(&bills)
		if bills > 0 {
			response.ErrorBadRequest(c, "该房源存在账单记录，不能删除；如需清理请先删除相关账单")
			return
		}
		var tenants int64
		tq := db.Model(&Tenant{}).Where("room_id = ? AND active = ?", room.ID, true)
		if ledgerColumnReady(db, "tenants") {
			tq = tq.Where("org_id = ?", room.LedgerID)
		}
		tq.Count(&tenants)
		if tenants > 0 {
			response.ErrorBadRequest(c, "该房源仍有在租租户，请先办理退租")
			return
		}
		if err := db.Delete(room).Error; err != nil {
			response.ErrorInternal(c, "删除房源失败")
			return
		}
		// 房源删除时顺带清理其抄表照片：照片按「房间 + 账期」归属，房间没了就
		// 再也看不到、也删不掉，只留在盘上就是孤儿文件。
		deleteRoomMeterPhotos(db, room.ID, room.LedgerID)
		// 房源照片同理（按房间归属）。
		deleteRoomPhotos(db, room.ID, room.LedgerID)
		// 房源删除时顺带清理其历史租户登记（账单不存在才会走到这里）；
		// 同样限定为本房源的所属台账。
		del := db.Where("room_id = ?", room.ID)
		if ledgerColumnReady(db, "tenants") {
			del = del.Where("org_id = ?", room.LedgerID)
		}
		// 历史租户的合同与资料附件也要跟着走：房源删除只允许在没有账单、
		// 没有在租租户时发生，剩下的都是已退租的历史租户，其合同/身份证照片
		// 若只删租户行就会变成孤儿文件（界面上再也看不到、也删不掉）。
		var histTenantIDs []uint
		hq := db.Model(&Tenant{}).Where("room_id = ?", room.ID)
		if ledgerColumnReady(db, "tenants") {
			hq = hq.Where("org_id = ?", room.LedgerID)
		}
		hq.Pluck("id", &histTenantIDs)
		for _, tid := range histTenantIDs {
			deleteTenantContracts(db, tid, room.LedgerID)
			deleteTenantAttachments(db, tid, room.LedgerID)
			deleteTenantMembers(db, tid, room.LedgerID)
		}
		del.Delete(&Tenant{})
		response.Success(c, nil)
	}
}

// validateRoomRequest 校验房源入参，返回空串表示通过。
// 水费计费方式与缴费周期自 v0.2.3 起在租户上设置（见 tenants.go），
// 房源只校验默认租金/费用与抄表底数不能为负。
func validateRoomRequest(req *roomRequest) string {
	if req.DefaultRent < 0 || req.DefaultSanitationFee < 0 || req.DefaultManagementFee < 0 {
		return "默认租金与费用不能为负数"
	}
	if req.ElecPrice < 0 || req.GasPrice < 0 {
		return "电费/燃气单价不能为负数"
	}
	if req.InitialWater < 0 || req.InitialElec < 0 || req.InitialGas < 0 {
		return "抄表底数不能为负数"
	}
	// 房东姓名与电话都是选填，但填了就要像样：姓名不超 30 字、电话走与登记链接
	// 同一套宽松规则（允许座机与分隔符），避免存进打不通的号码。
	if len([]rune(strings.TrimSpace(req.LandlordName))) > 30 {
		return "房东姓名不能超过 30 个字"
	}
	if phone := strings.TrimSpace(req.LandlordPhone); phone != "" && !applyPhoneRe.MatchString(phone) {
		return "房东联系电话格式不正确"
	}
	return ""
}

func applyRoomRequest(room *Room, req *roomRequest) {
	// 介绍费由调用方 normalizeReferral 归一化后塞进 req（见 handleRoomCreate/Update），
	// 所以这里直接落库；未归一化时 req 里是原值，由调用方保证。
	room.ReferralMode = req.ReferralMode
	room.ReferralValue = req.ReferralValue
	room.Building = req.Building
	room.Unit = req.Unit
	room.Floor = req.Floor
	room.DefaultRent = req.DefaultRent
	room.DefaultSanitationFee = req.DefaultSanitationFee
	room.DefaultManagementFee = req.DefaultManagementFee
	room.ElecPrice = req.ElecPrice
	room.GasPrice = req.GasPrice
	room.InitialWater = req.InitialWater
	room.InitialElec = req.InitialElec
	room.InitialGas = req.InitialGas
	room.Tags = normalizeTags(req.Tags)
	room.LandlordName = strings.TrimSpace(req.LandlordName)
	room.LandlordPhone = strings.TrimSpace(req.LandlordPhone)
	room.Notes = req.Notes
	// 房源上的水费/缴费字段是 0.2.2 及以前的遗留列，此处刻意不动：
	// 编辑房源不会覆盖或清掉老数据，迁移已把有效值搬到租户上。
}

func findUserRoom(db *gorm.DB, c *gin.Context) (*Room, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的房源 ID")
		return nil, false
	}
	var room Room
	// 数据共享后按 ID 直取，不再限定录入人；但必须校验台账归属，
	// 否则换个 ID 就能读到别的台账的房源。
	if err := db.First(&room, id).Error; err != nil {
		response.ErrorNotFound(c, "房源不存在")
		return nil, false
	}
	if !inLedgerScope(c, room.LedgerID) {
		response.ErrorNotFound(c, "房源不存在")
		return nil, false
	}
	return &room, true
}

// isDuplicateKey 识别 SQLite 唯一约束冲突（UNIQUE constraint failed）。
func isDuplicateKey(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}
