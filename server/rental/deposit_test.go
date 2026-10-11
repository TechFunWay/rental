// deposit_test.go — 新租户首张账单含押金的回归测试（2026-10-11 粉丝需求）。
//
// 需求：新租户入住第一次，押金和房租要在**同一张账单**里一起收，而不是分开算。
// 口径（与用户确认）：
//   - 押金取租户表单里已填的 Tenant.Deposit，自动并入该房**首张**账单；
//   - 只在首张收一次，之后的账单不再收（否则每月重复）；
//   - 只管新账单，已存在的历史账单一律不补收、不修改。
package rental

import (
	"net/http"
	"testing"
)

// seedRoomWithDeposit 建房 + 建在租租户（带押金），返回房间与租户 id。
func seedRoomWithDeposit(t *testing.T, env *testEnv, roomNo string, rent, deposit float64) (uint, uint) {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": roomNo, "default_rent": rent,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room %s: %d %s", roomNo, w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "押金租户", "deposit": deposit,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed tenant: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	return roomID, tenantID
}

// createBillFor 指定房间与账期建账单，返回账单 JSON。
func createBillFor(t *testing.T, env *testEnv, roomID uint, period string) map[string]interface{} {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": period,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create bill %s: %d %s", period, w.Code, w.Body.String())
	}
	return field(t, decodeBody(t, w), "data").(map[string]interface{})
}

// TestFirstBillIncludesDeposit 首张账单把押金与租金一起收。
func TestFirstBillIncludesDeposit(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D01", 1000, 2000)

	bill := createBillFor(t, env, roomID, "2026-09")

	if got := num(bill["deposit_fee"]); got != 2000 {
		t.Errorf("首张账单押金应为 2000，实得 %v", got)
	}
	if got := num(bill["rent"]); got != 1000 {
		t.Errorf("租金应为 1000，实得 %v", got)
	}
	// 合计必须把押金算进去：租金 1000 + 押金 2000 = 3000。
	if got := num(bill["total_amount"]); got != 3000 {
		t.Errorf("合计应含押金（1000+2000=3000），实得 %v", got)
	}
}

// TestSecondBillHasNoDeposit 第二张账单不再收押金（这是最重要的一条：
// 否则每月都会重复收押金）。
func TestSecondBillHasNoDeposit(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D02", 1000, 2000)

	first := createBillFor(t, env, roomID, "2026-09")
	if got := num(first["deposit_fee"]); got != 2000 {
		t.Fatalf("首张押金应为 2000，实得 %v", got)
	}

	second := createBillFor(t, env, roomID, "2026-10")
	if got := num(second["deposit_fee"]); got != 0 {
		t.Errorf("第二张账单不应再收押金，实得 %v", got)
	}
	if got := num(second["total_amount"]); got != 1000 {
		t.Errorf("第二张合计应只有租金 1000，实得 %v", got)
	}
}

// TestDepositNotChargedWhenTenantHasNone 租户没填押金 → 账单不收、不报错。
func TestDepositNotChargedWhenTenantHasNone(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D03", 1000, 0)

	bill := createBillFor(t, env, roomID, "2026-09")
	if got := num(bill["deposit_fee"]); got != 0 {
		t.Errorf("未填押金时不应收押金，实得 %v", got)
	}
	if got := num(bill["total_amount"]); got != 1000 {
		t.Errorf("合计应只有租金 1000，实得 %v", got)
	}
}

// TestDepositNotBackfilledToEarlierPeriod 补开更早账期不算「首张」：
// 先开 2026-10，再补 2026-09，补的那张也不该收押金（避免事后补账又收一遍）。
func TestDepositNotBackfilledToEarlierPeriod(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D04", 1000, 2000)

	// 先开 10 月——此时没有更早账期，它是首张，收押金。
	oct := createBillFor(t, env, roomID, "2026-10")
	if got := num(oct["deposit_fee"]); got != 2000 {
		t.Fatalf("首张（2026-10）应急 2000，实得 %v", got)
	}
	// 再补 9 月：已有更晚的账单，不算首张。
	sep := createBillFor(t, env, roomID, "2026-09")
	if got := num(sep["deposit_fee"]); got != 0 {
		t.Errorf("补开的更早账期不应收押金，实得 %v", got)
	}
}

