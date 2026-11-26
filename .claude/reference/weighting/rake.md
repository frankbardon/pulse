# Weighting — computing weights: `Pulse.Rake`

Slice of `.claude/reference/weighting.md` — load the index first, then only the slice(s) your change touches.

## Surface

**`Pulse.Rake(ctx, RakeRequest) (*RakeResult, error)`** (`rake.go`) computes rim weights (raking) or cell weights (post-stratification) and writes them into a NEW single-file cohort: the source's logical fields verbatim, then a nullable `f64` weight field (`weight_field`, default `rake_weight`; with `overwrite`, an existing field of that name is replaced IN PLACE). Request / report types are root aliases of `internal/rake`, so the no-execute layer and the CLI / MCP adapters share one shape; `RakeResult` is root-native. Not payload-reachable: no `format_version` or payload-schema change. Observability kind `observe.OpRake`; feature `capability:rake` (facade ungated, like `materialize`). Skill: `skills/survey-weights.md`.

**Adapters.** CLI `pulse rake` and MCP `pulse_rake` (skill `skills/tool-rake.md`) hold no logic; the tool's input mirrors `RakeRequest` field for field so it converts by plain type conversion (drift fails to compile). No predict leaf: a future one must call `Compile`.

**Why a cohort-writing operation.** A request weight must name a SCHEMA field ("Weight field existence"), so a weight computed inside a request could never weight that request; raking is also a global fixed point, not row-local. Targets are INLINE: a Populations file is a saved vector fit, not shares, and `OVERLAY_RAKE` rakes a crosstab read-only — neither is reused.

## The solver (`internal/rake`)

Leaf package (`TestRake_ImportBoundary`). Three steps:

- **`Compile(req, schema)`** — every static refusal, on the schema alone: `PULSE_RAKE_INVALID` (`details.reason`; the reason strings are in `Compile`) and `PULSE_RAKE_UNKNOWN_CATEGORY` (a target key the field cannot hold). A predict over the same schema must call `Compile` so it raises the same code.
- **`Plan.Load`** — reads every record once (archives in shard order). Refusals come after the FULL read so `details.rows` counts every offender: invalid base weight (`base_weight_invalid`, the `weighting.Classify` probability rule; zero valid), then `PULSE_RAKE_MISSING_VALUE` (null margin value under `missing: "refuse"`; a null `by` value always), then `PULSE_RAKE_UNTARGETED_CATEGORY`. Groups order by the `by` field's own order, never first appearance.
- **`Plan.Solve`** — per group: `PULSE_RAKE_EMPTY_CELL` for a positive target with no base weight; then IPF sweeps, margin by margin in request order (`missing: "base"` records are neither rescaled nor counted by that margin). After each sweep: rescale to Σw = n, clip to [`min_weight`, `max_weight`] relative to the mean, repeat until no clip (≤ 100 rounds). Converged when the largest share gap ≤ `tol` (default `1e-6`), else after `max_iter` (default 100) with a `PULSE_RAKE_NOT_CONVERGED` warning (the cohort is still written). Each group is finally scaled to Σw = its record count (or `total`). Sequential and order-fixed: bit-identical across runs and layouts (single file ≡ archive); every product is a lone `float64(a*b)`, so no FMA contraction.

Targets: `targets_as: "shares"` (default) must sum to 1 (±1e-6) or 100 (±1e-4); `"counts"` any positive total; both normalised by their sum.

## Output weights are PROBABILITY weights

Use the field with kind `probability` (the default). The weights are fractional, so kind `frequency` would reject them as non-integer. Under `by`, each group sums to its own n (or `total`): pooled figures weight groups by size.

## Efficiency report

`RakeEfficiency` (per group and pooled): `n`, `sum_weights`, `n_eff` (`weighting.KishNEff`), `deff_kish` = n / n_eff = 1 + `weight_cv`², `efficiency_pct`, weight min / max / range ratio / CV, `n_capped`. **`deff_kish` is the unequal-weighting design effect ONLY** — no strata, clusters or FPC ("What it is and is not"). Undefined figures are NaN in Go, `null` on the wire (`types.MarshalFinite`).

## Writing

The builder opens BEFORE any record is read (existing `out` without `overwrite` ⇒ `SERVICE_VALIDATION`, no scan); every refusal aborts it (nothing at `out`). Output is always `0x01` ungrouped. The source's SPSS metadata sidecar is CARRIED (prepared before the builder opens, since `out` may overwrite the source; written after `Close`, re-fingerprinted; a stale or uncarriable sidecar warns, never refuses); no other sidecar is. An overwrite that leaves stale sidecars warns `PULSE_RAKE_SIDECARS_INVALIDATED`. Detail: `.claude/reference/byte-layout.md` (SPSS metadata sidecar).

## Oracle

`TestSolve_MatchesSurveyRake` pins every record's weight at 1e-9 relative to R `survey` (`rake`, `postStratify`) in the generated `internal/rake/reference_values_test.go` (`internal/rake/testdata/gen_rake_reference.R`; never run in CI, never hand-edited). The fixed point is unique, so margin order cannot move it.

## Gates

`internal/rake` `TestSolve_*` / `TestCompile_Refusals` / `TestLoad_*` / `TestEfficiency_HandComputed`, root `TestRake_*`, plus the generic surface gates (observability, public API golden, error fixups / owners, feature `Since`).
