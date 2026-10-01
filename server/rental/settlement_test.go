// settlement_test.go — 账单与结算口径：一律按"设置好的计费方式"计算。
//
// 覆盖三条线：
//  1. 生成与结算：水费按该房租户的计费方式与金额算（包月按月数放大、按吨按用量），
//     租金/卫生费/管理费按缴费周期放大；单据数据、收款、欠缴与状态共用同一口径；
//  2. 账单内切换计费方式：没给金额时按该房配置的对应口径金额预填（与 waterAmountForMode 一致），
//     非法方式直接 400，不会被静默按"按吨"处理；
//  3. 快照语义：出账后的账单不随租户后续改动而变，新账单按新设置计算。
package rental

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// seedTenantViaAPI 走接口登记租户，同时验证计费入参归一与落库。
func (e *testEnv) seedTenantViaAPI(roomID uint, body map[string]interface{}) map[string]interface{} {
	e.t.Helper()
	body["room_id"] = roomID
	w := e.do(http.MethodPost, "/api/rental/tenants", body)
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed tenant via api: %d %s", w.Code, w.Body.String())
	}
	return field(e.t, decodeBody(e.t, w), "data").(map[string]interface{})
}

// fetchBill 按账期与房号取唯一一张账单。
func (e *testEnv) fetchBill(period, roomNo string) map[string]interface{} {
	e.t.Helper()
	w := e.do(http.MethodGet, "/api/rental/bills?period="+period+"&keyword="+roomNo, nil)
	items := e.items(decodeBody(e.t, w))
	if len(items) != 1 {
		e.t.Fatalf("%s %s bills = %d, want 1: %s", period, roomNo, len(items), w.Body.String())
	}
	return items[0].(map[string]interface{})
}

// setRoomFees 通过接口改房源的默认租金与卫生费/管理费。
func (e *testEnv) setRoomFees(roomID uint, roomNo string, rent, sanitation, management, initialWater float64) {
	e.t.Helper()
	w := e.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": roomNo, "default_rent": rent,
		"default_sanitation_fee": sanitation, "default_management_fee": management,
		"initial_water": initialWater,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("set room fees: %d %s", w.Code, w.Body.String())
	}
}

