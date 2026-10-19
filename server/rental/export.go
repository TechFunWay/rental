// export.go — 定时导出存档：给用户**主动下载留存**用的一组独立快照。
//
// 为什么与框架备份彻底分家（这是本文件最要紧的一条约束）：
//
// 框架 server/backup 的每日 03:00 自动备份按 **数量** 清理最旧的文件
// （Prune + backup_keep_count）。导出存档如果混在 backups/ 目录里、复用同一套
// 清理，就会被每日自动备份的保留数**连带删掉**——那正好违背「方便用户留存」的
// 本意：用户还没来得及下载的存档就被系统自己清了。因此：
//
//	框架备份   {data-dir}/backups/  backup_YYYYMMDD_HHMMSS.db   保留数 backup_keep_count
//	导出存档   {data-dir}/exports/  export_YYYYMMDD_HHMMSS.db   保留数 rental_export_keep_count
//
// 两套目录、两套命名、两套保留数，互不影响；配置也用独立的 rental_export_* 前缀，
// 不复用 backup_*，避免与框架配置语义混淆。
//
// 为什么放在实例层：框架包 server/backup 必须与 smallgo 基线保持一致
// （scripts/check-sync.sh 会 diff 报漂移），不能在那里加东西。这里自己实现
// 导出，快照同样用 SQLite 的 VACUUM INTO 生成——WAL 模式下也安全，且是
// 一致性快照（框架 backup.Create 是同一手法）。
//
// 数据安全口径：
//  1. 存档只增不覆盖，同秒内重复导出追加 -1/-2 序号，不会覆盖已有文件；
//  2. 保留数清理只删最旧的，且永远不会删到框架备份（目录不同）；
//  3. 定时任务失败只记日志、不 panic，也不影响主服务；
//  4. 文件名严格白名单校验后才参与拼路径，杜绝路径穿越。
package rental

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"smallgo/server/apps"
	"smallgo/server/audit"
	"smallgo/server/config"
	"smallgo/server/logger"
	"smallgo/server/response"
	"smallgo/server/scheduler"
	"smallgo/server/sysconfig"
)

// exportDirName 导出存档目录名，与框架备份的 backups/ 刻意不同名。
const exportDirName = "exports"

// exportNamePattern 导出存档的文件名白名单：export_YYYYMMDD_HHMMSS.db，
// 同秒重复导出时追加 -N 序号（见 CreateExport）。
//
// 这是**唯一**允许参与拼路径的形态：下载、删除两个接口都先过这道正则，
// `../../etc/passwd`、`export_xxx.db`、超长名等一律 400，绝不落到磁盘上。
var exportNamePattern = regexp.MustCompile(`^export_\d{8}_\d{6}(-\d+)?\.db$`)

// 导出配置的合法范围。注册进 sysconfig 的 Description 与前端提示都按这里写。
const (
	exportIntervalMin     = 1
	exportIntervalMax     = 720
	exportIntervalDefault = 24
	exportKeepMin         = 1
	exportKeepMax         = 500
	exportKeepDefault     = 30
)

// exportCheckInterval 定时任务的**唤醒**频率：每小时醒一次看是否到点。
// 真正的导出频率由 rental_export_interval_hours 决定，两者解耦——
// 这样用户把间隔从 24 小时改成 6 小时，最多 1 小时内就生效，不用重启。
const exportCheckInterval = time.Hour

var (
	// exportDB 由本文件注册的 App.Migrate / SetupAdmin 注入，供定时任务使用。
	exportDB *gorm.DB

	// exportRunning 是防重叠标志：VACUUM INTO 在大库上可能要跑一会儿，
	// 上一次没结束就不该再触发一次（定时与手动两条入口共用）。
	exportRunning int32

	// exportDirOverride 供测试注入临时目录；为空时按部署配置解析。
	exportDirOverride string
)

