// room_items.go — 房间物品台账：一间房里有什么东西（冰箱/洗衣机/机顶盒/路由器/两把钥匙…），
// 用于退租时核对缺失与损坏。
//
// 为什么**绑房间、不绑租户**（用户明确要求）：物品属于这间房，与哪一任租客无关。
// 「住户搬走时，我查看屋里还是入住时的那些物品就行」——绑租户的话换一任租客就要
// 重录一遍，历史清单还会跟着租户走。因此 RoomItem 只挂 room_id。
//
// Builtin 直接回答用户最痛的那一点：true = 房东提供的（退租要核对缺失/损坏），
// false = 租客自带的（搬走时跟着租客走）。用户原话就是「分不清哪些是自己买的、
// 哪些是租客自带的」。
//
// 为什么物品照片单独一张表（RoomItemPhoto）：与 room_photos.go / meter_photos.go 同一条
// 既有约定——「一类照片一张表」。物品照片描述的是某件物品，房源照片描述的是房子本身，
// 混在一张表里会让「删某件物品的照片」误删房源照片。
//
// 存储与读取口径与房源照片/租户附件完全一致：
//   - 文件本体存 <data-dir>/attachments/ledger-<ledgerId>/年/月/日/<uuid><ext>；
//   - 扩展名由服务端内容嗅探推导，绝不信任客户端文件名与扩展名；只收图片；
//   - 读取走带归属校验的专属接口，不用公开的 /uploads；越权一律 404「不存在」，
//     避免拿 ID 探测别的台账里有什么。
//
// 路由不挂在 setupRoutes 里（那是 rental.go 的地盘，改它容易与别人的改动撞车）：
// 本文件自己 apps.Register 一个 App，在 SetupAuth 里补上 resolveLedger 与
// denyTenantRole 两层中间件——口径与 setupRoutes（rental.go:616-623）逐字一致。
// 模型也在本文件的 init 里注册，同样不改 rental.go。
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

	"smallgo/server/apps"
	"smallgo/server/database"
	"smallgo/server/logger"
	"smallgo/server/response"
	"smallgo/server/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// 上限口径（与服务端一致的前端提示见 web/src/components/RoomItemsCard.vue）：
//   - 单文件 20MB（与附件/合同/照片一致，手机原图够用）、单请求最多 6 张；
//   - 每件物品最多 10 张（一台空调的型号铭牌、遥控器、外观各来一张就够）；
//   - 每间房最多 60 件物品（够记一整套出租屋的家具家电，又不至于变成资产系统）。
const (
	roomItemMaxFileSize     = 20 << 20
	roomItemPhotoBatchLimit = 6
	roomItemPhotoMaxPerItem = 10
	roomItemMaxPerRoom      = 60

	roomItemNameMax      = 50 // 物品名称上限（字）
	roomItemConditionMax = 50 // 状况说明上限（字）
	roomItemNoteMax      = 200
	roomItemQtyMin       = 1
	roomItemQtyMax       = 999
)

// roomItemPhotoInputHint 报错文案里的用途提示。
const roomItemPhotoInputHint = "请选择物品照片（图片文件）"

