APP_NAME := rental
VERSION := $(shell cat VERSION | tr -d '\n')
BUILD_TIME := $(shell date +%Y-%m-%dT%H:%M:%S)
GIT_COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS := -X smallgo/server/version.Version=$(VERSION) -X smallgo/server/version.BuildTime=$(BUILD_TIME) -X smallgo/server/version.GitCommit=$(GIT_COMMIT) -X smallgo/server/version.AppName=$(APP_NAME)

.PHONY: help dev start build build-frontend build-backend build-linux build-docker build-docker-multi build-all fnpack clean

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@echo "  help                 Show this help"
	@echo "  dev                  Build to dev/ directory and start (simulates production)"
	@echo "  start                Start the server directly"
	@echo "  build                Build frontend + backend"
	@echo "  build-frontend       Build frontend only"
	@echo "  build-backend        Build backend only"
	@echo "  build-linux          Build for linux/amd64"
	@echo "  build-docker         Build local single-arch Docker image"
	@echo "  build-docker-multi   Build amd64+arm64 merged local OCI archive (PC + 手机/ARM)"
	@echo "  build-all            Build for all platforms + screenshots/compose into release/"
	@echo "  fnpack               Build fnOS packages (with screenshots)"
	@echo "  clean                Clean build artifacts"

dev:
	@echo "Cleaning dev directory (preserving data)..."
	@rm -rf dev/static dev/$(APP_NAME)
	@mkdir -p dev/static/dist dev/data
	@echo "Building frontend..."
	cd web && npm ci && npm run build
	@echo "Copying frontend to dev/static/dist..."
	cp -r web/dist/* dev/static/dist/
	@echo "Building backend..."
	cd server && go build -ldflags "$(LDFLAGS)" -o ../dev/$(APP_NAME) .
	@echo "Starting server..."
	cd dev && ./$(APP_NAME) -data-dir=./data -web-dir=./static/dist

start:
	cd server && go run -ldflags "$(LDFLAGS)" .

build: build-frontend build-backend

build-frontend:
	cd web && npm ci && npm run build
	rm -rf server/static/dist
	mkdir -p server/static
	cp -r web/dist server/static/dist

build-backend:
	cd server && go build -ldflags "$(LDFLAGS)" -o $(APP_NAME) .

build-linux:
	docker run --rm \
		-v "$(CURDIR)/server:/src" \
		-v "go-build-cache:/root/.cache/go-build" \
		-v "go-mod-cache:/go/pkg/mod" \
		-w /src \
		--platform linux/amd64 \
		-e "LDFLAGS=$(LDFLAGS)" \
		golang:1.26-alpine \
		sh -c 'apk add --no-cache gcc musl-dev && CGO_ENABLED=1 go build -ldflags "$$LDFLAGS -extldflags -static" -o smallgo-linux-amd64 .'

build-docker:
	bash scripts/build-docker.sh

build-docker-multi:
	bash scripts/build-docker-multi.sh

build-all:
	bash scripts/build-all.sh

fnpack:
	bash scripts/build-fnpack.sh

clean:
	rm -f server/$(APP_NAME) server/$(APP_NAME)-*
	rm -rf server/static/dist
	rm -rf web/dist
	rm -rf build/
	rm -rf dev/static dev/$(APP_NAME)
