// 本地上传备份：把用户从本机选中的 .db 备份文件存进备份目录，之后走与服务器
// 上的备份完全相同的「恢复」流程（框架 backup 的恢复接口 + 恢复前快照）。
//
// 为什么要做这个：备份原本只能「服务器里恢复」，用户把备份下载到本机后，换机 /
// 异地容灾就传不回来——等于恢复只做了一半（家族规范见 .agents/skills/
// backup-restore/SKILL.md，参考实现 contract）。
//
// 为什么放在实例层：框架包 server/backup 必须与 smallgo 基线保持一致
// （scripts/check-sync.sh 会 diff），不能在那里加路由。这里经业务 App 的
// SetupAdmin 挂到同一个 adminGroup，与框架 /api/backups 同一层鉴权
// （已登录 + 管理员），无需重复实现权限。
//
// 为什么必须「先临时后转正」两步：备份目录里的**文件名**就是框架列表 / 下载 /
// 恢复的唯一入口，框架只放行 backup_YYYYMMDD_HHMMSS.db 这类名字。若边收边写
// 正式名，一个传输中断的半截文件会立刻出现在备份列表里，管理员一点「恢复」
// 就把主库换成了损坏文件——正好是用户最怕的「辛苦录入的数据干没有了」。
// 所以先落一个**不匹配**备份名正则的临时名（.upload-*.tmp），全链校验通过才
// rename 成正式名；任一环节失败都由 defer 删掉临时文件，目录里不留残余，
// 半截文件既进不了列表，也永远不可能被恢复。
//
// 校验链（缺一不可，任一失败 400 且不留残余）：
//  1. 请求体大小上限 http.MaxBytesReader 兜底——防把接口当网盘拖爆磁盘/内存；
//  2. 只收 .db 后缀——第一道粗筛，能绕过，只为挡掉明显的误选；
//  3. SQLite 文件头 "SQLite format 3\x00"——挡掉改了名的垃圾文件；
//  4. PRAGMA integrity_check——挡掉截断/损坏的库（上传中断最常见的形态）；
//  5. 必须含 users 表——挡掉任意别的 SQLite 库。本应用的库必有 users 表，
//     别的库恢复进来只会得到一个空壳应用。
//
// 租房管理的恢复走框架的文件级替换（换主库文件后进程退出等守护重启），恢复接口
// 本身不校验文件内容——所以上传时这条链是上传文件进入「可恢复集合」之前的
// **唯一**闸门，不能省。
//
// 原文件名只进审计日志、绝不参与拼路径：multipart 的 filename 完全由客户端
// 控制，拿它落盘就是路径穿越面。正式名一律由服务端生成。
package rental

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"smallgo/server/audit"
	"smallgo/server/backup"
	"smallgo/server/config"
	"smallgo/server/logger"
	"smallgo/server/response"
)

// maxUploadBackupBytes 单个上传备份的大小上限（256MB）。正常备份远小于此，
// 上限只作兜底防御：把接口当网盘用，或一个异常巨大的文件把磁盘塞满。
const maxUploadBackupBytes = 256 << 20

// SQLite 主库文件的魔数头，共 16 字节，见 https://www.sqlite.org/fileformat.html
const sqliteHeader = "SQLite format 3\x00"

// setupBackupUploadRoutes 挂本地上传备份接口。由 apps.App.SetupAdmin 调用，
// 落到 adminGroup → 只有管理员能调。
func setupBackupUploadRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	admin.POST("/rental/backups/upload", handleUploadBackup(db))
}

