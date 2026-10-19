// room_applications_test.go — 免登录房源登记（村委/中介场景）的集成测试：
// 公开提交（匿名可用）、链接与台账绑定、审核建房（含选填租户与照片并入）、驳回清理、
// 权限与台账隔离。
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

// roomApplySubmit 以匿名身份向公开登记接口提交一份房源登记（multipart）。
func roomApplySubmit(t *testing.T, env *testEnv, token string, fields map[string]string, photos map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	for name, content := range photos {
		part, err := mw.CreateFormFile("photos", name)
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
	// 不带 Authorization：这条路必须是免登录的
	req := httptest.NewRequest(http.MethodPost, "/api/rental/room-apply/"+token, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

// roomApplyLinkOf 取某台账的房源登记链接（顺手生成 token）。
func roomApplyLinkOf(t *testing.T, env *testEnv, token string, ledgerID uint) string {
	t.Helper()
	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-apply-link?ledger_id=%d", ledgerID), token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("room apply link: %d %s", w.Code, w.Body.String())
	}
	return field(t, decodeBody(t, w), "data", "token").(string)
}

func TestRoomApplicationPublicSubmitAndApprove(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "village-owner")
	ledger := createSharedLedger(t, env, owner.token, "幸福村村委")
	token := roomApplyLinkOf(t, env, owner.token, ledger)

	// 公开页信息：免登录能拿到台账名（村民确认没扫错码）
	w := doJSON(env.r, http.MethodGet, "/api/rental/room-apply/"+token, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("public info: %d %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "ledger_name"); got != "幸福村村委" {
		t.Errorf("ledger_name = %v", got)
	}
	// 无效 token
	if w := doJSON(env.r, http.MethodGet, "/api/rental/room-apply/abcdefghijklmnop", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("invalid token = %d, want 404", w.Code)
	}

	// 匿名提交（带两张照片 + 选填的在租租户）
	png := pngBytes(t)
	w = roomApplySubmit(t, env, token, map[string]string{
		"name": "王村民", "phone": "13800002222",
		"community": "幸福村", "building": "3号院", "room_no": "东厢房",
		"default_rent": "800", "notes": "临街，可做小卖部",
		"tenant_name": "李租客", "tenant_phone": "13900003333", "tenant_id_card": "330102199001011234",
	}, map[string][]byte{"a.png": png, "b.png": png})
	if w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))

	// 必填校验与重复提交
	if w := roomApplySubmit(t, env, token, map[string]string{"name": "王村民", "phone": "138", "room_no": "X"}, nil); w.Code != http.StatusBadRequest {
		t.Errorf("坏手机号 = %d, want 400", w.Code)
	}
	if w := roomApplySubmit(t, env, token, map[string]string{"name": "王村民", "phone": "13800002222", "community": "幸福村", "room_no": "东厢房"}, nil); w.Code != http.StatusBadRequest {
		t.Errorf("重复提交 = %d, want 400", w.Code)
	}

	// 待审队列（房东侧，需要登录）
	w = doJSON(env.r, http.MethodGet, fmt.Sprintf("/api/rental/room-applications?ledger_id=%d", ledger), owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	rows, _ := field(t, decodeBody(t, w), "data").([]interface{})
	if len(rows) != 1 {
		t.Fatalf("待审房源 = %d, want 1", len(rows))
	}
	row := rows[0].(map[string]interface{})
	if row["name"] != "王村民" || row["room_no"] != "东厢房" || num(row["default_rent"]) != 800 {
		t.Errorf("提交内容不对：%v", row)
	}
	if urls, _ := row["photo_urls"].([]interface{}); len(urls) != 2 {
		t.Errorf("照片数 = %d, want 2", len(urls))
	}
	// 照片可读（登录 + 可见台账）
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-applications/%d/photo/0?ledger_id=%d", appID, ledger), owner.token, nil); w.Code != http.StatusOK {
		t.Errorf("读登记照片 = %d, want 200", w.Code)
	}

	// 审核通过：建房源 + 租户 + 照片并入房间照片
	w = doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledger), owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	roomID := uint(num(data["room_id"]))
	if roomID == 0 || num(data["room_photos"]) != 2 {
		t.Fatalf("入库结果不对：%v", data)
	}
	var room Room
	env.db.First(&room, roomID)
	if room.LedgerID != ledger || room.RoomNo != "东厢房" || room.DefaultRent != 800 || room.Community != "幸福村" {
		t.Errorf("房源落库不对：%+v", room)
	}
	// 登记人的姓名电话落进房源自己的房东字段（v0.6.10 起不再只塞备注）
	if room.LandlordName != "王村民" || room.LandlordPhone != "13800002222" {
		t.Errorf("房源的房东信息 = %q/%q, want 王村民/13800002222", room.LandlordName, room.LandlordPhone)
	}
	if !contains(room.Notes, "临街") {
		t.Errorf("登记人备注应保留：%q", room.Notes)
	}
	var tenant Tenant
	env.db.Where("room_id = ?", roomID).First(&tenant)
	if tenant.Name != "李租客" || tenant.Phone != "13900003333" || tenant.LedgerID != ledger || !tenant.Active {
		t.Errorf("随登记转入的租户不对：%+v", tenant)
	}
	var photos []RoomPhoto
	env.db.Where("room_id = ?", roomID).Find(&photos)
	if len(photos) != 2 {
		t.Fatalf("房间照片 = %d, want 2", len(photos))
	}
	prefix := fmt.Sprintf("ledger-%d/", ledger)
	for _, p := range photos {
		if len(p.FilePath) < len(prefix) || p.FilePath[:len(prefix)] != prefix {
			t.Errorf("照片应挪进台账目录：%s", p.FilePath)
		}
		if !fileExists(attachmentAbsPath(p.FilePath)) {
			t.Errorf("照片文件不存在：%s", p.FilePath)
		}
	}
	// 申请行状态与照片字段已移交
	var app RoomApplication
	env.db.First(&app, appID)
	if app.Status != "approved" || app.RoomID != roomID || app.Photos != "[]" {
		t.Errorf("申请行状态不对：%+v", app)
	}
	// 房源列表里能看到（村委给咨询的人推荐）
	list := env.items(decodeBody(t, doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/rooms?ledger_id=%d&keyword=东厢房", ledger), owner.token, nil)))
	if len(list) != 1 {
		t.Errorf("审核后房源应进列表：%d", len(list))
	}
}

func TestRoomApplicationRejectAndIsolation(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	t.Cleanup(func() { attachmentsDirOverride = "" })

	owner := newPersonalLedger(t, env, "village-owner2")
	ledgerA := createSharedLedger(t, env, owner.token, "甲村委")
	ledgerB := createSharedLedger(t, env, owner.token, "乙村委")
	tokenA := roomApplyLinkOf(t, env, owner.token, ledgerA)
	tokenB := roomApplyLinkOf(t, env, owner.token, ledgerB)
	if tokenA == tokenB {
		t.Fatal("不同台账的登记链接不应相同")
	}

	// 甲村的链接提交，落到甲台账
	w := roomApplySubmit(t, env, tokenA, map[string]string{
		"name": "赵村民", "phone": "13800004444", "community": "甲村", "room_no": "1号", "default_rent": "500",
	}, map[string][]byte{"a.png": pngBytes(t)})
	if w.Code != http.StatusOK {
		t.Fatalf("submit A: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))
	var app RoomApplication
	env.db.First(&app, appID)
	if app.LedgerID != ledgerA {
		t.Fatalf("申请归属 = %d, want %d", app.LedgerID, ledgerA)
	}
	photoRel := roomApplyPhotos(app.Photos)[0]

	// 乙台账看不到甲村的登记
	w2 := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/room-applications?ledger_id=%d", ledgerB), owner.token, nil)
	rows, _ := field(t, decodeBody(t, w2), "data").([]interface{})
	if len(rows) != 0 {
		t.Errorf("乙台账不该看到甲村的登记：%d", len(rows))
	}
	// 乙台账也审不了甲村的登记（按不存在处理）
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledgerB), owner.token, nil); w.Code != http.StatusNotFound {
		t.Errorf("跨界审核 = %d, want 404", w.Code)
	}

	// 权限：只有查看档的成员不能生成链接、也不能审核
	viewerID, viewerTok := mkUser(t, env, "village-viewer", UserStatusActive)
	addActiveMember(t, env, ledgerA, viewerID, ModulePerms{ModuleRooms: PermView})
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-apply-link?ledger_id=%d", ledgerA), viewerTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("查看档生成链接 = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledgerA), viewerTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("查看档审核 = %d, want 403", w.Code)
	}

	// 驳回：状态变 rejected，暂存照片被删掉
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/reject?ledger_id=%d", appID, ledgerA), owner.token,
		map[string]interface{}{"note": "电话打不通"}); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	env.db.First(&app, appID)
	if app.Status != "rejected" || app.ReviewNote != "电话打不通" || app.Photos != "[]" {
		t.Errorf("驳回后状态不对：%+v", app)
	}
	if fileExists(attachmentAbsPath(photoRel)) {
		t.Errorf("驳回后暂存照片应被删除：%s", photoRel)
	}
	// 已处理的登记不能再审
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledgerA), owner.token, nil); w.Code != http.StatusBadRequest {
		t.Errorf("重复审核 = %d, want 400", w.Code)
	}

	// 房号重复时审核被拦（改房号可过）：先在该位置建一间房
	if err := env.db.Create(&Room{
		UserID: env.adminID, LedgerID: ledgerA, Community: "甲村", RoomNo: "1号",
	}).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	w = roomApplySubmit(t, env, tokenA, map[string]string{
		"name": "钱村民", "phone": "13800005555", "community": "甲村", "room_no": "2号",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("submit again: %d %s", w.Code, w.Body.String())
	}
	appID2 := uint(num(field(t, decodeBody(t, w), "data", "id")))
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID2, ledgerA), owner.token,
		map[string]interface{}{"room_no": "1号"}); w.Code != http.StatusBadRequest {
		t.Errorf("房号冲突 = %d, want 400", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID2, ledgerA), owner.token,
		map[string]interface{}{"room_no": "2号", "default_rent": 660}); w.Code != http.StatusOK {
		t.Fatalf("改房号后审核: %d %s", w.Code, w.Body.String())
	}
	var room2 Room
	env.db.Where("community = ? AND room_no = ?", "甲村", "2号").First(&room2)
	if room2.ID == 0 || room2.DefaultRent != 660 {
		t.Errorf("审核时改的租金没生效：%+v", room2)
	}
}

// TestRoomApplyLinkIsPublicGuard 公开登记接口必须在公开路由组里（免登录），
// 不能被租户角色的业务门禁套上——这是村委/中介场景能用的前提。
func TestRoomApplyLinkIsPublicGuard(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "village-owner3")
	ledger := createSharedLedger(t, env, owner.token, "丙村委")
	token := roomApplyLinkOf(t, env, owner.token, ledger)

	// 没有任何凭证也要能打开公开页（既有的公开登记回归用例同样守这条）
	w := doJSON(env.r, http.MethodGet, "/api/rental/room-apply/"+token, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("匿名访问公开页 = %d, want 200", w.Code)
	}
	// 房东侧的审核队列则必须登录
	if w := doJSON(env.r, http.MethodGet, "/api/rental/room-applications", "", nil); w.Code == http.StatusOK {
		t.Error("审核队列不应允许匿名访问")
	}
}
