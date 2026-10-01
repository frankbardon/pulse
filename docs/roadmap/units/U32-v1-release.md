---
id: U32
slug: v1-release
title: "Pulse v1.0.0 is released with a written stability promise"
track: API & release
size: S
status: not-started
depends_on: [U01, U02, U06, U10, U18, U19, U20, U23, U30, U31]
soft_depends_on: [all other units]
blocks: []
todo_items: [159, 160, 161, 162, 164, 165, 166, 167, 168, 169, 170]
branch: v1-release
---

# U32 — v1-release

**Outcome:** Pulse v1.0.0 is released with a written stability promise.

**Track:** API & release · **Size:** S · **Depends on:** [U01](U01-release-pipeline.md), [U02](U02-public-surface.md), [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U18](U18-response-shaping-execution.md), [U19](U19-resource-limits.md), [U20](U20-observability.md), [U23](U23-guidance-mcp.md), [U30](U30-matrix-extensions-hardening.md), [U31](U31-guidance-guides.md) · **Soft:** all other units · **Unblocks:** —

## Summary

Publish `STABILITY.md` with the final public package list, cut `v1.0.0-rc.1` through the release pipeline and validate it against the downstream library, run the cross-cutting verification sweep, then tag `v1.0.0`.

## References

**Theme documents (read before starting):**
- [api-and-release 02 — Stability policy draft](../v1.0.0-api-and-release/02-stability-policy.md) — STABILITY.md draft
- [api-and-release 01 — Release pipeline & versioning](../v1.0.0-api-and-release/01-release-pipeline.md) — Release workflow

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#159** (11. Release v1.0.0) `STABILITY.md` published at the repo root, with the final public package list ([api-and-release 02](v1.0.0-api-and-release/02-stability-policy.md))
- [ ] **#160** (11. Release v1.0.0) Release candidate tag (`v1.0.0-rc.1`) built through the release pipeline and exercised by the downstream library
- [ ] **#161** (11. Release v1.0.0) `v1.0.0` tagged
- [ ] **#162** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator in every theme is weight-aware (or explicitly refuses a weight) and multiplicity-aware where it emits p-values
- [ ] **#164** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) New `PULSE_MATRIX_*` / `PULSE_VECTOR_*` / `PULSE_OVERLAY_*` / `PULSE_PROFILE_*` / `PULSE_LIMIT_*` / `PULSE_WEIGHT_*` / `PULSE_RETURN_*` / advisory codes all have `codeMetadata` + fixups
- [ ] **#165** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new operator has `Purpose`, `Interpretation` (if inferential), `Since`, dependency edges and an atomic skill
- [ ] **#166** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) Every new gate is listed by name in CLAUDE.md "Non-Skippable CI Gates"
- [ ] **#167** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) The Update Demand table has rows for: `Purpose`, `Since` / dependencies, topical-skill fences, `Request.Vectors` / `Matrices`, `Response.Matrices`, `Request.Weight` / `Multiplicity` / `TimeZone` / `Return`, `Options.Limits` / `Logger` / `Hooks` / `Metrics`
- [ ] **#168** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) New env vars and CLI flags documented (CLAUDE.md "Build / Env", `flags.md`, `session-bootstrap.md`)
- [ ] **#169** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) CLAUDE.md stays at or under 50,000 bytes (long form moved to `.claude/reference/`)
- [ ] **#170** (12. Cross-cutting (applies throughout; tick when verified for the whole release)) `format_version` remains `"1.1"` (every wire change additive)

## Scope

**In scope**
- STABILITY.md
- rc tag + downstream validation
- Cross-cutting verification (TODO §12)
- v1.0.0 tag

**Out of scope**
- New features

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(v1-release/E<n>-S<m>): …`; close each epic with `milestone(v1-release/E<n>): vertical slice complete — <epic title>`.

### E1 — The promise is written
- S1: `STABILITY.md` at the root with the U02 package list

### E2 — Verified and released
- S1: cross-cutting sweep (§12 items)
- S2: `v1.0.0-rc.1`; downstream library validation
- S3: `v1.0.0`

## Acceptance criteria

- [ ] Every TODO item is ticked or explicitly moved to post-1.0 in the theme docs
- [ ] The rc builds through the release pipeline and the downstream library passes its own tests against it
- [ ] API-compat check is active against `v1.0.0`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- All non-skippable gates green; API-compat job enabled

## Update Demand companions

- README badges/links to STABILITY.md
- Release notes mention MCP default `standard` (from U18)

## Human inputs & decisions

- Maintainer runs the downstream validation and tags the release

## Notes

- TODO item 163 (missing-data modes) is verified in U16. It is listed in the sweep for completeness.
