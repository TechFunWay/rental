// tenant_portal_test.go — 租户登录与角色权限（v0.6.10）的端到端断言。
//
// 覆盖 docs/07-租户登录与角色权限设计.md §11.1 的 A–E：
//
//	A 角色与门禁：租户注册不开个人台账、未关联时只能读门户状态、租户对全部
//	  业务接口 403、出租人进不了门户；
//	B 关联/解绑：关联即审核通过、双向唯一性、跨台账与权限不足被挡；
//	C 门户只读范围：只看得到自己房间且姓名快照命中的账单、前任租客的账单不可见、
//	  缴费与合同/附件按本人收窄、越权 ID 一律 404；
//	D 变更申请：提交不落正式数据、通过才覆盖且只覆盖非空字段、驳回删照片、
//	  撤回、跨台账审核被挡；
//	E 回归：见 §11.1 的 go test / version gate（另有既有用例）。
package rental

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/database"
	"smallgo/server/sysconfig"
	"smallgo/server/user"
)

// ---------------- 测试夹具 ----------------

// registerTenant 注册一个租户账号（role=tenant），返回 id 与 token。
func registerTenant(t *testing.T, env *testEnv, username string) (uint, string) {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": username, "password": "secret123", "role": "tenant",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("注册租户 %s: %d %s", username, w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	uid := uint(field(t, body, "data", "user", "id").(float64))
	token := field(t, body, "data", "token").(string)
	// 注册响应必须带 status=2（待关联），前端据此渲染「等待关联」页。
	if got := uint(field(t, body, "data", "user", "status").(float64)); got != uint(UserStatusPending) {
		t.Fatalf("租户注册后 status = %d, want %d（待关联）", got, UserStatusPending)
	}
	return uid, token
}

// registerLandlord 注册一个普通出租人账号（role 缺省 = user）。
func registerLandlord(t *testing.T, env *testEnv, username string) (uint, string) {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": username, "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("注册出租人 %s: %d %s", username, w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	return uint(field(t, body, "data", "user", "id").(float64)),
		field(t, body, "data", "token").(string)
}

// seedTenantWithRoom 建一间房 + 一名租户（走房东 API，自动落个人台账），
// 并把租户的入住/退租日期与姓名写进档案（账单可见范围判定依赖它们）。
func seedTenantWithRoom(t *testing.T, env *testEnv, roomNo, name, moveIn, moveOut string) (uint, uint) {
	t.Helper()
	roomID := env.seedRoom(roomNo, 1000, 0)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": name,
		"move_in_date": moveIn, "move_out_date": moveOut,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed tenant %s: %d %s", name, w.Code, w.Body.String())
	}
	return roomID, uint(field(t, decodeBody(t, w), "data", "id").(float64))
}

// bindTenantAccount 房东把某账号关联到某租户档案。
func bindTenantAccount(t *testing.T, env *testEnv, token string, tenantID, uid uint) {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/account", tenantID),
		token, map[string]interface{}{"user_id": uid})
	if w.Code != http.StatusOK {
		t.Fatalf("关联账号 tenant=%d user=%d: %d %s", tenantID, uid, w.Code, w.Body.String())
	}
}

// portalGet 以租户身份 GET 门户接口。
func portalGet(env *testEnv, token, path string) *httptest.ResponseRecorder {
	return doJSON(env.r, http.MethodGet, path, token, nil)
}

