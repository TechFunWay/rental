// apply.go — 一键自更新（管理员，2026-10-11 粉丝需求：应用内检测到新版本后
// 点击即可自动下载、替换、重启，不再依赖群里发文件）。
//
// 流程：选对应当前平台的发行包（release 附件里的 <包名>-<tag>-<goos>-<goarch>.tar.gz，
// 内含 rental 二进制 + www/ 前端）→ 下载到临时目录 → 校验（可执行、格式正确、
// 带 index.html）→ 原子替换自身二进制与前端目录 → syscall.Exec 原地重启
// （进程镜像换成新二进制，不依赖飞牛/Docker 的进程管理拉起）。
//
// 失败安全性（粉丝问的"更新失败会不会影响当前版本"——不会）：
//   - 每一步都先落在临时/暂存文件，任何失败都发生在"动原文件"之前；
//   - 替换用 rename 原子操作，且旧的先改名留底（.bak/.old），新文件放不进去
//     会立刻回滚；唯一不可回滚点是最后的 exec——它失败时旧进程仍在运行，
//     磁盘上已是新版本，下次手动重启应用自然生效；
//   - 数据目录（SQLite/上传/日志）全程不碰，版本降级保护由启动迁移负责。
//
// 平台口径：Linux（飞牛/Docker）与 darwin 支持一键更新；Windows 的 exec 语义
// 不同（无法替换运行中的自身），直接拒绝并给手动下载提示。
package update

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"smallgo/server/config"
	"smallgo/server/response"
	"smallgo/server/version"
)

type applyStage string

const (
	stageIdle        applyStage = "idle"
	stageResolving   applyStage = "resolving"
	stageDownloading applyStage = "downloading"
	stageVerifying   applyStage = "verifying"
	stageReplacing   applyStage = "replacing"
	stageRestarting  applyStage = "restarting"
	stageFailed      applyStage = "failed"
)

// applyJob 全局同一时刻只允许一个更新任务；进程重启（含 exec 成功）后自然清零。
var applyJob struct {
	running atomic.Bool
	stage   atomic.Value // applyStage
	message atomic.Value // string
}

func setStage(s applyStage, msg string) {
	applyJob.stage.Store(s)
	applyJob.message.Store(msg)
}

func applyStatusView() gin.H {
	stage, _ := applyJob.stage.Load().(applyStage)
	msg, _ := applyJob.message.Load().(string)
	if stage == "" {
		stage = stageIdle
	}
	return gin.H{"stage": string(stage), "message": msg, "version": version.Version}
}

// RegisterApplyRoutes 一键更新接口（挂管理员组）。
func RegisterApplyRoutes(admin *gin.RouterGroup) {
	admin.POST("/update/apply", handleApplyStart)
	admin.GET("/update/apply/status", handleApplyStatus)
}

func handleApplyStart(c *gin.Context) {
	if runtime.GOOS == "windows" {
		response.ErrorBadRequest(c, "Windows 部署暂不支持一键更新，请到发行版页面手动下载替换")
		return
	}
	if applyJob.running.Load() {
		response.Success(c, applyStatusView())
		return
	}
	applyJob.running.Store(true)
	setStage(stageResolving, "正在获取最新版本信息…")
	go runApply()
	response.Success(c, applyStatusView())
}

func handleApplyStatus(c *gin.Context) {
	response.Success(c, applyStatusView())
}