// exportsDir 返回导出存档目录并确保存在（数据目录不存在就一并创建）。
func exportsDir() string {
	base := exportDirOverride
	if base == "" {
		root := config.C.DataDir
		if strings.TrimSpace(root) == "" {
			root = "./data"
		}
		base = filepath.Join(root, exportDirName)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		logger.Error("创建导出存档目录失败 %s: %v", base, err)
	}
	return base
}

// ExportItem 是导出存档的列表视图（与框架备份的 Item 同形，前端可复用渲染）。
type ExportItem struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateExport 生成一份一致性数据库快照，返回文件名。
//
// 与框架 backup.Create 同样的两点保障：
//   - 时间戳精确到秒，同秒内再次导出自动追加 -1/-2 序号，**绝不覆盖**已有存档；
//   - 名字由服务端生成，调用方无法影响。
func CreateExport(db *gorm.DB) (string, error) {
	if db == nil {
		return "", fmt.Errorf("数据库未就绪")
	}
	dir := exportsDir()
	base := "export_" + time.Now().Format("20060102_150405")
	name := base + ".db"
	for i := 1; i <= 99; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s-%d.db", base, i)
	}
	target := filepath.Join(dir, name)
	if err := db.Exec("VACUUM INTO ?", target).Error; err != nil {
		return "", fmt.Errorf("导出存档失败：%w", err)
	}
	return name, nil
}

// ListExports 返回全部导出存档，新的在前（按文件名倒序——名字里带时间戳，
// 字典序即时间序，同秒的 -N 序号也天然排在同一档内）。
func ListExports() ([]ExportItem, error) {
	entries, err := os.ReadDir(exportsDir())
	if err != nil {
		return nil, err
	}
	items := make([]ExportItem, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !exportNamePattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, ExportItem{
			Name:      e.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime(),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name > items[j].Name })
	return items, nil
}

