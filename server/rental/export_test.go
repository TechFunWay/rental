// export_test.go — 定时导出存档的用例。
//
// 覆盖任务要求的五组：快照合法可用、列表/下载/删除、保留数清理、路径穿越被拒、
// 非法配置被夹到合法范围。
//
// 用例全部走真实的 Gin 路由 + 真实 SQLite（httptest 只替换网络层），
// 不 mock 任何一层：导出存档的价值全在「产出的文件能不能被别人打开」，
// mock 掉 VACUUM INTO 等于没测。
package rental

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/gin-gonic/gin"

	"smallgo/server/database"
	"smallgo/server/sysconfig"
)

// exportTestEnv 是导出存档用例的运行环境：一份真实数据库 + 一个临时数据目录。
type exportTestEnv struct {
	r   http.Handler
	db  *gorm.DB
	dir string
}

// setupExportEnv 建临时数据目录、真实数据库，并把导出目录指到临时目录
// （exportDirOverride），保证用例不碰开发机上真实的 data/exports。
func setupExportEnv(t *testing.T) *exportTestEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := t.TempDir()
	db, err := database.InitDB(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatalf("init configs: %v", err)
	}
	t.Cleanup(func() { database.CloseDB(db) })

	prevDir := exportDirOverride
	exportDirOverride = filepath.Join(dir, exportDirName)
	t.Cleanup(func() { exportDirOverride = prevDir })

	prevDB := exportDB
	t.Cleanup(func() { exportDB = prevDB })

	// 与生产同一条装配路径：SetupAdmin → setupExportRoutes。
	engine := gin.New()
	setupExportRoutes(engine.Group("/api"), db)

	return &exportTestEnv{r: engine, db: db, dir: dir}
}

func (e *exportTestEnv) do(t *testing.T, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

// seedArchive 直接落一个指定时间戳的存档文件，用于构造保留数清理的场景
// （走真实导出接口没法控制时间戳）。
func seedArchive(t *testing.T, e *exportTestEnv, ts string) string {
	t.Helper()
	dir := exportsDir()
	name := "export_" + ts + ".db"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("snapshot"), 0o644); err != nil {
		t.Fatalf("seed archive %s: %v", name, err)
	}
	return name
}

// ---------------------------------------------------------------------------
// 一、快照本身合法、可被外部 SQLite 打开
// ---------------------------------------------------------------------------

// TestCreateExportProducesUsableDatabase 导出的文件必须是一份**独立可用**的
// 数据库：用一条全新的连接（不是应用的 GORM 句柄）打开它，确认含 users 表，
// 且能查到应用自己的业务表。
//
// 这一条是整个功能的地基：存档的唯一价值就是「下载到本机，以后还能还原」，
// 打不开的 .db 只是个占位文件。
func TestCreateExportProducesUsableDatabase(t *testing.T) {
	e := setupExportEnv(t)

	name, err := CreateExport(e.db)
	if err != nil {
		t.Fatalf("CreateExport: %v", err)
	}
	path := filepath.Join(exportsDir(), name)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("存档文件不存在: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("存档文件是空的")
	}

	// 用独立的 GORM 连接打开（模拟用户下载后用任意 SQLite 工具打开）。
	probe, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("导出的文件打不开: %v", err)
	}
	defer func() {
		if sqlDB, e := probe.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()

	for _, table := range []string{"users", "system_configs"} {
		var n int
		if err := probe.Raw(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&n).Error; err != nil {
			t.Fatalf("查询表 %s 失败: %v", table, err)
		}
		if n != 1 {
			t.Fatalf("导出存档缺少 %s 表", table)
		}
	}

	// 完整性检查：VACUUM INTO 的产物应当是完整库。
	var check string
	if err := probe.Raw("PRAGMA integrity_check").Scan(&check).Error; err != nil {
		t.Fatalf("integrity_check 失败: %v", err)
	}
	if check != "ok" {
		t.Fatalf("integrity_check = %q, want ok", check)
	}
}

