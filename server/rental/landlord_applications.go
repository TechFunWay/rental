// landlord_applications.go — 房东入驻申请（把租房管理当平台做，v0.6.10 追加）。
//
// 需求（用户 2026-10-13）：房东可以申请入驻，申请通过后才能管理自己的房源与租户；
// 后期还会有租户登录。角色三层：
//
//	admin  → 系统管理员（平台方：审核入驻、看系统配置与备份，**对业务数据无特权**）
//	user   → 房东（出租人：注册即自动获得一个只属于自己的个人台账，数据默认隔离）
//	tenant → 租户（只读自己的资料与账单，见 tenant_portal.go）
//
// 与既有的「注册审核」什么关系：注册审核管的是**账号能不能用**（users.status=2 → 1），
// 本文件管的是**入驻资料**——房东是谁、什么规模、哪个地区、为什么入驻。两件事一起发生，
// 但分开存：账号是框架层的行，入驻资料是业务层的行（`landlord_applications`）。
//
// 时序：注册页填账号 + 入驻资料 → 注册成功（status=2 待审核，能登录但业务接口 403）
// → 立刻提交入驻申请（待审核用户在**白名单内**，见 ledger_api.go 的 pendingWhitelist）
// → 平台管理员在「入驻申请」里通过/驳回。
package rental

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"smallgo/server/audit"
	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 入驻申请状态（与 users.status 语义对齐但不共用字段：一份是账号状态，一份是申请状态）。
const (
	LandlordApplyPending  = "pending"
	LandlordApplyApproved = "approved"
	LandlordApplyRejected = "rejected"
)

// LandlordApplication 房东入驻申请。一个账号同时只保留一条**待审**申请
// （重复提交按覆盖处理，改了资料重新提交即可）。
type LandlordApplication struct {
	ID         uint       `gorm:"primarykey" json:"id"`
	UserID     uint       `gorm:"index;not null" json:"user_id"` // 申请账号
	Username   string     `json:"username"`                      // 冗余用户名，列表直接显示
	Name       string     `gorm:"not null" json:"name"`          // 真实姓名 / 称谓
	Phone      string     `gorm:"not null" json:"phone"`         // 联系电话
	Wechat     string     `json:"wechat"`                        // 微信号（选填）
	Region     string     `json:"region"`                        // 所在地区（选填）
	RoomScale  string     `json:"room_scale"`                    // 房源规模（选填，前端给选项）
	Intro      string     `json:"intro"`                         // 申请说明（选填）
	Status     string     `gorm:"default:pending;index" json:"status"`
	ReviewNote string     `json:"review_note"`
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

func init() {
	database.RegisterModels(&LandlordApplication{})
}

// setupLandlordApplyRoutes 待审核房东自己用的接口（**在待审核白名单里**）：
// 提交/更新入驻申请、读自己的申请状态。
func setupLandlordApplyRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.POST("/rental/landlord-apply", handleLandlordApplySubmit(db))
	api.GET("/rental/landlord-apply/me", handleLandlordApplyMine(db))
}

// setupLandlordReviewRoutes 平台管理员侧的队列与审核。
func setupLandlordReviewRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	admin.GET("/rental/landlord-applications", handleLandlordApplyList(db))
	admin.POST("/rental/landlord-applications/:id/approve", handleLandlordApplyApprove(db))
	admin.POST("/rental/landlord-applications/:id/reject", handleLandlordApplyReject(db))
}

type landlordApplyRequest struct {
	Name      string `json:"name"`
	Phone     string `json:"phone"`
	Wechat    string `json:"wechat"`
	Region    string `json:"region"`
	RoomScale string `json:"room_scale"`
	Intro     string `json:"intro"`
}

