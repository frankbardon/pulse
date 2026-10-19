---
id: U32
slug: docs-audit
title: "Pulse goes live with the most helpful, current and comprehensive documentation we can produce"
track: API & release
size: L
status: not-started
depends_on: [U01, U02, U02b, U02c, U34, U35, U06, U10, U18, U19, U20, U23, U30, U31, U37, U38, U40, U41, U42, U43, U44, U45, U46, U47, U48, U49, U50, U51, U52, U53, U55]
soft_depends_on: [all feature units]
blocks: [U33]
todo_items: [159, 160, 161, 162, 163, 164, 165, 166, 167, 238, 239, 257, 268, 371, 412, 413, 435, 453, 463, 469, 472, 473, 474, 478]
branch: docs-audit
---

# U32 — docs-audit

**Outcome:** Pulse goes live with the most helpful, current and comprehensive documentation we can produce.

**Track:** API & release · **Size:** L · **Depends on:** [U01](U01-release-pipeline.md), [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md), [U02c](U02c-cohort-facade.md), [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U18](U18-response-shaping-execution.md), [U19](U19-resource-limits.md), [U20](U20-observability.md), [U23](U23-guidance-mcp.md), [U30](U30-matrix-extensions-hardening.md), [U31](U31-guidance-guides.md), [U37](U37-shell-completion.md), [U38](U38-skill-sync.md), [U40](U40-compose-sweep.md), [U41](U41-derived-cohort.md), [U42](U42-fit-scoring.md), [U43](U43-post-aggregation-ratios.md), [U44](U44-strict-input.md), [U45](U45-binary-and-decimal.md), [U46](U46-grouped-analysis.md), [U47](U47-survey-tables.md), [U48](U48-experiment-readout.md), [U49](U49-model-workbench.md), [U50](U50-local-time.md), [U51](U51-archive-scale.md), [U52](U52-synth-realism.md), [U53](U53-custom-regressions.md), [U55](U55-previous-state.md) · **Soft:** all feature units · **Unblocks:** [U33](U33-v1-release.md)

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
- [ ] **#238** (7. Guided analysis › Follow-ups from U21) Hidden-capability prose residue the slot-token filter does not reach (matrices-hidden `pulse_examples_search` / `pulse_manifest` sentences, unscrubbed topical and tool skill bodies, weighted aggregator keys on the Components page of a weighting-hidden instance)
- [ ] **#239** (7. Guided analysis › Follow-ups from U21) The Analysis Guide's search-index size (about 12 MB warning) and whether the PR-time `docs` job becomes a required check
- [ ] **#257** (7. Guided analysis › Follow-ups from U23) `pulse_predict` description keeps the generic "send exactly one alternative root alone" sentence on profiles hiding compose, facet and process_chain (the per-root sentences are scrubbed)
- [ ] **#268** (10. Vector & matrix — operators › Follow-ups from U24) Trim the soft-budget overruns in the `op-mat-*` atomic skills (`op-mat-correlation` body 1970 B against 1200) with the other over-budget atomic bodies
- [ ] **#371** (Follow-ups from the showcase examples) Docs: `--source-tz` accepts fixed `+HH:MM` offsets while request `time_zone` refuses them (state the asymmetry); projection's benefit is schema-dependent (the retail example measured no gain on 15 fields of 53 bytes); the showcase READMEs are re-verified after the units that simplify them land
- [x] **#412** (10. Vector & matrix — operators › Follow-ups from U25) `skills/statistical-testing.md` sits within 15 B of its 6000 B hard budget, so no multivariate or other test can add a line; split the multivariate tests into their own topical skill or compress it
- [x] **#413** (10. Vector & matrix — operators › Follow-ups from U25) Trim the soft-budget overruns in the `op-test-*` multivariate atomic skills (`op-test-hotelling-t2` 1633 B, `op-test-manova` 1435 B against 1200) with the other over-budget atomic bodies

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

## Inherited from U16

- **Buffered crosstab panics on a nil axis grouper** (`processing.PartitionByAxis`, `internal/processing/crosstab.go:62`; fusion parity corpus case `nil_grouper`, which `TestCrosstab_MatricesDoNotPerturb` skips). Pre-existing, found while landing the matrix host refusal. Needs a fix plus a coded refusal (never a nil dereference), and the corpus skip removed.
- **`op-mat-*` skills are over the 1,200-char `op-*` soft budget** (`op-mat-correlation` ~1,530, `op-mat-covariance` ~1,400 body chars after U16 moved prose to `skills/matrix-results.md`). Bring them within budget before #161 flips `TestSkillTokenBudget` to hard-failing.
- **`skills/response-components.md` is at 5,991 / 6,000** (`kind: design` hard budget) after U16 added `Components.Matrices`. Any further Components block must displace prose first.
- **CLAUDE.md headroom is 516 bytes** (49,484 / 50,000 after U16, which displaced the execution-modes pointer list into `.claude/reference/execution-modes.md` "Mode index").

## Inherited from U22

