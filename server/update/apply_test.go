package update

// apply_test.go — 一键更新的选包/解包/基址校验单测。
// 重点是 2026-10-19「飞牛网关形态点更新后白屏」这一类的回归：
// 选错包（发行包 vs 飞牛安装包）或前端基址与部署形态不一致，都必须在
// 替换文件之前被拦住。

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"smallgo/server/config"
)

// tarEntry 是构造 tar 流的条目。
type tarEntry struct {
	name string
	body []byte
	mode int64
	typ  byte
}

// tarFrom 把条目打成未压缩的 tar 流。
func tarFrom(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: e.name, Mode: mode, Size: int64(len(e.body)), Typeflag: typ}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("写 tar 头 %s 失败：%v", e.name, err)
		}
		if len(e.body) > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatalf("写 tar 内容 %s 失败：%v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("关闭 tar 流失败：%v", err)
	}
	return buf.Bytes()
}

// gzipBytes 把数据压成 gzip。
func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("写 gzip 失败：%v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("关闭 gzip 失败：%v", err)
	}
	return buf.Bytes()
}

// fakeBinary 生成能过 sanityCheckBinary 的假二进制（ELF 魔数 + 10MB 体积）。
func fakeBinary() []byte {
	b := make([]byte, 10<<20)
	copy(b, "\x7fELF")
	return b
}

// writeFakeFPK 以给定 manifest 与 app.tgz 合成一个 fpk（外层 tar.gz）。
func writeFakeFPK(t *testing.T, dir, manifest string, appTGZ []byte) string {
	t.Helper()
	outer := tarFrom(t, []tarEntry{
		{name: "manifest", body: []byte(manifest), mode: 0o644},
		{name: "app.tgz", body: appTGZ, mode: 0o644},
		// 安装包里的其它文件不参与自更新，用来确认不会被展开。
		{name: "cmd/main", body: []byte("#!/bin/sh\n"), mode: 0o755},
	})
	p := filepath.Join(dir, "techfunway-rental_v0.7.1_x86.fpk")
	if err := os.WriteFile(p, gzipBytes(t, outer), 0o644); err != nil {
		t.Fatalf("写 fpk 失败：%v", err)
	}
	return p
}

// setFnOSApp 临时切换部署形态（全局配置，测试串行执行，用完恢复）。
func setFnOSApp(t *testing.T, on bool) {
	t.Helper()
	old := config.C.FnOSApp
	config.C.FnOSApp = on
	t.Cleanup(func() { config.C.FnOSApp = old })
}

func TestPickFpkAsset(t *testing.T) {
	info := Info{
		Latest: "v0.7.1",
		FpkAssets: []FpkAsset{
			{Name: "techfunway-rental_v0.7.1_x86.fpk", URL: "https://example.com/x86"},
			{Name: "techfunway-rental_v0.7.1_arm64.fpk", URL: "https://example.com/arm64"},
		},
	}

	t.Run("amd64 取 x86 包", func(t *testing.T) {
		got, err := pickFpkAsset(info, "amd64")
		if err != nil {
			t.Fatalf("pickFpkAsset() err = %v", err)
		}
		if got.URL != "https://example.com/x86" {
			t.Fatalf("got = %+v, want x86", got)
		}
	})

	t.Run("arm64 取 arm64 包", func(t *testing.T) {
		got, err := pickFpkAsset(info, "arm64")
		if err != nil {
			t.Fatalf("pickFpkAsset() err = %v", err)
		}
		if got.URL != "https://example.com/arm64" {
			t.Fatalf("got = %+v, want arm64", got)
		}
	})

	t.Run("不支持的架构直接报错", func(t *testing.T) {
		if _, err := pickFpkAsset(info, "386"); err == nil {
			t.Fatal("pickFpkAsset() 应当报错")
		}
	})

	t.Run("发行版没有 fpk 时给人工下载指引", func(t *testing.T) {
		_, err := pickFpkAsset(Info{Latest: "v0.7.1"}, "amd64")
		if err == nil {
			t.Fatal("pickFpkAsset() 应当报错")
		}
		if !strings.Contains(err.Error(), "手动下载") {
			t.Fatalf("err = %v，应当提示手动下载", err)
		}
	})

	t.Run("缺少匹配架构时给人工下载指引", func(t *testing.T) {
		only := Info{Latest: "v0.7.1", FpkAssets: []FpkAsset{{Name: "techfunway-rental_v0.7.1_x86.fpk", URL: "u"}}}
		_, err := pickFpkAsset(only, "arm64")
		if err == nil || !strings.Contains(err.Error(), "手动下载") {
			t.Fatalf("err = %v，应当提示手动下载", err)
		}
	})
}

