// room_move_test.go — 房源跨台账移动：整簇数据（含文件）跟着走、权限与目标台账的限制、
// 失败不留下半迁移状态。
package rental

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// uploadAttachmentAs 以指定 token 上传一份租户资料附件（内置的 uploadAttachment
// 固定用 admin token，而这里的数据在共同台账里）。
func uploadAttachmentAs(t *testing.T, env *testEnv, token, path, fileName string, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("files", fileName)
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
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

// moveFixture 一次性把「一间住了人的房 + 该有的业务数据」建出来，返回房间与租户 id。
// 用真实接口建数据：这样账单、抄表沉淀、附件落盘都是真链路。
func moveFixture(t *testing.T, env *testEnv, token string, ledgerID uint, roomNo string) (uint, uint) {
	t.Helper()
	roomID := env.seedRoomDirect(ledgerID, roomNo)
	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenants?ledger_id=%d", ledgerID), token,
		map[string]interface{}{
			"room_id": roomID, "name": "迁移租户", "phone": "13800001111",
			"members": []map[string]interface{}{{"name": "迁移成员", "relation": "配偶"}},
		})
	if w.Code != http.StatusOK {
		t.Fatalf("seed tenant: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(num(field(t, decodeBody(t, w), "data", "id")))

	// 抄表记录 + 账单（账单会沉淀抄表记录、并把租户带进去）
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/meter-records?ledger_id=%d", ledgerID), token,
		map[string]interface{}{"room_id": roomID, "period": "2026-09", "water": 100, "elec": 200}); w.Code != http.StatusOK {
		t.Fatalf("seed meter record: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/bills?ledger_id=%d", ledgerID), token,
		map[string]interface{}{"room_id": roomID, "period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("seed bill: %d %s", w.Code, w.Body.String())
	}
	// 收款：直接落一条流水（金额随账单算，走接口容易受账单金额影响；
	// 这里要验的是归属迁移，不是收款链路）
	var bill Bill
	env.db.Where("room_id = ? AND period = ?", roomID, "2026-09").First(&bill)
	if bill.ID == 0 {
		t.Fatal("bill not created")
	}
	if err := env.db.Create(&Payment{
		LedgerID: ledgerID, BillID: bill.ID, Amount: 50, PaidAt: time.Now(), Items: "[]",
	}).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	// 附件（图片，真落盘）+ 房间照片 + 抄表照片
	if w := uploadAttachmentAs(t, env, token,
		fmt.Sprintf("/api/rental/tenants/%d/attachments?ledger_id=%d", tenantID, ledgerID),
		"身份证.png", pngBytes(t)); w.Code != http.StatusOK {
		t.Fatalf("seed attachment: %d %s", w.Code, w.Body.String())
	}
	if w := uploadRoomPhotos(t, env, token,
		fmt.Sprintf("/api/rental/rooms/%d/photos?ledger_id=%d", roomID, ledgerID),
		map[string][]byte{"客厅.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("seed room photo: %d %s", w.Code, w.Body.String())
	}
	if w := uploadMeterPhotos(t, env, token,
		fmt.Sprintf("/api/rental/rooms/%d/meter-photos?ledger_id=%d", roomID, ledgerID),
		"2026-09", "water", map[string][]byte{"水表.png": pngBytes(t)}); w.Code != http.StatusOK {
		t.Fatalf("seed meter photo: %d %s", w.Code, w.Body.String())
	}
	return roomID, tenantID
}

func TestRoomMoveCarriesWholeCluster(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "move-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "搬迁A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "搬迁B台账")
	roomID, tenantID := moveFixture(t, env, owner.token, ledgerA, "MV1")

	// 迁移前的文件都在 A 目录下
	var att TenantAttachment
	env.db.Where("tenant_id = ?", tenantID).First(&att)
	var roomPhoto RoomPhoto
	env.db.Where("room_id = ?", roomID).First(&roomPhoto)
	var meterPhoto MeterPhoto
	env.db.Where("room_id = ?", roomID).First(&meterPhoto)
	prefixA := fmt.Sprintf("ledger-%d/", ledgerA)
	for _, rel := range []string{att.FilePath, roomPhoto.FilePath, meterPhoto.FilePath} {
		if len(rel) < len(prefixA) || rel[:len(prefixA)] != prefixA {
			t.Fatalf("迁移前的路径应带 A 前缀：%s", rel)
		}
		if !fileExists(attachmentAbsPath(rel)) {
			t.Fatalf("迁移前的文件不存在：%s", rel)
		}
	}

	// 租户若已绑定登录账号：搬家后门户仍要能找到他（账号关联与台账无关，
	// 门户可见范围由档案自己推导——这条链路不能被搬家弄断）
	linkedID, _ := mkUser(t, env, "move-tenant-account", UserStatusActive)
	if err := env.db.Model(&Tenant{}).Where("id = ?", tenantID).
		Update("account_user_id", linkedID).Error; err != nil {
		t.Fatalf("link tenant account: %v", err)
	}

	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/move?ledger_id=%d", roomID, ledgerA), owner.token,
		map[string]interface{}{"target_ledger_id": ledgerB})
	if w.Code != http.StatusOK {
		t.Fatalf("move: %d %s", w.Code, w.Body.String())
	}
	if t2, ok := portalTenant(env.db, linkedID); !ok || t2.LedgerID != ledgerB || t2.ID != tenantID {
		t.Errorf("搬家后门户应仍能解析到该档案：ok=%v ledger=%v", ok, t2.LedgerID)
	}
	counts := field(t, decodeBody(t, w), "data", "counts").(map[string]interface{})
	if num(counts["tenants"]) != 1 || num(counts["members"]) != 1 || num(counts["bills"]) != 1 ||
		num(counts["payments"]) != 1 || num(counts["attachments"]) != 1 ||
		num(counts["room_photos"]) != 1 || num(counts["meter_photos"]) != 1 {
		t.Errorf("迁移件数不对：%v", counts)
	}

	// 库里：整簇数据的台账都变成 B
	if got := ledgerOf(t, env, &Room{}, roomID); got != ledgerB {
		t.Errorf("room.org_id = %d, want %d", got, ledgerB)
	}
	if got := ledgerOf(t, env, &Tenant{}, tenantID); got != ledgerB {
		t.Errorf("tenant.org_id = %d, want %d", got, ledgerB)
	}
	var member TenantMember
	env.db.Where("tenant_id = ?", tenantID).First(&member)
	if member.LedgerID != ledgerB {
		t.Errorf("member.org_id = %d, want %d", member.LedgerID, ledgerB)
	}
	var rec MeterRecord
	env.db.Where("room_id = ?", roomID).First(&rec)
	if rec.LedgerID != ledgerB {
		t.Errorf("meter_record.org_id = %d, want %d", rec.LedgerID, ledgerB)
	}
	var bill Bill
	env.db.Where("room_id = ?", roomID).First(&bill)
	if bill.LedgerID != ledgerB {
		t.Errorf("bill.org_id = %d, want %d", bill.LedgerID, ledgerB)
	}
	var payment Payment
	env.db.Where("bill_id = ?", bill.ID).First(&payment)
	if payment.LedgerID != ledgerB {
		t.Errorf("payment.org_id = %d, want %d", payment.LedgerID, ledgerB)
	}

	// 盘上：文件挪到 B 目录，老路径不再有文件；库里的相对路径同步改了
	prefixB := fmt.Sprintf("ledger-%d/", ledgerB)
	var att2 TenantAttachment
	env.db.First(&att2, att.ID)
	var roomPhoto2 RoomPhoto
	env.db.First(&roomPhoto2, roomPhoto.ID)
	var meterPhoto2 MeterPhoto
	env.db.First(&meterPhoto2, meterPhoto.ID)
	for _, rel := range []string{att2.FilePath, roomPhoto2.FilePath, meterPhoto2.FilePath} {
		if len(rel) < len(prefixB) || rel[:len(prefixB)] != prefixB {
			t.Fatalf("迁移后路径应带 B 前缀：%s", rel)
		}
		if !fileExists(attachmentAbsPath(rel)) {
			t.Fatalf("迁移后的文件不存在：%s", rel)
		}
	}
	if fileExists(attachmentAbsPath(att.FilePath)) {
		t.Errorf("A 目录下的老文件应已被搬走：%s", att.FilePath)
	}
	// 附件仍可读（前缀校验通过）
	if w := doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/attachments/%d/file?ledger_id=%d", att.ID, ledgerB), owner.token, nil); w.Code != http.StatusOK {
		t.Errorf("迁移后读附件 = %d, want 200", w.Code)
	}

	// 可见性：A 里查不到这间房，B 里能查到
	listA, listB := []interface{}{}, []interface{}{}
	listA = env.items(decodeBody(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/rooms?ledger_id=%d&keyword=MV1", ledgerA), owner.token, nil)))
	listB = env.items(decodeBody(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/rooms?ledger_id=%d&keyword=MV1", ledgerB), owner.token, nil)))
	if len(listA) != 0 || len(listB) != 1 {
		t.Errorf("迁移后可见性不对：A=%d B=%d", len(listA), len(listB))
	}
	// 账单也跟着走：A 里没有该房账单，B 里有
	var billsInA, billsInB int64
	env.db.Model(&Bill{}).Where("room_id = ? AND org_id = ?", roomID, ledgerA).Count(&billsInA)
	env.db.Model(&Bill{}).Where("room_id = ? AND org_id = ?", roomID, ledgerB).Count(&billsInB)
	if billsInA != 0 || billsInB != 1 {
		t.Errorf("账单归属不对：A=%d B=%d", billsInA, billsInB)
	}
}

// TestRoomMovePermissions 移动的权限口径：旧台账要完全档、目标台账要操作档，
// 目标必须是共同台账。
func TestRoomMovePermissions(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "move-perm-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "权限A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "权限B台账")
	roomID := env.seedRoomDirect(ledgerA, "MV2")

	move := func(token string, target uint) int {
		w := doJSON(env.r, http.MethodPost,
			fmt.Sprintf("/api/rental/rooms/%d/move?ledger_id=%d", roomID, ledgerA), token,
			map[string]interface{}{"target_ledger_id": target})
		return w.Code
	}

	// 目标必须是共同台账：给自己（owner 的个人台账）搬 → 400
	if code := move(owner.token, owner.ledgerID); code != http.StatusBadRequest {
		t.Errorf("搬到个人台账 = %d, want 400", code)
	}
	// 目标台账不存在 → 400
	if code := move(owner.token, 99999); code != http.StatusBadRequest {
		t.Errorf("目标不存在 = %d, want 400", code)
	}
	// 同一个台账 → 400
	if code := move(owner.token, ledgerA); code != http.StatusBadRequest {
		t.Errorf("搬到原台账 = %d, want 400", code)
	}

	// 只有 A 的编辑档：移出需要完全档 → 403
	editorID, editorTok := mkUser(t, env, "move-editor", UserStatusActive)
	addActiveMember(t, env, ledgerA, editorID, ModulePerms{ModuleRooms: PermEdit})
	if code := move(editorTok, ledgerB); code != http.StatusForbidden {
		t.Errorf("编辑档移出 = %d, want 403", code)
	}
	// 有 A 的完全档、但 B 只有查看档 → 403
	viewOnlyID, viewOnlyTok := mkUser(t, env, "move-view-only", UserStatusActive)
	addActiveMember(t, env, ledgerA, viewOnlyID, ModulePerms{ModuleRooms: PermFull})
	addActiveMember(t, env, ledgerB, viewOnlyID, ModulePerms{ModuleRooms: PermView})
	if code := move(viewOnlyTok, ledgerB); code != http.StatusForbidden {
		t.Errorf("目标只有查看档 = %d, want 403", code)
	}
	// 不是 B 的成员 → 403
	outsiderID, outsiderTok := mkUser(t, env, "move-outsider", UserStatusActive)
	addActiveMember(t, env, ledgerA, outsiderID, ModulePerms{ModuleRooms: PermFull})
	if code := move(outsiderTok, ledgerB); code != http.StatusForbidden {
		t.Errorf("非目标台账成员 = %d, want 403", code)
	}
	// 看不到的房源（别的台账）→ 404
	var bRoom uint = env.seedRoomDirect(ledgerB, "MV3")
	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/rooms/%d/move?ledger_id=%d", bRoom, ledgerA), owner.token,
		map[string]interface{}{"target_ledger_id": ledgerB})
	if w.Code != http.StatusNotFound {
		t.Errorf("移到看不见的房源 = %d, want 404", w.Code)
	}
	// 两边都够：A 完全 + B 操作 → 200
	editorFullID, editorFullTok := mkUser(t, env, "move-full-both", UserStatusActive)
	addActiveMember(t, env, ledgerA, editorFullID, ModulePerms{ModuleRooms: PermFull})
	addActiveMember(t, env, ledgerB, editorFullID, ModulePerms{ModuleRooms: PermEdit})
	if code := move(editorFullTok, ledgerB); code != http.StatusOK {
		t.Errorf("A 完全 + B 操作 = %d, want 200", code)
	}
	if got := ledgerOf(t, env, &Room{}, roomID); got != ledgerB {
		t.Errorf("迁移后 room.org_id = %d, want %d", got, ledgerB)
	}
}

func ledgerOf(t *testing.T, env *testEnv, model interface{}, id uint) uint {
	t.Helper()
	switch model.(type) {
	case *Room:
		var r Room
		if err := env.db.First(&r, id).Error; err != nil {
			t.Fatalf("load room: %v", err)
		}
		return r.LedgerID
	case *Tenant:
		var x Tenant
		if err := env.db.First(&x, id).Error; err != nil {
			t.Fatalf("load tenant: %v", err)
		}
		return x.LedgerID
	}
	t.Fatalf("unsupported model %T", model)
	return 0
}
