// access.go — 业务数据访问权限（按台账 + 按模块勾选）。
//
// 权限模型（2026-10-09 起，与数据归属模型同步重写；下面这段此前还停留在
// v0.5.0「全员共享 + 管理员按人授权」的旧口径，2026-10-11 更正）：
// 数据按**台账**隔离（见 ledger.go）。每个账号注册即得自己的「个人台账」，
// 可自建「共同台账」邀请他人。权限是「(用户, 台账)」这个关系的属性——同一用户
// 在不同台账可以有完全不同的模块勾选，存在 ledger_members 表的 perms 列里，
// 不再按角色一刀切；rental_accesses 只作 0.5.1 之前旧授权数据的来源，
// 迁移后不再读取。
//
// 门禁分三层：
//
//   - requireModule（读）：并集准入（任一所属台账满足档位即可进页面），
//     然后把可见台账收窄到「本模块至少 view」的那些——成员资格不等于
//     该台账全模块数据可见；
//   - requireLedgerModule（写）：本次请求**选中的台账**必须达到档位；
//   - requireLedgerWrite（写，by-id）：取到对象后再按**对象所属台账**复核
//     一次权限。少了这层，不带 ?ledger_id 时写入判定会回落到个人台账
//     （恒完全权限），在共同台账里只有查看权的成员就能改删对方数据。
//
// 模块（路由归属见各 setup*Routes）：
//
//	rooms     房源（列表、导出、增改删）
//	tenants   租户与合同（档案、退租、合同上传预览）
//	meters    抄表（台账查看、读数录入）
//	bills     账单与收费项（开票、收款登记、CSV 导入导出、打印）
//	payments  收款记录（流水查询，只读——收款动作走账单模块）
//	analytics 统计分析（总览驾驶舱、收入趋势与收缴率）
//
// 档位（模块内从低到高）：
//
//	view 查看 / edit 操作（含查看）/ full 管理（含操作、删除与导入）
//
// 兼容：Level 存的旧版整档字符串（none/meter/readonly/edit/full，
// v0.4.x）读取时自动换算成模块勾选，写入一律是新 JSON。
package rental

import (
	"encoding/json"
	"strings"
	"time"

	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 旧版整档等级（v0.4.x 存库值），仅在读取换算时使用。
const (
	AccessNone     = "none"
	AccessMeter    = "meter"
	AccessReadonly = "readonly"
	AccessEdit     = "edit"
	AccessFull     = "full"
)

// 业务模块与档位常量，存库值与前端同一套词。
const (
	ModuleRooms     = "rooms"
	ModuleTenants   = "tenants"
	ModuleMeters    = "meters"
	ModuleBills     = "bills"
	ModulePayments  = "payments"
	ModuleAnalytics = "analytics"

	PermView = "view"
	PermEdit = "edit"
	PermFull = "full"
)

// moduleKeys 模块稳定顺序（保存时归一化用）；moduleNames 中文名（文案用）。
var moduleKeys = []string{ModuleRooms, ModuleTenants, ModuleMeters, ModuleBills, ModulePayments, ModuleAnalytics}

var moduleNames = map[string]string{
	ModuleRooms:     "房源",
	ModuleTenants:   "租户与合同",
	ModuleMeters:    "抄表",
	ModuleBills:     "账单",
	ModulePayments:  "收款记录",
	ModuleAnalytics: "统计分析",
}

var permRank = map[string]int{
	PermView: 1,
	PermEdit: 2,
	PermFull: 3,
}

// ModulePerms 模块 → 档位；缺省/未出现的模块按无权限。
type ModulePerms map[string]string

// RentalAccess 用户级业务授权：每个用户一行，Level 存模块勾选 JSON
// （空串/空对象 = 全部模块无权限）。
type RentalAccess struct {
	UserID    uint      `gorm:"primarykey" json:"user_id"`
	Level     string    `json:"level"`
	UpdatedAt time.Time `json:"updated_at"`
}

// 注意：RentalAccess 只在**迁移里读取**（0.5.0 老库的全局权限表），
// 0.6 起不再是模型的一部分，所以这里**不注册**它——这样
// rental_accesses 表的存在就成了「这是老库」的可靠判据（见
// legacyTenantDataExists），全新安装也不会建出这张废表。

// parsePerms 把 Level 列的存量值归一化成模块勾选：JSON 直接解析（非法键
// 档位丢弃），旧版整档字符串按预设换算，空/未知按全无。
func parsePerms(raw string) ModulePerms {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == AccessNone {
		return ModulePerms{}
	}
	if strings.HasPrefix(raw, "{") {
		var m ModulePerms
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return ModulePerms{}
		}
		out := ModulePerms{}
		for _, k := range moduleKeys {
			if permRank[m[k]] > 0 {
				out[k] = m[k]
			}
		}
		return out
	}
	switch raw {
	case AccessMeter: // 抄表专用档：抄表可录 + 房间可见（表单下拉依赖）
		return ModulePerms{ModuleMeters: PermEdit, ModuleRooms: PermView}
	case AccessReadonly:
		out := ModulePerms{}
		for _, k := range moduleKeys {
			out[k] = PermView
		}
		return out
	case AccessEdit:
		out := ModulePerms{}
		for _, k := range moduleKeys {
			out[k] = PermEdit
		}
		return out
	case AccessFull:
		out := ModulePerms{}
		for _, k := range moduleKeys {
			out[k] = PermFull
		}
		return out
	}
	return ModulePerms{}
}

