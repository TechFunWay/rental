// room_items_test.go — 房间物品台账的集成测试：增删改查与边界校验、跨台账隔离、
// 物品照片的上传/列表/读取/删除、删物品连带删照片、Builtin（房东提供/租客自带）、
// 以及删房源时不留孤儿记录。
//
// 全部用例走真实 HTTP（httptest），不直接调 handler：本模块的路由是单独
// apps.Register 挂的（不在 setupRoutes 里），只有真跑一遍才能证明
// resolveLedger / denyTenantRole / 模块门禁确实挂上了。
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

	"smallgo/server/auth"
	"smallgo/server/database"
	"smallgo/server/sysconfig"
	"smallgo/server/user"
)

// uploadRoomItemPhotos 以指定 token 上传物品照片（multipart：files）。
// path 需自带 query（如 ?ledger_id=1）。
func uploadRoomItemPhotos(t *testing.T, env *testEnv, token, path string, files map[string][]byte) *httptest.ResponseRecorder {
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

// createRoomItem 用默认 token 在指定房间下建一件物品，返回物品行。
func (e *testEnv) createRoomItem(roomID uint, payload map[string]interface{}) map[string]interface{} {
	e.t.Helper()
	w := e.do(http.MethodPost, fmt.Sprintf("/api/rental/rooms/%d/items", roomID), payload)
	if w.Code != http.StatusOK {
		e.t.Fatalf("create room item: %d %s", w.Code, w.Body.String())
	}
	return field(e.t, decodeBody(e.t, w), "data").(map[string]interface{})
}

// roomItemID 从接口返回的 data 里取物品 id。
func roomItemID(t *testing.T, data map[string]interface{}) uint {
	t.Helper()
	return uint(num(data["id"]))
}

// ---------------------------------------------------------------------------
// 增删改查 + 边界
// ---------------------------------------------------------------------------

// TestRoomItemCRUD：新增 → 列表 → 编辑（含状况改成「缺失」，退租核对场景）→ 删除。
func TestRoomItemCRUD(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R1", 1000, 0)

	// 新增：房东提供的路由器，2 台，完好。
	created := env.createRoomItem(roomID, map[string]interface{}{
		"name": "路由器", "quantity": 2, "condition": "完好",
		"note": "放在客厅电视柜", "builtin": true,
	})
	itemID := roomItemID(t, created)
	if itemID == 0 {
		t.Fatal("created item id must not be 0")
	}
	if got := num(created["quantity"]); got != 2 {
		t.Fatalf("quantity = %v, want 2", got)
	}
	if created["name"] != "路由器" || created["condition"] != "完好" {
		t.Fatalf("created item = %v", created)
	}
	if created["builtin"] != true {
		t.Fatalf("builtin = %v, want true（房东提供）", created["builtin"])
	}
	if got := num(created["room_id"]); got != float64(roomID) {
		t.Fatalf("room_id = %v, want %d", got, roomID)
	}
	if photos, ok := created["photos"].([]interface{}); !ok || len(photos) != 0 {
		t.Fatalf("new item photos = %v, want 空数组", created["photos"])
	}

	// 列表（按房间）：能取到。
	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d", roomID), nil)))
	if len(rows) != 1 {
		t.Fatalf("items = %d, want 1", len(rows))
	}
	if got := num(rows[0].(map[string]interface{})["photo_count"]); got != 0 {
		t.Fatalf("photo_count = %v, want 0", got)
	}

	// 编辑：退租时把状况改成「缺失」并加备注——这是本功能的核心场景。
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/room-items/%d", itemID), map[string]interface{}{
		"name": "路由器", "quantity": 1, "condition": "缺失",
		"note": "退租核对时发现少了一台", "builtin": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update item: %d %s", w.Code, w.Body.String())
	}
	updated := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if updated["condition"] != "缺失" || num(updated["quantity"]) != 1 {
		t.Fatalf("updated item = %v", updated)
	}

	// 删除 → 再删 404。
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/room-items/%d", itemID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete item: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/room-items/%d", itemID), nil); w.Code != http.StatusNotFound {
		t.Fatalf("re-delete item = %d, want 404", w.Code)
	}
	var n int64
	env.db.Model(&RoomItem{}).Where("room_id = ?", roomID).Count(&n)
	if n != 0 {
		t.Fatalf("items after delete = %d, want 0", n)
	}
}

