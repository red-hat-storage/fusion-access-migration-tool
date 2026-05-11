# Binary output name (always migrate)
BINARY_NAME := migrate

# Use checked-in vendor/ so builds work without GOPROXY (air-gapped builders).
GOFLAGS ?= -mod=vendor
export GOFLAGS

# Default to the host platform; override for cross-compile, e.g.:
#   make build GOOS=linux GOARCH=amd64
#   make build GOOS=linux GOARCH=arm64
GOOS   ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)

# Lint uses staticcheck registered in go.mod (go tool); bump via: go get -tool honnef.co/go/tools/cmd/staticcheck@VERSION

IMAGE ?= fusion-access-migration-tool
TAG ?= latest
REGISTRY ?=quay.io/ocs-dev
CONTAINER_TOOL ?= podman
PLATFORMS ?= linux/amd64,linux/arm64
IMAGE_REF := $(if $(REGISTRY),$(REGISTRY)/,)$(IMAGE):$(TAG)
# Cluster RBAC, namespaces, etc. (apply before the Job)
RESOURCES_MANIFEST ?= deploy/resources.yaml
# Kubernetes Job only (image updated by job-image-manifest)
JOB_MANIFEST ?= deploy/migration-job.yaml
JOB_CONTAINER ?= migrate

.PHONY: build clean help fmt vet lint check vendor image image-push image-buildx job-image-manifest

## build: compile $(BINARY_NAME) for GOOS/GOARCH (defaults to this machine)
build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(BINARY_NAME) ./cmd/migrate

## fmt: go fmt all packages (rewrites files in place)
fmt:
	go fmt ./...

## vet: run go vet on all packages
vet:
	go vet ./...

## lint: run staticcheck ./... (version pinned in go.mod tool directive)
lint:
	go tool honnef.co/go/tools/cmd/staticcheck ./...

## check: vet then lint
check: vet lint

## vendor: refresh vendor/ from go.mod (clears GOFLAGS so modules can resolve when online)
vendor:
	env GOFLAGS= go mod vendor

## image: build container image with Dockerfile
image:
	$(CONTAINER_TOOL) build --platform linux/amd64 -t $(IMAGE_REF) .

## image-push: push container image to registry
image-push:
	$(CONTAINER_TOOL) push $(IMAGE_REF)

## image-buildx: build and push multi-arch image (requires docker buildx)
image-buildx:
	$(CONTAINER_TOOL) buildx build --platform $(PLATFORMS) -t $(IMAGE_REF) --push .

## job-image-manifest: update $(JOB_MANIFEST) container image to IMAGE_REF
job-image-manifest:
	@awk -v image="$(IMAGE_REF)" -v container="$(JOB_CONTAINER)" '\
		/^[[:space:]]*-[[:space:]]*name:[[:space:]]*/ { \
			name=$$0; \
			sub(/^[[:space:]]*-[[:space:]]*name:[[:space:]]*/, "", name); \
			gsub(/[[:space:]]+$$/, "", name); \
			in_container=(name==container); \
		} \
		in_container && /^[[:space:]]*image:[[:space:]]*/ { \
			sub(/image:[[:space:]].*/, "image: " image); \
			in_container=0; \
			updated=1; \
		} \
		{ print } \
		END { if (!updated) exit 2 } \
	' "$(JOB_MANIFEST)" > "$(JOB_MANIFEST).tmp" \
	&& mv "$(JOB_MANIFEST).tmp" "$(JOB_MANIFEST)" \
	&& echo "Updated $(JOB_MANIFEST) image to $(IMAGE_REF)"

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
	@echo "  make lint               staticcheck ./... (see go.mod tool honnef.co/go/tools/cmd/staticcheck)"
	@echo "  make check              vet + lint"
	@echo "  make vendor             go mod vendor (update vendor/ after go.mod changes)"
	@echo "  make image              Build container image $(IMAGE_REF)"
	@echo "  make image-push         Push container image $(IMAGE_REF)"
	@echo "  make image-buildx       Build and push multi-arch image $(IMAGE_REF) (PLATFORMS=$(PLATFORMS))"
	@echo "  make job-image-manifest Update $(JOB_MANIFEST) (Job manifest) image to $(IMAGE_REF)"
	@echo "  make clean              Remove $(BINARY_NAME)"
	@echo "  make help               This message"
	@echo ""
	@echo "Container build variables:"
	@echo "  IMAGE=$(IMAGE)"
	@echo "  TAG=$(TAG)"
	@echo "  REGISTRY=$(REGISTRY)"
	@echo "  CONTAINER_TOOL=$(CONTAINER_TOOL)"
	@echo "  PLATFORMS=$(PLATFORMS)"
	@echo "  RESOURCES_MANIFEST=$(RESOURCES_MANIFEST)"
	@echo "  JOB_MANIFEST=$(JOB_MANIFEST)"
	@echo "  JOB_CONTAINER=$(JOB_CONTAINER)"
	@echo ""
	@echo "Cross-compile examples:"
	@echo "  make build GOOS=linux   GOARCH=amd64"
	@echo "  make build GOOS=linux   GOARCH=arm64"
	@echo "  make build GOOS=darwin  GOARCH=amd64"
	@echo "  make build GOOS=darwin  GOARCH=arm64"
	@echo "  make build GOOS=windows GOARCH=amd64"
	@echo ""
	@echo "Container examples:"
	@echo "  make image TAG=v0.1.0"
	@echo "  make image REGISTRY=quay.io/myorg TAG=v0.1.0"
	@echo "  make image-push REGISTRY=quay.io/myorg TAG=v0.1.0"
	@echo "  make image-buildx REGISTRY=quay.io/myorg TAG=v0.1.0"
	@echo "  make job-image-manifest REGISTRY=quay.io/myorg TAG=v0.1.0"
