// meter_switch_test.go — 表种启停与抄表时间（2026-10-30 粉丝反馈）。
//
// 用户原话：「能不能把用不到的功能关掉（电表 燃气表）水表能不能添加上抄时间」。
//
// 这里考四件事：
//  1. 表种开关能被台账设置接口存下去（白名单漏登记会让开关静默失效，
//     界面上点了关、后端 400 或忽略，房东以为关了其实没关）；
//  2. 关掉电表后，**新生成的账单不再收电费**，且金额快照进 DisabledFees；
//     历史账单金额一分不变（账单是快照）；
//  3. 计费默认接口把开关下发给前端（抄表开票/房源表单据此隐藏输入）；
//  4. 抄表时间能录入、能改、能清空，并且不传时保留原值（不能因为「只改读数」
//     把之前记的时间弄丢）。
package rental

import (
	"net/http"
	"testing"
	"time"
)

// ledgerSettingsEffective 读某台账的生效设置 map。
func ledgerSettingsEffective(t *testing.T, env *testEnv, token string, ledgerID uint) map[string]interface{} {
	t.Helper()
	w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读台账设置失败: %d %s", w.Code, w.Body.String())
	}
	eff, _ := field(t, decodeBody(t, w), "data", "effective").(map[string]interface{})
	if eff == nil {
		t.Fatal("台账设置缺少 effective")
	}
	return eff
}

// TestMeterSwitchThroughLedgerSettings 表种开关走台账设置接口：能存、能读回、能关。
func TestMeterSwitchThroughLedgerSettings(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "meter-switch-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "表种开关台账")

	// 默认全开（升级后行为与升级前一致）。
	eff := ledgerSettingsEffective(t, env, owner.token, ledgerID)
	for _, key := range []string{meterElecEnabledKey, meterGasEnabledKey} {
		if got, _ := eff[key].(string); got != "true" {
			t.Errorf("默认 effective[%s] = %q, want \"true\"", key, got)
		}
	}

	// 关掉电表：白名单漏登记会让这里 400。
	w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{meterElecEnabledKey: "false"}})
	if w.Code != http.StatusOK {
		t.Fatalf("关闭电表失败（开关可能没登记进 rentalConfigKeys）: %d %s", w.Code, w.Body.String())
	}
	if meterEnabled(env.db, meterElecEnabledKey, ledgerID) {
		t.Fatal("设置接口返回成功，但电表开关未生效——白名单很可能漏了这个键")
	}
	// 燃气表没动过，仍然启用。
	if !meterEnabled(env.db, meterGasEnabledKey, ledgerID) {
		t.Error("只关了电表，燃气表不该被连带关掉")
	}

	// 读回：effective 里要反映关闭状态（弹窗靠它显示当前值）。
	eff = ledgerSettingsEffective(t, env, owner.token, ledgerID)
	if got, _ := eff[meterElecEnabledKey].(string); got != "false" {
		t.Errorf("effective[%s] = %q, want \"false\"", meterElecEnabledKey, got)
	}

	// 能双向切回（房东可能关错了又打开）。
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{meterElecEnabledKey: "true"}})
	if w.Code != http.StatusOK {
		t.Fatalf("重新开启电表失败: %d %s", w.Code, w.Body.String())
	}
	if !meterEnabled(env.db, meterElecEnabledKey, ledgerID) {
		t.Error("重新开启后电表仍为关闭")
	}
}

