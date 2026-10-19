package rental

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestCSVCellFormulaInjection 锁住 CSV 公式注入防护：= + - @ 开头的单元格
// 必须前置单引号，普通文本原样保留。
func TestCSVCellFormulaInjection(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"张三", "张三"},
		{"=1+1", "'=1+1"},
		{"=HYPERLINK(\"http://evil\",\"x\")", "'=HYPERLINK(\"http://evil\",\"x\")"},
		{"+1", "'+1"},
		{"-1", "'-1"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"1+1", "1+1"}, // 危险字符不在首位，不转义
		{"a=b", "a=b"},
	}
	for _, c := range cases {
		if got := csvCell(c.in); got != c.want {
			t.Errorf("csvCell(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestWriteCSVEndToEnd 走真实响应链路：确认表头与数据行里的公式字符都被前置
// 单引号，且普通中文不受影响。
func TestWriteCSVEndToEnd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	writeCSV(c, "tenants.csv", [][]string{
		{"姓名", "备注"},
		{"=cmd|'/c calc'!A1", "@evil"},
		{"张三", "正常"},
	})

	body := w.Body.String()
	if !strings.Contains(body, "'=cmd|'/c calc'!A1") {
		t.Fatalf("= 开头的公式未被转义，响应: %s", body)
	}
	if !strings.Contains(body, "'@evil") {
		t.Fatalf("@ 开头的公式未被转义，响应: %s", body)
	}
	if !strings.Contains(body, "张三") || !strings.Contains(body, "正常") {
		t.Fatalf("普通中文被破坏，响应: %s", body)
	}
}
