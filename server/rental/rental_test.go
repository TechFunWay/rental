package rental

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/config"
	"smallgo/server/database"
	ginserver "smallgo/server/server"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// money 纯函数单测
// ---------------------------------------------------------------------------

func almostEqual(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

func TestRound2(t *testing.T) {
	cases := []struct{ in, want float64 }{
		{20.5 * 1.2, 24.6},
		{1.005, 1.0}, // 二进制浮点下 1.005 略小于 1.005 → 1.00
		{2.675, 2.68},
		{0.1 + 0.2, 0.3},
		{10, 10},
	}
	for _, c := range cases {
		if got := round2(c.in); !almostEqual(got, c.want) {
			t.Errorf("round2(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestUsageAndFee(t *testing.T) {
	if got := usage(100, 120.5); !almostEqual(got, 20.5) {
		t.Errorf("usage = %v, want 20.5", got)
	}
	// 本月读数小于上月（换表倒转）→ 用量按 0 计。
	if got := usage(120, 100); got != 0 {
		t.Errorf("usage reversed = %v, want 0", got)
	}
	if got := meterFee(100, 120.5, 1.2); !almostEqual(got, 24.6) {
		t.Errorf("meterFee = %v, want 24.6", got)
	}
	if got := meterFee(120, 100, 1.2); got != 0 {
		t.Errorf("meterFee reversed = %v, want 0", got)
	}
}

func TestBillStatusAndArrears(t *testing.T) {
	cases := []struct {
		total, paid float64
		wantStatus  string
		wantArrears float64
	}{
		{100, 100, "paid", 0},
		{100, 99.996, "paid", 0}, // 半分容差内视为缴清
		{100, 50, "partial", 50}, //
		{100, 0.01, "partial", 99.99},
		{100, 0, "unpaid", 100},
		{100, 120, "paid", 0}, // 超收不产生负欠缴
	}
	for _, c := range cases {
		if got := billStatus(c.total, c.paid); got != c.wantStatus {
			t.Errorf("billStatus(%v, %v) = %q, want %q", c.total, c.paid, got, c.wantStatus)
		}
		if got := arrears(c.total, c.paid); !almostEqual(got, c.wantArrears) {
			t.Errorf("arrears(%v, %v) = %v, want %v", c.total, c.paid, got, c.wantArrears)
		}
	}
}

func TestReceiptNo(t *testing.T) {
	got := receiptNo("2026-09", "101", 7)
	if got != "R-202609-101-0007" {
		t.Errorf("receiptNo = %q, want R-202609-101-0007", got)
	}
	// 房号中的特殊字符替换为 '-'。
	if got := receiptNo("2026-09", "A栋/302#", 42); got != "R-202609-A--302--0042" {
		t.Errorf("receiptNo special = %q", got)
	}
}

// ---------------------------------------------------------------------------
// httptest 全链路集成测试
// ---------------------------------------------------------------------------

type testEnv struct {
	t     *testing.T
	r     http.Handler
	db    *gorm.DB
	token string
}

func setupEnv(t *testing.T) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	db, err := database.InitDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	// Windows 下打开的 SQLite 文件会阻止 t.TempDir 清理，显式关闭。
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatalf("init configs: %v", err)
	}
	secret, err := sysconfig.GetConfig(db, "jwt_secret", 0)
	if err != nil || secret == "" {
		t.Fatalf("jwt secret: %v", err)
	}

	r := ginserver.NewRouter(config.Config{CORSOrigin: "*", RateLimit: 0}, db, secret)

	// 首个注册用户即管理员。
	w := doJSON(r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "landlord", "password": "secret123",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
	token := field(t, decodeBody(t, w), "data", "token").(string)

	return &testEnv{t: t, r: r, db: db, token: token}
}

func doJSON(r http.Handler, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	var buf io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode response %q: %v", w.Body.String(), err)
	}
	return m
}

func field(t *testing.T, m map[string]interface{}, path ...string) interface{} {
	t.Helper()
	var cur interface{} = m
	for _, p := range path {
		cm, ok := cur.(map[string]interface{})
		if !ok {
			t.Fatalf("field %v: not an object at %q", path, p)
		}
		cur = cm[p]
		if cur == nil {
			t.Fatalf("field %v: missing at %q", path, p)
		}
	}
	return cur
}

func (e *testEnv) do(method, path string, body interface{}) *httptest.ResponseRecorder {
	e.t.Helper()
	return doJSON(e.r, method, path, e.token, body)
}

func (e *testEnv) items(resp map[string]interface{}) []interface{} {
	e.t.Helper()
	return field(e.t, resp, "data", "items").([]interface{})
}

func num(v interface{}) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// seedRoom 创建房源并返回 ID。
func (e *testEnv) seedRoom(roomNo string, rent float64, initElec float64) uint {
	e.t.Helper()
	w := e.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": roomNo, "default_rent": rent, "initial_elec": initElec,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed room %s: %d %s", roomNo, w.Code, w.Body.String())
	}
	return uint(field(e.t, decodeBody(e.t, w), "data", "id").(float64))
}

