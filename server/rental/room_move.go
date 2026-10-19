// room_move.go — 房源跨台账移动：把自己有权限的房源挪到另一个共同台账下。
//
// 为什么不能只改 rooms.org_id：房源是全应用的聚合根——租户与住户成员、抄表台账与
// 抄表照片、账单与收款、合同与资料附件、房间照片、待审的入住申请都挂在它下面，
// 而且**文件在盘上是按台账分目录存的**（attachments/ledger-<id>/…，读取时按前缀
// 校验归属）。只改房源那一行，结果是：租户/账单留在旧台账（列表里房子没了、账还在），
// 附件读取全 404。所以这是一次「整簇数据 + 磁盘文件」的迁移。
//
// 权限口径（与既有语义对齐）：
//   - 移出旧台账 = 删除口径 → 旧台账的「房源」模块要**完全档**；
//   - 移入新台账 = 新建口径 → 新台账的「房源」模块要**操作档**；
//   - 目标必须是**共同台账**（个人台账是默认隔离区，不能作为归属迁移的目标）；
//   - 两处判定都落在对象/目标台账上，不看本次请求带的 ?ledger_id=。
//
// 一致性策略：**先搬文件、再提交数据库**。搬文件阶段任一步失败就把已搬的挪回去；
// 数据库事务失败同样回滚文件。这样不会出现「库指向新目录、文件还在旧目录」。
package rental

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"smallgo/server/audit"
	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type roomMoveRequest struct {
	TargetLedgerID uint `json:"target_ledger_id"`
}

func setupRoomMoveRoutes(api *gin.RouterGroup, db *gorm.DB) {
	api.POST("/rental/rooms/:id/move", requireLedgerModule(db, ModuleRooms, PermFull), handleRoomMove(db))
}

func handleRoomMove(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		room, ok := findUserRoom(db, c)
		if !ok {
			return
		}
		// 移出旧台账：按删除口径要求完全档，且必须判在房源真正所属的台账上。
		if !requireLedgerWrite(db, c, room.LedgerID, ModuleRooms, PermFull) {
			return
		}
		var req roomMoveRequest
		if err := c.ShouldBindJSON(&req); err != nil || req.TargetLedgerID == 0 {
			response.ErrorBadRequest(c, "请选择要移动到的目标台账")
			return
		}
		if req.TargetLedgerID == room.LedgerID {
			response.ErrorBadRequest(c, "房源已经在目标台账里")
			return
		}
		target, ok := loadSharedLedgerForMove(db, c, req.TargetLedgerID)
		if !ok {
			return
		}
		// 移入新台账：按新建口径判操作档。
		if permRank[ledgerPerms(db, c.GetUint("userID"), target.ID)[ModuleRooms]] < permRank[PermEdit] {
			response.ErrorForbidden(c, "你在目标台账里没有「房源」模块的操作权限，请联系该台账管理者")
			return
		}

		var source Ledger
		_ = db.First(&source, room.LedgerID).Error

		counts, err := moveRoomToLedger(db, room, target.ID)
		if err != nil {
			response.ErrorInternal(c, "移动房源失败："+err.Error())
			return
		}
		audit.Log(db, c, "rental_room_move", "room", room.ID, fmt.Sprintf(
			"房源 %s 从台账「%s」移动到「%s」（租户 %d、成员 %d、账单 %d、抄表 %d、附件 %d）",
			room.RoomNo, source.Name, target.Name,
			counts["tenants"], counts["members"], counts["bills"], counts["meter_records"],
			counts["contracts"]+counts["attachments"]+counts["room_photos"]+counts["meter_photos"]))
		response.Success(c, gin.H{
			"room_id":          room.ID,
			"target_ledger_id": target.ID,
			"counts":           counts,
		})
	}
}

// loadSharedLedgerForMove 取目标台账并校验：必须存在、调用者是成员、且是共同台账。
// 三种情况各回各的错，别让用户只看到一句「数据不存在」。
func loadSharedLedgerForMove(db *gorm.DB, c *gin.Context, id uint) (*Ledger, bool) {
	var l Ledger
	if err := db.First(&l, id).Error; err != nil {
		response.ErrorBadRequest(c, "目标台账不存在")
		return nil, false
	}
	if !containsLedger(activeLedgerIDsOrNil(db, c.GetUint("userID")), l.ID) {
		response.ErrorForbidden(c, "你不是目标台账的成员")
		return nil, false
	}
	if l.Kind != LedgerKindShared {
		response.ErrorBadRequest(c, "只能把房源移动到共同台账")
		return nil, false
	}
	return &l, true
}

