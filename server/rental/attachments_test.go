// attachments_test.go — 租户资料附件（租房协议、车牌、租客照片、身份证）的集成测试。
//
// 与合同存档（contracts_test.go）是两套独立能力，这里考：多张上传、图片与 PDF
// 都支持、越权与跨台账不可见、删除清理文件、删租户连带清理。
package rental

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

// pdfBytes 返回一段足以被 http.DetectContentType 认成 application/pdf 的字节。
// DetectContentType 看的是开头 512 字节里的 %PDF- magic，不需要真 PDF 结构。
func pdfBytes() []byte {
	return []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\ntrailer\n<< >>\n%%EOF\n")
}

// uploadAttachment 用 multipart 表单上传一个文件。
func uploadAttachment(t *testing.T, env *testEnv, path, fieldName, fileName string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.token)
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

// uploadAttachmentMulti 一次上传多个文件，可选带 label。
func uploadAttachmentMulti(t *testing.T, env *testEnv, path string, label string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if label != "" {
		if err := mw.WriteField("label", label); err != nil {
			t.Fatalf("write label: %v", err)
		}
	}
	for name, content := range files {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create part: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write part: %v", err)
		}
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

func TestAttachmentUploadImageAndPDF(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("A1", 1000, 0)
	tenantID := env.seedTenant(roomID, "附件租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	// 图片上传成功。
	w := uploadAttachment(t, env, base, "files", "租客照片.png", pngBytes(t))
	if w.Code != http.StatusOK {
		t.Fatalf("上传图片: %d %s", w.Code, w.Body.String())
	}
	item := field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})
	if item["mime_type"] != "image/png" {
		t.Errorf("mime_type = %v, want image/png", item["mime_type"])
	}
	if item["file_name"] != "租客照片.png" {
		t.Errorf("file_name = %v, want 租客照片.png", item["file_name"])
	}

	// PDF 上传成功（这是本需求与合同不同的重点：车牌/身份证扫描件常是 PDF）。
	w = uploadAttachment(t, env, base, "files", "身份证.pdf", pdfBytes())
	if w.Code != http.StatusOK {
		t.Fatalf("上传 PDF: %d %s", w.Code, w.Body.String())
	}
	pdfItem := field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})
	if pdfItem["mime_type"] != "application/pdf" {
		t.Errorf("pdf mime_type = %v, want application/pdf", pdfItem["mime_type"])
	}

	// 列表能看到两张。
	w = doJSON(env.r, http.MethodGet, base, env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("列表: %d %s", w.Code, w.Body.String())
	}
	list := field(t, decodeBody(t, w), "data").([]interface{})
	if len(list) != 2 {
		t.Fatalf("应有 2 个附件，实得 %d", len(list))
	}

	// 图片以 inline 返回、可预览。
	pngID := uint(item["id"].(float64))
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file", pngID), env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("预览图片: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("预览 Content-Type = %q, want image/png", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !bytes.Contains([]byte(cd), []byte("inline")) {
		t.Errorf("默认应 inline 预览，实得 %q", cd)
	}

	// PDF 也能预览（Content-Disposition inline）。
	pdfID := uint(pdfItem["id"].(float64))
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file", pdfID), env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("预览 PDF: %d %s", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !bytes.Contains([]byte(cd), []byte("inline")) {
		t.Errorf("PDF 默认应 inline 预览，实得 %q", cd)
	}

	// download=1 变 attachment。
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file?download=1", pdfID), env.token, nil)
	if cd := w.Header().Get("Content-Disposition"); !bytes.Contains([]byte(cd), []byte("attachment")) {
		t.Errorf("download=1 应为 attachment，实得 %q", cd)
	}
}

