// bill_preview_test.go — 开票预览（抄表开票弹窗）与抄表记录账单金额的集成测试。
//
// 背景（2026-10-17 粉丝反馈）：新增的自定义收费项目没有在抄表记录里体现出来——
// 抄表开票弹窗的「应付合计」只算内置六项，不知道自定义项目、首张押金与上期
// 结转；落库账单却包含它们，用户看到一个比账单小的预览数字。
//
// 这里考开票预览接口：不写库、但必须与服务端建账同一条计算链（含自定义项目、
// 押金、结转），并且抄表记录列表能带出本期账单金额与自定义收费合计。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// seedCustomFee 建一个启用的自定义固定收费项目（默认月付），返回 key。
func seedCustomFee(t *testing.T, env *testEnv, name string, amount float64) string {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": name, "cycle": payCycleMonthly, "default_amount": amount,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create fee item %s: %d %s", name, w.Code, w.Body.String())
	}
	return field(t, decodeBody(t, w), "data", "key").(string)
}

// fetchPreview 调开票预览接口，返回 data 段。
func fetchPreview(t *testing.T, env *testEnv, roomID uint, period string) map[string]interface{} {
	t.Helper()
	path := fmt.Sprintf("/api/rental/bills/preview?room_id=%d&period=%s", roomID, period)
	w := env.do(http.MethodGet, path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("preview %s: %d %s", period, w.Code, w.Body.String())
	}
	return field(t, decodeBody(t, w), "data").(map[string]interface{})
}

// TestBillPreviewIncludesCustomFeesAndDeposit 预览必须包含自定义项目与首张押金，
// 且合计等于明细之和；预览本身不落库。
func TestBillPreviewIncludesCustomFeesAndDeposit(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "PV01", 1000, 2000)
	customKey := seedCustomFee(t, env, "宽带费", 30)

	// 首账月：租金 1000 + 押金 2000 + 自定义宽带 30 = 3030。
	data := fetchPreview(t, env, roomID, "2026-10")
	if exists, _ := data["exists"].(bool); exists {
		t.Fatalf("还没开票，exists 应为 false")
	}
	bill := data["bill"].(map[string]interface{})
	if !almostEqual(num(bill["deposit_fee"]), 2000) {
		t.Errorf("预览押金 = %v, want 2000（首账月应收押金）", bill["deposit_fee"])
	}
	if !almostEqual(num(bill["extra_amount"]), 30) {
		t.Errorf("预览自定义收费合计 = %v, want 30（宽带费）", bill["extra_amount"])
	}
	if !almostEqual(num(bill["total_amount"]), 3030) {
		t.Errorf("预览合计 = %v, want 3030（1000 租金 + 2000 押金 + 30 宽带）", bill["total_amount"])
	}

	// 明细：含宽带费与押金两行，且明细金额之和等于合计。
	items := data["items"].([]interface{})
	var sum float64
	foundCustom, foundDeposit := false, false
	for _, raw := range items {
		it := raw.(map[string]interface{})
		sum += num(it["amount"])
		if it["key"] == customKey {
			foundCustom = true
			if !almostEqual(num(it["amount"]), 30) {
				t.Errorf("宽带费明细金额 = %v, want 30", it["amount"])
			}
		}
		if it["key"] == feeKeyDeposit {
			foundDeposit = true
		}
	}
	if !foundCustom {
		t.Errorf("预览明细缺少自定义收费项目 %s", customKey)
	}
	if !foundDeposit {
		t.Errorf("预览明细缺少押金行")
	}
	if !almostEqual(sum, num(bill["total_amount"])) {
		t.Errorf("预览明细之和 = %v, 合计 = %v，两者应相等", sum, bill["total_amount"])
	}

	// 预览不能落库：账单列表仍为空。
	w := env.do(http.MethodGet, "/api/rental/bills?period=2026-10", nil)
	if got := num(field(t, decodeBody(t, w), "data", "total")); got != 0 {
		t.Fatalf("预览不应创建账单，账单数 = %v", got)
	}

	// 开票后预览改为返回真实账单（exists=true），金额与落库账单一致。
	created := createBillFor(t, env, roomID, "2026-10")
	data = fetchPreview(t, env, roomID, "2026-10")
	if exists, _ := data["exists"].(bool); !exists {
		t.Fatalf("已开票后 exists 应为 true")
	}
	existing := data["bill"].(map[string]interface{})
	if uint(num(existing["id"])) != uint(num(created["id"])) {
		t.Errorf("预览账单 id = %v, want %v", existing["id"], created["id"])
	}
	if !almostEqual(num(existing["total_amount"]), num(created["total_amount"])) {
		t.Errorf("预览合计 = %v, 落库账单合计 = %v", existing["total_amount"], created["total_amount"])
	}
}

