// rooms_import_test.go — 房源 CSV 导入的集成测试：新建、更新合并、
// 文件内去重、非法值报错与导出文件回导。
package rental

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// importRoomsCSVBody 上传房源导入 CSV，返回响应。
func importRoomsCSVBody(e *testEnv, content string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "rooms.csv")
	_, _ = part.Write([]byte(content))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/rental/rooms/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

const roomImportHeader = "房号,小区,楼栋,单元,楼层,默认租金,默认卫生费,默认管理费,电费单价,燃气单价,水表底数,电表底数,燃气表底数,备注"

func findImportedRoom(t *testing.T, env *testEnv, community, roomNo string) Room {
	t.Helper()
	var room Room
	if err := env.db.Where("community = ? AND room_no = ?", community, roomNo).
		Order("id ASC").First(&room).Error; err != nil {
		t.Fatalf("room %s/%s not found: %v", community, roomNo, err)
	}
	return room
}

func TestRoomsImportCreateAndRowErrors(t *testing.T) {
	env := setupEnv(t)

	// 首列带 BOM，模拟 Excel/WPS 另存的文件；坏行（房号缺失 / 租金非数字 /
	// 底数为负）逐行报错，不影响其他行；103 行带多余空格，验证 trim。
	csvBody := strings.Join([]string{
		"\ufeff" + roomImportHeader,
		"101,阳光小区,1栋,1单元,3,1500,10,30,1.2,3.5,100,800,50,首次导入",
		"102,,,,,1200,,,,,,,,",
		",阳光小区,,,,,,,,,,,,,",
		" 103 , 阳光小区 , 1栋 , 2单元 , 6 , 900 , , , 0.8 , , 30 , , , 带空格",
		"104,阳光小区,,,,ABC,,,,,,,,",
		"105,阳光小区,,,,-5,,,,,,,,",
	}, "\n")

	w := importRoomsCSVBody(env, csvBody)
	if w.Code != http.StatusOK {
		t.Fatalf("import rooms: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 3 {
		t.Fatalf("created = %v, want 3", res["created"])
	}
	if res["failed"].(float64) != 3 {
		t.Fatalf("failed = %v, want 3", res["failed"])
	}
	errs := res["errors"].([]interface{})
	if len(errs) != 3 {
		t.Fatalf("errors = %v", res["errors"])
	}
	// Excel 行号：表头第 1 行，坏行分别是第 4/6/7 行。
	for i, wantLine := range []float64{4, 6, 7} {
		e := errs[i].(map[string]interface{})
		if e["line"].(float64) != wantLine || e["reason"] == "" {
			t.Fatalf("error[%d] = %v, want line %d with reason", i, e, int(wantLine))
		}
	}

	// 落库值抽查：单元格两侧空格已 trim，数字按模板写入。
	room := findImportedRoom(t, env, "阳光小区", "101")
	if room.DefaultRent != 1500 || room.DefaultSanitationFee != 10 || room.DefaultManagementFee != 30 {
		t.Fatalf("room 101 fees = %v/%v/%v", room.DefaultRent, room.DefaultSanitationFee, room.DefaultManagementFee)
	}
	if room.ElecPrice != 1.2 || room.GasPrice != 3.5 || room.InitialWater != 100 || room.InitialElec != 800 || room.InitialGas != 50 {
		t.Fatalf("room 101 prices/meters = %v", room)
	}
	// 103 行：文本与数字都去了空格。
	room = findImportedRoom(t, env, "阳光小区", "103")
	if room.Building != "1栋" || room.Unit != "2单元" || room.Floor != "6" || room.Notes != "带空格" {
		t.Fatalf("room 103 location = %q/%q/%q/%q", room.Building, room.Unit, room.Floor, room.Notes)
	}
	if room.DefaultRent != 900 || room.ElecPrice != 0.8 || room.InitialWater != 30 {
		t.Fatalf("room 103 numbers = %v/%v/%v", room.DefaultRent, room.ElecPrice, room.InitialWater)
	}
	// 无小区房源：小区为空也能建档。
	if room := findImportedRoom(t, env, "", "102"); room.DefaultRent != 1200 {
		t.Fatalf("room 102 rent = %v", room.DefaultRent)
	}
}

func TestRoomsImportUpdateMergeAndDedup(t *testing.T) {
	env := setupEnv(t)

	// 首次导入建档。
	first := strings.Join([]string{
		roomImportHeader,
		"101,阳光小区,1栋,1单元,3,1500,10,30,1.2,3.5,100,800,50,首次导入",
	}, "\n")
	w := importRoomsCSVBody(env, first)
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 1 {
		t.Fatalf("first import = %v", res)
	}

	// 重复导入（幂等）：只改填了值的列（租金 1600、备注），空单元格保留
	// 旧值（楼栋/单元/楼层/水表底数），不产生重复房源。
	update := strings.Join([]string{
		roomImportHeader,
		"101,阳光小区,,,,1600,,,,,,,,租金调价",
	}, "\n")
	w = importRoomsCSVBody(env, update)
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["updated"].(float64) != 1 || res["created"].(float64) != 0 {
		t.Fatalf("update import = %v", res)
	}
	room := findImportedRoom(t, env, "阳光小区", "101")
	if room.DefaultRent != 1600 || room.Notes != "租金调价" {
		t.Fatalf("merged rent/notes = %v/%v", room.DefaultRent, room.Notes)
	}
	if room.Building != "1栋" || room.Unit != "1单元" || room.Floor != "3" || room.InitialWater != 100 {
		t.Fatalf("merge must keep untouched fields, got %v", room)
	}

	// 不同小区允许同房号：另建一间，互不影响。
	other := strings.Join([]string{
		roomImportHeader,
		"101,阳光二村,,,,1800,,,,,,,,",
	}, "\n")
	w = importRoomsCSVBody(env, other)
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 1 {
		t.Fatalf("other community import = %v", res)
	}
	if r := findImportedRoom(t, env, "阳光二村", "101"); r.DefaultRent != 1800 {
		t.Fatalf("other community rent = %v", r.DefaultRent)
	}

	// 文件内 (小区, 房号) 重复：后出现的行报错，指回首次出现的行号。
	dup := strings.Join([]string{
		roomImportHeader,
		"301,阳光小区,,,,1000,,,,,,,,",
		"302,阳光小区,,,,1100,,,,,,,,",
		"301,阳光小区,,,,1200,,,,,,,,",
	}, "\n")
	w = importRoomsCSVBody(env, dup)
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 2 || res["failed"].(float64) != 1 {
		t.Fatalf("dup import = %v", res)
	}
	errs := res["errors"].([]interface{})
	e := errs[0].(map[string]interface{})
	if e["line"].(float64) != 4 || !strings.Contains(e["reason"].(string), "第 2 行") {
		t.Fatalf("dup error = %v, want line 4 pointing to line 2", e)
	}
	if r := findImportedRoom(t, env, "阳光小区", "301"); r.DefaultRent != 1000 {
		t.Fatalf("dup first row must win, rent = %v", r.DefaultRent)
	}

	// 表头不符 → 400。
	w = importRoomsCSVBody(env, "a,b,c\n1,2,3\n")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad header = %d, want 400", w.Code)
	}
}

