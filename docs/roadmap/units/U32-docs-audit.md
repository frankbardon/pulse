---
id: U32
slug: docs-audit
title: "Pulse goes live with the most helpful, current and comprehensive documentation we can produce"
track: API & release
size: L
status: not-started
depends_on: [U01, U02, U02b, U02c, U34, U35, U06, U10, U18, U19, U20, U23, U30, U31, U37, U38, U40, U41, U42, U43, U44, U45, U46, U47, U48, U49, U50, U51, U52, U53, U55, U56, U54, U57, U58, U59, U60, U61, U62, U63, U64, U65, U66, U67, U68, U69, U70, U71, U72, U73, U74, U75, U76, U77, U78, U79]
soft_depends_on: [all feature units]
blocks: [U32b, U32c, U33]
todo_items: [159, 160, 161, 165, 238, 239, 257, 268, 412, 413, 435, 453, 463, 465, 472, 473, 474, 478, 527, 556, 558, 560, 706, 715, 737]
branch: docs-audit
---

# U32 — docs-audit

> **Split (2026-11-24).** U32 (the `docs-audit` branch, "U32a") now owns the audit machinery: the coverage matrix, the automated checks, the served-text and tooling fixes that need code, and the roadmap split itself. The content passes move to [U32b](U32b-docs-content.md) and the validation to [U32c](U32c-docs-validation.md). Precedent: the [U02](U02-public-surface.md) split into [U02b](U02b-extension-contract.md) and [U02c](U02c-cohort-facade.md).

**Outcome:** Pulse goes live with the most helpful, current and comprehensive documentation we can produce.

**Track:** API & release · **Size:** L · **Depends on:** [U01](U01-release-pipeline.md), [U02](U02-public-surface.md), [U02b](U02b-extension-contract.md), [U02c](U02c-cohort-facade.md), [U34](U34-extension-validation.md), [U35](U35-predict-runtime-parity.md), [U06](U06-profiles-mcp-tooling.md), [U10](U10-skill-ontology.md), [U18](U18-response-shaping-execution.md), [U19](U19-resource-limits.md), [U20](U20-observability.md), [U23](U23-guidance-mcp.md), [U30](U30-matrix-extensions-hardening.md), [U31](U31-guidance-guides.md), [U37](U37-shell-completion.md), [U38](U38-skill-sync.md), [U40](U40-compose-sweep.md), [U41](U41-derived-cohort.md), [U42](U42-fit-scoring.md), [U43](U43-post-aggregation-ratios.md), [U44](U44-strict-input.md), [U45](U45-binary-and-decimal.md), [U46](U46-grouped-analysis.md), [U47](U47-survey-tables.md), [U48](U48-experiment-readout.md), [U49](U49-model-workbench.md), [U50](U50-local-time.md), [U51](U51-archive-scale.md), [U52](U52-synth-realism.md), [U53](U53-custom-regressions.md), [U55](U55-previous-state.md), [U56](U56-operation-panic-recovery.md), [U54](U54-multi-grouper.md), [U57](U57-transformed-predictors.md), [U58](U58-expr-math-lags-params.md), [U59](U59-sem-extensions.md), [U60](U60-mmm-estimation.md), [U61](U61-cross-arch-determinism.md), [U62](U62-bayesian-mmm.md), [U63](U63-mmm-followups.md), [U64](U64-reference-validation.md), [U65](U65-regression-depth.md), [U66](U66-overlay-measure-correctness.md), [U67](U67-predict-extension-parity-2.md), [U68](U68-regression-ordinal-multinomial.md), [U69](U69-regression-depth-2.md), [U70](U70-mmm-bayes-depth.md), [U71](U71-mmm-optimize-depth.md), [U72](U72-compose-sweep-2.md), [U73](U73-archive-storage.md), [U74](U74-memory-bounds.md), [U75](U75-zones-labels-2.md), [U76](U76-synth-fidelity-2.md), [U77](U77-ci-platform.md), [U78](U78-test-hygiene.md) · **Soft:** all feature units · **Unblocks:** [U32b](U32b-docs-content.md), [U32c](U32c-docs-validation.md), [U33](U33-v1-release.md)

## Summary

