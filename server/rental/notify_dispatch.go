// notify_dispatch.go — 缴费提醒的每日推送。
//
// 每天 08:00 由调度器触发：按共享台账计算待提醒的缴费项，非空则给每个
// 绑定了提醒渠道（且有业务权限）的用户各发一条汇总消息。
// (user, channel, 日期) 唯一去重——每渠道每天至多一条，失败不重试，
// 错误记录在绑定行上，次日推送自然再试。
package rental

import (
	"context"
	"fmt"
	"time"

	"smallgo/server/logger"
	"smallgo/server/notify"
	"smallgo/server/scheduler"

	"gorm.io/gorm"
)

// appDB 由 apps.App.Migrate 在启动阶段注入，供调度任务闭包使用。
var appDB *gorm.DB

func init() {
	scheduler.Register(scheduler.Job{
		Name:  "rental-payment-notify",
		Daily: "08:00",
		Run: func() {
			if appDB != nil {
				dispatchPaymentNotices(appDB)
			}
		},
	})
	scheduler.Register(scheduler.Job{
		Name:     "rental-qq-gateway",
		Interval: 30 * time.Second,
		Run:      notify.EnsureQQGateway,
	})
}

// dispatchPaymentNotices 遍历绑定了提醒渠道的用户，逐渠道发送当日汇总。
// 数据共享后缴费提醒只有一套台账，收件人改成「绑定了渠道且业务权限不低于
// 只读的用户」——无权限的注册账号不会收到房源与欠缴信息。
func dispatchPaymentNotices(db *gorm.DB) {
	now := time.Now()
	var userIDs []uint
	if err := db.Model(&notify.ChannelBinding{}).Where("active = ?", true).
		Distinct().Pluck("user_id", &userIDs).Error; err != nil {
		logger.Error("rental: scan notify users: %v", err)
		return
	}
	sendDate := now.Format("2006-01-02")
	for _, userID := range userIDs {
		var role string
		db.Table("users").Select("role").Where("id = ?", userID).Scan(&role)
		if accessRank[accessLevel(db, userID, role)] < accessRank[AccessReadonly] {
			continue
		}
		items := computePaymentDue(db, now)
		if len(items) == 0 {
			continue
		}
		msg := paymentDigestMessage(items)
		for _, channel := range notify.SupportedChannels() {
			if !notify.ChannelEnabled(db, userID, channel) {
				continue
			}
			// 先占去重位再发送：进程重启/重入都不会把同一天发重。
			if !notify.TryMarkSent(db, userID, channel, sendDate) {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := notify.SendDigest(ctx, db, channel, userID, msg)
			cancel()
			notify.RecordSendResult(db, userID, channel, err)
			if err != nil {
				logger.Error("rental: notify %s to user %d: %v", channel, userID, err)
			}
		}
	}
}

// paymentDigestMessage 汇总文案：一行一个房间，逾期在前（computePaymentDue
// 已按缴费日升序，逾期项天然排在最前）。
func paymentDigestMessage(items []PaymentDueItem) notify.Message {
	body := ""
	for _, item := range items {
		when := fmt.Sprintf("%d 天后到期", item.DaysLeft)
		if item.DaysLeft < 0 {
			when = fmt.Sprintf("已逾期 %d 天", -item.DaysLeft)
		} else if item.DaysLeft == 0 {
			when = "今天到期"
		}
		monthDay := ""
		if t, err := parsePeriod(item.Period); err == nil {
			monthDay = (t.AddDate(0, 0, item.PayDay-1)).Format("1月2日")
		}
		line := fmt.Sprintf("· %s %s：%s应缴约 ¥%s（%s，水电按抄表另计），%s",
			item.RoomNo, item.TenantName, monthDay,
			formatPrice(item.ExpectedAmount), cycleLabel(item.PayCycle), when)
		if body != "" {
			body += "\n"
		}
		body += line
	}
	return notify.Message{Title: "缴费提醒", Body: body}
}
