// autobill_test.go — 到期自动生成账单（Gitee issue IKW41H 需求 4）的回归测试。
//
// 验收标准来自粉丝原话：「租房交租日期到了，不管是否抄表，都生成交租账单」，
// 因此三条口径必须钉死：
//   - 关闭（默认）时一张单都不出，升级后老用户行为不变；
//   - 按缴费日模式：缴费日当天出单，提前一天不出；重复执行不重复出单；
//   - 每月固定日模式：只在设定的日子出账，其余日子不出。
//
// 时间用固定值（2026-11 月）而不是 time.Now()，避免用例在月末/月初偶发失败。
package rental

import (
	"net/http"
	"testing"
	"time"
)

// seedActiveTenant 登记一位在租租户（不建首期账单，模拟升级前的老数据）。
func seedActiveTenant(t *testing.T, env *testEnv, roomID uint, name string, payDay int) {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": name,
		"move_in_date": "2026-10-01", "pay_day": payDay,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}
}

// countBills 该房某账期的账单数量。
func countBills(t *testing.T, env *testEnv, roomID uint, period string) int {
	t.Helper()
	return len(billsOfRoom(t, env, roomID, period))
}

// TestAutoBillOffByDefault 默认关闭：即使缴费日已到，也不出任何账单。
func TestAutoBillOffByDefault(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "A01", 1200)
	seedActiveTenant(t, env, roomID, "张三", 5)

	runAutoBill(env.db, time.Date(2026, 11, 5, 0, 30, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 0 {
		t.Fatalf("默认关闭时不应出账，实得 %d 张", got)
	}
}

// TestAutoBillByPayDay 按缴费日：缴费日当天出单、提前不出、重跑幂等。
func TestAutoBillByPayDay(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "A02", 1500)
	seedActiveTenant(t, env, roomID, "李四", 5)
	if err := setLedgerConfigValue(env.db, autobillModeConfigKey, autobillModePayDay, env.personalLedger); err != nil {
		t.Fatalf("开启按缴费日自动出账: %v", err)
	}

	// 4 号：还没到缴费日，不出单。
	runAutoBill(env.db, time.Date(2026, 11, 4, 0, 30, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 0 {
		t.Fatalf("未到缴费日不应出账，实得 %d 张", got)
	}

	// 5 号：到日子，出 2026-11 一张，租金按默认月租快照。
	runAutoBill(env.db, time.Date(2026, 11, 5, 0, 30, 0, 0, time.Local))
	list := billsOfRoom(t, env, roomID, "2026-11")
	if len(list) != 1 {
		t.Fatalf("缴费日当天应出 1 张，实得 %d", len(list))
	}
	if rent := num(list[0].(map[string]interface{})["rent"]); rent != 1500 {
		t.Errorf("账单租金应为 1500，实得 %v", rent)
	}

	// 同一天再跑一次：幂等，不出重复单。
	runAutoBill(env.db, time.Date(2026, 11, 5, 0, 30, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 1 {
		t.Fatalf("重复执行不应出重复账单，实得 %d 张", got)
	}
}

// TestAutoBillMonthly 每月固定日：只在设定的日子出账。
func TestAutoBillMonthly(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "A03", 900)
	seedActiveTenant(t, env, roomID, "王五", 0) // 缴费日 0 = 不提醒，不影响统一出账
	if err := setLedgerConfigValue(env.db, autobillModeConfigKey, autobillModeMonthly, env.personalLedger); err != nil {
		t.Fatalf("开启每月固定日自动出账: %v", err)
	}
	if err := setLedgerConfigValue(env.db, autobillDayConfigKey, "5", env.personalLedger); err != nil {
		t.Fatalf("设置统一出账日: %v", err)
	}

	// 4 号：不是设定的日子，不出单。
	runAutoBill(env.db, time.Date(2026, 11, 4, 0, 30, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 0 {
		t.Fatalf("非出账日不应出账，实得 %d 张", got)
	}

	// 5 号：出当月账单，重跑幂等。
	runAutoBill(env.db, time.Date(2026, 11, 5, 0, 30, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 1 {
		t.Fatalf("出账日应出 1 张，实得 %d", got)
	}
	runAutoBill(env.db, time.Date(2026, 11, 5, 23, 0, 0, 0, time.Local))
	if got := countBills(t, env, roomID, "2026-11"); got != 1 {
		t.Fatalf("重复执行不应出重复账单，实得 %d 张", got)
	}
}

// TestEmailBackupRejectsUnknownName 备份发邮件接口只接受列表里真实存在的文件名，
// 顺带挡住 ../ 这类路径穿越（不读文件就能拒绝，不需要 SMTP）。
func TestEmailBackupRejectsUnknownName(t *testing.T) {
	env := setupEnv(t)
	w := env.do(http.MethodPost, "/api/rental/backups/not-a-backup.db/email", nil)
	if w.Code == http.StatusOK {
		t.Fatalf("不存在的备份不应发送成功: %s", w.Body.String())
	}
}