// postPortalForm 以租户身份 multipart 提交（变更申请）。
func postPortalForm(t *testing.T, env *testEnv, token, path string, fields map[string]string, photos [][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	for i, p := range photos {
		part, err := mw.CreateFormFile("photos", fmt.Sprintf("id-%d.jpg", i))
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := part.Write(p); err != nil {
			t.Fatalf("write photo: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	env.r.ServeHTTP(w, req)
	return w
}

// newPortalFixture 是 C/D 两组用例的公共起点：启用图片附件目录覆盖、建房东的
// 房与租户、注册租户账号并关联。返回房东 token/id、租户 token/id、房源与档案 id。
type portalFixture struct {
	env        *testEnv
	landlordID uint
	landlord   string
	tenantUID  uint
	tenant     string
	roomID     uint
	tenantID   uint
}

func newPortalFixture(t *testing.T, roomNo, tenantName, moveIn, moveOut string) *portalFixture {
	t.Helper()
	// 附件与合同目录指向临时目录：变更申请的照片会真实落盘，不能污染 data/。
	attachmentsDirOverride = filepath.Join(t.TempDir(), "attachments")
	contractsDirOverride = filepath.Join(t.TempDir(), "contracts")
	t.Cleanup(func() {
		attachmentsDirOverride = ""
		contractsDirOverride = ""
	})

	env := setupEnv(t)
	roomID, tenantID := seedTenantWithRoom(t, env, roomNo, tenantName, moveIn, moveOut)
	uid, token := registerTenant(t, env, "renter-"+roomNo)
	bindTenantAccount(t, env, env.token, tenantID, uid)
	return &portalFixture{
		env: env, landlordID: env.adminID, landlord: env.token,
		tenantUID: uid, tenant: token, roomID: roomID, tenantID: tenantID,
	}
}

// setTenantProfile 直接落库设置档案的联系/证件字段（模拟房东登记时填过）。
func setTenantProfile(t *testing.T, env *testEnv, tenantID uint, updates map[string]interface{}) {
	t.Helper()
	if err := env.db.Model(&Tenant{}).Where("id = ?", tenantID).Updates(updates).Error; err != nil {
		t.Fatalf("set tenant profile: %v", err)
	}
}

// createBillWithTenant 直接落库一张账单（绕开出账逻辑，便于精确构造姓名快照与账期）。
func createBillWithTenant(t *testing.T, env *testEnv, roomID, ledgerID uint, period, tenantName string) uint {
	t.Helper()
	bill := Bill{
		UserID: env.adminID, LedgerID: ledgerID, RoomID: roomID,
		Period: period, RoomNo: "R-" + period, TenantName: tenantName,
		Rent: 1000, TotalAmount: 1000, Status: billStatusUnpaid,
	}
	if err := env.db.Create(&bill).Error; err != nil {
		t.Fatalf("create bill fixture: %v", err)
	}
	return bill.ID
}

// ---------------- A. 角色与门禁 ----------------

// TestTenantRegisterCreatesNoPersonalLedger：租户注册后**不该**有个人台账。
//
// 个人台账的语义是「自己就是一个房东」（恒全部模块完全权限）；给租户建一张
// 全权限空台账等于把「租户也是房东」写进数据里，与需求冲突（设计文档取舍二）。
func TestTenantRegisterCreatesNoPersonalLedger(t *testing.T) {
	env := setupEnv(t)
	uid, _ := registerTenant(t, env, "renter1")

	if l := personalLedgerOf(env.db, uid); l != nil {
		t.Fatalf("租户不该有个人台账，实得 ledger id=%d", l.ID)
	}
	var members int64
	env.db.Model(&LedgerMember{}).Where("user_id = ?", uid).Count(&members)
	if members != 0 {
		t.Fatalf("租户不该是任何台账成员，实得 %d 行", members)
	}
	// 对照：出租人注册**必须**有个人台账（回归，确认改动没有误伤）。
	_, ok := 0, false
	lid, _ := registerLandlord(t, env, "boss1")
	ok = personalLedgerOf(env.db, lid) != nil
	if !ok {
		t.Fatal("出租人注册后必须有个人台账（回归）")
	}
}

// TestTenantRegisterRespectsPortalSwitch：租户门户关掉后拒绝租户注册，
// 但不影响出租人注册。
func TestTenantRegisterRespectsPortalSwitch(t *testing.T) {
	env := setupEnv(t)
	if err := sysconfig.UpdateConfig(env.db, "rental_tenant_portal_enabled", "false", 0); err != nil {
		t.Fatalf("disable portal: %v", err)
	}

	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "renter-x", "password": "secret123", "role": "tenant",
	})
	if w.Code != http.StatusForbidden {
		t.Fatalf("关掉租户门户后注册租户 = %d, want 403: %s", w.Code, w.Body.String())
	}
	// 出租人注册不受影响。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "boss-x", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("关掉租户门户不该影响出租人注册：%d %s", w.Code, w.Body.String())
	}
}

// TestRegisterWithoutRoleStaysLandlord：不带 role 的注册（老前端）行为不变。
func TestRegisterWithoutRoleStaysLandlord(t *testing.T) {
	env := setupEnv(t)
	uid, _ := registerLandlord(t, env, "legacy-user")
	var role string
	env.db.Table("users").Select("role").Where("id = ?", uid).Scan(&role)
	if role != user.RoleUser {
		t.Fatalf("不带 role 注册的账号 role = %q, want %q", role, user.RoleUser)
	}
	if personalLedgerOf(env.db, uid) == nil {
		t.Fatal("不带 role 注册的账号必须有个人台账（回归）")
	}
}

// TestUnboundTenantCanOnlyReadPortalStatus：未关联的租户只能读门户状态，
// 门户数据接口与业务接口一律 403。
func TestUnboundTenantCanOnlyReadPortalStatus(t *testing.T) {
	env := setupEnv(t)
	// 保留「注册需审核」开启的默认口径不影响租户：租户恒为 status=2。
	_, token := registerTenant(t, env, "renter-unbound")

	w := portalGet(env, token, "/api/rental/portal/status")
	if w.Code != http.StatusOK {
		t.Fatalf("未关联租户读 /portal/status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if got := field(t, body, "data", "bound"); got != false {
		t.Fatalf("未关联时 bound 应为 false，实得 %v", got)
	}
	if got := field(t, body, "data", "role"); got != user.RoleTenant {
		t.Fatalf("role 应为 tenant，实得 %v", got)
	}

	// 门户其余接口全被待审核门禁拦下。
	for _, path := range []string{
		"/api/rental/portal/profile",
		"/api/rental/portal/bills",
		"/api/rental/portal/payments",
		"/api/rental/portal/contracts",
		"/api/rental/portal/change-requests",
	} {
		if w := portalGet(env, token, path); w.Code != http.StatusForbidden {
			t.Fatalf("未关联租户读 %s = %d, want 403: %s", path, w.Code, w.Body.String())
		}
	}
}

// TestTenantBlockedFromAllBusinessAPIs：租户对全部业务接口 403（「不能改任何数据」
// 的默认拒绝兜底）。逐个模块各取一个代表接口，含写与读。
func TestTenantBlockedFromAllBusinessAPIs(t *testing.T) {
	f := newPortalFixture(t, "101", "张三", "2026-01-01", "")

	cases := []struct {
		method, path string
		body         interface{}
	}{
		{http.MethodGet, "/api/rental/rooms", nil},
		{http.MethodPost, "/api/rental/rooms", map[string]interface{}{"room_no": "999"}},
		{http.MethodGet, "/api/rental/tenants", nil},
		{http.MethodPost, "/api/rental/tenants", map[string]interface{}{"room_id": f.roomID, "name": "李四"}},
		{http.MethodGet, "/api/rental/bills", nil},
		{http.MethodGet, "/api/rental/meter-records", nil},
		{http.MethodGet, "/api/rental/payments", nil},
		{http.MethodGet, "/api/rental/fee-items", nil},
		{http.MethodGet, "/api/rental/analytics", nil},
		{http.MethodGet, "/api/rental/ledgers/mine", nil},
		{http.MethodPost, "/api/rental/ledgers", map[string]interface{}{"name": "偷建的台账"}},
		{http.MethodGet, "/api/rental/export", nil},
	}
	for _, tc := range cases {
		w := doJSON(f.env.r, tc.method, tc.path, f.tenant, tc.body)
		// /ledgers/mine 在待审核白名单里，但租户已是 status=1，走的是正常鉴权。
		// 关键断言：**没有写成功**。读接口对租户同样应被 denyTenantRole 挡下。
		if w.Code != http.StatusForbidden {
			t.Fatalf("租户 %s %s = %d, want 403: %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	// 管理后台接口同样不可达。
	for _, path := range []string{"/api/users", "/api/backups", "/api/audit-logs", "/api/rental/users/pending"} {
		if w := portalGet(f.env, f.tenant, path); w.Code != http.StatusForbidden {
			t.Fatalf("租户读 %s = %d, want 403", path, w.Code)
		}
	}
}

// TestLandlordBlockedFromPortal：出租人进不了门户（两套入口不混用）。
func TestLandlordBlockedFromPortal(t *testing.T) {
	env := setupEnv(t)
	for _, path := range []string{
		"/api/rental/portal/status",
		"/api/rental/portal/profile",
		"/api/rental/portal/bills",
	} {
		if w := portalGet(env, env.token, path); w.Code != http.StatusForbidden {
			t.Fatalf("出租人读 %s = %d, want 403: %s", path, w.Code, w.Body.String())
		}
	}
}

// ---------------- B. 关联与解绑 ----------------

// TestBindTenantAccountActivatesPortal：关联成功即审核通过，账号可进门户。
func TestBindTenantAccountActivatesPortal(t *testing.T) {
	env := setupEnv(t)
	_, tenantID := seedTenantWithRoom(t, env, "201", "张三", "2026-01-01", "")
	uid, token := registerTenant(t, env, "renter-bind")

	bindTenantAccount(t, env, env.token, tenantID, uid)

	// 账号状态被置 1（关联即审核通过），档案写入 account_user_id。
	var u database.User
	if err := env.db.First(&u, uid).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if u.Status != UserStatusActive {
		t.Fatalf("关联后账号 status = %d, want %d", u.Status, UserStatusActive)
	}
	var t1 Tenant
	env.db.First(&t1, tenantID)
	if t1.AccountUserID != uid {
		t.Fatalf("档案 account_user_id = %d, want %d", t1.AccountUserID, uid)
	}

	// 门户可读，且回带的姓名与档案一致。
	w := portalGet(env, token, "/api/rental/portal/profile")
	if w.Code != http.StatusOK {
		t.Fatalf("关联后读资料 = %d: %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "profile", "name"); got != "张三" {
		t.Fatalf("portal profile name = %v, want 张三", got)
	}

	// 审计有记录。
	var n int64
	env.db.Model(&database.AuditLog{}).Where("action = ?", "rental.tenant_account.bind").Count(&n)
	if n == 0 {
		t.Fatal("关联账号必须写审计")
	}
}

// TestBindRejectsSecondBindingOnEitherSide：账号与档案双向唯一。
func TestBindRejectsSecondBindingOnEitherSide(t *testing.T) {
	env := setupEnv(t)
	_, tenantA := seedTenantWithRoom(t, env, "301", "甲", "2026-01-01", "")
	_, tenantB := seedTenantWithRoom(t, env, "302", "乙", "2026-01-01", "")
	uid1, _ := registerTenant(t, env, "renter-u1")
	uid2, _ := registerTenant(t, env, "renter-u2")

	bindTenantAccount(t, env, env.token, tenantA, uid1)

	// 同一个账号再绑第二条档案 → 拒绝。
	w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/account", tenantB),
		env.token, map[string]interface{}{"user_id": uid1})
	if w.Code == http.StatusOK {
		t.Fatalf("已绑定的账号不该能再绑第二条档案：%s", w.Body.String())
	}
	// 同一条档案再绑第二个账号 → 拒绝（要明确提示先解绑）。
	w = doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/account", tenantA),
		env.token, map[string]interface{}{"user_id": uid2})
	if w.Code == http.StatusOK {
		t.Fatalf("已绑定的档案不该能再绑第二个账号：%s", w.Body.String())
	}
	// 拒绝之后两条档案的绑定关系必须原封不动。
	var a, b Tenant
	env.db.First(&a, tenantA)
	env.db.First(&b, tenantB)
	if a.AccountUserID != uid1 || b.AccountUserID != 0 {
		t.Fatalf("拒绝后绑定关系被改动：a=%d b=%d", a.AccountUserID, b.AccountUserID)
	}
}

// TestBindRejectsNonTenantAccount：只能关联 role=tenant 的账号。
func TestBindRejectsNonTenantAccount(t *testing.T) {
	env := setupEnv(t)
	_, tenantID := seedTenantWithRoom(t, env, "401", "丙", "2026-01-01", "")
	landlordID, _ := registerLandlord(t, env, "boss-other")

	w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/account", tenantID),
		env.token, map[string]interface{}{"user_id": landlordID})
	if w.Code == http.StatusOK {
		t.Fatalf("不该把出租人账号关联成租户：%s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "租户") {
		t.Fatalf("错误信息应说明只接受租户账号，实得 %s", w.Body.String())
	}
}

// TestBindRejectsCrossLedger：跨台账关联必须被挡（按对象所属台账复核写权限）。
func TestBindRejectsCrossLedger(t *testing.T) {
	env := setupEnv(t)
	// 房东 B 用共同台账：env.token 是管理员（不是 B 台账的成员）。
	_, tenantID := seedTenantWithRoom(t, env, "501", "丁", "2026-01-01", "")
	uid, _ := registerTenant(t, env, "renter-cross")

	// 先造一条**别的台账**的租户档案：模拟 A 台账的房东。
	other := doJSON(env.r, http.MethodPost, "/api/rental/ledgers", env.token,
		map[string]interface{}{"name": "别人的台账"})
	if other.Code != http.StatusOK {
		t.Fatalf("create ledger: %d %s", other.Code, other.Body.String())
	}
	otherLedger := uint(field(t, decodeBody(t, other), "data", "id").(float64))

	// 造一个只属于 otherLedger 的房东（共同台账成员需要邀请接受，这里直接落库更直接）。
	strangerID, strangerToken := registerLandlord(t, env, "boss-stranger")
	if err := env.db.Create(&LedgerMember{
		LedgerID: otherLedger, UserID: strangerID,
		Role: MemberRoleOwner, Status: MemberStatusActive, Perms: "{\"tenants\":\"edit\"}",
	}).Error; err != nil {
		t.Fatalf("seed member: %v", err)
	}

	// 陌生房东想去关联**自己台账之外**的租户档案 tenantID（属于 env 的个人台账）。
	w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/account", tenantID),
		strangerToken, map[string]interface{}{"user_id": uid})
	if w.Code == http.StatusOK {
		t.Fatalf("跨台账关联不该成功：%s", w.Body.String())
	}
	var t1 Tenant
	env.db.First(&t1, tenantID)
	if t1.AccountUserID != 0 {
		t.Fatalf("跨台账关联失败后档案不该被改动，实得 account_user_id=%d", t1.AccountUserID)
	}
}

// TestBindRequiresEditPermission：只有查看权的成员不能关联账号。
func TestBindRequiresEditPermission(t *testing.T) {
	env := setupEnv(t)
	_, tenantID := seedTenantWithRoom(t, env, "601", "戊", "2026-01-01", "")
	uid, _ := registerTenant(t, env, "renter-perm")

	// 建共同台账，把 viewer 加进去但只给 tenants=view。
	created := doJSON(env.r, http.MethodPost, "/api/rental/ledgers", env.token,
		map[string]interface{}{"name": "只读台账"})
	ledgerID := uint(field(t, decodeBody(t, created), "data", "id").(float64))
	viewerID, viewerToken := registerLandlord(t, env, "boss-viewer")
	if err := env.db.Create(&LedgerMember{
		LedgerID: ledgerID, UserID: viewerID,
		Role: MemberRoleMember, Status: MemberStatusActive,
		Perms: "{\"tenants\":\"view\"}",
	}).Error; err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	// 该共同台账里也建一条租户档案，viewer 尝试关联 → 403。
	roomID := env.seedRoomInLedger(ledgerID, "602")
	tenantID2 := env.seedTenantInLedger(ledgerID, roomID, "己")
	w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenants/%d/account?ledger_id=%d", tenantID2, ledgerID),
		viewerToken, map[string]interface{}{"user_id": uid})
	if w.Code != http.StatusForbidden {
		t.Fatalf("只读成员关联账号 = %d, want 403: %s", w.Code, w.Body.String())
	}
	_ = tenantID
}

// TestUnbindReturnsTenantToWaiting：解绑后账号回「等待关联」，门户立刻不可读。
func TestUnbindReturnsTenantToWaiting(t *testing.T) {
	f := newPortalFixture(t, "701", "庚", "2026-01-01", "")

	if w := portalGet(f.env, f.tenant, "/api/rental/portal/profile"); w.Code != http.StatusOK {
		t.Fatalf("解绑前门户应可读：%d %s", w.Code, w.Body.String())
	}
	w := doJSON(f.env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/tenants/%d/account", f.tenantID), f.landlord, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("解绑失败：%d %s", w.Code, w.Body.String())
	}

	var u database.User
	f.env.db.First(&u, f.tenantUID)
	if u.Status != UserStatusPending {
		t.Fatalf("解绑后账号 status = %d, want %d", u.Status, UserStatusPending)
	}
	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.AccountUserID != 0 {
		t.Fatalf("解绑后档案 account_user_id = %d, want 0", t1.AccountUserID)
	}
	// 门户数据接口被待审核门禁拦下；状态接口仍可读且 bound=false。
	if w := portalGet(f.env, f.tenant, "/api/rental/portal/profile"); w.Code != http.StatusForbidden {
		t.Fatalf("解绑后读门户资料 = %d, want 403", w.Code)
	}
	w = portalGet(f.env, f.tenant, "/api/rental/portal/status")
	if w.Code != http.StatusOK {
		t.Fatalf("解绑后读门户状态 = %d, want 200", w.Code)
	}
	if got := field(t, decodeBody(t, w), "data", "bound"); got != false {
		t.Fatalf("解绑后 bound 应为 false，实得 %v", got)
	}
}

// TestUserSearchExcludesTenants：邀请成员时搜不到租户账号（租户不是协作者）。
func TestUserSearchExcludesTenants(t *testing.T) {
	env := setupEnv(t)
	registerTenant(t, env, "renter-searchme")

	w := env.do(http.MethodGet, "/api/rental/users/search?q=searchme", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("search = %d: %s", w.Code, w.Body.String())
	}
	items := decodeBody(t, w)["data"]
	if items == nil {
		t.Fatal("search 应返回数组")
	}
	for _, it := range items.([]interface{}) {
		m := it.(map[string]interface{})
		if strings.Contains(fmt.Sprint(m["username"]), "searchme") {
			t.Fatalf("邀请搜人不该返回租户账号：%v", m)
		}
	}
}

// ---------------- C. 门户只读范围 ----------------

// TestPortalBillsScopedToOwnRoomAndName：只看到自己房间且姓名命中的账单。
func TestPortalBillsScopedToOwnRoomAndName(t *testing.T) {
	f := newPortalFixture(t, "801", "张三", "2026-03-01", "")
	ledger := f.env.personalLedger

	// 本人账单（姓名命中，账期在入住之后）
	mine := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-04", "张三")
	// 自己房间但姓名是别人（前任租客，入住前）
	before := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-01", "前任")
	// 自己房间的联名账单（宿舍场景，含本人姓名）
	shared := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-05", "张三、李四")
	// 别的房间的账单（姓名恰好也是张三）
	otherRoom := f.env.seedRoom("899", 1000, 0)
	otherBill := createBillWithTenant(t, f.env, otherRoom, ledger, "2026-06", "张三")

	w := portalGet(f.env, f.tenant, "/api/rental/portal/bills?pageSize=50")
	if w.Code != http.StatusOK {
		t.Fatalf("读账单 = %d: %s", w.Code, w.Body.String())
	}
	ids := map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !ids[mine] {
		t.Fatal("看不到自己的账单（姓名快照命中）")
	}
	if !ids[shared] {
		t.Fatal("同房的联名账单应可见（宿舍一房多人共用一张账单）")
	}
	if ids[before] {
		t.Fatal("前任租客的账单（入住前账期 + 姓名不符）不该可见")
	}
	if ids[otherBill] {
		t.Fatal("别的房间的账单不该可见（哪怕姓名相同）")
	}
}

// TestPortalBillsAllowsEmptyNameSnapshot：姓名快照为空的历史账单要放行
// （老数据没有快照，不能因此让租户看不到自己的账单）。
func TestPortalBillsAllowsEmptyNameSnapshot(t *testing.T) {
	f := newPortalFixture(t, "802", "王五", "2026-03-01", "")
	legacy := createBillWithTenant(t, f.env, f.roomID, f.env.personalLedger, "2026-04", "")

	w := portalGet(f.env, f.tenant, "/api/rental/portal/bills?pageSize=50")
	ids := map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !ids[legacy] {
		t.Fatal("姓名快照为空的历史账单应可见（老库兼容）")
	}
}

// TestPortalBillDetailHidesOthers：越权账单 id 一律 404（不泄漏存在性）。
func TestPortalBillDetailHidesOthers(t *testing.T) {
	f := newPortalFixture(t, "803", "张三", "2026-03-01", "")
	otherRoom := f.env.seedRoom("897", 1000, 0)
	foreign := createBillWithTenant(t, f.env, otherRoom, f.env.personalLedger, "2026-04", "张三")
	// 前台：别的租户档案的账单虽然同房同姓名，也不在本次判定里越权；
	// 这里主要考「别的房间」的账单。
	w := portalGet(f.env, f.tenant, fmt.Sprintf("/api/rental/portal/bills/%d", foreign))
	if w.Code != http.StatusNotFound {
		t.Fatalf("越权账单详情 = %d, want 404: %s", w.Code, w.Body.String())
	}
	w = portalGet(f.env, f.tenant, "/api/rental/portal/bills/999999")
	if w.Code != http.StatusNotFound {
		t.Fatalf("不存在账单详情 = %d, want 404", w.Code)
	}
}

// TestPortalPaymentsScopedToVisibleBills：缴费记录只含可见账单的收款。
func TestPortalPaymentsScopedToVisibleBills(t *testing.T) {
	f := newPortalFixture(t, "804", "张三", "2026-03-01", "")
	ledger := f.env.personalLedger

	mine := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-04", "张三")
	otherRoom := f.env.seedRoom("896", 1000, 0)
	foreign := createBillWithTenant(t, f.env, otherRoom, ledger, "2026-04", "张三")
	before := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-01", "前任")

	for _, bid := range []uint{mine, foreign, before} {
		if err := f.env.db.Create(&Payment{
			UserID: f.env.adminID, LedgerID: ledger, BillID: bid,
			Amount: 500, Items: "[]",
		}).Error; err != nil {
			t.Fatalf("seed payment: %v", err)
		}
	}

	w := portalGet(f.env, f.tenant, "/api/rental/portal/payments?pageSize=50")
	if w.Code != http.StatusOK {
		t.Fatalf("读缴费记录 = %d: %s", w.Code, w.Body.String())
	}
	got := map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		got[uint(it.(map[string]interface{})["bill_id"].(float64))] = true
	}
	if !got[mine] {
		t.Fatal("自己的缴费记录应可见")
	}
	if got[foreign] {
		t.Fatal("别的房间的收款不该可见")
	}
	if got[before] {
		t.Fatal("前任租客的收款不该可见")
	}
}

// TestPortalContractsAndAttachmentsScopedToSelf：合同与附件只含自己的。
func TestPortalContractsAndAttachmentsScopedToSelf(t *testing.T) {
	f := newPortalFixture(t, "805", "张三", "2026-03-01", "")
	ledger := f.env.personalLedger
	// 同房的另一名租户档案（同姓名也不同档案）：其合同对自己不可见。
	var otherTenant Tenant
	f.env.db.First(&otherTenant, f.tenantID)
	otherTenant.ID = 0
	otherTenant.Name = "张三"
	otherTenant.AccountUserID = 0
	// 用同房同名的第二份档案，验证「只按 tenant_id 精确命中」。
	if err := f.env.db.Create(&Tenant{
		LedgerID: ledger, RoomID: f.roomID, Name: "张三", Active: true,
	}).Error; err != nil {
		t.Fatalf("seed sibling tenant: %v", err)
	}
	var sibling Tenant
	f.env.db.Where("id <> ? AND room_id = ?", f.tenantID, f.roomID).First(&sibling)

	mineC := Contract{UserID: f.env.adminID, LedgerID: ledger, TenantID: f.tenantID,
		FileName: "我的合同.pdf", FilePath: "mine.pdf", FileSize: 10, MimeType: "application/pdf"}
	otherC := Contract{UserID: f.env.adminID, LedgerID: ledger, TenantID: sibling.ID,
		FileName: "别人合同.pdf", FilePath: "other.pdf", FileSize: 10, MimeType: "application/pdf"}
	f.env.db.Create(&mineC)
	f.env.db.Create(&otherC)
	mineA := TenantAttachment{UserID: f.env.adminID, LedgerID: ledger, TenantID: f.tenantID,
		FileName: "我的证件.jpg", FilePath: "mine.jpg", FileSize: 10, MimeType: "image/jpeg"}
	otherA := TenantAttachment{UserID: f.env.adminID, LedgerID: ledger, TenantID: sibling.ID,
		FileName: "别人证件.jpg", FilePath: "other.jpg", FileSize: 10, MimeType: "image/jpeg"}
	f.env.db.Create(&mineA)
	f.env.db.Create(&otherA)

	w := portalGet(f.env, f.tenant, "/api/rental/portal/contracts")
	if w.Code != http.StatusOK {
		t.Fatalf("读合同 = %d: %s", w.Code, w.Body.String())
	}
	contractIDs := map[uint]bool{}
	for _, it := range decodeBody(t, w)["data"].([]interface{}) {
		contractIDs[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !contractIDs[mineC.ID] || contractIDs[otherC.ID] {
		t.Fatalf("合同范围错误：mine=%v other=%v", contractIDs[mineC.ID], contractIDs[otherC.ID])
	}

	// 越权取别人的合同文件 → 404。
	if w := portalGet(f.env, f.tenant,
		fmt.Sprintf("/api/rental/portal/contracts/%d/file", otherC.ID)); w.Code != http.StatusNotFound {
		t.Fatalf("取别人合同文件 = %d, want 404", w.Code)
	}
	// 越权取别人的附件 → 404。
	if w := portalGet(f.env, f.tenant,
		fmt.Sprintf("/api/rental/portal/attachments/%d/file", otherA.ID)); w.Code != http.StatusNotFound {
		t.Fatalf("取别人附件 = %d, want 404", w.Code)
	}

	// 资料接口里只带自己的附件。
	w = portalGet(f.env, f.tenant, "/api/rental/portal/profile")
	attIDs := map[uint]bool{}
	for _, it := range decodeBody(t, w)["data"].(map[string]interface{})["attachments"].([]interface{}) {
		attIDs[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !attIDs[mineA.ID] || attIDs[otherA.ID] {
		t.Fatalf("附件范围错误：mine=%v other=%v", attIDs[mineA.ID], attIDs[otherA.ID])
	}
}

// ---------------- D. 资料变更申请 ----------------

// TestChangeSubmitDoesNotTouchOfficialData：提交申请**不落正式数据**（核心断言）。
func TestChangeSubmitDoesNotTouchOfficialData(t *testing.T) {
	f := newPortalFixture(t, "901", "张三", "2026-01-01", "")
	setTenantProfile(t, f.env, f.tenantID, map[string]interface{}{
		"phone": "13800000000", "id_card": "110101199001011234", "address": "旧地址",
	})

	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests", map[string]string{
		"phone": "13900000000", "address": "新地址",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交变更申请 = %d: %s", w.Code, w.Body.String())
	}

	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.Phone != "13800000000" {
		t.Fatalf("提交申请后正式手机号被改动：%q（必须是等审核通过才覆盖）", t1.Phone)
	}
	if t1.Address != "旧地址" {
		t.Fatalf("提交申请后正式住址被改动：%q", t1.Address)
	}
	// 申请落库为待审。
	var req TenantChangeRequest
	if err := f.env.db.Where("tenant_id = ?", f.tenantID).First(&req).Error; err != nil {
		t.Fatalf("申请未落库：%v", err)
	}
	if req.Status != ChangeStatusPending {
		t.Fatalf("申请状态 = %q, want pending", req.Status)
	}
	// 资料接口回带 pending_change=true，供前端显示状态条。
	w = portalGet(f.env, f.tenant, "/api/rental/portal/profile")
	if got := field(t, decodeBody(t, w), "data", "profile", "pending_change"); got != true {
		t.Fatalf("pending_change 应为 true，实得 %v", got)
	}
}

// TestChangeSubmitRejectsEmptyAndDuplicate：全空申请与重复提交被拒。
func TestChangeSubmitRejectsEmptyAndDuplicate(t *testing.T) {
	f := newPortalFixture(t, "902", "张三", "2026-01-01", "")

	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"note": "只写了留言"}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("没有可变更内容的申请 = %d, want 400: %s", w.Code, w.Body.String())
	}

	if w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, nil); w.Code != http.StatusOK {
		t.Fatalf("首次提交失败：%d %s", w.Code, w.Body.String())
	}
	w = postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13911111111"}, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重复提交 = %d, want 400: %s", w.Code, w.Body.String())
	}
	// 仍只有一条申请。
	var n int64
	f.env.db.Model(&TenantChangeRequest{}).Where("tenant_id = ?", f.tenantID).Count(&n)
	if n != 1 {
		t.Fatalf("重复提交后申请数 = %d, want 1", n)
	}
}

