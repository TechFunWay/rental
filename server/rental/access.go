// access.go — 业务数据访问权限。
//
// 租房管理的数据对所有账号共享（房源、租户、账单等不属于任何个人），
// 但注册是开放的：谁能看、谁能录，由管理员在用户管理里按人授权。
// 授权存在应用自己的表里（rental_accesses），不动框架的 users 表；
// role=admin 恒为完全权限，不需要授权行。
//
// 等级语义（从低到高）：
//   none     无业务权限（新注册用户的默认），业务接口一律 403；
//   readonly 只读：查看全部业务数据与统计、导出；
//   edit     录入：只读 + 新增/修改房源、租户、上传合同、抄表、账单、收款、费用项；
//   full     完全：录入 + 删除、CSV 导入。
package rental

import (
	"strconv"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 业务权限等级常量，取值即存库值，前端直接用同一套词。
const (
	AccessNone     = "none"
	AccessReadonly = "readonly"
	AccessEdit     = "edit"
	AccessFull     = "full"
)

var accessRank = map[string]int{
	AccessNone:     0,
	AccessReadonly: 1,
	AccessEdit:     2,
	AccessFull:     3,
}

// RentalAccess 用户级业务授权：每个用户一行，Level 缺省/未知按 none。
type RentalAccess struct {
	UserID    uint      `gorm:"primarykey" json:"user_id"`
	Level     string    `gorm:"default:none" json:"level"`
	UpdatedAt time.Time `json:"updated_at"`
}

func init() {
	database.RegisterModels(&RentalAccess{})
}

// accessLevel 解析用户的业务权限等级。管理员恒为 full；未授权、未知值
// 或查询出错一律按 none（故障宁可关上也不敞开）。
func accessLevel(db *gorm.DB, userID uint, role string) string {
	if role == "admin" {
		return AccessFull
	}
	if userID == 0 {
		return AccessNone
	}
	var row RentalAccess
	if err := db.First(&row, userID).Error; err != nil {
		return AccessNone
	}
	if accessRank[row.Level] > 0 {
		return row.Level
	}
	return AccessNone
}

// requireAccess 业务路由门禁：当前用户等级不足直接 403。挂在每条业务
// 路由上（读 readonly、写 edit、删/导入 full），错误文案点明要找管理员。
func requireAccess(db *gorm.DB, min string) gin.HandlerFunc {
	return func(c *gin.Context) {
		level := accessLevel(db, c.GetUint("userID"), c.GetString("role"))
		if accessRank[level] < accessRank[min] {
			response.ErrorForbidden(c, "没有租房管理的访问权限，请联系管理员在用户管理中授权")
			c.Abort()
			return
		}
		c.Next()
	}
}

// setupAccessMeRoutes 挂载当前用户权限查询（前端菜单与按钮门禁用）。
func setupAccessMeRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/access/me", handleAccessMe(db))
}

// setupAccessAdminRoutes 挂载授权管理（框架 adminGroup，仅管理员）：
// 列出全部非管理员用户的权限，按用户设置等级。
func setupAccessAdminRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	admin.GET("/rental/access", handleAccessList(db))
	admin.PUT("/rental/access/:userID", handleAccessSet(db))
}

// handleAccessMe 当前登录用户的业务权限等级。
func handleAccessMe(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		response.Success(c, map[string]interface{}{
			"level": accessLevel(db, c.GetUint("userID"), c.GetString("role")),
		})
	}
}

// accessRow 管理员看到的授权清单行：把全部非管理员用户列出来，
// 未授权的按 none 展示，前端直接整表渲染。
type accessRow struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Level    string `json:"level"`
}

func handleAccessList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		rows := make([]accessRow, 0)
		err := db.Table("users").
			Select("users.id AS user_id, users.username, IFNULL(rental_accesses.level, 'none') AS level").
			Joins("LEFT JOIN rental_accesses ON rental_accesses.user_id = users.id").
			Where("users.role <> 'admin'").
			Order("users.id ASC").
			Scan(&rows).Error
		if err != nil {
			response.ErrorInternal(c, "读取权限列表失败")
			return
		}
		response.Success(c, rows)
	}
}

func handleAccessSet(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("userID"), 10, 32)
		if err != nil || id == 0 {
			response.ErrorBadRequest(c, "无效的用户 ID")
			return
		}
		var req struct {
			Level string `json:"level" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请提供权限等级")
			return
		}
		// none 之外的三个是合法授权值；none 也有意义（撤销授权），一并放行。
		switch req.Level {
		case AccessNone, AccessReadonly, AccessEdit, AccessFull:
		default:
			response.ErrorBadRequest(c, "无效的权限等级")
			return
		}
		var userCount int64
		if err := db.Table("users").Where("id = ? AND role <> 'admin'", id).Count(&userCount).Error; err != nil {
			response.ErrorInternal(c, "检查用户失败")
			return
		}
		if userCount == 0 {
			response.ErrorBadRequest(c, "用户不存在或无需授权（管理员默认拥有全部权限）")
			return
		}
		// Upsert：有行改值，没行建行。UpdatedAt 交给 gorm 自动维护。
		if err := db.Save(&RentalAccess{UserID: uint(id), Level: req.Level, UpdatedAt: time.Now()}).Error; err != nil {
			response.ErrorInternal(c, "保存授权失败")
			return
		}
		response.Success(c, map[string]string{"level": req.Level})
	}
}
