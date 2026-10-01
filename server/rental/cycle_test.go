// cycle_test.go — 缴费周期(月付/季付)与缴费提醒的口径测试。
package rental

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"smallgo/server/notify"
)

// ---------------------------------------------------------------------------
// 纯函数
// ---------------------------------------------------------------------------

func TestPayCycleHelpers(t *testing.T) {
	if got := normalizePayCycle(""); got != payCycleMonthly {
		t.Errorf("normalizePayCycle(\"\") = %q, want monthly", got)
	}
	if got := normalizePayCycle("quarterly"); got != payCycleQuarterly {
		t.Errorf("normalizePayCycle(quarterly) = %q, want quarterly", got)
	}
	if got := normalizePayCycle("weekly"); got != payCycleMonthly {
		t.Errorf("normalizePayCycle(weekly) = %q, want monthly", got)
	}
	if got := cycleMonths(payCycleMonthly); got != 1 {
		t.Errorf("cycleMonths(monthly) = %d, want 1", got)
	}
	if got := cycleMonths(payCycleQuarterly); got != 3 {
		t.Errorf("cycleMonths(quarterly) = %d, want 3", got)
	}
	if got := cycleLabel(payCycleQuarterly); got != "季付" {
		t.Errorf("cycleLabel(quarterly) = %q, want 季付", got)
	}
	if got := parsePayCycle("季付"); got != payCycleQuarterly {
		t.Errorf("parsePayCycle(季付) = %q, want quarterly", got)
	}
	if got := parsePayCycle("MONTHLY"); got != payCycleMonthly {
		t.Errorf("parsePayCycle(MONTHLY) = %q, want monthly", got)
	}
	if got := parsePayCycle("半年付"); got != "" {
		t.Errorf("parsePayCycle(半年付) = %q, want 空", got)
	}
}

func TestPeriodArithmetic(t *testing.T) {
	// 跨年：2026-11 + 3 = 2027-02。
	if got := addMonths("2026-11", 3); got != "2027-02" {
		t.Errorf("addMonths(2026-11, 3) = %q, want 2027-02", got)
	}
	if got := addMonths("2026-09", -3); got != "2026-06" {
		t.Errorf("addMonths(2026-09, -3) = %q, want 2026-06", got)
	}
	if got := addMonths("bad", 3); got != "bad" {
		t.Errorf("addMonths(bad, 3) = %q, want 原值", got)
	}
	if got := monthsBetween("2026-06", "2027-02"); got != 8 {
		t.Errorf("monthsBetween(2026-06, 2027-02) = %d, want 8", got)
	}
	if got := monthsBetween("2026-09", "2026-06"); got != -3 {
		t.Errorf("monthsBetween(2026-09, 2026-06) = %d, want -3", got)
	}
	if got := monthsBetween("bad", "2026-09"); got != 0 {
		t.Errorf("monthsBetween(bad, ...) = %d, want 0", got)
	}
	if got := clampRemindDays(-5); got != 0 {
		t.Errorf("clampRemindDays(-5) = %d, want 0", got)
	}
	if got := clampRemindDays(99); got != remindDaysMax {
		t.Errorf("clampRemindDays(99) = %d, want %d", got, remindDaysMax)
	}
}

// ---------------------------------------------------------------------------
// 租户计费与缴费字段校验
// ---------------------------------------------------------------------------