// TestChangeApproveOverwritesOnlyNonEmptyFields：通过时只覆盖非空字段。
func TestChangeApproveOverwritesOnlyNonEmptyFields(t *testing.T) {
	f := newPortalFixture(t, "903", "张三", "2026-01-01", "")
	setTenantProfile(t, f.env, f.tenantID, map[string]interface{}{
		"phone": "13800000000", "id_card": "110101199001011234",
		"address": "旧地址", "emergency_contact": "老联系人",
	})

	// 只申请改手机号：证件号/住址/紧急联系人必须保持原值。
	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve", reqID), f.landlord, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("通过申请 = %d: %s", w.Code, w.Body.String())
	}

	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.Phone != "13900000000" {
		t.Fatalf("手机号未覆盖：%q", t1.Phone)
	}
	if t1.IDCard != "110101199001011234" {
		t.Fatalf("未申请的证件号被改动：%q（必须保持原值）", t1.IDCard)
	}
	if t1.Address != "旧地址" {
		t.Fatalf("未申请的住址被改动：%q", t1.Address)
	}
	if t1.EmergencyContact != "老联系人" {
		t.Fatalf("未申请的紧急联系人被改动：%q", t1.EmergencyContact)
	}
	// 重复通过 → 400（幂等）。
	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve", reqID), f.landlord, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重复通过 = %d, want 400: %s", w.Code, w.Body.String())
	}
}