// TestRoomItemValidation：名称必填/超长、数量夹到 1–999、不存在房间 404、
// 每间房最多 60 件。
func TestRoomItemValidation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R2", 1000, 0)
	path := fmt.Sprintf("/api/rental/rooms/%d/items", roomID)

	// 名称空/纯空白 → 400。
	for _, name := range []string{"", "   "} {
		if w := env.do(http.MethodPost, path, map[string]interface{}{"name": name}); w.Code != http.StatusBadRequest {
			t.Errorf("name=%q = %d, want 400", name, w.Code)
		}
	}
	// 名称超过 50 字 → 400。
	long := ""
	for i := 0; i < roomItemNameMax+1; i++ {
		long += "物"
	}
	if w := env.do(http.MethodPost, path, map[string]interface{}{"name": long}); w.Code != http.StatusBadRequest {
		t.Errorf("long name = %d, want 400", w.Code)
	}
	// 正好 50 字 → 通过（边界值本身合法）。
	if w := env.do(http.MethodPost, path, map[string]interface{}{"name": long[:len("物")*roomItemNameMax]}); w.Code != http.StatusOK {
		t.Errorf("50-char name = %d, want 200", w.Code)
	}

	// 数量边界：0/负数按 1 计，超过 999 按 999 计（不整单报错）。
	zero := env.createRoomItem(roomID, map[string]interface{}{"name": "钥匙", "quantity": 0})
	if got := num(zero["quantity"]); got != 1 {
		t.Errorf("quantity 0 → %v, want 1", got)
	}
	neg := env.createRoomItem(roomID, map[string]interface{}{"name": "插线板", "quantity": -3})
	if got := num(neg["quantity"]); got != 1 {
		t.Errorf("quantity -3 → %v, want 1", got)
	}
	huge := env.createRoomItem(roomID, map[string]interface{}{"name": "螺丝", "quantity": 100000})
	if got := num(huge["quantity"]); got != roomItemQtyMax {
		t.Errorf("quantity 100000 → %v, want %d", got, roomItemQtyMax)
	}

	// 状况/备注超长 → 400。
	cond := ""
	for i := 0; i < roomItemConditionMax+1; i++ {
		cond += "状"
	}
	if w := env.do(http.MethodPost, path, map[string]interface{}{"name": "空调", "condition": cond}); w.Code != http.StatusBadRequest {
		t.Errorf("long condition = %d, want 400", w.Code)
	}
	note := ""
	for i := 0; i < roomItemNoteMax+1; i++ {
		note += "备"
	}
	if w := env.do(http.MethodPost, path, map[string]interface{}{"name": "空调", "note": note}); w.Code != http.StatusBadRequest {
		t.Errorf("long note = %d, want 400", w.Code)
	}

	// 不存在的房间 → 404（不是 400：与房源照片同一口径，不暴露房间是否存在）。
	if w := env.do(http.MethodPost, "/api/rental/rooms/99999/items",
		map[string]interface{}{"name": "凭空物品"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown room = %d, want 404", w.Code)
	}
	// 不存在的物品 → 404。
	if w := env.do(http.MethodPut, "/api/rental/room-items/99999",
		map[string]interface{}{"name": "无主物品"}); w.Code != http.StatusNotFound {
		t.Errorf("unknown item update = %d, want 404", w.Code)
	}

	// 每间房最多 60 件：直接把额度占满（走 DB，避免 60 次请求），再新增被拒。
	var count int64
	env.db.Model(&RoomItem{}).Where("room_id = ?", roomID).Count(&count)
	for i := int(count); i < roomItemMaxPerRoom; i++ {
		env.db.Create(&RoomItem{RoomID: roomID, LedgerID: env.personalLedger,
			Name: fmt.Sprintf("填充物品%d", i), Quantity: 1, Builtin: true})
	}
	if w := env.do(http.MethodPost, path, map[string]interface{}{"name": "第61件"}); w.Code != http.StatusBadRequest {
		t.Errorf("61st item = %d, want 400（每间房最多 %d 件）", w.Code, roomItemMaxPerRoom)
	}
}

