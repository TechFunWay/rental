// fpk.go — 飞牛安装包（.fpk）解析：飞牛网关形态的一键更新装的是它，
// 而不是发行包 tar.gz（2026-10-19 修复「点更新后页面空白」）。
//
// 为什么要按形态分包：前端有两次构建，资源基址不同——
//   - 飞牛 fpk 的 ui/：VITE_BASE_PATH=/app/techfunway-rental/，入口 HTML 引用
//     /app/techfunway-rental/assets/...，只能挂在飞牛统一网关前缀下；
//   - 发行包 tar.gz 与 Docker 镜像的 www/：base=/，入口 HTML 引用 /assets/...。
//
// 网关形态（-fnos-app）下飞牛接入层只把 /app/techfunway-rental/* 转发给应用，
// 一旦装成根基址前端，浏览器请求 /assets/... 转发不到应用 → 资源 404 → 白屏
// （用户现象：点完更新打开一片空白，手工装 fpk 才正常）。
// 所以网关形态必须装 fpk（自带网关基址 ui/），其它形态装发行包 www/。
//
// fpk 结构（见 scripts/build-fnpack.sh 与 fnpack 产物）：外层是 tar.gz，内含
// manifest 与 app.tgz；app.tgz 再是一层 tar.gz，内含可执行文件与 ui/。
// 自更新只取运行所需的两样，其余（cmd/、config/、wizard/ 等）仍以安装包为准。
package update

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// 安装包内的固定布局，改动要与 scripts/build-fnpack.sh 同步。
const (
	fpkManifestName = "manifest"
	fpkInnerArchive = "app.tgz"
	fpkUIDir        = "ui"
	// fpkAppName 是飞牛包名（fnpack/manifest 的 appname），用来防止装错包。
	fpkAppName = "techfunway-rental"
)

// fpkManifest 是 fpk 顶层 manifest 里自更新关心的字段。
type fpkManifest struct {
	Appname  string
	Version  string
	Platform string
}

// fpkPackage 是解包结果：可执行文件与网关基址前端目录。
type fpkPackage struct {
	Manifest fpkManifest
	BinPath  string
	WebPath  string
}

// parseFPKManifest 解析 manifest 的 key = value 行。
func parseFPKManifest(r io.Reader) (fpkManifest, error) {
	var m fpkManifest
	data, err := io.ReadAll(io.LimitReader(r, 64<<10))
	if err != nil {
		return m, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "appname":
			m.Appname = strings.TrimSpace(value)
		case "version":
			m.Version = strings.TrimSpace(value)
		case "platform":
			m.Platform = strings.TrimSpace(value)
		}
	}
	return m, nil
}

// extractFPK 打开 fpk，把内层 app.tgz 里的可执行文件与 ui/ 展开到 stageDir。
// 可执行文件统一落成 exeBase 这个名字，方便上层直接替换当前程序。
func extractFPK(fpkPath, stageDir, exeBase string) (fpkPackage, error) {
	var pkg fpkPackage
	f, err := os.Open(fpkPath)
	if err != nil {
		return pkg, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return pkg, fmt.Errorf("飞牛安装包不是有效的 gzip 包：%v", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var appSeen bool
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return pkg, fmt.Errorf("读取飞牛安装包失败：%v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		switch path.Clean(filepath.ToSlash(hdr.Name)) {
		case fpkManifestName:
			m, err := parseFPKManifest(tr)
			if err != nil {
				return pkg, fmt.Errorf("飞牛安装包 manifest 解析失败：%v", err)
			}
			pkg.Manifest = m
		case fpkInnerArchive:
			appSeen = true
			binPath, webPath, err := extractAppTGZ(tr, stageDir, exeBase)
			if err != nil {
				return pkg, err
			}
			pkg.BinPath, pkg.WebPath = binPath, webPath
		}
	}
	if !appSeen {
		return pkg, fmt.Errorf("飞牛安装包里没有 %s", fpkInnerArchive)
	}
	if pkg.BinPath == "" {
		return pkg, fmt.Errorf("飞牛安装包里没有可执行文件（期望 %s）", exeBase)
	}
	if _, err := os.Stat(filepath.Join(pkg.WebPath, "index.html")); err != nil {
		return pkg, fmt.Errorf("飞牛安装包里没有前端入口（%s/index.html）", fpkUIDir)
	}
	return pkg, nil
}

// extractAppTGZ 从 app.tgz 的 tar 流中取出可执行文件与 ui/，写入 stageDir。
// 只处理普通文件与目录；拒绝越界路径（安装包来自网络，不可信）。
func extractAppTGZ(r io.Reader, stageDir, exeBase string) (binPath, webPath string, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", "", fmt.Errorf("app.tgz 不是有效的 gzip 包：%v", err)
	}
	defer gz.Close()

	webRoot := filepath.Join(stageDir, fpkUIDir)
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("读取 app.tgz 失败：%v", err)
		}
		name := path.Clean(filepath.ToSlash(hdr.Name))
		if name == "." {
			continue
		}
		if strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
			return "", "", fmt.Errorf("安装包内出现越界路径 %q", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			// 目录与软链不参与自更新：目录在写文件时按需创建。
			continue
		}
		switch {
		case isPKGBinary(name, exeBase):
			binPath = filepath.Join(stageDir, exeBase)
			if err := writeFileFromTar(tr, binPath, 0o755); err != nil {
				return "", "", err
			}
		case strings.HasPrefix(name, fpkUIDir+"/"):
			rel := strings.TrimPrefix(name, fpkUIDir+"/")
			out := filepath.Join(webRoot, filepath.FromSlash(rel))
			if err := writeFileFromTar(tr, out, 0o644); err != nil {
				return "", "", err
			}
		}
	}
	webPath = webRoot
	return binPath, webPath, nil
}

// isPKGBinary 判断 app.tgz 的顶层条目是不是要装的可执行文件。
// 优先认当前运行的程序名；飞牛安装包里固定叫 rental，作为兜底，这样即便
// 以后可执行文件改名，也不会因为名字对不上而整包作废。
func isPKGBinary(name, exeBase string) bool {
	if strings.Contains(name, "/") {
		return false
	}
	if exeBase != "" && name == exeBase {
		return true
	}
	return name == "rental"
}

// checkFPKManifest 用安装包自带 manifest 核对发行版标签，防止下载到陈旧或
// 串号的包（渠道里可能残留旧附件）。manifest 缺字段时跳过对应校验。
func checkFPKManifest(m fpkManifest, latestTag string) error {
	if m.Appname != "" && m.Appname != fpkAppName {
		return fmt.Errorf("飞牛安装包的包名是 %s，不是 %s，疑似下错了包", m.Appname, fpkAppName)
	}
	if m.Version == "" {
		return nil
	}
	if got, want := ParseVersion(m.Version), ParseVersion(latestTag); got != want {
		return fmt.Errorf("飞牛安装包版本 %s 与发行版 %s 不一致，疑似下到了旧包，请到发行版页面手动下载", m.Version, latestTag)
	}
	return nil
}
