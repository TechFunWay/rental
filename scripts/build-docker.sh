#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

IMAGE="techfunways/rental"
VERSION="$(cat VERSION | tr -d '\n')"
VERSION="${VERSION#v}"
BUILD_TIME="$(date +%Y-%m-%dT%H:%M:%S)"
GIT_COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")"

echo "Building local Docker image (this host architecture only)..."
echo "For PC(x86)+手机/ARM 合并镜像请用: make build-docker-multi"
docker build \
  --build-arg "VERSION=${VERSION}" \
  --build-arg "BUILD_TIME=${BUILD_TIME}" \
  --build-arg "GIT_COMMIT=${GIT_COMMIT}" \
  -t "${IMAGE}:v${VERSION}" \
  -t "${IMAGE}:${VERSION}" \
  -t "${IMAGE}:latest" \
  .

echo "Docker image built: ${IMAGE}:v${VERSION}"