// seedTenant 创建在租租户并返回 ID。
func (e *testEnv) seedTenant(roomID uint, name string) uint {
	e.t.Helper()
	w := e.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": name,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("seed tenant %s: %d %s", name, w.Code, w.Body.String())
	}
	return uint(field(e.t, decodeBody(e.t, w), "data", "id").(float64))
}

func TestRentalAuthRequired(t *testing.T) {
	env := setupEnv(t)
	w := doJSON(env.r, http.MethodGet, "/api/rental/rooms", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anon /rental/rooms = %d, want 401", w.Code)
	}
}

func TestRoomCRUD(t *testing.T) {
	env := setupEnv(t)

	// 创建 + 重复房号拒绝。
	id := env.seedRoom("101", 1500, 800)
	env.seedRoom("102", 1200, 0)
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{"room_no": "101"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate room_no = %d, want 400", w.Code)
	}

	// 列表与关键字搜索。
	w = env.do(http.MethodGet, "/api/rental/rooms?page=1&pageSize=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("room list: %d", w.Code)
	}
	if got := len(env.items(decodeBody(t, w))); got != 2 {
		t.Fatalf("room list size = %d, want 2", got)
	}
	w = env.do(http.MethodGet, "/api/rental/rooms?keyword=101", nil)
	if got := len(env.items(decodeBody(t, w))); got != 1 {
		t.Fatalf("room search size = %d, want 1", got)
	}

	// 更新。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/rooms/%d", id), map[string]interface{}{
		"room_no": "101", "default_rent": 1600, "initial_elec": 800,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("room update: %d %s", w.Code, w.Body.String())
	}

	// 删除（无账单）→ OK；再次删除 → 404。
	w = env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", id), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("room delete: %d", w.Code)
	}
	w = env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", id), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("room re-delete = %d, want 404", w.Code)
	}
}

func TestRoomUniqueByCommunity(t *testing.T) {
	env := setupEnv(t)
	// A 小区 101
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "101", "community": "阳光花园",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create A/101: %d %s", w.Code, w.Body.String())
	}
	// B 小区同房号 101 → 允许
	w = env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "101", "community": "翡翠湾",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create B/101: %d %s", w.Code, w.Body.String())
	}
	// 同小区同房号 → 拒绝
	w = env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "101", "community": "阳光花园",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate A/101 = %d, want 400", w.Code)
	}
	// 无小区 101 → 允许（与带小区的 101 不冲突）
	w = env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{"room_no": "101"})
	if w.Code != http.StatusOK {
		t.Fatalf("create no-community/101: %d %s", w.Code, w.Body.String())
	}
}

func TestTenantLifecycle(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("201", 1000, 0)
	tenantID := env.seedTenant(roomID, "王小明")

	// 房源列表应展示在租租户。
	w := env.do(http.MethodGet, "/api/rental/rooms", nil)
	room := env.items(decodeBody(t, w))[0].(map[string]interface{})
	tenants := room["current_tenants"].([]interface{})
	if len(tenants) != 1 || tenants[0].(map[string]interface{})["name"] != "王小明" {
		t.Fatalf("current_tenants = %v", tenants)
	}

	// 宿舍场景：同房第二名租户。
	env.seedTenant(roomID, "李小华")
	w = env.do(http.MethodGet, "/api/rental/rooms", nil)
	room = env.items(decodeBody(t, w))[0].(map[string]interface{})
	if got := len(room["current_tenants"].([]interface{})); got != 2 {
		t.Fatalf("dorm tenants = %d, want 2", got)
	}

	// 退租。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/tenants/%d/checkout", tenantID), map[string]interface{}{})
	if w.Code != http.StatusOK {
		t.Fatalf("checkout: %d", w.Code)
	}
	w = env.do(http.MethodGet, "/api/rental/tenants?active=0", nil)
	if got := len(env.items(decodeBody(t, w))); got != 1 {
		t.Fatalf("checked-out list = %d, want 1", got)
	}

	// 有在租租户的房源不能删除。
	w = env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete occupied room = %d, want 400", w.Code)
	}
}

