// ledger_api.go — 台账 API：我的台账、共同台账的创建/改名/删除、成员与邀请、
// 按台账的租房设置，以及注册审批（待审核用户的通过/驳回）。
//
// 模型（0.5.2 起，参照兄弟应用「账单」的「个人 + 共享账本」形态）：
//
//	GET    /rental/ledgers/mine            我的台账（含待接受的邀请）
//	POST   /rental/ledgers                 新建共同台账（可同时邀请若干用户名）
//	PUT    /rental/ledgers/:id             改名（仅管理者）
//	DELETE /rental/ledgers/:id             删除（仅管理者；个人台账不可删、有数据不可删）
//	GET    /rental/ledgers/:id/members     成员列表（正式成员可见；管理者看到待接受/已拒绝）
//	POST   /rental/ledgers/:id/members     邀请成员（仅管理者）
//	PUT    /rental/ledgers/:id/members/:uid 设置某成员的模块权限（仅管理者）
//	DELETE /rental/ledgers/:id/members/:uid 移除成员（仅管理者）/ 自己退出（成员）
//	POST   /rental/ledgers/:id/invitations/respond 接受或拒绝邀请（被邀请人本人）
//	GET    /rental/users/search?q=         邀请时按用户名找人
//	GET/PUT/DELETE /rental/ledgers/:id/settings 按台账的租房设置（读=成员，写=管理者）
//
// 管理后台（框架 adminGroup）只剩账号相关：待审核列表、通过、驳回。
// **超管在业务数据上没有特权**：不是某台账的成员就看不到、改不了它的数据。
//
// 注册审批（status==2）的放行名单由 requiresApprovalPath 决定，默认拒绝。
package rental

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"smallgo/server/audit"
	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/sysconfig"
	"smallgo/server/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// 注册审批（status==2 待审核）常量与状态
// ---------------------------------------------------------------------------

// 用户状态：框架 database.User.Status 的取值。1/0 由框架定义（1=正常、0=禁用），
// 2 是 v0.6.0 引入的「待审核」——注册后默认落 2，需要管理员审批才转 1。
const (
	UserStatusDisabled int = 0 // 已禁用（驳回/停用）
	UserStatusActive   int = 1 // 正常
	UserStatusPending  int = 2 // 待审核：可登录，但除白名单外一律 403
)

// isAdminRole 是否为超级管理员。
func isAdminRole(role string) bool { return role == "admin" }

// userStatusName 用户状态的中文名（提示文案用）。
func userStatusName(status int) string {
	switch status {
	case UserStatusActive:
		return "正常"
	case UserStatusDisabled:
		return "已禁用"
	case UserStatusPending:
		return "待审核"
	}
	return fmt.Sprintf("未知(%d)", status)
}

// ---------------------------------------------------------------------------
// 待审核用户白名单：pendingWhitelist
// ---------------------------------------------------------------------------

// pendingWhitelist 是「待审核用户仍可访问」的路径前缀白名单（按 /api 之后的
// 路径比较）。默认拒绝：不在这里的一律 403，所以以后新增的接口自动受保护，
// 只有显式列进来的才放行。
//
// 每一条都有必要：
//
//	/api/auth/            登录、登出、查当前身份、改自己密码。待审核用户必须能
//	                      登录，否则前端连「您在等待审核」都拿不到。
//	/api/configs          站点标题/描述等公开配置 + 自己主题偏好，首屏要用。
//	/api/user/profile     个人资料，自读自改。
//	/api/version          前端版本号/启动时间。
//	/api/realtime         SSE 长连接（否则前端 EventSource 不停重连）。
//	/api/rental/ledgers/mine 自己的台账列表：待审核用户要看到「还没有共同台账」
//	                      这个事实；他的个人台账在注册时就建好了。
//	/api/rental/users/me/approval 自己读自己的审核状态。
//	/api/rental/portal/status v0.6.10：租户门户状态。未关联的租户（status=2）
//	                      唯一能读的接口，用来渲染「等待房东关联」页。
//	/api/health           存活探针。
//
// 明确**不**放行：任何业务数据接口（/api/rental/rooms 等）、**门户其余接口**
// （/api/rental/portal/profile 等——未关联就不该看到任何数据）、成员与邀请接口、
// /api/users、/api/backups、/api/audit、/api/upgrade-history。
var pendingWhitelist = []string{
	"/api/auth/",
	"/api/configs",
	"/api/user/profile",
	"/api/version",
	"/api/realtime",
	"/api/rental/ledgers/mine",
	"/api/rental/users/me/approval",
	// 房东入驻申请（v0.6.10）：待审核房东在等待审核期间**必须**能提交/更新自己的
	// 入驻资料并读取自己的申请状态，否则「注册 → 填资料」这条链路直接断掉。
	// 只放行房东自己那两个接口（/landlord-apply 与 /landlord-apply/me）；
	// 管理员的审核队列走 /landlord-applications，不在白名单里，且挂在 admin 组。
	"/api/rental/landlord-apply",
	"/api/rental/portal/status",
	"/api/health",
}

