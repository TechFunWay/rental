// tenant_change.go — 租户资料变更申请：提交、撤回、房东审核（v0.6.10）。
//
// 需求口径：「租户能改自己的登记信息，但是改完需要出租人审核才覆盖正式数据」。
// 因此这条链路**不直接写正式档案**：租户提交 → 落一条申请单 → 房东审核 → 通过才
// 覆盖。可改字段是白名单（手机号/证件号/住址/紧急联系人/紧急联系人电话），
// 姓名、房间、租金、租期、押金、计费设置一律不可改——它们直接决定账务口径。
//
// 覆盖语义（关键）：**只覆盖非空字段**。申请里留空的项表示「本次不改」，
// 不把正式数据清空（防止租户只改手机号时把证件号一起抹掉）。想显式清空某一项，
// 由房东在租户编辑弹窗里改——保留房东的最终决定权。
//
// 照片的处理：租户本次上传的证件照先落在**申请专属目录**
// （attachments/ledger-<id>/apply-change/<申请id>/），通过才挪进该租户的正式附件，
// 驳回/撤回即删除。未审核的证件照不该出现在房东看到的正式资料里。
package rental

import (
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"smallgo/server/audit"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// changeRequestView 申请出参：附上**正式档案当前值**，供房东逐项对照。
//
// 为什么要带正式值而不是让前端另拉一次租户档案：审核面板要并排显示「现在是什么
// → 申请改成什么」，分两次请求会有一致性窗口（中间房东恰好改了档案，对照就错了）。
type changeRequestView struct {
	TenantChangeRequest
	TenantName              string   `json:"tenant_name"`
	RoomLabel               string   `json:"room_label"`
	Applicant               string   `json:"applicant"`     // 申请人账号名
	PhotoURLs               []string `json:"photo_urls"`    // 本次上传照片的预览地址（审核面板用）
	CurrentPhone            string   `json:"current_phone"` // 正式档案当前值（对照用）
	CurrentIDCard           string   `json:"current_id_card"`
	CurrentAddress          string   `json:"current_address"`
	CurrentEmergencyContact string   `json:"current_emergency_contact"`
	CurrentEmergencyPhone   string   `json:"current_emergency_phone"`
}

// ---------------------------------------------------------------------------
// 租户侧
// ---------------------------------------------------------------------------

// handlePortalChangeList 我的变更申请历史（按时间倒序，含当前待审的那条）。
func handlePortalChangeList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		rows := make([]TenantChangeRequest, 0, 10)
		if err := db.Where("tenant_id = ? AND org_id = ?", t.ID, t.LedgerID).
			Order("created_at DESC, id DESC").Limit(50).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询变更申请失败")
			return
		}
		response.Success(c, rows)
	}
}

