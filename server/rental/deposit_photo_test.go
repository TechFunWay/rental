// deposit_photo_test.go — 押金单照片（租户附件 label=押金单）的集成测试。
//
// 背景（2026-10-17 粉丝反馈）：押金现在只能录一个数字，押金单实物没有地方留档。
// 实现复用租户附件能力：上传时打「押金单」标签，租户列表补一个押金单张数，
// 附件列表支持按标签过滤，前端据此做专门的押金单照片入口。
package rental

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"testing"
)

// TestDepositPhotoLabelFilterAndCount 押金单照片能按标签取到，
// 租户列表的押金单张数只统计标签为「押金单」的附件。
func TestDepositPhotoLabelFilterAndCount(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("DP01", 1000, 0)
	tenantID := env.seedTenant(roomID, "押金单租户")
	base := fmt.Sprintf("/api/rental/tenants/%d/attachments", tenantID)
	png := pngBytes(t)

	// 一张押金单 + 一张身份证：只有前者算押金单照片。
	if w := uploadAttachmentMulti(t, env, base, attachmentLabelDeposit,
		map[string][]byte{"押金单.jpg": png}); w.Code != http.StatusOK {
		t.Fatalf("上传押金单: %d %s", w.Code, w.Body.String())
	}
	if w := uploadAttachmentMulti(t, env, base, "身份证",
		map[string][]byte{"身份证.jpg": png}); w.Code != http.StatusOK {
		t.Fatalf("上传身份证: %d %s", w.Code, w.Body.String())
	}

	// 不带标签：两张都返回。
	w := env.do(http.MethodGet, base, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("附件列表: %d %s", w.Code, w.Body.String())
	}
	if got := len(field(t, decodeBody(t, w), "data").([]interface{})); got != 2 {
		t.Fatalf("附件总数 = %d, want 2", got)
	}

	// 按标签过滤：只回押金单那一张，且 label 正确。
	w = env.do(http.MethodGet, base+"?label="+url.QueryEscape(attachmentLabelDeposit), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("按标签过滤: %d %s", w.Code, w.Body.String())
	}
	list := field(t, decodeBody(t, w), "data").([]interface{})
	if len(list) != 1 {
		t.Fatalf("押金单附件数 = %d, want 1", len(list))
	}
	if got := list[0].(map[string]interface{})["label"]; got != attachmentLabelDeposit {
		t.Errorf("label = %v, want %s", got, attachmentLabelDeposit)
	}

	// 租户列表补上押金单张数。
	w = env.do(http.MethodGet, "/api/rental/tenants?room_id="+fmt.Sprint(roomID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("租户列表: %d %s", w.Code, w.Body.String())
	}
	rows := env.items(decodeBody(t, w))
	if len(rows) != 1 {
		t.Fatalf("租户数 = %d, want 1", len(rows))
	}
	row := rows[0].(map[string]interface{})
	if got := num(row["deposit_photos_count"]); got != 1 {
		t.Errorf("deposit_photos_count = %v, want 1", got)
	}
	// 资料附件总数仍是 2（押金单是附件的一种，不额外挤掉别的计数）。
	if got := num(row["attachments_count"]); got != 2 {
		t.Errorf("attachments_count = %v, want 2", got)
	}
}

// TestDepositPhotoEmptyCount 没有押金单照片时张数为 0（列表字段恒在）。
func TestDepositPhotoEmptyCount(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("DP02", 1000, 0)
	env.seedTenant(roomID, "无押金单")

	w := env.do(http.MethodGet, "/api/rental/tenants?room_id="+fmt.Sprint(roomID), nil)
	row := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if got := num(row["deposit_photos_count"]); got != 0 {
		t.Errorf("deposit_photos_count = %v, want 0", got)
	}
}
