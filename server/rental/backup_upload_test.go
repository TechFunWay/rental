package rental

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"smallgo/server/backup"
	"smallgo/server/config"
	"smallgo/server/database"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
)

// backupNamePattern 与框架 backup 包同规则（那边是私有变量，这里复刻一份）。
// 上传产出的文件名必须过得了这一关，才能被列表 / 下载 / 恢复放行。
var backupNamePattern = regexp.MustCompile(`^backup_\d{8}_\d{6}(?:-\d+)?\.db$`)

// setupUploadEnv 建一个完整可跑的备份上传环境：
// 主库落在 <dataDir>/db/rental.db（与框架 backup.Restore 约定的路径一致，
// 恢复用例才能按真实布局复刻替换），路由只挂上传接口（adminGroup 的鉴权由
// 框架 middleware 负责，这里注入身份即可）。
func setupUploadEnv(t *testing.T) (*gin.Engine, *gorm.DB, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "db", "rental.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("建 db 目录: %v", err)
	}
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

	prevDataDir := config.C.DataDir
	config.C.DataDir = dataDir
	backup.Dir(dataDir) // 预创建备份目录
	t.Cleanup(func() {
		config.C.DataDir = prevDataDir
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})

	r := gin.New()
	api := r.Group("/api")
	api.Use(func(c *gin.Context) {
		c.Set("userID", uint(1))
		c.Set("username", "admin")
		c.Next()
	})
	setupBackupUploadRoutes(api, db)
	return r, db, dataDir, dbPath
}

// seedRentalData 写入一条带敏感字段的业务数据（身份证号 / 押金 / 租约日期），
// 用来验证「备份 → 上传 → 恢复」之后数据逐字段原样回来。
func seedRentalData(t *testing.T, db *gorm.DB) (room Room, tenant Tenant) {
	t.Helper()
	room = Room{
		UserID: 1, Community: "阳光小区", RoomNo: "3-502",
		DefaultRent: 1800, Notes: "主卧带阳台",
	}
	if err := db.Create(&room).Error; err != nil {
		t.Fatalf("seed room: %v", err)
	}
	tenant = Tenant{
		UserID: 1, RoomID: room.ID, Name: "张三", Phone: "13800000000",
		IDCard: "310101199001011234", MoveInDate: "2026-01-01",
		LeaseEndDate: "2026-12-31", Deposit: 3600, Active: true,
	}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return room, tenant
}

