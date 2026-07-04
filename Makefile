# defib Makefile
#
# See AGENTS.md for how these targets are used. Run `make check` before every PR.

GO ?= go
MODULE := github.com/ya222/defib
BIN_DIR := bin
BINARY := $(BIN_DIR)/defib

GOBIN := $(shell $(GO) env GOPATH)/bin
GOLANGCI_LINT ?= $(GOBIN)/golangci-lint
GOIMPORTS ?= $(GOBIN)/goimports

# Pinned tool versions (see `make tools`).
GOLANGCI_LINT_VERSION ?= v1.59.1
GOIMPORTS_VERSION ?= v0.24.0

.PHONY: all build test lint fmt check e2e tools clean

all: build

## build: compile the defib binary into ./bin/defib
build:
	$(GO) build -o $(BINARY) ./cmd/defib

## test: run all unit tests with the race detector
test:
	$(GO) test -race ./...

## lint: run golangci-lint
lint:
	$(GOLANGCI_LINT) run

## fmt: gofmt + goimports the tree
fmt:
	gofmt -s -w .
	@if [ -x "$(GOIMPORTS)" ]; then \
		"$(GOIMPORTS)" -w -local $(MODULE) .; \
	else \
		echo "goimports not installed; run 'make tools' (skipping import grouping)"; \
	fi

## check: fmt + lint + test — run before every PR
check: fmt lint test

## e2e: end-to-end tests using the fake provider
e2e:
	$(GO) test -race -tags e2e ./... -run E2E

## tools: install pinned dev tools (golangci-lint, goimports)
tools:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	$(GO) install golang.org/x/tools/cmd/goimports@$(GOIMPORTS_VERSION)

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)
