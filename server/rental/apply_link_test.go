// apply_link_test.go — 租客登记链接基址（2026-10-12 粉丝反馈：飞牛上打开提示
// invalid token）。
//
// 事实：飞牛统一网关路径 /app/techfunway-rental/… 会先在接入层校验 NAS 登录态，
// 未登录请求直接被 nginx 拦成纯文本 "invalid token"，请求到不了应用。租客没有
// NAS 账号，所以登记链接必须指向应用自己的直连地址（不校验 NAS 会话）。
package rental

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"smallgo/server/config"
	"smallgo/server/middleware"
	ginserver "smallgo/server/server"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const testGatewayPrefix = "/app/techfunway-rental"

// withFnOSConfig 临时把进程级配置切成飞牛模式：applyLinkBase 从 config.C 读
// 端口与网关前缀。
func withFnOSConfig(t *testing.T, port int) {
	t.Helper()
	old := config.C
	config.C = config.Config{FnOSApp: true, GatewayPrefix: testGatewayPrefix, Port: port}
	t.Cleanup(func() { config.C = old })
}

// newGatewayTestRouter 建一个飞牛模式的路由（业务路由同样挂在网关前缀下，
// 与生产一致）。
func newGatewayTestRouter(t *testing.T, db *gorm.DB) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	secret, err := sysconfig.GetConfig(db, "jwt_secret", 0)
	if err != nil || secret == "" {
		t.Fatalf("jwt secret: %v", err)
	}
	return ginserver.NewRouter(config.Config{
		FnOSApp: true, GatewayPrefix: testGatewayPrefix, Port: 8910,
		CORSOrigin: "*", RateLimit: 0,
	}, db, secret)
}

