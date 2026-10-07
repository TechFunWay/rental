#!/bin/bash
set -e

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

APP_NAME=$(python3 -c "import json; print(json.load(open('app.json'))['appname'])")
FNOS_PKG_NAME=$(awk -F'=' '/^appname/ {gsub(/^[ \t]+|[ \t]+$/, "", $2); print $2}' fnpack/manifest)
VERSION=$(cat VERSION | tr -d '\n')
VERSION="${VERSION#v}"
BUILD_TIME=$(date +%Y-%m-%dT%H:%M:%S)
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS="-X smallgo/server/version.Version=${VERSION} -X smallgo/server/version.BuildTime=${BUILD_TIME} -X smallgo/server/version.GitCommit=${GIT_COMMIT} -X smallgo/server/version.AppName=${APP_NAME}"

# Verify Docker is available (required for CGO cross-compilation)
if ! command -v docker &>/dev/null; then
  echo "Error: Docker is required for fnOS package builds (CGO cross-compilation)."
  echo "Install Docker Desktop and try again."
  exit 1
fi

echo "Building frontend..."
VITE_FNOS_APP=true VITE_BASE_PATH="/app/${FNOS_PKG_NAME}/" npm --prefix web ci
VITE_FNOS_APP=true VITE_BASE_PATH="/app/${FNOS_PKG_NAME}/" npm --prefix web run build

echo "Copying frontend..."
rm -rf server/static/dist
cp -r web/dist server/static/dist

BUILD_DIR="release/v${VERSION}"
mkdir -p ${BUILD_DIR}

# 发行目录自包含 README 截图与更新日志，便于随包分发。
if [ -d docs/screenshots ]; then
  mkdir -p "${BUILD_DIR}/screenshots"
  cp docs/screenshots/*.png "${BUILD_DIR}/screenshots/"
  echo "Copied screenshots"
fi
if [ -f CHANGELOG.md ]; then
  cp CHANGELOG.md "${BUILD_DIR}/CHANGELOG.md"
fi

# Save original manifest
cp fnpack/manifest fnpack/manifest.bak

for ARCH in "amd64" "arm64"; do
  # 产物文件名架构标 x86（飞牛生态叫法），不用 amd64；arm64 保持不变
  PKG_ARCH="${ARCH}"
  if [ "$ARCH" = "amd64" ]; then
    PKG_ARCH="x86"
  fi
  echo "Building fnOS package for ${ARCH}..."

  # 用 zig cc 在本机交叉编译 linux/${ARCH} 静态二进制：容器路线要在 alpine 里
  # apk 装 gcc 并拉 Go 模块，实测单次 apk update 3 分 37 秒、两架构各卡十几分钟。
  # zig 路径可由 ZIG 环境变量覆盖，默认在仓库同级 tmp/tools 下找解压目录。
  if [ -z "${ZIG:-}" ]; then
    for cand in "$ROOT_DIR"/../tmp/tools/zig-*/zig "$HOME/tools/zig/zig"; do
      if [ -x "$cand" ]; then ZIG="$cand"; break; fi
    done
  fi
  if [ -z "${ZIG:-}" ] || [ ! -x "$ZIG" ]; then
    echo "错误：未找到 zig（可设 ZIG=/path/to/zig，或在 $ROOT_DIR/../tmp/tools/ 下放 zig-* 目录）"
    exit 1
  fi
  export ZIG_GLOBAL_CACHE_DIR="${ZIG_GLOBAL_CACHE_DIR:-$ROOT_DIR/../tmp/tools/zig-cache}"
  export ZIG_LOCAL_CACHE_DIR="${ZIG_LOCAL_CACHE_DIR:-$ROOT_DIR/../tmp/tools/zig-cache-local}"
  mkdir -p "$ZIG_GLOBAL_CACHE_DIR" "$ZIG_LOCAL_CACHE_DIR"

  if [ "$ARCH" = "amd64" ]; then
    ZIG_TARGET="x86_64-linux-musl"
  else
    ZIG_TARGET="aarch64-linux-musl"
  fi

  echo "  Compiling Go binary with zig cc (CGO_ENABLED=1, linux/${ARCH}, target ${ZIG_TARGET})..."
  cd "$ROOT_DIR/server"
  CGO_ENABLED=1 GOOS=linux GOARCH="$ARCH" CC="$ZIG cc -target $ZIG_TARGET" \
    go build -ldflags "${LDFLAGS} -extldflags -static" -o "rental-linux-${ARCH}" .
  cd "$ROOT_DIR"

  # Prepare build directory
  BUILD_PACK="${BUILD_DIR}/${APP_NAME}_${PKG_ARCH}"
  rm -rf "${BUILD_PACK}"
  mkdir -p "${BUILD_PACK}"

  # Copy fnpack template (only essential directories)
  cp -r fnpack/cmd "${BUILD_PACK}/"
  cp -r fnpack/config "${BUILD_PACK}/"
  cp -r fnpack/wizard "${BUILD_PACK}/"
  mkdir -p "${BUILD_PACK}/app"
  cp -r fnpack/app/ui "${BUILD_PACK}/app/"
  cp fnpack/ICON.PNG "${BUILD_PACK}/"
  cp fnpack/ICON_256.PNG "${BUILD_PACK}/"

  # Copy binary
  cp server/rental-linux-${ARCH} "${BUILD_PACK}/app/rental"
  chmod +x "${BUILD_PACK}/app/rental"
  rm server/rental-linux-${ARCH}

  # Copy frontend to app/ui
  cp -r server/static/dist/* "${BUILD_PACK}/app/ui/"

  # Generate manifest with correct platform
  if [ "$ARCH" = "amd64" ]; then
    FNOS_PLATFORM="x86"
  else
    FNOS_PLATFORM="arm"
  fi
  sed "s/^platform.*/platform              = ${FNOS_PLATFORM}/" fnpack/manifest > "${BUILD_PACK}/manifest"

  # Update version in manifest
  sed -i '' "s/^version.*/version               = ${VERSION#v}/" "${BUILD_PACK}/manifest" 2>/dev/null || \
  sed -i "s/^version.*/version               = ${VERSION#v}/" "${BUILD_PACK}/manifest"

  # Strip macOS metadata so it never ships inside the package
  find "${BUILD_PACK}" -name '.DS_Store' -delete

  # Build with fnpack
  cd "${BUILD_PACK}"
  fnpack build
  cd "$ROOT_DIR"

  # Move the built fpk to release directory（文件名带版本号，与 bill/reminders 一致）
  if [ -f "${BUILD_PACK}/${FNOS_PKG_NAME}.fpk" ]; then
    mv "${BUILD_PACK}/${FNOS_PKG_NAME}.fpk" "${BUILD_DIR}/${FNOS_PKG_NAME}_v${VERSION}_${PKG_ARCH}.fpk"
  elif [ -f "${BUILD_PACK}/../${FNOS_PKG_NAME}_${ARCH}.fpk" ]; then
    mv "${BUILD_PACK}/../${FNOS_PKG_NAME}_${ARCH}.fpk" "${BUILD_DIR}/${FNOS_PKG_NAME}_v${VERSION}_${PKG_ARCH}.fpk"
  fi

  # Clean up
  rm -rf "${BUILD_PACK}"

  echo "Built ${FNOS_PKG_NAME}_v${VERSION}_${PKG_ARCH}.fpk"
done

# Restore original manifest
mv fnpack/manifest.bak fnpack/manifest

find "${BUILD_DIR}" -name '.DS_Store' -delete

echo "fnOS packages completed in ${BUILD_DIR}/"