func TestBillGenerateAndMeterCarryOver(t *testing.T) {
	env := setupEnv(t)
	r1 := env.seedRoom("101", 1500, 800) // 底数 800
	r2 := env.seedRoom("102", 1200, 300)
	env.seedRoom("103", 900, 0) // 无租户：不参与生成
	env.seedTenant(r1, "张三")
	env.seedTenant(r2, "李四")

	// 生成 9 月账单：2 间创建；无租户房间不在生成范围，也不计入 skipped。
	w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"})
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["created"].(float64) != 2 || data["skipped"].(float64) != 0 {
		t.Fatalf("generate result = %v, want created=2 skipped=0", data)
	}

	// 9 月账单：上月读数 = 底数；单价 = 全局默认（电 1.2）；unpaid；单据号已生成。
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09", nil)
	bills := env.items(decodeBody(t, w))
	if len(bills) != 2 {
		t.Fatalf("sept bills = %d, want 2", len(bills))
	}
	var bill101 map[string]interface{}
	for _, b := range bills {
		m := b.(map[string]interface{})
		if m["room_no"] == "101" {
			bill101 = m
		}
	}
	if bill101 == nil {
		t.Fatal("bill for 101 missing")
	}
	if num(bill101["elec_last"]) != 800 {
		t.Fatalf("elec_last = %v, want 800 (initial)", bill101["elec_last"])
	}
	if num(bill101["elec_price"]) != 1.2 {
		t.Fatalf("elec_price = %v, want 1.2 (global default)", bill101["elec_price"])
	}
	if bill101["status"] != "unpaid" {
		t.Fatalf("status = %v, want unpaid", bill101["status"])
	}
	if bill101["receipt_no"] == "" {
		t.Fatal("receipt_no empty")
	}
	if bill101["tenant_name"] != "张三" {
		t.Fatalf("tenant_name = %v, want 张三", bill101["tenant_name"])
	}
	bill101ID := uint(bill101["id"].(float64))

	// 编辑 9 月账单：录入本月电表 920 → 电费 (920-800)*1.2 = 144。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", bill101ID), map[string]interface{}{
		"room_id": r1, "period": "2026-09", "tenant_name": "张三",
		"rent": 1500, "elec_last": 800, "elec_now": 920, "elec_price": 1.2,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("bill update: %d %s", w.Code, w.Body.String())
	}
	updated := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(updated["elec_fee"]), 144) {
		t.Fatalf("elec_fee = %v, want 144", updated["elec_fee"])
	}
	if !almostEqual(num(updated["total_amount"]), 1644) {
		t.Fatalf("total = %v, want 1644", updated["total_amount"])
	}

	// 生成 10 月账单：上月读数应衔接 9 月的本月读数（920）。
	w = env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"})
	if w.Code != http.StatusOK {
		t.Fatalf("generate oct: %d", w.Code)
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-10", nil)
	for _, b := range env.items(decodeBody(t, w)) {
		m := b.(map[string]interface{})
		if m["room_no"] == "101" && num(m["elec_last"]) != 920 {
			t.Fatalf("oct elec_last = %v, want 920 (carry-over)", m["elec_last"])
		}
	}

	// 同月重复生成只跳过。
	w = env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"})
	data = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["created"].(float64) != 0 || data["skipped"].(float64) != 2 {
		t.Fatalf("re-generate = %v, want created=0 skipped=2", data)
	}
}

func TestPayFlow(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("301", 1000, 0)
	env.seedTenant(roomID, "赵六")
	billID := env.createBill("301", "2026-09")

	// 录入抄数：电 100→200，单价 1.2 → 电费 120，合计 1120。
	env.updateBill(billID, "赵六", 100, 200)

	// 部分收款 500 → partial，欠缴 620。
	w := env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": 500})
	if w.Code != http.StatusOK {
		t.Fatalf("pay: %d %s", w.Code, w.Body.String())
	}
	bill := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if bill["status"] != "partial" {
		t.Fatalf("status = %v, want partial", bill["status"])
	}
	if !almostEqual(num(bill["paid_amount"]), 500) {
		t.Fatalf("paid = %v, want 500", bill["paid_amount"])
	}

	// 再收 620 → paid。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": 620})
	bill = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if bill["status"] != "paid" {
		t.Fatalf("status = %v, want paid", bill["status"])
	}

	// arrears 筛选（是否欠缴）不应再包含该账单。
	w = env.do(http.MethodGet, "/api/rental/bills?status=arrears", nil)
	if got := len(env.items(decodeBody(t, w))); got != 0 {
		t.Fatalf("arrears list = %d, want 0", got)
	}

	// 编辑账单把已收改回 0 → 回到 unpaid。
	env.updateBillPaid(billID, 0)
	w = env.do(http.MethodGet, "/api/rental/bills?status=arrears", nil)
	if got := len(env.items(decodeBody(t, w))); got != 1 {
		t.Fatalf("arrears list after reset = %d, want 1", got)
	}

	// 非法收款金额。
	w = env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": -5})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("negative pay = %d, want 400", w.Code)
	}
}

func (e *testEnv) createBill(roomNo, period string) uint {
	e.t.Helper()
	var roomID uint
	w := e.do(http.MethodGet, "/api/rental/rooms?keyword="+roomNo, nil)
	room := e.items(decodeBody(e.t, w))[0].(map[string]interface{})
	roomID = uint(room["id"].(float64))

	w = e.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": period,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("create bill: %d %s", w.Code, w.Body.String())
	}
	return uint(field(e.t, decodeBody(e.t, w), "data", "id").(float64))
}