// TestBillSettlementFollowsTenantMonthlyWater 包月 + 季付：账单、单据、收款与
// 缴费提醒的预计应缴都按同一口径（水费 = 包月金额 × 周期月数）。
func TestBillSettlementFollowsTenantMonthlyWater(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("901", 1000, 0)
	env.setRoomFees(roomID, "901", 1000, 20, 30, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "包月季付", "water_mode": "monthly", "water_monthly_fee": 40,
		"pay_cycle": "quarterly", "pay_day": 5, "remind_days": 3,
	})

	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	b := env.fetchBill("2026-09", "901")
	if b["pay_cycle"] != payCycleQuarterly || b["water_mode"] != waterModeMonthly {
		t.Fatalf("bill cycle/mode = %v/%v, want quarterly/monthly", b["pay_cycle"], b["water_mode"])
	}
	if !almostEqual(num(b["water_price"]), 40) {
		t.Errorf("water_price = %v, want 40（租户设置的包月金额）", b["water_price"])
	}
	if !almostEqual(num(b["water_fee"]), 40) {
		t.Errorf("water_fee = %v, want 40（包月按月收，水电不再随季付 ×3）", b["water_fee"])
	}
	if !almostEqual(num(b["rent"]), 3000) || !almostEqual(num(b["sanitation_fee"]), 60) || !almostEqual(num(b["management_fee"]), 90) {
		t.Errorf("季付费用 = 租金 %v / 卫生 %v / 管理 %v, want 3000/60/90",
			b["rent"], b["sanitation_fee"], b["management_fee"])
	}
	if !almostEqual(num(b["total_amount"]), 3190) {
		t.Errorf("total = %v, want 3270", b["total_amount"])
	}
	billID := uint(b["id"].(float64))

	// 单据数据（结算凭据）与账单同口径。
	w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billID), nil)
	receipt := field(t, decodeBody(t, w), "data", "bill").(map[string]interface{})
	if receipt["water_mode"] != waterModeMonthly || !almostEqual(num(receipt["water_fee"]), 40) ||
		!almostEqual(num(receipt["total_amount"]), 3190) {
		t.Errorf("receipt = mode %v fee %v total %v, want monthly/40/3190",
			receipt["water_mode"], receipt["water_fee"], receipt["total_amount"])
	}

	// 收 1000 → 部分已缴；结算口径不变。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": 1000})
	paid := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if paid["status"] != billStatusPartial || !almostEqual(num(paid["total_amount"]), 3190) || !almostEqual(num(paid["paid_amount"]), 1000) {
		t.Errorf("after pay = %v/%v/%v, want partial/3190/1000", paid["status"], paid["total_amount"], paid["paid_amount"])
	}

	// 包月不看抄表：录入读数后水费与合计保持不变。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "tenant_name": "包月季付", "rent": 3000,
		"water_last": 100, "water_now": 180, "water_mode": waterModeMonthly, "water_price": 40,
		"elec_last": 0, "elec_now": 0, "elec_price": 1.2, "gas_last": 0, "gas_now": 0, "gas_price": 3.5,
		"sanitation_fee": 60, "management_fee": 90, "paid_amount": 1000,
	})
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 40) || !almostEqual(num(upd["total_amount"]), 3190) || upd["status"] != billStatusPartial {
		t.Errorf("after reading edit = fee %v total %v status %v, want 40/3190/partial",
			upd["water_fee"], upd["total_amount"], upd["status"])
	}

	// 缴费提醒的预计应缴同口径：季付租金等 ×3 + 包月水费按月收 = 3190。
	var room Room
	if err := env.db.First(&room, roomID).Error; err != nil {
		t.Fatalf("reload room: %v", err)
	}
	today := time.Date(2026, 10, 3, 0, 0, 0, 0, time.Local)
	item := nextPaymentDue(env.db, &room, env.billingOf(&room), today)
	if item == nil {
		t.Fatalf("payment due = nil")
	}
	if item.PayCycle != payCycleQuarterly || item.PayDay != 5 || item.Months != 3 {
		t.Errorf("payment due = %v/%v/%d, want quarterly/5/3", item.PayCycle, item.PayDay, item.Months)
	}
	if !almostEqual(item.ExpectedAmount, 3190) {
		t.Errorf("expected amount = %v, want 3190（季付固定费 ×3，包月水费按月）", item.ExpectedAmount)
	}
}

// TestBillSettlementFollowsTenantMeterWater 按吨：水费 = 用量 × 租户设置的单价，
// 改读数后合计、欠缴与状态随之重算。
func TestBillSettlementFollowsTenantMeterWater(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("902", 1000, 0)
	env.setRoomFees(roomID, "902", 1000, 0, 0, 100) // 水表底数 100
	env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "按吨月付", "water_mode": "meter", "water_price": 6,
		"pay_cycle": "monthly", "pay_day": 10,
	})

	// 开票即抄表：本月 130 → 用量 30 × 6 = 180。
	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "water_now": 130,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	b := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if b["water_mode"] != waterModeMeter || !almostEqual(num(b["water_price"]), 6) {
		t.Fatalf("water = %v/%v, want meter/6（租户设置的按吨价）", b["water_mode"], b["water_price"])
	}
	if !almostEqual(num(b["water_last"]), 100) || !almostEqual(num(b["water_now"]), 130) {
		t.Errorf("readings = %v→%v, want 100→130（底数衔接）", b["water_last"], b["water_now"])
	}
	if !almostEqual(num(b["water_fee"]), 180) || !almostEqual(num(b["total_amount"]), 1180) {
		t.Fatalf("meter bill = fee %v total %v, want 180/1180", b["water_fee"], b["total_amount"])
	}
	billID := uint(b["id"].(float64))

	// 收清 → 已缴清。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": 1180})
	if paid := field(t, decodeBody(t, w), "data").(map[string]interface{}); paid["status"] != billStatusPaid {
		t.Fatalf("after pay status = %v, want paid", paid["status"])
	}

	// 改本月读数为 150 → 用量 50 × 6 = 300，合计 1300，已收 1180 → 部分已缴（欠缴 120）。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "tenant_name": "按吨月付", "rent": 1000,
		"water_last": 100, "water_now": 150, "water_mode": waterModeMeter, "water_price": 6,
		"elec_last": 0, "elec_now": 0, "elec_price": 1.2, "gas_last": 0, "gas_now": 0, "gas_price": 3.5,
		"paid_amount": 1180,
	})
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 300) || !almostEqual(num(upd["total_amount"]), 1300) {
		t.Errorf("after edit = fee %v total %v, want 300/1300", upd["water_fee"], upd["total_amount"])
	}
	if upd["status"] != billStatusPartial {
		t.Errorf("after edit status = %v, want partial（收 1180 < 应付 1300）", upd["status"])
	}
	if arrears := num(upd["total_amount"]) - num(upd["paid_amount"]); !almostEqual(arrears, 120) {
		t.Errorf("arrears = %v, want 120", arrears)
	}
}

