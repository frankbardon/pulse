---
id: U04
slug: profiles-model
title: "Every feature has a name, and a profile file can declare an instance's feature set"
track: Feature profiles
size: M
status: not-started
depends_on: [U02]
soft_depends_on: []
blocks: [U05, U11, U13]
todo_items: [10, 11, 12, 13, 14, 15, 16]
branch: profiles-model
---

# U04 — profiles-model

**Outcome:** Every feature has a name, and a profile file can declare an instance's feature set.

**Track:** Feature profiles · **Size:** M · **Depends on:** [U02](U02-public-surface.md) · **Unblocks:** [U05](U05-profiles-enforcement.md), [U11](U11-weighting-descriptive.md), [U13](U13-multiplicity.md)

## Summary

Build the feature registry (names, kinds, `Since`, dependency graph, always-present core), plus the profile file model and its validation at `pulse.New`. Nothing is hidden yet: this unit makes profiles *parse and validate*. U05 makes them take effect.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P1 What a feature is, P2 Profile shape, P3 Configuration sources
- [feature-profiles 00 — Feasibility & decisions](../v1.0.0-feature-profiles/00-feasibility.md) — Decisions 1–7

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#10** (2. Feature profiles — foundation › FP1 — Feature registry) Stable feature names and kinds (`capability`, `operator`, `io_format`, `mcp_extra`) across all registries
- [ ] **#11** (2. Feature profiles — foundation › FP1 — Feature registry) `Since` version on every registration, plus `TestFeaturesHaveSince`
- [ ] **#12** (2. Feature profiles — foundation › FP1 — Feature registry) Dependency graph in `descriptor/dependencies.go`, plus `TestProfileDependenciesComplete`
- [ ] **#13** (2. Feature profiles — foundation › FP1 — Feature registry) Always-present core defined: manifest, payload schema, skills, examples, errors lookup, inspect, predict
- [ ] **#14** (2. Feature profiles — foundation › FP2 — Profile model) Profile file format: an exact-name allowlist, `written_with`, optional `behaviour`
- [ ] **#15** (2. Feature profiles — foundation › FP2 — Profile model) `pulse.New` validation and the config codes (`PULSE_PROFILE_FEATURE_UNKNOWN`, `PULSE_PROFILE_DEPENDENCY`), plus `TestProfileRejectsPatterns`
- [ ] **#16** (2. Feature profiles — foundation › FP2 — Profile model) `Options.Profile`, `Options.ProfileFile`, the `PULSE_PROFILE` env var, and `pulse mcp --profile` (the CLI is otherwise unprofiled)

## Scope

**In scope**
- Feature kinds `capability` / `operator` / `io_format` / `mcp_extra` derived from existing registries
- `Since` on every registration (all `1.0.0`)
- `descriptor/dependencies.go` + completeness gate
- Profile JSON: exact names only, `written_with`, optional `behaviour` / `limits` / `return` sections (schemas reserved; limits and return are wired in later units)
- `Options.Profile`, `Options.ProfileFile`, `PULSE_PROFILE`, `pulse mcp --profile`
- Config codes `PULSE_PROFILE_FEATURE_UNKNOWN`, `PULSE_PROFILE_DEPENDENCY`

**Out of scope**
- Filtering any surface (U05, U06, U10)
- The CLI is not profiled (decided), except `pulse mcp --profile`

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(profiles-model/E<n>-S<m>): …`; close each epic with `milestone(profiles-model/E<n>): vertical slice complete — <epic title>`.

### E1 — Every feature has a stable name, a `Since` and its dependencies
- S1: feature registry over existing registries + `capability` / `io_format` / `mcp_extra` lists
- S2: `Since` field + `TestFeaturesHaveSince`
- S3: dependency graph (component readers, host capabilities) + `TestProfileDependenciesComplete`

### E2 — A profile file validates at startup
- S1: profile types + JSON decode (strict; patterns rejected)
- S2: `pulse.New` validation with coded config errors and fixups
- S3: `Options.Profile` / `ProfileFile` / `PULSE_PROFILE` / `pulse mcp --profile` plumbing (stored on the instance, not yet applied)

## Acceptance criteria

- [ ] `pulse.New` with no profile behaves exactly as today (no output change)
- [ ] A profile with a typo, a pattern, or a missing dependency fails `pulse.New` with the documented code and a fixup naming the problem
- [ ] The dependency gate fails if an operator reading another's components lacks an edge
- [ ] `PULSE_PROFILE` is documented
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestFeaturesHaveSince`
- `TestProfileDependenciesComplete`
- `TestProfileRejectsPatterns`

## Update Demand companions

- CLAUDE.md "Build / Env" (`PULSE_PROFILE`) + `skills/session-bootstrap.md` (`TestClaudeMdMentionsAllEnvVars`)
- `errors/fixup_metadata.go` for the config codes
- `update-demand.md` row: new feature → `Since` + dependency edges
- `.claude/reference/feature-profiles.md` (start it here)

## Human inputs & decisions

- None.
