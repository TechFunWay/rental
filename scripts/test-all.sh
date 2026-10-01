#!/bin/bash
# 全量后端测试（在 WSL 内执行，无 Windows 文件锁干扰）
export PATH=$HOME/gotip/go/bin:$PATH
export GOPATH=$HOME/go GOPROXY=https://goproxy.cn,direct
cd /mnt/f/workspace/techfunway/gitea/rental/server
CGO_ENABLED=1 go test ./... 2>&1 | grep -vE "^ok|no test files"
echo "==== 汇总 ===="
CGO_ENABLED=1 go test ./... 2>&1 | grep -cE "^ok"
