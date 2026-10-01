---
id: U23
slug: guidance-mcp
title: "MCP agents are guided from intent to result in few round-trips"
track: Guided analysis
size: S
status: not-started
depends_on: [U22]
soft_depends_on: []
blocks: [U32]
todo_items: [95, 96, 97, 98, 99]
branch: guidance-mcp
---

# U23 — guidance-mcp

**Outcome:** MCP agents are guided from intent to result in few round-trips.

**Track:** Guided analysis · **Size:** S · **Depends on:** [U22](U22-recommend-explain.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), intent and question search over examples and skills with a synonym table, the intent-scoped manifest, and tool descriptions rewritten to lead with when to call them.

## References

**Theme documents (read before starting):**
- [guided-analysis 03 — MCP & API](../v1.0.0-guided-analysis/03-mcp-and-api.md) — M4 intents & prompts, M5 search & token optimizations

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#95** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), plus the prompt gate
- [ ] **#96** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) `pulse_examples_search {intent}` and question search; synonym table
- [ ] **#97** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) `pulse_skills_list {intent}`
- [ ] **#98** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) Intent-scoped manifest (`pulse_manifest {intent}`)
- [ ] **#99** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) MCP tool descriptions lead with when to call the tool

## Scope

**In scope**
- Intent prompts + gate
- Search by intent/question; synonym table
- `pulse_manifest {intent}`
- Tool description rewrite

**Out of scope**
- —

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-mcp/E<n>-S<m>): …`; close each epic with `milestone(guidance-mcp/E<n>): vertical slice complete — <epic title>`.

### E1 — Agents are guided by intent
- S1: generated intent prompts + `pulse-author-request` routing + gate
- S2: `pulse_examples_search` / `pulse_skills_list` by intent; synonym table
- S3: intent-scoped manifest; tool descriptions lead with when-to-call

## Acceptance criteria

- [ ] Each intent has a prompt that walks inspect → recommend → confirm → process → explain
- [ ] The intent-scoped manifest is roughly an order of magnitude smaller than the full one for a typical intent
- [ ] Prompts and search respect the instance profile
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestSkillsCoverAllMCPPrompts` (new)

## Update Demand companions

- CLAUDE.md MCP-layer paragraph (prompts)
- `skills/session-bootstrap.md`; tool skills

## Human inputs & decisions

- None.
