GO ?= go
DOCKER ?= docker
GO_IMAGE ?= golang:1.26-bookworm
GOPROXY ?= $(shell $(GO) env GOPROXY 2>/dev/null)
ifeq ($(strip $(GOPROXY)),)
GOPROXY := https://proxy.golang.org,direct
endif
GO_MOD_CACHE_VOLUME ?= cpa-go-mod-cache
GOMODCACHE_HOST ?= $(shell $(GO) env GOMODCACHE 2>/dev/null)
ifneq ($(strip $(GOMODCACHE_HOST)),)
DOCKER_MOD_CACHE := $(GOMODCACHE_HOST)
else
DOCKER_MOD_CACHE := $(GO_MOD_CACHE_VOLUME)
endif
TARGET_ARCH ?= amd64
TARGET_PLATFORM := linux/$(TARGET_ARCH)
HOST_OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')
RAW_ARCH := $(shell uname -m)
ifeq ($(RAW_ARCH),x86_64)
HOST_ARCH := amd64
else ifeq ($(RAW_ARCH),aarch64)
HOST_ARCH := arm64
else
HOST_ARCH := $(RAW_ARCH)
endif
PACKAGE_NAME := cpa-qoder-linux-$(TARGET_ARCH)
PACKAGE_DIR := $(abspath dist/$(PACKAGE_NAME))
ARCHIVE := $(abspath dist/$(PACKAGE_NAME).tar.gz)
CHECKSUM := $(abspath dist/$(PACKAGE_NAME).sha256)
CONTAINER_PACKAGE_DIR := /src/dist/$(PACKAGE_NAME)
MACOS_PACKAGE_NAME := cpa-qoder-darwin-$(HOST_ARCH)
MACOS_PACKAGE_DIR := $(abspath dist/$(MACOS_PACKAGE_NAME))
MACOS_ARCHIVE := $(abspath dist/$(MACOS_PACKAGE_NAME).tar.gz)
MACOS_CHECKSUM := $(abspath dist/$(MACOS_PACKAGE_NAME).sha256)
ifeq ($(HOST_OS),darwin)
EXT := dylib
else
EXT := so
endif

.PHONY: all build package package-macos package-darwin package-linux release-linux-amd64 release-linux-arm64 test clean
all: build

build:
	mkdir -p build/plugins
	$(GO) build -buildmode=c-shared -o build/plugins/cpa-qoder.$(EXT) ./cmd/cpa-qoder
	$(GO) build -o build/cpa-qoder-helper ./cmd/qoder-main

package: package-$(HOST_OS)

package-darwin:
	@test "$(HOST_OS)" = "darwin" || { echo "package-darwin requires macOS"; exit 1; }
	$(MAKE) build
	rm -rf "$(MACOS_PACKAGE_DIR)" "$(MACOS_ARCHIVE)" "$(MACOS_CHECKSUM)"
	mkdir -p "$(MACOS_PACKAGE_DIR)/plugins" "$(MACOS_PACKAGE_DIR)/bin" "$(MACOS_PACKAGE_DIR)/auths"
	cp build/plugins/cpa-qoder.dylib "$(MACOS_PACKAGE_DIR)/plugins/"
	cp build/cpa-qoder-helper "$(MACOS_PACKAGE_DIR)/bin/"
	cp examples/auths/qoder.json examples/auths/qodercn.json "$(MACOS_PACKAGE_DIR)/auths/"
	cp configs/config.example.yaml README.md INSTALL.md "$(MACOS_PACKAGE_DIR)/"
	chmod 755 "$(MACOS_PACKAGE_DIR)/bin/cpa-qoder-helper"
	test -s "$(MACOS_PACKAGE_DIR)/plugins/cpa-qoder.dylib"
	test -x "$(MACOS_PACKAGE_DIR)/bin/cpa-qoder-helper"
	tar -C "$(abspath dist)" -czf "$(MACOS_ARCHIVE)" "$(MACOS_PACKAGE_NAME)"
	cd "$(abspath dist)" && { command -v sha256sum >/dev/null 2>&1 && sha256sum "$(MACOS_PACKAGE_NAME).tar.gz" > "$(MACOS_PACKAGE_NAME).sha256" || shasum -a 256 "$(MACOS_PACKAGE_NAME).tar.gz" > "$(MACOS_PACKAGE_NAME).sha256"; }
	@echo "Built $(MACOS_ARCHIVE) for darwin/$(HOST_ARCH)"

package-macos: package-darwin

package-linux:
	test "$(TARGET_ARCH)" = "amd64" -o "$(TARGET_ARCH)" = "arm64"
	rm -rf "$(PACKAGE_DIR)" "$(ARCHIVE)" "$(CHECKSUM)"
	mkdir -p "$(PACKAGE_DIR)/plugins" "$(PACKAGE_DIR)/bin" "$(PACKAGE_DIR)/auths"
	@[ -z "$(GOMODCACHE_HOST)" ] || mkdir -p "$(GOMODCACHE_HOST)"
	$(DOCKER) run --rm --platform "$(TARGET_PLATFORM)" --user "$$(id -u):$$(id -g)" \
		-e HOME=/tmp -e GOCACHE=/tmp/go-build -e GOMODCACHE=/go/pkg/mod \
		-e GOPROXY="$(GOPROXY)" \
		-v "$(DOCKER_MOD_CACHE):/go/pkg/mod" \
		-v "$(abspath .):/src" -w /src $(GO_IMAGE) sh -ceu '\
			test "$$(go env GOOS)" = linux; \
			test "$$(go env GOARCH)" = "$(TARGET_ARCH)"; \
			CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w" -buildmode=c-shared -o /tmp/cpa-qoder.so ./cmd/cpa-qoder; \
			cp /tmp/cpa-qoder.so "$(CONTAINER_PACKAGE_DIR)/plugins/cpa-qoder.so"; \
			go build -trimpath -buildvcs=false -ldflags="-s -w" -o "$(CONTAINER_PACKAGE_DIR)/bin/cpa-qoder-helper" ./cmd/qoder-main'
	cp examples/auths/qoder.json examples/auths/qodercn.json "$(PACKAGE_DIR)/auths/"
	cp configs/config.example.yaml README.md INSTALL.md "$(PACKAGE_DIR)/"
	chmod 755 "$(PACKAGE_DIR)/bin/cpa-qoder-helper"
	test -s "$(PACKAGE_DIR)/plugins/cpa-qoder.so"
	test -x "$(PACKAGE_DIR)/bin/cpa-qoder-helper"
	tar -C "$(abspath dist)" -czf "$(ARCHIVE)" "$(PACKAGE_NAME)"
	cd "$(abspath dist)" && { command -v sha256sum >/dev/null 2>&1 && sha256sum "$(PACKAGE_NAME).tar.gz" > "$(PACKAGE_NAME).sha256" || shasum -a 256 "$(PACKAGE_NAME).tar.gz" > "$(PACKAGE_NAME).sha256"; }
	@echo "Built $(ARCHIVE) for $(TARGET_PLATFORM)"

release-linux-amd64:
	$(MAKE) package-linux TARGET_ARCH=amd64

release-linux-arm64:
	$(MAKE) package-linux TARGET_ARCH=arm64

test:
	$(GO) test -race ./...

clean:
	rm -rf build dist
