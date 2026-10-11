// ledger_test.go — v0.6.0「个人台账 + 共同台账」模型的正式回归测试。
//
// 覆盖六类**安全关键**行为（都是「读代码不足以证明、必须真跑」的类型）：
//
//  1. 注册即得个人台账：kind=personal、owner=本人、role=owner/status=active、全模块 full。
//  2. 个人台账之间默认隔离：各自建房互不可见；按 ID 直取对方记录 404；
//     显式 ?ledger_id=对方个人台账 403。
//  3. 共同台账的邀请闭环：邀请落 pending → 被邀请人 respond 接受 → active 可见；
//     拒绝 → rejected 不可见。
//  4. 只有管理者能邀请/设权限/移除/改名/删除；成员只能看、按权限写。
//  5. **超管在业务数据上没有特权**：不是某台账成员就读不到它的数据（403）。
//  6. 删除保护：个人台账不可删；有业务数据的共同台账不可删。
//
// 全部用例走真实 HTTP（httptest），不直接调 handler，保证门禁真的挂在路由上。
package rental

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"smallgo/server/auth"
	"smallgo/server/database"
	"smallgo/server/sysconfig"
)

// itoa 测试内的小工具（避免 import strconv 到多个文件里各写一遍）。
func itoa(v uint) string { return strconv.FormatUint(uint64(v), 10) }

// contains 子串判断（测试断言提示语用）。
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ledgerFixture 一个隔离台账（默认是用户的个人台账）+ 它的主人。
type ledgerFixture struct {
	ledgerID uint
	userID   uint
	token    string
	env      *testEnv
}

// mkUser 直接建用户并签 token（绕过注册审核，审核流程由专门用例覆盖）。
//
// 新模型里每个账号必须有个人台账：直接 db.Create 不会触发注册钩子
// （钩子只在 user service 的注册/审批事务里跑），所以这里显式补建，
// 让 fixture 与真实注册路径一致。
func mkUser(t *testing.T, env *testEnv, username string, status int) (uint, string) {
	t.Helper()
	u := database.User{Username: username, Password: "x", Role: "user", Status: status}
	if err := env.db.Create(&u).Error; err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}
	// Status=0（已禁用）是零值，会被 gorm 的 `default:1` 顶掉，显式落库。
	if err := env.db.Model(&u).Update("status", status).Error; err != nil {
		t.Fatalf("set status for %s: %v", username, err)
	}
	if _, err := ensurePersonalLedger(env.db, u.ID); err != nil {
		t.Fatalf("ensure personal ledger for %s: %v", username, err)
	}
	secret, err := sysconfig.GetConfig(env.db, "jwt_secret", 0)
	if err != nil || secret == "" {
		t.Fatalf("jwt secret: %v", err)
	}
	tok, err := auth.GenerateToken(u.ID, u.Username, u.Role, u.AuthVersion, secret)
	if err != nil {
		t.Fatalf("token for %s: %v", username, err)
	}
	return u.ID, tok
}

// newPersonalLedger 建一个普通用户，返回其**个人台账**作为隔离台账。
// 个人台账天然只有本人（owner + 全模块 full），是验证隔离最干净的载体。
func newPersonalLedger(t *testing.T, env *testEnv, username string) ledgerFixture {
	t.Helper()
	uid, tok := mkUser(t, env, username, UserStatusActive)
	l := personalLedgerOf(env.db, uid)
	if l == nil {
		t.Fatalf("user %s must have a personal ledger", username)
	}
	return ledgerFixture{ledgerID: l.ID, userID: uid, token: tok, env: env}
}

// createSharedLedger 用某 token 建一个共同台账，返回台账 ID。
func createSharedLedger(t *testing.T, env *testEnv, token, name string) uint {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers", token,
		map[string]interface{}{"name": name})
	if w.Code != http.StatusOK {
		t.Fatalf("create shared ledger %s: %d %s", name, w.Code, w.Body.String())
	}
	id := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if id == 0 {
		t.Fatalf("created ledger id must not be 0")
	}
	return id
}

// addActiveMember 直接落一行正式成员关系（测试里用于构造权限场景）。
func addActiveMember(t *testing.T, env *testEnv, ledgerID, userID uint, perms ModulePerms) {
	t.Helper()
	stored, err := marshalPerms(perms)
	if err != nil {
		t.Fatalf("marshal perms: %v", err)
	}
	if err := env.db.Create(&LedgerMember{
		LedgerID: ledgerID, UserID: userID,
		Role: MemberRoleMember, Status: MemberStatusActive, Perms: stored,
	}).Error; err != nil {
		t.Fatalf("add member: %v", err)
	}
}

// assertFullModulePerms 断言全部模块都是 full。
func assertFullModulePerms(t *testing.T, perms ModulePerms, ctx string) {
	t.Helper()
	for _, k := range moduleKeys {
		if perms[k] != PermFull {
			t.Fatalf("%s: perms[%s] = %q, want full (got %v)", ctx, k, perms[k], perms)
		}
	}
}

