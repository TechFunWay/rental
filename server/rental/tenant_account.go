// tenant_account.go — 租户登录账号的关联与解绑（v0.6.10）。
//
// 需求口径：「只能给登记的人开飞牛账号了，登录飞牛账号后访问这个应用来操作」。
// 落实方式（用户 2026-10-13 选定）：租户**自己注册**账号（role=tenant，
// status=2 待关联），房东在租户管理里把它**关联**到某条租户档案——关联即审核
// 通过，账号可进门户；解绑则退回「等待关联」态（账号不删，可再关联别人）。
//
// 为什么关联与审核合成一个动作：用户选择的流程是「租户注册 → 房东关联并审核」，
// 关联时房东本来就要在候选列表里确认这个人是谁，再单独点一次「通过」是多余步骤。
// 若将来要拆成两步，改动局限在本文件与关联弹窗（见设计文档取舍三）。
//
// 一致性：一个账号只能绑一条档案、一条档案只能绑一个账号。数据库层由迁移建的
// 部分唯一索引兜底（account_user_id 非 0 时唯一），应用层在**同一事务**里双向
// 校验，避免并发关联出双绑。
package rental

import (
	"strings"

	"smallgo/server/audit"
	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/user"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// tenantAccountView 租户关联状态出参。
type tenantAccountView struct {
	TenantID    uint   `json:"tenant_id"`
	TenantName  string `json:"tenant_name"`
	Bound       bool   `json:"bound"`
	UserID      uint   `json:"user_id"`
	Username    string `json:"username"`
	Phone       string `json:"phone"`
	AccountStat int    `json:"account_status"` // 1 正常 / 2 待关联（解绑后回到 2）
}

// handleTenantAccountGet 查某条租户档案的账号关联状态。
func handleTenantAccountGet(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		out := tenantAccountView{TenantID: tenant.ID, TenantName: tenant.Name}
		if tenant.AccountUserID > 0 {
			var u database.User
			if err := db.First(&u, tenant.AccountUserID).Error; err == nil {
				out.Bound = true
				out.UserID = u.ID
				out.Username = u.Username
				out.Phone = u.Phone
				out.AccountStat = u.Status
			}
		}
		response.Success(c, out)
	}
}

// handleTenantAccountCandidates 候选账号：role=tenant 且未被任何档案绑定。
//
// 账号枚举防护沿用邀请搜人的口径（2026-10-11 审计）：要求至少 2 个字，
// 只回前 10 条。另外**必须过滤掉已绑定的账号**——否则房东会在候选里看到
// 别人的租户账号，选中后才报错，体验与安全都不好。
func handleTenantAccountCandidates(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := strings.TrimSpace(c.Query("keyword"))
		if len([]rune(q)) < 2 {
			response.ErrorBadRequest(c, "请至少输入 2 个字再搜索")
			return
		}
		type row struct {
			ID       uint   `json:"id"`
			Username string `json:"username"`
		}
		rows := make([]row, 0, 10)
		// 未绑定的判据：tenants.account_user_id 里没有这个 id。
		// 用 NOT IN 子查询而不是 LEFT JOIN：GORM 下更直白，且数据量小（租户数百）。
		//
		// **刻意不返回手机号**（2026-10-14 审计）：这个接口天生跨台账（租户账号在
		// 被关联之前不属于任何台账，没有可收窄的 org_id），房东能用 2 个字的模糊词
		// 反复查询把「未关联租户账号 + 手机号」当花名册捞走。只回用户名就够用了——
		// 租户把用户名告诉房东即可完成关联；要靠手机号确认身份，走免登录登记链接
		// 那条路（那条链接本身带提交通道）。status 保留 1 与 2：**待关联**的租户
		// 注册后正是 status=2，过滤掉他就没法关联了；被驳回停用的 status=0 已排除。
		if err := db.Table("users").
			Select("id, username").
			Where("role = ? AND status IN ?", user.RoleTenant,
				[]int{UserStatusActive, UserStatusPending}).
			Where("id NOT IN (SELECT account_user_id FROM tenants WHERE account_user_id > 0)").
			Where("username LIKE ?", "%"+q+"%").
			Order("id ASC").Limit(10).Scan(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询账号失败")
			return
		}
		response.Success(c, rows)
	}
}

