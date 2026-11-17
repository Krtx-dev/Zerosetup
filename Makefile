# ZEROSETUP developer tasks. Uses the project-local Go toolchain in .tools/
# when present (see docs/DEVELOPMENT.md); otherwise the Go on PATH.

SHELL := /bin/bash
ROOT := $(abspath .)
GO := go
ifneq ($(wildcard .tools/go/bin/go),)
GO := "$(ROOT)/.tools/go/bin/go"
export PATH := $(ROOT)/.tools/go/bin:$(PATH)
export GOPATH := $(ROOT)/.tools/gopath
export GOCACHE := $(ROOT)/.tools/gocache
export GOTOOLCHAIN := local
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS := -s -w -X github.com/zerosetup/zerosetup/internal/engine.Version=$(VERSION)
WEBDIST := internal/api/webdist/dist

.PHONY: docs-cli all build dashboard dashboard-deps go-build test test-race test-integration lint fmt vet check e2e clean

all: build

## build: dashboard + Go binary with the dashboard embedded
build: dashboard go-build

dashboard-deps:
	cd dashboard && npm ci --no-audit --no-fund

## dashboard: build the React app and copy it into the Go embed directory
dashboard:
	cd dashboard && npm run build
	find $(WEBDIST) -mindepth 1 -delete
	cp -R dashboard/dist/. $(WEBDIST)/

go-build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/zerosetup ./cmd/zerosetup

## test: Go unit tests + dashboard unit tests
test:
	$(GO) test ./...
	cd dashboard && npm test

test-race:
	$(GO) test -race ./...

## test-integration: runs fixtures in real Docker containers (needs Docker + network)
test-integration:
	$(GO) test -tags integration -timeout 40m -v ./tests/integration

## e2e: Playwright against a real `zerosetup dashboard` (needs Docker + network)
e2e: build
	cd dashboard && npx playwright test

fmt:
	gofmt -w cmd internal pkg tests

vet:
	$(GO) vet ./... && $(GO) vet -tags integration ./tests/integration

lint:
	test -z "$$(gofmt -l cmd internal pkg tests)" || (gofmt -l cmd internal pkg tests; exit 1)
	cd dashboard && npm run lint && npm run typecheck

## docs-cli: regenerate docs/CLI.md from the binary's --help output
docs-cli: go-build
	scripts/gen-cli-docs.sh bin/zerosetup

## check: everything CI runs without Docker
check: lint vet test

clean:
	rm -rf bin dashboard/dist
	git checkout -- $(WEBDIST) 2>/dev/null || true
