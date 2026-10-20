// renew_test.go — 租约续租的集成测试：到期日推算口径、提前续租/过期续租两种
// 基准日、自定义到期日、调租金、参数校验与台账隔离。
//
// 路由同样是本模块单独 apps.Register 挂的（见 renew.go 顶部说明），因此用例
// 全部走真实 HTTP，顺带证明 resolveLedger / denyTenantRole / 模块门禁挂上了。
package rental

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// leaseDate 返回 n 天后的本地日期（YYYY-MM-DD），用于构造「今天到期」这类用例。
func leaseDate(offsetDays int) string {
	return time.Now().AddDate(0, 0, offsetDays).Format("2006-01-02")
}

// seedTenantWithLease 建一个在租租户并指定到期日与押金（不传到期日则不写）。
func (e *testEnv) seedTenantWithLease(roomID uint, name, leaseEnd string) uint {
	e.t.Helper()
	payload := map[string]interface{}{"room_id": roomID, "name": name}
	if leaseEnd != "" {
		payload["lease_end_date"] = leaseEnd
	}
	w := e.do(http.MethodPost, "/api/rental/tenants", payload)
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed tenant %s: %d %s", name, w.Code, w.Body.String())
	}
	return tenantID(e.t, decodeBody(e.t, w))
}

// tenantID 从接口返回里取租户 id。
func tenantID(t *testing.T, body map[string]interface{}) uint {
	t.Helper()
	return uint(field(t, body, "data", "id").(float64))
}

// getTenantLeaseEnd 读租户当前 lease_end_date。
func (e *testEnv) getTenantLeaseEnd(id uint) string {
	e.t.Helper()
	var tenant Tenant
	if err := e.db.First(&tenant, id).Error; err != nil {
		e.t.Fatalf("load tenant %d: %v", id, err)
	}
	return tenant.LeaseEndDate
}

// TestLeaseEndAfter 纯函数口径：+N 月 −1 天、日号溢出取月末、与前端
// web/src/utils/lease.ts 的 leaseEndDate 互逆（前端有同名用例）。
func TestLeaseEndAfter(t *testing.T) {
	cases := []struct {
		base   string
		months int
		want   string
	}{
		{"2026-01-15", 3, "2026-04-14"},
		{"2026-01-15", 6, "2026-07-14"},
		{"2026-01-15", 12, "2027-01-14"},
		{"2026-01-31", 1, "2026-02-27"}, // 1/31 + 1 月 = 2/28（平年），−1 天 = 2/27
		{"2024-01-31", 1, "2024-02-28"}, // 闰年：1/31 + 1 月 = 2/29，−1 天 = 2/28
		{"2026-12-01", 1, "2026-12-31"},
		{"2026-05-01", 3, "2026-07-31"},
	}
	for _, tc := range cases {
		base, ok := parseLeaseDate(tc.base)
		if !ok {
			t.Fatalf("parse base %s", tc.base)
		}
		if got := leaseEndAfter(base, tc.months).Format("2006-01-02"); got != tc.want {
			t.Errorf("leaseEndAfter(%s, %d) = %s, want %s", tc.base, tc.months, got, tc.want)
		}
		if back := monthsBetweenLease(base, leaseEndAfter(base, tc.months)); back != tc.months {
			t.Errorf("monthsBetweenLease(%s, %d 月) = %d, want %d（来回换算必须一致）",
				tc.base, tc.months, back, tc.months)
		}
	}
}

// TestTenantRenewByMonths：提前续租（原到期日在未来）→ 从原到期日顺延。
func TestTenantRenewByMonths(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("101", 1500, 0)
	// 用相对日期：用例不依赖运行当天的日历，一年后到期 → 再续半年。
	base := time.Now().AddDate(1, 0, 0)
	baseStr := base.Format("2006-01-02")
	tenantID := env.seedTenantWithLease(roomID, "张三", baseStr)
	wantEnd := leaseEndAfter(base, 6).Format("2006-01-02")

	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/renew", tenantID),
		map[string]interface{}{"months": 6, "note": "续半年"})
	if w.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	renewal := field(t, body, "data", "renewal").(map[string]interface{})
	if got := renewal["to_end_date"]; got != wantEnd {
		t.Fatalf("to_end_date = %v, want %s", got, wantEnd)
	}
	if got := num(renewal["months"]); got != 6 {
		t.Fatalf("months = %v, want 6", got)
	}
	if got := env.getTenantLeaseEnd(tenantID); got != wantEnd {
		t.Fatalf("tenant lease_end_date = %s, want %s", got, wantEnd)
	}
	// 历史接口能读到刚才那条。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/tenants/%d/renewals", tenantID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("renewals: %d %s", w.Code, w.Body.String())
	}
	list := env.items(decodeBody(t, w))
	if len(list) != 1 {
		t.Fatalf("renewals len = %d, want 1", len(list))
	}
	row := list[0].(map[string]interface{})
	if row["from_end_date"] != baseStr || row["to_end_date"] != wantEnd {
		t.Fatalf("renewal row = %v", row)
	}
}