// ---------------------------------------------------------------------------
// 1. 注册即得个人台账
// ---------------------------------------------------------------------------

// TestNewUserGetsPersonalLedger 新注册账号自动拥有个人台账：kind=personal、
// owner 是自己、成员行 role=owner/status=active、全部模块 full。
func TestNewUserGetsPersonalLedger(t *testing.T) {
	env := setupEnv(t)

	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "solo", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	uid := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	token := field(t, decodeBody(t, w), "data", "token").(string)

	l := personalLedgerOf(env.db, uid)
	if l == nil {
		t.Fatal("注册后必须自动获得一个个人台账（注册钩子 ensurePersonalLedger）")
	}
	if l.Kind != LedgerKindPersonal {
		t.Fatalf("personal ledger kind = %q, want %q", l.Kind, LedgerKindPersonal)
	}
	if l.OwnerUserID != uid {
		t.Fatalf("personal ledger owner = %d, want %d", l.OwnerUserID, uid)
	}
	if l.Name != PersonalLedgerName {
		t.Fatalf("personal ledger name = %q, want %q", l.Name, PersonalLedgerName)
	}
	m := activeMemberOf(env.db, uid, l.ID)
	if m == nil {
		t.Fatal("个人台账必须有一行 status=active 的成员关系")
	}
	if m.Role != MemberRoleOwner {
		t.Fatalf("personal member role = %q, want %q", m.Role, MemberRoleOwner)
	}
	assertFullModulePerms(t, m.LedgerPerms(), "personal ledger perms")

	// 自己的台账列表里能看到它。
	w = doJSON(env.r, http.MethodGet, "/api/rental/ledgers/mine", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ledgers/mine: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, o := range field(t, decodeBody(t, w), "data").([]interface{}) {
		m := o.(map[string]interface{})
		if uint(m["id"].(float64)) == l.ID {
			found = true
			if m["kind"] != LedgerKindPersonal || m["role"] != MemberRoleOwner || m["status"] != MemberStatusActive {
				t.Fatalf("personal ledger view = %v, want personal/owner/active", m)
			}
		}
	}
	if !found {
		t.Fatal("/ledgers/mine 必须包含自己的个人台账")
	}
}

// ---------------------------------------------------------------------------
// 2. 个人台账之间默认隔离
// ---------------------------------------------------------------------------

// TestPersonalLedgerIsolation 两个普通用户默认互相看不到对方的数据：
// 各自在个人台账里建房，互相列表为空、按 ID 直取 404、显式指定对方台账 403。
func TestPersonalLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	alice := newPersonalLedger(t, env, "alice")
	bob := newPersonalLedger(t, env, "bob")

	// alice 不带 ledger_id 建房 → 必须落在她自己的个人台账。
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms", alice.token, map[string]interface{}{
		"community": "A区", "room_no": "101", "default_rent": 1000,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create room: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var room Room
	if err := env.db.First(&room, roomID).Error; err != nil {
		t.Fatalf("load room: %v", err)
	}
	if room.LedgerID != alice.ledgerID {
		t.Fatalf("room ledger = %d, want alice personal %d", room.LedgerID, alice.ledgerID)
	}

	// alice 看得到自己的一间。
	if got := len(itemsOf(t, doJSON(env.r, http.MethodGet, "/api/rental/rooms", alice.token, nil))); got != 1 {
		t.Fatalf("alice rooms = %d, want 1", got)
	}
	// bob 的列表为空（不是别人的数据）。
	w = doJSON(env.r, http.MethodGet, "/api/rental/rooms", bob.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("bob rooms: %d %s", w.Code, w.Body.String())
	}
	if got := len(itemsOf(t, w)); got != 0 {
		t.Fatalf("bob rooms = %d, want 0（两个个人台账默认互不可见）", got)
	}

	// bob 按 ID 直取 alice 的房间 → 404（不泄露存在性）。
	w = doJSON(env.r, http.MethodGet, "/api/rental/rooms/"+itoa(roomID), bob.token, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob GET alice room = %d, want 404", w.Code)
	}
	w = doJSON(env.r, http.MethodPut, "/api/rental/rooms/"+itoa(roomID), bob.token,
		map[string]interface{}{"community": "A区", "room_no": "101", "notes": "hacked"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("bob PUT alice room = %d, want 404", w.Code)
	}
	var after Room
	if err := env.db.First(&after, roomID).Error; err != nil {
		t.Fatalf("reload room: %v", err)
	}
	if after.Notes == "hacked" {
		t.Fatal("跨台账写必须不能改动对方的房源")
	}

	// 显式指定对方的个人台账 → 403；指定自己的 → 200。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(alice.ledgerID), bob.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("bob explicit alice ledger = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(bob.ledgerID), bob.token, nil); w.Code != http.StatusOK {
		t.Fatalf("bob explicit own ledger = %d, want 200", w.Code)
	}
}

