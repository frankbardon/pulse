---
id: U04
slug: profiles-model
title: "Every feature has a name, and a profile file can declare an instance's feature set"
track: Feature profiles
size: M
status: done
depends_on: [U02, U02b]
soft_depends_on: []
blocks: [U05, U11, U13]
todo_items: [10, 11, 12, 13, 14, 15, 16]
branch: profiles-model
---

# U04 — profiles-model

**Outcome:** Every feature has a name, and a profile file can declare an instance's feature set.

**Track:** Feature profiles · **Size:** M · **Depends on:** [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md) (landed: `extend` public, `processing` internal) · **Unblocks:** [U05](U05-profiles-enforcement.md), [U11](U11-weighting-descriptive.md), [U13](U13-multiplicity.md)

## Summary

Build the feature registry (names, kinds, `Since`, dependency graph, always-present core), plus the feature profile file model and its validation at `pulse.New`. Nothing is hidden yet: this unit makes feature profiles *parse and validate*, and only their `behaviour` switches take effect. U05 makes the feature list take effect.

**Naming (amended).** "Profile" is renamed **feature profile** everywhere — `pulse.FeatureProfile`, `Options.FeatureProfile` / `FeatureProfileFile`, `PULSE_FEATURE_PROFILE`, `pulse mcp --feature-profile`, `PULSE_FEATURE_PROFILE_*` — because synth data profiling already owns the bare name (`Pulse.Profile`, `pulse profile create`, `PULSE_PROFILE_FIELD_UNSUPPORTED`). Contract: `.claude/reference/feature-profiles.md`; embedder docs: `docs/src/library/feature-profiles.md`.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P1 What a feature is, P2 Profile shape, P3 Configuration sources
- [feature-profiles 00 — Feasibility & decisions](../v1.0.0-feature-profiles/00-feasibility.md) — Decisions 1–7

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#10** (2. Feature profiles — foundation › FP1 — Feature registry) Stable feature names and kinds (`capability`, `operator`, `io_format`, `mcp_extra`) across all registries
- [x] **#11** (2. Feature profiles — foundation › FP1 — Feature registry) `Since` version on every registration, plus `TestFeaturesHaveSince`
- [x] **#12** (2. Feature profiles — foundation › FP1 — Feature registry) Dependency graph in `internal/descriptor/features.go` (amended from `descriptor/dependencies.go`), plus `TestProfileDependenciesComplete`
- [x] **#13** (2. Feature profiles — foundation › FP1 — Feature registry) Always-present core defined: manifest, payload schema, skills, examples, errors lookup, inspect, predict
- [x] **#14** (2. Feature profiles — foundation › FP2 — Profile model) Profile file format: an exact-name allowlist, `written_with`, optional `behaviour`
- [x] **#15** (2. Feature profiles — foundation › FP2 — Profile model) `pulse.New` validation and the config codes (`PULSE_FEATURE_PROFILE_INVALID`, `PULSE_FEATURE_PROFILE_UNKNOWN`, `PULSE_FEATURE_PROFILE_DEPENDENCY`), plus `TestProfileRejectsPatterns`
- [x] **#16** (2. Feature profiles — foundation › FP2 — Profile model) `Options.FeatureProfile`, `Options.FeatureProfileFile`, the `PULSE_FEATURE_PROFILE` env var (read only by `pulse mcp` / `mcpserve.NewPulse`), and `pulse mcp --feature-profile` (the CLI is otherwise unprofiled)

## Scope

**In scope**
- Feature kinds `capability` / `operator` / `io_format` / `mcp_extra` derived from existing registries
- `Since` on every registration (all `1.0.0`)
- dependency graph in `internal/descriptor/features.go` + completeness gate
- Feature profile JSON: exact names only, `written_with`, optional `behaviour`; `limits` (U19) and `return` (U17) are refused as unknown keys until those units define them
- `Options.FeatureProfile`, `Options.FeatureProfileFile`, `PULSE_FEATURE_PROFILE`, `pulse mcp --feature-profile`
- Config codes `PULSE_FEATURE_PROFILE_INVALID`, `PULSE_FEATURE_PROFILE_UNKNOWN`, `PULSE_FEATURE_PROFILE_DEPENDENCY`

**Out of scope**
- Filtering any surface (U05, U06, U10)
- The CLI is not profiled (decided), except `pulse mcp --feature-profile`

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(profiles-model/E<n>-S<m>): …`; close each epic with `milestone(profiles-model/E<n>): vertical slice complete — <epic title>`.

### E1 — Every feature has a stable name, a `Since` and its dependencies
- S1: feature registry over existing registries + `capability` / `io_format` / `mcp_extra` lists
- S2: `Since` field + `TestFeaturesHaveSince`
- S3: dependency graph (component readers, host capabilities) + `TestProfileDependenciesComplete`

### E2 — A profile file validates at startup
- S1: profile types + JSON decode (strict; patterns rejected)
- S2: `pulse.New` validation with coded config errors and fixups
- S3: `Options.FeatureProfile` / `FeatureProfileFile` / `PULSE_FEATURE_PROFILE` / `pulse mcp --feature-profile` plumbing (stored on the instance, not yet applied)

## Acceptance criteria

- [x] `pulse.New` with no profile behaves exactly as today (no output change)
- [x] A profile with a typo, a pattern, or a missing dependency fails `pulse.New` with the documented code and a fixup naming the problem
- [x] The dependency gate fails if an operator reading another's components lacks an edge
- [x] `PULSE_FEATURE_PROFILE` is documented
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestFeaturesHaveSince`
- `TestProfileDependenciesComplete`
- `TestProfileRejectsPatterns`

