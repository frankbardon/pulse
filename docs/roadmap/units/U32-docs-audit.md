---
id: U32
slug: docs-audit
title: "Pulse goes live with the most helpful, current and comprehensive documentation we can produce"
track: API & release
size: L
status: not-started
depends_on: [U01, U02, U02b, U02c, U34, U35, U06, U10, U18, U19, U20, U23, U30, U31]
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

## Inherited from U13

- **CLAUDE.md headroom is 54 bytes** (49,946 / 50,000 after U13). U13 already displaced the labeled-date-ranges prose into `.claude/reference/execution-modes.md` and shortened the weighted-slots paragraph. Audit CLAUDE.md for further long form to move into `.claude/reference/` so later contracts have room; never raise `claudeMdSizeCeiling`.

## Inherited from U14

- **`skills/tool-import.md` is ~6.6K body chars against the 2,000-char `tool-*` budget** (over before U14; U14's source-zone rows grew it ~400). Bring it within budget before #161 flips `TestSkillTokenBudget` to hard-failing — candidates: move the per-format flag rows to `docs/src/cli/flags.md` and the zone/DST detail to `skills/time-zones.md`, leaving pointers.
- **`op-overlay-yoy.md` sits at its 1,200-char budget** and does not say `GROUP_DATE` `hour` feeds the hourly arm (covered in `skills/time-zones.md`). Re-check after any trim.
- **CLAUDE.md headroom is 65 bytes** (49,935 / 50,000 after U14). Displace long form before the next contract lands.

## Human inputs & decisions

- Maintainer sign-off; a fresh reader (developer without a statistics background) for the review

## Notes

- The manifest's own `format_version` is hard-coded `"1.0"` (`internal/descriptor/manifest.go`, pinned by `TestManifestFormatVersion`) while the envelope says `"1.1"`. Found during U01; reconcile it or document why they differ.
- Runs after every feature unit and before the release candidate, so the rc ships with the final docs and downstream validation exercises them too.
- `skills/session-bootstrap.md` is ~15.3K body chars against the 6,000-char `kind: design` budget (over before U07; the budget is soft today). It must be brought within budget before #161 flips `TestSkillTokenBudget` to hard-failing — e.g. move the CLI-flag tables to `docs/src/cli/flags.md` and leave pointers.
- **Arrow / Parquet schemas are documented as "authoritative" while only SPSS implements `iocore.SchemaAwareReader`** (found in U03, PR #301). `skills/cohort-schema-design.md` (declared widths never promote) is one such claim. Either confirm that Arrow / Parquet column types are honoured by another path and reword, or log the code gap with an issue.
- **Skill budget overruns from U08** (#161). U08's skill contradiction fixes pushed eight overlay skills past the 1,200-char `op-*` soft budget and grew `op-overlay-chisq-vs-pop` from 1,239 to 1,401 (the `-delta-vs-margin`, `-delta-vs-stage`, `-index-vs-margin`, `-index-vs-stage`, `-t-cell`, `-t-vs-ref`, `-z-vs-ref`, `-prop-z-cell` skills by 3–127 chars); the pairwise, `prop-z-panel` and `op-reg-*` skills were already over. Run `go test ./internal/skills/ -run TestSkillTokenBudget -v` for the full list before the flip.
- **CLAUDE.md does not name `make reference` or `internal/statdist`** (#162). Both landed in U08 and are documented in `.claude/reference/guided-analysis.md`, `architecture.md` and `update-demand.md`, but CLAUDE.md "Build / Env" and "Architecture" were left alone for the 50,000-byte budget (49,869 bytes after U08). Add them only after displacing long form (candidate: the "Shard archive variant" paragraph → `byte-layout.md`).
