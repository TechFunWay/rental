// carryover_test.go — 少交/多交结转次月（issue #5 需求 7）的回归测试。
//
// 最核心的用例是 TestCarryoverMatchesUserExample：它逐字复刻用户在 issue 里
// 给出的三行示例，是本功能的验收标准。
package rental

import (
	"net/http"
	"testing"
)

// fetchTenant 按 ID 取单条租户（走列表接口并按其 id 命中，与界面同路径）。
func (e *testEnv) fetchTenant(id uint) map[string]interface{} {
	e.t.Helper()
	w := e.do(http.MethodGet, "/api/rental/tenants?pageSize=100", nil)
	for _, it := range e.items(decodeBody(e.t, w)) {
		m := it.(map[string]interface{})
		if uint(num(m["id"])) == id {
			return m
		}
	}
	e.t.Fatalf("tenant %d not found: %s", id, w.Body.String())
	return nil
}

// enableCarryover 开启某台账的结转开关。
//
// 直接走台账配置的写入函数（用户界面最终也是落到这里），
// 避免把用例耦合到某个还没定的 HTTP 路由上。
func enableCarryover(t *testing.T, env *testEnv) {
	t.Helper()
	if err := setLedgerConfigValue(env.db, carryoverConfigKey, "true", env.personalLedger); err != nil {
		t.Fatalf("enable carryover: %v", err)
	}
	if !carryoverEnabled(env.db, env.personalLedger) {
		t.Fatal("结转开关写入后未生效")
	}
}

// TestCarryoverMatchesUserExample 复刻用户示例（验收标准）：
//
//	1月 费用500 实缴450           → 本期结转  50
//	2月 费用550 应缴=550+50=600 实缴700 → 本期结转-100
//	3月 费用450 应缴=450-100=350 实缴350 → 本期结转   0
//
// 语义提醒（这里最容易看错）：用户口中的「本期结转」= 本期应缴 - 本期实缴，
// 它体现为**下一期账单的 carry_in**。所以断言分两处：
//
//	本期的 payable   —— 由「上一期的结转」决定；
//	本期的 carry_in  —— 由「上一期的 payable - 上一期的实缴」决定。
//
// 按房东真实节奏操作：本期开票 → 收本期款 → 下期开票（结转在收款的当下被传播）。
func TestCarryoverMatchesUserExample(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)

	roomID := env.seedRoom("C1", 500, 0)
	env.setRoomFees(roomID, "C1", 500, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "结转租户", "pay_day": 5})

	type step struct {
		period      string
		fee         float64 // 本期费用（租金）
		paid        float64 // 本期实缴
		wantCarryIn float64 // 本期带入的「上期结转」
		wantPayable float64 // 本期应缴 = 费用 + 上期结转
		wantResult  float64 // 本期结转（= 应缴 - 实缴），即用户表里的那一列
	}
	steps := []step{
		{"2026-01", 500, 450, 0, 500, 50},
		{"2026-02", 550, 700, 50, 600, -100},
		{"2026-03", 450, 350, -100, 350, 0},
	}

	for _, s := range steps {
		// 1) 开票
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": s.period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", s.period, w.Code, w.Body.String())
		}
		b := env.fetchBill(s.period, "C1")
		billID := uint(num(b["id"]))

		// 2) 本期费用设成用例要求的值
		if w := env.do(http.MethodPut, "/api/rental/bills/"+itoa(billID), map[string]interface{}{
			"room_id": roomID, "period": s.period, "rent": s.fee,
		}); w.Code != http.StatusOK {
			t.Fatalf("set fee %s: %d %s", s.period, w.Code, w.Body.String())
		}

		got := env.fetchBill(s.period, "C1")
		if fee := num(got["total_amount"]); !almostEqual(fee, s.fee) {
			t.Errorf("%s 本期费用 = %v, want %v", s.period, fee, s.fee)
		}
		if carry := num(got["carry_in"]); !almostEqual(carry, s.wantCarryIn) {
			t.Errorf("%s 上期结转(carry_in) = %v, want %v（用户示例）", s.period, carry, s.wantCarryIn)
		}
		if payable := num(got["payable"]); !almostEqual(payable, s.wantPayable) {
			t.Errorf("%s 应缴(payable) = %v, want %v（用户示例）", s.period, payable, s.wantPayable)
		}

		// 3) 收本期款（应收不能超过应缴——这里同时验证收款上限用的是 payable）
		if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(billID)+"/pay",
			map[string]interface{}{"amount": s.paid}); w.Code != http.StatusOK {
			t.Fatalf("pay %s: %d %s", s.period, w.Code, w.Body.String())
		}

		// 4) 本期结转 = 应缴 - 实缴，应该等于用户示例那一列
		after := env.fetchBill(s.period, "C1")
		result := round2(num(after["payable"]) - num(after["paid_amount"]))
		if !almostEqual(result, s.wantResult) {
			t.Errorf("%s 本期结转 = %v, want %v（用户示例那一列）", s.period, result, s.wantResult)
		}
	}
}

