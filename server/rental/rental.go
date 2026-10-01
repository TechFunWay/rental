// Package rental — 租房管理系统业务模块。
//
// 提供房源、租户、月度抄表账单（水电气费用自动计算）、收款与欠缴跟踪、
// CSV 模板导入导出与收费单据数据。业务数据全部按登录用户（user_id）隔离。
package rental

import (
	"strings"
	"time"

	"smallgo/server/apps"
	"smallgo/server/database"
	"smallgo/server/notify"
	"smallgo/server/sysconfig"

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
// (用户, 小区, 房号) 唯一——不同小区允许相同房号。
// Default* 为开票默认值；ElecPrice/GasPrice 为按房间覆盖的单价，
// 0 表示使用用户级全局默认价；Initial* 为首次开账前的抄表底数。
//
// Deprecated: WaterMode/WaterPrice/WaterMonthlyFee/PayCycle/PayDay/RemindDays
// 自 v0.2.3 起搬到租户（Tenant）上——每个租户的计费方式与缴费周期可能不同。
// 这里保留列只为兼容老库与 0.2.1/0.2.2 的迁移，业务逻辑一律不再读取；
// 迁移 rental_tenant_billing_backfill 已把它们的值复制到在租租户并清零。
type Room struct {
	ID                   uint      `gorm:"primarykey" json:"id"`
	UserID               uint      `gorm:"index;uniqueIndex:idx_rental_room_user_no" json:"-"`
	Community            string    `gorm:"uniqueIndex:idx_rental_room_user_no" json:"community"` // 小区（单小区可留空）
	RoomNo               string    `gorm:"not null;uniqueIndex:idx_rental_room_user_no" json:"room_no"`
	Building             string    `json:"building"`
	Unit                 string    `json:"unit"`
	Floor                string    `json:"floor"`
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
// 用户级全局默认（偏好设置 → 租房设置）。房间账单按该房在租租户的设置生成，
// 同房多名在租租户时逐项取"第一个显式设置"的租户（见 effectiveRoomBilling）。
//
// ExcludedFees 是该租户不参与计费的收费项目 key（JSON 数组，如
// `["sanitation","gas"]`；空 = 全部项目参与）。前端表现为"收费项目勾选"：
// 勾选 = 参与，未勾选的 key 存进这里。存排除而非勾选，是为了让之后新增的
// 自定义收费项目默认参与全部租户计费，不用逐租户补勾。
type Tenant struct {
	ID              uint    `gorm:"primarykey" json:"id"`
	UserID          uint    `gorm:"index" json:"-"`
	RoomID          uint    `gorm:"index" json:"room_id"`
	Name            string  `gorm:"not null" json:"name"`
	Phone           string  `json:"phone"`
	IDCard          string  `json:"id_card"` // 身份证号，选填
	MoveInDate      string  `json:"move_in_date"`
	LeaseEndDate    string  `json:"lease_end_date"` // 租约到期时间 YYYY-MM-DD（空=未约定）
	MoveOutDate     string  `json:"move_out_date"`
	Deposit         float64 `gorm:"default:0" json:"deposit"` // 押金（元），退租结算时参考
	WaterMode       string  `gorm:"default:''" json:"water_mode"`
	WaterPrice      float64 `gorm:"default:0" json:"water_price"`       // 按吨单价 元/吨，0=跟随全局
	WaterMonthlyFee float64 `gorm:"default:0" json:"water_monthly_fee"` // 包月金额 元/月，0=跟随全局
	PayCycle        string  `gorm:"default:''" json:"pay_cycle"`        // ""跟随全局；monthly 月付；quarterly 季付
	// PayDay/RemindDays 不用 gorm default 标签：0 是合法取值（不提醒 / 当天提醒），
	// 打了 default 标签会让显式的 0 被默认值顶掉。-1 = 跟随全局默认。
	PayDay     int       `json:"pay_day"`
	RemindDays int       `json:"remind_days"`
	ExcludedFees []string  `gorm:"serializer:json" json:"excluded_fees"` // 不参与计费的项目 key（空=全部参与）
	Active       bool      `gorm:"default:true" json:"active"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Bill 月度账单（核心实体）。租户名、单价在开票时快照到行内，
// 历史账单不随房源/租户后续修改而变化；(user, room, period) 唯一。
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
	ID            uint       `gorm:"primarykey" json:"id"`
	UserID        uint       `gorm:"index;uniqueIndex:idx_rental_bill_unique" json:"-"`
	RoomID        uint       `gorm:"index;uniqueIndex:idx_rental_bill_unique" json:"room_id"`
	Period        string     `gorm:"not null;index;uniqueIndex:idx_rental_bill_unique" json:"period"` // YYYY-MM
	RoomNo        string     `gorm:"not null" json:"room_no"`
	TenantName    string     `json:"tenant_name"`
	Rent          float64    `json:"rent"`
	WaterLast     float64    `json:"water_last"`
	WaterNow      float64    `json:"water_now"`
	ElecLast      float64    `json:"elec_last"`
	ElecNow       float64    `json:"elec_now"`
	GasLast       float64    `json:"gas_last"`
	GasNow        float64    `json:"gas_now"`
	PayCycle      string     `gorm:"default:monthly" json:"pay_cycle"` // 开票快照：monthly 月付 / quarterly 季付
	WaterMode     string     `gorm:"default:meter" json:"water_mode"`  // 开票快照：meter 按吨 / monthly 包月
	ExcludedFees  []string   `gorm:"serializer:json" json:"excluded_fees"` // 开票快照：不参与计费的项目 key
	WaterPrice    float64    `json:"water_price"`                      // 按吨=元/吨，包月=元/月
	ElecPrice     float64    `json:"elec_price"`
	GasPrice      float64    `json:"gas_price"`
	WaterFee      float64    `json:"water_fee"`
	ElecFee       float64    `json:"elec_fee"`
	GasFee        float64    `json:"gas_fee"`
	SanitationFee float64    `json:"sanitation_fee"`
	ManagementFee float64    `json:"management_fee"`
	ExtraAmount   float64    `json:"extra_amount"` // 自定义收费项目（宽带费等）金额合计
	TotalAmount   float64    `json:"total_amount"`
	PaidAmount    float64    `gorm:"default:0" json:"paid_amount"`
	Status        string     `gorm:"default:unpaid" json:"status"` // paid / partial / unpaid
	ReceiptNo     string     `json:"receipt_no"`
	Remark        string     `json:"remark"`
	PaidAt        *time.Time `json:"paid_at"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
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

	registerConfigs()

	// 名下还有房源/账单的用户不允许被框架删除，业务数据仍被引用。
	database.RegisterUserDeleteGuard(func(db *gorm.DB, userID uint) (bool, error) {
		var rooms, bills int64
		if err := db.Model(&Room{}).Where("user_id = ?", userID).Count(&rooms).Error; err != nil {
			return false, err
		}
		if err := db.Model(&Bill{}).Where("user_id = ?", userID).Count(&bills).Error; err != nil {
			return false, err
		}
		return rooms > 0 || bills > 0, nil
	})
	database.RegisterUserDeleteCleanup(func(db *gorm.DB, userID uint) error {
		// 删用户时连同其名下租户与合同文件一并清理：
		// 合同先按租户删磁盘文件，再统一清行（含无租户归属的孤儿行）。
		tenantIDs := make([]uint, 0)
		if err := db.Model(&Tenant{}).Where("user_id = ?", userID).Pluck("id", &tenantIDs).Error; err != nil {
			return err
		}
		for _, tenantID := range tenantIDs {
			deleteTenantContracts(db, tenantID)
		}
		if err := db.Where("user_id = ?", userID).Delete(&Tenant{}).Error; err != nil {
			return err
		}
		return db.Where("user_id = ?", userID).Delete(&Contract{}).Error
	})

	apps.Register(apps.App{
		Name:        "rental",
		DisplayName: "租房管理",
		Icon:        "house",
		RoutePrefix: "/api/rental",
		NavPosition: 10,
		SetupAuth:   setupRoutes,
		// Migrate 在框架迁移阶段调用一次：把 db 注入给缴费提醒的调度任务
		// 与通知渠道层（QQ 网关等）。
		Migrate: func(db *gorm.DB) error {
			appDB = db
			notify.SetDB(db)
			return nil
		},
	})
}

// setupRoutes 挂载全部业务路由，均在框架 authGroup 下（登录后可用），
// 且自带审计中间件；数据按登录用户隔离。
func setupRoutes(api *gin.RouterGroup, db *gorm.DB) {
	setupRoomRoutes(api, db)
	setupTenantRoutes(api, db)
	setupContractRoutes(api, db)
	setupMeterRoutes(api, db)
	setupBillRoutes(api, db)
	setupDataRoutes(api, db)
	setupStatsRoutes(api, db)
	setupFeeRoutes(api, db)
	notify.SetupRoutes(api, db)
}

// registerConfigs 注册用户级偏好：默认单价与单据抬头，偏好设置页自动渲染。
func registerConfigs() {
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_price", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "5.00", Group: "租房设置", Label: "水费单价（元/吨）",
		Description: "按吨计费时的默认水价，可在租户或账单中覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_mode", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeSelect,
		Default: waterModeMeter, Options: []string{waterModeMeter, waterModeMonthly},
		Group: "租房设置", Label: "水费计费方式",
		Description: "按吨按用量计费，包月每月固定金额；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_water_monthly_fee", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "0.00", Group: "租房设置", Label: "水费包月金额（元/月）",
		Description: "计费方式为包月时的默认金额，租户可单独设置（如 40）",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_pay_cycle", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeSelect,
		Default: payCycleMonthly, Options: []string{payCycleMonthly, payCycleQuarterly},
		Group: "租房设置", Label: "缴费周期",
		Description: "月付每月一张账单、季付每 3 个月一张；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_pay_day", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeInt,
		Default: "0", Group: "租房设置", Label: "缴费日（每月几号）",
		Description: "默认每期收租的日期（1-28 号，0=不提醒）；租户可单独设置",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_elec_price", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "1.20", Group: "租房设置", Label: "电费单价（元/度）",
		Description: "生成账单时的默认电价，可在房间或账单中覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_gas_price", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "3.50", Group: "租房设置", Label: "燃气单价（元/方）",
		Description: "生成账单时的默认气价，可在房间或账单中覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_remind_days", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeInt,
		Default: "3", Group: "租房设置", Label: "缴费提前提醒天数",
		Description: "缴费日前 N 天开始在总览与通知中提醒（0=当天提醒），每个租户可单独覆盖",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_property_name", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据抬头（出租方名称）",
		Description: "打印收费单据时显示的出租方/物业名称",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_contact", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据联系电话",
		Description: "打印收费单据时显示的联系电话",
	})
	sysconfig.RegisterConfig(sysconfig.ConfigDef{
		Key: "rental_receipt_note", Scope: sysconfig.ScopeUser, Type: sysconfig.TypeString,
		Default: "", Group: "租房设置", Label: "单据底部备注",
		Description: "打印收费单据时显示在底部的提示文字",
	})
}
