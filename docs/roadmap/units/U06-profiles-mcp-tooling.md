---
id: U06
slug: profiles-mcp-tooling
title: "MCP servers expose only the profile, and embedders have tools to write and check profiles"
track: Feature profiles
size: M
status: not-started
depends_on: [U05]
soft_depends_on: []
blocks: [U32]
todo_items: [29, 30, 31, 32, 33, 35]
branch: profiles-mcp-tooling
---

# U06 — profiles-mcp-tooling

**Outcome:** MCP servers expose only the profile, and embedders have tools to write and check profiles.

**Track:** Feature profiles · **Size:** M · **Depends on:** [U05](U05-profiles-enforcement.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

Instance-scoped MCP registration (tools, prompts, resources) with filtered tool-input enums, the full invisibility parity gate, and the embedder tooling (init / check / diff / show) plus example feature profile files and embedder docs.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P4 (MCP), P5 Tooling for embedders, P6 Gates

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#29** (2. Feature profiles — foundation › FP6 — MCP) Instance-scoped registration of tools, prompts and resources
- [ ] **#30** (2. Feature profiles — foundation › FP6 — MCP) Tool input schemas carry the instance's enums only
- [ ] **#31** (2. Feature profiles — foundation › FP6 — MCP) `TestProfileInvisibilityParity`
- [ ] **#32** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Feature-profile tooling `init`, `check`, `diff` and `show` (leaf naming open: `pulse profile` is taken by synth data profiling)
- [ ] **#33** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Example profile files in `examples/profiles/`
- [ ] **#35** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Embedder docs at `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md`

## Scope

**In scope**
- `gosdk.Register` / `mcpserve` register only enabled tools, prompts and resources
- Tool input schemas use instance enums
- `TestProfileInvisibilityParity` across all instance surfaces
- feature-profile `init|check|diff|show` leaves (not under `pulse profile`, which is synth's `profile create`)
- `examples/profiles/*.json`
- `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md` completed (both started in U04)

**Out of scope**
- Skills/examples filtering (U10)
- `pulse docs export` (U21)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(profiles-mcp-tooling/E<n>-S<m>): …`; close each epic with `milestone(profiles-mcp-tooling/E<n>): vertical slice complete — <epic title>`.

### E1 — MCP exposes only the profile
- S1: instance-scoped tool/prompt/resource registration
- S2: filtered enums in reflected tool input schemas
- S3: `TestProfileInvisibilityParity`

### E2 — Embedders can write, check and upgrade profiles
- S1: `init` (full exact-name list, grouped and commented)
- S2: `check`, `show`, `diff` (highlight features with `Since` newer than `written_with`)
- S3: example profiles (an agent-oriented one may carry `return: standard` only once U17 defines `return`; until then the key is refused as unknown)
- S4: embedder docs page + reference file

## Acceptance criteria

- [ ] `tools/list` on a profiled MCP server lists only enabled tools; their input enums contain no hidden names
- [ ] The parity gate passes for every example profile
- [ ] `init > p.json && check p.json` succeeds on a fresh checkout
- [ ] `diff` lists a feature whose `Since` is newer than `written_with`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestProfileInvisibilityParity`
- `TestSkillsCoverAllCliLeaves` (new `profile *` leaves documented)

## Update Demand companions

- `docs/src/cli/flags.md` rows for the tooling leaves
- CLAUDE.md MCP-layer paragraph (instance-scoped registration)

## Human inputs & decisions

- None.

## Inherited from U04

- **Already landed:** `pulse mcp --feature-profile` and `PULSE_FEATURE_PROFILE` (read only by `mcpserve.NewPulse`), `mcpserve.Describe`, and `behaviour.disable_cohort_scan` honoured by `gosdk.Register`. The flag validates and stores; this unit makes it filter.
- **MCP tool → feature bindings** are data in `internal/descriptor/features.go` (`MCPToolBindings`, plus the prompt → `mcp_extra` map), pinned to `toolmeta` by `TestFeaturesHaveSince`. Registration should consume them.
- **`BindOnInspect` rebinds tools by name** (`internal/mcp/bind.go` `mergeEnumNames`, `buildRequestSchemaWithExtensions`), bypassing a registration-time filter — filter the rebind too.
- **`toolmeta` descriptions name operators in prose**; filtering enums alone leaves hidden names in tool text.
- **Prompts, the `pulse://schema` resource and skill resources take no `*Pulse`** (`registerPrompts`, `registerSchemaResource`, `registerSkillResources`); thread the instance in.
- **Tooling leaf naming** is open: `pulse profile` belongs to synth data profiling.

## Inherited from U05

U05 enforced the feature set through `*Pulse` and the descriptor builders; contract in `.claude/reference/feature-profiles.md` ("Notes for U05 / U06"). Interim leaks this unit closes:

- **Profiled `pulse mcp` still advertises hidden names** through tool registration, tool-input enums, the `strict.go` message and `toolmeta` prose; the `pulse://schema` resource serves the full schema, not `p.PayloadSchema()`.
- **`strict.go` location keys** are `request_index` / `stage_index`; the service names `request` / `stage`. Unify. Slot and unknown-field logic already delegates to `descx.JSONObjectKeys` / `descx.UnknownFieldError`.
- **`TestProfileInvisibilityParity`**: the parity harness exists in the root package (`runHiddenParity`, per-entry-point tables); finalize it over MCP and add malformed-request cells (zone `tz`, field-ref parameter keys and strict categorical checks run before lookup and may diverge).
- **Runtime refusal text** under `internal/processing` and `internal/service` is not swept for hidden names; `SeriesOverlayRequest.Overlays` is method-level and ungated.
- **Label-table enum in `BindOnInspect`** (`labelTableNames`, `internal/mcp/bind.go`) lists every label table even when `capability:labels` is hidden; the manifest already lists none (U05 decision: a hidden capability's named tables are listed as if none were registered).
- **Public example profiles** (`examples/profiles/*.json`): U05's `minimal`, `survey-crosstab` and `empty` fixtures are private under `descriptor/testdata/profiles/`.