// TestRoomItemBuiltin：房东提供（builtin=true）与租客自带（builtin=false）能存能取，
// 且缺省为「房东提供」；重新读列表时值不丢。
func TestRoomItemBuiltin(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R3", 1000, 0)

	// 显式 false（租客自带）：不能被 gorm 的 default 顶成 true。
	own := env.createRoomItem(roomID, map[string]interface{}{
		"name": "租客的机顶盒", "quantity": 1, "builtin": false,
	})
	if own["builtin"] != false {
		t.Fatalf("explicit builtin=false became %v（被 default 顶掉）", own["builtin"])
	}
	// 缺省（不传）→ true：房东提供的才是退租要核对的对象。
	def := env.createRoomItem(roomID, map[string]interface{}{"name": "冰箱", "quantity": 1})
	if def["builtin"] != true {
		t.Fatalf("default builtin = %v, want true", def["builtin"])
	}

	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d", roomID), nil)))
	if len(rows) != 2 {
		t.Fatalf("items = %d, want 2", len(rows))
	}
	got := map[string]bool{}
	for _, r := range rows {
		m := r.(map[string]interface{})
		got[m["name"].(string)] = m["builtin"].(bool)
	}
	if got["租客的机顶盒"] {
		t.Errorf("租客自带读回来变成房东提供：%v", got)
	}
	if !got["冰箱"] {
		t.Errorf("房东提供读回来变成租客自带：%v", got)
	}

	// 编辑时把「租客自带」改成「房东提供」（租客把机顶盒留下了）→ 能存能取。
	itemID := uint(num(own["id"]))
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/room-items/%d", itemID), map[string]interface{}{
		"name": "租客的机顶盒", "quantity": 1, "builtin": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update builtin: %d %s", w.Code, w.Body.String())
	}
	var row RoomItem
	if err := env.db.First(&row, itemID).Error; err != nil {
		t.Fatalf("reload item: %v", err)
	}
	if !row.Builtin {
		t.Fatalf("builtin after update = false, want true")
	}
}

// ---------------------------------------------------------------------------
// 照片：上传 → 列表 → 读取 → 删除，非图片被拒，批量与张数上限
// ---------------------------------------------------------------------------

// TestRoomItemPhotoUploadListFetchDelete：整条照片链路。
func TestRoomItemPhotoUploadListFetchDelete(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R4", 1000, 0)
	itemID := roomItemID(t, env.createRoomItem(roomID, map[string]interface{}{"name": "空调", "quantity": 1}))
	png := pngBytes(t)
	photoPath := fmt.Sprintf("/api/rental/room-items/%d/photos", itemID)

	// 两张一次传：铭牌 + 外观。
	w := uploadRoomItemPhotos(t, env, env.token, photoPath, map[string][]byte{"铭牌.png": png, "外观.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	if got := num(field(t, decodeBody(t, w), "data", "uploaded")); got != 2 {
		t.Fatalf("uploaded = %v, want 2", got)
	}

	// 列表：物品行上带出两张照片。
	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d", roomID), nil)))
	if len(rows) != 1 {
		t.Fatalf("items = %d, want 1", len(rows))
	}
	item := rows[0].(map[string]interface{})
	photos := item["photos"].([]interface{})
	if len(photos) != 2 {
		t.Fatalf("item photos = %d, want 2", len(photos))
	}
	if got := num(item["photo_count"]); got != 2 {
		t.Fatalf("photo_count = %v, want 2", got)
	}
	photoID := uint(num(photos[0].(map[string]interface{})["id"]))

	// 取文件：字节与上传一致。
	w = env.do(http.MethodGet, fmt.Sprintf("/api/rental/room-item-photos/%d/file", photoID), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("fetch file: %d %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(w.Body.Bytes(), png) {
		t.Fatalf("file body mismatch, got %d bytes", w.Body.Len())
	}

	// 删除一张：库行与盘上文件一起清掉，另一张还在。
	var row RoomItemPhoto
	if err := env.db.First(&row, photoID).Error; err != nil {
		t.Fatalf("load photo row: %v", err)
	}
	abs := attachmentAbsPath(row.FilePath)
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/room-item-photos/%d", photoID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete photo: %d %s", w.Code, w.Body.String())
	}
	if err := env.db.First(&RoomItemPhoto{}, photoID).Error; err == nil {
		t.Fatal("photo row should be deleted")
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Fatalf("file %s should be removed, err = %v", abs, err)
	}
	var left int64
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", itemID).Count(&left)
	if left != 1 {
		t.Fatalf("photos left = %d, want 1", left)
	}
}

