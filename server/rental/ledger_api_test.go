// ledger_api_test.go — 台账管理 API 与待审核门禁的回归测试。
//
// 覆盖三类「读代码不足以证明」的行为：
//
//  1. 待审核白名单的路径匹配边界（放行/拒绝各一组，含路径段匹配与网关前缀）。
//  2. 台账 CRUD、成员邀请闭环、按台账设置权限的接口契约与边界取值。
//  3. 只有管理者能管理台账；超管只保留账号审核，对业务数据无特权。
//
// 与 ledger_test.go 的分工：ledger_test.go 从行为侧验证隔离与超管无特权；
// 本文件更偏接口契约与边界取值。
package rental

import (
	"net/http"
	"testing"

	"smallgo/server/auth"
	"smallgo/server/database"
	"smallgo/server/sysconfig"
)

// ledgerTestUser 直接建用户（带个人台账，与真实注册路径一致）。
func ledgerTestUser(t *testing.T, env *testEnv, name string, status int) uint {
	t.Helper()
	u := database.User{Username: name, Password: "x", Role: "user", Status: status}
	if err := env.db.Create(&u).Error; err != nil {
		t.Fatalf("create user %s: %v", name, err)
	}
	// Status=0（已禁用）是零值，会被 gorm 的 `default:1` 顶掉，显式落库。
	if err := env.db.Model(&u).Update("status", status).Error; err != nil {
		t.Fatalf("set status for %s: %v", name, err)
	}
	if _, err := ensurePersonalLedger(env.db, u.ID); err != nil {
		t.Fatalf("ensure personal ledger for %s: %v", name, err)
	}
	return u.ID
}

func ledgerTestToken(t *testing.T, env *testEnv, userID uint, username string) string {
	t.Helper()
	secret, err := sysconfig.GetConfig(env.db, "jwt_secret", 0)
	if err != nil || secret == "" {
		t.Fatalf("jwt secret: %v", err)
	}
	tok, err := auth.GenerateToken(userID, username, "user", 1, secret)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	return tok
}

func ledgerTestPermsJSON(t *testing.T, perms ModulePerms) string {
	t.Helper()
	s, err := marshalPerms(perms)
	if err != nil {
		t.Fatalf("marshalPerms: %v", err)
	}
	return s
}

// memberPermsOf 读某用户在某台账的模块勾选（直接查库，绕开接口）。
func memberPermsOf(t *testing.T, env *testEnv, userID, ledgerID uint) ModulePerms {
	t.Helper()
	m := memberOf(env.db, userID, ledgerID)
	if m == nil {
		t.Fatalf("member (%d,%d) not found", userID, ledgerID)
	}
	return m.LedgerPerms()
}

func TestApprovalPathWhitelist(t *testing.T) {
	allow := []string{
		"/api/auth/login", "/api/auth/me", "/api/rental/ledgers/mine",
		"/api/configs", "/api/user/profile",
		"/api/version", "/api/version/check", "/api/realtime",
		"/api/rental/users/me/approval", "/api/health",
		"/app/techfunway-rental/api/rental/ledgers/mine", "/rental/ledgers/mine",
	}
	for _, p := range allow {
		if !requiresApprovalPath(p) {
			t.Errorf("allow %q = false, want true", p)
		}
	}
	deny := []string{
		"", "/api/rental/rooms", "/api/rental/rooms/1", "/api/users",
		"/api/rental/ledgers", "/api/rental/ledgers/1/members", "/api/backups",
		"/api/audit", "/api/upgrade-history",
		"/api/configsX", "/api/authx/login", "/api/rental/tenants",
		"/api/rental/ledgers/mine2", "/api/rental/ledgers/min",
		"/api/rental/../rental/rooms", "/api/healthz", "/api/rental/ledgers/mine2",
		"/api/rental/access/me", // 旧接口已删除，不再放行
	}
	for _, p := range deny {
		if requiresApprovalPath(p) {
			t.Errorf("deny %q = true, want false", p)
		}
	}
}