// handlePortalChangeSubmit 提交资料变更申请（multipart：字段 + 最多 3 张照片）。
//
// 校验顺序刻意是「先挡住重复提交、再校验内容」：租户在等待审核期间反复点提交
// 是最常见的误用，提示「已有一条待审」比逐项报格式错更有用。
func handlePortalChangeSubmit(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		userID := c.GetUint("userID")

		// 一条档案同时只允许一条待审申请（迁移建的部分唯一索引是最终兜底，
		// 这里先查一次是为了给出可读的提示而不是数据库报错）。
		var pendingCount int64
		db.Model(&TenantChangeRequest{}).
			Where("tenant_id = ? AND status = ?", t.ID, ChangeStatusPending).Count(&pendingCount)
		if pendingCount > 0 {
			response.ErrorBadRequest(c, "你已有一条待审核的变更申请，请等房东处理，或先撤回再重新提交")
			return
		}

		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "提交内容格式有误")
			return
		}
		req := TenantChangeRequest{
			Phone:            strings.TrimSpace(firstFormValue(form, "phone")),
			IDCard:           strings.TrimSpace(firstFormValue(form, "id_card")),
			Address:          strings.TrimSpace(firstFormValue(form, "address")),
			EmergencyContact: strings.TrimSpace(firstFormValue(form, "emergency_contact")),
			EmergencyPhone:   strings.TrimSpace(firstFormValue(form, "emergency_phone")),
			Note:             strings.TrimSpace(firstFormValue(form, "note")),
		}
		for label, v := range map[string]string{
			"手机号":    req.Phone,
			"身份证号":   req.IDCard,
			"住址":     req.Address,
			"紧急联系人":  req.EmergencyContact,
			"紧急联系电话": req.EmergencyPhone,
			"留言":     req.Note,
		} {
			if len([]rune(v)) > changeFieldMaxRunes {
				response.ErrorBadRequest(c, label+"过长")
				return
			}
		}
		if req.Phone != "" && !applyPhoneRe.MatchString(req.Phone) {
			response.ErrorBadRequest(c, "请填写正确的手机号")
			return
		}

		photos := append(form.File["photos"], form.File["photo"]...)
		if len(photos) > changePhotoMax {
			response.ErrorBadRequest(c, fmt.Sprintf("最多上传 %d 张照片", changePhotoMax))
			return
		}
		if req.Phone == "" && req.IDCard == "" && req.Address == "" &&
			req.EmergencyContact == "" && req.EmergencyPhone == "" && len(photos) == 0 {
			response.ErrorBadRequest(c, "没有需要变更的内容")
			return
		}

		req.LedgerID = t.LedgerID
		req.TenantID = t.ID
		req.ApplicantUserID = userID
		req.Status = ChangeStatusPending

		// 先落库拿到申请 id，照片才能按申请 id 分目录暂存；照片写失败则整条
		// 申请回滚（连同已落盘的文件一起清掉），不留半成品。
		if err := db.Create(&req).Error; err != nil {
			response.ErrorBadRequest(c, "你已有一条待审核的变更申请")
			return
		}
		saved, err := saveChangePhotos(c, &req, photos)
		if err != nil {
			// saveChangePhotos 是边落盘边校验：中途失败时它会把**已落盘的那几张**
			// 一起返回（不是 nil），所以这里必须清 returned 的 saved，而不是还没
			// 赋值的 req.NewPhotos——否则先落盘的那几张会永远留在暂存目录里。
			_ = db.Delete(&req).Error
			removeChangePhotos(saved)
			response.ErrorBadRequest(c, err.Error())
			return
		}
		req.NewPhotos = saved
		if err := db.Model(&req).Update("new_photos", marshalChangePhotos(saved)).Error; err != nil {
			_ = db.Delete(&req).Error
			removeChangePhotos(saved)
			response.ErrorInternal(c, "提交失败，请稍后再试")
			return
		}

		audit.Log(db, c, "rental.tenant_change.submit", "tenant", t.ID,
			fmt.Sprintf("租户提交资料变更申请：%s", changeFieldNames(req)))
		response.Success(c, req)
	}
}

// handlePortalChangeWithdraw 撤回自己待审核的申请。
//
// 撤回不删行而是置 rejected（review_note = 租户撤回）：审核历史要留痕，房东
// 之后看到「这条是被租户自己撤掉的」比记录凭空消失更清楚。
func handlePortalChangeWithdraw(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		req, ok := portalOwnChange(db, c, t)
		if !ok {
			return
		}
		if req.Status != ChangeStatusPending {
			response.ErrorBadRequest(c, "该申请已处理过，不能撤回")
			return
		}
		now := time.Now()
		if err := db.Model(req).Updates(map[string]interface{}{
			"status":      ChangeStatusRejected,
			"review_note": "租户撤回",
			"reviewed_at": &now,
		}).Error; err != nil {
			response.ErrorInternal(c, "撤回失败")
			return
		}
		removeChangePhotos(req.NewPhotos)
		db.Model(req).Update("new_photos", marshalChangePhotos(nil))
		response.Success(c, nil)
	}
}

