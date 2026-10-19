// apply_flow_test.go — 租客登记公开流的两个反馈（2026-10-13）：
//
//  1. 房东没配 AI 识别 Key 时，页面上仍显示「AI 自动识别」按钮，点了只看到
//     「网络异常」——实际服务端回了 400「房东还未配置 AI 识别服务」。修法：
//     公开信息接口回 ai_ocr 开关，前端没配就不显示按钮；错误文案取服务端 message。
//  2. 同一房源同一手机号第二次提交被拒（防冒充改资料，见 applications.go），
//     前端同样吞成「网络异常」。修法：拒绝时带业务码 2001，前端渲染成
//     「你已经提交过了」。
package rental

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/sysconfig"
)

// applyTokenForTest 固定 token，便于直接构造公开链接。
const applyTokenForTest = "0123456789abcdef0123456789abcdef"

func seedApplyRoom(t *testing.T, env *testEnv, roomNo string) Room {
	t.Helper()
	room := Room{
		UserID: env.adminID, LedgerID: env.personalLedger,
		Community: "测试小区", RoomNo: roomNo, ApplyToken: applyTokenForTest,
	}
	if err := env.db.Create(&room).Error; err != nil {
		t.Fatalf("seed apply room: %v", err)
	}
	return room
}

// jpegBytes 生成一段能被 http.DetectContentType 认成 image/jpeg 的字节。
func jpegBytes() []byte {
	return append([]byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
		bytes.Repeat([]byte{0x20}, 128)...)
}

// postApplyForm 以 multipart 提交公开登记接口（photo 为空则不带文件）。
func postApplyForm(t *testing.T, env *testEnv, path string, fields map[string]string, photo []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if photo != nil {
		part, err := mw.CreateFormFile("photo", "id-card.jpg")
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(photo); err != nil {
			t.Fatalf("write photo: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

func applyTmpFileCount(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, applyPhotoDir))
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read apply-tmp: %v", err)
	}
	return len(entries)
}

// TestApplyInfoExposesAIConfigState：公开页要知道房东有没有开 AI 识别，
// 否则点了按钮只能拿到「未配置」的失败。开关与 Key 两个条件缺一不可。
func TestApplyInfoExposesAIConfigState(t *testing.T) {
	env := setupEnv(t)
	seedApplyRoom(t, env, "101")

	aiOCR := func() interface{} {
		t.Helper()
		w := doJSON(env.r, http.MethodGet, "/api/rental/apply/"+applyTokenForTest, "", nil)
		return field(t, decodeBody(t, w), "data", "ai_ocr")
	}
	setKey := func(v string) {
		t.Helper()
		if err := sysconfig.UpdateConfig(env.db, "rental_ai_api_key", v, 0); err != nil {
			t.Fatalf("set ai key: %v", err)
		}
	}
	setSwitch := func(v string) {
		t.Helper()
		if err := sysconfig.UpdateConfig(env.db, aiEnabledConfigKey, v, 0); err != nil {
			t.Fatalf("set ai switch: %v", err)
		}
	}

	// 默认：开关开着但没配 Key → 不能用（配了 Key 才真正可用）。
	if got := aiOCR(); got != false {
		t.Fatalf("未配置 Key 时 ai_ocr 应为 false，实得 %v", got)
	}
	setKey("test-key")
	if got := aiOCR(); got != true {
		t.Fatalf("开关开 + 配了 Key 时 ai_ocr 应为 true，实得 %v", got)
	}
	// 后台开关关掉：即使配了 Key 也不显示。
	setSwitch("false")
	if got := aiOCR(); got != false {
		t.Fatalf("关掉 AI 开关后 ai_ocr 应为 false，实得 %v", got)
	}
	// 开关打开、Key 又被清掉：同样不能用。
	setSwitch("true")
	setKey("")
	if got := aiOCR(); got != false {
		t.Fatalf("清掉 Key 后 ai_ocr 应为 false，实得 %v", got)
	}
}

// TestApplyOCRWithoutKeyGivesClearMessage：不能用时必须给出可照读的原因
//（前端直接显示它，不再包装成「网络异常」），且请求不去打上游。
func TestApplyOCRWithoutKeyGivesClearMessage(t *testing.T) {
	env := setupEnv(t)
	seedApplyRoom(t, env, "102")

	w := postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest+"/ocr", nil, jpegBytes())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未配置时识别应 400，实得 %d %s", w.Code, w.Body.String())
	}
	msg, _ := decodeBody(t, w)["message"].(string)
	if !strings.Contains(msg, "AI 识别") {
		t.Fatalf("识别不可用的提示应说清原因，实得 %q", msg)
	}
}

