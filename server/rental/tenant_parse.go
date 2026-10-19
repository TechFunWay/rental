// tenant_parse.go — 租户资料「粘贴识别」：把微信里复制来的纯文本解析成租户草稿。
//
// 背景（2026-10-15 用户需求）：租客自助登记依赖「登记链接」，而飞牛网关域与
// 内网穿透都不通时链接打不开（见 applyBaseConfigKey 的说明）。用户提出的替代
// 路径就是**购物平台收货地址那一套**：租客把资料按约定格式写成一段文案，微信
// 发给房东，房东整段复制粘贴到租户页顶部的识别框里，系统解析成一条条草稿，
// 房东核对/补全后一键入库。多位租户的资料可以写在同一条消息里，一起识别。
//
// 因此本文件是**纯本地规则解析**：不调 AI、不发任何网络请求、不读任何第三方
// 服务——这正是它相对于「AI 证件识别」的价值：外网不通也能用。格式约定：
//   - 一条记录若干行，每行「字段名：值」，中英文冒号均可（先统一替换成半角）；
//   - 多位租户依次再写一遍，**同一字段第二次出现即视为下一条记录的开始**；
//   - 字段名支持常见别名（姓名/名字、手机号/电话、身份证号/证件…）；
//   - 没写字段名的裸行（单独一行的手机号/身份证号）按值本身推断。
//
// 设计取舍见仓库内 docs/13-租户资料粘贴识别设计.md。
//
// 解析只产出草稿与提示，**不写库**；入库走 POST /rental/tenants/batch，
// 复用租客 CSV 导入那一套（房源按「小区+房号」匹配、同名在租合并更新），
// 保证两条录入路径的口径一致。
package rental

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"smallgo/server/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 粘贴识别的规模上限：一次最多 50 位租户、2 万个字。
// 与 CSV 导入的错误行上限（50）同一个量级，挡住误把整本台账粘进来的操作。
const (
	maxPasteTenants = 50
	maxPasteChars   = 20000
)

// 字段别名表：field 是内部字段名，aliases 是租客在文案里可能的写法。
// 顺序即优先级——正则按别名长度降序构造，「身份证号」不会被「身份证」抢先。
var tenantFieldAliases = []struct {
	field   string
	aliases []string
}{
	{"name", []string{"租户姓名", "租户名称", "承租人", "姓名", "名字", "租户", "名称"}},
	{"phone", []string{"手机号码", "手机号", "联系电话", "电话号码", "联系方式", "手机", "电话"}},
	{"id_card", []string{"身份证号码", "身份证号", "证件号码", "证件号", "身份证", "证件"}},
	{"community", []string{"小区名称", "小区", "社区", "楼盘"}},
	{"room_no", []string{"房间号", "房号", "房间", "房屋"}},
	{"move_in_date", []string{"起租日期", "入住日期", "起租时间", "入住时间", "入住"}},
	{"lease_end_date", []string{"租约到期日", "租约到期", "到期日期", "到期时间", "到期日", "租期到"}},
	{"deposit", []string{"押金"}},
	{"notes", []string{"备注", "说明"}},
}

// tenantAliasRe 匹配「字段名（可选空白）冒号」；分组 1 就是字段名本身。
// 中英文冒号在 parseTenantText 里已统一替换成半角，这里只需认半角。
var tenantAliasRe = regexp.MustCompile(aliasPattern())

// tenantAliasField 别名 → 内部字段名。
var tenantAliasField = aliasFieldMap()

// aliasPattern 由别名表生成正则：别名长的排在前面，避免前缀抢匹配。
func aliasPattern() string {
	all := make([]string, 0, 32)
	for _, f := range tenantFieldAliases {
		all = append(all, f.aliases...)
	}
	// 长度降序（同长度按别名表顺序，正则本身稳定）。
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && len([]rune(all[j])) > len([]rune(all[j-1])); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
	return `(` + strings.Join(all, "|") + `)\s*:`
}

