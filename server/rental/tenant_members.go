// tenant_members.go — 住户成员：挂在租户（计费主体）名下的同住人。
//
// 设计见仓库内 docs/08-租户与住户成员设计.md，一句话口径：
// **账单里只有租户，户口本上才有住户成员。**
//
// 为什么归属键取 tenant_id 而不是 room_id（用户原话是「用户关联房源」）：
//   - 费用主体唯一：成员若直接挂房源，就会出现「没有租户的住户记录」，他算不算
//     在住、退租时谁带走他都没有答案；挂租户语义天然闭合；
//   - 一房多租户是既有且真实支持的场景（宿舍/合租，tenants.room_id 不是唯一键），
//     成员挂房源就分不清是谁的家属；
//   - 换房在应用里就是改 tenants.room_id（同台账内），成员跟着租户走，零同步成本。
// 房间维度只是聚合视图：列表接口支持 ?room_id=（JOIN tenants 的一次查询），
// 房源详情据此一次取回该房全部租户的成员。
package rental

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// memberMaxPerTenant 每个租户最多登记多少名同住人：宿舍场景够用，同时挡住误操作
// 批量灌数据。
const memberMaxPerTenant = 30

// idCardPattern 身份证号：18 位（末位数字或 X）或 15 位老号段。
var idCardPattern = regexp.MustCompile(`^(\d{17}[\dX]|\d{15})$`)