// TestChildRecordsInheritLedger 子记录必须继承父记录的台账，
// 否则构造请求就能把租户挂进别人的房源，绕过隔离。
func TestChildRecordsInheritLedger(t *testing.T) {
	env := setupEnv(t)
	a := newPersonalLedger(t, env, "landlord-a")
	b := newPersonalLedger(t, env, "landlord-b")

	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms", a.token, map[string]interface{}{
		"community": "A区", "room_no": "101",
	})
	roomA := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(env.r, http.MethodPost, "/api/rental/tenants", a.token, map[string]interface{}{
		"room_id": roomA, "name": "王五",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create tenant: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var tn Tenant
	if err := env.db.First(&tn, tenantID).Error; err != nil {
		t.Fatalf("load tenant: %v", err)
	}
	if tn.LedgerID != a.ledgerID {
		t.Fatalf("tenant ledger = %d, want %d (must inherit parent room's ledger)", tn.LedgerID, a.ledgerID)
	}

	// B 不能把租户挂到 A 的房源上。
	w = doJSON(env.r, http.MethodPost, "/api/rental/tenants", b.token, map[string]interface{}{
		"room_id": roomA, "name": "入侵租户",
	})
	if w.Code == http.StatusOK {
		t.Fatal("跨台账在他人的房源上建租户必须被拒绝")
	}
}

// ---------------------------------------------------------------------------
// 3. 共同台账的邀请闭环
// ---------------------------------------------------------------------------

// TestSharedLedgerInviteAcceptReject 建共同台账 → 邀请（pending）→ 接受后
// active 能看到数据；拒绝保持不可见。
func TestSharedLedgerInviteAcceptReject(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "owner")
	shared := createSharedLedger(t, env, owner.token, "A区三兄弟")

	guestID, guestTok := mkUser(t, env, "guest", UserStatusActive)
	rejectID, rejectTok := mkUser(t, env, "rejecter", UserStatusActive)

	// 管理者建房（共同台账里的业务数据）。
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), owner.token,
		map[string]interface{}{"community": "A区", "room_no": "101"})
	if w.Code != http.StatusOK {
		t.Fatalf("owner create room in shared: %d %s", w.Code, w.Body.String())
	}

	// 邀请两人：都先落 pending。
	for _, name := range []string{"guest", "rejecter"} {
		w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(shared)+"/members",
			owner.token, map[string]interface{}{"username": name})
		if w.Code != http.StatusOK {
			t.Fatalf("invite %s: %d %s", name, w.Code, w.Body.String())
		}
		if got := field(t, decodeBody(t, w), "data", "status").(string); got != MemberStatusPending {
			t.Fatalf("invite %s status = %q, want pending", name, got)
		}
	}
	if m := memberOf(env.db, guestID, shared); m == nil || m.Status != MemberStatusPending {
		t.Fatalf("guest member row = %+v, want status=pending", m)
	}
	if m := memberOf(env.db, rejectID, shared); m == nil || m.Status != MemberStatusPending {
		t.Fatalf("rejecter member row = %+v, want status=pending", m)
	}

	// 待接受时：我的台账列表里带 pending 标记，但访问该台账数据 403。
	w = doJSON(env.r, http.MethodGet, "/api/rental/ledgers/mine", guestTok, nil)
	foundPending := false
	for _, o := range decodeBody(t, w)["data"].([]interface{}) {
		m := o.(map[string]interface{})
		if uint(m["id"].(float64)) == shared {
			foundPending = true
			if m["status"] != MemberStatusPending {
				t.Fatalf("pending invite status = %v, want pending", m["status"])
			}
		}
	}
	if !foundPending {
		t.Fatal("待接受的邀请必须出现在 /ledgers/mine 里（前端要显示「接受/拒绝」）")
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), guestTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("pending guest data access = %d, want 403", w.Code)
	}
	if got := len(itemsOf(t, doJSON(env.r, http.MethodGet, "/api/rental/rooms", guestTok, nil))); got != 0 {
		t.Fatalf("pending guest rooms = %d, want 0（未接受就不该看到该台账数据）", got)
	}

	// guest 接受 → active，能看到该台账的数据。
	w = doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(shared)+"/invitations/respond",
		guestTok, map[string]interface{}{"accept": true})
	if w.Code != http.StatusOK {
		t.Fatalf("accept invite: %d %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "status").(string); got != MemberStatusActive {
		t.Fatalf("accept status = %q, want active", got)
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), guestTok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("accepted guest data access = %d %s, want 200", w.Code, w.Body.String())
	}
	if got := len(itemsOf(t, w)); got != 1 {
		t.Fatalf("accepted guest rooms = %d, want 1", got)
	}

	// rejecter 拒绝 → rejected，仍不可见，且不再出现在 /ledgers/mine。
	w = doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(shared)+"/invitations/respond",
		rejectTok, map[string]interface{}{"accept": false})
	if w.Code != http.StatusOK {
		t.Fatalf("reject invite: %d %s", w.Code, w.Body.String())
	}
	if got := field(t, decodeBody(t, w), "data", "status").(string); got != MemberStatusRejected {
		t.Fatalf("reject status = %q, want rejected", got)
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), rejectTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("rejected data access = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/ledgers/mine", rejectTok, nil)
	for _, o := range decodeBody(t, w)["data"].([]interface{}) {
		if uint(o.(map[string]interface{})["id"].(float64)) == shared {
			t.Fatal("已拒绝的邀请不应再出现在 /ledgers/mine（只保留自己的个人台账）")
		}
	}

	// 没有待处理邀请时再次 respond → 400。
	w = doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(shared)+"/invitations/respond",
		guestTok, map[string]interface{}{"accept": true})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("respond without pending invite = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 4. 管理者与成员的边界
// ---------------------------------------------------------------------------

// TestLedgerMemberManagementGuards 只有管理者能邀请/设权限/移除/改名/删除；
// 成员只能看、按权限写；管理者不能被移除。
func TestLedgerMemberManagementGuards(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "boss")
	shared := createSharedLedger(t, env, owner.token, "管理边界台账")

	memberID, memberTok := mkUser(t, env, "worker", UserStatusActive)
	thirdID, _ := mkUser(t, env, "third", UserStatusActive)
	addActiveMember(t, env, shared, memberID, ModulePerms{ModuleRooms: PermView})

	// 成员无权做的管理动作。
	w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(shared)+"/members",
		memberTok, map[string]interface{}{"username": "third"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("member invite = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(thirdID),
		memberTok, map[string]interface{}{"perms": map[string]string{ModuleRooms: PermFull}})
	if w.Code != http.StatusForbidden {
		t.Fatalf("member set others perms = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(owner.userID),
		memberTok, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member remove owner = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(shared), memberTok,
		map[string]interface{}{"name": "改名试试"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("member rename = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(shared), memberTok, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member delete ledger = %d, want 403", w.Code)
	}

	// 成员按 view 权限：能看，不能写。
	w = doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), memberTok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("member read = %d, want 200", w.Code)
	}
	w = doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), memberTok,
		map[string]interface{}{"community": "A区", "room_no": "999"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("view member write = %d, want 403", w.Code)
	}

	// 管理者设权限为 edit → 成员能写。
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(memberID),
		owner.token, map[string]interface{}{"perms": map[string]string{ModuleRooms: PermEdit}})
	if w.Code != http.StatusOK {
		t.Fatalf("owner set perms: %d %s", w.Code, w.Body.String())
	}
	w = doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), memberTok,
		map[string]interface{}{"community": "A区", "room_no": "999"})
	if w.Code != http.StatusOK {
		t.Fatalf("edit member write = %d %s, want 200", w.Code, w.Body.String())
	}
	var room Room
	env.db.Where("room_no = ?", "999").First(&room)
	if room.LedgerID != shared {
		t.Fatalf("member-created room ledger = %d, want shared %d", room.LedgerID, shared)
	}

	// 管理者不能被移除（自己移自己也不行）。
	w = doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(owner.userID),
		owner.token, nil)
	if w.Code == http.StatusOK {
		t.Fatal("台账管理者不能被移除")
	}

	// 管理者改名/删成员：能。
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(shared), owner.token,
		map[string]interface{}{"name": "新名字"})
	if w.Code != http.StatusOK {
		t.Fatalf("owner rename = %d %s", w.Code, w.Body.String())
	}
	w = doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(memberID),
		owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("owner remove member = %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), memberTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("removed member access = %d, want 403", w.Code)
	}

	// 成员可以主动退出。
	addActiveMember(t, env, shared, thirdID, ModulePerms{ModuleRooms: PermView})
	w = doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(thirdID),
		ledgerTestToken(t, env, thirdID, "third"), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("member self-exit = %d %s", w.Code, w.Body.String())
	}
	if memberOf(env.db, thirdID, shared) != nil {
		t.Fatal("主动退出后成员关系必须删除")
	}
}

