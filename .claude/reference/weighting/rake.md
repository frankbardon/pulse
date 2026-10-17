# Weighting — computing weights: `Pulse.Rake` (U47 E4-S1)

Per-topic slice of the weighting reference (computing weights with `Pulse.Rake`). Index (preamble + section map): `.claude/reference/weighting.md` — load it first, then only the slice(s) your change touches.

## Surface

**`Pulse.Rake(ctx, RakeRequest) (*RakeResult, error)`** (`rake.go`) computes rim weights (raking) or cell weights (post-stratification) for a cohort and writes them into a NEW single-file cohort: the source's logical fields verbatim, then a nullable `f64` weight column (`weight_field`, default `rake_weight`; with `overwrite`, an existing column of that name is replaced IN PLACE, keeping its position). Request and report types are root aliases of `internal/rake` (`RakeRequest` / `RakeMargin` / `RakeCell` / `RakeMarginReport` / `RakeCategoryReport` / `RakeGroupReport` / `RakeEfficiency`) so the no-execute layer and the thin CLI / MCP adapters share one shape; `RakeResult` is root-native (it carries `StaleSidecar`). Not payload-reachable: no `format_version` or payload-schema change. Observability kind `rake` (`observe.OpRake`). Feature row `capability:rake` (no dependency edge; the facade stays ungated, like `materialize`; every `PULSE_RAKE_*` code is `shared`). Skill: `skills/survey-weights.md`.

**Adapters (E4-S2).** CLI leaf `pulse rake` (`internal/cli/rake.go`; manifest command `rake`) and MCP tool `pulse_rake` (`internal/mcp`; skill `skills/tool-rake.md`), both bound to `capability:rake` and both holding no logic: the leaf assembles one `RakeRequest` from `--request` / `--margins` (each a JSON literal or a file, strict-decoded) plus one flag per scalar key, and the tool's `RakeIn` mirrors `RakeRequest` field for field so it converts by a plain type conversion (a drift fails to compile). A missing `cohort` / `out` / `margins` is the facade's `PULSE_RAKE_INVALID` on both. No predict leaf: a future one must call `Compile`.

**Why a cohort-writing operation.** A request weight must name a SCHEMA field (`surface-and-classification.md`, Weight field existence), so a weight computed inside a request could never weight that request; raking is also a global fixed point over every record, not row-local. Targets are INLINE: a Populations file is a saved vector fit, not shares, and `OVERLAY_RAKE` rakes a crosstab table read-only — neither is reused.

## The solver (`internal/rake`)

Leaf package (stdlib + `encoding` / `errors` / `types` + `internal/weighting`; `TestRake_ImportBoundary`). Three steps, all called by the facade:

- **`Compile(req, schema)`** — every static refusal, judged on the schema alone: `PULSE_RAKE_INVALID` (`details.reason`: `cohort_missing`, `out_missing`, `out_is_source`, `margins_empty`, `margin_shape`, `field_unknown`, `field_type`, `field_repeated`, `targets_invalid`, `targets_zero`, `targets_sum`, `targets_as`, `cell_shape`, `cell_duplicate`, `missing_policy`, `caps`, `max_iter`, `tol`, `total`, `weight_field_exists`, `weight_field_in_use`) and `PULSE_RAKE_UNKNOWN_CATEGORY` (a target key the field cannot hold: not a dictionary entry, not `true` / `false` for `packed_bool`, not a canonical in-range decimal for `u4`…`u64`). A predict over the same schema MUST call `Compile` so it raises the same code.
- **`Plan.Load(n, at)`** — reads every record once (`CohortReader.RecordAt`; an archive's records in shard order against the canonical schema). Refusals after the FULL read, so `details.rows` counts every offender: an invalid base weight (`PULSE_RAKE_INVALID` reason `base_weight_invalid`, by the shared `weighting.Classify` probability rule — null, negative, NaN / ±Inf; zero is valid), then `PULSE_RAKE_MISSING_VALUE` (a null margin value under `missing: "refuse"`; a null `by` value always), then `PULSE_RAKE_UNTARGETED_CATEGORY` (a record's category with no target). Groups order by the `by` field's own order (dictionary position, `false` before `true`, numeric), never first appearance.
- **`Plan.Solve`** — per group: `PULSE_RAKE_EMPTY_CELL` for a positive target whose category holds no base weight; then sweeps of IPF over the records — margin by margin in request order, each category's records × (target share × current weight of the margin's counted records) / (category weight); `missing: "base"` records are neither rescaled nor counted by that margin. After each sweep the caps: rescale to Σw = n, clip to [`min_weight`, `max_weight`] (relative to the mean weight), repeat until no clip (≤ 100 rounds) — the per-sweep rescale also stops scale drift. Converged when the largest |achieved − target| share gap ≤ `tol` (default `1e-6`), else after `max_iter` (default 100) sweeps with a `PULSE_RAKE_NOT_CONVERGED` warning (the cohort is still written). Finally each group is scaled to Σw = its record count (or `total`). A single margin (rim or joint) is exact after one sweep. Sequential and order-fixed: bit-identical across runs and layouts (single file ≡ archive, `TestRake_ShardArchiveMatchesSingleFile`); every product is a lone `float64(a*b)` so no FMA contraction applies.

