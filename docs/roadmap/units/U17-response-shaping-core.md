---
id: U17
slug: response-shaping-core
title: "Callers can say which parts of a response they want"
track: Response shaping
size: M
status: done
depends_on: [U05]
soft_depends_on: []
blocks: [U18]
todo_items: [82, 104, 106, 107, 109, 193]
branch: response-shaping-core
---

# U17 — response-shaping-core

**Outcome:** Callers can say which parts of a response they want.

**Track:** Response shaping · **Size:** M · **Depends on:** [U05](U05-profiles-enforcement.md) · **Unblocks:** [U18](U18-response-shaping-execution.md)

## Summary

Add `Request.Return {preset, include, exclude, precision}` and `Options.DefaultReturn` (library default `full`), the path grammar over the response schema with predict-time validation, presets listed in the manifest, float precision, the `returned` marker, and the old switches as shorthands. This unit implements selection at **serialization**. U18 pushes it into execution.

## References

**Theme documents (read before starting):**
- [response-shaping 00 — Design](../v1.0.0-response-shaping/00-design.md) — Options considered, Shape, Where data volume comes from, Contract notes

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#104** (8. Response shaping) `Request.Return {preset, include, exclude, precision}`; `Options.DefaultReturn` (library default `full`)
- [x] **#106** (8. Response shaping) Path grammar over the response schema; predict-time validation; `PULSE_RETURN_PATH_UNKNOWN`
- [x] **#107** (8. Response shaping) Presets `full` / `standard` / `minimal` listed in the manifest
- [x] **#109** (8. Response shaping) Float precision control; old switches documented as shorthands; `returned` marker
- [x] **#193** (8. Response shaping) Per-group aggregator Components: `Components.Aggregations` figures emitted inside each group of a grouped response, for built-in and extension operators alike

## Scope