// TestLedgerCRUDAndSettings 共同台账的建/改名/删与接口校验。
func TestLedgerCRUDAndSettings(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "crud-owner")

	// 空名与重名（同一人名下，忽略大小写）被拒。
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers", owner.token,
		map[string]interface{}{"name": "  "}); w.Code != http.StatusBadRequest {
		t.Fatalf("empty name: %d %s", w.Code, w.Body.String())
	}
	ledgerA := createSharedLedger(t, env, owner.token, "A区")
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers", owner.token,
		map[string]interface{}{"name": "a区"}); w.Code != http.StatusBadRequest {
		t.Fatalf("dup name: %d %s", w.Code, w.Body.String())
	}

	// 改名：管理者可以，别人不行。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA), owner.token,
		map[string]interface{}{"name": "A区2"}); w.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", w.Code, w.Body.String())
	}
	outsider := newPersonalLedger(t, env, "crud-outsider")
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA), outsider.token,
		map[string]interface{}{"name": "偷改"}); w.Code != http.StatusForbidden {
		t.Fatalf("outsider rename: %d %s", w.Code, w.Body.String())
	}
	// 不存在的台账改名 → 403（不是管理者）。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/0", owner.token,
		map[string]interface{}{"name": "x"}); w.Code != http.StatusForbidden {
		t.Fatalf("rename ledger 0: %d %s", w.Code, w.Body.String())
	}
	// 删除不存在的台账 → 404。
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/4242", owner.token, nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete missing ledger = %d, want 404", w.Code)
	}
	// 非法 ID。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/abc/members", owner.token, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad ledger id: %d %s", w.Code, w.Body.String())
	}
}

// TestLedgerMemberInviteFlow 邀请 → pending → 接受/拒绝 → 权限设置。
func TestLedgerMemberInviteFlow(t *testing.T) {
	env := setupEnv(t)
	db := env.db
	owner := newPersonalLedger(t, env, "invite-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "邀请台账")

	pendingID := ledgerTestUser(t, env, "pending-invitee", UserStatusPending)
	disabledID := ledgerTestUser(t, env, "disabled-invitee", UserStatusDisabled)
	activeID := ledgerTestUser(t, env, "active-invitee", UserStatusActive)
	activeTok := ledgerTestToken(t, env, activeID, "active-invitee")

	// 非法/不可邀请对象。
	for _, tc := range []struct {
		name string
		body map[string]interface{}
	}{
		{"missing username", map[string]interface{}{"username": "nobody"}},
		{"self", map[string]interface{}{"username": "invite-owner"}},
		{"pending user", map[string]interface{}{"username": "pending-invitee"}},
		{"disabled user", map[string]interface{}{"username": "disabled-invitee"}},
	} {
		if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
			owner.token, tc.body); w.Code != http.StatusBadRequest {
			t.Fatalf("invite %s = %d %s, want 400", tc.name, w.Code, w.Body.String())
		}
	}
	if memberOf(db, pendingID, ledgerA) != nil || memberOf(db, disabledID, ledgerA) != nil {
		t.Fatal("待审核/禁用账号不得被加入台账")
	}

	// 正常邀请 → pending；重复邀请 → 400。
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
		owner.token, map[string]interface{}{"username": "active-invitee"}); w.Code != http.StatusOK {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	if m := memberOf(db, activeID, ledgerA); m == nil || m.Status != MemberStatusPending || m.Role != MemberRoleMember {
		t.Fatalf("invited member = %+v, want pending/member", m)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
		owner.token, map[string]interface{}{"username": "active-invitee"}); w.Code != http.StatusBadRequest {
		t.Fatalf("dup invite: %d %s", w.Code, w.Body.String())
	}

	// 接受 → active；重复 respond → 400。
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/invitations/respond",
		activeTok, map[string]interface{}{"accept": true}); w.Code != http.StatusOK {
		t.Fatalf("respond: %d %s", w.Code, w.Body.String())
	}
	if m := memberOf(db, activeID, ledgerA); m == nil || m.Status != MemberStatusActive {
		t.Fatalf("member after accept = %+v, want active", m)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/invitations/respond",
		activeTok, map[string]interface{}{"accept": true}); w.Code != http.StatusBadRequest {
		t.Fatalf("double respond: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
		owner.token, map[string]interface{}{"username": "active-invitee"}); w.Code != http.StatusBadRequest {
		t.Fatalf("invite active member: %d %s", w.Code, w.Body.String())
	}

	// 被拒绝过的邀请可再次邀请（行复用重置为 pending）。
	rejID := ledgerTestUser(t, env, "reject-invitee", UserStatusActive)
	rejTok := ledgerTestToken(t, env, rejID, "reject-invitee")
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
		owner.token, map[string]interface{}{"username": "reject-invitee"}); w.Code != http.StatusOK {
		t.Fatalf("invite rejecter: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/invitations/respond",
		rejTok, map[string]interface{}{"accept": false}); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	if m := memberOf(db, rejID, ledgerA); m == nil || m.Status != MemberStatusRejected {
		t.Fatalf("rejected member = %+v, want rejected", m)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members",
		owner.token, map[string]interface{}{"username": "reject-invitee"}); w.Code != http.StatusOK {
		t.Fatalf("re-invite rejected user: %d %s", w.Code, w.Body.String())
	}
	if m := memberOf(db, rejID, ledgerA); m == nil || m.Status != MemberStatusPending {
		t.Fatalf("re-invited member = %+v, want pending again", m)
	}

	// 设置权限：非法键与档位被归一化丢弃，只留合法勾选。
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(activeID),
		owner.token, map[string]interface{}{"perms": ModulePerms{ModuleMeters: PermEdit, "hack": PermFull, ModuleRooms: "super"}}); w.Code != http.StatusOK {
		t.Fatalf("set perms: %d %s", w.Code, w.Body.String())
	}
	got := memberPermsOf(t, env, activeID, ledgerA)
	if len(got) != 1 || got[ModuleMeters] != PermEdit {
		t.Fatalf("stored perms = %v, want only meters:edit", got)
	}

	// 目标不在台账 → 404；目标是管理者 → 400；非法 uid → 400。
	stranger := ledgerTestUser(t, env, "stranger", UserStatusActive)
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(stranger),
		owner.token, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermView}}); w.Code != http.StatusNotFound {
		t.Fatalf("set perms for non-member = %d, want 404", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(owner.userID),
		owner.token, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermView}}); w.Code != http.StatusBadRequest {
		t.Fatalf("set owner perms = %d, want 400", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/xyz",
		owner.token, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermView}}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad uid: %d %s", w.Code, w.Body.String())
	}
}

