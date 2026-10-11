// applications.go — 租客自助登记（2026-10-11 粉丝反馈：房东手动录租户太累）。
//
// 流程：房东在房源管理里生成某间房的「登记链接」（room.apply_token）分享给
// 租客 → 租客手机打开公开页 /apply/<token>，拍照/上传身份证（AI 识别姓名、
// 证件号、住址；识别服务未配置时手填）并手填手机号提交 → 申请进入待审核
// 队列 → 房东在租户管理里「转入登记」（复用登记租户弹窗，保存请求带
// application_id），身份证照片转入该租户的资料附件；拒绝则删除照片。
//
// 隐私口径：身份证照片只存本机 <data-dir>/attachments/apply-tmp/（不可被
// URL 直接猜中），预览走登录接口按台账鉴权；入库即挪进租户附件目录，
// 拒绝即删除。AI 识别走 OpenAI 兼容的视觉接口（系统配置 → 租房设置里配
// Key，不配置则识别按钮提示手填，不影响提交）。
package rental

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/sysconfig"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TenantApplication 租客通过登记链接提交的入驻申请（待房东审核）。
type TenantApplication struct {
	ID         uint       `gorm:"primarykey" json:"id"`
	LedgerID   uint       `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账（随房源）
	RoomID     uint       `gorm:"index" json:"room_id"`
	Token      string     `gorm:"index;size:64" json:"-"`              // 提交所用的登记 token（溯源）
	Name       string     `gorm:"not null" json:"name"`                // 姓名（AI 识别或手填，可改正）
	Phone      string     `gorm:"not null" json:"phone"`               // 手机号，租客手填
	IDCard     string     `json:"id_card"`                             // 身份证号（AI 识别或手填）
	Address    string     `json:"address"`                             // 身份证住址（AI 识别或手填）
	Remark     string     `json:"remark"`                              // 租客留言
	IdPhoto    string     `json:"-"`                                   // 身份证照片相对路径（apply-tmp/ 下），空=未传
	Status     string     `gorm:"default:pending;index" json:"status"` // pending 待审核 / approved 已入库 / rejected 已拒绝
	ReviewNote string     `json:"review_note"`
	TenantID   uint       `json:"tenant_id"` // 入库后的租户 id（trace）
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func init() {
	database.RegisterModels(&TenantApplication{})
}

// applyPhotoDir 登记照片暂存目录（相对 attachmentsDir），入库/拒绝后即挪走或删除。
const applyPhotoDir = "apply-tmp"

var applyPhoneRe = regexp.MustCompile(`^[0-9+\- ]{5,20}$`)

// ------- 房东侧：登记链接 -------

// handleRoomApplyLink 生成/返回房源的登记 token（?regenerate=1 强制换新，
// 旧链接随即失效）。链接本身由前端拼：<origin>/apply/<token>。
func handleRoomApplyLink(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if c.Query("regenerate") == "1" || room.ApplyToken == "" {
			buf := make([]byte, 16)
			if _, err := rand.Read(buf); err != nil {
				response.ErrorInternal(c, "生成登记链接失败")
				return
			}
			room.ApplyToken = hex.EncodeToString(buf)
			if err := db.Save(room).Error; err != nil {
				response.ErrorInternal(c, "生成登记链接失败")
				return
			}
		}
		response.Success(c, gin.H{"token": room.ApplyToken})
	}
}

// ------- 房东侧：申请队列 -------

func setupApplicationRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 读用 requireModule（并集准入 + 按模块收窄可见台账）：此前读也走
	// requireLedgerModule，而它只看「本次请求选中的台账」——不带 ?ledger_id 时
	// 校验的是个人台账（恒完全权限），于是一个在共同台账里零权限的成员去掉
	// 参数就能读到别人的身份证号与证件照（2026-10-11 审计复现）。
	view := requireModule(db, ModuleTenants, PermView)
	edit := requireLedgerModule(db, ModuleTenants, PermEdit)
	roomEdit := requireLedgerModule(db, ModuleRooms, PermEdit)

	api.GET("/rental/tenant-applications", view, handleApplicationList(db))
	api.GET("/rental/tenant-applications/:id/photo", view, handleApplicationPhoto(db))
	api.POST("/rental/tenant-applications/:id/reject", edit, handleApplicationReject(db))
	api.POST("/rental/rooms/:id/apply-link", roomEdit, handleRoomApplyLink(db))
}

// applicationRow 列表出参：附房源位置与照片预览地址（登录接口取流）。
type applicationRow struct {
	TenantApplication
	RoomLabel string `json:"room_label"`
	PhotoURL  string `json:"photo_url"`
}

func handleApplicationList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		status := strings.TrimSpace(c.Query("status"))
		query, empty := scopeLedger(db.Model(&TenantApplication{}), c)
		if empty {
			response.Success(c, []applicationRow{})
			return
		}
		switch status {
		case "pending", "approved", "rejected":
			query = query.Where("status = ?", status)
		}
		rows := make([]TenantApplication, 0, 16)
		if err := query.Order("created_at DESC").Limit(200).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询入住申请失败")
			return
		}
		// 房源位置批量补齐
		roomIDs := make([]uint, 0, len(rows))
		for _, r := range rows {
			if r.RoomID > 0 {
				roomIDs = append(roomIDs, r.RoomID)
			}
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
		out := make([]applicationRow, 0, len(rows))
		for _, r := range rows {
			row := applicationRow{TenantApplication: r, RoomLabel: roomLabels[r.RoomID]}
			if r.IdPhoto != "" {
				row.PhotoURL = fmt.Sprintf("/api/rental/tenant-applications/%d/photo", r.ID)
			}
			out = append(out, row)
		}
		response.Success(c, out)
	}
}

// handleApplicationPhoto 流出登记照片（登录 + 台账可见才可读，照片不落公开目录）。
func handleApplicationPhoto(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findApplication(db, c)
		if !ok {
			return
		}
		if app.IdPhoto == "" {
			response.ErrorNotFound(c, "该申请没有照片")
			return
		}
		abs := attachmentAbsPath(app.IdPhoto)
		if !withinAttachmentsRoot(abs) {
			response.ErrorNotFound(c, "照片不存在")
			return
		}
		c.File(abs)
	}
}

func handleApplicationReject(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findApplication(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, app.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if app.Status != "pending" {
			response.ErrorBadRequest(c, "该申请已处理过")
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&req)
		note := strings.TrimSpace(req.Note)
		if len([]rune(note)) > 100 {
			response.ErrorBadRequest(c, "备注不能超过 100 字")
			return
		}
		now := time.Now()
		app.Status = "rejected"
		app.ReviewNote = note
		app.ReviewedAt = &now
		if err := db.Save(app).Error; err != nil {
			response.ErrorInternal(c, "操作失败")
			return
		}
		// 拒绝即删除照片（隐私：不留无用证件照）
		if app.IdPhoto != "" {
			_ = os.Remove(attachmentAbsPath(app.IdPhoto))
			app.IdPhoto = ""
			db.Model(app).Update("id_photo", "")
		}
		response.Success(c, app)
	}
}

// findApplication 按台账收窄取申请，找不到时已写 404。
func findApplication(db *gorm.DB, c *gin.Context) (*TenantApplication, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		response.ErrorNotFound(c, "申请不存在")
		return nil, false
	}
	app, empty := scopeLedger(db.Where("id = ?", id), c)
	if empty {
		response.ErrorNotFound(c, "申请不存在")
		return nil, false
	}
	var row TenantApplication
	if err := app.First(&row).Error; err != nil {
		response.ErrorNotFound(c, "申请不存在")
		return nil, false
	}
	return &row, true
}

// consumeApplication 转入登记：租户创建成功后把申请标记为已入库，
// 身份证照片挪进该租户的资料附件（失败不影响入库本身，只记日志级别地忽略）。
func consumeApplication(db *gorm.DB, app *TenantApplication, tenant *Tenant) {
	now := time.Now()
	app.Status = "approved"
	app.TenantID = tenant.ID
	app.ReviewedAt = &now
	if err := db.Save(app).Error; err != nil {
		return
	}
	if app.IdPhoto == "" {
		return
	}
	srcAbs := attachmentAbsPath(app.IdPhoto)
	ext := strings.ToLower(filepath.Ext(srcAbs))
	rel := attachmentLedgerRel(tenant.LedgerID, now, uuid.New().String()+ext)
	dstAbs := attachmentAbsPath(rel)
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return
	}
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		return
	}
	att := TenantAttachment{
		LedgerID: tenant.LedgerID,
		TenantID: tenant.ID,
		FileName: "身份证（租客登记）",
		FilePath: rel,
		Label:    "身份证",
	}
	if fi, err := os.Stat(dstAbs); err == nil {
		att.FileSize = fi.Size()
	}
	if ext == ".pdf" {
		att.MimeType = "application/pdf"
	} else {
		att.MimeType = "image/jpeg"
	}
	db.Create(&att)
	app.IdPhoto = ""
	db.Model(app).Update("id_photo", "")
}

// ------- 租客侧：公开接口（publicGroup，带限流） -------

// setupApplyPublicRoutes 公开登记接口：不需要登录，凭房源 token 操作。
func setupApplyPublicRoutes(public *gin.RouterGroup, db *gorm.DB) {
	g := public.Group("/rental/apply/:token")
	g.GET("", handleApplyInfo(db))
	g.POST("/ocr", handleApplyOCR(db))
	g.POST("", handleApplySubmit(db))
}

// applyRoomByToken 按 token 找房源；找不到时已写 404。
func applyRoomByToken(db *gorm.DB, c *gin.Context) (*Room, bool) {
	token := strings.TrimSpace(c.Param("token"))
	if len(token) < 16 {
		response.ErrorNotFound(c, "登记链接无效")
		return nil, false
	}
	var room Room
	if err := db.Where("apply_token = ?", token).First(&room).Error; err != nil {
		response.ErrorNotFound(c, "登记链接无效或已被重置，请联系房东重新分享")
		return nil, false
	}
	return &room, true
}

// handleApplyInfo 公开页加载：只回最小必要的位置信息。
func handleApplyInfo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := applyRoomByToken(db, c)
		if !ok {
			return
		}
		response.Success(c, gin.H{
			"community": room.Community,
			"room_no":   room.RoomNo,
			"building":  room.Building,
			"unit":      room.Unit,
			"floor":     room.Floor,
		})
	}
}

// handleApplyOCR 身份证照片识别：把照片交给配置的视觉模型，返回结构化字段。
// 照片本身不落盘（提交时才随表单上传一次）。
func handleApplyOCR(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := applyRoomByToken(db, c); !ok {
			return
		}
		file, err := c.FormFile("photo")
		if err != nil {
			response.ErrorBadRequest(c, "请选择身份证照片")
			return
		}
		if file.Size <= 0 || file.Size > 10<<20 {
			response.ErrorBadRequest(c, "照片需在 10MB 以内")
			return
		}
		src, err := file.Open()
		if err != nil {
			response.ErrorInternal(c, "读取照片失败")
			return
		}
		defer src.Close()
		data, err := io.ReadAll(src)
		if err != nil {
			response.ErrorInternal(c, "读取照片失败")
			return
		}
		mimeType := http.DetectContentType(data[:min(len(data), 512)])
		res, err := callIdCardOCR(db, data, mimeType)
		if err != nil {
			response.ErrorBadRequest(c, err.Error())
			return
		}
		response.Success(c, res)
	}
}

// handleApplySubmit 提交登记申请。同一房源同一手机号的待审核申请会被覆盖
// （租客重复提交以最后一次为准），避免房东的队列里堆重复项。
func handleApplySubmit(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := applyRoomByToken(db, c)
		if !ok {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "提交内容格式有误")
			return
		}
		name := strings.TrimSpace(firstFormValue(form, "name"))
		phone := strings.TrimSpace(firstFormValue(form, "phone"))
		idCard := strings.TrimSpace(firstFormValue(form, "id_card"))
		address := strings.TrimSpace(firstFormValue(form, "address"))
		remark := strings.TrimSpace(firstFormValue(form, "remark"))
		if name == "" {
			response.ErrorBadRequest(c, "请填写姓名")
			return
		}
		if len([]rune(name)) > 30 {
			response.ErrorBadRequest(c, "姓名过长")
			return
		}
		if !applyPhoneRe.MatchString(phone) {
			response.ErrorBadRequest(c, "请填写正确的手机号")
			return
		}
		if len([]rune(idCard)) > 18 || len([]rune(address)) > 60 || len([]rune(remark)) > 100 {
			response.ErrorBadRequest(c, "填写内容过长")
			return
		}

		app := TenantApplication{
			LedgerID: room.LedgerID, RoomID: room.ID, Token: room.ApplyToken,
			Name: name, Phone: phone, IDCard: idCard, Address: address, Remark: remark,
		}

		// 照片可选但强烈建议：有照片房东才能核对证件
		if fh := firstFormFile(form, "photo"); fh != nil {
			if fh.Size <= 0 || fh.Size > 10<<20 {
				response.ErrorBadRequest(c, "照片需在 10MB 以内")
				return
			}
			if err := saveApplyPhoto(db, c, fh, &app); err != nil {
				response.ErrorInternal(c, "保存照片失败")
				return
			}
		}

		// 同房源同手机号已有待审核申请：**拒绝重复提交**，不再覆盖。
		// 覆盖语义（此前实现）有两个问题：登记链接是发给多个看房人的，谁拿到
		// 链接、又知道对方手机号，就能把别人已提交的姓名与身份证号改成自己的，
		// 并顺手删掉对方已上传的身份证照片（2026-10-11 审计实测复现：同一 id
		// 被改写为「冒充者」，原照片文件消失）。要改资料请让房东处理，
		// 或由房东重置登记链接后重新提交。
		var existing TenantApplication
		err = db.Where("room_id = ? AND phone = ? AND status = ?", room.ID, phone, "pending").First(&existing).Error
		if err == nil {
			// 本次刚落盘的照片没人认领，删掉，别在暂存目录里留孤儿证件照。
			if app.IdPhoto != "" {
				_ = os.Remove(attachmentAbsPath(app.IdPhoto))
			}
			response.ErrorBadRequest(c, "该手机号已经提交过申请，请等房东联系；如需修改资料请联系房东")
			return
		}
		if err := db.Create(&app).Error; err != nil {
			response.ErrorInternal(c, "提交失败，请稍后再试")
			return
		}
		response.Success(c, gin.H{"id": app.ID})
	}
}

func firstFormValue(form *multipart.Form, key string) string {
	if v := form.Value[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func firstFormFile(form *multipart.Form, key string) *multipart.FileHeader {
	if v := form.File[key]; len(v) > 0 {
		return v[0]
	}
	return nil
}

// saveApplyPhoto 把登记照片落到 apply-tmp/（uuid 命名，不可猜）。
func saveApplyPhoto(db *gorm.DB, c *gin.Context, fh *multipart.FileHeader, app *TenantApplication) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	header := make([]byte, 512)
	n, readErr := src.Read(header)
	if readErr != nil && n == 0 {
		return fmt.Errorf("读取失败")
	}
	mimeType := http.DetectContentType(header[:n])
	ext, allowed := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}[mimeType]
	if !allowed {
		return fmt.Errorf("仅支持拍照或图片文件")
	}
	rel := filepath.ToSlash(filepath.Join(applyPhotoDir, uuid.New().String()+ext))
	abs := attachmentAbsPath(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	dst, err := os.Create(abs)
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(abs)
		return err
	}
	app.IdPhoto = rel
	return nil
}

// ------- AI 识别（OpenAI 兼容视觉接口） -------

type idCardOCR struct {
	Name    string `json:"name"`
	IDCard  string `json:"id_card"`
	Address string `json:"address"`
}

// callIdCardOCR 调用系统配置里的视觉模型识别身份证。
// 未配置 Key 时返回明确提示，前端引导手填——识别是加分项不是门槛。
func callIdCardOCR(db *gorm.DB, image []byte, mimeType string) (idCardOCR, error) {
	var res idCardOCR
	baseURL := strings.TrimRight(mustConfig(db, "rental_ai_base_url", "https://open.bigmodel.cn/api/paas/v4"), "/")
	apiKey := strings.TrimSpace(mustConfig(db, "rental_ai_api_key", ""))
	model := strings.TrimSpace(mustConfig(db, "rental_ai_model", "glm-4v-flash"))
	if apiKey == "" {
		return res, fmt.Errorf("房东还未配置 AI 识别服务，请手动填写各项信息")
	}

	dataURL := "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(image)
	payload := map[string]interface{}{
		"model": model,
		"messages": []map[string]interface{}{{
			"role": "user",
			"content": []map[string]interface{}{
				{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
				{"type": "text", "text": "识别这张中国居民身份证人像面照片。只输出一个 JSON 对象，不要输出任何其他文字：" +
					`{"name":"姓名","id_card":"18位身份证号","address":"住址"}` +
					"。字段值以照片为准，看不清的字段填空字符串。"},
			},
		}},
		"temperature": 0.1,
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("POST", baseURL+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		return res, fmt.Errorf("识别服务配置有误")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return res, fmt.Errorf("识别服务连接失败，请手动填写")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return res, fmt.Errorf("识别服务返回异常（%d），请手动填写", resp.StatusCode)
	}
	var env struct {
		Choices []struct {
			Message struct {
				Content interface{} `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return res, fmt.Errorf("识别结果解析失败，请手动填写")
	}
	if len(env.Choices) == 0 {
		return res, fmt.Errorf("识别服务没有返回结果，请手动填写")
	}
	// content 兼容字符串与分段数组两种返回形态
	var text string
	switch v := env.Choices[0].Message.Content.(type) {
	case string:
		text = v
	case []interface{}:
		for _, seg := range v {
			if m, ok := seg.(map[string]interface{}); ok {
				if t, ok := m["text"].(string); ok {
					text += t
				}
			}
		}
	}
	res = parseOCRJSON(text)
	if res.Name == "" && res.IDCard == "" && res.Address == "" {
		return res, fmt.Errorf("没能识别出有效信息，请核对照片后重试或手动填写")
	}
	return res, nil
}

// parseOCRJSON 从模型回复里抠 JSON（容忍 ```json 围栏与前后闲话）。
func parseOCRJSON(text string) idCardOCR {
	text = strings.TrimSpace(text)
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return idCardOCR{}
	}
	var res idCardOCR
	if err := json.Unmarshal([]byte(text[start:end+1]), &res); err != nil {
		return idCardOCR{}
	}
	res.Name = strings.TrimSpace(res.Name)
	res.IDCard = strings.TrimSpace(res.IDCard)
	res.Address = strings.TrimSpace(res.Address)
	return res
}

// mustConfig 读系统级配置，读不到用默认值（配置中心保证 key 已注册）。
func mustConfig(db *gorm.DB, key, def string) string {
	v, err := sysconfig.GetConfig(db, key, 0)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
