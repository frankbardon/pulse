.PHONY: build clean dist test test-heavy smoke contrib contrib-tidy cover fmt fmt-check vet lint bench bench-scale sites tzdata reference reference-optimize reference-mcmc docs docs-generate docs-serve docs-clean

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

# Two test tiers. `make test` is the fast tier: every untagged _test.go
# file. Tests that take >= 1 s live in *_heavy_test.go files that start
# with `//go:build heavy`; `make test-heavy` adds them (it runs both
# tiers). Helpers both tiers use stay in untagged files.
test:
	$(GO) test $(TESTFLAGS) ./...

test-heavy:
	$(GO) test -tags heavy $(TESTFLAGS) ./...

# smoke builds, vets and tests internal/embeddersmoke: a separate Go
# module (own go.mod, replace => ../..) that embeds Pulse through the
# exported surface only, so an API an embedder needs that slipped under
# internal/ fails here. The root ./... never descends into it (nested
# go.mod). -mod=mod lets a root dependency bump flow through without a
# hand-run `go mod tidy` in the nested module.
smoke:
	cd internal/embeddersmoke && GOFLAGS=-mod=mod $(GO) vet ./... && GOFLAGS=-mod=mod $(GO) test -count=1 ./...

# contrib vets, lints (staticcheck), checks tidiness of and tests every
# nested module under contrib/ (the otelpulse / prompulse observability
# adapters): separate Go modules with their own go.mod +
# go.sum, `require pulse` at the matching root version and
# `replace => ../..` for in-repo development. Unlike
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

# gofmt from the selected toolchain (GOTOOLCHAIN), so local `GOTOOLCHAIN=go1.26.1 make lint` matches CI.
GOFMT := $(shell go env GOROOT)/bin/gofmt

fmt:
	$(GOFMT) -w .

# fmt-check fails on any Go file gofmt would rewrite (the nested smoke and
# contrib modules included). It is part of `make lint`, which is what the CI
# `lint` job runs, so local lint == CI lint; `make fmt` fixes what it lists.
fmt-check:
	@out=$$($(GOFMT) -l .); if [ -n "$$out" ]; then echo "gofmt needed (run make fmt):"; echo "$$out"; exit 1; fi

# vet and staticcheck check both test tiers: the fast tier alone (a fast
# file must not lean on a heavy-only helper) and with -tags heavy.
# staticcheck -merge combines the two runs, so a helper only heavy tests
# use is not reported unused (U1000) by the fast-tier run.
vet:
	$(GO) vet ./...
	$(GO) vet -tags heavy ./...

lint: fmt-check vet
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
		$(GO) run $(STATICCHECK) -f binary ./... > "$$tmp/fast.bin"; \
		$(GO) run $(STATICCHECK) -f binary -tags heavy ./... > "$$tmp/heavy.bin"; \
		$(GO) run $(STATICCHECK) -merge "$$tmp/fast.bin" "$$tmp/heavy.bin"

# bench runs the in-tree benchmark suite. Manual target — not wired into
# `make test`. Covers the service-layer crosstab + scan benches, the
# matrix scale benches at their default tier (1M rows x p50 —
# matrix_scale_bench_test.go; the cohort is generated once into
# $PULSE_BENCH_DIR, default <tmp>/pulse-bench), the linalg CoMoment
# benches (p 8 / 50 / 256), the encoding-layer codec benches, and the
# root-package point-lookup
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
	$(GO) test -bench=. -benchmem -run='^$$' -count=1 . ./internal/service/... ./encoding/... ./internal/processing/... ./linalg/...

# bench-scale adds the opt-in matrix scale tiers — 1M x p256, 10M x p50
# and 10M x p256 — to the default 1M x p50 one (-matrix-scale). Manual
# and never in CI: the cohorts are stream-generated once into
# $PULSE_BENCH_DIR (default <tmp>/pulse-bench) and need ~55 GB there with
# their 8-shard archives; the pairwise 10M x p256 cases run for tens of
# minutes. Baseline: docs/internals/BENCHMARKS_BASELINE.md.
bench-scale:
	$(GO) test -bench=MatrixScale -benchmem -benchtime=1x -run='^$$' -count=1 -timeout=0 ./internal/service/ -args -matrix-scale