func aliasFieldMap() map[string]string {
	m := map[string]string{}
	for _, f := range tenantFieldAliases {
		for _, a := range f.aliases {
			m[a] = f.field
		}
	}
	return m
}

// tenantDraft 一条识别出来的租户草稿（尚未入库，前端可改）。
// Warnings 是格式类提示（手机号位数不对、日期认不出等），不阻断入库——
// 房东核对后自己决定改不改。Lines 记录来源行号，方便回头对照原文。
type tenantDraft struct {
	Name         string   `json:"name"`
	Phone        string   `json:"phone"`
	IDCard       string   `json:"id_card"`
	Community    string   `json:"community"`
	RoomNo       string   `json:"room_no"`
	MoveInDate   string   `json:"move_in_date"`
	LeaseEndDate string   `json:"lease_end_date"`
	Deposit      float64  `json:"deposit"`
	Notes        string   `json:"notes"`
	Warnings     []string `json:"warnings"`
	Lines        []int    `json:"lines"`
}

// tenantParseResult 粘贴识别接口出参。
type tenantParseResult struct {
	Drafts    []tenantDraft `json:"drafts"`
	Unmatched []string      `json:"unmatched_lines"` // 没能识别的原文行（原样带回，方便房东看出哪里写岔了）
	Count     int           `json:"count"`
}

// tenantPastePair 文案里拆出的一个「字段：值」。
type tenantPastePair struct {
	field string
	value string
	line  int
}

// parseTenantText 解析粘贴的纯文本：返回草稿列表与未能识别的原文行。
// 纯函数，不碰库、不发请求——同样的输入永远得到同样的输出（便于测试）。
func parseTenantText(text string) ([]tenantDraft, []string) {
	// 全角冒号统一成半角、全角空格统一成普通空格（用户明确要求两种冒号都支持）。
	text = strings.ReplaceAll(text, "：", ":")
	text = strings.ReplaceAll(text, "　", " ")

	lines := splitPasteLines(text)
	pairs, bareValues, unmatched := collectPastePairs(lines)
	drafts := groupPastePairs(pairs)
	fillBareValues(drafts, bareValues, &unmatched)
	return drafts, unmatched
}