// TestRoomItemPhotoValidation：空上传、非图片、混批不半成功、单批 6 张、
// 每物品最多 10 张。
func TestRoomItemPhotoValidation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R5", 1000, 0)
	itemID := roomItemID(t, env.createRoomItem(roomID, map[string]interface{}{"name": "洗衣机", "quantity": 1}))
	photoPath := fmt.Sprintf("/api/rental/room-items/%d/photos", itemID)
	png := pngBytes(t)

	if w := uploadRoomItemPhotos(t, env, env.token, photoPath, map[string][]byte{}); w.Code != http.StatusBadRequest {
		t.Errorf("empty upload = %d, want 400", w.Code)
	}
	// 非图片（PDF）被拒。
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath,
		map[string][]byte{"说明书.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("pdf upload = %d, want 400（物品照片只收图片）", w.Code)
	}
	// 一批里混了非图片 → 整批拒，且不能落下前一张（半成功）。
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath,
		map[string][]byte{"好图.png": png, "坏文件.pdf": pdfBytes()}); w.Code != http.StatusBadRequest {
		t.Errorf("batch with pdf = %d, want 400", w.Code)
	}
	var n int64
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", itemID).Count(&n)
	if n != 0 {
		t.Fatalf("整批被拒时不应留下 %d 张照片（半成功）", n)
	}

	// 单次最多 6 张。
	seven := map[string][]byte{}
	for i := 0; i < roomItemPhotoBatchLimit+1; i++ {
		seven[fmt.Sprintf("p%d.png", i)] = png
	}
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath, seven); w.Code != http.StatusBadRequest {
		t.Errorf("7-photo batch = %d, want 400", w.Code)
	}

	// 每件物品最多 10 张：前 6 张一批传满，余下 4 张再传一批；第 11 张被拒。
	files := map[string][]byte{}
	for i := 0; i < roomItemPhotoBatchLimit; i++ {
		files[fmt.Sprintf("b0-%d.png", i)] = png
	}
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath, files); w.Code != http.StatusOK {
		t.Fatalf("first batch: %d %s", w.Code, w.Body.String())
	}
	// 已到 6 张：再传刚好补满的 4 张可以通过（上限只管总数，不管批次）。
	fill := map[string][]byte{}
	for i := 0; i < roomItemPhotoMaxPerItem-roomItemPhotoBatchLimit; i++ {
		fill[fmt.Sprintf("fill%d.png", i)] = png
	}
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath, fill); w.Code != http.StatusOK {
		t.Fatalf("fill to %d: %d %s", roomItemPhotoMaxPerItem, w.Code, w.Body.String())
	}
	// 第 11 张：硬上限，拒绝。
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath,
		map[string][]byte{"第11张.png": png}); w.Code != http.StatusBadRequest {
		t.Errorf("11th photo = %d, want 400（每件物品最多 %d 张）", w.Code, roomItemPhotoMaxPerItem)
	}
	// 一次要传 6 张但只剩 0 张额度 → 也是 400（额度不足时整批拒，不是只收下能收的几张）。
	six := map[string][]byte{}
	for i := 0; i < roomItemPhotoBatchLimit; i++ {
		six[fmt.Sprintf("over%d.png", i)] = png
	}
	if w := uploadRoomItemPhotos(t, env, env.token, photoPath, six); w.Code != http.StatusBadRequest {
		t.Errorf("over-limit 6-photo batch = %d, want 400", w.Code)
	}
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", itemID).Count(&n)
	if n != roomItemPhotoMaxPerItem {
		t.Fatalf("photos = %d, want %d", n, roomItemPhotoMaxPerItem)
	}
}

