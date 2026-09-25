# WhaCP developer tasks.

GO      ?= go
PHP     ?= php
VERSION ?= $(shell cat VERSION 2>/dev/null || echo dev)

.PHONY: all build test test-agent test-panel lint smoke upgrade-test dev clean release

all: lint test

build: dist/whacp-agent

dist/whacp-agent: $(shell find agent -name '*.go')
	cd agent && CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../dist/whacp-agent ./cmd/whacp-agent

test: test-agent test-panel

test-agent:
	cd agent && $(GO) vet ./... && $(GO) test -race ./...

test-panel: panel/vendor/autoload.php
	cd panel && vendor/bin/phpunit

lint: panel/vendor/autoload.php
	@test -z "$$(cd agent && gofmt -l .)" || (echo "gofmt needed:"; cd agent && gofmt -l .; exit 1)
	cd panel && find app bin config database public resources routes tests -name '*.php' -print0 | xargs -0 -n1 -P4 $(PHP) -l > /dev/null
	for f in installer/install.sh installer/upgrade.sh installer/uninstall.sh scripts/dev.sh tests/integration/smoke.sh tests/integration/upgrade.sh scripts/release.sh; do bash -n $$f || exit 1; done

smoke: panel/vendor/autoload.php
	tests/integration/smoke.sh

upgrade-test: panel/vendor/autoload.php
	tests/integration/upgrade.sh

dev:
	scripts/dev.sh

release:
	scripts/release.sh

panel/vendor/autoload.php:
	cd panel && composer install --no-interaction

clean:
	rm -rf dist .dev panel/storage/.phpunit.cache
