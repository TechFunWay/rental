// notify_test.go — 通知渠道层的加密、目标校验与去重测试（不触网）。
package notify

import (
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/database"
	"smallgo/server/sysconfig"

	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.InitDB(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
	})
	if err := database.AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := sysconfig.InitDefaultConfigs(db); err != nil {
		t.Fatalf("init configs: %v", err)
	}
	return db
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	db := testDB(t)
	plain := "https://oapi.dingtalk.com/robot/send?access_token=abc123"
	enc, err := encryptTarget(db, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(enc, "abc123") {
		t.Fatalf("ciphertext leaks plaintext: %s", enc)
	}
	got, err := decryptTarget(db, enc)
	if err != nil || got != plain {
		t.Fatalf("round trip = %q, %v; want %q", got, err, plain)
	}
	// 同一明文两次加密应产生不同密文（随机 nonce）。
	enc2, _ := encryptTarget(db, plain)
	if enc2 == enc {
		t.Fatalf("ciphertext should be randomized")
	}
}

func TestNormalizeDingTalkWebhook(t *testing.T) {
	// 合法：官方域 + 仅 access_token 参数。
	ok := "https://oapi.dingtalk.com/robot/send?access_token=abcdef123456"
	got, err := normalizeDingTalkWebhook(ok)
	if err != nil || got != ok {
		t.Fatalf("normalize = %q, %v; want %q", got, err, ok)
	}
	bad := []string{
		"http://oapi.dingtalk.com/robot/send?access_token=x",                            // 非 https
		"https://evil.example.com/robot/send?access_token=x",                            // 非官方域
		"https://oapi.dingtalk.com/robot/send?access_token=x&x=1",                       // 多余参数
		"https://oapi.dingtalk.com/robot/send",                                          // 缺 token
		"https://oapi.dingtalk.com/robot/send?access_token=" + strings.Repeat("x", 501), // 超长
	}
	for _, b := range bad {
		if _, err := normalizeDingTalkWebhook(b); err == nil {
			t.Errorf("normalize should reject %q", b[:60])
		}
	}
}

func TestNormalizeAndMaskTarget(t *testing.T) {
	if got, err := normalizeTarget(ChannelSMS, "+8613812345678"); err != nil || got != "13812345678" {
		t.Fatalf("sms normalize = %q, %v", got, err)
	}
	if got, err := normalizeTarget(ChannelSMS, "12345"); err == nil {
		t.Fatalf("sms should reject %q", got)
	}
	if got, err := normalizeTarget(ChannelEmail, "a@b.com"); err != nil || got != "a@b.com" {
		t.Fatalf("email normalize = %q, %v", got, err)
	}
	if got := maskTarget(ChannelSMS, "13812345678"); got == "13812345678" {
		t.Fatalf("sms mask should hide digits: %q", got)
	}
	if got := maskTarget(ChannelEmail, "yiwei@example.com"); !strings.Contains(got, "***@example.com") {
		t.Fatalf("email mask = %q", got)
	}
}

func TestTryMarkSentDedup(t *testing.T) {
	db := testDB(t)
	if !TryMarkSent(db, 1, ChannelDingTalk, "2026-09-29") {
		t.Fatalf("first mark should succeed")
	}
	if TryMarkSent(db, 1, ChannelDingTalk, "2026-09-29") {
		t.Fatalf("second mark same day should be deduped")
	}
	// 不同用户/渠道/日期互不影响。
	if !TryMarkSent(db, 1, ChannelEmail, "2026-09-29") || !TryMarkSent(db, 2, ChannelDingTalk, "2026-09-29") ||
		!TryMarkSent(db, 1, ChannelDingTalk, "2026-09-30") {
		t.Fatalf("different user/channel/date should not be deduped")
	}
}

func TestChannelEnabled(t *testing.T) {
	db := testDB(t)
	if ChannelEnabled(db, 1, ChannelSMS) {
		t.Fatalf("no binding yet")
	}
	if err := db.Create(&ChannelBinding{UserID: 1, Channel: ChannelSMS, Target: "x", TargetMasked: "y", Status: "active"}).Error; err != nil {
		t.Fatalf("seed binding: %v", err)
	}
	if !ChannelEnabled(db, 1, ChannelSMS) {
		t.Fatalf("active binding should enable channel")
	}
	if ChannelEnabled(db, 2, ChannelSMS) {
		t.Fatalf("other user should not be enabled")
	}
	db.Model(&ChannelBinding{}).Where("user_id = ?", 1).Update("status", "disabled")
	if ChannelEnabled(db, 1, ChannelSMS) {
		t.Fatalf("disabled binding should not enable channel")
	}
}
