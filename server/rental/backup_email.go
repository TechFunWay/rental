// backup_email.go — 把一份数据库备份作为邮件附件发到管理员自己的邮箱
// （Gitee issue IKW41H 需求 8：「关于备份，备份可以通过给自己发送邮件完成
// 资料备份，以防丢失」）。
//
// 为什么放在实例层：框架 backup 包必须与 smallgo 基线保持一致（scripts/
// check-sync.sh 会逐文件 diff），本地上传备份当初也是因此放在实例层（见
// backup_upload.go）。这里同样只**新增**一个 adminGroup 路由，不改框架。
//
// 发送复用通知渠道层已配置的 SMTP 与邮箱绑定：房东在通知设置里填好 SMTP、
// 绑定收件邮箱后，备份页每份备份旁点「发到邮箱」即可。服务器侧不留档——
// 邮件本身就是异地副本，多存一份反而多一份泄露面。
package rental

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"smallgo/server/audit"
	"smallgo/server/backup"
	"smallgo/server/config"
	"smallgo/server/logger"
	"smallgo/server/notify"
	"smallgo/server/response"
)

// setupBackupEmailRoutes 挂「备份发邮件」接口。由 apps.App.SetupAdmin 调用，
// 落到 adminGroup → 只有管理员能调，与下载/恢复/删除同一层鉴权。
func setupBackupEmailRoutes(admin *gin.RouterGroup, db *gorm.DB) {
	admin.POST("/rental/backups/:name/email", handleEmailBackup(db))
}

// handleEmailBackup 把指定备份作为附件发到当前管理员已绑定的邮箱。
//
// 只读不写：发邮件不改动备份文件，也不改数据，失败了重试即可，天然幂等
// （最多多收一封邮件）。附件大小上限由 notify.MaxEmailAttachmentBytes 兜底，
// 这里先按列表里的大小早退，避免白读一遍几十 MB 的文件。
func handleEmailBackup(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		name := c.Param("name")

		// 文件名不自己写正则：直接拿框架的备份列表比对，命中即合法。
		// 既不用复制 namePattern（框架改了这里不会漂），也天然挡掉 ../ 这类
		// 路径穿越——列表里不可能出现那种名字。
		items, err := backup.List(config.C.DataDir)
		if err != nil {
			logger.Error("读取备份列表失败: %v", err)
			response.ErrorInternal(c, "读取备份列表失败")
			return
		}
		var size int64
		found := false
		for i := range items {
			if items[i].Name == name {
				found, size = true, items[i].Size
				break
			}
		}
		if !found {
			response.ErrorNotFound(c, "备份不存在")
			return
		}
		if size > notify.MaxEmailAttachmentBytes {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"备份 %dMB 超出邮箱附件上限（%dMB），请改用「下载」留存",
				size>>20, notify.MaxEmailAttachmentBytes>>20))
			return
		}

		path := filepath.Join(backup.Dir(config.C.DataDir), name)
		content, err := os.ReadFile(path)
		if err != nil {
			logger.Error("读取备份文件失败 %s: %v", name, err)
			response.ErrorInternal(c, "读取备份文件失败")
			return
		}

		subject := "租房管理数据库备份 " + name
		body := fmt.Sprintf(
			"这是租房管理的一份数据库备份，已作为附件附在本邮件中。\n\n"+
				"备份文件：%s\n文件大小：%d KB\n发送时间：%s\n\n"+
				"恢复方式：登录后进入「系统维护 → 备份」，点「上传备份」选择这个 .db 文件，"+
				"再在列表里点「恢复」。\n",
			name, len(content)>>10, time.Now().Format("2006-01-02 15:04:05"))

		if err := notify.SendEmailAttachment(db, currentUserID(c), subject, body, name, content); err != nil {
			// err 是渠道层的分类错误（未配 SMTP / 未绑邮箱 / 发送失败），
			// 文案已经是给用户看的中文说明，直接回传比再包一层更有用。
			logger.Error("备份邮件发送失败 %s: %v", name, err)
			response.ErrorBadRequest(c, err.Error())
			return
		}
		audit.Log(db, c, "backup_email", "backup", 0, name)
		response.Success(c, gin.H{"ok": true})
	}
}
