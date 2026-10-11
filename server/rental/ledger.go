// ledger.go — 台账（ledger）：业务数据的归属单元。
//
// 模型（2026-10-09 按用户要求改成与兄弟应用「账单」一致的形态，取代 0.5.1
// 的「台账/公共台账 + 超管全局可见」设计）：
//
//   - **个人台账**（kind=personal）：每个账号注册时自动创建，只有本人能用，
//     数据默认互不相通；不可删除、不可邀请成员、不显示在别人的切换器里。
//   - **共同台账**（kind=shared）：任何登录用户都能自建并命名（如「A区三兄弟」），
//     创建者就是**管理者**（role=owner）；管理者可以邀请其他账号加入、给每个
//     成员按模块勾选权限、移除成员；被邀请的人要**自己接受**才算成员，
//     成员也可以主动退出。
//   - 每条业务数据（房源/租户/合同/抄表/账单/收款）必属于**恰好一个**台账，
//     台账之间数据不互通。
//   - 权限是「(用户, 台账)」这个关系的属性，同一用户在不同台账可以有完全不同
//     的模块权限。
//   - **超级管理员在业务数据上没有特权**：不是某个台账的成员就看不到、改不了
//     它的数据（管理后台只做账号审核、系统配置、备份、操作日志）。
//     口径限定在**业务界面/业务接口**上：整库备份与备份下载属框架系统级能力，
//     仍对超管开放（服务器持有者本来就能直接读 SQLite 文件，把这条堵在 HTTP 上
//     只是安全剧场）；要收紧就单独设计「备份加密或降权」，不在这套模型里。
//
// 与账单应用的差异（有意保留）：账单的成员只有 owner/member 两档，本项目沿用
// 自己的**模块权限**（房源/租户/抄表/账单/收款/统计 各 view/edit/full），
// 管理者可以按人细配——需求里明确要「给加入的人设置权限」。
package rental

