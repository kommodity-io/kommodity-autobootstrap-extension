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

.PHONY: golangci-lint
golangci-lint: $(LINTER) ## Download golangci-lint locally if necessary.
$(LINTER):
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b bin/ v2.9.0

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