// TestPerLedgerPermissionsDiffer 同一用户在两个台账可有不同权限，
// 且**写操作按本次请求的台账判定**，不能被另一台账的更高权限带过去。
func TestPerLedgerPermissionsDiffer(t *testing.T) {
	env := setupEnv(t)
	worker := newPersonalLedger(t, env, "worker") // 个人台账：全模块 full
	// 共同台账由管理员创建（worker 只能作为被邀请的成员，权限可单独设）。
	viewOnly := createSharedLedger(t, env, env.token, "只读台账")
	addActiveMember(t, env, viewOnly, worker.userID, ModulePerms{ModuleRooms: PermView})

	// 两个台账的权限在 /ledgers/mine 里各算各的。
	w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/mine", worker.token, nil)
	arr, ok := decodeBody(t, w)["data"].([]interface{})
	if !ok {
		t.Fatalf("ledgers/mine data not an array: %s", w.Body.String())
	}
	permsByLedger := map[uint]ModulePerms{}
	for _, o := range arr {
		m := o.(map[string]interface{})
		id := uint(m["id"].(float64))
		p := ModulePerms{}
		if raw, ok := m["perms"].(map[string]interface{}); ok {
			for k, v := range raw {
				p[k] = v.(string)
			}
		}
		permsByLedger[id] = p
	}
	if permsByLedger[worker.ledgerID][ModuleRooms] != PermFull {
		t.Fatalf("personal ledger rooms perm = %q, want full", permsByLedger[worker.ledgerID][ModuleRooms])
	}
	if permsByLedger[viewOnly][ModuleRooms] != PermView {
		t.Fatalf("view-only ledger rooms perm = %q, want view (per-ledger perms must not be merged)",
			permsByLedger[viewOnly][ModuleRooms])
	}

	// ★ 核心断言：在只有 view 的台账建房 → 403。
	w = doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(viewOnly), worker.token,
		map[string]interface{}{"community": "C区", "room_no": "999"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("write to view-only ledger = %d %s, want 403 (per-ledger permission must gate writes)",
			w.Code, w.Body.String())
	}
	var cnt int64
	env.db.Model(&Room{}).Where("org_id = ? AND room_no = ?", viewOnly, "999").Count(&cnt)
	if cnt != 0 {
		t.Fatal("只读台账不应被写入新房源")
	}

	// 在 full 的个人台账建房 → 200，且归属落在个人台账。
	w = doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(worker.ledgerID), worker.token,
		map[string]interface{}{"community": "C区", "room_no": "999"})
	if w.Code != http.StatusOK {
		t.Fatalf("write to full ledger = %d %s, want 200", w.Code, w.Body.String())
	}
	newID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var r Room
	if err := env.db.First(&r, newID).Error; err != nil {
		t.Fatalf("load new room: %v", err)
	}
	if r.LedgerID != worker.ledgerID {
		t.Fatalf("new room ledger = %d, want %d", r.LedgerID, worker.ledgerID)
	}
}