Targets: `targets_as: "shares"` (default) must sum to 1 (±1e-6) or 100 (±1e-4); `"counts"` any positive total. Both are normalised by their sum, so different margins share one total.

## Output weights are PROBABILITY weights

The column is meant for `{"weight": {"field": "rake_weight"}}` with kind `probability` (the default). They are fractional, so kind `frequency` would reject them (`NonIntegerFrequency`). Under `by`, every group sums to its own n (or `total`): pooled figures weight each group by its size.

## Efficiency report

`RakeEfficiency` (per group and pooled): `n`, `sum_weights`, `n_eff` (`weighting.KishNEff`), `deff_kish` = n / n_eff = 1 + `weight_cv`² (population CV), `efficiency_pct` = 100 · n_eff / n, `weight_min`, `weight_max`, `weight_range_ratio`, `weight_cv`, `n_capped`. **`deff_kish` is the unequal-weighting (Kish) design effect ONLY** — no strata, clusters or FPC (`refusals-and-inference.md`, What it is and is not); complex-sample variance is out of scope. Undefined figures are NaN in Go and `null` on the wire (`MarshalJSON` through `types.MarshalFinite` on `RakeResult`, `RakeEfficiency`, `RakeCategoryReport`, `RakeGroupReport`).

## Writing

The builder opens BEFORE any record is read (an existing `out` without `overwrite` is `SERVICE_VALIDATION`, no scan), every refusal aborts it (nothing at `out`), an anchored `out` is `SERVICE_VALIDATION`. The output is always `0x01` ungrouped through `NewCohortBuilder`; the source's SPSS metadata sidecar is CARRIED (`spss.PrepareSidecarCarry` before the builder opens — `out` may overwrite a cohort whose sidecar the write stales — then `SidecarCarry.WriteFor` after `Close`: the weight gets a plain numeric variable, a replaced `weight_field` variable becomes one unless a response set / the derived registry names it, re-fingerprinted over `out`; a stale / invalid source sidecar or an uncarriable one is a warning with its own code, never a refusal), no other sidecar is; an overwrite that leaves stale sidecars warns `PULSE_RAKE_SIDECARS_INVALIDATED` — a carried SPSS sidecar is fresh and dropped from `invalidated_sidecars` (`carrySidecar`, root `sidecar_carry.go`). Detail: `.claude/reference/byte-layout.md` (SPSS metadata sidecar).

## Oracle

`TestSolve_MatchesSurveyRake` pins every record's weight at 1e-9 relative to R 4.6.1 `survey` 4.5 (`rake` to epsilon 1e-13, `postStratify`, per-wave subsets) on a 120-record sample written verbatim into the generated `internal/rake/reference_values_test.go` (`internal/rake/testdata/gen_rake_reference.R`; never run in CI, never hand-edited). The fixed point is unique, so the margin order cannot move it.

## Gates

`TestSolve_*`, `TestCompile_Refusals`, `TestLoad_*`, `TestEfficiency_HandComputed`, `TestRake_ImportBoundary` (`internal/rake`); `TestRake_*` (root); `TestObservedMethodsCoverSurface`, `TestObservabilityDocCoversEnums`; `TestPublicAPIGolden`; `TestCodesHaveFixups`, `TestErrorOwners_Complete`; `TestFeaturesHaveSince`.
