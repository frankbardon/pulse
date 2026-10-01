---
id: U17
slug: response-shaping-core
title: "Callers can say which parts of a response they want"
track: Response shaping
size: M
status: not-started
depends_on: [U05]
soft_depends_on: []
blocks: [U18]
todo_items: [104, 106, 107, 109]
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

- [ ] **#104** (8. Response shaping) `Request.Return {preset, include, exclude, precision}`; `Options.DefaultReturn` (library default `full`)
- [ ] **#106** (8. Response shaping) Path grammar over the response schema; predict-time validation; `PULSE_RETURN_PATH_UNKNOWN`
- [ ] **#107** (8. Response shaping) Presets `full` / `standard` / `minimal` listed in the manifest
- [ ] **#109** (8. Response shaping) Float precision control; old switches documented as shorthands; `returned` marker

## Scope

**In scope**
- Return surface + precedence
- Path grammar, validation, `PULSE_RETURN_PATH_UNKNOWN`
- Presets in `descriptor/` + manifest
- Precision, `returned` marker, shorthand mapping
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

- [ ] No `return` (or `full`) is byte-identical to today
- [ ] Every preset path resolves in the payload schema
- [ ] An unknown path, including one owned by a hidden feature, is `PULSE_RETURN_PATH_UNKNOWN` at predict
- [ ] Excluded fields are absent, never null
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestReturnFullIsIdentity`
- `TestReturnPathsMatchSchema`

## Update Demand companions

- Payload-schema golden; CLAUDE.md Output Format Contract
- `skills/response-shaping.md`; `request-envelope.md`
- `update-demand.md` row for `Request.Return`

## Human inputs & decisions

- None.
