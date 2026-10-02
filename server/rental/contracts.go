// contracts.go — 租赁合同存档:文件挂在租户名下,支持多页拍照/扫描件与 PDF。
//
// 文件本体存 <data-dir>/contracts/年/月/日/<uuid><ext>，路径由服务端生成、
// 内容嗅探判定类型，不信任客户端文件名与扩展名。读取走带归属校验的专属
// 接口而不是公开的 /uploads，保证合同文件不会被未登录访问。
package rental

import (
	"fmt"
	"net/http"
	"net/url"
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

// 上传限制：与框架 /api/upload 同量级（单文件 20MB），
// 合同多页常见，单请求最多 10 个文件、每租户最多 20 份。
const (
	contractMaxFileSize = 20 << 20
	contractMaxFiles    = 20
	contractBatchLimit  = 10
)

// contractAllowedTypes 内容嗅探白名单 → 标准扩展名。
// 扩展名永远由服务端按文件内容推导，客户端给的扩展名只作展示参考。
var contractAllowedTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/avif":      ".avif",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

// Contract 租赁合同附件。FilePath 存相对 contractsDir 的路径，
// FileName 保留用户上传时的原始文件名用于展示与下载命名。
type Contract struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`
	TenantID  uint      `gorm:"index;not null" json:"tenant_id"`
	FileName  string    `gorm:"not null" json:"file_name"`
	FilePath  string    `json:"-"`
	FileSize  int64     `json:"file_size"`
	MimeType  string    `json:"mime_type"`
	CreatedAt time.Time `json:"created_at"`
}

func init() {
	database.RegisterModels(&Contract{})
}

// contractsDirOverride 供测试注入临时目录；为空时按部署配置解析。
var contractsDirOverride string

// contractsDir 合同文件的存储根目录：<data-dir>/contracts。
func contractsDir() string {
	if contractsDirOverride != "" {
		return contractsDirOverride
	}
	base := config.C.DataDir
	if strings.TrimSpace(base) == "" {
		base = "./data"
	}
	return filepath.Join(base, "contracts")
}

// contractAbsPath 相对路径 → 磁盘绝对路径。
func contractAbsPath(rel string) string {
	return filepath.Join(contractsDir(), filepath.FromSlash(rel))
}

func setupContractRoutes(api *gin.RouterGroup, db *gorm.DB) {
	read := requireAccess(db, AccessReadonly)
	edit := requireAccess(db, AccessEdit)
	full := requireAccess(db, AccessFull)
	api.GET("/rental/tenants/:id/contracts", read, handleContractList(db))
	api.POST("/rental/tenants/:id/contracts", edit, handleContractUpload(db))
	api.GET("/rental/contracts/:id/file", read, handleContractFile(db))
	api.DELETE("/rental/contracts/:id", full, handleContractDelete(db))
}

func handleContractList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		contracts := make([]Contract, 0)
		if err := db.Where("tenant_id = ?", tenant.ID).Order("created_at ASC, id ASC").Find(&contracts).Error; err != nil {
			response.ErrorInternal(c, "查询合同失败")
			return
		}
		response.Success(c, contracts)
	}
}

// handleContractUpload 上传合同文件（multipart，字段名 files 或 file，可多个）。
func handleContractUpload(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, "请选择要上传的合同文件")
			return
		}
		files := append(form.File["files"], form.File["file"]...)
		if len(files) == 0 {
			response.ErrorBadRequest(c, "请选择要上传的合同文件")
			return
		}
		if len(files) > contractBatchLimit {
			response.ErrorBadRequest(c, fmt.Sprintf("单次最多上传 %d 个文件", contractBatchLimit))
			return
		}
		var existing int64
		db.Model(&Contract{}).Where("tenant_id = ?", tenant.ID).Count(&existing)
		if int(existing)+len(files) > contractMaxFiles {
			response.ErrorBadRequest(c, fmt.Sprintf("每个租户最多存 %d 份合同文件", contractMaxFiles))
			return
		}

		created := make([]Contract, 0, len(files))
		for _, fh := range files {
			if fh.Size <= 0 || fh.Size > contractMaxFileSize {
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
			ext, allowed := contractAllowedTypes[mimeType]
			if !allowed {
				response.ErrorBadRequest(c, fmt.Sprintf("%s：仅支持图片（拍照/截图）或 PDF 文件", fh.Filename))
				return
			}

			now := time.Now()
			rel := filepath.ToSlash(filepath.Join(
				fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()),
				uuid.New().String()+ext,
			))
			abs := contractAbsPath(rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				response.ErrorInternal(c, "创建存储目录失败")
				return
			}
			if err := c.SaveUploadedFile(fh, abs); err != nil {
				response.ErrorInternal(c, "保存合同文件失败")
				return
			}

			row := Contract{
				UserID:   currentUserID(c),
				TenantID: tenant.ID,
				FileName: strings.TrimSpace(fh.Filename),
				FilePath: rel,
				FileSize: fh.Size,
				MimeType: mimeType,
			}
			if row.FileName == "" {
				row.FileName = "合同" + ext
			}
			if err := db.Create(&row).Error; err != nil {
				_ = os.Remove(abs)
				response.ErrorInternal(c, "保存合同记录失败")
				return
			}
			created = append(created, row)
		}
		response.Success(c, gin.H{"uploaded": len(created), "items": created})
	}
}

// handleContractFile 流式返回合同文件：默认 inline（浏览器直接预览图片/PDF），
// download=1 时以原始文件名作为下载名。
func handleContractFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		contract, ok := findUserContract(db, c)
		if !ok {
			return
		}
		abs := contractAbsPath(contract.FilePath)
		f, err := os.Open(abs)
		if err != nil {
			response.ErrorNotFound(c, "合同文件不存在")
			return
		}
		defer f.Close()

		c.Header("Content-Type", contract.MimeType)
		c.Header("Content-Disposition", contractDisposition(c, contract.FileName, c.Query("download") == "1"))
		http.ServeContent(c.Writer, c.Request, filepath.Base(abs), contract.CreatedAt, f)
	}
}

// contractDisposition 拼接 Content-Disposition：中文文件名走 RFC 5987
// filename*，同时给一个 ASCII 回退 filename，保证各浏览器都能正确命名。
func contractDisposition(c *gin.Context, fileName string, attachment bool) string {
	kind := "inline"
	if attachment {
		kind = "attachment"
	}
	fallback := url.PathEscape(fileName)
	if fallback == "" {
		fallback = "contract"
	}
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`,
		kind, sanitizeASCIIFilename(fileName), fallback)
}

