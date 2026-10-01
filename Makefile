.PHONY: build clean dist test smoke cover fmt vet lint bench docs docs-serve docs-clean

BINARY_NAME=pulse
BUILD_DIR=bin
DIST_DIR=dist
GO=go
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
LDFLAGS=-s -w -X github.com/frankbardon/pulse/internal/buildinfo.version=$(VERSION)
BUILD_FLAGS=-trimpath -ldflags="$(LDFLAGS)"

# Pulse is pure Go — no CGO dependency in the build graph. Disabling CGO
# globally makes that a contract: any future import that pulls in a C
# toolchain fails the build instead of silently re-introducing the
# dependency. Override on the command line if a downstream consumer
# really needs CGO (e.g. `make build CGO_ENABLED=1`).
export CGO_ENABLED=0

ifneq (,$(wildcard ./.env))
    include .env
    export
endif

build:
	$(GO) build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/pulse

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR) coverage.out

# dist cross-compiles the six release archives (linux/darwin/windows x
# amd64/arm64) plus checksums.txt into $(DIST_DIR) via
# scripts/release-dist.sh — the same script release.yml calls, so the
# tag-push outcome is reproducible locally: `make dist VERSION=v1.2.3`.
dist:
	LDFLAGS="$(LDFLAGS)" GO="$(GO)" ./scripts/release-dist.sh "$(VERSION)" "$(DIST_DIR)"

test:
	$(GO) test ./...

# smoke builds, vets and tests internal/embeddersmoke: a separate Go
# module (own go.mod, replace => ../..) that embeds Pulse through the
# exported surface only, so an API an embedder needs that slipped under
# internal/ fails here. The root ./... never descends into it (nested
# go.mod). -mod=mod lets a root dependency bump flow through without a
# hand-run `go mod tidy` in the nested module.
smoke:
	cd internal/embeddersmoke && GOFLAGS=-mod=mod $(GO) vet ./... && GOFLAGS=-mod=mod $(GO) test -count=1 ./...

cover:
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

lint: vet
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

# bench runs the in-tree benchmark suite. Manual target — not wired into
# `make test`. Covers the service-layer crosstab + scan benches, the
# encoding-layer codec benches, and the root-package point-lookup
# sidecar-presence benchmark (BenchmarkProcess_SidecarIndexPresence —
# compares Process with/without a sidecar index present; see
# no_sidecar_touch_bench_test.go); new bench packages should be added
# here. Reference numbers for the crosstab-perf epic come from
# BenchmarkBufferedProcessWideCohort in ./internal/service. Pipe output through
# `benchstat` against a saved baseline to catch wall-clock regressions
# — this target intentionally does not assert a threshold itself (see
# no_sidecar_touch_perf_test.go's `-tags=perf` opt-in gate for the one
# in-repo wall-clock assertion, kept out of CI on purpose).
bench:
	$(GO) test -bench=. -benchmem -run='^$$' -count=1 . ./internal/service/... ./encoding/... ./processing/...

docs:
	mdbook build docs

docs-serve:
	mdbook serve docs --open

docs-clean:
	rm -rf docs/book

.DEFAULT_GOAL := build
