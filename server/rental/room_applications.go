// room_applications.go — 免登录的**房源登记**（村委 / 中介场景，v0.6.10 追加）。
//
// 需求（用户 2026-10-13，粉丝提出）：「我是村委工作人员，村民有房屋出租，他自己扫码
// 登记房源和租户信息，我这边后台也能看到；有人来村委会咨询哪里有空房出租，我就能在
// 后台看到、给别人推荐。当然这也适合房屋中介。」
//
// 与既有的**租客登记链接**（rooms.apply_token → /apply/<token>）刻意分开：
//   - 租客登记挂在一间**已存在**的房源上（`rooms.apply_token`）；
//   - 房源登记时房子还不存在，所以 token 挂在**台账**上（`orgs.room_apply_token`）：
//     一个台账（村委 / 中介 / 房东本人）分享一个链接，谁拿到都能提交一栋/一间的资料。
//
// 提交流程：公开页填房源信息（位置、房号、月租金、备注）+ 提交人（房东）姓名电话
// + 选填的现有租户（姓名/电话/身份证号）+ 最多 3 张照片 → 落 `room_applications`
// 待审队列，照片暂存在 attachments/room-apply-tmp/（不可猜的 uuid 命名）。
// 审核通过：建房源（可选同时建租户）+ 把照片并入该房源的房间照片，一个事务完成；
// 驳回：删掉照片（隐私：不留无用材料）。
package rental

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smallgo/server/audit"
	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// roomApplyPhotoDir 待审房源照片的暂存目录（相对 attachmentsDir）。
// 与租客登记的 apply-tmp 分开：两批材料的生命周期与审核入口不同。
const roomApplyPhotoDir = "room-apply-tmp"

// errApplyAlreadyHandled 并发审核同一条登记时，没抢到 CAS 的那个请求拿到的哨兵错误。
var errApplyAlreadyHandled = errors.New("room application already handled")

const (
	roomApplyMaxPhotos   = 3
	roomApplyMaxPhotoLen = 10 << 20 // 单张 10MB，与租客登记一致
)

// RoomApplication 待审的房源登记。Status：pending 待审核 / approved 已入库 / rejected 已驳回。
type RoomApplication struct {
	ID       uint   `gorm:"primarykey" json:"id"`
	LedgerID uint   `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账（来登记链接所属台账）
	Token    string `gorm:"index;size:64" json:"-"`                                  // 提交所用的登记令牌（溯源）

	// 提交人（房东 / 村民）——后台要能打电话核实与推荐
	Name  string `gorm:"not null" json:"name"`
	Phone string `gorm:"not null" json:"phone"`

	// 房源信息
	Community   string  `json:"community"`
	Building    string  `json:"building"`
	Unit        string  `json:"unit"`
	Floor       string  `json:"floor"`
	RoomNo      string  `gorm:"not null" json:"room_no"`
	DefaultRent float64 `json:"default_rent"`
	Notes       string  `json:"notes"`
	// 介绍费（见 referral.go）：村民登记时就挂赏金，审核通过写入房源
	ReferralMode  string  `gorm:"default:''" json:"referral_mode"`
	ReferralValue float64 `gorm:"default:0" json:"referral_value"`

	// 选填：现有租户（提交人顺手登记，审核通过时一并建档）
	TenantName   string `json:"tenant_name"`
	TenantPhone  string `json:"tenant_phone"`
	TenantIDCard string `json:"tenant_id_card"`

	// Photos 房间照片相对路径 JSON 数组（room-apply-tmp/ 下，最多 3 张）
	Photos string `gorm:"default:[]" json:"-"`

	Status     string     `gorm:"default:pending;index" json:"status"`
	ReviewNote string     `json:"review_note"`
	RoomID     uint       `json:"room_id"`   // 入库后的房源 id（trace）
	TenantID   uint       `json:"tenant_id"` // 若同时登记了租户，入库后的租户 id
	ReviewedAt *time.Time `json:"reviewed_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func init() {
	database.RegisterModels(&RoomApplication{})
}

