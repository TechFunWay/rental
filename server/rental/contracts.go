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

// Contract 租赁合同附件。FilePath 存相对 contractsDir 的路径
// （v0.6.0 起为 ledger-<ledgerId>/年/月/日/<uuid><ext>，老数据仍是无前缀的旧布局），
// FileName 保留用户上传时的原始文件名用于展示与下载命名。
type Contract struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
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

// contractAbsPath 相对路径 → 磁盘绝对路径（纯字符串拼接，不判断存在性）。
// 定位「实际该读哪个文件」请用 contractResolve。
func contractAbsPath(rel string) string {
	return filepath.Join(contractsDir(), filepath.FromSlash(rel))
}

// contractLedgerRel 拼接某个台账的新版相对路径：ledger-<ledgerId>/年/月/日/<uuid><ext>。
//
// 为什么要按台账分目录：合同是敏感件，若全部堆在同一个日期目录下，只要
// 文件名（uuid）泄露，别的台账的登录用户就能从共享目录直接取到文件本体——
// 数据库层的 ledger 过滤挡不住磁盘直读。分目录让「台账」成为路径的一部分，
// 越权者即便猜到 uuid 也落在别人的台账目录里。
func contractLedgerRel(ledgerID uint, now time.Time, name string) string {
	return filepath.ToSlash(filepath.Join(
		contractLedgerDir(ledgerID),
		fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", now.Month()), fmt.Sprintf("%02d", now.Day()),
		name,
	))
}

// contractLedgerDir 某台账的存储子目录名（相对 contractsDir）。
func contractLedgerDir(ledgerID uint) string {
	return fmt.Sprintf("ledger-%d", ledgerID)
}

// contractResolve 把数据库里的 Contract.FilePath 解析成磁盘上实际存在的绝对路径。
//
// 三条规则，顺序即优先级：
//  1. FilePath 带**本台账**的 ledger-<ledgerId>/ 前缀 → 直接用（新版布局）；
//  2. FilePath 带**别的台账**的 ledger-<n>/ 前缀 → 一律拒绝（跨台账路径）；
//  3. FilePath 没有任何台账前缀 → 老数据（旧布局）。**只有 0 号公共台账**
//     可以回退：老库升级后存量合同的 LedgerID 恒为 0、文件留在旧布局下，
//     这样它们仍能正常预览/下载；非 0 台账回退就会读到别人的老文件。
//
// 规则 2 是关键：没有它，0 号台账会顺着规则 3 的回退分支把
// ledger-2/... 这类别的台账路径当作「无前缀路径」直接读出来。
//
// 返回 (绝对路径, 是否找到)。不搬文件：文件迁移不可靠也不可逆，
// 回退查找既能兼容存量，又不会在失败时把数据弄丢。
func contractResolve(ledgerID uint, rel string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	prefix := contractLedgerDir(ledgerID)

	// 新版布局：FilePath 自带本台账的 ledger-<ledgerId>/ 前缀。
	if strings.HasPrefix(rel, prefix+"/") {
		if abs := contractAbsPath(rel); fileExists(abs) {
			return abs, true
		}
		// 带本台账前缀但文件不在：不再猜测别的布局，避免越权兜底。
		return "", false
	}

	// FilePath 带的是**别的台账**的 ledger-<n>/ 前缀时一律拒绝：
	// 这是跨台账路径，绝不能借 0 号台账的回退分支读到。
	if otherLedgerPrefix(rel) {
		return "", false
	}

	// FilePath 没有台账前缀 → 老数据（旧布局）。只有 0 号公共台账可回退：
	// 老库升级后存量合同的 LedgerID 恒为 0、文件留在旧布局下。
	// 非 0 台账不回退无前缀路径，否则会读到别人的老文件。
	if ledgerID != LegacySharedLedgerID {
		return "", false
	}
	if abs := contractAbsPath(rel); fileExists(abs) {
		return abs, true
	}
	return "", false
}

