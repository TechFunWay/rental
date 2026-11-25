// meters_test.go — 抄表台账的集成测试:upsert、上期读数/用量、建账沉淀。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

func TestMeterRecordUpsert(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("M1", 1000, 100) // 电表底数 100

	// 首次录入。
	w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "water": 50, "elec": 130, "gas": 10, "note": "首次",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	recID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 同房同月再录 → 覆盖而非新增。
	w = env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-07", "water": 55, "elec": 140, "gas": 12,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("upsert: %d %s", w.Code, w.Body.String())
	}
	if got := uint(field(t, decodeBody(t, w), "data", "id").(float64)); got != recID {
		t.Fatalf("upsert should reuse record %d, got %d", recID, got)
	}
	var n int64
	env.db.Model(&MeterRecord{}).Where("room_id = ?", roomID).Count(&n)
	if n != 1 {
		t.Fatalf("records = %d, want 1（同房同月覆盖）", n)
	}

	// 非法账期 / 他人房间拒绝。
	if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-7",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("bad period = %d, want 400", w.Code)
	}
}

func TestMeterListPrevAndUsage(t *testing.T) {
	env := setupEnv(t)
	// 水底数 10、电底数 100。
	roomID := env.seedRoomWithFees("M2", 1000, 10, 100)
	env.seedTenant(roomID, "台账租户")

	for _, r := range []struct {
		period      string
		water, elec float64
	}{
		{"2026-07", 15, 160},
		{"2026-08", 22, 200},
		{"2026-09", 21, 230}, // 水表倒挂（换表）→ 用量按 0
	} {
		if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
			"room_id": roomID, "period": r.period, "water": r.water, "elec": r.elec,
		}); w.Code != http.StatusOK {
			t.Fatalf("seed %s: %d %s", r.period, w.Code, w.Body.String())
		}
	}

	w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/meter-records?room_id=%d", roomID), nil)
	rows := env.items(decodeBody(t, w))
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// 列表倒序：第一条是 09。
	first := rows[0].(map[string]interface{})
	if first["period"] != "2026-09" {
		t.Fatalf("first period = %v, want 2026-09", first["period"])
	}
	// 09 月:上期是 08(22/200),水倒挂用量 0、电 30。
	if num(first["prev_water"]) != 22 || num(first["prev_elec"]) != 200 {
		t.Errorf("09 prev = %v/%v, want 22/200", first["prev_water"], first["prev_elec"])
	}
	if num(first["usage_water"]) != 0 || num(first["usage_elec"]) != 30 {
		t.Errorf("09 usage = %v/%v, want 0/30", first["usage_water"], first["usage_elec"])
	}
	// 07 月首条:上期对底数(10/100),用量 5/60。
	last := rows[2].(map[string]interface{})
	if last["period"] != "2026-07" {
		t.Fatalf("last period = %v, want 2026-07", last["period"])
	}
	if num(last["prev_water"]) != 10 || num(last["prev_elec"]) != 100 {
		t.Errorf("07 prev = %v/%v, want 10/100", last["prev_water"], last["prev_elec"])
	}
	if num(last["usage_water"]) != 5 || num(last["usage_elec"]) != 60 {
		t.Errorf("07 usage = %v/%v, want 5/60", last["usage_water"], last["usage_elec"])
	}
	if last["room_no"] != "M2" {
		t.Errorf("room_no = %v", last["room_no"])
	}

	// 月份筛选。
	w = env.do(http.MethodGet, "/api/rental/meter-records?period=2026-08", nil)
	if got := len(env.items(decodeBody(t, w))); got != 1 {
		t.Errorf("period filter rows = %d, want 1", got)
	}

	// 删除。
	recID := uint(first["id"].(float64))
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/meter-records/%d", recID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/meter-records?room_id=%d", roomID), nil)
	if got := len(env.items(decodeBody(t, w))); got != 2 {
		t.Errorf("rows after delete = %d, want 2", got)
	}
}

// seedRoomWithFees 建房并指定水/电底数。
func (e *testEnv) seedRoomWithFees(roomNo string, rent, initWater, initElec float64) uint {
	e.t.Helper()
	w := e.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": roomNo, "default_rent": rent, "initial_water": initWater, "initial_elec": initElec,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed room %s: %d %s", roomNo, w.Code, w.Body.String())
	}
	return uint(field(e.t, decodeBody(e.t, w), "data", "id").(float64))
}

