// pagination_test.go — 列表接口的 pageSize 上限行为，以及「抄表弹窗要拿全房间」
// 这个依赖的回归。
//
// 背景（2026-10-11 粉丝报的 bug）：抄表时费用显示成别的房间，删除房源重建后恢复。
// 根因有两条，都落在这里：
//  1. 抄表下拉的 option value 用 `账单 id ?? 房间 id`，两套 ID 空间重叠时串房
//     （前端已修，见 MeterReadingModal.vue 的 candidateValue）；
//  2. 前端请求 pageSize=200，但服务端 utils.NormalizePage 把它钳到 MaxPageSize=100，
//     房间/账单超过 100 条时列表被静默截断，「按 room_id 配对」就会缺项、错配。
//     前端改为跨页拉全（getAllRooms / getAllBills）。
//
// 本文件把 (2) 的服务端行为钉死：上限是 100 而不是 200。这样一旦有人再写
// pageSize: 200 去「一次拿全」，测试会提醒他这条路径本来就拿不全。
package rental

import (
	"fmt"
	"net/http"
	"testing"
)

// TestListPageSizeIsCappedAt100 服务端把 pageSize 上限钳在 100。
// 不是缺陷本身，而是「必须跨页拉全」这个结论的依据；前端 getAll* 依赖它。
func TestListPageSizeIsCappedAt100(t *testing.T) {
	env := setupEnv(t)
	// 造 120 间在租房（每间挂一个在租租户，否则 status=occupied 会过滤掉）。
	for i := 1; i <= 120; i++ {
		w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
			"room_no": fmt.Sprintf("P%03d", i), "default_rent": 1000,
		})
		if w.Code != http.StatusOK {
			t.Fatalf("seed room %d: %d %s", i, w.Code, w.Body.String())
		}
		roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
		w = env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
			"room_id": roomID, "name": fmt.Sprintf("租户%d", i),
		})
		if w.Code != http.StatusOK {
			t.Fatalf("seed tenant %d: %d %s", i, w.Code, w.Body.String())
		}
	}

	// pageSize=200 只回 100 条 —— 这正是「一次拿全」拿不到的证明。
	w := doJSON(env.r, http.MethodGet, "/api/rental/rooms?page=1&pageSize=200&status=occupied", env.token, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("rooms: %d %s", w.Code, w.Body.String())
	}
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	items := data["items"].([]interface{})
	if len(items) != 100 {
		t.Fatalf("pageSize=200 应被钳到 100，实得 %d", len(items))
	}
	if total := int(data["total"].(float64)); total != 120 {
		t.Fatalf("total 应为 120，实得 %d", total)
	}

	// 跨页拉全（前端 getAllRooms 的做法）能拿到全部 120 条。
	all := make([]interface{}, 0, 120)
	for page := 1; page <= 10; page++ {
		w := doJSON(env.r, http.MethodGet,
			fmt.Sprintf("/api/rental/rooms?page=%d&pageSize=100&status=occupied", page), env.token, nil)
		d := field(t, decodeBody(t, w), "data").(map[string]interface{})
		got := d["items"].([]interface{})
		all = append(all, got...)
		if len(got) == 0 || len(all) >= int(d["total"].(float64)) {
			break
		}
	}
	if len(all) != 120 {
		t.Errorf("跨页合并应拿到 120 间房，实得 %d", len(all))
	}
}

// TestBillsListPageSizeCapped：账单列表同样是 100 一页。
// 抄表弹窗要按 room_id 把「房间」与「本账期账单」配对，两边都必须拿全，
// 否则有账单的房会被当成没账单（或反之），用户看到的就是串房。
func TestBillsListPageSizeCapped(t *testing.T) {
	env := setupEnv(t)
	for i := 1; i <= 110; i++ {
		w := env.do(http.MethodPost, "/api/rental/rooms", map[string]interface{}{
			"room_no": fmt.Sprintf("Q%03d", i), "default_rent": 1000,
		})
		roomID := uint(field(t, decodeBody(t, w), "data", "id").(float64))
		env.do(http.MethodPost, "/api/rental/tenants", map[string]interface{}{
			"room_id": roomID, "name": fmt.Sprintf("T%d", i),
		})
		w = env.do(http.MethodPost, "/api/rental/bills", map[string]interface{}{
			"room_id": roomID, "period": "2026-09",
		})
		if w.Code != http.StatusOK {
			t.Fatalf("seed bill %d: %d %s", i, w.Code, w.Body.String())
		}
	}

	w := doJSON(env.r, http.MethodGet, "/api/rental/bills?page=1&pageSize=200&period=2026-09", env.token, nil)
	data := field(t, decodeBody(t, w), "data").(map[string]interface{})
	if got := len(data["items"].([]interface{})); got != 100 {
		t.Fatalf("账单 pageSize=200 应被钳到 100，实得 %d", got)
	}
	if total := int(data["total"].(float64)); total != 110 {
		t.Fatalf("账单 total 应为 110，实得 %d", total)
	}
}
