GO ?= go

MODULE := github.com/s1liconcow/skiff
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X '$(MODULE)/internal/buildinfo.Version=$(VERSION)' -X '$(MODULE)/internal/buildinfo.Commit=$(COMMIT)' -X '$(MODULE)/internal/buildinfo.BuildDate=$(BUILD_DATE)'

.PHONY: build test vet fmt lint generate smoke clean

build:
	mkdir -p bin
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff ./cmd/skiff
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiffd ./cmd/skiffd
	$(GO) build -ldflags "$(LDFLAGS)" -o bin/skiff-runner ./cmd/skiff-runner

test:
	$(GO) test ./...

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
