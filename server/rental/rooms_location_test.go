// rooms_location_test.go — 房源「定位地址 / 建筑面积」的集成测试（2026-10-20 粉丝需求）。
//
// 覆盖：新建与编辑的落库、超长/非法值的拒绝、CSV 新两列的导入与导出回导、
// 以及旧 14 列表头（没有新列）继续可导入——老模板不能因为加列就失效。
package rental

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestRoomAddressAndAreaCRUD：新建带地址面积 → 读取 → 编辑 → 校验上限。
func TestRoomAddressAndAreaCRUD(t *testing.T) {
	env := setupEnv(t)

	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no":   "801",
		"community": "幸福小区",
		"address":   "幸福路 88 号 3 栋东单元（导航到门口，进门左转）",
		"area_size": 68.5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create room: %d %s", w.Code, w.Body.String())
	}
	id := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	var room Room
	if err := env.db.First(&room, id).Error; err != nil {
		t.Fatalf("load room: %v", err)
	}
	if room.Address != "幸福路 88 号 3 栋东单元（导航到门口，进门左转）" {
		t.Fatalf("address = %q", room.Address)
	}
	if room.AreaSize != 68.5 {
		t.Fatalf("area_size = %v, want 68.5", room.AreaSize)
	}

	// 编辑：改地址、面积清成 0（0 表示未填，保留语义）。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", id), map[string]interface{}{
		"room_no": "801", "address": "调整后的地址", "area_size": 0,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update room: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&room, id).Error; err != nil {
		t.Fatalf("reload room: %v", err)
	}
	if room.Address != "调整后的地址" || room.AreaSize != 0 {
		t.Fatalf("after update address=%q area=%v", room.Address, room.AreaSize)
	}

	// 校验：地址超 200 字、面积为负/超上限都要 400。
	longAddr := strings.Repeat("地", 201)
	cases := []struct {
		name string
		body map[string]interface{}
	}{
		{"地址超长", map[string]interface{}{"room_no": "802", "address": longAddr}},
		{"面积为负", map[string]interface{}{"room_no": "803", "area_size": -1}},
		{"面积超上限", map[string]interface{}{"room_no": "804", "area_size": 100001}},
	}
	for _, tc := range cases {
		w := env.do(http.MethodPost, "/api/rental/rooms", tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400 (%s)", tc.name, w.Code, w.Body.String())
		}
	}
}

// TestRoomsImportAddressAndArea：新 16 列表头能导入地址与面积（含留空）。
func TestRoomsImportAddressAndArea(t *testing.T) {
	env := setupEnv(t)
	header := roomImportHeader + ",定位地址,面积(㎡)"
	// 行内列数写成显式的 16 项，避免手工数逗号数错列（曾经把它数成 15 列，
	// 结果地址取到了面积列的值）。
	row := func(fields ...string) string { return strings.Join(fields, ",") }
	csvBody := strings.Join([]string{
		header,
		row("201", "阳光小区", "1栋", "1单元", "2", "1500", "", "", "", "", "", "", "", "",
			"阳光路 12 号 1 栋（小区北门进）", "58.5"),
		row("202", "阳光小区", "1栋", "1单元", "2", "1400", "", "", "", "", "", "", "", "", "", ""),
	}, "\n")
	w := importRoomsCSVBody(env, csvBody)
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	created := field(t, decodeBody(t, w), "data", "created").(float64)
	if created != 2 {
		t.Fatalf("created = %v, want 2", created)
	}
	r1 := findImportedRoom(t, env, "阳光小区", "201")
	if r1.Address != "阳光路 12 号 1 栋（小区北门进）" || r1.AreaSize != 58.5 {
		t.Fatalf("201 address=%q area=%v", r1.Address, r1.AreaSize)
	}
	r2 := findImportedRoom(t, env, "阳光小区", "202")
	if r2.Address != "" || r2.AreaSize != 0 {
		t.Fatalf("202 should keep empty address/area, got %q/%v", r2.Address, r2.AreaSize)
	}
}

// TestRoomsExportContainsAddressAndArea：导出带上两列，且能原样回导（老模板兼容）。
func TestRoomsExportContainsAddressAndArea(t *testing.T) {
	env := setupEnv(t)
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "301", "community": "阳光小区",
		"address": "阳光路 3 号", "area_size": 72,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}

	w = env.do(http.MethodGet, "/api/rental/rooms/export", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "定位地址") || !strings.Contains(body, "面积(㎡)") {
		t.Fatalf("export header missing new columns: %q", body)
	}
	if !strings.Contains(body, "阳光路 3 号") {
		t.Fatalf("export missing address value: %q", body)
	}

	// 回导：导出文件原样导回去，全部按更新处理，地址面积不丢。
	w = importRoomsCSVBody(env, strings.TrimPrefix(body, "\ufeff"))
	if w.Code != http.StatusOK {
		t.Fatalf("re-import: %d %s", w.Code, w.Body.String())
	}
	room := findImportedRoom(t, env, "阳光小区", "301")
	if room.Address != "阳光路 3 号" || room.AreaSize != 72 {
		t.Fatalf("round trip address=%q area=%v", room.Address, room.AreaSize)
	}

	// 老 14 列表头（没有新列）仍然可导入：老模板不能因为加列就失效。
	oldCSV := strings.Join([]string{
		roomImportHeader,
		"302,阳光小区,,,,1300,,,,,,,,老模板",
	}, "\n")
	w = importRoomsCSVBody(env, oldCSV)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy header import: %d %s", w.Code, w.Body.String())
	}
	legacy := findImportedRoom(t, env, "阳光小区", "302")
	if legacy.DefaultRent != 1300 {
		t.Fatalf("legacy import room = %v", legacy)
	}
}
