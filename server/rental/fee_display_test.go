// fee_display_test.go — 收费项目的「停用真正生效」与「单据显示自定义」（2026-10-19 粉丝反馈）。
//
// 用户原话：「我把燃气费已经停用了，然后打印单据那里还是会显示有这个项的，
// 就是单据那里也能不能自定义显示项目啊」。
//
// 这里考两件事：
//  1. 内置项目（燃气费等）停用后，**新账单不再收**（此前停用只对自定义项目生效，
//     内置六项从 bills 固定列生成、从不读 FeeItem.Enabled，开关形同虚设）；
//     同时历史账单金额一分不变（账单是快照）。
//  2. 项目可以设为「单据不显示」：单据上不单列，但有金额时合并成「其他费用」，
//     保证单据明细之和仍等于合计；账单详情与统计**不受影响**。
package rental

import (
	"net/http"
	"testing"
)

// fetchFeeItem 按 key 从收费项目列表里取一行。
//
// 必须带 ledgerID 精确定位：列表接口是**并集读口径**（任一台账的成员都能读到
// 全部可见台账的项目），不带台账筛选会同时返回多个台账的同 key 行，
// 此时随便取一条就会改错台账的项目。
func fetchFeeItem(t *testing.T, env *testEnv, ledgerID uint, key string) map[string]interface{} {
	t.Helper()
	w := env.do(http.MethodGet, "/api/rental/fee-items?ledger_id="+itoa(ledgerID), nil)
	// 收费项目列表的 data 直接是数组（不是分页对象）。
	rows, _ := field(t, decodeBody(t, w), "data").([]interface{})
	for _, it := range rows {
		m := it.(map[string]interface{})
		if m["key"] == key && uint(num(m["ledger_id"])) == ledgerID {
			return m
		}
	}
	t.Fatalf("fee item %s in ledger %d not found: %s", key, ledgerID, w.Body.String())
	return nil
}

// updateFeeItemAPI 走接口改一个收费项目（name 必须带：后端是整体覆盖）。
func updateFeeItemAPI(t *testing.T, env *testEnv, item map[string]interface{}, patch map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{
		"name": item["name"], "cycle": item["cycle"],
		"default_amount": item["default_amount"], "enabled": item["enabled"],
	}
	for k, v := range patch {
		body[k] = v
	}
	path := "/api/rental/fee-items/" + itoa(uint(num(item["id"])))
	if w := env.do(http.MethodPut, path, body); w.Code != http.StatusOK {
		t.Fatalf("update fee item %v: %d %s", item["key"], w.Code, w.Body.String())
	}
}

// billItemByKey 从账单详情接口的明细里找一行。
func billItemByKey(t *testing.T, env *testEnv, billID uint, key string) (map[string]interface{}, bool) {
	t.Helper()
	w := env.do(http.MethodGet, "/api/rental/bills/"+itoa(billID), nil)
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	for _, raw := range data["items"].([]interface{}) {
		it := raw.(map[string]interface{})
		if it["key"] == key {
			return it, true
		}
	}
	return nil, false
}

