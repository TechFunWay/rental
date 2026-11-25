// first_bill_matrix_scope_test.go — 矩阵第二批：生成范围、并发与性能哨兵、CSV 旁路。
package rental

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestGenerateOnlyWritesSelectedLedger 一键生成是**单台账**动作（v0.6.0 口径）：
// 个人台账与共同台账各有一间在租房，只对共同台账点生成——个人台账不能被
// 「顺手」也出了单（多房东/代管场景里这是数据越权）。
func TestGenerateOnlyWritesSelectedLedger(t *testing.T) {
	env := setupEnv(t)
	shared := createSharedLedger(t, env, env.token, "matrix-scope")
	// 个人台账：房 M12 + 租户（不带勾选登记，无单）
	roomP := seedRentRoom(t, env, "M12", 1000)
	if w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomP, "name": "个人楼租户", "move_in_date": "2026-11-01",
	}); w.Code != http.StatusOK {
		t.Fatalf("个人楼登记: %d %s", w.Code, w.Body.String())
	}
	// 共同台账：房 M13 + 租户
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), env.token,
		map[string]interface{}{"community": "S区", "room_no": "M13", "default_rent": 800})
	roomS := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants?ledger_id="+itoa(shared), env.token,
		map[string]interface{}{"room_id": roomS, "name": "共同楼租户", "move_in_date": "2026-11-01"}); w.Code != http.StatusOK {
		t.Fatalf("共同楼登记: %d %s", w.Code, w.Body.String())
	}

	// 只对共同台账生成 2027-05
	if w := doJSON(env.r, http.MethodPost, "/api/rental/bills/generate?ledger_id="+itoa(shared), env.token,
		map[string]interface{}{"period": "2027-05"}); w.Code != http.StatusOK {
		t.Fatalf("共同台账生成: %d %s", w.Code, w.Body.String())
	}
	if n := len(billsOfRoom(t, env, roomS, "2027-05")); n != 1 {
		t.Errorf("共同台账房应有 1 张，实得 %d", n)
	}
	if n := len(billsOfRoom(t, env, roomP, "2027-05")); n != 0 {
		t.Errorf("个人台账房不该被顺手生成，实得 %d", n)
	}
}

// TestGenerateSkipsVacantRoom 空置房不出账：一键生成只服务在租房间，
// 没租户的房连 0 元单都不该有（否则空置分析刚做完、账单页又冒出一堆空单）。
func TestGenerateSkipsVacantRoom(t *testing.T) {
	env := setupEnv(t)
	vacant := seedRentRoom(t, env, "M14", 1000) // 只建房，不登记租户
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{
		"period": "2027-04",
	}); w.Code != http.StatusOK {
		t.Fatalf("生成: %d %s", w.Code, w.Body.String())
	}
	if n := len(billsOfRoom(t, env, vacant, "2027-04")); n != 0 {
		t.Errorf("空置房不应出账，实得 %d", n)
	}
}

// TestGenerateRejectsBadPeriod 账期格式在入口挡死：斜杠、缺位都不放行，
// 更不可能落库（防「脏账期把统计曲线拉花」）。
func TestGenerateRejectsBadPeriod(t *testing.T) {
	env := setupEnv(t)
	for _, bad := range []string{"2026/05", "2026-5", "202605", "2026-13", ""} {
		w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": bad})
		if w.Code == http.StatusOK {
			t.Errorf("坏账期 %q 竟然 200：%s", bad, w.Body.String())
		}
	}
}

// TestConcurrentRegisterSameRoomSamePeriod 两位同事几乎同时给同一间房登记
// 首期账单（同月入住的合住双档）：两个租户都要建成功，账单只能有 1 张、
// 押金只计一次。事务串行 + 计数检查保证这一点（无 500 甩给用户）。
func TestConcurrentRegisterSameRoomSamePeriod(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M15", 1000)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
				"room_id": roomID, "name": fmt.Sprintf("并发租户%d", i), "deposit": 1000,
				"move_in_date": "2026-11-01", "create_first_bill": true,
			})
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("并发登记 %d = %d, want 200（首期账单冲突不该冒泡成失败）", i, c)
		}
	}
	if n := len(billsOfRoom(t, env, roomID, "2026-11")); n != 1 {
		t.Errorf("并发同房同期只能 1 张账单，实得 %d", n)
	}
	// 两个租户都建档成功（账单幂等 ≠ 租户被顺手挡掉）
	w := env.do(http.MethodGet, "/api/rental/tenants?room_id="+itoa(roomID)+"&active=1", nil)
	if n := len(itemsOf(t, w)); n != 2 {
		t.Errorf("两位并发租户应都在档，实得 %d", n)
	}
}

