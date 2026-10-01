// Package notify — 缴费通知渠道层：钉钉群机器人、电子邮件、手机短信、QQ 机器人。
//
// 设计参照提醒事项（reminder）应用的通知体系并做了简化：
//   - 渠道按登录用户绑定（加密存储目标地址），全部房间共用，不做每房渠道路由；
//   - 没有投递队列与重试：每日定时任务对每位用户每渠道至多发一条缴费提醒
//     汇总，失败记录到绑定行，次日自然重试；
//   - 没有站内通知中心：站内呈现由总览页「缴费提醒」卡片与导航角标承担。
package notify

import (
	"time"

	"smallgo/server/database"
)

func init() {
	// 通知层的四张表随宿主应用一起 AutoMigrate，无需独立迁移项。
	database.RegisterModels(&ChannelBinding{}, &ProviderConfig{}, &QQBindCode{}, &NotifyLog{})
}

// 通知渠道标识。dingtalk 为钉钉群自定义机器人（用户粘贴 webhook，无需服务商配置）；
// email 走用户配置的 SMTP；sms 走用户自备的短信网关 webhook；qq 为 QQ 官方机器人 API。
const (
	ChannelDingTalk = "dingtalk"
	ChannelEmail    = "email"
	ChannelSMS      = "sms"
	ChannelQQ       = "qq"
)

// supportedChannels 是支持绑定与发送的渠道全集。
var supportedChannels = map[string]bool{
	ChannelDingTalk: true,
	ChannelEmail:    true,
	ChannelSMS:      true,
	ChannelQQ:       true,
}

// SupportedChannels 返回全部支持渠道的稳定列表。
func SupportedChannels() []string {
	return []string{ChannelDingTalk, ChannelEmail, ChannelSMS, ChannelQQ}
}

// ChannelLabel 渠道中文名。
func ChannelLabel(channel string) string {
	switch channel {
	case ChannelEmail:
		return "电子邮件"
	case ChannelSMS:
		return "手机短信"
	case ChannelQQ:
		return "QQ 机器人"
	case ChannelDingTalk:
		return "钉钉群机器人"
	default:
		return channel
	}
}

// ChannelBinding 一条渠道绑定：Target 是加密后的收信目标（webhook/邮箱/手机号/OpenID），
// 明文永不落库、永不回传前端。email 允许多条绑定（多个收件邮箱），其余渠道单绑定。
type ChannelBinding struct {
	ID            uint       `gorm:"primarykey" json:"id"`
	UserID        uint       `gorm:"not null;index:idx_notify_binding_user_channel" json:"-"`
	Channel       string     `gorm:"size:16;not null;index:idx_notify_binding_user_channel" json:"channel"`
	Target        string     `gorm:"type:text;not null" json:"-"`
	TargetMasked  string     `gorm:"size:120;not null" json:"target_masked"`
	Status        string     `gorm:"size:16;not null;default:active;index" json:"status"` // active / disabled
	VerifiedAt    *time.Time `json:"verified_at"`
	LastTestedAt  *time.Time `json:"last_tested_at"`
	LastErrorCode string     `gorm:"size:160" json:"last_error_code,omitempty"`
	LastErrorAt   *time.Time `json:"last_error_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// ProviderConfig 服务商凭据（SMTP 账号、短信网关、QQ 机器人应用），整包加密存储。
type ProviderConfig struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Provider  string    `gorm:"size:32;not null;uniqueIndex" json:"provider"`
	Secret    string    `gorm:"type:text;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// QQBindCode 把登录用户与一次 QQ 机器人私聊关联起来：用户在应用里取码，
// 给机器人发 "/绑定 <码>"，网关收到后写入绑定。无需用户去找平台 OpenID。
type QQBindCode struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"-"`
	Code      string    `gorm:"size:12;not null;uniqueIndex" json:"code"`
	ExpiresAt time.Time `gorm:"not null;index" json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// NotifyLog 每日推送去重：(user, channel, send_date) 唯一，
// 每渠道每天至多发一条缴费提醒，重复插入即视为已发过。
type NotifyLog struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"not null;uniqueIndex:idx_notify_log_dedup" json:"-"`
	Channel   string    `gorm:"size:16;not null;uniqueIndex:idx_notify_log_dedup" json:"channel"`
	SendDate  string    `gorm:"size:10;not null;uniqueIndex:idx_notify_log_dedup" json:"send_date"` // YYYY-MM-DD
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Message 一条待发送的缴费提醒：Title 用作邮件主题/摘要标题，Body 是逐行明细。
type Message struct {
	Title string
	Body  string
}

// ChannelStatus 渠道状态出参：给设置页渲染卡片用。
type ChannelStatus struct {
	Channel      string               `json:"channel"`
	Label        string               `json:"label"`
	Configured   bool                 `json:"configured"`
	Bound        bool                 `json:"bound"`
	Status       string               `json:"status"`
	TargetMasked string               `json:"target_masked"`
	Description  string               `json:"description"`
	BotLink      string               `json:"bot_link,omitempty"`
	Bindings     []ChannelBindingItem `json:"bindings,omitempty"`
}

// ChannelBindingItem 多绑定渠道（email）逐条展示的绑定摘要。
type ChannelBindingItem struct {
	ID           uint   `json:"id"`
	TargetMasked string `json:"target_masked"`
	Status       string `json:"status"`
}