// splitPasteLines 按行切开并丢掉空行，同时保留**原始行号**（从 1 开始），
// 报「第几行没识别出来」时对得上房东眼里的文案。
func splitPasteLines(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// bareValue 没有字段名的行（单独一行的手机号/身份证号），行号一并留着。
type bareValue struct {
	value string
	line  int
}

// collectPastePairs 逐行切出「字段：值」，并处理两种微信里常见的写法：
//  1. **字段名单独占一行、值在下一行**（微信复制长内容时会自动折行）：
//     上一行末尾字段的值为空时，下一行接上去；
//  2. **一行里写多个字段**（"姓名：张三 手机号：138…"）：按字段名边界切开。
//
// 同时返回没有字段名的行（bareValues）与彻底读不了的行（unmatched）。
func collectPastePairs(lines []string) (pairs []tenantPastePair, bare []bareValue, unmatched []string) {
	// pending 指向「值还空着、等下一行来接」的那个 pair；nil 表示没有。
	var pending *tenantPastePair

	for i, raw := range lines {
		lineNo := i + 1
		locs := tenantAliasRe.FindAllStringSubmatchIndex(raw, -1)
		if len(locs) == 0 {
			if pending != nil {
				// 续行：接到上一个空着值的字段后面（中间补一个空格，
				// 免得把两个词粘成一个；手机号/证件号后续会去掉空白）。
				pending.value = strings.TrimSpace(pending.value + " " + raw)
				pending = nil
				continue
			}
			bare = append(bare, bareValue{value: raw, line: lineNo})
			continue
		}
		for j, loc := range locs {
			field := tenantAliasField[raw[loc[2]:loc[3]]]
			end := len(raw)
			if j+1 < len(locs) {
				end = locs[j+1][0]
			}
			value := strings.TrimSpace(raw[loc[1]:end])
			value = strings.TrimRight(value, ",，;；")
			pairs = append(pairs, tenantPastePair{field: field, value: value, line: lineNo})
		}
		// 这一行最后一个字段的值空着 → 等下一行来接。
		if last := &pairs[len(pairs)-1]; last.value == "" {
			pending = last
		} else {
			pending = nil
		}
	}
	return pairs, bare, unmatched
}

// groupPastePairs 把「字段：值」序列切成一条条租户：**同一字段第二次出现
// 即视为新记录的开始**（租客不会在同一条资料里写两个姓名/两个手机号，
// 出现第二次只能是下一位租户），因此不需要依赖固定字段顺序。
func groupPastePairs(pairs []tenantPastePair) []tenantDraft {
	var drafts []tenantDraft
	fields := map[string]string{}
	lines := map[string][]int{}

	flush := func() {
		if len(fields) == 0 {
			return
		}
		drafts = append(drafts, newTenantDraft(fields, lines))
		fields = map[string]string{}
		lines = map[string][]int{}
	}

	for _, p := range pairs {
		if p.value == "" {
			// 空值不占位：写了「押金：」但没写数字，等同没写。
			continue
		}
		if _, dup := fields[p.field]; dup {
			flush()
		}
		fields[p.field] = p.value
		lines[p.field] = append(lines[p.field], p.line)
	}
	flush()
	return drafts
}

// fillBareValues 兜底：没有字段名的行按**值本身**推断——整体是 18/15 位
// 身份证号就填证件、是 11 位手机号就填电话。
//
// 归属按**行号**算：裸行落在哪位租户的字段区间里就归谁（一位租户的字段通常
// 连续若干行，下一个字段名出现即宣告上一位结束）。不能按「最后一条草稿」无脑
// 填——那样多位租户时，第一个人的裸行手机号会被塞进最后一个人名下，
// 而且每个人只填一个字段，多出来的裸行就白丢了（2026-10-15 复查实测）。
func fillBareValues(drafts []tenantDraft, bare []bareValue, unmatched *[]string) {
	if len(drafts) == 0 || len(bare) == 0 {
		// 一条草稿都没有时，裸行无从归属（整体也没有字段名），原样提示。
		if len(drafts) == 0 {
			for _, b := range bare {
				*unmatched = append(*unmatched, b.value)
			}
		}
		return
	}
	for _, b := range bare {
		if idx := draftIndexForLine(drafts, b.line); idx >= 0 {
			cur := &drafts[idx]
			switch {
			case cur.IDCard == "" && idCardPattern.MatchString(strings.ToUpper(compactDigits(b.value))):
				cur.IDCard = strings.ToUpper(compactDigits(b.value))
			case cur.Phone == "" && isPastedPhone(b.value):
				cur.Phone = normalizePastedPhone(b.value)
			default:
				*unmatched = append(*unmatched, b.value)
			}
			continue
		}
		*unmatched = append(*unmatched, b.value)
	}
}

// draftIndexForLine 裸行归属哪条草稿：取「起始行 ≤ 裸行行号」的最后一条
// （下一位租户的字段名还没出现，说明这行仍属于上一位）。
func draftIndexForLine(drafts []tenantDraft, line int) int {
	idx := -1
	for i := range drafts {
		if len(drafts[i].Lines) == 0 {
			continue
		}
		if drafts[i].Lines[0] <= line {
			idx = i
		}
	}
	return idx
}

// newTenantDraft 把一组字段值归一成草稿，并收集格式类提示。
func newTenantDraft(fields map[string]string, lines map[string][]int) tenantDraft {
	d := tenantDraft{Name: strings.TrimSpace(fields["name"])}
	if len([]rune(d.Name)) > 30 {
		d.Warnings = append(d.Warnings, "姓名超过 30 个字，入库时会被服务端拒绝")
	}
	if v := fields["phone"]; v != "" {
		phone := normalizePastedPhone(v)
		switch {
		case isPastedPhone(v):
			d.Phone = phone
		case leadingPhone(v) != "":
			// 认不出时再兜一刀：值里可能混着尾随的中文客套
			// （"18956565845 麻烦尽快登记"），截出数字段让房东看到号码本身。
			d.Phone = leadingPhone(v)
			d.Warnings = append(d.Warnings, "手机号后面还有别的内容，已只取号码部分："+d.Phone)
		default:
			d.Phone = phone
			d.Warnings = append(d.Warnings, "手机号不是 11 位数字，请核对："+phone)
		}
	}
	if v := fields["id_card"]; v != "" {
		id := strings.ToUpper(compactDigits(v))
		switch {
		case idCardPattern.MatchString(id):
			d.IDCard = id
		case leadingIDCard(v) != "":
			// 同手机号：混着中文时截出数字段，至少把证件号本身带出来。
			d.IDCard = leadingIDCard(v)
			d.Warnings = append(d.Warnings, "身份证号后面还有别的内容，已只取证件号部分："+d.IDCard)
		default:
			d.IDCard = id
			d.Warnings = append(d.Warnings, "身份证号应为 18 位（末位可为 X）或 15 位，请核对")
		}
	}
	d.Community = strings.TrimSpace(fields["community"])
	d.RoomNo = strings.TrimSpace(fields["room_no"])
	if v := fields["move_in_date"]; v != "" {
		d.MoveInDate, d.Warnings = normalizePastedDate(v, "入住日期", d.Warnings)
	}
	if v := fields["lease_end_date"]; v != "" {
		d.LeaseEndDate, d.Warnings = normalizePastedDate(v, "到期时间", d.Warnings)
	}
	if v := fields["deposit"]; v != "" {
		amount, ok := parsePastedMoney(v)
		if !ok {
			d.Warnings = append(d.Warnings, "押金不是有效数字，已忽略："+v)
		} else {
			d.Deposit = amount
		}
	}
	d.Notes = strings.TrimSpace(fields["notes"])
	if d.Name == "" {
		d.Warnings = append(d.Warnings, "没读到姓名，入库时会被服务端拒绝")
	}
	// 来源行号去重后按顺序带上（一位租户通常占 3-4 行）。
	seen := map[int]bool{}
	for _, f := range []string{"name", "phone", "id_card", "community", "room_no", "move_in_date", "lease_end_date", "deposit", "notes"} {
		for _, ln := range lines[f] {
			if !seen[ln] {
				seen[ln] = true
				d.Lines = append(d.Lines, ln)
			}
		}
	}
	return d
}

// compactDigits 去掉号码里的空格与分隔符（微信复制出来常带空格或折行）。
func compactDigits(v string) string {
	return strings.NewReplacer(" ", "", "-", "", "－", "", "—", "", ".", "", "·", "").Replace(v)
}

// normalizePastedPhone 规范化手机号：去空白与分隔符。
func normalizePastedPhone(v string) string {
	return compactDigits(strings.TrimSpace(v))
}

// isPastedPhone 是否像手机号（11 位、1 开头）。座机/带区号的写法不算——
// 那种按普通字符串存，由房东核对。
func isPastedPhone(v string) bool {
	p := normalizePastedPhone(v)
	return len(p) == 11 && strings.HasPrefix(p, "1")
}

// leadingPhone 从值的**开头**截一段像手机号的数字串：微信文案里手机号后面
// 常跟着客套话（"18956565845 麻烦尽快登记"），整串存进去既不像号码也会被
// 服务端拒；这里至少把号码本身捞出来给房东核对。找不到返回空串。
func leadingPhone(v string) string {
	p := normalizePastedPhone(v)
	// 数字段按 11 位窗口找：从第一个 1 开始，取连续的 11 位数字。
	for i := 0; i+11 <= len(p); i++ {
		if p[i] != '1' {
			continue
		}
		seg := p[i : i+11]
		if !isAllDigits(seg) {
			continue
		}
		return seg
	}
	return ""
}

// leadingIDCard 同 leadingPhone：从值里截一段像身份证号的数字串
// （18 位，末位可为 X）。找不到返回空串。
func leadingIDCard(v string) string {
	s := strings.ToUpper(compactDigits(strings.TrimSpace(v)))
	// 先按 X 结尾找（18 位的常见形态），再退回纯数字窗口。
	if i := strings.IndexByte(s, 'X'); i >= 17 {
		seg := s[i-17 : i+1]
		if isAllDigits(seg[:17]) {
			return seg
		}
	}
	for i := 0; i+18 <= len(s); i++ {
		seg := s[i : i+18]
		if isAllDigits(seg) {
			return seg
		}
	}
	return ""
}

// isAllDigits 是否全部是 ASCII 数字（空串返回 false）。
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// normalizePastedDate 把租客随手写的日期归一成 YYYY-MM-DD：
// 2026-01-01 / 2026/1/1 / 2026.1.1 / 2026年1月1日 / 20260101 都认；
// 认不出时原样带回并给提示（服务端日期列会拒，房东能看到原因）。
func normalizePastedDate(v, label string, warnings []string) (string, []string) {
	raw := strings.TrimSpace(v)
	nums := regexp.MustCompile(`\d+`).FindAllString(raw, -1)
	switch {
	case len(nums) == 3:
		y, m, d := nums[0], nums[1], nums[2]
		if len(y) == 2 {
			y = "20" + y
		}
		out := fmt.Sprintf("%s-%02s-%02s", y, m, d)
		if _, err := time.ParseInLocation("2006-01-02", out, time.Local); err != nil {
			return raw, append(warnings, label+"不是有效日期："+raw)
		}
		return out, warnings
	case len(nums) == 1 && len(nums[0]) == 8:
		out := fmt.Sprintf("%s-%s-%s", nums[0][:4], nums[0][4:6], nums[0][6:])
		if _, err := time.ParseInLocation("2006-01-02", out, time.Local); err != nil {
			return raw, append(warnings, label+"不是有效日期："+raw)
		}
		return out, warnings
	default:
		return raw, append(warnings, label+"格式应为 YYYY-MM-DD，请核对："+raw)
	}
}

// parsePastedMoney 解析金额：容忍「3000元」「3,000」「3000.00」。
// 值里混着中文客套（"3000元，谢谢配合"）时取**开头**的数字段——
// 押金这种数字字段丢一个字符就整项作废，宁可截出来让房东核对。
func parsePastedMoney(v string) (float64, bool) {
	s := strings.TrimSpace(v)
	s = strings.NewReplacer(",", "", "，", "", "元", "", "块", "", " ", "").Replace(s)
	// 截到第一个非数字字符（保留小数点：3000.00 里的点有意义）。
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9' || s[end] == '.') {
		end++
	}
	s = s[:end]
	if s == "" || s == "." {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	return f, true
}

// ---------------------------------------------------------------------------
// 接口
// ---------------------------------------------------------------------------

// handleTenantTextParse 解析粘贴的租户资料文案（只读，不写库）。
//
// 刻意不查库：解析结果要给房东核对/补全，房源匹配在前端做（那里已经有
// 全量房源列表），服务端少一次查询也少一处口径分歧。
func handleTenantTextParse() gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Text string `json:"text"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "请粘贴租户发来的资料文案")
			return
		}
		text := strings.TrimSpace(req.Text)
		if text == "" {
			response.ErrorBadRequest(c, "请先粘贴租户发来的资料文案")
			return
		}
		if len([]rune(text)) > maxPasteChars {
			response.ErrorBadRequest(c, fmt.Sprintf("一次最多粘贴 %d 个字，请分批识别", maxPasteChars))
			return
		}
		drafts, unmatched := parseTenantText(text)
		if len(drafts) == 0 {
			response.ErrorBadRequest(c, "没识别到租户资料：请按「姓名：张三」这样一行一个字段写，多位租户依次再写一遍")
			return
		}
		if len(drafts) > maxPasteTenants {
			drafts = drafts[:maxPasteTenants]
		}
		if unmatched == nil {
			unmatched = []string{}
		}
		// 空数组而不是 null：前端遍历时不必逐个判空。
		for i := range drafts {
			if drafts[i].Warnings == nil {
				drafts[i].Warnings = []string{}
			}
			if drafts[i].Lines == nil {
				drafts[i].Lines = []int{}
			}
		}
		response.Success(c, tenantParseResult{Drafts: drafts, Unmatched: unmatched, Count: len(drafts)})
	}
}

// tenantBatchItem 批量入库的一条草稿（房东核对/补全后的版本）。
// 字段与租客 CSV 导入模板逐列对应， deposit 是押金（元）。
type tenantBatchItem struct {
	Name         string  `json:"name"`
	Phone        string  `json:"phone"`
	IDCard       string  `json:"id_card"`
	Community    string  `json:"community"`
	RoomNo       string  `json:"room_no"`
	MoveInDate   string  `json:"move_in_date"`
	LeaseEndDate string  `json:"lease_end_date"`
	Deposit      float64 `json:"deposit"`
	Notes        string  `json:"notes"`
}

// handleTenantsBatch 把识别并核对过的草稿批量入库。
//
// 复用 importTenantRow（租客 CSV 导入的逐行逻辑）：房源必须已存在（按
// 「小区+房号」在本次写入台账内匹配，不自动建房）、同房同名在租租户按
// 「非空覆盖、空值保留」合并更新、批内重复键报错跳过。两条录入路径口径
// 完全一致，不会出现「CSV 导入建档、粘贴识别又建一条」的分裂。
func handleTenantsBatch(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Items []tenantBatchItem `json:"items"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.ErrorBadRequest(c, "没有要入库的租户资料")
			return
		}
		if len(req.Items) == 0 {
			response.ErrorBadRequest(c, "没有要入库的租户资料")
			return
		}
		if len(req.Items) > maxPasteTenants {
			response.ErrorBadRequest(c, fmt.Sprintf("一次最多入库 %d 位租户", maxPasteTenants))
			return
		}

		result := &importResult{Errors: []importRowError{}}
		seen := map[string]int{} // 小区\x00房号\x00姓名 → 首次出现序号（与 CSV 导入同规则）

		for i, item := range req.Items {
			lineNo := i + 1 // 批内序号，从 1 起（界面按「第 n 条」展示）
			created, reason := importTenantRow(db, c, tenantBatchRow(&item), seen, lineNo)
			switch {
			case reason != "":
				result.Failed++
				if len(result.Errors) < 50 {
					result.Errors = append(result.Errors, importRowError{Line: lineNo, Reason: reason})
				}
			case created:
				result.Created++
			default:
				result.Updated++
			}
		}
		response.Success(c, result)
	}
}

// tenantBatchRow 把一条草稿映射成租客导入模板的 15 列记录：
// 计费与缴费列（水费模式/金额、缴费周期/缴费日/提醒天数）一律留空 =
// 跟随全局默认，与「登记租户表单留空」同口径。
func tenantBatchRow(item *tenantBatchItem) []string {
	// 押金 0 一律按空单元格传：CSV 导入的合并口径是「空 = 不改」，填 0 会把
	// 已登记的押金清掉（重复识别同一位租户时不该丢数据）。
	deposit := ""
	if item.Deposit > 0 {
		deposit = formatPrice(item.Deposit)
	}
	return []string{
		strings.TrimSpace(item.Community),
		strings.TrimSpace(item.RoomNo),
		strings.TrimSpace(item.Name),
		strings.TrimSpace(item.Phone),
		strings.TrimSpace(item.IDCard),
		strings.TrimSpace(item.MoveInDate),
		strings.TrimSpace(item.LeaseEndDate),
		deposit,
		"", "", "", "", "", "",
		strings.TrimSpace(item.Notes),
	}
}
