package version

import "time"

// startedAt 进程启动时间：前端用它区分「本次运行」与「重启后」，
// 例如赞赏提示弹窗在应用重启后才会再次出现。
var startedAt = time.Now()

var (
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
	AppName   = "rental"
)

func GetVersion() map[string]string {
	return map[string]string{
		"version":   Version,
		"buildTime": BuildTime,
		"gitCommit": GitCommit,
		"appName":   AppName,
		"startedAt": startedAt.Format(time.RFC3339),
	}
}

func PrintVersion() {
	println(AppName, Version)
	println("Version:", Version)
	println("BuildTime:", BuildTime)
	println("GitCommit:", GitCommit)
}
