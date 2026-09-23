#!/bin/bash
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

PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
  IFS="/" read -r GOOS GOARCH <<< "$PLATFORM"
  OUTPUT_NAME="${PACKAGE_PREFIX}-v${VERSION}-${GOOS}-${GOARCH}"
  BINARY_NAME="${APP_NAME}-${GOOS}-${GOARCH}"

  echo "Building ${OUTPUT_NAME}..."

  if [ "$GOOS" = "linux" ]; then
    # Use Docker for Linux targets (CGO cross-compilation)
    docker run --rm \
      -v "${ROOT_DIR}/server:/src" \
      -v "go-build-cache:/root/.cache/go-build" \
      -v "go-mod-cache:/go/pkg/mod" \
      -w /src \
      --platform "linux/${GOARCH}" \
      -e "LDFLAGS=${LDFLAGS}" \
      -e "GOARCH=${GOARCH}" \
      -e "APP_NAME=${APP_NAME}" \
      golang:1.26-alpine \
      sh -c 'apk add --no-cache gcc musl-dev && CGO_ENABLED=1 go build -ldflags "$LDFLAGS -extldflags -static" -o "${APP_NAME}-linux-${GOARCH}" .'
  elif [ "$GOOS" = "windows" ]; then
    # Windows 也必须开 CGO：SQLite 驱动是 mattn/go-sqlite3，没有纯 Go 实现。交叉编译
    # 时 CGO_ENABLED 默认是 0，编出来的是官方 stub——程序能启动，一碰数据库就报
    # "go-sqlite3 requires cgo to work"。改用 mingw-w64 编真正的 CGO 二进制。
    if ! command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
      echo "错误：未找到 x86_64-w64-mingw32-gcc，请先安装：brew install mingw-w64"
      exit 1
    fi
    cd server
    CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH CC=x86_64-w64-mingw32-gcc \
      go build -ldflags "${LDFLAGS} -extldflags -static" -o ${BINARY_NAME}.exe .
    cd "$ROOT_DIR"
  else
    cd server
    CGO_ENABLED=1 GOOS=$GOOS GOARCH=$GOARCH go build -ldflags "${LDFLAGS}" -o ${BINARY_NAME} .
    cd "$ROOT_DIR"
  fi

  mkdir -p ${BUILD_DIR}/${OUTPUT_NAME}

  if [ "$GOOS" = "windows" ]; then
    cp server/${BINARY_NAME}.exe ${BUILD_DIR}/${OUTPUT_NAME}/rental.exe
    rm server/${BINARY_NAME}.exe
  else
    cp server/${BINARY_NAME} ${BUILD_DIR}/${OUTPUT_NAME}/rental
    rm server/${BINARY_NAME}
  fi

  cp -r server/static/dist ${BUILD_DIR}/${OUTPUT_NAME}/www

  cd "${ROOT_DIR}/${BUILD_DIR}"
  if [ "$GOOS" = "windows" ]; then
    zip -r ${OUTPUT_NAME}.zip ${OUTPUT_NAME}
  else
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
