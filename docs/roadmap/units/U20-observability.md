---
id: U20
slug: observability
title: "Hosts can see what Pulse is doing, whether or not they are a server"
track: Embedder operations
size: M
status: done
depends_on: [U02, U02b]
soft_depends_on: [U01]
blocks: [U32]
todo_items: [118, 119, 120, 121, 122, 123, 222, 223, 227]
branch: observability
---

# U20 — observability

**Outcome:** Hosts can see what Pulse is doing, whether or not they are a server.

**Track:** Embedder operations · **Size:** M · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) (landed: `extend` public, `processing` internal) · **Soft:** [U01](U01-release-pipeline.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`Options.Logger` (slog, silent by default, never logs row data), `Options.Hooks` (context-returning operation start/end + phase timings, panic-safe), opt-in `Options.Metrics` interface with bounded labels, `contrib/otelpulse` and `contrib/prompulse` as separate modules, and `pulse mcp` / CLI flags.

## References

**Theme documents (read before starting):**
- [embedder-operations 02 — Observability](../v1.0.0-embedder-operations/02-observability.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#118** (9. Embedder operations › Observability) `Options.Logger` (`slog`, nil = silent); context-aware; no row data
- [x] **#119** (9. Embedder operations › Observability) `Options.Hooks`: operation start/end (context-returning), phase timings; panic-safe
- [x] **#120** (9. Embedder operations › Observability) `Options.Metrics` interface (opt-in) with bounded labels
- [x] **#121** (9. Embedder operations › Observability) `contrib/otelpulse` and `contrib/prompulse` as separate modules
- [x] **#122** (9. Embedder operations › Observability) `pulse mcp` / CLI `--log-level`, `--log-format`, opt-in `--metrics-addr`
- [x] **#123** (9. Embedder operations › Observability) Silence, no-row-data, dependency and panic gates; "Observability" docs page

## Scope

**In scope**
- Logger, hooks, metrics interface
- Two contrib modules
- MCP/CLI flags; metrics port only on request
- Gates + docs page

**Out of scope**
- Pulse opening ports by default (never)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(observability/E<n>-S<m>): …`; close each epic with `milestone(observability/E<n>): vertical slice complete — <epic title>`.

### E1 — Hosts can log and time operations
- S1: `Options.Logger` with context-aware calls at documented levels
- S2: `Options.Hooks` start/end + phases; panic recovery
- S3: silence + no-row-data + panic gates

### E2 — Metrics, opt-in, without vendor lock-in
- S1: `Options.Metrics` + documented metric set
- S2: `contrib/otelpulse` (spans via hooks + metrics), `contrib/prompulse`
- S3: `pulse mcp --log-level/--log-format/--metrics-addr`; docs page

## Acceptance criteria

- [x] Default `Options` produce no output and call no hook
- [x] No log attribute ever contains a record value (capturing-handler test at Debug)
- [x] The core `go.mod` has no OTel/Prometheus dependency
- [x] An OTel-instrumented sample host shows Pulse spans nested under its request span
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestObservabilityDefaultsSilent`
- `TestObservabilityNoRowData`
- `TestCoreModuleNoObservabilityDeps`
- `TestHookPanicRecovered`

## Update Demand companions

- CLAUDE.md Knobs/Options paragraph
- `docs/src/library/` "Observability" page
- `docs/src/cli/flags.md`
- Release workflow builds/tests the contrib modules (coordinate with U01)

## Human inputs & decisions

- None.

## Inherited from U18

- **`pulse mcp` doesn't show the effective `return` preset** (#222). U18 made MCP tool calls default to `standard`, overridable by `pulse mcp --return`, `gosdk.Config.DefaultReturn` / `mcpserve.Options.DefaultReturn` and the feature profile's `return`. The startup line and `ServeInfo` were left unchanged, so an operator can't see which preset agents get. Add it alongside the other `pulse mcp` observability output.
- **`standard` preset's residual cost is unattributed** (#223). U18's `BenchmarkProcessDefaultReturn` (root `return_default_bench_test.go`) on darwin/arm64: unset 88.8 µs, `full` 89.6 µs, `standard` 97.7 µs; the cached plan resolution is 240 ns (~0.27%), so ~+9% comes from elsewhere — most likely the `returnshape.Apply` pruning pass. The operation hooks' phase timings should name a shaping phase so this is measurable in production, and a sub-benchmark should isolate Apply; optimise only if it dominates.

## Inherited from U19

- **`ServeInfo` omits effective limits** (#227). U19 added the manifest `limits` block + `limits_digest` and echoes tuned limits in the `pulse mcp` startup line (read from `p.Limits()`), but `mcpserve.Describe` / `ServeInfo` don't carry them. Add alongside #222.

## Landed

Shipped on `observability`, released as `v1.0.0-alpha.6` together with the two contrib module tags. Epics ran E1 logger and hooks, E2 plan/phase detail and child operations, E3 metrics and the `pulse mcp` / CLI flags, E4 vendor adapters, E5 docs. Guide: [Observability](../../src/library/observability.md); contract `.claude/reference/observability.md`; embedder rows in [03-embedder-migration](../v1.0.0-api-and-release/03-embedder-migration.md).

- **Surface (#118-#120).** `Options.Logger` (`*slog.Logger`, nil = silent, never `slog.Default()`), `Options.Hooks` (`*observe.Hooks`), `Options.Metrics` (`observe.Metrics`); all nil = zero cost with an allocation-identical off path (alloc-pin gate). New public package `observe` (stdlib-only vocabulary: `OperationKind`, `Phase`, `Arm`, `Scope` enums, instrument interfaces). No `PULSE_*` environment variable.
- **Deviations from the theme document.** The phase set is `plan, open, decode, scan, reduce, post, overlay, shape` (not `open | predict | decode | filter | aggregate | overlay | serialize`), reported as a per-operation summary just before `OnOperationEnd`, not in real time. Compose slots and ProcessChain stages are **child** operations (`scope=child`, parent's kind, `Parent`, `Index`). A `shape` phase exists (#223). Streaming operations end on drain, close, error or ctx cancel. `pulse_cache_hits_total` was dropped (no cache to count); added `pulse_phase_duration_seconds`, `pulse_hook_panics_total`, `pulse_operations_in_flight`. The Prometheus text exporter behind `--metrics-addr` is a stdlib implementation (`internal/obsprom`), so the core takes no Prometheus dependency; `prompulse` uses `client_golang` in its own module.
- **Metrics factory contract.** Instruments resolve lazily after `New` (only `code="ok"` and the small label spaces are eager), so a custom `observe.Metrics` factory must be concurrency-safe. `New` with metrics: about 0.33 ms, 0.40 MB.
- **Hosts (#121, #122).** `contrib/otelpulse` and `contrib/prompulse` are separate modules, tagged in lockstep with the root (`contrib/<m>/vX`); `make contrib` / `make contrib-tidy`; `release.yml` gained a `contrib` job. CLI `--log-level` / `--log-format` on every leaf (stderr only), `pulse mcp --metrics-addr`. `ServeInfo` gained `DefaultReturn` and `Limits` and the startup line moved to stderr after `Register`, which closes #222 and #227.
- **Gates (#123).** `TestObservabilityDefaultsSilent`, `TestObservabilityNoRowData`, `TestCoreModuleNoObservabilityDeps`, `TestHookPanicRecovered`, plus the allocation pin, `TestObservabilityDocCoversEnums` and `TestObservedMethodsCoverSurface`.
- **#223 outcome.** `BenchmarkReturnShapeApply` isolates the pruning pass: `Apply(standard)` costs about 4.2 µs (17 allocs) of the roughly 10.3 µs standard-versus-unset delta (about 11% of a 94.7 µs `Process`), ~40% and under the 50% bar, so it was NOT optimised. Cached plan resolution is 0.25 µs.
- **Left uninstrumented, by decision.** `CohortArtifacts`, `NewCohortBuilder`, `Imports`, `ResolveImport`, `ResolveCanonicalSchema`, `ApplySeriesOverlays`, `InvalidatedSidecars`, `Watch*` carry no `OperationKind` (listed in `uninstrumentedMethods`).

## Open follow-ups

Handed on; tracked in [TODO](../TODO.md) "Follow-ups from U20".

- #231 → [U34](U34-extension-validation.md): recover panics raised inside an operation into a coded error
- #232 → [U33](U33-v1-release.md): rehearse the lockstep contrib release on `rc.1`; keep `make contrib-tidy` green on root dependency bumps
- #233 → [U33](U33-v1-release.md): freeze decision on `mcpserve.ServeInfo.Limits` (non-comparable struct)
- #234 → [U33](U33-v1-release.md): confirm the deliberately uninstrumented `*Pulse` methods before the `observe` enum freezes
