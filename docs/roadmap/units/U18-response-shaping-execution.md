---
id: U18
slug: response-shaping-execution
title: "Unrequested work is never computed, and MCP returns lean responses by default"
track: Response shaping
size: M
status: not-started
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

- [ ] **#105** (8. Response shaping) MCP default `standard`: `gosdk.Config.DefaultReturn`, `pulse mcp --return`; MCP goldens regenerated once; release-note callout
- [ ] **#108** (8. Response shaping) Selection compiled into the execution plan (unrequested parts not computed)
- [ ] **#110** (8. Response shaping) Predict per-section size estimates
- [ ] **#111** (8. Response shaping) `TestReturnFullIsIdentity`, `TestReturnSkipsComputation`, `TestReturnPathsMatchSchema`; `response-shaping.md` skill (the skill and the identity / schema-path gates landed in U17: this unit adds `TestReturnSkipsComputation` and extends the skill)
- [ ] **#217** (8. Response shaping) `PULSE_RETURN_PATH_UNMATCHED` on streams: decide whether and how a stream surfaces it
- [ ] **#218** (8. Response shaping) MCP `pulse_process` streaming option writes rows through `returnshape.MarshalStreamRow`
- [ ] **#219** (8. Response shaping) Resolved-plan caching if an instance default's per-`Process` type walk shows in profiling

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

- [ ] Instrumented counters prove excluded components/overlays are not accumulated or folded
- [ ] Library default output unchanged; MCP tool output defaults to `standard`
- [ ] An agent can request `full` per call
- [ ] Predict reports per-section size estimates
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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
