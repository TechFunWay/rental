// elec_tou_test.go — 峰谷分时电价：分路用量×各自单价，合计列与抄表衔接。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

func TestElecFeeTOU(t *testing.T) {
	// 峰 100→150（50 度 ×1.5）+ 谷 200→280（80 度 ×0.6）= 75 + 48 = 123
	got := elecFee(true, 300, 430, 1.2,
		100, 150, 200, 280, 1.5, 0.6)
	if got != 123 {
		t.Fatalf("TOU fee = %v, want 123", got)
	}
	// 非 TOU 走单一电价：与改造前完全一致
	if got := elecFee(false, 800, 920, 1.2, 0, 0, 0, 0, 1.5, 0.6); got != 144 {
		t.Fatalf("flat fee = %v, want 144", got)
	}
	// 换表倒挂的一路按 0 用量
	if got := elecFee(true, 0, 0, 0, 10, 5, 0, 40, 1.5, 0.6); got != 24 {
		t.Fatalf("valley-only fee = %v, want 24", got)
	}
}

func TestElecTOUBillFlow(t *testing.T) {
	env := setupEnv(t)

	// 建峰谷房：底数峰 100 / 谷 200，峰价 1.5 谷价 0.6
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "A101", "default_rent": 1500,
		"elec_tou": true, "elec_peak_price": 1.5, "elec_valley_price": 0.6,
		"initial_elec": 300, "initial_elec_peak": 100, "initial_elec_valley": 200,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create room: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	env.seedTenant(roomID, "张三")

	// 建 2026-01 账单（默认本月=上月，用量 0）
	w = env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-01",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	bill := field(t, decodeBody(t, w), "data").(map[string]interface{})
	billID := uint(bill["id"].(float64))
	if bill["elec_tou"] != true {
		t.Fatalf("bill.elec_tou = %v, want true", bill["elec_tou"])
	}
	if num(bill["elec_peak_last"]) != 100 || num(bill["elec_valley_last"]) != 200 {
		t.Fatalf("peak/valley last = %v/%v, want 100/200",
			bill["elec_peak_last"], bill["elec_valley_last"])
	}

	// 录入本月峰 150、谷 280 → 电费 50×1.5 + 80×0.6 = 123；合计列 300→430
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-01",
		"elec_peak_now": 150.0, "elec_valley_now": 280.0,
		"elec_peak_price": 1.5, "elec_valley_price": 0.6,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update bill: %d %s", w.Code, w.Body.String())
	}
	updated := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(updated["elec_fee"]), 123) {
		t.Fatalf("elec_fee = %v, want 123", updated["elec_fee"])
	}
	if num(updated["elec_last"]) != 300 || num(updated["elec_now"]) != 430 {
		t.Fatalf("elec totals last/now = %v/%v, want 300/430",
			updated["elec_last"], updated["elec_now"])
	}

	// 下月建账：峰/谷读数各自衔接 150 / 280
	w = env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-02",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create feb bill: %d %s", w.Code, w.Body.String())
	}
	feb := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if num(feb["elec_peak_last"]) != 150 || num(feb["elec_valley_last"]) != 280 {
		t.Fatalf("Feb peak/valley last = %v/%v, want 150/280",
			feb["elec_peak_last"], feb["elec_valley_last"])
	}
	if num(feb["elec_last"]) != 430 {
		t.Fatalf("Feb elec_last total = %v, want 430", feb["elec_last"])
	}
}

func TestElecTOUSwitchFromFlat(t *testing.T) {
	env := setupEnv(t)

	// 单一电价房：底数 500
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "B201", "default_rent": 1200, "initial_elec": 500,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create room: %d", w.Code)
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	env.seedTenant(roomID, "李四")

	// 1 月账单：100→180 ×1.2 = 96
	w = env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-01",
	})
	billID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-01",
		"elec_last": 100.0, "elec_now": 180.0, "elec_price": 1.2,
	})
	updated := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(updated["elec_fee"]), 96) {
		t.Fatalf("flat fee = %v, want 96", updated["elec_fee"])
	}

	// 房源切到峰谷
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "B201", "default_rent": 1200, "initial_elec": 500,
		"elec_tou": true, "elec_peak_price": 1.5, "elec_valley_price": 0.6,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("enable TOU: %d %s", w.Code, w.Body.String())
	}

	// 2 月账单：上期总读数并入峰段，本月峰 200 谷 50 → 20×1.5 + 50×0.6 = 60
	w = env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-02",
	})
	febID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", febID), map[string]interface{}{
		"room_id": roomID, "period": "2026-02",
		"elec_peak_now": 200.0, "elec_valley_now": 50.0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update feb: %d %s", w.Code, w.Body.String())
	}
	feb := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if num(feb["elec_peak_last"]) != 180 || num(feb["elec_valley_last"]) != 0 {
		t.Fatalf("switch peak/valley last = %v/%v, want 180/0",
			feb["elec_peak_last"], feb["elec_valley_last"])
	}
	if !almostEqual(num(feb["elec_fee"]), 60) {
		t.Fatalf("switch TOU fee = %v, want 60", feb["elec_fee"])
	}
}