// TestChangeRejectKeepsDataAndDeletesPhotos：驳回不动正式数据并删除本次照片。
func TestChangeRejectKeepsDataAndDeletesPhotos(t *testing.T) {
	f := newPortalFixture(t, "904", "张三", "2026-01-01", "")
	setTenantProfile(t, f.env, f.tenantID, map[string]interface{}{"phone": "13800000000"})

	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, [][]byte{jpegBytes()})
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 照片已落盘。
	var req TenantChangeRequest
	f.env.db.First(&req, reqID)
	if len(req.NewPhotos) != 1 {
		t.Fatalf("申请应有 1 张照片，实得 %d", len(req.NewPhotos))
	}
	photoAbs := attachmentAbsPath(req.NewPhotos[0])
	if !fileExists(photoAbs) {
		t.Fatal("提交后照片应已落盘")
	}

	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/reject", reqID),
		f.landlord, map[string]interface{}{"note": "证件照不清晰"})
	if w.Code != http.StatusOK {
		t.Fatalf("驳回 = %d: %s", w.Code, w.Body.String())
	}

	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.Phone != "13800000000" {
		t.Fatalf("驳回后正式手机号被改动：%q", t1.Phone)
	}
	if fileExists(photoAbs) {
		t.Fatal("驳回后本次照片应被删除（隐私：不留无用证件照）")
	}
	var after TenantChangeRequest
	f.env.db.First(&after, reqID)
	if after.Status != ChangeStatusRejected || after.ReviewNote != "证件照不清晰" {
		t.Fatalf("驳回状态/理由未落库：status=%q note=%q", after.Status, after.ReviewNote)
	}
}