// roomApplyPhotos / setRoomApplyPhotos 序列化互转（坏数据按空数组处理）。
func roomApplyPhotos(raw string) []string {
	out := make([]string, 0)
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func setRoomApplyPhotos(app *RoomApplication, photos []string) {
	data, _ := json.Marshal(photos)
	app.Photos = string(data)
}

// ------- 房东侧：登记链接与审核队列 -------

func setupRoomApplicationRoutes(api *gin.RouterGroup, db *gorm.DB) {
	view := requireModule(db, ModuleRooms, PermView)
	edit := requireLedgerModule(db, ModuleRooms, PermEdit)

	api.POST("/rental/room-apply-link", edit, handleLedgerRoomApplyLink(db))
	api.GET("/rental/room-applications", view, handleRoomApplicationList(db))
	api.GET("/rental/room-applications/:id/photo/:index", view, handleRoomApplicationPhoto(db))
	api.POST("/rental/room-applications/:id/approve", edit, handleRoomApplicationApprove(db))
	api.POST("/rental/room-applications/:id/reject", edit, handleRoomApplicationReject(db))
}

// handleLedgerRoomApplyLink 生成本台账的免登录房源登记链接（链接由服务端拼：网关域要换成
// 应用直连地址，否则村民没有 NAS 账号打不开——与租客登记链接同一套判定）。
func handleLedgerRoomApplyLink(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 路由层的 requireLedgerModule 已经要求「本次请求的台账」有房源操作档，
		// 这里取出写入目标台账即可（ledgerScope 的 write 值）。
		_, ledgerID, _ := ledgerScope(c)
		if ledgerID == 0 {
			response.ErrorBadRequest(c, "请先选择要生成登记链接的台账")
			return
		}
		var ledger Ledger
		if err := db.First(&ledger, ledgerID).Error; err != nil {
			response.ErrorNotFound(c, "台账不存在")
			return
		}
		if c.Query("regenerate") == "1" || ledger.RoomApplyToken == "" {
			buf := make([]byte, 16)
			if _, err := rand.Read(buf); err != nil {
				response.ErrorInternal(c, "生成登记链接失败")
				return
			}
			token := hex.EncodeToString(buf)
			if err := db.Model(&Ledger{}).Where("id = ?", ledger.ID).
				Update("room_apply_token", token).Error; err != nil {
				response.ErrorInternal(c, "生成登记链接失败")
				return
			}
			ledger.RoomApplyToken = token
		}
		base, viaGateway, configured := applyLinkBase(c, db)
		var pending int64
		db.Model(&RoomApplication{}).
			Where("org_id = ? AND status = ?", ledger.ID, "pending").Count(&pending)
		response.Success(c, gin.H{
			"token":         ledger.RoomApplyToken,
			"base":          base,
			"link":          base + "/room-apply/" + ledger.RoomApplyToken,
			"via_gateway":   viaGateway,
			"configured":    configured,
			"ledger_name":   ledger.Name,
			"pending_count": pending,
		})
	}
}

// roomApplicationRow 列表出参：附照片预览地址（登录接口取流）。
type roomApplicationRow struct {
	RoomApplication
	PhotoURLs []string `json:"photo_urls"`
}

func handleRoomApplicationList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		query, empty := scopeLedger(db.Model(&RoomApplication{}), c)
		if empty {
			response.Success(c, []roomApplicationRow{})
			return
		}
		if status := strings.TrimSpace(c.Query("status")); status != "" {
			query = query.Where("status = ?", status)
		} else {
			// 默认只看待审：审核入口不需要把历史翻一遍
			query = query.Where("status = ?", "pending")
		}
		rows := make([]RoomApplication, 0, 16)
		if err := query.Order("created_at DESC").Limit(200).Find(&rows).Error; err != nil {
			response.ErrorInternal(c, "查询房源登记失败")
			return
		}
		out := make([]roomApplicationRow, 0, len(rows))
		for _, r := range rows {
			row := roomApplicationRow{RoomApplication: r, PhotoURLs: []string{}}
			for i := range roomApplyPhotos(r.Photos) {
				row.PhotoURLs = append(row.PhotoURLs, fmt.Sprintf("/api/rental/room-applications/%d/photo/%d", r.ID, i))
			}
			out = append(out, row)
		}
		response.Success(c, out)
	}
}