// handlePortalChangePhoto 租户预览自己申请里的照片（按本人档案鉴权）。
func handlePortalChangePhoto(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		t, ok := portalTenantRequired(db, c)
		if !ok {
			return
		}
		req, ok := portalOwnChange(db, c, t)
		if !ok {
			return
		}
		abs, ok := changePhotoPath(req, c.Param("idx"))
		if !ok {
			response.ErrorNotFound(c, "照片不存在")
			return
		}
		c.File(abs)
	}
}

// portalOwnChange 取本人档案下的一条变更申请；越权/不存在一律 404。
func portalOwnChange(db *gorm.DB, c *gin.Context, t *Tenant) (*TenantChangeRequest, bool) {
	id, parsed := parseLedgerID(c.Param("id"))
	if !parsed || id == 0 {
		response.ErrorNotFound(c, "变更申请不存在")
		return nil, false
	}
	var req TenantChangeRequest
	// 三重限定：申请 id + 本人档案 + 同台账。少了档案限定，换个 id 就能看/撤回
	// 别人的申请（申请 id 是全局递增的，可枚举）。
	if err := db.Where("id = ? AND tenant_id = ? AND org_id = ?", id, t.ID, t.LedgerID).
		First(&req).Error; err != nil {
		response.ErrorNotFound(c, "变更申请不存在")
		return nil, false
	}
	return &req, true
}

// changePhotoPath 校验并解析申请里第 idx 张照片的绝对路径。
func changePhotoPath(req *TenantChangeRequest, rawIdx string) (string, bool) {
	idx, parsed := parseLedgerID(rawIdx)
	if !parsed || int(idx) >= len(req.NewPhotos) {
		return "", false
	}
	rel := req.NewPhotos[idx]
	if rel == "" {
		return "", false
	}
	abs := attachmentAbsPath(rel)
	// 目录穿越防护：解析结果必须落在附件根目录内。
	if !withinAttachmentsRoot(abs) {
		return "", false
	}
	if !fileExists(abs) {
		return "", false
	}
	return abs, true
}

// saveChangePhotos 保存申请照片：落盘到申请专属目录，返回相对路径列表。
//
// 为什么暂存目录要带台账与申请 id 两级：附件根目录是共用的，按台账分一级与既有
// 附件/合同的布局一致（便于按台账清理）；再按申请 id 分一级，使「撤回/驳回时删除
// 本次全部照片」退化成删一个目录，不会误删别的申请或正式附件。
func saveChangePhotos(c *gin.Context, req *TenantChangeRequest, files []*multipart.FileHeader) ([]string, error) {
	if len(files) == 0 {
		return []string{}, nil
	}
	dir := filepath.ToSlash(filepath.Join(
		fmt.Sprintf("ledger-%d", req.LedgerID), changePhotoDir, fmt.Sprintf("%d", req.ID)))
	saved := make([]string, 0, len(files))
	for _, fh := range files {
		if fh == nil {
			continue
		}
		if fh.Size <= 0 || fh.Size > changePhotoMaxSize {
			return saved, fmt.Errorf("照片需在 10MB 以内")
		}
		src, err := fh.Open()
		if err != nil {
			return saved, fmt.Errorf("读取照片失败")
		}
		header := make([]byte, 512)
		n, readErr := src.Read(header)
		if readErr != nil && n == 0 {
			src.Close()
			return saved, fmt.Errorf("读取照片失败")
		}
		mimeType := http.DetectContentType(header[:n])
		ext, allowed := changePhotoTypes[mimeType]
		if !allowed {
			src.Close()
			return saved, fmt.Errorf("仅支持图片或 PDF 文件")
		}
		if _, err := src.Seek(0, io.SeekStart); err != nil {
			src.Close()
			return saved, fmt.Errorf("读取照片失败")
		}
		rel := filepath.ToSlash(filepath.Join(dir, uuid.New().String()+ext))
		abs := attachmentAbsPath(rel)
		if !withinAttachmentsRoot(abs) {
			src.Close()
			return saved, fmt.Errorf("照片路径非法")
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			src.Close()
			return saved, fmt.Errorf("创建存储目录失败")
		}
		dst, err := os.Create(abs)
		if err != nil {
			src.Close()
			return saved, fmt.Errorf("保存照片失败")
		}
		_, copyErr := io.Copy(dst, src)
		dst.Close()
		src.Close()
		if copyErr != nil {
			_ = os.Remove(abs)
			return saved, fmt.Errorf("保存照片失败")
		}
		saved = append(saved, rel)
	}
	return saved, nil
}