// movedFile 记录一次文件的搬迁，用于失败回滚。
type movedFile struct{ from, to string }

// moveOneFile 把一份附件从旧台账目录挪到新台账目录，返回新的相对路径。
//
//   - 带旧台账前缀：换成新台账前缀；
//   - 老布局（无 ledger- 前缀，0 号台账的存量数据）：一并搬进新台账目录，
//     否则新台账按前缀校验会拒绝读取；
//   - 带**别的**台账前缀（历史异常数据）：不动，避免把别人的文件搬走。
func moveOneFile(oldLedger, newLedger uint, rel string) (string, *movedFile, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return rel, nil, nil
	}
	rel = filepath.ToSlash(rel)
	oldPrefix := fmt.Sprintf("ledger-%d/", oldLedger)
	var newRel string
	switch {
	case strings.HasPrefix(rel, oldPrefix):
		newRel = fmt.Sprintf("ledger-%d/%s", newLedger, strings.TrimPrefix(rel, oldPrefix))
	case otherLedgerPrefix(rel):
		return rel, nil, nil
	default:
		newRel = fmt.Sprintf("ledger-%d/%s", newLedger, rel)
	}

	src := attachmentAbsPath(rel)
	if !fileExists(src) {
		// 文件本来就不在（历史脏数据）：路径保持原样，不因为搬家再把记录改坏。
		return rel, nil, nil
	}
	dst := attachmentAbsPath(newRel)
	if !withinAttachmentsRoot(dst) {
		return rel, nil, fmt.Errorf("附件目标路径越界")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return rel, nil, fmt.Errorf("创建附件目录失败")
	}
	if err := os.Rename(src, dst); err != nil {
		return rel, nil, fmt.Errorf("移动附件文件失败")
	}
	return newRel, &movedFile{from: src, to: dst}, nil
}