// handleRoomApplicationPhoto 流出登记照片（登录 + 台账可见才可读）。
func handleRoomApplicationPhoto(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findRoomApplication(db, c)
		if !ok {
			return
		}
		index, err := strconv.Atoi(c.Param("index"))
		photos := roomApplyPhotos(app.Photos)
		if err != nil || index < 0 || index >= len(photos) {
			response.ErrorNotFound(c, "照片不存在")
			return
		}
		abs := attachmentAbsPath(photos[index])
		if !withinAttachmentsRoot(abs) || !fileExists(abs) {
			response.ErrorNotFound(c, "照片不存在")
			return
		}
		c.File(abs)
	}
}

// handleRoomApplicationApprove 审核通过：建房源（可选同时建租户）+ 照片并入房间照片。
// 文件先搬、事务后提交，失败回滚，避免半成品。
func handleRoomApplicationApprove(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findRoomApplication(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, app.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if app.Status != "pending" {
			response.ErrorBadRequest(c, "该登记已处理过")
			return
		}
		var req struct {
			RoomNo      string   `json:"room_no"` // 审核时可改房号（同名重复时）
			DefaultRent *float64 `json:"default_rent"`
		}
		_ = c.ShouldBindJSON(&req)
		roomNo := strings.TrimSpace(req.RoomNo)
		if roomNo == "" {
			roomNo = app.RoomNo
		}
		rent := app.DefaultRent
		if req.DefaultRent != nil {
			rent = *req.DefaultRent
		}
		// 同台账同小区不允许重复房号（与手动新建同一口径）
		var dup int64
		db.Model(&Room{}).Where("org_id = ? AND community = ? AND room_no = ?",
			app.LedgerID, app.Community, roomNo).Count(&dup)
		if dup > 0 {
			response.ErrorBadRequest(c, fmt.Sprintf("「%s %s」已经存在，请改房号或驳回", app.Community, roomNo))
			return
		}

		// 1. 先把暂存照片搬进台账目录（room-apply-tmp → ledger-<id>/年/月/日/…）
		photos := roomApplyPhotos(app.Photos)
		moved := make([]movedFile, 0, len(photos))
		newRels := make([]string, 0, len(photos))
		rollback := func() {
			for i := len(moved) - 1; i >= 0; i-- {
				_ = os.MkdirAll(filepath.Dir(moved[i].from), 0o755)
				_ = os.Rename(moved[i].to, moved[i].from)
			}
		}
		for _, rel := range photos {
			src := attachmentAbsPath(rel)
			if !fileExists(src) {
				continue
			}
			ext := strings.ToLower(filepath.Ext(src))
			newRel := attachmentLedgerRel(app.LedgerID, time.Now(), uuid.New().String()+ext)
			dst := attachmentAbsPath(newRel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				rollback()
				response.ErrorInternal(c, "保存房源照片失败")
				return
			}
			if err := os.Rename(src, dst); err != nil {
				rollback()
				response.ErrorInternal(c, "保存房源照片失败")
				return
			}
			moved = append(moved, movedFile{from: src, to: dst})
			newRels = append(newRels, newRel)
		}

		// 2. 事务：房源 + 租户 + 房间照片 + 申请状态
		room := Room{
			UserID:    currentUserID(c),
			LedgerID:  app.LedgerID,
			Community: app.Community, Building: app.Building, Unit: app.Unit,
			Floor: app.Floor, RoomNo: roomNo, DefaultRent: rent,
			Notes: roomApplyNotes(app),
			// 登记人（房东/村民）的姓名电话写进房源自己的房东字段，
			// 不再只塞在备注里——房源列表/详情要能直接看到联系人
			LandlordName:  app.Name,
			LandlordPhone: app.Phone,
			ReferralMode:  app.ReferralMode,
			ReferralValue: app.ReferralValue,
		}
		var tenant Tenant
		err := db.Transaction(func(tx *gorm.DB) error {
			// 先用条件更新**认领**这条申请（CAS）：并发下（浏览器重试/双击）只有一个
			// 请求能把 pending 改成 approved，另一个 RowsAffected=0 直接失败并回滚，
			// 不会各建一间房、各建一个租户。上面那次 app.Status 检查只是快速路径。
			claimed := tx.Model(&RoomApplication{}).
				Where("id = ? AND status = ?", app.ID, "pending").
				Update("status", "approved")
			if claimed.Error != nil {
				return claimed.Error
			}
			if claimed.RowsAffected != 1 {
				return errApplyAlreadyHandled
			}
			if err := tx.Create(&room).Error; err != nil {
				return err
			}
			if app.TenantName != "" {
				tenant = Tenant{
					UserID: currentUserID(c), LedgerID: app.LedgerID, RoomID: room.ID,
					Name: app.TenantName, Phone: app.TenantPhone, IDCard: app.TenantIDCard,
					MoveInDate: time.Now().Format("2006-01-02"), Active: true,
					Notes: "随房源登记转入",
				}
				if err := tx.Create(&tenant).Error; err != nil {
					return err
				}
			}
			for _, rel := range newRels {
				row := RoomPhoto{
					UserID: currentUserID(c), LedgerID: app.LedgerID, RoomID: room.ID,
					FileName: "房源登记照片", FilePath: rel, MimeType: "image/jpeg",
				}
				if abs := attachmentAbsPath(rel); fileExists(abs) {
					if fi, err := os.Stat(abs); err == nil {
						row.FileSize = fi.Size()
					}
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			now := time.Now()
			app.RoomID = room.ID
			app.TenantID = tenant.ID
			app.ReviewedAt = &now
			app.Photos = "[]" // 照片已移交 RoomPhoto，申请行不再持有
			return tx.Model(&RoomApplication{}).Where("id = ?", app.ID).Updates(map[string]interface{}{
				"room_id": app.RoomID, "tenant_id": app.TenantID,
				"reviewed_at": now, "photos": "[]",
			}).Error
		})
		if err != nil {
			// 文件已搬走 → 回滚文件；再区分「被别人先审了」与真失败
			rollback()
			if errors.Is(err, errApplyAlreadyHandled) {
				response.ErrorBadRequest(c, "该登记已经被处理过了（可能是重复提交的审核请求）")
				return
			}
			response.ErrorInternal(c, "转入房源失败")
			return
		}
		audit.Log(db, c, "rental_room_apply_approve", "room", room.ID,
			fmt.Sprintf("房源登记通过：%s（提交人 %s %s）", roomLocationLabel(room), app.Name, app.Phone))
		response.Success(c, gin.H{
			"room_id":     room.ID,
			"tenant_id":   tenant.ID,
			"room_photos": len(newRels),
			"room_label":  roomLocationLabel(room),
		})
	}
}

// handleRoomApplicationReject 驳回：标状态 + 删掉暂存照片（不留无用材料）。
func handleRoomApplicationReject(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		app, ok := findRoomApplication(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, app.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if app.Status != "pending" {
			response.ErrorBadRequest(c, "该登记已处理过")
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
		for _, rel := range roomApplyPhotos(app.Photos) {
			if abs, ok := attachmentResolveAnyLedger(rel); ok {
				_ = os.Remove(abs)
			}
		}
		app.Photos = "[]"
		if err := db.Save(app).Error; err != nil {
			response.ErrorInternal(c, "操作失败")
			return
		}
		response.Success(c, app)
	}
}

// attachmentResolveAnyLedger 解析暂存目录（room-apply-tmp/ 无台账前缀）里的相对路径。
// 只允许落在附件根目录内，防目录穿越。
func attachmentResolveAnyLedger(rel string) (string, bool) {
	abs := attachmentAbsPath(rel)
	if !withinAttachmentsRoot(abs) || !fileExists(abs) {
		return "", false
	}
	return abs, true
}

// findRoomApplication 按台账收窄取一条登记，找不到时已写 404。
func findRoomApplication(db *gorm.DB, c *gin.Context) (*RoomApplication, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorNotFound(c, "登记不存在")
		return nil, false
	}
	q, empty := scopeLedger(db.Where("id = ?", id), c)
	if empty {
		response.ErrorNotFound(c, "登记不存在")
		return nil, false
	}
	var row RoomApplication
	if err := q.First(&row).Error; err != nil {
		response.ErrorNotFound(c, "登记不存在")
		return nil, false
	}
	return &row, true
}

// roomApplyNotes 入库时的备注：只放登记人自己写的说明。
// 房东姓名电话不放这里（v0.6.10 起有专门的房源字段），避免同一信息两处冗余、
// 后续改了其中一处对不上。
func roomApplyNotes(app *RoomApplication) string {
	return strings.TrimSpace(app.Notes)
}

// ------- 村民 / 中介侧：公开接口（publicGroup，带限流，免登录） -------

func setupRoomApplyPublicRoutes(public *gin.RouterGroup, db *gorm.DB) {
	g := public.Group("/rental/room-apply/:token")
	g.GET("", handleRoomApplyInfo(db))
	g.POST("", handleRoomApplySubmit(db))
}

// roomApplyLedgerByToken 按 token 找台账；找不到时已写 404。
func roomApplyLedgerByToken(db *gorm.DB, c *gin.Context) (*Ledger, bool) {
	token := strings.TrimSpace(c.Param("token"))
	if len(token) < 16 {
		response.ErrorNotFound(c, "登记链接无效")
		return nil, false
	}
	var ledger Ledger
	if err := db.Where("room_apply_token = ?", token).First(&ledger).Error; err != nil {
		response.ErrorNotFound(c, "登记链接无效或已被重置，请联系分享链接的人")
		return nil, false
	}
	return &ledger, true
}

// handleRoomApplyInfo 公开页加载：只回台账名（让村民确认没扫错码）。
func handleRoomApplyInfo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ledger, ok := roomApplyLedgerByToken(db, c)
		if !ok {
			return
		}
		response.Success(c, gin.H{"ledger_name": ledger.Name})
	}
}

