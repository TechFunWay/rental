// tenant_members_test.go — 住户成员的集成测试。
//
// 口径（见 docs/08-租户与住户成员设计.md）：住户成员挂在租户（计费主体）名下，
// 只登记身份信息、**不参与任何计费**；退租时批量置为已搬离，删租户/删房源连带清理。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// memberRows 成员列表接口返回的是数组（不分页），直接取 data。
func memberRows(t *testing.T, env *testEnv, path, token string) []interface{} {
	t.Helper()
	w := doJSON(env.r, http.MethodGet, path, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list %s: %d %s", path, w.Code, w.Body.String())
	}
	rows, _ := field(t, decodeBody(t, w), "data").([]interface{})
	return rows
}

func TestTenantMemberCRUDAndDerivation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("MB1", 1000, 100)
	tenantID := env.seedTenant(roomID, "王房东")

	// 姓名必填、身份证格式、日期格式都要被拦下。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{"name": "  "}); w.Code != http.StatusBadRequest {
		t.Errorf("缺姓名 = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{"name": "李四", "id_card": "123"}); w.Code != http.StatusBadRequest {
		t.Errorf("非法身份证 = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{"name": "李四", "move_in_date": "2026/01/01"}); w.Code != http.StatusBadRequest {
		t.Errorf("非法日期 = %d, want 400", w.Code)
	}

	// 正常建档：18 位身份证号派生出生日期与性别（第 17 位奇数 = 男）。
	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{
			"name": "李四", "relation": "配偶", "phone": "13900001111",
			"id_card": "330102199001011234", "move_in_date": "2026-01-01", "notes": "同住",
		})
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	row := decodeBody(t, w)
	data := field(t, row, "data").(map[string]interface{})
	memberID := uint(num(data["id"]))
	if data["birthday"] != "1990-01-01" || data["gender"] != "male" {
		t.Errorf("派生 = %v/%v, want 1990-01-01/male", data["birthday"], data["gender"])
	}
	if data["active"] != true {
		t.Errorf("新建成员默认在住，实际 %v", data["active"])
	}
	if data["tenant_id"] != float64(tenantID) {
		t.Errorf("tenant_id = %v, want %d", data["tenant_id"], tenantID)
	}

	// 同一租户下重复身份证号要被拦。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{"name": "李四（重）", "id_card": "330102199001011234"}); w.Code != http.StatusBadRequest {
		t.Errorf("重复身份证 = %d, want 400", w.Code)
	}

	// 再加一名不同证件的成员。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
		map[string]interface{}{"name": "小明", "relation": "子女", "id_card": "330102201505054321"}); w.Code != http.StatusOK {
		t.Fatalf("second create: %d %s", w.Code, w.Body.String())
	}

	// 列表：按租户、按房源、按在住筛。
	byTenant := memberRows(t, env, fmt.Sprintf("/api/rental/tenant-members?tenant_id=%d", tenantID), env.token)
	if len(byTenant) != 2 {
		t.Fatalf("成员数 = %d, want 2", len(byTenant))
	}
	byRoom := memberRows(t, env, fmt.Sprintf("/api/rental/tenant-members?room_id=%d", roomID), env.token)
	if len(byRoom) != 2 {
		t.Fatalf("按房源成员数 = %d, want 2", len(byRoom))
	}
	// 租户列表上的「成员 n」是在住成员数。
	tenants := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/tenants?room_id=%d", roomID), nil)))
	if len(tenants) != 1 {
		t.Fatalf("租户数 = %d, want 1", len(tenants))
	}
	if got := num(tenants[0].(map[string]interface{})["members_count"]); got != 2 {
		t.Errorf("members_count = %v, want 2", got)
	}

	// 修改：补搬离日期 → 自动收尾为已搬离；在住筛选随之少一人。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenant-members/%d", memberID),
		map[string]interface{}{
			"name": "李四", "relation": "配偶", "phone": "13900001111",
			"id_card": "330102199001011234", "move_in_date": "2026-01-01",
			"move_out_date": "2026-06-30",
		})
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	updated := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if updated["active"] != false || updated["move_out_date"] != "2026-06-30" {
		t.Errorf("补搬离日期后 = %v/%v, want false/2026-06-30", updated["active"], updated["move_out_date"])
	}
	active := memberRows(t, env, fmt.Sprintf("/api/rental/tenant-members?tenant_id=%d&active=1", tenantID), env.token)
	if len(active) != 1 {
		t.Errorf("在住成员 = %d, want 1", len(active))
	}
	// 在住成员数只算在住的。
	tenants = env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/tenants?room_id=%d", roomID), nil)))
	if got := num(tenants[0].(map[string]interface{})["members_count"]); got != 1 {
		t.Errorf("members_count（已搬离后）= %v, want 1", got)
	}

	// 删除。
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/tenant-members/%d", memberID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&TenantMember{}).Where("tenant_id = ?", tenantID).Count(&n)
	if n != 1 {
		t.Errorf("剩余成员 = %d, want 1", n)
	}
}