import (
	"encoding/json"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 台账类型。
const (
	LedgerKindPersonal = "personal"
	LedgerKindShared   = "shared"
)

// 成员角色：创建者是 owner（管理者），被邀请的人是 member。
const (
	MemberRoleOwner  = "owner"
	MemberRoleMember = "member"
)

// 成员状态：邀请先落 pending，被邀请人接受后 active、拒绝后 rejected。
const (
	MemberStatusPending  = "pending"
	MemberStatusActive   = "active"
	MemberStatusRejected = "rejected"
)

// LegacySharedLedgerID / LegacySharedLedgerName 是 0.5.1「组织」时代内置的
// 0 号「公共台账」。**只给迁移用**：0.5.2 迁移把它转成一条普通的共同台账
// （管理者=最早的管理员，成员与权限原样保留），新代码不再对它做任何特殊处理。
const (
	LegacySharedLedgerID   uint = 0
	LegacySharedLedgerName      = "公共台账"
)

// PersonalLedgerName 是自动创建的个人台账名（前端切换器里显示）。
const PersonalLedgerName = "我的台账"

// Ledger 是一个数据归属单元。
type Ledger struct {
	ID   uint   `gorm:"primarykey" json:"id"`
	Name string `gorm:"not null" json:"name"`
	// Kind 见 LedgerKind*：个人台账 / 共同台账。
	Kind string `gorm:"size:16;not null;default:shared;index" json:"kind"`
	// OwnerUserID 是共同台账的管理者（个人台账即本人）；成员权限判定不看它，
	// 但「谁能邀请/移除/改名」看它，冗余一份便于按 owner 查个人台账。
	OwnerUserID uint      `gorm:"not null;default:0;index" json:"owner_user_id"`
	Notes       string    `json:"notes"`
	CreatedBy   uint      `json:"created_by"` // 创建者，仅溯源
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// LedgerMember 是「用户 ↔ 台账」成员关系，也是**该用户在该台账的权限载体**。
//
// 一行同时承载「邀请」与「成员」：status=pending 是待接受的邀请，
// active 是正式成员，rejected 是被拒绝过的邀请（管理者可再次邀请，行复用）。
type LedgerMember struct {
	LedgerID  uint      `gorm:"column:org_id;primarykey" json:"ledger_id"`
	UserID    uint      `gorm:"primarykey;index:idx_ledger_member_user" json:"user_id"`
	Role      string    `gorm:"size:16;not null;default:member" json:"role"`
	Status    string    `gorm:"size:16;not null;default:active;index" json:"status"`
	Perms     string    `json:"perms"` // 模块勾选 JSON，形如 {"rooms":"view"}
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName 固定物理表名。
//
// 0.5.1 把「台账」这个概念引入时，物理表叫 orgs / user_orgs、业务表的归属列叫
// org_id；0.5.2 改成「个人台账 + 共同台账」的模型（语义、API、界面全部叫
// 台账），但**不动物理名**：升级阶梯跑在 AutoMigrate 之后，若这里换名，
// 老库会先被 AutoMigrate 建出空的新表/新列，再叠加一次重命名，既复杂又
// 容易在唯一索引上撞车。物理名保持不动，代价只是这四行注释。
func (Ledger) TableName() string { return "orgs" }

// TableName 见 Ledger.TableName 的说明。
func (LedgerMember) TableName() string { return "user_orgs" }

func init() {
	database.RegisterModels(&Ledger{}, &LedgerMember{})

	// 每个新账号自动获得一个**个人台账**（自己是管理者 + 全部模块完全权限），
	// 默认就是数据隔离；共同台账要用户自己建、自己邀请人。
	//
	// 靠钩子而不是改框架 user 包：框架层不该知道 rental 的表；钩子在注册事务里
	// 执行，失败即回滚，不会留下「注册成功却没有归属」的半残账号。
	database.RegisterUserCreateHook(func(db *gorm.DB, userID uint) error {
		_, err := ensurePersonalLedger(db, userID)
		return err
	})
}

// ---------------------------------------------------------------------------
// 权限原语（basis）：模块 × 档位，与 access.go 的 moduleKeys/permRank 同一套词。
// ---------------------------------------------------------------------------

// LedgerPerms 解析成员行的模块勾选；空/非法按全无（故障宁可关上也不敞开）。
func (m *LedgerMember) LedgerPerms() ModulePerms {
	return parsePerms(m.Perms)
}

// marshalPerms 把归一化后的模块勾选序列化成存库 JSON（空勾选存空串）。
func marshalPerms(perms ModulePerms) (string, error) {
	if len(perms) == 0 {
		return "", nil
	}
	buf, err := json.Marshal(perms)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// normalizePerms 只保留合法模块与合法档位，丢弃其余键。
func normalizePerms(in ModulePerms) ModulePerms {
	out := ModulePerms{}
	for _, k := range moduleKeys {
		if permRank[in[k]] > 0 {
			out[k] = in[k]
		}
	}
	return out
}

// fullPerms 全部模块完全权限（台账管理者与个人台账主人恒用这一份）。
func fullPerms() ModulePerms {
	out := ModulePerms{}
	for _, k := range moduleKeys {
		out[k] = PermFull
	}
	return out
}

// ---------------------------------------------------------------------------
// 成员查询
// ---------------------------------------------------------------------------

// memberOf 取用户在某台账的成员行（含待接受的邀请）；不存在返回 nil。
func memberOf(db *gorm.DB, userID, ledgerID uint) *LedgerMember {
	if userID == 0 {
		return nil
	}
	var row LedgerMember
	if err := db.Where("user_id = ? AND org_id = ?", userID, ledgerID).First(&row).Error; err != nil {
		return nil
	}
	return &row
}

// activeMemberOf 取用户在某台账的**正式成员**行；待接受/已拒绝/非成员返回 nil。
func activeMemberOf(db *gorm.DB, userID, ledgerID uint) *LedgerMember {
	m := memberOf(db, userID, ledgerID)
	if m == nil || m.Status != MemberStatusActive {
		return nil
	}
	return m
}

// isLedgerOwner 用户是否为该台账的管理者（个人台账即本人）。
func isLedgerOwner(db *gorm.DB, userID, ledgerID uint) bool {
	m := activeMemberOf(db, userID, ledgerID)
	return m != nil && m.Role == MemberRoleOwner
}

// ledgerPerms 用户在**指定台账**的模块勾选：管理者恒为全部模块完全权限，
// 正式成员按成员行勾选，其余（待接受/被拒/非成员）一律全无。
func ledgerPerms(db *gorm.DB, userID, ledgerID uint) ModulePerms {
	m := activeMemberOf(db, userID, ledgerID)
	if m == nil {
		return ModulePerms{}
	}
	if m.Role == MemberRoleOwner {
		return fullPerms()
	}
	return m.LedgerPerms()
}

// activeLedgerIDs 用户**能实际使用**的台账 ID（正式成员，含自己的个人台账），
// 按 ID 升序。
func activeLedgerIDs(db *gorm.DB, userID uint) ([]uint, error) {
	ids := make([]uint, 0)
	if userID == 0 {
		return ids, nil
	}
	err := db.Model(&LedgerMember{}).
		Where("user_id = ? AND status = ?", userID, MemberStatusActive).
		Order("org_id ASC").Pluck("org_id", &ids).Error
	return ids, err
}

// mergedPerms 用户跨全部**正式成员**台账的权限并集（每个模块取最高档）。
// 用于「没有指定台账」的读门禁与登录落地判定：只要在某个台账里有该模块的
// 查看权，就允许进这个页面（数据范围仍按 resolveLedger 收窄）。
func mergedPerms(db *gorm.DB, userID uint) ModulePerms {
	out := ModulePerms{}
	ids, err := activeLedgerIDs(db, userID)
	if err != nil {
		return out
	}
	for _, ledgerID := range ids {
		m := activeMemberOf(db, userID, ledgerID)
		if m == nil {
			continue
		}
		perms := m.LedgerPerms()
		if m.Role == MemberRoleOwner {
			perms = fullPerms()
		}
		for k, tier := range perms {
			if permRank[tier] > permRank[out[k]] {
				out[k] = tier
			}
		}
	}
	return out
}

// anyLedgerPerm 用户是否在**任一**正式成员台账里对某模块达到某档位。
func anyLedgerPerm(db *gorm.DB, userID uint, module, tier string) bool {
	return permRank[mergedPerms(db, userID)[module]] >= permRank[tier]
}

// activeLedgerIDsOrNil 同 activeLedgerIDs，但查库失败时返回 nil（调用方按
// 「无可见台账」处理，宁可拒绝也不要放行）。
func activeLedgerIDsOrNil(db *gorm.DB, userID uint) []uint {
	ids, err := activeLedgerIDs(db, userID)
	if err != nil {
		return nil
	}
	return ids
}

// moduleLedgerIDs 用户对某模块达到指定档位的台账集合：读接口据此把可见范围
// 收窄到「这个模块真的有权看」的台账，而不是全部成员台账。
func moduleLedgerIDs(db *gorm.DB, userID uint, module, tier string) []uint {
	ids, err := activeLedgerIDs(db, userID)
	if err != nil {
		return nil
	}
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if permRank[ledgerPerms(db, userID, id)[module]] >= permRank[tier] {
			out = append(out, id)
		}
	}
	return out
}

// containsLedger 判断台账 ID 是否在集合里。
func containsLedger(ids []uint, id uint) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// intersectLedgers 取两个台账集合的交集（保持 base 的顺序）。
func intersectLedgers(base, allowed []uint) []uint {
	if len(base) == 0 || len(allowed) == 0 {
		return []uint{}
	}
	out := make([]uint, 0, len(base))
	for _, id := range base {
		if containsLedger(allowed, id) {
			out = append(out, id)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// 个人台账
// ---------------------------------------------------------------------------

// personalLedgerOf 取用户的个人台账；没有（历史账号/异常数据）返回 nil。
func personalLedgerOf(db *gorm.DB, userID uint) *Ledger {
	if userID == 0 {
		return nil
	}
	var l Ledger
	err := db.Where("kind = ? AND owner_user_id = ?", LedgerKindPersonal, userID).
		Order("id ASC").First(&l).Error
	if err != nil {
		return nil
	}
	return &l
}

// ensurePersonalLedger 保证用户有个人台账（幂等）：没有就建，并写一行
// role=owner / status=active / 全部模块完全权限的成员关系。
//
// 注册钩子（见 init）与「历史账号补建」都走这里；同一事务内重复调用安全。
func ensurePersonalLedger(db *gorm.DB, userID uint) (*Ledger, error) {
	if l := personalLedgerOf(db, userID); l != nil {
		// 成员行可能因历史数据缺失，一并补齐。
		if m := memberOf(db, userID, l.ID); m == nil {
			perms, err := marshalPerms(fullPerms())
			if err != nil {
				return nil, err
			}
			if err := db.Create(&LedgerMember{
				LedgerID: l.ID, UserID: userID,
				Role: MemberRoleOwner, Status: MemberStatusActive, Perms: perms,
			}).Error; err != nil {
				return nil, err
			}
		}
		return l, nil
	}
	perms, err := marshalPerms(fullPerms())
	if err != nil {
		return nil, err
	}
	ledger := Ledger{
		Name: PersonalLedgerName, Kind: LedgerKindPersonal,
		OwnerUserID: userID, CreatedBy: userID,
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&ledger).Error; err != nil {
			return err
		}
		return tx.Create(&LedgerMember{
			LedgerID: ledger.ID, UserID: userID,
			Role: MemberRoleOwner, Status: MemberStatusActive, Perms: perms,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &ledger, nil
}

// ---------------------------------------------------------------------------
// 台账列表与视图
// ---------------------------------------------------------------------------

// LedgerView 是台账出参：附带当前用户在这个台账里的角色/状态/权限，
// 前端据此渲染台账切换器、按钮门禁与「待接受邀请」。
type LedgerView struct {
	ID          uint        `json:"id"`
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`
	OwnerUserID uint        `json:"owner_user_id"`
	OwnerName   string      `json:"owner_name"`
	Role        string      `json:"role"`   // owner / member
	Status      string      `json:"status"` // active / pending / rejected
	Perms       ModulePerms `json:"perms"`  // 我在本台账的模块勾选
	MemberCount int64       `json:"member_count"`
}

// visibleLedgers 返回当前用户可见的台账：自己的个人台账 + 自己参与（含待接受
// 邀请）的共同台账。**管理员看全部**这条规则已被删除——业务数据上超管无特权。
// 排序：个人台账永远第一，其余按 ID 升序。
func visibleLedgers(db *gorm.DB, userID uint) ([]LedgerView, error) {
	members := make([]LedgerMember, 0)
	if err := db.Where("user_id = ?", userID).
		Where("status <> ?", MemberStatusRejected).
		Order("org_id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return []LedgerView{}, nil
	}
	ids := make([]uint, 0, len(members))
	for _, m := range members {
		ids = append(ids, m.LedgerID)
	}
	var ledgers []Ledger
	if err := db.Where("id IN ?", ids).Order("id ASC").Find(&ledgers).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]LedgerMember, len(members))
	for _, m := range members {
		byID[m.LedgerID] = m
	}
	out := make([]LedgerView, 0, len(ledgers))
	for _, l := range ledgers {
		m := byID[l.ID]
		perms := m.LedgerPerms()
		if m.Role == MemberRoleOwner {
			perms = fullPerms()
		}
		var count int64
		db.Model(&LedgerMember{}).
			Where("org_id = ? AND status = ?", l.ID, MemberStatusActive).Count(&count)
		out = append(out, LedgerView{
			ID: l.ID, Name: l.Name, Kind: l.Kind,
			OwnerUserID: l.OwnerUserID, OwnerName: usernameOf(db, l.OwnerUserID),
			Role: m.Role, Status: m.Status, Perms: perms, MemberCount: count,
		})
	}
	// 个人台账排最前。
	sortLedgersPersonalFirst(out)
	return out, nil
}

// usernameOf 取用户名（展示用）；查不到返回空串。
func usernameOf(db *gorm.DB, userID uint) string {
	if userID == 0 {
		return ""
	}
	var name string
	db.Table("users").Select("username").Where("id = ?", userID).Scan(&name)
	return name
}

// sortLedgersPersonalFirst 个人台账排第一，其余保持 ID 升序。
func sortLedgersPersonalFirst(list []LedgerView) {
	for i := 0; i < len(list); i++ {
		if list[i].Kind != LedgerKindPersonal {
			continue
		}
		if i > 0 {
			item := list[i]
			copy(list[1:i+1], list[0:i])
			list[0] = item
		}
		return
	}
}

// ledgerExists 台账是否存在。
func ledgerExists(db *gorm.DB, ledgerID uint) bool {
	var count int64
	db.Model(&Ledger{}).Where("id = ?", ledgerID).Count(&count)
	return count > 0
}

// ledgerHasBusinessData 台账里是否已经有业务数据（删台账/删账号的前置检查）。
func ledgerHasBusinessData(db *gorm.DB, ledgerID uint) bool {
	for _, t := range ledgerScopedTables {
		if !tableExists(db, t) {
			continue
		}
		var n int64
		if err := db.Table(t).Where("org_id = ?", ledgerID).Count(&n).Error; err != nil {
			continue
		}
		if n > 0 {
			return true
		}
	}
	return false
}

// businessDataLedgerIDs 全部「已经有业务数据」的台账 ID（清理时保住它们）。
func businessDataLedgerIDs(db *gorm.DB) []uint {
	seen := map[uint]bool{}
	out := make([]uint, 0)
	for _, t := range ledgerScopedTables {
		if !tableExists(db, t) {
			continue
		}
		part := make([]uint, 0)
		if err := db.Table(t).Distinct().Pluck("org_id", &part).Error; err != nil {
			continue
		}
		for _, id := range part {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// userOwnsLedgerWithData 是「删账号」的守卫判定：这个账号名下有没有不能
// 随他一起消失的台账。
//   - 他创建的共同台账：创建者就是管理者，本版没有转让功能，删号会让台账
//     无主（其他成员谁也改不了设置、加不了人）→ 一律拦下；
//   - 他的个人台账里已经有业务数据 → 拦下，免得数据变成孤儿。
//
// 其它情况（只有空台账）放行，清理钩子会把空台账一并删掉。
func userOwnsLedgerWithData(db *gorm.DB, userID uint) (bool, error) {
	owned := make([]Ledger, 0)
	if err := db.Where("owner_user_id = ?", userID).Find(&owned).Error; err != nil {
		return false, err
	}
	for _, l := range owned {
		if l.Kind == LedgerKindShared {
			return true, nil
		}
		if ledgerHasBusinessData(db, l.ID) {
			return true, nil
		}
	}
	return false, nil
}

// ---------------------------------------------------------------------------
// 请求上下文里的台账解析
// ---------------------------------------------------------------------------

// ledgerIDKey / ledgerIDsKey 是 context 键：resolveLedger 中间件把解析结果放进去，
// 各 handler 与门禁通过 ledgerScope(c) 读取。
const (
	ledgerIDKey       = "rentalLedgerID"       // 写入目标台账（单个）
	ledgerIDsKey      = "rentalLedgerIDs"      // 可见台账集合（查询范围）
	ledgerExplicitKey = "rentalLedgerExplicit" // 是否由 ?ledger_id 显式指定（false = 回落到个人台账）
)

// ledgerScope 取本次请求的查询范围与写入目标。
//   - ids   ：查询应过滤 ledger_id IN ids
//   - write ：写入应落的台账
//   - single：是否显式指定了单个台账（true 时 ids 只有一个元素）
func ledgerScope(c *gin.Context) (ids []uint, write uint, single bool) {
	if v, ok := c.Get(ledgerIDsKey); ok {
		ids, _ = v.([]uint)
	}
	if v, ok := c.Get(ledgerIDKey); ok {
		write, _ = v.(uint)
	}
	_, single = c.Get(ledgerIDKey)
	return
}

// visibleLedgerIDs 便捷取查询范围。
func visibleLedgerIDs(c *gin.Context) []uint {
	ids, _, _ := ledgerScope(c)
	return ids
}

// resolveLedger 解析本次请求的台账范围并放进 context。挂在业务路由组上。
//
// 规则：
//   - 显式 ?ledger_id=N：必须是该台账的**正式成员**（管理员也一样），
//     否则 403 —— 台账之间数据不互通，超管没有数据特权；
//   - 未指定：查询范围 = 我的全部正式成员台账；写入目标 = 我的个人台账
//     （前端永远会带 ledger_id，这里是兜底，保证「随手新建」也落在自己的
//     个人台账里，不会写进别人的共同台账）。
func resolveLedger(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("userID")

		if raw := strings.TrimSpace(c.Query("ledger_id")); raw != "" {
			ledgerID, valid := parseLedgerID(raw)
			if !valid {
				response.ErrorBadRequest(c, "无效的台账 ID")
				c.Abort()
				return
			}
			m := activeMemberOf(db, userID, ledgerID)
			if m == nil {
				response.ErrorForbidden(c, "没有该台账的访问权限")
				c.Abort()
				return
			}
			c.Set(ledgerIDKey, ledgerID)
			c.Set(ledgerIDsKey, []uint{ledgerID})
			c.Set(ledgerExplicitKey, true)
			c.Next()
			return
		}

		ids, err := activeLedgerIDs(db, userID)
		if err != nil {
			response.ErrorInternal(c, "查询台账归属失败")
			c.Abort()
			return
		}
		c.Set(ledgerIDsKey, ids)
		c.Set(ledgerExplicitKey, false)
		if p := personalLedgerOf(db, userID); p != nil {
			c.Set(ledgerIDKey, p.ID)
		}
		c.Next()
	}
}

// parseLedgerID 解析台账 ID；0 是合法的（0.5.1 迁移过来的公共台账）。
func parseLedgerID(raw string) (uint, bool) {
	if raw == "" {
		return 0, false
	}
	var n uint64
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		n = n*10 + uint64(ch-'0')
		if n > 1<<32 {
			return 0, false
		}
	}
	return uint(n), true
}