// TestWriteWithoutLedgerLandsInPersonal 不传 ledger_id 时写入目标是**个人台账**
// （绝不写进别人的共同台账）；显式传共同台账则按该台账权限判定。
func TestWriteWithoutLedgerLandsInPersonal(t *testing.T) {
	env := setupEnv(t)
	worker := newPersonalLedger(t, env, "worker")
	shared := createSharedLedger(t, env, env.token, "共同台账")
	addActiveMember(t, env, shared, worker.userID, ModulePerms{ModuleRooms: PermFull})

	// 同时属于个人台账与共同台账，不指定时写个人台账。
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms", worker.token, map[string]interface{}{
		"community": "A区", "room_no": "101",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("write without ledger_id = %d %s, want 200", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var r Room
	if err := env.db.First(&r, roomID).Error; err != nil {
		t.Fatalf("load room: %v", err)
	}
	if r.LedgerID != worker.ledgerID {
		t.Fatalf("implicit write ledger = %d, want personal %d（不能写进共同台账）", r.LedgerID, worker.ledgerID)
	}

	// 显式指定共同台账 → 落在共同台账。
	w = doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), worker.token,
		map[string]interface{}{"community": "A区", "room_no": "202"})
	if w.Code != http.StatusOK {
		t.Fatalf("explicit shared write = %d %s", w.Code, w.Body.String())
	}
	sharedRoomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	var sr Room
	if err := env.db.First(&sr, sharedRoomID).Error; err != nil {
		t.Fatalf("load shared room: %v", err)
	}
	if sr.LedgerID != shared {
		t.Fatalf("explicit write ledger = %d, want shared %d", sr.LedgerID, shared)
	}
}

// TestNonMemberWriteForbidden 非成员显式指定台账 → 403（不能靠构造 ID 写入）。
func TestNonMemberWriteForbidden(t *testing.T) {
	env := setupEnv(t)
	outsider := newPersonalLedger(t, env, "outsider")
	owner := newPersonalLedger(t, env, "owner")
	shared := createSharedLedger(t, env, owner.token, "别人的台账")

	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), outsider.token,
		map[string]interface{}{"community": "A区", "room_no": "101"})
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-member write = %d, want 403", w.Code)
	}
	var cnt int64
	env.db.Model(&Room{}).Where("org_id = ?", shared).Count(&cnt)
	if cnt != 0 {
		t.Fatal("非成员不得往台账里写数据")
	}
}

// ---------------------------------------------------------------------------
// 5. 超管在业务数据上没有特权
// ---------------------------------------------------------------------------

