// meter_photos_test.go — 抄表照片的集成测试：上传/列表/读取/删除、入参白名单、
// 跨台账隔离与删房源清理。与 attachments_test.go（租户资料附件）是两套独立能力。
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

// seedRoomDirect 在指定台账里直接落一行房源（抄表照片与房源照片的用例共用）。
// 为什么不走 API：不带 ?ledger_id 的写操作会落到调用者的**个人台账**，
// 而跨台账用例需要房间真的落在共同台账里；直接用 env.db 建行最直白。
func (e *testEnv) seedRoomDirect(ledgerID uint, roomNo string) uint {
	e.t.Helper()
	room := Room{UserID: e.adminID, LedgerID: ledgerID, RoomNo: roomNo}
	if err := e.db.Create(&room).Error; err != nil {
		e.t.Fatalf("seed room in ledger: %v", err)
	}
	return room.ID
}

// uploadMeterPhotos 以指定 token 上传抄表照片（multipart：period + meter + files）。
// path 需自带 query（如 ?ledger_id=1）。
func uploadMeterPhotos(t *testing.T, env *testEnv, token, path, period, meter string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if period != "" {
		if err := mw.WriteField("period", period); err != nil {
			t.Fatalf("write period: %v", err)
		}
	}
	if meter != "" {
		if err := mw.WriteField("meter", meter); err != nil {
			t.Fatalf("write meter: %v", err)
		}
	}
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

func TestMeterPhotoUploadListDelete(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("P1", 1000, 100)
	png := pngBytes(t)

	// 先录一条抄表记录（照片挂在「房间 + 账期」上，记录本身由抄表动作产生）。
	if w := env.do(http.MethodPost, "/api/rental/meter-records", map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "water": 120, "elec": 180,
	}); w.Code != http.StatusOK {
		t.Fatalf("seed meter record: %d %s", w.Code, w.Body.String())
	}

	// 水表两张 + 电表一张。
	w := uploadMeterPhotos(t, env, env.token, fmt.Sprintf("/api/rental/rooms/%d/meter-photos", roomID),
		"2026-09", "water", map[string][]byte{"水表1.png": png, "水表2.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("upload water: %d %s", w.Code, w.Body.String())
	}
	if got := num(field(t, decodeBody(t, w), "data", "uploaded")); got != 2 {
		t.Fatalf("uploaded = %v, want 2", got)
	}
	w = uploadMeterPhotos(t, env, env.token, fmt.Sprintf("/api/rental/rooms/%d/meter-photos", roomID),
		"2026-09", "elec", map[string][]byte{"电表.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("upload elec: %d %s", w.Code, w.Body.String())
	}

	// 列表：按房间 + 账期取到 3 张，按表种筛选出水表 2 张。
	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/meter-photos?room_id=%d&period=2026-09", roomID), nil)))
	if len(rows) != 3 {
		t.Fatalf("photos = %d, want 3", len(rows))
	}
	rows = env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/meter-photos?room_id=%d&period=2026-09&meter=water", roomID), nil)))
	if len(rows) != 2 {
		t.Fatalf("water photos = %d, want 2", len(rows))
	}
	first := rows[0].(map[string]interface{})
	photoID := uint(num(first["id"]))
	if first["meter"] != "water" || first["period"] != "2026-09" {
		t.Fatalf("photo row = %v", first)
	}

	// 抄表记录列表带上照片数（列表页据此显示入口，不必逐行请求）。
	recs := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/meter-records?room_id=%d&period=2026-09", roomID), nil)))
	if len(recs) != 1 {
		t.Fatalf("meter records = %d, want 1", len(recs))
	}
	if got := num(recs[0].(map[string]interface{})["photo_count"]); got != 3 {
		t.Fatalf("photo_count = %v, want 3", got)
	}

	// 取文件：内容与上传字节一致。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/meter-photos/%d/file", photoID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("fetch file: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("file body mismatch, got %d bytes", w.Body.Len())
	}

	// 删除：库行消失、盘上文件也被清掉。
	var row MeterPhoto
	if err := env.db.First(&row, photoID).Error; err != nil {
		t.Fatalf("load photo row: %v", err)
	}
	abs := attachmentAbsPath(row.FilePath)
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/meter-photos/%d", photoID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&MeterPhoto{}, photoID).Error; err == nil {
		t.Fatal("photo row should be deleted")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("file %s should be removed, err = %v", abs, err)
	}
	rows = env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/meter-photos?room_id=%d&period=2026-09", roomID), nil)))
	if len(rows) != 2 {
		t.Fatalf("photos after delete = %d, want 2", len(rows))
	}
}

