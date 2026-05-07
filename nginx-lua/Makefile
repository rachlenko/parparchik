# ──────────────────────────────────────────────────────────────
#  parparchik (nginx + lua) — Makefile
# ──────────────────────────────────────────────────────────────

SHELL          := /bin/bash
.DEFAULT_GOAL  := help

COMPOSE := docker compose
MC      ?= mc

# ──────────────────────────────────────────────
#  Docker
# ──────────────────────────────────────────────

.PHONY: up
up: ## Start MinIO + parparchik containers
	$(COMPOSE) up -d --build

.PHONY: down
down: ## Stop and remove all containers
	$(COMPOSE) down

.PHONY: logs
logs: ## Tail container logs
	$(COMPOSE) logs -f

.PHONY: ps
ps: ## Show container status
	$(COMPOSE) ps

.PHONY: restart
restart: down up ## Rebuild and restart everything

# ──────────────────────────────────────────────
#  Test
# ──────────────────────────────────────────────

.PHONY: test
test: ## Run e2e tests (containers must be running)
	MC=$(MC) ./test/e2e_test.sh

.PHONY: test-all
test-all: up ## Start containers and run e2e tests
	@echo "Waiting for services..."
	@for i in $$(seq 1 60); do \
		curl -sf http://localhost:8080/status >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	MC=$(MC) ./test/e2e_test.sh

# ──────────────────────────────────────────────
#  Status
# ──────────────────────────────────────────────

.PHONY: status
status: ## Check service status
	@curl -sf http://localhost:8080/status | python3 -m json.tool 2>/dev/null || echo "Service not running"

.PHONY: list
list: ## List all registered files
	@curl -sf http://localhost:8080/list | python3 -m json.tool 2>/dev/null || echo "Service not running"

.PHONY: metrics
metrics: ## Print Prometheus metrics
	@curl -sf http://localhost:8080/metrics || echo "Service not running"

# ──────────────────────────────────────────────
#  Help
# ──────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