// TestRoomItemDeleteCascadesPhotos：删物品时它的照片记录与盘上文件一起清掉。
func TestRoomItemDeleteCascadesPhotos(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R6", 1000, 0)
	itemID := roomItemID(t, env.createRoomItem(roomID, map[string]interface{}{"name": "热水器", "quantity": 1}))
	// 同房间的第二件物品：它的照片不该被误删（这是「一类照片一张表、按物品外键清理」
	// 要证明的东西）。
	otherID := roomItemID(t, env.createRoomItem(roomID, map[string]interface{}{"name": "花洒", "quantity": 1}))

	for _, id := range []uint{itemID, otherID} {
		if w := uploadRoomItemPhotos(t, env, env.token,
			fmt.Sprintf("/api/rental/room-items/%d/photos", id),
			map[string][]byte{"照.png": pngBytes(t)}); w.Code != http.StatusOK {
			t.Fatalf("upload for item %d: %d %s", id, w.Code, w.Body.String())
		}
	}
	var rows []RoomItemPhoto
	env.db.Where("room_item_id = ?", itemID).Find(&rows)
	if len(rows) != 1 {
		t.Fatalf("item photos = %d, want 1", len(rows))
	}
	abs := attachmentAbsPath(rows[0].FilePath)
	var otherRows []RoomItemPhoto
	env.db.Where("room_item_id = ?", otherID).Find(&otherRows)
	if len(otherRows) != 1 {
		t.Fatalf("other item photos = %d, want 1", len(otherRows))
	}
	otherAbs := attachmentAbsPath(otherRows[0].FilePath)

	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/room-items/%d", itemID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete item: %d %s", w.Code, w.Body.String())
	}
	var left int64
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", itemID).Count(&left)
	if left != 0 {
		t.Errorf("photos after item delete = %d, want 0", left)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("file %s should be removed, err = %v", abs, err)
	}
	// 另一件物品的照片完好。
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", otherID).Count(&left)
	if left != 1 {
		t.Errorf("other item photos = %d, want 1（不该误删）", left)
	}
	if _, err := os.Stat(otherAbs); err != nil {
		t.Errorf("other item's file %s should still exist, err = %v", otherAbs, err)
	}
}

// ---------------------------------------------------------------------------
// 台账隔离
// ---------------------------------------------------------------------------

