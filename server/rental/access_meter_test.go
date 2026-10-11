// access_meter_test.go — 按台账的模块权限集成测试：同一用户在共同台账里
// 只拿到被勾选的模块；未勾选的模块在该台账里一律 403；写入要够档位。
//
// 新模型（个人台账 + 共同台账）下，页面级门禁是「跨台账并集」——每个账号在
// 自己的个人台账里天然全模块 full，所以「未授权就整页 403」只对**具体台账**
// 成立。本文件的断言全部带 ?ledger_id= 指向共同台账，考的正是按台账的权限隔离。
package rental

import (
	"net/http"
	"testing"
)

// TestLedgerModulePerms 按台账勾选模块：可达/不可达、可写/不可写都由该台账
// 的成员权限决定；非法键在保存时被丢弃。
func TestLedgerModulePerms(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "meter-owner")
	shared := createSharedLedger(t, env, owner.token, "抄表台账")
	roomID := func() uint {
		w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), owner.token,
			map[string]interface{}{"community": "M区", "room_no": "M9"})
		if w.Code != http.StatusOK {
			t.Fatalf("seed room: %d %s", w.Code, w.Body.String())
		}
		return uint(field(t, decodeBody(t, w), "data", "id").(float64))
	}()

	// 注册抄表员 → 邀请 → 接受。
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "meter-reader", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	readerID := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	readerToken := field(t, decodeBody(t, w), "data", "token").(string)
	inviteAndAccept(t, env, owner.token, readerToken, shared, "meter-reader")

	grant := map[string]interface{}{"perms": map[string]string{
		ModuleMeters: PermEdit,
		ModuleRooms:  PermView,
	}}
	if w := doJSON(env.r, http.MethodPut,
		"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(readerID), owner.token, grant); w.Code != http.StatusOK {
		t.Fatalf("grant perms: %d %s", w.Code, w.Body.String())
	}

	// 可达：该台账的房间列表、抄表台账、读数录入。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), readerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("meter rooms = %d, want 200", w.Code)
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/meter-records?ledger_id="+itoa(shared), readerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("meter list = %d, want 200", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/meter-records?ledger_id="+itoa(shared), readerToken, map[string]interface{}{
		"room_id": roomID, "period": "2026-10", "water": 66, "elec": 136, "gas": 12, "note": "抄表员录入",
	}); w.Code != http.StatusOK {
		t.Fatalf("meter upsert: %d %s", w.Code, w.Body.String())
	}

	// 读也按**台账 + 模块**卡（2026-10-11 收紧）：只勾了抄表（和房源 view）的成员，
	// 指定该台账读账单/收款/统计/租户一律 403 —— 旧口径是「成员读页面一律放行」，
	// 结果抄表员也能读到身份证号、合同与全部收款流水，与按模块勾选的产品口径不符。
	for _, path := range []string{
		"/api/rental/bills", "/api/rental/stats", "/api/rental/payments",
		"/api/rental/analytics", "/api/rental/tenants",
	} {
		if w := doJSON(env.r, http.MethodGet, path+"?ledger_id="+itoa(shared), readerToken, nil); w.Code != http.StatusForbidden {
			t.Errorf("meter GET %s = %d, want 403（该台账未勾选对应模块）", path, w.Code)
		}
	}
	// 不带 ledger_id 时并集门禁仍放行（页面可进），但可见范围里没有该台账，
	// 于是返回空列表而不是 403 —— 前端按当前台账的勾选隐藏菜单，两条口径一致。
	if w := doJSON(env.r, http.MethodGet, "/api/rental/bills", readerToken, nil); w.Code != http.StatusOK {
		t.Errorf("meter GET bills（无 ledger_id）= %d, want 200", w.Code)
	} else if n := len(itemsOf(t, w)); n != 0 {
		t.Errorf("meter GET bills（无 ledger_id）items = %d, want 0", n)
	}

	// 不可达：非成员台账一律 403（数据隔离不靠模块勾选兜底）。
	other := createSharedLedger(t, env, owner.token, "无关台账")
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(other), readerToken, nil); w.Code != http.StatusForbidden {
		t.Errorf("meter GET foreign ledger = %d, want 403", w.Code)
	}

	// 不可写：房源新增要 full；抄表删除要 full；账单生成无权限。
	if w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(shared), readerToken, map[string]interface{}{
		"room_no": "M90",
	}); w.Code != http.StatusForbidden {
		t.Errorf("meter room create = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, "/api/rental/meter-records/1?ledger_id="+itoa(shared), readerToken, nil); w.Code != http.StatusForbidden {
		t.Errorf("meter record delete = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/bills/generate?ledger_id="+itoa(shared), readerToken, map[string]interface{}{
		"period": "2026-10",
	}); w.Code != http.StatusForbidden {
		t.Errorf("meter bill generate = %d, want 403", w.Code)
	}

	// 非法模块键在保存时丢弃：勾了 meters:edit + 伪造键，存量只剩合法勾选。
	if w := doJSON(env.r, http.MethodPut,
		"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(readerID), owner.token,
		map[string]interface{}{"perms": map[string]string{ModuleMeters: PermEdit, "hack": PermFull}}); w.Code != http.StatusOK {
		t.Fatalf("grant with invalid key: %d %s", w.Code, w.Body.String())
	}
	var row LedgerMember
	if err := env.db.Where("user_id = ? AND org_id = ?", readerID, shared).First(&row).Error; err != nil {
		t.Fatalf("load member row: %v", err)
	}
	if got := parsePerms(row.Perms); len(got) != 1 || got[ModuleMeters] != PermEdit {
		t.Fatalf("stored perms = %q -> %v, want only meters:edit", row.Perms, got)
	}

	// 只读用户：能看抄表台账，但不能录读数（操作档门槛）。
	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "meter-viewer", "password": "secret123",
	})
	viewerID := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	viewerToken := field(t, decodeBody(t, w), "data", "token").(string)
	inviteAndAccept(t, env, owner.token, viewerToken, shared, "meter-viewer")
	if w := doJSON(env.r, http.MethodPut,
		"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(viewerID), owner.token,
		map[string]interface{}{"perms": map[string]string{ModuleMeters: PermView}}); w.Code != http.StatusOK {
		t.Fatalf("grant viewer: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/meter-records?ledger_id="+itoa(shared), viewerToken, nil); w.Code != http.StatusOK {
		t.Errorf("viewer meter list = %d, want 200", w.Code)
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/meter-records?ledger_id="+itoa(shared), viewerToken, map[string]interface{}{
		"room_id": roomID, "period": "2026-10", "water": 70, "elec": 140,
	}); w.Code != http.StatusForbidden {
		t.Errorf("viewer meter upsert = %d, want 403", w.Code)
	}

	// 空勾选 = 撤销该台账的全部权限：写一律 403，读该台账也 403
	// （2026-10-11 起读按台账+模块收窄，不再「成员读页面一律放行」）。
	if w := doJSON(env.r, http.MethodPut,
		"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(readerID), owner.token,
		map[string]interface{}{"perms": map[string]string{}}); w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/meter-records?ledger_id="+itoa(shared), readerToken, map[string]interface{}{
		"room_id": roomID, "period": "2026-11", "water": 1, "elec": 1,
	}); w.Code != http.StatusForbidden {
		t.Errorf("revoked meter upsert = %d, want 403", w.Code)
	}
	if w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?ledger_id="+itoa(shared), readerToken, nil); w.Code != http.StatusForbidden {
		t.Errorf("revoked member read = %d, want 403（撤权后该台账不再可读）", w.Code)
	}
}