// TestBillModeSwitchTakesConfiguredAmount 账单里切换计费方式时，没带金额就按
// 该房配置的对应口径金额计算；非法方式直接 400。
func TestBillModeSwitchTakesConfiguredAmount(t *testing.T) {
	env := setupEnv(t)
	for k, v := range map[string]string{"rental_water_price": "5", "rental_water_monthly_fee": "20"} {
		if w := env.do(http.MethodPut, "/api/configs", map[string]interface{}{"key": k, "value": v}); w.Code != http.StatusOK {
			t.Fatalf("set config %s: %d %s", k, w.Code, w.Body.String())
		}
	}
	roomID := env.seedRoom("903", 1000, 0)
	// 租户：包月 45；另给一个按吨价 6，验证两种口径各自的配置金额。
	env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "可切换", "water_mode": "monthly", "water_monthly_fee": 45, "water_price": 6,
	})

	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{"room_id": roomID, "period": "2026-09"})
	if w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	billID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if b := env.fetchBill("2026-09", "903"); !almostEqual(num(b["water_fee"]), 45) {
		t.Fatalf("initial water_fee = %v, want 45（租户包月金额）", b["water_fee"])
	}

	// 非法方式：编辑与建账都拒绝，不静默当按吨。
	put := func(body map[string]interface{}) *httptest.ResponseRecorder {
		body["room_id"] = roomID
		body["period"] = "2026-09"
		return env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), body)
	}
	if w := put(map[string]interface{}{"water_mode": "weekly"}); w.Code != http.StatusBadRequest {
		t.Errorf("update weekly mode = %d, want 400 (%s)", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-10", "water_mode": "weekly",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("create weekly mode = %d, want 400 (%s)", w.Code, w.Body.String())
	}

	// 切成按吨、不带金额 → 取该房按吨口径金额（租户的 6 元/吨），用量 40 → 240。
	w = put(map[string]interface{}{
		"tenant_name": "可切换", "rent": 1000,
		"water_mode": waterModeMeter, "water_last": 100, "water_now": 140,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("switch to meter: %d %s", w.Code, w.Body.String())
	}
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_price"]), 6) || !almostEqual(num(upd["water_fee"]), 240) {
		t.Errorf("meter switch = price %v fee %v, want 6/240", upd["water_price"], upd["water_fee"])
	}

	// 切回包月、不带金额 → 取该房包月口径金额（45，月付 ×1）。
	w = put(map[string]interface{}{
		"tenant_name": "可切换", "rent": 1000,
		"water_mode": waterModeMonthly, "water_last": 100, "water_now": 140,
	})
	upd = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_price"]), 45) || !almostEqual(num(upd["water_fee"]), 45) {
		t.Errorf("monthly switch = price %v fee %v, want 45/45", upd["water_price"], upd["water_fee"])
	}

	// 同方式下显式给金额：以传入为准。
	w = put(map[string]interface{}{"tenant_name": "可切换", "rent": 1000, "water_mode": waterModeMonthly, "water_price": 60})
	upd = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_price"]), 60) || !almostEqual(num(upd["water_fee"]), 60) {
		t.Errorf("explicit price = %v/%v, want 60/60", upd["water_price"], upd["water_fee"])
	}
}

