//go:build windows

package update

import "fmt"

// syscallExec Windows 上无法替换运行中的自身进程镜像；
// 一键更新入口在 Windows 部署下早已拒绝，这里只是编译兜底。
func syscallExec(path string, argv []string, env []string) error {
	return fmt.Errorf("windows 不支持原地重启，请手动停止应用后替换文件再启动")
}
