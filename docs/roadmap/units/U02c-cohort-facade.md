---
id: U02c
slug: cohort-facade
title: "Embedders read and write cohorts record by record, and predict says whether a crosstab fuses"
track: API & release
size: M
status: not-started
depends_on: [U02]
soft_depends_on: []
blocks: [U32]
todo_items: [190, 191, 192]
branch: cohort-facade
---

# U02c — cohort-facade

**Outcome:** Embedders read and write cohorts record by record, and predict says whether a crosstab fuses.

**Track:** API & release · **Size:** M · **Depends on:** [U02](U02-public-surface.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

U02 narrows `encoding` to schema nouns plus raw-byte primitives for ungrouped `0x01` cohorts, and makes the map-filling `ReadRecordAt` internal. This unit adds the recommended path on the facade: `CohortReader` (`Schema()`, `Len()`, `RecordAt(i)`) for reading records by index, and `CohortBuilder` (schema + append rows) for writing cohorts, grouped (`0x02`) ones included. The raw primitives stay as the expert escape hatch.

It also replaces `processing.CanFuseCrosstab` with a no-execute answer: `PredictResult.CrosstabFusable`, mirroring how `Streamable` mirrors `CanStreamRequest`. A parity gate keeps predict's answer equal to the engine's runtime fusion decision. The field is payload-reachable and additive, so the payload-schema golden is regenerated and `format_version` stays `"1.1"`.

## References

**Theme documents (read before starting):**
- [api-and-release 00 — Public Go surface](../v1.0.0-api-and-release/00-public-surface.md) — Decisions (`encoding` row), Fixed points, Facade additions, Unit split
- [api-and-release 03 — Embedder migration guide](../v1.0.0-api-and-release/03-embedder-migration.md) — `encoding` and Engine packages → internal (every row tagged U02c)
- `.claude/reference/byte-layout.md` — load before `CohortBuilder` (grouped cohorts, null bitmap, format version as a function of schema content)
- `.claude/reference/predict-inspect.md` — load before `CrosstabFusable` (predict import ban, streamability mirror)

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#190** (1. API surface & release pipeline › Public Go surface › Cohort facade) `CohortReader` on the facade: `Schema()`, `Len()`, `RecordAt(i)`
- [ ] **#191** (1. API surface & release pipeline › Public Go surface › Cohort facade) `CohortBuilder` on the facade: schema + append rows, grouped (`0x02`) cohorts included
- [ ] **#192** (1. API surface & release pipeline › Public Go surface › Cohort facade) `PredictResult.CrosstabFusable` (no-execute) with a runtime parity gate against the engine's fusion check; payload-schema golden regenerated, `format_version` stays `"1.1"`

## Scope

**In scope**
- `CohortReader`: open a cohort, expose its schema and record count, return one decoded record by index (single-file and shard-archive cohorts)
- `CohortBuilder`: take a schema, append rows, write a valid `.pulse` file — ungrouped and grouped — with the format version chosen from schema content as today
- Fusion rules moved to a no-execute home that `descriptor/predict.go` may import (as `types/streamability.go` is for streaming), consumed by both predict and the engine
- `PredictResult.CrosstabFusable` and its parity gate
- Payload-schema golden regeneration and the Update Demand companions for a payload-reachable predict field

**Out of scope**
- Public raw-byte writing of grouped (`0x02`) cohorts — `CohortBuilder` is the only public grouped write path
- Any change to the `.pulse` byte layout or the raw-byte primitives U02 freezes
- Changing when a crosstab fuses: the runtime decision is moved, not altered
- Streaming or bulk-import replacements: `Import` / `ImportJob` remain the bulk path
- `processing` extension types ([U02b](U02b-extension-contract.md))

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test|docs(cohort-facade/E<n>-S<m>): …`; close each epic with `milestone(cohort-facade/E<n>): vertical slice complete — <epic title>`.

### E1 — Embedders read and write cohorts through the facade
- S1: `CohortReader` (`Schema()`, `Len()`, `RecordAt(i)`) over single-file and shard-archive cohorts; round-trip tests against the raw-primitive reader
- S2: `CohortBuilder` for ungrouped cohorts; byte-identical to a raw-primitive write of the same rows
- S3: `CohortBuilder` for grouped cohorts; reads back identically through `CohortReader` and `Process`

### E2 — Predict says whether a crosstab fuses
- S1: fusion rules moved to a no-execute home; the engine's `CanFuseCrosstab` consumes it; parity gate
- S2: `PredictResult.CrosstabFusable`; payload-schema golden regenerated; skills, CLAUDE.md and migration-guide rows updated

## Acceptance criteria

- [ ] `CohortReader.RecordAt(i)` returns the same values as the engine's decode for every field type, null and set width, on single-file and shard-archive cohorts
- [ ] A cohort written by `CohortBuilder` reads back with identical values through `CohortReader` and `Process`, for ungrouped and grouped schemas; an ungrouped write is byte-identical to a raw-primitive write of the same rows
- [ ] `PredictResult.CrosstabFusable` equals the runtime fusion decision for every request in the parity corpus
- [ ] `descriptor/predict.go` still imports no `service/` or `processing/`
- [ ] `format_version` stays `"1.1"`; the payload-schema golden is regenerated with `-update`, never hand-edited
- [ ] Every [03](../v1.0.0-api-and-release/03-embedder-migration.md) row tagged U02c is true of the branch
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New `CrosstabFusable` parity gate mirroring the `TestStreamability_*` pattern (predict answer vs runtime decision over a request corpus)
- `TestPredictNoExecutionImports` green
- `TestPayloadSchemaGolden` regenerated; `TestPayloadSchema_VersionMatchesEnvelope` green
- `CohortReader` / `CohortBuilder` round-trip and byte-identity tests, including grouped and wide-set (`set_u128` / `set_u256`) fields

## Update Demand companions

- CLAUDE.md "Design principles" facade method list, "Predict / Inspect contracts" (`CrosstabFusable` beside `Streamable`), "Execution modes" crosstab bullet (`CanFuseCrosstab` pointer)
- `.claude/reference/predict-inspect.md`, `.claude/reference/execution-modes.md`
- `docs/src/contract/payload-schema.md` + regenerated `descriptor/testdata/payload-schema.json`
- `skills/tool-predict.md`, `skills/crosstab-guide.md`, `skills/cohort-schema-design.md` (reader / writer path)
- `docs/src/library/` page for the reader / writer
- [03-embedder-migration.md](../v1.0.0-api-and-release/03-embedder-migration.md): correct any U02c row whose landed spelling differs

## Human inputs & decisions

- **Open, decided in E1:** the exact `CohortBuilder` row-append signature (typed row struct vs value slice vs map), and how a `CohortReader` / `CohortBuilder` is obtained from the facade.
- **Decided:** grouped raw-byte writes stay out of the public primitive set; `CohortBuilder` is the grouped path.

## Notes

- **Rule-drift risk.** `CrosstabFusable` in predict requires moving fusion rules out of `processing` into a no-execute home; predict and runtime could drift. The parity gate, like `TestStreamability_*`, is part of acceptance.
- The raw-byte primitives (U02) remain supported for ungrouped `0x01` writes; `CohortBuilder` is the recommended path, not a replacement.