// TestTenantMemberLifecycle 退租、删租户、删房源三种收尾。
func TestTenantMemberLifecycle(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("MB2", 1000, 100)
	tenantID := env.seedTenant(roomID, "退租测试")

	for _, name := range []string{"成员甲", "成员乙"} {
		if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
			map[string]interface{}{"name": name}); w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
	}

	// 退租：成员批量置为已搬离并补搬离日期，行保留。
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", tenantID),
		map[string]interface{}{"move_out_date": "2026-07-31"}); w.Code != http.StatusOK {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	var members []TenantMember
	env.db.Where("tenant_id = ?", tenantID).Find(&members)
	if len(members) != 2 {
		t.Fatalf("退租后成员行 = %d, want 2（保留历史）", len(members))
	}
	for _, m := range members {
		if m.Active || m.MoveOutDate != "2026-07-31" {
			t.Errorf("退租后 %s = active:%v moveOut:%q", m.Name, m.Active, m.MoveOutDate)
		}
	}
	// 租户列表里的成员数回落到 0。
	tenants := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/tenants?room_id=%d&active=1", roomID), nil)))
	if len(tenants) != 0 {
		t.Fatalf("退租后在租租户 = %d, want 0", len(tenants))
	}

	// 删租户档案：成员随档案一起删除。
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/tenants/%d", tenantID), nil); w.Code != http.StatusOK {
		t.Fatalf("delete tenant: %d %s", w.Code, w.Body.String())
	}
	var n int64
	env.db.Model(&TenantMember{}).Where("tenant_id = ?", tenantID).Count(&n)
	if n != 0 {
		t.Errorf("删租户后成员 = %d, want 0", n)
	}

	// 删房源：历史租户（无账单、无在租）与其成员一起清理。
	roomID2 := env.seedRoom("MB3", 1000, 100)
	tenantID2 := env.seedTenant(roomID2, "历史租户")
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID2),
		map[string]interface{}{"name": "随房清理"}); w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", tenantID2),
		map[string]interface{}{"move_out_date": "2026-07-31"}); w.Code != http.StatusOK {
		t.Fatalf("checkout2: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID2), nil); w.Code != http.StatusOK {
		t.Fatalf("delete room: %d %s", w.Code, w.Body.String())
	}
	env.db.Model(&TenantMember{}).Where("tenant_id = ?", tenantID2).Count(&n)
	if n != 0 {
		t.Errorf("删房源后成员 = %d, want 0", n)
	}
}

