// first_bill_test.go — 入住登记即出首期账单的回归测试（2026-11-18 粉丝需求：
// 「登记入住之后没有生成待缴费订单（房租和押金单）」）。
//
// 口径（见 tenants.go 的 CreateFirstBill 与 bills.go 的 generateBillForRoom）：
//   - 勾选后首期账单与租户**同事务**落库，账期取入住日期所在月；
//   - 押金沿用 firstBillDeposit 的「该房首张账单」既有口径，与租金同一张单收取；
//   - 同房同月已有账单 → 静默跳过（幂等），不挡租户登记；
//   - 不传 create_first_bill 时行为与旧版完全一致（老客户端零影响）；
//   - 生成首期账单要求请求人在该台账同时具备账单模块操作档（界面隐藏不是权限边界）。
package rental

import (
	"net/http"
	"testing"
)

// seedRentRoom 建一间带月租的空房，返回房间 id。
func seedRentRoom(t *testing.T, env *testEnv, roomNo string, rent float64) uint {
	t.Helper()
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": roomNo, "default_rent": rent,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room %s: %d %s", roomNo, w.Code, w.Body.String())
	}
	return uint(field(t, decodeBody(t, w), "data", "id").(float64))
}

// billsOfRoom 该房某账期的账单列表条目。
func billsOfRoom(t *testing.T, env *testEnv, roomID uint, period string) []interface{} {
	t.Helper()
	w := env.do(http.MethodGet, "/api/rental/bills?room_id="+itoa(roomID)+"&period="+period+"&pageSize=100", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list bills: %d %s", w.Code, w.Body.String())
	}
	return itemsOf(t, w)
}

// TestRegisterTenantCreatesFirstBill 登记勾选首期账单：房租+押金同一张单，
// 账期取入住月，状态未缴、单据号补写，明细含租金与押金两行。
func TestRegisterTenantCreatesFirstBill(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "F01", 1500)

	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "韦先生", "deposit": 1500,
		"move_in_date": "2026-11-05", "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}

	list := billsOfRoom(t, env, roomID, "2026-11")
	if len(list) != 1 {
		t.Fatalf("首期账单应恰好 1 张，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if got := num(b["rent"]); got != 1500 {
		t.Errorf("租金应为 1500，实得 %v", got)
	}
	if got := num(b["deposit_fee"]); got != 1500 {
		t.Errorf("首期账单押金应为 1500，实得 %v", got)
	}
	if got := num(b["total_amount"]); got != 3000 {
		t.Errorf("合计应为 1500+1500=3000，实得 %v", got)
	}
	if got, _ := b["status"].(string); got != "unpaid" {
		t.Errorf("首期账单状态应为 unpaid，实得 %v", got)
	}
	if rn, _ := b["receipt_no"].(string); rn == "" {
		t.Errorf("自动生成也必须补写单据号")
	}

	// 明细链与手动开票同源：租金、押金各占一行。
	w = env.do(http.MethodGet, "/api/rental/bills/"+itoa(uint(num(b["id"]))), nil)
	items, _ := field(t, decodeBody(t, w), "data", "items").([]interface{})
	amounts := map[string]float64{}
	for _, it := range items {
		m := it.(map[string]interface{})
		amounts[m["key"].(string)] = num(m["amount"])
	}
	if amounts["rent"] != 1500 {
		t.Errorf("明细租金行应为 1500，实得 %v", amounts["rent"])
	}
	if amounts["deposit"] != 1500 {
		t.Errorf("明细应有押金行 1500，实得 %v", amounts["deposit"])
	}
}

// TestRegisterTenantWithoutFlagNoBill 不勾选时零行为变化：老客户端与
// 不想自动出单的房东都不受影响（历史兼容口径）。
func TestRegisterTenantWithoutFlagNoBill(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "F02", 1200)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "不自动出单租户", "deposit": 1200,
		"move_in_date": "2026-11-01",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}
	if n := len(billsOfRoom(t, env, roomID, "2026-11")); n != 0 {
		t.Errorf("未勾选不应建账，实得 %d 张", n)
	}
}

