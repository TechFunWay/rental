// landlord_applications_test.go — 房东入驻申请：提交/覆盖、等待审核期的白名单、
// 平台管理员审核（通过 = 账号可用 + 个人台账就绪；驳回 = 账号停用）、权限与角色边界。
package rental

import (
	"fmt"
	"net/http"
	"testing"

	"smallgo/server/database"
)

func applyBody(name, phone string) map[string]interface{} {
	return map[string]interface{}{
		"name": name, "phone": phone, "wechat": "wx_landlord",
		"region": "浙江杭州", "room_scale": "6-20 间", "intro": "有十几间城中村房源",
	}
}

func TestLandlordApplySubmitAndApprove(t *testing.T) {
	env := setupEnv(t)
	// 一个待审核的房东账号（注册后 status=2，能登录但业务接口 403）
	userID, tok := mkUser(t, env, "landlord-pending", UserStatusPending)

	// 提交入驻申请
	w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok, applyBody("陈房东", "13800001111"))
	if w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))
	row := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if row["status"] != "pending" || row["name"] != "陈房东" || row["room_scale"] != "6-20 间" {
		t.Errorf("申请内容不对：%v", row)
	}

	// 入参校验
	if w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok,
		map[string]interface{}{"name": "陈房东", "phone": "abc"}); w.Code != http.StatusBadRequest {
		t.Errorf("坏电话 = %d, want 400", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok,
		map[string]interface{}{"name": "", "phone": "13800001111"}); w.Code != http.StatusBadRequest {
		t.Errorf("缺姓名 = %d, want 400", w.Code)
	}

	// 等待审核期不影响业务接口：房源列表仍被待审核门禁拦下
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms", tok, nil); w.Code != http.StatusForbidden {
		t.Errorf("待审核用户读业务接口 = %d, want 403", w.Code)
	}
	// 但自己的申请状态读得到
	w = doJSON(env.r, http.MethodGet, "/api/rental/landlord-apply/me", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("me: %d %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "id"); num(got) != float64(appID) {
		t.Errorf("me 返回的申请 id = %v, want %d", got, appID)
	}

	// 重复提交按覆盖处理（同一账号同时只有一条待审申请）
	w = doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok, applyBody("陈房东", "13800002222"))
	if w.Code != http.StatusOK {
		t.Fatalf("resubmit: %d %s", w.Code, w.Body.String())
	}
	if got := num(field(t, decodeBody(t, w), "data", "id")); got != float64(appID) {
		t.Errorf("覆盖提交应复用同一条申请：%v vs %d", got, appID)
	}
	var count int64
	env.db.Model(&LandlordApplication{}).Where("user_id = ?", userID).Count(&count)
	if count != 1 {
		t.Errorf("同一账号的申请行数 = %d, want 1", count)
	}

	// 平台管理员看队列
	w = doJSON(env.r, http.MethodGet, "/api/rental/landlord-applications", env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("admin list: %d %s", w.Code, w.Body.String())
	}
	rows, _ := field(t, decodeBody(t, w), "data").([]interface{})
	if len(rows) != 1 {
		t.Fatalf("待审入驻申请 = %d, want 1", len(rows))
	}
	got := rows[0].(map[string]interface{})
	if got["name"] != "陈房东" || got["phone"] != "13800002222" || num(got["user_status"]) != float64(UserStatusPending) {
		t.Errorf("队列行不对：%v", got)
	}

	// 通过：账号可用 + 个人台账就绪 + 申请置 approved
	w = doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/landlord-applications/%d/approve", appID), env.token,
		map[string]interface{}{"note": "材料齐全"})
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	var u database.User
	env.db.First(&u, userID)
	if u.Status != UserStatusActive {
		t.Errorf("通过后账号状态 = %d, want %d", u.Status, UserStatusActive)
	}
	if personalLedgerOf(env.db, userID) == nil {
		t.Error("通过后应拥有个人台账")
	}
	var app LandlordApplication
	env.db.First(&app, appID)
	if app.Status != "approved" || app.ReviewNote != "材料齐全" || app.ReviewedAt == nil {
		t.Errorf("申请行状态不对：%+v", app)
	}
	// 通过后业务接口可用了
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms", tok, nil); w.Code != http.StatusOK {
		t.Errorf("通过后读房源 = %d, want 200", w.Code)
	}
	// 通过后不能再提交入驻申请
	if w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok,
		applyBody("陈房东", "13800002222")); w.Code != http.StatusBadRequest {
		t.Errorf("通过后再申请 = %d, want 400", w.Code)
	}
	// 已处理过的申请不能再审
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/landlord-applications/%d/approve", appID), env.token, nil); w.Code != http.StatusBadRequest {
		t.Errorf("重复审核 = %d, want 400", w.Code)
	}
}

func TestLandlordApplyRejectAndGuards(t *testing.T) {
	env := setupEnv(t)
	userID, tok := mkUser(t, env, "landlord-reject", UserStatusPending)
	w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tok, applyBody("钱房东", "13800003333"))
	if w.Code != http.StatusOK {
		t.Fatalf("submit: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))

	// 非管理员不能看队列、不能审（admin 组 + 待审核门禁双重把关）
	if w := doJSON(env.r, http.MethodGet, "/api/rental/landlord-applications", tok, nil); w.Code == http.StatusOK {
		t.Error("普通房东不该能看入驻申请队列")
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/landlord-applications/%d/approve", appID), tok, nil); w.Code == http.StatusOK {
		t.Error("普通房东不该能自己通过入驻申请")
	}

	// 驳回：账号停用 + 申请 rejected + 理由入库
	w = doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/landlord-applications/%d/reject", appID), env.token,
		map[string]interface{}{"note": "电话核实不符"})
	if w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	var u database.User
	env.db.First(&u, userID)
	if u.Status != UserStatusDisabled {
		t.Errorf("驳回后账号状态 = %d, want %d", u.Status, UserStatusDisabled)
	}
	var app LandlordApplication
	env.db.First(&app, appID)
	if app.Status != "rejected" || app.ReviewNote != "电话核实不符" {
		t.Errorf("申请行状态不对：%+v", app)
	}

	// 租户角色不能提交入驻申请
	tenantID, tenantTok := mkUser(t, env, "landlord-tenant-role", UserStatusActive)
	if err := env.db.Model(&database.User{}).Where("id = ?", tenantID).
		Update("role", "tenant").Error; err != nil {
		t.Fatalf("set role: %v", err)
	}
	// 租户角色先被业务组的租户门禁拦下（403）；handler 里还有一道「租户不适用入驻
	// 申请」的判断作为兜底（路由万一挪出该组时仍拦得住）
	if w := doJSON(env.r, http.MethodPost, "/api/rental/landlord-apply", tenantTok,
		applyBody("我是租户", "13800004444")); w.Code != http.StatusForbidden {
		t.Errorf("租户提交入驻申请 = %d, want 403", w.Code)
	}

	// 平台管理员账号不能被驳回
	adminApp := LandlordApplication{
		UserID: env.adminID, Username: "admin", Name: "平台", Phone: "13800000000",
		Status: "pending",
	}
	if err := env.db.Create(&adminApp).Error; err != nil {
		t.Fatalf("seed admin app: %v", err)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/landlord-applications/%d/reject", adminApp.ID), env.token, nil); w.Code != http.StatusForbidden {
		t.Errorf("驳回管理员 = %d, want 403", w.Code)
	}
}