// TestCarryoverOffByDefault 默认关闭：账单金额与改造前逐字一致。
//
// 这是「不要动老用户」的硬约束：不开启结转时，
//
//	carry_in 恒 0、payable 恒 == total_amount。
func TestCarryoverOffByDefault(t *testing.T) {
	env := setupEnv(t) // 不调用 enableCarryover
	roomID := env.seedRoom("C2", 800, 0)
	env.setRoomFees(roomID, "C2", 800, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "老用户", "pay_day": 5})

	// 第 1 期只交一半，制造「欠缴」；若结转被默认打开，第 2 期应缴会变成 1200。
	for _, p := range []string{"2026-01", "2026-02"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": p}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	b1 := env.fetchBill("2026-01", "C2")
	env.updateBillPaid(uint(num(b1["id"])), 400)

	got := env.fetchBill("2026-02", "C2")
	if carry := num(got["carry_in"]); !almostEqual(carry, 0) {
		t.Errorf("默认关闭时 %s 结转 = %v, want 0", "2026-02", carry)
	}
	if payable, total := num(got["payable"]), num(got["total_amount"]); !almostEqual(payable, total) {
		t.Errorf("默认关闭时应缴 = %v, want 等于本期费用 %v", payable, total)
	}
}

// TestCarryoverDoesNotDoubleCountArrears 最关键的数值安全用例。
//
// 结转**不能**折进 total_amount，否则全应用三处
// `SUM(total_amount - paid_amount)` 会把历史差额重复累加。
// 用例：用户示例跑完后各期都结清，累计欠缴必须为 0。
func TestCarryoverDoesNotDoubleCountArrears(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)

	roomID := env.seedRoom("C3", 500, 0)
	env.setRoomFees(roomID, "C3", 500, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "结清租户", "pay_day": 5})

	for _, s := range []struct {
		period string
		fee    float64
		paid   float64
	}{{"2026-01", 500, 450}, {"2026-02", 550, 700}, {"2026-03", 450, 350}} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": s.period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", s.period, w.Code, w.Body.String())
		}
		b := env.fetchBill(s.period, "C3")
		id := uint(num(b["id"]))
		if w := env.do(http.MethodPut, "/api/rental/bills/"+itoa(id),
			map[string]interface{}{"room_id": roomID, "period": s.period, "rent": s.fee}); w.Code != http.StatusOK {
			t.Fatalf("set fee %s: %d %s", s.period, w.Code, w.Body.String())
		}
		// 走真实收款接口（而不是直接改 paid_amount），顺带验证收款上限按 payable 卡、
		// 以及收款后结转自动向后传播。
		if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(id)+"/pay",
			map[string]interface{}{"amount": s.paid}); w.Code != http.StatusOK {
			t.Fatalf("pay %s: %d %s", s.period, w.Code, w.Body.String())
		}
	}

	// 全应用口径：Σ(total_amount - paid_amount) 必须等于真实累计净额 0。
	//
	// 真实净额 = Σ(本期费用 - 实缴) = (500-450)+(550-700)+(450-350) = 0。
	// 若把结转折进 total_amount，这里会算出 -50（重复累加）。
	var arrearsFromTotals float64
	if err := env.db.Model(&Bill{}).
		Select("COALESCE(SUM(total_amount - paid_amount), 0)").Scan(&arrearsFromTotals).Error; err != nil {
		t.Fatalf("sum: %v", err)
	}
	if !almostEqual(arrearsFromTotals, 0) {
		t.Errorf("Σ(total_amount - paid_amount) = %v, want 0 —— 结转不能折进 total_amount，"+
			"否则欠缴聚合会重复计算历史差额", arrearsFromTotals)
	}

	// 反向断言：Σ(payable - paid_amount) **不等于**真实欠款，会重复计算结转。
	//
	// 这不是 bug，而是「payable 只适合单期展示、不适合跨期求和」的证明。
	// 用一条断言把这条边界钉住，防止后人"顺手"把跨期聚合改成 payable 口径。
	//
	// 推导：Σ(payable_i - paid_i) = Σ(fee_i - paid_i) + Σ(carry_i)
	//       carry = [0, 50, -100] ⇒ 0 + (-50) = -50。
	var payableArrears float64
	if err := env.db.Model(&Bill{}).
		Select("COALESCE(SUM(payable - paid_amount), 0)").Scan(&payableArrears).Error; err != nil {
		t.Fatalf("sum payable: %v", err)
	}
	if !almostEqual(payableArrears, -50) {
		t.Errorf("Σ(payable - paid_amount) = %v, want -50。"+
			"该值等于『真实欠款 0』减去『结转累计 -50』，正说明跨期求和会重复计算结转；"+
			"跨期聚合必须继续用 total_amount", payableArrears)
	}
}