// TestBillSnapshotUnaffectedByTenantSettingChange 出账即快照：改租户设置只影响
// 之后新开的账单，已出账单的金额与结算口径不变。
func TestBillSnapshotUnaffectedByTenantSettingChange(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("904", 1000, 0)
	tenant := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "先包月", "water_mode": "monthly", "water_monthly_fee": 40, "pay_cycle": "monthly",
	})
	tenantID := uint(tenant["id"].(float64))

	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate 09: %d %s", w.Code, w.Body.String())
	}
	a := env.fetchBill("2026-09", "904")
	if !almostEqual(num(a["water_fee"]), 40) || !almostEqual(num(a["total_amount"]), 1040) {
		t.Fatalf("bill A = fee %v total %v, want 40/1040", a["water_fee"], a["total_amount"])
	}
	billAID := uint(a["id"].(float64))

	// 租户改成按吨 6 元/吨。
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "先包月后按吨", "water_mode": waterModeMeter, "water_price": 6,
		"pay_cycle": payCycleMonthly,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update tenant: %d %s", w.Code, w.Body.String())
	}

	// 老账单仍是快照（包月 40），金额与结算口径不动。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", billAID), nil)
	old := field(t, decodeBody(t, w), "data", "bill").(map[string]interface{})
	if old["water_mode"] != waterModeMonthly || !almostEqual(num(old["water_price"]), 40) ||
		!almostEqual(num(old["water_fee"]), 40) || !almostEqual(num(old["total_amount"]), 1040) {
		t.Errorf("bill A changed = %v/%v/%v/%v, want monthly/40/40/1040",
			old["water_mode"], old["water_price"], old["water_fee"], old["total_amount"])
	}

	// 新账单按新设置（按吨 6）算：10 月沿用 9 月读数（0），改读数后 30 × 6 = 180。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate 10: %d %s", w.Code, w.Body.String())
	}
	b := env.fetchBill("2026-10", "904")
	if b["water_mode"] != waterModeMeter || !almostEqual(num(b["water_price"]), 6) {
		t.Fatalf("bill B = %v/%v, want meter/6", b["water_mode"], b["water_price"])
	}
	billBID := uint(b["id"].(float64))
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billBID), map[string]interface{}{
		"room_id": roomID, "period": "2026-10", "tenant_name": "先包月后按吨", "rent": 1000,
		"water_last": 0, "water_now": 30, "water_mode": waterModeMeter, "water_price": 6,
	})
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 180) || !almostEqual(num(upd["total_amount"]), 1180) {
		t.Errorf("bill B after edit = fee %v total %v, want 180/1180", upd["water_fee"], upd["total_amount"])
	}
}

// TestRoomBillingViewExposesBothModeAmounts 房源出参把两种口径的金额都给出来，
// 前端切计费方式时才能按配置金额预填。
func TestRoomBillingViewExposesBothModeAmounts(t *testing.T) {
	env := setupEnv(t)
	for k, v := range map[string]string{"rental_water_price": "5", "rental_water_monthly_fee": "30"} {
		if w := env.do(http.MethodPut, "/api/configs", map[string]interface{}{"key": k, "value": v}); w.Code != http.StatusOK {
			t.Fatalf("set config %s: %d %s", k, w.Code, w.Body.String())
		}
	}
	env.seedRoom("905", 1000, 0) // 无租户：完全跟随全局
	roomB := env.seedRoom("906", 1000, 0)
	env.seedTenantViaAPI(roomB, map[string]interface{}{
		"name": "包月四十五", "water_mode": "monthly", "water_monthly_fee": 45, "water_price": 6,
	})

	check := func(roomNo string, mode string, amount, meterPrice, monthlyFee float64) {
		t.Helper()
		w := env.do(http.MethodGet, "/api/rental/rooms?keyword="+roomNo, nil)
		room := env.items(decodeBody(t, w))[0].(map[string]interface{})
		billing, ok := room["billing"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s billing missing: %s", roomNo, w.Body.String())
		}
		if billing["water_mode"] != mode ||
			!almostEqual(num(billing["water_amount"]), amount) ||
			!almostEqual(num(billing["water_meter_price"]), meterPrice) ||
			!almostEqual(num(billing["water_monthly_fee"]), monthlyFee) {
			t.Errorf("%s billing = %v, want mode %s / amount %v / meter %v / monthly %v",
				roomNo, billing, mode, amount, meterPrice, monthlyFee)
		}
	}
	check("905", waterModeMeter, 5, 5, 30)
	check("906", waterModeMonthly, 45, 6, 45)
}