// TestRoomItemLedgerIsolation：A 台账的成员读不到也改不了 B 台账的物品与照片，
// 越权一律 404；只有租户模块权限的成员碰不了物品台账。
func TestRoomItemLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "room-item-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "物品台账A")
	ledgerB := createSharedLedger(t, env, owner.token, "物品台账B")
	roomA := env.seedRoomDirect(ledgerA, "IA")
	roomB := env.seedRoomDirect(ledgerB, "IB")

	// 所有者往 B 台账建物品并传一张照片。
	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/items?ledger_id=%d", roomB, ledgerB), owner.token,
		map[string]interface{}{"name": "B台账的路由器", "quantity": 1, "builtin": true})
	if w.Code != http.StatusOK {
		t.Fatalf("owner create in B: %d %s", w.Code, w.Body.String())
	}
	bItemID := roomItemID(t, field(t, decodeBody(t, w), "data").(map[string]interface{}))
	if w := uploadRoomItemPhotos(t, env, owner.token,
		fmt.Sprintf("/api/rental/room-items/%d/photos?ledger_id=%d", bItemID, ledgerB),
		map[string][]byte{"B物品.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("owner upload in B: %d %s", w.Code, w.Body.String())
	}
	var bPhoto RoomItemPhoto
	if err := env.db.Where("room_item_id = ?", bItemID).First(&bPhoto).Error; err != nil {
		t.Fatalf("load B photo: %v", err)
	}

	// 成员只在 A 台账、有房源操作档。
	memberID, memberTok := mkUser(t, env, "room-item-member", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleRooms: PermEdit})

	// 成员能在 A 台账建物品、传照片。
	w = doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/items?ledger_id=%d", roomA, ledgerA), memberTok,
		map[string]interface{}{"name": "A台账的洗衣机", "quantity": 1})
	if w.Code != http.StatusOK {
		t.Fatalf("member create in A: %d %s", w.Code, w.Body.String())
	}
	aItemID := roomItemID(t, field(t, decodeBody(t, w), "data").(map[string]interface{}))
	if w := uploadRoomItemPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/room-items/%d/photos?ledger_id=%d", aItemID, ledgerA),
		map[string][]byte{"A物品.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("member upload in A: %d %s", w.Code, w.Body.String())
	}

	// 指名 B 台账：路由层台账门禁先挡下（成员不属于 B）→ 403。
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d&ledger_id=%d", roomB, ledgerB), memberTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("member list with ledger B = %d, want 403", w.Code)
	}
	// 带自己有权限的 A 台账、却指向 B 的房间建房 → 房间不可见 → 404。
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/items?ledger_id=%d", roomB, ledgerA), memberTok,
		map[string]interface{}{"name": "越权物品"}); w.Code != http.StatusNotFound {
		t.Errorf("member create in B room = %d, want 404", w.Code)
	}
	// 按 ID 直取/改/删 B 的物品：越权一律 404（不暴露它存在）。
	if w := doJSON(env.r, http.MethodPut,
		fmt.Sprintf("/api/rental/room-items/%d?ledger_id=%d", bItemID, ledgerA), memberTok,
		map[string]interface{}{"name": "改别人的"}); w.Code != http.StatusNotFound {
		t.Errorf("member update B item = %d, want 404", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/room-items/%d?ledger_id=%d", bItemID, ledgerA), memberTok, nil); w.Code != http.StatusNotFound {
		t.Errorf("member delete B item = %d, want 404", w.Code)
	}
	// B 的照片文件读不到。
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-item-photos/%d/file?ledger_id=%d", bPhoto.ID, ledgerA), memberTok, nil); w.Code != http.StatusNotFound {
		t.Errorf("member read B item photo = %d, want 404", w.Code)
	}
	// 也不许删 B 的照片。
	if w := doJSON(env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/room-item-photos/%d?ledger_id=%d", bPhoto.ID, ledgerA), memberTok, nil); w.Code != http.StatusNotFound {
		t.Errorf("member delete B item photo = %d, want 404", w.Code)
	}
	// 也不能往 B 的物品上传照片。
	if w := uploadRoomItemPhotos(t, env, memberTok,
		fmt.Sprintf("/api/rental/room-items/%d/photos?ledger_id=%d", bItemID, ledgerA),
		map[string][]byte{"越权.png": pngBytes(t)}); w.Code != http.StatusNotFound {
		t.Errorf("member upload to B item = %d, want 404", w.Code)
	}

	// 列表：成员在 A 台账下只看到 A 的物品（1 件）。
	rows := itemsOf(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?ledger_id=%d", ledgerA), memberTok, nil))
	if len(rows) != 1 {
		t.Errorf("member ledger A items = %d, want 1", len(rows))
	}
	// 不带 ledger_id：范围收窄到「房源模块至少 view」的台账，B 不在其中。
	rows = itemsOf(t, doJSON(env.r, http.MethodGet, "/api/rental/room-items", memberTok, nil))
	if len(rows) != 1 {
		t.Errorf("member no-param items = %d, want 1（B 不在可见范围）", len(rows))
	}

	// 只有租户模块权限的成员碰不了物品台账（物品归房源模块）。
	tenantID, tenantTok := mkUser(t, env, "room-item-tenants-only", UserStatusActive)
	addActiveMember(t, env, ledgerA, tenantID, ModulePerms{ModuleTenants: PermEdit})
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?ledger_id=%d", ledgerA), tenantTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("tenants-only member list = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/items?ledger_id=%d", roomA, ledgerA), tenantTok,
		map[string]interface{}{"name": "无权限物品"}); w.Code != http.StatusForbidden {
		t.Errorf("tenants-only member create = %d, want 403", w.Code)
	}

	// 只读（view）成员：能看，不能建/改/删——requireLedgerWrite 按对象所属台账复核。
	viewerID, viewerTok := mkUser(t, env, "room-item-viewer", UserStatusActive)
	addActiveMember(t, env, ledgerA, viewerID, ModulePerms{ModuleRooms: PermView})
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d&ledger_id=%d", roomA, ledgerA), viewerTok, nil); w.Code != http.StatusOK {
		t.Errorf("viewer list = %d, want 200", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/items?ledger_id=%d", roomA, ledgerA), viewerTok,
		map[string]interface{}{"name": "只读不能建"}); w.Code != http.StatusForbidden {
		t.Errorf("viewer create = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/room-items/%d?ledger_id=%d", aItemID, ledgerA), viewerTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("viewer delete = %d, want 403", w.Code)
	}
}