// TestCarryoverAccumulatesUntilSettled 未结清时逐期累积（不是只带一期）。
func TestCarryoverAccumulatesUntilSettled(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)

	roomID := env.seedRoom("C4", 500, 0)
	env.setRoomFees(roomID, "C4", 500, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "欠缴户", "pay_day": 5})

	// 1 月费用 500、不交 → 欠 500；2 月费用 500、不交 → 应缴 1000、欠 1000；
	// 3 月费用 500、不交 → 应缴 1500、欠 1500。
	for i, p := range []string{"2026-01", "2026-02", "2026-03"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": p}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", p, w.Code, w.Body.String())
		}
		got := env.fetchBill(p, "C4")
		wantPayable := float64(500 * (i + 1))
		if payable := num(got["payable"]); !almostEqual(payable, wantPayable) {
			t.Errorf("%s 应缴 = %v, want %v（逐期累积）", p, payable, wantPayable)
		}
		if status := got["status"]; status != "unpaid" {
			t.Errorf("%s 状态 = %v, want unpaid", p, status)
		}
	}
}

// TestCarryoverFirstBillIsZero 首账没有上一期，结转必然为 0。
func TestCarryoverFirstBillIsZero(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)
	roomID := env.seedRoom("C5", 600, 0)
	env.setRoomFees(roomID, "C5", 600, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "首账户", "pay_day": 5})

	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-01"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	b := env.fetchBill("2026-01", "C5")
	if carry := num(b["carry_in"]); !almostEqual(carry, 0) {
		t.Errorf("首账结转 = %v, want 0", carry)
	}
	if payable, total := num(b["payable"]), num(b["total_amount"]); !almostEqual(payable, total) {
		t.Errorf("首账应缴 = %v, want %v", payable, total)
	}
}

