// approval.go — 待审核门禁（v0.6.0）。
//
// 背景：应用可以给不同的人用，注册是开放的，但不该注册完就能看别人的台账。
// 新注册账号默认 `status = 2`（待审核）：**可以登录**，但除个人中心与
// 台账查询等白名单接口外，业务接口一律 403，直到管理员审核通过。
//
// 为什么让待审核用户「能登录」：否则他只会看到「账号或密码错误」，
// 以为注册失败而反复重试。能登录并看到明确的「待审核」提示，体验更好。
//
// 本文件只负责**门禁**；审核相关的常量、白名单与路径判定在 ledger_api.go
// （UserStatusPending、requiresApprovalPath、pendingWhitelist）。
//
// 为什么门禁做在**应用自己的路由组**上（而不是框架 middleware）：框架的
// middleware 包不能反向 import 业务包（会形成 import 环），而业务 app 的
// SetupAuth 拿到的正是 authGroup，包一层即可，且**新增业务路由自动受保护**
// ——不会因为以后加了接口忘登记而漏。
package rental

import (
	"net/http"

	"smallgo/server/database"
	"smallgo/server/middleware"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func init() {
	// 把待审核门禁注册进框架鉴权中间件。中间件刻意不 import 业务包，
	// 只依赖这个钩子，框架层因此保持与 smallgo 基线一致。
	middleware.RegisterApprovalGate(pendingGateDenied)
	middleware.RegisterApprovalMessage(PendingUserMessage)
}

// userStatus 读用户当前状态；查不到按「停用」处理（故障宁可关上）。
func userStatus(db *gorm.DB, userID uint) int {
	var status int
	if err := db.Table("users").Select("status").Where("id = ?", userID).Scan(&status).Error; err != nil {
		return UserStatusDisabled
	}
	return status
}

// pendingGateDenied 是注册给框架 middleware 的待审核钩子：返回 true = 拦下。
//
// 判定分两步，顺序不能反：
//  1. 用户确实是待审核（读库，避免对正常用户做多余判断）；
//  2. 该路径不在白名单里 —— 白名单是**默认拒绝**的，所以以后新增的接口
//     自动对待审核用户关闭，不会因为忘记登记而漏。
//
// 注意 db 由中间件作为参数传入，这里不捕获任何全局 db：避免业务包里再存
// 一份全局连接（既多一处注入时序风险，也让框架层与业务层耦合更紧）。
func pendingGateDenied(db *gorm.DB, userID uint, reqPath string) bool {
	if db == nil {
		// 没有库连接时保守放行：宁可漏挡一次，也不要把所有正常用户挡在外面
		// （此处只影响「待审核」这一种状态的用户，且下一次请求仍会被判）。
		return false
	}
	if userStatus(db, userID) != UserStatusPending {
		return false
	}
	return !requiresApprovalPath(reqPath)
}

// setupRoutesWithApprovalGate 在业务路由组外层包上待审核门禁。
//
// 这一层是**双保险**：主门禁在框架 middleware.RequireAuth（那里才能拦住
// 挂 RequireAuth 的框架路由，如 /api/auth/me）。这里再包一层，覆盖
// 万一将来有绕过 RequireAuth 的业务挂载方式。
func setupRoutesWithApprovalGate(api *gin.RouterGroup, db *gorm.DB) {
	guarded := api.Group("", approvalGate(db))
	setupRoutes(guarded, db)
	// 租户门户（v0.6.10）：同样经过待审核门禁——未关联的租户（status=2）只有
	// /rental/portal/status 在白名单里，其余门户接口一律 403，因此未关联时
	// 拿不到任何业务数据。门户刻意不挂在 setupRoutes 里：那里被 denyTenantRole
	// 包着，而门户正是要放行租户。
	setupPortalRoutes(guarded, db)
}

// approvalGate 业务侧待审核门禁（与框架钩子同口径）。
func approvalGate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if userID == 0 {
			c.Next()
			return
		}
		if !pendingGateDenied(db, userID, c.Request.URL.Path) {
			c.Next()
			return
		}
		response.Error(c, http.StatusForbidden, response.CodeForbidden, PendingUserMessage)
		c.Abort()
	}
}

// PendingUserIDs 全部待审核用户 ID（管理员侧展示与提醒用）。
func PendingUserIDs(db *gorm.DB) []uint {
	ids := make([]uint, 0)
	_ = db.Model(&database.User{}).Where("status = ?", UserStatusPending).
		Order("id ASC").Pluck("id", &ids).Error
	return ids
}