func TestBillCreatesMeterRecord(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoomWithFees("M3", 1000, 10, 100)
	env.seedTenant(roomID, "沉淀租户")

	// 抄表录入建账(带当期读数) → 台账自动沉淀一条。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "water_now": 18, "elec_now": 175,
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	var rec MeterRecord
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-09").First(&rec).Error; err != nil {
		t.Fatalf("meter record not settled: %v", err)
	}
	if rec.Water != 18 || rec.Elec != 175 {
		t.Errorf("settled readings = %v/%v, want 18/175", rec.Water, rec.Elec)
	}

	// 已有台账记录时再建同月账单(先删账单再重建模拟) → 不覆盖原记录。
	rec.Water = 20
	env.db.Save(&rec)
	env.db.Where("room_id = ? AND period = ?", roomID, "2026-09").Delete(&Bill{})
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "water_now": 99, "elec_now": 999,
	}); w.Code != http.StatusOK {
		t.Fatalf("recreate bill: %d %s", w.Code, w.Body.String())
	}
	var after MeterRecord
	env.db.Where("room_id = ? AND period = ?", roomID, "2026-09").First(&after)
	if after.Water != 20 || after.Elec != 175 {
		t.Errorf("existing record overwritten: %v/%v", after.Water, after.Elec)
	}
}

// TestBillUpdateSyncsMeterRecord 复现并验证 2026-10-31 粉丝反馈的修复：
// 水电表不在一处，先抄水开票（建账 + 沉淀台账，电还是旧值），再抄电时账单
// 已存在、走**更新账单**路径——台账的电读数必须跟着更新，否则「抄表记录」
// 界面显示旧值而账单金额是对的（用户看到的"副号抄的数据不显示"）。
func TestBillUpdateSyncsMeterRecord(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoomWithFees("M4", 1000, 10, 100)
	env.seedTenant(roomID, "分表租户")

	// 第一遍：只抄水建账。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-11", "water_now": 18,
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}

	var rec MeterRecord
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-11").First(&rec).Error; err != nil {
		t.Fatalf("meter record not settled on create: %v", err)
	}
	if rec.Water != 18 || rec.Elec != 100 {
		t.Fatalf("settled readings = %v/%v, want 18/100", rec.Water, rec.Elec)
	}

	// 第二遍：抄电表，更新账单（读数 + 备注，模拟抄表弹窗保存）。
	var bill Bill
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-11").First(&bill).Error; err != nil {
		t.Fatalf("find bill: %v", err)
	}
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", bill.ID), map[string]interface{}{
		"room_id": roomID, "period": "2026-11",
		"water_last": 10, "water_now": 18,
		"elec_last": 100, "elec_now": 260, "elec_price": 1.2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update bill: %d %s", w.Code, w.Body.String())
	}

	var synced MeterRecord
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-11").First(&synced).Error; err != nil {
		t.Fatalf("find meter record after bill update: %v", err)
	}
	if synced.Elec != 260 {
		t.Errorf("ledger elec = %v, want 260（更新账单后台账必须跟上）", synced.Elec)
	}
	if synced.Water != 18 {
		t.Errorf("ledger water = %v, want 18", synced.Water)
	}
	// 只更新一条，不新增。
	var n int64
	env.db.Model(&MeterRecord{}).Where("room_id = ? AND period = ?", roomID, "2026-11").Count(&n)
	if n != 1 {
		t.Errorf("meter records = %d, want 1", n)
	}

	// 台账被删掉后更新账单 → 按建账口径补一条（不静默丢失）。
	env.db.Where("room_id = ? AND period = ?", roomID, "2026-11").Delete(&MeterRecord{})
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", bill.ID), map[string]interface{}{
		"room_id": roomID, "period": "2026-11",
		"water_last": 10, "water_now": 18,
		"elec_last": 100, "elec_now": 265, "elec_price": 1.2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update bill #2: %d %s", w.Code, w.Body.String())
	}
	var recreated MeterRecord
	if err := env.db.Where("room_id = ? AND period = ?", roomID, "2026-11").First(&recreated).Error; err != nil {
		t.Fatalf("meter record not recreated after update: %v", err)
	}
	if recreated.Elec != 265 {
		t.Errorf("recreated elec = %v, want 265", recreated.Elec)
	}
}