// handleRoomApplySubmit 村民提交房源登记（multipart）。
func handleRoomApplySubmit(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ledger, ok := roomApplyLedgerByToken(db, c)
		if !ok {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "提交内容格式有误")
			return
		}
		app := RoomApplication{
			LedgerID:     ledger.ID,
			Token:        ledger.RoomApplyToken,
			Name:         strings.TrimSpace(firstFormValue(form, "name")),
			Phone:        strings.TrimSpace(firstFormValue(form, "phone")),
			Community:    strings.TrimSpace(firstFormValue(form, "community")),
			Building:     strings.TrimSpace(firstFormValue(form, "building")),
			Unit:         strings.TrimSpace(firstFormValue(form, "unit")),
			Floor:        strings.TrimSpace(firstFormValue(form, "floor")),
			RoomNo:       strings.TrimSpace(firstFormValue(form, "room_no")),
			Notes:        strings.TrimSpace(firstFormValue(form, "notes")),
			TenantName:   strings.TrimSpace(firstFormValue(form, "tenant_name")),
			TenantPhone:  strings.TrimSpace(firstFormValue(form, "tenant_phone")),
			TenantIDCard: strings.ToUpper(strings.TrimSpace(firstFormValue(form, "tenant_id_card"))),
		}
		if v := strings.TrimSpace(firstFormValue(form, "default_rent")); v != "" {
			rent, err := strconv.ParseFloat(v, 64)
			if err != nil || rent < 0 || rent > 1e7 {
				response.ErrorBadRequest(c, "月租金填写有误")
				return
			}
			app.DefaultRent = rent
		}
		// 介绍费：与房源表单同一套归一化（平台策略 off 忽略 / min 校验下限 / free 选填）
		refMode, refValue, refMsg := normalizeReferral(db, firstFormValue(form, "referral_mode"),
			formFloat(form, "referral_value"), app.DefaultRent)
		if refMsg != "" {
			response.ErrorBadRequest(c, refMsg)
			return
		}
		app.ReferralMode, app.ReferralValue = refMode, refValue
		if msg := validateRoomApplication(&app); msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}

		// 照片：最多 3 张，逐张嗅探内容定扩展名（不信任客户端文件名）
		files := append(form.File["photos"], form.File["photo"]...)
		if len(files) > roomApplyMaxPhotos {
			response.ErrorBadRequest(c, fmt.Sprintf("最多上传 %d 张照片", roomApplyMaxPhotos))
			return
		}
		saved := make([]string, 0, len(files))
		for _, fh := range files {
			rel, err := saveRoomApplyPhoto(fh)
			if err != nil {
				for _, old := range saved {
					if abs, ok := attachmentResolveAnyLedger(old); ok {
						_ = os.Remove(abs)
					}
				}
				response.ErrorBadRequest(c, err.Error())
				return
			}
			saved = append(saved, rel)
		}
		setRoomApplyPhotos(&app, saved)

		// 同台账同小区同房号 + 同手机号的待审登记：拒绝重复提交（避免覆盖别人的材料）
		var dup int64
		db.Model(&RoomApplication{}).
			Where("org_id = ? AND community = ? AND room_no = ? AND phone = ? AND status = ?",
				ledger.ID, app.Community, app.RoomNo, app.Phone, "pending").Count(&dup)
		if dup > 0 {
			for _, rel := range saved {
				if abs, ok := attachmentResolveAnyLedger(rel); ok {
					_ = os.Remove(abs)
				}
			}
			response.Error(c, http.StatusBadRequest, CodeApplyDuplicate,
				"这条房源已经提交过、正在等审核，无需重复提交")
			return
		}
		if err := db.Create(&app).Error; err != nil {
			for _, rel := range saved {
				if abs, ok := attachmentResolveAnyLedger(rel); ok {
					_ = os.Remove(abs)
				}
			}
			response.ErrorInternal(c, "提交失败，请稍后再试")
			return
		}
		response.Success(c, gin.H{"id": app.ID})
	}
}

