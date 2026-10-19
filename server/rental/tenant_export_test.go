// tenant_export_test.go — 租户批量导出 CSV（派出所场景）的用例。
//
// 覆盖五组：
//  1. 表头 = 租客模板 15 列 + 末尾 4 列只读列（顺序不能变，回导靠截断）；
//  2. 逐字段正确：身份证号、押金、入住/到期日期、生效计费与缴费设置；
//  3. **筛选与列表一致**：active / keyword / community / tag / room_id 各筛一遍，
//     导出条数与列表接口逐一对齐（「列表看到什么导出就是什么」是这功能最要紧的口径）；
//  4. 台账隔离：A 台账导出看不到 B 台账的租户，显式指定无权台账被 403；
//  5. 权限：租户模块只有查看档也能导出（不该被 full 挡）；
//  6. 出生日期/性别由身份证派生：18 位派生、15 位留空；
//  7. 空结果仍输出表头（派出所拿到空表要能看出查询条件）。
//
// 全部走真实 Gin 路由 + 真实 SQLite（与 rental_test.go 的 setupEnv 同一装配路径），
// 只把响应体当 CSV 解析——导出的价值全在「产出的文件能不能被 Excel 打开」，mock 掉
// handler 等于没测。
package rental

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// tenantExportHeader 导出的完整表头（15 列模板 + 末尾 4 列只读信息）。
var tenantExportHeader = append(append([]string{}, tenantColumns[:]...), tenantExportExtraColumns[:]...)

// exportTenantsCSV 调导出接口并把响应体解析成 [][]string（剥掉 UTF-8 BOM）。
// token 省略时用环境默认账号；传空串同样按默认账号处理。
func exportTenantsCSV(t *testing.T, env *testEnv, query string, token ...string) [][]string {
	t.Helper()
	path := "/api/rental/tenants/export"
	if query != "" {
		path += "?" + query
	}
	tok := env.token
	if len(token) > 0 && token[0] != "" {
		tok = token[0]
	}
	w := doJSON(env.r, http.MethodGet, path, tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("tenants export %s: %d %s", query, w.Code, w.Body.String())
	}
	body := strings.TrimPrefix(w.Body.String(), "\ufeff")
	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("解析导出 CSV 失败: %v\n%s", err, body)
	}
	return records
}

// assertExportHeader 表头必须与「15 列模板 + 4 列追加」逐列相同：
// 多一列少一列、顺序变了都会破坏回导能力。
func assertExportHeader(t *testing.T, records [][]string) {
	t.Helper()
	if len(records) == 0 {
		t.Fatal("导出内容为空，连表头都没有")
	}
	got := records[0]
	if len(got) != len(tenantExportHeader) {
		t.Fatalf("表头列数 = %d, want %d: %v", len(got), len(tenantExportHeader), got)
	}
	for i, want := range tenantExportHeader {
		if got[i] != want {
			t.Fatalf("第 %d 列表头 = %q, want %q（完整表头 %v）", i+1, got[i], want, got)
		}
	}
}

// exportRowBy 从导出结果里按姓名取数据行（找不到即失败）。
func exportRowBy(t *testing.T, records [][]string, name string) []string {
	t.Helper()
	for _, row := range records[1:] {
		if len(row) > 2 && row[2] == name {
			return row
		}
	}
	t.Fatalf("导出结果里没有租户 %q：%v", name, records)
	return nil
}

// ---------------------------------------------------------------------------
// 一、表头与逐字段正确
// ---------------------------------------------------------------------------

func TestTenantsExportHeaderAndFields(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "101")

	// 带全套字段建档：身份证 18 位（第 17 位奇数 = 男）、押金、计费与缴费设置。
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "张三", "phone": "13800000001",
		"id_card": "110101199001011234", "move_in_date": "2026-01-01",
		"lease_end_date": "2026-12-31", "deposit": 3000,
		"water_mode": "meter", "water_price": 5, "pay_cycle": "quarterly",
		"pay_day": 5, "remind_days": 3, "notes": "首次租房",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create tenant: %d %s", w.Code, w.Body.String())
	}

	records := exportTenantsCSV(t, env, "")
	assertExportHeader(t, records)
	if len(records) != 2 {
		t.Fatalf("导出行数 = %d, want 表头+1 行：%v", len(records), records)
	}
	row := records[1]
	if len(row) != len(tenantExportHeader) {
		t.Fatalf("数据行列数 = %d, want %d: %v", len(row), len(tenantExportHeader), row)
	}
	// 前 15 列：与导入模板一一对应（顺序即 tenantColumns）。
	want := map[int]string{
		0: "阳光小区", 1: "101", 2: "张三", 3: "13800000001", 4: "110101199001011234",
		5: "2026-01-01", 6: "2026-12-31", 7: "3000",
		8: "按吨", 9: "5", 10: "", 11: "季付", 12: "5", 13: "3", 14: "首次租房",
		// 追加 4 列：在租状态、出生日期、年龄、性别。
		// 不再重复「房号」（前 15 列已有），避免表头重名导致 Excel 透视失败。
		15: "在租", 16: "1990-01-01", 17: "36", 18: "男",
	}
	for i, wantV := range want {
		if row[i] != wantV {
			t.Errorf("第 %d 列（%s） = %q, want %q", i+1, tenantExportHeader[i], row[i], wantV)
		}
	}
}

