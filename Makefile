.PHONY: build test lint build-image clean fmt vet

VERSION			?= $(shell git describe --tags --always)
TREE_STATE      ?= $(shell git describe --always --dirty --exclude='*' | grep -q dirty && echo dirty || echo clean)
COMMIT			?= $(shell git rev-parse HEAD)
BUILD_DATE		?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GO_FLAGS		:= -ldflags "-X 'k8s.io/component-base/version.gitVersion=$(VERSION)' -X 'k8s.io/component-base/version.gitTreeState=$(TREE_STATE)' -X 'k8s.io/component-base/version.buildDate=$(BUILD_DATE)' -X 'k8s.io/component-base/version.gitCommit=$(COMMIT)'"
SOURCES			:= $(shell find . -name '*.go')
UPX_FLAGS		?= -qq

IMAGE ?= ghcr.io/kommodity-io/kommodity-autobootstrap-extension
CONTAINER_RUNTIME ?= docker

LINTER := bin/golangci-lint
GOLANGCI_LINT_VERSION := v2.13.1

.PHONY: golangci-lint
golangci-lint: $(LINTER) ## Download golangci-lint locally if necessary.
# install.sh cannot verify v2.12.0+: it greps the checksums file for the tarball
# basename, which also matches the sibling .tar.gz.sbom.json line.
$(LINTER):
	@mkdir -p bin
	@set -e; \
	OS=$$(go env GOOS); ARCH=$$(go env GOARCH); \
	V=$(GOLANGCI_LINT_VERSION); V=$${V#v}; \
	TARBALL=golangci-lint-$$V-$$OS-$$ARCH.tar.gz; \
	TMP=$$(mktemp -d); trap 'rm -rf $$TMP' EXIT; \
	curl -sSfL -o $$TMP/$$TARBALL https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/$$TARBALL; \
	curl -sSfL -o $$TMP/checksums.txt https://github.com/golangci/golangci-lint/releases/download/$(GOLANGCI_LINT_VERSION)/golangci-lint-$$V-checksums.txt; \
	WANT=$$(grep " $$TARBALL$$" $$TMP/checksums.txt | cut -d' ' -f1); \
	if [ -z "$$WANT" ]; then echo "no checksum found for $$TARBALL" >&2; exit 1; fi; \
	if command -v sha256sum >/dev/null 2>&1; then GOT=$$(sha256sum $$TMP/$$TARBALL | cut -d' ' -f1); else GOT=$$(shasum -a 256 $$TMP/$$TARBALL | cut -d' ' -f1); fi; \
	if [ "$$WANT" != "$$GOT" ]; then echo "golangci-lint checksum mismatch (expected $$WANT, got $$GOT)" >&2; exit 1; fi; \
	tar -xzf $$TMP/$$TARBALL -C $$TMP; \
	cp $$TMP/golangci-lint-$$V-$$OS-$$ARCH/golangci-lint $(LINTER)

# Build the binary for Linux
build: $(SOURCES) ## Build the application.
	go build $(GO_FLAGS) -o bin/kommodity-autobootstrap-extension ./cmd/kommodity-autobootstrap-extension/main.go
ifneq ($(UPX_FLAGS),)
	upx $(UPX_FLAGS) bin/kommodity-autobootstrap-extension
endif

# Run tests
test:
	go test -v -race ./...

# Run tests with coverage
test-coverage:
	go test -v -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

## Run the linter.
lint: $(LINTER)
	$(LINTER) run

## Run the linter and fix issues.
lint-fix: $(LINTER)
	$(LINTER) run --fix

# Format code
fmt:
	go fmt ./...

# Run go vet
vet:
	go vet ./...

# Build container image
build-image: ## Build the Docker image.
	${CONTAINER_RUNTIME} buildx build \
	-f Containerfile \
	-t ${IMAGE}:latest \
	. \
	--build-arg VERSION=$(VERSION) \
	--load

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f coverage.out coverage.html

# Download dependencies
deps:
	go mod download
	go mod tidy

# Run all checks
check: fmt vet lint test