// RoomItem 房间物品台账的一行：某间房里的某件物品。
//
// 归属：LedgerID（org_id）决定数据隔离，RoomID 决定它属于哪间房；
// UserID 只是录入人戳，仅作溯源。
// Builtin 不用 gorm 的 default 标签：false（租客自带）是合法取值，打了 default
// 会被 GORM 用默认值顶掉——与 Tenant.PayDay/RemindDays 同一个坑（rental.go:155-157）。
type RoomItem struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"index" json:"-"`                                          // 录入人，仅溯源
	LedgerID  uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID    uint      `gorm:"index;not null" json:"room_id"`
	Name      string    `gorm:"not null" json:"name"` // 物品名，如「路由器」
	Quantity  int       `json:"quantity"`             // 数量，1–999
	Condition string    `json:"condition"`            // 状况：完好/损坏/缺失…自由文本
	Note      string    `json:"note"`                 // 备注
	Builtin   bool      `json:"builtin"`              // true=房东提供；false=租客自带
	Sort      int       `json:"sort"`                 // 展示顺序，小的在前
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RoomItemPhoto 物品照片。FilePath 存相对 attachmentsDir 的路径
// （ledger-<ledgerId>/年/月/日/<uuid><ext>），FileName 保留原始文件名用于展示。
type RoomItemPhoto struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	UserID     uint      `gorm:"index" json:"-"`                                          // 上传人，仅溯源
	LedgerID   uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomItemID uint      `gorm:"index;not null" json:"room_item_id"`
	FileName   string    `gorm:"not null" json:"file_name"`
	FilePath   string    `json:"-"`
	FileSize   int64     `json:"file_size"`
	MimeType   string    `json:"mime_type"`
	CreatedAt  time.Time `json:"created_at"`
}

func init() {
	database.RegisterModels(&RoomItem{}, &RoomItemPhoto{})

	// 单独注册一个 App 只为挂本模块的路由：功能完全自包含在一个文件里，
	// 不必改 rental.go 的 init（也就不会与别人的改动撞车）。用 SetupAuth（框架
	// authGroup，已登录）而不是 Setup（publicGroup，免登录限流组）——物品台账是
	// 房东内部数据，绝不能挂在公开组上。
	apps.Register(apps.App{
		Name:        "rental_room_items",
		DisplayName: "房间物品台账",
		SetupAuth: func(auth *gin.RouterGroup, db *gorm.DB) {
			// 与 setupRoutes 同一套前置中间件：resolveLedger 解析本次请求的台账
			// 范围与写入目标，denyTenantRole 一律拒绝租户账号。
			scoped := auth.Group("", resolveLedger(db))
			scoped = scoped.Group("", denyTenantRole())
			setupRoomItemRoutes(scoped, db)
		},
	})
}

// setupRoomItemRoutes 挂载房间物品台账接口。
//
// 权限与房源照片同档（物品是房源模块的一部分）：
//   - 读（列表、取照片文件）用 requireModule 并集准入，handler 按可见台账收窄；
//   - 写（增改删、传照片）用 requireLedgerModule 判本次请求选中的台账，handler
//     取到对象后再用 requireLedgerWrite 按**对象所属台账**复核一次，防「去掉
//     ledger_id 就改别的台账的数据」。
func setupRoomItemRoutes(api *gin.RouterGroup, db *gorm.DB) {
	view := requireModule(db, ModuleRooms, PermView)
	edit := requireLedgerModule(db, ModuleRooms, PermEdit)

	api.GET("/rental/room-items", view, handleRoomItemList(db))
	api.POST("/rental/rooms/:id/items", edit, handleRoomItemCreate(db))
	api.PUT("/rental/room-items/:id", edit, handleRoomItemUpdate(db))
	api.DELETE("/rental/room-items/:id", edit, handleRoomItemDelete(db))

	api.POST("/rental/room-items/:id/photos", edit, handleRoomItemPhotoUpload(db))
	api.GET("/rental/room-item-photos/:id/file", view, handleRoomItemPhotoFile(db))
	api.DELETE("/rental/room-item-photos/:id", edit, handleRoomItemPhotoDelete(db))
}

// roomItemView 列表出参：在物品行上带出它的照片（前端一件物品一组照片，
// 一次请求取全，避免每件物品再打一次接口）。
type roomItemView struct {
	RoomItem
	Photos     []RoomItemPhoto `json:"photos"`
	PhotoCount int             `json:"photo_count"`
}

// roomItemRequest 新增/编辑物品的入参。
// Builtin/Sort 用指针：nil 表示「没传」，新增时分别取默认值 true（房东提供）
// 与「排到最后」，编辑时保持原值。
type roomItemRequest struct {
	Name      string `json:"name"`
	Quantity  int    `json:"quantity"`
	Condition string `json:"condition"`
	Note      string `json:"note"`
	Builtin   *bool  `json:"builtin"`
	Sort      *int   `json:"sort"`
}

// roomItemFields 归一化后的落库字段（校验通过后由调用方写入模型）。
type roomItemFields struct {
	Name      string
	Quantity  int
	Condition string
	Note      string
	Builtin   bool
}

// normalizeRoomItemRequest 校验并归一化入参。返回空串表示通过。
//
// 口径：
//   - name 去首尾空白后必填、≤50 字；
//   - quantity 夹在 1–999（用户填 0 或负数按 1 计，填超了就按上限，不整单报错——
//     「两把钥匙」记成 2，「一批 1200 个螺丝」也不该被拦下）；
//   - condition ≤50 字、note ≤200 字（自由文本，但要有上限，防止把整段话塞进来）；
//   - builtin 缺省为 true（房东提供，最常见的情形）。
func normalizeRoomItemRequest(req *roomItemRequest) (roomItemFields, string) {
	out := roomItemFields{}
	out.Name = strings.TrimSpace(req.Name)
	if out.Name == "" {
		return out, "请填写物品名称"
	}
	if len([]rune(out.Name)) > roomItemNameMax {
		return out, fmt.Sprintf("物品名称最多 %d 个字", roomItemNameMax)
	}
	out.Quantity = req.Quantity
	if out.Quantity < roomItemQtyMin {
		out.Quantity = roomItemQtyMin
	}
	if out.Quantity > roomItemQtyMax {
		out.Quantity = roomItemQtyMax
	}
	out.Condition = strings.TrimSpace(req.Condition)
	if len([]rune(out.Condition)) > roomItemConditionMax {
		return out, fmt.Sprintf("状况最多 %d 个字", roomItemConditionMax)
	}
	out.Note = strings.TrimSpace(req.Note)
	if len([]rune(out.Note)) > roomItemNoteMax {
		return out, fmt.Sprintf("备注最多 %d 个字", roomItemNoteMax)
	}
	out.Builtin = true
	if req.Builtin != nil {
		out.Builtin = *req.Builtin
	}
	return out, ""
}

// handleRoomItemList 列出可见台账内的房间物品（可按 room_id 筛），
// 每行带出该物品的照片。排序：sort 小的在前，同 sort 按创建顺序。
func handleRoomItemList(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 每间房最多 60 件，pageSize 给 100 足够一次取全。
		page, pageSize := utils.NormalizePage(utils.Atoi(c.Query("page"), 1), utils.Atoi(c.Query("pageSize"), 100))
		query, empty := scopeLedger(db.Model(&RoomItem{}), c)
		if empty {
			response.SuccessPage(c, []roomItemView{}, 0, page, pageSize)
			return
		}
		if roomID := utils.Atoi(c.Query("room_id"), 0); roomID > 0 {
			query = query.Where("room_id = ?", roomID)
		}
		var total int64
		if err := query.Count(&total).Error; err != nil {
			response.ErrorInternal(c, "查询房间物品失败")
			return
		}
		items := make([]RoomItem, 0)
		if err := query.Order("sort ASC, id ASC").
			Offset(utils.Offset(page, pageSize)).Limit(pageSize).
			Find(&items).Error; err != nil {
			response.ErrorInternal(c, "查询房间物品失败")
			return
		}
		response.SuccessPage(c, roomItemViews(db, items), total, page, pageSize)
	}
}

// roomItemViews 把物品行拼成出参：一次查出这批物品的全部照片再按物品分组，
// 不做「每件物品查一次照片」的 N+1。
func roomItemViews(db *gorm.DB, items []RoomItem) []roomItemView {
	views := make([]roomItemView, 0, len(items))
	if len(items) == 0 {
		return views
	}
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	photos := make([]RoomItemPhoto, 0)
	db.Where("room_item_id IN ?", ids).Order("id ASC").Find(&photos)
	grouped := make(map[uint][]RoomItemPhoto, len(items))
	for _, p := range photos {
		grouped[p.RoomItemID] = append(grouped[p.RoomItemID], p)
	}
	for _, it := range items {
		list := grouped[it.ID]
		if list == nil {
			list = make([]RoomItemPhoto, 0)
		}
		views = append(views, roomItemView{RoomItem: it, Photos: list, PhotoCount: len(list)})
	}
	return views
}

// handleRoomItemCreate 在指定房间下新增一件物品（POST /rental/rooms/:id/items）。
// 权限落在房间所属台账上，并在那里复核「房源」模块的操作档。
func handleRoomItemCreate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		var req roomItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		fields, msg := normalizeRoomItemRequest(&req)
		if msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		var existing int64
		db.Model(&RoomItem{}).Where("room_id = ? AND org_id = ?", room.ID, room.LedgerID).Count(&existing)
		if int(existing) >= roomItemMaxPerRoom {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"%s 的物品最多 %d 件，请先删除不用的再添加", room.RoomNo, roomItemMaxPerRoom))
			return
		}

		item := RoomItem{
			UserID:   currentUserID(c),
			LedgerID: room.LedgerID,
			RoomID:   room.ID,
			Name:     fields.Name, Quantity: fields.Quantity,
			Condition: fields.Condition, Note: fields.Note, Builtin: fields.Builtin,
		}
		if req.Sort != nil {
			item.Sort = *req.Sort
		} else {
			item.Sort = nextRoomItemSort(db, room.ID, room.LedgerID)
		}
		if err := db.Create(&item).Error; err != nil {
			response.ErrorInternal(c, "保存物品失败")
			return
		}
		response.Success(c, roomItemView{RoomItem: item, Photos: make([]RoomItemPhoto, 0)})
	}
}

// nextRoomItemSort 新物品的排位：现有最大 sort + 1（排在最后，符合「记一笔加一条」
// 的直觉）。房内没有物品时返回 1。
func nextRoomItemSort(db *gorm.DB, roomID, ledgerID uint) int {
	var row struct{ Max int }
	db.Model(&RoomItem{}).Select("COALESCE(MAX(sort), 0) AS max").
		Where("room_id = ? AND org_id = ?", roomID, ledgerID).Scan(&row)
	return row.Max + 1
}

// handleRoomItemUpdate 编辑一件物品（名称/数量/状况/备注/房东提供/排序）。
func handleRoomItemUpdate(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomItem(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		var req roomItemRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请求无效")
			return
		}
		fields, msg := normalizeRoomItemRequest(&req)
		if msg != "" {
			response.ErrorBadRequest(c, msg)
			return
		}
		item.Name = fields.Name
		item.Quantity = fields.Quantity
		item.Condition = fields.Condition
		item.Note = fields.Note
		item.Builtin = fields.Builtin
		if req.Sort != nil {
			item.Sort = *req.Sort
		}
		if err := db.Save(item).Error; err != nil {
			response.ErrorInternal(c, "保存物品失败")
			return
		}
		views := roomItemViews(db, []RoomItem{*item})
		response.Success(c, views[0])
	}
}

// handleRoomItemDelete 删除一件物品：它的照片记录与盘上文件一并清掉。
//
// 清理动作放在 RoomItem.BeforeDelete 钩子里，所以不论从哪条路径删（本接口、
// 房间删除级联、将来的批量操作）都不会留下孤儿照片。
func handleRoomItemDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomItem(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if err := db.Delete(item).Error; err != nil {
			response.ErrorInternal(c, "删除物品失败")
			return
		}
		response.Success(c, gin.H{"deleted": item.ID})
	}
}

// handleRoomItemPhotoUpload 上传物品照片（multipart，字段名 files 或 file）。
// 与房源照片同一口径：先整批校验（大小 + 内容嗅探），全过再落盘，
// 避免「第 3 张不是图片」时前两张已经存进去的半成功状态。
func handleRoomItemPhotoUpload(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomItem(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		form, err := c.MultipartForm()
		if err != nil {
			response.ErrorBadRequest(c, roomItemPhotoInputHint)
			return
		}
		files := append(form.File["files"], form.File["file"]...)
		if len(files) == 0 {
			response.ErrorBadRequest(c, roomItemPhotoInputHint)
			return
		}
		if len(files) > roomItemPhotoBatchLimit {
			response.ErrorBadRequest(c, fmt.Sprintf("单次最多上传 %d 张照片", roomItemPhotoBatchLimit))
			return
		}
		var existing int64
		db.Model(&RoomItemPhoto{}).
			Where("room_item_id = ? AND org_id = ?", item.ID, item.LedgerID).Count(&existing)
		if int(existing)+len(files) > roomItemPhotoMaxPerItem {
			response.ErrorBadRequest(c, fmt.Sprintf(
				"「%s」的照片最多 %d 张，请先删除不用的再传", item.Name, roomItemPhotoMaxPerItem))
			return
		}

		type pendingPhoto struct {
			file *multipart.FileHeader
			ext  string
			mime string
		}
		pending := make([]pendingPhoto, 0, len(files))
		for _, fh := range files {
			if fh.Size <= 0 || fh.Size > roomItemMaxFileSize {
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
				response.ErrorBadRequest(c, fmt.Sprintf("%s：只支持物品照片（图片文件）", fh.Filename))
				return
			}
			pending = append(pending, pendingPhoto{file: fh, ext: ext, mime: mimeType})
		}

		created := make([]RoomItemPhoto, 0, len(pending))
		for _, p := range pending {
			now := time.Now()
			rel := attachmentLedgerRel(item.LedgerID, now, uuid.New().String()+p.ext)
			abs := attachmentAbsPath(rel)
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				response.ErrorInternal(c, "创建存储目录失败")
				return
			}
			if err := c.SaveUploadedFile(p.file, abs); err != nil {
				response.ErrorInternal(c, "保存照片失败")
				return
			}
			row := RoomItemPhoto{
				UserID:     currentUserID(c),
				LedgerID:   item.LedgerID,
				RoomItemID: item.ID,
				FileName:   strings.TrimSpace(p.file.Filename),
				FilePath:   rel,
				FileSize:   p.file.Size,
				MimeType:   p.mime,
			}
			if row.FileName == "" {
				row.FileName = "物品照片" + p.ext
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

// handleRoomItemPhotoFile 读取一张物品照片（带归属校验，越权 404）。
func handleRoomItemPhotoFile(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomItemPhoto(db, c)
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

// handleRoomItemPhotoDelete 删除一张物品照片：先删库再删盘（与房源照片同口径）。
func handleRoomItemPhotoDelete(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		item, ok := findUserRoomItemPhoto(db, c)
		if !ok {
			return
		}
		if !requireLedgerWrite(db, c, item.LedgerID, ModuleRooms, PermEdit) {
			return
		}
		if err := db.Delete(&RoomItemPhoto{}, item.ID).Error; err != nil {
			response.ErrorInternal(c, "删除照片失败")
			return
		}
		if abs, ok := attachmentResolve(item.LedgerID, item.FilePath); ok {
			_ = os.Remove(abs)
		}
		response.Success(c, gin.H{"deleted": item.ID})
	}
}

// findUserRoomItem 取路径上的物品并校验台账可见性（不可见按「不存在」处理）。
func findUserRoomItem(db *gorm.DB, c *gin.Context) (*RoomItem, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的物品 ID")
		return nil, false
	}
	var item RoomItem
	if err := db.First(&item, id).Error; err != nil {
		response.ErrorNotFound(c, "物品不存在")
		return nil, false
	}
	if !inLedgerScope(c, item.LedgerID) {
		response.ErrorNotFound(c, "物品不存在")
		return nil, false
	}
	return &item, true
}

// findUserRoomItemPhoto 取路径上的物品照片并校验台账可见性。
func findUserRoomItemPhoto(db *gorm.DB, c *gin.Context) (*RoomItemPhoto, bool) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		response.ErrorBadRequest(c, "无效的照片 ID")
		return nil, false
	}
	var item RoomItemPhoto
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

// BeforeDelete 是 RoomItem 的 GORM 删前钩子：删一件物品时把它的照片记录与盘上
// 文件一并清掉。放在模型钩子里而不是只写在 handler 里，是为了让任何删除路径
// （本模块接口、房间删除时的级联清理、将来的批量操作）都不会留下孤儿照片。
func (i *RoomItem) BeforeDelete(tx *gorm.DB) error {
	if i.ID == 0 {
		// 批量删除（Where + Delete(&RoomItem{})、Delete(&RoomItem{}, ids)）时钩子
		// 拿到零值物品，具体要清哪些物品未知，这里不猜；那些路径由调用方显式调
		// deleteRoomItemPhotosByItemIDs 完成。
		return nil
	}
	deleteRoomItemPhotosByItemIDs(tx, []uint{i.ID})
	return nil
}

// AfterDelete 是 Room 的 GORM 删后钩子：房源删除时把它名下的物品台账（行 +
// 盘上照片）一并清掉。
//
// 为什么用模型钩子而不是在 handleRoomDelete 里加一行调用：rooms.go 是本模块的
// 公共文件，物品台账不该为了自己的一行清理去改它（也就不会与别人的改动撞车）；
// 钩子挂在 Room 上，任何删房源的路径都自动生效——与既有的「删房源时显式调
// deleteRoomPhotos / deleteRoomMeterPhotos」是同一个清理目标，只是触发点不同。
//
// 前提：调用方传的是**已加载的** Room（handleRoomDelete 就是 db.Delete(room)），
// 钩子才拿得到 ID 与台账。若以 db.Delete(&Room{}, id) 这种零值形态删除，钩子
// 拿到的是零值、无从得知删了哪一间——不猜，只记一条告警，避免留下无声的孤儿。
func (r *Room) AfterDelete(tx *gorm.DB) error {
	if r.ID == 0 {
		logger.Warn("删除了房源但钩子拿不到房源 ID（零值删除形态），房间物品台账未随之清理；请改用已加载的 Room 删除")
		return nil
	}
	deleteRoomItems(tx, r.ID, r.LedgerID)
	return nil
}

// freshQuery 给模型钩子里的嵌套读写用：返回一份 Statement 干净、但**共用外层
// 事务连接**的会话，清理动作因此与「删房源 / 删物品」同事务，外层回滚时一并回滚。
//
// 为什么不能只写 db.Session(&gorm.Session{NewDB: true})：那一步只是把 Statement 指到
// 外层那条语句上（表名、Model、WHERE、Vars 全是外层留下的），后续链式调用会带着
// 外层的表名拼 SQL——实测在删 rooms 的钩子里发 DELETE，语句被拼成对 rooms 表操作，
// 一行都删不掉。这里补一次 Model(model) 把语句落到调用方自己的表上，彻底断开继承。
func freshQuery(db *gorm.DB, model interface{}) *gorm.DB {
	return db.Session(&gorm.Session{NewDB: true}).Model(model)
}

// deleteRoomItems 清理某房间名下的全部物品（行 + 盘上照片），只清本台账的行。
// 供 Room.AfterDelete 调用；roomID 为 0 时直接返回（无从得知要清哪一间）。
func deleteRoomItems(tx *gorm.DB, roomID, ledgerID uint) {
	if roomID == 0 || !tableExists(freshQuery(tx, &RoomItem{}), "room_items") {
		return
	}
	var items []RoomItem
	if err := freshQuery(tx, &RoomItem{}).
		Where("room_id = ? AND org_id = ?", roomID, ledgerID).Find(&items).Error; err != nil {
		// 清理失败不能反过来让删房源失败（房源已经删掉了），记日志即可。
		logger.Error("清理房间物品失败（读取物品列表出错）room=%d: %v", roomID, err)
		return
	}
	if len(items) == 0 {
		return
	}
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	deleteRoomItemPhotosByItemIDs(tx, ids)
	// 照片已在上一步显式清掉；这里删物品行本身。批量删除时 BeforeDelete 钩子拿到
	// 的是零值物品（i.ID == 0），钩子直接返回，不会重复清理。
	if err := freshQuery(tx, &RoomItem{}).
		Where("room_id = ? AND org_id = ?", roomID, ledgerID).Delete(&RoomItem{}).Error; err != nil {
		logger.Error("清理房间物品失败（删除物品行出错）room=%d: %v", roomID, err)
	}
}

// deleteRoomItemPhotosByItemIDs 删除若干物品的全部照片：先删盘上文件再删库行。
// 先删文件可以保证不会出现「库行没了、文件还留在盘上」的孤儿；即使库行删除失败，
// 前端也只会看到「照片文件不存在」，而不是一份删不掉的隐藏文件。
func deleteRoomItemPhotosByItemIDs(tx *gorm.DB, itemIDs []uint) {
	if len(itemIDs) == 0 || !tableExists(freshQuery(tx, &RoomItemPhoto{}), "room_item_photos") {
		return
	}
	var photos []RoomItemPhoto
	if err := freshQuery(tx, &RoomItemPhoto{}).
		Where("room_item_id IN ?", itemIDs).Find(&photos).Error; err != nil {
		logger.Error("清理物品照片失败（读取照片列表出错）: %v", err)
		return
	}
	for _, p := range photos {
		if abs, ok := attachmentResolve(p.LedgerID, p.FilePath); ok {
			_ = os.Remove(abs)
		}
	}
	if err := freshQuery(tx, &RoomItemPhoto{}).
		Where("room_item_id IN ?", itemIDs).Delete(&RoomItemPhoto{}).Error; err != nil {
		logger.Error("清理物品照片失败（删除照片行出错）: %v", err)
	}
}