// TestRegisterTenantFirstBillIdempotent 同房同月已有账单时再登记（合住/补登
// 场景）：租户照常建成，账单不重复、押金不再收（firstBillDeposit 的
// 「该房已有账单就不再收」口径）。
func TestRegisterTenantFirstBillIdempotent(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "F03", 1000)
	for _, name := range []string{"首位租户", "同住租户"} {
		w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
			"room_id": roomID, "name": name, "deposit": 1000,
			"move_in_date": "2026-11-05", "create_first_bill": true,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("登记 %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	if len(list) != 1 {
		t.Fatalf("同房同月应只有 1 张账单，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if got := num(b["deposit_fee"]); got != 1000 {
		t.Errorf("押金只应出现在首张账单一次，实得 %v", got)
	}
}

// TestRegisterTenantFirstBillNeedsBillPerm 权限边界：只有租户权限的成员
// 不能借登记造账单（403 且档案不落库）；补上账单操作档后放行。
func TestRegisterTenantFirstBillNeedsBillPerm(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "firstbill-owner")
	shared := createSharedLedger(t, env, owner.token, "firstbill-ledger")
	ledgerQ := "?ledger_id=" + itoa(shared)

	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms"+ledgerQ, owner.token,
		map[string]interface{}{"community": "A区", "room_no": "F90", "default_rent": 800})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "firstbill-emp", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register emp: %d %s", w.Code, w.Body.String())
	}
	empID := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	empToken := field(t, decodeBody(t, w), "data", "token").(string)
	inviteAndAccept(t, env, owner.token, empToken, shared, "firstbill-emp")

	setPerms := func(perms ModulePerms) {
		t.Helper()
		if w := doJSON(env.r, http.MethodPut,
			"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(empID), owner.token,
			map[string]interface{}{"perms": perms}); w.Code != http.StatusOK {
			t.Fatalf("set perms %v: %d %s", perms, w.Code, w.Body.String())
		}
	}

	body := map[string]interface{}{
		"room_id": roomID, "name": "受限员工登记租户", "deposit": 800,
		"move_in_date": "2026-11-05", "create_first_bill": true,
	}

	// 只有租户权限：账单写权限缺失 → 403，租户也不该落库（校验在事务之前）。
	// 台账参数只能有一个 ?：路径已带 query 时接 &（写成 "+ledgerQ" 会把
	// ledger_id 粘进上一个参数的值里，查空还查错账，2026-11-18 测试踩到）。
	setPerms(ModulePerms{ModuleRooms: PermView, ModuleTenants: PermEdit})
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants"+ledgerQ, empToken, body); w.Code != http.StatusForbidden {
		t.Fatalf("无账单权限登记 = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/tenants?room_id="+itoa(roomID)+"&ledger_id="+itoa(shared), owner.token, nil)
	if n := len(itemsOf(t, w)); n != 0 {
		t.Errorf("越权登记不应留下租户档案，实得 %d 条", n)
	}

	// 补账单操作档：登记 + 首期账单一起成功。
	setPerms(ModulePerms{ModuleRooms: PermView, ModuleTenants: PermEdit, ModuleBills: PermEdit})
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants"+ledgerQ, empToken, body); w.Code != http.StatusOK {
		t.Fatalf("补齐权限后登记: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/bills?room_id="+itoa(roomID)+"&period=2026-11&ledger_id="+itoa(shared), owner.token, nil)
	list := itemsOf(t, w)
	if len(list) != 1 {
		t.Fatalf("应有 1 张首期账单，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 800 || num(b["deposit_fee"]) != 800 {
		t.Errorf("账单金额应随房源月租与租户押金，实得 rent=%v deposit=%v", b["rent"], b["deposit_fee"])
	}
}