func (e *testEnv) updateBill(billID uint, tenantName string, elecLast, elecNow float64, period ...string) {
	e.t.Helper()
	p := "2026-09"
	if len(period) > 0 {
		p = period[0]
	}
	w := e.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": 1, "period": p, "rent": 1000, "tenant_name": tenantName,
		"elec_last": elecLast, "elec_now": elecNow, "elec_price": 1.2,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("update bill: %d %s", w.Code, w.Body.String())
	}
}

func (e *testEnv) updateBillPaid(billID uint, paid float64) {
	e.t.Helper()
	w := e.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": 1, "period": "2026-09", "rent": 1000,
		"elec_last": 100, "elec_now": 200, "elec_price": 1.2,
		"paid_amount": paid,
	})
	if w.Code != http.StatusOK {
		e.t.Fatalf("update bill paid: %d %s", w.Code, w.Body.String())
	}
}

func TestBillDuplicateRejected(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("401", 800, 0)
	env.seedTenant(roomID, "孙七")

	body := map[string]interface{}{"room_id": roomID, "period": "2026-09"}
	if w := env.do(http.MethodPost, "/api/rental/bills", body); w.Code != http.StatusOK {
		t.Fatalf("first bill: %d", w.Code)
	}
	w := env.do(http.MethodPost, "/api/rental/bills", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate bill = %d, want 400", w.Code)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("501", 2000, 100)
	env.seedTenant(roomID, "周八")
	env.seedRoom("502", 1800, 50) // 无租户：不生成账单，仅验证建房
	billID := env.createBill("501", "2026-09")
	env.updateBill(billID, "周八", 100, 250)

	// 导出。
	w := env.do(http.MethodGet, "/api/rental/export", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("export: %d", w.Code)
	}
	csvBody := w.Body.Bytes()
	if len(csvBody) < 3 || csvBody[0] != 0xEF || csvBody[1] != 0xBB || csvBody[2] != 0xBF {
		t.Fatal("export missing UTF-8 BOM")
	}
	if !strings.Contains(w.Body.String(), "501") || !strings.Contains(w.Body.String(), "周八") {
		t.Fatal("export missing room/tenant")
	}
	// 导出文件原样回导：同房同月 → 更新（updated=1）。
	payload := strings.TrimPrefix(w.Body.String(), "\ufeff")
	w = env.importCSVBody(payload)
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["updated"].(float64) != 1 {
		t.Fatalf("import updated = %v, want 1 (same period overwritten)", res["updated"])
	}
	if res["created"].(float64) != 0 {
		t.Fatalf("import created = %v, want 0", res["created"])
	}

	// 导入 10 月新账单。
	w = env.importCSVBody("房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注\n" +
		"501,周八,2026-10,2000,0,0,250,370,0,0,5,1.2,3.5,10,20,500,导入测试\n")
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 1 {
		t.Fatalf("import oct created = %v, want 1", res["created"])
	}

	// 导入后账单金额正确：电费 (370-250)*1.2=144，合计 2000+144+10+20=2174，欠缴 1674。
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-10", nil)
	bills := env.items(decodeBody(t, w))
	m := bills[0].(map[string]interface{})
	if !almostEqual(num(m["total_amount"]), 2174) {
		t.Fatalf("imported total = %v, want 2174", m["total_amount"])
	}
	if m["status"] != "partial" {
		t.Fatalf("imported status = %v, want partial", m["status"])
	}

	// 坏行不拖垮整体：房号缺失 → failed，其余成功。
	w = env.importCSVBody("房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注\n" +
		",无名,2026-11,1000,0,0,0,0,0,0,1,1,1,0,0,0,\n" +
		"502,新租客,2026-11,1500,0,0,0,0,0,0,1,1,1,0,0,0,\n")
	res = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["failed"].(float64) != 1 || res["created"].(float64) != 1 {
		t.Fatalf("mixed import = %v", res)
	}
	if len(res["errors"].([]interface{})) != 1 {
		t.Fatalf("errors detail = %v", res["errors"])
	}

	// 表头不符 → 400。
	w = env.importCSVBody("a,b\n1,2\n")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad header = %d, want 400", w.Code)
	}
}

func (e *testEnv) importCSVBody(content string) *httptest.ResponseRecorder {
	e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "import.csv")
	_, _ = part.Write([]byte(content))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/rental/import", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+e.token)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w
}

func TestRoomDeleteBlockedByBills(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("601", 999, 0)
	env.seedTenant(roomID, "钱九")
	env.createBill("601", "2026-09")

	w := env.do(http.MethodDelete, fmt.Sprintf("/api/rental/rooms/%d", roomID), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("delete room with bills = %d, want 400", w.Code)
	}
}

