// meter_photos.go — 抄表照片：水/电/燃气表盘照片留档（2026-10-13 粉丝反馈）。
//
// 为什么单开一张表、而不是挂在抄表台账（MeterRecord）或账单（Bill）上：
//   - 抄表有两条入口——「抄表开票」（建/改账单）与「录入读数」（只记台账）。
//     照片是**读数这一刻的现场凭证**，与从哪条入口进来无关，所以归属键取
//     (房间, 账期, 表种)——与 MeterRecord 的 (room, period) 唯一键同口径；
//   - 开票前就要能传照片（用户正看着表盘填数字），此时若挂 MeterRecord.ID
//     还会因为「记录尚未生成」而无处可挂；挂 Bill.ID 同理（新账单还没有 id）。
//     按 (房间, 账期, 表种) 归属，开票前后都能上传，界面也只需一套。
//
// 存储与读取口径与租户资料附件（attachments.go）完全一致：
//   - 文件本体存 <data-dir>/attachments/ledger-<ledgerId>/年/月/日/<uuid><ext>；
//   - 扩展名由服务端内容嗅探推导，绝不信任客户端文件名；
//   - 读取走带归属校验的专属接口，不用公开的 /uploads；
//   - 越权一律回 404「不存在」，避免用 ID 探测别的台账有什么。
//
// 只收图片：抄表照片是表盘现场照，PDF 在这里没有意义（附件那边才允许 PDF）。
package rental