// requiresApprovalPath 判断这个路径对「待审核（status==2）」用户是否**放行**。
// 返回 true = 允许通过，false = 应当 403。默认拒绝，故新接口自动受保护。
//
// 匹配口径是**按路径段的前缀**，不是裸字符串前缀：列 /api/configs 只放行
// /api/configs 与 /api/configs/xxx，不会顺带放行 /api/configsX。
func requiresApprovalPath(p string) bool {
	clean := normalizeAPIPath(p)
	if clean == "" {
		return false
	}
	for _, allow := range pendingWhitelist {
		if allow == "" {
			continue
		}
		base := strings.TrimSuffix(allow, "/")
		if base == "" {
			continue
		}
		if clean == base || strings.HasPrefix(clean, base+"/") {
			return true
		}
	}
	return false
}

// normalizeAPIPath 把请求路径归一化成「以 /api/ 开头」的形式（去掉网关前缀、
// 查询串与尾斜杠，并做 path.Clean 消歧义）。
func normalizeAPIPath(p string) string {
	if p == "" {
		return ""
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = path.Clean("/" + strings.TrimSpace(p))
	if p == "/" {
		return p
	}
	if idx := strings.Index(p, "/api/"); idx >= 0 {
		p = p[idx:]
	} else if strings.HasSuffix(p, "/api") {
		p = p[len(p)-len("/api"):]
	} else if !strings.HasPrefix(p, "/api/") {
		p = "/api" + p
	}
	if len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimRight(p, "/")
	}
	return p
}

// ApprovalRequired 报告该用户是否处于「待审核」状态（status==2）。
func ApprovalRequired(db *gorm.DB, userID uint) bool {
	if db == nil || userID == 0 {
		return false
	}
	var u struct {
		Status int
	}
	if err := db.Table("users").Select("status").Where("id = ?", userID).Take(&u).Error; err != nil {
		return false
	}
	return u.Status == UserStatusPending
}

// PendingUserDenied 给框架中间件的一次性判定入口：返回 true = 本次请求应当 403。
func PendingUserDenied(db *gorm.DB, userID uint, reqPath string) bool {
	if !ApprovalRequired(db, userID) {
		return false
	}
	return !requiresApprovalPath(reqPath)
}

// PendingUserMessage 是待审核用户被拦下时的统一中文提示。
const PendingUserMessage = "账号正在等待管理员审核，审核通过前无法使用该功能"

// ---------------------------------------------------------------------------
// 路由挂载
// ---------------------------------------------------------------------------

// setupLedgerAdminRoutes 挂载账号审核（框架 adminGroup，仅超级管理员）。
//
// 台账本身不在这里：任何用户都能自建共同台账并管理自己的成员，
// 超管对别人的台账没有数据特权。
func setupLedgerAdminRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	admin.GET("/rental/users/pending", handlePendingUsers(db))
	admin.POST("/rental/users/:id/approve", handleApproveUser(db))
	admin.POST("/rental/users/:id/reject", handleRejectUser(db))
}

// setupLedgerAuthRoutes 挂载台账与成员接口（框架 authGroup，任何已登录用户）。
// 每个 handler 自己判定「是不是这个台账的管理者 / 正式成员」。
func setupLedgerAuthRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/ledgers/mine", handleMyLedgers(db))
	api.POST("/rental/ledgers", handleLedgerCreate(db))
	api.PUT("/rental/ledgers/:id", handleLedgerUpdate(db))
	api.DELETE("/rental/ledgers/:id", handleLedgerDelete(db))

	api.GET("/rental/ledgers/:id/members", handleLedgerMembers(db))
	api.POST("/rental/ledgers/:id/members", handleLedgerMemberAdd(db))
	api.PUT("/rental/ledgers/:id/members/:uid", handleLedgerMemberUpdate(db))
	api.DELETE("/rental/ledgers/:id/members/:uid", handleLedgerMemberRemove(db))
	api.POST("/rental/ledgers/:id/invitations/respond", handleRespondInvitation(db))

	// 邀请时按用户名找人（与账单应用一致：输入已注册用户名）。
	api.GET("/rental/users/search", handleUserSearch(db))

	// 自读审核状态：待审核用户唯一能进的接口之一（在 pending 白名单里），
	// 「等待审核」页据此渲染，不依赖别的接口的 403。
	api.GET("/rental/users/me/approval", handleMyApproval(db))

	// 按台账的租房设置：水价/电价/气价/计费方式/缴费周期/缴费日/提醒天数/单据抬头。
	// 读 = 该台账正式成员，写 = 该台账管理者；无覆盖时回退系统级。
	api.GET("/rental/ledgers/:id/settings", handleLedgerSettingsGet(db))
	api.PUT("/rental/ledgers/:id/settings", handleLedgerSettingsSet(db))
	api.DELETE("/rental/ledgers/:id/settings", handleLedgerSettingsReset(db))
}

