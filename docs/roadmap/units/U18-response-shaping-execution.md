---
id: U18
slug: response-shaping-execution
title: "Unrequested work is never computed, and MCP returns lean responses by default"
track: Response shaping
size: M
status: done
depends_on: [U17]
soft_depends_on: []
blocks: [U32]
todo_items: [105, 108, 110, 111, 217, 218, 219]
branch: response-shaping-execution
---

# U18 — response-shaping-execution

**Outcome:** Unrequested work is never computed, and MCP returns lean responses by default.

**Track:** Response shaping · **Size:** M · **Depends on:** [U17](U17-response-shaping-core.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Compile the return selection into the execution plan, so excluded components, overlays and auxiliaries are not computed. Add predict per-section size estimates. Make `standard` the default on every MCP surface (`gosdk.Config.DefaultReturn`, `pulse mcp --return`), regenerating MCP goldens once with a release-note callout.

## References

**Theme documents (read before starting):**
- [response-shaping 00 — Design](../v1.0.0-response-shaping/00-design.md) — Recommendation (compiled into a computation plan), Defaults (MCP = standard), Decisions

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#105** (8. Response shaping) MCP default `standard`: `gosdk.Config.DefaultReturn`, `pulse mcp --return`; MCP goldens regenerated once; release-note callout
- [x] **#108** (8. Response shaping) Selection compiled into the execution plan (unrequested parts not computed)
- [x] **#110** (8. Response shaping) Predict per-section size estimates
- [x] **#111** (8. Response shaping) `TestReturnFullIsIdentity`, `TestReturnSkipsComputation`, `TestReturnPathsMatchSchema`; `response-shaping.md` skill (the skill and the identity / schema-path gates landed in U17: this unit adds `TestReturnSkipsComputation` and extends the skill)
- [x] **#217** (8. Response shaping) `PULSE_RETURN_PATH_UNMATCHED` on streams: decide whether and how a stream surfaces it
- [x] **#218** (8. Response shaping) MCP `pulse_process` streaming option writes rows through `returnshape.MarshalStreamRow`
- [x] **#219** (8. Response shaping) Resolved-plan caching if an instance default's per-`Process` type walk shows in profiling

## Scope

**In scope**
- Plan compilation (components, overlays, auxiliaries, mergeable accumulators skipped)
- Predict size estimates
- MCP default `standard` + overrides
- `TestReturnSkipsComputation`

**Out of scope**
- —

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(response-shaping-execution/E<n>-S<m>): …`; close each epic with `milestone(response-shaping-execution/E<n>): vertical slice complete — <epic title>`.

### E1 — Unrequested parts are never computed
- S1: plan compilation + instrumentation counters
- S2: `TestReturnSkipsComputation`
- S3: predict per-section size estimates

### E2 — MCP returns lean responses by default
- S1: `gosdk.Config.DefaultReturn` + `pulse mcp --return`, default `standard`
- S2: MCP goldens regenerated; release-note label `breaking` for the MCP default

## Acceptance criteria

- [x] Instrumented counters prove excluded components/overlays are not accumulated or folded
- [x] Library default output unchanged; MCP tool output defaults to `standard`
- [x] An agent can request `full` per call
- [x] Predict reports per-section size estimates
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestReturnSkipsComputation`

## Update Demand companions

- CLAUDE.md MCP-layer paragraph (default `standard`)
- `docs/src/mcp/index.md`; `skills/session-bootstrap.md`
- `docs/src/cli/flags.md` (`--return`)

## Human inputs & decisions

- None.

## Handed on from U17

[U17](U17-response-shaping-core.md) shapes at serialization only; everything below is this unit's.

- **Skip computation of excluded parts, per-group accumulators included** (#108). U17's per-group `AggregationComponents.Groups` (#193) are computed even when `components` is excluded; the plan must reach the group accumulators too, along with overlays and auxiliaries. The `DisableComponents` compute gate (`Service.effectiveDisableComponents`) is the existing precedent: extend it from "components off" to "plan excludes the part".
- **MCP default `standard`** (#105): `standard` already exists in the manifest `return_presets` and the resolver; this unit wires `gosdk.Config.DefaultReturn` / `pulse mcp --return` over `Options.DefaultReturn`. Remember that a request `return` replaces the default, and that a `--no-components` instance keeps components off under a request `return` (only a request `disable_components: false` re-opens them).
- **Predict size estimates** (#110): `PredictResult.Return` already carries the resolved plan and digest; add per-section byte estimates with and without the selection.
- **`PULSE_RETURN_PATH_UNMATCHED` on streams** (#217): a stream has no warnings slot, so the warning is buffered-only today. Decide: terminal-chunk warning, predict-time note, or documented absence.
- **MCP streaming row encoding** (#218): only `pulse api process|compose --stream` is verified to call `returnshape.MarshalStreamRow`; check the MCP streaming option writes precision-rounded, column-selected rows.
- **Plan caching** (#219): an instance default re-walks the `Response` type per `Process`; cache the plan without its request-derived `Exact` set if profiling shows it. A request-derived exact set can never be cached across requests.

## Landed

Shipped on `response-shaping-execution` (no release tag cut by the unit). Epics ran E1 CLAUDE.md slimmed to a 40,000-byte ceiling, E2 compute plan, E4 predict sizes, E3 overlays / tests / regressions skip, E5 MCP default and docs.

- **Compute plan (#108, #111).** `processing.ComputePlan` plus `WorkStats()` counters; excluded components (aggregations incl. `groups`, groupers, filterers, run), crosstab cell maps, auxiliary margins and matrices (slot, auxiliary, scalars, vectors) are never computed on every arm, per Compose slot and per chain stage. The execution arm is chosen from the ORIGINAL request, so a shaped request never changes path. `TestReturnSkipsComputation` is the gate.
- **Overlays, tests, regressions.** An excluded overlay layer, test / post-test entry or whole regressions slot is skipped. Vetoes keep the run correct: an overlay that reads host components keeps `components.crosstab` computed (absent on the wire); a multiplicity family claims its layers / tests so `p_adjusted` and `m` stay byte-identical; a Compose overlay dependency vetoes.
- **Error rule.** An excluded part is not computed and therefore not validated: no `PULSE_OVERLAY_COMPONENTS_REQUIRED`, `PULSE_TEST_*`, `PROCESSING_REGRESSION_*` or `PULSE_WEIGHT_LOW_NEFF`. `Predict` stays the validator. Chain stages still refuse tests / regressions (`PULSE_CHAIN_NOT_MERGEABLE`) because the refusal reads the original request. In Go results a partly skipped `tests` / `post_tests` slice holds nil at skipped positions.
- **Predict sizes (#110).** `return.sizes[]` `{section, full_bytes, shaped_bytes, basis}` and `return.unresolved_includes`.
- **MCP default (#105).** `standard`; `gosdk.Config.DefaultReturn`, `mcpserve.Options.DefaultReturn`, `pulse mcp --return`. Precedence: request, host, feature profile, built-in. Breaking for MCP users; migration guide lists it.
- **#217** decided: a stream never raises `PULSE_RETURN_PATH_UNMATCHED`; the predict-time `unresolved_includes` note is the surface.
- **#218** finding: the MCP tools have no streaming option, so there is no path to verify; library `StreamChunk` values are not precision-rounded, because precision is wire-only (only the CLI writers round).
- **#219** measured: uncached resolution was 126% / 669% of a small Process; the plan cache (per `InstanceSnapshot`, keyed by effective return JSON, at most 64 entries; `Exact` sets and refusals never cached) brings a full-preset request to +0.8% and a cached resolve to 0.27%.

## Open follow-ups (owner decisions)

- Predict `sizes` appear only when an effective `return` block exists (FR-30 placement); a request without one reports none.
- The chi-square / Fisher component veto is over-cautious when the run is unweighted.
- Layer-local multiplicity families veto conservatively.
- Compose-host overlays are skipped all-or-nothing.
- Facet overlays are untouched (no compute plan there).
- No Compose integration test for the compose-family test veto.
- The `pulse mcp` startup line does not show the effective preset.
- The `standard` preset keeps about +10% residual cost, unattributed (likely the shaping apply pass).
