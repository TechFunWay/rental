package backup

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"smallgo/server/database"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func setup(t *testing.T) (*gin.Engine, *gorm.DB, string) {
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

	Dir(dir) // 预创建备份目录

	r := gin.New()
	RegisterRoutes(r.Group("/api"), db, dir)
	return r, db, dir
}

func TestCreateAndList(t *testing.T) {
	r, _, dir := setup(t)

	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/backups", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("create status = %d, body = %s", resp.Code, resp.Body.String())
	}

	items, err := List(dir)
	if err != nil || len(items) != 1 {
		t.Fatalf("List() = %v, %v", items, err)
	}
	info, err := os.Stat(filepath.Join(dir, dirName, items[0].Name))
	if err != nil || info.Size() == 0 {
		t.Fatalf("backup file missing or empty: %v", err)
	}

	// 快照可被当作 SQLite 数据库打开（VACUUM INTO 产物完整）
	snap, err := database.InitDB(filepath.Join(dir, dirName, items[0].Name))
	if err != nil {
		t.Fatalf("snapshot not a valid sqlite db: %v", err)
	}
	database.CloseDB(snap)
}

func TestDownloadAndDelete(t *testing.T) {
	r, _, dir := setup(t)

	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/backups", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("create status = %d", resp.Code)
	}
	items, _ := List(dir)
	name := items[0].Name

	// 下载
	resp = httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/backups/"+name+"/download", nil))
	if resp.Code != http.StatusOK || resp.Body.Len() == 0 {
		t.Fatalf("download status = %d", resp.Code)
	}

	// 非法文件名（含路径穿越形态、不匹配白名单正则）必须被拒；
	// gin 路由会把 %2F 解码后清洗为 404，这里用单段非法名验证正则防线。
	for _, bad := range []string{"evil.db", "backup_20260101_000000.db.bak", ".."} {
		resp = httptest.NewRecorder()
		r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/backups/"+bad+"/download", nil))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid name %q download status = %d, want 400", bad, resp.Code)
		}
		resp = httptest.NewRecorder()
		r.ServeHTTP(resp, httptest.NewRequest(http.MethodDelete, "/api/backups/"+bad, nil))
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid name %q delete status = %d, want 400", bad, resp.Code)
		}
	}

	// 删除
	resp = httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodDelete, "/api/backups/"+name, nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", resp.Code, resp.Body.String())
	}
	items, _ = List(dir)
	if len(items) != 0 {
		t.Fatalf("backup not deleted: %d left", len(items))
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	_, _, dir := setup(t)
	for _, ts := range []string{"20260101_000000", "20260102_000000", "20260103_000000", "20260104_000000"} {
		p := filepath.Join(dir, dirName, fmt.Sprintf("backup_%s.db", ts))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed backup: %v", err)
		}
	}
	removed, err := Prune(dir, 2)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	items, _ := List(dir)
	if len(items) != 2 || items[0].Name != "backup_20260104_000000.db" || items[1].Name != "backup_20260103_000000.db" {
		t.Fatalf("kept = %+v", items)
	}
}

func TestAutoBackupDisabledByConfig(t *testing.T) {
	r, db, dir := setup(t)
	if err := sysconfig.UpdateConfig(db, "backup_auto_enabled", "false", 0); err != nil {
		t.Fatalf("disable auto backup: %v", err)
	}

	// runAutoBackup 应直接跳过：列表为空
	dbRef, dataRef = db, dir
	runAutoBackup()
	items, _ := List(dir)
	if len(items) != 0 {
		t.Fatalf("auto backup ran despite disabled config: %+v", items)
	}
	_ = r
}

func TestAutoBackupRunsAndPrunes(t *testing.T) {
	r, db, dir := setup(t)
	dbRef, dataRef = db, dir
	t.Cleanup(func() { dbRef, dataRef = nil, "" })

	if err := sysconfig.UpdateConfig(db, "backup_keep_count", "2", 0); err != nil {
		t.Fatalf("set keep count: %v", err)
	}
	for i := 0; i < 3; i++ {
		runAutoBackup()
	}
	items, _ := List(dir)
	if len(items) != 2 {
		t.Fatalf("backups kept = %d, want 2", len(items))
	}

	var listResp struct {
		Data struct {
			AutoEnabled bool   `json:"auto_enabled"`
			KeepCount   int    `json:"keep_count"`
			Items       []Item `json:"items"`
		} `json:"data"`
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/backups", nil))
	if err := json.Unmarshal(resp.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if !listResp.Data.AutoEnabled || listResp.Data.KeepCount != 2 || len(listResp.Data.Items) != 2 {
		t.Fatalf("list payload = %+v", listResp.Data)
	}
}

// TestRestore 恢复流程：建库→写标记数据→备份→删数据→恢复→主库内容回到备份时点。
// exitHook 被替换以拦截 os.Exit；主库路径按 config 的布局放在 <dir>/db/test.db。
func TestRestore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	dbDir := filepath.Join(dir, "db")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dbDir, "rental.db")
	db, err := database.InitDB(dbPath)
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatalf("init configs: %v", err)
	}

	exited := make(chan struct{})
	restoreExited := exitHook
	exitHook = func() { close(exited) }
	t.Cleanup(func() {
		exitHook = restoreExited
		database.CloseDB(db)
	})

	Dir(dir)
	r := gin.New()
	RegisterRoutes(r.Group("/api"), db, dir)

	// 写入标记配置并备份
	if err := sysconfig.UpdateConfig(db, "site_title", "restore-marker", 0); err != nil {
		t.Fatalf("set marker: %v", err)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/backups", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("create backup status = %d", resp.Code)
	}
	items, _ := List(dir)
	if len(items) != 1 {
		t.Fatalf("backups = %d, want 1", len(items))
	}
	backupName := items[0].Name

	// 破坏现场：改成另一个值
	if err := sysconfig.UpdateConfig(db, "site_title", "damaged", 0); err != nil {
		t.Fatal(err)
	}

	// 恢复
	resp2 := httptest.NewRecorder()
	r.ServeHTTP(resp2, httptest.NewRequest(http.MethodPost, "/api/backups/"+backupName+"/restore", nil))
	if resp2.Code != http.StatusOK {
		t.Fatalf("restore status = %d, body = %s", resp2.Code, resp2.Body.String())
	}

	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("process did not schedule exit after restore")
	}

	// 重新打开主库，验证内容回到备份时点
	restored, err := database.InitDB(dbPath)
	if err != nil {
		t.Fatalf("reopen restored db: %v", err)
	}
	defer database.CloseDB(restored)
	v, err := sysconfig.GetConfig(restored, "site_title", 0)
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if v != "restore-marker" {
		t.Fatalf("site_title after restore = %q, want restore-marker", v)
	}

	// 恢复前安全备份应已生成（列表里出现第二个备份）
	items2, _ := List(dir)
	if len(items2) != 2 {
		t.Fatalf("backups after restore = %d, want 2 (含恢复前快照)", len(items2))
	}
}

func TestRestoreRejectsBadName(t *testing.T) {
	r, _, _ := setup(t)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/api/backups/evil.db/restore", nil))
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.Code)
	}
}
