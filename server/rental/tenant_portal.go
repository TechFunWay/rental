// tenant_portal.go — 租户登录与只读门户（v0.6.10）。
//
// 背景：此前租客只是**被登记的数据**（tenants 表一行），看不到任何东西，一切靠
// 房东打印单据传达。本模块让租客用自己的账号登录，只读地看到**本人**的登记资料、
// 账单、缴费记录与合同，并通过「资料变更申请」改自己的联系与证件信息——改完必须
// 房东审核通过才覆盖正式数据。
//
// 角色与门禁的分层（详见 docs/07-租户登录与角色权限设计.md）：
//
//   - role=tenant 的账号**不是任何台账的成员**，因此台账 × 模块那套权限对它恒为空；
//   - 业务路由组整体包一层 denyTenantRole（默认拒绝），租户对全部业务接口 403——
//     这是「租户不能改任何数据」的兜底，将来新增业务路由忘检查也不会漏；
//   - 门户路由组包 requireTenantRole，只放行租户，且所有数据都按**当前账号绑定的
//     那条租户档案**收窄，**不接受客户端传入 tenant_id**（否则换个 id 就能看别人）。
//
// 租户账号的生命周期：注册（role=tenant, status=2, 无个人台账）→ 房东在租户管理里
// 「关联登录账号」→ status=1、tenants.account_user_id 写入 → 进门户。解绑则回退到
// status=2 的「等待关联」态，账号不删除，可再关联别的档案。
package rental

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/user"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TenantChangeRequest 租户提交的资料变更申请（待房东审核后才覆盖正式档案）。
//
// 为什么独立成表而不是给 tenants 加一堆「待审核字段」：
//   - 一张档案同时只允许一条待审申请，独立表用**部分唯一索引**表达这个约束更清楚；
//   - 申请要留历史（谁在什么时候申请改什么、房东批没批、为什么驳回），混进 tenants
//     会让正式档案被审核流水列污染；
//   - 驳回/撤回时要删掉租户本次上传的证件照，独立表能把「本次新增的附件」记成 JSON。
type TenantChangeRequest struct {
	ID       uint `gorm:"primarykey" json:"id"`
	LedgerID uint `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账（随租户档案）
	TenantID uint `gorm:"index;not null" json:"tenant_id"`
	// ApplicantUserID 提交人，即该租户档案绑定的登录账号。冗余一份是为了
	// 「我这边的申请列表」能按账号直查，不必每次 JOIN tenants。
	ApplicantUserID uint `gorm:"index;not null" json:"applicant_user_id"`
	// 申请修改的字段。**空串 = 本次不改这一项**（不是「改成空」）——这样租户
	// 只改手机号时不会把证件号一起清空。想显式清空由房东在租户编辑弹窗里操作。
	Phone            string `json:"phone"`
	IDCard           string `json:"id_card"`
	Address          string `json:"address"`
	EmergencyContact string `json:"emergency_contact"`
	EmergencyPhone   string `json:"emergency_phone"`
	// Note 是租户给房东的留言（说明为什么改）。
	Note string `json:"note"`
	// NewPhotos 本次新上传的证件照（相对 attachmentsDir 的路径 JSON 数组）。
	// 暂存在申请专属目录，**通过才**挪进该租户的正式附件，驳回/撤回即删除——
	// 未审核的证件照不该出现在房东看到的正式资料里。
	NewPhotos []string `gorm:"serializer:json" json:"-"`
	// Status：pending 待审核 / approved 已通过 / rejected 已驳回或租户撤回。
	Status     string     `gorm:"default:pending;index" json:"status"`
	ReviewNote string     `json:"review_note"`
	ReviewedBy uint       `json:"reviewed_by"` // 审核人（房东账号 id）
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func init() {
	database.RegisterModels(&TenantChangeRequest{})
}

// 变更申请状态。
const (
	ChangeStatusPending  = "pending"
	ChangeStatusApproved = "approved"
	ChangeStatusRejected = "rejected"
)

// changePhotoDir 申请照片的暂存子目录（相对 attachmentsDir，再按台账分一级）。
const changePhotoDir = "apply-change"

// changePhotoMax 单条申请最多几张照片（身份证正反面 + 其他证明，3 张足够）。
const changePhotoMax = 3

// changePhotoMaxSize 单张上限，与公开登记流同口径。
const changePhotoMaxSize = 10 << 20

// changeFieldMaxRunes 变更申请里短文本字段的字数上限（手机号/证件号/住址/联系人）。
const changeFieldMaxRunes = 60

// ---------------------------------------------------------------------------
// 角色判定与门禁
// ---------------------------------------------------------------------------

// userRole 读账号角色；查不到返回空串（调用方按「非常规角色」保守处理）。
//
// 注意：**请求链路里不要用它**——middleware.RequireAuth 每请求都会重新读一遍
// 用户行（认人时顺带校验 status 与 auth_version），并把 role 放进了 context。
// 门禁直接用 c.GetString("role") 即可，既权威又不多一次查询。本函数留给
// **没有请求上下文**的场景（如注册钩子、后台任务）。
func userRole(db *gorm.DB, userID uint) string {
	var role string
	if err := db.Table("users").Select("role").Where("id = ?", userID).Scan(&role).Error; err != nil {
		return ""
	}
	return role
}

// requireTenantRole 只放行租户角色（门户路由组用）。
//
// 为什么必须先判角色再判绑定：房东账号绑不绑定都进不了门户——门户的数据范围
// 是「本人档案」，与房东的台账权限是两套东西，混进来只会产生歧义入口。
func requireTenantRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 用 context 里的 role（RequireAuth 认人时读出来的，权威且省一次查询），
		// 不在这里再查一次库：SQLite MaxOpenConns=1，每请求多一条 SQL 是要还的。
		if c.GetString("role") != user.RoleTenant {
			response.ErrorForbidden(c, "该功能仅租户可用")
			c.Abort()
			return
		}
		c.Next()
	}
}

// denyTenantRole 对租户角色一律 403，包在**业务路由组**外层。
//
// 默认拒绝的口径：只要角色是租户，无论走哪个业务接口（含将来新增的）都拦下，
// 不必逐个 handler 记得判断。租户唯一的写入口是门户里的「资料变更申请」，
// 而那条链路不直接写正式数据（走审核）。
func denyTenantRole() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") == user.RoleTenant {
			response.ErrorForbidden(c, "租户账号只能查看自己的信息")
			c.Abort()
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// 档案绑定读取（门户数据范围的唯一来源）
// ---------------------------------------------------------------------------

// portalTenant 取当前登录账号**绑定的**租户档案；未绑定返回 false。
//
// 这是门户所有数据接口的唯一起点：数据范围永远由「当前 userID 绑定的档案」
// 推导，绝不接受客户端传入的 tenant_id / room_id（否则换个 id 就能看别人）。
func portalTenant(db *gorm.DB, userID uint) (*Tenant, bool) {
	if userID == 0 {
		return nil, false
	}
	var t Tenant
	if err := db.Where("account_user_id = ?", userID).First(&t).Error; err != nil {
		return nil, false
	}
	return &t, true
}

// portalTenantRequired 取绑定档案，取不到时已写出响应（403）。
//
// 走到这里说明账号角色是租户。取不到绑定只有一种到达路径：刚被房东解绑（未关联
// 的租户被框架待审核门禁挡在白名单外，到不了本函数）。统一按「还没有关联租户
// 档案」提示，让用户知道该去找房东，而不是看到莫名其妙的 403。
func portalTenantRequired(db *gorm.DB, c *gin.Context) (*Tenant, bool) {
	t, ok := portalTenant(db, c.GetUint("userID"))
	if !ok {
		response.Error(c, http.StatusForbidden, response.CodeForbidden,
			"你的账号还没有关联租户档案，请联系房东在「租户管理」里关联")
		return nil, false
	}
	return t, true
}

// ---------------------------------------------------------------------------
// 可见范围（越权防护的核心）
// ---------------------------------------------------------------------------

// applyPortalBillScope 把「我的账单」条件应用到查询上（账单与缴费记录共用）。
//
// 账单表没有 tenant_id（它是**按房按月**出的，宿舍一房多人共用一张账单，加
// tenant_id 会迫使每张账单选定一个「主租户」，与既有出账/收款/欠缴统计口径
// 冲突——见设计文档取舍一），所以可见范围用四重收窄：
//
//  1. org_id  = 档案所属台账   —— 跨台账数据一律不可见；
//  2. room_id = 档案所在房源   —— 只可能是本人住的房；
//  3. tenant_name 命中本人姓名（快照）—— 同房先后住过两个人时，只靠 room_id
//     会让新租客看到前任的账单。开票时会把在租租户姓名快照进 Bill.TenantName，
//     姓名字段**租户不可修改**，因此这个匹配长期稳定。
//     快照为空的历史账单放行：老数据没有快照，不能因此让租户看不到自己的账单。
//  4. 账期落在 [入住月, 退租月] 窗口内 —— 与 3 互补，堵住「姓名相同/为空」时
//     把入住前的账单漏给对方的情形。
//
// 宿舍一房多人的**联名账单**（TenantName 为「张三、李四」）会被每一位命中姓名的
// 租户看到——这正是期望行为：那张账单确实是他们共同的。
func applyPortalBillScope(q *gorm.DB, t *Tenant) *gorm.DB {
	q = q.Where("org_id = ? AND room_id = ?", t.LedgerID, t.RoomID)
	name := strings.TrimSpace(t.Name)
	if name != "" {
		q = q.Where("(tenant_name = '' OR tenant_name LIKE ?)", "%"+name+"%")
	}
	// 下界**一定**要有（见 portalBillFloor 的注释）：姓名快照为空的历史账单要放行，
	// 但「快照为空」+「入住日期没填」的组合会让同房前任的账单落到新租户眼里。
	if from := portalBillFloor(q, t, name); from != "" {
		q = q.Where("period >= ?", from)
	}
	if to := periodOfDate(t.MoveOutDate); to != "" {
		q = q.Where("period <= ?", to)
	}
	return q
}

// portalBillFloor 我的账单可见范围的**下界账期**，三级回退，保证一定有下界：
//
//  1. 入住日期所在月——房东填了就用它（最准）；
//  2. 本房最早一张「姓名快照命中我」的账单所在月——出账会把在租租户姓名快照进
//     账单，所以那张单子的账期不会早于我的入住月（房东没填日期时的客观证据）；
//  3. 租户档案的建立月——记录是什么时候录进来的，最后一道兜底。
//
// 为什么必须有下界：见 applyPortalBillScope 的说明。没有更准依据时**宁可少显示**
// （第 2、3 级），也不把前任的账单多显示给新租户；房东补齐「入住日期」即可恢复
// 精确的可见范围。
func portalBillFloor(q *gorm.DB, t *Tenant, name string) string {
	if from := periodOfDate(t.MoveInDate); from != "" {
		return from
	}
	if name != "" {
		var first string
		// Session(NewDB) 复用同一个连接但清掉已有的 Where，避免把「我已经加过的
		// 可见范围条件」带进这条只用于求下界的聚合查询。
		err := q.Session(&gorm.Session{NewDB: true}).Model(&Bill{}).
			Where("org_id = ? AND room_id = ? AND tenant_name LIKE ?", t.LedgerID, t.RoomID, "%"+name+"%").
			Select("MIN(period)").Scan(&first).Error
		if err == nil {
			if first = strings.TrimSpace(first); first != "" {
				return first
			}
		}
	}
	if !t.CreatedAt.IsZero() {
		return t.CreatedAt.Format("2006-01")
	}
	return ""
}

// portalBillQuery 我的账单查询（模型预设好，调用方继续加过滤/排序）。
func portalBillQuery(db *gorm.DB, t *Tenant) *gorm.DB {
	return applyPortalBillScope(db.Model(&Bill{}), t)
}

// periodOfDate 把 YYYY-MM-DD 转成账单账期 YYYY-MM；解析失败返回空串（不设边界）。
func periodOfDate(date string) string {
	date = strings.TrimSpace(date)
	if len(date) < 10 {
		return ""
	}
	parsed, err := time.Parse("2006-01-02", date[:10])
	if err != nil {
		return ""
	}
	return parsed.Format("2006-01")
}

// portalContractQuery 我的合同：合同表有 tenant_id，直接精确命中（同台账防孤儿）。
func portalContractQuery(db *gorm.DB, t *Tenant) *gorm.DB {
	return db.Model(&Contract{}).Where("org_id = ? AND tenant_id = ?", t.LedgerID, t.ID)
}

// portalAttachmentQuery 我的资料附件：同样按 tenant_id 精确命中。
func portalAttachmentQuery(db *gorm.DB, t *Tenant) *gorm.DB {
	return db.Model(&TenantAttachment{}).Where("org_id = ? AND tenant_id = ?", t.LedgerID, t.ID)
}

// ---------------------------------------------------------------------------
// 门户路由
// ---------------------------------------------------------------------------

// setupPortalRoutes 挂载租户门户：只读接口 + 变更申请。全部经 requireTenantRole。
//
// 挂载位置：authGroup（已登录）之内、业务路由组**之外**——业务组被 denyTenantRole
// 包着会拦下租户，门户必须走另一条枝。
func setupPortalRoutes(api *gin.RouterGroup, db *gorm.DB) {
	g := api.Group("/rental/portal", requireTenantRole())

	// 门户状态：未关联时唯一能用的接口（在 pendingWhitelist 里）。
	g.GET("/status", handlePortalStatus(db))

	g.GET("/profile", handlePortalProfile(db))

	g.GET("/bills", handlePortalBillList(db))
	g.GET("/bills/:id", handlePortalBillDetail(db))
	g.GET("/bills/:id/receipt", handlePortalBillReceipt(db))

	g.GET("/payments", handlePortalPaymentList(db))

	g.GET("/contracts", handlePortalContractList(db))
	g.GET("/contracts/:id/file", handlePortalContractFile(db))

	g.GET("/attachments/:id/file", handlePortalAttachmentFile(db))

	g.GET("/change-requests", handlePortalChangeList(db))
	g.POST("/change-requests", handlePortalChangeSubmit(db))
	g.DELETE("/change-requests/:id", handlePortalChangeWithdraw(db))
	g.GET("/change-requests/:id/photo/:idx", handlePortalChangePhoto(db))
}

// ---------------------------------------------------------------------------
// 门户状态
// ---------------------------------------------------------------------------

// handlePortalStatus 租户门户状态：角色、是否已关联、档案与台账名、账号状态。
//
// 未关联（status=2）的租户只能调这一个接口——它在 pendingWhitelist 里，用来渲染
// 「等待房东关联」页；门户其余接口都不在白名单，因此未关联时拿不到任何数据。
func handlePortalStatus(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		out := gin.H{
			"role":     user.RoleTenant,
			"bound":    false,
			"status":   userStatus(db, userID),
			"username": c.GetString("username"),
		}
		if t, ok := portalTenant(db, userID); ok {
			out["bound"] = true
			out["tenant_name"] = t.Name
			out["ledger_name"] = ledgerNameOf(db, t.LedgerID)
			out["room_label"] = portalRoomLabel(db, t)
		}
		response.Success(c, out)
	}
}

// ledgerNameOf 取台账显示名；查不到返回空串（个人台账显示成「我的台账」）。
func ledgerNameOf(db *gorm.DB, ledgerID uint) string {
	var l Ledger
	if err := db.First(&l, ledgerID).Error; err != nil {
		return ""
	}
	if l.Kind == LedgerKindPersonal {
		return PersonalLedgerName
	}
	return l.Name
}

// portalRoomLabel 档案所在房源的位置标签（小区 楼栋 单元 N层 房号）。
func portalRoomLabel(db *gorm.DB, t *Tenant) string {
	var room Room
	if err := db.Where("id = ? AND org_id = ?", t.RoomID, t.LedgerID).First(&room).Error; err != nil {
		return ""
	}
	return roomLocationLabel(room)
}

// ---------------------------------------------------------------------------
// 我的登记资料
// ---------------------------------------------------------------------------

// portalProfileView 门户「我的资料」出参。
//
// 刻意**只回租户本人该看到的字段**：不含台账 id、录入人、备注、排除收费项这些
// 内部字段。计费摘要对租户是必要信息——他要据此核对账单，所以保留 billing_summary
// 而不是把整个 Tenant 结构丢出去。
type portalProfileView struct {
	Name             string  `json:"name"`
	Phone            string  `json:"phone"`
	IDCard           string  `json:"id_card"`
	Address          string  `json:"address"`
	EmergencyContact string  `json:"emergency_contact"`
	EmergencyPhone   string  `json:"emergency_phone"`
	RoomLabel        string  `json:"room_label"`
	Community        string  `json:"community"`
	MoveInDate       string  `json:"move_in_date"`
	LeaseEndDate     string  `json:"lease_end_date"`
	MoveOutDate      string  `json:"move_out_date"`
	Active           bool    `json:"active"`
	Deposit          float64 `json:"deposit"`
	// BillingSummary 计费摘要（水费方式、单价、缴费周期），账单核对用。
	BillingSummary string `json:"billing_summary"`
	// PendingChange 是否有待审核的变更申请（前端据此显示状态条）。
	PendingChange bool `json:"pending_change"`
}

func handlePortalProfile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		var room Room
		db.Where("id = ? AND org_id = ?", t.RoomID, t.LedgerID).First(&room)

		view := portalProfileView{
			Name:             t.Name,
			Phone:            t.Phone,
			IDCard:           maskIDCard(t.IDCard),
			Address:          t.Address,
			EmergencyContact: t.EmergencyContact,
			EmergencyPhone:   t.EmergencyPhone,
			RoomLabel:        roomLocationLabel(room),
			Community:        room.Community,
			MoveInDate:       t.MoveInDate,
			LeaseEndDate:     t.LeaseEndDate,
			MoveOutDate:      t.MoveOutDate,
			Active:           t.Active,
			Deposit:          t.Deposit,
			BillingSummary:   portalBillingSummary(db, &room),
		}
		var pending int64
		db.Model(&TenantChangeRequest{}).
			Where("tenant_id = ? AND status = ?", t.ID, ChangeStatusPending).Count(&pending)
		view.PendingChange = pending > 0

		// 我的资料附件（身份证照片等），只回元数据，文件流走
		// /portal/attachments/:id/file（按本人档案鉴权）。
		items := make([]TenantAttachment, 0, 8)
		portalAttachmentQuery(db, t).Order("created_at ASC, id ASC").Find(&items)

		response.Success(c, gin.H{"profile": view, "attachments": items})
	}
}

// portalBillingSummary 该房租户生效的计费摘要（复用房东侧的解析链）。
//
// 复用 effectiveRoomBilling 而不是自己读租户字段：那条链已经处理了「租户显式
// 设置 → 台账覆盖 → 系统默认」的回退，另写一份迟早分叉，导致租户看到的计费口径
// 与账单不一致。
func portalBillingSummary(db *gorm.DB, room *Room) string {
	if room == nil || room.ID == 0 {
		return ""
	}
	cfg := effectiveRoomBilling(db, room)
	parts := []string{waterModeLabel(cfg.WaterMode) + " " + formatAmount(cfg.WaterAmount)}
	parts = append(parts, cycleLabel(cfg.PayCycle))
	if cfg.PayDay >= 1 && cfg.PayDay <= 28 {
		parts = append(parts, fmt.Sprintf("每月 %d 号收", cfg.PayDay))
	}
	return strings.Join(parts, " · ")
}

// formatAmount 金额展示：两位小数（计费摘要用，与前端展示口径一致）。
func formatAmount(v float64) string {
	return strconv.FormatFloat(round2(v), 'f', 2, 64)
}

// maskIDCard 证件号脱敏：只露前 6 后 4（18 位），中间打码。
//
// 为什么门户要脱敏而不是原样返回：这是「查看自己的信息」，本可不脱敏；但门户
// 页面常被租户在他人面前打开（前台、宿舍），证件号是可用来办业务的敏感项，
// 前端展示脱敏值就够核对。要改证件号走变更申请，不需要回显完整原值。
func maskIDCard(id string) string {
	id = strings.TrimSpace(id)
	r := []rune(id)
	if len(r) <= 10 {
		return id
	}
	return string(r[:6]) + strings.Repeat("*", len(r)-10) + string(r[len(r)-4:])
}

// ---------------------------------------------------------------------------
// 我的账单
// ---------------------------------------------------------------------------

func handlePortalBillList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		q := portalBillQuery(db, t)
		if period := strings.TrimSpace(c.Query("period")); period != "" {
			q = q.Where("period = ?", period)
		}
		var total int64
		if err := q.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询账单失败")
			return
		}
		rows := make([]Bill, 0, pageSize)
		if err := q.Order("period DESC, id DESC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询账单失败")
			return
		}
		response.SuccessPage(c, rows, total, page, pageSize)
	}
}

// portalBill 取本人可见范围内的账单；越权/不存在一律 404（不泄漏存在性）。
func portalBill(db *gorm.DB, c *gin.Context, t *Tenant) (*Bill, bool) {
	id, parsed := parseLedgerID(c.Param("id"))
	if !parsed || id == 0 {
		response.ErrorNotFound(c, "账单不存在")
		return nil, false
	}
	var bill Bill
	if err := portalBillQuery(db, t).Where("id = ?", id).First(&bill).Error; err != nil {
		response.ErrorNotFound(c, "账单不存在")
		return nil, false
	}
	return &bill, true
}

func handlePortalBillDetail(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		bill, ok := portalBill(db, c, t)
		if !ok {
			return
		}
		response.Success(c, portalBillDetailPayload(db, t, bill))
	}
}

// handlePortalBillReceipt 我的单据数据：与账单详情同结构（含收费项与收款分摊、
// 单据抬头），让租户能预览/打印与自己账单一致的单据。
func handlePortalBillReceipt(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		bill, ok := portalBill(db, c, t)
		if !ok {
			return
		}
		response.Success(c, portalBillDetailPayload(db, t, bill))
	}
}

// portalBillDetailPayload 账单详情投影：明细行带已缴与欠缴、收款流水、单据抬头。
//
// 之所以另写一份而不复用房东侧 handleBillDetail：那个 handler 依赖请求上下文里的
// 可见台账（scopeLedger / inLedgerScope），而租户不是任何台账的成员、上下文里
// 没有台账范围。这里改成显式按**已校验过的本人档案**收窄，语义更直白，也避免
// 为了复用去伪造 context 里的台账。
func portalBillDetailPayload(db *gorm.DB, t *Tenant, bill *Bill) gin.H {
	items := make([]BillItem, 0)
	db.Where("bill_id = ? AND org_id = ?", bill.ID, t.LedgerID).Order("id ASC").Find(&items)

	payments := make([]Payment, 0)
	db.Where("bill_id = ? AND org_id = ?", bill.ID, t.LedgerID).
		Order("paid_at DESC, id DESC").Find(&payments)

	// 已缴金额按收费项分摊（收款流水的 items JSON），与房东侧口径一致。
	paids := map[string]float64{}
	for _, p := range payments {
		for _, share := range paymentItems(p.Items) {
			paids[share.Key] = round2(paids[share.Key] + share.Amount)
		}
	}
	// 单据「不单列」的项目：与房东侧同一口径（按账单所属台账读收费项目设置），
	// 否则租户在门户里打印出来的单据会与房东打印的不一致。
	hidden := excludedFeeSet(hiddenReceiptFeeKeys(db, t.LedgerID))
	itemRows := make([]gin.H, 0, len(items))
	for _, it := range items {
		itemRows = append(itemRows, gin.H{
			"key": it.FeeKey, "name": it.Name, "kind": it.Kind,
			"amount": it.Amount, "detail": it.Detail, "cycle": it.Cycle,
			"paid":    paids[it.FeeKey],
			"arrears": round2(it.Amount - paids[it.FeeKey]),
			// 单据不单列该项目（只在收费单据上生效）
			"receipt_hidden": hidden[it.FeeKey],
		})
	}
	payRows := make([]gin.H, 0, len(payments))
	for _, p := range payments {
		payRows = append(payRows, gin.H{
			"id": p.ID, "paid_at": p.PaidAt, "amount": p.Amount,
			"note": p.Note, "items": paymentItems(p.Items),
		})
	}
	name, contact, note := receiptHeaderFor(db, t.LedgerID)
	return gin.H{
		"bill":     bill,
		"items":    itemRows,
		"payments": payRows,
		"property": gin.H{"name": name, "contact": contact, "note": note},
	}
}

// ---------------------------------------------------------------------------
// 我的缴费记录
// ---------------------------------------------------------------------------

func handlePortalPaymentList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))

		// 只回「可见账单」的收款：用子查询把范围下推到 SQL，分页前就把范围算准，
		// 不把账单拉回内存（账单可能上千张）。
		visibleBills := applyPortalBillScope(db.Model(&Bill{}).Select("id"), t)
		q := db.Model(&Payment{}).
			Where("org_id = ? AND bill_id IN (?)", t.LedgerID, visibleBills)

		var total int64
		if err := q.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询缴费记录失败")
			return
		}
		rows := make([]Payment, 0, pageSize)
		if err := q.Order("paid_at DESC, id DESC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询缴费记录失败")
			return
		}
		response.SuccessPage(c, rows, total, page, pageSize)
	}
}

// ---------------------------------------------------------------------------
// 我的合同 / 我的附件文件流
// ---------------------------------------------------------------------------

func handlePortalContractList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		items := make([]Contract, 0, 8)
		if err := portalContractQuery(db, t).Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询合同失败")
			return
		}
		response.Success(c, items)
	}
}

func handlePortalContractFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		id, parsed := parseLedgerID(c.Param("id"))
		if !parsed || id == 0 {
			response.ErrorNotFound(c, "合同不存在")
			return
		}
		var contract Contract
		if err := portalContractQuery(db, t).Where("id = ?", id).First(&contract).Error; err != nil {
			response.ErrorNotFound(c, "合同不存在")
			return
		}
		abs, resolved := contractResolve(contract.LedgerID, contract.FilePath)
		if !resolved || !fileExists(abs) {
			response.ErrorNotFound(c, "合同文件不存在")
			return
		}
		c.Header("Content-Disposition", contractDisposition(c, contract.FileName, c.Query("download") == "1"))
		c.File(abs)
	}
}

func handlePortalAttachmentFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		id, parsed := parseLedgerID(c.Param("id"))
		if !parsed || id == 0 {
			response.ErrorNotFound(c, "附件不存在")
			return
		}
		var item TenantAttachment
		if err := portalAttachmentQuery(db, t).Where("id = ?", id).First(&item).Error; err != nil {
			response.ErrorNotFound(c, "附件不存在")
			return
		}
		abs, resolved := attachmentResolve(item.LedgerID, item.FilePath)
		if !resolved || !fileExists(abs) {
			response.ErrorNotFound(c, "附件不存在")
			return
		}
		c.File(abs)
	}
}
