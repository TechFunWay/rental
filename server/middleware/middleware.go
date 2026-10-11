package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"smallgo/server/auth"
	"smallgo/server/database"
	"smallgo/server/logger"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type fnOSGatewayContextKey struct{}

// authenticate 认人成功时通过 c.Set("auth_via", …) 记下这次靠的是什么凭证，
// 供需要区分「会话归谁」的下游使用（如 /auth/check 的 session_source：只有
// AuthViaGateway 才意味着会话跟随 NAS，其余都是应用自己的会话）。
const (
	AuthViaAPIKey  = "api_key"
	AuthViaToken   = "token"
	AuthViaGateway = "gateway"
)

// MarkFnOSGateway marks a request connection that arrived through the fnOS
// unified Unix-socket gateway. TCP clients can send the same header names, so
// the injected identity headers are only trusted when this marker was applied
// by server.Start's ConnContext.
func MarkFnOSGateway(ctx context.Context) context.Context {
	return context.WithValue(ctx, fnOSGatewayContextKey{}, true)
}

// OnFnOSGateway reports whether ConnContext marked this request as arriving
// through the fnOS unified gateway socket.
func OnFnOSGateway(ctx context.Context) bool {
	return ctx.Value(fnOSGatewayContextKey{}) == true
}

// GatewayIdentityUser 返回「仅凭网关注入的 NAS 身份」解析出的应用账号，即不需要
// 任何应用自己的凭证就成立的隐式登录态。它同时被两处使用，语义必须一致：
// authenticate 用它充当登录态，handleCheckAuth 用它区分当前会话属于网关还是
// 属于应用自己。用户主动登出后（suppressed）这里不再返回账号。
func GatewayIdentityUser(r *http.Request, db *gorm.DB) (database.User, bool) {
	if r == nil || db == nil {
		return database.User{}, false
	}
	if !OnFnOSGateway(r.Context()) {
		return database.User{}, false
	}
	return userFromGatewayIdentityHeader(r, db)
}

func userFromGatewayIdentityHeader(r *http.Request, db *gorm.DB) (database.User, bool) {
	raw := strings.TrimSpace(r.Header.Get("X-Trim-Userid"))
	if raw == "" {
		return database.User{}, false
	}
	nasUserID, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || nasUserID == 0 {
		return database.User{}, false
	}
	var user database.User
	if err := db.Where("fn_os_user_id = ? AND status = ?", uint(nasUserID), 1).First(&user).Error; err != nil {
		return database.User{}, false
	}
	if appSessionSuppressed(db, user.ID) {
		return database.User{}, false
	}
	return user, true
}

// appSessionSuppressed 读 user_sessions.suppressed。查询失败按「未抑制」处理：
// 一次数据库抖动不该把所有网关用户挡在登录页外。读写都只碰 database 包，
// 避免 middleware → user 的循环依赖。
func appSessionSuppressed(db *gorm.DB, userID uint) bool {
	var session database.UserSession
	if err := db.Where("user_id = ?", userID).First(&session).Error; err != nil {
		return false
	}
	return session.Suppressed
}

func LimitJSONBody(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

func CORS(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization, X-API-Key")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

// 用户状态取值（与 database.User.Status 一致）。
//
// 1/0 是框架原有语义（1=正常、0=停用）；2=「待审核」是业务应用引入的扩展
// 语义（新注册账号默认为 2，可登录但业务接口全禁）。这里刻意各自声明一份，
// 而不是 import 业务包：middleware 属框架层，必须与 smallgo 基线保持一致，
// 不感知任何具体业务。
const (
	UserStatusDisabled = 0
	UserStatusActive   = 1
	UserStatusPending  = 2
)

// ApprovalGate 是「待审核用户」的准入判定钩子，由业务应用注册。
//
// 为什么做成钩子而不是直接 import 业务包：middleware 是框架层，应当与
// smallgo 基线保持一致、不感知具体业务；同时 status=2 的语义（待审核）
// 与放行白名单都属于业务约定，放在业务包里维护更合适。用法与既有的
// database.RegisterUserDeleteGuard 同一套路。
//
// db 由调用方（RequireAuth 本来就持有）作为参数传入，而不是在注册时闭包
// 捕获：闭包捕获需要在业务包里另存一份全局 db（如 appDB），一旦注入时机
// 早于使用就会出现 nil 窗口。走参数则依赖显式、没有时序问题。
type ApprovalGate func(db *gorm.DB, userID uint, reqPath string) bool

var approvalGate ApprovalGate

// approvalMessage 待审核时的用户提示文案，可由业务侧覆盖以保持前后端一致。
var approvalMessage = "账号待管理员审核，审核通过并分配组织后即可使用"

// RegisterApprovalGate 注册待审核门禁钩子（业务 app 在 init 里调用一次）。
// 未注册时框架按「没有待审核概念」处理，行为与基线一致。
// 钩子返回 true 表示「应当拦下」。
func RegisterApprovalGate(fn ApprovalGate) { approvalGate = fn }

// RegisterApprovalMessage 覆盖待审核提示文案。
func RegisterApprovalMessage(msg string) {
	if msg != "" {
		approvalMessage = msg
	}
}

// RequireAuth 鉴权中间件。
//
// status 语义：1=正常放行；2=待审核——**允许登录态成立**（否则用户看到的是
// 「用户名或密码错误」，会误以为注册失败），但只放行白名单路径，其余返回
// 403（已认证但未授权），白名单由业务包通过 ApprovalGate 提供；0=停用不通过。
//
// 待审核必须在这里放行、而不是在业务路由组上门禁：挂 RequireAuth 的路由
// （含 /api/auth/me 等框架路由）如果只认 status=1，待审核用户连自己「在等
// 审核」这个事实都查不到，业务侧的白名单永远不可达（2026-10-08 实测踩到）。
func RequireAuth(jwtSecret string, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, ok := authenticate(c, jwtSecret, db)
		if !ok {
			response.ErrorUnauthorized(c, "未授权")
			c.Abort()
			return
		}
		setUserContext(c, user)

		// 待审核用户：由业务钩子判定是否放行（未注册钩子则一律放行，
		// 与基线行为一致——没有钩子说明该应用没有待审核概念）。
		if user.Status == UserStatusPending && approvalGate != nil &&
			approvalGate(db, user.ID, c.Request.URL.Path) {
			response.Error(c, http.StatusForbidden, response.CodeForbidden, approvalMessage)
			c.Abort()
			return
		}
		c.Next()
	}
}

func OptionalAuth(jwtSecret string, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if user, ok := authenticate(c, jwtSecret, db); ok {
			setUserContext(c, user)
		}
		c.Next()
	}
}