// TestSuperAdminHasNoDataPrivilege 超级管理员不是某台账成员时，
// 读该台账数据 403——这是本次重构的关键行为。
func TestSuperAdminHasNoDataPrivilege(t *testing.T) {
	env := setupEnv(t) // env.token 是首个注册用户（role=admin）的令牌
	owner := newPersonalLedger(t, env, "owner")
	shared := createSharedLedger(t, env, owner.token, "与超管无关的台账")
	if w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), owner.token,
		map[string]interface{}{"community": "A区", "room_no": "101"}); w.Code != http.StatusOK {
		t.Fatalf("owner create room: %d %s", w.Code, w.Body.String())
	}

	// 管理员的台账列表里没有这个台账。
	w := env.do(http.MethodGet, "/api/rental/ledgers/mine", nil)
	for _, o := range decodeBody(t, w)["data"].([]interface{}) {
		if uint(o.(map[string]interface{})["id"].(float64)) == shared {
			t.Fatal("超管的 /ledgers/mine 不该包含别人的台账")
		}
	}

	// 读数据、成员、设置一律 403。
	if w := env.do(http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), nil); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin read foreign ledger = %d, want 403", w.Code)
	}
	if w := env.do(http.MethodGet, "/api/rental/bills?ledger_id="+itoa(shared), nil); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin read foreign bills = %d, want 403", w.Code)
	}
	if w := env.do(http.MethodGet, "/api/rental/ledgers/"+itoa(shared)+"/members", nil); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin list foreign members = %d, want 403", w.Code)
	}
	if w := env.do(http.MethodGet, "/api/rental/ledgers/"+itoa(shared)+"/settings", nil); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin read foreign settings = %d, want 403", w.Code)
	}
	if w := env.do(http.MethodPut, "/api/rental/ledgers/"+itoa(shared), map[string]interface{}{"name": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin rename foreign ledger = %d, want 403", w.Code)
	}
	if w := env.do(http.MethodDelete, "/api/rental/ledgers/"+itoa(shared), nil); w.Code != http.StatusForbidden {
		t.Fatalf("superadmin delete foreign ledger = %d, want 403", w.Code)
	}
	// 但管理员自己的个人台账照常可用。
	if w := env.do(http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(env.personalLedger), nil); w.Code != http.StatusOK {
		t.Fatalf("superadmin own ledger = %d, want 200", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 6. 删除保护
// ---------------------------------------------------------------------------

// TestLedgerDeleteGuards 删除台账的保护：个人台账不可删；有业务数据的共同台账
// 不可删；无数据的共同台账可删；非管理者不可删。
func TestLedgerDeleteGuards(t *testing.T) {
	env := setupEnv(t)

	// 个人台账不可删（自己也不行）。
	if w := env.do(http.MethodDelete, "/api/rental/ledgers/"+itoa(env.personalLedger), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("delete personal ledger = %d %s, want 400", w.Code, w.Body.String())
	}
	owner := newPersonalLedger(t, env, "owner")
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(owner.ledgerID), owner.token, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("delete own personal ledger = %d, want 400", w.Code)
	}

	// 空的共同台账可以删。
	empty := createSharedLedger(t, env, owner.token, "临时台账")
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(empty), owner.token, nil); w.Code != http.StatusOK {
		t.Fatalf("delete empty shared ledger = %d %s, want 200", w.Code, w.Body.String())
	}

	// 有数据的共同台账不可删（4xx 且提示明确，不能是 5xx）。
	withData := createSharedLedger(t, env, owner.token, "有房台账")
	if w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(withData), owner.token,
		map[string]interface{}{"community": "A区", "room_no": "101"}); w.Code != http.StatusOK {
		t.Fatalf("seed room: %d %s", w.Code, w.Body.String())
	}
	w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(withData), owner.token, nil)
	if w.Code == http.StatusOK {
		t.Fatal("有数据的台账不允许删除")
	}
	if w.Code >= 500 {
		t.Fatalf("delete ledger with data = %d, want a 4xx with a clear message, not a 5xx", w.Code)
	}
	if !contains(w.Body.String(), "数据") {
		t.Fatalf("delete-with-data message = %s, want it to explain the data guard", w.Body.String())
	}

	// 非管理者删除 → 403。
	memberID, memberTok := mkUser(t, env, "member2", UserStatusActive)
	addActiveMember(t, env, withData, memberID, ModulePerms{ModuleRooms: PermView})
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(withData), memberTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-owner delete = %d, want 403", w.Code)
	}
}

