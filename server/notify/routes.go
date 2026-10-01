// routes.go — 缴费通知渠道的设置页接口，全部挂在登录组下、按登录用户隔离。
package notify

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func currentUserID(c *gin.Context) uint { return c.GetUint("userID") }

// SetupRoutes 挂载渠道设置接口（宿主应用在登录组上调用）。
func SetupRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.GET("/rental/notify/channels", handleChannelStatuses(db))
	api.PUT("/rental/notify/bind/:channel", handleBindChannel(db))
	api.DELETE("/rental/notify/bind/:channel", handleUnbindChannel(db))
	api.DELETE("/rental/notify/bind/:channel/:id", handleDeleteChannelBinding(db))
	api.POST("/rental/notify/toggle/:channel", handleToggleChannel(db))
	api.POST("/rental/notify/test/:channel", handleTestChannel(db))
	api.GET("/rental/notify/provider/:provider", handleProviderStatus(db))
	api.POST("/rental/notify/provider/:provider", handleSaveProvider(db))
	api.POST("/rental/notify/qq/bindcode", handleCreateQQBindCode(db))
}

func handleChannelStatuses(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		response.Success(c, channelStatuses(db, currentUserID(c)))
	}
}

// handleBindChannel 绑定渠道目标。email 可绑多个收件邮箱（重复拒绝），
// 其余渠道保持单绑定替换语义。
func handleBindChannel(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.ToLower(c.Param("channel"))
		if !supportedChannels[channel] {
			response.ErrorBadRequest(c, "该渠道不支持绑定")
			return
		}
		var in struct {
			Target string `json:"target"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			response.ErrorBadRequest(c, "绑定参数无效")
			return
		}
		target, err := normalizeTarget(channel, in.Target)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		if channel == ChannelEmail {
			duplicate, err := emailTargetExists(db, currentUserID(c), target)
			if err != nil {
				response.ErrorInternal(c, "读取已有绑定失败")
				return
			}
			if duplicate {
				response.ErrorBadRequest(c, "该邮箱已绑定过")
				return
			}
		}
		encrypted, err := encryptTarget(db, target)
		if err != nil {
			response.ErrorInternal(c, "加密绑定信息失败")
			return
		}
		now := time.Now()
		binding := ChannelBinding{
			UserID: currentUserID(c), Channel: channel, Target: encrypted,
			TargetMasked: maskTarget(channel, target), Status: "active", VerifiedAt: &now,
		}
		var saveErr error
		if channel == ChannelEmail {
			saveErr = db.Create(&binding).Error
		} else {
			saveErr = db.Where("user_id = ? AND channel = ?", binding.UserID, channel).
				Assign(map[string]interface{}{
					"target": encrypted, "target_masked": binding.TargetMasked,
					"status": "active", "verified_at": &now, "last_error_code": "",
				}).FirstOrCreate(&binding).Error
		}
		if saveErr != nil {
			response.ErrorInternal(c, "保存绑定失败")
			return
		}
		response.Success(c, gin.H{"bound": true, "target_masked": binding.TargetMasked})
	}
}

// emailTargetExists 加密后的密文带随机性，无法在 SQL 里去重，
// 只能解密比对明文。
func emailTargetExists(db *gorm.DB, userID uint, target string) (bool, error) {
	var bindings []ChannelBinding
	if err := db.Where("user_id = ? AND channel = ?", userID, ChannelEmail).Find(&bindings).Error; err != nil {
		return false, err
	}
	for _, binding := range bindings {
		plain, err := decryptTarget(db, binding.Target)
		if err != nil {
			continue
		}
		if strings.EqualFold(plain, target) {
			return true, nil
		}
	}
	return false, nil
}

// handleDeleteChannelBinding 删除一条绑定（email 逐个删收件邮箱用）。
func handleDeleteChannelBinding(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.ToLower(c.Param("channel"))
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil || !supportedChannels[channel] {
			response.ErrorBadRequest(c, "绑定记录无效")
			return
		}
		result := db.Where("id = ? AND user_id = ? AND channel = ?", id, currentUserID(c), channel).Delete(&ChannelBinding{})
		if result.Error != nil {
			response.ErrorInternal(c, "删除绑定失败")
			return
		}
		if result.RowsAffected == 0 {
			response.ErrorBadRequest(c, "绑定记录不存在")
			return
		}
		response.Success(c, gin.H{"deleted": true})
	}
}

func handleToggleChannel(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.ToLower(c.Param("channel"))
		var in struct {
			Enabled bool `json:"enabled"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			response.ErrorBadRequest(c, "渠道参数无效")
			return
		}
		status := "disabled"
		if in.Enabled {
			status = "active"
		}
		result := db.Model(&ChannelBinding{}).Where("user_id = ? AND channel = ?", currentUserID(c), channel).Update("status", status)
		if result.Error != nil {
			response.ErrorInternal(c, "更新渠道失败")
			return
		}
		if result.RowsAffected == 0 {
			response.ErrorBadRequest(c, "请先绑定该渠道")
			return
		}
		response.Success(c, gin.H{"status": status})
	}
}

func handleUnbindChannel(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.ToLower(c.Param("channel"))
		if !supportedChannels[channel] {
			response.ErrorBadRequest(c, "不支持的通知渠道")
			return
		}
		if err := db.Where("user_id = ? AND channel = ?", currentUserID(c), channel).Delete(&ChannelBinding{}).Error; err != nil {
			response.ErrorInternal(c, "解绑失败")
			return
		}
		response.Success(c, gin.H{"deleted": true})
	}
}