func TestStats(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("701", 1000, 0)
	env.seedTenant(roomID, "吴十")
	period := currentPeriodStr()
	billID := env.createBill("701", period)
	env.updateBill(billID, "吴十", 0, 100, period) // 电费 120，合计 1120
	env.do(http.MethodPost, fmt.Sprintf("/api/rental/bills/%d/pay", billID), map[string]interface{}{"amount": 1000})

	w := env.do(http.MethodGet, "/api/rental/stats", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("stats: %d", w.Code)
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if data["rooms_total"].(float64) != 1 || data["rooms_occupied"].(float64) != 1 {
		t.Fatalf("rooms stats = %v", data)
	}
	month := data["month"].(map[string]interface{})
	if !almostEqual(num(month["total"]), 1120) || !almostEqual(num(month["paid"]), 1000) {
		t.Fatalf("month stats = %v", month)
	}
	if !almostEqual(num(month["outstanding"]), 120) {
		t.Fatalf("outstanding = %v, want 120", month["outstanding"])
	}
	overall := data["overall"].(map[string]interface{})
	if !almostEqual(num(overall["outstanding"]), 120) {
		t.Fatalf("overall outstanding = %v, want 120", overall["outstanding"])
	}
}

func TestDataIsolationBetweenUsers(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("801", 1000, 0)
	env.seedTenant(roomID, "隔离甲")
	env.createBill("801", "2026-09")

	// 第二个用户（普通用户）。
	w := doJSON(env.r, http.MethodPost, "/api/auth/register", "", map[string]interface{}{
		"username": "other", "password": "secret123",
	})
	otherToken := field(t, decodeBody(t, w), "data", "token").(string)

	w = doJSON(env.r, http.MethodGet, "/api/rental/rooms", otherToken, nil)
	if got := len(itemsOf(t, w)); got != 0 {
		t.Fatalf("other user rooms = %d, want 0", got)
	}
	// 直接访问他人账单 → 404。
	w = doJSON(env.r, http.MethodGet, "/api/rental/bills/1", otherToken, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("other user bill = %d, want 404", w.Code)
	}
}

func itemsOf(t *testing.T, w *httptest.ResponseRecorder) []interface{} {
	t.Helper()
	m := decodeBody(t, w)
	items, ok := m["data"].(map[string]interface{})["items"].([]interface{})
	if !ok {
		t.Fatalf("items missing: %s", w.Body.String())
	}
	return items
}

func TestPeriodValidation(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("901", 100, 0)
	w := env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
		"room_id": roomID, "period": "2026/09",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad period = %d, want 400", w.Code)
	}
}

// ---------------------------------------------------------------------------
// 水费包月模式
// ---------------------------------------------------------------------------

func TestWaterFeeByMode(t *testing.T) {
	// 包月：水费 = 每月固定金额，与抄表读数无关。
	monthly := Bill{WaterMode: waterModeMonthly, WaterPrice: 40, WaterLast: 100, WaterNow: 130}
	if got := monthly.waterFee(); !almostEqual(got, 40) {
		t.Errorf("monthly waterFee = %v, want 40", got)
	}
	// 按吨：水费 = 用量 × 单价。
	meter := Bill{WaterMode: waterModeMeter, WaterPrice: 5, WaterLast: 100, WaterNow: 130}
	if got := meter.waterFee(); !almostEqual(got, 150) {
		t.Errorf("meter waterFee = %v, want 150", got)
	}
	// 历史账单 water_mode 为空 → 兼容按"按吨"口径。
	legacy := Bill{WaterMode: "", WaterPrice: 5, WaterLast: 10, WaterNow: 20}
	if got := legacy.waterFee(); !almostEqual(got, 50) {
		t.Errorf("legacy waterFee = %v, want 50", got)
	}
}

