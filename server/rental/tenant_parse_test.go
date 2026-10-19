// tenant_parse_test.go — 粘贴识别的纯函数与接口测试。
//
// 重点考三件事：
//  1. 用户给的三种字段名（姓名/手机号/身份证号）与中英文冒号都能认；
//  2. 多位租户写在同一条文案里能按「同字段第二次出现」切开；
//  3. 认不出的内容不静默丢弃——要么提示、要么原样带回 unmatched。
package rental

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// parseText 走接口解析一段文案（与前端真实链路一致）。
func parseText(e *testEnv, text string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/rental/tenants/parse-text", map[string]interface{}{"text": text})
}

// parseData 取接口返回的识别结果。
func parseData(t *testing.T, e *testEnv, text string) tenantParseResult {
	t.Helper()
	w := parseText(e, text)
	if w.Code != http.StatusOK {
		t.Fatalf("parse text: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	out := tenantParseResult{Count: int(num(res["count"]))}
	if raw, ok := res["drafts"].([]interface{}); ok {
		for _, item := range raw {
			m := item.(map[string]interface{})
			d := tenantDraft{
				Name: m["name"].(string), Phone: m["phone"].(string), IDCard: m["id_card"].(string),
				Community: m["community"].(string), RoomNo: m["room_no"].(string),
				MoveInDate: m["move_in_date"].(string), LeaseEndDate: m["lease_end_date"].(string),
				Deposit: num(m["deposit"]), Notes: m["notes"].(string),
			}
			if raw, ok := m["warnings"].([]interface{}); ok {
				for _, w := range raw {
					d.Warnings = append(d.Warnings, w.(string))
				}
			}
			out.Drafts = append(out.Drafts, d)
		}
	}
	if raw, ok := res["unmatched_lines"].([]interface{}); ok {
		for _, u := range raw {
			out.Unmatched = append(out.Unmatched, u.(string))
		}
	}
	return out
}

// 用户需求原文：一个数据一行，中间用冒号，中英文冒号都支持。
func TestParseTenantTextSingleChineseAndEnglishColon(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：张三\n手机号：18956565845\n身份证:525645487895412546")
	if res.Count != 1 {
		t.Fatalf("count = %d, want 1", res.Count)
	}
	d := res.Drafts[0]
	if d.Name != "张三" || d.Phone != "18956565845" || d.IDCard != "525645487895412546" {
		t.Fatalf("draft = %+v", d)
	}
	// 身份证号 18 位但第 7-14 位不是合法日期：只提示、不阻断（可能是测试号）。
	if len(d.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", d.Warnings)
	}
}

// 多位租户写在同一条文案里：按「同字段第二次出现」切开。
func TestParseTenantTextMultipleTenants(t *testing.T) {
	env := setupEnv(t)
	text := strings.Join([]string{
		"姓名：张三", "手机号：18956565845", "身份证:525645487895412546",
		"姓名：李四", "手机号：13800001111", "身份证号：110101199001011234",
		"姓名：王五", "电话：13900002222",
	}, "\n")
	res := parseData(t, env, text)
	if res.Count != 3 {
		t.Fatalf("count = %d, want 3", res.Count)
	}
	names := []string{}
	for _, d := range res.Drafts {
		names = append(names, d.Name)
	}
	if strings.Join(names, ",") != "张三,李四,王五" {
		t.Fatalf("names = %v", names)
	}
	if res.Drafts[2].Phone != "13900002222" {
		t.Fatalf("王五 phone = %q", res.Drafts[2].Phone)
	}
}

// 房号与小区、日期、押金：日期随手写的几种格式都要归一。
func TestParseTenantTextFieldsAndDates(t *testing.T) {
	env := setupEnv(t)
	text := strings.Join([]string{
		"小区：阳光小区", "房号：101", "姓名：张三",
		"手机号：18956565845", "起租日期：2026/1/5", "到期时间：2027年1月4日",
		"押金：3000元",
	}, "\n")
	res := parseData(t, env, text)
	d := res.Drafts[0]
	if d.Community != "阳光小区" || d.RoomNo != "101" {
		t.Fatalf("room = %+v", d)
	}
	if d.MoveInDate != "2026-01-05" || d.LeaseEndDate != "2027-01-04" {
		t.Fatalf("dates = %+v", d)
	}
	if d.Deposit != 3000 {
		t.Fatalf("deposit = %v", d.Deposit)
	}
	if len(d.Warnings) != 0 {
		t.Fatalf("warnings = %v", d.Warnings)
	}
}

// 手机号位数不对、日期认不出、押金不是数字：都要有提示且不阻断。
func TestParseTenantTextWarnings(t *testing.T) {
	env := setupEnv(t)
	text := "姓名：张三\n手机号：12345\n入住日期：下个月一号\n押金：若干"
	res := parseData(t, env, text)
	d := res.Drafts[0]
	joined := strings.Join(d.Warnings, "|")
	for _, want := range []string{"手机号", "入住日期", "押金"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("warnings = %v, want containing %q", d.Warnings, want)
		}
	}
	if d.Phone != "12345" {
		t.Fatalf("phone = %q, want kept as-is for landlord to fix", d.Phone)
	}
}

// 微信折行：字段名单独占一行、值在下一行，要能接上。
func TestParseTenantTextWrappedLine(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：\n张三\n手机号：\n18956565845")
	d := res.Drafts[0]
	if d.Name != "张三" || d.Phone != "18956565845" {
		t.Fatalf("draft = %+v", d)
	}
}

// 一行里写多个字段也要切开。
func TestParseTenantTextMultipleFieldsOnOneLine(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：张三 手机号：18956565845 身份证号：110101199001011234")
	if res.Count != 1 {
		t.Fatalf("count = %d, want 1", res.Count)
	}
	d := res.Drafts[0]
	if d.Name != "张三" || d.Phone != "18956565845" || d.IDCard != "110101199001011234" {
		t.Fatalf("draft = %+v", d)
	}
}

// 没有字段名的裸行：整体是手机号/身份证号时按值推断。
func TestParseTenantTextBareValues(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：张三\n18956565845\n110101199001011234")
	d := res.Drafts[0]
	if d.Phone != "18956565845" || d.IDCard != "110101199001011234" {
		t.Fatalf("draft = %+v", d)
	}
}

// 多位租户各自的裸行要**各归各的**：不能全塞进最后一条草稿（2026-10-15 复查
// 实测的缺陷——那样第一个人的手机号会丢、第二个人的会被串到第一个名下）。
func TestParseTenantTextBareValuesPerTenant(t *testing.T) {
	env := setupEnv(t)
	text := strings.Join([]string{
		"姓名：张三", "18956565845", "110101199001011234",
		"姓名：李四", "13800001111", "110101199002022345",
	}, "\n")
	res := parseData(t, env, text)
	if res.Count != 2 {
		t.Fatalf("count = %d, want 2", res.Count)
	}
	if res.Drafts[0].Phone != "18956565845" || res.Drafts[0].IDCard != "110101199001011234" {
		t.Fatalf("张三 = %+v", res.Drafts[0])
	}
	if res.Drafts[1].Phone != "13800001111" || res.Drafts[1].IDCard != "110101199002022345" {
		t.Fatalf("李四 = %+v", res.Drafts[1])
	}
	if len(res.Unmatched) != 0 {
		t.Fatalf("unmatched = %v, want none", res.Unmatched)
	}
}

// 值后面跟着中文客套话（微信里极常见）：号码/证件/押金要能截出来，
// 不能整项作废或被污染（2026-10-15 复查实测的缺陷）。
func TestParseTenantTextValueWithTrailingChinese(t *testing.T) {
	env := setupEnv(t)
	text := strings.Join([]string{
		"姓名：张三",
		"手机号：18956565845 麻烦尽快登记",
		"身份证号：110101199001011234 这是证件",
		"押金：3000元，谢谢配合",
	}, "\n")
	res := parseData(t, env, text)
	d := res.Drafts[0]
	if d.Phone != "18956565845" || d.IDCard != "110101199001011234" || d.Deposit != 3000 {
		t.Fatalf("draft = %+v", d)
	}
	// 截出来的要有提示（房东知道原始值里还有别的内容），但不阻断。
	if len(d.Warnings) == 0 {
		t.Fatal("trailing chinese should warn")
	}
}

// 干净的值不能因为兜底逻辑被误报或被截断。
func TestParseTenantTextCleanValuesNoFalseWarning(t *testing.T) {
	env := setupEnv(t)
	text := strings.Join([]string{
		"姓名：张三", "手机号：18956565845", "身份证号：110101199001011234",
		"押金：3,000",
	}, "\n")
	res := parseData(t, env, text)
	d := res.Drafts[0]
	if d.Phone != "18956565845" || d.IDCard != "110101199001011234" || d.Deposit != 3000 {
		t.Fatalf("draft = %+v", d)
	}
	if len(d.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", d.Warnings)
	}
}

// 读不了的行原样带回，不静默丢弃。
func TestParseTenantTextUnmatchedLines(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：张三\n手机号：18956565845\n这段是闲聊与客套")
	if len(res.Unmatched) != 1 || !strings.Contains(res.Unmatched[0], "闲聊") {
		t.Fatalf("unmatched = %v", res.Unmatched)
	}
}

// 空内容 / 读不出任何字段：400 并给出格式提示。
func TestParseTenantTextEmptyAndUseless(t *testing.T) {
	env := setupEnv(t)
	w := parseText(env, "   ")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty text = %d, want 400", w.Code)
	}
	w = parseText(env, "今天天气不错")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("no field = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "姓名") {
		t.Fatalf("hint should mention format, got %q", w.Body.String())
	}
}