// handleTenantAccountBind 把某个租户账号关联到该租户档案（关联即审核通过）。
//
// 校验（缺一不可，任一失败都按明确业务错误返回）：
//  1. 调用者对**该租户所属台账**的 tenants 模块 ≥ edit（按对象台账复核，
//     防跨台账越权——只挂在路由上的 requireModule 在校验「本次请求选中的台账」，
//     不带参数时会回落到个人台账，挡不住往别人台账里关联账号）；
//  2. 目标账号存在、role=tenant、未停用；
//  3. 目标账号**未被任何档案绑定**；
//  4. 该档案**未绑定任何账号**。
//
// 同事务完成「写档案 + 改账号状态 + 写审计」，避免留下「绑了一半」的中间态。
func handleTenantAccountBind(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		var body struct {
			UserID uint `json:"user_id"`
		}
		if err := c.ShouldBindJSON(&body); err != nil || body.UserID == 0 {
			response.ErrorBadRequest(c, "请选择要关联的租户账号")
			return
		}

		var bound database.User
		err := db.Transaction(func(tx *gorm.DB) error {
			var target database.User
			if err := tx.First(&target, body.UserID).Error; err != nil {
				return errAccountNotFound
			}
			if target.Role != user.RoleTenant || target.Status == UserStatusDisabled {
				return errAccountNotTenant
			}
			// 同一个账号不能绑两条档案（唯一索引的兜底之外，先给出可读错误）。
			var already uint
			if err := tx.Model(&Tenant{}).Where("account_user_id = ? AND id <> ?", target.ID, tenant.ID).
				Limit(1).Pluck("id", &already).Error; err != nil {
				return err
			}
			if already > 0 {
				return errAccountAlreadyBound
			}
			// 一条档案不能绑两个账号：已绑且不是同一个 → 明确提示先解绑。
			var current Tenant
			if err := tx.Where("id = ?", tenant.ID).First(&current).Error; err != nil {
				return errAccountNotFound
			}
			if current.AccountUserID > 0 && current.AccountUserID != target.ID {
				return errTenantAlreadyBound
			}
			if err := tx.Model(&Tenant{}).Where("id = ? AND org_id = ?", tenant.ID, tenant.LedgerID).
				Update("account_user_id", target.ID).Error; err != nil {
				return err
			}
			// 关联即审核通过：状态置 1，账号可以进门户。
			if target.Status != UserStatusActive {
				if err := tx.Model(&database.User{}).Where("id = ?", target.ID).
					Update("status", UserStatusActive).Error; err != nil {
					return err
				}
			}
			bound = target
			return nil
		})
		if err != nil {
			switch err {
			case errAccountNotFound:
				response.ErrorNotFound(c, "账号不存在")
			case errAccountNotTenant:
				response.ErrorBadRequest(c, "只能关联租户类型（注册时选「我是租客」）的账号")
			case errAccountAlreadyBound:
				response.ErrorBadRequest(c, "该账号已关联了其他租户档案，请先在那条档案上解除关联")
			case errTenantAlreadyBound:
				response.ErrorBadRequest(c, "该租户已关联了其他账号，请先解除关联")
			default:
				response.ErrorInternal(c, "关联失败")
			}
			return
		}

		audit.Log(db, c, "rental.tenant_account.bind", "tenant", tenant.ID,
			"关联租户登录账号："+bound.Username)
		response.Success(c, tenantAccountView{
			TenantID: tenant.ID, TenantName: tenant.Name, Bound: true,
			UserID: bound.ID, Username: bound.Username, Phone: bound.Phone,
			AccountStat: UserStatusActive,
		})
	}
}

// handleTenantAccountUnbind 解除关联：档案清 account_user_id，账号状态回 2
// （「等待关联」），可再被关联到别的档案。
//
// 为什么账号状态要回 2 而不是保持 1：status=1 表示「已被某个房东确认过」，
// 解绑后他不再属于任何档案，门前数据一律不可达；回到 2 才能复用 v0.6.0 的
// 待审核门禁（门户只放行 /portal/status），前端也据此渲染「等待关联」页。
func handleTenantAccountUnbind(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if tenant.AccountUserID == 0 {
			response.ErrorBadRequest(c, "该租户还没有关联登录账号")
			return
		}
		accountID := tenant.AccountUserID
		var unbindUser database.User
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&Tenant{}).Where("id = ? AND org_id = ?", tenant.ID, tenant.LedgerID).
				Update("account_user_id", uint(0)).Error; err != nil {
				return err
			}
			if err := tx.First(&unbindUser, accountID).Error; err != nil {
				// 账号已被删除：档案照样解绑成功（外键层面本来就没有约束）。
				return nil
			}
			// 只对**本账号仍是租户角色**的情况回退状态；若账号已被改成别的角色
			// （见 user.UpdateUser 的守卫，正常不会发生），不去动它的状态。
			if unbindUser.Role != user.RoleTenant {
				return nil
			}
			return tx.Model(&database.User{}).Where("id = ?", accountID).
				Update("status", UserStatusPending).Error
		})
		if err != nil {
			response.ErrorInternal(c, "解除关联失败")
			return
		}
		audit.Log(db, c, "rental.tenant_account.unbind", "tenant", tenant.ID,
			"解除租户登录账号关联："+unbindUser.Username)
		response.Success(c, nil)
	}
}

// 本文件用到的业务错误（映射到明确的 400/404，而不是笼统 500）。
var (
	errAccountNotFound     = errString("账号不存在")
	errAccountNotTenant    = errString("只能关联租户账号")
	errAccountAlreadyBound = errString("该账号已关联其他租户")
	errTenantAlreadyBound  = errString("该租户已关联其他账号")
)

// errString 是一个极简的 error 实现，避免为一个内部判定引入 fmt.Errorf 的包装
// （这些错误只在同文件内比较，不外传、不需要 %w 链）。
type errString string

func (e errString) Error() string { return string(e) }

// tenantAccountOf 读某档案绑定的账号（未绑定返回 nil）。
//
// 保留为独立函数是给删除/清理路径复用的：删除租户档案时要顺带把账号状态退回
// 「等待关联」，避免账号卡在「已关联」但档案已消失的状态。
func tenantAccountOf(db *gorm.DB, tenant *Tenant) *database.User {
	if tenant == nil || tenant.AccountUserID == 0 {
		return nil
	}
	var u database.User
	if err := db.First(&u, tenant.AccountUserID).Error; err != nil {
		return nil
	}
	return &u
}

// releaseTenantAccount 在删除租户档案时解除账号关联并把状态退回「等待关联」。
//
// 与 handleTenantAccountUnbind 同口径，但供删除路径（无需鉴权上下文）调用。
// 返回错误由调用方决定是否阻断删除：解绑失败不该让房东删不掉档案，
// 所以调用方通常只记日志。
func releaseTenantAccount(db *gorm.DB, tenant *Tenant) error {
	if tenant == nil || tenant.AccountUserID == 0 {
		return nil
	}
	account := tenantAccountOf(db, tenant)
	if account == nil {
		return nil
	}
	if account.Role != user.RoleTenant {
		return nil
	}
	return db.Model(&database.User{}).Where("id = ?", account.ID).
		Update("status", UserStatusPending).Error
}