// handleLandlordApplySubmit 房东提交/更新自己的入驻申请。
// 待审核用户可调用（在白名单里）；已经是正常状态的账号不允许再"申请入驻"。
func handleLandlordApplySubmit(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			response.ErrorForbidden(c, "请先登录")
			return
		}
		var u database.User
		if err := db.First(&u, userID).Error; err != nil {
			response.ErrorNotFound(c, "账号不存在")
			return
		}
		if u.Role == user.RoleTenant {
			response.ErrorBadRequest(c, "租户账号不需要入驻申请，请联系房东关联你的档案")
			return
		}
		var req landlordApplyRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写姓名与联系电话")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		req.Phone = strings.TrimSpace(req.Phone)
		req.Wechat = strings.TrimSpace(req.Wechat)
		req.Region = strings.TrimSpace(req.Region)
		req.RoomScale = strings.TrimSpace(req.RoomScale)
		req.Intro = strings.TrimSpace(req.Intro)
		if msg := validateLandlordApply(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}

		// 已有待审申请 → 覆盖（改了资料重新提交）；已通过 → 直接告诉他不用再申请。
		var existing LandlordApplication
		err := db.Where("user_id = ? AND status = ?", userID, LandlordApplyPending).First(&existing).Error
		if err == nil {
			existing.Username = u.Username
			existing.Name, existing.Phone = req.Name, req.Phone
			existing.Wechat, existing.Region = req.Wechat, req.Region
			existing.RoomScale, existing.Intro = req.RoomScale, req.Intro
			if err := db.Save(&existing).Error; err != nil {
				response.ErrorInternal(c, "提交失败，请稍后再试")
				return
			}
			response.Success(c, existing)
			return
		}
		var approved int64
		db.Model(&LandlordApplication{}).
			Where("user_id = ? AND status = ?", userID, LandlordApplyApproved).Count(&approved)
		if approved > 0 && u.Status == UserStatusActive {
			response.ErrorBadRequest(c, "你的入驻申请已经通过，无需重复提交")
			return
		}
		app := LandlordApplication{
			UserID: userID, Username: u.Username,
			Name: req.Name, Phone: req.Phone, Wechat: req.Wechat,
			Region: req.Region, RoomScale: req.RoomScale, Intro: req.Intro,
			Status: LandlordApplyPending,
		}
		if err := db.Create(&app).Error; err != nil {
			response.ErrorInternal(c, "提交失败，请稍后再试")
			return
		}
		audit.Log(db, c, "rental_landlord_apply", "user", userID,
			fmt.Sprintf("提交房东入驻申请：%s %s", req.Name, req.Phone))
		response.Success(c, app)
	}
}

// handleLandlordApplyMine 读自己的入驻申请（等待页据此显示「已提交，等待审核」）。
func handleLandlordApplyMine(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		var app LandlordApplication
		err := db.Where("user_id = ?", userID).Order("id DESC").First(&app).Error
		if err != nil {
			response.Success(c, nil)
			return
		}
		response.Success(c, app)
	}
}

// handleLandlordApplyList 平台管理员看入驻申请队列（默认待审）。
func handleLandlordApplyList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		query := db.Model(&LandlordApplication{})
		switch strings.TrimSpace(c.Query("status")) {
		case LandlordApplyApproved, LandlordApplyRejected, LandlordApplyPending:
			query = query.Where("status = ?", strings.TrimSpace(c.Query("status")))
		default:
			query = query.Where("status = ?", LandlordApplyPending)
		}
		rows := make([]LandlordApplication, 0, 16)
		if err := query.Order("created_at ASC").Limit(200).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询入驻申请失败")
			return
		}
		// 附上账号当前状态：管理员一眼看出「申请还没处理但账号已被别处通过/停用」这类不一致
		type row struct {
			LandlordApplication
			UserStatus int `json:"user_status"`
		}
		out := make([]row, 0, len(rows))
		for _, r := range rows {
			out = append(out, row{LandlordApplication: r, UserStatus: userStatus(db, r.UserID)})
		}
		response.Success(c, out)
	}
}