// TestDisabledMeterStopsCharging 关掉电表后新账单不再收电费；历史账单不变。
func TestDisabledMeterStopsCharging(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "meter-charge-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "电费停收台账")

	// 先开一张电表还启用时的账单（1 月，电表底数 100 → 本月 130）。
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{
			"room_no": "S01", "default_rent": 1000, "initial_elec": 100,
		})
	if w.Code != http.StatusOK {
		t.Fatalf("建房源: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{"room_id": roomID, "name": "电表租户", "rent": 1000}); w.Code != http.StatusOK {
		t.Fatalf("建租户: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(env.r, http.MethodPost, "/api/rental/bills?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{"room_id": roomID, "period": "2026-01", "elec_now": 130})
	if w.Code != http.StatusOK {
		t.Fatalf("开 1 月账单: %d %s", w.Code, w.Body.String())
	}
	janID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	janBill := decodeBody(t, w)["data"].(map[string]interface{})
	janElecFee := num(janBill["elec_fee"])
	if janElecFee <= 0 {
		t.Fatalf("1 月电费 = %v，用例前提是电表启用时确实收电费", janElecFee)
	}

	// 关掉电表，再开 2 月账单。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{meterElecEnabledKey: "false"}}); w.Code != http.StatusOK {
		t.Fatalf("关闭电表: %d %s", w.Code, w.Body.String())
	}

	w = doJSON(env.r, http.MethodPost, "/api/rental/bills?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{"room_id": roomID, "period": "2026-02", "elec_now": 180})
	if w.Code != http.StatusOK {
		t.Fatalf("开 2 月账单: %d %s", w.Code, w.Body.String())
	}
	febBill := decodeBody(t, w)["data"].(map[string]interface{})
	// 关掉后新账单不收电费：金额 0、快照里记着「电表停收」。
	if got := num(febBill["elec_fee"]); got != 0 {
		t.Errorf("关掉电表后 2 月电费 = %v, want 0", got)
	}
	disabled, _ := febBill["disabled_fees"].([]interface{})
	found := false
	for _, k := range disabled {
		if k == feeKeyElec {
			found = true
		}
	}
	if !found {
		t.Errorf("2 月账单 disabled_fees = %v, want 含 %q", disabled, feeKeyElec)
	}
	// 读数仍照常记录（只是不计费），房东核对用量还要看它。
	if got := num(febBill["elec_now"]); got != 180 {
		t.Errorf("关掉电表后 2 月电表读数 = %v, want 180（读数照记，只是不计费）", got)
	}

	// 历史 1 月账单金额与明细都不变（账单是快照）。
	// 注意账单详情接口把账单放在 data.bill 下（与 items/payments/property 并列），
	// 不是扁平的账单对象。
	janAfter := decodeBody(t, doJSON(env.r, http.MethodGet,
		"/api/rental/bills/"+itoa(janID)+"?ledger_id="+itoa(ledgerID), owner.token, nil))["data"].(map[string]interface{})
	janBillAfter, _ := janAfter["bill"].(map[string]interface{})
	if janBillAfter == nil {
		t.Fatalf("账单详情缺少 data.bill: %v", janAfter)
	}
	if got := num(janBillAfter["elec_fee"]); got != janElecFee {
		t.Errorf("历史 1 月电费被改成 %v, want %v（关表种不该动已开出的账单）", got, janElecFee)
	}
}

// TestBillingDefaultsExposeMeterSwitches 计费默认接口要把表种开关下发给前端。
//
// 抄表开票弹窗、房源表单、账单列表都读它来决定渲染哪些表——接口漏了字段，
// 前端就永远按默认（全开）渲染，房东关了开关却看不到变化。
func TestBillingDefaultsExposeMeterSwitches(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "meter-defaults-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "默认值台账")

	w := doJSON(env.r, http.MethodGet, "/api/rental/billing-defaults?ledger_id="+itoa(ledgerID), owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("读计费默认失败: %d %s", w.Code, w.Body.String())
	}
	data := decodeBody(t, w)["data"].(map[string]interface{})
	// 默认全开。
	if b, ok := data["elec_enabled"].(bool); !ok || !b {
		t.Errorf("默认 elec_enabled = %v, want true", data["elec_enabled"])
	}
	if b, ok := data["gas_enabled"].(bool); !ok || !b {
		t.Errorf("默认 gas_enabled = %v, want true", data["gas_enabled"])
	}

	// 关掉燃气表后接口要反映出来。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{meterGasEnabledKey: "false"}}); w.Code != http.StatusOK {
		t.Fatalf("关闭燃气表: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/billing-defaults?ledger_id="+itoa(ledgerID), owner.token, nil)
	data = decodeBody(t, w)["data"].(map[string]interface{})
	if b, _ := data["gas_enabled"].(bool); b {
		t.Errorf("关闭后 gas_enabled = %v, want false", data["gas_enabled"])
	}
}

// TestMeterRecordReadAt 抄表时间：能录、能改、能清空；不传时保留原值。
func TestMeterRecordReadAt(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R01", 1000, 100)

	// 录入带抄表时间。
	w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "water": 50, "elec": 130, "gas": 10,
		"read_at": "2026-07-05",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("录入: %d %s", w.Code, w.Body.String())
	}
	recID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if got, _ := decodeBody(t, w)["data"].(map[string]interface{})["read_at"].(string); got != "2026-07-05" {
		t.Errorf("read_at = %q, want 2026-07-05", got)
	}

	// 只改读数、不传 read_at → 时间保留（不能因为改数把凭证弄丢）。
	w = env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "water": 55, "elec": 140,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("改读数: %d %s", w.Code, w.Body.String())
	}
	var rec MeterRecord
	if err := env.db.First(&rec, recID).Error; err != nil {
		t.Fatalf("读记录: %v", err)
	}
	if rec.ReadAt != "2026-07-05" {
		t.Errorf("只改读数后 read_at = %q, want 保留 2026-07-05", rec.ReadAt)
	}

	// 显式清空。
	if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "water": 55, "elec": 140, "read_at": "",
	}); w.Code != http.StatusOK {
		t.Fatalf("清空时间: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&rec, recID).Error; err != nil {
		t.Fatalf("读记录: %v", err)
	}
	if rec.ReadAt != "" {
		t.Errorf("清空后 read_at = %q, want 空", rec.ReadAt)
	}

	// 非法日期被拒。
	if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "read_at": "2026-13-40",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("非法日期 = %d, want 400", w.Code)
	}
	// 带时分秒的输入截到日期（前端传 datetime 也能用）。
	if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "read_at": "2026-07-08T10:30",
	}); w.Code != http.StatusOK {
		t.Fatalf("带时间的输入: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&rec, recID).Error; err != nil {
		t.Fatalf("读记录: %v", err)
	}
	if rec.ReadAt != "2026-07-08" {
		t.Errorf("带时间的输入应截到日期，got %q", rec.ReadAt)
	}
}

