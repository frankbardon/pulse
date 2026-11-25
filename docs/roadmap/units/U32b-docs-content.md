---
id: U32b
slug: docs-content
title: "Every surface a reader or agent reads is accurate, current, complete and concise, and the docs baselines are empty"
track: API & release
size: L
status: not-started
depends_on: [U32]
soft_depends_on: []
blocks: [U32c, U33]
todo_items: [162, 163, 164, 165, 371, 504, 505, 557, 666, 667, 757, 758, 759, 760, 761, 762, 763, 764, 765, 766]
branch: docs-content
---

# U32b — docs-content

**Outcome:** Every surface a reader or agent reads is accurate, current, complete and concise, and the docs baselines are empty.

**Track:** API & release · **Size:** L · **Depends on:** [U32](U32-docs-audit.md) · **Unblocks:** [U32c](U32c-docs-validation.md), [U33](U33-v1-release.md)

## Summary

U32 builds the checks (coverage matrix, link checker, runnable snippets, help parity, removed-name scan) with shrink-only baseline allowlists. This unit does the content work those checks measure: it burns every allowlist down to empty, then locks that in with `TestDocsBaselinesEmpty`. The principle throughout is **docs informative but concise**: say what a reader needs, point to the rest, and cut prose that restates code or another page.

It also owns the content half of #165 (terminology aligned to the glossary; pre-1.0 and removed names purged). U32 lands the scan, and this unit clears what it finds and ticks #165.

## References

