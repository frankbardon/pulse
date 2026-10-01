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

Instance-scoped MCP registration (tools, prompts, resources) with filtered tool-input enums, the full invisibility parity gate, and the embedder tooling `pulse profile init/check/diff/show` plus example profile files and embedder docs.

## References

**Theme documents (read before starting):**
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P4 (MCP), P5 Tooling for embedders, P6 Gates

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#29** (2. Feature profiles — foundation › FP6 — MCP) Instance-scoped registration of tools, prompts and resources
- [ ] **#30** (2. Feature profiles — foundation › FP6 — MCP) Tool input schemas carry the instance's enums only
- [ ] **#31** (2. Feature profiles — foundation › FP6 — MCP) `TestProfileInvisibilityParity`
- [ ] **#32** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) `pulse profile init`, `check`, `diff` and `show`
- [ ] **#33** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Example profile files in `examples/profiles/`
- [ ] **#35** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) Embedder docs at `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md`

## Scope

**In scope**
- `gosdk.Register` / `mcpserve` register only enabled tools, prompts and resources
- Tool input schemas use instance enums
- `TestProfileInvisibilityParity` across all instance surfaces
- `pulse profile init|check|diff|show`
- `examples/profiles/*.json`
- `docs/src/library/feature-profiles.md`; `.claude/reference/feature-profiles.md` completed

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
- S1: `pulse profile init` (full exact-name list, grouped and commented)
- S2: `check`, `show`, `diff` (highlight features with `Since` newer than `written_with`)
- S3: example profiles (incl. an agent-oriented one recommending `return: standard`)
- S4: embedder docs page + reference file

## Acceptance criteria

- [ ] `tools/list` on a profiled MCP server lists only enabled tools; their input enums contain no hidden names
- [ ] The parity gate passes for every example profile
- [ ] `pulse profile init > p.json && pulse profile check p.json` succeeds on a fresh checkout
- [ ] `pulse profile diff` lists a feature whose `Since` is newer than `written_with`
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestProfileInvisibilityParity`
- `TestSkillsCoverAllCliLeaves` (new `profile *` leaves documented)

## Update Demand companions

- `docs/src/cli/flags.md` rows for `profile init|check|diff|show`
- CLAUDE.md MCP-layer paragraph (instance-scoped registration)

## Human inputs & decisions

- None.
