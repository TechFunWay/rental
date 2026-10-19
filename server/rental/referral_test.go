// referral_test.go — 介绍费：平台策略三档下的归一化、房源读写、免登录房源登记的带入。
package rental

import (
	"fmt"
	"net/http"
	"testing"

	"smallgo/server/sysconfig"
)

func setReferralPolicy(t *testing.T, env *testEnv, policy, min string) {
	t.Helper()
	if err := sysconfig.UpdateConfig(env.db, referralPolicyConfigKey, policy, 0); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if err := sysconfig.UpdateConfig(env.db, referralMinConfigKey, min, 0); err != nil {
		t.Fatalf("set min: %v", err)
	}
}

func TestReferralPolicyOnRoomCRUD(t *testing.T) {
	env := setupEnv(t)

	// free（默认）：选填，填了落库
	roomID := env.seedRoom("RF1", 1500, 100)
	w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "community": "幸福小区", "default_rent": 1500,
		"referral_mode": "fixed", "referral_value": 300,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("free 固定金额: %d %s", w.Code, w.Body.String())
	}
	var room Room
	env.db.First(&room, roomID)
	if room.ReferralMode != "fixed" || room.ReferralValue != 300 {
		t.Errorf("介绍费落库 = %q/%v, want fixed/300", room.ReferralMode, room.ReferralValue)
	}
	// 百分比方式按租金折算展示（值原样存百分比）
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "community": "幸福小区", "default_rent": 1500,
		"referral_mode": "percent", "referral_value": 50,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("free 百分比: %d %s", w.Code, w.Body.String())
	}
	env.db.First(&room, roomID)
	if room.ReferralMode != "percent" || room.ReferralValue != 50 {
		t.Errorf("百分比落库 = %q/%v, want percent/50", room.ReferralMode, room.ReferralValue)
	}
	if got := referralAmount(room.ReferralMode, room.ReferralValue, room.DefaultRent); got != 750 {
		t.Errorf("折算金额 = %v, want 750", got)
	}
	// 非法值
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "referral_mode": "percent", "referral_value": 150,
	}); w.Code != http.StatusBadRequest {
		t.Errorf("百分比 >100 = %d, want 400", w.Code)
	}

	// min：必须提供且折算不低于最低值
	setReferralPolicy(t, env, referralPolicyMin, "500")
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "default_rent": 1500, "referral_mode": "", "referral_value": 0,
	}); w.Code != http.StatusBadRequest {
		t.Errorf("min 模式下不填 = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "default_rent": 1500, "referral_mode": "fixed", "referral_value": 200,
	}); w.Code != http.StatusBadRequest {
		t.Errorf("min 模式下低于下限（固定） = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "default_rent": 1500, "referral_mode": "percent", "referral_value": 20,
	}); w.Code != http.StatusBadRequest {
		t.Errorf("min 模式下低于下限（百分比折算 300 < 500） = %d, want 400", w.Code)
	}
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "default_rent": 1500, "referral_mode": "percent", "referral_value": 50,
	}); w.Code != http.StatusOK {
		t.Errorf("min 模式下达标（750 ≥ 500） = %d, want 200（%s）", w.Code, w.Body.String())
	}

	// off：忽略提交的介绍费（不报错），按「不提供」落库
	setReferralPolicy(t, env, referralPolicyOff, "500")
	if w := env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", roomID), map[string]interface{}{
		"room_no": "RF1", "default_rent": 1500, "referral_mode": "fixed", "referral_value": 800,
	}); w.Code != http.StatusOK {
		t.Fatalf("off 模式带值 = %d, want 200（%s）", w.Code, w.Body.String())
	}
	env.db.First(&room, roomID)
	if room.ReferralMode != "" || room.ReferralValue != 0 {
		t.Errorf("off 模式下介绍费应被忽略：%q/%v", room.ReferralMode, room.ReferralValue)
	}
	// 新建房源同理
	if w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "RF2", "referral_mode": "fixed", "referral_value": 500,
	}); w.Code != http.StatusOK {
		t.Fatalf("off 模式下新建: %d %s", w.Code, w.Body.String())
	}
	var room2 Room
	env.db.Where("room_no = ?", "RF2").First(&room2)
	if room2.ReferralMode != "" {
		t.Errorf("off 模式下新建房源不该有介绍费：%q", room2.ReferralMode)
	}

	// free 下新建带介绍费
	setReferralPolicy(t, env, referralPolicyFree, "200")
	if w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "RF3", "default_rent": 1200, "referral_mode": "fixed", "referral_value": 400,
	}); w.Code != http.StatusOK {
		t.Fatalf("free 下新建: %d %s", w.Code, w.Body.String())
	}
	var room3 Room
	env.db.Where("room_no = ?", "RF3").First(&room3)
	if room3.ReferralMode != "fixed" || room3.ReferralValue != 400 {
		t.Errorf("新建介绍费 = %q/%v", room3.ReferralMode, room3.ReferralValue)
	}
}

func TestReferralInPublicRoomApply(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "referral-village")
	ledger := createSharedLedger(t, env, owner.token, "介绍费村委")
	token := roomApplyLinkOf(t, env, owner.token, ledger)
	setReferralPolicy(t, env, referralPolicyMin, "300")

	// 低于下限：公开提交直接拒
	w := roomApplySubmit(t, env, token, map[string]string{
		"name": "王村民", "phone": "13800001111", "community": "幸福村", "room_no": "东厢房",
		"default_rent": "1000", "referral_mode": "fixed", "referral_value": "100",
	}, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("低于最低介绍费 = %d, want 400", w.Code)
	}
	// 达标：通过审核后写进房源
	w = roomApplySubmit(t, env, token, map[string]string{
		"name": "王村民", "phone": "13800001111", "community": "幸福村", "room_no": "东厢房",
		"default_rent": "1000", "referral_mode": "percent", "referral_value": "50",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("提交: %d %s", w.Code, w.Body.String())
	}
	appID := uint(num(field(t, decodeBody(t, w), "data", "id")))
	var app RoomApplication
	env.db.First(&app, appID)
	if app.ReferralMode != "percent" || app.ReferralValue != 50 {
		t.Errorf("申请里的介绍费 = %q/%v", app.ReferralMode, app.ReferralValue)
	}
	if w := doJSON(env.r, http.MethodPost,
		fmt.Sprintf("/api/rental/room-applications/%d/approve?ledger_id=%d", appID, ledger), owner.token, nil); w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	var room Room
	env.db.Where("community = ? AND room_no = ?", "幸福村", "东厢房").First(&room)
	if room.ReferralMode != "percent" || room.ReferralValue != 50 {
		t.Errorf("审核后房源的介绍费 = %q/%v, want percent/50", room.ReferralMode, room.ReferralValue)
	}

	// off：登记页带介绍费也被忽略
	setReferralPolicy(t, env, referralPolicyOff, "300")
	w = roomApplySubmit(t, env, token, map[string]string{
		"name": "赵村民", "phone": "13800002222", "community": "幸福村", "room_no": "西厢房",
		"referral_mode": "fixed", "referral_value": "999",
	}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("off 下提交: %d %s", w.Code, w.Body.String())
	}
	appID2 := uint(num(field(t, decodeBody(t, w), "data", "id")))
	var app2 RoomApplication
	env.db.First(&app2, appID2)
	if app2.ReferralMode != "" || app2.ReferralValue != 0 {
		t.Errorf("off 模式下申请不该带介绍费：%q/%v", app2.ReferralMode, app2.ReferralValue)
	}
}