func TestTenantWaterMonthlyBilling(t *testing.T) {
	env := setupEnv(t)

	// 建一间房，租户设为包月：水费 40 元/月。
	w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
		"room_no": "701", "default_rent": 1000,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create room: %d %s", w.Code, w.Body.String())
	}
	roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "包租婆", "water_mode": "monthly", "water_monthly_fee": 40,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create monthly tenant: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(field(t, decodeBody(t, w), "data", "id").(float64))

	// 非法计费方式 → 400。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": roomID, "name": "包租婆", "water_mode": "weekly",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid water mode = %d, want 400", w.Code)
	}

	// 生成账单：水费按包月金额计，水费单价字段存的也是包月金额。
	w = env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"})
	if w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09", nil)
	bills := env.items(decodeBody(t, w))
	if len(bills) != 1 {
		t.Fatalf("sept bills = %d, want 1", len(bills))
	}
	b := bills[0].(map[string]interface{})
	if b["water_mode"] != waterModeMonthly {
		t.Fatalf("water_mode = %v, want monthly", b["water_mode"])
	}
	if !almostEqual(num(b["water_price"]), 40) {
		t.Fatalf("water_price = %v, want 40 (包月金额)", b["water_price"])
	}
	if !almostEqual(num(b["water_fee"]), 40) {
		t.Fatalf("water_fee = %v, want 40", b["water_fee"])
	}
	if !almostEqual(num(b["total_amount"]), 1040) {
		t.Fatalf("total = %v, want 1040", b["total_amount"])
	}
	billID := uint(b["id"].(float64))

	// 编辑时即便带了抄表读数，包月水费仍不按用量算。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "tenant_name": "包租婆", "rent": 1000,
		"water_last": 100, "water_now": 150, "water_price": 40, "water_mode": waterModeMonthly,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("update monthly bill: %d %s", w.Code, w.Body.String())
	}
	upd := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 40) {
		t.Fatalf("monthly water_fee with readings = %v, want 40", upd["water_fee"])
	}
	if !almostEqual(num(upd["total_amount"]), 1040) {
		t.Fatalf("monthly total = %v, want 1040", upd["total_amount"])
	}

	// 同一张账单切回按吨：水费变回用量 × 单价（50 × 5 = 250）。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/bills/%d", billID), map[string]interface{}{
		"room_id": roomID, "period": "2026-09", "tenant_name": "包租婆", "rent": 1000,
		"water_last": 100, "water_now": 150, "water_price": 5, "water_mode": waterModeMeter,
	})
	upd = field(t, decodeBody(t, w), "data").(map[string]interface{})
	if !almostEqual(num(upd["water_fee"]), 250) {
		t.Fatalf("meter water_fee = %v, want 250", upd["water_fee"])
	}
}

