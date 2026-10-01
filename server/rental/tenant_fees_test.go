// tenant_fees_test.go — 租户收费项目勾选（excluded_fees）：不参与计费的项目
// 出账清零、快照进账单、自定义项目按租户跳过。
//
// 覆盖三条线：
//  1. 开票结算：排除卫生费/管理费/燃气费的租户，生成的账单只有其余项目，
//     燃气读数照常记录但费用为 0；
//  2. 快照语义：出账后的账单不随租户后续改动勾选而变，新账单按新勾选计算；
//  3. 自定义收费项目：被排除的租户不并入，未排除的照常收取。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// seedTenantWithExclusions 走接口登记租户并带收费项目排除列表。
func (e *testEnv) seedTenantWithExclusions(roomID uint, name string, excluded []string) uint {
	e.t.Helper()
	w := e.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": name, "excluded_fees": excluded,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed tenant %s: %d %s", name, w.Code, w.Body.String())
	}
	return uint(field(e.t, decodeBody(e.t, w), "data", "id").(float64))
}

func TestTenantExcludedFeesSettlement(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("701", 1000, 100)
	env.setRoomFees(roomID, "701", 1000, 20, 30, 10)

	// 排除卫生费/管理费/燃气费：账单只收租金、水费（按吨）、电费。
	tenantID := env.seedTenantWithExclusions(roomID, "只收租水电", []string{"sanitation", "management", "gas"})

	// 开票并录入本月读数（燃气读数照录，只是不收费）。
	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-10",
		"elec_now": 120.0, "gas_now": 50.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	b := env.fetchBill("2026-10", "701")
	if !almostEqual(num(b["rent"]), 1000) {
		t.Errorf("rent = %v, want 1000（未排除）", b["rent"])
	}
	if !almostEqual(num(b["sanitation_fee"]), 0) || !almostEqual(num(b["management_fee"]), 0) {
		t.Errorf("sanitation/management = %v/%v, want 0/0（已排除）", b["sanitation_fee"], b["management_fee"])
	}
	if !almostEqual(num(b["elec_fee"]), 144) {
		t.Errorf("elec_fee = %v, want 144（120×1.2，setRoomFees 后底数为 0）", b["elec_fee"])
	}
	if !almostEqual(num(b["gas_fee"]), 0) {
		t.Errorf("gas_fee = %v, want 0（已排除，读数保留）", b["gas_fee"])
	}
	if !almostEqual(num(b["gas_now"]), 50) {
		t.Errorf("gas_now = %v, want 50（排除项目读数仍记录）", b["gas_now"])
	}
	// 总额 = 租金 1000 + 电费 144（水费用量 0、燃气已排除）。
	if !almostEqual(num(b["total_amount"]), 1144) {
		t.Errorf("total = %v, want 1144", b["total_amount"])
	}

	// 快照语义：租户改为全部参与后，已出账单不变。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "只收租水电", "excluded_fees": []string{},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update tenant: %d %s", w.Code, w.Body.String())
	}
	b = env.fetchBill("2026-10", "701")
	if !almostEqual(num(b["sanitation_fee"]), 0) {
		t.Errorf("sanitation_fee = %v, want 0（账单快照不随租户改动变化）", b["sanitation_fee"])
	}

	// 新账单按新勾选：全部项目参与。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-11"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	b = env.fetchBill("2026-11", "701")
	if !almostEqual(num(b["sanitation_fee"]), 20) || !almostEqual(num(b["management_fee"]), 30) {
		t.Errorf("new bill sanitation/management = %v/%v, want 20/30", b["sanitation_fee"], b["management_fee"])
	}
}

func TestTenantExcludedCustomFeeItem(t *testing.T) {
	env := setupEnv(t)
	roomA := env.seedRoom("801", 800, 0)
	roomB := env.seedRoom("802", 800, 0)

	// 自定义收费项目：宽带费 50 元/月（key 由服务端生成，从响应里取）。
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "宽带费", "cycle": "monthly", "default_amount": 50,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create fee item: %d %s", w.Code, w.Body.String())
	}
	broadbandKey := field(t, decodeBody(t, w), "data", "key").(string)

	// A 房租户排除宽带费，B 房租户不排除。
	env.seedTenantWithExclusions(roomA, "不含宽带", []string{broadbandKey})
	env.seedTenant(roomB, "全项参与")

	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	a := env.fetchBill("2026-10", "801")
	if !almostEqual(num(a["extra_amount"]), 0) || !almostEqual(num(a["total_amount"]), 800) {
		t.Errorf("A 房 extra/total = %v/%v, want 0/800（宽带费已排除）", a["extra_amount"], a["total_amount"])
	}
	bb := env.fetchBill("2026-10", "802")
	if !almostEqual(num(bb["extra_amount"]), 50) || !almostEqual(num(bb["total_amount"]), 850) {
		t.Errorf("B 房 extra/total = %v/%v, want 50/850（宽带费照常收取）", bb["extra_amount"], bb["total_amount"])
	}
}

// TestMonthlyCycleFixedFeesEveryMonth 月付租户连续生成账单时固定费每月照收
// （回归：fixedItemDue 此前漏了月付分支，第二个月起租金被清零）。
func TestMonthlyCycleFixedFeesEveryMonth(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("601", 1000, 0)
	env.setRoomFees(roomID, "601", 1000, 20, 30, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "月付租户"})

	for i, period := range []string{"2026-10", "2026-11", "2026-12"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", period, w.Code, w.Body.String())
		}
		b := env.fetchBill(period, "601")
		if !almostEqual(num(b["rent"]), 1000) || !almostEqual(num(b["sanitation_fee"]), 20) {
			t.Errorf("%s 账单租金/卫生费 = %v/%v, want 1000/20（月付每月到期）",
				period, b["rent"], b["sanitation_fee"])
		}
		if i > 0 && !almostEqual(num(b["total_amount"]), 1050) {
			t.Errorf("%s total = %v, want 1050（读数未录，仅固定费）", period, b["total_amount"])
		}
	}
}

func TestTenantIDCardSaved(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("901", 800, 0)

	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "证件租户", "id_card": " 110101199003077512 ",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create tenant: %d %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "id_card").(string); got != "110101199003077512" {
		t.Errorf("id_card = %q, want 去空白后落库", got)
	}
}