**Theme documents (read before starting):**
- [docs-audit 00 — Plan](../v1.0.0-docs-audit/00-plan.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#162** (11. Documentation audit) Accuracy and currency pass: mdBook site, `README.md` / `CONTRIBUTING.md` / `SECURITY.md` / `STABILITY.md`, `CLAUDE.md` and `.claude/reference/`
- [ ] **#163** (11. Documentation audit) Accuracy and currency pass: skills (atomic and topical), examples library, MCP tool / prompt / resource descriptions, error messages and fixups, manifest descriptions, `Purpose` / `Interpretation` / glossary
- [x] **#164** (11. Documentation audit) Getting Started rewritten for v1 (install from GitHub Releases → first cohort → first analysis → first MCP session) and a single embedder guide (profiles, limits, observability, response shaping)
- [ ] **#371** (Follow-ups from the showcase examples) Docs: `--source-tz` accepts fixed `+HH:MM` offsets while request `time_zone` refuses them (state the asymmetry); projection's benefit is schema-dependent; the showcase READMEs are re-verified after the units that simplify them land
- [ ] **#504** (Handed on from U31) The perceptual-maps primer (`docs/src/concepts/perceptual-maps.md`) describes a recipe (PCA loadings → `ATTR_PC_SCORE` per row → group averages → `MAT_DISTANCE` over the group centres) with no runnable example and no story golden behind it; add the example chain and pin its output, then point the primer at it
- [ ] **#505** (Handed on from U31) `skills/session-bootstrap.md` does not mention the CLI `pulse examples search --intent`; the skill sits at its topical budget. Make room and add the line, or record why the CLI flag needs no bootstrap mention
- [ ] **#557** The `compose-requests` skill has no route to `compose-optimize` (hard budget); make room or route through another skill
- [ ] **#666** Library doc pages for the embedder surfaces that have none: `extend`, `mcpserve` / `mcp/gosdk`, `synth` as a Go package, the `io` factory, lookup / range / label tables (`PULSE_LOOKUP_TABLES_DIR`, `PULSE_RANGE_TABLES_DIR`, `PULSE_LABEL_TABLES_DIR`, absent from `docs/src/library/options.md`) and SEM
- [ ] **#667** MCP protocol-version documentation: which protocol revision(s) the server speaks, how negotiation behaves, and the go-sdk upgrade policy; mirrored as a `STABILITY.md` MCP row
- [ ] **#165** (11. Documentation audit) Terminology made consistent with the glossary; pre-1.0 and removed names purged. [U32](U32-docs-audit.md) landed the scan (`TestNoRemovedNamesInDocs`); this unit does the content purge
- [x] **#757** (11. Documentation audit) `make sites` misses whole-repo runners and floors (the every-example runner, the `ReadSchema` call-site floor) and the per-case-table attribution inside a shared generator (the rest of #453)
- [ ] **#758** (11. Documentation audit) `docs/src/examples/library.md` tag table drifted from `CanonicalTags`; stale per-section counts
- [x] **#759** (11. Documentation audit) `flags.md` per-leaf granularity: the flags check is names-only across the tree
- [ ] **#760** (11. Documentation audit) Word-level capability prose in topical skills under a hidden feature (no token, so the served-text sweep cannot fence it)
- [x] **#761** (11. Documentation audit) The removed-name scan does not reach MCP prompt bodies or the `ExportReference` output; add a heavy sibling
- [x] **#762** (11. Documentation audit) Example `crosstab-share-of-col-and-total` names `ApplyOverlays` (the `removed-names.txt` entry)
- [x] **#763** (11. Documentation audit) Twelve generated skill-page JSON blocks need `cohort=` / `norun` tags in the skill sources (`snippets.txt`)
- [x] **#764** (11. Documentation audit) Retire `TestSkillSeeExampleTagsResolve`, superseded by `TestSkillExampleTagPointersResolve`
- [x] **#765** (11. Documentation audit) The coverage matrix's mdBook column is near-vacuous for operator, field-type and MCP-tool rows (the generated guide names every one) and for Request / Response slot names that are common words; give it a stricter rule
- [x] **#766** (11. Documentation audit) The removed-name scan matches qualified `io.` entries whole-word only, so the `pio.` alias spelling slips through

## Baseline burn-down

The allowlists U32 committed under `internal/doccheck/allowlists/`, counted at U32's close (entries, header lines excluded). Every one is now at zero, and `TestDocsBaselinesEmpty` keeps it there:

| Check | Allowlist | Entries at U32's close | Now |
|---|---|---|---|
| `TestDocsLinksResolve` | `links.txt` | 6 | 0 (done) |
| `TestCliHelpMatchesFlagsDoc` | `cli-flags.txt` | 32 | 0 (done) |
| `TestDocsSnippetsRun` | `snippets.txt` | 31 (12 on generated skill pages, #763) | 0 (done) |
| `TestDocsSnippetsCompile` (heavy) | `snippets-go.txt` | 0 | 0 (done) |
| `TestDocsCoverageNoGaps` | `coverage.txt` | 123 (84 `api`, 36 `error-code`, 2 `cli-flag`, 1 `mcp-tool`) | 0 (done; the gaps E1-S1's stricter rule added burned too) |
| `TestProfileServedTextNoHiddenFeature` (heavy) | `served-text.txt` | 0 | 0 (done) |
| `TestNoRemovedNamesInDocs` | `removed-names.txt` | 1 (#762) | 0 (done) |
| `TestNoRemovedNamesInPromptsAndReference` (heavy, added in E1-S2) | `removed-names-prompts.txt` | — | 0 (done) |

How the lists work: `docs/src/contributing/doc-checks.md`.

## Scope

**In scope**
- Burn every U32 baseline allowlist down to empty; add `TestDocsBaselinesEmpty` so none can return
- The U32 hand-offs #757–#766 (checks to widen, examples and skills to fix, a superseded gate to retire)
- Accuracy and currency passes over the mdBook, repo docs, skills, examples, MCP text, error fixups, manifest, `Purpose` / `Interpretation` / glossary
- Getting Started for v1 and the single embedder guide
- Library pages for the embedder surfaces that have none, including the three `PULSE_*_TABLES_DIR` variables (#666)
- MCP protocol-version documentation (#667)
- The perceptual-maps example chain and golden (#504), the bootstrap `--intent` line (#505), the compose-optimize route (#557), the showcase and time-zone notes (#371)
- The manifest `format_version` note: the manifest says `"1.0"` (`internal/descriptor/manifest.go`, pinned by `TestManifestFormatVersion`) and the envelope `"1.1"`; reconcile or document why they differ
- Arrow / Parquet wording: `skills/cohort-width-overflow.md` and `skills/cohort-schema-design.md` call those schemas "authoritative" while only SPSS implements `iocore.SchemaAwareReader`; confirm another path honours the types and reword, or log the code gap with an issue
- `make reference` and `internal/statdist` pointers: PR #458 removed them from CLAUDE.md. Re-check whether a `.claude/reference/` file still needs them; CLAUDE.md is not a companion and takes no edit

**Out of scope**
- The checks themselves ([U32](U32-docs-audit.md)) and the validation ([U32c](U32c-docs-validation.md))
- Casebook conversions (#464, #469): acceptance runs in [U33](U33-v1-release.md)
- New features. Gaps that need code are logged and deferred with an issue unless trivial

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|docs|test(docs-content/E<n>-S<m>): …`; close each epic with `milestone(docs-content/E<n>): vertical slice complete — <epic title>`.

### E1 — The baselines are empty
- S1: burn down each U32 allowlist (link, snippet, help-parity, removed-name, coverage matrix)
- S2: `TestDocsBaselinesEmpty` and the gate-table row

### E2 — Everything says what v1 actually does
- S1: accuracy and currency: mdBook, README / CONTRIBUTING / SECURITY / STABILITY, `.claude/reference/`
- S2: accuracy and currency: skills, examples, MCP text, fixups, manifest, `Purpose` / `Interpretation` / glossary
- S3: terminology and removed-name purge

### E3 — New users and agents succeed on the first try
- S1: Getting Started and the embedder guide
- S2: library pages and the protocol-version page

## Acceptance criteria

- [ ] Every baseline allowlist is empty and `TestDocsBaselinesEmpty` is green
- [ ] Each item in `todo_items` is delivered and ticked, and #165 is ticked
- [ ] The manifest / envelope `format_version` difference is reconciled or documented
- [ ] The Arrow / Parquet wording matches what the code honours
- [ ] Prose follows "informative but concise": no page restates code or another page without a reason
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New: `TestDocsBaselinesEmpty`
- Every U32 check, now with empty baselines

## Update Demand companions

- `docs/src/contributing/testing.md` gate table and `.claude/reference/update-demand/gates*.md` (the new gate)
- `docs/src/SUMMARY.md` (Getting Started, embedder guide and library pages)
- `docs/src/library/options.md` (the three table directory variables)
- `.claude/reference/` files that carry `make reference` / `internal/statdist` guidance

## Human inputs & decisions

- None. Gaps that need a code change are filed with an owner unit, not left in prose.

## Notes

- Split from U32 on 2026-11-24, following the U02 → U02b / U02c precedent.
