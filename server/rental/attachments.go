// attachments.go — 租户资料附件：租房协议、车牌、租客本人照片、身份证照片等。
//
// 与「合同存档」（contracts.go）**刻意分开**：合同是签约凭证、有明确语义，
// 而这里是房东自用的杂项留档（协议照片、汽车车牌、租客照片、身份证正反面）。
// 两者分成独立表与独立入口，合同弹窗保持只管合同，附件入口独立命名，
// 避免把身份证、车牌都叫成「合同」造成语义混乱。
//
// 实现口径与 contracts.go 一致：
//   - 文件本体存 <data-dir>/attachments/ledger-<ledgerId>/年/月/日/<uuid><ext>
//   - 扩展名由服务端内容嗅探推导，绝不信任客户端文件名与扩展名
//   - 读取走带归属校验的专属接口，不用公开的 /uploads
//   - 支持图片与 PDF，多张上传，可预览、可删除
package rental

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"smallgo/server/config"
	"smallgo/server/database"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 上传限制：单文件 20MB（与合同一致），单请求最多 10 个，每租户最多 40 份
// （比合同宽松：身份证 + 车牌 + 照片 + 协议往往一次拍好多张）。
const (
	attachmentMaxFileSize = 20 << 20
	attachmentMaxFiles    = 40
	attachmentBatchLimit  = 10
)

// attachmentAllowedTypes 内容嗅探白名单 → 标准扩展名。与合同一致（图片 + PDF）。
var attachmentAllowedTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/avif":      ".avif",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