// TestChangeApproveMovesPhotosToAttachments：通过后照片并入租户正式附件。
func TestChangeApproveMovesPhotosToAttachments(t *testing.T) {
	f := newPortalFixture(t, "905", "张三", "2026-01-01", "")

	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"id_card": "110101199001011234"}, [][]byte{jpegBytes(), jpegBytes()})
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve", reqID), f.landlord, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("通过 = %d: %s", w.Code, w.Body.String())
	}

	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.IDCard != "110101199001011234" {
		t.Fatalf("证件号未覆盖：%q", t1.IDCard)
	}
	var atts []TenantAttachment
	f.env.db.Where("tenant_id = ?", f.tenantID).Find(&atts)
	if len(atts) != 2 {
		t.Fatalf("通过后应并入 2 个附件，实得 %d", len(atts))
	}
	for _, a := range atts {
		if !fileExists(attachmentAbsPath(a.FilePath)) {
			t.Fatalf("附件文件不存在：%s", a.FilePath)
		}
	}
	// 申请里的照片路径已清空（避免指向已挪走的路径）。
	var req TenantChangeRequest
	f.env.db.First(&req, reqID)
	if len(req.NewPhotos) != 0 {
		t.Fatalf("通过后申请照片列表应清空，实得 %v", req.NewPhotos)
	}
}