func TestAttachmentMultiUploadWithLabel(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("A2", 1000, 0)
	tenantID := env.seedTenant(roomID, "多张附件租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	// 一次传 3 张，带用途备注「身份证」。
	w := uploadAttachmentMulti(t, env, base, "身份证", map[string][]byte{
		"身份证正面.png": pngBytes(t),
		"身份证反面.png": pngBytes(t),
		"身份证扫描.pdf": pdfBytes(),
	})
	if w.Code != http.StatusOK {
		t.Fatalf("多张上传: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if n := int(data["uploaded"].(float64)); n != 3 {
		t.Fatalf("应上传 3 个，实得 %d", n)
	}
	items := data["items"].([]interface{})
	for _, it := range items {
		if label := it.(map[string]interface{})["label"]; label != "身份证" {
			t.Errorf("label = %v, want 身份证", label)
		}
	}

	// 落盘路径必须带台账前缀（台账即目录，防跨台账直读磁盘）。
	var rows []TenantAttachment
	env.db.Where("tenant_id = ?", tenantID).Find(&rows)
	if len(rows) != 3 {
		t.Fatalf("应有 3 行记录，实得 %d", len(rows))
	}
	for _, r := range rows {
		if !bytes.HasPrefix([]byte(r.FilePath), []byte(fmt.Sprintf("ledger-%d/", r.LedgerID))) {
			t.Errorf("落盘路径应以 ledger-%d/ 开头，实得 %q", r.LedgerID, r.FilePath)
		}
	}
}

func TestAttachmentRejectsUnsupportedType(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("A3", 1000, 0)
	tenantID := env.seedTenant(roomID, "非法类型租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	// 可执行文件（此处用文本冒充）应被内容嗅探拦下——扩展名不可信。
	w := uploadAttachment(t, env, base, "files", "恶意.exe", []byte("this is plain text, not an image"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("不支持的类型应 400，实得 %d %s", w.Code, w.Body.String())
	}
}

func TestAttachmentDeleteRemovesFile(t *testing.T) {
	env := setupEnv(t)
	dir := filepath.Join(t.TempDir(), "attachments")
	attachmentsDirOverride = dir
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("A4", 1000, 0)
	tenantID := env.seedTenant(roomID, "删除附件租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	w := uploadAttachment(t, env, base, "files", "车牌.png", pngBytes(t))
	if w.Code != http.StatusOK {
		t.Fatalf("上传: %d %s", w.Code, w.Body.String())
	}
	item := field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})
	id := uint(item["id"].(float64))

	var row TenantAttachment
	if err := env.db.First(&row, id).Error; err != nil {
		t.Fatalf("取记录: %v", err)
	}
	abs, ok := attachmentResolve(row.LedgerID, row.FilePath)
	if !ok || !fileExists(abs) {
		t.Fatalf("上传后文件应存在: %s", abs)
	}

	w = doJSON(env.r, http.MethodDelete, fmt.Sprintf("/api/rental/attachments/%d", id), env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("删除: %d %s", w.Code, w.Body.String())
	}
	if fileExists(abs) {
		t.Error("删除记录后磁盘文件也应被清掉")
	}
	// 再取应 404。
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file", id), env.token, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("已删除的附件应 404，实得 %d", w.Code)
	}
}

// TestAttachmentCrossLedgerInvisible：别的台账的登录用户既看不到也取不到别人的附件。
func TestAttachmentCrossLedgerInvisible(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	// landlord（env.token）在自己的个人台账里建档并上传。
	roomID := env.seedRoom("B1", 1000, 0)
	tenantID := env.seedTenant(roomID, "本人租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)
	w := uploadAttachment(t, env, base, "files", "身份证.png", pngBytes(t))
	if w.Code != http.StatusOK {
		t.Fatalf("上传: %d %s", w.Code, w.Body.String())
	}
	id := uint(field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})["id"].(float64))

	// 另注册一个用户（自带的个人台账里没有这份附件）。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "outsider", "password": "secret123", "phone": "13700000000",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("注册 outsider: %d %s", w.Code, w.Body.String())
	}
	otherToken := field(t, decodeBody(t, w), "data", "token").(string)

	// 越权取文件：必须 404（不是 403——不泄漏「这个 id 存在」）。
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file", id), otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("跨台账取文件应 404，实得 %d %s", w.Code, w.Body.String())
	}
	// 越权删：同样 404。
	w = doJSON(env.r, http.MethodDelete, fmt.Sprintf("/api/rental/attachments/%d", id), otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("跨台账删附件应 404，实得 %d %s", w.Code, w.Body.String())
	}
	// 本人仍然能取到（确认上面不是因为文件本身坏了）。
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file", id), env.token, nil)
	if w.Code != http.StatusOK {
		t.Errorf("本人取文件应 200，实得 %d", w.Code)
	}
}

// TestDeleteTenantCleansAttachments：删租户时附件行与文件一并清理。
func TestDeleteTenantCleansAttachments(t *testing.T) {
	env := setupEnv(t)
	dir := filepath.Join(t.TempDir(), "attachments")
	attachmentsDirOverride = dir
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("C1", 1000, 0)
	tenantID := env.seedTenant(roomID, "待删租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	w := uploadAttachment(t, env, base, "files", "协议.png", pngBytes(t))
	if w.Code != http.StatusOK {
		t.Fatalf("上传: %d %s", w.Code, w.Body.String())
	}
	var row TenantAttachment
	env.db.Where("tenant_id = ?", tenantID).First(&row)
	abs, _ := attachmentResolve(row.LedgerID, row.FilePath)
	if !fileExists(abs) {
		t.Fatalf("上传后文件应存在: %s", abs)
	}

	w = doJSON(env.r, http.MethodDelete, fmt.Sprintf("/api/rental/tenants/%d", tenantID), env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("删租户: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&TenantAttachment{}).Where("tenant_id = ?", tenantID).Count(&n)
	if n != 0 {
		t.Errorf("删租户后附件行应清空，实得 %d", n)
	}
	if fileExists(abs) {
		t.Error("删租户后附件文件应被清掉")
	}
}

// TestAttachmentLabelTooLong：用途备注超长应被拒（防脏数据）。
func TestAttachmentLabelTooLong(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("C2", 1000, 0)
	tenantID := env.seedTenant(roomID, "备注超长租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)

	w := uploadAttachmentMulti(t, env, base, "这是一个非常长的用途备注超过二十个字应该被拒绝掉", map[string][]byte{
		"x.png": pngBytes(t),
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超长备注应 400，实得 %d %s", w.Code, w.Body.String())
	}
}