// doGatewayJSON 发一个「从飞牛网关 socket 进来」的请求：带上网关标记与
// 房东浏览器里的 Host（网关域名，如 https://172.18.99.151:5643）。
func doGatewayJSON(r http.Handler, method, path, token, host string, body interface{}) *httptest.ResponseRecorder {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Host = host
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req = req.WithContext(middleware.MarkFnOSGateway(req.Context()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// seedRoom 直接落库一间房（挂在管理员个人台账下，管理员有完全权限）。
func seedRoom(t *testing.T, env *testEnv, roomNo string) uint {
	t.Helper()
	room := Room{UserID: env.adminID, LedgerID: env.personalLedger, Community: "测试小区", RoomNo: roomNo}
	if err := env.db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	return room.ID
}

// applyLinkData 取登记链接接口的出参。
func applyLinkData(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("apply-link 失败: %d %s", w.Code, w.Body.String())
	}
	data, ok := decodeBody(t, w)["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data 不是对象: %s", w.Body.String())
	}
	return data
}

// TestApplyLinkOnGatewayUsesDirectAddress：网关域上必须换成应用直连地址
// （飞牛网关路径租客打不开），主机沿用房东当前的 Host、端口换成服务端口。
func TestApplyLinkOnGatewayUsesDirectAddress(t *testing.T) {
	env := setupEnv(t)
	withFnOSConfig(t, 8910)
	r := newGatewayTestRouter(t, env.db)
	roomID := seedRoom(t, env, "2201")

	w := doGatewayJSON(r, http.MethodPost, testGatewayPrefix+"/api/rental/rooms/"+itoa(roomID)+"/apply-link",
		env.token, "172.18.99.151:5643", nil)
	data := applyLinkData(t, w)

	token := data["token"].(string)
	want := "http://172.18.99.151:8910" + testGatewayPrefix + "/apply/" + token
	if got := data["link"].(string); got != want {
		t.Fatalf("网关域上的登记链接 = %q，期望直连地址 %q", got, want)
	}
	if data["via_gateway"] != true {
		t.Fatalf("via_gateway 应为 true，实得 %v", data["via_gateway"])
	}
	if data["configured"] != false {
		t.Fatalf("未配置时应为 configured=false，实得 %v", data["configured"])
	}
	// 关键回归：链接里不能出现网关端口/网关路径叠加（5643 是飞牛网关入口）。
	if strings.Contains(data["link"].(string), ":5643") {
		t.Fatalf("登记链接不能指向飞牛网关：%q", data["link"])
	}
}

// TestApplyLinkHonorsConfiguredBase：管理员配置了基址就照用；只填域名端口时
// 自动补上网关前缀（飞牛模式下应用路由都在前缀下）。
func TestApplyLinkHonorsConfiguredBase(t *testing.T) {
	env := setupEnv(t)
	withFnOSConfig(t, 8910)
	r := newGatewayTestRouter(t, env.db)
	roomID := seedRoom(t, env, "2202")

	set := func(v string) map[string]interface{} {
		t.Helper()
		if err := sysconfig.UpdateConfig(env.db, applyBaseConfigKey, v, 0); err != nil {
			t.Fatalf("set apply base: %v", err)
		}
		w := doGatewayJSON(r, http.MethodPost, testGatewayPrefix+"/api/rental/rooms/"+itoa(roomID)+"/apply-link",
			env.token, "172.18.99.151:5643", nil)
		return applyLinkData(t, w)
	}

	// 只填域名+端口（末尾还有斜杠）：补前缀、去尾斜杠。
	data := set("https://zufang.example.com:8443/")
	token := data["token"].(string)
	want := "https://zufang.example.com:8443" + testGatewayPrefix + "/apply/" + token
	if got := data["link"].(string); got != want {
		t.Fatalf("配置基址后的链接 = %q，期望 %q", got, want)
	}
	if data["configured"] != true {
		t.Fatalf("配置后 configured 应为 true，实得 %v", data["configured"])
	}

	// 自带路径的基址原样使用（管理员自己决定怎么反代）。
	data = set("https://zufang.example.com/zufang")
	token = data["token"].(string)
	want = "https://zufang.example.com/zufang/apply/" + token
	if got := data["link"].(string); got != want {
		t.Fatalf("带路径基址的链接 = %q，期望 %q", got, want)
	}
}

// TestApplyLinkOnDirectPortKeepsOrigin：房东本来就是直连端口访问的 → 用当前
// 访问地址（带应用基址），不额外汇总。
func TestApplyLinkOnDirectPortKeepsOrigin(t *testing.T) {
	env := setupEnv(t)
	withFnOSConfig(t, 8910)
	r := newGatewayTestRouter(t, env.db)
	roomID := seedRoom(t, env, "2203")

	w := doJSON(r, http.MethodPost, testGatewayPrefix+"/api/rental/rooms/"+itoa(roomID)+"/apply-link", env.token, nil)
	data := applyLinkData(t, w)

	// httptest 默认 Host 是 example.com。
	want := "http://example.com" + testGatewayPrefix + "/apply/" + data["token"].(string)
	if got := data["link"].(string); got != want {
		t.Fatalf("直连端口上的登记链接 = %q，期望 %q", got, want)
	}
	if data["via_gateway"] != false {
		t.Fatalf("直连端口 via_gateway 应为 false，实得 %v", data["via_gateway"])
	}
}

// TestApplyLinkWithoutFnOSModeHasNoPrefix：Docker/普通部署（非飞牛模式）基址
// 就是站点根，不补网关前缀。
func TestApplyLinkWithoutFnOSModeHasNoPrefix(t *testing.T) {
	env := setupEnv(t)
	old := config.C
	config.C = config.Config{}
	t.Cleanup(func() { config.C = old })

	roomID := seedRoom(t, env, "2204")
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms/"+itoa(roomID)+"/apply-link", env.token, nil)
	data := applyLinkData(t, w)

	want := "http://example.com/apply/" + data["token"].(string)
	if got := data["link"].(string); got != want {
		t.Fatalf("普通部署的登记链接 = %q，期望 %q", got, want)
	}
}

// TestUrlPathEmpty：基址路径判定（决定要不要补网关前缀）。
func TestUrlPathEmpty(t *testing.T) {
	for _, c := range []struct {
		in   string
		want bool
	}{
		{"http://host:8910", true},
		{"http://host:8910/", true},
		{"https://zufang.example.com/zufang", false},
		{"https://zufang.example.com/app/techfunway-rental", false},
	} {
		if got := urlPathEmpty(c.in); got != c.want {
			t.Errorf("urlPathEmpty(%q) = %v，期望 %v", c.in, got, c.want)
		}
	}
}

// TestGatewayDirectHostKeepsDomain：Host 是域名（DDNS / fn Connect 域名 / 反代
// 域名）时**直接用域名**——域名本身就是公网可达的入口，早先一律退化成本机
// 局域网 IP 会让外网租客必打不开，而浏览器里就带着可用域名。
func TestGatewayDirectHostKeepsDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct {
		name string
		host string
		want string
	}{
		{"DDNS 域名带端口", "m5plus.wycto.cc:5643", "m5plus.wycto.cc"},
		{"裸域名无端口", "zufang.example.com", "zufang.example.com"},
		{"IPv4 照用", "172.18.99.151:5643", "172.18.99.151"},
		{"IPv4 无端口", "192.168.1.10", "192.168.1.10"},
		{"IPv6 照用", "[fe80::1]:5643", "fe80::1"},
		{"localhost 视为本机", "localhost:5643", ""},
		{".local 域名视为本机", "nas.local:5643", ""},
		{"空 Host", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			ctx.Request.Host = c.host

			if got := gatewayDirectHost(ctx); got != c.want {
				t.Fatalf("gatewayDirectHost(%q) = %q，期望 %q", c.host, got, c.want)
			}
		})
	}
}

// TestApplyLinkOnGatewayKeepsDomain：房东用 DDNS 域名访问飞牛桌面时，登记链接
// 必须沿用该域名（端口换成应用端口），不能退化成本机内网 IP。
func TestApplyLinkOnGatewayKeepsDomain(t *testing.T) {
	env := setupEnv(t)
	withFnOSConfig(t, 8910)
	r := newGatewayTestRouter(t, env.db)
	roomID := seedRoom(t, env, "2205")

	w := doGatewayJSON(r, http.MethodPost, testGatewayPrefix+"/api/rental/rooms/"+itoa(roomID)+"/apply-link",
		env.token, "m5plus.wycto.cc:5643", nil)
	data := applyLinkData(t, w)

	want := "http://m5plus.wycto.cc:8910" + testGatewayPrefix + "/apply/" + data["token"].(string)
	if got := data["link"].(string); got != want {
		t.Fatalf("网关域（域名）上的登记链接 = %q，期望 %q", got, want)
	}
	// 不能夹带私有 IP：那是租客打不开的内网地址。
	u, err := url.Parse(data["link"].(string))
	if err != nil {
		t.Fatalf("解析链接失败：%v", err)
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isPrivateIP(ip) {
		t.Fatalf("登记链接基址退化成内网 IP：%q", data["base"])
	}
}

// isPrivateIP 私有/回环/链路本地 IPv4（测试断言用）。
func isPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		return ip4[0] == 10 || ip4[0] == 127 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168) ||
			(ip4[0] == 169 && ip4[1] == 254)
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast()
}
