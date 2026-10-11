// Package rental — 租房管理系统业务模块。
//
// 提供房源、租户、月度抄表账单（水电气费用自动计算）、收款与欠缴跟踪、
// CSV 模板导入导出与收费单据数据，以及租客自助登记（公开登记链接）。
//
// 数据归属（2026-10-09 起，见 ledger.go）：每条业务数据必属于恰好一个**台账**。
// 每个账号注册即拥有只有自己能用「个人台账」；任何用户可自建「共同台账」并邀请
// 他人，按人按模块勾选权限（房源/租户/抄表/账单/收款/统计 各 view/edit/full）。
// user_id 列只作录入人溯源，不再用于隔离。超管在业务数据上没有特权：不是某个
// 台账的成员就看不到它的数据（数据库整库备份属系统级能力，仍对超管开放——
// 服务器持有者本来就能直接读库文件，这一点在 ledger.go 的口径里已注明）。
package rental

import (
	"errors"
	"strings"
	"time"

	"smallgo/server/apps"
	"smallgo/server/database"
	"smallgo/server/notify"
	"smallgo/server/sysconfig"
	"smallgo/server/version"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 水费计费方式：按吨（用量 × 单价）或包月（每月固定金额）。
// v0.2.3 起按租户设置，留空表示跟随用户级全局默认（偏好设置 → 租房设置）。
const (
	waterModeMeter   = "meter"   // 按吨计价：水费 = 用量 × 单价
	waterModeMonthly = "monthly" // 包月：水费 = 每月固定金额
)

// 租户级"跟随全局默认"哨兵值：缴费日与提前提醒天数用 -1 表示未设置，
// 由 effectiveRoomBilling 回退到偏好设置里的全局默认。
const tenantFollowGlobal = -1

// normalizeWaterMode 规范化计费方式，空值与未知值一律按"按吨"处理。
func normalizeWaterMode(mode string) string {
	if strings.TrimSpace(mode) == waterModeMonthly {
		return waterModeMonthly
	}
	return waterModeMeter
}

// waterModeLabel 计费方式的中文名（导出 CSV 用）。
func waterModeLabel(mode string) string {
	if normalizeWaterMode(mode) == waterModeMonthly {
		return "包月"
	}
	return "按吨"
}

// parseWaterMode 解析用户输入的计费方式（"包月"/"monthly" 均可），
// 未识别或未填写返回空串，由调用方决定缺省口径。
func parseWaterMode(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case waterModeMonthly, "包月":
		return waterModeMonthly
	case waterModeMeter, "按吨":
		return waterModeMeter
	default:
		return ""
	}
}