// uploadFile 构造一次 multipart 上传请求。
func uploadFile(t *testing.T, r http.Handler, filename string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("构造上传表单: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("写上传内容: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("关闭上传表单: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/rental/backups/upload", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func decodeUploadResp(t *testing.T, rec *httptest.ResponseRecorder) (int, string, string) {
	t.Helper()
	var body struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应失败: %v (body=%s)", err, rec.Body.String())
	}
	return body.Code, body.Message, body.Data.Name
}

// noUploadResidue 断言备份目录里没有留下任何临时上传文件。
func noUploadResidue(t *testing.T, dataDir string) {
	t.Helper()
	entries, err := os.ReadDir(backup.Dir(dataDir))
	if err != nil {
		t.Fatalf("读备份目录: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".upload-") {
			t.Errorf("留下临时上传残余文件: %s", e.Name())
		}
	}
}

// makeDownloadedBackup 造一份当前库的备份并读出字节，随后把服务器上那份删掉——
// 模拟用户「下载到本机、服务器上已没有这份文件」的换机 / 异地容灾场景。
func makeDownloadedBackup(t *testing.T, db *gorm.DB, dataDir string) []byte {
	t.Helper()
	name, err := backup.Create(db, dataDir)
	if err != nil {
		t.Fatalf("创建备份: %v", err)
	}
	src := filepath.Join(backup.Dir(dataDir), name)
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("读备份: %v", err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatalf("删除服务器上的备份（模拟已下载到本机）: %v", err)
	}
	return body
}

// restoreByReplacingMainDB 用指定备份覆盖主库并重新打开，完全复刻框架
// backup.Restore 的第 2、3 步（关连接 → 同分区临时名 + rename 原子替换 →
// 清 WAL/SHM）。
//
// 这里刻意不直接调 backup.Restore：它的第 4 步会异步 os.Exit 退出进程等守护
// 重启（exitHook 未导出，实例层测试无法拦截），那样会把整个测试二进制带下去、
// 后面的用例一条都不执行。
func restoreByReplacingMainDB(t *testing.T, db *gorm.DB, dbPath, backupPath string) *gorm.DB {
	t.Helper()
	if err := database.CloseDB(db); err != nil {
		t.Fatalf("关闭主库连接: %v", err)
	}
	in, err := os.Open(backupPath)
	if err != nil {
		t.Fatalf("打开备份: %v", err)
	}
	tmp := dbPath + ".restore-tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		in.Close()
		t.Fatalf("建替换临时文件: %v", err)
	}
	if _, err := out.ReadFrom(in); err != nil {
		in.Close()
		out.Close()
		t.Fatalf("复制备份: %v", err)
	}
	in.Close()
	out.Close()
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}
	if err := os.Rename(tmp, dbPath); err != nil {
		t.Fatalf("替换主库: %v", err)
	}
	restored, err := database.InitDB(dbPath)
	if err != nil {
		t.Fatalf("重开主库: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := restored.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return restored
}

// TestUploadBackupRestoresData 是主链路用例：备份 → 上传 → 进列表 → 删数据 →
// 恢复 → 数据原样回来。覆盖用户最关心的换机 / 异地容灾场景。
func TestUploadBackupRestoresData(t *testing.T) {
	r, db, dataDir, dbPath := setupUploadEnv(t)
	room, tenant := seedRentalData(t, db)
	body := makeDownloadedBackup(t, db, dataDir)

	// 上传：原文件名带中文与空格（浏览器下载名常见形态）
	rec := uploadFile(t, r, "我的 备份 2026.db", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传失败: status=%d body=%s", rec.Code, rec.Body.String())
	}
	code, msg, name := decodeUploadResp(t, rec)
	if code != 0 {
		t.Fatalf("上传业务码 = %d, msg=%s", code, msg)
	}
	// 正式名由服务端重新生成，必须满足框架的备份名规则（顺带证明原文件名
	// 没有落盘——返回名里没有任何中文、空格或原始片段）
	if !backupNamePattern.MatchString(name) {
		t.Fatalf("上传后的文件名 %q 不符合 backup_YYYYMMDD_HHMMSS.db 规则", name)
	}

	// 备份列表里能看到它
	items, err := backup.List(dataDir)
	if err != nil {
		t.Fatalf("读备份列表: %v", err)
	}
	found := false
	for _, it := range items {
		if it.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("上传的备份 %q 不在列表中: %+v", name, items)
	}

	// 上传的是一份独立副本（源文件已从服务器删除）
	got, err := os.ReadFile(filepath.Join(backup.Dir(dataDir), name))
	if err != nil {
		t.Fatalf("读上传后的备份: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("上传后的备份内容与源文件不一致（%d vs %d 字节）", len(got), len(body))
	}

	// 破坏现场：删光业务数据
	if err := db.Where("1 = 1").Delete(&Tenant{}).Error; err != nil {
		t.Fatalf("删租户: %v", err)
	}
	if err := db.Where("1 = 1").Delete(&Room{}).Error; err != nil {
		t.Fatalf("删房源: %v", err)
	}
	var cnt int64
	if err := db.Model(&Room{}).Count(&cnt).Error; err != nil || cnt != 0 {
		t.Fatalf("删除后房源仍有 %d 条 (err=%v)", cnt, err)
	}

	// 恢复：走与线上一致的文件级替换
	restored := restoreByReplacingMainDB(t, db, dbPath, filepath.Join(backup.Dir(dataDir), name))

	var gotRoom Room
	if err := restored.First(&gotRoom, room.ID).Error; err != nil {
		t.Fatalf("恢复后查房源: %v", err)
	}
	if gotRoom.Community != room.Community || gotRoom.RoomNo != room.RoomNo || gotRoom.Notes != room.Notes {
		t.Errorf("恢复后房源 = %+v, want %+v", gotRoom, room)
	}
	var gotTenant Tenant
	if err := restored.First(&gotTenant, tenant.ID).Error; err != nil {
		t.Fatalf("恢复后查租户: %v", err)
	}
	if gotTenant.IDCard != tenant.IDCard || gotTenant.Deposit != tenant.Deposit ||
		gotTenant.MoveInDate != tenant.MoveInDate || gotTenant.LeaseEndDate != tenant.LeaseEndDate {
		t.Errorf("恢复后租户 = %+v, want %+v", gotTenant, tenant)
	}

	noUploadResidue(t, dataDir)
}

// TestUploadBackupRejectsBadFiles 覆盖校验链的各个失败分支，以及「失败时目录里
// 不留残余文件」。
func TestUploadBackupRejectsBadFiles(t *testing.T) {
	r, db, dataDir, _ := setupUploadEnv(t)
	seedRentalData(t, db)
	good := makeDownloadedBackup(t, db, dataDir)

	// 缺 users 表的合法 SQLite：结构完好、内容合法，但不是本应用的备份
	noUsersPath := filepath.Join(t.TempDir(), "other.db")
	other, err := gorm.Open(sqlite.Open(noUsersPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("建外部库: %v", err)
	}
	if err := other.Exec("CREATE TABLE unrelated (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("建表: %v", err)
	}
	if sqlDB, e := other.DB(); e == nil {
		_ = sqlDB.Close()
	}
	noUsers, err := os.ReadFile(noUsersPath)
	if err != nil {
		t.Fatalf("读外部库: %v", err)
	}

	// 传输中断的半截文件：SQLite 魔数完整，完整性检查才认得出问题
	truncated := good[:len(good)/2]

	cases := []struct {
		name     string
		filename string
		body     []byte
		wantMsg  string
	}{
		{"非 .db 后缀", "not-a-backup.txt", good, "只支持 .db 备份文件"},
		{"纯垃圾内容", "junk.db", []byte("this is not a database"), "不是有效的数据库备份"},
		{"文件过小", "tiny.db", []byte("hi"), "不是有效的数据库备份"},
		{"合法 SQLite 但缺 users 表", "other-app.db", noUsers, "users 表"},
		{"上传中断的半截文件", "half.db", truncated, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := uploadFile(t, r, tc.filename, tc.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", rec.Code, rec.Body.String())
			}
			_, msg, name := decodeUploadResp(t, rec)
			if name != "" {
				t.Errorf("失败响应不应返回备份名, got %q", name)
			}
			if tc.wantMsg != "" && !strings.Contains(msg, tc.wantMsg) {
				t.Errorf("错误提示 = %q, 期望包含 %q", msg, tc.wantMsg)
			}
			// 每个失败分支都不许在备份目录里留下任何东西
			noUploadResidue(t, dataDir)
			items, err := backup.List(dataDir)
			if err != nil {
				t.Fatalf("读备份列表: %v", err)
			}
			if len(items) != 0 {
				t.Fatalf("校验失败的 %s 竟出现在备份列表里: %+v", tc.filename, items)
			}
		})
	}

	// 不带 file 字段 → 400
	rec := uploadFile(t, r, "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("空上传 status = %d, want 400", rec.Code)
	}
	noUploadResidue(t, dataDir)
}

// TestUploadBackupIgnoresClientFilename 验证原文件名只进审计日志、不参与落盘：
// multipart 的 filename 完全由客户端控制，带路径穿越形态也必须被丢弃，
// 落盘名一律是服务端生成的 backup_YYYYMMDD_HHMMSS.db。
func TestUploadBackupIgnoresClientFilename(t *testing.T) {
	r, db, dataDir, _ := setupUploadEnv(t)
	seedRentalData(t, db)
	good := makeDownloadedBackup(t, db, dataDir)

	rec := uploadFile(t, r, "../../../../etc/evil.db", good)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传失败: status=%d body=%s", rec.Code, rec.Body.String())
	}
	_, _, name := decodeUploadResp(t, rec)
	if !backupNamePattern.MatchString(name) {
		t.Fatalf("落盘名 %q 未按服务端规则生成", name)
	}
	if _, err := os.Stat(filepath.Join(backup.Dir(dataDir), name)); err != nil {
		t.Fatalf("按生成名找不到上传的备份: %v", err)
	}

	// 两次上传（同一秒）不能互相覆盖：同秒冲突追加 -N
	rec2 := uploadFile(t, r, "第二份.db", good)
	if rec2.Code != http.StatusOK {
		t.Fatalf("第二次上传失败: status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	_, _, name2 := decodeUploadResp(t, rec2)
	if name2 == name {
		t.Fatalf("同秒两次上传拿到同一个名字 %q，后一次覆盖了前一次", name2)
	}
	items, err := backup.List(dataDir)
	if err != nil {
		t.Fatalf("读备份列表: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("备份列表 = %+v, want 2 份", items)
	}
	noUploadResidue(t, dataDir)
}

// TestUploadBackupWritesAuditLog 验证上传留痕：动作 backup_upload，细节里带
// 原文件名（管理员事后能在审计里对上「这份备份是哪来的」）。
func TestUploadBackupWritesAuditLog(t *testing.T) {
	r, db, dataDir, _ := setupUploadEnv(t)
	seedRentalData(t, db)
	good := makeDownloadedBackup(t, db, dataDir)

	rec := uploadFile(t, r, "2026 年 3 月的备份.db", good)
	if rec.Code != http.StatusOK {
		t.Fatalf("上传失败: status=%d body=%s", rec.Code, rec.Body.String())
	}
	_, _, name := decodeUploadResp(t, rec)

	var entry database.AuditLog
	if err := db.Where("action = ?", "backup_upload").First(&entry).Error; err != nil {
		t.Fatalf("查审计日志: %v", err)
	}
	if !strings.Contains(entry.Detail, "2026 年 3 月的备份.db") {
		t.Errorf("审计细节 = %q, 期望含原文件名", entry.Detail)
	}
	if !strings.Contains(entry.Detail, name) {
		t.Errorf("审计细节 = %q, 期望含落盘名 %s", entry.Detail, name)
	}
	if entry.TargetType != "backup" {
		t.Errorf("审计对象类型 = %q, want backup", entry.TargetType)
	}
}

// TestVerifyUploadedBackupUnit 直接打校验函数：确保三步判据（文件头 /
// integrity_check / users 表）各自独立生效，不只是被 handler 顺带覆盖。
func TestVerifyUploadedBackupUnit(t *testing.T) {
	_, db, dataDir, _ := setupUploadEnv(t)
	seedRentalData(t, db)
	good := filepath.Join(backup.Dir(dataDir), "backup_20260101_000000.db")
	body := makeDownloadedBackup(t, db, dataDir)
	if err := os.WriteFile(good, body, 0o644); err != nil {
		t.Fatalf("写备份: %v", err)
	}
	if err := verifyUploadedBackup(good); err != nil {
		t.Fatalf("合法备份应通过校验: %v", err)
	}

	cases := map[string][]byte{
		"非 SQLite":     []byte(strings.Repeat("x", 4096)),
		"空文件":          {},
		"截断的备份":        body[:len(body)/3],
		"只有 SQLite 魔数": []byte(sqliteHeader),
	}
	for name, content := range cases {
		p := filepath.Join(t.TempDir(), "c.db")
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatalf("写用例文件: %v", err)
		}
		if err := verifyUploadedBackup(p); err == nil {
			t.Errorf("%s 应校验失败", name)
		}
	}

	// 打开失败路径：文件不存在
	if err := verifyUploadedBackup(filepath.Join(t.TempDir(), "missing.db")); err == nil {
		t.Error("不存在的文件应校验失败")
	}
}