func TestTenantBillingValidation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("801", 1000, 0)

	cases := []struct {
		name string
		body map[string]interface{}
		want int
	}{
		{"季付+缴费日合法", map[string]interface{}{"room_id": roomID, "name": "甲", "pay_cycle": "quarterly", "pay_day": 5, "remind_days": 3}, http.StatusOK},
		{"中文季付", map[string]interface{}{"room_id": roomID, "name": "乙", "pay_cycle": "季付", "pay_day": 1}, http.StatusOK},
		{"非法周期", map[string]interface{}{"room_id": roomID, "name": "丙", "pay_cycle": "weekly"}, http.StatusBadRequest},
		{"缴费日越界", map[string]interface{}{"room_id": roomID, "name": "丁", "pay_day": 29}, http.StatusBadRequest},
		{"缴费日 -1 跟随全局", map[string]interface{}{"room_id": roomID, "name": "戊", "pay_day": -1}, http.StatusOK},
		{"提醒天数越界", map[string]interface{}{"room_id": roomID, "name": "己", "remind_days": 31}, http.StatusBadRequest},
		{"非法水费方式", map[string]interface{}{"room_id": roomID, "name": "庚", "water_mode": "weekly"}, http.StatusBadRequest},
		{"包月金额合法", map[string]interface{}{"room_id": roomID, "name": "辛", "water_mode": "包月", "water_monthly_fee": 40}, http.StatusOK},
	}
	for _, tc := range cases {
		w := env.do(http.MethodPost, "/api/rental/tenants", tc.body)
		if w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, w.Code, tc.want, w.Body.String())
		}
	}

	// 设置落库并归一为规范值。
	w := env.do(http.MethodGet, "/api/rental/tenants?keyword=甲", nil)
	tenant := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if tenant["pay_cycle"] != payCycleQuarterly || num(tenant["pay_day"]) != 5 || num(tenant["remind_days"]) != 3 {
		t.Errorf("tenant billing = %+v, want quarterly/5/3", tenant)
	}
	// 中文"季付"归一；没传的提醒天数存 -1（跟随全局），不是 0。
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=乙", nil)
	tenant = env.items(decodeBody(t, w))[0].(map[string]interface{})
	if tenant["pay_cycle"] != payCycleQuarterly {
		t.Errorf("中文季付 pay_cycle = %v, want quarterly", tenant["pay_cycle"])
	}
	if num(tenant["pay_day"]) != 1 || num(tenant["remind_days"]) != -1 {
		t.Errorf("unset remind_days = %v, want -1（跟随全局）", tenant["remind_days"])
	}
	// 中文"包月"归一为 monthly。
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=辛", nil)
	tenant = env.items(decodeBody(t, w))[0].(map[string]interface{})
	if tenant["water_mode"] != waterModeMonthly || !almostEqual(num(tenant["water_monthly_fee"]), 40) {
		t.Errorf("包月 tenant = %+v, want monthly/40", tenant)
	}
}

// ---------------------------------------------------------------------------
// 季付开票
// ---------------------------------------------------------------------------

func TestQuarterlyBilling(t *testing.T) {
	env := setupEnv(t)

	// 季付租户：租金 1000、卫生 20、管理 30、水包月 40、每 3 个月一张账单。
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "901", "default_rent": 1000, "default_sanitation_fee": 20,
		"default_management_fee": 30,
	})
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "季付租户", "water_mode": "monthly", "water_monthly_fee": 40,
		"pay_cycle": "quarterly", "pay_day": 5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed quarterly tenant: %d %s", w.Code, w.Body.String())
	}

	// 首账月 2026-09：租金/卫生/管理/包月水都按 3 个月计。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate 09: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09", nil)
	bills := env.items(decodeBody(t, w))
	if len(bills) != 1 {
		t.Fatalf("sept bills = %d, want 1", len(bills))
	}
	b := bills[0].(map[string]interface{})
	if b["pay_cycle"] != payCycleQuarterly {
		t.Fatalf("pay_cycle = %v, want quarterly", b["pay_cycle"])
	}
	if !almostEqual(num(b["rent"]), 3000) {
		t.Errorf("rent = %v, want 3000", b["rent"])
	}
	if !almostEqual(num(b["sanitation_fee"]), 60) {
		t.Errorf("sanitation = %v, want 60", b["sanitation_fee"])
	}
	if !almostEqual(num(b["management_fee"]), 90) {
		t.Errorf("management = %v, want 90", b["management_fee"])
	}
	// 包月水费按月收（水电不再随季付 ×3），首账月固定费一次收 3 个月。
	if !almostEqual(num(b["water_fee"]), 40) {
		t.Errorf("water_fee = %v, want 40", b["water_fee"])
	}
	if !almostEqual(num(b["total_amount"]), 3190) {
		t.Errorf("total = %v, want 3190", b["total_amount"])
	}
	billID := uint(b["id"].(float64))

	// 季付房 10 月是中间月份：仍出"月账单"——只有水电等月付项目（水费按月收），
	// 固定费（租金等）未到期不计。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate 10: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-10&keyword=901", nil)
	oct := env.items(decodeBody(t, w))
	if got := len(oct); got != 1 {
		t.Fatalf("oct bills for quarterly room = %d, want 1", got)
	} else {
		ob := oct[0].(map[string]interface{})
		if !almostEqual(num(ob["rent"]), 0) || !almostEqual(num(ob["water_fee"]), 40) || !almostEqual(num(ob["total_amount"]), 40) {
			t.Errorf("oct bill = rent %v water %v total %v, want 0/40/40（月账单）",
				ob["rent"], ob["water_fee"], ob["total_amount"])
		}
	}

	// 12 月是下一个账单月（整差 3 个月），可以出账，读数衔接 9 月账单。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-12"}); w.Code != http.StatusOK {
		t.Fatalf("generate 12: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-12", nil)
	q := env.items(decodeBody(t, w))
	if len(q) != 1 {
		t.Fatalf("dec bills = %d, want 1", len(q))
	}
	if !almostEqual(num(q[0].(map[string]interface{})["rent"]), 3000) {
		t.Errorf("dec rent = %v, want 3000", q[0].(map[string]interface{})["rent"])
	}

	// 编辑季付账单：包月水费按月收（40 元/月），不随抄表读数变化。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "tenant_name": "季付租户", "rent": 3000,
		"water_last": 100, "water_now": 180, "water_price": 40, "water_mode": waterModeMonthly,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 40) {
		t.Errorf("quarterly monthly water_fee with readings = %v, want 40", upd["water_fee"])
	}
}