// Room 房源：一个可出租的房间，位置由小区/楼栋/单元/楼层/房号构成，
// (小区, 房号) 唯一——不同小区允许相同房号。UserID 是「录入人」戳：
// 数据全员共享后它只作溯源，不再隔离数据。
// Default* 为开票默认值；ElecPrice/GasPrice 为按房间覆盖的单价，
// 0 表示使用全局默认价；Initial* 为首次开账前的抄表底数。
//
// Deprecated: WaterMode/WaterPrice/WaterMonthlyFee/PayCycle/PayDay/RemindDays
// 自 v0.2.3 起搬到租户（Tenant）上——每个租户的计费方式与缴费周期可能不同。
// 这里保留列只为兼容老库与 0.2.1/0.2.2 的迁移，业务逻辑一律不再读取；
// 迁移 rental_tenant_billing_backfill 已把它们的值复制到在租租户并清零。
type Room struct {
	ID                   uint      `gorm:"primarykey" json:"id"`
	UserID               uint      `gorm:"index" json:"-"`
	LedgerID             uint      `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账，0=公共台账
	Community            string    `json:"community"`                                               // 小区（单小区可留空）
	RoomNo               string    `gorm:"not null" json:"room_no"`
	Building             string    `json:"building"`
	Unit                 string    `json:"unit"`
	Floor                string    `json:"floor"`
	Tags                 []string  `gorm:"serializer:json" json:"tags"` // 标签（如 物业代管/临街/精装），自定义分组用
	ApplyToken           string    `gorm:"index;size:64" json:"-"`      // 租客登记链接 token（公开页 /apply/<token>），json:- 防泄漏
	DefaultRent          float64   `gorm:"default:0" json:"default_rent"`
	DefaultSanitationFee float64   `gorm:"default:0" json:"default_sanitation_fee"`
	DefaultManagementFee float64   `gorm:"default:0" json:"default_management_fee"`
	WaterMode            string    `gorm:"default:''" json:"-"` // Deprecated: 见类型注释
	WaterPrice           float64   `gorm:"default:0" json:"-"`  // Deprecated: 见类型注释
	WaterMonthlyFee      float64   `gorm:"default:0" json:"-"`  // Deprecated: 见类型注释
	ElecPrice            float64   `gorm:"default:0" json:"elec_price"`
	GasPrice             float64   `gorm:"default:0" json:"gas_price"`
	InitialWater         float64   `gorm:"default:0" json:"initial_water"`
	InitialElec          float64   `gorm:"default:0" json:"initial_elec"`
	InitialGas           float64   `gorm:"default:0" json:"initial_gas"`
	PayCycle             string    `gorm:"default:''" json:"-"` // Deprecated: 见类型注释
	PayDay               int       `gorm:"default:0" json:"-"`  // Deprecated: 见类型注释
	RemindDays           int       `gorm:"default:-1" json:"-"` // Deprecated: 见类型注释
	Notes                string    `json:"notes"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Tenant 租户。宿舍场景同一房间可登记多名在租租户；
// 退租通过 MoveOutDate + Active=false 表达，历史账单不受影响。
//
// 计费与缴费设置（v0.2.3 起从房源搬到这里）：每个租户的水费计费方式、
// 水费金额、缴费周期、缴费日与提前提醒天数都可以不一样；未设置时回退到
// 系统级全局默认（系统配置 → 租房设置）。房间账单按该房在租租户的设置生成，
// 同房多名在租租户时逐项取"第一个显式设置"的租户（见 effectiveRoomBilling）。
//
// ExcludedFees 是该租户不参与计费的收费项目 key（JSON 数组，如
// `["sanitation","gas"]`；空 = 全部项目参与）。前端表现为"收费项目勾选"：
// 勾选 = 参与，未勾选的 key 存进这里。存排除而非勾选，是为了让之后新增的
// 自定义收费项目默认参与全部租户计费，不用逐租户补勾。
type Tenant struct {
	ID              uint     `gorm:"primarykey" json:"id"`
	UserID          uint     `gorm:"index" json:"-"`                                          // 录入人戳，仅溯源
	LedgerID        uint     `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID          uint     `gorm:"index" json:"room_id"`
	Name            string   `gorm:"not null" json:"name"`
	Phone           string   `json:"phone"`
	IDCard          string   `json:"id_card"`                     // 身份证号，选填
	Tags            []string `gorm:"serializer:json" json:"tags"` // 标签（如 优质/中介/长租），自定义分组用
	MoveInDate      string   `json:"move_in_date"`
	LeaseEndDate    string   `json:"lease_end_date"` // 租约到期时间 YYYY-MM-DD（空=未约定）
	MoveOutDate     string   `json:"move_out_date"`
	Deposit         float64  `gorm:"default:0" json:"deposit"` // 押金（元），退租结算时参考
	WaterMode       string   `gorm:"default:''" json:"water_mode"`
	WaterPrice      float64  `gorm:"default:0" json:"water_price"`       // 按吨单价 元/吨，0=跟随全局
	WaterMonthlyFee float64  `gorm:"default:0" json:"water_monthly_fee"` // 包月金额 元/月，0=跟随全局
	PayCycle        string   `gorm:"default:''" json:"pay_cycle"`        // ""跟随全局；monthly 月付；quarterly 季付
	// PayDay/RemindDays 不用 gorm default 标签：0 是合法取值（不提醒 / 当天提醒），
	// 打了 default 标签会让显式的 0 被默认值顶掉。-1 = 跟随全局默认。
	PayDay       int       `json:"pay_day"`
	RemindDays   int       `json:"remind_days"`
	ExcludedFees []string  `gorm:"serializer:json" json:"excluded_fees"` // 不参与计费的项目 key（空=全部参与）
	Active       bool      `gorm:"default:true" json:"active"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Bill 月度账单（核心实体）。租户名、单价在开票时快照到行内，
// 历史账单不随房源/租户后续修改而变化；(room, period) 唯一。
// UserID 是「录入人」戳：数据全员共享后只作溯源。
//
// 水费按开票时的 WaterMode 快照计费：meter（按吨）时 WaterPrice 是元/吨单价、
// 水费 = 用量 × 单价；monthly（包月）时 WaterPrice 是包月金额（元/月）、
// 水费 = WaterPrice × PayCycle 覆盖月数，抄表读数仅作记录不参与计算。
// PayCycle 是开票时快照的缴费周期：月付账单覆盖 1 个月，季付账单覆盖 3 个月
// （租金/卫生费/管理费/包月水费在开票时按月数预填）。
// ExcludedFees 是开票时快照的"不参与计费项目"（取该房在租租户的生效设置，
// 见 effectiveRoomExcludedFees）：recalc 对这些项目一律清零，账单不会因
// 租户后续改动勾选而变化。
type Bill struct {
	ID            uint     `gorm:"primarykey" json:"id"`
	UserID        uint     `gorm:"index" json:"-"`
	LedgerID      uint     `gorm:"column:org_id;index;not null;default:0" json:"ledger_id"` // 归属台账
	RoomID        uint     `gorm:"index" json:"room_id"`
	Period        string   `gorm:"not null;index" json:"period"` // YYYY-MM
	RoomNo        string   `gorm:"not null" json:"room_no"`
	TenantName    string   `json:"tenant_name"`
	Rent          float64  `json:"rent"`
	WaterLast     float64  `json:"water_last"`
	WaterNow      float64  `json:"water_now"`
	ElecLast      float64  `json:"elec_last"`
	ElecNow       float64  `json:"elec_now"`
	GasLast       float64  `json:"gas_last"`
	GasNow        float64  `json:"gas_now"`
	PayCycle      string   `gorm:"default:monthly" json:"pay_cycle"`     // 开票快照：monthly 月付 / quarterly 季付
	WaterMode     string   `gorm:"default:meter" json:"water_mode"`      // 开票快照：meter 按吨 / monthly 包月
	ExcludedFees  []string `gorm:"serializer:json" json:"excluded_fees"` // 开票快照：不参与计费的项目 key
	WaterPrice    float64  `json:"water_price"`                          // 按吨=元/吨，包月=元/月
	ElecPrice     float64  `json:"elec_price"`
	GasPrice      float64  `json:"gas_price"`
	WaterFee      float64  `json:"water_fee"`
	ElecFee       float64  `json:"elec_fee"`
	GasFee        float64  `json:"gas_fee"`
	SanitationFee float64  `json:"sanitation_fee"`
	ManagementFee float64  `json:"management_fee"`
	// DepositFee 押金（元）：**只在租户的首张账单**上收一次，与租金一起结清
	// （2026-10-11 粉丝需求：新租户入住第一次，押金和房租要在账单里一起收）。
	// 金额取开票时该房租户的 Tenant.Deposit，快照进账单——之后改租户押金不影响
	// 已开出的账单。已有历史账单不会被补收（判定见 isFirstBillForRoom）。
	DepositFee  float64    `json:"deposit_fee"`
	ExtraAmount float64    `json:"extra_amount"` // 自定义收费项目（宽带费等）金额合计
	TotalAmount float64    `json:"total_amount"`
	PaidAmount  float64    `gorm:"default:0" json:"paid_amount"`
	Status      string     `gorm:"default:unpaid" json:"status"` // paid / partial / unpaid
	ReceiptNo   string     `json:"receipt_no"`
	Remark      string     `json:"remark"`
	PaidAt      *time.Time `json:"paid_at"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

func init() {
	database.RegisterModels(&Room{}, &Tenant{}, &Bill{})

	// 首次启动的站点标题种子：让实例开箱显示"租房管理"而非框架默认标题。
	// 只在 system_configs 中没有 site_title 记录时写入，不覆盖用户已改的标题。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.0.1",
		Name:    "rental_seed_site_title",
		Upgrade: func(db *gorm.DB) error {
			var count int64
			if err := db.Model(&database.SystemConfig{}).
				Where("`key` = ?", "site_title").Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			return db.Create(&database.SystemConfig{Key: "site_title", Value: "租房管理", Public: true}).Error
		},
	})

	// 0.4.0：清掉英文站名残留。站名的框架默认值是内部应用名（version.AppName，
	// 即 "rental"）。0.0.1 的种子只在 system_configs 没有 site_title 记录时才写入，
	// 而用未打版本号的开发包（version.Version 为 "dev"）初始化过数据库时，升级阶梯
	// 因版本比较被整体跳过、框架默认配置先落库，种子随后看到已有记录直接返回——
	// 站名的英文就这样固化下来，且这类库的 upgrade_records 是空的，之后换正式版本包
	// 也不会自愈。这里只在站名仍是框架默认值（内部应用名 / smallgo / 空）时改写为
	// 「租房管理」；管理员自己改过的标题一律保留。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.4.0",
		Name:    "rental_fix_default_site_title",
		Upgrade: func(db *gorm.DB) error {
			var cfg database.SystemConfig
			err := db.Where("user_id = ? AND `key` = ?", 0, "site_title").First(&cfg).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			switch cfg.Value {
			case version.AppName, "smallgo", "":
				return db.Model(&database.SystemConfig{}).
					Where("id = ?", cfg.ID).
					Update("value", "租房管理").Error
			}
			return nil
		},
	})

	// 0.5.4：站点描述与匿名统计口径（2026-10-11 用户要求）。
	//   1) 站点描述的框架默认值原本是「现代化的全栈应用框架」，凡是用旧默认值
	//      初始化过的库都已把它固化进 system_configs，改 registry 默认值救不了
	//      存量安装；这里只在值仍是那句框架文案时改写为租房管理口径，管理员
	//      自己改过的一律保留。
	//   2) stats_enabled 已从系统配置里摘掉（统计默认开启、应用内不可关闭），
	//      顺手删掉历史遗留的配置行，避免它继续躺在库里让人误以为还能关。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.5.4",
		Name:    "rental_site_description_and_stats_default",
		Upgrade: func(db *gorm.DB) error {
			const legacyDesc = "现代化的全栈应用框架"
			const newDesc = "面向房东、公寓与宿舍管理者的租房管理：房源与租户登记、抄表出账、收款与欠缴跟踪，数据按台账隔离"
			var cfg database.SystemConfig
			err := db.Where("user_id = ? AND `key` = ?", 0, "site_description").First(&cfg).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				if err := db.Create(&database.SystemConfig{Key: "site_description", Value: newDesc, Public: true}).Error; err != nil {
					return err
				}
			case err != nil:
				return err
			case cfg.Value == legacyDesc:
				if err := db.Model(&database.SystemConfig{}).Where("id = ?", cfg.ID).Update("value", newDesc).Error; err != nil {
					return err
				}
			}
			return db.Where("user_id = ? AND `key` = ?", 0, "stats_enabled").
				Delete(&database.SystemConfig{}).Error
		},
	})

	// 0.0.2：房源唯一索引从 (user_id, room_no) 重建为 (user_id, community, room_no)，
	// 支持不同小区使用相同房号。旧数据 community 为空串，重建后约束语义不变。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.0.2",
		Name:    "rental_room_location_unique_index",
		Upgrade: func(db *gorm.DB) error {
			if err := db.Exec("DROP INDEX IF EXISTS idx_rental_room_user_no").Error; err != nil {
				return err
			}
			return db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_room_user_no ON rooms(user_id, community, room_no)").Error
		},
	})

	// 水费计费方式列补齐（随 v0.2.1 发布）。AutoMigrate 给老库加列时旧行
	// 可能读出 NULL，这里兜底：账单统一成"按吨"（升级前的原口径），房源统一
	// 成空串（跟随全局默认），保证升级后读得出来、算得对。
	//
	// Version 跟随系统版本：填本次发版时 VERSION 文件的版本号（本次 0.2.1），
	// 以后新增迁移照此填写，不用 0.0.3 这类私有序号。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.2.1",
		Name:    "rental_water_mode_backfill",
		Upgrade: func(db *gorm.DB) error {
			if err := db.Exec("UPDATE bills SET water_mode = 'meter' WHERE water_mode IS NULL OR water_mode = ''").Error; err != nil {
				return err
			}
			return db.Exec("UPDATE rooms SET water_mode = '' WHERE water_mode IS NULL").Error
		},
	})

	// 0.2.2：缴费周期与提醒字段补齐（随月付/季付功能发布）。AutoMigrate 加列后
	// 旧行可能读出 NULL：周期统一成"月付"（升级前的原口径），缴费日 0（不提醒），
	// 提前提醒天数 -1（跟随全局默认），保证升级后读得出来、算得对。
	//
	// Version 跟随系统版本：填本次发版时 VERSION 文件的版本号（本次 0.2.2）。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.2.2",
		Name:    "rental_pay_cycle_backfill",
		Upgrade: func(db *gorm.DB) error {
			if err := db.Exec("UPDATE rooms SET pay_cycle = 'monthly' WHERE pay_cycle IS NULL OR pay_cycle = ''").Error; err != nil {
				return err
			}
			if err := db.Exec("UPDATE bills SET pay_cycle = 'monthly' WHERE pay_cycle IS NULL OR pay_cycle = ''").Error; err != nil {
				return err
			}
			if err := db.Exec("UPDATE rooms SET pay_day = 0 WHERE pay_day IS NULL").Error; err != nil {
				return err
			}
			return db.Exec("UPDATE rooms SET remind_days = -1 WHERE remind_days IS NULL").Error
		},
	})

	// 0.2.3：水费计费方式与缴费周期/缴费日/提醒天数从房源搬到租户。
	// 0.2.3 之前租户上没有这些列，所以按房源设置无条件回填到该房全部租户，
	// 房源没设置的项回填成"跟随全局"的哨兵值（水费方式空串、周期空串、
	// 缴费日/提醒天数 -1）；缴费周期只搬"季付"，月付是 0.2.2 的回填默认值、
	// 搬过去会让租户失去"跟随全局"语义。最后把房源级字段清零——新口径下
	// 它们不再参与计费，留着只会误导。
	//
	// Version 跟随系统版本：填本次发版时 VERSION 文件的版本号（本次 0.2.3）。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.2.3",
		Name:    "rental_tenant_billing_backfill",
		Upgrade: func(db *gorm.DB) error {
			var rooms []Room
			if err := db.Find(&rooms).Error; err != nil {
				return err
			}
			for i := range rooms {
				room := &rooms[i]
				updates := map[string]interface{}{
					"water_mode":        parseWaterMode(room.WaterMode),
					"water_price":       room.WaterPrice,
					"water_monthly_fee": room.WaterMonthlyFee,
					"pay_cycle":         "",
					"pay_day":           tenantFollowGlobal,
					"remind_days":       tenantFollowGlobal,
				}
				if parsePayCycle(room.PayCycle) == payCycleQuarterly {
					updates["pay_cycle"] = payCycleQuarterly
				}
				if room.PayDay > 0 {
					updates["pay_day"] = room.PayDay
				}
				if room.RemindDays >= 0 {
					updates["remind_days"] = room.RemindDays
				}
				if err := db.Model(&Tenant{}).Where("room_id = ?", room.ID).
					Updates(updates).Error; err != nil {
					return err
				}
			}
			return db.Exec("UPDATE rooms SET water_mode = '', water_price = 0, water_monthly_fee = 0, pay_cycle = '', pay_day = 0, remind_days = -1").Error
		},
	})

	// 0.2.6：租户收费项目勾选与身份证号（随本次功能发布）。AutoMigrate 给老库
	// 加列后旧行可能读出 NULL：排除项目统一成空串（= 全部项目参与，升级前的
	// 原口径），身份证号统一成空串（本来就没填过），保证升级后读得出来。
	//
	// Version 跟随系统版本：填本次发版时 VERSION 文件的版本号（本次 0.2.6）。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.2.6",
		Name:    "rental_tenant_fee_options_backfill",
		Upgrade: func(db *gorm.DB) error {
			if err := db.Exec("UPDATE tenants SET excluded_fees = '' WHERE excluded_fees IS NULL").Error; err != nil {
				return err
			}
			if err := db.Exec("UPDATE tenants SET id_card = '' WHERE id_card IS NULL").Error; err != nil {
				return err
			}
			return db.Exec("UPDATE bills SET excluded_fees = '' WHERE excluded_fees IS NULL").Error
		},
	})

	// 0.3.3：业务数据全员共享 + 租房设置升系统级（随共享数据与员工权限发布）。
	// 三步：
	//  1. 租房设置（rental_*）从用户级偏好升为系统级：把登记最早用户（通常是
	//     管理员）的值写入系统级（user_id=0），再删除全部用户级行。
	//  2. 共享化去重：不同录入人可能建过同小区同房号的房源、同房同月的账单
	//     与抄表、同 key 的收费项目——唯一键不再含 user_id，先并重复行，
	//     否则新唯一索引建不起来。
	//  3. 重建唯一索引（不再含 user_id）。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.3.3",
		Name:    "rental_shared_data",
		Upgrade: func(db *gorm.DB) error {
			if err := migrateConfigsToSystem(db); err != nil {
				return err
			}
			if err := dedupeSharedData(db); err != nil {
				return err
			}
			for _, idx := range []string{
				"idx_rental_room_user_no", "idx_rental_bill_unique",
				"idx_meter_record_unique", "idx_rental_fee_item_user_key",
			} {
				if err := db.Exec("DROP INDEX IF EXISTS " + idx).Error; err != nil {
					return err
				}
			}
			for _, stmt := range []string{
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_room_location ON rooms(community, room_no)",
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_bill_room_period ON bills(room_id, period)",
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_meter_record_room_period ON meter_records(room_id, period)",
				"CREATE UNIQUE INDEX IF NOT EXISTS idx_rental_fee_item_key ON fee_items(`key`)",
			} {
				if err := db.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	})

	// 台账数据隔离与归属（功能随 v0.6.0 发布）。
	//
	// 老库升级后行为必须与升级前一致：全部业务数据归 0 号「公共台账」台账，
	// 全部现有用户加入该台账，原全局权限平移到成员关系上。想隔离的新用户
	// 由管理员另建台账并单独加入。详见 ledger.go 与 migrate_ledger.go。
	//
	// ⚠️ 迁移版本用 0.5.1 而不是 0.6.0：升级阶梯只跑「迁移版本 ≤ 应用 VERSION」
	// 的迁移。本功能尚未发版（dev 包 VERSION=0.5.1），若迁移标 0.6.0 会被跳过，
	// 老库升级会丢权限。发版时：已装 0.5.1 的库迁移记录为 0.5.1 ≤ 新版本号
	// 不重复跑，全新库正常跑。护栏见 version_gate_test.go。
	//
	// 0.5.2 起这条迁移只在**老库**上有效（见 legacyTenantDataExists）：全新
	// 安装没有账号也没有历史权限，跑它只会留下一张空的公共台账。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.5.1",
		Name:    "rental_ledger_data_isolation",
		// DDL（ALTER TABLE / 索引重建）在 SQLite 上不能安全地包在事务里，
		// 与 0.3.3 同策略：整段迁移以 NoTx 执行，靠 IF NOT EXISTS 与
		// 列存在性判断保证可重入。
		NoTx: true,
		Upgrade: func(db *gorm.DB) error {
			if err := migrateOrgEraSchema(db); err != nil {
				return err
			}
			return migrateAccessToLedgers(db)
		},
	})

	// 0.5.2：台账模型换成「个人台账 + 共同台账」。
	//
	// 只做加法：老台账补 kind/owner、成员行补 role/status、给每个存量账号建
	// 个人台账；**业务数据一行不动**，老用户升级后仍在自己原来的台账里看到
	// 原来的数据。DDL（DROP INDEX）同样不包事务，理由同 0.5.1。
	database.Upgrades = append(database.Upgrades, database.Upgrade{
		Version: "0.5.2",
		Name:    "rental_personal_ledgers",
		NoTx:    true,
		Upgrade: migratePersonalLedgers,
	})

	registerConfigs()

	// 删号守卫（0.5.2 新模型）：数据归属台账，所以「有数据的账号」不能一删了之。
	//   - 自己是共同台账的管理者：台账会变成无主（本版没有转让功能）→ 拒绝；
	//   - 个人台账里有业务数据（房源/租户/合同/抄表/账单/收款）→ 拒绝，
	//     免得把台账与数据一起变成孤儿。
	// 其它情况放行：清掉成员关系与空台账即可，业务数据留在它所属的台账里。
	database.RegisterUserDeleteGuard(func(db *gorm.DB, userID uint) (bool, error) {
		return userOwnsLedgerWithData(db, userID)
	})
	database.RegisterUserDeleteCleanup(func(db *gorm.DB, userID uint) error {
		if err := db.Where("user_id = ?", userID).Delete(&LedgerMember{}).Error; err != nil {
			return err
		}
		// 空的无主台账一并清掉：个人台账只属于本人；共同台账能走到这里说明
		// 守卫已经确认它没有数据（有数据或自己是管理者的都被拦下了）。
		q := db.Where("owner_user_id = ?", userID)
		if ids := businessDataLedgerIDs(db); len(ids) > 0 {
			q = q.Where("id NOT IN ?", ids)
		}
		if err := q.Delete(&Ledger{}).Error; err != nil {
			return err
		}
		// 0.5.x 时代的全局权限表：只有老库有，且 0.6 起不再读，顺手清掉。
		if tableExists(db, "rental_accesses") {
			if err := db.Where("user_id = ?", userID).Delete(&RentalAccess{}).Error; err != nil {
				return err
			}
		}
		return nil
	})

	apps.Register(apps.App{
		Name:        "rental",
		DisplayName: "租房管理",
		Icon:        "house",
		RoutePrefix: "/api/rental",
		NavPosition: 10,
		SetupAuth:   setupRoutesWithApprovalGate,
		// 公开路由：租客自助登记（/api/rental/apply/<token>），走 publicGroup 限流
		Setup: setupApplyPublicRoutes,
		// 管理后台只保留「账号」相关能力：注册审核（待审核用户通过/驳回）。
		// 台账由用户自己建、自己管成员；超管在业务数据上没有特权（0.5.2 起）。
		SetupAdmin: func(admin *gin.RouterGroup, db *gorm.DB) {
			setupLedgerAdminRoutes(admin, db)
		},
		// Migrate 在框架迁移阶段调用一次：把 db 注入给缴费提醒的调度任务
		// 与通知渠道层（QQ 网关等）。
		//
		// 台账不再需要「启动时保证某行存在」：个人台账在注册钩子里建
		// （ensurePersonalLedger），共同台账由用户自己建；历史库由 0.5.2
		// 升级补齐。这里只做依赖注入。
		Migrate: func(db *gorm.DB) error {
			appDB = db
			notify.SetDB(db)
			return nil
		},
	})
}

// setupRoutes 挂载全部业务路由（框架 authGroup，登录后可用）。
//
// v0.6.0 起权限与数据范围都按**台账**判定：
//   - resolveLedger 先把本次请求的台账范围（?ledger_id= 或用户全部所属台账）
//     解析进 context；
//   - requireModule 做模块准入（跨台账并集口径，防误挡只读员工）；
//   - 各 handler 用 ledgerScope(c) 把查询收窄到可见台账，写入落到目标台账。
//
// 默认不传 ledger_id 时：查询范围 = 我的全部正式成员台账，写入落个人台账。
func setupRoutes(api *gin.RouterGroup, db *gorm.DB) {
	// 台账范围解析必须挂在所有业务路由之前。
	scoped := api.Group("", resolveLedger(db))

	setupLedgerAuthRoutes(scoped, db)
	setupRoomRoutes(scoped, db)
	setupTenantRoutes(scoped, db)
	setupApplicationRoutes(scoped, db)
	setupContractRoutes(scoped, db)
	setupAttachmentRoutes(scoped, db)
	setupMeterRoutes(scoped, db)
	setupBillRoutes(scoped, db)
	setupDataRoutes(scoped, db)
	setupStatsRoutes(scoped, db)
	setupFeeRoutes(scoped, db)
	// 共享计费默认值：设置升为系统级后，员工在偏好设置里看不到租房设置组，
	// 开票/抄表弹窗的"跟随全局默认"改走这个接口。
	// 共享计费默认值：开票/收款弹窗的"跟随全局默认"走这个接口，归账单模块。
	scoped.GET("/rental/billing-defaults", requireModule(db, ModuleBills, PermView), handleBillingDefaults(db))
	// 通知渠道是个人提醒配置，任一模块可看即允许绑自己的渠道。
	notify.SetupRoutes(scoped, db, requireAnyView(db))
}

// registerConfigs 注册系统级共享设置：默认单价与单据抬头。数据共享后这些
// 口径全员一套，由管理员在系统配置 → 租房设置里维护（v0.3.3 起从用户级
// 偏好升级而来，迁移把原用户级值搬进系统级）。
func registerConfigs() {
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_price", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "5.00", Group: "租房设置", Label: "水费单价（元/吨）",
		Description: "按吨计费时的默认水价，可在租户或账单中覆盖",
	})
	// AI 证件识别（租客自助登记用）：OpenAI 兼容视觉接口，Key 留空 = 关闭识别，
	// 登记页自动退化为手填。默认指向智谱开放平台（glm-4v-flash 免费档）。
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_ai_base_url", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "https://open.bigmodel.cn/api/paas/v4", Group: "租房设置", Label: "AI 识别接口地址",
		Description: "OpenAI 兼容的 chat/completions 基址（含版本号），用于身份证照片识别",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_ai_api_key", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "AI 识别 API Key",
		Description: "留空则关闭识别，租客登记页自动退化为手动填写",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_ai_model", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "glm-4v-flash", Group: "租房设置", Label: "AI 识别模型",
		Description: "支持图片输入的视觉模型，如 glm-4v-flash",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_mode", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeSelect,
		Default: waterModeMeter, Options: []string{waterModeMeter, waterModeMonthly},
		Group: "租房设置", Label: "水费计费方式",
		Description: "按吨按用量计费，包月每月固定金额；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_monthly_fee", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "0.00", Group: "租房设置", Label: "水费包月金额（元/月）",
		Description: "计费方式为包月时的默认金额，租户可单独设置（如 40）",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_pay_cycle", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeSelect,
		Default: payCycleMonthly, Options: []string{payCycleMonthly, payCycleQuarterly},
		Group: "租房设置", Label: "缴费周期",
		Description: "月付每月一张账单、季付每 3 个月一张；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_pay_day", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeInt,
		Default: "0", Group: "租房设置", Label: "缴费日（每月几号）",
		Description: "默认每期收租的日期（1-28 号，0=不提醒）；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_elec_price", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "1.20", Group: "租房设置", Label: "电费单价（元/度）",
		Description: "生成账单时的默认电价，可在房间或账单中覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_gas_price", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "3.50", Group: "租房设置", Label: "燃气单价（元/方）",
		Description: "生成账单时的默认气价，可在房间或账单中覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_remind_days", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeInt,
		Default: "3", Group: "租房设置", Label: "缴费提前提醒天数",
		Description: "缴费日前 N 天开始在总览与通知中提醒（0=当天提醒），每个租户可单独覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_property_name", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据抬头（出租方名称）",
		Description: "打印收费单据时显示的出租方/物业名称",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_contact", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据联系电话",
		Description: "打印收费单据时显示的联系电话",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_receipt_note", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据底部备注",
		Description: "打印收费单据时显示在底部的提示文字",
	})

	// v0.6.0：注册审核开关。开户给别人用时打开，新注册账号先进「待审核」，
	// 管理员在用户管理里审核通过后才可用（首个管理员不受影响；个人台账在注册时就建好）。
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "register_need_approval", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeBool,
		Default: "true", Group: "访问控制", Label: "注册需管理员审核",
		Description: "开启后新注册账号为「待审核」，可登录但看不到任何业务数据，需管理员审核通过后才能使用",
	})

	// 注册手机号（2026-10-11 粉丝需求）：管理员在用户列表里要能看出谁是谁。
	// 手机号只作联系/认人用途——不做唯一校验、不参与登录、不作为身份凭据。
	// Public: true —— 注册页（未登录）要读这两个开关来决定显不显示输入框、
	// 以及要不要标成必填；只暴露开关本身，不涉及任何用户数据。
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "register_phone_enabled", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeBool,
		Default: "true", Public: true, Group: "访问控制", Label: "注册时收集手机号",
		Description: "关闭后注册页不显示手机号输入框；手机号仅用于管理员识别账号归属，不做登录凭据",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "register_phone_required", Scope: sysconfig.ScopeSystem, Type: sysconfig.TypeBool,
		Default: "true", Public: true, Group: "访问控制", Label: "注册手机号必填",
		Description: "开启后不填手机号无法注册；仅在「注册时收集手机号」开启时生效",
	})
}
