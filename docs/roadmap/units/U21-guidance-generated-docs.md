---
id: U21
slug: guidance-generated-docs
title: "Plain-language reference docs and skill sections generate themselves from metadata"
track: Guided analysis
size: M
status: done
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

- [x] **#85** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) `internal/docgen`, `make docs` integration, `TestDocsGeneratedCurrent`
- [x] **#86** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Generated operator catalog pages
- [x] **#87** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Generated glossary page
- [x] **#88** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) "Reading your results" pages (tests, regressions, matrices, overlays, components)
- [x] **#89** (7. Guided analysis — docs, API & MCP › G3 — Generated docs & skill sections) Rendered skill sections `## Use when` / `## Reading the output`; `skill-pack.md` updated, plus `TestSkillPurposeSectionsCurrent`
- [x] **#34** (2. Feature profiles — foundation › FP7 — Embedder tooling & export) `pulse docs export` / `p.ExportReference` (shares the G3 generator)

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

- [x] Regenerating docs on a clean tree is a no-op (`TestDocsGeneratedCurrent`)
- [x] Every `op-*` skill has rendered `## Use when`; inferential ones have `## Reading the output` (`TestSkillPurposeSectionsCurrent`)
- [x] An export for an example profile contains no hidden operator (`TestProfileExportTreeSweep`)
- [x] `mdbook build` succeeds (the `docs` CI job)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- `TestDocsGeneratedCurrent`
- `TestSkillPurposeSectionsCurrent`
- `TestAtomicSkillHasRequiredSections` updated for new sections

## Update Demand companions

- `.claude/reference/skill-pack.md`
- `docs/src/SUMMARY.md` (Analysis Guide part)
- `docs/src/cli/flags.md` (`docs export`)

## Consumes from U10

- Walk `p.Ontology()` (pruned per instance) rather than the embedded pack, so generated catalog, reference and rendered skill sections follow the instance. Skill bodies already render by feature fences and a `## See` by edge (`p.Skill(name)`); generated sections must be fenced or ontology-driven the same way (`TestSkillsCoverFeatureFences` binds).
- CLI `pulse skills` / `pulse examples` list only the embedded library, not embedder additions: decide whether the export covers `Extensions.Skills` / `Extensions.Examples`.

## Inherited from U13

- **Prose names a hideable slot.** U13 reworded the `TEST_*` / `OVERLAY_*` Purpose and Interpretation prose (`internal/descriptor/purposes_*.go`, `interpretations*.go`) to point at the `multiplicity` slot and `p_adjusted`. The feature-profile prose scrub keys on operator and tool tokens, not request-slot names, so an instance hiding `capability:multiplicity` serves prose naming a slot it refuses. Generated docs and the profile-scoped `pulse docs export` must render that sentence only when the capability is enabled (or the scrub must learn slot names); add a parity case to the profile pruning tests.
- Guidance lint gained rule `MULTI-COMP-MANUAL` (no hand-correction wording); generated sections must pass it.

## Human inputs & decisions

- None.

## Landed

Merged without a release (rolls into v1.0.0); `format_version` stays `"1.1"`. Epics ran E1 read-time skill sections and the slot-token scrub, E2 `internal/docgen` and the instance export, E3 the public book and the contracts. Guide: [Analysis Guide](../../src/guide/index.md) and [Guided analysis](../../src/library/guided-analysis.md); contracts `.claude/reference/guided-analysis.md`, `feature-profiles.md` (Reference export), `skill-pack.md` (Generated sections).

