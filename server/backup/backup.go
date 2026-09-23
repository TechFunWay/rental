// Package backup 提供数据库备份管理：手动/自动创建 SQLite 快照、列表、
// 下载与删除，并按保留数量自动清理。
//
// 备份文件放在 <data-dir>/backups 下，命名为 backup_YYYYMMDD_HHMMSS.db，
// 由 SQLite 的 VACUUM INTO 生成一致性快照（WAL 模式下也安全）。
package backup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"smallgo/server/audit"
	"smallgo/server/logger"
	"smallgo/server/response"
	"smallgo/server/scheduler"
	"smallgo/server/sysconfig"
)

const dirName = "backups"

// backup_YYYYMMDD_HHMMSS.db；同秒多次备份时追加 -N 序号（见 Create）。
var namePattern = regexp.MustCompile(`^backup_\d{8}_\d{6}(?:-\d+)?\.db$`)

var (
	dbRef   *gorm.DB
	dataRef string
)

// Dir 返回备份目录路径并确保存在。
func Dir(dataDir string) string {
	dir := filepath.Join(dataDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("创建备份目录失败: %v", err)
	}
	return dir
}

// Create 生成一个一致性数据库快照，返回文件名。
// 时间戳精确到秒，同秒内再次备份时自动追加 -1/-2 序号避免覆盖。
func Create(db *gorm.DB, dataDir string) (string, error) {
	dir := Dir(dataDir)
	base := "backup_" + time.Now().Format("20060102_150405")
	target := filepath.Join(dir, base+".db")
	if _, err := os.Stat(target); err == nil {
		for i := 1; ; i++ {
			name := fmt.Sprintf("%s-%d.db", base, i)
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); os.IsNotExist(err) {
				target = p
				break
			}
			if i >= 99 {
				return "", fmt.Errorf("备份文件名冲突过多")
			}
		}
	}
	if err := db.Exec("VACUUM INTO ?", target).Error; err != nil {
		return "", fmt.Errorf("VACUUM INTO 失败: %w", err)
	}
	return filepath.Base(target), nil
}

// Item 是备份文件的列表视图。
type Item struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// List 返回全部备份，新在前。
func List(dataDir string) ([]Item, error) {
	entries, err := os.ReadDir(Dir(dataDir))
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0)
	for _, e := range entries {
		if e.IsDir() || !namePattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, Item{
			Name:      e.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name > items[j].Name })
	return items, nil
}

