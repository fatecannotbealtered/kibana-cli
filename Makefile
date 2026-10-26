BINARY_NAME := kibana-cli
MODULE      := github.com/fatecannotbealtered/kibana-cli
CMD_PATH    := ./cmd/kibana-cli
BIN_DIR     := bin

# Version from git tag, fallback to dev
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS     := -s -w -X $(MODULE)/cmd.version=$(VERSION)

# The aicli conformance suite runs inside `go test` (aicli.RunConformance, VER-001);
# this pattern selects it. Name the test TestConformance, as template/go/example does.
CONFORMANCE_RUN ?= Conformance

.PHONY: build test conformance vet lint fmt check-fmt check vuln spec-check spec-sync clean install snapshot help

## build: compile the binary into bin/
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_PATH)

## test: run all tests, conformance suite included, with race detection
test: vet
	go test -race ./...

## conformance: run only the aicli conformance suite (write gate, contract, coverage)
conformance:
	go test -count=1 -run '$(CONFORMANCE_RUN)' ./...

## vet: run static analysis
vet:
	go vet ./...

## check-fmt: verify formatting (fails if unformatted, Unix only)
check-fmt:
	@test -z "$$(gofmt -l .)" || (echo "Run 'make fmt' to fix formatting" && gofmt -l . && exit 1)

## fmt: apply gofmt formatting
fmt:
	gofmt -w .

## check: formatting + vet + vendored spec/library drift guard (Unix only)
check: check-fmt vet spec-check

## lint: run golangci-lint (install if missing); internal/aicli is excluded in .golangci.yml
lint:
	@which golangci-lint > /dev/null 2>&1 || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest)
	golangci-lint run ./...

## vuln: Go vulnerability audit, vendored internal/aicli included (install if missing)
vuln:
	@which govulncheck > /dev/null 2>&1 || (echo "Installing govulncheck..." && go install golang.org/x/vuln/cmd/govulncheck@latest)
	govulncheck ./...

## spec-check: verify .agent/, contract/ and internal/aicli/ match the pinned spec (.agent/SPEC_VERSION)
spec-check:
	node scripts/check-spec.js

## spec-sync: re-vendor .agent/, contract/ and internal/aicli/ at the pinned spec
spec-sync:
	node scripts/sync-spec.js

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR) dist

## install: build and install to GOPATH/bin
install:
	go install -ldflags "$(LDFLAGS)" $(CMD_PATH)

## snapshot: build a local goreleaser snapshot (no publish)
snapshot:
	goreleaser release --snapshot --clean

## help: show this help
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | column -t -s ':'