// TestTenantsExportEffectiveBillingWhenFollowingGlobal 租户留空（跟随全局默认）时，
// 导出的是**生效值**（台账/全局默认），与房源导出的口径一致——派出所拿到的表
// 不该出现空的水费模式与缴费周期。
func TestTenantsExportEffectiveBillingWhenFollowingGlobal(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "201")
	env.seedTenant(roomID, "李四") // 全部留空 = 跟随全局

	records := exportTenantsCSV(t, env, "")
	row := exportRowBy(t, records, "李四")
	if row[8] != "按吨" {
		t.Errorf("水费模式 = %q, want 按吨（全局默认）", row[8])
	}
	if row[9] != "5" {
		t.Errorf("水费单价 = %q, want 5（全局默认单价）", row[9])
	}
	if row[10] != "" {
		t.Errorf("包月金额 = %q, want 空（按吨口径不填）", row[10])
	}
	if row[11] != "月付" {
		t.Errorf("缴费周期 = %q, want 月付（全局默认）", row[11])
	}
	if row[13] != "3" {
		t.Errorf("提前提醒天数 = %q, want 3（全局默认）", row[13])
	}
}

// ---------------------------------------------------------------------------
// 二、筛选与列表一致
// ---------------------------------------------------------------------------

// TestTenantsExportFiltersMatchList 逐个筛选参数与列表接口对齐条数：
// 「列表看到什么，导出就是什么」。
func TestTenantsExportFiltersMatchList(t *testing.T) {
	env := setupEnv(t)
	roomA := seedCommunityRoom(t, env, "阳光小区", "301")
	roomB := seedCommunityRoom(t, env, "翡翠湾", "401")

	// 在租（阳光小区/301，带标签「优质」）、在租（翡翠湾/401）、已退租（阳光小区/301）。
	activeA := env.seedTenant(roomA, "在租甲")
	env.seedTenant(roomB, "在租乙")
	checkedOut := env.seedTenant(roomA, "退租丙")
	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", checkedOut), map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("checkout: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", activeA), map[string]interface{}{
		"room_id": roomA, "name": "在租甲", "tags": []string{"优质"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("tag tenant: %d %s", w.Code, w.Body.String())
	}

	cases := []struct {
		name  string
		query string
		want  []string // 期望出现的姓名
	}{
		{"全部", "", []string{"在租甲", "在租乙", "退租丙"}},
		{"active=1", "active=1", []string{"在租甲", "在租乙"}},
		{"active=0", "active=0", []string{"退租丙"}},
		{"keyword", "keyword=乙", []string{"在租乙"}},
		{"community", "community=阳光小区", []string{"在租甲", "退租丙"}},
		{"tag", "tag=优质", []string{"在租甲"}},
		{"room_id", "room_id=" + itoa(roomB), []string{"在租乙"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			records := exportTenantsCSV(t, env, tc.query)
			assertExportHeader(t, records)
			got := map[string]bool{}
			for _, row := range records[1:] {
				got[row[2]] = true
			}
			if len(records)-1 != len(tc.want) {
				t.Fatalf("导出行数 = %d, want %d（%v）", len(records)-1, len(tc.want), records)
			}
			for _, name := range tc.want {
				if !got[name] {
					t.Errorf("导出缺少 %q：%v", name, records)
				}
			}

			// 与列表接口对齐条数：同一组参数，两个接口必须给出同样多的租户。
			lw := env.do(http.MethodGet, "/api/rental/tenants?"+tc.query, nil)
			if lw.Code != http.StatusOK {
				t.Fatalf("list %s: %d %s", tc.query, lw.Code, lw.Body.String())
			}
			listTotal := int(field(t, decodeBody(t, lw), "data", "total").(float64))
			if listTotal != len(tc.want) {
				t.Fatalf("列表 total = %d, 导出 %d 行，口径不一致（%s）", listTotal, len(records)-1, tc.query)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 三、台账隔离与权限
// ---------------------------------------------------------------------------

func TestTenantsExportLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	a := newPersonalLedger(t, env, "export-a")
	b := newPersonalLedger(t, env, "export-b")

	// A 台账建房 + 租户；B 台账同样。
	roomA := seedRoomInLedger(t, env, a.token, a.ledgerID, "A区", "501", "甲租户")
	_ = seedRoomInLedger(t, env, b.token, b.ledgerID, "B区", "601", "乙租户")

	records := exportTenantsCSV(t, env, "ledger_id="+itoa(a.ledgerID), a.token)
	assertExportHeader(t, records)
	if len(records) != 2 || records[1][2] != "甲租户" {
		t.Fatalf("A 台账导出 = %v, want 只有甲租户", records)
	}
	for _, row := range records {
		if len(row) > 2 && row[2] == "乙租户" {
			t.Fatal("A 台账导出里出现了 B 台账的租户（跨台账越权）")
		}
	}

	// B 显式指定 A 的台账 → 403（不是成员）。
	if w := doJSON(env.r, http.MethodGet,
		"/api/rental/tenants/export?ledger_id="+itoa(a.ledgerID), b.token, nil); w.Code != http.StatusForbidden {
		t.Fatalf("B 导出 A 台账 = %d, want 403", w.Code)
	}
	// 不带参数：各自只看自己的台账。
	recordsB := exportTenantsCSV(t, env, "", b.token)
	if len(recordsB) != 2 || recordsB[1][2] != "乙租户" {
		t.Fatalf("B 台账导出 = %v, want 只有乙租户", recordsB)
	}
	_ = roomA
}

// TestTenantsExportRequiresViewOnly 只有 tenants:view 的成员也能导出：
// 导出是只读操作，不该被 full 挡（否则只读员工给派出所导表得先要完全权限）。
func TestTenantsExportRequiresViewOnly(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "export-owner")
	shared := createSharedLedger(t, env, owner.token, "只读台账")

	// 管理者建房 + 租户。
	seedRoomInLedger(t, env, owner.token, shared, "阳光小区", "701", "只读租户")

	// 只勾 tenants:view 的成员。
	memberID, memberTok := mkUser(t, env, "export-viewer", UserStatusActive)
	addActiveMember(t, env, shared, memberID, ModulePerms{ModuleTenants: PermView})

	records := exportTenantsCSV(t, env, "ledger_id="+itoa(shared), memberTok)
	assertExportHeader(t, records)
	if len(records) != 2 || records[1][2] != "只读租户" {
		t.Fatalf("只读成员导出 = %v, want 一行只读租户", records)
	}

	// 对照：完全没勾租户模块的成员被挡（403），证明门禁真的挂在路由上。
	outsiderID, outsiderTok := mkUser(t, env, "export-outsider", UserStatusActive)
	addActiveMember(t, env, shared, outsiderID, ModulePerms{ModuleRooms: PermView})
	if w := doJSON(env.r, http.MethodGet,
		"/api/rental/tenants/export?ledger_id="+itoa(shared), outsiderTok, nil); w.Code != http.StatusForbidden {
		t.Fatalf("无租户模块权限导出 = %d, want 403", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 四、身份证派生与空结果
// ---------------------------------------------------------------------------

// TestTenantsExportDerivesFromIDCard 18 位身份证派生出生日期与性别（复用
// deriveFromIDCard 的口径），15 位老号段不猜、留空。
func TestTenantsExportDerivesFromIDCard(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "801")

	create := func(name, idCard string) {
		t.Helper()
		w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
			"room_id": roomID, "name": name, "id_card": idCard,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("create %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	create("十八位男", "110101199001011234") // 第 17 位 3（奇）= 男
	create("十八位女", "330102201505054322") // 第 17 位 2（偶）= 女
	create("十五位", "110101900101123")
	create("无证件", "")

	records := exportTenantsCSV(t, env, "")
	// 列位：16=出生日期、17=年龄、18=性别（15=在租状态）。
	if row := exportRowBy(t, records, "十八位男"); row[16] != "1990-01-01" || row[18] != "男" {
		t.Errorf("18 位男派生 = %q/%q, want 1990-01-01/男", row[16], row[18])
	}
	if row := exportRowBy(t, records, "十八位女"); row[16] != "2015-05-05" || row[18] != "女" {
		t.Errorf("18 位女派生 = %q/%q, want 2015-05-05/女", row[16], row[18])
	}
	if row := exportRowBy(t, records, "十五位"); row[16] != "" || row[17] != "" || row[18] != "" {
		t.Errorf("15 位派生 = %q/%q/%q, want 留空（老号段不猜）", row[16], row[17], row[18])
	}
	if row := exportRowBy(t, records, "无证件"); row[16] != "" || row[17] != "" || row[18] != "" {
		t.Errorf("无证件派生 = %q/%q/%q, want 留空", row[16], row[17], row[18])
	}
	// 年龄必须按导出当天算：1990-01-01 出生的人，2026 年内周岁是 35 或 36，
	// 取决于查询当天是否已过生日。用区间断言避免用例在生日前后翻车。
	if row := exportRowBy(t, records, "十八位男"); row[17] != "35" && row[17] != "36" {
		t.Errorf("年龄 = %q, want 35 或 36（按导出当天算周岁）", row[17])
	}
	if row := exportRowBy(t, records, "十八位女"); row[17] != "10" && row[17] != "11" {
		t.Errorf("年龄 = %q, want 10 或 11（2015-05-05 出生）", row[17])
	}
}

// TestTenantsExportEmptyStillHasHeader 没有任何匹配租户时也要输出表头：
// 派出所拿到的空表要能看出查询的是哪些列（以及自己加的条件）。
func TestTenantsExportEmptyStillHasHeader(t *testing.T) {
	env := setupEnv(t)

	// 一个租户都没有。
	records := exportTenantsCSV(t, env, "")
	assertExportHeader(t, records)
	if len(records) != 1 {
		t.Fatalf("空库导出行数 = %d, want 只有表头", len(records))
	}

	// 有租户但筛选不中。
	roomID := seedCommunityRoom(t, env, "阳光小区", "901")
	env.seedTenant(roomID, "有租户")
	records = exportTenantsCSV(t, env, "keyword=查无此人")
	assertExportHeader(t, records)
	if len(records) != 1 {
		t.Fatalf("筛选不中导出行数 = %d, want 只有表头", len(records))
	}
}

// TestTenantsExportFirst15ColumnsReimportable 回导保护：把导出的**前 15 列**
// 原样改回 CSV 后必须能被租客导入接口接受（追加列在末尾、导入按列数截断，
// 所以导出→改→再导入这条路不能断）。这是「不在末尾加列」这条设计约束的守卫：
// 哪天有人把追加列插到中间或改动前 15 列顺序，这条用例就会红。
func TestTenantsExportFirst15ColumnsReimportable(t *testing.T) {
	env := setupEnv(t)
	roomID := seedCommunityRoom(t, env, "阳光小区", "1001")
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "回导甲", "phone": "13800000009",
		"id_card": "110101199001011234", "move_in_date": "2026-01-01",
		"lease_end_date": "2026-12-31", "deposit": 2500,
		"water_mode": "meter", "water_price": 6, "pay_cycle": "monthly", "pay_day": 8, "remind_days": 2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create tenant: %d %s", w.Code, w.Body.String())
	}

	records := exportTenantsCSV(t, env, "")
	if len(records) != 2 {
		t.Fatalf("导出行数 = %d, want 表头+1", len(records))
	}
	// 只裁剪出前 15 列（模拟用户「导出→改→再导入」时的模板列）。
	lines := []string{strings.Join(records[0][:len(tenantColumns)], ",")}
	for _, row := range records[1:] {
		lines = append(lines, strings.Join(row[:len(tenantColumns)], ","))
	}
	csvBody := "\ufeff" + strings.Join(lines, "\n")

	w = uploadContract(t, env, "/api/rental/tenants/import", "file", "tenants.csv", []byte(csvBody))
	if w.Code != http.StatusOK {
		t.Fatalf("回导前 15 列 = %d %s（表头必须与导入模板一致）", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 0 || res["updated"].(float64) != 1 || res["failed"].(float64) != 0 {
		t.Fatalf("回导结果 = %v, want updated=1（同一租户合并更新、不重复建档）", res)
	}
	// 回导后原值不被破坏（空单元格保留旧值这条既有合并规则）。
	tenant := findImportedTenant(t, env, roomID, "回导甲")
	if tenant.Phone != "13800000009" || tenant.Deposit != 2500 || tenant.IDCard != "110101199001011234" {
		t.Fatalf("回导后租户 = %+v，字段被破坏", tenant)
	}
	var count int64
	env.db.Model(&Tenant{}).Where("room_id = ?", roomID).Count(&count)
	if count != 1 {
		t.Fatalf("回导后有 %d 个租户，want 1（不得重复建档）", count)
	}
}

// seedRoomInLedger 在某台账里建房 + 一名在租租户，返回房间 ID。
func seedRoomInLedger(t *testing.T, env *testEnv, token string, ledgerID uint, community, roomNo, tenantName string) uint {
	t.Helper()
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms?ledger_id="+itoa(ledgerID), token,
		map[string]interface{}{"community": community, "room_no": roomNo, "default_rent": 1500})
	if w.Code != http.StatusOK {
		t.Fatalf("seed room %s/%s: %d %s", community, roomNo, w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(env.r, http.MethodPost, "/api/rental/tenants", token,
		map[string]interface{}{"room_id": roomID, "name": tenantName})
	if w.Code != http.StatusOK {
		t.Fatalf("seed tenant %s: %d %s", tenantName, w.Code, w.Body.String())
	}
	return roomID
}
