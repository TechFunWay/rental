// version_gate_test.go — 发版前的「版本号 ≥ 最新迁移版本」自检。
//
// 为什么需要它：升级阶梯只跑 `compareVersions(迁移版本, 二进制版本) <= 0`
// 的迁移。若新增了迁移却忘了把 VERSION 提上去，迁移会被**静默跳过**——
// 全新安装看不出问题（AutoMigrate 按新模型建表），但**老库升级会坏**。
//
// 2026-10-09 实测踩到：台账迁移注册为 0.5.1 时若 VERSION 仍是 0.5.0，
// 用该包升级真 v0.5.0 老库时迁移被跳过，ledger_members 为空，
// 所有老用户的权限与数据归属全部丢失（员工从「只读」变成处处 403）。
//
// 本功能的迁移版本刻意用 **0.5.1**（不是 0.6.0）：按发版约定，未发版阶段
// 只顺延小版本号到 0.5.1，真正发版时才升 0.6.0。迁移版本必须 ≤ VERSION，
// 所以迁移也用 0.5.1。将来发 0.6.0 时：已装 0.5.1 的库迁移记录 0.5.1 ≤ 0.6.0
// 不重复跑，全新 0.6.0 库正常跑——两者都不受影响。
// 这个测试就是「迁移版本与 VERSION 脱节」那次事故的护栏。
package rental

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"smallgo/server/database"
)

// cmpVersion 与 database.compareVersions 同口径（按 . 分段数值比较，
// 忽略 v/V 前缀）。这里复刻一份，因为框架那份未导出。
func cmpVersion(a, b string) int {
	a = strings.TrimPrefix(strings.TrimPrefix(a, "v"), "V")
	b = strings.TrimPrefix(strings.TrimPrefix(b, "v"), "V")
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		ai, bi := 0, 0
		if i < len(as) {
			ai, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			bi, _ = strconv.Atoi(bs[i])
		}
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}

// latestUpgradeVersion 返回已注册迁移里的最高版本号。
func latestUpgradeVersion() string {
	latest := ""
	for _, u := range database.Upgrades {
		if latest == "" || cmpVersion(u.Version, latest) > 0 {
			latest = u.Version
		}
	}
	return latest
}

// readRepoVersion 读仓库根的 VERSION 文件（发版时注入 ldflags 的那个）。
func readRepoVersion(t *testing.T) string {
	t.Helper()
	// 测试的工作目录是 server/rental，仓库根在上两级。
	for _, rel := range []string{"../../VERSION", "../../../VERSION"} {
		b, err := os.ReadFile(filepath.Clean(rel))
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	t.Fatalf("找不到 VERSION 文件（从 %s 向上找了两级）", filepath.Dir("."))
	return ""
}

// TestVersionCoversLatestUpgrade 断言 VERSION 不低于最新迁移版本。
//
// 失败意味着：这个包发出去时，最新那条迁移不会执行，老库升级会漏数据。
// 修法：把 VERSION（以及 app.json / fnpack/manifest）提到迁移版本。
func TestVersionCoversLatestUpgrade(t *testing.T) {
	latest := latestUpgradeVersion()
	if latest == "" {
		t.Skip("没有注册任何迁移")
	}
	repo := readRepoVersion(t)
	if repo == "" {
		t.Fatal("VERSION 为空")
	}
	if cmpVersion(repo, latest) < 0 {
		t.Fatalf("VERSION=%s 低于最新迁移版本 %s：该迁移在发版后**不会执行**，"+
			"老库升级会丢失数据归属与权限。请把 VERSION、app.json、fnpack/manifest "+
			"一并提到 %s。", repo, latest, latest)
	}
	t.Logf("VERSION=%s ≥ 最新迁移 %s，升级阶梯会执行全部迁移", repo, latest)
}
