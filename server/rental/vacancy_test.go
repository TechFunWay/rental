// vacancy_test.go — 空置分析（issue #2）的口径与隔离回归测试。
//
// 覆盖五类「读代码不足以证明」的行为：
//
//  1. 无数据台账：返回零值结构而不是报错，区间与 data_from 仍然可用。
//  2. 当前空置判定：完全由 active 租户派生（与 stats.go 同源）。
//  3. 历史空置按月重建：日期齐全的房逐月判定、日期缺失的房历史整体不计入。
//  4. 排序与分页：sort=loss/months 两种口径 + 内存切片分页。
//  5. 台账隔离：A 的台账读不到 B 的房源与租户。
//
// 全部走真实 HTTP（httptest），不直接调 handler——路由与权限门禁必须真的挂上。
package rental

import (
	"net/http"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// fixture 助手
// ---------------------------------------------------------------------------

// vacancyPeriodAgo 距当前月 n 个月的账期（YYYY-MM）。
func vacancyPeriodAgo(n int) string { return addMonths(currentPeriodStr(), -n) }

// vacancyMonthTime 账期的当月 1 号（用来给房源设置建档时间）。
func vacancyMonthTime(period string) time.Time {
	t, err := parsePeriod(period)
	if err != nil {
		return time.Now()
	}
	return t
}

// vacancySeedRoom 直接落一间房：显式指定台账与建档时间（走 API 无法控制
// CreatedAt，而历史窗口的起点正是由它决定的）。
//
// GORM 只在 CreatedAt 为零值时自动填当前时间，这里给了非零值就会被保留。
func vacancySeedRoom(t *testing.T, env *testEnv, ledgerID uint, roomNo string, rent float64, createdAt time.Time) uint {
	t.Helper()
	room := Room{
		LedgerID: ledgerID, RoomNo: roomNo, DefaultRent: rent, CreatedAt: createdAt,
	}
	if err := env.db.Create(&room).Error; err != nil {
		t.Fatalf("seed room %s: %v", roomNo, err)
	}
	return room.ID
}

// vacancySeedTenant 直接落一名租户（含在租/退租与日期），返回 ID。
//
// Active 必须**建完再显式改**：Tenant.Active 打了 `gorm:"default:true"`，GORM 对
// 带默认值标签的零值字段会跳过插入、让数据库填默认值，于是 Active=false 会被顶成
// true——退租租户全变成在租，历史重建的断言就会失真。这个坑与
// ledger_api_test.go 里给 User.Status 补写的处理同源。
func vacancySeedTenant(t *testing.T, env *testEnv, ledgerID, roomID uint, name string, active bool, moveIn, moveOut string) uint {
	t.Helper()
	tenant := Tenant{
		LedgerID: ledgerID, RoomID: roomID, Name: name,
		Active: active, MoveInDate: moveIn, MoveOutDate: moveOut,
	}
	if err := env.db.Create(&tenant).Error; err != nil {
		t.Fatalf("seed tenant %s: %v", name, err)
	}
	if !active {
		if err := env.db.Model(&Tenant{}).Where("id = ?", tenant.ID).
			Update("active", false).Error; err != nil {
			t.Fatalf("mark tenant %s inactive: %v", name, err)
		}
	}
	return tenant.ID
}

// vacancyFetch 调接口并返回 data 对象。
func vacancyFetch(t *testing.T, env *testEnv, token, query string) map[string]interface{} {
	t.Helper()
	w := doJSON(env.r, http.MethodGet, "/api/rental/analytics/vacancy"+query, token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("vacancy analytics %s: %d %s", query, w.Code, w.Body.String())
	}
	return field(t, decodeBody(t, w), "data").(map[string]interface{})
}

// vacancySummaryOf 取 summary 并断言几个整型字段。
func vacancySummaryOf(t *testing.T, data map[string]interface{}) map[string]interface{} {
	t.Helper()
	return field(t, data, "summary").(map[string]interface{})
}

// ---------------------------------------------------------------------------
// 1. 无数据台账
// ---------------------------------------------------------------------------

func TestVacancyEmptyLedger(t *testing.T) {
	env := setupEnv(t)

	data := vacancyFetch(t, env, env.token, "?months=6")
	summary := vacancySummaryOf(t, data)

	if got := num(summary["vacant_rooms"]); got != 0 {
		t.Errorf("vacant_rooms = %v, want 0", got)
	}
	if got := num(summary["rooms_total"]); got != 0 {
		t.Errorf("rooms_total = %v, want 0", got)
	}
	if got := num(summary["vacancy_rate"]); got != 0 {
		t.Errorf("vacancy_rate = %v, want 0", got)
	}
	if got := num(summary["vacant_room_months"]); got != 0 {
		t.Errorf("vacant_room_months = %v, want 0", got)
	}
	if got := num(summary["loss_estimate"]); got != 0 {
		t.Errorf("loss_estimate = %v, want 0", got)
	}

	series := field(t, data, "series").([]interface{})
	if len(series) != 6 {
		t.Fatalf("series len = %d, want 6（区间仍要按月铺满）", len(series))
	}
	rooms := field(t, data, "rooms").(map[string]interface{})
	if got := num(rooms["total"]); got != 0 {
		t.Errorf("rooms.total = %v, want 0", got)
	}
	if items := rooms["items"].([]interface{}); len(items) != 0 {
		t.Errorf("rooms.items len = %d, want 0", len(items))
	}
	if got := field(t, data, "data_from").(string); got != vacancyPeriodAgo(5) {
		t.Errorf("data_from = %q, want %q", got, vacancyPeriodAgo(5))
	}
	if got := field(t, data, "caveat").(string); got != "" {
		t.Errorf("caveat = %q, want 空串（没有房源就没有估算）", got)
	}
}

// ---------------------------------------------------------------------------
// 2. 当前空置判定（实时，由 active 租户派生）
// ---------------------------------------------------------------------------

func TestVacancyCurrentStatusDerivedFromActiveTenants(t *testing.T) {
	env := setupEnv(t)
	past := vacancyMonthTime(vacancyPeriodAgo(5))

	occupied := vacancySeedRoom(t, env, env.personalLedger, "101", 1000, past)
	vacancySeedRoom(t, env, env.personalLedger, "102", 1000, past)
	vacancySeedRoom(t, env, env.personalLedger, "103", 1000, past)
	// 在租 → 占用。
	vacancySeedTenant(t, env, env.personalLedger, occupied, "在租客", true, vacancyPeriodAgo(3)+"-01", "")
	// 已退租（日期齐全）→ 当前不算占用。
	vacancySeedTenant(t, env, env.personalLedger, occupied, "已退客", false,
		vacancyPeriodAgo(5)+"-01", vacancyPeriodAgo(4)+"-01")

	data := vacancyFetch(t, env, env.token, "?months=6")
	summary := vacancySummaryOf(t, data)

	if got := num(summary["rooms_total"]); got != 3 {
		t.Errorf("rooms_total = %v, want 3", got)
	}
	// 101 有在租租户 → 占用；102/103 无租户 → 空置。已退租的房不被算作占用。
	if got := num(summary["vacant_rooms"]); got != 2 {
		t.Errorf("vacant_rooms = %v, want 2（101 有在租租户）", got)
	}
	if got := num(summary["vacancy_rate"]); !almostEqual(got, 66.7) {
		t.Errorf("vacancy_rate = %v, want 66.7", got)
	}

	rooms := field(t, data, "rooms").(map[string]interface{})
	items := rooms["items"].([]interface{})
	statusOf := map[string]string{}
	for _, it := range items {
		row := it.(map[string]interface{})
		statusOf[row["room_label"].(string)] = row["current_status"].(string)
	}
	if statusOf["101"] != "occupied" {
		t.Errorf("101 current_status = %q, want occupied", statusOf["101"])
	}
	if statusOf["102"] != "vacant" || statusOf["103"] != "vacant" {
		t.Errorf("102/103 current_status = %q/%q, want vacant",
			statusOf["102"], statusOf["103"])
	}
}

// ---------------------------------------------------------------------------
// 3. 历史空置按月重建（含日期缺失的房不计入）
// ---------------------------------------------------------------------------

// TestVacancyHistoryRebuild 是最核心的口径用例，四间房各考一件事：
//
//	101：在租，3 个月前入住 → 前 2 个月空置、后 4 个月占用
//	102：退租，4 个月前入住、3 个月前退租 → 空置 4 个月
//	103：在租但没填入住日期 → 历史不可重建，只在当前月按实时状态统计
//	104：从无租户 → 整段区间都空置
//
// 区间近 6 个月，下标 0 = 5 个月前，下标 5 = 当前月。
func TestVacancyHistoryRebuild(t *testing.T) {
	env := setupEnv(t)
	created := vacancyMonthTime(vacancyPeriodAgo(6)) // 建档早于区间起点

	r101 := vacancySeedRoom(t, env, env.personalLedger, "101", 1000, created)
	r102 := vacancySeedRoom(t, env, env.personalLedger, "102", 1000, created)
	r103 := vacancySeedRoom(t, env, env.personalLedger, "103", 1000, created)
	vacancySeedRoom(t, env, env.personalLedger, "104", 1000, created)

	vacancySeedTenant(t, env, env.personalLedger, r101, "长租客", true, vacancyPeriodAgo(3)+"-01", "")
	vacancySeedTenant(t, env, env.personalLedger, r102, "短租客", false,
		vacancyPeriodAgo(4)+"-01", vacancyPeriodAgo(3)+"-28")
	// 日期缺失：入住日期留空（接口允许、登记不校验）。
	vacancySeedTenant(t, env, env.personalLedger, r103, "没填日期", true, "", "")

	data := vacancyFetch(t, env, env.token, "?months=6")
	summary := vacancySummaryOf(t, data)

	// 101: 2；102: 4；103: 0（历史不计入）；104: 6 → 合计 12 个空置房月。
	if got := num(summary["vacant_room_months"]); got != 12 {
		t.Errorf("vacant_room_months = %v, want 12", got)
	}
	// 固定费 1000 + 0 + 0 = 1000 元/月，包月水费未开启。
	if got := num(summary["loss_estimate"]); !almostEqual(got, 12000) {
		t.Errorf("loss_estimate = %v, want 12000", got)
	}
	// 建档早于区间起点 → data_from 就是区间起点。
	if got := field(t, data, "data_from").(string); got != vacancyPeriodAgo(5) {
		t.Errorf("data_from = %q, want %q", got, vacancyPeriodAgo(5))
	}
	// 有有效入住日期的房：101、102 → 2 间。
	if got := num(summary["rooms_dated"]); got != 2 {
		t.Errorf("rooms_dated = %v, want 2（103 缺日期、104 无租户）", got)
	}
	// 103 缺日期 → 必须有 caveat 提示。
	if got := field(t, data, "caveat").(string); got == "" {
		t.Error("caveat 为空，want 非空（存在日期缺失的租户）")
	}

	// 逐月趋势：每月空置房数（101 占用从下标 2 起、102 占用下标 1~2）。
	wantVacant := []int{3, 2, 1, 2, 2, 2}
	series := field(t, data, "series").([]interface{})
	if len(series) != len(wantVacant) {
		t.Fatalf("series len = %d, want %d", len(series), len(wantVacant))
	}
	for i, want := range wantVacant {
		row := series[i].(map[string]interface{})
		if got := num(row["vacant_rooms"]); int(got) != want {
			t.Errorf("series[%d] (%v) vacant_rooms = %v, want %d",
				i, row["period"], got, want)
		}
	}
	// 下标 0 分母 3（101/102/104；103 历史不可重建不计入）→ 3/3 = 100%。
	p0 := series[0].(map[string]interface{})
	if got := num(p0["vacancy_rate"]); !almostEqual(got, 100) {
		t.Errorf("series[0] vacancy_rate = %v, want 100（103 不该进分母）", got)
	}
	// 当前月分母 4（实时状态对每间房都成立）→ 2/4 = 50%。
	p5 := series[5].(map[string]interface{})
	if got := num(p5["vacancy_rate"]); !almostEqual(got, 50) {
		t.Errorf("series[5] vacancy_rate = %v, want 50", got)
	}

	// 汇总卡的当前空置 = 趋势最后一期，两处必须一致。
	last := series[len(series)-1].(map[string]interface{})
	if num(summary["vacant_rooms"]) != num(last["vacant_rooms"]) {
		t.Errorf("summary.vacant_rooms = %v, series last = %v，两处口径必须一致",
			num(summary["vacant_rooms"]), num(last["vacant_rooms"]))
	}

	// 按房排行：103 历史不可重建 → 0 空置月；104 六个月内每月都空置。
	rooms := field(t, data, "rooms").(map[string]interface{})
	monthsOf := map[string]int{}
	for _, it := range rooms["items"].([]interface{}) {
		row := it.(map[string]interface{})
		monthsOf[row["room_label"].(string)] = int(num(row["vacant_months"]))
	}
	for label, want := range map[string]int{"101": 2, "102": 4, "103": 0, "104": 6} {
		if monthsOf[label] != want {
			t.Errorf("%s vacant_months = %d, want %d", label, monthsOf[label], want)
		}
	}

	// 包月水费参与损失：把台账水费模式改成包月 100 元后，单月损失应变成 1100。
	if err := setLedgerConfigValue(env.db, "rental_water_mode", waterModeMonthly, env.personalLedger); err != nil {
		t.Fatalf("set water mode: %v", err)
	}
	if err := setLedgerConfigValue(env.db, "rental_water_monthly_fee", "100", env.personalLedger); err != nil {
		t.Fatalf("set water monthly fee: %v", err)
	}
	data = vacancyFetch(t, env, env.token, "?months=6")
	summary = vacancySummaryOf(t, data)
	if got := num(summary["loss_estimate"]); !almostEqual(got, 13200) {
		t.Errorf("包月水费后 loss_estimate = %v, want 13200（12 × 1100）", got)
	}
}

// TestVacancyRoomNoTenantRecordIsAllVacant 无租户记录的房既不是「不可信」也不该
// 被当成占用：整段可追溯区间都计空置（这正是「租不出去」的典型形态）。
func TestVacancyRoomNoTenantRecordIsAllVacant(t *testing.T) {
	env := setupEnv(t)
	vacancySeedRoom(t, env, env.personalLedger, "101", 800, vacancyMonthTime(vacancyPeriodAgo(6)))

	data := vacancyFetch(t, env, env.token, "?months=6")
	summary := vacancySummaryOf(t, data)
	if got := num(summary["vacant_room_months"]); got != 6 {
		t.Errorf("vacant_room_months = %v, want 6", got)
	}
	if got := num(summary["loss_estimate"]); !almostEqual(got, 4800) {
		t.Errorf("loss_estimate = %v, want 4800", got)
	}
	if got := num(summary["vacant_rooms"]); got != 1 {
		t.Errorf("vacant_rooms = %v, want 1", got)
	}
	// 无租户 → 没有缺失日期，不该无端弹 caveat。
	if got := field(t, data, "caveat").(string); got != "" {
		t.Errorf("caveat = %q, want 空串", got)
	}
}

// TestVacancyDataFromFollowsRoomCreation 房源建档晚于区间起点时，区间起点跟着
// 建档月走：建档之前房间还不存在，不能算空置损失。
func TestVacancyDataFromFollowsRoomCreation(t *testing.T) {
	env := setupEnv(t)
	// 建档于 2 个月前（区间是近 6 个月）。
	vacancySeedRoom(t, env, env.personalLedger, "101", 1000, vacancyMonthTime(vacancyPeriodAgo(2)))

	data := vacancyFetch(t, env, env.token, "?months=6")
	summary := vacancySummaryOf(t, data)
	if got := field(t, data, "data_from").(string); got != vacancyPeriodAgo(2) {
		t.Errorf("data_from = %q, want %q", got, vacancyPeriodAgo(2))
	}
	// 建档月（下标 3）到当前月（下标 5）共 3 个月计入；前面 3 个月房间还不存在。
	if got := num(summary["vacant_room_months"]); got != 3 {
		t.Errorf("vacant_room_months = %v, want 3（只算建档月及以后）", got)
	}
}

// ---------------------------------------------------------------------------
// 4. 排序与分页
// ---------------------------------------------------------------------------

func TestVacancySortAndPagination(t *testing.T) {
	env := setupEnv(t)
	created := vacancyMonthTime(vacancyPeriodAgo(6))

	// 空置月数 / 单月损失（租金）各不相同，便于区分两种排序口径。
	type seed struct {
		roomNo string
		rent   float64
		months int // 空置月数（= 无租户则 6；否则 6 - 已租月数）
	}
	seeds := []seed{
		{"101", 1000, 6}, // 从无租户
		{"102", 2000, 4}, // 2 个月前入住
		{"103", 3000, 3}, // 3 个月前入住
		{"104", 500, 6},  // 从无租户
		{"105", 100, 5},  // 1 个月前入住
	}
	for _, s := range seeds {
		id := vacancySeedRoom(t, env, env.personalLedger, s.roomNo, s.rent, created)
		if s.months < 6 {
			// 想让「空置 months 个月」，租户就要在 (5-months) 个月前入住：
			// 区间下标 0..5 对应当前月往前 5..0 个月，入住月及以后的月份算占用。
			vacancySeedTenant(t, env, env.personalLedger, id, s.roomNo+"租客", true,
				vacancyPeriodAgo(5-s.months)+"-01", "")
		}
	}

	// 默认 sort=loss：单月损失 = 租金（无其它固定费），损失 = 月数 × 租金。
	data := vacancyFetch(t, env, env.token, "?months=6&pageSize=100")
	items := field(t, data, "rooms", "items").([]interface{})
	gotOrder := make([]string, 0, len(items))
	for _, it := range items {
		gotOrder = append(gotOrder, it.(map[string]interface{})["room_label"].(string))
	}
	// 3000×3=9000 > 2000×4=8000 > 1000×6=6000 > 500×6=3000 > 100×5=500
	wantLoss := []string{"103", "102", "101", "104", "105"}
	if !equalStrings(gotOrder, wantLoss) {
		t.Errorf("sort=loss 顺序 = %v, want %v", gotOrder, wantLoss)
	}

	// sort=months：6、6、5、4、3；同值按房号升序 → 101 在 104 前。
	data = vacancyFetch(t, env, env.token, "?months=6&pageSize=100&sort=months")
	items = field(t, data, "rooms", "items").([]interface{})
	gotOrder = gotOrder[:0]
	for _, it := range items {
		gotOrder = append(gotOrder, it.(map[string]interface{})["room_label"].(string))
	}
	wantMonths := []string{"101", "104", "105", "102", "103"}
	if !equalStrings(gotOrder, wantMonths) {
		t.Errorf("sort=months 顺序 = %v, want %v", gotOrder, wantMonths)
	}

	// 非法 sort 回退默认（loss）；分页切片正确。
	data = vacancyFetch(t, env, env.token, "?months=6&sort=whatever&page=2&pageSize=2")
	rooms := field(t, data, "rooms").(map[string]interface{})
	if got := num(rooms["total"]); got != 5 {
		t.Errorf("rooms.total = %v, want 5", got)
	}
	if got := num(rooms["page"]); got != 2 {
		t.Errorf("rooms.page = %v, want 2", got)
	}
	if got := num(rooms["pageSize"]); got != 2 {
		t.Errorf("rooms.pageSize = %v, want 2", got)
	}
	page2 := rooms["items"].([]interface{})
	if len(page2) != 2 {
		t.Fatalf("page 2 len = %d, want 2", len(page2))
	}
	// loss 序第 2 页 = 101、104。
	if l0 := page2[0].(map[string]interface{})["room_label"].(string); l0 != "101" {
		t.Errorf("page2[0] = %q, want 101", l0)
	}
	if l1 := page2[1].(map[string]interface{})["room_label"].(string); l1 != "104" {
		t.Errorf("page2[1] = %q, want 104", l1)
	}

	// pageSize 超上限被 NormalizePage 收�窄到 100；page 越界返回空 items、total 不变。
	data = vacancyFetch(t, env, env.token, "?months=6&page=99&pageSize=999")
	rooms = field(t, data, "rooms").(map[string]interface{})
	if got := num(rooms["pageSize"]); got != 100 {
		t.Errorf("pageSize = %v, want 100（上限）", got)
	}
	if got := num(rooms["total"]); got != 5 {
		t.Errorf("total = %v, want 5", got)
	}
	if items := rooms["items"].([]interface{}); len(items) != 0 {
		t.Errorf("越界页 items len = %d, want 0", len(items))
	}
}

// ---------------------------------------------------------------------------
// 5. 台账隔离
// ---------------------------------------------------------------------------

func TestVacancyLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	alice := newPersonalLedger(t, env, "vacancy-alice")
	bob := newPersonalLedger(t, env, "vacancy-bob")

	created := vacancyMonthTime(vacancyPeriodAgo(6))
	// Alice：2 间房，其中 1 间有在租租户。
	a1 := vacancySeedRoom(t, env, alice.ledgerID, "101", 1000, created)
	vacancySeedRoom(t, env, alice.ledgerID, "102", 1000, created)
	vacancySeedTenant(t, env, alice.ledgerID, a1, "爱丽丝的租客", true, vacancyPeriodAgo(4)+"-01", "")
	// Bob：3 间房全空置。
	for _, no := range []string{"201", "202", "203"} {
		vacancySeedRoom(t, env, bob.ledgerID, no, 1000, created)
	}

	// Alice 只看得到自己的 2 间房。
	data := vacancyFetch(t, env, alice.token, "?months=6&ledger_id="+itoa(alice.ledgerID)+"&pageSize=100")
	summary := vacancySummaryOf(t, data)
	if got := num(summary["rooms_total"]); got != 2 {
		t.Errorf("Alice rooms_total = %v, want 2", got)
	}
	if got := num(summary["vacant_rooms"]); got != 1 {
		t.Errorf("Alice vacant_rooms = %v, want 1", got)
	}
	items := field(t, data, "rooms", "items").([]interface{})
	for _, it := range items {
		if label := it.(map[string]interface{})["room_label"].(string); label[0] != '1' {
			t.Errorf("Alice 看到了不属于她的房源 %q", label)
		}
	}

	// Bob 只看得到自己的 3 间房，且全空置。
	data = vacancyFetch(t, env, bob.token, "?months=6&ledger_id="+itoa(bob.ledgerID)+"&pageSize=100")
	summary = vacancySummaryOf(t, data)
	if got := num(summary["rooms_total"]); got != 3 {
		t.Errorf("Bob rooms_total = %v, want 3", got)
	}
	if got := num(summary["vacant_rooms"]); got != 3 {
		t.Errorf("Bob vacant_rooms = %v, want 3", got)
	}

	// 显式指定别人的台账 → 403（连统计口径都不暴露）。
	w := doJSON(env.r, http.MethodGet,
		"/api/rental/analytics/vacancy?ledger_id="+itoa(alice.ledgerID), bob.token, nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("Bob 读 Alice 台账 = %d, want 403", w.Code)
	}
}

// equalStrings 顺序完全一致的切片比较（排序断言用）。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