// TestCarryoverOverpaymentReducesNextPayable 多交自动抵扣下期。
func TestCarryoverOverpaymentReducesNextPayable(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)
	roomID := env.seedRoom("C6", 400, 0)
	env.setRoomFees(roomID, "C6", 400, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "多交户", "pay_day": 5})

	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-01"}); w.Code != http.StatusOK {
		t.Fatalf("gen jan: %d %s", w.Code, w.Body.String())
	}
	// 1 月本期费用 400，按 payable 收款上限收 1000（多交 600）。
	//
	// 顺带验证收款上限用的是 payable 而非 total_amount：本期无结转时两者相等，
	// 这里先验证「能收到超过本期费用」不被误挡（未开启结转时才应被挡）。
	b1 := env.fetchBill("2026-01", "C6")
	if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(uint(num(b1["id"])))+"/pay",
		map[string]interface{}{"amount": 1000}); w.Code != http.StatusOK {
		t.Fatalf("多交应被接受: %d %s", w.Code, w.Body.String())
	}

	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-02"}); w.Code != http.StatusOK {
		t.Fatalf("gen feb: %d %s", w.Code, w.Body.String())
	}
	feb := env.fetchBill("2026-02", "C6")
	if carry := num(feb["carry_in"]); !almostEqual(carry, -600) {
		t.Errorf("多交后结转 = %v, want -600", carry)
	}
	// 2 月本期费用 400，应缴 = 400 - 600 = -200（多交结转完当月仍有余）。
	if payable := num(feb["payable"]); !almostEqual(payable, -200) {
		t.Errorf("2 月应缴 = %v, want -200", payable)
	}
}

