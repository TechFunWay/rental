// fixes_v0610_test.go — 2026-10-14 审计修复的回归：
// ① 待审房源登记并发审核只能成功一次（CAS 认领）；
// ② 房源跨台账移动要把该房的登记申请一起带走；
// ③ 候选租户账号不返回手机号（防跨台账捞号）。
package rental

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
)

// TestRoomApplicationApproveIsAtomicOnConcurrentRequests：两个审核请求同时到达，
// 只能建出一间房源、一个租户，另一个拿到 400。
func TestRoomApplicationApproveIsAtomicOnConcurrentRequests(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "concurrent-village")
	ledger := createSharedLedger(t, env, owner.token, "并发村委")
	token := roomApplyLinkOf(t, env, owner.token, ledger)

	w := roomApplySubmit(t, env, token, map[string]string{
		"name": "王村民", "phone": "13800001111", "community": "并发村",
		"room_no": "东厢房", "default_rent": "800", "tenant_name": "李租客",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))

	var before int64
	env.db.Model(&Room{}).Where("org_id = ?", ledger).Count(&before)

	const n = 4
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := doJSON(env.r, http.MethodPost,
				fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledger), owner.token, nil)
			codes[i] = res.Code
		}(i)
	}
	wg.Wait()

	okCount := 0
	for _, code := range codes {
		if code == http.StatusOK {
			okCount++
		}
	}
	if okCount != 1 {
		t.Fatalf("并发审核成功次数 = %d, want 1（响应码 %v）", okCount, codes)
	}
	var after int64
	env.db.Model(&Room{}).Where("org_id = ?", ledger).Count(&after)
	if after-before != 1 {
		t.Errorf("房源数变化 = %d, want 1（并发重复建房）", after-before)
	}
	var tenants int64
	env.db.Model(&Tenant{}).Where("org_id = ?", ledger).Count(&tenants)
	if tenants != 1 {
		t.Errorf("租户数 = %d, want 1（并发重复建租户）", tenants)
	}
	var app RoomApplication
	env.db.First(&app, appID)
	if app.Status != "approved" || app.RoomID == 0 {
		t.Errorf("申请行状态不对：%+v", app)
	}
}

// TestRoomMoveCarriesRoomApplication：房源移动时该房的登记申请（含待审的）一起迁走，
// 避免源台账残留「指向已搬走房源」的待审登记被再审一次。
func TestRoomMoveCarriesRoomApplication(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "move-apply-owner")
	source := createSharedLedger(t, env, owner.token, "移动源台账")
	target := createSharedLedger(t, env, owner.token, "移动目标台账")
	roomID := env.seedRoomInLedger(source, "901")

	// 源台账上有一条**已通过**的登记（这间房的来历）与一条待审登记
	approved := RoomApplication{
		LedgerID: source, Name: "王村民", Phone: "13800001111",
		Community: "移动村", RoomNo: "901", Status: "approved", RoomID: roomID,
	}
	pending := RoomApplication{
		LedgerID: source, Name: "赵村民", Phone: "13800002222",
		Community: "移动村", RoomNo: "902", Status: "pending",
	}
	if err := env.db.Create(&approved).Error; err != nil {
		t.Fatalf("seed approved: %v", err)
	}
	if err := env.db.Create(&pending).Error; err != nil {
		t.Fatalf("seed pending: %v", err)
	}

	w := doJSON(env.r, http.MethodPost, fmt.Sprintf("/api/rental/rooms/%d/move?ledger_id=%d", roomID, source),
		owner.token, map[string]interface{}{"target_ledger_id": target})
	if w.Code != http.StatusOK {
		t.Fatalf("移动: %d %s", w.Code, w.Body.String())
	}
	var moved RoomApplication
	env.db.First(&moved, approved.ID)
	if moved.LedgerID != target {
		t.Errorf("已通过的登记没跟着搬：org_id = %d, want %d", moved.LedgerID, target)
	}

	// 移动后源台账不该再看到这条房源的登记（按房源 id 关联）
	var left int64
	env.db.Model(&RoomApplication{}).Where("room_id = ? AND org_id = ?", roomID, source).Count(&left)
	if left != 0 {
		t.Errorf("源台账仍残留该房源的登记 %d 条", left)
	}
}

// TestTenantAccountCandidatesHidePhone：候选账号只回用户名，不回手机号
// （接口天生跨台账，回手机号等于给人捞号的花名册）。
func TestTenantAccountCandidatesHidePhone(t *testing.T) {
	env := setupEnv(t)
	// 用管理员自己的**个人台账**（台账与登录账号要对得上，否则门禁 403）
	_, tok := mkUser(t, env, "cand-tenant-acct", UserStatusPending)
	// 把候选账号改成租户角色并填手机号
	if err := env.db.Table("users").Where("username = ?", "cand-tenant-acct").
		Updates(map[string]interface{}{"role": "tenant", "phone": "13912345678"}).Error; err != nil {
		t.Fatalf("prepare candidate: %v", err)
	}
	w := doJSON(env.r, http.MethodGet,
		fmt.Sprintf("/api/rental/tenant-accounts/candidates?keyword=cand-tenant&ledger_id=%d", env.personalLedger),
		env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("候选查询: %d %s", w.Code, w.Body.String())
	}
	rows, _ := field(t, decodeBody(t, w), "data").([]interface{})
	if len(rows) == 0 {
		t.Fatal("应能搜到未关联的租户账号")
	}
	row := rows[0].(map[string]interface{})
	if _, has := row["phone"]; has {
		t.Errorf("候选账号不该返回手机号：%v", row)
	}
	if row["username"] != "cand-tenant-acct" {
		t.Errorf("候选账号用户名不对：%v", row)
	}
	_ = tok
}