// ---------------------------------------------------------------------------
// 出账节奏 nextPaymentDue / computePaymentDue
// ---------------------------------------------------------------------------

// seedPayRoom 建一间房，并给它在租租户设置缴费周期/缴费日/提醒天数
// （v0.2.3 起这些设置挂在租户上）；tenantName 为空表示空置房。
func (e *testEnv) seedPayRoom(roomNo, tenantName, cycle string, payDay, remindDays int) *Room {
	e.t.Helper()
	room := Room{UserID: 1, RoomNo: roomNo, DefaultRent: 1000}
	if err := e.db.Create(&room).Error; err != nil {
		e.t.Fatalf("seed pay room: %v", err)
	}
	if tenantName != "" {
		e.seedBillingTenant(room.ID, tenantName, cycle, payDay, remindDays)
	}
	return &room
}

// seedBillingTenant 直接落库一名在租租户并写入计费与缴费设置（绕过 API）。
func (e *testEnv) seedBillingTenant(roomID uint, name, cycle string, payDay, remindDays int) *Tenant {
	e.t.Helper()
	tenant := Tenant{UserID: 1, RoomID: roomID, Name: name, Active: true,
		PayCycle: cycle, PayDay: payDay, RemindDays: remindDays}
	if err := e.db.Create(&tenant).Error; err != nil {
		e.t.Fatalf("seed billing tenant: %v", err)
	}
	return &tenant
}

// billingOf 取房间当前生效的计费与缴费设置。
func (e *testEnv) billingOf(room *Room) billingConfig {
	return effectiveRoomBilling(e.db, room)
}

func TestNextPaymentDue(t *testing.T) {
	env := setupEnv(t)
	today := time.Date(2026, 10, 3, 15, 0, 0, 0, time.Local)

	// 月付房、无账单：待收账期 = 当前月，缴费日 5 号 → 还差 2 天。
	room := env.seedPayRoom("A1", "甲", payCycleMonthly, 5, 3)
	item := nextPaymentDue(env.db, room, env.billingOf(room), today)
	if item == nil {
		t.Fatalf("monthly no-bill due = nil")
	}
	if item.Period != "2026-10" || item.DueDate != "2026-10-05" || item.DaysLeft != 2 {
		t.Errorf("monthly due = %+v", item)
	}
	if !almostEqual(item.ExpectedAmount, 1000) {
		t.Errorf("expected amount = %v, want 1000", item.ExpectedAmount)
	}

	// 最近一张未收清 → 待收账期就是它（逾期）。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{"room_id": room.ID, "period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("create bill: %s", w.Body.String())
	}
	item = nextPaymentDue(env.db, room, env.billingOf(room), today)
	if item.Period != "2026-09" || item.DueDate != "2026-09-05" || item.DaysLeft != -28 {
		t.Errorf("unpaid last bill due = %+v", item)
	}
	if item.BillID == 0 {
		t.Errorf("unpaid last bill should link bill id")
	}

	// 收清 9 月后 → 下一期 10 月。
	env.db.Model(&Bill{}).Where("room_id = ? AND period = ?", room.ID, "2026-09").
		Updates(map[string]interface{}{"paid_amount": 1000, "status": billStatusPaid})
	item = nextPaymentDue(env.db, room, env.billingOf(room), today)
	if item.Period != "2026-10" || item.DaysLeft != 2 {
		t.Errorf("paid last bill due = %+v", item)
	}

	// 季付房、9 月账单已收清 → 下一期 12 月。
	qroom := env.seedPayRoom("A2", "乙", payCycleQuarterly, 5, 3)
	env.db.Create(&Bill{UserID: 1, RoomID: qroom.ID, Period: "2026-09", RoomNo: "A2",
		PayCycle: payCycleQuarterly, Rent: 3000, TotalAmount: 3000, Status: billStatusPaid})
	item = nextPaymentDue(env.db, qroom, env.billingOf(qroom), today)
	if item.Period != "2026-12" || item.DueDate != "2026-12-05" || item.Months != 3 {
		t.Errorf("quarterly due = %+v", item)
	}
	if !almostEqual(item.ExpectedAmount, 3000) {
		t.Errorf("quarterly expected = %v, want 3000", item.ExpectedAmount)
	}

	// 未设置缴费日 / 空置房 → 不提醒。
	noRemind := env.seedPayRoom("A3", "丙", payCycleMonthly, 0, 3)
	if got := nextPaymentDue(env.db, noRemind, env.billingOf(noRemind), today); got != nil {
		t.Errorf("pay_day=0 should be nil, got %+v", got)
	}
	vacant := env.seedPayRoom("A4", "", payCycleMonthly, 5, 3)
	if got := nextPaymentDue(env.db, vacant, env.billingOf(vacant), today); got != nil {
		t.Errorf("vacant room should be nil, got %+v", got)
	}
}