// PruneExports 按保留数量清理最旧的导出存档，返回删除的文件数。
//
// 只在 exports/ 目录内操作，**不会**碰框架的 backups/；keep 小于 1 时按 1 兜底，
// 保证任何配置下都至少留一份（用户全删光就失去留存的意义了）。
func PruneExports(keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	items, err := ListExports()
	if err != nil {
		return 0, err
	}
	dir := exportsDir()
	removed := 0
	for i := keep; i < len(items); i++ {
		if err := os.Remove(filepath.Join(dir, items[i].Name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// lastExportAt 返回最近一次导出的时间。目录里一份都没有时返回零值与 false
// （调用方据此判断「从没导过」→ 立刻补一份，不等间隔）。
//
// 用**磁盘上的存档**而不是内存里的时间戳来推算，这样进程重启后间隔判断依旧
// 正确，不会每次重启都补导一份。
func lastExportAt() (time.Time, bool) {
	items, err := ListExports()
	if err != nil || len(items) == 0 {
		return time.Time{}, false
	}
	return items[0].CreatedAt, true
}

// exportEnabled 读取定时导出总开关，默认开启（用户明确要求「一定要保证数据安全」）。
func exportEnabled(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	v, err := sysconfig.GetConfig(db, "rental_export_enabled", 0)
	if err != nil || v == "" {
		return true
	}
	return v != "false"
}

// clampExportInterval 把导出间隔夹到 [1, 720] 小时。
// 非法（非数字）值返回默认 24，超界值夹到最近的边界。
func clampExportInterval(n int) int {
	if n < exportIntervalMin {
		return exportIntervalMin
	}
	if n > exportIntervalMax {
		return exportIntervalMax
	}
	return n
}

// clampExportKeep 把保留数量夹到 [1, 500] 份，非法值返回默认 30。
func clampExportKeep(n int) int {
	if n < exportKeepMin {
		return exportKeepMin
	}
	if n > exportKeepMax {
		return exportKeepMax
	}
	return n
}

// intConfig 读取一个整数配置；缺失、非数字时返回 def。
func intConfig(db *gorm.DB, key string, def int) int {
	if db == nil {
		return def
	}
	v, err := sysconfig.GetConfig(db, key, 0)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		logger.Warn("配置 %s 的值 %q 不是整数，回退默认值 %d", key, v, def)
		return def
	}
	return n
}

// exportIntervalHours 导出间隔（小时），已夹到合法范围。
func exportIntervalHours(db *gorm.DB) int {
	return clampExportInterval(intConfig(db, "rental_export_interval_hours", exportIntervalDefault))
}

// exportKeepCount 存档保留数量，已夹到合法范围。
func exportKeepCount(db *gorm.DB) int {
	return clampExportKeep(intConfig(db, "rental_export_keep_count", exportKeepDefault))
}

// tryStartExport 抢占导出执行权。上一次未结束时返回 false（防重叠）。
func tryStartExport() bool {
	return atomic.CompareAndSwapInt32(&exportRunning, 0, 1)
}

func finishExport() { atomic.StoreInt32(&exportRunning, 0) }

// runExportNow 导出一份并按保留数清理，返回存档名。
//
// 防重入由调用方（定时任务 / 手动接口）用 tryStartExport 保证——导出大库可能
// 耗时，并发触发会同时占磁盘与 SQLite 写锁。
func runExportNow(db *gorm.DB) (string, int, error) {
	name, err := CreateExport(db)
	if err != nil {
		return "", 0, err
	}
	removed, err := PruneExports(exportKeepCount(db))
	if err != nil {
		// 清理失败不影响这份存档本身已经生成成功，如实记日志即可。
		logger.Error("导出存档清理失败 %s: %v", name, err)
	}
	return name, removed, nil
}

// runScheduledExport 定时任务的执行体：到点就导一份，没到点直接返回。
//
// 判定「距上次导出是否已达间隔」基于磁盘上最新的那份存档（见 lastExportAt），
// 而不是内存计数——重启后判断依旧正确。
func runScheduledExport() {
	db := exportDB
	if db == nil {
		return
	}
	if !exportEnabled(db) {
		return
	}
	if last, ok := lastExportAt(); ok {
		interval := time.Duration(exportIntervalHours(db)) * time.Hour
		if time.Since(last) < interval {
			return
		}
	}
	if !tryStartExport() {
		logger.Info("定时导出存档：上一次导出尚未结束，本次跳过")
		return
	}
	defer finishExport()

	name, removed, err := runExportNow(db)
	if err != nil {
		// 失败只记日志：定时任务失败绝不能拖垮主服务，也不能 panic。
		logger.Error("定时导出存档失败: %v", err)
		return
	}
	if removed > 0 {
		logger.Info("定时导出存档 %s 完成，清理了 %d 个过期存档", name, removed)
	} else {
		logger.Info("定时导出存档 %s 完成", name)
	}
}

// setupExportRoutes 挂导出存档接口。由本文件注册的 App.SetupAdmin 调用，
// 落到 adminGroup → 与框架 /api/backups 同一层鉴权（已登录 + 管理员）。
func setupExportRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	exportDB = db
	admin.GET("/rental/exports", handleListExports(db))
	admin.POST("/rental/exports", handleCreateExport(db))
	admin.GET("/rental/exports/:name/download", handleDownloadExport(db))
	admin.DELETE("/rental/exports/:name", handleDeleteExport(db))
}

// handleListExports 列出存档，并回带当前配置（前端配置区直接用这份数据渲染）。
func handleListExports(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		items, err := ListExports()
		if err != nil {
			logger.Error("读取导出存档列表失败: %v", err)
			response.ErrorInternal(c, "读取导出存档列表失败")
			return
		}
		response.Success(c, gin.H{
			"items":          items,
			"dir":            exportsDir(),
			"enabled":        exportEnabled(db),
			"interval_hours": exportIntervalHours(db),
			"keep_count":     exportKeepCount(db),
		})
	}
}

// handleCreateExport 立即导出一份（「立即导出」按钮）。
func handleCreateExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !tryStartExport() {
			response.ErrorBadRequest(c, "上一次导出还没结束，请稍后再试")
			return
		}
		defer finishExport()

		name, removed, err := runExportNow(db)
		if err != nil {
			logger.Error("手动导出存档失败: %v", err)
			// 面向管理员的中文提示，不把底层 500 原文抛给用户。
			response.ErrorInternal(c, "导出存档失败，请稍后重试；若持续失败请查看运行日志")
			return
		}
		audit.Log(db, c, "export_create", "export", 0,
			fmt.Sprintf("%s（清理过期存档 %d 个）", name, removed))
		logger.Info("手动导出存档 %s 完成", name)
		response.Success(c, gin.H{"name": name, "removed": removed})
	}
}

