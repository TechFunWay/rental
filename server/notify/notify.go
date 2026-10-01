package notify

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smallgo/server/sysconfig"

	"gorm.io/gorm"
)

// deliveryError 区分"下次还会试"的暂时错误与"需要人处理"的永久错误。
// 每日推送不做自动重试，Permanent 只影响前端提示口径。
type deliveryError struct {
	Code      string
	Message   string
	Permanent bool
}

func (e *deliveryError) Error() string { return e.Message }

var cnMobile = regexp.MustCompile(`^1[3-9]\d{9}$`)

// notificationBrand 消息里展示的来源名，统一带出应用名。
func notificationBrand() string { return "租房管理" }

// encryptionKey 绑定目标的加密密钥：优先环境变量，缺省由站点 jwt_secret 派生，
// 密文与站点的登录密钥同源，无需额外配置。
func encryptionKey(db *gorm.DB) ([]byte, error) {
	if raw := os.Getenv("RENTAL_DATA_KEY"); raw != "" {
		sum := sha256.Sum256([]byte(raw))
		return sum[:], nil
	}
	secret, err := sysconfig.GetConfig(db, "jwt_secret", 0)
	if err != nil || secret == "" {
		return nil, errors.New("missing encryption key")
	}
	sum := sha256.Sum256([]byte("rental-notify:" + secret))
	return sum[:], nil
}