// validateRoomApplication 公开提交的入参校验（公开接口，宁可严一点）。
func validateRoomApplication(app *RoomApplication) string {
	if app.Name == "" {
		return "请填写您的姓名"
	}
	if len([]rune(app.Name)) > 30 {
		return "姓名过长"
	}
	if !applyPhoneRe.MatchString(app.Phone) {
		return "请填写正确的手机号"
	}
	if app.RoomNo == "" {
		return "请填写房号或房屋称呼"
	}
	if len([]rune(app.RoomNo)) > 30 {
		return "房号过长"
	}
	if len([]rune(app.Community)) > 30 || len([]rune(app.Building)) > 20 ||
		len([]rune(app.Unit)) > 20 || len([]rune(app.Floor)) > 10 {
		return "位置信息过长"
	}
	if len([]rune(app.Notes)) > 100 {
		return "备注不能超过 100 字"
	}
	if len([]rune(app.TenantName)) > 30 {
		return "租户姓名过长"
	}
	if app.TenantPhone != "" && !applyPhoneRe.MatchString(app.TenantPhone) {
		return "租户手机号填写有误"
	}
	if app.TenantIDCard != "" && !idCardPattern.MatchString(app.TenantIDCard) {
		return "租户身份证号应为 18 位（末位可为 X）或 15 位"
	}
	return ""
}