// inviteAndAccept 管理者按用户名邀请，被邀请人接受（测试夹具）。
func inviteAndAccept(t *testing.T, env *testEnv, ownerToken, guestToken string, ledgerID uint, username string) {
	t.Helper()
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerID)+"/members",
		ownerToken, map[string]interface{}{"username": username}); w.Code != http.StatusOK {
		t.Fatalf("invite %s: %d %s", username, w.Code, w.Body.String())
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/ledgers/"+itoa(ledgerID)+"/invitations/respond",
		guestToken, map[string]interface{}{"accept": true}); w.Code != http.StatusOK {
		t.Fatalf("accept %s: %d %s", username, w.Code, w.Body.String())
	}
}

// TestLegacyLevelCompat 旧版整档字符串（v0.4.x）读取自动换算成模块勾选。
func TestLegacyLevelCompat(t *testing.T) {
	if got := parsePerms(AccessNone); len(got) != 0 {
		t.Fatalf("none -> %v, want empty", got)
	}
	meter := parsePerms(AccessMeter)
	if meter[ModuleMeters] != PermEdit || meter[ModuleRooms] != PermView || len(meter) != 2 {
		t.Fatalf("meter -> %v, want meters:edit + rooms:view", meter)
	}
	readonly := parsePerms(AccessReadonly)
	for _, k := range moduleKeys {
		if readonly[k] != PermView {
			t.Fatalf("readonly[%s] = %q, want view", k, readonly[k])
		}
	}
	edit := parsePerms(AccessEdit)
	if edit[ModuleBills] != PermEdit {
		t.Fatalf("edit[bills] = %q, want edit", edit[ModuleBills])
	}
	full := parsePerms(AccessFull)
	assertFullModulePerms(t, full, "legacy full")
	// JSON 里的非法模块/档位被丢弃。
	mixed := parsePerms(`{"rooms":"full","bogus":"full","bills":"super"}`)
	if len(mixed) != 1 || mixed[ModuleRooms] != PermFull {
		t.Fatalf("mixed JSON -> %v, want only rooms:full", mixed)
	}
}
