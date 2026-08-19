# ──────────────────────────────────────────────────────────────
#  parparchik — S3 file routing web service
# ──────────────────────────────────────────────────────────────
#
#  The Go implementation (golang/) is the primary, recommended
#  implementation. The `go-*` targets below delegate into it.
#  A Python reference server (server.py) remains available via
#  `make run-docker` / `make test-all` for comparison.

SHELL          := /bin/bash
.DEFAULT_GOAL  := help

MC             ?= mc
ZENSICAL       ?= uvx zensical

# Docker
COMPOSE        := docker compose

# ──────────────────────────────────────────────
#  Go implementation (golang/) — primary
# ──────────────────────────────────────────────

.PHONY: go-build
go-build: ## Build the Go binary (golang/)
	cd golang && go build ./...

.PHONY: go-test
go-test: ## Run the Go test suite with race detection and coverage
	cd golang && go vet ./... && gofmt -l . && go test -race -cover ./...

.PHONY: go-lint
go-lint: ## Run golangci-lint against golang/
	cd golang && golangci-lint run ./...

.PHONY: go-docker-up
go-docker-up: ## Start MinIO + the Go parparchik service
	cd golang && $(COMPOSE) up -d --build

.PHONY: go-docker-down
go-docker-down: ## Stop the Go implementation's Docker stack
	cd golang && $(COMPOSE) down

.PHONY: go-docker-logs
go-docker-logs: ## Tail the Go implementation's container logs
	cd golang && $(COMPOSE) logs -f

.PHONY: go-run-docker
go-run-docker: go-docker-up ## Start the Go implementation's full Docker stack
	@echo "parparchik (Go) running at http://localhost:8080"
	@echo "MinIO console at             http://localhost:9001 (minioadmin/minioadmin)"

.PHONY: go-test-e2e
go-test-e2e: go-docker-up ## Start the Go stack and run the e2e test suite against it
	@echo "Waiting for services..."
	@for i in $$(seq 1 30); do \
		curl -sf http://localhost:8080/status >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	MC=$(MC) ./test/e2e_test.sh

# ──────────────────────────────────────────────
#  Docker (Python reference server)
# ──────────────────────────────────────────────

.PHONY: docker-up
docker-up: ## Start MinIO + the Python reference server
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

.PHONY: run-docker
run-docker: docker-up ## Start the Python reference server's full stack in Docker
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

.PHONY: test-mock-metrics
test-mock-metrics: docker-up ## Run mock S3 manifest/Prometheus metrics scenario
	@echo "Waiting for services..."
	@for i in $$(seq 1 60); do \
		curl -sf http://localhost:8080/healthcheck >/dev/null 2>&1 && break; \
		sleep 1; \
	done
	MC=$(MC) ./test/mock_s3_manifest_metrics_test.sh

# ──────────────────────────────────────────────
#  Status / Health
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
#  Documentation / static site
# ──────────────────────────────────────────────

.PHONY: docs-check
docs-check: ## Validate Zensical documentation build (source: docs/, output: site/)
	$(ZENSICAL) build --strict

.PHONY: docs-site
docs-site: ## Build static documentation into site/ (source: docs/)
	$(ZENSICAL) build

.PHONY: docs-serve
docs-serve: ## Serve documentation locally at localhost:8000
	$(ZENSICAL) serve --dev-addr localhost:8000

.PHONY: docs-procedure
docs-procedure: ## Show documentation and skills update procedure
	@cat procedures/documentation-site-and-skills.md

# ──────────────────────────────────────────────
#  Help
# ──────────────────────────────────────────────

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