// TestLedgerDeleteReleasesMembers 删除无数据的共同台账时，成员关系一并清理
// （否则会留下悬空成员行）。
func TestLedgerDeleteReleasesMembers(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "owner")
	memberID, _ := mkUser(t, env, "member3", UserStatusActive)
	ledger := createSharedLedger(t, env, owner.token, "待删台账")
	addActiveMember(t, env, ledger, memberID, ModulePerms{ModuleRooms: PermView})

	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledger), owner.token, nil); w.Code != http.StatusOK {
		t.Fatalf("delete ledger: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&LedgerMember{}).Where("org_id = ?", ledger).Count(&n)
	if n != 0 {
		t.Fatalf("member rows after ledger delete = %d, want 0", n)
	}
	if ledgerExists(env.db, ledger) {
		t.Fatal("台账行必须被删除")
	}
}

// ---------------------------------------------------------------------------
// 7. 注册审核
// ---------------------------------------------------------------------------

// TestRegistrationNeedsApproval 注册审核开关的行为：开启时新账号落「待审核」，
// **能登录**但业务接口一律 403，白名单接口放行。
func TestRegistrationNeedsApproval(t *testing.T) {
	env := setupEnv(t)
	if err := sysconfig.UpdateConfig(env.db, "register_need_approval", "true", 0); err != nil {
		t.Fatalf("enable approval: %v", err)
	}

	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "newbie", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	body := decodeBody(t, w)
	if got := uint(field(t, body, "data", "user", "status").(float64)); got != uint(UserStatusPending) {
		t.Fatalf("new user status = %d, want %d (pending)", got, UserStatusPending)
	}
	token := field(t, body, "data", "token").(string)

	// 待审核**必须也能登录**。
	w = doJSON(env.r, http.MethodPost, "/api/auth/login", "", map[string]interface{}{
		"username": "newbie", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("pending login = %d %s, want 200", w.Code, w.Body.String())
	}

	// 业务接口一律 403，且提示语点明「审核」。
	for _, path := range []string{"/api/rental/rooms", "/api/rental/tenants", "/api/rental/bills"} {
		w := doJSON(env.r, http.MethodGet, path, token, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("pending GET %s = %d, want 403", path, w.Code)
		}
		if msg := field(t, decodeBody(t, w), "message").(string); !contains(msg, "审核") {
			t.Fatalf("pending 403 message = %q, want it to mention 审核", msg)
		}
	}

	// 白名单放行：待审核用户要能查自己的身份、台账与审核状态。
	for _, path := range []string{"/api/auth/me", "/api/rental/ledgers/mine", "/api/rental/users/me/approval"} {
		if w := doJSON(env.r, http.MethodGet, path, token, nil); w.Code != http.StatusOK {
			t.Fatalf("pending whitelist GET %s = %d %s, want 200", path, w.Code, w.Body.String())
		}
	}
}

// TestApprovedUserCanUseBusiness 审核通过（只置 status=1，不再分配台账）后，
// 用户就能在自己的个人台账里正常使用业务接口。
func TestApprovedUserCanUseBusiness(t *testing.T) {
	env := setupEnv(t)
	if err := sysconfig.UpdateConfig(env.db, "register_need_approval", "true", 0); err != nil {
		t.Fatalf("enable approval: %v", err)
	}
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "newbie", "password": "secret123",
	})
	uid := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	token := field(t, decodeBody(t, w), "data", "token").(string)

	// 审批通过：body 里即便带了 ledger_id/perms 也应被忽略（新接口不再接受它们）。
	w = env.do(http.MethodPost, "/api/rental/users/"+itoa(uid)+"/approve", map[string]interface{}{
		"ledger_id": 12345,
		"perms":     map[string]string{ModuleRooms: PermView},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if got := uint(field(t, decodeBody(t, w), "data", "status").(float64)); got != uint(UserStatusActive) {
		t.Fatalf("approved status = %d, want %d", got, UserStatusActive)
	}
	if l := personalLedgerOf(env.db, uid); l == nil {
		t.Fatal("审批通过后必须有个人台账")
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms", token, nil); w.Code != http.StatusOK {
		t.Fatalf("approved GET rooms = %d %s, want 200", w.Code, w.Body.String())
	}
	// 重复审批 → 400。
	if w := env.do(http.MethodPost, "/api/rental/users/"+itoa(uid)+"/approve", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("re-approve = %d, want 400", w.Code)
	}
}

// TestPendingUserSessionSurvivesRefresh 待审核用户的登录态必须在刷新后仍然成立。
func TestPendingUserSessionSurvivesRefresh(t *testing.T) {
	env := setupEnv(t)
	if err := sysconfig.UpdateConfig(env.db, "register_need_approval", "true", 0); err != nil {
		t.Fatalf("enable approval: %v", err)
	}
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "waiting", "password": "secret123",
	})
	token := field(t, decodeBody(t, w), "data", "token").(string)

	w = doJSON(env.r, http.MethodGet, "/api/auth/check", token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("auth/check = %d %s, want 200", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["authenticated"] != true {
		t.Fatalf("pending user auth/check authenticated = %v, want true", data["authenticated"])
	}
	u := data["user"].(map[string]interface{})
	if got := uint(u["status"].(float64)); got != uint(UserStatusPending) {
		t.Fatalf("auth/check user.status = %d, want %d", got, UserStatusPending)
	}
}

// ---------------------------------------------------------------------------
// 8. 按台账的租房设置
// ---------------------------------------------------------------------------

// TestLedgerSettingsIsolatedPerLedger 租房设置按台账隔离：A 台账改水价不影响 B，
// 未设置的台账读系统级值；「清空覆盖」后回到系统级；非成员不可读、非管理者不可写。
func TestLedgerSettingsIsolatedPerLedger(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "settings-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "B台账")

	w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{"rental_water_price": "9.99"}})
	if w.Code != http.StatusOK {
		t.Fatalf("set ledger A settings: %d %s", w.Code, w.Body.String())
	}

	effectiveOf := func(ledgerID uint, token string) string {
		w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", token, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("get settings ledger %d: %d %s", ledgerID, w.Code, w.Body.String())
		}
		eff := field(t, decodeBody(t, w), "data", "effective").(map[string]interface{})
		return eff["rental_water_price"].(string)
	}
	if got := effectiveOf(ledgerA, owner.token); got != "9.99" {
		t.Fatalf("ledger A water price = %q, want 9.99", got)
	}
	if got := effectiveOf(ledgerB, owner.token); got != "5.00" {
		t.Fatalf("ledger B water price = %q, want 5.00 (must fall back to system)", got)
	}

	// 清空 A 的覆盖 → 回到系统级。
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerA)+"/settings", owner.token, nil); w.Code != http.StatusOK {
		t.Fatalf("reset ledger A settings: %d %s", w.Code, w.Body.String())
	}
	if got := effectiveOf(ledgerA, owner.token); got != "5.00" {
		t.Fatalf("ledger A after reset = %q, want 5.00", got)
	}

	// 成员只能读 A，不能读 B，也不能改 A。
	memberID, memberTok := mkUser(t, env, "settings-member", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleBills: PermView})
	if got := effectiveOf(ledgerA, memberTok); got != "5.00" {
		t.Fatalf("member read A = %q, want 5.00", got)
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerB)+"/settings", memberTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member read B settings = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/settings", memberTok,
		map[string]interface{}{"settings": map[string]string{"rental_water_price": "1.00"}}); w.Code != http.StatusForbidden {
		t.Fatalf("member write A settings = %d, want 403", w.Code)
	}
}

