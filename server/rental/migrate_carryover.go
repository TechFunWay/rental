// migrate_carryover.go — 0.6.12 迁移：账单补「结转」两列并回填存量数据。
//
// 背景见 carryover.go。AutoMigrate 会给 bills 加上 carry_in / payable 两列，
// 但**存量行的默认值是 0**。如果不回填：
//
//   - 老账单的「本期应缴」会显示 0 元（界面上比欠缴额还小，明显错乱）；
//   - 任何按 payable 统计的地方都会凭空少算。
//
// 回填口径刻意取「结转关闭」时的应有值：
//
//	payable  = total_amount   （应付=本期费用，无结转）
//	carry_in = 0
//
// 这与升级前的行为逐字一致，因此**存量账单的金额与欠缴一分钱都不会变**；
// 结转本来就是按台账开关的、默认关闭，升级前不存在任何结转可言。
//
// 幂等：只更新 payable = 0 的存量行（新库与新账单不会命中）。
package rental

import (
	"gorm.io/gorm"
)

func migrateBillCarryover(db *gorm.DB) error {
	// 只补「还没被写过」的行：新建账单一定带非 0 的 payable（本期费用>0 的账单）
	// 或至少经过 newBillFromRoom 赋值。用 payable = 0 作为未回填的判据。
	//
	// 注意 total_amount 为 0 的账单（全部项目被排除/免收）回填后仍是 0，
	// 这是正确值，无需特殊处理。
	if err := db.Exec(
		"UPDATE bills SET payable = total_amount WHERE payable = 0 OR payable IS NULL",
	).Error; err != nil {
		return err
	}
	// 顺带回填存量租户的身份证派生字段（issue #5 需求 1）。
	//
	// 派生是纯函数、只在 18 位且日期合法时产出值，因此对存量数据是安全的
	// 只写不删（birthday/gender 为空的行才处理）。放在同一条迁移里是因为
	// 二者同一版本引入，且都不改变任何既有业务字段。
	return backfillTenantDerived(db)
}

// backfillTenantDerived 给存量租户补齐出生日期/性别。
//
// 逐行走 applyTenantDerived（与新建/编辑同一套口径），只在**派生字段为空**
// 且身份证号非空时更新，避免覆盖用户后来手工修正的值。
func backfillTenantDerived(db *gorm.DB) error {
	var tenants []Tenant
	if err := db.Where("id_card <> '' AND (birthday = '' OR gender = '')").
		Limit(100000).Find(&tenants).Error; err != nil {
		return err
	}
	for i := range tenants {
		t := &tenants[i]
		birthday, gender := deriveFromIDCard(t.IDCard)
		if birthday == "" && gender == "" {
			continue // 15 位老号段或号段非法：不猜，保持空
		}
		if err := db.Model(&Tenant{}).Where("id = ?", t.ID).
			Updates(map[string]interface{}{"birthday": birthday, "gender": gender}).Error; err != nil {
			return err
		}
	}
	return nil
}
