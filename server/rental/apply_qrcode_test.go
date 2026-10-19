// apply_qrcode_test.go — 租客登记二维码（issue #5 需求 2）。
//
// 二维码这类东西最容易「看起来像对的」：图能生成、能下载，但内容错了扫出来
// 是废链。所以这里的核心用例不做「字节数 > 0」这种敷衍断言，而是**把 PNG
// 解码回文本**，逐字比对弹窗里显示的链接。
package rental

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"testing"

	qrcode "github.com/skip2/go-qrcode"
)

// assertQRCodeEncodes 断言 PNG 二维码的内容**恰好**是 want 这段文本。
//
// 为什么不用「解码」来验证：go-qrcode 这个库只导出编码、不导出解码（它自己的
// 解码测试依赖外部 zbarimg，本机没有）。改为两条路径互证：
//
//  1. 把服务端返回的 PNG 解码成像素，按模块中心采样还原成位矩阵；
//  2. 用 want 文本重新编码出位矩阵；
//  3. 两个矩阵必须逐位相同。
//
// 这比「字节数 > 0」强得多：链接错一个字符，矩阵就会不同，测试立刻红。
// 也不引入任何新依赖（纯 image/png + 同一个库的 Bitmap）。
func assertQRCodeEncodes(t *testing.T, pngBytes []byte, want string) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("返回的不是合法 PNG: %v", err)
	}

	// 期望矩阵：用同一库对 want 重新编码（Medium 等级，与服务端一致）。
	expect, err := qrcode.New(want, qrcode.Medium)
	if err != nil {
		t.Fatalf("encode want: %v", err)
	}
	expectBitmap := expect.Bitmap() // 含静默区（border），与 Image() 渲染的一致

	// 实际矩阵：从 PNG 像素里按模块采样。
	got := sampleBitmap(t, img, len(expectBitmap))
	if len(got) != len(expectBitmap) {
		t.Fatalf("模块数不一致: got %d, want %d", len(got), len(expectBitmap))
	}
	diff := 0
	for y := range expectBitmap {
		for x := range expectBitmap[y] {
			if got[y][x] != expectBitmap[y][x] {
				diff++
			}
		}
	}
	if diff != 0 {
		t.Errorf("二维码内容与期望文本不一致：%d 个模块不同（期望内容 %q）。"+
			"链接错字/错 token 会直接体现为这里的差异", diff, want)
	}
}

// sampleBitmap 把图片按 n×n 网格采样成位矩阵（模块中心取亮度）。
//
// 注意不能用「尺寸 / n 必须整除」的假设：qrcode.Encode 会按目标尺寸缩放，
// 480 / 41 模块 = 11.7 px 不是整数（实测就是这里踩到的）。改为按**比例**
// 定位每个模块的中心点，任何缩放比例都能采到正确位置。
func sampleBitmap(t *testing.T, img image.Image, n int) [][]bool {
	t.Helper()
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	out := make([][]bool, n)
	for y := 0; y < n; y++ {
		out[y] = make([]bool, n)
		for x := 0; x < n; x++ {
			// 模块中心在整图里的相对位置（+0.5 取格子中心）。
			cx := b.Min.X + int((float64(x)+0.5)*float64(w)/float64(n))
			cy := b.Min.Y + int((float64(y)+0.5)*float64(h)/float64(n))
			if cx >= b.Max.X {
				cx = b.Max.X - 1
			}
			if cy >= b.Max.Y {
				cy = b.Max.Y - 1
			}
			r, g, bl, _ := img.At(cx, cy).RGBA()
			avg := (int(r>>8) + int(g>>8) + int(bl>>8)) / 3
			out[y][x] = avg < 128 // 深色 = 模块点亮
		}
	}
	return out
}

// TestApplyQRCodeMatchesApplyLink 二维码内容必须与「登记链接」接口返回的链接一致。
//
// 这是本功能唯一真正重要的不变量：房东在弹窗里看到链接 A、下载的二维码扫出
// 链接 B，只要 A ≠ B，印出去贴墙就是废的。
func TestApplyQRCodeMatchesApplyLink(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("Q1", 1000, 0)

	// 1) 先走「生成登记链接」，拿到弹窗里显示的那条链接。
	w := env.do(http.MethodPost, "/api/rental/rooms/"+itoa(roomID)+"/apply-link", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-link: %d %s", w.Code, w.Body.String())
	}
	link := field(t, decodeBody(t, w), "data", "link").(string)
	if link == "" {
		t.Fatal("登记链接为空")
	}

	// 2) 再取二维码，解码后必须逐字相同。
	w = env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-qrcode: %d %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	assertQRCodeEncodes(t, w.Body.Bytes(), link)
}