// TestDepositNotAffectedByLaterEdit 编辑账单（改读数/已收）不会把押金弄丢或翻倍。
func TestDepositNotAffectedByLaterEdit(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D05", 1000, 2000)

	bill := createBillFor(t, env, roomID, "2026-09")
	billID := uint(num(bill["id"]))

	// 只改已收金额（不动押金）。room_id/period 是必填项，照实传。
	w := env.do(http.MethodPut, "/api/rental/bills/"+itoa(billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "paid_amount": 3000,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("更新账单: %d %s", w.Code, w.Body.String())
	}
	after := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := num(after["deposit_fee"]); got != 2000 {
		t.Errorf("编辑后押金应保持 2000，实得 %v", got)
	}
	if got := num(after["total_amount"]); got != 3000 {
		t.Errorf("编辑后合计应保持 3000，实得 %v", got)
	}
}

// TestDepositAppearsAsBillItem 押金在账单明细里单列一项（单据/详情可见），
// 且 fee_key 固定为 deposit，便于前端与收款分摊识别。
func TestDepositAppearsAsBillItem(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D06", 1000, 2000)

	bill := createBillFor(t, env, roomID, "2026-09")
	billID := uint(num(bill["id"]))

	var items []BillItem
	env.db.Where("bill_id = ?", billID).Find(&items)
	var found *BillItem
	for i := range items {
		if items[i].FeeKey == feeKeyDeposit {
			found = &items[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("账单明细里应有押金一项，实得 %+v", items)
	}
	if found.Name != "押金" {
		t.Errorf("明细名称应为「押金」，实得 %q", found.Name)
	}
	if found.Amount != 2000 {
		t.Errorf("明细金额应为 2000，实得 %v", found.Amount)
	}
}

// TestDepositUsesFirstTenantWhenMultiple 多人同住时押金按第一位（整套房一份），
// 不按人头累加。
func TestDepositUsesFirstTenantWhenMultiple(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D07", 1000, 2000)

	// 第二个人也填押金 1500 —— 不应累加成 3500。
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "同住人", "deposit": 1500,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed 2nd tenant: %d %s", w.Code, w.Body.String())
	}

	bill := createBillFor(t, env, roomID, "2026-09")
	if got := num(bill["deposit_fee"]); got != 2000 {
		t.Errorf("押金应取第一位租户的 2000（不按人头累加），实得 %v", got)
	}
}

// TestDepositEditableAndClearable 押金可手动改、可清零（房东临时减免押金的场景）。
func TestDepositEditableAndClearable(t *testing.T) {
	env := setupEnv(t)
	roomID, _ := seedRoomWithDeposit(t, env, "D08", 1000, 2000)

	bill := createBillFor(t, env, roomID, "2026-09")
	billID := uint(num(bill["id"]))

	// 改成 500。
	w := env.do(http.MethodPut, "/api/rental/bills/"+itoa(billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "deposit_fee": 500,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("改押金: %d %s", w.Code, w.Body.String())
	}
	after := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := num(after["deposit_fee"]); got != 500 {
		t.Errorf("押金应改为 500，实得 %v", got)
	}
	if got := num(after["total_amount"]); got != 1500 {
		t.Errorf("合计应为 1000+500=1500，实得 %v", got)
	}

	// 清零（免押金）。
	w = env.do(http.MethodPut, "/api/rental/bills/"+itoa(billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "deposit_fee": 0,
	})
	after = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := num(after["deposit_fee"]); got != 0 {
		t.Errorf("押金应可清零，实得 %v", got)
	}
	if got := num(after["total_amount"]); got != 1000 {
		t.Errorf("清零后合计应为 1000，实得 %v", got)
	}
}
