// room_photos_test.go — 房源照片的集成测试：上传/列表/取文件/删除、入参白名单与
// 张数上限、跨台账隔离、删房源清理，以及房源列表上的照片数。
package rental

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// uploadRoomPhotos 以指定 token 上传房源照片（multipart：files）。
func uploadRoomPhotos(t *testing.T, env *testEnv, token, path string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for name, content := range files {
		part, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write part: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

func TestRoomPhotoUploadListDelete(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R1", 1000, 100)
	png := pngBytes(t)
	path := fmt.Sprintf("/api/rental/rooms/%d/photos", roomID)

	// 两张一次传：一室一厅的常规留档。
	w := uploadRoomPhotos(t, env, env.token, path, map[string][]byte{"客厅.png": png, "门牌.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if got := num(field(t, decodeBody(t, w), "data", "uploaded")); got != 2 {
		t.Fatalf("uploaded = %v, want 2", got)
	}

	// 列表：按房间取到 2 张；房源列表上也带 photo_count。
	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-photos?room_id=%d", roomID), nil)))
	if len(rows) != 2 {
		t.Fatalf("photos = %d, want 2", len(rows))
	}
	photoID := uint(num(rows[0].(map[string]interface{})["id"]))
	rooms := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/rooms?keyword=R1"), nil)))
	if len(rooms) != 1 {
		t.Fatalf("rooms = %d, want 1", len(rooms))
	}
	if got := num(rooms[0].(map[string]interface{})["photo_count"]); got != 2 {
		t.Fatalf("room photo_count = %v, want 2", got)
	}

	// 取文件：字节与上传一致。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/room-photos/%d/file", photoID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("fetch file: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("file body mismatch, got %d bytes", w.Body.Len())
	}

	// 删除：库行与盘上文件一起清掉。
	var row RoomPhoto
	if err := env.db.First(&row, photoID).Error; err != nil {
		t.Fatalf("load photo row: %v", err)
	}
	abs := attachmentAbsPath(row.FilePath)
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/room-photos/%d", photoID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&RoomPhoto{}, photoID).Error; err == nil {
		t.Fatal("photo row should be deleted")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("file %s should be removed, err = %v", abs, err)
	}
}

func TestRoomPhotoValidation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R2", 1000, 100)
	path := fmt.Sprintf("/api/rental/rooms/%d/photos", roomID)
	png := pngBytes(t)

	// 空上传、非图片、一批里混了非图片都要被拒；混批时不能落下一半。
	if w := uploadRoomPhotos(t, env, env.token, path, map[string][]byte{}); w.Code != http.StatusBadRequest {
		t.Errorf("empty upload = %d, want 400", w.Code)
	}
	if w := uploadRoomPhotos(t, env, env.token, path,
		map[string][]byte{"合同.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("pdf upload = %d, want 400（房源照片只收图片）", w.Code)
	}
	if w := uploadRoomPhotos(t, env, env.token, path,
		map[string][]byte{"好图.png": png, "坏文件.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("batch with pdf = %d, want 400", w.Code)
	}
	var n int64
	env.db.Model(&RoomPhoto{}).Where("room_id = ?", roomID).Count(&n)
	if n != 0 {
		t.Fatalf("整批被拒时不应留下 %d 张照片（半成功）", n)
	}

	// 不存在的房源。
	if w := uploadRoomPhotos(t, env, env.token, "/api/rental/rooms/99999/photos",
		map[string][]byte{"a.png": png}); w.Code != http.StatusNotFound {
		t.Errorf("unknown room = %d, want 404", w.Code)
	}

	// 每间房最多 30 张：5 批 ×6 张传满，再传第 31 张被拒。
	for batch := 0; batch < roomPhotoMaxPerRoom/roomPhotoBatchLimit; batch++ {
		files := map[string][]byte{}
		for i := 0; i < roomPhotoBatchLimit; i++ {
			files[fmt.Sprintf("p%d-%d.png", batch, i)] = png
		}
		if w := uploadRoomPhotos(t, env, env.token, path, files); w.Code != http.StatusOK {
			t.Fatalf("batch %d: %d %s", batch, w.Code, w.Body.String())
		}
	}
	if w := uploadRoomPhotos(t, env, env.token, path,
		map[string][]byte{"第31张.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("31st photo = %d, want 400", w.Code)
	}
}

// TestRoomPhotoLedgerIsolation 房源照片按台账隔离：成员只能看/传自己所属台账的房源照片，
// 写权限也落在房间真正所属的台账上，且必须持有「房源」模块的权限。
func TestRoomPhotoLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "room-photo-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "房源照片A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "房源照片B台账")
	roomA := env.seedRoomDirect(ledgerA, "RA")
	roomB := env.seedRoomDirect(ledgerB, "RB")
	png := pngBytes(t)

	// 所有者往 B 台账传一张，成员（只在 A）看不到。
	w := uploadRoomPhotos(t, env, owner.token,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomB, ledgerB),
		map[string][]byte{"B房源.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("owner upload to B: %d %s", w.Code, w.Body.String())
	}
	bPhotoID := uint(num(field(t, decodeBody(t, w), "data", "items").([]interface{})[0].(map[string]interface{})["id"]))

	memberID, memberTok := mkUser(t, env, "room-photo-member", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleRooms: PermEdit})

	// 成员能往 A 台账的房源传。
	if w := uploadRoomPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomA, ledgerA),
		map[string][]byte{"A房源.png": png}); w.Code != http.StatusOK {
		t.Fatalf("member upload to A: %d %s", w.Code, w.Body.String())
	}
	// 指名 B 台账：路由层台账门禁先挡下（成员不属于 B）→ 403。
	if w := uploadRoomPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomB, ledgerB),
		map[string][]byte{"越权.png": png}); w.Code != http.StatusForbidden {
		t.Errorf("member upload with ledger B = %d, want 403", w.Code)
	}
	// 带自己有权限的 A 台账、却指向 B 的房源：房间不可见 → 404。
	if w := uploadRoomPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomB, ledgerA),
		map[string][]byte{"越权2.png": png}); w.Code != http.StatusNotFound {
		t.Errorf("member upload to B room = %d, want 404", w.Code)
	}
	// B 的照片文件读不到。
	if w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/room-photos/%d/file", bPhotoID), nil); w.Code != http.StatusNotFound {
		t.Errorf("member read B photo = %d, want 404", w.Code)
	}
	// 列表：成员在当前台账下只看到 A 的照片。
	rows := env.items(decodeBody(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-photos?ledger_id=%d", ledgerA), memberTok, nil)))
	if len(rows) != 1 {
		t.Errorf("member ledger A photos = %d, want 1", len(rows))
	}

	// 只有租户模块权限的成员不能传房源照片（照片归房源模块）。
	tenantID, tenantTok := mkUser(t, env, "room-photo-tenant", UserStatusActive)
	addActiveMember(t, env, ledgerA, tenantID, ModulePerms{ModuleTenants: PermEdit})
	if w := uploadRoomPhotos(t, env, tenantTok,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomA, ledgerA),
		map[string][]byte{"无权限.png": png}); w.Code != http.StatusForbidden {
		t.Errorf("tenants-only member upload = %d, want 403", w.Code)
	}
}

// TestRoomPhotoRoomDeleteCleanup 删房源时房源照片行与文件一起清理。
func TestRoomPhotoRoomDeleteCleanup(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R3", 1000, 100)
	if w := uploadRoomPhotos(t, env, env.token, fmt.Sprintf("/api/rental/rooms/%d/photos", roomID),
		map[string][]byte{"房间.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var row RoomPhoto
	if err := env.db.Where("room_id = ?", roomID).First(&row).Error; err != nil {
		t.Fatalf("load photo: %v", err)
	}
	abs := attachmentAbsPath(row.FilePath)

	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete room: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&RoomPhoto{}).Where("room_id = ?", roomID).Count(&n)
	if n != 0 {
		t.Errorf("photos after room delete = %d, want 0", n)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("file %s should be removed, err = %v", abs, err)
	}
}