func encryptTarget(db *gorm.DB, plain string) (string, error) {
	key, err := encryptionKey(db)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

func decryptTarget(db *gorm.DB, encoded string) (string, error) {
	key, err := encryptionKey(db)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("invalid encrypted target")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// saveProviderSettings 服务商配置整包加密后落库（smtp/短信网关/QQ 应用）。
func saveProviderSettings(db *gorm.DB, provider string, values map[string]string) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	secret, err := encryptTarget(db, string(raw))
	if err != nil {
		return err
	}
	row := ProviderConfig{Provider: provider, Secret: secret}
	return db.Where("provider = ?", provider).Assign(map[string]interface{}{"secret": secret}).FirstOrCreate(&row).Error
}

// providerSettings 读取服务商配置；环境变量作为部署期的后备配置方式。
func providerSettings(db *gorm.DB, provider string) (map[string]string, error) {
	if db != nil {
		var row ProviderConfig
		if err := db.Where("provider = ?", provider).First(&row).Error; err == nil {
			plain, decErr := decryptTarget(db, row.Secret)
			if decErr != nil {
				return nil, decErr
			}
			values := map[string]string{}
			if err := json.Unmarshal([]byte(plain), &values); err == nil {
				return values, nil
			}
		}
	}
	values := map[string]string{}
	switch provider {
	case ChannelEmail:
		values = map[string]string{
			"host": os.Getenv("SMTP_HOST"), "port": os.Getenv("SMTP_PORT"),
			"from_address": os.Getenv("SMTP_FROM_ADDRESS"), "from_name": os.Getenv("SMTP_FROM_NAME"),
			"username": os.Getenv("SMTP_USERNAME"), "password": os.Getenv("SMTP_PASSWORD"),
		}
	case ChannelSMS:
		values = map[string]string{"webhook_url": os.Getenv("SMS_WEBHOOK_URL"), "webhook_token": os.Getenv("SMS_WEBHOOK_TOKEN")}
	case ChannelQQ:
		values = map[string]string{"app_id": os.Getenv("QQ_BOT_APP_ID"), "app_secret": os.Getenv("QQ_BOT_APP_SECRET"), "api_base": os.Getenv("QQ_BOT_API_BASE")}
	}
	return values, nil
}

func emailConfigured(db *gorm.DB) bool {
	values, err := providerSettings(db, ChannelEmail)
	return err == nil && values["host"] != "" && values["from_address"] != ""
}

func smsConfigured(db *gorm.DB) bool {
	values, err := providerSettings(db, ChannelSMS)
	return err == nil && values["webhook_url"] != ""
}

func qqConfigured(db *gorm.DB) bool {
	values, err := providerSettings(db, ChannelQQ)
	return err == nil && values["app_id"] != "" && values["app_secret"] != ""
}

// normalizeTarget 校验并规范化绑定目标。
func normalizeTarget(channel, raw string) (string, error) {
	target := strings.TrimSpace(raw)
	switch channel {
	case ChannelEmail:
		addr, err := mail.ParseAddress(target)
		if err != nil || addr.Address != target {
			return "", errors.New("请输入有效的邮箱地址")
		}
	case ChannelSMS:
		target = strings.TrimPrefix(target, "+86")
		if !cnMobile.MatchString(target) {
			return "", errors.New("请输入有效的中国大陆手机号")
		}
	case ChannelQQ:
		if target == "" || len(target) > 200 {
			return "", errors.New("绑定目标无效")
		}
	case ChannelDingTalk:
		return normalizeDingTalkWebhook(target)
	default:
		return "", errors.New("不支持的通知渠道")
	}
	return target, nil
}

// maskTarget 绑定目标的脱敏展示，明文不回传前端。
func maskTarget(channel, target string) string {
	switch channel {
	case ChannelEmail:
		parts := strings.Split(target, "@")
		if len(parts) == 2 {
			name := parts[0]
			if len(name) > 2 {
				name = name[:2] + "***"
			} else {
				name += "***"
			}
			return name + "@" + parts[1]
		}
	case ChannelSMS:
		if len(target) == 11 {
			return target[:3] + "****" + target[7:]
		}
	case ChannelDingTalk:
		return "钉钉机器人 Webhook（已加密）"
	default:
		if len(target) > 10 {
			return target[:5] + "…" + target[len(target)-4:]
		}
	}
	return target
}

// normalizeDingTalkWebhook 只接受钉钉官方自定义机器人地址。用户粘贴的
// webhook 是一条出站请求能力，放行任意 URL 会把个人渠道绑定变成 SSRF 入口。
func normalizeDingTalkWebhook(raw string) (string, error) {
	endpoint, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(endpoint.Scheme, "https") ||
		!strings.EqualFold(endpoint.Hostname(), "oapi.dingtalk.com") || endpoint.Port() != "" ||
		endpoint.User != nil || endpoint.Path != "/robot/send" || endpoint.Fragment != "" {
		return "", errors.New("请输入有效的钉钉自定义机器人 Webhook 地址")
	}
	query := endpoint.Query()
	tokens, ok := query["access_token"]
	if !ok || len(query) != 1 || len(tokens) != 1 || strings.TrimSpace(tokens[0]) == "" || len(tokens[0]) > 500 {
		return "", errors.New("请粘贴仅含 access_token 的钉钉机器人 Webhook 地址")
	}
	endpoint.Scheme = "https"
	endpoint.Host = "oapi.dingtalk.com"
	endpoint.RawQuery = url.Values{"access_token": []string{tokens[0]}}.Encode()
	return endpoint.String(), nil
}

// channelStatuses 汇总各渠道的服务商配置状态与用户绑定状态，设置页渲染用。
func channelStatuses(db *gorm.DB, userID uint) []ChannelStatus {
	var bindings []ChannelBinding
	_ = db.Where("user_id = ?", userID).Find(&bindings).Error
	byChannel := map[string][]ChannelBinding{}
	for _, binding := range bindings {
		byChannel[binding.Channel] = append(byChannel[binding.Channel], binding)
	}
	defs := []ChannelStatus{
		{Channel: ChannelDingTalk, Label: "钉钉群机器人", Configured: true,
			Description: "在钉钉群里建自定义机器人，把 Webhook 地址粘贴过来即可"},
		{Channel: ChannelEmail, Label: "电子邮件", Configured: emailConfigured(db),
			Description: "配置 SMTP 后可绑定多个接收邮箱，适合较长的提醒内容"},
		{Channel: ChannelSMS, Label: "手机短信", Configured: smsConfigured(db),
			Description: "通过自备短信网关的 Webhook 发送，无需打开应用即可收到"},
		{Channel: ChannelQQ, Label: "QQ 机器人", Configured: qqConfigured(db),
			Description: "通过 QQ 官方机器人私聊提醒，先配好应用再取绑定码"},
	}
	for i := range defs {
		// 机器人主页链接是有意公开的元数据，方便用户还没绑定就去找机器人。
		if defs[i].Channel == ChannelQQ {
			if settings, err := providerSettings(db, ChannelQQ); err == nil {
				defs[i].BotLink = settings["bot_link"]
			}
		}
		list := byChannel[defs[i].Channel]
		if len(list) == 0 {
			defs[i].Status = "unbound"
			continue
		}
		defs[i].Bound = true
		defs[i].Status, defs[i].TargetMasked = list[0].Status, list[0].TargetMasked
		for _, binding := range list {
			if binding.Status == "active" {
				defs[i].Status, defs[i].TargetMasked = binding.Status, binding.TargetMasked
				break
			}
		}
		// 只有 email 逐条展示绑定（可绑多个收件邮箱，逐个增删）。
		if defs[i].Channel == ChannelEmail {
			for _, binding := range list {
				defs[i].Bindings = append(defs[i].Bindings, ChannelBindingItem{
					ID: binding.ID, TargetMasked: binding.TargetMasked, Status: binding.Status,
				})
			}
		}
	}
	return defs
}

// ChannelEnabled 用户在某渠道上有 active 绑定即视为启用。
func ChannelEnabled(db *gorm.DB, userID uint, channel string) bool {
	var n int64
	db.Model(&ChannelBinding{}).
		Where("user_id = ? AND channel = ? AND status = ?", userID, channel, "active").
		Count(&n)
	return n > 0
}

// TryMarkSent 每日推送去重：写入 NotifyLog 成功 = 今天还没发过，允许发送；
// 唯一冲突 = 今天已经发过，返回 false。
func TryMarkSent(db *gorm.DB, userID uint, channel, sendDate string) bool {
	err := db.Create(&NotifyLog{UserID: userID, Channel: channel, SendDate: sendDate}).Error
	return err == nil
}

// RecordSendResult 发送结果写回绑定行，设置页据此展示最近一次失败原因。
func RecordSendResult(db *gorm.DB, userID uint, channel string, err error) {
	updates := map[string]interface{}{"last_error_at": time.Now()}
	if err != nil {
		updates["last_error_code"] = truncateRunes(err.Error(), 150)
	} else {
		updates["last_error_code"] = ""
	}
	db.Model(&ChannelBinding{}).
		Where("user_id = ? AND channel = ?", userID, channel).Updates(updates)
}

// SendDigest 把一条缴费提醒汇总发到用户绑定的目标上。
func SendDigest(ctx context.Context, db *gorm.DB, channel string, userID uint, msg Message) error {
	_, err := sendChannel(ctx, db, channel, userID, msg, "digest-"+time.Now().Format("2006-01-02"), nil)
	return err
}

// sendChannel 在一个渠道上投递一条消息。targetIDs 只对 email 有意义，
// 限定发给哪些邮箱绑定；nil 表示发给全部 active 绑定。
func sendChannel(ctx context.Context, db *gorm.DB, channel string, userID uint, msg Message, idempotencyKey string, targetIDs []uint) (struct{ ExternalID string }, error) {
	type sendResult = struct{ ExternalID string }
	if !supportedChannels[channel] {
		return sendResult{}, &deliveryError{Code: "CHANNEL_UNSUPPORTED", Message: "不支持的通知渠道", Permanent: true}
	}
	var target string
	if channel != ChannelEmail {
		var binding ChannelBinding
		if err := db.Where("user_id = ? AND channel = ? AND status = ?", userID, channel, "active").
			Order("id ASC").First(&binding).Error; err != nil {
			return sendResult{}, &deliveryError{Code: "CHANNEL_NOT_BOUND", Message: "该通知渠道尚未绑定或已停用", Permanent: true}
		}
		decrypted, err := decryptTarget(db, binding.Target)
		if err != nil {
			return sendResult{}, &deliveryError{Code: "TARGET_DECRYPT_FAILED", Message: "读取渠道绑定信息失败", Permanent: true}
		}
		target = decrypted
	}
	switch channel {
	case ChannelEmail:
		recipients, err := activeEmailRecipients(db, userID, targetIDs)
		if err != nil {
			return sendResult{}, &deliveryError{Code: "TARGET_DECRYPT_FAILED", Message: "读取渠道绑定信息失败", Permanent: true}
		}
		if len(recipients) == 0 {
			return sendResult{}, &deliveryError{Code: "CHANNEL_NOT_BOUND", Message: "该通知渠道尚未绑定或已停用", Permanent: true}
		}
		return sendResult{}, sendEmail(db, recipients, msg)
	case ChannelSMS:
		return sendResult{}, sendSMSWebhook(ctx, db, target, msg, idempotencyKey)
	case ChannelQQ:
		return sendResult{}, sendQQ(ctx, db, target, msg)
	case ChannelDingTalk:
		return sendResult{}, sendDingTalk(ctx, target, msg)
	default:
		return sendResult{}, &deliveryError{Code: "CHANNEL_UNSUPPORTED", Message: "不支持的通知渠道", Permanent: true}
	}
}

func activeEmailRecipients(db *gorm.DB, userID uint, targetIDs []uint) ([]string, error) {
	q := db.Where("user_id = ? AND channel = ? AND status = ?", userID, ChannelEmail, "active")
	if len(targetIDs) > 0 {
		q = q.Where("id IN ?", targetIDs)
	}
	var bindings []ChannelBinding
	if err := q.Find(&bindings).Error; err != nil {
		return nil, err
	}
	recipients := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		target, err := decryptTarget(db, binding.Target)
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, target)
	}
	return recipients, nil
}