// requireModule 模块档位门禁（读接口）：粗粒度准入 + 台账范围收窄。
//
//  1. 并集口径：当前用户在**任一**所属台账里对该模块达到该档位即放行——它决定
//     「菜单/页面能不能进」，若改成「必须某个台账满足」，只读员工会被误挡；
//  2. 收窄口径（2026-10-11 补）：放行之后把本次请求的可见台账收窄为「对该模块
//     至少有 view 权限」的那些台账。此前只做第 1 步，成员资格就等于**该台账
//     全模块数据可见**：只勾了抄表的临时工照样能读到租户身份证号、合同 PDF 与
//     收款流水，与「按人按模块勾选」的产品口径不符。
//     显式指定了 ?ledger_id 且该台账没有本模块 view 时直接 403（与写接口同一
//     报错口径），未指定则取交集，交集为空 → handler 返回空列表。
//
// v0.6.0 起权限来自 ledger_members 成员行（每台账各一套），不再是 rental_accesses
// 的单行全局权限；后者仅作旧数据来源，迁移后不再读取。
func requireModule(db *gorm.DB, module, tier string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")
		if !anyLedgerPerm(db, userID, module, tier) {
			response.ErrorForbidden(c, "没有「"+moduleNames[module]+"」模块的权限，请联系该台账的管理者为你的账号勾选")
			c.Abort()
			return
		}
		allowed := moduleLedgerIDs(db, userID, module, PermView)
		if explicit, _ := c.Get(ledgerExplicitKey); explicit == true {
			id, _ := c.Get(ledgerIDKey)
			if ledgerID, ok := id.(uint); ok && !containsLedger(allowed, ledgerID) {
				response.ErrorForbidden(c, "没有「"+moduleNames[module]+"」模块的权限，请联系该台账的管理者为你的账号勾选")
				c.Abort()
				return
			}
		} else {
			c.Set(ledgerIDsKey, intersectLedgers(visibleLedgerIDs(c), allowed))
		}
		c.Next()
	}
}

// requireLedgerModule 台账内模块档位门禁：**指定的单个台账**必须满足档位。
// 用于写操作——写必须落在明确的一个台账里，不能靠并集蒙混。
//
// 依赖 resolveLedger 中间件把「本次请求的台账」放进 context（ledgerIDKey）。
//
// 注意：本门禁只看「本次请求选中的台账」，没带 ?ledger_id 时会回落到调用者的
// 个人台账（恒为完全权限）。因此 by-id 写接口取到对象后还必须调用
// requireLedgerWrite（按**对象所属台账**再判一次），否则去掉参数就能改删
// 别的台账里、自己只有查看权的数据。
func requireLedgerModule(db *gorm.DB, module, tier string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ledgerID, ok := c.Get(ledgerIDKey)
		if !ok {
			response.ErrorBadRequest(c, "请指定要操作的台账（ledger_id）")
			c.Abort()
			return
		}
		id, _ := ledgerID.(uint)
		if permRank[ledgerPerms(db, c.GetUint("userID"), id)[module]] < permRank[tier] {
			response.ErrorForbidden(c, "没有「"+moduleNames[module]+"」模块的权限，请联系该台账的管理者为你的账号勾选")
			c.Abort()
			return
		}
		c.Next()
	}
}

// requireLedgerWrite 按**对象所属台账**判定写权限：by-id 写接口（改/删/退租/
// 收款/拒绝/换房）在取到对象后必须调用一次。返回 false 时已写出 403。
//
// 为什么需要它：路由上的 requireLedgerModule 校验的是「本次请求选中的台账」，
// 而没带 ?ledger_id 时那会回落到调用者的个人台账（恒完全权限）；对象却是按
// 「全部可见台账」取的。于是一个在共同台账里只有查看权的成员，去掉参数就能
// 删除该台账的账单、改租户、删附件——2026-10-11 审计复现过的越权。
// 写权限必须落在对象真正所属的台账上，与请求有没有带参数无关。
func requireLedgerWrite(db *gorm.DB, c *gin.Context, ledgerID uint, module, tier string) bool {
	if !containsLedger(activeLedgerIDsOrNil(db, c.GetUint("userID")), ledgerID) {
		response.ErrorNotFound(c, "数据不存在")
		return false
	}
	if permRank[ledgerPerms(db, c.GetUint("userID"), ledgerID)[module]] < permRank[tier] {
		response.ErrorForbidden(c, "没有「"+moduleNames[module]+"」模块的权限，请联系该台账的管理者为你的账号勾选")
		return false
	}
	return true
}

// requireAnyView 任一模块可看即放行：通知渠道绑定是个人提醒配置，
// 不按模块卡，只要进得了业务就允许绑自己的渠道。
func requireAnyView(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		perms := mergedPerms(db, c.GetUint("userID"))
		for _, k := range moduleKeys {
			if permRank[perms[k]] >= permRank[PermView] {
				c.Next()
				return
			}
		}
		response.ErrorForbidden(c, "还没有任何台账权限，请联系台账管理者为你的账号勾选")
		c.Abort()
	}
}
