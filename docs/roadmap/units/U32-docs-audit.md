---
id: U32
slug: docs-audit
title: "Pulse goes live with the most helpful, current and comprehensive documentation we can produce"
track: API & release
size: L
status: not-started
depends_on: [U01, U02, U02b, U02c, U06, U10, U18, U19, U20, U23, U30, U31]
soft_depends_on: [all feature units]
blocks: [U33]
todo_items: [159, 160, 161, 162, 163, 164, 165, 166, 167]
branch: docs-audit
---

# U32 — docs-audit

**Outcome:** Pulse goes live with the most helpful, current and comprehensive documentation we can produce.

**Track:** API & release · **Size:** L · **Depends on:** [U01](U01-release-pipeline.md), [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md), [U02c](U02c-cohort-facade.md), [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U18](U18-response-shaping-execution.md), [U19](U19-resource-limits.md), [U20](U20-observability.md), [U23](U23-guidance-mcp.md), [U30](U30-matrix-extensions-hardening.md), [U31](U31-guidance-guides.md) · **Soft:** all feature units · **Unblocks:** [U33](U33-v1-release.md)

## Summary

A deliberate final pass over every surface a human or agent reads (mdBook, README and repo docs, GoDoc, CLI help, skills, MCP descriptions, manifest and guidance text, error fixups, examples, CLAUDE.md and references), against six criteria: accurate, current, complete, helpful, consistent, runnable. Backed by automated checks that stay in CI after release, and validated by a fresh-reader review and an agent task evaluation over MCP.

## References

**Theme documents (read before starting):**
- [docs-audit 00 — Plan](../v1.0.0-docs-audit/00-plan.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#159** (11. Documentation audit) Documentation inventory and coverage matrix: every public API symbol, CLI leaf and flag, MCP tool / prompt / resource, operator, field type, error code, env var, `Options` field and request/response slot, mapped to where it is documented
- [ ] **#160** (11. Documentation audit) Automated checks in CI: link checker, runnable-snippet test, CLI help ↔ `flags.md` parity, GoDoc `Example*` functions for the public facade, removed-name scan
- [ ] **#161** (11. Documentation audit) `TestSkillTokenBudget` flipped from soft to hard-failing, with every skill within budget
- [ ] **#162** (11. Documentation audit) Accuracy and currency pass: mdBook site, `README.md` / `CONTRIBUTING.md` / `SECURITY.md` / `STABILITY.md`, `CLAUDE.md` and `.claude/reference/`
- [ ] **#163** (11. Documentation audit) Accuracy and currency pass: skills (atomic and topical), examples library, MCP tool / prompt / resource descriptions, error messages and fixups, manifest descriptions, `Purpose` / `Interpretation` / glossary
- [ ] **#164** (11. Documentation audit) Getting Started rewritten for v1 (install from GitHub Releases → first cohort → first analysis → first MCP session) and a single embedder guide (profiles, limits, observability, response shaping)
- [ ] **#165** (11. Documentation audit) Terminology made consistent with the glossary; pre-1.0 and removed names purged
- [ ] **#166** (11. Documentation audit) Fresh-reader review and agent task evaluation over MCP (~20 tasks, kept as a regression set); every failure fixed
- [ ] **#167** (11. Documentation audit) Findings log closed (fixed or deferred with reason and issue) and maintainer sign-off recorded

## Scope

**In scope**
- Coverage matrix of every public thing → where documented (generated where possible)
- Automated checks: link checker, runnable snippets, CLI help ↔ flags.md, GoDoc examples, removed-name scan, hard skill budgets
- Accuracy/currency passes over all surfaces
- v1 Getting Started + single embedder guide
- Terminology alignment with the glossary
- Fresh-reader review; agent task evaluation (~20 tasks, kept as a regression set)
- Findings log and sign-off

**Out of scope**
- The roadmap documents (`docs/roadmap/**`)
- New features. Gaps that need code are logged and deferred with an issue unless trivial

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(docs-audit/E<n>-S<m>): …`; close each epic with `milestone(docs-audit/E<n>): vertical slice complete — <epic title>`.

### E1 — Gaps are visible and checked automatically
- S1: coverage matrix (generated columns from manifest, payload schema, `go doc`, CLI tree)
- S2: link checker + runnable-snippet test in CI
- S3: CLI help ↔ `flags.md` parity; GoDoc `Example*` for the facade; removed-name scan
- S4: `TestSkillTokenBudget` hard-failing; trim over-budget skills

### E2 — Everything says what v1 actually does
- S1: accuracy/currency pass: mdBook, README/CONTRIBUTING/SECURITY/STABILITY, CLAUDE.md + references
- S2: accuracy/currency pass: skills, examples, MCP descriptions/prompts, error fixups, manifest, Purpose/Interpretation/glossary
- S3: terminology alignment; removed and pre-1.0 names purged

### E3 — New users and agents succeed on the first try
- S1: Getting Started for v1 + embedder guide
- S2: fresh-reader review; fixes
- S3: agent task evaluation over MCP; fixes until it passes; task set committed
- S4: findings log closed; sign-off

## Acceptance criteria

- [ ] The coverage matrix has no blank cell for any public thing
- [ ] Link checker, runnable-snippet, help-parity, GoDoc-example and removed-name checks are green in CI and remain there
- [ ] Every skill is within budget with `TestSkillTokenBudget` hard-failing
- [ ] The fresh reader completes Getting Started and two question guides without outside help
- [ ] The agent task evaluation passes every task
- [ ] Every finding is fixed or deferred with a reason and an issue link; maintainer sign-off recorded
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New: link check, `TestDocsSnippetsRun`, `TestCliHelpMatchesFlagsDoc`, `TestNoRemovedNamesInDocs`, GoDoc `Example*` tests
- `TestSkillTokenBudget` (now hard)
- Agent task evaluation (scripted; kept for future releases)

## Update Demand companions

- CLAUDE.md gate list (any new prefix-matched gates)
- `.claude/reference/skill-pack.md` (budget regime now hard)
- `docs/src/SUMMARY.md` (Getting Started / embedder guide placement)

## Human inputs & decisions

- Maintainer sign-off; a fresh reader (developer without a statistics background) for the review

## Notes

- The manifest's own `format_version` is hard-coded `"1.0"` (`descriptor/manifest.go`) while the envelope says `"1.1"`. Found during U01; reconcile it or document why they differ.
- Runs after every feature unit and before the release candidate, so the rc ships with the final docs and downstream validation exercises them too.