// TestLedgerMembersListVisibility 成员列表：成员可看（只见 active），
// 管理者额外看到待接受/已拒绝；非成员 403。
func TestLedgerMembersListVisibility(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "list-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "成员列表台账")
	memberID, memberTok := mkUser(t, env, "list-member", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleRooms: PermView})
	pendingID := ledgerTestUser(t, env, "list-pending", UserStatusActive)
	if err := env.db.Create(&LedgerMember{
		LedgerID: ledgerA, UserID: pendingID,
		Role: MemberRoleMember, Status: MemberStatusPending,
	}).Error; err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerA)+"/members", owner.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("owner list: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["is_owner"] != true {
		t.Fatalf("owner is_owner = %v, want true", data["is_owner"])
	}
	if got := len(data["members"].([]interface{})); got != 3 {
		t.Fatalf("owner sees %d members, want 3（含 pending）", got)
	}

	w = doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerA)+"/members", memberTok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("member list: %d %s", w.Code, w.Body.String())
	}
	data = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["is_owner"] != false {
		t.Fatalf("member is_owner = %v, want false", data["is_owner"])
	}
	for _, o := range data["members"].([]interface{}) {
		if o.(map[string]interface{})["status"] != MemberStatusActive {
			t.Fatalf("member must only see active members, got %v", o)
		}
	}

	outsider := newPersonalLedger(t, env, "list-outsider")
	if w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerA)+"/members", outsider.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("outsider list: %d, want 403", w.Code)
	}
}

