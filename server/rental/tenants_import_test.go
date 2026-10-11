// tenants_import_test.go — 租客 CSV 导入与批量单据数据的集成测试。
package rental

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const tenantImportHeader = "小区,房号,姓名,电话,身份证号,入住日期,租约到期日,押金,水费模式,水费单价,水费包月金额,缴费周期,缴费日,提前提醒天数,备注"

func importTenantsCSVBody(e *testEnv, content string) *httptest.ResponseRecorder {
	e.t.Helper()
	return uploadContract(e.t, e, "/api/rental/tenants/import", "file", "tenants.csv", []byte(content))
}

func findImportedTenant(t *testing.T, env *testEnv, roomID uint, name string) Tenant {
	t.Helper()
	var tenant Tenant
	if err := env.db.Where("room_id = ? AND name = ?", roomID, name).
		Order("id ASC").First(&tenant).Error; err != nil {
		t.Fatalf("tenant %s not found: %v", name, err)
	}
	return tenant
}

// seedCommunityRoom 建一间带小区的房源并返回 ID。
func seedCommunityRoom(t *testing.T, env *testEnv, community, roomNo string) uint {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"community": community, "room_no": roomNo, "default_rent": 1500,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room %s/%s: %d %s", community, roomNo, w.Code, w.Body.String())
	}
	return uint(field(t, decodeBody(t, w), "data", "id").(float64))
}

func TestTenantsImportCreateUpdateAndRowErrors(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "101")
	seedCommunityRoom(t, env, "", "102")

	// 坏行覆盖：房源不存在、姓名缺失、日期格式、缴费日越界、文件内重复。
	// 缴费日列（第 13 列，下标 12）填 35 越界。
	payDayBad := strings.Join([]string{
		"阳光小区", "101", "赵六", "", "", "", "", "", "", "", "", "", "35", "", "",
	}, ",")
	csvBody := strings.Join([]string{
		"\ufeff" + tenantImportHeader,
		"阳光小区,101,张三,13800000001,110101199001011234,2026-01-01,2026-12-31,3000,按吨,5,,月付,5,3,首次导入",
		"阳光小区,201,王五,,,,,,,,,,,,",
		"阳光小区,101,,13800000002,,,,,,,,,,,,",
		payDayBad,
		"阳光小区,101,孙七,,,,,,,,,,,,,",
		"阳光小区,101,孙七,,,,,,,,,,,,,",
		"阳光小区,101,周八,13900000000,,2026-13-40,,,,,,,,,",
		",102,李四,,,,,,,,,,,,",
	}, "\n")

	w := importTenantsCSVBody(env, csvBody)
	if w.Code != http.StatusOK {
		t.Fatalf("import tenants: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 3 {
		t.Fatalf("created = %v, want 3", res["created"])
	}
	if res["failed"].(float64) != 5 {
		t.Fatalf("failed = %v, want 5", res["failed"])
	}
	errs := res["errors"].([]interface{})
	if len(errs) != 5 {
		t.Fatalf("errors = %v", res["errors"])
	}
	// 行号：3-8 行依次是坏行（房源不存在/姓名缺失/缴费日越界/文件内重复/日期格式）。
	wantReasons := []string{"房源不存在", "姓名不能为空", "缴费日", "重复", "入住日期"}
	for i, want := range wantReasons {
		e := errs[i].(map[string]interface{})
		if !strings.Contains(e["reason"].(string), want) {
			t.Fatalf("error[%d] = %v, want reason containing %q", i, e, want)
		}
	}

	// 落库抽查：计费与缴费字段按模板写入。
	tenant := findImportedTenant(t, env, roomID, "张三")
	if tenant.Phone != "13800000001" || tenant.IDCard != "110101199001011234" {
		t.Fatalf("tenant contact = %v/%v", tenant.Phone, tenant.IDCard)
	}
	if tenant.MoveInDate != "2026-01-01" || tenant.LeaseEndDate != "2026-12-31" || tenant.Deposit != 3000 {
		t.Fatalf("tenant dates/deposit = %v/%v/%v", tenant.MoveInDate, tenant.LeaseEndDate, tenant.Deposit)
	}
	if tenant.WaterMode != "meter" || tenant.WaterPrice != 5 || tenant.PayCycle != "monthly" {
		t.Fatalf("tenant billing = %v/%v/%v", tenant.WaterMode, tenant.WaterPrice, tenant.PayCycle)
	}
	if tenant.PayDay != 5 || tenant.RemindDays != 3 {
		t.Fatalf("tenant pay day/remind = %v/%v", tenant.PayDay, tenant.RemindDays)
	}
	if !tenant.Active {
		t.Fatal("imported tenant must be active")
	}

	// 幂等重导：只改填了值的列（电话、缴费周期），空单元格保留旧值，不重复建档。
	updateRow := strings.Join([]string{
		"阳光小区", "101", "张三", "13911112222", "", "", "", "", "", "", "", "月付", "", "", "",
	}, ",")
	w = importTenantsCSVBody(env, tenantImportHeader+"\n"+updateRow)
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["updated"].(float64) != 1 || res["created"].(float64) != 0 {
		t.Fatalf("update import = %v", res)
	}
	tenant = findImportedTenant(t, env, roomID, "张三")
	if tenant.Phone != "13911112222" || tenant.Deposit != 3000 {
		t.Fatalf("merged phone/deposit = %v/%v", tenant.Phone, tenant.Deposit)
	}
	if tenant.IDCard != "110101199001011234" || tenant.MoveInDate != "2026-01-01" {
		t.Fatalf("merge must keep untouched fields, got %v", tenant)
	}
	if tenant.WaterMode != "meter" || tenant.WaterPrice != 5 {
		t.Fatalf("merge must keep billing, got %v/%v", tenant.WaterMode, tenant.WaterPrice)
	}

	// 表头不符 → 400。
	w = importTenantsCSVBody(env, "a,b\n1,2\n")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad header = %d, want 400", w.Code)
	}
}