import (
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smallgo/server/database"
	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 上传限制：单文件 20MB（与附件/合同一致，手机原图够用）、单请求最多 6 张、
// 每房每账期每种表最多 6 张（水/电/燃气各自计数，互不占额度）。
const (
	meterPhotoMaxFileSize = 20 << 20
	meterPhotoBatchLimit  = 6
	meterPhotoMaxPerMeter = 6
)

// meterPhotoKinds 允许的表种 → 中文名，用于报错文案；也是入参白名单。
var meterPhotoKinds = map[string]string{
	"water": "水",
	"elec":  "电",
	"gas":   "燃气",
}

// 抄表照片只收图片：白名单是 imageAllowedTypes（attachments.go，与房源照片共用），
// 见文件头说明——表盘现场照用不上 PDF。

// MeterPhoto 抄表照片。FilePath 存相对 attachmentsDir 的路径
// （ledger-<ledgerId>/年/月/日/<uuid><ext>），FileName 保留原始文件名用于展示与下载。
type MeterPhoto struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`                                          // 上传人，仅溯源
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID    uint      `gorm:"index;not null" json:"room_id"`
	Period    string    `gorm:"not null;index" json:"period"` // YYYY-MM
	Meter     string    `gorm:"not null;index" json:"meter"`  // water / elec / gas
	FileName  string    `gorm:"not null" json:"file_name"`
	FilePath  string    `json:"-"`
	FileSize  int64     `json:"file_size"`
	MimeType  string    `json:"mime_type"`
	CreatedAt time.Time `json:"created_at"`
}

func init() {
	database.RegisterModels(&MeterPhoto{})
}

func setupMeterPhotoRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 照片属于抄表动作的一部分，走「抄表」模块的勾选：查看档能看照片，
	// 操作档能传能删（抄表员拍错了要能自己重拍，不必等台账管理者）。
	view := requireModule(db, ModuleMeters, PermView)
	edit := requireLedgerModule(db, ModuleMeters, PermEdit)
	api.GET("/rental/meter-photos", view, handleMeterPhotoList(db))
	api.POST("/rental/rooms/:id/meter-photos", edit, handleMeterPhotoUpload(db))
	api.GET("/rental/meter-photos/:id/file", view, handleMeterPhotoFile(db))
	api.DELETE("/rental/meter-photos/:id", edit, handleMeterPhotoDelete(db))
}

// handleMeterPhotoList 列出可见台账内的抄表照片。
// 筛选：room_id（房间）、period（账期，精确）、meter（表种）。
// 抄表记录页与开票弹窗都是按「某房某月某表」取，故按 room_id+period 用法最常见。
func handleMeterPhotoList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		query, empty := scopeLedger(db.Model(&MeterPhoto{}), c)
		if empty {
			response.SuccessPage(c, []MeterPhoto{}, 0, page, pageSize)
			return
		}
		if roomID := utils.Atoi(c.Query("room_id"), 0); roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		if period := strings.TrimSpace(c.Query("period")); period != "" {
			query = query.Where("period = ?", period)
		}
		if meter := strings.TrimSpace(c.Query("meter")); meter != "" {
			if _, ok := meterPhotoKinds[meter]; !ok {
				response.ErrorBadRequest(c, "表种只能是 water / elec / gas")
				return
			}
			query = query.Where("meter = ?", meter)
		}

		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询抄表照片失败")
			return
		}
		items := make([]MeterPhoto, 0)
		if err := query.Order("meter ASC, id ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询抄表照片失败")
			return
		}
		response.SuccessPage(c, items, total, page, pageSize)
	}
}

// handleMeterPhotoUpload 上传抄表照片（multipart：files/file + period + meter）。
// 权限：路由层要求本次请求的台账有抄表操作档，对象层再按房间所属台账判一次
// （与抄表记录录入同一口径，防「去掉 ledger_id 写别的台账」）。
func handleMeterPhotoUpload(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleMeters, PermEdit) {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "请选择要上传的表盘照片")
			return
		}
		files := append(form.File["files"], form.File["file"]...)
		if len(files) == 0 {
			response.ErrorBadRequest(c, "请选择要上传的表盘照片")
			return
		}
		if len(files) > meterPhotoBatchLimit {
			response.ErrorBadRequest(c, fmt.Sprintf("单次最多上传 %d 张照片", meterPhotoBatchLimit))
			return
		}
		period := ""
		if v := form.Value["period"]; len(v) > 0 {
			period = strings.TrimSpace(v[0])
		}
		if !isValidPeriod(period) {
			response.ErrorBadRequest(c, "账期格式应为 YYYY-MM")
			return
		}
		meter := ""
		if v := form.Value["meter"]; len(v) > 0 {
			meter = strings.TrimSpace(v[0])
		}
		kindName, known := meterPhotoKinds[meter]
		if !known {
			response.ErrorBadRequest(c, "表种只能是 water / elec / gas")
			return
		}

		var existing int64
		cq, _ := scopeLedger(db.Where("room_id = ? AND period = ? AND meter = ?",
			room.ID, period, meter), c)
		cq.Model(&MeterPhoto{}).Count(&existing)
		if int(existing)+len(files) > meterPhotoMaxPerMeter {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"%s %s 的%s表照片最多 %d 张，请先删除不用的再传",
				room.RoomNo, period, kindName, meterPhotoMaxPerMeter))
			return
		}

		created := make([]MeterPhoto, 0, len(files))
		// 先整批校验（大小 + 内容嗅探），全过再落盘：否则「第 3 张不是图片」时
		// 前两张已经存进去、用户却只收到一句失败提示，留下半成功状态。
		type pendingPhoto struct {
			file *multipart.FileHeader
			ext  string
			mime string
		}
		pending := make([]pendingPhoto, 0, len(files))
		for _, fh := range files {
			if fh.Size <= 0 || fh.Size > meterPhotoMaxFileSize {
				response.ErrorBadRequest(c, fmt.Sprintf("%s：文件为空或超过 20MB", fh.Filename))
				return
			}
			src, err := fh.Open()
			if err != nil {
				response.ErrorInternal(c, "读取上传文件失败")
				return
			}
			header := make([]byte, 512)
			n, readErr := src.Read(header)
			src.Close()
			if readErr != nil && n == 0 {
				response.ErrorBadRequest(c, fmt.Sprintf("%s：读取文件内容失败", fh.Filename))
				return
			}
			mimeType := http.DetectContentType(header[:n])
			ext, allowed := imageAllowedTypes[mimeType]
			if !allowed {
				response.ErrorBadRequest(c, fmt.Sprintf("%s：只支持表盘照片（图片文件）", fh.Filename))
				return
			}
			pending = append(pending, pendingPhoto{file: fh, ext: ext, mime: mimeType})
		}

		for _, p := range pending {
			now := time.Now()
			rel := attachmentLedgerRel(room.LedgerID, now, uuid.New().String()+p.ext)
			abs := attachmentAbsPath(rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				response.ErrorInternal(c, "创建存储目录失败")
				return
			}
			if err := c.SaveUploadedFile(p.file, abs); err != nil {
				response.ErrorInternal(c, "保存照片失败")
				return
			}

			row := MeterPhoto{
				UserID:   currentUserID(c),
				LedgerID: room.LedgerID,
				RoomID:   room.ID,
				Period:   period,
				Meter:    meter,
				FileName: strings.TrimSpace(p.file.Filename),
				FilePath: rel,
				FileSize: p.file.Size,
				MimeType: p.mime,
			}
			if row.FileName == "" {
				row.FileName = kindName + "表照片" + p.ext
			}
			if err := db.Create(&row).Error; err != nil {
				_ = os.Remove(abs)
				response.ErrorInternal(c, "保存照片记录失败")
				return
			}
			created = append(created, row)
		}
		response.Success(c, gin.H{"uploaded": len(created), "items": created})
	}
}

// handleMeterPhotoFile 流式返回照片：默认 inline（浏览器直接渲染），
// download=1 时以原始文件名作为下载名。
func handleMeterPhotoFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserMeterPhoto(db, c)
		if !ok {
			return
		}
		abs, ok := attachmentResolve(item.LedgerID, item.FilePath)
		if !ok || !fileExists(abs) {
			response.ErrorNotFound(c, "照片文件不存在")
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Content-Disposition", contractDisposition(c, item.FileName, c.Query("download") == "1"))
		if item.MimeType != "" {
			c.Header("Content-Type", item.MimeType)
		}
		c.File(abs)
	}
}

// handleMeterPhotoDelete 删除一张抄表照片：先删库再删盘（与附件同口径，
// 反过来会留下指向空文件的记录，用户点开就报错）。
func handleMeterPhotoDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserMeterPhoto(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleMeters, PermEdit) {
			return
		}
		if err := db.Delete(&MeterPhoto{}, item.ID).Error; err != nil {
			response.ErrorInternal(c, "删除照片失败")
			return
		}
		if abs, ok := attachmentResolve(item.LedgerID, item.FilePath); ok {
			_ = os.Remove(abs)
		}
		response.Success(c, gin.H{"deleted": item.ID})
	}
}

// findUserMeterPhoto 取路径上的照片并校验台账可见性（不可见按「不存在」处理）。
func findUserMeterPhoto(db *gorm.DB, c *gin.Context) (*MeterPhoto, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的照片 ID")
		return nil, false
	}
	var item MeterPhoto
	if err := db.First(&item, id).Error; err != nil {
		response.ErrorNotFound(c, "照片不存在")
		return nil, false
	}
	if !inLedgerScope(c, item.LedgerID) {
		response.ErrorNotFound(c, "照片不存在")
		return nil, false
	}
	return &item, true
}

// deleteRoomMeterPhotos 删房源时一并清理其抄表照片（库 + 盘）。
// 只删本台账的行：room_id 全局唯一，但归属列仍按房间所属台账收窄，
// 与删租户附件同口径，避免异常数据被跨台账误删。
func deleteRoomMeterPhotos(db *gorm.DB, roomID uint, ledgerID uint) {
	var items []MeterPhoto
	db.Where("room_id = ? AND org_id = ?", roomID, ledgerID).Find(&items)
	for _, it := range items {
		if abs, ok := attachmentResolve(it.LedgerID, it.FilePath); ok {
			_ = os.Remove(abs)
		}
	}
	db.Where("room_id = ? AND org_id = ?", roomID, ledgerID).Delete(&MeterPhoto{})
}