func TestComputePaymentDueWindow(t *testing.T) {
	env := setupEnv(t)
	today := time.Date(2026, 10, 3, 0, 0, 0, 0, time.Local)

	// 全局默认提醒 3 天：缴费日 20 号还差 17 天 → 窗口外。
	if w := env.do(http.MethodPut, "/api/configs", map[string]interface{}{"key": "rental_remind_days", "value": "3"}); w.Code != http.StatusOK {
		t.Fatalf("set remind days: %s", w.Body.String())
	}
	far := env.seedPayRoom("B1", "远期", payCycleMonthly, 20, -1)
	if got := computePaymentDue(env.db, 1, today); len(got) != 0 {
		t.Errorf("far due should be filtered, got %+v", got)
	}

	// 租户覆盖提醒 30 天 → 进入窗口。
	env.db.Model(&Tenant{}).Where("room_id = ?", far.ID).Update("remind_days", 30)
	got := computePaymentDue(env.db, 1, today)
	if len(got) != 1 || got[0].RoomNo != "B1" {
		t.Errorf("remind 30 should include B1, got %+v", got)
	}

	// 逾期恒提醒，即使提醒天数是 0。
	overdue := env.seedPayRoom("B2", "逾期", payCycleMonthly, 1, 0)
	env.db.Create(&Bill{UserID: 1, RoomID: overdue.ID, Period: "2026-09", RoomNo: "B2",
		Rent: 1000, TotalAmount: 1000, Status: billStatusUnpaid})
	got = computePaymentDue(env.db, 1, today)
	if len(got) != 2 || got[0].RoomNo != "B2" || got[0].DaysLeft >= 0 {
		t.Errorf("overdue should come first with negative days, got %+v", got)
	}
}

