SHELL := /bin/bash
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GO ?= go
PNPM ?= pnpm
BIN := bin/harbor-pilot
DEMO_CLOCK ?= 2026-11-05T12:00:00Z
E2E_PORT ?= 18189
FUZZTIME ?= 30s

.PHONY: all web web-deps build build-go run demo dev test test-go test-web race lint golint vet typecheck check \
	gen gen-api gen-web fuzz-smoke e2e e2e-install docker docker-up release-snapshot clean

all: build

web-deps:
	cd web && $(PNPM) install --frozen-lockfile

web: web-deps
	cd web && $(PNPM) run build

build: web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harbor-pilot

# Go-only build using whatever SPA is already in internal/web/dist.
build-go:
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/harbor-pilot

run: build-go
	$(BIN) serve --config deploy/examples/harbor-pilot.yaml

demo: build-go
	$(BIN) serve --demo --listen 127.0.0.1:8080 --data-dir $(CURDIR)/.data-demo

# dev builds the UI and a separate binary, then serves a demo instance you
# can keep using while the tree changes. DEV_RESET=1 starts from fresh data.
DEV_ADDR ?= 127.0.0.1:8080
DEV_BIN := bin/harbor-pilot-dev
dev: web
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(DEV_BIN) ./cmd/harbor-pilot
	@if [ "$(DEV_RESET)" = "1" ]; then rm -rf $(CURDIR)/.data-dev; fi
	@echo "Harbor Pilot demo: http://$(DEV_ADDR)  (admin@example.test / harbor-pilot-demo)"
	$(DEV_BIN) serve --demo --listen $(DEV_ADDR) --data-dir $(CURDIR)/.data-dev

vet:
	$(GO) vet ./...

test-go:
	$(GO) test ./...

race:
	$(GO) test -race -count=1 ./...

golint:
	$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...

typecheck:
	cd web && $(PNPM) run typecheck

lint: golint
	cd web && $(PNPM) run lint
	@test -z "$$(gofmt -l ./cmd ./internal ./migrations)" || (echo "gofmt needed:"; gofmt -l ./cmd ./internal ./migrations; exit 1)

test-web:
	cd web && $(PNPM) run test

test: test-go test-web

check: vet lint race typecheck test-web
	cd web && $(PNPM) run build

# Regenerate the committed OpenAPI document and the web types derived from it.
gen: gen-api gen-web

gen-api:
	$(GO) run -ldflags "-X main.version=dev" ./cmd/harbor-pilot openapi > api/openapi.yaml

gen-web:
	cd web && $(PNPM) run gen:api

fuzz-smoke:
	@for pkg in $$($(GO) list ./internal/...); do \
		for f in $$($(GO) test -list 'Fuzz.*' $$pkg 2>/dev/null | grep '^Fuzz'); do \
			echo "fuzz $$pkg $$f"; $(GO) test -run '^$$' -fuzz "^$$f$$" -fuzztime $(FUZZTIME) $$pkg || exit 1; \
		done; \
	done

e2e-install:
	cd web && $(PNPM) exec playwright install chromium

e2e: build
	cd web && HP_E2E_PORT=$(E2E_PORT) HP_E2E_BIN=$(CURDIR)/$(BIN) HP_E2E_CLOCK=$(DEMO_CLOCK) $(PNPM) exec playwright test

docker:
	docker build --build-arg VERSION=$(VERSION) -f deploy/Dockerfile -t harbor-pilot:$(VERSION) -t harbor-pilot:latest .

docker-up:
	docker compose -f deploy/docker-compose.yml up --build

release-snapshot:
	$(GO) run github.com/goreleaser/goreleaser/v2@v2.12.7 release --snapshot --clean

clean:
	rm -rf bin dist .data-demo .data-dev web/node_modules/.tmp web/test-results web/playwright-report web/.e2e
