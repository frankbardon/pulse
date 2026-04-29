.PHONY: build clean test cover fmt vet lint

BINARY_NAME=pulse
BUILD_DIR=bin
GO=go

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

build:
	$(GO) build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/pulse

clean:
	rm -rf $(BUILD_DIR) coverage.out

test:
	$(GO) test ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

lint: vet
	$(GO) run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./...

.DEFAULT_GOAL := build
