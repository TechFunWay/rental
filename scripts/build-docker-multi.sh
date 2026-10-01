#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

APP_NAME="techfunway-rental"
IMAGE_NAME="techfunways/rental"
BUILDER_NAME="${BUILDER_NAME:-techfunway-rental-multiarch}"
VERSION="$(cat VERSION | tr -d '\n')"
VERSION="${VERSION#v}"
RELEASE_DIR="${ROOT_DIR}/release/v${VERSION}"
OCI_FILE="${RELEASE_DIR}/${APP_NAME}-v${VERSION}-multiarch.oci.tar"

if ! command -v docker >/dev/null 2>&1; then
  echo "Error: Docker is required to build the multi-platform image."
  exit 1
fi

if [ ! -d "$RELEASE_DIR" ]; then
  echo "Missing ${RELEASE_DIR}. Build release archives first: make build-all"
  exit 1
fi

# build-all 压缩后即清理中间目录；多平台构建需要 linux 平台目录，
# 缺失时从对应的 tar.gz 解包，用完在脚本末尾清理。
DOCKER_TMP_DIRS=()
for arch in amd64 arm64; do
  DIR_NAME="${APP_NAME}-v${VERSION}-linux-${arch}"
  if [ ! -d "${RELEASE_DIR}/${DIR_NAME}" ]; then
    TAR_FILE="${RELEASE_DIR}/${DIR_NAME}.tar.gz"
    if [ ! -f "${TAR_FILE}" ]; then
      echo "Missing ${TAR_FILE}. Run: bash scripts/build-all.sh"
      exit 1
    fi
    tar -xzf "${TAR_FILE}" -C "${RELEASE_DIR}"
    DOCKER_TMP_DIRS+=("${RELEASE_DIR}/${DIR_NAME}")
  fi
done

find "${RELEASE_DIR}" -name '.DS_Store' -delete 2>/dev/null || true
rm -f "${OCI_FILE}"

# 合并 manifest 的 OCI 归档必须用 docker-container 驱动的 builder，
# 默认驱动不推 registry 导不出多平台合并结果。
if ! docker buildx inspect "${BUILDER_NAME}" >/dev/null 2>&1; then
  docker buildx create --name "${BUILDER_NAME}" --driver docker-container --bootstrap >/dev/null
fi

echo "Building ${IMAGE_NAME}:v${VERSION} for linux/amd64,linux/arm64..."
echo "Writing local OCI image archive (no registry push): ${OCI_FILE}"

CONTEXT_DIR="${ROOT_DIR}/build/docker-multi-context"
rm -rf "${CONTEXT_DIR}"
mkdir -p "${CONTEXT_DIR}"
trap 'rm -rf "${CONTEXT_DIR}"; for d in "${DOCKER_TMP_DIRS[@]:-}"; do [ -n "$d" ] && rm -rf "$d"; done' EXIT

# 用已编译好的双架构二进制 + FROM scratch 组镜像，避免 buildx 再拉基础镜像。
cat > "${CONTEXT_DIR}/Dockerfile" <<'EOF'
FROM scratch

ARG TARGETARCH
ARG VERSION

LABEL org.opencontainers.image.title="techfunway-rental"
LABEL org.opencontainers.image.description="面向房东、公寓与宿舍管理者的租房管理系统"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.source="https://github.com/TechFunWay/rental"

WORKDIR /app
COPY techfunway-rental-v${VERSION}-linux-${TARGETARCH}/rental /app/rental
COPY techfunway-rental-v${VERSION}-linux-${TARGETARCH}/www /app/static/dist

EXPOSE 8910
VOLUME ["/app/data"]

ENV PORT=8910
ENV DATA_DIR=/app/data

ENTRYPOINT ["/app/rental"]
CMD ["-data-dir", "/app/data", "-web-dir", "./static/dist", "-device-type", "docker"]
EOF

# ARG VERSION 需要在 COPY 路径展开前注入到上下文目录名（buildkit 支持 ${VERSION} 在 COPY 源路径）
# 上面的 Dockerfile 在 COPY 中使用 ${VERSION}，build-arg 会在该行生效。

for arch in amd64 arm64; do
  cp -a "${RELEASE_DIR}/${APP_NAME}-v${VERSION}-linux-${arch}" "${CONTEXT_DIR}/"
done

docker buildx build \
  --builder "${BUILDER_NAME}" \
  --platform linux/amd64,linux/arm64 \
  --build-arg "VERSION=${VERSION}" \
  -t "${IMAGE_NAME}:v${VERSION}" \
  -t "${IMAGE_NAME}:${VERSION}" \
  -t "${IMAGE_NAME}:latest" \
  --output "type=oci,dest=${OCI_FILE}" \
  "${CONTEXT_DIR}"

echo "Multi-platform OCI image archive completed: ${OCI_FILE}"
echo "Load it on a target with: docker load -i ${OCI_FILE}"
