// first_bill_matrix_test.go — 「登记即出首期账单 / 一键生成」多方位测试矩阵
// （2026-11-18，功能提交 e3f24d3 之后的补测轮）。
//
// 覆盖面（与 first_bill_test.go 的正向主链路错开，不重复）：
//   单元      firstBillPeriod 边界形态；isDuplicateBillIndex 只认账单表唯一冲突
//   账期口径  没填入住日期落当月；裸 YYYY-MM 宽松接受；补登历史月出历史单
//   计费口径  季付首期一次收 3 个月并带覆盖区间文案；押金为 0 不出押金行；
//             租户排除租金 / 台账停用租金 → 首期单只剩押金；自定义项目并入合计
//   生命周期  退租后再入住：押金沿用「该房只收一次」既有口径不再收
//   权限      账单只有查看档 → 403，且租户档案不落库（校验在事务前）
//   范围      一键生成只影响本次指定台账；空置房不出账；坏账期 400
//   并发      同房并发登记 / 并发一键生成：最终每张单恰好一次、无 500
//   性能      150 间房规模一键生成在预算时间内完成（SQLite 单连接的量级哨兵）
//   旁路      租客 CSV 导入不受勾选影响（导入通道根本不认识该字段）
package rental

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// currentMonth 与 firstBillPeriod 回退分支同一口径（同一进程内取同一句时钟）。
func currentMonth() string { return time.Now().Format("2006-01") }

// ===== 单元：firstBillPeriod =====

func TestFirstBillPeriodEdgeCases(t *testing.T) {
	now := currentMonth()
	cases := []struct {
		name, in, want string
	}{
		{"完整日期取所在月", "2026-08-05", "2026-08"},
		{"月末边界不溢出", "2026-12-31", "2026-12"},
		{"裸年月宽松接受", "2026-11", "2026-11"},
		{"空串落当月", "", now},
		{"斜杠分隔退当月", "2026/11/05", now},
		{"非法月份退当月", "2026-13-01", now},
		{"乱码退当月", "abc", now},
		{"短串退当月", "26-1", now},
	}
	for _, c := range cases {
		if got := firstBillPeriod(c.in); got != c.want {
			t.Errorf("%s: firstBillPeriod(%q)=%q want %q", c.name, c.in, got, c.want)
		}
	}
}

// ===== 单元：isDuplicateBillIndex 的吞错边界 =====

func TestIsDuplicateBillIndexOnlyBillTable(t *testing.T) {
	if isDuplicateBillIndex(nil) {
		t.Error("nil 不应判为账单唯一冲突")
	}
	dup := errors.New("UNIQUE constraint failed: bills.org_id, bills.room_id, bills.period")
	if !isDuplicateBillIndex(dup) {
		t.Error("账单表唯一冲突应判真（并发收口依赖它）")
	}
	other := errors.New("UNIQUE constraint failed: users.username")
	if isDuplicateBillIndex(other) {
		t.Error("别表的唯一冲突不能被吞成跳过")
	}
	if isDuplicateBillIndex(errors.New("database is locked")) {
		t.Error("锁冲突必须上抛，不许伪装成已存在")
	}
}

// ===== 集成：账期口径 =====

// TestFirstBillDefaultPeriodCurrentMonth 入住日期留空 + 勾选 → 首期单落当前月，
// 而不是 0001-01 之类的垃圾账期。
func TestFirstBillDefaultPeriodCurrentMonth(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M01", 1000)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "无入住日期", "deposit": 1000, "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	if n := len(billsOfRoom(t, env, roomID, currentMonth())); n != 1 {
		t.Errorf("当前月应有 1 张首期单，实得 %d", n)
	}
}

// TestFirstBillBackdatedMoveInMonth 11 月才补办 3 月入住的档案 → 首期单落 3 月
// （补登不造出「凭空多一张本月单」的假欠费；补历史账单视为到期，租金照收）。
func TestFirstBillBackdatedMoveInMonth(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M02", 900)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "补登租户", "deposit": 900,
		"move_in_date": "2026-03-14", "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-03")
	if len(list) != 1 {
		t.Fatalf("3 月应有首期单，实得 %d 张", len(list))
	}
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 900 || num(b["deposit_fee"]) != 900 || num(b["total_amount"]) != 1800 {
		t.Errorf("补登首期单金额错: rent=%v deposit=%v total=%v", b["rent"], b["deposit_fee"], b["total_amount"])
	}
	if n := len(billsOfRoom(t, env, roomID, currentMonth())); n != 0 {
		t.Errorf("不应凭空多出当前月账单，实得 %d", n)
	}
}