// TestCreateExportNeverOverwrites 同秒内连续导出不得互相覆盖——用户辛苦导的
// 存档被下一次导出盖掉，等同于数据丢失。
func TestCreateExportNeverOverwrites(t *testing.T) {
	e := setupExportEnv(t)

	first, err := CreateExport(e.db)
	if err != nil {
		t.Fatalf("第一次导出: %v", err)
	}
	second, err := CreateExport(e.db)
	if err != nil {
		t.Fatalf("第二次导出: %v", err)
	}
	if first == second {
		t.Fatalf("同秒两次导出得到同一个文件名 %q，既有存档会被覆盖", first)
	}
	items, err := ListExports()
	if err != nil {
		t.Fatalf("ListExports: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("存档数 = %d, want 2（两次导出都应保留）", len(items))
	}
}

// TestExportsDirIsolatedFromFrameworkBackups 导出存档目录必须与框架备份目录
// 分开：本功能的全部意义就是存档不会被框架的保留数清理连带删掉。
func TestExportsDirIsolatedFromFrameworkBackups(t *testing.T) {
	e := setupExportEnv(t)
	if _, err := CreateExport(e.db); err != nil {
		t.Fatalf("CreateExport: %v", err)
	}
	if got := filepath.Base(exportsDir()); got != exportDirName {
		t.Fatalf("导出目录名 = %q, want %q（不得与框架 backups/ 同目录）", got, exportDirName)
	}
	if got := filepath.Base(exportsDir()); got == "backups" {
		t.Fatal("导出存档目录不能是框架的 backups/")
	}
}

// ---------------------------------------------------------------------------
// 二、列表 / 下载 / 删除
// ---------------------------------------------------------------------------

func TestExportListCreateDownloadDelete(t *testing.T) {
	e := setupExportEnv(t)

	// 列表（空）
	w := e.do(t, http.MethodGet, "/api/rental/exports")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
	}
	empty := decodeExportData(t, w)
	if len(empty.Items) != 0 {
		t.Fatalf("初始列表应为空, got %+v", empty.Items)
	}
	if !empty.Enabled || empty.IntervalHours != exportIntervalDefault || empty.KeepCount != exportKeepDefault {
		t.Fatalf("列表应回带默认配置, got %+v", empty)
	}

	// 立即导出
	w = e.do(t, http.MethodPost, "/api/rental/exports")
	if w.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}
	created := decodeExportCreate(t, w)
	if created.Name == "" {
		t.Fatal("导出接口未返回存档名")
	}

	// 列表（非空，且是刚导出的那份）
	w = e.do(t, http.MethodGet, "/api/rental/exports")
	list := decodeExportData(t, w)
	if len(list.Items) != 1 || list.Items[0].Name != created.Name {
		t.Fatalf("列表 = %+v, want 1 份且名为 %s", list.Items, created.Name)
	}
	if list.Items[0].Size <= 0 {
		t.Fatalf("列表里的存档大小应为正数, got %d", list.Items[0].Size)
	}

	// 下载
	w = e.do(t, http.MethodGet, "/api/rental/exports/"+created.Name+"/download")
	if w.Code != http.StatusOK {
		t.Fatalf("download status = %d, body = %s", w.Code, w.Body.String())
	}
	if w.Body.Len() == 0 {
		t.Fatal("下载内容为空")
	}
	// 下载文件名由服务端生成（加 rental_ 前缀），不含客户端可控内容。
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "rental_"+created.Name) {
		t.Fatalf("Content-Disposition = %q, want 含 rental_%s", cd, created.Name)
	}

	// 删除
	w = e.do(t, http.MethodDelete, "/api/rental/exports/"+created.Name)
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(exportsDir(), created.Name)); !os.IsNotExist(err) {
		t.Fatal("删除后文件仍在")
	}
	w = e.do(t, http.MethodGet, "/api/rental/exports")
	if len(decodeExportData(t, w).Items) != 0 {
		t.Fatal("删除后列表应为空")
	}
}