// TestTenantRenewExpiredBaseline：租户已过期（到期日在过去）→ 从今天起算，
// 不能得到过去的日期（这就是「只到三个月」场景里最容易出错的地方）。
func TestTenantRenewExpiredBaseline(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("101", 1500, 0)
	tenantID := env.seedTenantWithLease(roomID, "李四", leaseDate(-40))

	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/renew", tenantID),
		map[string]interface{}{"months": 3})
	if w.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	renewal := field(t, decodeBody(t, w), "data", "renewal").(map[string]interface{})
	want := time.Now().AddDate(0, 3, -1).Format("2006-01-02")
	if got := renewal["to_end_date"]; got != want {
		t.Fatalf("to_end_date = %v, want %s（过期租户从今天起算）", got, want)
	}
}

// TestTenantRenewByCustomEndDate：自定义到期日优先于 months，并回报月数。
func TestTenantRenewByCustomEndDate(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("101", 1500, 0)
	baseStr := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	tenantID := env.seedTenantWithLease(roomID, "王五", baseStr)
	target := time.Now().AddDate(2, 0, -1).Format("2006-01-02")

	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/renew", tenantID),
		map[string]interface{}{"months": 1, "to_end_date": target})
	if w.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	renewal := field(t, decodeBody(t, w), "data", "renewal").(map[string]interface{})
	if got := renewal["to_end_date"]; got != target {
		t.Fatalf("to_end_date = %v, want %s（自定义日期优先）", got, target)
	}
	if got := num(renewal["months"]); got != 12 {
		t.Fatalf("months = %v, want 12（一年后到期顺延一年记 12 个月）", got)
	}
}

// TestTenantRenewWithRentChange：续租同时调租金 → 房源默认月租更新、留痕记录前后值。
func TestTenantRenewWithRentChange(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("101", 1500, 0)
	tenantID := env.seedTenantWithLease(roomID, "赵六", leaseDate(30))

	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/renew", tenantID),
		map[string]interface{}{"months": 12, "rent": 1800})
	if w.Code != http.StatusOK {
		t.Fatalf("renew: %d %s", w.Code, w.Body.String())
	}
	renewal := field(t, decodeBody(t, w), "data", "renewal").(map[string]interface{})
	if num(renewal["rent_before"]) != 1500 || num(renewal["rent_after"]) != 1800 {
		t.Fatalf("rent snapshot = %v → %v, want 1500 → 1800",
			renewal["rent_before"], renewal["rent_after"])
	}
	var room Room
	if err := env.db.First(&room, roomID).Error; err != nil {
		t.Fatalf("load room: %v", err)
	}
	if room.DefaultRent != 1800 {
		t.Fatalf("room default_rent = %v, want 1800", room.DefaultRent)
	}
}

// TestTenantRenewValidation：参数与状态校验。
func TestTenantRenewValidation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("101", 1500, 0)
	tenantID := env.seedTenantWithLease(roomID, "钱七", leaseDate(10))
	path := fmt.Sprintf("/api/rental/tenants/%d/renew", tenantID)

	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"既没月数也没到期日", map[string]interface{}{}},
		{"月数为 0", map[string]interface{}{"months": 0}},
		{"月数超上限", map[string]interface{}{"months": 121}},
		{"到期日格式错误", map[string]interface{}{"to_end_date": "2026/12/31"}},
		{"到期日不晚于当前", map[string]interface{}{"to_end_date": leaseDate(5)}},
		{"租金为负", map[string]interface{}{"months": 3, "rent": -1}},
		{"备注超长", map[string]interface{}{"months": 3, "note": longText(201)}},
	}
	for _, tc := range cases {
		w := env.do(http.MethodPost, path, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400 (%s)", tc.name, w.Code, w.Body.String())
		}
	}

	// 已退租的租户不能续租。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", tenantID),
		map[string]interface{}{"move_out_date": leaseDate(0)}); w.Code != http.StatusOK {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, path, map[string]interface{}{"months": 6}); w.Code != http.StatusBadRequest {
		t.Fatalf("renew checked-out tenant = %d, want 400", w.Code)
	}
}

// TestTenantRenewNotFound：不存在的租户 404（不暴露是否存在）。
func TestTenantRenewNotFound(t *testing.T) {
	env := setupEnv(t)
	w := env.do(http.MethodPost, "/api/rental/tenants/99999/renew", map[string]interface{}{"months": 3})
	if w.Code != http.StatusNotFound {
		t.Fatalf("renew missing tenant = %d, want 404", w.Code)
	}
}

// longText 生成 n 个汉字，用于超长校验。
func longText(n int) string {
	buf := make([]rune, 0, n)
	for i := 0; i < n; i++ {
		buf = append(buf, '续')
	}
	return string(buf)
}