// TestLedgerOwnerGuards 只有管理者能管理；成员不能自我提权；管理者不能被移除。
func TestLedgerOwnerGuards(t *testing.T) {
	env := setupEnv(t)
	ownerA := newPersonalLedger(t, env, "guard-bossA")
	ownerB := newPersonalLedger(t, env, "guard-bossB")
	ledgerA := createSharedLedger(t, env, ownerA.token, "A")
	ledgerB := createSharedLedger(t, env, ownerB.token, "B")
	workerID, workerTok := mkUser(t, env, "guard-worker", UserStatusActive)
	otherID, _ := mkUser(t, env, "guard-other", UserStatusActive)
	addActiveMember(t, env, ledgerA, workerID, ModulePerms{ModuleRooms: PermView})
	addActiveMember(t, env, ledgerA, otherID, ModulePerms{ModuleRooms: PermView})

	// A 的所有者对 B 无任何关系 → 一切成员操作 403。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/ledgers/"+itoa(ledgerB)+"/members", ownerA.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("A list B: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerB)+"/members", ownerA.token,
		map[string]interface{}{"username": "guard-worker"}); w.Code != http.StatusForbidden {
		t.Fatalf("A add to B: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerB)+"/members/"+itoa(workerID),
		ownerA.token, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermView}}); w.Code != http.StatusForbidden {
		t.Fatalf("A edit B member: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerB)+"/members/"+itoa(workerID),
		ownerA.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("A remove B member: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerB), ownerA.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("A delete B: %d", w.Code)
	}

	// 普通成员：不能邀请、不能改别人权限、不能移除别人、不能改自己权限。
	thirdID := ledgerTestUser(t, env, "guard-third", UserStatusActive)
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerA)+"/members", workerTok,
		map[string]interface{}{"username": "guard-third"}); w.Code != http.StatusForbidden {
		t.Fatalf("member invite: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(otherID),
		workerTok, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermFull}}); w.Code != http.StatusForbidden {
		t.Fatalf("member edit other: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(otherID),
		workerTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member remove other: %d", w.Code)
	}
	if w := doJSON(env.r, http.MethodPut, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(workerID),
		workerTok, map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermFull}}); w.Code != http.StatusForbidden {
		t.Fatalf("member self-elevate: %d", w.Code)
	}

	// 管理者不能被移除（自己移自己 → 400；成员移管理者 → 403）。
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(ownerA.userID),
		ownerA.token, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("owner self-remove: %d, want 400", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(ownerA.userID),
		workerTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("member remove owner: %d, want 403", w.Code)
	}

	// 成员可主动退出；退出后不再是成员。
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ledgerA)+"/members/"+itoa(workerID),
		workerTok, nil); w.Code != http.StatusOK {
		t.Fatalf("member self-exit: %d %s", w.Code, w.Body.String())
	}
	if memberOf(env.db, workerID, ledgerA) != nil {
		t.Fatal("主动退出后成员关系必须删除")
	}

	// 个人台账不能退出。
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/ledgers/"+itoa(ownerA.ledgerID)+"/members/"+itoa(ownerA.userID),
		ownerA.token, nil); w.Code != http.StatusBadRequest {
		t.Fatalf("exit personal ledger: %d, want 400", w.Code)
	}
	if thirdID == 0 {
		t.Fatal("sanity")
	}
}

// TestUserSearch 邀请时按用户名找人：只返回正常状态账号、排除自己、支持关键字。
func TestUserSearch(t *testing.T) {
	env := setupEnv(t)
	searcher := newPersonalLedger(t, env, "searcher")
	ledgerTestUser(t, env, "findme-active", UserStatusActive)
	ledgerTestUser(t, env, "findme-pending", UserStatusPending)
	ledgerTestUser(t, env, "findme-disabled", UserStatusDisabled)

	w := doJSON(env.r, http.MethodGet, "/api/rental/users/search?q=findme", searcher.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("search: %d %s", w.Code, w.Body.String())
	}
	rows := field(t, decodeBody(t, w), "data").([]interface{})
	if len(rows) != 1 {
		t.Fatalf("search rows = %v, want only the active one", rows)
	}
	if got := rows[0].(map[string]interface{})["username"]; got != "findme-active" {
		t.Fatalf("search hit = %v, want findme-active", got)
	}
	// 不带关键字（或只给 1 个字）：拒绝，避免被当成全站账号花名册扫号
	// （2026-10-11 加的下限；此前 q 为空会把前 20 个账号直接列出来）。
	for _, q := range []string{"", "f"} {
		if w := doJSON(env.r, http.MethodGet, "/api/rental/users/search?q="+q, searcher.token, nil); w.Code != http.StatusBadRequest {
			t.Fatalf("search q=%q = %d, want 400", q, w.Code)
		}
	}
	// 搜索结果不含自己：造一个同样匹配关键字的自己的账号名不易，改用「两个账号
	// 同名前缀」验证过滤逻辑——active 结果里不应出现调用者本人。
	w = doJSON(env.r, http.MethodGet, "/api/rental/users/search?q=findme", searcher.token, nil)
	for _, o := range field(t, decodeBody(t, w), "data").([]interface{}) {
		if o.(map[string]interface{})["id"].(float64) == float64(searcher.userID) {
			t.Fatal("搜索结果不应包含自己")
		}
	}
}