// TestCarryoverRecalcPropagates 改历史账单后，显式重算能把后续账期的结转更正。
func TestCarryoverRecalcPropagates(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)
	roomID := env.seedRoom("C7", 300, 0)
	env.setRoomFees(roomID, "C7", 300, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "重算户", "pay_day": 5})

	for _, p := range []string{"2026-01", "2026-02", "2026-03"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": p}); w.Code != http.StatusOK {
			t.Fatalf("gen %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	// 三期都开了票但都没收款 → 逐期累积：
	//   1月应缴 300、2月应缴 600、3月应缴 900（每期多带 300）。
	three := env.fetchBill("2026-03", "C7")
	if payable := num(three["payable"]); !almostEqual(payable, 900) {
		t.Fatalf("重算前 3 月应缴 = %v, want 900（1、2 月都没收款，逐期累积）", payable)
	}

	// 现在把 1、2 月都收掉（各 300）→ 3 月应缴应回到 300（本期费用本身）。
	// 收 1 月款时会自动传播，2 月与 3 月的 carry_in 应跟着更正。
	for _, p := range []string{"2026-01", "2026-02"} {
		b := env.fetchBill(p, "C7")
		if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(uint(num(b["id"])))+"/pay",
			map[string]interface{}{"amount": num(b["payable"])}); w.Code != http.StatusOK {
			t.Fatalf("pay %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	three = env.fetchBill("2026-03", "C7")
	if payable := num(three["payable"]); !almostEqual(payable, 300) {
		t.Fatalf("收款自动传播后 3 月应缴 = %v, want 300（本期费用本身）", payable)
	}

	// 手动重算：把 2 月改回只交 100（欠 200）后，从 2026-03 起重算应得到 300+200=500。
	var b2 Bill
	if err := env.db.Where("period = ?", "2026-02").First(&b2).Error; err != nil {
		t.Fatalf("load feb: %v", err)
	}
	b2.PaidAmount = 100
	if err := env.db.Save(&b2).Error; err != nil {
		t.Fatalf("set feb paid: %v", err)
	}

	w := env.do(http.MethodPost, "/api/rental/bills/carryover/recalc", map[string]interface{}{
		"room_id": roomID, "period": "2026-03",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("recalc: %d %s", w.Code, w.Body.String())
	}
	after := env.fetchBill("2026-03", "C7")
	if carry := num(after["carry_in"]); !almostEqual(carry, 200) {
		t.Errorf("重算后 3 月结转 = %v, want 200", carry)
	}
	if payable := num(after["payable"]); !almostEqual(payable, 500) {
		t.Errorf("重算后 3 月应缴 = %v, want 500", payable)
	}
}

// TestCarryoverRecalcRejectedWhenDisabled 未开启结转时重算应被拒（不静默无操作）。
func TestCarryoverRecalcRejectedWhenDisabled(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("C8", 300, 0)
	env.setRoomFees(roomID, "C8", 300, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "未开结转", "pay_day": 5})
	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-01"}); w.Code != http.StatusOK {
		t.Fatalf("gen: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodPost, "/api/rental/bills/carryover/recalc", map[string]interface{}{
		"room_id": roomID, "period": "2026-01",
	})
	if w.Code == http.StatusOK {
		t.Errorf("未开启结转时重算应返回错误，实际 200")
	}
}

// TestCarryoverMigrationBackfillsExistingBills 存量账单回填 payable。
//
// 模拟升级前的老账单（payable = 0），跑迁移后应等于 total_amount，
// 从而保证老账单界面金额与欠缴聚合都不变。
func TestCarryoverMigrationBackfillsExistingBills(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("C9", 700, 0)
	env.setRoomFees(roomID, "C9", 700, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "老账", "pay_day": 5})
	if w := env.do(http.MethodPost, "/api/rental/bills/generate",
		map[string]interface{}{"period": "2026-01"}); w.Code != http.StatusOK {
		t.Fatalf("gen: %d %s", w.Code, w.Body.String())
	}

	// 人为退化成「升级前的样子」：payable 归 0。
	if err := env.db.Exec("UPDATE bills SET payable = 0, carry_in = 0").Error; err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := migrateBillCarryover(env.db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	b := env.fetchBill("2026-01", "C9")
	if payable, total := num(b["payable"]), num(b["total_amount"]); !almostEqual(payable, total) {
		t.Errorf("回填后应缴 = %v, want 等于本期费用 %v", payable, total)
	}
	if carry := num(b["carry_in"]); !almostEqual(carry, 0) {
		t.Errorf("回填后结转 = %v, want 0", carry)
	}

	// 幂等：再跑一次不改变结果。
	if err := migrateBillCarryover(env.db); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	b2 := env.fetchBill("2026-01", "C9")
	if payable := num(b2["payable"]); !almostEqual(payable, 700) {
		t.Errorf("迁移应幂等，第二次后应缴 = %v, want 700", payable)
	}
}

// ---------------------------------------------------------------------------
// 身份证派生（issue #5 需求 1 剩余部分）
// ---------------------------------------------------------------------------

// TestTenantIDCardDerivation 主租户档案按身份证号派生出生日期与性别。
func TestTenantIDCardDerivation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("D1", 1000, 0)
	env.setRoomFees(roomID, "D1", 1000, 0, 0, 0)

	// 18 位号：110101 19900101 123 4 → 生日 1990-01-01，第 17 位 3（奇数）= 男。
	got := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "张三", "id_card": "110101199001011234", "pay_day": 5,
	})
	if b := got["birthday"]; b != "1990-01-01" {
		t.Errorf("birthday = %v, want 1990-01-01", b)
	}
	if g := got["gender"]; g != "male" {
		t.Errorf("gender = %v, want male（第 17 位为奇数）", g)
	}

	// 偶数第 17 位 → 女。
	got2 := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "李四", "id_card": "110101199001011222", "pay_day": 5,
	})
	if g := got2["gender"]; g != "female" {
		t.Errorf("gender = %v, want female（第 17 位 2 为偶数）", g)
	}

	// 15 位老号段：不猜，两个字段都留空。
	got3 := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "王五", "id_card": "110101900101123", "pay_day": 5,
	})
	if b := got3["birthday"]; b != "" {
		t.Errorf("15 位号段不应派生 birthday，得到 %v", b)
	}
	if g := got3["gender"]; g != "" {
		t.Errorf("15 位号段不应派生 gender，得到 %v", g)
	}
}