// TestTenantMemberLedgerIsolation 成员按台账隔离，且权限走「租户与合同」模块。
func TestTenantMemberLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "member-owner")
	ledgerA := createSharedLedger(t, env, owner.token, "成员A台账")
	ledgerB := createSharedLedger(t, env, owner.token, "成员B台账")
	roomA := env.seedRoomDirect(ledgerA, "MA")
	roomB := env.seedRoomDirect(ledgerB, "MB")
	tenantA := env.seedTenantInLedger(ledgerA, roomA, "A租户")
	tenantB := env.seedTenantInLedger(ledgerB, roomB, "B租户")

	// 所有者往两边各加一名成员。
	if w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members?ledger_id=%d", tenantB, ledgerB), owner.token,
		map[string]interface{}{"name": "B成员"}); w.Code != http.StatusOK {
		t.Fatalf("owner create B: %d %s", w.Code, w.Body.String())
	}
	var bMember TenantMember
	if err := env.db.Where("tenant_id = ?", tenantB).First(&bMember).Error; err != nil {
		t.Fatalf("load B member: %v", err)
	}
	if w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members?ledger_id=%d", tenantA, ledgerA), owner.token,
		map[string]interface{}{"name": "A成员"}); w.Code != http.StatusOK {
		t.Fatalf("owner create A: %d %s", w.Code, w.Body.String())
	}

	memberID, memberTok := mkUser(t, env, "member-user", UserStatusActive)
	addActiveMember(t, env, ledgerA, memberID, ModulePerms{ModuleTenants: PermView})

	// 只能看到 A 的成员；B 的成员改/删一律按不存在处理。
	rows := memberRows(t, env, fmt.Sprintf("/api/rental/tenant-members?ledger_id=%d", ledgerA), memberTok)
	if len(rows) != 1 {
		t.Errorf("成员可见数 = %d, want 1", len(rows))
	}
	if w := doJSON(env.r, http.MethodPut, fmt.Sprintf("/api/rental/tenant-members/%d", bMember.ID), memberTok,
		map[string]interface{}{"name": "越权"}); w.Code != http.StatusNotFound {
		t.Errorf("改 B 成员 = %d, want 404", w.Code)
	}
	if w := doJSON(env.r, http.MethodDelete, fmt.Sprintf("/api/rental/tenant-members/%d", bMember.ID), memberTok, nil); w.Code != http.StatusNotFound {
		t.Errorf("删 B 成员 = %d, want 404", w.Code)
	}
	// 只有查看档：不能加成员。
	if w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members?ledger_id=%d", tenantA, ledgerA), memberTok,
		map[string]interface{}{"name": "无操作权"}); w.Code != http.StatusForbidden {
		t.Errorf("查看档加成员 = %d, want 403", w.Code)
	}
	// 没有租户模块权限的成员不能读。
	otherID, otherTok := mkUser(t, env, "member-rooms-only", UserStatusActive)
	addActiveMember(t, env, ledgerA, otherID, ModulePerms{ModuleRooms: PermView})
	if w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/tenant-members?ledger_id=%d", ledgerA), otherTok, nil); w.Code != http.StatusForbidden {
		t.Errorf("无租户权限读成员 = %d, want 403", w.Code)
	}
	// 往别的台账的租户上加成员：带去的是自己有权限的台账，租户不可见 → 404
	// （上面那位只有查看档，路由层就先 403 了；这里用有操作档的人才会走到对象判定）。
	editorID, editorTok := mkUser(t, env, "member-editor", UserStatusActive)
	addActiveMember(t, env, ledgerA, editorID, ModulePerms{ModuleTenants: PermEdit})
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenants/%d/members?ledger_id=%d", tenantB, ledgerA), editorTok,
		map[string]interface{}{"name": "串台账"}); w.Code != http.StatusNotFound {
		t.Errorf("越租户加成员 = %d, want 404", w.Code)
	}
	// 同一操作档在本台账里能正常加。
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/tenants/%d/members?ledger_id=%d", tenantA, ledgerA), editorTok,
		map[string]interface{}{"name": "本台账成员"}); w.Code != http.StatusOK {
		t.Errorf("本台账加成员 = %d, want 200（%s）", w.Code, w.Body.String())
	}
}

