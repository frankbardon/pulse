---
id: U41
slug: derived-cohort
title: "A request's derived columns become a new cohort with the schema carried over, and the import that follows cannot mislabel a column"
track: API & release
size: L
status: not-started
depends_on: [U40]
soft_depends_on: []
blocks: [U42]
todo_items: [279, 280, 281, 282, 283, 284, 285]
branch: derived-cohort
---

# U41 — derived-cohort

**Outcome:** A request's derived columns become a new cohort with the schema carried over, and the import that follows cannot mislabel a column.

**Track:** API & release · **Size:** L · **Depends on:** [U40](U40-compose-sweep.md) · **Unblocks:** [U42](U42-fit-scoring.md)

## Summary

A window, feature or attribute can compute new columns, but a regression cannot read a window computed in the same request, and `process-chain` refuses windows. The worked example therefore streamed the derived rows to NDJSON, hand-wrote a 41-field schema and imported it. Three sharp edges came with that route: the NDJSON import maps schema fields to keys by position, so a schema that is not sorted by key name silently mislabels columns; a stream with projection on drops every column the request does not reference, with no warning; and an inferred `f32` drifts on sums.

This unit makes materialising a first-class step: a request's derived columns are written into a new cohort whose schema is the source schema plus the derived columns (typed `f64`, with generated descriptions). The NDJSON import matching by key name is a bug-class fix and lands first. The same unit adds the two request-shape conveniences the example needed in the same step: fan-out of a window list over a parameter within one request, and a `decay` parameter on `WIN_EWMA` so `alpha = 1 - decay` is not hand arithmetic.

**Source of inspiration:** the maintainer's marketing-mix worked example. Its inputs, requests and verified outputs are checked in at [`fixtures/mmm-harbor-pine/`](../fixtures/mmm-harbor-pine/README.md). The sibling units are [U40](U40-compose-sweep.md) (sweep and rank), [U41](U41-derived-cohort.md), [U42](U42-fit-scoring.md) and [U43](U43-post-aggregation-ratios.md), in that order; together they make the whole example native.

Numbering note: appended after U40, not renumbered. This unit was not in the original plan.

## References

**Read before starting:**
- CLAUDE.md "Update Demand", "Output Format Contract" and "Byte-layout invariants"
- `.claude/reference/update-demand.md`: add the per-slot rows this unit needs
- `.claude/reference/execution-modes.md`: process, streaming and chain wiring
- `.claude/reference/architecture.md`: adding a public symbol, `TestPublicAPIGolden`
- `.claude/reference/byte-layout.md`: the sidecars and the import adapters (load before any import or schema change)
- `skills/tool-import.md`, `skills/process-chain.md`, `skills/op-win-ewma.md`

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#279** (Derived cohorts) NDJSON import matches schema fields to keys by name, not by position; a key with no schema field and a schema field with no key are coded refusals (bug-class fix; today an unsorted schema silently mislabels columns)
- [ ] **#280** (Derived cohorts) Materialise a request's derived columns into a new `.pulse` cohort (`Process` with an output cohort, and a CLI and MCP path with no business logic in `cmd/pulse/`): source schema carried over, derived columns appended as `f64` with generated descriptions
- [ ] **#281** (Derived cohorts) Warn when a stream runs with projection and its output is destined for an import (or make the materialise path the supported one); the silent column drop is the bug
- [ ] **#282** (Derived cohorts) Import inference for money-like columns: infer `f64`, or add a `--float f64` import flag, so sums do not drift under `f32`
- [ ] **#283** (Derived cohorts) Fan-out of a window (or feature) list over a parameter within one request, so the 23-entry adstock request is one declaration; reuse the U40 substitution rather than a second syntax
- [ ] **#284** (Derived cohorts) `WIN_EWMA` `decay` parameter (`alpha = 1 - decay`) beside `alpha`; the two are mutually exclusive and the skill states the relation
- [ ] **#285** (Derived cohorts) Acceptance: materialise the example's feature cohort natively and fit on it, with no hand-written schema

## Scope

- **Order of work.** #279 first (it is a bug), then #281 and #282 (they narrow the old route), then #280 (the native route), then #283 and #284.
- **Schema carry-over.** The new cohort keeps the source field order, types, nullability, dictionaries and descriptions; the derived columns follow in request order.

**In scope**
- Materialising to a `.pulse` cohort from `Process`, the CLI and MCP
- NDJSON import by key name, with coded refusals
- Stream/import projection warning; import float inference
- Window/feature fan-out over a parameter in one request; `WIN_EWMA` `decay`

**Out of scope**
- Sharded or multi-cohort output
- Applying a fitted model to rows ([U42](U42-fit-scoring.md))
- Parent-group (`0x02`) output; the derived cohort is written ungrouped

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|test|docs(derived-cohort/E<n>-S<m>): …`; close each epic with `milestone(derived-cohort/E<n>): vertical slice complete — <epic title>`.

### E1 — The import cannot mislabel a column
- S1: NDJSON import matches by key name; coded refusals; regression test on an unsorted schema
- S2: projection-into-import warning; `f64` inference or flag

### E2 — Derived columns become a cohort
- S1: the materialise path over `Process`, with schema carry-over and generated descriptions
- S2: CLI and MCP surfaces, skills and docs; a feature-profile row if a new capability is named

### E3 — One declaration for the window grid
- S1: window/feature fan-out over a parameter; `WIN_EWMA` `decay`; the fixture's feature step converted

## Acceptance criteria

- [ ] An NDJSON import whose schema is not sorted by key name produces correct columns (or a coded refusal), never a silent mislabel
- [ ] A materialised cohort reproduces the fixture's 41-field feature cohort without a hand-written schema
- [ ] Sum-heavy money columns no longer drift on the documented path
- [ ] Re-run the MMM fixture by hand and convert the steps this unit covers to native Pulse; paste the results into the PR body
- [ ] The fixture README's "still outside Pulse" list drops every gap this unit closes
- [ ] `format_version` stays `"1.1"`; every change is additive
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestPayloadSchemaGolden`, `TestPublicAPIGolden`, `TestCodesHaveFixups`, `TestErrorOwners_Complete`, `TestSkillsCoverAllCliLeaves`
- New: unsorted-schema import regression, schema carry-over round trip, projection-warning and fan-out expansion tests

## Update Demand companions

- Skills: `tool-import.md`, `process-chain.md`, `op-win-ewma.md`, and a new or extended skill for the materialise path; `session-bootstrap.md` for any new flag
- `byte-layout.md` if the cohort writer or a sidecar changes; CLAUDE.md "Byte-layout invariants" only if the format moves (it should not)
- Error codes (`errors/fixup_metadata.go`, `internal/descriptor/error_owners.go`) for the new import refusals
- `docs/src/cli/flags.md` for any new CLI leaf or flag
- Payload-schema golden if a request or response slot moves

## Human inputs & decisions

- **Open:** v1 membership: owner call. This unit is written as a post-U40 unit; the owner decides whether it ships in v1.0.0 (then add it to U32's `depends_on`) or after.
- **Open:** the shape of the materialise surface: a `Process` option, a new `Materialize` library call, or a new request root. Prefer the smallest public surface.
- **Open:** whether the derived cohort carries a sidecar linking it to its source (lineage), or stays a plain cohort.
- **Open:** gap 6: warn, or refuse, when a projected stream is destined for import.
