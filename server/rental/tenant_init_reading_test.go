// tenant_init_reading_test.go — 入住登记记录水电煤气起始读数（Gitee issue IKW41H 需求 7）。
//
// 粉丝原话：「入住登记的时候，顺便记录水电煤气各类项目的起始读数」。
// 三条口径要钉住：
//   - 登记时填了底数，首期账单的「上月读数」就是这些底数（不是上一任租客的收尾读数）；
//   - 底数只作用于首张账单，续期账单仍按上一张账单的本月读数衔接；
//   - 负数底数直接拒绝，不写出负用量的账单。
package rental

import (
	"net/http"
	"testing"
	"time"
)

// TestTenantInitialReadingsFeedFirstBill 底数进首期账单的「上月读数」，本月读数同值（用量 0 待抄）。
func TestTenantInitialReadingsFeedFirstBill(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "I01", 1000)

	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "赵六", "deposit": 1000,
		"move_in_date": "2026-11-03", "create_first_bill": true,
		"init_water": 120.5, "init_elec": 340.2, "init_gas": 56.8,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}

	list := billsOfRoom(t, env, roomID, "2026-11")
	if len(list) != 1 {
		t.Fatalf("首期账单应恰好 1 张，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	for _, c := range []struct {
		field string
		want  float64
	}{
		{"water_last", 120.5}, {"elec_last", 340.2}, {"gas_last", 56.8},
		{"water_now", 120.5}, {"elec_now", 340.2}, {"gas_now", 56.8},
	} {
		if got := num(b[c.field]); got != c.want {
			t.Errorf("%s 应为 %v（入住底数），实得 %v", c.field, c.want, got)
		}
	}
}

// TestTenantInitialReadingsWithoutMoveInDate 没填入住日期（选填）时底数照样生效。
// 2026-11-24 无头实测抓到的真缺陷：登记表单默认不填入住日期，原来直接把底数丢掉了。
func TestTenantInitialReadingsWithoutMoveInDate(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "I04", 1000)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "周九", "create_first_bill": true,
		"init_water": 88.5, "init_elec": 199.5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, time.Now().Format("2006-01"))
	if len(list) != 1 {
		t.Fatalf("首期账单应恰好 1 张，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if num(b["water_last"]) != 88.5 || num(b["elec_last"]) != 199.5 {
		t.Errorf("没填入住日期时底数也应生效，实得 水 %v / 电 %v", b["water_last"], b["elec_last"])
	}
}

// TestTenantInitialReadingsOnlyFirstBill 底数只用于首张账单：续期账单按上期读数衔接。
func TestTenantInitialReadingsOnlyFirstBill(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "I02", 1000)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "钱七",
		"move_in_date": "2026-11-03", "create_first_bill": true,
		"init_water": 100, "init_elec": 200, "init_gas": 30,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记租户: %d %s", w.Code, w.Body.String())
	}
	// 抄表后本月读数推进到 150。
	if err := env.db.Model(&Bill{}).Where("room_id = ? AND period = ?", roomID, "2026-11").
		UpdateColumn("water_now", 150).Error; err != nil {
		t.Fatalf("更新首期读数: %v", err)
	}

	var room Room
	if err := env.db.First(&room, roomID).Error; err != nil {
		t.Fatalf("取房源: %v", err)
	}
	second, created, err := generateBillForRoom(env.db, &room, "2026-12", 0)
	if err != nil || !created {
		t.Fatalf("生成 12 月账单失败: created=%v err=%v", created, err)
	}
	// 12 月的上月读数应是 11 月的本月读数 150，而不是入住底数 100。
	if second.WaterLast != 150 {
		t.Errorf("续期账单上月读数应为上期本月读数 150，实得 %v", second.WaterLast)
	}
}

// TestTenantInitialReadingsRejectNegative 负数底数拒绝登记。
func TestTenantInitialReadingsRejectNegative(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "I03", 1000)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "孙八", "init_water": -5,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("负数底数应被拒绝，实得 %d %s", w.Code, w.Body.String())
	}
}