// TestSettleMeterRecordStampsReadAt 开票自动沉淀的台账记录也要带抄表时间。
//
// 不填的话，房东在抄表记录页看到的是「未记」——而这条记录本来就是开票时
// 顺手生成的，时点就是开票当天，白丢一条可用的凭证。
func TestSettleMeterRecordStampsReadAt(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R02", 1000, 100)
	env.seedTenant(roomID, "沉淀租户")

	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-08", "elec_now": 175,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("开票: %d %s", w.Code, w.Body.String())
	}
	var rec MeterRecord
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-08").First(&rec).Error; err != nil {
		t.Fatalf("台账未沉淀: %v", err)
	}
	if rec.ReadAt == "" {
		t.Error("开票沉淀的台账记录没有抄表时间，抄表记录页会显示「未记」")
	}
	if _, err := time.ParseInLocation("2006-01-02", rec.ReadAt, time.Local); err != nil {
		t.Errorf("沉淀的 read_at = %q 不是合法日期: %v", rec.ReadAt, err)
	}
}

// TestDisabledMeterStopsChargingForTOU 峰谷分时房关掉电表后，新账单同样不收电费。
//
// 峰谷分时（5e4d82c）把电费拆成峰/谷两路计价，走的是 elecFee 的另一条分支。
// 表种开关（2026-10-30）在 recalc 里统一按 feeKeyElec 清零，两条路径必须一致——
// 这里专门验证峰谷房，避免只测单一电价房而漏掉分时分支。
func TestDisabledMeterStopsChargingForTOU(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "tou-switch-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "峰谷停收台账")

	// 建峰谷房：底数峰 100 / 谷 200，峰价 1.5 谷价 0.6
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{
			"room_no": "T01", "default_rent": 1500,
			"elec_tou": true, "elec_peak_price": 1.5, "elec_valley_price": 0.6,
			"initial_elec": 300, "initial_elec_peak": 100, "initial_elec_valley": 200,
		})
	if w.Code != http.StatusOK {
		t.Fatalf("建峰谷房: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{"room_id": roomID, "name": "峰谷租户", "rent": 1500}); w.Code != http.StatusOK {
		t.Fatalf("建租户: %d %s", w.Code, w.Body.String())
	}

	// 关掉电表，再开账单：峰谷读数照录，但电费必须为 0。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{meterElecEnabledKey: "false"}}); w.Code != http.StatusOK {
		t.Fatalf("关闭电表: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(env.r, http.MethodPost, "/api/rental/bills?ledger_id="+itoa(ledgerID), owner.token,
		map[string]interface{}{"room_id": roomID, "period": "2026-03",
			"elec_peak_now": 150, "elec_valley_now": 280})
	if w.Code != http.StatusOK {
		t.Fatalf("开峰谷账单: %d %s", w.Code, w.Body.String())
	}
	bill := decodeBody(t, w)["data"].(map[string]interface{})
	if got := num(bill["elec_fee"]); got != 0 {
		t.Errorf("峰谷房关掉电表后电费 = %v, want 0（峰谷分支也要清零）", got)
	}
	disabled, _ := bill["disabled_fees"].([]interface{})
	found := false
	for _, k := range disabled {
		if k == feeKeyElec {
			found = true
		}
	}
	if !found {
		t.Errorf("峰谷账单 disabled_fees = %v, want 含 %q", disabled, feeKeyElec)
	}
	// 峰谷读数仍照记（只是不计费），房东核对用量要看。
	if got := num(bill["elec_peak_now"]); got != 150 {
		t.Errorf("峰表读数 = %v, want 150（读数照记）", got)
	}
}