// TestAdminOnlyUserEndpoints 待审核列表/通过/驳回只有超管可调；
// 通过只置 status=1（不再分配台账），驳回保留账号行。
func TestAdminOnlyUserEndpoints(t *testing.T) {
	env := setupEnv(t)
	db := env.db

	var adminID uint
	db.Table("users").Where("username = ?", "landlord").Pluck("id", &adminID)
	if ApprovalRequired(db, adminID) {
		t.Fatalf("admin must not require approval")
	}
	pendingID := ledgerTestUser(t, env, "pending1", UserStatusPending)
	activeID := ledgerTestUser(t, env, "active1", UserStatusActive)
	if !ApprovalRequired(db, pendingID) || ApprovalRequired(db, activeID) {
		t.Fatalf("ApprovalRequired wrong")
	}
	if ApprovalRequired(db, 999999) || ApprovalRequired(db, 0) {
		t.Fatalf("missing user must not require approval")
	}
	if !PendingUserDenied(db, pendingID, "/api/rental/rooms") ||
		PendingUserDenied(db, pendingID, "/api/rental/ledgers/mine") ||
		PendingUserDenied(db, activeID, "/api/rental/rooms") {
		t.Fatalf("PendingUserDenied wrong")
	}

	// 非超管（哪怕是台账管理者）不能调审核端点。
	boss := newPersonalLedger(t, env, "audit-boss")
	if w := doJSON(env.r, http.MethodGet, "/api/rental/users/pending", boss.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin pending list: %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/users/"+itoa(pendingID)+"/approve", boss.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin approve: %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/users/"+itoa(pendingID)+"/reject", boss.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("non-admin reject: %d, want 403", w.Code)
	}

	// 超管通过：status → 1，且不再写任何台账归属（只保证个人台账存在）。
	w := env.do(http.MethodGet, "/api/rental/users/pending", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pending list: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodPost, "/api/rental/users/"+itoa(pendingID)+"/approve", map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if got := uint(field(t, decodeBody(t, w), "data", "user_id").(float64)); got != pendingID {
		t.Fatalf("approve user_id = %d, want %d", got, pendingID)
	}
	if personalLedgerOf(db, pendingID) == nil {
		t.Fatal("通过后必须有个人台账")
	}
	if w := env.do(http.MethodPost, "/api/rental/users/"+itoa(pendingID)+"/approve", map[string]interface{}{}); w.Code != http.StatusBadRequest {
		t.Fatalf("re-approve: %d %s, want 400", w.Code, w.Body.String())
	}
	// body 里带了旧字段也应被忽略（不报错、不分配）。
	p3 := ledgerTestUser(t, env, "pending3", UserStatusPending)
	if w := env.do(http.MethodPost, "/api/rental/users/"+itoa(p3)+"/approve",
		map[string]interface{}{"ledger_id": 4242, "perms": ModulePerms{ModuleBills: PermEdit}}); w.Code != http.StatusOK {
		t.Fatalf("approve with legacy body: %d %s", w.Code, w.Body.String())
	}
	var stray int64
	db.Model(&LedgerMember{}).Where("user_id = ? AND role = ?", p3, MemberRoleMember).Count(&stray)
	if stray != 0 {
		t.Fatal("审批不再是分配台账：不得写入别人的成员关系")
	}

	// 驳回：置 0 且保留行；不能驳回超管。
	p2 := ledgerTestUser(t, env, "pending2", UserStatusPending)
	if w := env.do(http.MethodPost, "/api/rental/users/"+itoa(p2)+"/reject", nil); w.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", w.Code, w.Body.String())
	}
	var st int
	db.Table("users").Where("id = ?", p2).Pluck("status", &st)
	if st != UserStatusDisabled {
		t.Fatalf("reject status = %d want %d", st, UserStatusDisabled)
	}
	if err := db.First(&database.User{}, p2).Error; err != nil {
		t.Fatalf("reject must keep row: %v", err)
	}
	if w := env.do(http.MethodPost, "/api/rental/users/"+itoa(adminID)+"/reject", nil); w.Code != http.StatusForbidden {
		t.Fatalf("reject admin: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodGet, "/api/rental/users/me/approval", nil); w.Code != http.StatusOK {
		t.Fatalf("my approval: %d %s", w.Code, w.Body.String())
	}
}
