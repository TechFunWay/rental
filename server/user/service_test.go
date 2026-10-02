package user

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/auth"
	"smallgo/server/database"
	"smallgo/server/middleware"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const testJWTSecret = "test-jwt-secret"

func setupUserDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.InitDB(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseDB(db) })
	if err := database.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func createTestUser(t *testing.T, db *gorm.DB, username, password string) database.User {
	t.Helper()
	hashed, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	user := database.User{Username: username, Password: hashed, Role: "user", Status: 1, APIKey: "key-" + username, AuthVersion: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func jsonRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// gatewayRequest 模拟一次从飞牛网关 unix socket 进来的请求：ConnContext 打上
// 网关标记并注入 X-Trim-* 身份头。
func gatewayRequest(method, target, body string, uid int, nasUsername string) *http.Request {
	req := jsonRequest(method, target, body)
	if uid > 0 {
		req.Header.Set("X-Trim-Userid", itoa(uid))
		req.Header.Set("X-Trim-Username", nasUsername)
	}
	return req.WithContext(middleware.MarkFnOSGateway(context.Background()))
}

func perform(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func mustCode(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(w.Body.String(), `"code":0`) {
		t.Fatalf("expected code 0, got status %d body %s", w.Code, w.Body.String())
	}
}

func userByUsername(t *testing.T, db *gorm.DB, username string) database.User {
	t.Helper()
	var user database.User
	if err := db.Where("username = ?", username).First(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

// TestBindFnOSAllowsReloginOfBoundAccountWhileSuppressed：NAS 用户曾绑定账号并
// 主动退出（抑制标记生效）后，凭同一账号的账密重新登录必须成功——绑定关系还在
// 不等于「已绑定其他应用账号」。
func TestBindFnOSAllowsReloginOfBoundAccountWhileSuppressed(t *testing.T) {
	db := setupUserDB(t)
	user := createTestUser(t, db, "alice", "secret123")
	uid := uint(42)
	if err := db.Model(&user).Updates(map[string]interface{}{"fn_os_user_id": uid, "fn_os_username": "alice"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := LogoutApplicationSession(db, user.ID); err != nil {
		t.Fatal(err)
	}

	identity := FnOSIdentity{UserID: uid, Username: "alice"}
	result, err := BindFnOSAccount(db, identity, "alice", "secret123", "bind", testJWTSecret)
	if err != nil {
		t.Fatalf("expected relogin of own bound account to succeed, got %v", err)
	}
	if result["token"] == "" {
		t.Fatal("expected token in result")
	}

	var session database.UserSession
	if err := db.Where("user_id = ?", user.ID).First(&session).Error; err != nil {
		t.Fatal(err)
	}
	if session.Suppressed {
		t.Fatal("explicit relogin must clear the suppression marker")
	}
}

// TestBindFnOSRejectsDifferentAppAccount：NAS 用户已绑定 alice 时，再用 bob 的
// 账密绑定必须被拒绝。
func TestBindFnOSRejectsDifferentAppAccount(t *testing.T) {
	db := setupUserDB(t)
	alice := createTestUser(t, db, "alice", "secret123")
	createTestUser(t, db, "bob", "secret456")
	uid := uint(42)
	if err := db.Model(&alice).Updates(map[string]interface{}{"fn_os_user_id": uid, "fn_os_username": "alice"}).Error; err != nil {
		t.Fatal(err)
	}

	identity := FnOSIdentity{UserID: uid, Username: "bob"}
	if _, err := BindFnOSAccount(db, identity, "bob", "secret456", "bind", testJWTSecret); err == nil {
		t.Fatal("expected bind to be rejected, got success")
	} else if !errors.Is(err, ErrFnOSAlreadyBound) {
		t.Fatalf("expected ErrFnOSAlreadyBound, got %v", err)
	}
}

// TestBindFnOSRejectsAccountBoundToOtherNasUser：应用账号已绑定其他 NAS 用户时
// 不能再被当前 NAS 用户绑定。
func TestBindFnOSRejectsAccountBoundToOtherNasUser(t *testing.T) {
	db := setupUserDB(t)
	alice := createTestUser(t, db, "alice", "secret123")
	bound := uint(7)
	if err := db.Model(&alice).Updates(map[string]interface{}{"fn_os_user_id": bound, "fn_os_username": "someone-else"}).Error; err != nil {
		t.Fatal(err)
	}

	identity := FnOSIdentity{UserID: uint(42), Username: "bob"}
	_, err := BindFnOSAccount(db, identity, "alice", "secret123", "bind", testJWTSecret)
	if err == nil || !strings.Contains(err.Error(), "已绑定其他飞牛") {
		t.Fatalf("expected cross-NAS binding rejection, got %v", err)
	}
}

// TestGatewayRegisterKeepsBindingUntouched：注册只创建应用账号，不读取、不绑定
// 网关注入的飞牛身份——哪怕该飞牛账号已经绑给了别的应用账号。曾因「注册即绑定」
// 把无关的账密注册挡死在「此飞牛 NAS 账号已绑定其他应用账号」上。
func TestGatewayRegisterKeepsBindingUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserDB(t)
	createTestUser(t, db, "admin", "admin-pass") // 应用已初始化，carol 是后来者
	// 飞牛账号 42 已绑定管理员。
	admin := userByUsername(t, db, "admin")
	if err := db.Model(&admin).Updates(map[string]interface{}{"fn_os_user_id": uint(42), "fn_os_username": "nas-admin"}).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/api/auth/register", handleRegister(db, "/app/techfunway-rental"))

	w := perform(r, gatewayRequest("POST", "/api/auth/register", `{"username":"carol","password":"secret123"}`, 42, "nas-admin"))
	mustCode(t, w)

	carol := userByUsername(t, db, "carol")
	if carol.FnOSUserID != nil {
		t.Fatalf("gateway register must not touch fn_os_user_id, got %v", *carol.FnOSUserID)
	}
	if carol.Role != "user" {
		t.Fatalf("non-first user should be role=user, got %q", carol.Role)
	}
}

// TestGatewayLoginIgnoresConflictingBinding：用户报的 bug——飞牛账号已绑定管理
// 员时，普通账号的账密在网关域登录必须照常成功，而不是被「此飞牛 NAS 账号已
// 绑定其他应用账号」挡死；同时既有绑定不得被改动或转移。
func TestGatewayLoginIgnoresConflictingBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserDB(t)
	createTestUser(t, db, "weiyi", "admin-pass")
	createTestUser(t, db, "weiyi2", "user-pass")
	admin := userByUsername(t, db, "weiyi")
	if err := db.Model(&admin).Updates(map[string]interface{}{"fn_os_user_id": uint(42), "fn_os_username": "nas-admin"}).Error; err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.POST("/api/auth/login", handleLogin(db, "/app/techfunway-rental"))

	w := perform(r, gatewayRequest("POST", "/api/auth/login", `{"username":"weiyi2","password":"user-pass"}`, 42, "nas-admin"))
	mustCode(t, w)

	admin = userByUsername(t, db, "weiyi")
	if admin.FnOSUserID == nil || *admin.FnOSUserID != 42 {
		t.Fatalf("existing binding must stay with weiyi, got %v", admin.FnOSUserID)
	}
	other := userByUsername(t, db, "weiyi2")
	if other.FnOSUserID != nil {
		t.Fatalf("password login must not bind fn_os_user_id, got %v", *other.FnOSUserID)
	}
}

// TestGatewayPasswordLoginIssuesSessionCookie：网关域上账密登录/注册要种下应用
// 会话 cookie（Path=网关前缀、HttpOnly）——应用自己的 JWT 在该域上没有别的传输
// 通道；直连端口则不种，会话照旧走 Authorization。
func TestGatewayPasswordLoginIssuesSessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserDB(t)
	createTestUser(t, db, "alice", "secret123")
	r := gin.New()
	r.POST("/api/auth/login", handleLogin(db, "/app/techfunway-rental"))

	w := perform(r, gatewayRequest("POST", "/api/auth/login", `{"username":"alice","password":"secret123"}`, 42, "nas-admin"))
	mustCode(t, w)
	cookie := sessionCookie(t, w)
	if cookie.Path != "/app/techfunway-rental" || !cookie.HttpOnly || cookie.Value == "" {
		t.Fatalf("gateway login must issue app session cookie, got %#v", cookie)
	}

	// 直连端口不种 cookie。
	direct := gin.New()
	direct.POST("/api/auth/login", handleLogin(db, ""))
	w = perform(direct, jsonRequest("POST", "/api/auth/login", `{"username":"alice","password":"secret123"}`))
	mustCode(t, w)
	if len(w.Result().Cookies()) != 0 {
		t.Fatalf("direct-port login must not set cookies, got %v", w.Result().Cookies())
	}
}

// TestGatewayPasswordLoginCookieKeepsSession：网关域上拿着账密登录种下的
// cookie（不带 Authorization）就能维持应用会话，且会话归属是应用自己。
func TestGatewayPasswordLoginCookieKeepsSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserDB(t)
	createTestUser(t, db, "alice", "secret123")
	r := gin.New()
	secret := getJWTSecret(db) // handleLogin 签发 token 用的是库里的 jwt_secret
	r.GET("/api/auth/check", middleware.OptionalAuth(secret, db), func(c *gin.Context) {
		userID, _ := c.Get("userID")
		c.JSON(http.StatusOK, gin.H{"userID": userID})
	})
	login := gin.New()
	login.POST("/api/auth/login", handleLogin(db, "/app/techfunway-rental"))
	w := perform(login, gatewayRequest("POST", "/api/auth/login", `{"username":"alice","password":"secret123"}`, 42, "nas-admin"))
	mustCode(t, w)

	req := jsonRequest("GET", "/api/auth/check", "")
	req.AddCookie(sessionCookie(t, w))
	req = req.WithContext(middleware.MarkFnOSGateway(context.Background()))
	req.Header.Set("X-Trim-Userid", "999") // 别的 NAS 用户，绝不能认成它
	req.Header.Set("X-Trim-Username", "someone-else")
	w = perform(r, req)
	if got := w.Body.String(); !strings.Contains(got, `"userID":1`) {
		t.Fatalf("cookie session must authenticate as alice(id=1), got %s", got)
	}
}

// sessionCookie 取登录响应种下的应用会话 cookie。
func sessionCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "token" {
			return cookie
		}
	}
	t.Fatalf("no token cookie in response, headers: %v", w.Header().Values("Set-Cookie"))
	return nil
}

// TestDirectPortAuthLeavesBindingUnset：直连端口的注册与登录靠应用自己的 JWT
// 维持会话，不应碰 fn_os_user_id 绑定。
func TestDirectPortAuthLeavesBindingUnset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupUserDB(t)
	r := gin.New()
	r.POST("/api/auth/register", handleRegister(db, ""))
	r.POST("/api/auth/login", handleLogin(db, ""))

	w := perform(r, jsonRequest("POST", "/api/auth/register", `{"username":"dave","password":"secret123"}`))
	mustCode(t, w)
	w = perform(r, jsonRequest("POST", "/api/auth/login", `{"username":"dave","password":"secret123"}`))
	mustCode(t, w)

	dave := userByUsername(t, db, "dave")
	if dave.FnOSUserID != nil {
		t.Fatalf("direct-port auth must not set fn_os_user_id, got %v", *dave.FnOSUserID)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
