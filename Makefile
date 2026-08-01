SHELL := /bin/bash
BINARY := omp-sync
DIST := dist
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X github.com/donrami/omp-sync/internal/version.Version=$(VERSION)

.PHONY: all build test test-unit test-integration lint clean run release help

all: lint test build

help:
	@echo "Targets:"
	@echo "  build          - Compile the binary to ./bin/$(BINARY)"
	@echo "  test           - Run go test ./..."
	@echo "  test-unit      - Run unit tests only"
	@echo "  test-integration - Run integration tests (requires docker-compose)"
	@echo "  lint           - Run golangci-lint"
	@echo "  run            - Run the binary with --help"
	@echo "  clean          - Remove build artifacts"
	@echo "  release        - Cross-platform build via GoReleaser"

build:
	@mkdir -p bin
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

test:
	go test ./...

test-unit:
	go test -short ./internal/...

test-integration:
	go test -tags=integration ./tests/integration/...

lint:
	golangci-lint run

run: build
	./bin/$(BINARY) --help

clean:
	rm -rf bin $(DIST)

release:
	goreleaser release --clean