// ---------------------------------------------------------------------------
// 我的台账
// ---------------------------------------------------------------------------

// handleMyLedgers 我可见的台账：自己的个人台账 + 参与（含待接受邀请）
// 的共同台账。前端台账切换器、待处理邀请都读它。
func handleMyLedgers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		views, err := visibleLedgers(db, userID)
		if err != nil {
			response.ErrorInternal(c, "查询台账失败")
			return
		}
		response.Success(c, views)
	}
}

// ---------------------------------------------------------------------------
// 共同台账：新建 / 改名 / 删除
// ---------------------------------------------------------------------------

// normalizeLedgerName 归一化台账名：去首尾空白、压缩内部空白、拒绝空名与超长名。
func normalizeLedgerName(raw string) (string, bool) {
	name := strings.Join(strings.Fields(raw), " ")
	if name == "" {
		return "", false
	}
	if len([]rune(name)) > 50 {
		return "", false
	}
	return name, true
}

// ledgerNameTakenForOwner 同一个人名下的共同台账不能重名（不同人可重名）。
// excludeID 用于改名时排除自己。
func ledgerNameTakenForOwner(db *gorm.DB, ownerID uint, name string, excludeID uint) bool {
	var count int64
	q := db.Model(&Ledger{}).
		Where("owner_user_id = ? AND kind = ? AND LOWER(name) = ?", ownerID, LedgerKindShared, strings.ToLower(name))
	if excludeID != 0 {
		q = q.Where("id <> ?", excludeID)
	}
	q.Count(&count)
	return count > 0
}

// defaultMemberPerms 新成员加入时的默认权限：全部模块**只读**。
//
// 为什么给只读而不是什么都不给：被邀请的人接受后应该立刻看得到这个台账的
// 内容（否则他以为加了个空台账）；管理员随后在成员管理里按人调整档位。
func defaultMemberPerms() ModulePerms {
	out := ModulePerms{}
	for _, k := range moduleKeys {
		out[k] = PermView
	}
	return out
}

// splitUsernames 把「用逗号/顿号/空白分隔的用户名串」拆成用户名列表。
func splitUsernames(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '、' || r == ';' || r == '；' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		name := strings.TrimSpace(f)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// resolveInviteTargets 把用户名列表解析成可邀请的用户：不存在的、被禁用的、
// 待审核的、自己的都剔除，并返回被剔除的名字用于提示（不因此整单失败——
// 建台账失败在一个打错的用户名上体验很差）。
func resolveInviteTargets(db *gorm.DB, names []string, selfID uint) (targets []database.User, skipped []string) {
	for _, name := range names {
		var u database.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			skipped = append(skipped, name)
			continue
		}
		if u.ID == selfID || u.Status != UserStatusActive {
			skipped = append(skipped, name)
			continue
		}
		targets = append(targets, u)
	}
	return targets, skipped
}