// TestDisabledBuiltInFeeStopsCharging 停用燃气费后，新账单不再收燃气费；
// 历史账单（停用前已开出）金额与单据行都不变。
func TestDisabledBuiltInFeeStopsCharging(t *testing.T) {
	env := setupEnv(t)
	// 燃气底数 100：开票时「上月读数」取底数，本月填 120 即用量 20 方。
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "G01", "default_rent": 1000, "initial_gas": 100,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "停气户", "pay_day": 5})

	// 停用前：开 2026-01，用抄表读数让燃气费 > 0（20 方 × 3.5 = 70）。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-01", "gas_now": 120, "gas_price": 3.5,
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill jan: %d %s", w.Code, w.Body.String())
	}
	jan := env.fetchBill("2026-01", "G01")
	if got := num(jan["gas_fee"]); !almostEqual(got, 70) {
		t.Fatalf("停用前 1 月燃气费 = %v, want 70（20 方 × 3.5）", got)
	}
	if _, ok := billItemByKey(t, env, uint(num(jan["id"])), feeKeyGas); !ok {
		t.Fatalf("停用前 1 月账单明细应有燃气费行")
	}

	// 停用燃气费（内置项目，只改 enabled）。
	updateFeeItemAPI(t, env, fetchFeeItem(t, env, env.personalLedger, feeKeyGas), map[string]interface{}{"enabled": false})

	// 停用后：开 2026-02，同样读数，但燃气费必须为 0，且明细里没有燃气行。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-02", "gas_now": 140, "gas_price": 3.5,
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill feb: %d %s", w.Code, w.Body.String())
	}
	feb := env.fetchBill("2026-02", "G01")
	if got := num(feb["gas_fee"]); !almostEqual(got, 0) {
		t.Errorf("停用后 2 月燃气费 = %v, want 0（停用即不计费）", got)
	}
	// 读数照常保留：停用不等于不抄表。
	if got := num(feb["gas_now"]); !almostEqual(got, 140) {
		t.Errorf("停用后 2 月本月读数 = %v, want 140（读数仍记录）", got)
	}
	if _, ok := billItemByKey(t, env, uint(num(feb["id"])), feeKeyGas); ok {
		t.Errorf("停用后 2 月账单明细不应再有燃气费行")
	}
	// 合计也必须是「减掉燃气」后的数：租金 1000 + 0 燃气 = 1000。
	if got := num(feb["total_amount"]); !almostEqual(got, 1000) {
		t.Errorf("停用后 2 月合计 = %v, want 1000", got)
	}
	// 快照记录停用项，便于排查与界面提示。
	if disabled, _ := feb["disabled_fees"].([]interface{}); len(disabled) != 1 || disabled[0] != feeKeyGas {
		t.Errorf("2 月账单 disabled_fees = %v, want [gas]", feb["disabled_fees"])
	}

	// 历史账单（1 月）一分不动：停用不改写已开出的账单。
	janAfter := env.fetchBill("2026-01", "G01")
	if got := num(janAfter["gas_fee"]); !almostEqual(got, 70) {
		t.Errorf("停用后历史 1 月燃气费 = %v, want 70（历史账单是快照）", got)
	}
	if got := num(janAfter["total_amount"]); !almostEqual(got, num(jan["total_amount"])) {
		t.Errorf("停用后历史 1 月合计 = %v, want %v（不得变化）", got, jan["total_amount"])
	}
	// 历史账单也没有停用快照（开票时燃气还在收）。
	if disabled, _ := janAfter["disabled_fees"].([]interface{}); len(disabled) != 0 {
		t.Errorf("历史 1 月 disabled_fees = %v, want 空", janAfter["disabled_fees"])
	}

	// 重新启用后，3 月恢复收费（停用是开关，不是删除）。
	updateFeeItemAPI(t, env, fetchFeeItem(t, env, env.personalLedger, feeKeyGas), map[string]interface{}{"enabled": true})
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-03", "gas_now": 160, "gas_price": 3.5,
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill mar: %d %s", w.Code, w.Body.String())
	}
	if got := num(env.fetchBill("2026-03", "G01")["gas_fee"]); !almostEqual(got, 70) {
		t.Errorf("重新启用后 3 月燃气费 = %v, want 70", got)
	}
}