// handleUploadBackup 接收本机上传的备份文件，校验通过后存为一份备份。
//
// 严格两步：上传本身不改动当前数据，真正恢复仍走列表行的「恢复」按钮
// （带确认弹窗与恢复前快照）。这样用户在上传后还能核对文件，两条入口复用
// 同一条恢复链路。
func handleUploadBackup(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 请求体上限。放在最前面：超限时 multipart 解析直接失败，后面
		// 不会白读一遍磁盘。
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBackupBytes)

		fileHeader, err := c.FormFile("file")
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				response.ErrorBadRequest(c, "备份文件过大，上限 256MB")
				return
			}
			response.ErrorBadRequest(c, "请选择要上传的备份文件（.db）")
			return
		}
		if fileHeader.Size > maxUploadBackupBytes {
			response.ErrorBadRequest(c, "备份文件过大，上限 256MB")
			return
		}

		// 2. 只收 .db 后缀（粗筛，可绕过；真正的判据是下面的校验链）。
		if ext := strings.ToLower(filepath.Ext(fileHeader.Filename)); ext != ".db" {
			response.ErrorBadRequest(c, "只支持 .db 备份文件")
			return
		}

		dir := backup.Dir(config.C.DataDir)
		// 3. 落临时名。CreateTemp 保证名字唯一，且 .upload-*.tmp 不匹配框架
		// 的备份名正则，即便进程在这里被杀也不会留下一份「看起来能恢复」的
		// 半截文件。
		tmp, err := os.CreateTemp(dir, ".upload-*.tmp")
		if err != nil {
			logger.Error("创建上传临时文件失败: %v", err)
			response.ErrorInternal(c, "保存上传文件失败")
			return
		}
		tmpName := tmp.Name()
		// 成功改名后临时路径已不存在，这一句退化成空操作；失败路径上它是
		// 「不留残余文件」的唯一保证。
		defer func() {
			if err := os.Remove(tmpName); err != nil && !os.IsNotExist(err) {
				logger.Error("清理上传临时文件失败 %s: %v", tmpName, err)
			}
		}()

		src, err := fileHeader.Open()
		if err != nil {
			tmp.Close()
			response.ErrorBadRequest(c, "读取上传文件失败")
			return
		}
		_, copyErr := io.Copy(tmp, src)
		src.Close()
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil {
			logger.Error("写入上传文件失败: %v %v", copyErr, closeErr)
			response.ErrorInternal(c, "保存上传文件失败")
			return
		}

		// 4. 校验链全过才转正。
		if err := verifyUploadedBackup(tmpName); err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}

		name, err := uniqueBackupName(dir)
		if err != nil {
			response.ErrorInternal(c, err.Error())
			return
		}
		if err := os.Rename(tmpName, filepath.Join(dir, name)); err != nil {
			logger.Error("上传备份转正失败: %v", err)
			response.ErrorInternal(c, "保存备份失败")
			return
		}

		// 上传的备份名字时间戳最新，不参与本次保留数清理（Prune 只删最旧的），
		// 天然不会被下一次自动备份连带清掉。
		logger.Info("上传备份 %s（原文件名 %s，%d 字节）", name, fileHeader.Filename, fileHeader.Size)
		audit.Log(db, c, "backup_upload", "backup", 0,
			fmt.Sprintf("%s（原文件 %s）", name, fileHeader.Filename))
		response.Success(c, gin.H{"name": name})
	}
}

// verifyUploadedBackup 校验上传的候选文件是不是一份可用的本应用备份。
// 三步缺一不可：SQLite 文件头 → PRAGMA integrity_check → 必须含 users 表。
// 错误文案直接面向管理员，要能据此判断是「选错文件」还是「文件损坏」。
func verifyUploadedBackup(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("读取上传文件失败：%v", err)
	}
	head := make([]byte, len(sqliteHeader))
	_, readErr := io.ReadFull(f, head)
	f.Close()
	if readErr != nil {
		// 文件比 16 字节还短：不可能是 SQLite 库。
		return fmt.Errorf("文件不是有效的数据库备份（文件过小，可能上传中断）")
	}
	if string(head) != sqliteHeader {
		return fmt.Errorf("文件不是有效的数据库备份（不是 SQLite 数据库文件）")
	}

	probe, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		return fmt.Errorf("打开上传文件失败，可能不是有效的数据库备份：%v", err)
	}
	defer func() {
		if sqlDB, e := probe.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}()
	sqlDB, err := probe.DB()
	if err != nil {
		return fmt.Errorf("读取上传文件失败：%v", err)
	}

	// integrity_check：挡掉截断 / 页损坏的库。上传中断、磁盘满写出来的文件
	// 头仍是合法 SQLite 魔数，只有这一关能认出来。
	rows, err := sqlDB.Query("PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("数据库备份校验失败：%v", err)
	}
	problems := make([]string, 0, 1)
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return fmt.Errorf("数据库备份校验失败：%v", err)
		}
		problems = append(problems, s)
	}
	rows.Close()
	if len(problems) == 0 || problems[0] != "ok" {
		return fmt.Errorf("备份文件已损坏，完整性检查未通过：%s", strings.Join(problems, "; "))
	}

	var n int
	if err := sqlDB.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='users'`,
	).Scan(&n); err != nil {
		return fmt.Errorf("检查备份表结构失败：%v", err)
	}
	if n == 0 {
		return fmt.Errorf("备份中缺少 users 表，可能不是本应用导出的备份")
	}
	return nil
}

// uniqueBackupName 生成正式备份名 backup_YYYYMMDD_HHMMSS.db，同秒冲突追加
// -1/-2 序号，与框架 backup.Create 的规则一致——这样产出的名字天然能通过
// 列表 / 下载 / 恢复的文件名校验，不需要额外白名单，也天然没有路径穿越面。
func uniqueBackupName(dir string) (string, error) {
	base := "backup_" + time.Now().Format("20060102_150405")
	name := base + ".db"
	// 99 次同秒冲突即视为异常（同框架 backup.Create 的上限）。
	for i := 1; i <= 99; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			return name, nil
		}
		name = fmt.Sprintf("%s-%d.db", base, i)
	}
	return "", fmt.Errorf("备份文件名冲突过多，请稍后重试")
}