// handleDownloadExport 下载存档。下载文件名由服务端按存档名生成。
func handleDownloadExport(_ *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		if !exportNamePattern.MatchString(name) {
			response.ErrorBadRequest(c, "无效的存档文件名")
			return
		}
		path := filepath.Join(exportsDir(), name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			response.ErrorNotFound(c, "导出存档不存在")
			return
		}
		// 下载文件名由服务端生成：name 已过白名单正则，只含数字/下划线/短横/点，
		// 不含任何客户端可控的引号、CR/LF，不会污染 Content-Disposition。
		downloadName := "rental_" + name
		c.FileAttachment(path, downloadName)
	}
}

// handleDeleteExport 删除某份存档（严格校验文件名，防路径穿越）。
func handleDeleteExport(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")
		if !exportNamePattern.MatchString(name) {
			response.ErrorBadRequest(c, "无效的存档文件名")
			return
		}
		path := filepath.Join(exportsDir(), name)
		if _, err := os.Stat(path); err != nil {
			response.ErrorNotFound(c, "导出存档不存在")
			return
		}
		if err := os.Remove(path); err != nil {
			logger.Error("删除导出存档失败 %s: %v", name, err)
			response.ErrorInternal(c, "删除导出存档失败")
			return
		}
		audit.Log(db, c, "export_delete", "export", 0, name)
		response.Success(c, gin.H{"ok": true})
	}
}

func init() {
	// 每小时醒一次看是否到点；是否真正导出由 rental_export_* 三个配置决定。
	scheduler.Register(scheduler.Job{
		Name:     "rental_export_archive",
		Interval: exportCheckInterval,
		Run:      runScheduledExport,
	})

	// 独立命名空间，不复用 backup_*：避免与框架备份的配置语义混淆。
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_export_enabled", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeBool,
		Default: "true", Group: "export", Label: "定时导出存档",
		Description: "开启后按设定间隔自动导出一份数据库存档到数据目录 exports/ 下，供下载留存（与服务器内的备份相互独立）",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_export_interval_hours", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeInt,
		Default: "24", Group: "export", Label: "导出间隔（小时）",
		Description: fmt.Sprintf("两次定时导出之间的间隔，%d-%d，默认 %d；系统每小时检查一次是否到点",
			exportIntervalMin, exportIntervalMax, exportIntervalDefault),
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_export_keep_count", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeInt,
		Default: "30", Group: "export", Label: "存档保留数量",
		Description: fmt.Sprintf("导出存档超出该数量时自动清理最旧的，%d-%d，默认 %d；仅对导出存档生效，不影响服务器内的备份",
			exportKeepMin, exportKeepMax, exportKeepDefault),
	})

	// 单独注册一个 App 只为挂 SetupAdmin 路由：这样本功能完全自包含在一个文件里，
	// 不必改动 rental.go 的 init（也就不会与别人的改动撞车）。
	apps.Register(apps.App{
		Name:        "rental_export",
		DisplayName: "定时导出存档",
		Migrate: func(db *gorm.DB) error {
			exportDB = db
			return nil
		},
		SetupAdmin: setupExportRoutes,
	})
}