# sites prints the change-site checklist for an operator or a
# Request/Response slot: every file + declaration to edit, the gate
# tests to rerun and the goldens to regenerate, derived from code
# (registries, AST, classified identifier occurrences, go/types for
# slots). Development tool only (internal/tools/sites). Examples:
#   make sites ARGS="op MAT_PCA"
#   make sites ARGS="new-op overlay"
#   make sites ARGS="slot request matrices"
#   make sites ARGS="--json categories"
# Recipe: docs/src/internals/finding-change-sites.md.
sites:
	$(GO) run ./internal/tools/sites $(ARGS)

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
# primitives (Student t, chi-square, noncentral chi-square, F, normal,
# Kolmogorov, studentized range) and the multivariate matrix and test outputs (mv_*.json, written
# by scripts/reference/gen_multivariate.R and the gen_multivariate_tests.R,
# gen_rotation.R and gen_factor.R it sources) into $(REFERENCE_DIR), writes
# the fixture CSVs under $(REFERENCE_DIR)/fixtures and their .pulse
# twins (internal/tools/refcohorts), then appends the `// golden-hash:`
# footer TestGoldensNotHandEdited checks. Needs Rscript + jsonlite and
# the pinned psych / car / ppcor / perturb / corpcor / Matrix / Hotelling /
# GPArotation / sandwich / lmtest / biotools / pROC (the full list with versions,
# install lines and the Homebrew Fortran workaround:
# docs/src/internals/regenerating-goldens.md, The R toolchain). Manual target: CI
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

# reference-optimize regenerates the optimize package's R oracles
# (optimize/testdata/least_squares.json vs minpack.lm::nlsLM,
# minimize.json vs nloptr NLOPT_LD_SLSQP) with
# optimize/testdata/gen_optimize.R, then appends the `// golden-hash:`
# footer TestOptimize_OracleGoldensHashed checks. Needs Rscript +
# jsonlite, minpack.lm 1.2-4 (install with the Homebrew Fortran
# workaround in docs/src/internals/regenerating-goldens.md) and nloptr
# 2.2.1. Manual target: CI never runs R.
OPTIMIZE_REFERENCE_DIR=optimize/testdata
reference-optimize:
	Rscript $(OPTIMIZE_REFERENCE_DIR)/gen_optimize.R $(OPTIMIZE_REFERENCE_DIR)
	@for f in $(OPTIMIZE_REFERENCE_DIR)/*.json; do \
		h=$$(shasum -a 256 "$$f" | cut -d' ' -f1); \
		printf '\n// golden-hash: %s\n' "$$h" >> "$$f"; \
	done

# reference-mcmc regenerates the mcmc package's ArviZ oracles
# (mcmc/testdata/diagnostics.json: RHat / ESSBulk / ESSTail vs
# arviz_stats rhat(method="rank") / ess(method="bulk" | "tail")) with
# mcmc/testdata/gen_diagnostics.py, then appends the `// golden-hash:`
# footer TestMCMC_OracleGoldensHashed checks. Needs a Python with the
# pinned arviz-stats 1.3.3, numpy 2.5.4 and scipy 1.18.1 (set PYTHON to a
# venv's interpreter; the script's header has the install line). Manual
# target: CI never runs Python.
PYTHON ?= python3
MCMC_REFERENCE_DIR=mcmc/testdata
reference-mcmc:
	$(PYTHON) -I $(MCMC_REFERENCE_DIR)/gen_diagnostics.py $(MCMC_REFERENCE_DIR)
	@for f in $(MCMC_REFERENCE_DIR)/*.json; do \
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
DOCS_GUIDE_ENV=env -u PULSE_LABEL_TABLES_DIR -u PULSE_RANGE_TABLES_DIR -u PULSE_POPULATIONS_DIR -u PULSE_TEMPLATES_DIR -u PULSE_FEATURE_PROFILE

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
