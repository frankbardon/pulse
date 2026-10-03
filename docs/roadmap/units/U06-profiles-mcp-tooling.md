---
id: U06
slug: profiles-mcp-tooling
title: "MCP servers expose only the profile, and embedders have tools to write and check profiles"
track: Feature profiles
size: M
status: done
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

- [x] **#29** (2. Feature profiles — foundation › FP6 — MCP) Instance-scoped registration of tools, prompts and resources
- [x] **#30** (2. Feature profiles — foundation › FP6 — MCP) Tool input schemas carry the instance's enums only
- [x] **#31** (2. Feature profiles — foundation › FP6 — MCP) `TestProfileInvisibilityParity`
- [x] **#32** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Feature-profile tooling `init`, `check`, `diff` and `show` (leaf naming open: `pulse profile` is taken by synth data profiling)
- [x] **#33** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Example profile files in `examples/profiles/`
- [x] **#35** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Embedder docs at `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md`

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

### E1 — Profiled MCP server exposes only the profile
- Instance-scoped tool / prompt / resource registration, scoped bound schemas, strict decode and prose, skill / example discovery prune
### E2 — Public example profiles + MCP invisibility parity
- `examples/profiles/*.json`, `ExampleFeatureProfiles()` / `ExampleFeatureProfile(name)`, `TestProfileInvisibilityParity`
### E3 — Embedders can write, check and upgrade profiles
- `InitFeatureProfile`, `CheckFeatureProfile`, `DiffFeatureProfile`, `DescribeFeatureProfile`; `pulse features {init,check,diff,show}`
### E4 — Docs and roadmap close-out
- Embedder docs, contract reference, roadmap bookkeeping

## Acceptance criteria

- [x] `tools/list` on a profiled MCP server lists only enabled tools; their input enums contain no hidden names
- [x] The parity gate passes for every example profile
- [x] `init > p.json && check p.json` succeeds on a fresh checkout
- [x] `diff` lists a feature whose `Since` is newer than `written_with`
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

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
- **Prompts and skill resources take no `*Pulse`** (`registerPrompts`, `registerSkillResources`); thread the instance in (`registerSchemaResource` already takes it — landed in U05).
- **Tooling leaf naming** is open: `pulse profile` belongs to synth data profiling.

## Inherited from U05

U05 enforced the feature set through `*Pulse` and the descriptor builders; contract in `.claude/reference/feature-profiles.md` ("Notes for U05 / U06"). Interim leaks this unit closes:

- **Profiled `pulse mcp` still advertises hidden names** through tool registration, tool-input enums and `toolmeta` prose.

Landed in U05 after all (no longer this unit's): the `pulse://schema` resource serves `p.PayloadSchema()`; the `strict.go` location keys are the service's `request` / `stage` (slot and unknown-field logic already delegates to `descx.JSONObjectKeys` / `descx.UnknownFieldError`); runtime refusal text under `internal/processing` / `internal/service` names only offered built-ins (remedy clauses rewritten by `ExtensionRegistry.ScopeRefusal`, the chain gate's non-scalar note at its site; `TestProfileRuntimeRefusalSweep`); malformed-request parity cells over every library entry point incl. Predict (`TestHiddenOperatorMalformedParity`: zone `tz`, field-ref parameter keys, empty filter field, strict categorical, decimal128 dispatch, facet `additive_fields` over an expression filter, crosstab fused-gate bails), which found and fixed the decimal-dispatch and facet-additive divergences; a hidden `capability:labels` / `capability:range_tables` makes its registered tables resolve as unregistered at run time and in predict (`TestHiddenNamedTableParity`).
- **`TestProfileInvisibilityParity`**: the parity harness exists in the root package (`runHiddenParity` / `runHiddenParityWith`, per-entry-point tables, malformed cells in `feature_parity_malformed_test.go`); finalize it over MCP (add MCP tool entry points to the same tables).
- **`SeriesOverlayRequest.Overlays`** is method-level and ungated (methods are ungated by design; revisit only if a served surface exposes it).
- **Label-table enum in `BindOnInspect`** (`labelTableNames`, `internal/mcp/bind.go`) lists every label table even when `capability:labels` is hidden; the manifest already lists none (U05 decision: a hidden capability's named tables are listed as if none were registered).
- **Public example profiles** (`examples/profiles/*.json`): U05's `minimal`, `survey-crosstab` and `empty` fixtures are private under `descriptor/testdata/profiles/`.

## Landed deviations

Contract of record is `.claude/reference/feature-profiles.md` ("Notes for U06", "Example profiles", "Tooling (U06)"); embedder prose is `docs/src/library/feature-profiles.md`.

- **Instance-scoped MCP.** `gosdk.Register` mounts tools by `MCPToolBindings` (core-bound always; feature-bound iff enabled), prompts by `mcp_extra:prompt_*`, and skips the cohort scan without `mcp_extra:cohort_resources`. A hidden tool, prompt or resource reads byte-identically to a never-registered one. `RegisteredTools()` / `RegisteredPrompts()` stay global.
- **Scoped schemas and strict decode.** `BindOnInspect` binds through `BindForInstance`: enums keep only enabled names, an enum filtered to nothing is omitted, hidden slot keys are refused as unknown (even when empty).
- **Prose scrub.** Tool, prompt and schema descriptions go through the manifest's whole-token sentence scrub (`descx.ProseScrub`); a sentence mixing enabled and hidden names is dropped whole. Profile-free wording was reworded where it abbreviated operator lists (manifest goldens moved by those rewords only).
- **Skills and examples pulled forward from U10 (minimal).** A per-instance prune (`inst.Discovery()`) hides atomic skills and examples whose surface is hidden, and a pruned name reads like a nonexistent one (the design said direct reads stay). Topical bodies are exempt until U10; `pulse skills` / `pulse examples` stay unprofiled.
- **Tooling.** Leaves live under `pulse features` (not `pulse profile`); `init` writes strict JSON (no comments); CLI `check` is offline-only (extension-like unresolved names warn); every leaf exits non-zero on failure under `--json`. `written_with` is stamped from the running release.
- **Parity gate.** `TestProfileInvisibilityParity` runs the operator harness through MCP and sweeps every rendered surface; its single exemption is a topical skill body.

## Handed to U10

- Topical skill body fencing / rendering (`TestSkillsCoverProfileGet`).
- The line-wise atomic-skill scrub can break a table or code fence it cuts into.
- Manifest `skills[].description` is not routed through `Discovery.renderMetadata`.
- Overlay examples with an empty `_meta.operators` match only by body token.
- Capability-keyed pruning past facet / crosstab / joins / compose.
- Housekeeping (done): the host-path profile-file reader is one function, `internal/profilefile.ReadOS`, shared by `mcpserve.NewPulse` and `pulse features`.
