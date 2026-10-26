# WhaCP developer tasks.

GO      ?= go
VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)

.PHONY: all build test test-agent test-server lint dev clean release

all: lint test

build: dist/whacp-agent dist/whacp

dist/whacp-agent: $(shell find agent -name '*.go')
	cd agent && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist/whacp-agent ./cmd/whacp-agent

dist/whacp: $(shell find server -name '*.go')
	cd server && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist/whacp ./cmd/whacp

test: test-agent test-server

test-agent:
	cd agent && $(GO) vet ./... && $(GO) test -race ./...

test-server:
	cd server && $(GO) vet ./... && $(GO) test -race ./...

lint:
	@test -z "$$(gofmt -l $$(find agent server -name '*.go'))" || (echo "gofmt needed:"; gofmt -l $$(find agent server -name '*.go'); exit 1)
	@for f in installer/install.sh installer/upgrade.sh installer/uninstall.sh scripts/dev.sh tests/integration/smoke.sh tests/integration/upgrade.sh tests/integration/public-https.sh tests/e2e/run.sh scripts/release.sh; do [ -f $$f ] && bash -n $$f || exit 1; done

dev:
	scripts/dev.sh

release:
	scripts/release.sh

clean:
	rm -rf dist .dev