// changePhotoTypes 内容嗅探白名单 → 扩展名（与登记流/附件同口径：图片 + PDF）。
var changePhotoTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/avif":      ".avif",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

// removeChangePhotos 删除申请本次上传的照片（驳回/撤回时，隐私上不留无用证件照）。
func removeChangePhotos(photos []string) {
	for _, rel := range photos {
		if rel == "" {
			continue
		}
		abs := attachmentAbsPath(rel)
		if withinAttachmentsRoot(abs) {
			_ = os.Remove(abs)
		}
	}
}

// marshalChangePhotos 把照片路径列表序列化成**列里实际存的 JSON 文本**。
//
// 为什么不能直接把 []string 交给 Update：模型上的 `serializer:json` 只在
// GORM 走「结构体字段」写库时生效；`Update("new_photos", []string{...})` 这种
// 显式列写法会绕过序列化，把切片当成裸值写进去（实测落库是 `p1.jpg` 而不是
// `["p1.jpg"]`），随后读回来 json.Unmarshal 失败、照片列表恒为空——申请照片
// 就"提交成功但看不到"。所以写路径统一经本函数显式序列化。
//
// 空列表存 `[]` 而不是空串：与 serializer 的产物一致，读回来是空切片而非 nil。
func marshalChangePhotos(photos []string) string {
	if len(photos) == 0 {
		return "[]"
	}
	buf, err := json.Marshal(photos)
	if err != nil {
		return "[]"
	}
	return string(buf)
}

// changeFieldNames 列出本次申请改动的字段中文名（审计与列表展示用）。
func changeFieldNames(req TenantChangeRequest) string {
	names := make([]string, 0, 5)
	if strings.TrimSpace(req.Phone) != "" {
		names = append(names, "手机号")
	}
	if strings.TrimSpace(req.IDCard) != "" {
		names = append(names, "身份证号")
	}
	if strings.TrimSpace(req.Address) != "" {
		names = append(names, "住址")
	}
	if strings.TrimSpace(req.EmergencyContact) != "" {
		names = append(names, "紧急联系人")
	}
	if strings.TrimSpace(req.EmergencyPhone) != "" {
		names = append(names, "紧急联系电话")
	}
	if len(req.NewPhotos) > 0 {
		names = append(names, fmt.Sprintf("证件照 %d 张", len(req.NewPhotos)))
	}
	if len(names) == 0 {
		return "无"
	}
	return strings.Join(names, "、")
}

// ---------------------------------------------------------------------------
// 房东侧
// ---------------------------------------------------------------------------