// runApply 更新主流程；每个阶段的失败都以 failed 收尾，原进程照常服务。
func runApply() {
	defer func() {
		if r := recover(); r != nil {
			failApply(fmt.Sprintf("更新过程发生异常：%v（当前版本未受影响）", r))
		}
	}()

	// 1. 取最新版本与发行包地址（强制绕过 24h 缓存，以发布页为准）
	setStage(stageResolving, "正在获取最新版本信息…")
	InvalidateCache()
	info := Check(version.Version)
	if !info.HasUpdate {
		failApply("当前已是最新版本，无需更新")
		return
	}
	archive, err := pickArchiveAsset(info, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		failApply(err.Error())
		return
	}

	// 2. 下载
	setStage(stageDownloading, fmt.Sprintf("正在下载 %s…", archive.Name))
	tmpDir, err := os.MkdirTemp("", "rental-update-")
	if err != nil {
		failApply("创建临时目录失败：" + err.Error())
		return
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, archive.Name)
	if err := downloadFile(archive.BrowserDownloadURL, archivePath); err != nil {
		failApply("下载失败（已取消，当前版本不受影响）：" + err.Error())
		return
	}

	// 3. 解包校验
	setStage(stageVerifying, "正在校验更新包…")
	binPath, wwwPath, err := extractPackage(archivePath, tmpDir)
	if err != nil {
		failApply("更新包校验失败（当前版本不受影响）：" + err.Error())
		return
	}

	// 4. 替换自身
	setStage(stageReplacing, "正在替换程序文件…")
	exePath, err := os.Executable()
	if err != nil {
		failApply("定位程序文件失败：" + err.Error())
		return
	}
	exePath, _ = filepath.Abs(exePath)
	if err := replaceBinary(exePath, binPath); err != nil {
		failApply("替换程序失败（当前版本不受影响）：" + err.Error())
		return
	}
	if err := replaceWebDir(wwwPath); err != nil {
		// 二进制已换新但前端未换：回滚二进制，保持前后端版本一致
		_ = restoreBinaryBackup(exePath)
		failApply("替换前端文件失败（已回滚，当前版本不受影响）：" + err.Error())
		return
	}

	// 5. 原地重启：进程镜像换成新二进制，监听端口断开几秒后由新进程接管
	setStage(stageRestarting, "替换完成，正在重启服务…")
	time.Sleep(300 * time.Millisecond) // 让前端把 restarting 状态轮询走
	failMsg := fmt.Sprintf("重启执行失败：%v。当前版本仍在运行，磁盘上已是新版，重启应用后生效", err)
	if err := syscallExec(exePath, os.Args, os.Environ()); err != nil {
		failApply(failMsg)
	}
	// exec 成功则进程镜像已替换，后续代码不会执行
}

func failApply(msg string) {
	setStage(stageFailed, msg)
	applyJob.running.Store(false)
}

// pickArchiveAsset 从发行版附件里挑当前平台的更新包。
// 命名规则（pack-release.sh）：techfunway-rental-<tag>-<goos>-<goarch>.tar.gz。
func pickArchiveAsset(info Info, goos, goarch string) (releaseAsset, error) {
	suffix := fmt.Sprintf("-%s-%s.tar.gz", goos, goarch)
	for _, a := range info.assets {
		if strings.HasSuffix(strings.ToLower(a.Name), suffix) {
			return a, nil
		}
	}
	return releaseAsset{}, fmt.Errorf("发行版 %s 里没有适配 %s/%s 的更新包（.tar.gz），请到发行版页面手动下载", info.Latest, goos, goarch)
}

func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载返回 %d", resp.StatusCode)
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	// 200MB 上限：发行包 ~20MB，超限视为异常直接失败
	n, err := io.Copy(out, io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return err
	}
	if n < 1<<20 {
		return fmt.Errorf("下载内容过小（%d 字节），疑似损坏", n)
	}
	return nil
}

// extractPackage 解开 tar.gz：顶层目录下的 rental 二进制与 www/ 前端。
// 不假定顶层目录名（linux 包是 rental-linux-amd64/，后续可能调整）。
func extractPackage(archivePath, tmpDir string) (binPath, wwwPath string, err error) {
	staging := filepath.Join(tmpDir, "staging")
	f, err := os.Open(archivePath)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", "", fmt.Errorf("不是有效的 tar.gz：%v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		name := filepath.ToSlash(hdr.Name)
		// 去掉顶层目录：rental-linux-amd64/rental → rental
		rel := name
		if i := strings.Index(name, "/"); i >= 0 {
			rel = name[i+1:]
		}
		if rel == "" || hdr.Typeflag != tar.TypeReg {
			continue
		}
		switch {
		case rel == "rental":
			if err := writeFileFromTar(tr, filepath.Join(staging, "rental"), 0o755); err != nil {
				return "", "", err
			}
			binPath = filepath.Join(staging, "rental")
		case strings.HasPrefix(rel, "www/"):
			out := filepath.Join(staging, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return "", "", err
			}
			if err := writeFileFromTar(tr, out, 0o644); err != nil {
				return "", "", err
			}
		}
	}
	if binPath == "" {
		return "", "", fmt.Errorf("包内没有 rental 程序文件")
	}
	wwwPath = filepath.Join(staging, "www")
	if _, err := os.Stat(filepath.Join(wwwPath, "index.html")); err != nil {
		return "", "", fmt.Errorf("包内没有前端文件（www/index.html）")
	}
	if err := sanityCheckBinary(binPath); err != nil {
		return "", "", err
	}
	return binPath, wwwPath, nil
}

