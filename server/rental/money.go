// money.go — 租房账单的金额/用量计算规则（纯函数）。
//
// 所有费用口径集中在这里，前后端展示与数据库存储共用同一套规则：
//
//	用量 = max(0, 本月读数 - 上月读数)
//	费用 = round2(用量 × 单价)
//	应付合计 = round2(租金 + 水费 + 电费 + 燃气费 + 卫生费 + 管理费)
//	欠缴额   = max(0, 合计 - 已收)（展示时计算，不落库）
//	状态     合计-已收 ≤ 0.005 → paid；已收 > 0.005 → partial；否则 unpaid
package rental

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// feeTolerance 是半分容差：浮点累计误差小于 0.005 元时视为相等，
// 避免 0.1+0.2 类误差把"缴清"误判成"欠一分钱"。
const feeTolerance = 0.005

// round2 四舍五入到分。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// usage 返回本期用量；本月读数小于上月读数（换表倒转）时按 0 计。
func usage(last, now float64) float64 {
	if now < last {
		return 0
	}
	return now - last
}

// meterFee = round2(max(0, now-last) × price)。
func meterFee(last, now, price float64) float64 {
	return round2(usage(last, now) * price)
}

// 账单缴纳状态(billStatus 的返回值,也是 bills.status 的取值)。
const (
	billStatusPaid    = "paid"
	billStatusPartial = "partial"
	billStatusUnpaid  = "unpaid"
)

// billStatus 依据应付合计与已收金额判定缴纳状态。
func billStatus(total, paid float64) string {
	switch {
	case total-paid <= feeTolerance:
		return billStatusPaid
	case paid > feeTolerance:
		return billStatusPartial
	default:
		return billStatusUnpaid
	}
}

// arrears 返回欠缴额，最小为 0。
func arrears(total, paid float64) float64 {
	if total-paid <= feeTolerance {
		return 0
	}
	return round2(total - paid)
}

// receiptNo 生成收费单据编号：R-YYYYMM-<房号>-<ID 补零 4 位>。
// 房号中的非字母数字字符统一替换为 '-'，保证编号对文件名/URL 安全；
// 编号由账单 ID 与开票月份决定，账单存在期内保持稳定。
func receiptNo(period, roomNo string, id uint) string {
	t, err := time.ParseInLocation("2006-01", period, time.Local)
	ym := strings.ReplaceAll(period, "-", "")
	if err == nil {
		ym = t.Format("200601")
	}
	var b strings.Builder
	b.WriteString("R-")
	b.WriteString(ym)
	b.WriteByte('-')
	for _, r := range roomNo {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return fmt.Sprintf("%s-%04d", b.String(), id)
}