- **Two design skills now sit exactly at the 6,000-byte `kind: design` budget** (#161): `skills/weighting.md` (U22 E1-S3 trimmed its SPSS sentence to fit) and `skills/session-bootstrap.md` (U22 E3-S4 shortened step 1's version sentence to fit the `pulse explain` line). The next addition to either must displace text first; U23's `intent` routing left `session-bootstrap.md` at 5,999 bytes. Found in U22 (PR #331), extended in U23.

## Handed on from U25

| Item | Source |
|---|---|
| ~~`statistical-testing.md` budget~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #412 |
| ~~`op-test-*` soft budgets~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #413 |

## Inherited from U46

Filed from [U46](U46-grouped-analysis.md); tick in [`TODO.md`](../TODO.md) when delivered.

- [ ] **#435** (Grouped analysis › Follow-ups from U46) `skills/op-test-trend.md` is about 30% over the `op-*` soft body budget; bring it within before #161 flips `TestSkillTokenBudget` to hard-failing. Found in U46 (E3-S2).


## Handed on from U30

Filed from [U30](U30-matrix-extensions-hardening.md); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#453** (11. Documentation audit) `sites` change-site tool gaps found by the U30 story agents: no operator-param index beyond enums (`params.reference` forms), metric-registry consumers listed only in part, false `category-norm site missing` warnings, `new-op` missing hand lists that name an operator subset (`vectors.VectorAggregationTypes`), imprecise attribution of shared oracle fixtures and generators. Recipe: `docs/src/internals/finding-change-sites.md`. Found in U30 (E9-S4 annotation, E3–E7 hand-backs).

## Human inputs & decisions

- Maintainer sign-off; a fresh reader (developer without a statistics background) for the review

## Notes

- The manifest's own `format_version` is hard-coded `"1.0"` (`internal/descriptor/manifest.go`, pinned by `TestManifestFormatVersion`) while the envelope says `"1.1"`. Found during U01; reconcile it or document why they differ.
- Runs after every feature unit and before the release candidate, so the rc ships with the final docs and downstream validation exercises them too.
- `skills/session-bootstrap.md` is ~15.3K body chars against the 6,000-char `kind: design` budget (over before U07; the budget is soft today). It must be brought within budget before #161 flips `TestSkillTokenBudget` to hard-failing — e.g. move the CLI-flag tables to `docs/src/cli/flags.md` and leave pointers.
- **Arrow / Parquet schemas are documented as "authoritative" while only SPSS implements `iocore.SchemaAwareReader`** (found in U03, PR #301). `skills/cohort-schema-design.md` (declared widths never promote) is one such claim. Either confirm that Arrow / Parquet column types are honoured by another path and reword, or log the code gap with an issue.
- **Skill budget overruns from U08** (#161). U08's skill contradiction fixes pushed eight overlay skills past the 1,200-char `op-*` soft budget and grew `op-overlay-chisq-vs-pop` from 1,239 to 1,401 (the `-delta-vs-margin`, `-delta-vs-stage`, `-index-vs-margin`, `-index-vs-stage`, `-t-cell`, `-t-vs-ref`, `-z-vs-ref`, `-prop-z-cell` skills by 3–127 chars); the pairwise, `prop-z-panel` and `op-reg-*` skills were already over. Run `go test ./internal/skills/ -run TestSkillTokenBudget -v` for the full list before the flip.
- **CLAUDE.md does not name `make reference` or `internal/statdist`** (#162). Both landed in U08 and are documented in `.claude/reference/guided-analysis.md`, `architecture.md` and `update-demand.md`, but CLAUDE.md "Build / Env" and "Architecture" were left alone for the 50,000-byte budget (49,869 bytes after U08). Add them only after displacing long form (candidate: the "Shard archive variant" paragraph → `byte-layout.md`).

## Handed on from U45

Filed from [U45](U45-binary-and-decimal.md); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#469** Convert the `pulse-casebook` credit / A-B / privacy / retail examples to the U45 idioms (no `outcome` twin or `2.718… **`, binary metrics as `packed_bool`, direct `TEST_CHISQ` on the flag, `month_index` retention flags) and refresh each README and `REPRODUCE.md`. The in-pulse proof shipped in U45 (#317); the casebook repository is separate. Found in U45 (E4-S3)

## Handed on from U35

Filed from [U35](U35-predict-runtime-parity.md); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#463** Four atomic op skills sit over the soft 1200-byte budget after U35: `op-attr-reg-fitted`, `op-attr-reg-residual`, `op-attr-reg-leverage`, `op-agg-ratio`; trim in the docs audit. Found in U35 (E7-S3)

## Handed on from U47

Filed from [U47](U47-survey-tables.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#472** Skill budgets after U47: `op-group-set-per-element` body 1,366 B over its 1,200 B soft atomic budget (pre-existing 1,306 B) and `crosstab-guide.md` at 5,999 / 6,000 B; trim in the docs audit. Found in U47 (E4-S5)
- [ ] **#473** Split `.claude/reference/weighting/inference-multivariate-and-overlays.md` (32,699 / 32,768 B, at its byte ceiling) into two slices, naming both in the `weighting.md` index, before the next edit that grows it. Found in U47 (E4-S5)
- [ ] **#474** The ontology example detector has no `cells` entry (crosstab `cells` / `capability:crosstab_cells`), so example 19 declares `_meta.capabilities` by hand; add the detector entry. Found in U47 (E4-S5)

## Handed on from U48

Filed from [U48](U48-experiment-readout.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#478** Skill `pulse_examples_search tags=[…]` pointers that no example carries (`synth` in the 14 `op-synth-*` skills, `target-encoding` / `polynomial-regression` / `train-test-split`, `survey`, `ranking` / `moving-average` / `lag-lead`); repoint them at canonical tags an example carries and gate it. Found in U48 (E3-S3)