## Update Demand companions

- CLAUDE.md "Build / Env" (`PULSE_FEATURE_PROFILE`) (`TestClaudeMdMentionsAllEnvVars`). **Not** `skills/session-bootstrap.md` or any runtime skill (amended): a served skill describing profiles tells an agent hidden features exist
- `errors/fixup_metadata.go` for the config codes
- `update-demand.md` row: new feature → `Since` + dependency edges
- `.claude/reference/feature-profiles.md` (started here) + `docs/src/library/feature-profiles.md`

## Human inputs & decisions

- Decided in the unit interview; see Landed deviations.

## Landed deviations

- **Renamed to "feature profile"** throughout (see Summary). The design's `PULSE_PROFILE_FEATURE_UNKNOWN` / `PULSE_PROFILE_DEPENDENCY` became `PULSE_FEATURE_PROFILE_UNKNOWN` / `_DEPENDENCY`, plus a third code `PULSE_FEATURE_PROFILE_INVALID` for structural faults (unreadable file, malformed JSON, unknown key, missing `features`, duplicates, both options set). Classes run INVALID → UNKNOWN → DEPENDENCY, stop at the first failing class, and report every instance in it, sorted; each carries a `details.reason`.
- **Feature table is internal** (`internal/descriptor/features.go`), not a `Since` field on registrations or `descriptor.Operator`: the manifest and public-API goldens did not move for it. The dependency graph lives in the same file (no `descriptor/dependencies.go`).
- **Capability names:** `index` and `shard` (read and write siblings collapse; no `index_build` / `shard_write`), and `sample`, `labels`, `range_tables`, `filter_to_file` added. `recommend` / `explain` arrive with their units. One `io_format` name gates import and export; `TEST_X` is one name for both tiers.
- **Not features:** synth distributions (`capability:synth` gates all of them), field types, named tables, expr functions, behaviour switches. Core adds `open`, `count_records` and `cohort_artifacts` to the design's list.
- **Dependencies** are an AND of any-of groups derived from rules: operators need a request host (`process` / `compose` / `process_chain`); `stream` / `watch` / `filter_to_file` need `process`; overlays need a host whose handler map lists them. Hard edges: the Welford-reading overlays → `AGG_WELFORD`, `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z` → `AGG_WEIGHTED_MEAN`, `ATTR_REG_*` → `REG_OLS`, and `OVERLAY_YOY` → `GROUP_DATE` (beyond the design: its series host must lead with a date grouper). **No `TEST_TUKEY_HSD` → `TEST_ANOVA_F` edge**: the pairing is advice.
- **Extensions are features** listed by registered name, without `Since`. Operator registrations gained optional `DependsOn []string` (single-name AND groups), validated at every `pulse.New`, profile or not.
- **`Since` compare** uses the running build's major.minor.patch core, so pre-releases offer their line's features; `devel`, SHAs, unparseable and `0.0.0` pseudo-versions count as newest. `written_with` is stored, never compared. Fixing this exposed that `pulse.Version()` reported an embedder's own module version; it now resolves Pulse's module from build-info `Deps`.
- **`limits` / `return` are refused**, not reserved-and-ignored: an unknown key is a hard error until U19 / U17 define them.
- **Behaviour switches take effect now**, ORed into `Options` (a profile can turn one on, never off); `disable_cohort_scan` reaches `gosdk.Register` through an internal bridge hook.
- **The env var is read only by `mcpserve.NewPulse`** (and so `pulse mcp`), never by `pulse.New`, because every CLI leaf calls `pulse.New`. Flag and env are host OS paths, not `DataDir`-relative; `Options.FeatureProfileFile` stays on the instance Fs. New public `pulse.ParseFeatureProfile`, `mcpserve.NewPulse`, `mcpserve.Describe`.
- **A nil `Features` slice is invalid**; Go embedders write `[]string{}` for an empty profile.
- **Open, for U05 / U06:** the stored-profile accessor; tooling leaf naming (`pulse profile` is taken); `BindOnInspect` rebinds tools by name; `toolmeta` prose names operators; prompts and schema / skill resources take no `*Pulse`; extension operators omitted from a profile become hidden once U05 applies the list. Recorded in `.claude/reference/feature-profiles.md` (Notes for U05 / U06).