// setupTenantAccountRoutes 房东侧的「租户登录账号」与「资料变更审核」接口。
//
// 权限：关联/审核都要**该租户所属台账**的 tenants 模块 ≥ edit（按对象台账复核，
// 见 requireTenantWriteOn）；列表要 ≥ view。挂在业务路由组内（scoped），
// 因此天然被 resolveLedger 解析台账范围，也天然被 denyTenantRole 挡住租户。
func setupTenantAccountRoutes(api *gin.RouterGroup, db *gorm.DB) {
	view := requireModule(db, ModuleTenants, PermView)

	// 关联登录账号
	api.GET("/rental/tenants/:id/account", view, handleTenantAccountGet(db))
	api.GET("/rental/tenant-accounts/candidates", requireModule(db, ModuleTenants, PermEdit), handleTenantAccountCandidates(db))
	api.POST("/rental/tenants/:id/account", requireModule(db, ModuleTenants, PermEdit), handleTenantAccountBind(db))
	api.DELETE("/rental/tenants/:id/account", requireModule(db, ModuleTenants, PermEdit), handleTenantAccountUnbind(db))

	// 资料变更审核
	api.GET("/rental/tenant-change-requests", view, handleChangeRequestList(db))
	api.GET("/rental/tenant-change-requests/pending-count", view, handleChangeRequestPendingCount(db))
	api.GET("/rental/tenant-change-requests/:id/photo/:idx", view, handleChangeRequestPhoto(db))
	api.POST("/rental/tenant-change-requests/:id/approve", requireModule(db, ModuleTenants, PermEdit), handleChangeRequestApprove(db))
	api.POST("/rental/tenant-change-requests/:id/reject", requireModule(db, ModuleTenants, PermEdit), handleChangeRequestReject(db))
}

// findLedgerChangeRequest 房东侧取一条申请，并完成**对象所属台账**的写权限复核。
//
// 用 requireLedgerWrite（而不是只看路由上的 requireModule）：路由层校验的是
// 「本次请求选中的台账」，不带 ?ledger_id 时会回落到调用者的个人台账（恒完全
// 权限）；对象却按可见台账取。不按对象台账复核，一个在共同台账里只有查看权的
// 成员去掉参数就能审核/驳回别人的申请（2026-10-11 审计复现过的同类越权）。
func findLedgerChangeRequest(db *gorm.DB, c *gin.Context, tier string) (*TenantChangeRequest, bool) {
	id, parsed := parseLedgerID(c.Param("id"))
	if !parsed || id == 0 {
		response.ErrorNotFound(c, "变更申请不存在")
		return nil, false
	}
	var req TenantChangeRequest
	q, empty := scopeLedger(db.Where("id = ?", id), c)
	if empty {
		response.ErrorNotFound(c, "变更申请不存在")
		return nil, false
	}
	if err := q.First(&req).Error; err != nil {
		response.ErrorNotFound(c, "变更申请不存在")
		return nil, false
	}
	if !requireLedgerWrite(db, c, req.LedgerID, ModuleTenants, tier) {
		return nil, false
	}
	return &req, true
}

// handleChangeRequestList 申请列表：默认只列待审，可按 status 过滤。
//
// 附上正式档案当前值供逐项对照；照片预览地址给**房东侧**接口（按台账鉴权），
// 而不是门户那条（那条只认本人账号，房东打开会 403）。
func handleChangeRequestList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, empty := scopeLedger(db.Model(&TenantChangeRequest{}), c)
		if empty {
			response.Success(c, []changeRequestView{})
			return
		}
		switch status := strings.TrimSpace(c.Query("status")); status {
		case ChangeStatusPending, ChangeStatusApproved, ChangeStatusRejected:
			q = q.Where("status = ?", status)
		default:
			// 不传时**只列待审**：这个面板的用途就是处理待办，历史记录按需再查。
			q = q.Where("status = ?", ChangeStatusPending)
		}
		rows := make([]TenantChangeRequest, 0, 32)
		if err := q.Order("created_at DESC, id DESC").Limit(200).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询变更申请失败")
			return
		}
		response.Success(c, buildChangeRequestViews(db, c, rows))
	}
}