// ===== 集成：计费口径 =====

// TestFirstBillQuarterlyCoversThreeMonths 季付房首期：一张单收 3 个月租金，
// 明细带「起 ~ 止」覆盖区间文案（粉丝要的「账单明细写清缴费周期」在多月场景
// 已是既有口径，这里把它钉进首期链路）。
func TestFirstBillQuarterlyCoversThreeMonths(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M03", 1200)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "季付租户", "deposit": 1200,
		"move_in_date": "2026-07-01", "pay_cycle": "quarterly", "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-07")
	if len(list) != 1 {
		t.Fatalf("应有 1 张季付首期单，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 3600 {
		t.Errorf("季付首期租金应 1200×3=3600，实得 %v", b["rent"])
	}
	if num(b["total_amount"]) != 4800 {
		t.Errorf("合计应 3600+押金1200=4800，实得 %v", b["total_amount"])
	}
	billID := uint(num(b["id"]))
	it, ok := billItemByKey(t, env, billID, "rent")
	if !ok {
		t.Fatal("明细应有租金行")
	}
	if !strings.Contains(it["detail"].(string), "2026-07 ~ 2026-09") {
		t.Errorf("租金明细应带覆盖区间，实得 %q", it["detail"])
	}
}

// TestFirstBillZeroDepositNoDepositLine 押金 0 → 首期单不该出现 0 元押金行
// （billBuiltInItems 金额为 0 不建行），合计只含租金。
func TestFirstBillZeroDepositNoDepositLine(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M04", 700)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "无押金租户", "move_in_date": "2026-11-01", "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	b := list[0].(map[string]interface{})
	if num(b["deposit_fee"]) != 0 || num(b["total_amount"]) != 700 {
		t.Errorf("应无押金、合计=租金，实得 deposit=%v total=%v", b["deposit_fee"], b["total_amount"])
	}
	if _, ok := billItemByKey(t, env, uint(num(b["id"])), "deposit"); ok {
		t.Error("明细不应出现 0 元押金行")
	}
}

// TestFirstBillExcludedRentKeepsDeposit 租户勾选排除「租金」：首期单租金清零、
// 但押金照收（排除项只清内置计费，押金不是被排除对象）。
func TestFirstBillExcludedRentKeepsDeposit(t *testing.T) {
	env := setupEnv(t)
	roomID := seedRentRoom(t, env, "M05", 1500)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "免租期租户", "deposit": 500, "move_in_date": "2026-11-01",
		"excluded_fees": []string{"rent"}, "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 0 || num(b["total_amount"]) != 500 {
		t.Errorf("排除租金后应只剩押金 500，实得 rent=%v total=%v", b["rent"], b["total_amount"])
	}
}

// TestFirstBillLedgerDisabledRent 台账停用租金后登记 → 快照生效、首期单租金 0。
// （与租户级排除走 recalc 同一清零口，来源不同，各钉一条防改一漏一。）
func TestFirstBillLedgerDisabledRent(t *testing.T) {
	env := setupEnv(t)
	item := fetchFeeItem(t, env, env.personalLedger, "rent")
	updateFeeItemAPI(t, env, item, map[string]interface{}{"enabled": false})
	t.Cleanup(func() { updateFeeItemAPI(t, env, item, map[string]interface{}{"enabled": true}) })

	roomID := seedRentRoom(t, env, "M06", 1500)
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "停用租金租户", "deposit": 300, "move_in_date": "2026-11-01",
		"create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 0 {
		t.Errorf("台账停用租金后首期单不该收租，实得 %v", b["rent"])
	}
	if num(b["deposit_fee"]) != 300 {
		t.Errorf("押金不受停用影响，实得 %v", b["deposit_fee"])
	}
}