func authenticate(c *gin.Context, jwtSecret string, db *gorm.DB) (database.User, bool) {
	if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
		var user database.User
		// 与 token 分支同口径：待审核（2）也要能通过认证，白名单由 RequireAuth 收口。
		if err := db.Where("api_key = ? AND status IN ?", apiKey,
			[]int{UserStatusActive, UserStatusPending}).First(&user).Error; err == nil {
			c.Set("auth_via", AuthViaAPIKey)
			return user, true
		}
	}

	tokenString := ""
	if authHeader := c.GetHeader("Authorization"); strings.HasPrefix(authHeader, "Bearer ") {
		tokenString = strings.TrimPrefix(authHeader, "Bearer ")
	} else if cookie, err := c.Cookie("token"); err == nil {
		// 网关域上应用自己的 Authorization 送不进应用，账密登录换来的 JWT 改由
		// HttpOnly cookie（Path=网关前缀）携带，见 user.handleLogin。cookie 在
		// 这里与 Authorization 同等对待：有且有效就优先于网关隐式身份。
		tokenString = cookie
	}
	if tokenString != "" {
		if userID, _, _, authVersion, err := auth.ParseToken(tokenString, jwtSecret); err == nil {
			var user database.User
			// 接受 status IN (1,2)：2=待审核也要能建立登录态，白名单由上面的
			// RequireAuth 统一收口；0=停用仍然认不出来。
			if err := db.Where("id = ? AND status IN ?", userID,
				[]int{UserStatusActive, UserStatusPending}).
				First(&user).Error; err == nil && user.AuthVersion == authVersion {
				c.Set("auth_via", AuthViaToken)
				return user, true
			}
		}
	}

	// 飞牛统一网关：网关在转发前已经校验过 NAS 会话，并按官方约定注入
	// X-Trim-Userid / X-Trim-Username。**应用自己的 Authorization 在网关域上不
	// 保证能到达应用**——接入层会把 Authorization 当成它自己的会话 token，认不出
	// 就直接回 200 "invalid token"（请求压根进不了应用），所以网关域以「该 NAS
	// 用户已绑定的应用账号」作为登录态，这正是飞牛文档给统一网关应用的身份通道。
	// 直连端口（局域网直连、旧书签）拿不到网关头，仍然只认自己的 JWT。
	//
	// 但隐式登录态只在用户没有主动登出时成立：应用自己的登录/退出归应用管，
	// 飞牛授权只是身份来源（见 user_sessions 表与 GatewayIdentityUser）。
	if user, ok := GatewayIdentityUser(c.Request, db); ok {
		c.Set("auth_via", AuthViaGateway)
		return user, true
	}

	return database.User{}, false
}

func setUserContext(c *gin.Context, user database.User) {
	c.Set("userID", user.ID)
	c.Set("username", user.Username)
	c.Set("role", user.Role)
}

func RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != "admin" {
			response.ErrorForbidden(c, "禁止访问")
			c.Abort()
			return
		}
		c.Next()
	}
}

func Logger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		method := c.Request.Method

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		ip := c.ClientIP()

		logger.Info("%3d | %13v | %15s | %-7s %s", status, latency, ip, method, path)
	}
}
