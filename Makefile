# Makefile for AlertSnitch
# vim: set ft=make ts=8 noet

COMMIT_ID := $(shell git log -1 --format=%H 2>/dev/null || echo "unknown")
COMMIT_DATE := $(shell git log -1 --format=%aI 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION := $(or $(CI_COMMIT_TAG),$(shell git describe --tags --always 2>/dev/null || echo "dev"))
SHELL := /bin/bash

GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0
CURRENT_DIR := $(shell pwd)

LDFLAGS := -s -w \
	-X gitlab.com/yakshaving.art/alertsnitch/version.Version=$(VERSION) \
	-X gitlab.com/yakshaving.art/alertsnitch/version.Commit=$(COMMIT_ID) \
	-X gitlab.com/yakshaving.art/alertsnitch/version.Date=$(COMMIT_DATE)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags "$(LDFLAGS)" -o alertsnitch-$(GOOS)-$(GOARCH)

.PHONY: build-all
build-all: ## Build for all platforms
	@for os in linux darwin; do \
		for arch in amd64 arm64; do \
			echo "Building $$os/$$arch..."; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -ldflags "$(LDFLAGS)" -o alertsnitch-$$os-$$arch; \
		done \
	done

.PHONY: install
install: ## Install to GOPATH/bin
	go install -ldflags "$(LDFLAGS)"

.PHONY: test
test: ## Run unit tests
	go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1

.PHONY: test-short
test-short: ## Run short tests only
	go test -v -short ./...

.PHONY: coverage
coverage: test ## Generate and open coverage report
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

.PHONY: lint
lint: ## Run golangci-lint
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run --timeout=5m

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with auto-fix
	golangci-lint run --fix --timeout=5m

.PHONY: fmt
fmt: ## Format code
	go fmt ./...
	goimports -w .

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: tidy
tidy: ## Tidy go modules
	go mod tidy

.PHONY: check
check: fmt vet lint test ## Run all checks (fmt, vet, lint, test)

.PHONY: clean
clean: ## Clean build artifacts
	rm -f alertsnitch-* coverage.out coverage.html
	go clean -cache -testcache

.PHONY: docker-build
docker-build: ## Build Docker image
	docker build -t alertsnitch:local .

.PHONY: docker-run
docker-run: ## Run Docker container locally
	docker run --rm -p 9567:9567 alertsnitch:local

.PHONY: pre-commit-install
pre-commit-install: ## Install pre-commit hooks
	@which pre-commit > /dev/null || (echo "Install pre-commit first: brew install pre-commit" && exit 1)
	pre-commit install

.PHONY: pre-commit-run
pre-commit-run: ## Run pre-commit on all files
	pre-commit run --all-files

.PHONY: release-dry
release-dry: ## Dry run goreleaser
	@which goreleaser > /dev/null || (echo "Install goreleaser first: brew install goreleaser" && exit 1)
	goreleaser release --snapshot --clean

.PHONY: deps
deps: ## Download dependencies
	go mod download

.PHONY: deps-update
deps-update: ## Update all dependencies
	go get -u ./...
	go mod tidy

# Database bootstrap targets
.PHONY: bootstrap-mysql
bootstrap-mysql: ## Bootstrap MySQL schema (requires MYSQL_* env vars)
	bash script.d/bootstrap_mysql.sh

.PHONY: bootstrap-postgres
bootstrap-postgres: ## Bootstrap PostgreSQL schema (requires POSTGRES_* env vars)
	bash script.d/bootstrap_postgres.sh

.PHONY: bootstrap_local_testing
bootstrap_local_testing: ## Builds and bootstraps a local integration testing environment
	@if [[ -z "$(MYSQL_ROOT_PASSWORD)" ]]; then echo "MYSQL_ROOT_PASSWORD is not set" ; exit 1; fi
	@if [[ -z "$(MYSQL_DATABASE)" ]]; then echo "MYSQL_DATABASE is not set" ; exit 1; fi
	@echo "Launching alertsnitch-mysql integration container"
	@docker run --rm --name alertsnitch-mysql \
		-e MYSQL_ROOT_PASSWORD=$(MYSQL_ROOT_PASSWORD) \
		-e MYSQL_DATABASE=$(MYSQL_DATABASE) \
		-p 3306:3306 \
		-v $(CURRENT_DIR)/database/mysql:/db.scripts \
		-d \
		mysql:8.4
	@while ! docker exec alertsnitch-mysql mysql --database=$(MYSQL_DATABASE) --password=$(MYSQL_ROOT_PASSWORD) -e "SELECT 1" >/dev/null 2>&1 ; do \
		echo "Waiting for database connection..." ; \
		sleep 1 ; \
	done

# Development helpers
.PHONY: run
run: ## Run the application locally
	go run main.go -debug

.PHONY: watch
watch: ## Run with file watching (requires air)
	@which air > /dev/null || (echo "Install air first: go install github.com/air-verse/air@latest" && exit 1)
	air

# Docker Compose targets
.PHONY: up
up: ## Start all services with docker-compose
	docker-compose up -d

.PHONY: down
down: ## Stop all services
	docker-compose down

.PHONY: logs
logs: ## Show logs from docker-compose
	docker-compose logs -f