// moveRoomToLedger 把一间房及其名下全部业务数据迁到目标台账（库 + 盘）。
// 返回给前端的件数统计。任何一步失败都尽量回到原状。
func moveRoomToLedger(db *gorm.DB, room *Room, targetLedgerID uint) (map[string]int, error) {
	oldLedger := room.LedgerID
	counts := map[string]int{}

	var tenants []Tenant
	db.Where("room_id = ? AND org_id = ?", room.ID, oldLedger).Find(&tenants)
	tenantIDs := make([]uint, 0, len(tenants))
	for _, t := range tenants {
		tenantIDs = append(tenantIDs, t.ID)
	}
	var bills []Bill
	db.Where("room_id = ? AND org_id = ?", room.ID, oldLedger).Find(&bills)
	billIDs := make([]uint, 0, len(bills))
	for _, b := range bills {
		billIDs = append(billIDs, b.ID)
	}

	// ---------- 1. 先搬盘上的文件 ----------
	// 四类带文件的记录各一条 SELECT 取回 id + 路径（不要按行查，避免 N+1）
	type fileRow struct {
		ID       uint
		FilePath string
	}
	var contractRows, attachmentRows, roomPhotoRows, meterPhotoRows []fileRow
	if len(tenantIDs) > 0 {
		db.Model(&Contract{}).Select("id", "file_path").
			Where("tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger).Scan(&contractRows)
		db.Model(&TenantAttachment{}).Select("id", "file_path").
			Where("tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger).Scan(&attachmentRows)
	}
	db.Model(&RoomPhoto{}).Select("id", "file_path").
		Where("room_id = ? AND org_id = ?", room.ID, oldLedger).Scan(&roomPhotoRows)
	db.Model(&MeterPhoto{}).Select("id", "file_path").
		Where("room_id = ? AND org_id = ?", room.ID, oldLedger).Scan(&meterPhotoRows)

	moved := make([]movedFile, 0)
	rollbackFiles := func() {
		for i := len(moved) - 1; i >= 0; i-- {
			_ = os.MkdirAll(filepath.Dir(moved[i].from), 0o755)
			_ = os.Rename(moved[i].to, moved[i].from)
		}
	}

	// 四类文件各自搬家，记录新路径供事务里写回
	newContractPath := map[uint]string{}
	newAttachmentPath := map[uint]string{}
	newRoomPhotoPath := map[uint]string{}
	newMeterPhotoPath := map[uint]string{}
	moveAll := func(rows []fileRow, store map[uint]string) error {
		for _, r := range rows {
			newRel, mf, err := moveOneFile(oldLedger, targetLedgerID, r.FilePath)
			if err != nil {
				return err
			}
			if mf != nil {
				moved = append(moved, *mf)
			}
			store[r.ID] = newRel
		}
		return nil
	}
	for _, job := range []struct {
		rows  []fileRow
		store map[uint]string
	}{
		{contractRows, newContractPath},
		{attachmentRows, newAttachmentPath},
		{roomPhotoRows, newRoomPhotoPath},
		{meterPhotoRows, newMeterPhotoPath},
	} {
		if err := moveAll(job.rows, job.store); err != nil {
			rollbackFiles()
			return nil, err
		}
	}

	// ---------- 2. 再提交数据库 ----------
	err := db.Transaction(func(tx *gorm.DB) error {
		move := func(model interface{}, where string, args ...interface{}) error {
			return tx.Model(model).Where(where, args...).Update("org_id", targetLedgerID).Error
		}
		if err := tx.Model(&Room{}).Where("id = ?", room.ID).Update("org_id", targetLedgerID).Error; err != nil {
			return err
		}
		if err := move(&RoomPhoto{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if err := move(&MeterRecord{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if err := move(&MeterPhoto{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if err := move(&Tenant{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if err := move(&TenantApplication{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		// 免登录**房源登记**（room_applications）也挂在这间房上：不跟着走的话，
		// 源台账会留着一条指向「已经搬走的房源」的历史登记；如果它还是待审状态，
		// 源台账的村委点「通过」会按房号再建一间新房源（去重只查 Room 表），
		// 同一间房在两边各有一份。
		if err := move(&RoomApplication{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if err := move(&Bill{}, "room_id = ? AND org_id = ?", room.ID, oldLedger); err != nil {
			return err
		}
		if len(tenantIDs) > 0 {
			if err := move(&TenantMember{}, "tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger); err != nil {
				return err
			}
			if err := move(&TenantChangeRequest{}, "tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger); err != nil {
				return err
			}
			if err := move(&Contract{}, "tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger); err != nil {
				return err
			}
			if err := move(&TenantAttachment{}, "tenant_id IN ? AND org_id = ?", tenantIDs, oldLedger); err != nil {
				return err
			}
		}
		if len(billIDs) > 0 {
			if err := move(&Payment{}, "bill_id IN ? AND org_id = ?", billIDs, oldLedger); err != nil {
				return err
			}
		}
		// 附件路径写回（库里的相对路径必须与盘上一致，否则读取按前缀校验会 404）
		for id, rel := range newContractPath {
			if err := tx.Model(&Contract{}).Where("id = ?", id).Update("file_path", rel).Error; err != nil {
				return err
			}
		}
		for id, rel := range newAttachmentPath {
			if err := tx.Model(&TenantAttachment{}).Where("id = ?", id).Update("file_path", rel).Error; err != nil {
				return err
			}
		}
		for id, rel := range newRoomPhotoPath {
			if err := tx.Model(&RoomPhoto{}).Where("id = ?", id).Update("file_path", rel).Error; err != nil {
				return err
			}
		}
		for id, rel := range newMeterPhotoPath {
			if err := tx.Model(&MeterPhoto{}).Where("id = ?", id).Update("file_path", rel).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		rollbackFiles()
		return nil, err
	}

	// ---------- 3. 统计（供审计与前端提示） ----------
	counts["tenants"] = len(tenantIDs)
	counts["bills"] = len(billIDs)
	counts["contracts"] = len(contractRows)
	counts["attachments"] = len(attachmentRows)
	counts["room_photos"] = len(roomPhotoRows)
	counts["meter_photos"] = len(meterPhotoRows)
	counts["meter_records"] = int(countInLedger(db, &MeterRecord{}, "room_id = ? AND org_id = ?", room.ID, targetLedgerID))
	if len(tenantIDs) > 0 {
		counts["members"] = int(countInLedger(db, &TenantMember{}, "tenant_id IN ? AND org_id = ?", tenantIDs, targetLedgerID))
		counts["change_requests"] = int(countInLedger(db, &TenantChangeRequest{}, "tenant_id IN ? AND org_id = ?", tenantIDs, targetLedgerID))
	}
	if len(billIDs) > 0 {
		counts["payments"] = int(countInLedger(db, &Payment{}, "bill_id IN ? AND org_id = ?", billIDs, targetLedgerID))
	}
	counts["applications"] = int(countInLedger(db, &TenantApplication{}, "room_id = ? AND org_id = ?", room.ID, targetLedgerID))
	counts["room_applications"] = int(countInLedger(db, &RoomApplication{}, "room_id = ? AND org_id = ?", room.ID, targetLedgerID))
	return counts, nil
}

func countInLedger(db *gorm.DB, model interface{}, where string, args ...interface{}) int64 {
	var n int64
	db.Model(model).Where(where, args...).Count(&n)
	return n
}
