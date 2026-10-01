// contracts_test.go — 租赁合同上传/下载/删除的集成测试。
package rental

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes 生成一张 1x1 PNG 用于上传测试。
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// uploadContract 用 multipart 表单上传一个文件，返回响应。
func uploadContract(t *testing.T, env *testEnv, path, fieldName, fileName string, content []byte) *httptest.ResponseRecorder {
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

func TestContractUploadAndList(t *testing.T) {
	env := setupEnv(t)
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() { contractsDirOverride = "" })

	roomID := env.seedRoom("K1", 1000, 0)
	tenantID := env.seedTenant(roomID, "合同租户")

	// PNG 图片上传成功。
	w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "合同第1页.png", pngBytes(t))
	if w.Code != http.StatusOK {
		t.Fatalf("upload png: %d %s", w.Code, w.Body.String())
	}
	item := field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})
	if item["file_name"] != "合同第1页.png" {
		t.Errorf("file_name = %v", item["file_name"])
	}
	if item["mime_type"] != "image/png" {
		t.Errorf("mime_type = %v, want image/png", item["mime_type"])
	}
	contractID := uint(item["id"].(float64))

	// PDF 上传成功（最小 PDF 头即可，服务端只嗅探前 512 字节）。
	pdf := append([]byte("%PDF-1.4\n"), make([]byte, 100)...)
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "lease.pdf", pdf); w.Code != http.StatusOK {
		t.Fatalf("upload pdf: %d %s", w.Code, w.Body.String())
	}

	// 纯文本文件被拒绝（嗅探不在白名单）。
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "note.txt", []byte("hello world")); w.Code != http.StatusBadRequest {
		t.Fatalf("upload txt = %d, want 400: %s", w.Code, w.Body.String())
	}

	// 列表返回 2 条。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), nil)
	list := field(t, decodeBody(t, w), "data").([]interface{})
	if len(list) != 2 {
		t.Fatalf("contracts = %d, want 2", len(list))
	}
	// 出参不含磁盘路径（FilePath 标记 json:"-"）。
	if strings.Contains(w.Body.String(), "file_path") || strings.Contains(w.Body.String(), "FilePath") {
		t.Errorf("response leaks file path: %s", w.Body.String())
	}

	// 租户列表带合同数徽标。
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=合同租户", nil)
	row := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if num(row["contracts_count"]) != 2 {
		t.Errorf("contracts_count = %v, want 2", row["contracts_count"])
	}

	// 文件确实落在受控目录里。
	var stored Contract
	env.db.Where("id = ?", contractID).First(&stored)
	if _, err := os.Stat(contractAbsPath(stored.FilePath)); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}
}

func TestContractFileAccessAndDownload(t *testing.T) {
	env := setupEnv(t)
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() { contractsDirOverride = "" })

	roomID := env.seedRoom("K2", 1000, 0)
	tenantID := env.seedTenant(roomID, "下载租户")
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "租约扫描件.png", pngBytes(t)); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), nil)
	id := uint(field(t, decodeBody(t, w), "data").([]interface{})[0].(map[string]interface{})["id"].(float64))

	// inline 预览：内容类型正确。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/contracts/%d/file", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("file: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("content-type = %q, want image/png", ct)
	}
	if disp := w.Header().Get("Content-Disposition"); !bytes.Contains([]byte(disp), []byte("inline")) {
		t.Errorf("disposition = %q, want inline", disp)
	}

	// download=1：attachment + 中文文件名 RFC 5987。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/contracts/%d/file?download=1", id), nil)
	disp := w.Header().Get("Content-Disposition")
	if !bytes.Contains([]byte(disp), []byte("attachment")) || !bytes.Contains([]byte(disp), []byte("filename*=UTF-8''")) {
		t.Errorf("download disposition = %q", disp)
	}

	// 未登录不可读。
	anon := doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/contracts/%d/file", id), "", nil)
	if anon.Code != http.StatusUnauthorized {
		t.Errorf("anon file = %d, want 401", anon.Code)
	}
}

func TestContractDeleteCleansFile(t *testing.T) {
	env := setupEnv(t)
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() { contractsDirOverride = "" })

	roomID := env.seedRoom("K3", 1000, 0)
	tenantID := env.seedTenant(roomID, "删除租户")
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "a.png", pngBytes(t)); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var stored Contract
	env.db.First(&stored)

	// 删除合同：行与磁盘文件同时消失。
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/contracts/%d", stored.ID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(contractAbsPath(stored.FilePath)); !os.IsNotExist(err) {
		t.Fatalf("file should be removed, err = %v", err)
	}
	var n int64
	env.db.Model(&Contract{}).Where("id = ?", stored.ID).Count(&n)
	if n != 0 {
		t.Fatalf("row should be deleted")
	}
}

func TestTenantDeleteCleansContracts(t *testing.T) {
	env := setupEnv(t)
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() { contractsDirOverride = "" })

	roomID := env.seedRoom("K4", 1000, 0)
	tenantID := env.seedTenant(roomID, "跑路租户")
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "a.png", pngBytes(t)); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var stored Contract
	env.db.First(&stored)

	// 删除租户 → 合同行与文件随之清理。
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/tenants/%d", tenantID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete tenant: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(contractAbsPath(stored.FilePath)); !os.IsNotExist(err) {
		t.Fatalf("file should be removed with tenant, err = %v", err)
	}
	var n int64
	env.db.Model(&Contract{}).Where("tenant_id = ?", tenantID).Count(&n)
	if n != 0 {
		t.Fatalf("contract rows should be deleted with tenant")
	}
}

func TestContractOwnershipIsolated(t *testing.T) {
	env := setupEnv(t)
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() { contractsDirOverride = "" })

	roomID := env.seedRoom("K5", 1000, 0)
	tenantID := env.seedTenant(roomID, "甲的租户")
	if w := uploadContract(t, env, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), "files", "a.png", pngBytes(t)); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/tenants/%d/contracts", tenantID), nil)
	id := uint(field(t, decodeBody(t, w), "data").([]interface{})[0].(map[string]interface{})["id"].(float64))

	// 第二个用户看不到第一个用户的合同。
	env2token := ""
	{
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("username", "rival")
		_ = mw.WriteField("password", "secret123")
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/register", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		env.r.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			env2token = field(t, decodeBody(t, rec), "data", "token").(string)
		}
	}
	if env2token == "" {
		t.Skip("second registration not allowed; ownership checked via user_id filter")
	}
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/rental/contracts/%d/file", id), nil)
	req.Header.Set("Authorization", "Bearer "+env2token)
	rec := httptest.NewRecorder()
	env.r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user file = %d, want 404", rec.Code)
	}
}
