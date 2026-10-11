package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.2.0", "v0.1.2", true},
		{"0.2.0", "0.1.99", true},
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v1.0.1", false},
		{"v1.0", "v1.0.0", false},  // 缺省段补 0
		{"v1.0.0", "v1.0", false},  // 缺省段补 0
		{"v2.0", "v1.9.9", true},   // 段数不同也能比较
		{"", "v1.0.0", false},
		{"v1.0.0", "", false},
		{"release-2026", "v1.0.0", false}, // 非数字前缀按 0
		{"v0.1.2-beta", "v0.1.1", true},   // 取数字段
	}
	for _, c := range cases {
		if got := IsNewer(c.latest, c.current); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// overrideSources 把两个源都指向 fake（单测不许出网，双源覆盖防止漏网真实外呼）。
func overrideSources(t *testing.T, giteeURL, githubURL string) {
	t.Helper()
	t.Setenv("UPDATE_CHECK_GITEE_URL", giteeURL)
	t.Setenv("UPDATE_CHECK_URL", githubURL)
}

func TestCheckWithFakeReleaseServer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// fakeRelease 返回一个 release JSON（含可选 fpk 附件），并统计命中次数
	newFake := func(t *testing.T, tag string, calls *atomic.Int32, fpk ...string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			rel := release{TagName: tag, HTMLURL: "https://example.com/rel"}
			for _, name := range fpk {
				rel.Assets = append(rel.Assets, releaseAsset{Name: name, BrowserDownloadURL: "https://example.com/" + name})
			}
			_ = json.NewEncoder(w).Encode(rel)
		}))
	}

	t.Run("gitee 优先命中", func(t *testing.T) {
		InvalidateCache()
		var giteeCalls, githubCalls atomic.Int32
		gitee := newFake(t, "v9.9.9", &giteeCalls)
		github := newFake(t, "v8.8.8", &githubCalls)
		defer gitee.Close()
		defer github.Close()
		overrideSources(t, gitee.URL, github.URL)

		got := Check("v0.1.2")
		if !got.HasUpdate || got.Latest != "v9.9.9" || got.Source != "gitee" {
			t.Fatalf("Check() = %+v", got)
		}
		if giteeCalls.Load() != 1 || githubCalls.Load() != 0 {
			t.Fatalf("gitee calls=%d github calls=%d，应只命中 gitee", giteeCalls.Load(), githubCalls.Load())
		}
		// 缓存生效：第二次调用不再外呼
		Check("v0.1.2")
		if giteeCalls.Load() != 1 {
			t.Fatalf("cache miss: upstream calls = %d, want 1", giteeCalls.Load())
		}
	})

	t.Run("gitee 失败回退 github", func(t *testing.T) {
		InvalidateCache()
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden) // gitee 限流/失效常见
		}))
		defer dead.Close()
		var githubCalls atomic.Int32
		github := newFake(t, "v8.8.8", &githubCalls)
		defer github.Close()
		overrideSources(t, dead.URL, github.URL)

		got := Check("v0.1.2")
		if !got.HasUpdate || got.Source != "github" || got.Latest != "v8.8.8" {
			t.Fatalf("Check() = %+v, want github fallback", got)
		}
	})

	t.Run("fpk 附件直链解析", func(t *testing.T) {
		InvalidateCache()
		var calls atomic.Int32
		gitee := newFake(t, "v9.9.9", &calls,
			"techfunway-rental_v9.9.9_x86.fpk", "techfunway-rental_v9.9.9_arm64.fpk", "docker-compose.latest.yml")
		github := newFake(t, "v8.8.8", &calls)
		defer gitee.Close()
		defer github.Close()
		overrideSources(t, gitee.URL, github.URL)

		got := Check("v0.1.2")
		if len(got.FpkAssets) != 2 {
			t.Fatalf("fpk assets = %+v, want 2（compose 不算 fpk）", got.FpkAssets)
		}
		if got.FpkAssets[0].URL != "https://example.com/techfunway-rental_v9.9.9_x86.fpk" {
			t.Fatalf("fpk url = %+v", got.FpkAssets[0])
		}
	})

	t.Run("双源全失败优雅降级", func(t *testing.T) {
		InvalidateCache()
		dead1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer dead1.Close()
		dead2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer dead2.Close()
		overrideSources(t, dead1.URL, dead2.URL)

		got := Check("v1.0.0")
		if got.HasUpdate || got.Current != "v1.0.0" {
			t.Fatalf("Check() = %+v, want graceful degradation", got)
		}
	})

	t.Run("no update", func(t *testing.T) {
		InvalidateCache()
		var calls atomic.Int32
		fake := newFake(t, "v0.0.1", &calls)
		defer fake.Close()
		overrideSources(t, fake.URL, fake.URL)

		if got := Check("v1.0.0"); got.HasUpdate {
			t.Fatalf("Check() = %+v, want no update", got)
		}
	})

	t.Run("route responds", func(t *testing.T) {
		InvalidateCache()
		var calls atomic.Int32
		fake := newFake(t, "v9.9.9", &calls)
		defer fake.Close()
		overrideSources(t, fake.URL, fake.URL)

		r := gin.New()
		RegisterRoutes(r.Group("/api"))
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/version/check", nil))
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", resp.Code, resp.Body.String())
		}
		var body struct {
			Code int `json:"code"`
			Data struct {
				HasUpdate bool   `json:"has_update"`
				Source    string `json:"source"`
			} `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body.Code != 0 || !body.Data.HasUpdate || body.Data.Source != "gitee" {
			t.Fatalf("body = %s", resp.Body.String())
		}
	})
}