func TestRoomsImportExportRoundTrip(t *testing.T) {
	env := setupEnv(t)

	// 建两间房（一间带在租租户，导出的计费列取租户/全局生效值）。
	roomID := env.seedRoom("601", 2000, 100)
	env.seedTenant(roomID, "赵一")
	env.seedRoom("602", 1500, 0)
	// 给 601 补上位置与费用信息。
	env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "601", "community": "阳光小区", "building": "2栋", "unit": "2单元", "floor": "6",
		"default_rent": 2000, "default_sanitation_fee": 10, "default_management_fee": 20,
		"elec_price": 1.5, "initial_elec": 100,
	})

	// 导出房源（20 列、UTF-8 BOM）→ 原样回导：全部按"更新"处理，
	// 不产生重复房源。
	w := env.do(http.MethodGet, "/api/rental/rooms/export", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rooms export: %d", w.Code)
	}
	body := w.Body.Bytes()
	if len(body) < 3 || body[0] != 0xEF || body[1] != 0xBB || body[2] != 0xBF {
		t.Fatal("rooms export missing UTF-8 BOM")
	}
	if !strings.Contains(w.Body.String(), "水费模式") {
		t.Fatal("rooms export missing billing columns")
	}
	w = importRoomsCSVBody(env, strings.TrimPrefix(w.Body.String(), "\ufeff"))
	if w.Code != http.StatusOK {
		t.Fatalf("rooms re-import: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["updated"].(float64) != 2 || res["created"].(float64) != 0 {
		t.Fatalf("round trip import = %v, want updated=2", res)
	}

	// 回导是合并更新，不破坏原值。
	room := findImportedRoom(t, env, "阳光小区", "601")
	if room.DefaultRent != 2000 || room.Building != "2栋" || room.InitialElec != 100 {
		t.Fatalf("round trip room 601 = %v", room)
	}
	var count int64
	env.db.Model(&Room{}).Count(&count)
	if count != 2 {
		t.Fatalf("room count = %d, want 2 (no duplicates)", count)
	}
}

func TestRoomsTemplateDownload(t *testing.T) {
	env := setupEnv(t)

	w := env.do(http.MethodGet, "/api/rental/rooms/template", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("template: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "房号") || !strings.Contains(w.Body.String(), "示例行") {
		t.Fatalf("template content = %q", w.Body.String())
	}
}
