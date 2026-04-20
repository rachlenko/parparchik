# ──────────────────────────────────────────────────────────────
#  parparchik — S3 file routing web service
# ──────────────────────────────────────────────────────────────

SHELL          := /bin/bash
.DEFAULT_GOAL  := help

# Paths
VCPKGPROXY_DIR := $(abspath ../vcpkgproxy)
VCPKG_ROOT     := $(VCPKGPROXY_DIR)/vcpkg
VCPKG_TOOLCHAIN:= $(VCPKG_ROOT)/scripts/buildsystems/vcpkg.cmake
BUILD_DIR      := build
TRIPLET_OVERLAY:= $(VCPKGPROXY_DIR)/triplets
MC             ?= mc

# Ensure vcpkgproxy/bin is on PATH (for sccache)
export PATH := $(VCPKGPROXY_DIR)/bin:$(PATH)

# macOS: ensure SDK root is set so vcpkg builds find system headers
export SDKROOT ?= $(shell xcrun --show-sdk-path 2>/dev/null)

# Docker
COMPOSE        := docker compose

# ──────────────────────────────────────────────
#  vcpkg
# ──────────────────────────────────────────────

.PHONY: vcpkg-setup
vcpkg-setup: ## Set up vcpkgproxy: clone vcpkg, install sccache, fetch packages
	$(VCPKGPROXY_DIR)/scripts/setup.sh

.PHONY: vcpkg-sync
vcpkg-sync: ## Sync vcpkg from upstream GitHub
	$(VCPKGPROXY_DIR)/scripts/sync-vcpkg.sh

.PHONY: sccache-install
sccache-install: ## Install sccache from mozilla/sccache releases
	$(VCPKGPROXY_DIR)/scripts/install-sccache.sh

# ──────────────────────────────────────────────
#  C++ build (native)
# ──────────────────────────────────────────────

.PHONY: configure
configure: ## Configure CMake with vcpkg toolchain + sccache
	cmake -B $(BUILD_DIR) \
		-DCMAKE_TOOLCHAIN_FILE=$(VCPKG_TOOLCHAIN) \
		-DVCPKG_OVERLAY_TRIPLETS=$(TRIPLET_OVERLAY) \
		-DCMAKE_C_COMPILER_LAUNCHER=sccache \
		-DCMAKE_CXX_COMPILER_LAUNCHER=sccache \
		-DCMAKE_BUILD_TYPE=Release

.PHONY: configure-debug
configure-debug: ## Configure CMake in Debug mode
	cmake -B $(BUILD_DIR) \
		-DCMAKE_TOOLCHAIN_FILE=$(VCPKG_TOOLCHAIN) \
		-DVCPKG_OVERLAY_TRIPLETS=$(TRIPLET_OVERLAY) \
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