func TestPickUpdateAsset(t *testing.T) {
	info := Info{
		Latest: "v0.7.1",
		assets: []releaseAsset{
			{Name: "techfunway-rental-v0.7.1-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/tar"},
		},
		FpkAssets: []FpkAsset{{Name: "techfunway-rental_v0.7.1_x86.fpk", URL: "https://example.com/fpk"}},
	}

	t.Run("飞牛网关形态取 fpk", func(t *testing.T) {
		setFnOSApp(t, true)
		got, err := pickUpdateAsset(info, "linux", "amd64")
		if err != nil {
			t.Fatalf("pickUpdateAsset() err = %v", err)
		}
		if got.URL != "https://example.com/fpk" {
			t.Fatalf("got = %+v, want fpk", got)
		}
	})

	t.Run("直连形态取发行包", func(t *testing.T) {
		setFnOSApp(t, false)
		got, err := pickUpdateAsset(info, "linux", "amd64")
		if err != nil {
			t.Fatalf("pickUpdateAsset() err = %v", err)
		}
		if got.URL != "https://example.com/tar" {
			t.Fatalf("got = %+v, want tar", got)
		}
	})

	t.Run("网关形态缺 fpk 时不得退回发行包", func(t *testing.T) {
		setFnOSApp(t, true)
		noFpk := Info{Latest: "v0.7.1", assets: info.assets}
		if _, err := pickUpdateAsset(noFpk, "linux", "amd64"); err == nil {
			t.Fatal("退回根基址发行包会白屏，必须报错")
		}
	})
}