// 手机号带分隔符（微信复制常带空格）要归一成纯数字。
func TestParseTenantTextPhoneNormalized(t *testing.T) {
	env := setupEnv(t)
	res := parseData(t, env, "姓名：张三\n手机号：189 5656 5845")
	if res.Drafts[0].Phone != "18956565845" {
		t.Fatalf("phone = %q", res.Drafts[0].Phone)
	}
}

// ---------------------------------------------------------------------------
// 批量入库（POST /rental/tenants/batch）
// ---------------------------------------------------------------------------

// batchTenants 把核对过的草稿批量入库。
func batchTenants(e *testEnv, items []map[string]interface{}) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/rental/tenants/batch", map[string]interface{}{"items": items})
}

// 多位租户一次入库：房源必须已存在（按小区+房号匹配），字段逐列落库。
func TestTenantsBatchCreate(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "101")

	w := batchTenants(env, []map[string]interface{}{
		{
			"name": "张三", "phone": "18956565845", "id_card": "110101199001011234",
			"community": "阳光小区", "room_no": "101",
			"move_in_date": "2026-01-01", "lease_end_date": "2026-12-31", "deposit": 3000,
		},
		{"name": "李四", "phone": "13800001111", "community": "阳光小区", "room_no": "101"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 2 || res["failed"].(float64) != 0 {
		t.Fatalf("batch = %v", res)
	}

	a := findImportedTenant(t, env, roomID, "张三")
	if a.Phone != "18956565845" || a.IDCard != "110101199001011234" {
		t.Fatalf("张三 contact = %v/%v", a.Phone, a.IDCard)
	}
	if a.MoveInDate != "2026-01-01" || a.Deposit != 3000 || !a.Active {
		t.Fatalf("张三 = %+v", a)
	}
	// 没填计费列 = 跟随全局默认（与登记表单留空同口径）。
	if a.PayDay != tenantFollowGlobal || a.RemindDays != tenantFollowGlobal {
		t.Fatalf("张三 pay day/remind = %v/%v, want follow-global", a.PayDay, a.RemindDays)
	}
	if b := findImportedTenant(t, env, roomID, "李四"); !b.Active {
		t.Fatalf("李四 must be active")
	}
}

// 重复导入同一位租户：按「非空覆盖、空值保留」合并更新，不重复建档。
func TestTenantsBatchIdempotentUpdate(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "101")

	items := []map[string]interface{}{
		{"name": "张三", "phone": "18956565845", "community": "阳光小区", "room_no": "101", "deposit": 3000},
	}
	if w := batchTenants(env, items); w.Code != http.StatusOK {
		t.Fatalf("first batch: %d %s", w.Code, w.Body.String())
	}
	// 第二次只改电话，押金留空：应更新电话、保留押金。
	items[0]["phone"] = "13800002222"
	items[0]["deposit"] = 0
	w := batchTenants(env, items)
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["updated"].(float64) != 1 || res["created"].(float64) != 0 {
		t.Fatalf("second batch = %v", res)
	}
	tenant := findImportedTenant(t, env, roomID, "张三")
	if tenant.Phone != "13800002222" || tenant.Deposit != 3000 {
		t.Fatalf("merged = %+v", tenant)
	}
}

// 坏行不阻断其他行：房源不存在、缺姓名、批内重复各报一条错。
func TestTenantsBatchRowErrors(t *testing.T) {
	env := setupEnv(t)
	seedCommunityRoom(t, env, "阳光小区", "101")

	w := batchTenants(env, []map[string]interface{}{
		{"name": "张三", "community": "阳光小区", "room_no": "101"},
		{"name": "李四", "community": "阳光小区", "room_no": "999"}, // 房源不存在
		{"name": "", "community": "阳光小区", "room_no": "101"},    // 姓名缺失
		{"name": "王五", "community": "阳光小区", "room_no": "101"},
		{"name": "王五", "community": "阳光小区", "room_no": "101"}, // 批内重复
	})
	if w.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 2 || res["failed"].(float64) != 3 {
		t.Fatalf("batch = %v", res)
	}
	errs := res["errors"].([]interface{})
	if len(errs) != 3 {
		t.Fatalf("errors = %v", errs)
	}
	// 行号是批内序号（从 1 起），界面上按「第 n 条」展示。
	for i, want := range []string{"房源不存在", "姓名不能为空", "重复"} {
		e := errs[i].(map[string]interface{})
		if !strings.Contains(e["reason"].(string), want) {
			t.Fatalf("error[%d] = %v, want containing %q", i, e, want)
		}
	}
}

// 空数组 / 超量：400，不落库。
func TestTenantsBatchRejectsBadPayload(t *testing.T) {
	env := setupEnv(t)
	if w := batchTenants(env, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("empty items = %d, want 400", w.Code)
	}
	if w := batchTenants(env, []map[string]interface{}{}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty list = %d, want 400", w.Code)
	}
	items := make([]map[string]interface{}, maxPasteTenants+1)
	for i := range items {
		items[i] = map[string]interface{}{"name": "甲", "community": "阳光小区", "room_no": "101"}
	}
	if w := batchTenants(env, items); w.Code != http.StatusBadRequest {
		t.Fatalf("too many = %d, want 400", w.Code)
	}
}

// 解析 → 核对 → 入库 的完整链路：一位租户从文案变成库里的行。
func TestPasteTextToBatchEndToEnd(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "101")

	res := parseData(t, env, "小区：阳光小区\n房号：101\n姓名：张三\n手机号：18956565845\n身份证号：110101199001011234\n起租日期：2026年1月1日")
	if res.Count != 1 {
		t.Fatalf("parse count = %d", res.Count)
	}
	d := res.Drafts[0]
	w := batchTenants(env, []map[string]interface{}{{
		"name": d.Name, "phone": d.Phone, "id_card": d.IDCard,
		"community": d.Community, "room_no": d.RoomNo, "move_in_date": d.MoveInDate,
	}})
	if w.Code != http.StatusOK {
		t.Fatalf("batch: %d %s", w.Code, w.Body.String())
	}
	tenant := findImportedTenant(t, env, roomID, "张三")
	if tenant.Phone != "18956565845" || tenant.MoveInDate != "2026-01-01" {
		t.Fatalf("tenant = %+v", tenant)
	}
}