// handleLedgerCreate 新建共同台账：创建者就是管理者（role=owner），
// 可同时在 body 里带一串用户名直接发出邀请（pending）。
func handleLedgerCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		var req struct {
			Name      string `json:"name"`
			Usernames string `json:"usernames"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		name, ok := normalizeLedgerName(req.Name)
		if !ok {
			response.ErrorBadRequest(c, "请填写台账名称（1–50 个字）")
			return
		}
		if ledgerNameTakenForOwner(db, userID, name, 0) {
			response.ErrorBadRequest(c, "你已经有一个同名的共同台账了")
			return
		}
		targets, skipped := resolveInviteTargets(db, splitUsernames(req.Usernames), userID)

		perms, err := marshalPerms(fullPerms())
		if err != nil {
			response.ErrorInternal(c, "创建台账失败")
			return
		}
		memberPerms, err := marshalPerms(defaultMemberPerms())
		if err != nil {
			response.ErrorInternal(c, "创建台账失败")
			return
		}

		ledger := Ledger{Name: name, Kind: LedgerKindShared, OwnerUserID: userID, CreatedBy: userID}
		err = db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&ledger).Error; err != nil {
				return err
			}
			if err := tx.Create(&LedgerMember{
				LedgerID: ledger.ID, UserID: userID,
				Role: MemberRoleOwner, Status: MemberStatusActive, Perms: perms,
			}).Error; err != nil {
				return err
			}
			for _, u := range targets {
				if err := tx.Create(&LedgerMember{
					LedgerID: ledger.ID, UserID: u.ID,
					Role: MemberRoleMember, Status: MemberStatusPending, Perms: memberPerms,
				}).Error; err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			response.ErrorInternal(c, "创建台账失败")
			return
		}
		audit.Log(db, c, "rental_ledger_create", "ledger", ledger.ID,
			fmt.Sprintf("创建共同台账 %q，邀请 %d 人", ledger.Name, len(targets)))
		response.Success(c, map[string]interface{}{
			"id": ledger.ID, "name": ledger.Name, "kind": ledger.Kind,
			"invited": len(targets), "skipped": skipped,
		})
	}
}

// handleLedgerUpdate 改名（仅管理者；个人台账也允许改名——那只是自己的显示名）。
func handleLedgerUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能修改台账设置")
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		name, ok := normalizeLedgerName(req.Name)
		if !ok {
			response.ErrorBadRequest(c, "请填写台账名称（1–50 个字）")
			return
		}
		if ledgerNameTakenForOwner(db, userID, name, ledgerID) {
			response.ErrorBadRequest(c, "你已经有一个同名的共同台账了")
			return
		}
		if err := db.Model(&Ledger{}).Where("id = ?", ledgerID).
			Update("name", name).Error; err != nil {
			response.ErrorInternal(c, "保存台账失败")
			return
		}
		audit.Log(db, c, "rental_ledger_update", "ledger", ledgerID, "重命名台账为 "+name)
		response.Success(c, map[string]interface{}{"id": ledgerID, "name": name})
	}
}

// handleLedgerDelete 删除共同台账（仅管理者）：个人台账不可删，台账里还有
// 业务数据时也不可删（先清空或把数据留在那儿）。
func handleLedgerDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		var ledger Ledger
		if err := db.First(&ledger, ledgerID).Error; err != nil {
			response.ErrorNotFound(c, "台账不存在")
			return
		}
		if ledger.Kind == LedgerKindPersonal {
			response.ErrorBadRequest(c, "个人台账不能删除")
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能删除台账")
			return
		}
		if ledgerHasBusinessData(db, ledgerID) {
			response.ErrorBadRequest(c, "该台账里还有房源、租户或账单等数据，请先清空后再删除")
			return
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("org_id = ?", ledgerID).Delete(&LedgerMember{}).Error; err != nil {
				return err
			}
			// 台账级租房设置（存在 system_configs 的保留 user_id 段）一并清掉。
			if err := tx.Where("user_id = ?", ledgerConfigUserID(ledgerID)).
				Delete(&database.SystemConfig{}).Error; err != nil {
				return err
			}
			return tx.Delete(&Ledger{}, ledgerID).Error
		})
		if err != nil {
			response.ErrorInternal(c, "删除台账失败")
			return
		}
		audit.Log(db, c, "rental_ledger_delete", "ledger", ledgerID, "删除共同台账 "+ledger.Name)
		response.Success(c, map[string]interface{}{"id": ledgerID})
	}
}

// ---------------------------------------------------------------------------
// 成员与邀请
// ---------------------------------------------------------------------------

// ledgerMemberView 成员列表行。
type ledgerMemberView struct {
	UserID    uint        `json:"user_id"`
	Username  string      `json:"username"`
	Role      string      `json:"role"`
	Status    string      `json:"status"`
	Perms     ModulePerms `json:"perms"`
	IsSelf    bool        `json:"is_self"`
	JoinedAt  time.Time   `json:"joined_at"`
	StatusCap string      `json:"status_label"`
}

// memberStatusLabel 成员状态的中文名（前端直接用）。
func memberStatusLabel(status string) string {
	switch status {
	case MemberStatusActive:
		return "已加入"
	case MemberStatusPending:
		return "等待接受"
	case MemberStatusRejected:
		return "已拒绝"
	}
	return status
}

// handleLedgerMembers 成员列表：正式成员都能看；管理者还能看到待接受/已拒绝的邀请。
func handleLedgerMembers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if activeMemberOf(db, userID, ledgerID) == nil {
			response.ErrorForbidden(c, "你不是该台账的成员")
			return
		}
		owner := isLedgerOwner(db, userID, ledgerID)
		q := db.Model(&LedgerMember{}).Where("org_id = ?", ledgerID)
		if !owner {
			q = q.Where("status = ?", MemberStatusActive)
		}
		var rows []LedgerMember
		if err := q.Order("role DESC, user_id ASC").Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询成员失败")
			return
		}
		out := make([]ledgerMemberView, 0, len(rows))
		for _, m := range rows {
			perms := m.LedgerPerms()
			if m.Role == MemberRoleOwner {
				perms = fullPerms()
			}
			out = append(out, ledgerMemberView{
				UserID: m.UserID, Username: usernameOf(db, m.UserID),
				Role: m.Role, Status: m.Status, Perms: perms,
				IsSelf: m.UserID == userID, JoinedAt: m.CreatedAt,
				StatusCap: memberStatusLabel(m.Status),
			})
		}
		response.Success(c, map[string]interface{}{
			"members":  out,
			"is_owner": owner,
		})
	}
}

// handleLedgerMemberAdd 邀请成员（仅管理者）：按用户名邀请；被拒绝过的邀请
// 复用原行重置为 pending；已是成员或已发出邀请则提示。
func handleLedgerMemberAdd(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能邀请成员")
			return
		}
		var req struct {
			Username string `json:"username"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写要邀请的用户名")
			return
		}
		name := strings.TrimSpace(req.Username)
		if name == "" {
			response.ErrorBadRequest(c, "请填写要邀请的用户名")
			return
		}
		var u database.User
		if err := db.Where("username = ?", name).First(&u).Error; err != nil {
			response.ErrorBadRequest(c, "用户不存在："+name)
			return
		}
		if u.ID == userID {
			response.ErrorBadRequest(c, "不用邀请自己，你已经是这个台账的管理者")
			return
		}
		if u.Status == UserStatusPending {
			response.ErrorBadRequest(c, "该账号还在等待管理员审核，审核通过后再邀请")
			return
		}
		if u.Status != UserStatusActive {
			response.ErrorBadRequest(c, "该账号已被禁用，无法加入台账")
			return
		}
		perms, err := marshalPerms(defaultMemberPerms())
		if err != nil {
			response.ErrorInternal(c, "邀请失败")
			return
		}
		existing := memberOf(db, u.ID, ledgerID)
		if existing != nil {
			switch existing.Status {
			case MemberStatusActive:
				response.ErrorBadRequest(c, "该用户已经是这个台账的成员")
				return
			case MemberStatusPending:
				response.ErrorBadRequest(c, "已经邀请过该用户，正在等待他接受")
				return
			}
			// rejected：重新邀请（保留他原来被拒绝的状态行，重置为待接受）。
			if err := db.Model(&LedgerMember{}).
				Where("org_id = ? AND user_id = ?", ledgerID, u.ID).
				Updates(map[string]interface{}{
					"status": MemberStatusPending, "perms": perms,
				}).Error; err != nil {
				response.ErrorInternal(c, "邀请失败")
				return
			}
		} else if err := db.Create(&LedgerMember{
			LedgerID: ledgerID, UserID: u.ID,
			Role: MemberRoleMember, Status: MemberStatusPending, Perms: perms,
		}).Error; err != nil {
			response.ErrorInternal(c, "邀请失败")
			return
		}
		audit.Log(db, c, "rental_ledger_member_invite", "ledger", ledgerID, "邀请成员 "+u.Username)
		response.Success(c, map[string]interface{}{"user_id": u.ID, "username": u.Username, "status": MemberStatusPending})
	}
}