// TestReceiptHiddenFeeMergedIntoOther 单据不显示的项目不单列，但金额合并成
// 「其他费用」，保证单据明细之和 = 合计；账单详情不受影响。
func TestReceiptHiddenFeeMergedIntoOther(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("H01", 1000, 0)
	env.setRoomFees(roomID, "H01", 1000, 0, 0, 0)
	env.seedTenantViaAPI(roomID, map[string]interface{}{"name": "隐藏项户", "pay_day": 5})
	broadbandKey := seedCustomFee(t, env, "宽带费", 30)

	// 把宽带费设为「单据不显示」。
	updateFeeItemAPI(t, env, fetchFeeItem(t, env, env.personalLedger, broadbandKey), map[string]interface{}{"receipt_hidden": true})

	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-05",
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	bill := env.fetchBill("2026-05", "H01")
	billID := uint(num(bill["id"]))

	// 账单金额不受「单据显示」影响：宽带费照收。
	if got := num(bill["extra_amount"]); !almostEqual(got, 30) {
		t.Fatalf("宽带费应照收 30，实得 %v（单据显示不影响计费）", got)
	}

	// 明细行仍返回（账单详情要用），但带 receipt_hidden 标记。
	it, ok := billItemByKey(t, env, billID, broadbandKey)
	if !ok {
		t.Fatalf("账单明细应仍包含宽带费（详情/统计要用）")
	}
	if hidden, _ := it["receipt_hidden"].(bool); !hidden {
		t.Errorf("宽带费明细 receipt_hidden = false, want true")
	}

	// 单据接口（批量打印数据源）同样带标记，未隐藏的项目为 false。
	w := env.do(http.MethodGet, "/api/rental/bills/receipts?from=2026-05&to=2026-05", nil)
	entries := field(t, decodeBody(t, w), "data", "bills").([]interface{})
	if len(entries) != 1 {
		t.Fatalf("单据数 = %d, want 1", len(entries))
	}
	items := entries[0].(map[string]interface{})["items"].([]interface{})
	var sum float64
	hiddenSum := 0.0
	for _, raw := range items {
		row := raw.(map[string]interface{})
		sum += num(row["amount"])
		if hid, _ := row["receipt_hidden"].(bool); hid {
			hiddenSum += num(row["amount"])
			if row["key"] != broadbandKey {
				t.Errorf("只有宽带费该被隐藏，%v 也带标记了", row["key"])
			}
		}
	}
	if !almostEqual(hiddenSum, 30) {
		t.Errorf("被隐藏项目的金额合计 = %v, want 30", hiddenSum)
	}
	// 关键不变量：合并后单据明细之和仍等于账单合计（前端把隐藏项合并成一行
	// 「其他费用」，这里验证数据层面金额没丢）。
	if !almostEqual(sum, num(bill["total_amount"])) {
		t.Errorf("单据明细之和 = %v, 账单合计 = %v，隐藏项目不得让金额凭空消失", sum, bill["total_amount"])
	}

	// 取消隐藏后标记消失（历史账单也跟着恢复显示：单据展示口径不进数据库）。
	updateFeeItemAPI(t, env, fetchFeeItem(t, env, env.personalLedger, broadbandKey), map[string]interface{}{"receipt_hidden": false})
	it2, ok := billItemByKey(t, env, billID, broadbandKey)
	if !ok {
		t.Fatalf("宽带费明细缺失")
	}
	if hidden, _ := it2["receipt_hidden"].(bool); hidden {
		t.Errorf("取消隐藏后 receipt_hidden 仍为 true")
	}
}

// TestDisabledFeeTogglesAcrossLedgers 停用是**台账级**的：A 台账停用燃气费不影响 B。
func TestDisabledFeeTogglesAcrossLedgers(t *testing.T) {
	env := setupEnv(t)
	sharedID := createSharedLedger(t, env, env.token, "燃气台账B")

	// 先在共同台账里把内置项目 seed 出来（列表接口会惰性 seed 写入目标台账）。
	if w := env.do(http.MethodGet, "/api/rental/fee-items?ledger_id="+itoa(sharedID), nil); w.Code != http.StatusOK {
		t.Fatalf("list fee items of shared ledger: %d %s", w.Code, w.Body.String())
	}

	pl := fetchFeeItem(t, env, env.personalLedger, feeKeyGas) // 个人台账里的燃气费
	updateFeeItemAPI(t, env, pl, map[string]interface{}{"enabled": false})
	// 停用必须落到个人台账这一行（不是共同台账的同 key 行）。

	if feeItemEnabledInLedger(t, env, env.personalLedger, feeKeyGas) {
		t.Errorf("个人台账的燃气费应已停用")
	}
	if !feeItemEnabledInLedger(t, env, sharedID, feeKeyGas) {
		t.Errorf("另一个台账的燃气费不该被连带停用")
	}
}

// feeItemEnabledInLedger 直接查库判断某台账的某收费项目是否启用。
func feeItemEnabledInLedger(t *testing.T, env *testEnv, ledgerID uint, key string) bool {
	t.Helper()
	var enabled int
	if err := env.db.Raw(
		"SELECT enabled FROM fee_items WHERE org_id = ? AND `key` = ?", ledgerID, key,
	).Scan(&enabled).Error; err != nil {
		t.Fatalf("query fee item: %v", err)
	}
	return enabled == 1
}
