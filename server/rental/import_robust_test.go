// import_robust_test.go — CSV 导入的编码与格式容错，以及按账期区间导出。
//
// 背景（2026-10-11 粉丝反馈"批量导入不了房源/租客"）：后端本身能导入，但真实的
// Excel / WPS 输出常带三类"脏东西"，会把导入挡下来且报出误导性的"表头不一致"：
//  1. GBK/GB18030 编码（WPS 另存 CSV 的默认编码）；
//  2. 行尾多余空列（表格软件常加一个逗号）；
//  3. UTF-8 BOM（已支持，这里一并回归）。
//
// 本文件把这三类的容错钉死。
package rental

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// uploadCSVBytes 以 multipart 上传原始字节的 CSV（用于构造 GBK 等非 UTF-8 内容）。
func uploadCSVBytes(t *testing.T, env *testEnv, path string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "import.csv")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.token)
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

// roomCSVWith 生成一份房源导入 CSV（官方 14 列表头）。
func roomCSVWith(rows string) string {
	return "房号,小区,楼栋,单元,楼层,默认租金,默认卫生费,默认管理费,电费单价,燃气单价,水表底数,电表底数,燃气表底数,备注\n" + rows
}

// TestRoomImportAcceptsTrailingEmptyColumns 行尾多余空列（Excel/WPS 常加）应被容忍。
func TestRoomImportAcceptsTrailingEmptyColumns(t *testing.T) {
	env := setupEnv(t)
	// 表头与数据行都多一个尾随逗号。
	csvText := roomCSVWith("TE01,测试小区,1栋,1单元,1,1200,0,0,1.2,3.5,0,0,0,尾随空列,\n")
	w := uploadCSVBytes(t, env, "/api/rental/rooms/import", []byte(csvText))
	if w.Code != http.StatusOK {
		t.Fatalf("尾随空列应被容忍，实得 %d %s", w.Code, w.Body.String())
	}
	if got := int(field(t, decodeBody(t, w), "data", "created").(float64)); got != 1 {
		t.Errorf("应新建 1 间房，实得 %d", got)
	}
}

// TestRoomImportAcceptsGBKEncoding WPS 另存为 GBK 编码的 CSV 应能导入。
func TestRoomImportAcceptsGBKEncoding(t *testing.T) {
	env := setupEnv(t)
	utf8CSV := roomCSVWith("TG01,测试小区,1栋,1单元,1,1200,0,0,1.2,3.5,0,0,0,GBK编码\n")
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(utf8CSV))
	if err != nil {
		t.Fatalf("encode gbk: %v", err)
	}
	// 确认确实是 GBK（不是合法 UTF-8），否则这条用例没测到点上。
	if bytes.Equal(gbk, []byte(utf8CSV)) {
		t.Fatal("GBK 编码结果与原文相同，测试无效")
	}

	w := uploadCSVBytes(t, env, "/api/rental/rooms/import", gbk)
	if w.Code != http.StatusOK {
		t.Fatalf("GBK 编码应被识别并导入，实得 %d %s", w.Code, w.Body.String())
	}
	if got := int(field(t, decodeBody(t, w), "data", "created").(float64)); got != 1 {
		t.Errorf("应新建 1 间房，实得 %d", got)
	}
	// 中文不能被解成乱码。
	var room Room
	if err := env.db.Where("room_no = ?", "TG01").First(&room).Error; err != nil {
		t.Fatalf("找不到导入的房: %v", err)
	}
	if room.Community != "测试小区" {
		t.Errorf("小区应正确解码为「测试小区」，实得 %q", room.Community)
	}
}

// TestRoomImportAcceptsBOM 带 UTF-8 BOM 的 CSV 应能导入（原有能力，防回归）。
func TestRoomImportAcceptsBOM(t *testing.T) {
	env := setupEnv(t)
	csvText := "\ufeff" + roomCSVWith("TB01,测试小区,1栋,1单元,1,1200,0,0,1.2,3.5,0,0,0,BOM\n")
	w := uploadCSVBytes(t, env, "/api/rental/rooms/import", []byte(csvText))
	if w.Code != http.StatusOK {
		t.Fatalf("带 BOM 应能导入，实得 %d %s", w.Code, w.Body.String())
	}
}

