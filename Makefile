.PHONY: build clean test cover fmt vet lint docs docs-serve docs-clean

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
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

docs:
	mdbook build docs

docs-serve:
	mdbook serve docs --open

docs-clean:
	rm -rf docs/book

.DEFAULT_GOAL := build