// handleLedgerMemberUpdate 设置某成员的模块权限（仅管理者；管理者的权限固定
// 为全部模块，不允许改自己）。
func handleLedgerMemberUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能设置成员权限")
			return
		}
		targetID, err := strconv.ParseUint(c.Param("uid"), 10, 32)
		if err != nil || targetID == 0 {
			response.ErrorBadRequest(c, "无效的用户 ID")
			return
		}
		target := memberOf(db, uint(targetID), ledgerID)
		if target == nil {
			response.ErrorNotFound(c, "该用户不在这个台账里")
			return
		}
		if target.Role == MemberRoleOwner {
			response.ErrorBadRequest(c, "台账管理者的权限固定为全部模块，不能修改")
			return
		}
		var req struct {
			Perms ModulePerms `json:"perms"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请提供权限勾选")
			return
		}
		perms := normalizePerms(req.Perms)
		stored, err := marshalPerms(perms)
		if err != nil {
			response.ErrorInternal(c, "保存权限失败")
			return
		}
		if err := db.Model(&LedgerMember{}).
			Where("org_id = ? AND user_id = ?", ledgerID, uint(targetID)).
			Update("perms", stored).Error; err != nil {
			response.ErrorInternal(c, "保存权限失败")
			return
		}
		audit.Log(db, c, "rental_ledger_member_update", "ledger", ledgerID,
			fmt.Sprintf("设置成员 %d 的模块权限", targetID))
		response.Success(c, map[string]interface{}{"user_id": targetID, "perms": perms})
	}
}

// handleLedgerMemberRemove 移除成员：管理者可以移除别人，成员可以移除自己
// （主动退出）。管理者不能把自己移出去（台账会无主），个人台账的成员只有本人。
func handleLedgerMemberRemove(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		targetID, err := strconv.ParseUint(c.Param("uid"), 10, 32)
		if err != nil || targetID == 0 {
			response.ErrorBadRequest(c, "无效的用户 ID")
			return
		}
		var ledger Ledger
		if err := db.First(&ledger, ledgerID).Error; err != nil {
			response.ErrorNotFound(c, "台账不存在")
			return
		}
		if ledger.Kind == LedgerKindPersonal {
			response.ErrorBadRequest(c, "个人台账不能退出")
			return
		}
		owner := isLedgerOwner(db, userID, ledgerID)
		leaving := uint(targetID) == userID
		if !owner && !leaving {
			response.ErrorForbidden(c, "只有台账管理者能移除其他成员")
			return
		}
		target := memberOf(db, uint(targetID), ledgerID)
		if target == nil {
			response.ErrorNotFound(c, "该用户不在这个台账里")
			return
		}
		if target.Role == MemberRoleOwner {
			response.ErrorBadRequest(c, "台账管理者不能移除自己；如需停用请先删除该台账")
			return
		}
		if err := db.Where("org_id = ? AND user_id = ?", ledgerID, uint(targetID)).
			Delete(&LedgerMember{}).Error; err != nil {
			response.ErrorInternal(c, "移除成员失败")
			return
		}
		action := "移除成员"
		if leaving {
			action = "退出台账"
		}
		audit.Log(db, c, "rental_ledger_member_remove", "ledger", ledgerID,
			fmt.Sprintf("%s %d", action, targetID))
		response.Success(c, map[string]interface{}{"user_id": targetID, "left": leaving})
	}
}

// handleRespondInvitation 被邀请人接受或拒绝邀请。
func handleRespondInvitation(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		var req struct {
			Accept bool `json:"accept"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		m := memberOf(db, userID, ledgerID)
		if m == nil || m.Status != MemberStatusPending {
			response.ErrorBadRequest(c, "没有待处理的邀请")
			return
		}
		next := MemberStatusRejected
		if req.Accept {
			next = MemberStatusActive
		}
		if err := db.Model(&LedgerMember{}).
			Where("org_id = ? AND user_id = ?", ledgerID, userID).
			Update("status", next).Error; err != nil {
			response.ErrorInternal(c, "处理邀请失败")
			return
		}
		audit.Log(db, c, "rental_ledger_invite_respond", "ledger", ledgerID,
			"处理邀请："+memberStatusLabel(next))
		response.Success(c, map[string]interface{}{"ledger_id": ledgerID, "status": next})
	}
}

