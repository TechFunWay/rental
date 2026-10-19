// apply_public_regression_test.go — 免登录登记链接的回归护栏（2026-10-13）。
//
// 背景：v0.6.10 引入了租户登录与角色权限（见 docs/07-租户登录与角色权限设计.md）。
// 该功能新增了两种门禁——业务路由组的 denyTenantRole（默认拒绝）与门户的
// requireTenantRole——很容易被"顺手"套到公开登记接口上。**但公开登记链接必须保持
// 免登录**：它是房东把链接发给还没账号的看房人用的，对方没有应用账号、更不会有
// 租户角色；一旦被角色门禁拦下，这条路就废了。
//
// 用户 2026-10-13 明确：免登录登记功能要保留，「网络问题那是用户自己的事情，
// 功能要有，后期部署到公网就能直接用了」。也就是说**可达性属于部署方**，
// 功能本身必须完整——本文件就是钉住这一点的护栏。
//
// 断言三件事：
//  1. 未带任何凭证（无 token / 无 cookie）即可读登记页信息与提交登记；
//  2. 公开登记接口挂的是**公开路由组**（不在 /api 登录态之内），
//     且不因调用者角色（房东/租户/匿名）而改变行为；
//  3. 公开登记**不被**新增的角色门禁影响：租户账号访问同样 200（它本就与角色无关），
//     而业务接口仍对租户 403（两条路径互不干扰）。
package rental

import (
	"net/http"
	"testing"
)

// TestPublicApplyWorksWithoutAnyCredential 免登录登记：不带任何凭证即可读页与提交。
//
// 这是"登记链接发给看房人"的核心前提——对方没有账号，拿不出 token。
func TestPublicApplyWorksWithoutAnyCredential(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = t.TempDir()
	t.Cleanup(func() { attachmentsDirOverride = "" })
	seedApplyRoom(t, env, "101")

	// 1) 读登记页信息（房东、房号、AI 开关）——完全匿名。
	w := doJSON(env.r, http.MethodGet, "/api/rental/apply/"+applyTokenForTest, "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("匿名读登记页 = %d, want 200：%s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if got := field(t, body, "data", "room_no"); got != "101" {
		t.Fatalf("登记页房号 = %v, want 101", got)
	}
	// AI 识别开关必须回给公开页——否则页面会显示一个点了必然失败（未配 Key）的按钮。
	if _, ok := body["data"].(map[string]interface{})["ai_ocr"]; !ok {
		t.Fatal("登记页必须回 ai_ocr 开关（没配 Key 时前端不该显示识别按钮）")
	}

	// 2) 匿名提交登记。
	w = postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest,
		map[string]string{"name": "王五", "phone": "13600001111", "id_card": "110101199505054321"},
		jpegBytes())
	if w.Code != http.StatusOK {
		t.Fatalf("匿名提交登记 = %d, want 200：%s", w.Code, w.Body.String())
	}
	appID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 3) 申请确实落进房东的待审队列。
	var app TenantApplication
	if err := env.db.First(&app, appID).Error; err != nil {
		t.Fatalf("申请未落库：%v", err)
	}
	if app.Status != "pending" || app.Name != "王五" {
		t.Fatalf("申请状态/姓名不对：status=%q name=%q", app.Status, app.Name)
	}
	if app.LedgerID != env.personalLedger {
		t.Fatalf("申请归属台账 = %d, want %d（应随房源）", app.LedgerID, env.personalLedger)
	}
}

// TestPublicApplyUnaffectedByRoleGates 两条路径互不干扰：
// 公开登记与角色无关（匿名/房东/租户访问都一样），而业务接口对租户仍 403
// ——确保没人把 denyTenantRole / requireTenantRole 套到公开登记上。
func TestPublicApplyUnaffectedByRoleGates(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = t.TempDir()
	t.Cleanup(func() { attachmentsDirOverride = "" })
	seedApplyRoom(t, env, "102")

	// 注册一个租户账号（status=2 待关联，未绑定任何档案）。
	_, tenantToken := registerTenant(t, env, "renter-apply")

	// 公开登记接口对三种身份都必须 200：它压根不该看角色。
	for name, token := range map[string]string{
		"匿名": "",
		"房东": env.token,
		"租户": tenantToken,
	} {
		w := doJSON(env.r, http.MethodGet, "/api/rental/apply/"+applyTokenForTest, token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s身份读登记页 = %d, want 200（公开登记与角色无关）：%s", name, w.Code, w.Body.String())
		}
	}

	// 对照组：业务接口对租户必须仍然 403（角色门禁没被这次改动削弱）。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms", tenantToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("租户读业务接口 = %d, want 403", w.Code)
	}
	// 门户接口对房东必须 403（两条路径不混用）。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/portal/status", env.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("房东读门户 = %d, want 403", w.Code)
	}
}