// handleTestChannel 发一条固定测试消息，验证渠道链路。
func handleTestChannel(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		channel := strings.ToLower(c.Param("channel"))
		if !supportedChannels[channel] {
			response.ErrorBadRequest(c, "不支持的通知渠道")
			return
		}
		userID := currentUserID(c)
		msg := Message{Title: "这是一条测试提醒", Body: "渠道已连接成功，缴费提醒将发送到这里。"}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		if _, err := sendChannel(ctx, db, channel, userID, msg, "test-"+strconv.FormatInt(time.Now().UnixNano(), 10), nil); err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		now := time.Now()
		_ = db.Model(&ChannelBinding{}).Where("user_id = ? AND channel = ?", userID, channel).Updates(map[string]interface{}{
			"last_tested_at": &now, "last_error_code": "",
		}).Error
		response.Success(c, gin.H{"sent": true})
	}
}

func handleProviderStatus(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		provider := strings.ToLower(c.Param("provider"))
		switch provider {
		case ChannelEmail:
			// 回显非敏感字段便于修改；密码永不回传，留空表示沿用旧值。
			values, _ := providerSettings(db, provider)
			response.Success(c, gin.H{
				"configured":   emailConfigured(db),
				"host":         values["host"],
				"port":         values["port"],
				"from_address": values["from_address"],
				"from_name":    values["from_name"],
				"username":     values["username"],
			})
		case ChannelSMS:
			values, _ := providerSettings(db, provider)
			response.Success(c, gin.H{
				"configured":  smsConfigured(db),
				"webhook_url": values["webhook_url"],
			})
		case ChannelQQ:
			values, _ := providerSettings(db, ChannelQQ)
			response.Success(c, gin.H{
				"configured": qqConfigured(db),
				"app_id":     values["app_id"],
				"bot_link":   values["bot_link"],
			})
		default:
			response.ErrorBadRequest(c, "不支持的通知服务")
		}
	}
}

// handleSaveProvider 保存服务商配置（整包加密落库）。email 的密码留空
// 表示沿用旧密码，避免每次保存都要重填。QQ 的 app_id / bot_link 属于
// 非敏感字段，读回展示便于核对；密钥类字段永不回传。
func handleSaveProvider(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		provider := strings.ToLower(c.Param("provider"))
		if provider != ChannelEmail && provider != ChannelSMS && provider != ChannelQQ {
			response.ErrorBadRequest(c, "不支持的通知服务")
			return
		}
		var values map[string]string
		if err := c.ShouldBindJSON(&values); err != nil {
			response.ErrorBadRequest(c, "配置参数无效")
			return
		}
		for k, v := range values {
			values[k] = strings.TrimSpace(v)
		}
		switch provider {
		case ChannelEmail:
			host := strings.TrimSpace(strings.ToLower(values["host"]))
			if !strings.Contains(host, ".") || strings.ContainsAny(host, " /:@") || values["from_address"] == "" {
				response.ErrorBadRequest(c, "SMTP 服务器应填写完整域名，例如 smtp.qq.com，并填写发件邮箱")
				return
			}
			if values["password"] == "" {
				if old, err := providerSettings(db, provider); err == nil {
					values["password"] = old["password"]
				}
			}
		case ChannelSMS:
			if values["webhook_url"] == "" {
				response.ErrorBadRequest(c, "请填写短信网关 Webhook 地址")
				return
			}
			if _, err := url.ParseRequestURI(values["webhook_url"]); err != nil {
				response.ErrorBadRequest(c, "短信网关地址应是有效的 http 或 https 链接")
				return
			}
		case ChannelQQ:
			if values["app_id"] == "" || values["app_secret"] == "" {
				// 只改主页链接时允许不重填密钥：沿用旧值。
				if old, err := providerSettings(db, provider); err == nil {
					if values["app_id"] == "" {
						values["app_id"] = old["app_id"]
					}
					if values["app_secret"] == "" {
						values["app_secret"] = old["app_secret"]
					}
				}
			}
			if values["app_id"] == "" || values["app_secret"] == "" {
				response.ErrorBadRequest(c, "请填写 App ID 和 App Secret")
				return
			}
			if link := values["bot_link"]; link != "" {
				parsed, err := url.ParseRequestURI(link)
				if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
					response.ErrorBadRequest(c, "机器人主页应填写有效的 http 或 https 链接")
					return
				}
			}
		}
		if err := saveProviderSettings(db, provider, values); err != nil {
			response.ErrorInternal(c, "保存通知服务配置失败")
			return
		}
		response.Success(c, gin.H{"configured": true})
	}
}

// handleCreateQQBindCode 生成 10 分钟有效的 QQ 绑定码：用户把它发给机器人
// （"/绑定 码"），网关匹配后完成绑定。同一用户旧码作废。
func handleCreateQQBindCode(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !qqConfigured(db) {
			response.ErrorBadRequest(c, "请先填写 QQ 机器人的 App ID 和 App Secret")
			return
		}
		_ = db.Where("user_id = ?", currentUserID(c)).Delete(&QQBindCode{}).Error
		code, err := newQQBindCode()
		if err != nil {
			response.ErrorInternal(c, "生成绑定码失败")
			return
		}
		row := QQBindCode{UserID: currentUserID(c), Code: code, ExpiresAt: time.Now().Add(10 * time.Minute)}
		if err := db.Create(&row).Error; err != nil {
			response.ErrorInternal(c, "保存绑定码失败")
			return
		}
		response.Success(c, gin.H{"code": code, "expires_at": row.ExpiresAt})
	}
}