// buildChangeRequestViews 批量补齐对照信息（租户姓名、房号、正式值、照片地址）。
//
// 全部批量查询（一条取租户、一条取房源、一条取用户名），不做 N+1——列表一次
// 最多 200 条，逐条查库在 SQLite（MaxOpenConns=1）上会把响应时间拉长到秒级。
func buildChangeRequestViews(db *gorm.DB, c *gin.Context, rows []TenantChangeRequest) []changeRequestView {
	out := make([]changeRequestView, 0, len(rows))
	if len(rows) == 0 {
		return out
	}
	tenantIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		tenantIDs = append(tenantIDs, r.TenantID)
	}
	tenants := make([]Tenant, 0, len(tenantIDs))
	tq, _ := scopeLedger(db.Where("id IN ?", tenantIDs), c)
	tq.Find(&tenants)
	tenantByID := make(map[uint]Tenant, len(tenants))
	roomIDs := make([]uint, 0, len(tenants))
	for _, t := range tenants {
		tenantByID[t.ID] = t
		roomIDs = append(roomIDs, t.RoomID)
	}
	roomLabels := map[uint]string{}
	if len(roomIDs) > 0 {
		rooms := make([]Room, 0, len(roomIDs))
		rq, _ := scopeLedger(db.Where("id IN ?", roomIDs), c)
		rq.Find(&rooms)
		for _, r := range rooms {
			roomLabels[r.ID] = roomLocationLabel(r)
		}
	}
	userIDs := make([]uint, 0, len(rows))
	for _, r := range rows {
		userIDs = append(userIDs, r.ApplicantUserID)
	}
	names := map[uint]string{}
	if len(userIDs) > 0 {
		type nameRow struct {
			ID       uint
			Username string
		}
		list := make([]nameRow, 0, len(userIDs))
		db.Table("users").Select("id, username").Where("id IN ?", userIDs).Scan(&list)
		for _, n := range list {
			names[n.ID] = n.Username
		}
	}

	for _, r := range rows {
		t := tenantByID[r.TenantID]
		view := changeRequestView{
			TenantChangeRequest:     r,
			TenantName:              t.Name,
			RoomLabel:               roomLabels[t.RoomID],
			Applicant:               names[r.ApplicantUserID],
			PhotoURLs:               changePhotoURLs(r),
			CurrentPhone:            t.Phone,
			CurrentIDCard:           t.IDCard,
			CurrentAddress:          t.Address,
			CurrentEmergencyContact: t.EmergencyContact,
			CurrentEmergencyPhone:   t.EmergencyPhone,
		}
		out = append(out, view)
	}
	return out
}

// changePhotoURLs 房东侧照片预览地址（按申请 id + 序号）。
func changePhotoURLs(req TenantChangeRequest) []string {
	urls := make([]string, 0, len(req.NewPhotos))
	for i := range req.NewPhotos {
		urls = append(urls, fmt.Sprintf("/api/rental/tenant-change-requests/%d/photo/%d", req.ID, i))
	}
	return urls
}

func handleChangeRequestPendingCount(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		q, empty := scopeLedger(db.Model(&TenantChangeRequest{}), c)
		if empty {
			response.Success(c, gin.H{"count": 0})
			return
		}
		var count int64
		if err := q.Where("status = ?", ChangeStatusPending).Count(&count).Error; err != nil {
			response.ErrorInternal(c, "查询待审数量失败")
			return
		}
		response.Success(c, gin.H{"count": count})
	}
}

// handleChangeRequestPhoto 房东侧预览申请照片（按台账鉴权，不落公开目录）。
func handleChangeRequestPhoto(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := findLedgerChangeRequest(db, c, PermView)
		if !ok {
			return
		}
		abs, ok := changePhotoPath(req, c.Param("idx"))
		if !ok {
			response.ErrorNotFound(c, "照片不存在")
			return
		}
		c.File(abs)
	}
}

