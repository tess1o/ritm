# Ritm — developer tasks. `make dev` runs the Go API (:7486) and the Vite dev server (:5173).
SHELL := /bin/bash
GO    ?= go
NPM   ?= npm
BIN   := bin/ritm
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
IMAGE     ?= ritm
PLATFORMS ?= linux/amd64,linux/arm64

.PHONY: help dev dev-api dev-web env web-deps build build-web build-go test test-web lint openapi import seed backup bench docker docker-multiarch clean

help: ## Show targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

env: ## Create .env from .env.example if missing
	@test -f .env || (cp .env.example .env && echo "created .env from .env.example — edit RITM_INIT_PASSWORD")

web/node_modules: web/package.json
	cd web && $(NPM) install
	@touch web/node_modules

web-deps: web/node_modules ## Install frontend dependencies

dev: env web/node_modules ## Run Go API + Vite dev server (open http://localhost:5173)
	@trap 'kill 0' EXIT INT TERM; \
	  ( set -a; source ./.env; set +a; $(GO) run ./cmd/ritm serve ) & \
	  ( cd web && $(NPM) run dev ) & \
	  wait

dev-api: env ## Run only the Go API on :7486
	@set -a; source ./.env; set +a; $(GO) run ./cmd/ritm serve

dev-web: web/node_modules ## Run only the Vite dev server on :5173 (proxies /api to :7486)
	cd web && $(NPM) run dev

build: build-web build-go ## Build the UI and a binary that embeds it (bin/ritm)

build-web:
	cd web && $(NPM) ci && $(NPM) run build

build-go:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/ritm

test: ## Run Go tests
	$(GO) test ./...

test-web: web/node_modules ## Type-check, lint and size-check the frontend
	cd web && $(NPM) run typecheck && $(NPM) run lint && $(NPM) run build && $(NPM) run size

lint: ## go vet + gofmt check
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd internal tools web/*.go migrations)" || (gofmt -l cmd internal tools; exit 1)

openapi: web/node_modules ## Regenerate docs/openapi.yaml and the frontend's API types from the Go handlers
	$(GO) run ./cmd/ritm openapi > docs/openapi.yaml
	cd web && $(NPM) run gen:api

import: env ## Import an export from the CLI: make import FILE=~/Downloads/export.zip [FULL=1]
	@test -n "$(FILE)" || (echo "usage: make import FILE=path/to/export.zip" && exit 1)
	@set -a; source ./.env; set +a; $(GO) run ./cmd/ritm import --user "$$RITM_INIT_USER" $(if $(FULL),--full) "$(FILE)"

seed: env ## Generate and import 180 days of synthetic data (for UI development)
	$(GO) run ./tools/genexport -days 180 -o data/synthetic-export.zip
	@set -a; source ./.env; set +a; $(GO) run ./cmd/ritm import --user "$$RITM_INIT_USER" data/synthetic-export.zip

backup: env ## Back up every database: make backup DIR=backups/2026-10-22
	@test -n "$(DIR)" || (echo "usage: make backup DIR=path/to/new-dir" && exit 1)
	@set -a; source ./.env; set +a; $(GO) run ./cmd/ritm backup "$(DIR)"

bench: build-go ## Check the performance budgets: make bench [EXPORT=path/to/export.zip] [DAYS=1825 HR=300]
	$(GO) run ./tools/bench -bin $(BIN) $(if $(EXPORT),-export "$(EXPORT)") -days $(or $(DAYS),1825) -hr $(or $(HR),300)

docker: ## Build the image for this machine's platform (IMAGE=ritm)
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

docker-multiarch: ## Build linux/amd64 + linux/arm64 (PUSH=1 pushes to IMAGE's registry)
	docker buildx build --platform $(PLATFORMS) --build-arg VERSION=$(VERSION) \
	  -t $(IMAGE):$(VERSION) -t $(IMAGE):latest $(if $(PUSH),--push) .

clean: ## Remove build output (keeps ./data)
	rm -rf bin web/dist/assets web/dist/index.html
