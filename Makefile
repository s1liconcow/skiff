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
PACKER ?= packer
RUNNER_IMAGE_DIR ?= build/runner-image
RUNNER_IMAGE_DIST ?= $(CURDIR)/dist
RUNNER_IMAGE_VALIDATE_DIST ?= /private/tmp/skiff-packer-validate-dist
RUNNER_IMAGE_REGION ?= us-west-2
RUNNER_IMAGE_AMI_REGIONS ?= []
RUNNER_IMAGE_CHANNEL ?= stable
RUNNER_IMAGE_VERSION ?= $(VERSION)
RUNNER_IMAGE_PROVENANCE_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)

.PHONY: build install test ci install-hooks readiness e2e-local e2e-apple-container e2e-apple-stateful e2e-apple-opsem-profiles e2e-apple-stateful-packages e2e-aws demo-local demo-test demo-apple-container demo-apple-postgres-ha demo-apple-context demo-apple-up demo-apple-down clean-apple-containers codex-apple-sandbox codex-apple-sandbox-playwright vet fmt lint generate smoke runner-image-fmt runner-image-init runner-image-archives runner-image-validate runner-image-build clean

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

ci:
	@test -z "$$(gofmt -l $$(find cmd internal tests -name '*.go' -print))"
	$(GO_TEST_ENV) $(GO) test ./tests/conformance/...
	$(MAKE) e2e-local
	$(MAKE) readiness
	$(GO_TEST_ENV) $(GO) test ./...
	$(GO_TEST_ENV) $(GO) vet ./...

install-hooks:
	git config core.hooksPath .githooks
	@echo "Installed Skiff git hooks from .githooks"

readiness:
	$(GO_TEST_ENV) $(GO) test ./tests/readiness ./tests/chaos -count=1 -v

e2e-local:
	$(GO_TEST_ENV) $(GO) test ./tests/e2e ./tests/conformance/provider ./tests/conformance/plugin -count=1 -v

e2e-apple-container:
	SKIFF_APPLE_CONTAINER_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAppleContainerRustFSCaddyRollout -count=1 -v

e2e-apple-stateful:
	SKIFF_APPLE_STATEFUL_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run 'TestAppleStatefulGroupRustFSE2E|TestOpsemAppleStatefulHarness' -count=1 -v

e2e-apple-opsem-profiles:
	SKIFF_OPSEM_PROFILES_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestOpsemAppleOperationProfilesE2E -count=1 -v

e2e-apple-stateful-packages:
	SKIFF_APPLE_STATEFUL_PACKAGES_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run 'TestStatefulPackageValidationMatrixData|TestOpsemAppleOperationProfilesE2E' -count=1 -v

e2e-aws:
	SKIFF_AWS_E2E=1 $(GO_TEST_ENV) $(GO) test ./tests/e2e -run TestAWSE2E -count=1 -v

demo-local:
	./demos/quickstart-fake.sh

demo-test:
	./demos/test-local-demo.sh

demo-apple-container:
	./demos/apple-container-caddy.sh

demo-apple-postgres-ha:
	./demos/apple-postgres-ha.sh

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

runner-image-fmt:
	$(PACKER) fmt -check $(RUNNER_IMAGE_DIR)/packer.pkr.hcl

runner-image-init:
	$(PACKER) init $(RUNNER_IMAGE_DIR)

runner-image-archives:
	$(GO_TEST_ENV) OUT_DIR=$(RUNNER_IMAGE_VALIDATE_DIST) PLATFORMS='linux/amd64 linux/arm64' ./scripts/build-release.sh $(RUNNER_IMAGE_VERSION)

runner-image-validate: runner-image-fmt runner-image-init runner-image-archives
	$(PACKER) validate \
		-var region=$(RUNNER_IMAGE_REGION) \
		-var 'ami_regions=$(RUNNER_IMAGE_AMI_REGIONS)' \
		-var skiff_version=$(RUNNER_IMAGE_VERSION) \
		-var artifact_dir=$(RUNNER_IMAGE_VALIDATE_DIST) \
		-var channel=$(RUNNER_IMAGE_CHANNEL) \
		-var provenance_commit=$(RUNNER_IMAGE_PROVENANCE_COMMIT) \
		$(RUNNER_IMAGE_DIR)/packer.pkr.hcl
	$(GO_TEST_ENV) $(GO) test ./tests/packaging

runner-image-build: runner-image-fmt runner-image-init
	$(GO_TEST_ENV) OUT_DIR=$(RUNNER_IMAGE_DIST) PLATFORMS='linux/amd64 linux/arm64' ./scripts/build-release.sh $(RUNNER_IMAGE_VERSION)
	$(PACKER) build \
		-var region=$(RUNNER_IMAGE_REGION) \
		-var 'ami_regions=$(RUNNER_IMAGE_AMI_REGIONS)' \
		-var skiff_version=$(RUNNER_IMAGE_VERSION) \
		-var artifact_dir=$(RUNNER_IMAGE_DIST) \
		-var channel=$(RUNNER_IMAGE_CHANNEL) \
		-var provenance_commit=$(RUNNER_IMAGE_PROVENANCE_COMMIT) \
		$(RUNNER_IMAGE_DIR)/packer.pkr.hcl

clean:
	rm -rf bin