func TestExtractFPK(t *testing.T) {
	appTGZ := gzipBytes(t, tarFrom(t, []tarEntry{
		{name: "rental", body: []byte("\x7fELFfake"), mode: 0o755},
		{name: "ui", typ: tar.TypeDir, mode: 0o755},
		{name: "ui/index.html", body: []byte(`<script src="/app/techfunway-rental/assets/a.js"></script>`)},
		{name: "ui/assets/a.js", body: []byte("console.log(1)")},
	}))
	manifest := "appname                    = techfunway-rental\nversion               = 0.7.1\nplatform                   = x86\n"

	t.Run("解出二进制与 ui 前端", func(t *testing.T) {
		dir := t.TempDir()
		fpk := writeFakeFPK(t, dir, manifest, appTGZ)
		stage := filepath.Join(dir, "stage")

		pkg, err := extractFPK(fpk, stage, "rental")
		if err != nil {
			t.Fatalf("extractFPK() err = %v", err)
		}
		if pkg.Manifest.Appname != "techfunway-rental" || pkg.Manifest.Version != "0.7.1" || pkg.Manifest.Platform != "x86" {
			t.Fatalf("manifest = %+v", pkg.Manifest)
		}
		if pkg.BinPath != filepath.Join(stage, "rental") {
			t.Fatalf("BinPath = %s", pkg.BinPath)
		}
		fi, err := os.Stat(pkg.BinPath)
		if err != nil {
			t.Fatalf("二进制未落地：%v", err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("二进制缺少可执行位：%v", fi.Mode())
		}
		if _, err := os.Stat(filepath.Join(pkg.WebPath, "assets", "a.js")); err != nil {
			t.Fatalf("前端资源未落地：%v", err)
		}
		if _, err := os.Stat(filepath.Join(stage, "cmd")); !os.IsNotExist(err) {
			t.Fatalf("cmd/ 不应参与自更新，stat err = %v", err)
		}
	})

	t.Run("程序名不同也认包内的 rental", func(t *testing.T) {
		dir := t.TempDir()
		fpk := writeFakeFPK(t, dir, manifest, appTGZ)
		stage := filepath.Join(dir, "stage")

		pkg, err := extractFPK(fpk, stage, "smallgo")
		if err != nil {
			t.Fatalf("extractFPK() err = %v", err)
		}
		if pkg.BinPath != filepath.Join(stage, "smallgo") {
			t.Fatalf("BinPath = %s", pkg.BinPath)
		}
	})

	t.Run("没有 app.tgz 报错", func(t *testing.T) {
		dir := t.TempDir()
		outer := tarFrom(t, []tarEntry{{name: "manifest", body: []byte("version = 0.7.1\n")}})
		fpk := filepath.Join(dir, "broken.fpk")
		if err := os.WriteFile(fpk, gzipBytes(t, outer), 0o644); err != nil {
			t.Fatalf("写 fpk 失败：%v", err)
		}
		_, err := extractFPK(fpk, filepath.Join(dir, "stage"), "rental")
		if err == nil || !strings.Contains(err.Error(), "app.tgz") {
			t.Fatalf("err = %v，应当说明缺 app.tgz", err)
		}
	})

	t.Run("越界路径被拒绝", func(t *testing.T) {
		for _, name := range []string{"../evil.sh", "/etc/passwd", "ui/../../evil.sh"} {
			appTGZ := gzipBytes(t, tarFrom(t, []tarEntry{{name: name, body: []byte("x")}}))
			dir := t.TempDir()
			fpk := writeFakeFPK(t, dir, manifest, appTGZ)
			if _, err := extractFPK(fpk, filepath.Join(dir, "stage"), "rental"); err == nil {
				t.Fatalf("条目 %q 应当被拒绝", name)
			}
		}
	})
}

func TestCheckFPKManifest(t *testing.T) {
	cases := []struct {
		name    string
		m       fpkManifest
		latest  string
		wantErr bool
	}{
		{"版本一致", fpkManifest{Appname: "techfunway-rental", Version: "0.7.1"}, "v0.7.1", false},
		{"版本不一致", fpkManifest{Appname: "techfunway-rental", Version: "0.7.0"}, "v0.7.1", true},
		{"包名不对", fpkManifest{Appname: "techfunway-other", Version: "0.7.1"}, "v0.7.1", true},
		{"老包没有版本字段则跳过", fpkManifest{}, "v0.7.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkFPKManifest(tc.m, tc.latest)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkFPKManifest() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestVerifyFrontendBase(t *testing.T) {
	const prefix = "/app/techfunway-rental"
	gatewayPage := `<link rel="manifest" href="/app/techfunway-rental/manifest.webmanifest">` +
		`<script src="/app/techfunway-rental/assets/a.js"></script>` +
		`<link href="/app/techfunway-rental/assets/a.css" rel="stylesheet">`
	rootPage := `<link rel="manifest" href="/manifest.webmanifest">` +
		`<script src="/assets/a.js"></script>` +
		`<link href="/assets/a.css" rel="stylesheet">`

	cases := []struct {
		name    string
		page    string
		gateway bool
		prefix  string
		wantErr bool
	}{
		{"网关形态 + 网关基址前端", gatewayPage, true, prefix, false},
		{"网关形态 + 根基址前端（白屏组合）", rootPage, true, prefix, true},
		{"直连形态 + 根基址前端", rootPage, false, prefix, false},
		{"直连形态 + 网关基址前端", gatewayPage, false, prefix, true},
		{"网关形态缺前缀配置", gatewayPage, true, "", true},
		{"入口没有资源引用", "<html></html>", false, prefix, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(tc.page), 0o644); err != nil {
				t.Fatalf("写 index.html 失败：%v", err)
			}
			err := verifyFrontendBase(dir, tc.gateway, tc.prefix)
			if (err != nil) != tc.wantErr {
				t.Fatalf("verifyFrontendBase() err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

func TestHtmlRootRefs(t *testing.T) {
	page := `<script src="/app/x/assets/a.js"></script>
		<link href="relative.css" rel="stylesheet">
		<script src="//cdn.example.com/x.js"></script>
		<img src="/favicon-32.png">`
	got := htmlRootRefs(page)
	want := []string{"/app/x/assets/a.js", "/favicon-32.png"}
	if len(got) != len(want) {
		t.Fatalf("htmlRootRefs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("htmlRootRefs() = %v, want %v", got, want)
		}
	}
}

func TestExtractUpdateAsset(t *testing.T) {
	appTGZ := func(bin []byte) []byte {
		return gzipBytes(t, tarFrom(t, []tarEntry{
			{name: "rental", body: bin, mode: 0o755},
			{name: "ui/index.html", body: []byte(`<script src="/app/techfunway-rental/assets/a.js"></script>`)},
			{name: "ui/assets/a.js", body: []byte("x")},
		}))
	}
	manifest := "appname = techfunway-rental\nversion = 0.7.1\n"

	t.Run("网关形态从 fpk 解出二进制与 ui", func(t *testing.T) {
		setFnOSApp(t, true)
		dir := t.TempDir()
		fpk := writeFakeFPK(t, dir, manifest, appTGZ(fakeBinary()))
		exe := filepath.Join(dir, "rental")

		binPath, webPath, err := extractUpdateAsset(fpk, dir, exe, Info{Latest: "v0.7.1"})
		if err != nil {
			t.Fatalf("extractUpdateAsset() err = %v", err)
		}
		if binPath != filepath.Join(dir, "staging", "rental") {
			t.Fatalf("binPath = %s", binPath)
		}
		if webPath != filepath.Join(dir, "staging", "ui") {
			t.Fatalf("webPath = %s", webPath)
		}
		if err := verifyFrontendBase(webPath, true, "/app/techfunway-rental"); err != nil {
			t.Fatalf("解出的前端基址应当与网关形态一致：%v", err)
		}
	})

	t.Run("网关形态 manifest 版本不一致时报错", func(t *testing.T) {
		setFnOSApp(t, true)
		dir := t.TempDir()
		fpk := writeFakeFPK(t, dir, manifest, appTGZ(fakeBinary()))
		_, _, err := extractUpdateAsset(fpk, dir, filepath.Join(dir, "rental"), Info{Latest: "v0.7.2"})
		if err == nil || !strings.Contains(err.Error(), "不一致") {
			t.Fatalf("err = %v，应当提示版本不一致", err)
		}
	})

	t.Run("直连形态从发行包解出二进制与 www", func(t *testing.T) {
		setFnOSApp(t, false)
		dir := t.TempDir()
		top := "techfunway-rental-v0.7.1-linux-amd64/"
		archive := tarFrom(t, []tarEntry{
			{name: top + "rental", body: fakeBinary(), mode: 0o755},
			{name: top + "www/index.html", body: []byte(`<script src="/assets/a.js"></script>`)},
			{name: top + "www/assets/a.js", body: []byte("x")},
		})
		p := filepath.Join(dir, "techfunway-rental-v0.7.1-linux-amd64.tar.gz")
		if err := os.WriteFile(p, gzipBytes(t, archive), 0o644); err != nil {
			t.Fatalf("写发行包失败：%v", err)
		}

		binPath, webPath, err := extractUpdateAsset(p, dir, filepath.Join(dir, "rental"), Info{Latest: "v0.7.1"})
		if err != nil {
			t.Fatalf("extractUpdateAsset() err = %v", err)
		}
		if binPath != filepath.Join(dir, "staging", "rental") {
			t.Fatalf("binPath = %s", binPath)
		}
		if webPath != filepath.Join(dir, "staging", "www") {
			t.Fatalf("webPath = %s", webPath)
		}
		if err := verifyFrontendBase(webPath, false, "/app/techfunway-rental"); err != nil {
			t.Fatalf("解出的前端基址应当与直连形态一致：%v", err)
		}
	})
}

// TestCheckReportsFnOSInstall 版本检查要把部署形态如实下发给前端：
// 前端据此说明「点更新会装什么」，口径必须与后端选包同源。
func TestCheckReportsFnOSInstall(t *testing.T) {
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := `{"tag_name":"v9.9.9","html_url":"https://example.com","assets":[]}`
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer release.Close()
	overrideSources(t, release.URL, release.URL)

	setFnOSApp(t, true)
	InvalidateCache()
	if got := Check("v0.7.0"); !got.FnOSInstall {
		t.Fatalf("飞牛安装形态下 fnos_install 应为 true：%+v", got)
	}

	setFnOSApp(t, false)
	InvalidateCache()
	if got := Check("v0.7.0"); got.FnOSInstall {
		t.Fatalf("直连形态下 fnos_install 应为 false：%+v", got)
	}
}