// TestMembersDoNotAffectBilling 成员只进「在住人数」，不进任何金额口径。
func TestMembersDoNotAffectBilling(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("MB4", 1000, 100)
	tenantID := env.seedTenant(roomID, "计费不受影响")

	statsOf := func() map[string]interface{} {
		w := env.do(http.MethodGet, "/api/rental/stats", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("stats: %d %s", w.Code, w.Body.String())
		}
		return decodeBody(t, w)["data"].(map[string]interface{})
	}
	before := statsOf()
	beforeTenants := num(before["tenants_active"])
	beforeBills := num(before["month"].(map[string]interface{})["bill_count"])

	for _, name := range []string{"甲", "乙", "丙"} {
		if w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/members", tenantID),
			map[string]interface{}{"name": name}); w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	after := statsOf()
	if got := num(after["members_active"]); got != 3 {
		t.Errorf("members_active = %v, want 3", got)
	}
	if got := num(after["residents_active"]); got != beforeTenants+3 {
		t.Errorf("residents_active = %v, want %v", got, beforeTenants+3)
	}
	if got := num(after["tenants_active"]); got != beforeTenants {
		t.Errorf("tenants_active 不该被成员影响：%v → %v", beforeTenants, got)
	}
	if got := num(after["month"].(map[string]interface{})["bill_count"]); got != beforeBills {
		t.Errorf("账单张数不该被成员影响：%v → %v", beforeBills, got)
	}
	// 建账时也不会把成员算成计费对象：一间房一张账单。
	if w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026-09",
	}); w.Code != http.StatusOK {
		t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	var bills int64
	env.db.Model(&Bill{}).Where("room_id = ?", roomID).Count(&bills)
	if bills != 1 {
		t.Errorf("账单 = %d, want 1（成员不额外产生账单）", bills)
	}
}