// handleLandlordApplyApprove 通过入驻申请：账号置为正常 + 申请行置 approved。
// 通过后房东就拥有自己的个人台账（注册钩子已建，缺失时兜底补建）。
func handleLandlordApplyApprove(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findLandlordApplication(db, c)
		if !ok {
			return
		}
		if app.Status != LandlordApplyPending {
			response.ErrorBadRequest(c, "该申请已处理过")
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&req)
		note := strings.TrimSpace(req.Note)
		if len([]rune(note)) > 100 {
			response.ErrorBadRequest(c, "备注不能超过 100 字")
			return
		}
		now := time.Now()
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&database.User{}).Where("id = ?", app.UserID).
				Update("status", UserStatusActive).Error; err != nil {
				return err
			}
			// 兜底：个人台账在注册钩子里建好；历史/异常数据缺了就补一个
			if _, err := ensurePersonalLedger(tx, app.UserID); err != nil {
				return err
			}
			app.Status = LandlordApplyApproved
			app.ReviewNote = note
			app.ReviewedAt = &now
			return tx.Save(app).Error
		})
		if err != nil {
			response.ErrorInternal(c, "审批失败")
			return
		}
		audit.Log(db, c, "rental_landlord_approve", "user", app.UserID,
			fmt.Sprintf("通过房东入驻申请：%s（%s）", app.Name, app.Phone))
		response.Success(c, app)
	}
}

// handleLandlordApplyReject 驳回入驻申请：账号置为停用（保留用户行与申请行，可逆）。
func handleLandlordApplyReject(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findLandlordApplication(db, c)
		if !ok {
			return
		}
		if app.Status != LandlordApplyPending {
			response.ErrorBadRequest(c, "该申请已处理过")
			return
		}
		// 平台管理员自己的账号不能被这样驳回
		var u database.User
		if err := db.First(&u, app.UserID).Error; err == nil && isAdminRole(u.Role) {
			response.ErrorForbidden(c, "不能驳回系统管理员账号")
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&req)
		note := strings.TrimSpace(req.Note)
		if len([]rune(note)) > 100 {
			response.ErrorBadRequest(c, "备注不能超过 100 字")
			return
		}
		now := time.Now()
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&database.User{}).Where("id = ?", app.UserID).
				Update("status", UserStatusDisabled).Error; err != nil {
				return err
			}
			app.Status = LandlordApplyRejected
			app.ReviewNote = note
			app.ReviewedAt = &now
			return tx.Save(app).Error
		})
		if err != nil {
			response.ErrorInternal(c, "驳回失败")
			return
		}
		audit.Log(db, c, "rental_landlord_reject", "user", app.UserID,
			fmt.Sprintf("驳回房东入驻申请：%s，原因 %s", app.Name, note))
		response.Success(c, app)
	}
}

// findLandlordApplication 按 id 取申请，找不到时已写 404。
func findLandlordApplication(db *gorm.DB, c *gin.Context) (*LandlordApplication, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorNotFound(c, "申请不存在")
		return nil, false
	}
	var app LandlordApplication
	if err := db.First(&app, id).Error; err != nil {
		response.ErrorNotFound(c, "申请不存在")
		return nil, false
	}
	return &app, true
}

// validateLandlordApply 入驻申请入参校验。
func validateLandlordApply(req *landlordApplyRequest) string {
	if req.Name == "" {
		return "请填写姓名或称谓"
	}
	if len([]rune(req.Name)) > 30 {
		return "姓名不能超过 30 个字"
	}
	if !applyPhoneRe.MatchString(req.Phone) {
		return "请填写正确的联系电话"
	}
	if len([]rune(req.Wechat)) > 50 {
		return "微信号过长"
	}
	if len([]rune(req.Region)) > 30 {
		return "所在地区过长"
	}
	if len([]rune(req.RoomScale)) > 20 {
		return "房源规模过长"
	}
	if len([]rune(req.Intro)) > 200 {
		return "申请说明不能超过 200 字"
	}
	return ""
}
