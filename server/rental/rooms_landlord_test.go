// rooms_landlord_test.go — 房源的房东姓名与联系电话（选填）：读写、校验、
// 以及免登录房源登记审核通过后的带入。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

func TestRoomLandlordFields(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("LD1", 1200, 100)

	// 填了：落库
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "LD1", "community": "幸福小区", "default_rent": 1200,
		"landlord_name": " 陈房东 ", "landlord_phone": "138-0000-1111",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("带房东信息保存: %d %s", w.Code, w.Body.String())
	}
	var room Room
	env.db.First(&room, roomID)
	if room.LandlordName != "陈房东" || room.LandlordPhone != "138-0000-1111" {
		t.Errorf("房东信息 = %q/%q, want 陈房东/138-0000-1111（姓名要去空白）", room.LandlordName, room.LandlordPhone)
	}
	// 列表接口带出这两个字段（前端徽标/详情直接用）
	list := env.items(decodeBody(t, env.do(http.MethodGet, "/api/rental/rooms", nil)))
	if len(list) == 0 {
		t.Fatal("房源列表为空")
	}
	row := list[0].(map[string]interface{})
	if row["landlord_name"] != "陈房东" || row["landlord_phone"] != "138-0000-1111" {
		t.Errorf("列表未带房东信息：%v/%v", row["landlord_name"], row["landlord_phone"])
	}

	// 选填：清空也允许
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "LD1", "default_rent": 1200,
	}); w.Code != http.StatusOK {
		t.Fatalf("清空房东信息: %d %s", w.Code, w.Body.String())
	}
	env.db.First(&room, roomID)
	if room.LandlordName != "" || room.LandlordPhone != "" {
		t.Errorf("清空后应为空：%q/%q", room.LandlordName, room.LandlordPhone)
	}

	// 校验：姓名过长 / 电话不像电话
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "LD1", "landlord_name": string(make([]rune, 31)),
	}); w.Code != http.StatusBadRequest {
		t.Errorf("姓名过长 = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "LD1", "landlord_phone": "电话问他",
	}); w.Code != http.StatusBadRequest {
		t.Errorf("电话非法 = %d, want 400", w.Code)
	}
	// 新建房源也能带
	if w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "LD2", "landlord_name": "赵房东", "landlord_phone": "0571-88886666",
	}); w.Code != http.StatusOK {
		t.Fatalf("新建带房东信息: %d %s", w.Code, w.Body.String())
	}
	var room2 Room
	env.db.Where("room_no = ?", "LD2").First(&room2)
	if room2.LandlordName != "赵房东" || room2.LandlordPhone != "0571-88886666" {
		t.Errorf("新建房东信息 = %q/%q", room2.LandlordName, room2.LandlordPhone)
	}
}