// TestChangeWithdrawRemovesPhotos：撤回后照片被删除、状态为 rejected。
func TestChangeWithdrawRemovesPhotos(t *testing.T) {
	f := newPortalFixture(t, "906", "张三", "2026-01-01", "")

	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, [][]byte{jpegBytes()})
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var req TenantChangeRequest
	f.env.db.First(&req, reqID)
	photoAbs := attachmentAbsPath(req.NewPhotos[0])

	w = doJSON(f.env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/portal/change-requests/%d", reqID), f.tenant, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("撤回 = %d: %s", w.Code, w.Body.String())
	}
	if fileExists(photoAbs) {
		t.Fatal("撤回后照片应被删除")
	}
	var after TenantChangeRequest
	f.env.db.First(&after, reqID)
	if after.Status != ChangeStatusRejected {
		t.Fatalf("撤回后状态 = %q, want rejected", after.Status)
	}
	// 撤回后可以重新提交（不再有待审）。
	w = postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13911111111"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("撤回后重新提交失败：%d %s", w.Code, w.Body.String())
	}
}

// TestChangeReviewRejectsCrossLedgerAndViewer：跨台账审核与只读成员审核都被挡。
func TestChangeReviewRejectsCrossLedgerAndViewer(t *testing.T) {
	f := newPortalFixture(t, "907", "张三", "2026-01-01", "")
	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 跨台账：陌生房东（别的台账 owner）审核 → 404/403。
	created := doJSON(f.env.r, http.MethodPost, "/api/rental/ledgers", f.landlord,
		map[string]interface{}{"name": "另一个台账"})
	otherLedger := uint(field(t, decodeBody(t, created), "data", "id").(float64))
	strangerID, strangerToken := registerLandlord(t, f.env, "boss-outsider")
	f.env.db.Create(&LedgerMember{
		LedgerID: otherLedger, UserID: strangerID,
		Role: MemberRoleOwner, Status: MemberStatusActive, Perms: "{\"tenants\":\"full\"}",
	})
	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve", reqID), strangerToken, nil)
	if w.Code == http.StatusOK {
		t.Fatalf("跨台账审核不该成功：%s", w.Body.String())
	}

	// 只读成员审核 → 403。
	viewerID, viewerToken := registerLandlord(t, f.env, "boss-readonly")
	f.env.db.Create(&LedgerMember{
		LedgerID: f.env.personalLedger, UserID: viewerID,
		Role: MemberRoleMember, Status: MemberStatusActive, Perms: "{\"tenants\":\"view\"}",
	})
	w = doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve?ledger_id=%d", reqID, f.env.personalLedger),
		viewerToken, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("只读成员审核 = %d, want 403: %s", w.Code, w.Body.String())
	}

	// 申请仍是待审，正式数据未变。
	var req TenantChangeRequest
	f.env.db.First(&req, reqID)
	if req.Status != ChangeStatusPending {
		t.Fatalf("被拒的审核不该改动状态，实得 %q", req.Status)
	}
	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.Phone == "13900000000" {
		t.Fatal("被拒的审核不该覆盖正式数据")
	}
}