func writeFileFromTar(tr *tar.Reader, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, tr)
	return err
}

func sanityCheckBinary(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() < 10<<20 {
		return fmt.Errorf("程序文件异常（%d 字节）", fi.Size())
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		return err
	}
	// ELF（Linux）与 Mach-O 64 位（darwin）两种合法形态
	if string(magic) != "\x7fELF" && string(magic) != "\xcf\xfa\xed\xfe" && string(magic) != "\xfe\xed\xfa\xcf" {
		return fmt.Errorf("不是有效的可执行文件")
	}
	return nil
}

// replaceBinary 原子替换运行中的自身：.new 暂存 → 旧文件留底 .bak → rename。
// 任一步失败都回滚，原文件保持完好。
func replaceBinary(exePath, newBin string) error {
	tmpBin := exePath + ".new"
	bakBin := exePath + ".bak"
	if err := copyFile(newBin, tmpBin); err != nil {
		return fmt.Errorf("写入新程序失败：%v", err)
	}
	if err := os.Chmod(tmpBin, 0o755); err != nil {
		os.Remove(tmpBin)
		return fmt.Errorf("设置执行权限失败：%v", err)
	}
	if err := os.Rename(exePath, bakBin); err != nil {
		os.Remove(tmpBin)
		return fmt.Errorf("留底旧程序失败（多半是目录无写权限）：%v", err)
	}
	if err := os.Rename(tmpBin, exePath); err != nil {
		_ = os.Rename(bakBin, exePath) // 回滚
		os.Remove(tmpBin)
		return fmt.Errorf("替换失败（目录可能无写权限）：%v", err)
	}
	return nil
}

// restoreBinaryBackup 替换前端失败时把二进制退回旧版，保持前后端版本一致。
func restoreBinaryBackup(exePath string) error {
	bakBin := exePath + ".bak"
	if _, err := os.Stat(bakBin); err != nil {
		return err
	}
	return os.Rename(bakBin, exePath)
}

// replaceWebDir 原子替换前端目录：.new 暂存 → 旧目录留底 .old → rename → 删旧。
func replaceWebDir(newWWW string) error {
	webDir := config.C.WebDir
	if webDir == "" {
		return fmt.Errorf("未配置前端目录")
	}
	absOld, err := filepath.Abs(webDir)
	if err != nil {
		return err
	}
	tmpNew := absOld + ".new"
	oldKeep := absOld + ".old"
	_ = os.RemoveAll(tmpNew)
	if err := copyDir(newWWW, tmpNew); err != nil {
		os.RemoveAll(tmpNew)
		return fmt.Errorf("暂存新前端失败：%v", err)
	}
	if _, err := os.Stat(absOld); err == nil {
		_ = os.RemoveAll(oldKeep)
		if err := os.Rename(absOld, oldKeep); err != nil {
			os.RemoveAll(tmpNew)
			return fmt.Errorf("留底旧前端失败（目录可能无写权限）：%v", err)
		}
	}
	if err := os.Rename(tmpNew, absOld); err != nil {
		_ = os.Rename(oldKeep, absOld) // 回滚
		os.RemoveAll(tmpNew)
		return fmt.Errorf("替换前端失败（目录可能无写权限）：%v", err)
	}
	_ = os.RemoveAll(oldKeep)
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		return copyFile(path, out)
	})
}