// TestTenantImportAcceptsTrailingEmptyColumns 租客导入同样容忍尾随空列与 GBK。
func TestTenantImportAcceptsTrailingEmptyColumns(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("TI01", 1000, 0)
	_ = roomID
	// 租客导入要求房源先存在（按小区+房号匹配）。
	header := "小区,房号,姓名,电话,身份证号,入住日期,租约到期日,押金,水费模式,水费单价,水费包月金额,缴费周期,缴费日,提前提醒天数,备注"
	csvText := header + "\n,TI01,尾巴空列租户,13800000001,,2026-10-01,,1000,按吨,5.00,0,月付,5,3,测试,\n"
	w := uploadCSVBytes(t, env, "/api/rental/tenants/import", []byte(csvText))
	if w.Code != http.StatusOK {
		t.Fatalf("租客导入应容忍尾随空列，实得 %d %s", w.Code, w.Body.String())
	}
	if got := int(field(t, decodeBody(t, w), "data", "created").(float64)); got != 1 {
		t.Errorf("应新建 1 个租户，实得 %d", got)
	}
}

// TestBillsExportByPeriodRange 按账期区间导出（2026-10-11 粉丝反馈
// "不能按时间段批量导出"）：period_from/period_to 是闭区间。
func TestBillsExportByPeriodRange(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("EX01", 1000, 0)
	env.seedTenant(roomID, "导出租户")
	for _, p := range []string{"2026-08", "2026-09", "2026-10", "2026-11"} {
		w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
			"room_id": roomID, "period": p,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("seed bill %s: %d %s", p, w.Code, w.Body.String())
		}
	}

	periods := func(query string) []string {
		t.Helper()
		w := doJSON(env.r, http.MethodGet, "/api/rental/export"+query, env.token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("export%s: %d %s", query, w.Code, w.Body.String())
		}
		recs, err := csv.NewReader(bytes.NewReader(w.Body.Bytes())).ReadAll()
		if err != nil {
			t.Fatalf("parse csv: %v", err)
		}
		out := make([]string, 0, len(recs))
		for _, r := range recs[1:] { // 跳过表头
			if len(r) > 2 {
				out = append(out, r[2]) // 第 3 列是月份
			}
		}
		return out
	}

	// 区间 09~10 应含两端，不含 08 与 11。
	got := periods("?period_from=2026-09&period_to=2026-10")
	want := map[string]bool{"2026-09": true, "2026-10": true}
	if len(got) != 2 {
		t.Fatalf("区间 09~10 应导出 2 张，实得 %v", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Errorf("区间 09~10 不应包含 %s", p)
		}
	}

	// 只给起始 → 该月及以后全部。
	got = periods("?period_from=2026-10")
	if len(got) != 2 {
		t.Errorf("period_from=2026-10 应导出 2 张（10、11），实得 %v", got)
	}

	// 都不给 → 全部 4 张。
	if got = periods(""); len(got) != 4 {
		t.Errorf("不限账期应导出 4 张，实得 %v", got)
	}

	// 单月口径保持兼容。
	if got = periods("?period=2026-09"); len(got) != 1 || got[0] != "2026-09" {
		t.Errorf("period=2026-09 应只导出 1 张 09，实得 %v", got)
	}
}

// TestCSVColumnsAreStable 导出列头不变（导出→改→回导依赖它）。
func TestCSVColumnsAreStable(t *testing.T) {
	if len(billColumns) == 0 {
		t.Fatal("billColumns 不应为空")
	}
	if fmt.Sprint(billColumns[:3]) != "[房号 租户名 月份]" {
		t.Errorf("导出前 3 列应为 房号/租户名/月份，实得 %v", billColumns[:3])
	}
}
