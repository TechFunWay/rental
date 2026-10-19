// fees_test.go — 收费项目自定义、按项目周期出账与按项目收款的集成测试。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// TestSemiannualCustomFeeAnchored 自定义收费项目按**自身周期**锚点收：
// 半年付宽带费 30 元/月 → 锚点月 180 元，非锚点月 0，且不被房间缴费周期影响。
// （回归：customBillItems 此前只认 quarterly，半年付项目会被每月重复收。）
func TestSemiannualCustomFeeAnchored(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("601", 1000, 0)
	env.setRoomFees(roomID, "601", 1000, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "月付房", "pay_day": 5})

	// 月付房 + 半年付自定义项目（宽带费 30 元/月，按半年收一次）。
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "宽带费", "cycle": "semiannual", "default_amount": 30,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create fee item: %d %s", w.Code, w.Body.String())
	}
	broadbandKey := field(t, decodeBody(t, w), "data", "key").(string)

	periods := []string{"2026-10", "2026-11", "2026-12", "2027-01", "2027-02", "2027-03", "2027-04"}
	extraByPeriod := map[string]float64{}
	rentByPeriod := map[string]float64{}
	for _, period := range periods {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", period, w.Code, w.Body.String())
		}
		b := env.fetchBill(period, "601")
		extraByPeriod[period] = num(b["extra_amount"])
		rentByPeriod[period] = num(b["rent"])
	}

	// 首账月 2026-10：项目首期到期 → 30 × 6 = 180。
	if !almostEqual(extraByPeriod["2026-10"], 180) {
		t.Errorf("oct extra = %v, want 180（半年付首期）", extraByPeriod["2026-10"])
	}
	// 中间月（11/12/2027-01/02/03）：未到期 → 0，不重复收 6 个月；
	// 但月付房租金每月照收 1000（账单仍然每月一张）。
	for _, period := range []string{"2026-11", "2026-12", "2027-01", "2027-02", "2027-03"} {
		if !almostEqual(extraByPeriod[period], 0) {
			t.Errorf("%s extra = %v, want 0（半年付宽带未到期）", period, extraByPeriod[period])
		}
		if !almostEqual(rentByPeriod[period], 1000) {
			t.Errorf("%s rent = %v, want 1000（月付租金每月照收）", period, rentByPeriod[period])
		}
	}
	// 2027-04 与 2026-10 整差 6 个月 → 下一个锚点月，再收 180。
	// 这是本次修的关键回归点：锚点必须按项目自己的收费历史找，
	// 若借用"最近一张含固定费账单"（月付房每月都有租金），这里会恒为 0。
	if !almostEqual(extraByPeriod["2027-04"], 180) {
		t.Errorf("2027-04 extra = %v, want 180（第二个锚点月，按项目自身周期）", extraByPeriod["2027-04"])
	}

	// 明细里保留该项目的 key（收款分摊按项目走）；首期账单应含宽带行。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills?period=2026-10&keyword=601"), nil)
	oct := env.items(decodeBody(t, w))[0].(map[string]interface{})
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/bills/%d", uint(oct["id"].(float64))), nil)
	detail := field(t, decodeBody(t, w), "data").(map[string]interface{})
	found := false
	for _, raw := range detail["items"].([]interface{}) {
		if row := raw.(map[string]interface{}); row["key"] == broadbandKey {
			found = true
			if !almostEqual(num(row["amount"]), 180) {
				t.Errorf("宽带明细金额 = %v, want 180", row["amount"])
			}
		}
	}
	if !found {
		t.Errorf("账单明细缺少宽带费行")
	}
}