// TestConcurrentGenerateNoDuplicate 对同一台账并发点两次「一键生成」：
// 双双成功返回（count→create 窗口由唯一索引 + isDuplicateBillIndex 收口成
// 跳过，不是 500），最终每间房恰好一张。
func TestConcurrentGenerateNoDuplicate(t *testing.T) {
	env := setupEnv(t)
	const rooms = 6
	var ids []uint
	for i := 0; i < rooms; i++ {
		id := seedRentRoom(t, env, fmt.Sprintf("M16%d", i), 500)
		if w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
			"room_id": id, "name": fmt.Sprintf("并发生成租户%d", i), "move_in_date": "2026-11-01",
		}); w.Code != http.StatusOK {
			t.Fatalf("登记: %d %s", w.Code, w.Body.String())
		}
		ids = append(ids, id)
	}
	// 原 fixture 循环已并入 ids 版本（避免两次 seed）
	var wg sync.WaitGroup
	codes := make([]int, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2027-06"})
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("并发生成第 %d 路 = %d, want 200（唯一冲突必须收口成跳过，见 isDuplicateBillIndex）", i, c)
		}
	}
	total := 0
	for _, id := range ids {
		total += len(billsOfRoom(t, env, id, "2027-06"))
	}
	if total != rooms {
		t.Errorf("%d 间房应各 1 张，实得 %d 张（并发下出现重复单即为竞态缺陷）", rooms, total)
	}
}

// billsOf 按账期取全部账单条目（性能与批量场景的全量核对口）。
func billsOf(t *testing.T, env *testEnv, period string) []interface{} {
	t.Helper()
	w := env.do(http.MethodGet, "/api/rental/bills?period="+period+"&pageSize=500", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list bills %s: %d %s", period, w.Code, w.Body.String())
	}
	return itemsOf(t, w)
}

// TestTenantsCSVImportNeverAutoBills 租客 CSV 导入走的是独立通道，
// 根本不带 create_first_bill——导入 20 人也只能建档不出单。
// 「登记即出单」是房东在表单里的**明示勾选**，绝不许顺着导入批量造欠费单
// （历史兼容与统计口径的护栏）。
func TestTenantsCSVImportNeverAutoBills(t *testing.T) {
	env := setupEnv(t)
	for _, no := range []string{"M17A", "M17B"} {
		seedRentRoom(t, env, no, 600)
	}
	body := "\ufeff" + tenantImportHeader + "\n" +
		",M17A,导入甲,13800000001,,2026-11-01,,600,,,,,,,\n" +
		",M17B,导入乙,13800000002,,2026-11-05,,600,,,,,,,\n"
	w := importTenantsCSVBody(env, body)
	if w.Code != http.StatusOK {
		t.Fatalf("导入: %d %s", w.Code, w.Body.String())
	}
	if n := len(billsOf(t, env, "2026-11")); n != 0 {
		t.Errorf("CSV 导入不应生成任何账单，实得 %d", n)
	}
}

// TestGeneratePerformanceSentinel 150 间在租房规模的一键生成耗时上界。
// 定位是**量级哨兵**（SQLite MaxOpenConns=1 下 N+1 化明显的退化会被它抓住），
// 不是性能竞赛；上界取宽松值，慢机器不误报。
// 直插 fixture（不走 API）避免把建数据的时间记进测项。
func TestGeneratePerformanceSentinel(t *testing.T) {
	if testing.Short() {
		t.Skip("性能哨兵：-short 模式跳过")
	}
	env := setupEnv(t)
	const n = 150
	period := "2027-08"
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		room := Room{LedgerID: env.personalLedger, UserID: env.adminID,
			RoomNo: fmt.Sprintf("P%03d", i), DefaultRent: 1000}
		if err := env.db.Create(&room).Error; err != nil {
			t.Fatalf("直插房 %d: %v", i, err)
		}
		tenant := Tenant{LedgerID: env.personalLedger, UserID: env.adminID,
			RoomID: room.ID, Name: fmt.Sprintf("规模租户%d", i), Active: true}
		if err := env.db.Create(&tenant).Error; err != nil {
			t.Fatalf("直插租户 %d: %v", i, err)
		}
		ids = append(ids, room.ID)
	}

	start := time.Now()
	w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": period})
	elapsed := time.Since(start)
	if w.Code != http.StatusOK {
		t.Fatalf("规模生成: %d %s", w.Code, w.Body.String())
	}
	created := num(field(t, decodeBody(t, w), "data", "created"))
	if int(created) != n {
		t.Errorf("应生成 %d 张，实得 %v", n, created)
	}
	// 直查库张数：列表接口有 MaxPageSize=100 的硬上限（合理护栏），
	// 150 间规模不能再用分页列表核对总数。
	var stored int64
	if err := env.db.Table("bills").Where("period = ?", period).Count(&stored).Error; err != nil {
		t.Fatalf("直查张数: %v", err)
	}
	if int(stored) != n {
		t.Errorf("落库张数应为 %d，实得 %d", n, stored)
	}
	t.Logf("一键生成 %d 间耗时 %v", n, elapsed)
	if elapsed > 20*time.Second {
		t.Errorf("%d 间生成耗时 %v，超过 20s 预算——查是否引入按房多次查询", n, elapsed)
	}

	// 第二遍应全跳过且更快（计数口没退化）
	start = time.Now()
	w2 := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": period})
	if w2.Code != http.StatusOK {
		t.Fatalf("第二遍生成: %d %s", w2.Code, w2.Body.String())
	}
	skipped := num(field(t, decodeBody(t, w2), "data", "skipped"))
	t.Logf("重跑跳过 %v 张，耗时 %v", skipped, time.Since(start))
	if int(skipped) != n {
		t.Errorf("重跑应全跳过，实得 skipped=%v", skipped)
	}
	_ = ids
}
