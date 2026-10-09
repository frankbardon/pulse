---
id: U23
slug: guidance-mcp
title: "MCP agents are guided from intent to result in few round-trips"
track: Guided analysis
size: M
status: done
depends_on: [U22]
soft_depends_on: []
blocks: [U32]
todo_items: [95, 96, 97, 98, 99, 246, 247]
branch: guidance-mcp
---

# U23 — guidance-mcp

**Outcome:** MCP agents are guided from intent to result in few round-trips.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U22](U22-recommend-explain.md) · **Unblocks:** [U32](U32-docs-audit.md)

## Summary

One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), intent and question search over examples and skills with a synonym table, the intent-scoped manifest, and tool descriptions rewritten to lead with when to call them.

## References

**Theme documents (read before starting):**
- [guided-analysis 03 — MCP & API](../v1.0.0-guided-analysis/03-mcp-and-api.md) — M4 intents & prompts, M5 search & token optimizations

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#95** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) One MCP prompt per intent (extending `pulse-bootstrap` / `pulse-author-request`), plus the prompt gate
- [x] **#96** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) `pulse_examples_search {intent}` and question search; synonym table
- [x] **#97** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) `pulse_skills_list {intent}`
- [x] **#98** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) Intent-scoped manifest (`pulse_manifest {intent}`)
- [x] **#99** (7. Guided analysis — docs, API & MCP › G5 — MCP guidance layer) MCP tool descriptions lead with when to call the tool
- [x] **#246** (7. Guided analysis › Follow-ups from U22) Compose and Facet predict get a direct facade, CLI and MCP entry (today their advisories reach callers only through Explain and bound Recommend; `PredictOptions.SidecarLoader` is wired from the Explain facade only)
- [x] **#247** (7. Guided analysis › Follow-ups from U22) The MCP core reflector (`internal/mcp/schema.go`) rendered `json.RawMessage` as a byte array (operator `params`, `pulse_recommend`'s `recommendations[].request`); it now renders any-JSON

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

## Consumes from U10

- Search by intent / question runs over the pruned instance graph (`p.Ontology()`), the same view `pulse_skills_*` and `pulse_examples_*` already read. `pulse_skills_list` has no intent argument today; adding it belongs here or in U22.
- toolmeta `DescExamplesSearch` / `DescExamplesGet` still say "embedded library"; reword them in the tool-description rewrite, since embedder examples are searchable.

## Inherited from U13

- **Closed in U23 (E5).** **MCP tool input schemas missed `multiplicity`.** The hand-built schemas in `internal/mcp/bind.go` do not list the `multiplicity` block (Request, `tests[]`, `overlays[]`, ComposedRequest, compose `overlays[]`), although the payload schema does and root keys still strict-decode through `VisibleSlotKeys`. Add the properties and drop them when `capability:multiplicity` is hidden, with a parity test against `Pulse.PayloadSchema()`. Both landed.

## Human inputs & decisions

- None.

## Inherited from U22

- **#246** — Compose and Facet predict get a direct facade, CLI and MCP entry (today their advisories reach callers only through Explain and bound Recommend; `PredictOptions.SidecarLoader` is wired from the Explain facade only). Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).
- **#247** — The MCP core reflector (`internal/mcp/schema.go`) rendered `json.RawMessage` as a byte array (operator `params`, `pulse_recommend`'s `recommendations[].request`); it now renders any-JSON. Found in U22 (PR #331); see [U22 handed on](U22-recommend-explain.md#handed-on).

## Landed

Merged without a release (rolls into v1.0.0); `format_version` stays `"1.1"`. Intent prompts and routing, `intent` on `pulse_examples_search` / `pulse_skills_list`, the synonym table (`Purpose.KnownAs` plus a curated list), the intent-scoped manifest, when-to-call tool descriptions, direct Compose / Facet / Chain predict (facade, CLI, MCP), and MCP input schemas that advertise `multiplicity` and any-JSON slots, with a parity test against the payload schema.

## Handed on

| Item | Owner |
|---|---|
| Fuzzy (typo-tolerant) example and skill search as a final `matchTier` after the literal and `KnownAs`/Sound tiers | [U31](U31-guidance-guides.md) #250 |
| `_meta.intent` (#102 wording) vs `_meta.intents` (what examples carry) | [U31](U31-guidance-guides.md) #251 |
| `pulse examples search` has no `--intent` flag | [U31](U31-guidance-guides.md) #252 |
| Compose predict does not judge slot operators by name (unknown or hidden operator predicts valid) | [U35](U35-predict-runtime-parity.md) #253 |
| Chain predict does not validate stage label bindings | [U35](U35-predict-runtime-parity.md) #254 |
| `PredictCompose` / `PredictFacet` / `PredictChain` and their CLI leaves ignore Strict and EchoRequest | [U35](U35-predict-runtime-parity.md) #255 |
| Bare MCP `pulse_predict` returns no `errors` / `warnings`, so `valid: false` has no reason | [U35](U35-predict-runtime-parity.md) #256 |
| `pulse_predict` description keeps the generic alternative-root sentence on profiles hiding compose, facet and process_chain | [U32](U32-docs-audit.md) #257 |
| `MAT_RELIABILITY` needs `Purpose.KnownAs` aliases ("cronbach's alpha") when it lands | [U24](U24-matrix-operators.md) #258 |
| Facet overlay `weight` is accepted and silently ignored; should refuse | [U35](U35-predict-runtime-parity.md) #259 |
| Bound `joins` field-name enums cover only the left cohort | [U35](U35-predict-runtime-parity.md) #260 |
| `session-bootstrap.md` at 5,999 of 6,000 bytes; the next addition must displace text | [U32](U32-docs-audit.md) #161 (extended) |
| `simulate` intent has no MCP tool, so `pulse-author-request` never routes it; the line appears if a synth tool is added | none, by design |
| Registration-time reflected input schemas carry hidden slot keys (documented in `.claude/reference/feature-profiles.md`) | none, by design |
