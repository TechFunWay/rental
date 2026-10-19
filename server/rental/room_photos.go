// room_photos.go — 房源照片：一间房本身的照片（室内、门牌、水表电表位置等）。
//
// 与「抄表照片」（meter_photos.go，按房间 + 账期 + 表种）**分开成两张表**：
// 房源照片描述的是房子本身（长期有效、与账期无关），抄表照片是某一期的现场凭证，
// 生命周期与语义都不同；混在一张表里会让「删掉某月抄表照片」误删房源照片。
//
// 与租户资料附件（attachments.go）也分开：那是挂在租户名下的人的资料，
// 换租客就跟着走；房源照片跟着房子走，换几任租客都还在。
//
// 存储与读取口径同上：文件存 <data-dir>/attachments/ledger-<id>/年/月/日/<uuid><ext>，
// 扩展名由内容嗅探推导，读取走带归属校验的专属接口（不用公开的 /uploads），
// 越权一律回 404「不存在」。只收图片。
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

// 上传限制：单文件 20MB、单请求最多 6 张、每间房最多 30 张
// （房源照片比抄表照片多：一室一厅的各个角度、门牌、表位都要留档）。
const (
	roomPhotoMaxFileSize = 20 << 20
	roomPhotoBatchLimit  = 6
	roomPhotoMaxPerRoom  = 30
)

// roomPhotoInputHint 报错文案里的用途提示。
const roomPhotoInputHint = "请选择房间照片（图片文件）"

// RoomPhoto 房源照片。FilePath 存相对 attachmentsDir 的路径。
type RoomPhoto struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`                                          // 上传人，仅溯源
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID    uint      `gorm:"index;not null" json:"room_id"`
	FileName  string    `gorm:"not null" json:"file_name"`
	FilePath  string    `json:"-"`
	FileSize  int64     `json:"file_size"`
	MimeType  string    `json:"mime_type"`
	CreatedAt time.Time `json:"created_at"`
}

func init() {
	database.RegisterModels(&RoomPhoto{})
}

func setupRoomPhotoRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 房源照片归「房源」模块：查看档能看，操作档能传能删。
	view := requireModule(db, ModuleRooms, PermView)
	edit := requireLedgerModule(db, ModuleRooms, PermEdit)
	api.GET("/rental/room-photos", view, handleRoomPhotoList(db))
	api.POST("/rental/rooms/:id/photos", edit, handleRoomPhotoUpload(db))
	api.GET("/rental/room-photos/:id/file", view, handleRoomPhotoFile(db))
	api.DELETE("/rental/room-photos/:id", edit, handleRoomPhotoDelete(db))
}

// handleRoomPhotoList 列出可见台账内的房源照片（按房间筛，按上传顺序返回）。
func handleRoomPhotoList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 20))
		query, empty := scopeLedger(db.Model(&RoomPhoto{}), c)
		if empty {
			response.SuccessPage(c, []RoomPhoto{}, 0, page, pageSize)
			return
		}
		if roomID := utils.Atoi(c.Query("room_id"), 0); roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询房源照片失败")
			return
		}
		items := make([]RoomPhoto, 0)
		if err := query.Order("id ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询房源照片失败")
			return
		}
		response.SuccessPage(c, items, total, page, pageSize)
	}
}

// handleRoomPhotoUpload 上传房源照片（multipart，字段名 files 或 file）。
// 权限落在房间所属台账上（与抄表照片、附件同一口径）。
func handleRoomPhotoUpload(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, roomPhotoInputHint)
			return
		}
		files := append(form.File["files"], form.File["file"]...)
		if len(files) == 0 {
			response.ErrorBadRequest(c, roomPhotoInputHint)
			return
		}
		if len(files) > roomPhotoBatchLimit {
			response.ErrorBadRequest(c, fmt.Sprintf("单次最多上传 %d 张照片", roomPhotoBatchLimit))
			return
		}
		var existing int64
		cq, _ := scopeLedger(db.Where("room_id = ?", room.ID), c)
		cq.Model(&RoomPhoto{}).Count(&existing)
		if int(existing)+len(files) > roomPhotoMaxPerRoom {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"%s 的照片最多 %d 张，请先删除不用的再传", room.RoomNo, roomPhotoMaxPerRoom))
			return
		}

		// 先整批校验（大小 + 内容嗅探），全过再落盘：与抄表照片同一口径，
		// 避免「第 3 张不是图片」时前两张已经存进去的半成功状态。
		type pendingPhoto struct {
			file *multipart.FileHeader
			ext  string
			mime string
		}
		pending := make([]pendingPhoto, 0, len(files))
		for _, fh := range files {
			if fh.Size <= 0 || fh.Size > roomPhotoMaxFileSize {
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
				response.ErrorBadRequest(c, fmt.Sprintf("%s：只支持房间照片（图片文件）", fh.Filename))
				return
			}
			pending = append(pending, pendingPhoto{file: fh, ext: ext, mime: mimeType})
		}

		created := make([]RoomPhoto, 0, len(pending))
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
			row := RoomPhoto{
				UserID:   currentUserID(c),
				LedgerID: room.LedgerID,
				RoomID:   room.ID,
				FileName: strings.TrimSpace(p.file.Filename),
				FilePath: rel,
				FileSize: p.file.Size,
				MimeType: p.mime,
			}
			if row.FileName == "" {
				row.FileName = "房间照片" + p.ext
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

func handleRoomPhotoFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomPhoto(db, c)
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

// handleRoomPhotoDelete 删除一张房源照片：先删库再删盘（与附件同口径）。
func handleRoomPhotoDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomPhoto(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if err := db.Delete(&RoomPhoto{}, item.ID).Error; err != nil {
			response.ErrorInternal(c, "删除照片失败")
			return
		}
		if abs, ok := attachmentResolve(item.LedgerID, item.FilePath); ok {
			_ = os.Remove(abs)
		}
		response.Success(c, gin.H{"deleted": item.ID})
	}
}

// findUserRoomPhoto 取路径上的照片并校验台账可见性（不可见按「不存在」处理）。
func findUserRoomPhoto(db *gorm.DB, c *gin.Context) (*RoomPhoto, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的照片 ID")
		return nil, false
	}
	var item RoomPhoto
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

// deleteRoomPhotos 删房源时一并清理其照片（库 + 盘），只清本台账的行。
func deleteRoomPhotos(db *gorm.DB, roomID uint, ledgerID uint) {
	var items []RoomPhoto
	db.Where("room_id = ? AND org_id = ?", roomID, ledgerID).Find(&items)
	for _, it := range items {
		if abs, ok := attachmentResolve(it.LedgerID, it.FilePath); ok {
			_ = os.Remove(abs)
		}
	}
	db.Where("room_id = ? AND org_id = ?", roomID, ledgerID).Delete(&RoomPhoto{})
}
