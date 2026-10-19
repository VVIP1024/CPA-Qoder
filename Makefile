GO ?= go
DOCKER ?= docker
GO_IMAGE ?= golang:1.26-bookworm
TARGET_ARCH ?= amd64
TARGET_PLATFORM := linux/$(TARGET_ARCH)
PACKAGE_NAME := cpa-qoder-linux-$(TARGET_ARCH)
PACKAGE_DIR := $(abspath dist/$(PACKAGE_NAME))
ARCHIVE := $(abspath dist/$(PACKAGE_NAME).tar.gz)
CHECKSUM := $(abspath dist/$(PACKAGE_NAME).sha256)
CONTAINER_PACKAGE_DIR := /src/dist/$(PACKAGE_NAME)
SYSTEM := $(shell uname -s)
ifeq ($(SYSTEM),Darwin)
EXT := dylib
else
EXT := so
endif
ifeq ($(OS),Windows_NT)
EXT := dll
endif

.PHONY: all build package release-linux-amd64 release-linux-arm64 test clean
all: package

build:
	mkdir -p build/plugins
	$(GO) build -buildmode=c-shared -o build/plugins/cpa-qoder.$(EXT) .
	$(GO) build -o build/cpa-qoder-helper .

package:
	test "$(TARGET_ARCH)" = "amd64" -o "$(TARGET_ARCH)" = "arm64"
	rm -rf "$(PACKAGE_DIR)" "$(ARCHIVE)" "$(CHECKSUM)"
	mkdir -p "$(PACKAGE_DIR)/plugins" "$(PACKAGE_DIR)/bin" "$(PACKAGE_DIR)/auths"
	$(DOCKER) run --rm --platform "$(TARGET_PLATFORM)" --user "$$(id -u):$$(id -g)" \
		-e HOME=/tmp -e GOCACHE=/tmp/go-build -e GOMODCACHE=/tmp/go-mod \
		-v "$(abspath .):/src" -w /src $(GO_IMAGE) sh -ceu '\
			test "$$(go env GOOS)" = linux; \
			test "$$(go env GOARCH)" = "$(TARGET_ARCH)"; \
			CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w" -buildmode=c-shared -o /tmp/cpa-qoder.so .; \
			cp /tmp/cpa-qoder.so "$(CONTAINER_PACKAGE_DIR)/plugins/cpa-qoder.so"; \
			CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="-s -w" -o "$(CONTAINER_PACKAGE_DIR)/bin/cpa-qoder-helper" .'
	cp auths/qoder-global.json auths/qoder-cn.json "$(PACKAGE_DIR)/auths/"
	cp config.example.yaml "$(PACKAGE_DIR)/config.example.yaml"
	cp INSTALL.md "$(PACKAGE_DIR)/INSTALL.md"
	chmod 755 "$(PACKAGE_DIR)/bin/cpa-qoder-helper"
	test -s "$(PACKAGE_DIR)/plugins/cpa-qoder.so"
	test -x "$(PACKAGE_DIR)/bin/cpa-qoder-helper"
	tar -C "$(abspath dist)" -czf "$(ARCHIVE)" "$(PACKAGE_NAME)"
	cd "$(abspath dist)" && { command -v sha256sum >/dev/null 2>&1 && sha256sum "$(PACKAGE_NAME).tar.gz" > "$(PACKAGE_NAME).sha256" || shasum -a 256 "$(PACKAGE_NAME).tar.gz" > "$(PACKAGE_NAME).sha256"; }
	@echo "Built $(ARCHIVE) for $(TARGET_PLATFORM)"

release-linux-amd64:
	$(MAKE) package TARGET_ARCH=amd64

release-linux-arm64:
	$(MAKE) package TARGET_ARCH=arm64

test:
	$(GO) test -race ./...

clean:
	rm -rf build dist