// handleUserSearch 邀请时按用户名找人：只返回状态正常的账号，最多 20 条。
func handleUserSearch(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		q := strings.TrimSpace(c.Query("q"))
		type row struct {
			ID       uint   `json:"id"`
			Username string `json:"username"`
		}
		// 账号枚举防护（2026-10-11）：这个接口是「邀请成员时搜人」用的，
		// 此前 q 为空会把前 20 个账号直接列出来，任意登录用户拿它当花名册
		// 扫号。现在要求至少 2 个字，且只回前 10 条精确一点的匹配。
		if len([]rune(q)) < 2 {
			response.ErrorBadRequest(c, "请至少输入 2 个字再搜索")
			return
		}
		rows := make([]row, 0)
		// v0.6.10：**排除租户账号**（role=tenant）。这个接口是「邀请台账成员」
		// 用的，而租户不是协作者——他不该被邀请进任何台账（进了就会拿到业务
		// 权限，与「租户只读本人数据」冲突）。过滤放在 SQL 里，租户账号根本
		// 不出现在候选里。
		query := db.Table("users").Select("id, username").
			Where("status = ? AND id <> ? AND role <> ?", UserStatusActive, userID, user.RoleTenant).
			Where("username LIKE ?", "%"+q+"%")
		if err := query.Order("id ASC").Limit(10).Scan(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询用户失败")
			return
		}
		response.Success(c, rows)
	}
}

// ---------------------------------------------------------------------------
// 注册审批（管理员）：通过 / 驳回
// ---------------------------------------------------------------------------