// TestDownloadMissingArchive 下载不存在的存档返回 404（而不是 500 或空文件）。
func TestDownloadMissingArchive(t *testing.T) {
	e := setupExportEnv(t)
	w := e.do(t, http.MethodGet, "/api/rental/exports/export_20260101_000000.db/download")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 三、保留数清理
// ---------------------------------------------------------------------------

// TestPruneExportsKeepsNewest 造超过 N 份后只留最新的 N 份。
func TestPruneExportsKeepsNewest(t *testing.T) {
	e := setupExportEnv(t)
	for _, ts := range []string{
		"20260101_000000", "20260102_000000", "20260103_000000",
		"20260104_000000", "20260105_000000",
	} {
		seedArchive(t, e, ts)
	}

	removed, err := PruneExports(3)
	if err != nil {
		t.Fatalf("PruneExports: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	items, err := ListExports()
	if err != nil {
		t.Fatalf("ListExports: %v", err)
	}
	want := []string{
		"export_20260105_000000.db",
		"export_20260104_000000.db",
		"export_20260103_000000.db",
	}
	if len(items) != len(want) {
		t.Fatalf("保留数 = %d, want %d: %+v", len(items), len(want), items)
	}
	for i, name := range want {
		if items[i].Name != name {
			t.Fatalf("第 %d 份 = %q, want %q（留的必须是最新的）", i, items[i].Name, name)
		}
	}
}

// TestPruneExportsKeepsAtLeastOne 保留数配错成 0 时仍至少留一份——存档全被清光
// 就失去了留存的意义。
func TestPruneExportsKeepsAtLeastOne(t *testing.T) {
	e := setupExportEnv(t)
	for _, ts := range []string{"20260101_000000", "20260102_000000", "20260103_000000"} {
		seedArchive(t, e, ts)
	}
	if _, err := PruneExports(0); err != nil {
		t.Fatalf("PruneExports(0): %v", err)
	}
	items, _ := ListExports()
	if len(items) != 1 || items[0].Name != "export_20260103_000000.db" {
		t.Fatalf("keep=0 时应留最新一份, got %+v", items)
	}
}

// TestExportCreatePrunesByKeepCount 立即导出后应按保留数清理，且**只清导出
// 存档**——框架备份目录里的文件一个都不能少。
func TestExportCreatePrunesByKeepCount(t *testing.T) {
	e := setupExportEnv(t)
	if err := sysconfig.UpdateConfig(e.db, "rental_export_keep_count", "2", 0); err != nil {
		t.Fatalf("设置保留数: %v", err)
	}

	// 在框架备份目录里放一份「不该被动到」的备份。
	backupDir := filepath.Join(e.dir, "backups")
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		t.Fatal(err)
	}
	untouched := filepath.Join(backupDir, "backup_20260101_000000.db")
	if err := os.WriteFile(untouched, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, ts := range []string{"20260101_000000", "20260102_000000"} {
		seedArchive(t, e, ts)
	}
	for i := 0; i < 2; i++ {
		if w := e.do(t, http.MethodPost, "/api/rental/exports"); w.Code != http.StatusOK {
			t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
		}
	}

	items, _ := ListExports()
	if len(items) != 2 {
		t.Fatalf("保留数=2 时应留 2 份, got %d: %+v", len(items), items)
	}
	if _, err := os.Stat(untouched); err != nil {
		t.Fatalf("框架备份目录里的文件被导出存档的清理误删了: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 四、路径穿越与非法文件名
// ---------------------------------------------------------------------------

// TestExportRejectsTraversalAndBadNames 非法文件名一律 400。
//
// 说明两点：
//   - gin 路由会把 %2F 解码成路径分隔符再做匹配，`../../etc/passwd` 这类请求
//     根本匹配不到 `:name`（返回 404，攻击在路由层就被挡掉），所以这里用
//     **单段**非法名验证业务层的正则防线；路径穿越形态由
//     TestExportNamePatternRejectsTraversal 与
//     TestTraversalRequestNeverTouchesOutsideFile 单独覆盖，两层都要过。
//   - 含 NUL 等控制字符的名字拼不进合法 HTTP 请求行（httptest 直接 panic），
//     同样由上面的正则单测覆盖。
func TestExportRejectsTraversalAndBadNames(t *testing.T) {
	e := setupExportEnv(t)
	longName := "export_" + strings.Repeat("9", 300) + ".db"
	for _, bad := range []string{
		"evil.db",                          // 完全不匹配
		"export_xxx.db",                    // 时间戳位不是数字
		"export_20261012_030000.db.bak",    // 多一层后缀
		"backup_20261012_030000.db",        // 备份名混入
		"export_2026101_030000.db",         // 日期少一位
		"export_20261012_03000.db",         // 时分秒少一位
		"export_20261012_030000.db%20",     // 尾部空格（编码形态）
		"%20export_20261012_030000.db",     // 头部空格（编码形态）
		"export_20261012_030000_a.db",      // 序号位不是数字
		"export_20261012_030000.db.sqlite", // 改了后缀
		longName,                           // 超长名
	} {
		w := e.do(t, http.MethodGet, "/api/rental/exports/"+bad+"/download")
		if w.Code != http.StatusBadRequest {
			t.Fatalf("下载非法名 %q status = %d, want 400", bad, w.Code)
		}
		w = e.do(t, http.MethodDelete, "/api/rental/exports/"+bad)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("删除非法名 %q status = %d, want 400", bad, w.Code)
		}
	}
}

// TestExportNamePatternRejectsTraversal 直接对白名单正则断言，含各种路径
// 穿越形态——这是拼路径前的最后一道闸门，必须独立于路由层被验证。
func TestExportNamePatternRejectsTraversal(t *testing.T) {
	for _, bad := range []string{
		"../../etc/passwd",
		"../export_20260101_000000.db",
		"..%2f..%2fetc%2fpasswd",
		"/etc/passwd",
		`..\..\windows\system32`,
		"export_20260101_000000.db/",
		"export_20260101_000000.db\x00.txt",
		"prefix_export_20260101_000000.db",
		"export_20260101_000000Xdb",
		"sub/export_20260101_000000.db",
		"export_20260101_000000.db.bak",
	} {
		if exportNamePattern.MatchString(bad) {
			t.Fatalf("正则不该接受 %q", bad)
		}
	}
	for _, good := range []string{
		"export_20260101_000000.db",
		"export_20261231_235959.db",
		"export_20260101_000000-1.db",
		"export_20260101_000000-99.db",
		// 序号位数不设上限：与框架 backup 的名正则同口径，且序号只可能由
		// 服务端 CreateExport 生成（上限 99），外部无法借此穿越路径。
		"export_20260101_000000-99999999999.db",
	} {
		if !exportNamePattern.MatchString(good) {
			t.Fatalf("正则不该拒绝合法存档名 %q", good)
		}
	}
}

// TestTraversalRequestNeverTouchesOutsideFile 造一个目录外的哨兵文件，用
// `..` 形态的下载/删除请求去够它，确认它始终纹丝不动。
func TestTraversalRequestNeverTouchesOutsideFile(t *testing.T) {
	e := setupExportEnv(t)

	// 存档目录的同级放一个「外部文件」，用 ../exports_sibling.db 去够它。
	sibling := filepath.Join(e.dir, "exports_sibling.db")
	if err := os.WriteFile(sibling, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, raw := range []string{
		"..%2Fexports_sibling.db",
		"..%2f..%2fetc%2fpasswd",
		"%2e%2e%2fexports_sibling.db",
	} {
		w := e.do(t, http.MethodDelete, "/api/rental/exports/"+raw)
		// 匹配不到路由（404）或被正则拒绝（400）都算守住，绝不能是 200。
		if w.Code == http.StatusOK {
			t.Fatalf("路径穿越请求 %q 竟然返回 200", raw)
		}
		w = e.do(t, http.MethodGet, "/api/rental/exports/"+raw+"/download")
		if w.Code == http.StatusOK {
			t.Fatalf("路径穿越下载 %q 竟然返回 200", raw)
		}
	}

	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("目录外的文件被删了: %v", err)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "secret" {
		t.Fatalf("目录外的文件内容被改写: %v %q", err, data)
	}
}

// ---------------------------------------------------------------------------
// 五、配置夹取与定时任务
// ---------------------------------------------------------------------------

// TestClampExportConfig 非法配置值夹到合法范围。
func TestClampExportConfig(t *testing.T) {
	intervalCases := map[int]int{
		-100: exportIntervalMin, 0: exportIntervalMin, 1: 1, 24: 24,
		720: 720, 721: exportIntervalMax, 100000: exportIntervalMax,
	}
	for in, want := range intervalCases {
		if got := clampExportInterval(in); got != want {
			t.Errorf("clampExportInterval(%d) = %d, want %d", in, got, want)
		}
	}
	keepCases := map[int]int{
		-5: exportKeepMin, 0: exportKeepMin, 1: 1, 30: 30,
		500: 500, 501: exportKeepMax, 99999: exportKeepMax,
	}
	for in, want := range keepCases {
		if got := clampExportKeep(in); got != want {
			t.Errorf("clampExportKeep(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestExportConfigValuesAreClamped 库里存了越界值时，读出来的配置必须是夹过的
// 合法值——脏配置不能变成「每 0 小时导一次」或「删到只剩 0 份」。
func TestExportConfigValuesAreClamped(t *testing.T) {
	e := setupExportEnv(t)

	if err := sysconfig.UpdateConfig(e.db, "rental_export_interval_hours", "99999", 0); err != nil {
		t.Fatal(err)
	}
	if err := sysconfig.UpdateConfig(e.db, "rental_export_keep_count", "0", 0); err != nil {
		t.Fatal(err)
	}
	if got := exportIntervalHours(e.db); got != exportIntervalMax {
		t.Fatalf("越界间隔未被夹住: got %d, want %d", got, exportIntervalMax)
	}
	if got := exportKeepCount(e.db); got != exportKeepMin {
		t.Fatalf("越界保留数未被夹住: got %d, want %d", got, exportKeepMin)
	}

	// 列表接口回带的也必须是夹过的值。
	w := e.do(t, http.MethodGet, "/api/rental/exports")
	data := decodeExportData(t, w)
	if data.IntervalHours != exportIntervalMax || data.KeepCount != exportKeepMin {
		t.Fatalf("列表未回带夹取后的配置: %+v", data)
	}
}

// TestExportConfigNonNumericFallsBackToDefault 库里存了非数字时回退默认值，
// 而不是变成 0。
func TestExportConfigNonNumericFallsBackToDefault(t *testing.T) {
	e := setupExportEnv(t)
	if err := sysconfig.UpdateConfig(e.db, "rental_export_interval_hours", "abc", 0); err != nil {
		t.Fatal(err)
	}
	if err := sysconfig.UpdateConfig(e.db, "rental_export_keep_count", "", 0); err != nil {
		t.Fatal(err)
	}
	if got := exportIntervalHours(e.db); got != exportIntervalDefault {
		t.Fatalf("非数字间隔应回退默认, got %d, want %d", got, exportIntervalDefault)
	}
	if got := exportKeepCount(e.db); got != exportKeepDefault {
		t.Fatalf("空保留数应回退默认, got %d, want %d", got, exportKeepDefault)
	}
}

// TestExportConfigDefaultsAreRegistered 三个配置项必须已注册且带中文
// Label/Description——否则系统配置页上根本看不到这一组。
func TestExportConfigDefaultsAreRegistered(t *testing.T) {
	for key, wantType := range map[string]sysconfig.ValueType{
		"rental_export_enabled":        sysconfig.TypeBool,
		"rental_export_interval_hours": sysconfig.TypeInt,
		"rental_export_keep_count":     sysconfig.TypeInt,
	} {
		def, ok := sysconfig.GetConfigDef(key)
		if !ok {
			t.Fatalf("配置 %s 未注册", key)
		}
		if def.Type != wantType {
			t.Errorf("%s 类型 = %q, want %q", key, def.Type, wantType)
		}
		if def.Scope != sysconfig.ScopeSystem {
			t.Errorf("%s 作用域 = %q, want system", key, def.Scope)
		}
		if strings.TrimSpace(def.Label) == "" || strings.TrimSpace(def.Description) == "" {
			t.Errorf("%s 缺中文 Label/Description", key)
		}
		// 组键是英文键 export，中文标题「数据安全（定时导出）」由前端翻译
		//（2026-10-13 配置页分组优化，与 billing/rental 组同一口径）。
		if def.Group != "export" {
			t.Errorf("%s 分组 = %q, want export", key, def.Group)
		}
	}
	// 默认值：开关开、24 小时、留 30 份。
	if v, _ := sysconfig.GetConfigDef("rental_export_enabled"); v.Default != "true" {
		t.Errorf("rental_export_enabled 默认 = %q, want true", v.Default)
	}
	if v, _ := sysconfig.GetConfigDef("rental_export_interval_hours"); v.Default != "24" {
		t.Errorf("rental_export_interval_hours 默认 = %q, want 24", v.Default)
	}
	if v, _ := sysconfig.GetConfigDef("rental_export_keep_count"); v.Default != "30" {
		t.Errorf("rental_export_keep_count 默认 = %q, want 30", v.Default)
	}
}

// TestScheduledExportRespectsSwitchAndInterval 定时任务的三条行为：
// 关掉开关不导出；没到间隔不导出；到点（或从没导过）才导出。
func TestScheduledExportRespectsSwitchAndInterval(t *testing.T) {
	e := setupExportEnv(t)

	// 开关关掉：即使从没导过也不导出。
	if err := sysconfig.UpdateConfig(e.db, "rental_export_enabled", "false", 0); err != nil {
		t.Fatal(err)
	}
	runScheduledExport()
	if items, _ := ListExports(); len(items) != 0 {
		t.Fatalf("开关关闭时不应导出, got %d 份", len(items))
	}

	// 打开：从未导过 → 立刻补一份。
	if err := sysconfig.UpdateConfig(e.db, "rental_export_enabled", "true", 0); err != nil {
		t.Fatal(err)
	}
	runScheduledExport()
	items, _ := ListExports()
	if len(items) != 1 {
		t.Fatalf("从未导出时应立刻补一份, got %d", len(items))
	}

	// 间隔未到（默认 24 小时）：不重复导出。
	runScheduledExport()
	if items, _ := ListExports(); len(items) != 1 {
		t.Fatalf("未到导出间隔时不应重复导出, got %d 份", len(items))
	}

	// 把间隔调到 1 小时、并把上一份的时间戳改老 → 到点，导出第二份。
	if err := sysconfig.UpdateConfig(e.db, "rental_export_interval_hours", "1", 0); err != nil {
		t.Fatal(err)
	}
	ageItems, _ := ListExports()
	old := filepath.Join(exportsDir(), ageItems[0].Name)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	runScheduledExport()
	if items, _ := ListExports(); len(items) != 2 {
		t.Fatalf("到达导出间隔后应新增一份, got %d 份", len(items))
	}
}

// TestScheduledExportFailureIsContained 定时任务在数据库不可用时只记日志，
// 不 panic——一个后台任务绝不能把主服务带崩。
func TestScheduledExportFailureIsContained(t *testing.T) {
	prev := exportDB
	exportDB = nil // 未注入数据库 = 依赖缺失
	defer func() {
		exportDB = prev
		if r := recover(); r != nil {
			t.Fatalf("定时任务 panic 了: %v", r)
		}
	}()
	runScheduledExport() // 应静默返回
}

// TestConcurrentExportIsSerialized 并发触发「立即导出」时只有一个真正执行，
// 其余被拒（防重叠），且导出标记一定会被释放。
func TestConcurrentExportIsSerialized(t *testing.T) {
	e := setupExportEnv(t)

	if !tryStartExport() {
		t.Fatal("首次抢占应成功")
	}
	// 模拟另一个请求同时点「立即导出」。
	w := e.do(t, http.MethodPost, "/api/rental/exports")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("重叠导出应被拒, status = %d, want 400", w.Code)
	}
	finishExport()

	// 标记已释放，后续导出恢复可用。
	w = e.do(t, http.MethodPost, "/api/rental/exports")
	if w.Code != http.StatusOK {
		t.Fatalf("释放后应可导出, status = %d, body = %s", w.Code, w.Body.String())
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

type exportListData struct {
	Items         []ExportItem `json:"items"`
	Dir           string       `json:"dir"`
	Enabled       bool         `json:"enabled"`
	IntervalHours int          `json:"interval_hours"`
	KeepCount     int          `json:"keep_count"`
}

// exportCreateData 是「立即导出」的响应体（列表接口的结构不同，分开解）。
type exportCreateData struct {
	Name    string `json:"name"`
	Removed int    `json:"removed"`
}

func decodeExportCreate(t *testing.T, w *httptest.ResponseRecorder) exportCreateData {
	t.Helper()
	var resp struct {
		Code int              `json:"code"`
		Data exportCreateData `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析导出响应失败: %v (body=%s)", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("业务码 = %d, want 0 (body=%s)", resp.Code, w.Body.String())
	}
	return resp.Data
}

func decodeExportData(t *testing.T, w *httptest.ResponseRecorder) exportListData {
	t.Helper()
	var resp struct {
		Code int            `json:"code"`
		Data exportListData `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("业务码 = %d, want 0 (body=%s)", resp.Code, w.Body.String())
	}
	return resp.Data
}
