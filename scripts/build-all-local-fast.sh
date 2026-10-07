#!/bin/bash
# 全平台二进制构建（本机直编，不依赖 Docker 容器）
#
# 背景：原 scripts/build-all.sh 用 golang:1.26-alpine 容器编译，容器内 apk 装 gcc
# 与拉 Go 模块都要走代理，单次 apk update 实测 3 分 37 秒，Linux 两个架构各卡十几分钟。
# 本脚本改为在本机直接编译，全部产物 1 分钟内出齐：
#   darwin/amd64、darwin/arm64 → 本机 clang（原生）
#   linux/amd64、linux/arm64   → zig cc + musl 静态（CGO 交叉编译）
#   windows/amd64              → zig cc + windows-gnu（CGO，避免出静默坏包）
#
# 依赖：zig（默认从 tmp/tools 下找，可用 ZIG 环境变量覆盖）。
set -e

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

APP_NAME="rental"
PACKAGE_PREFIX="techfunway-rental"
VERSION=$(cat VERSION | tr -d '\n')
VERSION="${VERSION#v}"
BUILD_TIME=$(date +%Y-%m-%dT%H:%M:%S)
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS="-X smallgo/server/version.Version=${VERSION} -X smallgo/server/version.BuildTime=${BUILD_TIME} -X smallgo/server/version.GitCommit=${GIT_COMMIT} -X smallgo/server/version.AppName=${APP_NAME}"

# zig 位置：优先环境变量，其次仓库同级 tmp/tools 下的解压目录
ZIG="${ZIG:-}"
if [ -z "$ZIG" ]; then
  for cand in "$ROOT_DIR"/../tmp/tools/zig-*/zig "$HOME/tools/zig/zig"; do
    if [ -x "$cand" ]; then ZIG="$cand"; break; fi
  done
fi

# zig 缓存必须放可写且非网络盘的目录，否则报 unable to create compilation: AccessDenied
if [ -n "$ZIG" ]; then
  export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$ROOT_DIR/../tmp/tools/zig-cache}"
  export ZIG_LOCAL_CACHE_DIR="${ZIG_LOCAL_CACHE_DIR:-$ROOT_DIR/../tmp/tools/zig-cache-local}"
  mkdir -p "$ZIG_GLOBAL_CACHE_DIR" "$ZIG_LOCAL_CACHE_DIR"
fi

if [ -z "$ZIG" ] || [ ! -x "$ZIG" ]; then
  echo "错误：未找到 zig（可设 ZIG=/path/to/zig，或在 $ROOT_DIR/../tmp/tools/ 下放 zig-* 目录）"
  exit 1
fi
echo "使用 zig：$ZIG（$("$ZIG" version)）"

echo "Building frontend..."
cd web && npm ci && npm run build && cd ..

echo "Copying frontend..."
rm -rf server/static/dist
cp -r web/dist server/static/dist

BUILD_DIR="release/v${VERSION}"
rm -rf ${BUILD_DIR}
mkdir -p ${BUILD_DIR}

# 发行目录自包含 README 截图（真实来源仍以 docs/screenshots/ 为准）。
if [ -d docs/screenshots ]; then
  mkdir -p "${BUILD_DIR}/screenshots"
  cp docs/screenshots/*.png "${BUILD_DIR}/screenshots/"
  (cd "${BUILD_DIR}" && zip -qr "screenshots-v${VERSION}.zip" screenshots)
  echo "Copied screenshots and screenshots-v${VERSION}.zip"
fi

if [ -f CHANGELOG.md ]; then
  cp CHANGELOG.md "${BUILD_DIR}/CHANGELOG.md"
fi

# 平台 → CGO 交叉编译器组合
PLATFORMS=(
  "linux/amd64 zig-musl"
  "linux/arm64 zig-musl"
  "darwin/amd64 native"
  "darwin/arm64 native"
  "windows/amd64 zig-mingw"
)

for ENTRY in "${PLATFORMS[@]}"; do
  read -r PLATFORM TOOLCHAIN <<< "$ENTRY"
  IFS="/" read -r GOOS GOARCH <<< "$PLATFORM"
  OUTPUT_NAME="${PACKAGE_PREFIX}-v${VERSION}-${GOOS}-${GOARCH}"
  BINARY_NAME="${APP_NAME}-${GOOS}-${GOARCH}"

  echo "Building ${OUTPUT_NAME}..."

  EXTRA_LDFLAGS=""
  case "$TOOLCHAIN" in
    zig-musl)
      # Linux：musl 静态链接，不依赖目标机 glibc 版本
      case "$GOARCH" in
        amd64) ZIG_TARGET="x86_64-linux-musl" ;;
        arm64) ZIG_TARGET="aarch64-linux-musl" ;;
      esac
      CC_CMD="$ZIG cc -target $ZIG_TARGET"
      EXTRA_LDFLAGS=" -extldflags -static"
      ;;
    zig-mingw)
      # Windows 必须开 CGO：SQLite 驱动是 mattn/go-sqlite3，没有纯 Go 实现
      CC_CMD="$ZIG cc -target x86_64-windows-gnu"
      EXTRA_LDFLAGS=" -extldflags -static"
      ;;
    native)
      CC_CMD=""   # 交给本机 clang
      ;;
  esac

  cd server
  if [ -n "$CC_CMD" ]; then
    CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH CC="$CC_CMD" \
      go build -ldflags "${LDFLAGS}${EXTRA_LDFLAGS}" -o ${BINARY_NAME} .
  else
    CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH \
      go build -ldflags "${LDFLAGS}" -o ${BINARY_NAME} .
  fi
  cd "$ROOT_DIR"

  mkdir -p ${BUILD_DIR}/${OUTPUT_NAME}

  if [ "$GOOS" = "windows" ]; then
    cp server/${BINARY_NAME} ${BUILD_DIR}/${OUTPUT_NAME}/rental.exe
    rm server/${BINARY_NAME}
  else
    cp server/${BINARY_NAME} ${BUILD_DIR}/${OUTPUT_NAME}/rental
    rm server/${BINARY_NAME}
  fi

  cp -r server/static/dist ${BUILD_DIR}/${OUTPUT_NAME}/www

  cd "${ROOT_DIR}/${BUILD_DIR}"
  if [ "$GOOS" = "windows" ]; then
    zip -qr ${OUTPUT_NAME}.zip ${OUTPUT_NAME}
  else
    # COPYFILE_DISABLE=1：darwin 下不加会把 ._* 扩展属性文件打进 tar
    COPYFILE_DISABLE=1 tar czf ${OUTPUT_NAME}.tar.gz ${OUTPUT_NAME}
  fi
  cd "$ROOT_DIR"

  rm -rf ${BUILD_DIR}/${OUTPUT_NAME}

  echo "Built ${OUTPUT_NAME}"
done

# docker-compose：版本镜像 tag 钉死本版本；latest 文件原样复制。
if [ -d deploy ]; then
  sed "s|techfunways/rental:latest|techfunways/rental:v${VERSION}|" deploy/docker-compose.yml > "${BUILD_DIR}/docker-compose.yml"
  cp deploy/docker-compose.latest.yml "${BUILD_DIR}/docker-compose.latest.yml"
  if [ -f deploy/docker-compose.version.yml ]; then
    cp deploy/docker-compose.version.yml "${BUILD_DIR}/docker-compose.version.yml"
  fi
  echo "Copied docker-compose files"
fi

find "${BUILD_DIR}" -name '.DS_Store' -delete

echo "All builds completed in ${BUILD_DIR}/"