// handleChangeRequestApprove 审核通过：把**非空**字段覆盖到正式档案，照片并入
// 该租户的正式附件。两步在同一事务里，失败则正式档案不变（不出现「值改了、
// 照片没进来」的半成品）。
func handleChangeRequestApprove(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := findLedgerChangeRequest(db, c, PermEdit)
		if !ok {
			return
		}
		if req.Status != ChangeStatusPending {
			response.ErrorBadRequest(c, "该申请已处理过")
			return
		}
		var tenant Tenant
		if err := db.Where("id = ? AND org_id = ?", req.TenantID, req.LedgerID).First(&tenant).Error; err != nil {
			response.ErrorNotFound(c, "租户档案不存在")
			return
		}

		// 覆盖与照片迁移必须在同一事务里。照片是**文件操作**，事务回滚不了文件，
		// 因此顺序是「先把文件挪到位（失败即整体放弃）→ 再写库」，而不是先写库
		// 再去挪文件——后者挪失败就会留下「值改了、附件缺了」的不一致。
		moved, err := moveChangePhotosToTenant(db, req, &tenant)
		if err != nil {
			// 同上：moveChangePhotosToTenant 中途失败会带着**已搬走的那几张**返回，
			// 必须把它们挪回暂存目录，否则正式附件目录里会多出没有库记录的文件。
			rollbackMovedPhotos(moved, req)
			response.ErrorInternal(c, "迁移证件照失败，本次审核未生效")
			return
		}

		now := time.Now()
		txErr := db.Transaction(func(tx *gorm.DB) error {
			updates := map[string]interface{}{}
			// 只覆盖非空字段：留空 = 本次不改，不把正式数据清空。
			if v := strings.TrimSpace(req.Phone); v != "" {
				updates["phone"] = v
			}
			if v := strings.TrimSpace(req.IDCard); v != "" {
				updates["id_card"] = v
			}
			if v := strings.TrimSpace(req.Address); v != "" {
				updates["address"] = v
			}
			if v := strings.TrimSpace(req.EmergencyContact); v != "" {
				updates["emergency_contact"] = v
			}
			if v := strings.TrimSpace(req.EmergencyPhone); v != "" {
				updates["emergency_phone"] = v
			}
			if len(updates) > 0 {
				if err := tx.Model(&Tenant{}).Where("id = ? AND org_id = ?", tenant.ID, tenant.LedgerID).
					Updates(updates).Error; err != nil {
					return err
				}
			}
			for _, att := range moved {
				if err := tx.Create(&att).Error; err != nil {
					return err
				}
			}
			return tx.Model(&TenantChangeRequest{}).Where("id = ?", req.ID).Updates(map[string]interface{}{
				"status":      ChangeStatusApproved,
				"review_note": strings.TrimSpace(req.ReviewNote),
				"reviewed_by": c.GetUint("userID"),
				"reviewed_at": &now,
				"new_photos":  marshalChangePhotos(nil),
			}).Error
		})
		if txErr != nil {
			// 事务回滚后把已挪走的文件移回申请目录，保持「库与文件一致」。
			rollbackMovedPhotos(moved, req)
			response.ErrorInternal(c, "审核失败，本次变更未生效")
			return
		}

		audit.Log(db, c, "rental.tenant_change.approve", "tenant", tenant.ID,
			fmt.Sprintf("通过租户资料变更申请：%s", changeFieldNames(*req)))
		response.Success(c, nil)
	}
}

// handleChangeRequestReject 驳回：写理由，删除本次上传的照片，正式档案不动。
func handleChangeRequestReject(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		req, ok := findLedgerChangeRequest(db, c, PermEdit)
		if !ok {
			return
		}
		if req.Status != ChangeStatusPending {
			response.ErrorBadRequest(c, "该申请已处理过")
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&body)
		note := strings.TrimSpace(body.Note)
		if len([]rune(note)) > 100 {
			response.ErrorBadRequest(c, "驳回理由不能超过 100 字")
			return
		}
		now := time.Now()
		if err := db.Model(req).Updates(map[string]interface{}{
			"status":      ChangeStatusRejected,
			"review_note": note,
			"reviewed_by": c.GetUint("userID"),
			"reviewed_at": &now,
			"new_photos":  marshalChangePhotos(nil),
		}).Error; err != nil {
			response.ErrorInternal(c, "驳回失败")
			return
		}
		// 驳回即删照片（隐私口径与租客登记流一致：不留无用证件照）。
		removeChangePhotos(req.NewPhotos)

		audit.Log(db, c, "rental.tenant_change.reject", "tenant", req.TenantID,
			fmt.Sprintf("驳回租户资料变更申请：%s", note))
		response.Success(c, nil)
	}
}