// TestTenantIDCardDerivationUpdatesAndClears 改号重算、清号清空。
func TestTenantIDCardDerivationUpdatesAndClears(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("D2", 1000, 0)
	env.setRoomFees(roomID, "D2", 1000, 0, 0, 0)
	created := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "改号", "id_card": "110101199001011234", "pay_day": 5,
	})
	tenantID := uint(num(created["id"]))

	// 改成另一个号 → 派生字段跟着变。
	if w := env.do(http.MethodPut, "/api/rental/tenants/"+itoa(tenantID), map[string]interface{}{
		"room_id": roomID, "name": "改号", "id_card": "110101198505054351", "pay_day": 5,
	}); w.Code != http.StatusOK {
		t.Fatalf("update id_card: %d %s", w.Code, w.Body.String())
	}
	after := env.fetchTenant(tenantID)
	if b := after["birthday"]; b != "1985-05-05" {
		t.Errorf("改号后 birthday = %v, want 1985-05-05", b)
	}
	if g := after["gender"]; g != "male" {
		t.Errorf("改号后 gender = %v, want male（第 17 位 5 为奇数）", g)
	}

	// 清空证件号 → 派生字段一并清空，不留残留。
	if w := env.do(http.MethodPut, "/api/rental/tenants/"+itoa(tenantID), map[string]interface{}{
		"room_id": roomID, "name": "改号", "id_card": "", "pay_day": 5,
	}); w.Code != http.StatusOK {
		t.Fatalf("clear id_card: %d %s", w.Code, w.Body.String())
	}
	cleared := env.fetchTenant(tenantID)
	if b := cleared["birthday"]; b != "" {
		t.Errorf("清空证件号后 birthday 应为空，得到 %v", b)
	}
	if g := cleared["gender"]; g != "" {
		t.Errorf("清空证件号后 gender 应为空，得到 %v", g)
	}
}

// TestTenantDerivedBackfillMigration 存量租户回填派生字段（幂等、不覆盖已有值）。
func TestTenantDerivedBackfillMigration(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("D3", 1000, 0)
	env.setRoomFees(roomID, "D3", 1000, 0, 0, 0)
	created := env.seedTenantViaAPI(roomID, map[string]interface{}{
		"name": "存量", "id_card": "110101199001011234", "pay_day": 5,
	})
	tenantID := uint(num(created["id"]))

	// 模拟升级前：派生列为空。
	if err := env.db.Exec("UPDATE tenants SET birthday = '', gender = ''").Error; err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := backfillTenantDerived(env.db); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got := env.fetchTenant(tenantID)
	if b := got["birthday"]; b != "1990-01-01" {
		t.Errorf("回填后 birthday = %v, want 1990-01-01", b)
	}

	// 用户手工改过的值不能被回填覆盖（派生字段已非空 → 跳过）。
	if err := env.db.Exec("UPDATE tenants SET birthday = '2000-02-02' WHERE id = ?", tenantID).Error; err != nil {
		t.Fatalf("set manual: %v", err)
	}
	if err := backfillTenantDerived(env.db); err != nil {
		t.Fatalf("backfill again: %v", err)
	}
	kept := env.fetchTenant(tenantID)
	if b := kept["birthday"]; b != "2000-02-02" {
		t.Errorf("回填不应覆盖已有值，得到 %v", b)
	}
}

// TestCarryoverToggleThroughLedgerSettings 走真实的台账设置接口开关结转。
//
// 这条用例的价值在于覆盖「开关能不能被存下去」：
// 台账设置的写入有 rentalConfigKeys 白名单，漏登记这个键会让开关静默失效
// （界面点了保存、后端 400 或忽略，房东以为开了其实没开）。
func TestCarryoverToggleThroughLedgerSettings(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "carry-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "结转台账")

	// 先确认默认关闭（系统级默认 false）。
	if carryoverEnabled(env.db, ledgerID) {
		t.Fatal("新建台账的结转应默认关闭")
	}

	// 通过设置接口打开。
	w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{carryoverConfigKey: "true"}})
	if w.Code != http.StatusOK {
		t.Fatalf("开启结转失败（开关可能没登记进 rentalConfigKeys）: %d %s", w.Code, w.Body.String())
	}
	if !carryoverEnabled(env.db, ledgerID) {
		t.Fatal("设置接口返回成功，但结转开关未生效——白名单很可能漏了这个键")
	}

	// 设置接口应能读回该键（overrides/effective 里都要有）。
	w = doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读设置失败: %d %s", w.Code, w.Body.String())
	}
	eff := field(t, decodeBody(t, w), "data", "effective").(map[string]interface{})
	if got, _ := eff[carryoverConfigKey].(string); got != "true" {
		t.Errorf("effective[%s] = %q, want \"true\"", carryoverConfigKey, got)
	}

	// 再关掉，确认可双向切换。
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{carryoverConfigKey: "false"}})
	if w.Code != http.StatusOK {
		t.Fatalf("关闭结转失败: %d %s", w.Code, w.Body.String())
	}
	if carryoverEnabled(env.db, ledgerID) {
		t.Error("关闭后结转仍为开启")
	}
}