func sendEmail(db *gorm.DB, recipients []string, msg Message) error {
	values, err := providerSettings(db, ChannelEmail)
	if err != nil || values["host"] == "" || values["from_address"] == "" {
		return &deliveryError{Code: "PROVIDER_CONFIG_INVALID", Message: "邮件服务尚未配置，请先在设置里填写 SMTP", Permanent: true}
	}
	host := values["host"]
	port := values["port"]
	if port == "" {
		port = "587"
	}
	from := values["from_address"]
	name := values["from_name"]
	if name == "" {
		name = notificationBrand()
	}
	subject := "提醒：" + msg.Title
	body := msg.Body
	message := []byte("From: " + name + " <" + from + ">\r\n" +
		"To: " + strings.Join(recipients, ", ") + "\r\n" +
		"Subject: =?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + body)
	var auth smtp.Auth
	if user := values["username"]; user != "" {
		auth = smtp.PlainAuth("", user, values["password"], host)
	}
	if err := sendSMTP(host, port, auth, from, recipients, message); err != nil {
		return &deliveryError{Code: "SMTP_SEND_FAILED", Message: smtpErrorMessage(host, err)}
	}
	return nil
}

func smtpErrorMessage(host string, err error) string {
	detail := strings.ToLower(err.Error())
	if strings.Contains(detail, "535") || strings.Contains(detail, "authentication failed") || strings.Contains(detail, "login fail") {
		if strings.EqualFold(host, "smtp.qq.com") {
			return "QQ 邮箱 SMTP 登录失败：请先在 QQ 邮箱“设置 → 账号”中开启 POP3/SMTP 或 IMAP/SMTP 服务，用户名填写完整邮箱，并使用新生成的授权码（不是 QQ/邮箱登录密码）"
		}
		return "SMTP 登录失败：请检查用户名，并使用邮件服务商提供的 SMTP 授权码或应用专用密码"
	}
	return "邮件发送失败：" + safeError(err)
}

// sendSMTP 兼容 587 STARTTLS 与 465 隐式 TLS 两种主流国内邮箱端口。
func sendSMTP(host, port string, auth smtp.Auth, from string, recipients []string, message []byte) error {
	if port != "465" {
		return smtp.SendMail(host+":"+port, auth, from, recipients, message)
	}
	conn, err := tls.Dial("tcp", host+":"+port, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer conn.Close()
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Quit()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		_ = writer.Close()
		return err
	}
	return writer.Close()
}

// sendSMSWebhook 通过用户自备的短信网关发送：POST JSON 到网关地址，
// 网关负责真正的短信下发，应用不绑定任何短信服务商。
func sendSMSWebhook(ctx context.Context, db *gorm.DB, target string, msg Message, key string) error {
	values, _ := providerSettings(db, ChannelSMS)
	endpoint := values["webhook_url"]
	if endpoint == "" {
		return &deliveryError{Code: "PROVIDER_CONFIG_INVALID", Message: "短信服务尚未配置，请先在设置里填写网关地址", Permanent: true}
	}
	payload := map[string]interface{}{
		"phone": target, "title": truncateRunes(msg.Title, 32), "body": msg.Body,
		"idempotency_key": key,
	}
	return postJSON(ctx, endpoint, values["webhook_token"], payload, nil)
}

// sendDingTalk 发到用户自建的钉钉群机器人。安全关键词约定为“提醒”，
// 每条消息正文都带该词，用户无需维护加签密钥。
func sendDingTalk(ctx context.Context, webhook string, msg Message) error {
	var result struct {
		ErrCode *int   `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := postJSON(ctx, webhook, "", map[string]interface{}{
		"msgtype": "text",
		"text":    map[string]string{"content": "【" + notificationBrand() + "】\n⏰ 提醒：" + msg.Title + "\n" + msg.Body},
	}, &result); err != nil {
		return doNotRetryExternalSend(err)
	}
	if result.ErrCode == nil || *result.ErrCode != 0 {
		code := "未知"
		if result.ErrCode != nil {
			code = strconv.Itoa(*result.ErrCode)
		}
		return &deliveryError{
			Code:      "DINGTALK_SEND_FAILED",
			Message:   "钉钉机器人拒绝了消息，请检查机器人仍在群内、Webhook 是否完整，并确认安全关键词为“提醒”（错误码：" + code + "）",
			Permanent: true,
		}
	}
	return nil
}

func sendQQ(ctx context.Context, db *gorm.DB, openID string, msg Message) error {
	values, err := providerSettings(db, ChannelQQ)
	if err != nil || values["app_id"] == "" || values["app_secret"] == "" {
		return &deliveryError{Code: "PROVIDER_CONFIG_INVALID", Message: "QQ 机器人尚未配置", Permanent: true}
	}
	token, err := qqAccessToken(ctx, values)
	if err != nil {
		return err
	}
	if _, err := sendQQMessage(ctx, token, values, openID, "【"+notificationBrand()+"】\n⏰ "+msg.Title+"\n"+msg.Body); err != nil {
		return doNotRetryExternalSend(err)
	}
	return nil
}

func qqAccessToken(ctx context.Context, values map[string]string) (string, error) {
	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := postJSON(ctx, "https://bots.qq.com/app/getAppAccessToken", "", map[string]string{
		"appId": values["app_id"], "clientSecret": values["app_secret"],
	}, &tokenResp); err != nil {
		return "", err
	}
	if tokenResp.AccessToken == "" {
		return "", &deliveryError{Code: "QQ_TOKEN_FAILED", Message: "获取 QQ 机器人访问凭证失败"}
	}
	return tokenResp.AccessToken, nil
}

// sendQQText 给指定 OpenID 发纯文本，QQ 绑定网关复用。
func sendQQText(ctx context.Context, token string, values map[string]string, openID, content string) error {
	_, err := sendQQMessage(ctx, token, values, openID, content)
	return err
}

func sendQQMessage(ctx context.Context, token string, values map[string]string, openID, content string) (struct{ ExternalID string }, error) {
	type sendResult = struct{ ExternalID string }
	base := strings.TrimRight(values["api_base"], "/")
	if base == "" {
		base = "https://api.bot.qq.com"
	}
	endpoint := base + "/v2/users/" + url.PathEscape(openID) + "/messages"
	var out struct {
		ID string `json:"id"`
	}
	if err := postJSON(ctx, endpoint, "QQBot "+token, map[string]interface{}{
		"content": content, "msg_type": 0,
	}, &out); err != nil {
		return sendResult{}, err
	}
	return sendResult{ExternalID: out.ID}, nil
}

// doNotRetryExternalSend 超时或 5xx 可能发生在机器人已收到消息之后，
// 重试会重复打扰，因此外部机器人发送一律只记一次并明确提示未自动重试。
func doNotRetryExternalSend(err error) error {
	var classified *deliveryError
	if errors.As(err, &classified) {
		return &deliveryError{Code: classified.Code, Message: classified.Message + "（为避免重复发送，未自动重试）", Permanent: true}
	}
	return &deliveryError{Code: "PROVIDER_SEND_UNCERTAIN", Message: "机器人发送结果不确定（为避免重复发送，未自动重试）", Permanent: true}
}

func postJSON(ctx context.Context, endpoint, token string, payload, out interface{}) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		if strings.Contains(token, " ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &deliveryError{Code: "PROVIDER_UNAVAILABLE", Message: "通知服务暂时不可用：" + safeError(err)}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		permanent := resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests
		return &deliveryError{Code: "PROVIDER_HTTP_" + strconv.Itoa(resp.StatusCode), Message: fmt.Sprintf("通知服务返回 %d", resp.StatusCode), Permanent: permanent}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return &deliveryError{Code: "PROVIDER_RESPONSE_INVALID", Message: "通知服务响应格式无效"}
		}
	}
	return nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}

func safeError(err error) string {
	text := err.Error()
	if len(text) > 160 {
		return text[:160]
	}
	return text
}