// TestRoomItemTenantRoleDenied：租户账号对物品台账一律 403（业务路由组的默认拒绝）。
func TestRoomItemTenantRoleDenied(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("R7", 1000, 0)

	// 直接落一个 role=tenant 的账号并签 token：mkdir 的 mkUser 固定 role=user，
	// 而租户门禁读的是 token 里的 role 声明，所以这里自己建、自己签。
	tenant := database.User{Username: "room-item-tenant", Password: "x", Role: user.RoleTenant, Status: UserStatusActive}
	if err := env.db.Create(&tenant).Error; err != nil {
		t.Fatalf("create tenant user: %v", err)
	}
	if _, err := ensurePersonalLedger(env.db, tenant.ID); err != nil {
		t.Fatalf("ensure personal ledger: %v", err)
	}
	secret, err := sysconfig.GetConfig(env.db, "jwt_secret", 0)
	if err != nil || secret == "" {
		t.Fatalf("jwt secret: %v", err)
	}
	tenantTok, err := auth.GenerateToken(tenant.ID, tenant.Username, tenant.Role, tenant.AuthVersion, secret)
	if err != nil {
		t.Fatalf("tenant token: %v", err)
	}

	if w := doJSON(env.r, http.MethodGet, "/api/rental/room-items", tenantTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("tenant list items = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/rooms/%d/items", roomID), tenantTok,
		map[string]interface{}{"name": "租户不能建"}); w.Code != http.StatusForbidden {
		t.Errorf("tenant create item = %d, want 403", w.Code)
	}
}

// TestRoomItemSortAndScope：默认排位（新加的排最后）与列表范围
// （不传 room_id 时列本台账全部物品，且只列可见台账的）。
func TestRoomItemSortAndScope(t *testing.T) {
	env := setupEnv(t)

	// 同一台账两间房：每间房各记两件，验证按 sort ASC 返回、每间房的排序互不影响。
	roomA := env.seedRoom("SA", 1000, 0)
	roomB := env.seedRoom("SB", 1000, 0)
	for _, name := range []string{"冰箱", "洗衣机", "空调"} {
		env.createRoomItem(roomA, map[string]interface{}{"name": name, "quantity": 1})
	}
	env.createRoomItem(roomB, map[string]interface{}{"name": "B房电视", "quantity": 1})

	namesOf := func(rows []interface{}) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.(map[string]interface{})["name"].(string))
		}
		return out
	}

	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d", roomA), nil)))
	if len(rows) != 3 {
		t.Fatalf("room A items = %d, want 3", len(rows))
	}
	// 录入顺序即返回顺序（sort 递增）。
	if got := namesOf(rows); got[0] != "冰箱" || got[1] != "洗衣机" || got[2] != "空调" {
		t.Fatalf("room A order = %v, want [冰箱 洗衣机 空调]（按录入顺序）", got)
	}
	// 每间房各一套 sort：B 房第一件的 sort 也是 1，不是 4。
	var b RoomItem
	if err := env.db.Where("room_id = ? AND name = ?", roomB, "B房电视").First(&b).Error; err != nil {
		t.Fatalf("load B item: %v", err)
	}
	if b.Sort != 1 {
		t.Fatalf("B item sort = %d, want 1（sort 按房间各自递增）", b.Sort)
	}

	// 显式指定 sort：编辑时能改，列表按新顺序返回。
	lastID := uint(num(rows[2].(map[string]interface{})["id"]))
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/room-items/%d", lastID),
		map[string]interface{}{"name": "空调", "quantity": 1, "sort": 0}); w.Code != http.StatusOK {
		t.Fatalf("reorder: %d %s", w.Code, w.Body.String())
	}
	rows = env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/room-items?room_id=%d", roomA), nil)))
	if got := namesOf(rows); got[0] != "空调" {
		t.Fatalf("after reorder = %v, want 空调 first", got)
	}

	// 不传 room_id：本台账全部 4 件。
	rows = env.items(decodeBody(t, env.do(http.MethodGet, "/api/rental/room-items", nil)))
	if len(rows) != 4 {
		t.Fatalf("all items = %d, want 4", len(rows))
	}

	// 别的台账的物品不在范围里。
	other := newPersonalLedger(t, env, "room-item-sort-other")
	otherRoom := env.seedRoomDirect(other.ledgerID, "SC")
	env.db.Create(&RoomItem{LedgerID: other.ledgerID, RoomID: otherRoom,
		Name: "别人的物品", Quantity: 1, Builtin: true})
	rows = env.items(decodeBody(t, env.do(http.MethodGet, "/api/rental/room-items", nil)))
	if len(rows) != 4 {
		t.Fatalf("all items after other ledger = %d, want 4（别的台账不该进来）", len(rows))
	}
}

// ---------------------------------------------------------------------------
// 源码级：路由与模型的注册口径
// ---------------------------------------------------------------------------