// otherLedgerPrefix 判断相对路径是否以某个 ledger-<n>/ 目录开头
// （n 为任意数字，含本台账的情形由调用方先行处理）。
func otherLedgerPrefix(rel string) bool {
	first, _, found := strings.Cut(rel, "/")
	if !found {
		return false
	}
	rest, ok := strings.CutPrefix(first, "ledger-")
	if !ok || rest == "" {
		return false
	}
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// fileExists 判断磁盘文件是否存在（且不是目录）。
func fileExists(abs string) bool {
	info, err := os.Stat(abs)
	return err == nil && !info.IsDir()
}

func setupContractRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 合同挂在租户名下，走「租户与合同」模块的勾选。
	// 读（列表/文件预览）并集、写（上传/删除）单台账（详见 rooms.go）。
	view := requireModule(db, ModuleTenants, PermView)
	edit := requireLedgerModule(db, ModuleTenants, PermEdit)
	full := requireLedgerModule(db, ModuleTenants, PermFull)
	api.GET("/rental/tenants/:id/contracts", view, handleContractList(db))
	api.POST("/rental/tenants/:id/contracts", edit, handleContractUpload(db))
	api.GET("/rental/contracts/:id/file", view, handleContractFile(db))
	api.DELETE("/rental/contracts/:id", full, handleContractDelete(db))
}

func handleContractList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenant, ok := findUserTenant(db, c)
		if !ok {
			return
		}
		contracts := make([]Contract, 0)
		// 租户归属已在 findUserTenant 校验；再限定同台账，避免跨台账孤儿合同
		// （历史脏数据）出现在本台账的租户名下。
		q, _ := scopeLedger(db.Where("tenant_id = ?", tenant.ID), c)
		if err := q.Order("created_at ASC, id ASC").Find(&contracts).Error; err != nil {
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
		if !requireLedgerWrite(db, c, tenant.LedgerID, ModuleTenants, PermEdit) {
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
		cq, _ := scopeLedger(db.Where("tenant_id = ?", tenant.ID), c)
		cq.Model(&Contract{}).Count(&existing)
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

			// v0.6.0：落盘路径带上合同所属台账（ledger-<ledgerId>/年/月/日/...），
			// 台账即目录，避免文件本体被跨台账直读。
			now := time.Now()
			rel := contractLedgerRel(tenant.LedgerID, now, uuid.New().String()+ext)
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
				UserID: currentUserID(c),
				// 合同继承租户台账：租户在哪个台账，合同就存在哪个台账。
				LedgerID: tenant.LedgerID,
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
		// 用数据库里的 LedgerID 解析路径（不信请求参数），并兼容旧布局的存量文件。
		abs, found := contractResolve(contract.LedgerID, contract.FilePath)
		if !found {
			response.ErrorNotFound(c, "合同文件不存在")
			return
		}
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
		if !requireLedgerWrite(db, c, contract.LedgerID, ModuleTenants, PermFull) {
			return
		}
		if err := db.Delete(contract).Error; err != nil {
			response.ErrorInternal(c, "删除合同失败")
			return
		}
		// 删文件同样按数据库里的 LedgerID 解析，兼容旧布局的存量文件——
		// 否则老合同删了行却留下文件，磁盘上永远清不掉。
		if abs, found := contractResolve(contract.LedgerID, contract.FilePath); found {
			_ = os.Remove(abs)
		}
		response.Success(c, gin.H{"deleted": true})
	}
}

// deleteTenantContracts 删除某租户的全部合同行与文件。
// 只删该租户所属台账的合同：跨台账孤儿合同（历史脏数据）不随本台账的
// 租户删除而被误清。
func deleteTenantContracts(db *gorm.DB, tenantID uint, ledgerID uint) {
	rows := make([]Contract, 0)
	q := db.Where("tenant_id = ?", tenantID)
	if ledgerColumnReady(db, "contracts") {
		q = q.Where("org_id = ?", ledgerID)
	}
	if err := q.Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		// 逐条按各自的 LedgerID 解析，老布局（ledger 0）与新布局都能清掉。
		if abs, found := contractResolve(row.LedgerID, row.FilePath); found {
			_ = os.Remove(abs)
		}
	}
	dq := db.Where("tenant_id = ?", tenantID)
	if ledgerColumnReady(db, "contracts") {
		dq = dq.Where("org_id = ?", ledgerID)
	}
	_ = dq.Delete(&Contract{}).Error
}

func findUserContract(db *gorm.DB, c *gin.Context) (*Contract, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的合同 ID")
		return nil, false
	}
	var contract Contract
	// 数据共享后按 ID 直取，不再限定录入人；但必须校验台账归属——
	// 合同文件是敏感件，换个 ID 就能下载别台账的合同是不可接受的。
	if err := db.First(&contract, id).Error; err != nil {
		response.ErrorNotFound(c, "合同不存在")
		return nil, false
	}
	if !inLedgerScope(c, contract.LedgerID) {
		response.ErrorNotFound(c, "合同不存在")
		return nil, false
	}
	return &contract, true
}