- **Surface (#85, #34).** `internal/docgen` renders an `InstanceSnapshot` into a deterministic Markdown tree; `p.ExportReference(fs, dir, ExportReferenceOptions{OmitSkills})` and `pulse docs export --out DIR [--feature-profile] [--no-skills] [--json]` write it under a `.pulse-docs-export` marker (`PULSE_DOCS_EXPORT_DIR_NOT_EMPTY`). `make docs` runs `make docs-generate` first, which regenerates the committed `docs/src/guide` and splices the `docs/src/SUMMARY.md` span (`internal/tools/docsummary`).
- **Pages (#86-#88).** Operator catalog per category, glossary, and `reading/{test,regression,matrix,overlay,descriptive,components}` pages.
- **Skill sections (#89).** `## Use when` and `## Reading the output` render at read time from Purpose and Interpretation through markers in all 168 `op-*` skills.
- **Gates.** `TestDocsGeneratedCurrent`, `TestSkillPurposeSectionsCurrent`, `TestProfileExportTreeSweep`, `TestProfileRenderedSkillSweep`, `TestProfileHiddenSlotTokenSweep`, `TestSlotTokens_EveryRequestSlotCapability`.

### Deviations from the plan

- **No `internal/docgen` main.** The generator is a library package behind `p.ExportReference` and the `docs export` leaf; `make docs-generate` calls the CLI.
- **Skill sections are rendered at read time, not committed.** The skill files carry only a marker line; `skills.Get`, `p.Skill` and the export substitute the prose, so a profiled instance gets pruned text. Generated hard caps: `## Use when` 450 B, `## Reading the output` 600 B; overflow truncates, never fails.
- **Budget split.** `TestSkillTokenBudget` measures the hand-written body (generated sections excluded), so adding the sections moved no skill over budget.
- **All skills are in the book.** The export includes every skill the instance serves (279 at landing; use `len(skills.List())`), not only operator skills.
- **Slot-token scrub added.** The scrub learned request-slot tokens (`multiplicity`, `p_adjusted`, `matrices`, ...) via `slotTokens`, as U13 asked.
- **Roadmap relocation.** The skill-sync work U09 handed to this unit moved to the new [U38](U38-skill-sync.md).

### Decisions

- The reading marker sits at the end of `## Output`; `op-reg-mod-resample` and `op-reg-mod-selection` have a use-when marker but no Purpose, and are exempted in `TestSkillPurposeSectionsCurrent`.
- Glossary terms are kept by owning operators; their `Forms` render verbatim, so "transition matrices" survives a matrices-hidden instance with its term.
- Topical-skill sentences naming a hideable slot are fenced (12 sentences in 6 skills, one reworded matrices to grids).
- The `.pulse-docs-export` marker lands in the committed `docs/src/guide` tree (deterministic).
- The Components page intro's source of truth is `internal/docgen/components_intro.md`.
- Per-skill hand-written cap measurement: `skills.RenderGenerated(RenderFences(raw), nil)`.

## Handed on

| Item | Owner |
|---|---|
| Stale hand-written skill claims (`t_critical`, leverage range, `order_by`, `WIN_LAG` offset and output type, `GROUP_RANGE` / `ROUNDED` interval), the over-budget `op-*` bodies and the hard budget flip (#235, #236) | [U38](U38-skill-sync.md) |
| `pulse skills` / `pulse examples` and toolmeta "embedded library" wording vs embedder additions; synth example tags (#237) | [U38](U38-skill-sync.md) |
| Hidden-capability prose the slot filter does not reach: matrices-hidden `pulse_examples_search` / `pulse_manifest` sentences (splitting moves the manifest golden), unscrubbed topical and tool skill bodies, weighted aggregator keys on a weighting-hidden Components page (#238) | [U32](U32-docs-audit.md) |
| Search index about 12 MB warning; the `docs` CI job is not a required check (#239) | [U32](U32-docs-audit.md) |
| Registration-time reflected MCP input schemas still carry hidden slot property KEYS (`multiplicity`, `matrices`) on a profiled instance (pre-existing; the U13 `bind.go` row) | [U23](U23-guidance-mcp.md) |
| The Update Demand rows for Purpose, Interpretation, glossary, skills and `ComponentSchema` now name `make docs` and `TestDocsGeneratedCurrent`; U31's guides sit beside the generated tree, not inside it | [U31](U31-guidance-guides.md) |
