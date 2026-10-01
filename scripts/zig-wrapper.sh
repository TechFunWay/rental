#!/bin/bash
# zig wrapper：记录参数后透传（用于定位 CGO darwin 构建失败的真实命令）
echo "$(date +%T) cwd=$(pwd) args: $@" >> /tmp/zig-calls.log
# flock 串行化：zig 共享缓存在并发首次创建时竞争会报 AccessDenied
exec flock /tmp/zig-serial.lock $HOME/tools/zig/zig cc "$@"