func sanitizeASCIIFilename(name string) string {
	replaced := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	if strings.TrimSpace(replaced) == "" {
		return "contract"
	}
	return replaced
}

func handleContractDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		contract, ok := findUserContract(db, c)
		if !ok {
			return
		}
		if err := db.Delete(contract).Error; err != nil {
			response.ErrorInternal(c, "删除合同失败")
			return
		}
		_ = os.Remove(contractAbsPath(contract.FilePath))
		response.Success(c, gin.H{"deleted": true})
	}
}

// deleteTenantContracts 删除某租户的全部合同行与文件。
func deleteTenantContracts(db *gorm.DB, tenantID uint) {
	rows := make([]Contract, 0)
	if err := db.Where("tenant_id = ?", tenantID).Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		_ = os.Remove(contractAbsPath(row.FilePath))
	}
	_ = db.Where("tenant_id = ?", tenantID).Delete(&Contract{}).Error
}

func findUserContract(db *gorm.DB, c *gin.Context) (*Contract, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的合同 ID")
		return nil, false
	}
	var contract Contract
	// 数据共享后按 ID 直取，不再限定录入人。
	if err := db.First(&contract, id).Error; err != nil {
		response.ErrorNotFound(c, "合同不存在")
		return nil, false
	}
	return &contract, true
}