// Prune 按保留数量清理最旧的备份，返回删除的文件数。
func Prune(dataDir string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	items, err := List(dataDir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for i := keep; i < len(items); i++ {
		if err := os.Remove(filepath.Join(Dir(dataDir), items[i].Name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// Restore 用指定备份覆盖主库文件（<data-dir>/db/rental.db），返回恢复前
// 自动创建的安全备份名。执行序列：
//
//  1. 先 VACUUM INTO 一份"恢复前快照"——恢复动作本身可回退；
//  2. 关闭数据库连接（SQLite 不允许在打开状态下替换自身文件，MaxOpenConns=1
//     关闭后不再有活跃句柄）；
//  3. 用备份文件原子替换主库（同分区 rename），并清理 WAL/SHM 残留；
//  4. 进程退出。飞牛对 checkport=true 的应用监控端口，进程退出后自动拉起，
//     重启后即运行在恢复出的数据上；非守护场景（make dev、裸进程）需手动重启。
//
// 步骤 2 之后任何失败都不可再返回错误给调用方处理业务——连接已断，进程注定
// 要退，直接记日志并退出，避免"半恢复"状态下继续服务。
func Restore(dataDir, backupName string) (preBackup string, err error) {
	if !namePattern.MatchString(backupName) {
		return "", fmt.Errorf("无效的备份文件名")
	}
	src := filepath.Join(Dir(dataDir), backupName)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("备份不存在")
	}

	// 1. 恢复前安全备份（复用 Create 的同秒序号逻辑，不覆盖既有文件）。
	preBackup, err = Create(dbRef, dataDir)
	if err != nil {
		return "", fmt.Errorf("创建恢复前安全备份失败: %w", err)
	}

	target := filepath.Join(dataDir, "db", "rental.db")
	if _, err := os.Stat(target); err != nil {
		return "", fmt.Errorf("找不到主数据库文件: %w", err)
	}

	// 2. 关闭连接池，释放主库文件句柄。
	sqlDB, err := dbRef.DB()
	if err != nil {
		return "", fmt.Errorf("获取底层连接失败: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		logger.Error("关闭数据库连接失败（继续恢复）: %v", err)
	}

	// 3. 同分区临时名 + rename 原子替换；清掉 WAL/SHM，避免旧 WAL 回放污染新库。
	tmp := target + ".restore-tmp"
	if err := copyFile(src, tmp); err != nil {
		logger.Error("复制备份文件失败: %v", err)
		go exitHook()
		return preBackup, nil
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(target + suffix)
	}
	if err := os.Rename(tmp, target); err != nil {
		logger.Error("替换主库文件失败: %v", err)
		go exitHook()
		return preBackup, nil
	}
	logger.Info("数据库已从 %s 恢复（恢复前快照 %s），进程即将退出等待自动重启", backupName, preBackup)

	// 4. 异步退出：让 HTTP 响应先写回客户端。
	go exitHook()
	return preBackup, nil
}

// exitHook 恢复成功后的退出动作，测试里替换以拦截 os.Exit。
var exitHook = fatalExit

func fatalExit() {
	time.Sleep(300 * time.Millisecond)
	logger.Info("恢复完成，进程退出（等待飞牛守护自动拉起）")
	os.Exit(0)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Sync()
}

// keepCount 读取保留数量配置，非法值回退默认 7。
func keepCount(db *gorm.DB) int {
	keep := 7
	if db == nil {
		return keep
	}
	if v, err := sysconfig.GetConfig(db, "backup_keep_count", 0); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			keep = n
		}
	}
	return keep
}

// autoEnabled 读取自动备份开关（默认开启）。
func autoEnabled(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	v, err := sysconfig.GetConfig(db, "backup_auto_enabled", 0)
	if err != nil || v == "" {
		return true
	}
	return v != "false"
}

func runAutoBackup() {
	db, dataDir := dbRef, dataRef
	if db == nil || dataDir == "" {
		return
	}
	if !autoEnabled(db) {
		return
	}
	name, err := Create(db, dataDir)
	if err != nil {
		logger.Error("自动备份失败: %v", err)
		return
	}
	if n, err := Prune(dataDir, keepCount(db)); err != nil {
		logger.Error("备份清理失败: %v", err)
	} else if n > 0 {
		logger.Info("自动备份 %s 完成，清理了 %d 个过期备份", name, n)
	}
}

// RegisterRoutes 挂载管理员备份接口，并记住运行时依赖供自动备份任务使用。
func RegisterRoutes(admin *gin.RouterGroup, db *gorm.DB, dataDir string) {
	dbRef, dataRef = db, dataDir

	admin.GET("/backups", func(c *gin.Context) {
		items, err := List(dataDir)
		if err != nil {
			response.ErrorInternal(c, "读取备份列表失败")
			return
		}
		response.Success(c, gin.H{
			"items":         items,
			"dir":           Dir(dataDir),
			"auto_enabled":  autoEnabled(db),
			"keep_count":    keepCount(db),
		})
	})

	admin.POST("/backups", func(c *gin.Context) {
		name, err := Create(db, dataDir)
		if err != nil {
			logger.Error("手动备份失败: %v", err)
			response.ErrorInternal(c, "备份失败："+err.Error())
			return
		}
		if n, err := Prune(dataDir, keepCount(db)); err == nil && n > 0 {
			logger.Info("备份 %s 完成，清理了 %d 个过期备份", name, n)
		}
		audit.Log(db, c, "backup_create", "backup", 0, name)
		response.Success(c, gin.H{"name": name})
	})

	admin.GET("/backups/:name/download", func(c *gin.Context) {
		name := c.Param("name")
		if !namePattern.MatchString(name) {
			response.ErrorBadRequest(c, "无效的备份文件名")
			return
		}
		path := filepath.Join(Dir(dataDir), name)
		if _, err := os.Stat(path); err != nil {
			response.ErrorNotFound(c, "备份不存在")
			return
		}
		c.FileAttachment(path, name)
	})

	admin.POST("/backups/:name/restore", func(c *gin.Context) {
		name := c.Param("name")
		pre, err := Restore(dataDir, name)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		audit.Log(db, c, "backup_restore", "backup", 0, name)
		response.Success(c, gin.H{"ok": true, "pre_backup": pre, "restarting": true})
	})

	admin.DELETE("/backups/:name", func(c *gin.Context) {
		name := c.Param("name")
		if !namePattern.MatchString(name) {
			response.ErrorBadRequest(c, "无效的备份文件名")
			return
		}
		path := filepath.Join(Dir(dataDir), name)
		if _, err := os.Stat(path); err != nil {
			response.ErrorNotFound(c, "备份不存在")
			return
		}
		if err := os.Remove(path); err != nil {
			response.ErrorInternal(c, "删除备份失败")
			return
		}
		audit.Log(db, c, "backup_delete", "backup", 0, name)
		response.Success(c, gin.H{"ok": true})
	})
}

func init() {
	scheduler.Register(scheduler.Job{
		Name: "auto_backup",
		// 每天低峰期执行一次；是否真正备份由 backup_auto_enabled 决定
		Daily: "03:00",
		Run:   runAutoBackup,
	})

	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key:     "backup_auto_enabled",
		Scope:   sysconfig.ScopeSystem,
		Type:    sysconfig.TypeBool,
		Default: "true",
		Group:   "backup",
		Label:   "自动备份",
		Description: "每天 03:00 自动创建一次数据库快照，存放于数据目录 backups/ 下",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key:     "backup_keep_count",
		Scope:   sysconfig.ScopeSystem,
		Type:    sysconfig.TypeInt,
		Default: "7",
		Group:   "backup",
		Label:   "备份保留数量",
		Description: "自动/手动备份超出该数量时自动清理最旧的备份",
	})
}