// TestChangeSubmitIgnoresNonWhitelistedFields：非白名单字段（姓名/房间）被忽略。
func TestChangeSubmitIgnoresNonWhitelistedFields(t *testing.T) {
	f := newPortalFixture(t, "908", "张三", "2026-01-01", "")
	before := f.roomID

	// 恶意构造：带上 name / room_id / deposit，服务端不该采纳。
	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests", map[string]string{
		"phone": "13900000000", "name": "李四", "room_id": "1", "deposit": "99999",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	var req TenantChangeRequest
	f.env.db.Where("tenant_id = ?", f.tenantID).First(&req)
	reqID := req.ID
	doJSON(f.env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/approve", reqID), f.landlord, nil)

	var t1 Tenant
	f.env.db.First(&t1, f.tenantID)
	if t1.Name != "张三" {
		t.Fatalf("姓名不该被租户改动：%q", t1.Name)
	}
	if t1.RoomID != before {
		t.Fatalf("房间不该被租户改动：%d", t1.RoomID)
	}
	if t1.Phone != "13900000000" {
		t.Fatalf("白名单内的手机号应被覆盖：%q", t1.Phone)
	}
}

// TestPortalProfileMasksIDCard：门户回显的证件号脱敏（完整值不出现在响应里）。
func TestPortalProfileMasksIDCard(t *testing.T) {
	f := newPortalFixture(t, "909", "张三", "2026-01-01", "")
	const full = "110101199001011234"
	setTenantProfile(t, f.env, f.tenantID, map[string]interface{}{"id_card": full})

	w := portalGet(f.env, f.tenant, "/api/rental/portal/profile")
	if strings.Contains(w.Body.String(), full) {
		t.Fatalf("门户不该回显完整证件号：%s", w.Body.String())
	}
	got := fmt.Sprint(field(t, decodeBody(t, w), "data", "profile", "id_card"))
	if !strings.HasPrefix(got, "110101") || !strings.HasSuffix(got, "1234") {
		t.Fatalf("脱敏格式不符：%q", got)
	}
}

// TestTenantRoleChangeRequiresUnbind：已绑定档案的租户账号不能直接改角色。
func TestTenantRoleChangeRequiresUnbind(t *testing.T) {
	f := newPortalFixture(t, "910", "张三", "2026-01-01", "")

	// 管理员尝试把已绑定的租户账号改成 user → 拒绝。
	w := doJSON(f.env.r, http.MethodPut, fmt.Sprintf("/api/users/%d", f.tenantUID),
		f.env.token, map[string]interface{}{"role": "user"})
	if w.Code == http.StatusOK {
		t.Fatalf("已绑定的租户账号不该能直接改角色：%s", w.Body.String())
	}

	// 解绑后可以改。
	doJSON(f.env.r, http.MethodDelete,
		fmt.Sprintf("/api/rental/tenants/%d/account", f.tenantID), f.landlord, nil)
	w = doJSON(f.env.r, http.MethodPut, fmt.Sprintf("/api/users/%d", f.tenantUID),
		f.env.token, map[string]interface{}{"role": "user"})
	if w.Code != http.StatusOK {
		t.Fatalf("解绑后改角色失败：%d %s", w.Code, w.Body.String())
	}
}

// TestTenantCandidatesExcludeBoundAccounts：候选列表里不出现已绑定的账号。
func TestTenantCandidatesExcludeBoundAccounts(t *testing.T) {
	env := setupEnv(t)
	_, tenantID := seedTenantWithRoom(t, env, "911", "张三", "2026-01-01", "")
	boundUID, _ := registerTenant(t, env, "renter-bound1")
	freeUID, _ := registerTenant(t, env, "renter-free1")
	bindTenantAccount(t, env, env.token, tenantID, boundUID)

	w := env.do(http.MethodGet, "/api/rental/tenant-accounts/candidates?keyword=renter-", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("候选账号 = %d: %s", w.Code, w.Body.String())
	}
	ids := map[uint]bool{}
	for _, it := range decodeBody(t, w)["data"].([]interface{}) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if ids[boundUID] {
		t.Fatal("已绑定的账号不该出现在候选列表")
	}
	if !ids[freeUID] {
		t.Fatal("未绑定的租户账号应出现在候选列表")
	}
	// 关键字太短 → 400（账号枚举防护）。
	if w := env.do(http.MethodGet, "/api/rental/tenant-accounts/candidates?keyword=r", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("关键字过短 = %d, want 400", w.Code)
	}
}

// TestPortalChangePhotoAccessControl：照片只能由本人或本台账房东读到。
func TestPortalChangePhotoAccessControl(t *testing.T) {
	f := newPortalFixture(t, "912", "张三", "2026-01-01", "")
	w := postPortalForm(t, f.env, f.tenant, "/api/rental/portal/change-requests",
		map[string]string{"phone": "13900000000"}, [][]byte{jpegBytes()})
	if w.Code != http.StatusOK {
		t.Fatalf("提交失败：%d %s", w.Code, w.Body.String())
	}
	reqID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 本人可读。
	if w := portalGet(f.env, f.tenant,
		fmt.Sprintf("/api/rental/portal/change-requests/%d/photo/0", reqID)); w.Code != http.StatusOK {
		t.Fatalf("本人读照片 = %d: %s", w.Code, w.Body.String())
	}
	// 房东侧可读（按台账鉴权）。
	if w := portalGet(f.env, f.landlord,
		fmt.Sprintf("/api/rental/tenant-change-requests/%d/photo/0", reqID)); w.Code != http.StatusOK {
		t.Fatalf("房东读照片 = %d: %s", w.Code, w.Body.String())
	}
	// 越界序号 → 404。
	if w := portalGet(f.env, f.tenant,
		fmt.Sprintf("/api/rental/portal/change-requests/%d/photo/9", reqID)); w.Code != http.StatusNotFound {
		t.Fatalf("越界照片序号 = %d, want 404", w.Code)
	}
	// 别人的申请 id → 404。
	if w := portalGet(f.env, f.tenant, "/api/rental/portal/change-requests/999999/photo/0"); w.Code != http.StatusNotFound {
		t.Fatalf("越权申请照片 = %d, want 404", w.Code)
	}
	// 公开静态目录里不该出现申请照片（未审核证件照不进公开目录）。
	if w := portalGet(f.env, f.tenant, "/uploads/apply-change/x.jpg"); w.Code == http.StatusOK {
		t.Fatal("申请照片不该出现在公开的 /uploads 目录下")
	}
}

// TestPortalBillReceiptMatchesBill：单据数据含明细与抬头，且只含本人的账单。
func TestPortalBillReceiptMatchesBill(t *testing.T) {
	f := newPortalFixture(t, "913", "张三", "2026-03-01", "")
	billID := createBillWithTenant(t, f.env, f.roomID, f.env.personalLedger, "2026-04", "张三")
	if err := f.env.db.Create(&BillItem{
		UserID: f.env.adminID, LedgerID: f.env.personalLedger, BillID: billID,
		FeeKey: "rent", Name: "租金", Kind: "fixed", Amount: 1000,
	}).Error; err != nil {
		t.Fatalf("seed bill item: %v", err)
	}

	w := portalGet(f.env, f.tenant, fmt.Sprintf("/api/rental/portal/bills/%d/receipt", billID))
	if w.Code != http.StatusOK {
		t.Fatalf("读单据 = %d: %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	items := field(t, body, "data", "items").([]interface{})
	if len(items) != 1 {
		t.Fatalf("单据明细应为 1 条，实得 %d", len(items))
	}
	if _, ok := body["data"].(map[string]interface{})["property"]; !ok {
		t.Fatal("单据应带 property 抬头信息")
	}
}

// seedRoomInLedger / seedTenantInLedger 指定台账落库建 fixture。
//
// 为什么要绕开 API：不带 ?ledger_id 的写操作会落到调用者的**个人台账**，而
// 权限用例需要把数据放在共同台账里。直接用 env.db 建行最直白，也让「共同台账里
// 只有查看权的成员能不能越权」这类断言不受 API 行为影响。
func (e *testEnv) seedRoomInLedger(ledgerID uint, roomNo string) uint {
	e.t.Helper()
	room := Room{UserID: e.adminID, LedgerID: ledgerID, RoomNo: roomNo}
	if err := e.db.Create(&room).Error; err != nil {
		e.t.Fatalf("seed room in ledger: %v", err)
	}
	return room.ID
}

func (e *testEnv) seedTenantInLedger(ledgerID, roomID uint, name string) uint {
	e.t.Helper()
	tenant := Tenant{UserID: e.adminID, LedgerID: ledgerID, RoomID: roomID, Name: name, Active: true}
	if err := e.db.Create(&tenant).Error; err != nil {
		e.t.Fatalf("seed tenant in ledger: %v", err)
	}
	return tenant.ID
}