// pendingUserRow 待审核用户行。
type pendingUserRow struct {
	ID        uint      `json:"id"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// handlePendingUsers 列出待审核用户（status==2）。
func handlePendingUsers(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows := make([]pendingUserRow, 0)
		err := db.Table("users").
			Select("id, username, created_at").
			Where("status = ?", UserStatusPending).
			Order("id ASC").
			Scan(&rows).Error
		if err != nil {
			response.ErrorInternal(c, "查询待审核用户失败")
			return
		}
		response.Success(c, rows)
	}
}

// handleMyApproval 当前用户自读审核状态（在 pending 白名单里）。
func handleMyApproval(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		status := UserStatusActive
		role := user.RoleUser
		var u struct {
			Status int
			Role   string
		}
		if err := db.Table("users").Select("status, role").Where("id = ?", userID).Take(&u).Error; err == nil {
			status = u.Status
			if u.Role != "" {
				role = u.Role
			}
		}
		response.Success(c, map[string]interface{}{
			"status":  status,
			"pending": status == UserStatusPending,
			"message": PendingUserMessage,
			// v0.6.10：带上角色。前端在 /auth/check 认不下待审核会话时靠这个接口
			// 恢复登录态，而**待关联的租户也是 status=2**——少了角色，前端会把
			// 租户恢复成 user，从而把他送进房东的「等待审核」页而不是租户的
			// 「等待房东关联」页。
			"role": role,
		})
	}
}

// handleApproveUser 审批通过：把状态置为正常。
//
// 不再「分配台账」：新模型里每个账号注册时就有自己的个人台账，通过即可用；
// 要让他参与别的台账，由那个台账的管理者邀请（管理者的个人台账他自然也看不到）。
// 幂等：重复点通过会提示当前状态。
func handleApproveUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUserIDParam(c.Param("id"))
		if !ok {
			response.ErrorBadRequest(c, "无效的用户 ID")
			return
		}
		var u database.User
		if err := db.First(&u, id).Error; err != nil {
			response.ErrorNotFound(c, "用户不存在")
			return
		}
		if u.Status != UserStatusPending {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"该用户不在待审核状态（当前状态：%s）", userStatusName(u.Status)))
			return
		}
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&database.User{}).Where("id = ?", id).
				Update("status", UserStatusActive).Error; err != nil {
				return err
			}
			// 兜底：个人台账在注册钩子里已建好；历史/异常数据缺了就补一个，
			// 免得他通过之后无处落数据。
			_, err := ensurePersonalLedger(tx, id)
			return err
		})
		if err != nil {
			response.ErrorInternal(c, "审批失败")
			return
		}
		audit.Log(db, c, "rental_user_approve", "user", id,
			fmt.Sprintf("审批通过用户 %q", u.Username))
		response.Success(c, map[string]interface{}{"user_id": id, "status": UserStatusActive})
	}
}

// handleRejectUser 驳回注册：把状态置为 0（已禁用）并**保留用户行**。
//
// 为什么不删行：驳回是可逆的——管理员之后可能改主意，而删掉账号会连带
// 清掉注册痕迹与审计线索。置 0 后该账号无法登录，效果等价于拒绝。
func handleRejectUser(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseUserIDParam(c.Param("id"))
		if !ok {
			response.ErrorBadRequest(c, "无效的用户 ID")
			return
		}
		var u database.User
		if err := db.First(&u, id).Error; err != nil {
			response.ErrorNotFound(c, "用户不存在")
			return
		}
		if isAdminRole(u.Role) {
			response.ErrorForbidden(c, "不能驳回超级管理员账号")
			return
		}
		if u.Status == UserStatusDisabled {
			response.ErrorBadRequest(c, "该用户已被禁用")
			return
		}
		if err := db.Model(&database.User{}).Where("id = ?", id).
			Update("status", UserStatusDisabled).Error; err != nil {
			response.ErrorInternal(c, "驳回失败")
			return
		}
		audit.Log(db, c, "rental_user_reject", "user", id,
			fmt.Sprintf("驳回用户 %q 的注册申请（已禁用，账号保留）", u.Username))
		response.Success(c, map[string]interface{}{
			"user_id": id,
			"status":  UserStatusDisabled,
			"message": "已驳回该注册申请：账号已禁用但保留，如需重新启用请在用户管理中调整状态",
		})
	}
}

// ---------------------------------------------------------------------------
// 按台账的租房设置
// ---------------------------------------------------------------------------

// ledgerSettingsResponse 某台账的生效租房设置 + 哪些键是该台账自己的覆盖值。
type ledgerSettingsResponse struct {
	LedgerID  uint              `json:"ledger_id"`
	Effective map[string]string `json:"effective"`
	Overrides map[string]string `json:"overrides"`
}

// handleLedgerSettingsGet 读某台账的生效租房设置（该台账正式成员可读）。
func handleLedgerSettingsGet(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if activeMemberOf(db, c.GetUint("userID"), ledgerID) == nil {
			response.ErrorForbidden(c, "你不是该台账的成员")
			return
		}
		response.Success(c, buildLedgerSettings(db, ledgerID))
	}
}

// handleLedgerSettingsSet 写某台账的覆盖值（仅管理者）：只接受已登记的
// rental_* 键，值按注册的类型校验，避免存进脏值。
func handleLedgerSettingsSet(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能修改这个台账的设置")
			return
		}
		var req struct {
			Settings map[string]string `json:"settings"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		if len(req.Settings) == 0 {
			response.ErrorBadRequest(c, "请提供要保存的设置")
			return
		}
		for key, value := range req.Settings {
			if !isRentalConfigKey(key) {
				response.ErrorBadRequest(c, "不支持的设置项："+key)
				return
			}
			if def, found := sysconfig.GetConfigDef(key); found {
				if err := sysconfig.Validate(def, value); err != nil {
					response.ErrorBadRequest(c, "「"+def.Label+"」取值无效："+err.Error())
					return
				}
			}
			if err := setLedgerConfigValue(db, key, value, ledgerID); err != nil {
				response.ErrorInternal(c, "保存设置失败")
				return
			}
		}
		audit.Log(db, c, "ledger_settings_update", "ledger", ledgerID, "按台账修改租房设置")
		response.Success(c, buildLedgerSettings(db, ledgerID))
	}
}

