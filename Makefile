# ──────────────────────────────────────────────────────────────
#  parparchik — S3 file routing web service
# ──────────────────────────────────────────────────────────────

SHELL          := /bin/bash
.DEFAULT_GOAL  := help

# Paths — all dependencies live in ../vcpkgproxy
VCPKGPROXY_DIR := $(abspath ../vcpkgproxy)
VCPKG_ROOT     := $(VCPKGPROXY_DIR)/vcpkg
VCPKG_TOOLCHAIN:= $(VCPKG_ROOT)/scripts/buildsystems/vcpkg.cmake
BUILD_DIR      := build
TRIPLET_OVERLAY:= $(VCPKGPROXY_DIR)/triplets
MC             ?= mc

# Point vcpkg at the local proxy for all downloads and binary caches.
# After `make sync`, no network access is needed.
export PATH                    := $(VCPKGPROXY_DIR)/bin:$(PATH)
export VCPKG_ROOT              := $(VCPKG_ROOT)
export VCPKG_DOWNLOADS         := $(VCPKGPROXY_DIR)/downloads
export VCPKG_DEFAULT_BINARY_CACHE := $(VCPKGPROXY_DIR)/binary-cache

# macOS: ensure SDK root is set so vcpkg builds find system headers
export SDKROOT ?= $(shell xcrun --show-sdk-path 2>/dev/null)

# Docker
COMPOSE        := docker compose

# ──────────────────────────────────────────────
#  vcpkgproxy (offline proxy)
# ──────────────────────────────────────────────

.PHONY: sync
sync: ## Download all dependencies into vcpkgproxy (requires network)
	$(VCPKGPROXY_DIR)/scripts/sync.sh

.PHONY: vcpkg-setup
vcpkg-setup: ## Install packages from local vcpkgproxy cache (offline)
	$(VCPKGPROXY_DIR)/scripts/setup.sh

# ──────────────────────────────────────────────
#  C++ build (native, offline after sync)
# ──────────────────────────────────────────────

.PHONY: configure
configure: ## Configure CMake with vcpkg toolchain + sccache
	cmake -B $(BUILD_DIR) \
		-DCMAKE_TOOLCHAIN_FILE=$(VCPKG_TOOLCHAIN) \
		-DVCPKG_OVERLAY_TRIPLETS=$(TRIPLET_OVERLAY) \
		-DVCPKG_INSTALLED_DIR=$(VCPKGPROXY_DIR)/installed \
		-DCMAKE_C_COMPILER_LAUNCHER=sccache \
		-DCMAKE_CXX_COMPILER_LAUNCHER=sccache \
		-DCMAKE_BUILD_TYPE=Release

.PHONY: configure-debug
configure-debug: ## Configure CMake in Debug mode
	cmake -B $(BUILD_DIR) \
		-DCMAKE_TOOLCHAIN_FILE=$(VCPKG_TOOLCHAIN) \
		-DVCPKG_OVERLAY_TRIPLETS=$(TRIPLET_OVERLAY) \
		-DVCPKG_INSTALLED_DIR=$(VCPKGPROXY_DIR)/installed \
		-DCMAKE_C_COMPILER_LAUNCHER=sccache \
		-DCMAKE_CXX_COMPILER_LAUNCHER=sccache \
		-DCMAKE_BUILD_TYPE=Debug

.PHONY: build
build: ## Build the C++ binary
	cmake --build $(BUILD_DIR) -j$$(nproc 2>/dev/null || sysctl -n hw.ncpu)

.PHONY: build-all
build-all: vcpkg-setup configure build ## Full pipeline: vcpkg → configure → build

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

# ──────────────────────────────────────────────
#  Docker
# ──────────────────────────────────────────────

.PHONY: docker-up
docker-up: ## Start MinIO + parparchik containers
	$(COMPOSE) up -d --build

.PHONY: docker-down
docker-down: ## Stop and remove all containers
	$(COMPOSE) down

.PHONY: docker-logs
docker-logs: ## Tail container logs
	$(COMPOSE) logs -f

.PHONY: docker-ps
docker-ps: ## Show container status
	$(COMPOSE) ps

.PHONY: docker-restart
docker-restart: docker-down docker-up ## Rebuild and restart everything

# ──────────────────────────────────────────────
#  Run
# ──────────────────────────────────────────────

.PHONY: run-native
run-native: ## Run the C++ binary locally (requires env vars or .env)
	@if [ -f .env ]; then set -a && source .env && set +a; fi; \
	$(BUILD_DIR)/parparchik

.PHONY: run-docker
run-docker: docker-up ## Start the full stack in Docker
	@echo "parparchik running at http://localhost:8080"
	@echo "MinIO console at    http://localhost:9001 (minioadmin/minioadmin)"

# ──────────────────────────────────────────────
#  Test
# ──────────────────────────────────────────────

.PHONY: test
test: ## Run e2e tests (containers must be running)
	MC=$(MC) ./test/e2e_test.sh

.PHONY: test-all
test-all: docker-up ## Start containers and run e2e tests
	@echo "Waiting for services..."
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:8080/status >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	MC=$(MC) ./test/e2e_test.sh

# ──────────────────────────────────────────────
#  Status / Health
# ──────────────────────────────────────────────

.PHONY: status
status: ## Check service status
	@curl -sf http://localhost:8080/status | python3 -m json.tool 2>/dev/null || echo "Service not running"

.PHONY: list
list: ## List all registered files
	@curl -sf http://localhost:8080/list | python3 -m json.tool 2>/dev/null || echo "Service not running"

# ──────────────────────────────────────────────
#  Help
# ──────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
