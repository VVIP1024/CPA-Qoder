GO ?= go
CPA_DIR ?= ../CLIProxyAPI
PLUGIN_DIR ?= $(CPA_DIR)/plugins
AUTH_DIR ?= $(CPA_DIR)/auths
SYSTEM := $(shell uname -s)
ifeq ($(SYSTEM),Darwin)
EXT := dylib
else
EXT := so
endif
ifeq ($(OS),Windows_NT)
EXT := dll
endif

.PHONY: all build test server run install clean
all: build

build:
	mkdir -p build/plugins
	$(GO) build -buildmode=c-shared -o build/plugins/cpa-qoder.$(EXT) .
	$(GO) build -o build/cpa-qoder-helper .

test:
	$(GO) test -race ./...

server:
	cd $(CPA_DIR) && $(GO) build -o $(abspath build/cli-proxy-api) ./cmd/server

run: build server
	./build/cli-proxy-api --config config.example.yaml --local-model

install: build
	mkdir -p "$(PLUGIN_DIR)" "$(AUTH_DIR)"
	cp build/plugins/cpa-qoder.$(EXT) "$(PLUGIN_DIR)/cpa-qoder.$(EXT)"
	cp -n auths/qoder-global.json "$(AUTH_DIR)/qoder-global.json"
	cp -n auths/qoder-cn.json "$(AUTH_DIR)/qoder-cn.json"
	@echo "Set plugins.configs.cpa-qoder.helper to $(abspath build/cpa-qoder-helper)"
	@echo "Enable cpa-qoder in your CPA configuration; see README_CN.md."

clean:
	rm -rf build