// TestCarryoverOverpaymentNetsAgainstEarlierArrears 多交要能冲减前面的欠缴。
//
// 这是本轮实测在真机上抓到的缺陷：引入「多交顺延」后，总览/统计分析的欠缴
// 仍按 `WHERE status <> 'paid'` 过滤求和，多收的那张单被排除，于是
//
//	1月 应缴500 收450 → partial（欠 50）
//	2月 应缴600 收700 → paid（多 100）   ← 被过滤掉
//
// 总览显示「欠 50」，可实际上 2 月多交的 100 早已把 1 月的 50 抵平。
// 修正后按「全部账单求和相减」的净额口径，多收自然冲减欠缴。
func TestCarryoverOverpaymentNetsAgainstEarlierArrears(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)

	roomID := env.seedRoom("N1", 500, 0)
	env.setRoomFees(roomID, "N1", 500, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "净额户", "pay_day": 5})

	// 照用户示例操作，最后各期账实相符、房东净多收 50。
	steps := []struct {
		period string
		fee    float64
		paid   float64
	}{{"2026-01", 500, 450}, {"2026-02", 550, 700}, {"2026-03", 450, 350}}
	for _, s := range steps {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": s.period}); w.Code != http.StatusOK {
			t.Fatalf("generate %s: %d %s", s.period, w.Code, w.Body.String())
		}
		b := env.fetchBill(s.period, "N1")
		id := uint(num(b["id"]))
		env.do(http.MethodPut, "/api/rental/bills/"+itoa(id),
			map[string]interface{}{"room_id": roomID, "period": s.period, "rent": s.fee})
		if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(id)+"/pay",
			map[string]interface{}{"amount": s.paid}); w.Code != http.StatusOK {
			t.Fatalf("pay %s: %d %s", s.period, w.Code, w.Body.String())
		}
	}

	// 总览：净额口径，多收冲减后不应再显示欠缴。
	w := env.do(http.MethodGet, "/api/rental/stats?period=2026-03", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	overall := field(t, decodeBody(t, w), "data", "overall").(map[string]interface{})
	if got := num(overall["outstanding"]); !almostEqual(got, 0) {
		t.Errorf("总览 overall.outstanding = %v, want 0。"+
			"多收的 100 应冲减 1 月欠的 50（净多收 50 → 显示 0）；"+
			"若按 status<>paid 过滤求和会错误地显示 50", got)
	}

	// 统计分析：同一口径。
	w = env.do(http.MethodGet, "/api/rental/analytics?months=12", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("analytics: %d %s", w.Code, w.Body.String())
	}
	summary := field(t, decodeBody(t, w), "data", "summary").(map[string]interface{})
	if got := num(summary["arrears"]); !almostEqual(got, 0) {
		t.Errorf("统计分析 arrears = %v, want 0（口径应与总览一致）", got)
	}
}