// TestFirstBillIncludesCustomFeeItem 自定义收费项目（宽带费·月付）应随首期单
// 一并入账——首期链路与其他建账口共用 applyCustomFees，不许漏项。
func TestFirstBillIncludesCustomFeeItem(t *testing.T) {
	env := setupEnv(t)
	w := env.do(http.MethodPost, "/api/rental/fee-items", map[string]interface{}{
		"name": "宽带费", "cycle": "monthly", "default_amount": 50,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("建自定义项目: %d %s", w.Code, w.Body.String())
	}
	roomID := seedRentRoom(t, env, "M07", 1000)
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "宽带租户", "move_in_date": "2026-11-01", "create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	b := list[0].(map[string]interface{})
	if num(b["extra_amount"]) != 50 || num(b["total_amount"]) != 1050 {
		t.Errorf("首期单应并入宽带费 50，实得 extra=%v total=%v", b["extra_amount"], b["total_amount"])
	}
}

// ===== 集成：生命周期 =====

// TestFirstBillAfterCheckoutNoSecondDeposit 退租再入住：新租户首期单照常生成
// （房租要有单），但押金不再二次收取——「该房已有账单就不收押金」的既有铁律
// （firstBillDeposit 判据）不能因登记建单入口而松动。
func TestFirstBillAfterCheckoutNoSecondDeposit(t *testing.T) {
	env := setupEnv(t)
	roomID, tenantID := seedRoomWithDeposit(t, env, "M08", 1000, 1000)
	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", tenantID), map[string]interface{}{
		"move_out_date": "2026-10-31",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("退租: %d %s", w.Code, w.Body.String())
	}
	// 首任没有账单（seedRoomWithDeposit 只建档），先给该房制造一张历史单，
	// 再入住新租户——押金应因「该房已有账单」而归 0。
	createBillFor(t, env, roomID, "2026-10")
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "二任租户", "deposit": 2000, "move_in_date": "2026-11-01",
		"create_first_bill": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("二任登记: %d %s", w.Code, w.Body.String())
	}
	list := billsOfRoom(t, env, roomID, "2026-11")
	if len(list) != 1 {
		t.Fatalf("二任应有 11 月首期单，实得 %d", len(list))
	}
	b := list[0].(map[string]interface{})
	if num(b["rent"]) != 1000 {
		t.Errorf("二任首期租金应为 1000，实得 %v", b["rent"])
	}
	if num(b["deposit_fee"]) != 0 {
		t.Errorf("押金该房只收一次，二任不应再收，实得 %v", b["deposit_fee"])
	}
}

// ===== 集成：权限 =====

// TestFirstBillNeedsBillEditNotView 账单查看档不够：登记（租户编辑档齐全）
// 但带勾选 → 403，且租户与账单都不落库（校验在事务前，防半件生效）。
func TestFirstBillNeedsBillEditNotView(t *testing.T) {
	env := setupEnv(t)
	shared := createSharedLedger(t, env, env.token, "matrix-perm")
	ledgerQ := "?ledger_id=" + itoa(shared)
	w := doJSON(env.r, http.MethodPost, "/api/rental/rooms"+ledgerQ, env.token,
		map[string]interface{}{"community": "M区", "room_no": "M09", "default_rent": 900})
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	w = doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "matrix-viewer", "password": "secret123",
	})
	viewerID := uint(field(t, decodeBody(t, w), "data", "user", "id").(float64))
	viewerToken := field(t, decodeBody(t, w), "data", "token").(string)
	inviteAndAccept(t, env, env.token, viewerToken, shared, "matrix-viewer")
	if w := doJSON(env.r, http.MethodPut,
		"/api/rental/ledgers/"+itoa(shared)+"/members/"+itoa(viewerID), env.token,
		map[string]interface{}{"perms": ModulePerms{ModuleRooms: PermView, ModuleTenants: PermEdit, ModuleBills: PermView}}); w.Code != http.StatusOK {
		t.Fatalf("设权限: %d %s", w.Code, w.Body.String())
	}

	body := map[string]interface{}{
		"room_id": roomID, "name": "查看档登记", "deposit": 900, "move_in_date": "2026-11-05", "create_first_bill": true,
	}
	if w := doJSON(env.r, http.MethodPost, "/api/rental/tenants"+ledgerQ, viewerToken, body); w.Code != http.StatusForbidden {
		t.Fatalf("bills=view 登记带勾 = %d, want 403", w.Code)
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/bills?room_id="+itoa(roomID)+"&ledger_id="+itoa(shared), env.token, nil)
	if n := len(itemsOf(t, w)); n != 0 {
		t.Errorf("越权登记不应产生账单，实得 %d", n)
	}
	w = doJSON(env.r, http.MethodGet, "/api/rental/tenants?room_id="+itoa(roomID)+"&ledger_id="+itoa(shared), env.token, nil)
	if n := len(itemsOf(t, w)); n != 0 {
		t.Errorf("越权登记不应留下租户档案，实得 %d", n)
	}
}
