---
id: U20
slug: observability
title: "Hosts can see what Pulse is doing, whether or not they are a server"
track: Embedder operations
size: M
status: not-started
depends_on: [U02, U02b]
soft_depends_on: [U01]
blocks: [U32]
todo_items: [118, 119, 120, 121, 122, 123, 222, 223]
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

- [ ] **#118** (9. Embedder operations › Observability) `Options.Logger` (`slog`, nil = silent); context-aware; no row data
- [ ] **#119** (9. Embedder operations › Observability) `Options.Hooks`: operation start/end (context-returning), phase timings; panic-safe
- [ ] **#120** (9. Embedder operations › Observability) `Options.Metrics` interface (opt-in) with bounded labels
- [ ] **#121** (9. Embedder operations › Observability) `contrib/otelpulse` and `contrib/prompulse` as separate modules
- [ ] **#122** (9. Embedder operations › Observability) `pulse mcp` / CLI `--log-level`, `--log-format`, opt-in `--metrics-addr`
- [ ] **#123** (9. Embedder operations › Observability) Silence, no-row-data, dependency and panic gates; "Observability" docs page

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

- [ ] Default `Options` produce no output and call no hook
- [ ] No log attribute ever contains a record value (capturing-handler test at Debug)
- [ ] The core `go.mod` has no OTel/Prometheus dependency
- [ ] An OTel-instrumented sample host shows Pulse spans nested under its request span
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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