// TestBillPreviewExcludesTenantFee 租户勾掉的收费项目预览按 0 计（与建账同口径）。
func TestBillPreviewExcludesTenantFee(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("PV02", 1000, 0)
	parkingKey := seedCustomFee(t, env, "停车费", 50)
	if w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "不交停车费", "excluded_fees": []string{parkingKey},
	}); w.Code != http.StatusOK {
		t.Fatalf("seed tenant: %d %s", w.Code, w.Body.String())
	}

	data := fetchPreview(t, env, roomID, "2026-10")
	bill := data["bill"].(map[string]interface{})
	if !almostEqual(num(bill["extra_amount"]), 0) {
		t.Errorf("被排除的停车费仍计入预览 extra = %v, want 0", bill["extra_amount"])
	}
	for _, raw := range data["items"].([]interface{}) {
		if raw.(map[string]interface{})["key"] == parkingKey {
			t.Errorf("被排除的停车费不应出现在预览明细里")
		}
	}
	if !almostEqual(num(bill["rent"]), 1000) {
		t.Errorf("预览租金 = %v, want 1000", bill["rent"])
	}
}

// TestImportKeepsBillItemsInSync CSV 导入覆盖已有账单后必须重建费用明细：
// 否则单据/详情里的明细还是旧金额、合计却是新金额（2026-10-17 审计发现的
// 「明细加起来对不上合计」残留，与粉丝反馈的收费单据金额问题同源）。
func TestImportKeepsBillItemsInSync(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("IM01", 1000, 0)
	env.seedTenant(roomID, "张三")
	seedCustomFee(t, env, "宽带费", 30)
	created := createBillFor(t, env, roomID, "2026-10")

	header := "房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注\n"
	w := env.importCSVBody(header + "IM01,张三,2026-10,1200,0,0,0,0,0,0,5,1.2,3.5,0,0,0,导入覆盖\n")
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}

	billID := uint(num(created["id"]))
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bill detail: %d %s", w.Code, w.Body.String())
	}
	detail := field(t, decodeBody(t, w), "data").(map[string]interface{})
	bill := detail["bill"].(map[string]interface{})
	// 租金改成 1200 + 自定义宽带 30 = 合计 1230。
	if !almostEqual(num(bill["total_amount"]), 1230) {
		t.Fatalf("导入后合计 = %v, want 1230", bill["total_amount"])
	}
	var sum float64
	foundRent := false
	for _, raw := range detail["items"].([]interface{}) {
		it := raw.(map[string]interface{})
		sum += num(it["amount"])
		if it["key"] == feeKeyRent {
			foundRent = true
			if !almostEqual(num(it["amount"]), 1200) {
				t.Errorf("导入后租金明细 = %v, want 1200（明细未重建）", it["amount"])
			}
		}
	}
	if !foundRent {
		t.Errorf("明细缺少租金行")
	}
	if !almostEqual(sum, num(bill["total_amount"])) {
		t.Errorf("明细之和 = %v, 合计 = %v，两者应相等", sum, bill["total_amount"])
	}
}

// TestMeterRecordShowsBillAmount 抄表记录列表带出本期账单金额与自定义收费合计。
func TestMeterRecordShowsBillAmount(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "MR01", 1000, 0)
	seedCustomFee(t, env, "宽带费", 30)
	created := createBillFor(t, env, roomID, "2026-10")

	w := env.do(http.MethodGet, "/api/rental/meter-records?period=2026-10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("meter list: %d %s", w.Code, w.Body.String())
	}
	items := env.items(decodeBody(t, w))
	if len(items) != 1 {
		t.Fatalf("抄表记录数 = %d, want 1（开票时自动沉淀）", len(items))
	}
	row := items[0].(map[string]interface{})
	if uint(num(row["bill_id"])) != uint(num(created["id"])) {
		t.Errorf("抄表记录 bill_id = %v, want %v", row["bill_id"], created["id"])
	}
	if !almostEqual(num(row["bill_total"]), num(created["total_amount"])) {
		t.Errorf("抄表记录账单金额 = %v, want %v", row["bill_total"], created["total_amount"])
	}
	if !almostEqual(num(row["bill_extra"]), 30) {
		t.Errorf("抄表记录自定义收费合计 = %v, want 30", row["bill_extra"])
	}
}