// TestCustomFeeAnnualCycleOnlyFourValues 自定义项目周期只接受四周期，
// 未知值（如 weekly）经 normalizePayCycle 兜底为月付；年付项目按 12 个月锚点收。
func TestCustomFeeAnnualCycleOnlyFourValues(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("602", 1000, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "兜底", "pay_day": 5})

	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "未知周期费", "cycle": "weekly", "default_amount": 10,
	})
	b := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if b["cycle"] != payCycleMonthly {
		t.Errorf("未知周期落库 = %v, want monthly（兜底月付）", b["cycle"])
	}

	// 年付项目：首账月收 10 × 12 = 120，次月 0。
	if w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "年付服务费", "cycle": "annual", "default_amount": 10,
	}); w.Code != http.StatusOK {
		t.Fatalf("create annual item: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	first := env.fetchBill("2026-10", "602")
	if !almostEqual(num(first["extra_amount"]), 130) {
		t.Errorf("首月 extra = %v, want 130（未知周期按月 10 + 年付 120）", first["extra_amount"])
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-11"}); w.Code != http.StatusOK {
		t.Fatalf("generate nov: %d %s", w.Code, w.Body.String())
	}
	second := env.fetchBill("2026-11", "602")
	if !almostEqual(num(second["extra_amount"]), 10) {
		t.Errorf("次月 extra = %v, want 10（未知周期按月、年付未到期）", second["extra_amount"])
	}
}

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

// ---------------------------------------------------------------------------
// 取暖费 / 生效月份窗口（GitHub issue #5 需求 6）
// ---------------------------------------------------------------------------

// TestFeeItemInSeasonUnit 窗口判断是纯函数，先把跨年等边界钉死。
func TestFeeItemInSeasonUnit(t *testing.T) {
	type tc struct {
		name  string
		from  int
		to    int
		month string // 账期
		want  bool
	}
	cases := []tc{
		// 不限（存量项目）：任何月份都收。
		{"不限 1 月", 0, 0, "2026-01", true},
		{"不限 7 月", 0, 0, "2026-07", true},
		// 跨年供暖季 11 → 3（北方典型）。
		{"供暖季 11 月", 11, 3, "2026-11", true},
		{"供暖季 12 月", 11, 3, "2026-12", true},
		{"供暖季 次年 1 月", 11, 3, "2027-01", true},
		{"供暖季 次年 2 月", 11, 3, "2027-02", true},
		{"供暖季 次年 3 月", 11, 3, "2027-03", true},
		{"供暖季 4 月不收", 11, 3, "2026-04", false},
		{"供暖季 7 月不收", 11, 3, "2026-07", false},
		{"供暖季 10 月不收", 11, 3, "2026-10", false},
		// 只填起始：该月及之后。
		{"只填起始 10 月", 10, 0, "2026-10", true},
		{"只填起始 11 月", 10, 0, "2026-11", true},
		{"只填起始 9 月不收", 10, 0, "2026-09", false},
		// 只填结束：该月及之前。
		{"只填结束 3 月", 0, 3, "2026-03", true},
		{"只填结束 1 月", 0, 3, "2026-01", true},
		{"只填结束 4 月不收", 0, 3, "2026-04", false},
		// 单月。
		{"单月 7 月命中", 7, 7, "2026-07", true},
		{"单月 6 月不命中", 7, 7, "2026-06", false},
		// 同一年内区间。
		{"6~9 月 7 月", 6, 9, "2026-07", true},
		{"6~9 月 10 月", 6, 9, "2026-10", false},
	}
	for _, c := range cases {
		fi := FeeItem{SeasonFromMonth: c.from, SeasonToMonth: c.to}
		if got := feeItemInSeason(fi, c.month); got != c.want {
			t.Errorf("%s（%d→%d, %s）= %v, want %v", c.name, c.from, c.to, c.month, got, c.want)
		}
	}
}

// TestHeatingSeasonBilledOnlyInWindow 取暖费只在供暖季出账。
func TestHeatingSeasonBilledOnlyInWindow(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("H1", 1000, 0)
	env.setRoomFees(roomID, "H1", 1000, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "供暖户", "pay_day": 5})

	// 取暖费：300 元/月，月付周期，只在 11 月到次年 3 月收。
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "取暖费", "cycle": "monthly", "default_amount": 300,
		"season_from_month": 11, "season_to_month": 3,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create heating fee: %d %s", w.Code, w.Body.String())
	}

	// 逐月出账：10 月不收（窗户还没开），11/12/1/2/3 收，4 月起不收。
	expect := map[string]float64{
		"2026-10": 0, "2026-11": 300, "2026-12": 300,
		"2027-01": 300, "2027-02": 300, "2027-03": 300, "2027-04": 0,
	}
	for period, want := range expect {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", period, w.Code, w.Body.String())
		}
		b := env.fetchBill(period, "H1")
		if got := num(b["extra_amount"]); !almostEqual(got, want) {
			t.Errorf("%s 取暖费 = %v, want %v", period, got, want)
		}
	}
}