// TestApplyOCRRespectsSwitch：后台开关关掉后，即使配了 Key 也不再识别。
func TestApplyOCRRespectsSwitch(t *testing.T) {
	env := setupEnv(t)
	seedApplyRoom(t, env, "104")
	if err := sysconfig.UpdateConfig(env.db, "rental_ai_api_key", "test-key", 0); err != nil {
		t.Fatalf("set ai key: %v", err)
	}
	if err := sysconfig.UpdateConfig(env.db, aiEnabledConfigKey, "false", 0); err != nil {
		t.Fatalf("disable ai: %v", err)
	}

	w := postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest+"/ocr", nil, jpegBytes())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("关掉开关后识别应 400，实得 %d %s", w.Code, w.Body.String())
	}
	msg, _ := decodeBody(t, w)["message"].(string)
	if !strings.Contains(msg, "未开启") {
		t.Fatalf("关掉开关的提示应说清原因，实得 %q", msg)
	}
}

// TestApplySubmitDuplicateRejectedWithCode：同房源同手机号第二次提交要被拒，
// 并带上业务码 2001 供前端渲染成「你已经提交过了」；被拒的那次不留孤儿照片。
func TestApplySubmitDuplicateRejectedWithCode(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })
	seedApplyRoom(t, env, "103")

	fields := map[string]string{"name": "张三", "phone": "13800000001", "id_card": "110101199001011234"}

	// 第一次：成功，照片落进 apply-tmp。
	w := postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest, fields, jpegBytes())
	if w.Code != http.StatusOK {
		t.Fatalf("首次提交应成功，实得 %d %s", w.Code, w.Body.String())
	}
	var cnt int64
	env.db.Model(&TenantApplication{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("首次提交后应有 1 条申请，实得 %d", cnt)
	}
	if got := applyTmpFileCount(t, attachmentsDirOverride); got != 1 {
		t.Fatalf("首次提交的照片应落盘 1 个，实得 %d", got)
	}

	// 第二次（同手机号）：拒绝，业务码 2001，数据不新增，照片被清理。
	w = postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest, fields, jpegBytes())
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重复提交应被拒，实得 %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if code := int(body["code"].(float64)); code != CodeApplyDuplicate {
		t.Fatalf("重复提交的业务码应为 %d，实得 %d", CodeApplyDuplicate, code)
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "无需重复提交") {
		t.Fatalf("重复提交的提示应说清结论，实得 %q", msg)
	}
	env.db.Model(&TenantApplication{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("重复提交不得新增申请，实得 %d", cnt)
	}
	if got := applyTmpFileCount(t, attachmentsDirOverride); got != 1 {
		t.Fatalf("被拒那次的照片必须清掉，apply-tmp 应仍为 1，实得 %d", got)
	}

	// 换手机号：另一个人正常提交，互不影响。
	other := map[string]string{"name": "李四", "phone": "13900000002"}
	w = postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest, other, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("不同手机号应能提交，实得 %d %s", w.Code, w.Body.String())
	}
	env.db.Model(&TenantApplication{}).Count(&cnt)
	if cnt != 2 {
		t.Fatalf("不同手机号应有 2 条申请，实得 %d", cnt)
	}
}