// TestPublicApplyRouteStaysOnPublicGroup 结构性护栏：公开登记路由必须挂在
// **公开路由组**（apps.App.Setup → publicGroup，免登录 + 限流），
// 而不是 SetupAuth（authGroup，需登录）。
//
// 为什么用"匿名可读"来间接断言而不是直接读路由表：挂在 authGroup 上时，
// 匿名请求会被 RequireAuth 拦成 401；本测试断言的是 200，因此**即使将来有人
// 把这条路由挪到受保护组，测试也会立刻红**。这比断言内部注册结构更贴近真实
// 语义（用户关心的就是"对方没账号也能打开"）。
func TestPublicApplyRouteStaysOnPublicGroup(t *testing.T) {
	env := setupEnv(t)
	seedApplyRoom(t, env, "103")

	w := doJSON(env.r, http.MethodGet, "/api/rental/apply/"+applyTokenForTest, "", nil)
	if w.Code == http.StatusUnauthorized {
		t.Fatal("公开登记接口返回 401：它被挪进了需要登录的路由组，" +
			"没有账号的看房人将无法打开登记链接")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("匿名读登记页 = %d, want 200：%s", w.Code, w.Body.String())
	}

	// 无效 token 仍按 404 处理（不泄漏"这个 token 不存在还是已失效"）。
	w = doJSON(env.r, http.MethodGet, "/api/rental/apply/"+"00000000000000000000000000000000", "", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("无效 token 读登记页 = %d, want 404", w.Code)
	}
}

// TestApplySubmitThenConvertKeepsAddress 回归：免登录登记提交的**住址**要能随
// 「转入登记」落进正式档案。
//
// 为什么专门测：v0.6.10 之前住址只存在于申请单里，转入登记后就丢了（正式档案
// 没有这一列）。本次给 tenants 补了 address 列并在前端带入，这条路要走通，
// 否则「租客当初在登记页填的住址」会第二次丢失。
func TestApplySubmitThenConvertKeepsAddress(t *testing.T) {
	env := setupEnv(t)
	attachmentsDirOverride = t.TempDir()
	t.Cleanup(func() { attachmentsDirOverride = "" })
	seedApplyRoom(t, env, "104")

	w := postApplyForm(t, env, "/api/rental/apply/"+applyTokenForTest, map[string]string{
		"name": "赵六", "phone": "13600002222",
		"id_card": "110101199606067890", "address": "广东省某市某区某路 1 号",
	}, jpegBytes())
	if w.Code != http.StatusOK {
		t.Fatalf("提交登记 = %d：%s", w.Code, w.Body.String())
	}
	appID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 房东「转入登记」：创建租户时带上申请 id 与住址（前端行为，这里直接调 API）。
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": 1, "name": "赵六", "phone": "13600002222",
		"id_card": "110101199606067890", "address": "广东省某市某区某路 1 号",
		"application_id": appID,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("转入登记 = %d：%s", w.Code, w.Body.String())
	}
	tenantID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	var tenant Tenant
	if err := env.db.First(&tenant, tenantID).Error; err != nil {
		t.Fatalf("读租户：%v", err)
	}
	if tenant.Address != "广东省某市某区某路 1 号" {
		t.Fatalf("转入登记后住址 = %q，登记页填的住址丢了", tenant.Address)
	}
	// 申请被标记已入库，身份证照片转成该租户的附件。
	var app TenantApplication
	env.db.First(&app, appID)
	if app.Status != "approved" || app.TenantID != tenantID {
		t.Fatalf("申请未正确入库：status=%q tenant_id=%d", app.Status, app.TenantID)
	}
	var atts int64
	env.db.Model(&TenantAttachment{}).Where("tenant_id = ?", tenantID).Count(&atts)
	if atts == 0 {
		t.Fatal("登记时上传的身份证照片未转成租户附件")
	}
}
