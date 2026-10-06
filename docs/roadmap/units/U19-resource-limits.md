---
id: U19
slug: resource-limits
title: "Embedders can bound runaway requests, with defaults that never get in the way"
track: Embedder operations
size: M
status: not-started
depends_on: [U05]
soft_depends_on: [U16]
blocks: [U32]
todo_items: [82, 112, 113, 114, 115, 116, 117]
branch: resource-limits
---

# U19 — resource-limits

**Outcome:** Embedders can bound runaway requests, with defaults that never get in the way.

**Track:** Embedder operations · **Size:** M · **Depends on:** [U05](U05-profiles-enforcement.md) · **Soft:** [U16](U16-matrix-result.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

`Options.Limits` with high defaults (groups, crosstab cells, memory estimate, matrix dim, compose/chain fan-out, join build rows, optional timeout). Predict-time findings plus runtime checks, with `PULSE_LIMIT_EXCEEDED` carrying tuning fixups. The profile file gets a `limits` section, the manifest a `limits` block, and `pulse mcp` a `--limit` flag.

## References

**Theme documents (read before starting):**
- [embedder-operations 01 — Resource limits](../v1.0.0-embedder-operations/01-resource-limits.md) — whole document
- [embedder-operations 00 — Overview](../v1.0.0-embedder-operations/00-overview.md) — stance

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#112** (9. Embedder operations › Resource limits) `Options.Limits` with high defaults; validation at `pulse.New`
- [ ] **#113** (9. Embedder operations › Resource limits) Predict-time checks + `PredictResult.LimitFindings`
- [ ] **#114** (9. Embedder operations › Resource limits) Runtime checks (groups, crosstab cells, join build, matrix dim, compose / chain fan-out, memory estimate, timeout)
- [ ] **#115** (9. Embedder operations › Resource limits) `PULSE_LIMIT_EXCEEDED` with tuning fixups
- [ ] **#116** (9. Embedder operations › Resource limits) Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit`
- [ ] **#117** (9. Embedder operations › Resource limits) Defaults-never-trip, predict/runtime parity and memory-release gates; "Tuning limits" docs page

## Scope

**In scope**
- Limits + validation
- Predict findings + runtime checks + error/fixups
- Profile/manifest/MCP surfaces
- Gates + "Tuning limits" docs

**Out of scope**
- Output row caps (decided: not added)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(resource-limits/E<n>-S<m>): …`; close each epic with `milestone(resource-limits/E<n>): vertical slice complete — <epic title>`.

### E1 — Limits exist and predict sees them
- S1: `Options.Limits` + defaults + validation
- S2: predict `LimitFindings`; process refuses before scanning

### E2 — Runtime enforcement that teaches tuning
- S1: runtime checks in grouper, crosstab, join build, compose/chain, matrix (U16 landed: enforce `MaxMatrixDim` and the buckets x p^2 guard)
- S2: `PULSE_LIMIT_EXCEEDED` + fixups; memory released on trip
- S3: feature-profile `limits` (U04's strict decode refuses the key as unknown until this story adds it to `pulse.FeatureProfile` and `.claude/reference/feature-profiles.md`), manifest `limits`, `pulse mcp --limit`; docs page

## Acceptance criteria

- [ ] Every example and golden request runs under default limits with no finding
- [ ] A limit predict says will trip does trip at runtime with the same code, and vice versa where knowable
- [ ] After a runtime trip, buffered state is released (heap check)
- [ ] Every limit error names the option to raise and its current value
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestLimitsDefaultsNeverTripGoldens`
- `TestLimitsPredictRuntimeParity`
- `TestLimitsReleaseMemory`

## Update Demand companions

- `errors/fixup_metadata.go`
- CLAUDE.md Knobs paragraph (limits)
- `docs/src/library/` "Tuning limits" page
- `docs/src/cli/flags.md` (`--limit`)

## Human inputs & decisions

- None.

## Notes

- U16 landed first and enforces nothing: `MaxMatrixDim` (TODO #82's limit half) and a buckets x p^2 guard are this unit's. Predict already reports per matrix `shape`, `accumulator_bytes` and, for grouped requests, `bucket_basis` / `estimated_buckets` / `estimated_cells` / `estimated_bytes` (`PredictResult.matrices`, `internal/vectors/matrix_rules.go`) as an upper bound with no guard: wire the limit findings to those figures, and refuse a matrix over the cap at predict with `PULSE_LIMIT_EXCEEDED`. `accumulator_bytes` is the state of ONE merge block; a run holds one per populated block per bucket, so count the real bytes (blocks x buckets) when sizing the memory estimate. Contract: `.claude/reference/matrix-and-vectors.md` (Predict).
