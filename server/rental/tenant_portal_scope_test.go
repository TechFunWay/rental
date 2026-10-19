// tenant_portal_scope_test.go — 门户可见范围的越权回归（v0.6.10 修复）：
// 「姓名快照为空 + 入住日期没填」这个组合曾让同房新租客看到前任的账单。
package rental

import (
	"net/http"
	"testing"
)

func TestPortalBillsHidePreviousTenantWithoutMoveInDate(t *testing.T) {
	// 新租户没填入住日期（表单里是选填）
	f := newPortalFixture(t, "803", "新租客", "", "")
	ledger := f.env.personalLedger

	// 前任的账单：姓名快照为空（老库历史数据）
	legacyOthers := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-01", "")
	// 自己的账单（姓名快照命中）
	mine := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-04", "新租客")

	w := portalGet(f.env, f.tenant, "/api/rental/portal/bills?pageSize=50")
	if w.Code != http.StatusOK {
		t.Fatalf("读账单 = %d: %s", w.Code, w.Body.String())
	}
	ids := map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !ids[mine] {
		t.Fatal("自己的账单必须可见")
	}
	if ids[legacyOthers] {
		t.Fatal("没有入住日期时，前任的空快照账单不该可见（下界要落到档案建立月/最早本人账单）")
	}

	// 收款记录走同一份可见范围（收款挂在账单上）：前任那张账单的收款也不能露
	pay := Payment{UserID: f.env.adminID, LedgerID: ledger, BillID: legacyOthers, Amount: 1000}
	if err := f.env.db.Create(&pay).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	paid := Payment{UserID: f.env.adminID, LedgerID: ledger, BillID: mine, Amount: 1000}
	if err := f.env.db.Create(&paid).Error; err != nil {
		t.Fatalf("seed payment: %v", err)
	}
	w = portalGet(f.env, f.tenant, "/api/rental/portal/payments?pageSize=50")
	ids = map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if ids[pay.ID] {
		t.Error("前任账期的收款不该可见")
	}
	if !ids[paid.ID] {
		t.Error("自己账期的收款应可见")
	}
}

// TestPortalBillsFloorUsesFirstNamedBill：没填入住日期时，下界取「本房最早一张
// 姓名命中我的账单」——这样早于这条账单、但姓名快照为空的历史账单不会被误认为是
// 前任的（老库兼容），也不会把更早的前任账单放进来。
func TestPortalBillsFloorUsesFirstNamedBill(t *testing.T) {
	f := newPortalFixture(t, "804", "李四", "", "")
	ledger := f.env.personalLedger
	// 前任（更早的账期，空快照）
	oldOthers := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-01", "")
	// 自己最早的一张（姓名命中）
	firstMine := createBillWithTenant(t, f.env, f.roomID, ledger, "2026-03", "李四")

	w := portalGet(f.env, f.tenant, "/api/rental/portal/bills?pageSize=50")
	ids := map[uint]bool{}
	for _, it := range f.env.items(decodeBody(t, w)) {
		ids[uint(it.(map[string]interface{})["id"].(float64))] = true
	}
	if !ids[firstMine] {
		t.Fatal("姓名命中的账单必须可见")
	}
	if ids[oldOthers] {
		t.Fatal("早于最早本人账单的空快照账单不该可见")
	}
}
