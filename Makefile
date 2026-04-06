# Binary output name (always migrate)
BINARY_NAME := migrate

# Default to the host platform; override for cross-compile, e.g.:
#   make build GOOS=linux GOARCH=amd64
#   make build GOOS=linux GOARCH=arm64
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Go has no "go lint" subcommand; staticcheck is the common replacement.
# Default tracks recent Go; on older toolchains use e.g. STATICCHECK_VERSION=2024.1.1
STATICCHECK_VERSION ?= 2025.1.1

.PHONY: build clean help fmt vet lint check

## build: compile $(BINARY_NAME) for GOOS/GOARCH (defaults to this machine)
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY_NAME) .

## fmt: go fmt all packages (rewrites files in place)
fmt:
	go fmt ./...

## vet: run go vet on all packages
vet:
	go vet ./...

## lint: run staticcheck ./... (override STATICCHECK_VERSION=... or use latest if your Go needs it)
lint:
	go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION) ./...

## check: vet then lint
check: vet lint

## clean: remove built binary
clean:
	rm -f $(BINARY_NAME)

## help: show targets
help:
	@echo "Targets:"
	@echo "  make build              Build $(BINARY_NAME) for this host (GOOS=$(shell go env GOOS) GOARCH=$(shell go env GOARCH))"
	@echo "  make build GOOS=... GOARCH=...   Cross-compile (examples below)"
	@echo "  make fmt                go fmt ./..."
	@echo "  make vet                go vet ./..."
	@echo "  make lint               staticcheck ./... (STATICCHECK_VERSION=$(STATICCHECK_VERSION), override if needed)"
	@echo "  make check              vet + lint"
	@echo "  make clean              Remove $(BINARY_NAME)"
	@echo "  make help               This message"
	@echo ""
	@echo "Cross-compile examples:"
	@echo "  make build GOOS=linux   GOARCH=amd64"
	@echo "  make build GOOS=linux   GOARCH=arm64"
	@echo "  make build GOOS=darwin  GOARCH=amd64"
	@echo "  make build GOOS=darwin  GOARCH=arm64"
	@echo "  make build GOOS=windows GOARCH=amd64"