func TestWaterModeFollowGlobalDefault(t *testing.T) {
	env := setupEnv(t)

	// 偏好设置把全局水费改成包月 30 元/月。
	for k, v := range map[string]string{
		"rental_water_mode":        waterModeMonthly,
		"rental_water_monthly_fee": "30",
	} {
		if w := env.do(http.MethodPut, "/api/configs", map[string]interface{}{"key": k, "value": v}); w.Code != http.StatusOK {
			t.Fatalf("set config %s: %d %s", k, w.Code, w.Body.String())
		}
	}

	// 租户没设置水费 → 跟随全局默认 → 包月 30。
	roomID := env.seedRoom("711", 900, 0)
	env.seedTenant(roomID, "跟班")
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodGet, "/api/rental/bills?period=2026-09", nil)
	b := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b["water_mode"] != waterModeMonthly || !almostEqual(num(b["water_fee"]), 30) {
		t.Fatalf("global-default bill = mode %v fee %v, want monthly 30", b["water_mode"], b["water_fee"])
	}

	// 租户显式指定按吨 → 覆盖全局包月，按用量计费（底数 0，用量 0 → 水费 0）。
	room2 := env.seedRoom("712", 800, 0)
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": room2, "name": "另类", "water_mode": waterModeMeter, "water_price": 5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create meter tenant: %d %s", w.Code, w.Body.String())
	}
	tenantID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate 2: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=712", nil)
	b2 := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b2["water_mode"] != waterModeMeter || !almostEqual(num(b2["water_fee"]), 0) {
		t.Fatalf("override bill = mode %v fee %v, want meter 0", b2["water_mode"], b2["water_fee"])
	}

	// 租户改回"跟随全局"（water_mode 空串）→ 新账单重新按全局包月 30。
	w = env.do(http.MethodPut, fmt.Sprintf("/api/rental/tenants/%d", tenantID), map[string]interface{}{
		"room_id": room2, "name": "另类", "water_mode": "",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("reset tenant mode: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=另类", nil)
	got := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if got["water_mode"] != "" {
		t.Fatalf("tenant water_mode = %v, want 空（跟随全局）", got["water_mode"])
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-10"}); w.Code != http.StatusOK {
		t.Fatalf("generate 3: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-10&keyword=712", nil)
	b3 := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b3["water_mode"] != waterModeMonthly || !almostEqual(num(b3["water_fee"]), 30) {
		t.Fatalf("follow-global bill = mode %v fee %v, want monthly 30", b3["water_mode"], b3["water_fee"])
	}
}

// TestPayCycleFollowGlobalDefault 缴费周期同理：租户没设置时用全局默认，
// 租户显式设置覆盖全局。
func TestPayCycleFollowGlobalDefault(t *testing.T) {
	env := setupEnv(t)

	// 全局默认改成季付。
	if w := env.do(http.MethodPut, "/api/configs", map[string]interface{}{"key": "rental_pay_cycle", "value": payCycleQuarterly}); w.Code != http.StatusOK {
		t.Fatalf("set pay cycle: %d %s", w.Code, w.Body.String())
	}

	// 租户没设置周期 → 跟随全局季付：租金按 3 个月计。
	roomID := env.seedRoom("721", 1000, 0)
	env.seedTenant(roomID, "跟全局")
	room := Room{UserID: 1, ID: roomID}
	if cfg := effectiveRoomBilling(env.db, &room); cfg.PayCycle != payCycleQuarterly {
		t.Fatalf("effective pay cycle = %v, want quarterly", cfg.PayCycle)
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=721", nil)
	b := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b["pay_cycle"] != payCycleQuarterly || !almostEqual(num(b["rent"]), 3000) {
		t.Fatalf("global cycle bill = %v/%v, want quarterly/3000", b["pay_cycle"], b["rent"])
	}

	// 租户显式月付 → 覆盖全局季付。
	room2 := env.seedRoom("722", 1000, 0)
	w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": room2, "name": "月付人", "pay_cycle": "monthly",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create monthly tenant: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate 2: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=722", nil)
	b2 := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b2["pay_cycle"] != payCycleMonthly || !almostEqual(num(b2["rent"]), 1000) {
		t.Fatalf("tenant cycle bill = %v/%v, want monthly/1000", b2["pay_cycle"], b2["rent"])
	}
}

// TestMultiTenantBillingPrecedence 同房多名在租租户时，逐项取"第一个显式
// 设置"的租户（按登记先后），都没设置才回退全局。
func TestMultiTenantBillingPrecedence(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("741", 1000, 0)

	// 第一位租户未设置，第二位租户设置包月 45 与季付 → 生效的是第二位的设置。
	env.seedTenant(roomID, "甲")
	env.seedBillingTenant(roomID, "乙", payCycleQuarterly, 5, -1)
	env.db.Model(&Tenant{}).Where("room_id = ? AND name = ?", roomID, "乙").
		Updates(map[string]interface{}{"water_mode": waterModeMonthly, "water_monthly_fee": 45})

	room := Room{UserID: 1, ID: roomID}
	cfg := effectiveRoomBilling(env.db, &room)
	if cfg.WaterMode != waterModeMonthly || !almostEqual(cfg.WaterAmount, 45) {
		t.Fatalf("multi-tenant water = %v/%v, want monthly/45", cfg.WaterMode, cfg.WaterAmount)
	}
	if cfg.PayCycle != payCycleQuarterly {
		t.Fatalf("multi-tenant cycle = %v, want quarterly", cfg.PayCycle)
	}

	// 第一位租户显式设置按吨 6 元 → 优先于第二位。
	env.db.Model(&Tenant{}).Where("room_id = ? AND name = ?", roomID, "甲").
		Updates(map[string]interface{}{"water_mode": waterModeMeter, "water_price": 6})
	cfg = effectiveRoomBilling(env.db, &room)
	if cfg.WaterMode != waterModeMeter || !almostEqual(cfg.WaterAmount, 6) {
		t.Fatalf("first-tenant water = %v/%v, want meter/6", cfg.WaterMode, cfg.WaterAmount)
	}

	// 季付账单按生效设置预填：包月水费也按月数放大（此处按吨，水量 0 → 水费 0）。
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}
	w := env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=741", nil)
	b := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b["pay_cycle"] != payCycleQuarterly || !almostEqual(num(b["rent"]), 3000) {
		t.Fatalf("multi-tenant bill = %v/%v, want quarterly/3000", b["pay_cycle"], b["rent"])
	}
}

func TestWaterMonthlyCSVImportExport(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("731", 1200, 0)
	// 租户改成包月 35。
	w := env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
		"room_id": roomID, "name": "月付哥", "water_mode": waterModeMonthly, "water_monthly_fee": 35,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("set monthly: %d %s", w.Code, w.Body.String())
	}
	if w := env.do(http.MethodPost, "/api/rental/bills/generate", map[string]interface{}{"period": "2026-09"}); w.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", w.Code, w.Body.String())
	}

	// 导出带"水费模式"列与包月金额。
	w = env.do(http.MethodGet, "/api/rental/export", nil)
	csvText := strings.TrimPrefix(w.Body.String(), "\ufeff")
	if !strings.Contains(csvText, "水费模式") || !strings.Contains(csvText, "包月") {
		t.Fatalf("export missing 水费模式 column:\n%s", csvText)
	}
	// 房源导出的是生效设置（租户包月 35）。
	w = env.do(http.MethodGet, "/api/rental/rooms/export", nil)
	roomsCSV := strings.TrimPrefix(w.Body.String(), "\ufeff")
	if !strings.Contains(roomsCSV, "包月") || !strings.Contains(roomsCSV, "35") {
		t.Fatalf("rooms export missing effective monthly fee:\n%s", roomsCSV)
	}

	// 新模板（18 列，含水费模式）导入：按包月 30 计费，顺带自动建房与租户。
	header := "房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注,水费模式\n"
	row := "732,新月租,2026-09,1500,0,0,0,0,0,0,30,1.2,3.5,0,0,0,,包月\n"
	w = env.importCSVBody(header + row)
	if w.Code != http.StatusOK {
		t.Fatalf("import: %d %s", w.Code, w.Body.String())
	}
	res := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if res["created"].(float64) != 1 {
		t.Fatalf("import created = %v, want 1", res)
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=732", nil)
	b := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b["water_mode"] != waterModeMonthly || !almostEqual(num(b["water_fee"]), 30) {
		t.Fatalf("imported bill = mode %v fee %v, want monthly 30", b["water_mode"], b["water_fee"])
	}
	if !almostEqual(num(b["total_amount"]), 1530) {
		t.Fatalf("imported total = %v, want 1530", b["total_amount"])
	}

	// CSV 里显式的水费模式落到自动建的租户上（包月金额存进 water_monthly_fee）。
	w = env.do(http.MethodGet, "/api/rental/tenants?keyword=新月租", nil)
	tenant := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if tenant["water_mode"] != waterModeMonthly || !almostEqual(num(tenant["water_monthly_fee"]), 30) {
		t.Fatalf("auto tenant = mode %v fee %v, want monthly 30", tenant["water_mode"], tenant["water_monthly_fee"])
	}

	// 旧 17 列模板（没有水费模式列）仍可导入，按吨计费。
	legacyHeader := "房号,租户名,月份,租金,上月水表,本月水表,上月电表,本月电表,上月燃气表,本月燃气表,水费单价,电费单价,燃气单价,卫生费,管理费,已收金额,备注\n"
	legacyRow := "733,老模板,2026-09,1000,100,120,0,0,0,0,5,1.2,3.5,0,0,0,\n"
	w = env.importCSVBody(legacyHeader + legacyRow)
	if w.Code != http.StatusOK {
		t.Fatalf("legacy import: %d %s", w.Code, w.Body.String())
	}
	w = env.do(http.MethodGet, "/api/rental/bills?period=2026-09&keyword=733", nil)
	b3 := env.items(decodeBody(t, w))[0].(map[string]interface{})
	if b3["water_mode"] != waterModeMeter || !almostEqual(num(b3["water_fee"]), 100) {
		t.Fatalf("legacy bill = mode %v fee %v, want meter 100", b3["water_mode"], b3["water_fee"])
	}
}

