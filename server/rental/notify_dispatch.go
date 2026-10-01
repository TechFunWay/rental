// notify_dispatch.go — 缴费提醒的每日推送。
//
// 每天 08:00 由调度器触发：对每位设置了缴费日（pay_day>0）的用户计算
// 待提醒的缴费项，非空则按其绑定的渠道各发一条汇总消息。
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

// dispatchPaymentNotices 遍历有在租租户的用户，逐渠道发送当日汇总。
// 缴费设置挂在租户上（租户没设置时用全局默认），所以候选用户按在租租户取；
// 生效缴费日为 0（不提醒）的用户由 computePaymentDue 自然过滤掉。
func dispatchPaymentNotices(db *gorm.DB) {
	now := time.Now()
	var userIDs []uint
	if err := db.Model(&Tenant{}).Where("active = ?", true).Distinct().Pluck("user_id", &userIDs).Error; err != nil {
		logger.Error("rental: scan notify users: %v", err)
		return
	}
	sendDate := now.Format("2006-01-02")
	for _, userID := range userIDs {
		items := computePaymentDue(db, userID, now)
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