// TenantAttachment 租户资料附件。FilePath 存相对 attachmentsDir 的路径
// （ledger-<ledgerId>/年/月/日/<uuid><ext>），FileName 保留原始文件名用于展示与下载。
type TenantAttachment struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`                                          // 上传人，仅溯源
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	TenantID  uint      `gorm:"index;not null" json:"tenant_id"`
	FileName  string    `gorm:"not null" json:"file_name"`
	FilePath  string    `json:"-"`
	FileSize  int64     `json:"file_size"`
	MimeType  string    `json:"mime_type"`
	Label     string    `json:"label"` // 用途备注，如「身份证正面」「车牌」，选填
	CreatedAt time.Time `json:"created_at"`
}

func init() {
	database.RegisterModels(&TenantAttachment{})
}

// attachmentsDirOverride 供测试注入临时目录；为空时按部署配置解析。
var attachmentsDirOverride string

// attachmentsDir 附件存储根目录：<data-dir>/attachments。
func attachmentsDir() string {
	if attachmentsDirOverride != "" {
		return attachmentsDirOverride
	}
	base := config.C.DataDir
	if strings.TrimSpace(base) == "" {
		base = "./data"
	}
	return filepath.Join(base, "attachments")
}

// attachmentAbsPath 把相对路径解析成绝对路径。
func attachmentAbsPath(rel string) string {
	return filepath.Join(attachmentsDir(), filepath.FromSlash(rel))
}

// attachmentLedgerRel 生成带台账前缀的落盘相对路径，台账即目录。
func attachmentLedgerRel(ledgerID uint, now time.Time, name string) string {
	return filepath.ToSlash(filepath.Join(
		fmt.Sprintf("ledger-%d", ledgerID),
		fmt.Sprintf("%04d", now.Year()),
		fmt.Sprintf("%02d", now.Month()),
		fmt.Sprintf("%02d", now.Day()),
		name,
	))
}

// attachmentResolve 把数据库里的相对路径解析成本次请求可访问的绝对路径。
// 与 contractResolve 同口径：
//  1. 新版布局（自带本台账 ledger-<id>/ 前缀）→ 直接拼绝对路径；
//  2. 带**别的台账**前缀 → 一律拒绝，绝不借回退分支读别人的文件；
//  3. 无前缀的老布局 → 只有 0 号台账可回退。
func attachmentResolve(ledgerID uint, rel string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	prefix := fmt.Sprintf("ledger-%d", ledgerID)

	// 1. 本台账前缀：直接拼（拼完仍校验落在根目录内，防目录穿越）。
	if strings.HasPrefix(rel, prefix+"/") {
		abs := attachmentAbsPath(rel)
		if !withinAttachmentsRoot(abs) {
			return "", false
		}
		if fileExists(abs) {
			return abs, true
		}
		// 带本台账前缀但文件不在：不猜别的布局，避免越权兜底。
		return "", false
	}

	// 2. 别的台账前缀：拒绝。
	if otherLedgerPrefix(rel) {
		return "", false
	}

	// 3. 无前缀老布局：只有 0 号台账可回退（老库升级后存量数据 LedgerID 恒为 0）。
	if ledgerID != LegacySharedLedgerID {
		return "", false
	}
	abs := attachmentAbsPath(rel)
	if !withinAttachmentsRoot(abs) {
		return "", false
	}
	if fileExists(abs) {
		return abs, true
	}
	return "", false
}

// withinAttachmentsRoot 防目录穿越：解析后的绝对路径必须落在附件根目录内。
func withinAttachmentsRoot(abs string) bool {
	root, err := filepath.Abs(attachmentsDir())
	if err != nil {
		return false
	}
	full, err := filepath.Abs(abs)
	if err != nil {
		return false
	}
	return full == root || strings.HasPrefix(full, root+string(os.PathSeparator))
}

func setupAttachmentRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 附件挂在租户名下，走「租户与合同」模块的勾选。
	view := requireModule(db, ModuleTenants, PermView)
	edit := requireLedgerModule(db, ModuleTenants, PermEdit)
	full := requireLedgerModule(db, ModuleTenants, PermFull)
	api.GET("/rental/tenants/:id/attachments", view, handleAttachmentList(db))
	api.POST("/rental/tenants/:id/attachments", edit, handleAttachmentUpload(db))
	api.GET("/rental/attachments/:id/file", view, handleAttachmentFile(db))
	api.DELETE("/rental/attachments/:id", full, handleAttachmentDelete(db))
}

func handleAttachmentList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		items := make([]TenantAttachment, 0)
		q, _ := scopeLedger(db.Where("tenant_id = ?", tenant.ID), c)
		if err := q.Order("created_at ASC, id ASC").Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询附件失败")
			return
		}
		response.Success(c, items)
	}
}

// handleAttachmentUpload 上传租户资料附件（multipart，字段名 files 或 file，可多个）。
func handleAttachmentUpload(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "请选择要上传的图片或 PDF")
			return
		}
		files := append(form.File["files"], form.File["file"]...)
		if len(files) == 0 {
			response.ErrorBadRequest(c, "请选择要上传的图片或 PDF")
			return
		}
		if len(files) > attachmentBatchLimit {
			response.ErrorBadRequest(c, fmt.Sprintf("单次最多上传 %d 个文件", attachmentBatchLimit))
			return
		}
		// 用途备注（选填）：整批用同一个标签，如「身份证」「车牌」。
		label := ""
		if v := form.Value["label"]; len(v) > 0 {
			label = strings.TrimSpace(v[0])
		}
		if len([]rune(label)) > 20 {
			response.ErrorBadRequest(c, "用途备注不能超过 20 字")
			return
		}

		var existing int64
		cq, _ := scopeLedger(db.Where("tenant_id = ?", tenant.ID), c)
		cq.Model(&TenantAttachment{}).Count(&existing)
		if int(existing)+len(files) > attachmentMaxFiles {
			response.ErrorBadRequest(c, fmt.Sprintf("每个租户最多存 %d 个附件", attachmentMaxFiles))
			return
		}

		created := make([]TenantAttachment, 0, len(files))
		for _, fh := range files {
			if fh.Size <= 0 || fh.Size > attachmentMaxFileSize {
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
			ext, allowed := attachmentAllowedTypes[mimeType]
			if !allowed {
				response.ErrorBadRequest(c, fmt.Sprintf("%s：仅支持图片（拍照/截图）或 PDF 文件", fh.Filename))
				return
			}

			now := time.Now()
			rel := attachmentLedgerRel(tenant.LedgerID, now, uuid.New().String()+ext)
			abs := attachmentAbsPath(rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				response.ErrorInternal(c, "创建存储目录失败")
				return
			}
			if err := c.SaveUploadedFile(fh, abs); err != nil {
				response.ErrorInternal(c, "保存附件失败")
				return
			}

			row := TenantAttachment{
				UserID:   currentUserID(c),
				LedgerID: tenant.LedgerID,
				TenantID: tenant.ID,
				FileName: strings.TrimSpace(fh.Filename),
				FilePath: rel,
				FileSize: fh.Size,
				MimeType: mimeType,
				Label:    label,
			}
			if row.FileName == "" {
				row.FileName = "附件" + ext
			}
			if err := db.Create(&row).Error; err != nil {
				_ = os.Remove(abs)
				response.ErrorInternal(c, "保存附件记录失败")
				return
			}
			created = append(created, row)
		}
		response.Success(c, gin.H{"uploaded": len(created), "items": created})
	}
}

// handleAttachmentFile 流式返回附件：默认 inline（浏览器直接预览图片/PDF），
// download=1 时以原始文件名作为下载名。
func handleAttachmentFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserAttachment(db, c)
		if !ok {
			return
		}
		abs, ok := attachmentResolve(item.LedgerID, item.FilePath)
		if !ok || !fileExists(abs) {
			response.ErrorNotFound(c, "附件文件不存在")
			return
		}
		c.Header("X-Content-Type-Options", "nosniff")
		// PDF 内联预览时禁掉脚本执行面，只当作静态文档渲染。
		c.Header("Content-Security-Policy", "default-src 'none'; img-src 'self' data:; object-src 'self'; sandbox")
		attachment := c.Query("download") == "1"
		c.Header("Content-Disposition", contractDisposition(c, item.FileName, attachment))
		if item.MimeType != "" {
			c.Header("Content-Type", item.MimeType)
		}
		c.File(abs)
	}
}

func handleAttachmentDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserAttachment(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleTenants, PermEdit) {
			return
		}
		if err := db.Delete(&TenantAttachment{}, item.ID).Error; err != nil {
			response.ErrorInternal(c, "删除附件失败")
			return
		}
		// 先删库再删盘：库删成功但盘删失败只会留下孤儿文件（不占用户可见空间），
		// 反过来则会留下指向空文件的记录，用户点开就报错。
		if abs, ok := attachmentResolve(item.LedgerID, item.FilePath); ok {
			_ = os.Remove(abs)
		}
		response.Success(c, gin.H{"deleted": item.ID})
	}
}

// findUserAttachment 取路径上的附件并校验访问权限。
// 与 findUserContract 同口径：附件（身份证、车牌、租客照片）是敏感件，
// 越权一律回 404「不存在」而不是 403，避免用 ID 探测出别的台账有什么。
func findUserAttachment(db *gorm.DB, c *gin.Context) (*TenantAttachment, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的附件 ID")
		return nil, false
	}
	var item TenantAttachment
	if err := db.First(&item, id).Error; err != nil {
		response.ErrorNotFound(c, "附件不存在")
		return nil, false
	}
	if !inLedgerScope(c, item.LedgerID) {
		response.ErrorNotFound(c, "附件不存在")
		return nil, false
	}
	return &item, true
}

// deleteTenantAttachments 删租户时一并清理其附件（与合同同口径）。
func deleteTenantAttachments(db *gorm.DB, tenantID uint, ledgerID uint) {
	var items []TenantAttachment
	db.Where("tenant_id = ? AND org_id = ?", tenantID, ledgerID).Find(&items)
	for _, it := range items {
		if abs, ok := attachmentResolve(it.LedgerID, it.FilePath); ok {
			_ = os.Remove(abs)
		}
	}
	db.Where("tenant_id = ? AND org_id = ?", tenantID, ledgerID).Delete(&TenantAttachment{})
}