// TestArrearsUnchangedWithoutCarryover 未开启结转时欠缴口径与改造前一致。
//
// 这是「老用户零影响」的守卫：没有超收时，净额口径与
// 「未缴清账单求和」完全等价。
func TestArrearsUnchangedWithoutCarryover(t *testing.T) {
	env := setupEnv(t) // 不开启结转
	roomID := env.seedRoom("N2", 800, 0)
	env.setRoomFees(roomID, "N2", 800, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "老用户", "pay_day": 5})

	// 两期都没收：欠 800 + 800 = 1600。
	for _, p := range []string{"2026-01", "2026-02"} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": p}); w.Code != http.StatusOK {
			t.Fatalf("gen %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	w := env.do(http.MethodGet, "/api/rental/stats?period=2026-02", nil)
	overall := field(t, decodeBody(t, w), "data", "overall").(map[string]interface{})
	if got := num(overall["outstanding"]); !almostEqual(got, 1600) {
		t.Errorf("未开结转时总览欠缴 = %v, want 1600（与改造前口径一致）", got)
	}

	// 部分收款：只交 1 月的 300 → 欠 (800-300) + 800 = 1300。
	//
	// 注意不能用 updateBillPaid helper：它把 rent 写死成 1000、period 写死成
	// "2026-09"，会把本用例的账单改坏（实测踩过）。这里显式走 PUT。
	b1 := env.fetchBill("2026-01", "N2")
	if w := env.do(http.MethodPut, "/api/rental/bills/"+itoa(uint(num(b1["id"]))),
		map[string]interface{}{
			"room_id": roomID, "period": "2026-01", "paid_amount": 300,
		}); w.Code != http.StatusOK {
		t.Fatalf("set paid: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/stats?period=2026-02", nil)
	overall = field(t, decodeBody(t, w), "data", "overall").(map[string]interface{})
	if got := num(overall["outstanding"]); !almostEqual(got, 1300) {
		t.Errorf("部分收款后总览欠缴 = %v, want 1300", got)
	}
}

// TestCarryoverMonthCardIsSelfConsistent 单月卡片内部必须自洽（真机抓到的问题）。
//
// 三个字段的关系是死的：outstanding == total - paid，且欠缴不可能大于应收。
// 修复前总分览页出现过「应收 3,600 / 欠缴 4,900」——
// 因为 total 用 total_amount、outstanding 用 payable，两个基准不同。
// 这条断言把三个字段钉在同一口径上（都用 payable）。
func TestCarryoverMonthCardIsSelfConsistent(t *testing.T) {
	env := setupEnv(t)
	enableCarryover(t, env)

	roomID := env.seedRoom("M1", 800, 0)
	env.setRoomFees(roomID, "M1", 800, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "自洽户", "pay_day": 5})

	// 1 月应缴 800、收 750 → 欠 50；2 月应缴 850（800+50）、一分未收。
	for _, s := range []struct {
		period string
		paid   float64
	}{{"2026-01", 750}, {"2026-02", 0}} {
		if w := env.do(http.MethodPost, "/api/rental/bills/generate",
			map[string]interface{}{"period": s.period}); w.Code != http.StatusOK {
			t.Fatalf("gen %s: %d %s", s.period, w.Code, w.Body.String())
		}
		if s.paid > 0 {
			b := env.fetchBill(s.period, "M1")
			if w := env.do(http.MethodPost, "/api/rental/bills/"+itoa(uint(num(b["id"])))+"/pay",
				map[string]interface{}{"amount": s.paid}); w.Code != http.StatusOK {
				t.Fatalf("pay %s: %d %s", s.period, w.Code, w.Body.String())
			}
		}
	}

	w := env.do(http.MethodGet, "/api/rental/stats?period=2026-02", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", w.Code, w.Body.String())
	}
	month := field(t, decodeBody(t, w), "data", "month").(map[string]interface{})
	total, paid, outstanding := num(month["total"]), num(month["paid"]), num(month["outstanding"])

	// 2 月应缴 = 800 + 50 = 850（含 1 月少交的 50）。
	if !almostEqual(total, 850) {
		t.Errorf("month.total = %v, want 850（应含上期结转 50）", total)
	}
	if !almostEqual(outstanding, total-paid) {
		t.Errorf("month.outstanding = %v, want total - paid = %v（三个字段必须同口径）",
			outstanding, total-paid)
	}
	if outstanding > total {
		t.Errorf("欠缴 %v 大于应收 %v —— 欠得比应收还多是不可能的（口径混用）", outstanding, total)
	}

	// 欠缴名单（同屏另一块）必须与卡片同口径：金额应相等。
	list := field(t, decodeBody(t, w), "data", "arrears_list").([]interface{})
	var listSum float64
	for _, it := range list {
		listSum += num(it.(map[string]interface{})["arrears"])
	}
	if !almostEqual(listSum, outstanding) {
		t.Errorf("欠缴名单合计 = %v, 与卡片 outstanding %v 不一致（同屏必须同口径）",
			listSum, outstanding)
	}
}