**In scope**
- Return surface + precedence
- Path grammar, validation, `PULSE_RETURN_PATH_UNKNOWN`
- Presets in `descriptor/` + manifest
- Precision, `returned` marker, shorthand mapping
- Per-group aggregator Components (#193), carried over from [U02b](U02b-extension-contract.md): today `Components.Aggregations` is emitted only for ungrouped responses, so no operator, built-in or extension, reports figures inside each group. Landing it reshapes `Response.Components`, so it rides this unit's payload-schema and selection-path work (the new paths must be addressable by `Request.Return`). Touches `descriptor/testdata/payload-schema.json`, `.claude/reference/response-components.md` (load first) and the Components skills
- `TestReturnFullIsIdentity`, `TestReturnPathsMatchSchema`

**Out of scope**
- Skipping computation (U18)
- MCP default (U18)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(response-shaping-core/E<n>-S<m>): …`; close each epic with `milestone(response-shaping-core/E<n>): vertical slice complete — <epic title>`.

### E1 — Callers can select response parts
- S1: `Request.Return` + `Options.DefaultReturn`; path grammar; predict validation
- S2: presets `full`/`standard`/`minimal` in the manifest
- S3: serialization-time selection; precision; `returned` marker; shorthands

## Acceptance criteria

- [x] No `return` (or `full`) is byte-identical to today
- [x] Every preset path resolves in the payload schema
- [x] An unknown path, including one owned by a hidden feature, is `PULSE_RETURN_PATH_UNKNOWN` at predict
- [x] Excluded fields are absent, never null
- [x] Every grouped response carries per-group aggregator Components, byte-identical to the ungrouped figures for the same records; the new paths resolve in the payload schema and are selectable through `Request.Return`; `Request.DisableComponents` still yields the pre-Components wire form
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestReturnFullIsIdentity`
- `TestReturnPathsMatchSchema`

## Update Demand companions

- Payload-schema golden; CLAUDE.md Output Format Contract
- `Response.Components` per-group aggregator rows: `.claude/reference/response-components.md`, `skills/response-components.md`, each aggregator's atomic skill `## Components`, `docs/src/contract/payload-schema.md`
- `skills/response-shaping.md`; `request-envelope.md`
- `update-demand.md` row for `Request.Return`

## Handed on from U16 (resolved)

- **Matrix cells under `Return.precision`** (the `precision` half of TODO #82). `MatrixResult.primary.values` and `auxiliary.n` are `[][]float64 | null` inside a `types.MatrixValues`; the rounding applies to `primary` and the `top_pairs` `r` figures, never to `auxiliary.n` (integer counts) or to `scalars.determinant` unless the preset says so. Decide how the path grammar addresses `matrices[].primary.values`, and that `upper` encoding is unaffected. Contract: `.claude/reference/matrix-and-vectors.md` (Matrix slot).


## Human inputs & decisions

- None.

## Notes

- **Feature-profile `return` section.** The design (feature-profiles 01, P2) lets a feature profile carry an instance default `return`. U04's strict decode refuses `return` as an unknown key, so adding it here means a new `pulse.FeatureProfile` field, its precedence against `Options.DefaultReturn`, and `.claude/reference/feature-profiles.md` (load first).

## Landed deviations

PR #318 (no release tag cut by the unit). Contract: `.claude/reference/update-demand.md` (`Request.Return` row), `docs/src/contract/payload-schema.md` (Return slot), `skills/response-shaping.md`, `docs/src/library/response-shaping.md`. Where the code differs from [the design](../v1.0.0-response-shaping/00-design.md):

- **Include-only is an allowlist.** `include` with no preset starts from an EMPTY base; `exclude` / `precision` alone shape the full response. Top-level `warnings` joins every selection and a nested `warnings` is kept wherever its parent is emitted.
- **`minimal` is the primary result of each slot**, not "data and warnings only": crosstab matrix, matrix primaries, test statistic and p-value, regression coefficients. `standard` keeps `metadata` and whole crosstab / overlays, trims tests and regressions, and drops `components`.
- **Two-layer apply.** The finished Go value is pruned (excluded ⇒ zero / nil) by the `returnplan` applier, and the plan rides the unexported `Response.plan` into a planned wire encoder that writes absent keys and rounds floats. Precision is wire-only; Go values keep full float64.
- **Strict `data` columns.** `data[*].<column>` is refused (`PULSE_RETURN_PATH_UNKNOWN`) when the request cannot produce the column; under a join, a crosstab or an unknown feature type the columns stay open.
- **Three codes, not one:** `PULSE_RETURN_PATH_UNKNOWN`, `PULSE_RETURN_INVALID` (bad preset, precision, path, or `returned` named), and the buffered-only warning `PULSE_RETURN_PATH_UNMATCHED`.
- **Feature-profile `return` landed here** (`pulse.FeatureProfile.Return`, `invalid_return` reason), beside `Options.DefaultReturn`; a request block replaces the instance default entirely.
- **`ComposedRequest.Return`** (overlays only, rooted at `ComposedResponse`) joined per-slot and per-stage `return`; chain shaping runs after the whole chain.
- **`DisableComponents` shorthand** adds `exclude: ["components"]` only when a `return` layer exists; alone it stays byte-identical with no marker. An engine `DisableComponents` always wins over a request `return` block (owner call, reversing an earlier draft that re-opened the gate); only an explicit request `disable_components: false` re-opens it, as before U17. `EchoRequest` is NOT folded into `include: ["request"]` (its own envelope switch).
- **Counts stay exact under precision:** count-aggregation columns, count crosstab cells and margins, `matrices[*].auxiliary.n`. The matrix `precision` half of #82 is closed here.
- **Per-group Components (#193)** landed as `AggregationComponents.Groups`, in `data` order, on buffered, streaming and sharded runs.
- **Marker:** `returned {preset, digest, precision?}` only for a non-identity plan; `custom` when no preset; unexcludable.
- **Not in this unit (design items moved on):** skipping computation, MCP `standard` default and predict size estimates are U18; the open edges below each have an owner.

## Handed on from U17

- **[U18](U18-response-shaping-execution.md):** skip computation of every excluded part including per-group accumulators; MCP `standard` default; predict per-section size estimates; `PULSE_RETURN_PATH_UNMATCHED` on streams (#217); MCP streaming row encoding (#218); plan caching (#219).
- **[U35](U35-predict-runtime-parity.md):** `Response.overlays` feature gate (#213); joined slot / stage `Plan.Exact` (#214); `finalizeMergedPartial` empty partial (#215).
- **[U34](U34-extension-validation.md):** extension count-semantic declaration (#216).
- **[U33](U33-v1-release.md):** `pulse.ReturnedMarker` root alias (#220).
