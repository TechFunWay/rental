// referral.go — 介绍费（房源挂赏金，中介/村委推荐成交后房东付给介绍人）。
//
// 需求（用户 2026-10-13，粉丝提出）：「可以搞个介绍费，房东选择是否出介绍费，
// 管理员也可以设置无介绍费、最低介绍费，或者介绍费自己填」。
//
// 定位：**平台只做标注与规则约束，不经手钱**。介绍费挂在房源上（房东对某套房源
// 愿意出的赏金），中介/村委在给咨询者推荐空房时能直接看到，成交后线下结算。
// 刻意不做的事：不做收款/托管/结算流水——那会把这个本地自托管应用变成支付系统，
// 与「数据保存在本机、平台不经手资金」的既有定调冲突。
//
// 三种计价方式（房东侧）：
//
//	'' 或 none  不提供介绍费
//	fixed       固定金额（元）
//	percent     月租金的百分比（%）——按房源默认月租金折算展示金额
//
// 平台策略（系统配置，管理员在「系统配置 → 介绍费」里设）：
//
//	off   平台不设介绍费：房东侧不出现该设置，已存的值也按「不提供」对待
//	min   最低介绍费：房东必须提供，且折算金额不得低于「最低介绍费」配置值
//	free  自愿填写：选填，多少都行（默认，开箱即可用；要收紧改成 min 或 off）
package rental

import (
	"fmt"
	"strconv"
	"strings"

	"smallgo/server/sysconfig"

	"gorm.io/gorm"
)

// 平台策略取值。
const (
	referralPolicyOff  = "off"
	referralPolicyMin  = "min"
	referralPolicyFree = "free"
)

// 房东侧的计价方式取值（空串 = 不提供）。
const (
	referralModeNone    = "" // 不提供
	referralModeFixed   = "fixed"
	referralModePercent = "percent"
)

// 配置键。
const (
	referralPolicyConfigKey = "rental_referral_policy"
	referralMinConfigKey    = "rental_referral_min"
)

// registerReferralConfigs 介绍费组：平台策略与最低金额。
func registerReferralConfigs() {
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: referralPolicyConfigKey, Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeSelect,
		Default: referralPolicyFree,
		Options: []string{referralPolicyFree, referralPolicyMin, referralPolicyOff},
		Group:   "referral", Label: "介绍费规则",
		Public: true, // 房东侧表单要据此显示/校验，不是敏感项
		Description: "自愿填写：房东可选是否出介绍费、多少都行；最低介绍费：房东必须填且不低于下面的最低金额；" +
			"无介绍费：平台不设介绍费，房东侧不出现该设置。平台只做标注，不经手资金",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: referralMinConfigKey, Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeInt,
		Default: "200", Group: "referral", Label: "最低介绍费（元）",
		Public: true,
		Description: "规则为「最低介绍费」时生效：房东填的固定金额，或按月租金百分比折算出的金额，" +
			"都不得低于这个数",
	})
}

// referralPolicy 读平台策略；配置读不到或取值非法时按「自愿填写」处理（功能可用优先）。
func referralPolicy(db *gorm.DB) string {
	if db == nil {
		return referralPolicyFree
	}
	v, err := sysconfig.GetConfig(db, referralPolicyConfigKey, 0)
	if err != nil {
		return referralPolicyFree
	}
	switch strings.TrimSpace(v) {
	case referralPolicyOff, referralPolicyMin, referralPolicyFree:
		return strings.TrimSpace(v)
	default:
		return referralPolicyFree
	}
}

// referralMin 读最低介绍费金额（元）；非法/缺失按默认 200。
func referralMin(db *gorm.DB) float64 {
	if db == nil {
		return 200
	}
	v, err := sysconfig.GetConfig(db, referralMinConfigKey, 0)
	if err != nil {
		return 200
	}
	n, err2 := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err2 != nil || n < 0 {
		return 0
	}
	return n
}

// referralAmount 把「计价方式 + 取值」折算成展示金额（元）。
// percent 按房源默认月租金折算；固定金额原样返回。
func referralAmount(mode string, value, monthlyRent float64) float64 {
	switch mode {
	case referralModeFixed:
		if value < 0 {
			return 0
		}
		return round2(value)
	case referralModePercent:
		if value <= 0 || monthlyRent <= 0 {
			return 0
		}
		return round2(monthlyRent * value / 100)
	default:
		return 0
	}
}

// normalizeReferral 按平台策略归一化房东提交的介绍费，返回 (mode, value, 错误文案)。
//
//   - off ：一律按「不提供」处理（老数据/老客户端提交的值静默忽略，不报错——
//     平台随时可切策略，不该因为历史值让房东的保存失败）
//   - min ：必须提供，且折算金额 ≥ 最低介绍费
//   - free：选填，填了要合法（固定金额 > 0；百分比 1–100）
func normalizeReferral(db *gorm.DB, mode string, value, monthlyRent float64) (string, float64, string) {
	// off：平台不设介绍费，提交上来的值一律按「不提供」处理。
	// 刻意不报错——平台随时可以切换策略，不该因为历史值/老前端让房东的保存失败。
	if referralPolicy(db) == referralPolicyOff {
		return referralModeNone, 0, ""
	}
	mode = strings.TrimSpace(mode)
	if mode == "none" {
		mode = referralModeNone
	}
	if mode != referralModeNone && mode != referralModeFixed && mode != referralModePercent {
		return referralModeNone, 0, "介绍费方式不合法"
	}
	if mode == referralModeNone {
		if referralPolicy(db) == referralPolicyMin {
			return referralModeNone, 0, "当前规则要求房东提供介绍费，请填写金额或月租金百分比"
		}
		return referralModeNone, 0, ""
	}
	if value < 0 {
		return referralModeNone, 0, "介绍费不能为负数"
	}
	if mode == referralModePercent && value > 100 {
		return referralModeNone, 0, "介绍费百分比不能超过 100%"
	}
	if mode == referralModeFixed && value > 1000000 {
		return referralModeNone, 0, "介绍费金额过大，请核对"
	}
	if referralPolicy(db) == referralPolicyMin {
		min := referralMin(db)
		if got := referralAmount(mode, value, monthlyRent); got < min {
			return referralModeNone, 0, fmt.Sprintf(
				"按当前规则，介绍费折算后不能低于 %s 元（当前 %s 元）", trimNum(min), trimNum(got))
		}
	}
	return mode, round2(value), ""
}

// trimNum 去掉金额末尾多余的 0（文案用：200 而不是 200.00）。
func trimNum(v float64) string {
	return strconv.FormatFloat(round2(v), 'f', -1, 64)
}