// TestNoSeasonKeepsLegacyBehaviour 不填窗口 = 全年都收，与改造前完全一致。
//
// 这是「老用户零影响」的守卫：存量收费项目没有这两个字段（默认 0），
// 行为必须与加字段之前逐笔相同。
func TestNoSeasonKeepsLegacyBehaviour(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("H2", 800, 0)
	env.setRoomFees(roomID, "H2", 800, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "老用户", "pay_day": 5})

	// 不传 season 字段的老式请求。
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "宽带费", "cycle": "monthly", "default_amount": 50,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	// 任意月份都该收 50（含原本供暖季窗口外/内的各月）。
	for _, period := range []string{"2026-01", "2026-06", "2026-12"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", period, w.Code, w.Body.String())
		}
		b := env.fetchBill(period, "H2")
		if got := num(b["extra_amount"]); !almostEqual(got, 50) {
			t.Errorf("%s extra = %v, want 50（不填窗口应全年都收）", period, got)
		}
	}
}

// TestSeasonMonthValidation 非法月份被拒（0 与 1-12 之外一律 400）。
func TestSeasonMonthValidation(t *testing.T) {
	env := setupEnv(t)
	for _, bad := range []int{-1, 13, 99} {
		w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
			"name": "非法窗口", "cycle": "monthly", "default_amount": 10,
			"season_from_month": bad,
		})
		if w.Code == http.StatusOK {
			t.Errorf("season_from_month=%d 应被拒，却返回 200", bad)
		}
		w = env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
			"name": "非法窗口", "cycle": "monthly", "default_amount": 10,
			"season_to_month": bad,
		})
		if w.Code == http.StatusOK {
			t.Errorf("season_to_month=%d 应被拒，却返回 200", bad)
		}
	}
	// 合法值 0 与 1-12 应通过。
	for _, ok := range []int{0, 1, 6, 12} {
		w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
			"name": "合法窗口", "cycle": "monthly", "default_amount": 10,
			"season_from_month": ok, "season_to_month": ok,
		})
		if w.Code != http.StatusOK {
			t.Errorf("season=%d 应通过，得到 %d %s", ok, w.Code, w.Body.String())
		}
	}
}

// TestSeasonWithYearlyCycleCombines 窗口与周期是两条独立规则：窗口内按年付锚点收一次。
func TestSeasonWithYearlyCycleCombines(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("H3", 1000, 0)
	env.setRoomFees(roomID, "H3", 1000, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "年付取暖", "pay_day": 5})

	// 取暖费 200 元/月、年付周期（一次收 12 个月）、窗口 11~3。
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "取暖费", "cycle": "annual", "default_amount": 200,
		"season_from_month": 11, "season_to_month": 3,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	// 11 月首账：窗口内 + 年付首期 → 200 × 12 = 2400。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-11"}); w.Code != http.StatusOK {
		t.Fatalf("gen 11: %d %s", w.Code, w.Body.String())
	}
	if got := num(env.fetchBill("2026-11", "H3")["extra_amount"]); !almostEqual(got, 2400) {
		t.Errorf("2026-11 extra = %v, want 2400（年付首期）", got)
	}
	// 12 月窗口内但未到期 → 0（不重复收）。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-12"}); w.Code != http.StatusOK {
		t.Fatalf("gen 12: %d %s", w.Code, w.Body.String())
	}
	if got := num(env.fetchBill("2026-12", "H3")["extra_amount"]); !almostEqual(got, 0) {
		t.Errorf("2026-12 extra = %v, want 0（年付未到期，窗口内也不重复收）", got)
	}
	// 7 月窗口外 → 0。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2027-07"}); w.Code != http.StatusOK {
		t.Fatalf("gen 7: %d %s", w.Code, w.Body.String())
	}
	if got := num(env.fetchBill("2027-07", "H3")["extra_amount"]); !almostEqual(got, 0) {
		t.Errorf("2027-07 extra = %v, want 0（窗口外）", got)
	}
}