// TestTenantFormSavesMembersTogether 登记/编辑租户表单里的「入住成员」与租户一起保存：
// 新建时一次落库、编辑时增改删按表单同步，任一行不合法则整单回滚。
func TestTenantFormSavesMembersTogether(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("MB5", 1000, 100)

	// 新建：租户 + 两名成员一次提交。
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "陈房东", "phone": "13800000001",
		"members": []map[string]interface{}{
			{"name": "陈太", "relation": "配偶", "id_card": "330102199001011234"},
			{"name": "小陈", "relation": "子女", "phone": "13900000002"},
		},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create with members: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(num(field(t, decodeBody(t, w), "data", "id")))
	var members []TenantMember
	env.db.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&members)
	if len(members) != 2 {
		t.Fatalf("成员数 = %d, want 2", len(members))
	}
	if members[0].Birthday != "1990-01-01" || members[0].Gender != "male" {
		t.Errorf("派生 = %v/%v, want 1990-01-01/male", members[0].Birthday, members[0].Gender)
	}
	if !members[0].Active || !members[1].Active {
		t.Errorf("表单新建的成员默认在住，实际 %v/%v", members[0].Active, members[1].Active)
	}
	// 列表上的成员数是在住成员数。
	rows := env.items(decodeBody(t, env.do(http.MethodGet,
		fmt.Sprintf("/api/rental/tenants?room_id=%d", roomID), nil)))
	if got := num(rows[0].(map[string]interface{})["members_count"]); got != 2 {
		t.Errorf("members_count = %v, want 2", got)
	}

	// 编辑：改第一个人（关系）、加一个新人、删掉第二个人 —— 表单即真相。
	firstID := members[0].ID
	payload := map[string]interface{}{
		"room_id": roomID, "name": "陈房东", "phone": "13800000001",
		"members": []map[string]interface{}{
			{"id": members[0].ID, "name": "陈太", "relation": "父母", "id_card": "330102199001011234"},
			{"name": "老三", "relation": "亲属"},
		},
	}
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), payload); w.Code != http.StatusOK {
		t.Fatalf("update with members: %d %s", w.Code, w.Body.String())
	}
	members = nil
	env.db.Where("tenant_id = ?", tenantID).Order("id ASC").Find(&members)
	if len(members) != 2 {
		t.Fatalf("编辑后成员数 = %d, want 2（改一、加一、删一）", len(members))
	}
	names := map[string]string{}
	for _, m := range members {
		names[m.Name] = m.Relation
	}
	if names["陈太"] != "父母" {
		t.Errorf("陈太的关系 = %q, want 父母", names["陈太"])
	}
	if _, ok := names["老三"]; !ok {
		t.Errorf("新增的成员没落库：%v", names)
	}
	if _, ok := names["小陈"]; ok {
		t.Errorf("表单里删掉的成员应一并删除：%v", names)
	}
	keptInPlace := false
	for _, m := range members {
		if m.ID == firstID && m.Name == "陈太" {
			keptInPlace = true
		}
	}
	if !keptInPlace {
		t.Errorf("已有成员应原地更新（保留 id %d）而不是重建：%+v", firstID, members)
	}

	// 补搬离日期 → 自动收尾为已搬离；在住成员数随之变化。
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "陈房东",
		"members": []map[string]interface{}{
			{"id": members[0].ID, "name": "陈太", "id_card": "330102199001011234", "move_out_date": "2026-06-30"},
			{"name": "老三", "relation": "亲属"},
		},
	}); w.Code != http.StatusOK {
		t.Fatalf("update move out: %d %s", w.Code, w.Body.String())
	}
	var one TenantMember
	env.db.First(&one, members[0].ID)
	if one.Active || one.MoveOutDate != "2026-06-30" {
		t.Errorf("补搬离日期后 = active:%v moveOut:%q", one.Active, one.MoveOutDate)
	}

	// 入参不合法：整单回滚，不能留下「租户建好了、成员没进去」的半成品。
	before := countTenants(t, env)
	if w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "半成品",
		"members": []map[string]interface{}{{"name": "  "}},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("成员缺姓名 = %d, want 400", w.Code)
	}
	if after := countTenants(t, env); after != before {
		t.Errorf("成员不合法时租户不该落库：%d → %d", before, after)
	}
	// 表单内两位成员证件号重复也要拦。
	if w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "重复证件",
		"members": []map[string]interface{}{
			{"name": "甲", "id_card": "330102199001011234"},
			{"name": "乙", "id_card": "330102199001011234"},
		},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("表单内重复证件 = %d, want 400", w.Code)
	}
	// 改别人的成员：id 不属于本租户 → 400。
	other := env.seedTenant(roomID, "另一个租户")
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", other), map[string]interface{}{
		"room_id": roomID, "name": "另一个租户",
		"members": []map[string]interface{}{{"id": members[0].ID, "name": "越权"}},
	}); w.Code != http.StatusBadRequest {
		t.Errorf("改别的租户的成员 = %d, want 400", w.Code)
	}
	// 不带 members 字段（老客户端）→ 不动已有成员。
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "陈房东",
	}); w.Code != http.StatusOK {
		t.Fatalf("update without members: %d %s", w.Code, w.Body.String())
	}
	var still int64
	env.db.Model(&TenantMember{}).Where("tenant_id = ?", tenantID).Count(&still)
	if still != 2 {
		t.Errorf("不带 members 时不该动成员：%d, want 2", still)
	}
	// 显式空数组 → 删光（表单里把行都删掉了）。
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "陈房东", "members": []map[string]interface{}{},
	}); w.Code != http.StatusOK {
		t.Fatalf("clear members: %d %s", w.Code, w.Body.String())
	}
	env.db.Model(&TenantMember{}).Where("tenant_id = ?", tenantID).Count(&still)
	if still != 0 {
		t.Errorf("显式空数组应清空成员：%d, want 0", still)
	}
}

func countTenants(t *testing.T, env *testEnv) int64 {
	t.Helper()
	var n int64
	env.db.Model(&Tenant{}).Count(&n)
	return n
}
