GO ?= go
GO_TEST_ENV := GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/gomod

MODULE := github.com/s1liconcow/skiff
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' -X '$(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)'

.PHONY: build test e2e-local e2e-apple-container e2e-aws codex-apple-sandbox vet fmt lint generate smoke clean

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff ./cmd/skiff
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiffd ./cmd/skiffd
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-runner ./cmd/skiff-runner
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-mtls-plugin ./cmd/skiff-mtls-plugin

test:
	$(GO_TEST_ENV) $(GO) test ./...

e2e-local:
	$(GO_TEST_ENV) $(GO) test ./tests/e2e ./tests/conformance/provider ./tests/conformance/plugin -count=1 -v

e2e-apple-container:
	SKIFF_APPLE_CONTAINER_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAppleContainerRustFSCaddyRollout -count=1 -v

e2e-aws:
	SKIFF_AWS_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAWSE2E -count=1 -v

codex-apple-sandbox:
	./scripts/codex-apple-sandbox.sh

vet:
	$(GO) vet ./...

fmt:
	@files=$$(find cmd internal tests -name '*.go' -print); \
	if [ -n "$$files" ]; then gofmt -w $$files; fi

lint: vet

generate:
	$(GO) generate ./...

smoke: build
	./bin/skiff version --format json >/dev/null
	./bin/skiffd version --format json >/dev/null
	./bin/skiff-runner version --format json >/dev/null

clean:
	rm -rf bin
