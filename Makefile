.PHONY: build clean dist test smoke contrib contrib-tidy cover fmt vet lint bench tzdata reference docs docs-generate docs-serve docs-clean

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

# contrib vets, lints (staticcheck), checks tidiness of and tests every
# nested adapter module under contrib/ (otelpulse, prompulse): separate Go
# modules with their own go.mod + go.sum, `require pulse` at the matching
# root version and `replace => ../..` for in-repo development. Unlike
# `make smoke` these are PUBLISHED modules, so there is no -mod=mod: a
# stale go.mod/go.sum fails here (and in CI) instead of reaching a tag or
# an IDE. A root dependency bump therefore needs `make contrib-tidy` in
# the same PR. Lockstep release tags: scripts/release-contrib.sh.
CONTRIB_MODULES=$(patsubst %/go.mod,%,$(wildcard contrib/*/go.mod))
STATICCHECK=honnef.co/go/tools/cmd/staticcheck@latest

contrib:
	@set -e; for m in $(CONTRIB_MODULES); do \
		echo "contrib: $$m"; \
		(cd $$m && $(GO) mod tidy -diff && $(GO) vet ./... && $(GO) run $(STATICCHECK) ./... && $(GO) test -count=1 ./...); \
	done

# contrib-tidy re-tidies every contrib module (after a root dependency
# bump, or a contrib pin change).
contrib-tidy:
	@set -e; for m in $(CONTRIB_MODULES); do \
		echo "contrib-tidy: $$m"; (cd $$m && $(GO) mod tidy); \
	done

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
	$(GO) test -bench=. -benchmem -run='^$$' -count=1 . ./internal/service/... ./encoding/... ./internal/processing/...

# tzdata refreshes Pulse's embedded tz database from the active Go
# toolchain's $GOROOT/lib/time/zoneinfo.zip and prints the IANA release
# (the DATA= line of the toolchain's lib/time/update.bash) and the new
# SHA-256. Record both in internal/temporal/tzdata.go (TZDataVersion,
# tzdataSHA256) — TestTZDataVersion_MatchesEmbeddedZip fails until you
# do. Recipe: docs/src/internals/refreshing-tzdata.md.
tzdata:
	cp "$$($(GO) env GOROOT)/lib/time/zoneinfo.zip" internal/temporal/zoneinfo.zip
	@echo "TZDataVersion = $$(sed -n 's/^DATA=//p' "$$($(GO) env GOROOT)/lib/time/update.bash")"
	@echo "tzdataSHA256  = $$(shasum -a 256 internal/temporal/zoneinfo.zip | cut -d' ' -f1)"

# reference regenerates the R oracle goldens for the shared statistical
# primitives (Student t, chi-square, F, normal, Kolmogorov, studentized
# range) and the multivariate matrix outputs (mv_*.json, written by
# scripts/reference/gen_multivariate.R) into $(REFERENCE_DIR), writes
# the fixture CSVs under $(REFERENCE_DIR)/fixtures and their .pulse
# twins (internal/tools/refcohorts), then appends the `// golden-hash:`
# footer TestGoldensNotHandEdited checks. Needs Rscript + jsonlite and
# the pinned psych / car / ppcor / perturb / corpcor / Matrix (versions
# and install line in gen_multivariate.R's header). Manual target: CI
# never runs R, the goldens are committed. Consumed by
# internal/processing/reference_oracle_test.go,
# internal/statdist/reference_oracle_test.go and the matrix-operator
# tests; internal/tools/refcohorts tests pin the fixtures.
REFERENCE_DIR=internal/processing/testdata/reference
reference:
	Rscript scripts/reference/gen_reference.R $(REFERENCE_DIR)
	go run ./internal/tools/refcohorts -dir $(REFERENCE_DIR)/fixtures
	@for f in $(REFERENCE_DIR)/*.json; do \
		h=$$(shasum -a 256 "$$f" | cut -d' ' -f1); \
		printf '\n// golden-hash: %s\n' "$$h" >> "$$f"; \
	done

# docs regenerates the committed Analysis Guide (docs/src/guide/**, the
# instance-wide reference rendered from the guidance registries), splices
# its SUMMARY.md entries into docs/src/SUMMARY.md between the
# docgen:summary lines, then builds the book. The export runs with the
# table/template directory env vars cleared so a developer's .env cannot
# perturb the committed tree; TestDocsGeneratedCurrent fails CI when the
# tree is stale. Recipe: docs/src/internals/regenerating-goldens.md.
DOCS_GUIDE_ENV=env -u PULSE_LABEL_TABLES_DIR -u PULSE_RANGE_TABLES_DIR -u PULSE_TEMPLATES_DIR -u PULSE_FEATURE_PROFILE

docs: docs-generate
	mdbook build docs

docs-generate:
	$(DOCS_GUIDE_ENV) $(GO) run ./cmd/pulse docs export --out docs/src/guide
	$(GO) run ./internal/tools/docsummary -book docs/src/SUMMARY.md -fragment docs/src/guide/SUMMARY.md -prefix guide/

docs-serve:
	mdbook serve docs --open

docs-clean:
	rm -rf docs/book

.DEFAULT_GOAL := build
