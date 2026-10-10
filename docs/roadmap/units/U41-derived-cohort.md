---
id: U41
slug: derived-cohort
title: "A request's derived columns become a new cohort with the schema carried over, and the import that follows cannot mislabel a column"
track: API & release
size: L
status: done
depends_on: [U40]
soft_depends_on: []
blocks: [U42]
todo_items: [279, 280, 281, 282, 283, 284, 285]
branch: derived-cohort
---

# U41 — derived-cohort

**Outcome:** A request's derived columns become a new cohort with the schema carried over, and the import that follows cannot mislabel a column.

**Track:** API & release · **Size:** L · **Depends on:** [U40](U40-compose-sweep.md) · **Unblocks:** [U42](U42-fit-scoring.md)

**Status:** done (2026-10-10, branch `derived-cohort`). No release tag is cut by this unit; it rides the next `v1.0.0-alpha.N`. Embedder-visible changes: [migration guide, U41 section](../v1.0.0-api-and-release/03-embedder-migration.md#changes-from-u41-derived-cohort). Deferrals: [Handed on](#handed-on).

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

- [x] **#279** (Derived cohorts) NDJSON import matches schema fields to keys by name, not by position; a key with no schema field and a schema field with no key are coded refusals (bug-class fix; today an unsorted schema silently mislabels columns)
- [x] **#280** (Derived cohorts) Materialise a request's derived columns into a new `.pulse` cohort (`Process` with an output cohort, and a CLI and MCP path with no business logic in `cmd/pulse/`): source schema carried over, derived columns appended as `f64` with generated descriptions. Delivered as `Pulse.Materialize`, `pulse api materialize`, MCP `pulse_materialize`
- [x] **#281** (Derived cohorts) Warn when a stream runs with projection and its output is destined for an import (or make the materialise path the supported one); the silent column drop is the bug. Delivered at the import side: an explicit schema field with no column is `PULSE_IMPORT_SCHEMA_FIELD_MISSING`, and materialise is the supported route; the stream is unchanged
- [x] **#282** (Derived cohorts) Import inference for money-like columns: infer `f64`, or add a `--float f64` import flag, so sums do not drift under `f32`
- [x] **#283** (Derived cohorts) Fan-out of a window (or feature) list over a parameter within one request, so the 23-entry adstock request is one declaration; reuse the U40 substitution rather than a second syntax
- [x] **#284** (Derived cohorts) `WIN_EWMA` `decay` parameter (`alpha = 1 - decay`) beside `alpha`; the two are mutually exclusive and the skill states the relation
- [x] **#285** (Derived cohorts) Acceptance: materialise the example's feature cohort natively and fit on it, with no hand-written schema

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

As delivered:

### E1 — The import cannot mislabel a column
- S1: explicit import schemas match columns by name on every adapter; `PULSE_IMPORT_SCHEMA_{FIELD_MISSING,COLUMN_UNMAPPED,DUPLICATE,KEY_LATE}`; `--ignore-extra`
- S2: inferred floats are `f64` (`--float f32` opt-in, `PULSE_IMPORT_FLOAT_WIDTH_INVALID`); an unknown schema type name is `PULSE_IMPORT_SCHEMA_TYPE_UNKNOWN`
- S3: schema files are laid out through the one canonical layout routine (a bit-packed field first no longer shifts later offsets)
- S4: `pulse_import` takes `schema` / `ignore_extra` / `float`; a malformed schema is `PULSE_IMPORT_SCHEMA_INVALID` on MCP and CLI
- S5: shard dictionary rewrites and explicit-schema imports address bytes by the decoder's type-derived layout

### E2 — Derived columns become a cohort
- S1: `Pulse.Materialize`: source fields verbatim, then one `f64` per derived output with a generated description; refusals before any write; atomic write
- S2: `pulse api materialize` and MCP `pulse_materialize`; `capability:materialize`; skills `tool-materialize`, `derived-cohorts`

### E3 — One declaration for the window grid
- S1: `WIN_EWMA` `decay` (exactly one of `alpha` / `decay`; exact decimal `1 - decay`)
- S2: `for_each` on window, feature and attribute items (`grid` / `zip`); `PULSE_FOR_EACH_INVALID`; `Limits.MaxDerivedColumns`; `capability:for_each`; predict `for_each` summary
- S3: `exactly_one_of` param groups in the manifest, so Recommend drafts `WIN_EWMA` and `FEAT_BUCKETIZE`
- S4: `for_each` through cohort-bound MCP schemas, stored request templates (`PULSE_TEMPLATE_AXIS_COLLISION`) and Explain

### E4 — The MMM example is native
- S1: the fixture's step 04 is one materialise request with `for_each` + `decay`, checked bit for bit against the old route; roadmap closed out

## Acceptance criteria

- [x] An NDJSON import whose schema is not sorted by key name produces correct columns (or a coded refusal), never a silent mislabel
- [x] A materialised cohort reproduces the fixture's 41-field feature cohort without a hand-written schema: 40 of 41 columns bit-identical to the old route; `week` is now the source `date` (the old hand schema typed it `u16`, the same epoch day). See the fixture README, "The materialise acceptance"
- [x] Sum-heavy money columns no longer drift on the documented path: an inferred import of the panel gives the hand schema's types and bit-identical values
- [x] Re-run the MMM fixture by hand and convert the steps this unit covers to native Pulse; paste the results into the PR body
- [x] The fixture README's "still outside Pulse" list drops every gap this unit closes (1 windows, 3, 4, 5, 6, 20)
- [x] `format_version` stays `"1.1"`; wire changes are additive. Import behaviour changes (by-name schemas, `f64` inference) are listed in the migration guide
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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

- **Decided (2026-10-10):** ships in v1.0.0. The owner wants Pulse as self-sufficient as possible at v1; this unit is in U32's `depends_on`.
- **Decided (2026-10-10):** the materialise surface is a new facade call, `Pulse.Materialize` (with `pulse api materialize` and `pulse_materialize`), not a `Process` option or a new request root; the payload schema is untouched.
- **Decided (2026-10-10):** lineage: the derived cohort is a plain cohort; lineage lives in each derived column's generated description. No sidecar.
- **Decided (2026-10-10):** gap 6 is refused at the import side (an explicit schema field with no column is `PULSE_IMPORT_SCHEMA_FIELD_MISSING`); the stream is unchanged, and the docs point at materialise.
- **Decided (2026-10-10):** a `for_each` inside a compose `sweep` body is refused for v1 (`nested_in_sweep`); lexical scoping can come later without a break (#380).
- **Decided (2026-10-10, owner):** the two legacy-cohort gaps found in E1-S5 are fixed later, to the resolution bar recorded in #372 and #373.

## Handed on

Everything found during this unit and not delivered by it. Each row has a TODO id and an owner; `#N` are TODO ids in [`TODO.md`](../TODO.md), not GitHub issues.

| Found in | What is still open | TODO | Owner |
|---|---|---|---|
| E1-S5 | Bit-packed reads diverge on legacy cohorts: export reads the whole byte of a `u4` / `packed_bool` field, while the engine readers and the SPSS writer read the stored `BitPosition`, so a cohort with a nonzero stored `BitPosition` exports different values than it processes. Required: proper reads, with every reader decoding legacy cohorts correctly and identically, no re-import | #372 | [U35](U35-predict-runtime-parity.md) |
| E1-S5 | Shard cohesion and the shard dictionary rewrite refuse a cohort whose stored `ByteOffset` / `BitPosition` differ from the canonical layout (older zero-offset explicit-schema cohorts, older schema-file cohorts with a bit-packed field first). Required: full compatibility, so such cohorts join and interoperate with new cohorts and archives without re-import | #373 | [U51](U51-archive-scale.md) |
| E1-S1 | `LateKeyRefuser` is not on the public `io` facade, so an embedder's keyed reader cannot arm the late-key refusal | #374 | [U34](U34-extension-validation.md) |
| E1-S2 | `pulse import auto` (the managed import) has no `--float`, `--schema` or `--ignore-extra`; they reach it only through `ImportSpec` and MCP `pulse_import` | #375 | [U44](U44-strict-input.md) |
| E1-S2 | `session-bootstrap.md` has no `--float` routing hint (the skill is at its design budget) | #376 | [U38](U38-skill-sync.md) |
| E2-S2 | Recommend has no intent route that drafts a materialise step | #377 | [U31](U31-guidance-guides.md) |
| E3-S4 | A compose `sweep` body inside a stored request template: the renderer consumes the sweep's `{{axis}}` placeholders (the bug E3-S4 fixed for `for_each`) | #378 | [U35](U35-predict-runtime-parity.md) |
| E3-S4 | Cohort-bound MCP schemas refuse a field whose name itself contains a `{{ident}}` token on an item without `for_each` | #379 | [U35](U35-predict-runtime-parity.md) |
| Interview | A `for_each` item inside a compose `sweep` body (lexical scoping of item axes under sweep axes); refused today with `nested_in_sweep` | #380 | U40 follow-up |