// TestLedgerSettingsValidation 非法键与非法取值必须被拒。
func TestLedgerSettingsValidation(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "validation-owner")
	ledgerID := createSharedLedger(t, env, owner.token, "校验台账")

	w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{"rental_not_a_key": "1"}})
	if w.Code == http.StatusOK {
		t.Fatal("未登记的配置键必须被拒绝")
	}
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerID)+"/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{"rental_pay_day": "not-a-number"}})
	if w.Code == http.StatusOK {
		t.Fatal("非法取值必须被拒绝（复用框架类型校验）")
	}
	w = doJSON(env.r, http.MethodPut, "/api/rental/ledgers/99999/settings", owner.token,
		map[string]interface{}{"settings": map[string]string{"rental_water_price": "1.00"}})
	if w.Code == http.StatusOK {
		t.Fatal("不存在的台账必须被拒绝")
	}
}

// ---------------------------------------------------------------------------
// 9. 账单部分更新
// ---------------------------------------------------------------------------

// TestBillUpdatePartialKeepsUnsentFields 编辑账单是**部分更新**：
// 未传的字段必须保留原值，不能被零值覆盖。
func TestBillUpdatePartialKeepsUnsentFields(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("P1", 1000, 0)
	env.seedTenant(roomID, "部分更新租户")
	billID := env.createBill("P1", "2026-09")

	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID),
		map[string]interface{}{"amount": 600})
	if w.Code != http.StatusOK {
		t.Fatalf("pay: %d %s", w.Code, w.Body.String())
	}
	var before Bill
	if err := env.db.First(&before, billID).Error; err != nil {
		t.Fatalf("load bill: %v", err)
	}
	if before.PaidAmount <= 0 {
		t.Fatalf("precondition: paid amount = %v, want > 0", before.PaidAmount)
	}
	if before.WaterPrice <= 0 {
		t.Fatalf("precondition: water price = %v, want > 0", before.WaterPrice)
	}

	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID),
		map[string]interface{}{
			"room_id": roomID, "period": "2026-09",
			"elec_last": 0, "elec_now": 50,
		})
	if w.Code != http.StatusOK {
		t.Fatalf("partial update: %d %s", w.Code, w.Body.String())
	}

	var after Bill
	if err := env.db.First(&after, billID).Error; err != nil {
		t.Fatalf("reload bill: %v", err)
	}
	if !almostEqual(after.PaidAmount, before.PaidAmount) {
		t.Fatalf("已收金额被未传字段清零：%v -> %v（收款流水还在，账单却显示未收）",
			before.PaidAmount, after.PaidAmount)
	}
	if !almostEqual(after.WaterPrice, before.WaterPrice) {
		t.Fatalf("水价被未传字段清零：%v -> %v", before.WaterPrice, after.WaterPrice)
	}
	if !almostEqual(after.Rent, before.Rent) {
		t.Fatalf("租金被未传字段清零：%v -> %v", before.Rent, after.Rent)
	}
	if after.ElecNow != 50 {
		t.Fatalf("显式传入的电量读数未生效：%v", after.ElecNow)
	}
	if after.PaidAt == nil {
		t.Fatal("已收金额仍在，PaidAt 不该被清空")
	}
	if after.Status == billStatusUnpaid {
		t.Fatalf("状态被改回未收（应仍为部分收款）：%s", after.Status)
	}

	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID),
		map[string]interface{}{
			"room_id": roomID, "period": "2026-09", "paid_amount": 0,
		})
	if w.Code != http.StatusOK {
		t.Fatalf("explicit zero: %d %s", w.Code, w.Body.String())
	}
	var zeroed Bill
	if err := env.db.First(&zeroed, billID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if zeroed.PaidAmount != 0 {
		t.Fatalf("显式传 0 应清零，实际 %v", zeroed.PaidAmount)
	}
	if zeroed.PaidAt != nil {
		t.Fatal("已收清零后 PaidAt 应为空")
	}
}
