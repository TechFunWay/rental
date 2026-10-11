//go:build !windows

package update

import "syscall"

// syscallExec 用新二进制替换当前进程镜像（原地重启，不依赖进程管理器拉起）。
// 成功后本函数不返回。
func syscallExec(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}