// saveRoomApplyPhoto 把房源照片落到 room-apply-tmp/（uuid 命名，不可猜），返回相对路径。
func saveRoomApplyPhoto(fh *multipart.FileHeader) (string, error) {
	if fh.Size <= 0 || fh.Size > roomApplyMaxPhotoLen {
		return "", fmt.Errorf("每张照片需在 10MB 以内")
	}
	src, err := fh.Open()
	if err != nil {
		return "", fmt.Errorf("读取照片失败")
	}
	defer src.Close()
	header := make([]byte, 512)
	n, readErr := src.Read(header)
	if readErr != nil && n == 0 {
		return "", fmt.Errorf("读取照片失败")
	}
	mimeType := http.DetectContentType(header[:n])
	ext, allowed := imageAllowedTypes[mimeType]
	if !allowed {
		return "", fmt.Errorf("仅支持拍照或图片文件")
	}
	rel := filepath.ToSlash(filepath.Join(roomApplyPhotoDir, uuid.New().String()+ext))
	abs := attachmentAbsPath(rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("保存照片失败")
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("保存照片失败")
	}
	dst, err := os.Create(abs)
	if err != nil {
		return "", fmt.Errorf("保存照片失败")
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		_ = os.Remove(abs)
		return "", fmt.Errorf("保存照片失败")
	}
	return rel, nil
}

// formFloat 读一个浮点表单字段（空/非法按 0）。
func formFloat(form *multipart.Form, key string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(firstFormValue(form, key)), 64)
	if err != nil {
		return 0
	}
	return v
}