// moveChangePhotosToTenant 把申请照片挪进该租户的正式附件目录，返回待落库的
// 附件行（**未写库**，由调用方在事务里创建）。
//
// 为什么用「先算好、后写库」：文件挪动不可回滚，所以先做完文件操作再进事务写库；
// 事务若失败，调用方用 rollbackMovedPhotos 把文件挪回，两边重新一致。
func moveChangePhotosToTenant(db *gorm.DB, req *TenantChangeRequest, tenant *Tenant) ([]TenantAttachment, error) {
	rows := make([]TenantAttachment, 0, len(req.NewPhotos))
	if len(req.NewPhotos) == 0 {
		return rows, nil
	}
	now := time.Now()
	for i, rel := range req.NewPhotos {
		srcAbs := attachmentAbsPath(rel)
		if !withinAttachmentsRoot(srcAbs) || !fileExists(srcAbs) {
			return rows, fmt.Errorf("申请照片已不存在")
		}
		ext := strings.ToLower(filepath.Ext(srcAbs))
		dstRel := attachmentLedgerRel(tenant.LedgerID, now, uuid.New().String()+ext)
		dstAbs := attachmentAbsPath(dstRel)
		if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
			return rows, err
		}
		if err := os.Rename(srcAbs, dstAbs); err != nil {
			// 跨设备（临时目录与数据目录不在同一文件系统，测试环境常见）时
			// rename 会失败，退回「拷贝 + 删除」。
			if err := copyFile(srcAbs, dstAbs); err != nil {
				return rows, err
			}
			_ = os.Remove(srcAbs)
		}
		row := TenantAttachment{
			UserID:   req.ApplicantUserID,
			LedgerID: tenant.LedgerID,
			TenantID: tenant.ID,
			FileName: fmt.Sprintf("证件照（租户申请 %d）", i+1),
			FilePath: dstRel,
			MimeType: mimeFromExt(ext),
			Label:    "租户提交",
		}
		if fi, err := os.Stat(dstAbs); err == nil {
			row.FileSize = fi.Size()
		}
		// 记录新位置，供事务失败时回滚。
		req.NewPhotos[i] = dstRel
		rows = append(rows, row)
	}
	return rows, nil
}

// rollbackMovedPhotos 事务失败时把已挪进租户目录的照片移回申请目录。
//
// 尽力而为：挪不回去只记日志不影响响应（正式档案未被改动，附件表也没有这些行，
// 最多留下几个孤立文件，不影响正确性）。
func rollbackMovedPhotos(rows []TenantAttachment, req *TenantChangeRequest) {
	for _, row := range rows {
		src := attachmentAbsPath(row.FilePath)
		dst := attachmentAbsPath(filepath.ToSlash(filepath.Join(
			fmt.Sprintf("ledger-%d", req.LedgerID), changePhotoDir, fmt.Sprintf("%d", req.ID),
			filepath.Base(row.FilePath))))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			_ = copyFile(src, dst)
			_ = os.Remove(src)
		}
	}
}

// copyFile 文件拷贝（跨设备 rename 的回退路径）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// mimeFromExt 扩展名 → MIME（附件表记录展示用）。
func mimeFromExt(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".heic", ".heif":
		return "image/heic"
	case ".avif":
		return "image/avif"
	default:
		return "image/jpeg"
	}
}