A deliberate final pass over every surface a human or agent reads (mdBook, README and repo docs, GoDoc, CLI help, skills, MCP descriptions, manifest and guidance text, error fixups, examples, CLAUDE.md and references), against six criteria: accurate, current, complete, helpful, consistent, runnable. Backed by automated checks that stay in CI after release, and validated by a fresh-reader review and an agent task evaluation over MCP.

The pass runs in three units. **U32 (this unit, U32a)** builds the checks and fixes what needs code: the coverage matrix (#159), the CI checks and committed baselines (#160, the test half of #165), the hidden-capability, `pulse_predict`, glossary, canonical-tag, skill-tag and `make sites` gaps, and the nil-axis-grouper crash. Each new check reads a committed allowlist (entry plus reason): a new violation fails and a stale entry fails too, so allowlists only shrink. **[U32b](U32b-docs-content.md)** burns the allowlists down to empty (`TestDocsBaselinesEmpty`) and writes the content. **[U32c](U32c-docs-validation.md)** validates with an agent MCP evaluation and a fresh-reader review, then closes the findings log and records sign-off.

## References

**Theme documents (read before starting):**
- [docs-audit 00 — Plan](../v1.0.0-docs-audit/00-plan.md) — whole document

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR). #162–#164, #166, #167, #371, #504, #505, #557, #666 and #667 moved to [U32b](U32b-docs-content.md) and [U32c](U32c-docs-validation.md). #161, #268, #412, #413, #435, #463, #465, #472, #473, #527 and #737 were found delivered by later units and are ticked (evidence: the U32 reconcile, 2026-11-24):

- [ ] **#159** (11. Documentation audit) Documentation inventory and coverage matrix: every public API symbol, CLI leaf and flag, MCP tool / prompt / resource, operator, field type, error code, env var, `Options` field and request/response slot, mapped to where it is documented
- [ ] **#160** (11. Documentation audit) Automated checks in CI: link checker, runnable-snippet test, CLI help ↔ `flags.md` parity, GoDoc `Example*` functions for the public facade, removed-name scan
- [x] **#161** (11. Documentation audit) `TestSkillTokenBudget` flipped from soft to hard-failing, with every skill within budget
- [ ] **#165** (11. Documentation audit) Terminology made consistent with the glossary; pre-1.0 and removed names purged. Split: this unit lands the removed-name scan (test half); [U32b](U32b-docs-content.md) does the content purge and ticks the item
- [ ] **#165** (11. Documentation audit) Terminology made consistent with the glossary; pre-1.0 and removed names purged
- [x] **#465** (Inherited) Skill bodies within about 500 bytes of the topical hard budget; trim before the budget flips to hard-failing. Delivered; the hard budget gate passes
- [x] **#527** (Inherited) The mdBook "unclosed HTML tag" warning on the Analysis Guide. Delivered by the generator escape (`internal/docgen/escape.go`)
- [ ] **#238** (7. Guided analysis › Follow-ups from U21) Hidden-capability prose residue the slot-token filter does not reach (matrices-hidden `pulse_examples_search` / `pulse_manifest` sentences, unscrubbed topical and tool skill bodies, weighted aggregator keys on the Components page of a weighting-hidden instance)
- [ ] **#239** (7. Guided analysis › Follow-ups from U21) The Analysis Guide's search-index size (about 12 MB warning) and whether the PR-time `docs` job becomes a required check
- [ ] **#257** (7. Guided analysis › Follow-ups from U23) `pulse_predict` description keeps the generic "send exactly one alternative root alone" sentence on profiles hiding compose, facet and process_chain (the per-root sentences are scrubbed)
- [x] **#268** (10. Vector & matrix — operators › Follow-ups from U24) Trim the soft-budget overruns in the `op-mat-*` atomic skills (`op-mat-correlation` body 1970 B against 1200) with the other over-budget atomic bodies
- [x] **#412** (10. Vector & matrix — operators › Follow-ups from U25) `skills/statistical-testing.md` sits within 15 B of its 6000 B hard budget, so no multivariate or other test can add a line; split the multivariate tests into their own topical skill or compress it
- [x] **#413** (10. Vector & matrix — operators › Follow-ups from U25) Trim the soft-budget overruns in the `op-test-*` multivariate atomic skills (`op-test-hotelling-t2` 1633 B, `op-test-manova` 1435 B against 1200) with the other over-budget atomic bodies
- [ ] **#556** Glossary terms for shadow price and marginal return, which `compose-optimize` and the `optimizations` result use; they need orphan-report exemptions. Found in U60 (E3-S3) · [U32](U32-docs-audit.md)
- [ ] **#558** No canonical `optimize` tag in `internal/examples/library.go` `CanonicalTags`; the U60 optimize examples carry the `capability:compose_optimize` tag and the `drivers` intent only, so a search for "optimize" finds them only through the skill. Found in U60 (E3-S3) · [U32](U32-docs-audit.md)
- [ ] **#560** `make sites` does not cover `ComposedRequest` sub-fields (`sweep`, `optimize`), so a change there lists no skill / example sites. Found in U60 (E2-S1) · [U32](U32-docs-audit.md)

