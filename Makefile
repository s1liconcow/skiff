GO ?= go
GO_TEST_ENV := GOCACHE=$(CURDIR)/.cache/go-build GOMODCACHE=$(CURDIR)/.cache/gomod

MODULE := github.com/s1liconcow/skiff
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' -X '$(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)'
INSTALL ?= install
PREFIX ?= $(HOME)/.local
BINDIR ?= $(PREFIX)/bin
CORE_BINS := skiff skiffd skiff-runner skiff-worker

.PHONY: build install test readiness e2e-local e2e-apple-container e2e-aws demo-local demo-test demo-apple-container demo-apple-context demo-apple-up demo-apple-down clean-apple-containers codex-apple-sandbox codex-apple-sandbox-playwright vet fmt lint generate smoke clean

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff ./cmd/skiff
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiffd ./cmd/skiffd
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-runner ./cmd/skiff-runner
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-worker ./cmd/skiff-worker
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-mtls-plugin ./cmd/skiff-mtls-plugin

install: build
	$(INSTALL) -d "$(DESTDIR)$(BINDIR)"
	for bin in $(CORE_BINS); do \
		$(INSTALL) -m 0755 "bin/$$bin" "$(DESTDIR)$(BINDIR)/$$bin"; \
	done

test:
	$(GO_TEST_ENV) $(GO) test ./...

readiness:
	$(GO_TEST_ENV) $(GO) test ./tests/readiness ./tests/chaos -count=1 -v

e2e-local:
	$(GO_TEST_ENV) $(GO) test ./tests/e2e ./tests/conformance/provider ./tests/conformance/plugin -count=1 -v

e2e-apple-container:
	SKIFF_APPLE_CONTAINER_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAppleContainerRustFSCaddyRollout -count=1 -v

e2e-aws:
	SKIFF_AWS_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAWSE2E -count=1 -v

demo-local:
	./demos/quickstart-fake.sh

demo-test:
	./demos/test-local-demo.sh

demo-apple-container:
	./demos/apple-container-caddy.sh

demo-apple-context demo-apple-up:
	SKIFF_APPLE_CONTAINER_PERSIST=1 ./demos/apple-container-caddy.sh

demo-apple-down:
	./demos/apple-container-down.sh

clean-apple-containers:
	./demos/apple-container-down.sh --all

codex-apple-sandbox:
	./scripts/codex-apple-sandbox.sh

codex-apple-sandbox-playwright:
	./scripts/codex-apple-sandbox.sh --playwright --memory 4G

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
	./bin/skiff-worker --help >/dev/null

clean:
	rm -rf bin
