---
id: U19
slug: resource-limits
title: "Embedders can bound runaway requests, with defaults that never get in the way"
track: Embedder operations
size: M
status: done
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

- [x] **#112** (9. Embedder operations › Resource limits) `Options.Limits` with high defaults; validation at `pulse.New`
- [x] **#113** (9. Embedder operations › Resource limits) Predict-time checks + `PredictResult.LimitFindings`
- [x] **#114** (9. Embedder operations › Resource limits) Runtime checks (groups, crosstab cells, join build, matrix dim, compose / chain fan-out, memory estimate, timeout)
- [x] **#115** (9. Embedder operations › Resource limits) `PULSE_LIMIT_EXCEEDED` with tuning fixups
- [x] **#116** (9. Embedder operations › Resource limits) Profile-file `limits` section; manifest `limits` block; `pulse mcp --limit`
- [x] **#117** (9. Embedder operations › Resource limits) Defaults-never-trip, predict/runtime parity and memory-release gates; "Tuning limits" docs page

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
- S3: feature-profile `limits` (landed as `pulse.FeatureProfile.Limits`, a `*FeatureProfileLimits`; U04's strict decode now accepts the key and validates its values), manifest `limits`, `pulse mcp --limit`; docs page

_As planned; the shipped epics are listed under Landed._

## Acceptance criteria

- [x] Every example and golden request runs under default limits with no finding
- [x] A limit predict says will trip does trip at runtime with the same code, and vice versa where knowable
- [x] After a runtime trip, buffered state is released (heap check)
- [x] Every limit error names the option to raise and its current value
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

## Landed

Shipped on `resource-limits` (no release tag cut by the unit). Epics ran E1 fan-out and dimension limits, E4 host surfaces, E2 data-dependent limits, E3 cancellation and timeout, E5 gates and docs. Guide: [Tuning Limits](../../src/library/tuning-limits.md); embedder rows in [03-embedder-migration](../v1.0.0-api-and-release/03-embedder-migration.md).

- **Surface (#112, #115).** `pulse.Limits` (alias of `internal/limits.Limits`), `pulse.Unlimited`, `pulse.Default*`, `Options.Limits`, `Pulse.Limits()`. One encoding on every layer: `0` = default, `-1` = unlimited, other negatives `PULSE_LIMIT_INVALID`. Per-field precedence `Options.Limits`, profile, default. `PULSE_LIMIT_EXCEEDED` details `{limit, configured, observed, option}`.
- **Predict (#113).** `PredictResult.LimitFindings` with a grade: `certain` (also refuses, `valid` false) or `possible` (informational). `MaxGroups` and `MaxCrosstabCells` are `possible`; the rest `certain`. One rule shared with the runtime pre-flight (`LimitRefusal`), so a `certain` breach is refused before any record decodes.
- **Runtime (#114).** Counted at every bucket mint, both crosstab arms, the join build, the matrix slot, Compose, chain and Facet; memory released on a trip (`TestLimitsReleaseMemory`).
- **Hosts (#116).** Profile `limits` section (`pulse.FeatureProfileLimits`), manifest `limits` + `limits_digest`, `pulse mcp --limit`.
- **Gates (#117).** `TestLimitsDefaultsNeverTripGoldens`, `TestLimitsPredictRuntimeParity`, `TestLimitsReleaseMemory`; the Tuning Limits page.
- **Deviations from the theme document.** `MaxEstimatedMemory` defaults to unlimited (no `GOMEMLIMIT` derivation); findings carry a `certain` / `possible` grade; the manifest carries `limits_digest` (outside `feature_set_digest`); Facet is covered (rich facet pre-flight and loop polling; the simple `pulse.Facet(path, field)` has no pre-flight); `RequestTimeout` also bounds the `ProcessStreamResult` drain; `0` means "default" and `-1` "unlimited" instead of `0` = unlimited. See the amendments in [embedder-operations 01](../v1.0.0-embedder-operations/01-resource-limits.md).
- **Ctx polling.** Serial loops poll every 4,096 rows; benchstat against the pre-change base showed no significant row (processing geomean +1.25%, service +1.34%).
- **Behaviour changes** (migration guide): ctx honoured on serial paths, `FailFast` winner reporting, `ComposeParallel` on a done ctx errors, join pre-flight before the right side decodes, matrix `EstimatedBytes` value change, crosstab return-size sizes `GROUP_DATE_RANGES`.
- **Found along the way.** Fixed bit-rotted `internal/examples/facet/01_simple.json` and `02_additive.json` (field names); the memory estimate sits at 1.33x to 4.94x measured peak (table in `.claude/reference/predict-inspect.md`).

## Open follow-ups

- `ComposeOptions.FailFast=false` returns `(nil, SERVICE_INTERNAL)`, discarding successful slots; the code was the intended contract and the doc was fixed. Whether to return the partial slots is an owner decision.
- `pulse.Facet(path, field)` (the simple facade) has no limits pre-flight.
- `mcpserve.Describe` / `ServeInfo` does not report the effective limits.
- No predict surface for Compose / chain / facet limit findings.
