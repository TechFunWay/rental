#!/bin/bash
# 全平台二进制构建（在 WSL 内执行）：
#   linux/amd64（原生 gcc，静态 musl 用 zig）、linux/arm64、darwin/amd64、darwin/arm64（zig cc 交叉 CGO）
# Windows amd64 由本机 MinGW 构建后放置 server/rental-windows-amd64.exe。
set -e
ROOT=/mnt/f/workspace/techfunway/gitea/rental
SERVER=$ROOT/server
export PATH=$HOME/gotip/go/bin:$HOME/tools/zig:$HOME/bin:$PATH
export GOPATH=$HOME/go GOPROXY=https://goproxy.cn,direct
# zig 缓存必须放 WSL 原生文件系统（/mnt 下 9p 不支持 mmap 锁，会 AccessDenied）
export ZIG_GLOBAL_CACHE_DIR=$HOME/.zig-cache ZIG_LOCAL_CACHE_DIR=$HOME/.zig-cache-local
VER=$(cat $ROOT/VERSION | tr -d 'v\n')
BUILD_TIME=$(date +%Y-%m-%dT%H:%M:%S)
COMMIT=$(git -C $ROOT rev-parse --short HEAD 2>/dev/null || cut -c1-7 $ROOT/.git/refs/heads/main 2>/dev/null || echo unknown)
LDFLAGS_BASE="-X smallgo/server/version.Version=${VER} -X smallgo/server/version.BuildTime=${BUILD_TIME} -X smallgo/server/version.GitCommit=${COMMIT} -X smallgo/server/version.AppName=rental"

cd "$SERVER"

echo "== linux/amd64（原生 gcc，静态）=="
CGO_ENABLED=1 go build -ldflags "$LDFLAGS_BASE -extldflags -static" -o rental-linux-amd64 . && echo OK

echo "== linux/arm64（zig cc + musl，静态）=="
CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
  CC="zig cc -target aarch64-linux-musl" \
  go build -ldflags "$LDFLAGS_BASE -extldflags -static" -o rental-linux-arm64 . && echo OK

# darwin 目标在 /mnt/f（9p）上构建会 AccessDenied：把源码复制到 WSL 原生目录构建
# 另外 go 模块缓存目录是只读的（cgo 会 cd 进去编译），zig 需要可写 cwd：
# darwin 构建使用独立可写 GOMODCACHE，依赖重新拉取。
DARWIN_BUILD=$HOME/darwin-build
rm -rf "$DARWIN_BUILD"
mkdir -p "$DARWIN_BUILD"
# 整模块复制（排除产物与静态资源），模块根与子包都必须在
tar -C "$SERVER" --exclude='rental-*' --exclude='static' --exclude='rental.exe' -cf - . | tar -C "$DARWIN_BUILD" -xf -

build_darwin() {
  local GOARCH=$1 OUT=$2 TARGET=$3
  echo "== darwin/$GOARCH（zig cc，原生目录构建）=="
  cd "$DARWIN_BUILD"
  CGO_ENABLED=1 GOOS=darwin GOARCH=$GOARCH \
    GOMODCACHE=$HOME/gomodcache-rw \
    CC="$ROOT/scripts/zig-wrapper.sh -target $TARGET" \
    go build -p 1 -ldflags "$LDFLAGS_BASE" -o "$SERVER/$OUT" . && echo OK
  cd "$SERVER"
}

build_darwin amd64 rental-darwin-amd64 x86_64-macos
build_darwin arm64 rental-darwin-arm64 aarch64-macos

echo "== windows/amd64（检查本机构建产物）=="
[ -f rental-windows-amd64.exe ] && echo "exists: $(du -h rental-windows-amd64.exe | cut -f1)" || { echo "MISSING"; exit 1; }

echo "== file 类型检查 =="
for f in rental-linux-amd64 rental-linux-arm64 rental-darwin-amd64 rental-darwin-arm64; do
  printf "%s: " "$f"; head -c 20 "$f" | od -An -tx1 | tr -d ' \n' | cut -c1-16; echo
done
echo "ALL-BUILDS-OK"