// TestTenantBillingBackfillUpgrade 0.2.3 迁移：老库房源上的计费与缴费设置
// 搬到在租租户上，房源级字段清零（新口径下不再参与计费）。
func TestTenantBillingBackfillUpgrade(t *testing.T) {
	dir := t.TempDir()
	db, err := database.InitDB(filepath.Join(dir, "upgrade.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 老数据：季付房（缴费日 5、提醒 3、水包月 40）上有一名在租租户与一名已退租租户。
	room := Room{UserID: 1, RoomNo: "901", DefaultRent: 1000, WaterMode: waterModeMonthly,
		WaterMonthlyFee: 40, PayCycle: payCycleQuarterly, PayDay: 5, RemindDays: 3}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	active := Tenant{UserID: 1, RoomID: room.ID, Name: "在租", Active: true}
	left := Tenant{UserID: 1, RoomID: room.ID, Name: "已退", Active: false}
	db.Create(&active)
	db.Create(&left)

	if err := database.RunUpgrades(db, "0.2.3", database.Upgrades); err != nil {
		t.Fatalf("run upgrades: %v", err)
	}

	var got Tenant
	if err := db.First(&got, active.ID).Error; err != nil {
		t.Fatalf("reload tenant: %v", err)
	}
	if got.WaterMode != waterModeMonthly || !almostEqual(got.WaterMonthlyFee, 40) {
		t.Errorf("tenant water = %v/%v, want monthly/40", got.WaterMode, got.WaterMonthlyFee)
	}
	if got.PayCycle != payCycleQuarterly || got.PayDay != 5 || got.RemindDays != 3 {
		t.Errorf("tenant pay = %v/%v/%v, want quarterly/5/3", got.PayCycle, got.PayDay, got.RemindDays)
	}

	var cleared Room
	if err := db.First(&cleared, room.ID).Error; err != nil {
		t.Fatalf("reload room: %v", err)
	}
	if cleared.WaterMode != "" || cleared.WaterMonthlyFee != 0 || cleared.PayCycle != "" ||
		cleared.PayDay != 0 || cleared.RemindDays != -1 {
		t.Errorf("room legacy billing not cleared: %+v", cleared)
	}

	// 迁移可重复执行（upgrade_records 去重），且幂等。
	if err := database.RunUpgrades(db, "0.2.3", database.Upgrades); err != nil {
		t.Fatalf("run upgrades twice: %v", err)
	}
}