func TestMeterPhotoValidation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("P2", 1000, 100)
	path := fmt.Sprintf("/api/rental/rooms/%d/meter-photos", roomID)
	png := pngBytes(t)

	// 账期缺失/非法、表种非法、非图片（PDF）都要被拒。
	if w := uploadMeterPhotos(t, env, env.token, path, "", "water",
		map[string][]byte{"a.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("missing period = %d, want 400", w.Code)
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-9", "water",
		map[string][]byte{"a.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("bad period = %d, want 400", w.Code)
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "oil",
		map[string][]byte{"a.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("bad meter = %d, want 400", w.Code)
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "water",
		map[string][]byte{"合同.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("pdf upload = %d, want 400（抄表照片只收图片）", w.Code)
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "water",
		map[string][]byte{}); w.Code != http.StatusBadRequest {
		t.Errorf("empty upload = %d, want 400", w.Code)
	}

	// 一批里有非图片 → 整批拒绝，不能落一半（校验通过后才开始写盘）。
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "elec",
		map[string][]byte{"好图.png": png, "坏文件.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("batch with pdf = %d, want 400", w.Code)
	}
	var half int64
	env.db.Model(&MeterPhoto{}).Where("period = ? AND meter = ?", "2026-09", "elec").Count(&half)
	if half != 0 {
		t.Errorf("整批被拒时不应留下 %d 张照片（半成功）", half)
	}

	// 每房每账期每表最多 6 张：传满 6 张后第 7 张被拒，别的表种不受影响。
	files := map[string][]byte{}
	for i := 0; i < meterPhotoBatchLimit; i++ {
		files[fmt.Sprintf("水%d.png", i)] = png
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "water", files); w.Code != http.StatusOK {
		t.Fatalf("upload 6: %d %s", w.Code, w.Body.String())
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "water",
		map[string][]byte{"第7张.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("7th photo = %d, want 400", w.Code)
	}
	if w := uploadMeterPhotos(t, env, env.token, path, "2026-09", "gas",
		map[string][]byte{"燃气.png": png}); w.Code != http.StatusOK {
		t.Errorf("gas upload = %d, want 200（各表种独立计数）", w.Code)
	}
}

// TestMeterPhotoLedgerIsolation 抄表照片必须按台账隔离：共同台账里的成员
// 只能看/传自己所属台账的房源照片，写权限也落在房间真正所属的台账上。
func TestMeterPhotoLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "photo-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "照片A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "照片B台账")
	roomA := env.seedRoomDirect(ledgerA, "PA")
	roomB := env.seedRoomDirect(ledgerB, "PB")
	png := pngBytes(t)

	// 所有者往 B 台账传一张，成员（只在 A 有权限）看不到也读不到。
	w := uploadMeterPhotos(t, env, owner.token,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomB, ledgerB),
		"2026-09", "elec", map[string][]byte{"B电表.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("owner upload to B: %d %s", w.Code, w.Body.String())
	}
	uploaded := field(t, decodeBody(t, w), "data", "items").([]interface{})
	bPhotoID := uint(num(uploaded[0].(map[string]interface{})["id"]))

	memberID, memberTok := mkUser(t, env, "photo-member", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleMeters: PermEdit, ModuleRooms: PermView})

	// 成员能往 A 台账的房源传照片。
	if w := uploadMeterPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomA, ledgerA),
		"2026-09", "water", map[string][]byte{"A水表.png": png}); w.Code != http.StatusOK {
		t.Fatalf("member upload to A: %d %s", w.Code, w.Body.String())
	}
	// 直接指名 B 台账：路由层的台账门禁先挡下（成员不是 B 的成员）→ 403。
	if w := uploadMeterPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomB, ledgerB),
		"2026-09", "water", map[string][]byte{"越权.png": png}); w.Code != http.StatusForbidden {
		t.Errorf("member upload with ledger B = %d, want 403", w.Code)
	}
	// 带上自己有权限的 A 台账、却指向 B 的房源：房间不可见 → 404（不泄漏存在性）。
	if w := uploadMeterPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomB, ledgerA),
		"2026-09", "water", map[string][]byte{"越权2.png": png}); w.Code != http.StatusNotFound {
		t.Errorf("member upload to B room = %d, want 404", w.Code)
	}
	// B 的照片文件也读不到。
	if w := env.do(http.MethodGet, fmt.Sprintf("/api/rental/meter-photos/%d/file", bPhotoID), nil); w.Code != http.StatusNotFound {
		t.Errorf("member read B photo = %d, want 404", w.Code)
	}
	// 列表：成员在当前台账下只能看到 A 的照片。
	rows := env.items(decodeBody(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/meter-photos?ledger_id=%d", ledgerA), memberTok, nil)))
	if len(rows) != 1 {
		t.Errorf("member ledger A photos = %d, want 1", len(rows))
	}

	// 只有账单权限的成员不能传抄表照片（照片归抄表模块）。
	billsID, billsTok := mkUser(t, env, "photo-bills", UserStatusActive)
	addActiveMember(t, env, ledgerA, billsID, ModulePerms{ModuleBills: PermEdit})
	if w := uploadMeterPhotos(t, env, billsTok,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomA, ledgerA),
		"2026-09", "water", map[string][]byte{"无权限.png": png}); w.Code != http.StatusForbidden {
		t.Errorf("bills-only member upload = %d, want 403", w.Code)
	}
}

// TestMeterPhotoRoomDeleteCleanup 删房源时照片行与文件一起清理。
func TestMeterPhotoRoomDeleteCleanup(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("P3", 1000, 100)
	if w := uploadMeterPhotos(t, env, env.token, fmt.Sprintf("/api/rental/rooms/%d/meter-photos", roomID),
		"2026-09", "water", map[string][]byte{"水表.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var row MeterPhoto
	if err := env.db.Where("room_id = ?", roomID).First(&row).Error; err != nil {
		t.Fatalf("load photo: %v", err)
	}
	abs := attachmentAbsPath(row.FilePath)

	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete room: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&MeterPhoto{}).Where("room_id = ?", roomID).Count(&n)
	if n != 0 {
		t.Errorf("photos after room delete = %d, want 0", n)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("file %s should be removed, err = %v", abs, err)
	}
}