// TestApplyQRCodeCreatesTokenWhenMissing 没有 token 时先建再出码（不能给空码）。
func TestApplyQRCodeCreatesTokenWhenMissing(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("Q2", 1000, 0)

	// 直接要二维码，从未生成过链接。
	w := env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-qrcode: %d %s", w.Code, w.Body.String())
	}
	// token 必须已落库：再取一次链接，应拿到同一个 token（而不是又新建一个）。
	w = env.do(http.MethodPost, "/api/rental/rooms/"+itoa(roomID)+"/apply-link", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-link: %d %s", w.Code, w.Body.String())
	}
	link := field(t, decodeBody(t, w), "data", "link").(string)
	if !bytes.Contains([]byte(link), []byte("/apply/")) {
		t.Fatalf("登记链接 %q 里没有 /apply/ 路径", link)
	}

	// 重新取一次二维码，应编码「现在这条链接」——证明两次用的是同一个 token。
	w = env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-qrcode again: %d %s", w.Code, w.Body.String())
	}
	assertQRCodeEncodes(t, w.Body.Bytes(), link)
}

// TestApplyQRCodeRegenerateChangesContent 换新 token 后二维码内容随之改变。
func TestApplyQRCodeRegenerateChangesContent(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("Q3", 1000, 0)

	w := env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("first qrcode: %d", w.Code)
	}
	first := w.Body.Bytes()

	// 同时记录此刻的链接，确认首张二维码编码的就是它。
	w = env.do(http.MethodPost, "/api/rental/rooms/"+itoa(roomID)+"/apply-link", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-link: %d %s", w.Code, w.Body.String())
	}
	firstLink := field(t, decodeBody(t, w), "data", "link").(string)
	assertQRCodeEncodes(t, first, firstLink)

	w = env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode?regenerate=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("regen qrcode: %d", w.Code)
	}
	regen := w.Body.Bytes()

	// 换 token 后内容必须变：分别与「换 token 之后」的链接比对。
	w = env.do(http.MethodPost, "/api/rental/rooms/"+itoa(roomID)+"/apply-link", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("apply-link: %d %s", w.Code, w.Body.String())
	}
	newLink := field(t, decodeBody(t, w), "data", "link").(string)
	assertQRCodeEncodes(t, regen, newLink)
	if bytes.Equal(first, regen) {
		t.Errorf("regenerate=1 后二维码图片字节完全相同：旧链接应失效")
	}
}

// TestApplyQRCodeDownloadDisposition download=1 触发附件下载，默认内联显示。
func TestApplyQRCodeDownloadDisposition(t *testing.T) {
	env := setupEnv(t)
	roomID := env.seedRoom("Q4", 1000, 0)

	w := env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("inline: %d", w.Code)
	}
	if disp := w.Header().Get("Content-Disposition"); !bytes.HasPrefix([]byte(disp), []byte("inline")) {
		t.Errorf("默认应为 inline，得到 %q", disp)
	}

	w = env.do(http.MethodGet, "/api/rental/rooms/"+itoa(roomID)+"/apply-qrcode?download=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("download: %d", w.Code)
	}
	disp := w.Header().Get("Content-Disposition")
	if !bytes.HasPrefix([]byte(disp), []byte("attachment")) {
		t.Errorf("download=1 应为 attachment，得到 %q", disp)
	}
	if !bytes.Contains([]byte(disp), []byte("filename*=UTF-8''")) {
		t.Errorf("中文文件名应按 RFC 5987 编码，得到 %q", disp)
	}
}

// TestApplyQRCodeLedgerIsolation 别的台账的房间拿不到二维码（也不能被探测存在）。
func TestApplyQRCodeLedgerIsolation(t *testing.T) {
	env := setupEnv(t)
	owner := newPersonalLedger(t, env, "qr-owner-a")
	ledgerB := createSharedLedger(t, env, owner.token, "B台账")
	// B 台账的房间（只有 A 的 owner 是它的成员）。
	roomB := seedRoomInLedger(t, env, owner.token, ledgerB, "隔离小区", "Q5", "隔离租户")

	// 另一个只属于自己的用户，拿 B 的 room_id 请求。
	other := newPersonalLedger(t, env, "qr-owner-b")
	w := doJSON(env.r, http.MethodGet, "/api/rental/rooms/"+itoa(roomB)+"/apply-qrcode", other.token, nil)
	if w.Code == http.StatusOK {
		t.Errorf("非本台账成员应拿不到二维码，却返回 200")
	}
	if w.Code != http.StatusNotFound {
		t.Errorf("越权应回 404（不泄漏存在性），得到 %d %s", w.Code, w.Body.String())
	}
}
