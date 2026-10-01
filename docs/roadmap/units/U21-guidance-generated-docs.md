---
id: U21
slug: guidance-generated-docs
title: "Plain-language reference docs and skill sections generate themselves from metadata"
track: Guided analysis
size: M
status: not-started
depends_on: [U09, U10]
soft_depends_on: []
blocks: [U31]
todo_items: [85, 86, 87, 88, 89, 34]
branch: guidance-generated-docs
---

# U21 — guidance-generated-docs

**Outcome:** Plain-language reference docs and skill sections generate themselves from metadata.

**Track:** Guided analysis · **Size:** M · **Depends on:** [U09](U09-guidance-backfill-descriptive.md), [U10](U10-skill-ontology.md) · **Unblocks:** [U31](U31-guidance-guides.md)

## Summary

`internal/docgen` renders the operator catalog, glossary, "Reading your results" pages and the skill `## Use when` / `## Reading the output` sections from Purpose/Interpretation metadata, with a currency gate. Includes the profile-scoped `pulse docs export` / `p.ExportReference`.

## References

**Theme documents (read before starting):**
- [guided-analysis 02 — Documentation](../v1.0.0-guided-analysis/02-documentation.md) — D3 catalog, D4 Reading your results, D5 glossary, Gate & build notes
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P5 skill integration
- [feature-profiles 01 — Design & phasing](../v1.0.0-feature-profiles/01-design.md) — P4 Reference export

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#85** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) `internal/docgen`, `make docs` integration, `TestDocsGeneratedCurrent`
- [ ] **#86** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Generated operator catalog pages
- [ ] **#87** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Generated glossary page
- [ ] **#88** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) "Reading your results" pages (tests, regressions, matrices, overlays, components)
- [ ] **#89** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Rendered skill sections `## Use when` / `## Reading the output`; `skill-pack.md` updated, plus `TestSkillPurposeSectionsCurrent`
- [ ] **#34** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) `pulse docs export` / `p.ExportReference` (shares the G3 generator)

## Scope

**In scope**
- Doc generator + `make docs` + currency gate
- Catalog, glossary, reading-results pages
- Rendered skill sections (+ skill-pack budget update)
- `pulse docs export` / `p.ExportReference` (instance-scoped)

**Out of scope**
- Hand-written guides (U31)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-generated-docs/E<n>-S<m>): …`; close each epic with `milestone(guidance-generated-docs/E<n>): vertical slice complete — <epic title>`.

### E1 — Reference docs write themselves
- S1: `internal/docgen` + `TestDocsGeneratedCurrent`
- S2: catalog + glossary + reading-results pages under `docs/src/guide/`

### E2 — Skills and exports stay in sync
- S1: rendered skill sections between markers + `TestSkillPurposeSectionsCurrent`; `skill-pack.md` table/budget
- S2: `pulse docs export --profile` / `p.ExportReference`

## Acceptance criteria

- [ ] Regenerating docs on a clean tree is a no-op (gate)
- [ ] Every `op-*` skill has rendered `## Use when`; inferential ones have `## Reading the output`
- [ ] An export for an example profile contains no hidden operator
- [ ] `mdbook build` succeeds
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestDocsGeneratedCurrent`
- `TestSkillPurposeSectionsCurrent`
- `TestAtomicSkillHasRequiredSections` updated for new sections

## Update Demand companions

- `.claude/reference/skill-pack.md`
- `docs/src/SUMMARY.md` (Analysis Guide part)
- `docs/src/cli/flags.md` (`docs export`)

## Human inputs & decisions

- None.