func TestStatsPaymentDue(t *testing.T) {
	env := setupEnv(t)

	// 缴费日设为"今天"（或 28 号兜底），当期账单未收清 → 一定落在提醒窗口内。
	payDay := time.Now().Day()
	if payDay > payDayMax {
		payDay = payDayMax
	}
	env.seedPayRoom("C1", "仪表盘", payCycleMonthly, payDay, 3)
	period := time.Now().Format("2006-01")
	env.createBill("C1", period)

	w := env.do(http.MethodGet, "/api/rental/stats", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	due := field(t, decodeBody(t, w), "data", "payment_due").([]interface{})
	if len(due) != 1 {
		t.Fatalf("payment_due = %d items, want 1: %s", len(due), w.Body.String())
	}
	item := due[0].(map[string]interface{})
	if item["room_no"] != "C1" || item["pay_cycle"] != payCycleMonthly {
		t.Errorf("payment_due item = %+v", item)
	}
	if item["period"] != period {
		t.Errorf("payment_due period = %v, want %v", item["period"], period)
	}
}

// ---------------------------------------------------------------------------
// CSV
// ---------------------------------------------------------------------------

func TestPayCycleCSV(t *testing.T) {
	env := setupEnv(t)

	// 建一间房 + 一名季付租户并出账（缴费设置在租户上）。
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "D1", "default_rent": 1000,
	})
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "季付csv", "pay_cycle": "quarterly", "pay_day": 5, "remind_days": 3,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed quarterly tenant: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}

	// 房源导出包含周期/缴费日/提醒天数；账单导出包含缴费周期与"季付"。
	w = env.do(http.MethodGet, "/api/rental/rooms/export", nil)
	roomsCSV := strings.TrimPrefix(w.Body.String(), "\ufeff")
	for _, col := range []string{"缴费周期", "缴费日", "提前提醒天数", "季付"} {
		if !strings.Contains(roomsCSV, col) {
			t.Errorf("rooms export missing %q:\n%s", col, roomsCSV)
		}
	}
	w = env.do(http.MethodGet, "/api/rental/export", nil)
	billsCSV := strings.TrimPrefix(w.Body.String(), "\ufeff")
	if !strings.Contains(billsCSV, "缴费周期") || !strings.Contains(billsCSV, "季付") {
		t.Errorf("bills export missing pay cycle:\n%s", billsCSV)
	}

	// 新模板（19 列，含缴费周期）导入：季付落库到账单与自动建的租户。
	header := "房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注,水费模式,缴费周期\n"
	row := "D2,新季付,2026-09,3000,0,0,0,0,0,0,5,1.2,3.5,0,0,0,,按吨,季付\n"
	w = env.importCSVBody(header + row)
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=D2", nil)
	b := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b["pay_cycle"] != payCycleQuarterly {
		t.Errorf("imported bill pay_cycle = %v, want quarterly", b["pay_cycle"])
	}
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=新季付", nil)
	tenant := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if tenant["pay_cycle"] != payCycleQuarterly || tenant["water_mode"] != waterModeMeter {
		t.Errorf("auto tenant = %+v, want quarterly/meter", tenant)
	}

	// 旧 18 列模板（只有水费模式）仍可导入，周期缺省月付。
	legacyHeader := "房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注,水费模式\n"
	legacyRow := "D3,旧模板,2026-09,1000,0,0,0,0,0,0,5,1.2,3.5,0,0,0,,按吨\n"
	w = env.importCSVBody(legacyHeader + legacyRow)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy import: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=D3", nil)
	b3 := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b3["pay_cycle"] != payCycleMonthly {
		t.Errorf("legacy bill pay_cycle = %v, want monthly", b3["pay_cycle"])
	}
}

// ---------------------------------------------------------------------------
// 每日推送
// ---------------------------------------------------------------------------

func TestPaymentDigestText(t *testing.T) {
	items := []PaymentDueItem{
		{RoomNo: "501", TenantName: "张三", PayCycle: payCycleQuarterly, PayDay: 5,
			Period: "2026-10", Months: 3, DueDate: "2026-10-05", DaysLeft: 3, ExpectedAmount: 3050},
		{RoomNo: "402", TenantName: "李四", PayCycle: payCycleMonthly, PayDay: 1,
			Period: "2026-09", Months: 1, DueDate: "2026-09-01", DaysLeft: -2, ExpectedAmount: 1050},
	}
	msg := paymentDigestMessage(items)
	if msg.Title != "缴费提醒" {
		t.Errorf("title = %q", msg.Title)
	}
	// 逾期行在前，且带"已逾期"；钉钉安全关键词"提醒"在标题里。
	if !strings.Contains(msg.Body, "已逾期 2 天") || !strings.Contains(msg.Body, "3 天后到期") {
		t.Errorf("body = %q", msg.Body)
	}
	if !strings.Contains(msg.Body, "季付") || !strings.Contains(msg.Body, "501") {
		t.Errorf("body = %q", msg.Body)
	}
}

func TestDispatchWithoutBindings(t *testing.T) {
	env := setupEnv(t)
	// 有待提醒房间、但没绑任何渠道：派发应为空操作且不报错。
	env.seedPayRoom("E1", "无渠道", payCycleMonthly, time.Now().Day(), 3)
	dispatchPaymentNotices(env.db)
	var logs int64
	env.db.Model(&notify.NotifyLog{}).Count(&logs)
	if logs != 0 {
		t.Errorf("notify logs = %d, want 0（未绑定渠道不应产生发送记录）", logs)
	}
}