// TestRoomItemModelsRegistered：物品与照片两张表都要进 AutoMigrate
// （模型由本文件的 init 注册，不改 rental.go）。
func TestRoomItemModelsRegistered(t *testing.T) {
	env := setupEnv(t)
	for _, table := range []string{"room_items", "room_item_photos"} {
		if !tableExists(env.db, table) {
			t.Errorf("表 %s 未建（模型没注册进 AutoMigrate）", table)
		}
	}
	// 物品表必须有 room_id / org_id 两列：绑房间 + 台账隔离都靠它们。
	for _, col := range []string{"room_id", "org_id", "builtin"} {
		if !columnExists(env.db, "room_items", col) {
			t.Errorf("room_items 缺列 %s", col)
		}
	}
	// 照片表按物品外键归属，且有 org_id 做台账隔离。
	for _, col := range []string{"room_item_id", "org_id"} {
		if !columnExists(env.db, "room_item_photos", col) {
			t.Errorf("room_item_photos 缺列 %s", col)
		}
	}
}

// TestRoomItemRoutesRegistered：路由确实挂在框架 authGroup 上（未登录 401）。
// 本模块是单独 apps.Register 挂的，只有真发请求才能证明挂上了。
func TestRoomItemRoutesRegistered(t *testing.T) {
	env := setupEnv(t)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/rental/room-items"},
		{http.MethodPost, "/api/rental/rooms/1/items"},
		{http.MethodPut, "/api/rental/room-items/1"},
		{http.MethodDelete, "/api/rental/room-items/1"},
		{http.MethodPost, "/api/rental/room-items/1/photos"},
		{http.MethodGet, "/api/rental/room-item-photos/1/file"},
		{http.MethodDelete, "/api/rental/room-item-photos/1"},
	} {
		w := doJSON(env.r, tc.method, tc.path, "", nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s 匿名 = %d, want 401（路由未挂或未走鉴权）", tc.method, tc.path, w.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// 删房源不留孤儿
// ---------------------------------------------------------------------------

// TestRoomItemRoomDeleteCleanup：删除房源时物品行、照片行与盘上文件一起清掉，
// 且不影响别的房源的同名物品。
func TestRoomItemRoomDeleteCleanup(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	roomID := env.seedRoom("R8", 1000, 0)
	keepRoomID := env.seedRoom("R9", 1000, 0)
	itemID := roomItemID(t, env.createRoomItem(roomID, map[string]interface{}{"name": "电视", "quantity": 1}))
	keepItemID := roomItemID(t, env.createRoomItem(keepRoomID, map[string]interface{}{"name": "电视", "quantity": 1}))

	for _, id := range []uint{itemID, keepItemID} {
		if w := uploadRoomItemPhotos(t, env, env.token,
			fmt.Sprintf("/api/rental/room-items/%d/photos", id),
			map[string][]byte{"电视.png": pngBytes(t)}); w.Code != http.StatusOK {
			t.Fatalf("upload for item %d: %d %s", id, w.Code, w.Body.String())
		}
	}
	var photo RoomItemPhoto
	if err := env.db.Where("room_item_id = ?", itemID).First(&photo).Error; err != nil {
		t.Fatalf("load photo: %v", err)
	}
	abs := attachmentAbsPath(photo.FilePath)

	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete room: %d %s", w.Code, w.Body.String())
	}

	// 物品行、照片行、盘上文件都不留孤儿。
	var items, photos int64
	env.db.Model(&RoomItem{}).Where("room_id = ?", roomID).Count(&items)
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", itemID).Count(&photos)
	if items != 0 {
		t.Errorf("items after room delete = %d, want 0", items)
	}
	if photos != 0 {
		t.Errorf("item photos after room delete = %d, want 0", photos)
	}
	if _, err := os.Stat(abs); !os.IsNotExist(err) {
		t.Errorf("file %s should be removed, err = %v", abs, err)
	}

	// 另一间房的同名物品与照片不受影响。
	env.db.Model(&RoomItem{}).Where("room_id = ?", keepRoomID).Count(&items)
	env.db.Model(&RoomItemPhoto{}).Where("room_item_id = ?", keepItemID).Count(&photos)
	if items != 1 || photos != 1 {
		t.Errorf("keep room items/photos = %d/%d, want 1/1（不该误删）", items, photos)
	}
}
