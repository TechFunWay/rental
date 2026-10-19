// Package update 提供「检查新版本」能力：查询发布渠道的最新版本，
// 与当前运行版本做 semver 比较，供前端展示升级提示条。
//
// 发布渠道有两个（2026-10-11 粉丝反馈"要群里通知才知道更新"）：
//   - Gitee（国内可达，优先）：gitee.com/TechFunWay/rental
//   - GitHub（兜底）：github.com/TechFunWay/rental
// 任一命中即用；全失败优雅降级（has_update=false），绝不影响正常使用。
// 检查结果缓存 24 小时，避免频繁外呼。
//
// 飞牛应用本体无法自我自动更新（fpk 由系统应用中心管理，应用无权替换自身），
// 本包只负责"发现新版本并引导下载"；真·自动更新只有 Docker + Watchtower 路径。
package update

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"smallgo/server/config"
	"smallgo/server/response"
	"smallgo/server/version"
)

const cacheTTL = 24 * time.Hour

var httpClient = &http.Client{Timeout: 5 * time.Second}

// 发布仓库约定（app-release 技能：GitHub/Gitee 同名仓库 TechFunWay/<应用名>，
// 每次发版两处都要建 Release 并传产物，fpk 附件名形如 techfunway-rental_v*_x86.fpk）。
const (
	giteeAPIURL  = "https://gitee.com/api/v5/repos/TechFunWay/rental/releases/latest"
	githubAPIURL = "https://api.github.com/repos/TechFunWay/rental/releases/latest"
	giteePageURL = "https://gitee.com/TechFunWay/rental/releases"
	githubPageURL = "https://github.com/TechFunWay/rental/releases"
)

// GiteeAPIURL/GitHubAPIURL 返回两个源的查询地址（测试可用环境变量覆盖）。
func GiteeAPIURL() string {
	if u := os.Getenv("UPDATE_CHECK_GITEE_URL"); u != "" {
		return u
	}
	return giteeAPIURL
}

// GitHubAPIURL 兼容旧覆盖变量名 UPDATE_CHECK_URL（历史测试与自建渠道用）。
func GitHubAPIURL() string {
	if u := os.Getenv("UPDATE_CHECK_URL"); u != "" {
		return u
	}
	return githubAPIURL
}

type cacheEntry struct {
	info Info
	checkedAt time.Time
}

var (
	mu    sync.Mutex
	cache *cacheEntry
)

// releaseAsset 发行版附件（两平台同构字段：名称 + 下载直链）。
type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// release 是两平台响应中用到的字段（Gitee v5 与 GitHub API 同构）。
type release struct {
	TagName string         `json:"tag_name"`
	HTMLURL string         `json:"html_url"`
	Assets  []releaseAsset `json:"assets"`
}

// FpkAsset 飞牛安装包直链（x86/arm64 各一个，名字里带架构）。
type FpkAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Info 是版本检查的 API 视图。
type Info struct {
	Current     string     `json:"current"`
	Latest      string     `json:"latest"`
	DownloadURL string     `json:"download_url"` // 命中源的发行版页面
	HasUpdate   bool       `json:"has_update"`
	Source      string     `json:"source"`     // gitee / github（命中源）
	GiteeURL    string     `json:"gitee_url"`  // 两平台发行版列表页（恒给，前端双入口）
	GitHubURL   string     `json:"github_url"`
	FpkAssets   []FpkAsset `json:"fpk_assets"` // 命中源里的 .fpk 附件直链
	// FnOSInstall 表示当前进程跑在飞牛安装形态（-fnos-app）：一键更新装的是
	// .fpk（含网关基址前端），否则装发行包（根基址 www）。前端据此说明「点更新
	// 会装什么」，与后端 pickUpdateAsset 的判断同源，不靠前端自己猜。
	FnOSInstall bool `json:"fnos_install"`
	// assets 命中源的全部附件（含 .tar.gz 更新包），仅供同包的一键更新选包，
	// 不下发给前端（前端只需要 fpk_assets 与页面链接）。
	assets []releaseAsset `json:"-"`
}

// Check 查询最新版本并比较：Gitee 优先、GitHub 兜底，任一命中即用。
func Check(current string) Info {
	mu.Lock()
	defer mu.Unlock()
	if cache != nil && time.Since(cache.checkedAt) < cacheTTL {
		info := cache.info
		info.Current = current
		info.FnOSInstall = config.C.FnOSApp
		info.HasUpdate = IsNewer(info.Latest, current)
		return info
	}

	type source struct {
		name string
		url  string
	}
	sources := []source{
		{"gitee", GiteeAPIURL()},
		{"github", GitHubAPIURL()},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	for _, s := range sources {
		info, ok := fetchRelease(client, s.url)
		if !ok {
			continue
		}
		info.Source = s.name
		info.GiteeURL = giteePageURL
		info.GitHubURL = githubPageURL
		info.Current = current
		info.FnOSInstall = config.C.FnOSApp
		info.HasUpdate = IsNewer(info.Latest, current)
		cache = &cacheEntry{info: info, checkedAt: time.Now()}
		return info
	}
	return Info{Current: current, FnOSInstall: config.C.FnOSApp}
}

// fetchRelease 拉取并解析单个源的 latest 发行版；任何失败返回 ok=false。
func fetchRelease(client *http.Client, url string) (Info, bool) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Info{}, false
	}
	req.Header.Set("User-Agent", version.AppName+"/"+version.Version)
	resp, err := client.Do(req)
	if err != nil {
		return Info{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Info{}, false
	}
	var rel release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil || rel.TagName == "" {
		return Info{}, false
	}
	info := Info{Latest: rel.TagName, DownloadURL: rel.HTMLURL, assets: rel.Assets}
	for _, a := range rel.Assets {
		if strings.HasSuffix(strings.ToLower(a.Name), ".fpk") {
			info.FpkAssets = append(info.FpkAssets, FpkAsset{Name: a.Name, URL: a.BrowserDownloadURL})
		}
	}
	return info, true
}

// RegisterRoutes 挂载需要登录的版本检查端点。
func RegisterRoutes(auth *gin.RouterGroup) {
	auth.GET("/version/check", func(c *gin.Context) {
		response.Success(c, Check(version.Version))
	})
}

var semverPart = regexp.MustCompile(`^\d+`)

// ParseVersion 把 "v1.2.3" 解析为 [1,2,3]，非数字段按 0 处理。
func ParseVersion(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.SplitN(v, ".", 3)
	var nums [3]int
	for i, p := range parts {
		if m := semverPart.FindString(p); m != "" {
			n, err := strconv.Atoi(m)
			if err == nil {
				nums[i] = n
			}
		}
	}
	return nums
}

// IsNewer 判断 latest 是否比 current 更新（逐位比较，缺省段补 0）。
func IsNewer(latest, current string) bool {
	if latest == "" || current == "" {
		return false
	}
	l, c := ParseVersion(latest), ParseVersion(current)
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// InvalidateCache 清空检查缓存（测试用）。
func InvalidateCache() {
	mu.Lock()
	defer mu.Unlock()
	cache = nil
}
