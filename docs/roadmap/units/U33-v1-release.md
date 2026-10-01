---
id: U33
slug: v1-release
title: "Pulse v1.0.0 is released with a written stability promise"
track: API & release
size: S
status: not-started
depends_on: [U32]
soft_depends_on: [all other units]
blocks: []
todo_items: [168, 169, 170, 171, 173, 174, 175, 176, 177, 178, 179]
branch: v1-release
---

# U33 — v1-release

**Outcome:** Pulse v1.0.0 is released with a written stability promise.

**Track:** API & release · **Size:** S · **Depends on:** [U32](U32-docs-audit.md) · **Soft:** all other units · **Unblocks:** —

## Summary

Publish `STABILITY.md` with the final public package list, cut `v1.0.0-rc.1` through the release pipeline and validate it against the downstream library, run the cross-cutting verification sweep, then tag `v1.0.0`.

## References

**Theme documents (read before starting):**
- [api-and-release 02 — Stability policy draft](../v1.0.0-api-and-release/02-stability-policy.md) — STABILITY.md draft
- [api-and-release 01 — Release pipeline & versioning](../v1.0.0-api-and-release/01-release-pipeline.md) — Release workflow

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#168** (12. Release v1.0.0) `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md))
- [ ] **#169** (12. Release v1.0.0) Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library
- [ ] **#170** (12. Release v1.0.0) `v1.0.0` tagged
- [ ] **#171** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values
- [ ] **#173** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups
- [ ] **#174** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] **#175** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] **#176** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics`
- [ ] **#177** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`)
- [ ] **#178** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) CLAUDE.md stays at or under 50,000 bytes (long form moved to `.claude/reference/`)
- [ ] **#179** (13. Cross-cutting (applies throughout; tick when verified for the whole release)) `format_version` remains `"1.1"` (every wire change additive)

## Scope

**In scope**
- STABILITY.md
- rc tag + downstream validation
- Cross-cutting verification (TODO §13)
- v1.0.0 tag

**Out of scope**
- New features
- Documentation work (U32)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(v1-release/E<n>-S<m>): …`; close each epic with `milestone(v1-release/E<n>): vertical slice complete — <epic title>`.

### E1 — The promise is written
- S1: `STABILITY.md` at the root with the U02 package list

### E2 — Verified and released
- S1: cross-cutting sweep (§13 items)
- S2: `v1.0.0-rc.1`; downstream library validation
- S3: `v1.0.0`

## Acceptance criteria

- [ ] Every TODO item is ticked, or explicitly moved to post-1.0 in the theme docs
- [ ] The rc builds through the release pipeline, and the downstream library passes its own tests against it
- [ ] The API-compat check is active against `v1.0.0`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- All non-skippable gates green; API-compat job enabled

## Update Demand companions

- README links to STABILITY.md
- Release notes mention the MCP default `standard` (from U18)

## Human inputs & decisions

- Maintainer runs the downstream validation and tags the release

## Notes

- TODO item 172 (missing-data modes) is verified in U16. It is listed in the sweep for completeness.