## Scope

**In scope**
- Coverage matrix of every public thing → where documented (generated where possible) (#159)
- Automated checks: link checker, runnable snippets, CLI help ↔ flags.md, GoDoc examples, removed-name scan, each reading a shrink-only baseline allowlist (#160, #165 test half); hard skill budgets (delivered, #161)
- Hidden-capability prose residue (#238), the `pulse_predict` alternative-root sentence (#257), the Analysis Guide search-index size and the required-check decision (#239)
- Glossary, canonical-tag and skill-tag residue: shadow price and marginal return (#556), the `optimize` tag (#558), skill pointers no example carries (#478), the ontology detector `cells` entry (#474)
- `make sites` gaps: `ComposedRequest` sub-fields, shared series engines, the operator-param index (#453, #560, #706, #715)
- The nil-axis-grouper crash in buffered crosstab (needs a coded refusal; remove the corpus skip)
- The roadmap split into U32 / U32b / U32c

**Out of scope**
- The content passes, Getting Started, the embedder guide, library pages and baseline burn-down ([U32b](U32b-docs-content.md))
- The agent evaluation, fresh-reader review, findings log and sign-off ([U32c](U32c-docs-validation.md))
- Casebook conversions (#464, #469) — they are acceptance runs in [U33](U33-v1-release.md)
- New features. Gaps that need code are logged and deferred with an issue unless trivial

Lands after every other feature unit, including U57–U78 (owner decision 2026-11-02: every open item ships in v1.0.0 except the post-v1 section of `TODO.md`), so the audit reads the final surface.

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test|docs(docs-audit/E<n>-S<m>): …`; close each epic with `milestone(docs-audit/E<n>): vertical slice complete — <epic title>`. The epic and story set lives in the effort plan under `.planning/docs-audit/`.

### E1 — Ground truth and quick fixes
- S1: the roadmap records the U32 split and the true state of every item
- Served-text and tooling fixes that need code: #257, #556, #558, #478, #474, the nil-axis-grouper refusal, `make sites` gaps (#453, #560, #706, #715), #238, #239

### E2 — Gaps are visible and checked automatically
- Coverage matrix (generated columns from manifest, payload schema, `go doc`, CLI tree)
- Link checker + runnable-snippet test in CI
- CLI help ↔ `flags.md` parity; GoDoc `Example*` for the facade; removed-name scan
- Shrink-only baseline allowlists, a gate-table row in `docs/src/contributing/testing.md`, the `docs` job required

## Acceptance criteria

- [ ] The coverage matrix has no blank cell for any public thing, or each blank is a baseline entry with a reason
- [ ] Link checker, runnable-snippet, help-parity, GoDoc-example and removed-name checks are green in CI and remain there; each allowlist fails on a new violation and on a stale entry
- [ ] Every skill is within budget with `TestSkillTokenBudget` hard-failing (delivered; verified 2026-11-24)
- [ ] The roadmap lists U32b and U32c with their TODO items, and every item delivered by a later unit is ticked
- [ ] Every open item above is delivered or re-filed under an owner unit with a reason
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New: link check, `TestDocsSnippetsRun`, `TestCliHelpMatchesFlagsDoc`, `TestNoRemovedNamesInDocs`, GoDoc `Example*` tests, each with a committed baseline allowlist
- `TestSkillTokenBudget` (hard, delivered)
- The agent task evaluation moved to [U32c](U32c-docs-validation.md)

## Update Demand companions

- `.claude/reference/update-demand/gates*.md` and `docs/src/contributing/testing.md` (rows for each new gate)
- `docs/src/SUMMARY.md` (Getting Started / embedder guide placement)


> **Delivered / stale.** The soft-budget and headroom notes below are met or superseded: `TestSkillTokenBudget` is hard-failing and passes, and CLAUDE.md was cut to the rules every session needs (`TestClaudeMdSizeBudget` enforces its ceiling).

- **CLAUDE.md headroom is 54 bytes** (49,946 / 50,000 after U13). U13 already displaced the labeled-date-ranges prose into `.claude/reference/execution-modes.md` and shortened the weighted-slots paragraph. Audit CLAUDE.md for further long form to move into `.claude/reference/` so later contracts have room; never raise `claudeMdSizeCeiling`.


> **Delivered / stale.** The soft-budget and headroom notes below are met or superseded: `TestSkillTokenBudget` is hard-failing and passes, and CLAUDE.md was cut to the rules every session needs (`TestClaudeMdSizeBudget` enforces its ceiling).

- **`skills/tool-import.md` is ~6.6K body chars against the 2,000-char `tool-*` budget** (over before U14; U14's source-zone rows grew it ~400). Bring it within budget before #161 flips `TestSkillTokenBudget` to hard-failing — candidates: move the per-format flag rows to `docs/src/cli/flags.md` and the zone/DST detail to `skills/time-zones.md`, leaving pointers.
- **`op-overlay-yoy.md` sits at its 1,200-char budget** and does not say `GROUP_DATE` `hour` feeds the hourly arm (covered in `skills/time-zones.md`). Re-check after any trim.
- **CLAUDE.md headroom is 65 bytes** (49,935 / 50,000 after U14). Displace long form before the next contract lands.

## Inherited from U16

> **Delivered / stale.** The soft-budget and headroom notes below are met or superseded: `TestSkillTokenBudget` is hard-failing and passes, and CLAUDE.md was cut to the rules every session needs (its ceiling is enforced by `TestClaudeMdSizeBudget`). Only the nil-axis-grouper item below is open (this unit).

- **Buffered crosstab panics on a nil axis grouper** (`processing.PartitionByAxis`, `internal/processing/crosstab.go:62`; fusion parity corpus case `nil_grouper`, which `TestCrosstab_MatricesDoNotPerturb` skips). Pre-existing, found while landing the matrix host refusal. Needs a fix plus a coded refusal (never a nil dereference), and the corpus skip removed.
- **`op-mat-*` skills are over the 1,200-char `op-*` soft budget** (`op-mat-correlation` ~1,530, `op-mat-covariance` ~1,400 body chars after U16 moved prose to `skills/matrix-results.md`). Bring them within budget before #161 flips `TestSkillTokenBudget` to hard-failing.
- **`skills/response-components.md` is at 5,991 / 6,000** (`kind: design` hard budget) after U16 added `Components.Matrices`. Any further Components block must displace prose first.
- **CLAUDE.md headroom is 516 bytes** (49,484 / 50,000 after U16, which displaced the execution-modes pointer list into `.claude/reference/execution-modes.md` "Mode index").


> **Delivered / stale.** The soft-budget and headroom notes below are met or superseded: `TestSkillTokenBudget` is hard-failing and passes, and CLAUDE.md was cut to the rules every session needs (`TestClaudeMdSizeBudget` enforces its ceiling).

- **Two design skills now sit exactly at the 6,000-byte `kind: design` budget** (#161): `skills/weighting.md` (U22 E1-S3 trimmed its SPSS sentence to fit) and `skills/session-bootstrap.md` (U22 E3-S4 shortened step 1's version sentence to fit the `pulse explain` line). The next addition to either must displace text first; U23's `intent` routing left `session-bootstrap.md` at 5,999 bytes. Found in U22 (PR #331), extended in U23.

## Handed on from U25

| Item | Source |
|---|---|
| ~~`statistical-testing.md` budget~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #412 |
| ~~`op-test-*` soft budgets~~ — resolved in U25 | [U25](U25-multivariate-tests-segmentation.md) #413 |

## Inherited from U46

Filed from [U46](U46-grouped-analysis.md); tick in [`TODO.md`](../TODO.md) when delivered.

- [x] **#435** (Grouped analysis › Follow-ups from U46) `skills/op-test-trend.md` is about 30% over the `op-*` soft body budget; bring it within before #161 flips `TestSkillTokenBudget` to hard-failing. Found in U46 (E3-S2).


## Handed on from U30

Filed from [U30](U30-matrix-extensions-hardening.md); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#453** (11. Documentation audit) `sites` change-site tool gaps found by the U30 story agents: no operator-param index beyond enums (`params.reference` forms), metric-registry consumers listed only in part, false `category-norm site missing` warnings, `new-op` missing hand lists that name an operator subset (`vectors.VectorAggregationTypes`), imprecise attribution of shared oracle fixtures and generators. Recipe: `docs/src/internals/finding-change-sites.md`. Found in U30 (E9-S4 annotation, E3–E7 hand-backs).

## Human inputs & decisions

- None for U32a. The maintainer sign-off and the fresh reader are [U32c](U32c-docs-validation.md) inputs

## Notes

- Runs after every feature unit and before the release candidate, so the rc ships with the final docs and downstream validation exercises them too.
- The manifest `format_version` note, the Arrow / Parquet "authoritative" wording and the `make reference` / `internal/statdist` pointers moved to [U32b](U32b-docs-content.md).
- The `session-bootstrap.md` budget and the U08 overlay-skill budget notes are delivered: the hard budget gate passes (#161).

## Handed on from U35

Filed from [U35](U35-predict-runtime-parity.md); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#463** Four atomic op skills sit over the soft 1200-byte budget after U35: `op-attr-reg-fitted`, `op-attr-reg-residual`, `op-attr-reg-leverage`, `op-agg-ratio`; trim in the docs audit. Found in U35 (E7-S3)

## Handed on from U47

Filed from [U47](U47-survey-tables.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#472** Skill budgets after U47: `op-group-set-per-element` body 1,366 B over its 1,200 B soft atomic budget (pre-existing 1,306 B) and `crosstab-guide.md` at 5,999 / 6,000 B; trim in the docs audit. Found in U47 (E4-S5)
- [x] **#473** Split `.claude/reference/weighting/inference-multivariate-and-overlays.md` (32,699 / 32,768 B, at its byte ceiling) into two slices, naming both in the `weighting.md` index, before the next edit that grows it. Found in U47 (E4-S5). Delivered by U59 (E3-S4): `weighting/inference-multivariate.md` + `weighting/overlays.md`
- [ ] **#474** The ontology example detector has no `cells` entry (crosstab `cells` / `capability:crosstab_cells`), so example 19 declares `_meta.capabilities` by hand; add the detector entry. Found in U47 (E4-S5)

## Handed on from U48

Filed from [U48](U48-experiment-readout.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#478** Skill `pulse_examples_search tags=[…]` pointers that no example carries (`synth` in the 14 `op-synth-*` skills, `target-encoding` / `polynomial-regression` / `train-test-split`, `survey`, `ranking` / `moving-average` / `lag-lead`); repoint them at canonical tags an example carries and gate it. Found in U48 (E3-S3)

## Handed on from U31

#504 and #505 moved to [U32b](U32b-docs-content.md).

## Handed on from U79

Filed from [U79](U79-library-gaps.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [ ] **#706** `make sites` covers `types.Request` only, not `ComposedRequest`: a change to a Compose-level slot lists none of `setSlots`, the request-reads walker, Explain, MCP bind / `return_default`, the strict-acceptance families, the example profiles, the limits-parity site lists or `ResultCatalog` (extends #560, which names `sweep` / `optimize`). Found in library-gaps E1-S1 and E6-S1 · [U32](units/U32-docs-audit.md)
- [ ] **#715** `make sites ARGS="op OVERLAY_FORMULA"` does not list `internal/seriesfield` / `internal/seriespartition` (the shared `params.field` / `partition_by` engines) as sites. Found in U66 (E1-S1) · [U32](units/U32-docs-audit.md)

## Handed on from U76

Filed from [U76](U76-synth-fidelity-2.md#handed-on); tick in [`TODO.md`](../TODO.md) when done.

- [x] **#737** The skill `## See` parser (`seeTags` / `renderSeeSection` in `internal/descriptor`) does not prune a list item whose parenthetical annotation contains a backtick span when the skill it points to is pruned by a feature profile; fix it, or reword the annotations so none carries a backtick span. *Done in U76 (E6-S5): a `## See` item whose parenthetical annotation or ` — ` description carries a backtick span is now pruned with its hidden target (only the spans outside both point); `cohort-union`'s natural wording is restored and a heavy rendered-See sweep (`TestProfileRenderedSeeNoHiddenSkill`) proves no profile serves a dangling pointer.*
