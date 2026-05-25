BINARY_NAME := kibana-cli
MODULE      := github.com/fatecannotbealtered/kibana-cli
CMD_PATH    := ./cmd/kibana-cli
BIN_DIR     := bin
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "1.0.1")
LDFLAGS     := -s -w -X github.com/fatecannotbealtered/kibana-cli/cmd.version=$(VERSION)

.PHONY: build test test-all coverage vet fmt clean check-clean help

build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/$(BINARY_NAME) $(CMD_PATH)

test: fmt vet
	go test -race ./...

test-all: check-clean test

coverage:
	powershell -NoProfile -ExecutionPolicy Bypass -File scripts/coverage.ps1

check-clean:
	@bash scripts/check-clean.sh

vet:
	go vet ./...

fmt:
	@test -z "$$(gofmt -l .)" || (echo "Run gofmt -w ." && gofmt -l . && exit 1)

clean:
	rm -rf $(BIN_DIR)

help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //'