// handleLedgerSettingsReset 清空某台账的全部覆盖值（恢复跟随系统级，仅管理者）。
func handleLedgerSettingsReset(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		ledgerID, ok := parseLedgerIDParam(c)
		if !ok {
			return
		}
		if !isLedgerOwner(db, userID, ledgerID) {
			response.ErrorForbidden(c, "只有台账管理者能修改这个台账的设置")
			return
		}
		for _, key := range rentalConfigKeys {
			if err := clearLedgerConfigValue(db, key, ledgerID); err != nil {
				response.ErrorInternal(c, "重置设置失败")
				return
			}
		}
		audit.Log(db, c, "ledger_settings_reset", "ledger", ledgerID, "恢复台账设置为跟随系统")
		response.Success(c, buildLedgerSettings(db, ledgerID))
	}
}

// parseUserIDParam 解析用户 ID 路径参数（纯数字，0 视为无效）。
func parseUserIDParam(raw string) (uint, bool) {
	id, ok := parseLedgerID(raw)
	if !ok || id == 0 {
		return 0, false
	}
	return id, true
}

// parseLedgerIDParam 解析 :id 路径参数。
func parseLedgerIDParam(c *gin.Context) (uint, bool) {
	raw := strings.TrimSpace(c.Param("id"))
	ledgerID, valid := parseLedgerID(raw)
	if !valid {
		response.ErrorBadRequest(c, "无效的台账 ID")
		return 0, false
	}
	return ledgerID, true
}

// isRentalConfigKey 是否为可被台账覆盖的租房设置键。
func isRentalConfigKey(key string) bool {
	for _, k := range rentalConfigKeys {
		if k == key {
			return true
		}
	}
	return false
}

// buildLedgerSettings 汇总某台账的生效值与覆盖清单。
func buildLedgerSettings(db *gorm.DB, ledgerID uint) ledgerSettingsResponse {
	d := ledgerBillingDefaultsFor(db, ledgerID)
	name, contact, note := receiptHeaderFor(db, ledgerID)
	effective := map[string]string{
		"rental_water_mode":        d.WaterMode,
		"rental_water_price":       fmt.Sprintf("%.2f", d.WaterPrice),
		"rental_water_monthly_fee": fmt.Sprintf("%.2f", d.WaterMonthlyFee),
		"rental_elec_price":        fmt.Sprintf("%.2f", d.ElecPrice),
		"rental_gas_price":         fmt.Sprintf("%.2f", d.GasPrice),
		"rental_pay_cycle":         d.PayCycle,
		"rental_pay_day":           fmt.Sprintf("%d", d.PayDay),
		"rental_remind_days":       fmt.Sprintf("%d", d.RemindDays),
		"rental_property_name":     name,
		"rental_contact":           contact,
		"rental_receipt_note":      note,
		// 结转开关（issue #5 需求 7）：必须出现在 effective 里，否则台账设置
		// 弹窗读不到当前值，开关永远显示成默认态，房东改完看不到变化。
		carryoverConfigKey: strconv.FormatBool(carryoverEnabled(db, ledgerID)),
	}
	overrides := map[string]string{}
	for _, key := range rentalConfigKeys {
		if v, ok := ledgerConfigValue(db, key, ledgerID); ok {
			overrides[key] = v
		}
	}
	return ledgerSettingsResponse{LedgerID: ledgerID, Effective: effective, Overrides: overrides}
}
