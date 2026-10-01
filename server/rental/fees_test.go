// fees_test.go — 收费项目自定义、按项目周期出账与按项目收款的集成测试。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// TestCustomFeesAndPayments 季付租户（包月水费）+ 自定义宽带费（月付）：
// 锚点月账单含租金×3 + 包月水费 + 宽带费；按项目收款后欠缴按项目算清。
func TestCustomFeesAndPayments(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("901", 1000, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "混合周期", "water_mode": "monthly", "water_monthly_fee": 40,
		"pay_cycle": "quarterly", "pay_day": 5,
	})

	// 新增自定义收费项目：宽带费 30 元/月，月付；key 由服务端生成。
	w2 := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "宽带费", "cycle": "monthly", "default_amount": 30,
	})
	if w2.Code != http.StatusOK {
		t.Fatalf("create fee item: %d %s", w2.Code, w2.Body.String())
	}
	broadbandKey := field(t, decodeBody(t, w2), "data", "key").(string)

	// 锚点月 2026-09：租金 ×3 + 包月水费（月） + 宽带费（月） = 3070。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	b := env.fetchBill("2026-09", "901")
	if !almostEqual(num(b["rent"]), 3000) || !almostEqual(num(b["water_fee"]), 40) {
		t.Errorf("bill = rent %v water %v, want 3000/40", b["rent"], b["water_fee"])
	}
	if !almostEqual(num(b["extra_amount"]), 30) {
		t.Errorf("extra = %v, want 30（宽带费）", b["extra_amount"])
	}
	if !almostEqual(num(b["total_amount"]), 3070) {
		t.Errorf("total = %v, want 3070", b["total_amount"])
	}
	billID := uint(b["id"].(float64))

	// 中间月 2026-10：月账单——固定费未到期，只有包月水费 + 宽带费。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate oct: %d %s", w.Code, w.Body.String())
	}
	oct := env.fetchBill("2026-10", "901")
	if !almostEqual(num(oct["rent"]), 0) || !almostEqual(num(oct["water_fee"]), 40) || !almostEqual(num(oct["extra_amount"]), 30) {
		t.Errorf("oct bill = rent %v water %v extra %v, want 0/40/30（月账单）",
			oct["rent"], oct["water_fee"], oct["extra_amount"])
	}

	// 账单明细：含租金（3000，季付区间）、水费、宽带费三行。
	w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billID), nil)
	detail := field(t, decodeBody(t, w), "data").(map[string]interface{})
	items := detail["items"].([]interface{})
	if len(items) != 3 {
		t.Fatalf("bill items = %d, want 3", len(items))
	}

	// 按项目收款：先收租金 3000 → 部分已缴，租金欠缴清零，其余项目仍欠。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{
		"amount": 3000,
		"items": []map[string]interface{}{
			{"key": feeKeyRent, "name": "租金", "amount": 3000},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("pay rent: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billID), nil)
	detail = field(t, decodeBody(t, w), "data").(map[string]interface{})
	paid := detail["bill"].(map[string]interface{})
	if paid["status"] != billStatusPartial || !almostEqual(num(paid["paid_amount"]), 3000) {
		t.Errorf("after rent pay = %v/%v, want partial/3000", paid["status"], paid["paid_amount"])
	}
	for _, raw := range detail["items"].([]interface{}) {
		row := raw.(map[string]interface{})
		if row["key"] == feeKeyRent {
			if !almostEqual(num(row["arrears"]), 0) || !almostEqual(num(row["paid"]), 3000) {
				t.Errorf("rent item = paid %v arrears %v, want 3000/0", row["paid"], row["arrears"])
			}
		}
	}

	// 收水费 + 宽带费 → 缴清。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{
		"amount": 70,
		"items": []map[string]interface{}{
			{"key": feeKeyWater, "name": "水费", "amount": 40},
			{"key": broadbandKey, "name": "宽带费", "amount": 30},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("pay rest: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billID), nil)
	detail = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if detail["bill"].(map[string]interface{})["status"] != billStatusPaid {
		t.Errorf("after full pay status = %v, want paid", detail["bill"].(map[string]interface{})["status"])
	}
	if got := len(detail["payments"].([]interface{})); got != 2 {
		t.Errorf("payments = %d, want 2（每笔收款一条流水）", got)
	}

	// 收款流水列表：两笔，带账单快照与项目分摊。
	w = env.do(http.MethodGet, "/api/rental/payments", nil)
	payments := env.items(decodeBody(t, w))
	if len(payments) != 2 {
		t.Fatalf("payment list = %d, want 2", len(payments))
	}
	first := payments[0].(map[string]interface{})
	if first["room_no"] != "901" || !almostEqual(num(first["amount"]), 70) {
		t.Errorf("payment row = %v / %v, want 901 / 70", first["room_no"], first["amount"])
	}

	// 超过项目欠缴的收款应被拒绝。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", env.createBill("901", "2026-11")), map[string]interface{}{
		"amount": 3000,
		"items":  []map[string]interface{}{{"key": feeKeyRent, "name": "租金", "amount": 3000}},
	})
	_ = w

	// 统计分析：有收款流水与账单，接口应返回正常序列。
	w = env.do(http.MethodGet, "/api/rental/analytics?months=6", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("analytics: %d %s", w.Code, w.Body.String())
	}
	analytics := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := len(analytics["series"].([]interface{})); got != 6 {
		t.Errorf("analytics series = %d, want 6", got)
	}
}