func TestTenantsTemplateDownload(t *testing.T) {
	env := setupEnv(t)

	w := env.do(http.MethodGet, "/api/rental/tenants/template", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("template: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "房号") || !strings.Contains(w.Body.String(), "示例行") {
		t.Fatalf("template content = %q", w.Body.String())
	}
}

func TestBillReceiptsBatch(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("701", 1000, 0)
	env.seedTenant(roomID, "吴十")
	sepID := env.createBill("701", "2026-09")
	env.createBill("701", "2026-10")
	env.updateBill(sepID, "吴十", 0, 100) // 电费 120，合计 1120

	// 按区间取单据数据：账单 + 明细 + 共用抬头。
	w := env.do(http.MethodGet, "/api/rental/bills/receipts?from=2026-09&to=2026-09", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("receipts: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	bills := data["bills"].([]interface{})
	if len(bills) != 1 {
		t.Fatalf("bills = %d, want 1", len(bills))
	}
	first := bills[0].(map[string]interface{})
	bill := first["bill"].(map[string]interface{})
	if bill["period"] != "2026-09" || bill["room_no"] != "701" {
		t.Fatalf("bill = %v", bill)
	}
	if !almostEqual(num(bill["total_amount"]), 1120) {
		t.Fatalf("total = %v, want 1120", bill["total_amount"])
	}
	if _, ok := first["items"].([]interface{}); !ok {
		t.Fatalf("items missing: %v", first)
	}
	prop := data["property"].(map[string]interface{})
	if _, ok := prop["name"]; !ok {
		t.Fatalf("property meta missing: %v", prop)
	}

	// 区间过滤：不含 9 月的区间查不到。
	w = env.do(http.MethodGet, "/api/rental/bills/receipts?from=2026-11", nil)
	data = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := len(data["bills"].([]interface{})); got != 0 {
		t.Fatalf("out-of-range bills = %d, want 0", got)
	}
	// 不传区间 = 全部。
	w = env.do(http.MethodGet, "/api/rental/bills/receipts", nil)
	data = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := len(data["bills"].([]interface{})); got != 2 {
		t.Fatalf("all bills = %d, want 2", got)
	}
	// 账期格式非法 → 400。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/receipts?from=%s", "2026-13"), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad period = %d, want 400", w.Code)
	}
}