// TenantMember 住户成员。
//
// Gender/Birthday 是**由身份证号派生的快照**（见 deriveFromIDCard）：录入时算一次
// 落库，界面上直接用；不作为计费、权限或登录依据。
type TenantMember struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	UserID      uint      `gorm:"index" json:"-"`                                          // 录入人，仅溯源
	LedgerID    uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账（恒等于所属租户的台账）
	TenantID    uint      `gorm:"index;not null" json:"tenant_id"`                         // 所属租户（计费主体）
	Name        string    `gorm:"not null" json:"name"`
	Relation    string    `json:"relation"`                              // 与租户关系（自由文本，前端给常用选项）
	Phone       string    `json:"phone"`                                 // 选填
	IDCard      string    `json:"id_card"`                               // 选填
	Gender      string    `gorm:"default:''" json:"gender"`              // male / female / ''（派生）
	Birthday    string    `gorm:"default:''" json:"birthday"`            // YYYY-MM-DD（派生）
	MoveInDate  string    `json:"move_in_date"`                          // 选填
	MoveOutDate string    `json:"move_out_date"`                         // 退租时自动补
	Active      bool      `gorm:"default:true" json:"active"`            // 是否在住
	Notes       string    `json:"notes"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func init() {
	database.RegisterModels(&TenantMember{})
}

func setupTenantMemberRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 住户成员是租户档案的从属数据，权限走「租户与合同」模块：
	// 查看档能看，操作档能建能改，完全档才能删（与删租户档案本身同一口径）。
	view := requireModule(db, ModuleTenants, PermView)
	edit := requireLedgerModule(db, ModuleTenants, PermEdit)
	full := requireLedgerModule(db, ModuleTenants, PermFull)
	api.GET("/rental/tenant-members", view, handleMemberList(db))
	api.POST("/rental/tenants/:id/members", edit, handleMemberCreate(db))
	api.PUT("/rental/tenant-members/:id", edit, handleMemberUpdate(db))
	api.DELETE("/rental/tenant-members/:id", full, handleMemberDelete(db))
}

// memberRequest 成员入参。Active 用指针：没传时「建」按在住、改则保持原值。
// ID 只在「登记/编辑租户表单里一起提交」时出现：带 id = 改这一行，不带 = 新增，
// 没出现在数组里的成员会被删除（表单即真相，见 syncTenantMembers）。
type memberRequest struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Relation    string `json:"relation"`
	Phone       string `json:"phone"`
	IDCard      string `json:"id_card"`
	MoveInDate  string `json:"move_in_date"`
	MoveOutDate string `json:"move_out_date"`
	Active      *bool  `json:"active"`
	Notes       string `json:"notes"`
}

// memberPayloadError 表单里的成员数据不合法：回 400 而不是 500，并且让外层事务回滚
// （租户与成员必须一起成功，不能出现「租户建好了、成员没进去」）。
type memberPayloadError struct{ msg string }

func (e memberPayloadError) Error() string { return e.msg }

// handleMemberList 列出可见台账内的住户成员。
// 筛选：tenant_id（某租户的成员）、room_id（某房源全部租户的成员）、active（1/0/空）。
func handleMemberList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, empty := scopeLedger(db.Model(&TenantMember{}), c)
		if empty {
			response.Success(c, []TenantMember{})
			return
		}
		if tenantID := atoiQuery(c, "tenant_id"); tenantID > 0 {
			query = query.Where("tenant_id = ?", tenantID)
		}
		if roomID := atoiQuery(c, "room_id"); roomID > 0 {
			// 房间维度：该房全部租户的成员，一次 JOIN 取回。房源限定在可见台账内，
			// 避免跨台账的孤儿租户借同名房间串进来。
			query = query.Where(
				"tenant_id IN (SELECT id FROM tenants WHERE room_id = ? AND org_id IN ?)",
				roomID, visibleLedgerIDs(c))
		}
		switch strings.TrimSpace(c.Query("active")) {
		case "1":
			query = query.Where("active = ?", true)
		case "0":
			query = query.Where("active = ?", false)
		}
		items := make([]TenantMember, 0)
		if err := query.Order("active DESC, id ASC").Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询住户成员失败")
			return
		}
		response.Success(c, items)
	}
}

func handleMemberCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		var req memberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写成员姓名")
			return
		}
		if msg := validateMemberRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		var existing int64
		mq, _ := scopeLedger(db.Where("tenant_id = ?", tenant.ID), c)
		mq.Model(&TenantMember{}).Count(&existing)
		if int(existing) >= memberMaxPerTenant {
			response.ErrorBadRequest(c, fmt.Sprintf("每户最多登记 %d 名同住成员", memberMaxPerTenant))
			return
		}
		if memberIDCardTaken(db, tenant.LedgerID, tenant.ID, req.IDCard, 0) {
			response.ErrorBadRequest(c, "该租户下已有相同身份证号的成员，请勿重复登记")
			return
		}
		member := TenantMember{
			UserID:   currentUserID(c),
			LedgerID: tenant.LedgerID,
			TenantID: tenant.ID,
		}
		applyMemberRequest(&member, &req)
		if err := db.Create(&member).Error; err != nil {
			response.ErrorInternal(c, "保存住户成员失败")
			return
		}
		response.Success(c, member)
	}
}

func handleMemberUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		member, ok := findUserMember(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, member.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		var req memberRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请填写成员姓名")
			return
		}
		if msg := validateMemberRequest(&req); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		if memberIDCardTaken(db, member.LedgerID, member.TenantID, req.IDCard, member.ID) {
			response.ErrorBadRequest(c, "该租户下已有相同身份证号的成员，请勿重复登记")
			return
		}
		applyMemberRequest(member, &req)
		if err := db.Save(member).Error; err != nil {
			response.ErrorInternal(c, "保存住户成员失败")
			return
		}
		response.Success(c, member)
	}
}

func handleMemberDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		member, ok := findUserMember(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, member.LedgerID, ModuleTenants, PermFull) {
			return
		}
		if err := db.Delete(&TenantMember{}, member.ID).Error; err != nil {
			response.ErrorInternal(c, "删除住户成员失败")
			return
		}
		response.Success(c, gin.H{"deleted": member.ID})
	}
}

// findUserMember 取路径上的成员并校验台账可见性（不可见按「不存在」处理）。
func findUserMember(db *gorm.DB, c *gin.Context) (*TenantMember, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的成员 ID")
		return nil, false
	}
	var member TenantMember
	if err := db.First(&member, id).Error; err != nil {
		response.ErrorNotFound(c, "住户成员不存在")
		return nil, false
	}
	if !inLedgerScope(c, member.LedgerID) {
		response.ErrorNotFound(c, "住户成员不存在")
		return nil, false
	}
	return &member, true
}

// validateMemberRequest 校验并归一化入参，返回空串表示通过。
func validateMemberRequest(req *memberRequest) string {
	req.Name = strings.TrimSpace(req.Name)
	req.Relation = strings.TrimSpace(req.Relation)
	req.Phone = strings.TrimSpace(req.Phone)
	req.IDCard = strings.ToUpper(strings.TrimSpace(req.IDCard))
	req.MoveInDate = strings.TrimSpace(req.MoveInDate)
	req.MoveOutDate = strings.TrimSpace(req.MoveOutDate)
	req.Notes = strings.TrimSpace(req.Notes)

	if req.Name == "" {
		return "请填写成员姓名"
	}
	if len([]rune(req.Name)) > 30 {
		return "成员姓名不能超过 30 个字"
	}
	if len([]rune(req.Relation)) > 20 {
		return "与租户关系不能超过 20 个字"
	}
	if len([]rune(req.Phone)) > 20 {
		return "手机号不能超过 20 个字"
	}
	if len([]rune(req.Notes)) > 200 {
		return "备注不能超过 200 个字"
	}
	if req.IDCard != "" && !idCardPattern.MatchString(req.IDCard) {
		return "身份证号应为 18 位（末位可为 X）或 15 位"
	}
	if req.MoveInDate != "" && !isValidDay(req.MoveInDate) {
		return "入住日期格式应为 YYYY-MM-DD"
	}
	if req.MoveOutDate != "" && !isValidDay(req.MoveOutDate) {
		return "搬离日期格式应为 YYYY-MM-DD"
	}
	return ""
}

// applyMemberRequest 把校验过的入参落到成员行，并派生性别与出生日期。
func applyMemberRequest(m *TenantMember, req *memberRequest) {
	m.Name = req.Name
	m.Relation = req.Relation
	m.Phone = req.Phone
	m.IDCard = req.IDCard
	m.Notes = req.Notes
	m.MoveInDate = req.MoveInDate
	m.MoveOutDate = req.MoveOutDate
	// 身份证号变化即重新派生；清空身份证号时派生字段一并清空。
	m.Birthday, m.Gender = deriveFromIDCard(req.IDCard)

	switch {
	case req.Active != nil:
		m.Active = *req.Active
	case m.ID == 0:
		// 新建：直接填了搬离日期，就按「已搬离」建档（补录历史同住人）。
		m.Active = req.MoveOutDate == ""
	case req.MoveOutDate != "":
		// 修改：补上了搬离日期，自动收尾为已搬离——否则会出现「已搬离但仍在住」的矛盾记录。
		m.Active = false
	}
}

// deriveFromIDCard 从 18 位身份证号派生出生日期与性别。
// 15 位老号段不猜（编码规则与校验位都不同），只做长度校验，派生字段留空。
func deriveFromIDCard(idCard string) (birthday, gender string) {
	if len(idCard) != 18 {
		return "", ""
	}
	if _, err := time.ParseInLocation("20060102", idCard[6:14], time.Local); err != nil {
		return "", ""
	}
	birthday = fmt.Sprintf("%s-%s-%s", idCard[6:10], idCard[10:12], idCard[12:14])
	if (idCard[16]-'0')%2 == 1 {
		gender = "male"
	} else {
		gender = "female"
	}
	return birthday, gender
}

// isValidDay 校验 YYYY-MM-DD 日期。
func isValidDay(s string) bool {
	_, err := time.ParseInLocation("2006-01-02", s, time.Local)
	return err == nil
}

// memberIDCardTaken 同一租户下是否已有相同身份证号的成员（空号段视为无证件，不判重）。
func memberIDCardTaken(db *gorm.DB, ledgerID, tenantID uint, idCard string, excludeID uint) bool {
	if idCard == "" {
		return false
	}
	q := db.Model(&TenantMember{}).
		Where("org_id = ? AND tenant_id = ? AND id_card = ?", ledgerID, tenantID, idCard)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var n int64
	q.Count(&n)
	return n > 0
}

// atoiQuery 读一个整数查询参数（非法按 0）。
func atoiQuery(c *gin.Context, key string) int {
	v, err := strconv.Atoi(strings.TrimSpace(c.Query(key)))
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// validateMemberPayload 校验一整个成员数组（登记/编辑租户表单里一起提交的那份）。
// 纯校验、不碰库：先在事务外把整份数据拦下来，再进事务落库。
func validateMemberPayload(reqs []memberRequest) string {
	if len(reqs) > memberMaxPerTenant {
		return fmt.Sprintf("每户最多登记 %d 名同住成员", memberMaxPerTenant)
	}
	seenIDCard := map[string]int{}
	for i := range reqs {
		req := &reqs[i]
		if msg := validateMemberRequest(req); msg != "" {
			return fmt.Sprintf("第 %d 位成员：%s", i+1, msg)
		}
		if req.IDCard != "" {
			if first, dup := seenIDCard[req.IDCard]; dup {
				return fmt.Sprintf("第 %d 位成员与第 %d 位的身份证号重复，请核对", i+1, first)
			}
			seenIDCard[req.IDCard] = i + 1
		}
	}
	return ""
}

// applyMemberPayload 在事务里把表单里的成员落到库：带 id 的改、不带 id 的新增、
// 表单里没有的删除（**表单即真相**——用户在登记/编辑页面删掉一行并保存，就该删掉）。
// 只动本租户 + 本台账的成员；id 不属于本租户时按非法入参处理（防越权改别人的成员）。
func applyMemberPayload(tx *gorm.DB, ledgerID, tenantID, userID uint, reqs []memberRequest) error {
	var existing []TenantMember
	if err := tx.Where("tenant_id = ? AND org_id = ?", tenantID, ledgerID).Find(&existing).Error; err != nil {
		return err
	}
	owned := make(map[uint]bool, len(existing))
	for _, m := range existing {
		owned[m.ID] = true
	}

	keptIDs := make([]uint, 0, len(reqs))
	for i := range reqs {
		req := &reqs[i]
		if req.ID > 0 {
			if !owned[req.ID] {
				return memberPayloadError{msg: fmt.Sprintf("第 %d 位成员不属于该租户，无法修改", i+1)}
			}
			var m TenantMember
			if err := tx.First(&m, req.ID).Error; err != nil {
				return err
			}
			applyMemberRequest(&m, req)
			if err := tx.Save(&m).Error; err != nil {
				return err
			}
			keptIDs = append(keptIDs, m.ID)
			continue
		}
		m := TenantMember{UserID: userID, LedgerID: ledgerID, TenantID: tenantID}
		applyMemberRequest(&m, req)
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		keptIDs = append(keptIDs, m.ID)
	}

	del := tx.Where("tenant_id = ? AND org_id = ?", tenantID, ledgerID)
	if len(keptIDs) > 0 {
		del = del.Where("id NOT IN ?", keptIDs)
	}
	return del.Delete(&TenantMember{}).Error
}

// deleteTenantMembers 删租户档案时一并清理其成员（成员没有附件，无孤儿文件问题）。
func deleteTenantMembers(db *gorm.DB, tenantID uint, ledgerID uint) {
	db.Where("tenant_id = ? AND org_id = ?", tenantID, ledgerID).Delete(&TenantMember{})
}

// checkoutTenantMembers 租户退租时把名下成员一并置为已搬离。
// 用两条 UPDATE 而不是逐行 Save：SQLite 单连接，批量场景要避免 N 次写。
// 行保留（历史可查），与「退租的租户档案保留 active=false」同一口径。
func checkoutTenantMembers(db *gorm.DB, tenantID uint, ledgerID uint, moveOutDate string) {
	if strings.TrimSpace(moveOutDate) == "" {
		moveOutDate = time.Now().Format("2006-01-02")
	}
	db.Model(&TenantMember{}).
		Where("tenant_id = ? AND org_id = ? AND active = ?", tenantID, ledgerID, true).
		Update("active", false)
	// 搬离日期只给空着的补，不覆盖房东手填的日期。
	db.Model(&TenantMember{}).
		Where("tenant_id = ? AND org_id = ? AND (move_out_date = '' OR move_out_date IS NULL)", tenantID, ledgerID).
		Update("move_out_date", moveOutDate)
}
