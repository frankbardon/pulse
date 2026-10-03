---
id: U09
slug: guidance-backfill-descriptive
title: "Every operator carries guidance, and the guidance gates are binding"
track: Guided analysis
size: L
status: done
depends_on: [U08]
soft_depends_on: [U36]
blocks: [U10, U21, U22]
todo_items: [46, 47, 48, 49]
branch: guidance-backfill-descriptive
---

# U09 — guidance-backfill-descriptive

**Outcome:** Every operator carries guidance, and the guidance gates are binding.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U08](U08-guidance-backfill-inferential.md) (soft: [U36](U36-reference-oracles.md)) · **Unblocks:** [U10](U10-skill-ontology.md), [U21](U21-guidance-generated-docs.md), [U22](U22-recommend-explain.md)

## Summary

Finish the back-fill (aggregators, attributes, filterers, groupers, windows, features, synth distributions). Then flip the guidance gates from report-only to failing, so every future operator must ship with guidance.

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P2, P3, P6
- [guided-analysis 04 — Phasing](../v1.0.0-guided-analysis/04-phasing.md) — G2 — Back-fill

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#46** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Aggregators (`AGG_*`)
- [x] **#47** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Attributes, filterers, groupers, windows and features
- [x] **#48** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Synth distributions
- [x] **#49** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Gates flipped from report-only to failing

## Scope

**In scope**
- Purpose (+ Interpretation where an output needs reading) for the remaining categories
- Flip the U07 gates to failing
- Flip `TestGlossary_OrphanReport` from report-only to failing (every glossary term linked by at least one built-in Purpose, or removed)

**Out of scope**
- Rendering (U21)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-backfill-descriptive/E<n>-S<m>): …`; close each epic with `milestone(guidance-backfill-descriptive/E<n>): vertical slice complete — <epic title>`.

### E1 — Descriptive operators explain themselves
- S1: `AGG_*`
- S2: attributes, filterers, groupers
- S3: windows, features, synth distributions

### E2 — Guidance becomes a contract
- S1: flip the gates to failing (incl. `TestGlossary_OrphanReport`); Update Demand row enforced
- S2: reviewer sign-off on the whole back-fill

## Acceptance criteria

- [x] All guidance gates fail CI on a missing Purpose
- [x] `TestGlossary_OrphanReport` fails on an orphan glossary term
- [x] The full suite passes with the gates failing-mode
- [x] Automated review recorded (human sign-off is U33 #206)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- U07 gates switched to failing
- `TestGlossary_OrphanReport` switched to failing; `TestGuidanceProseLint` (binding since U08) covers every new Purpose and Interpretation automatically

## Update Demand companions

- CLAUDE.md gate list unchanged in names, but note in `guided-analysis.md` that they are now binding

## Human inputs & decisions

- Statistics reviewer sign-off before the flip: not available. The gates flipped on an automated review (U09 section of [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md)); human sign-off of the U08 and U09 guidance is U33 #206.

## Inherited from U07

- **Tag examples with `_meta.intents` before the flip.** U07 made the field optional on examples (`internal/examples/library.go`; values validated against the taxonomy by `TestExamples_IntentsFromTaxonomy`, binding) and no example carries it yet. `TestPurposeQuestionsResolve`'s coverage half logs every intent with fewer than three declaring operators or no `_meta.intents`-tagged example (`.claude/reference/guided-analysis.md`), so flipping it to failing (#49) needs every intent to have at least one tagged example. U23's `pulse_examples_search {intent}` (#96) reads the same tags. The landed spelling is `_meta.intents` (a list); TODO #102 ([U31](U31-guidance-guides.md)) still writes `_meta.intent`, so U31 should use the landed field.

## Inherited from U08

- **Why U36 first.** The gates flip on content a reviewer has signed off, and the inferential numbers that content explains must be externally verified before the guidance is locked in. [U36](U36-reference-oracles.md) pins every inferential output to R; U09 waits for it.
- **The prose lint is already binding.** `TestGuidanceProseLint` (`internal/descriptor/guidance_lint_test.go`) applies its text rules to every built-in Purpose and Interpretation, so the descriptive backfill must pass it from its first commit. Operator-scoped rules key off data tables (`lintScope`, `contentRules`); an exemption needs a justified `guidanceLintAllowlist` entry. Contract: `.claude/reference/guided-analysis.md` (Interpretation).
- **Bands come only from the convention registry** (`internal/descriptor/conventions.go` + `testdata/conventions.json`). A descriptive Interpretation that wants bands needs a sourced convention first; otherwise it stays unbanded with a reason in the fixture's `excluded`.
- **Glossary size gate is 55..150** (`TestGlossary_Size`), loosened in U08 so backfills can add the terms their prose links. The orphan report lists the terms no Purpose links yet; flipping it (above) means linking or removing each.
- **Overlay kinds and `REG_*` types are already binding-covered** (`TestOverlayPurposes_CoverEveryKind`, `TestRegressionPurposes_CoverEveryType`); the flip extends that to the remaining categories.
- **Reviewer.** U08's review was automated ([record](../reviews/U08-statistics-review.md)); the human sign-off is [U33](U33-v1-release.md) #206. U09's own reviewer pass (E2-S2) should append a section to that record, not start a new file.

## As built

**What shipped.**
- Purpose for every descriptive built-in (aggregators, attributes, filterers, groupers, windows, features, synth distributions); Interpretation for every one that needs reading, on the new `value` / `value.*` output paths (`internal/descriptor/interpretation_reading.go`, with the needs-reading and self-reading lists and the rule that classifies them; `TestDescriptiveReadingLists`).
- The coverage gates are binding. Every gap needs an owner-tagged entry in one exemption ledger (`internal/descriptor/guidance_exemptions_test.go`); an entry goes stale when its gap is covered or its owner unit is `status: done`. No entry is U09-owned. Permanent keys: `lookup` (both intent tables) and `simulate` (example gap, since synth specs are not library examples). Remaining owners: U24 (`measure_construct`, eigenvalue / loading / principal-component / reliability / pairwise-deletion), U25 (centroid, distance), U27 (similarity), U28 (`flows`, raking, stochastic-matrix, steady-state).
- All 169 library examples carry `_meta.intents` (binding `TestExamples_EveryExampleHasIntent`); the intent-to-Purpose consistency check stays report-only.
- Skewness, kurtosis and z-score are unbanded: no sourced convention met the fixture bar, so they sit in `conventions.json` `excluded` with reasons.
- Review: U09 section of [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md). Contract: `.claude/reference/guided-analysis.md`.

**Decisions made during the build.**
- The 4096-byte manifest guidance total became two caps: 64 B per entry and a fixed 1280 B for top-level guidance. A total scales with the registry and would re-trip on every later operator.
- `AGG_AVERAGE` is self-reading (Purpose only).
- `AGG_FREQUENCY` is admitted to ProcessChain (the contract tests pin its scalar modal-count shape).
- The target-encode leakage predict warning now fires after a `FEAT_TRAIN_TEST_SPLIT` too, because the encoder ignores the split; the `leakage-safe` claims were dropped from the examples.

**Deviations.** U36 was a hard dependency in the plan; it became soft (U09 documents the runtime defects it found rather than waiting for the oracle work). Reading-semantics tests were added per operator so each Interpretation is pinned to what the engine does, which found and fixed several skill and manifest contradictions along the way.

## Handed on

- **[U36](U36-reference-oracles.md) (runtime).** Make `FEAT_TARGET_ENCODE` split-aware. `AGG_ZSCORE` returns the mean of row z-scores, always 0. `AGG_CI_*` use normal z, not t, so intervals are too narrow on small n. `ATTR_PERCENTILE` uses an unstable sort, so ties get distinct percentiles. Attributes write 0 (50 for TSCORE) for missing inputs instead of null. `GROUP_QUANTILE` / `RANGE` / `ROUNDED` accept categorical at runtime though documented as rejected. `GROUP_QUANTILE` ties can straddle buckets against its linear-interpolation claim. `GROUP_CATEGORY` / `SET_VALUE` Include may count excluded values in `n_null` (unverified). The synth `u4` writer wraps draws above 15 and does not clamp negatives.
- **[U21](U21-guidance-generated-docs.md) (skill sync).** `op-agg-ci-*` calls `t_critical` a scaled critical value; `ATTR_REG_LEVERAGE` says range [0,1] but can exceed 1 outside the fit; `op-win-*` say `order_by` numeric/date while all but `set_*` are admitted; `WIN_LAG` offset doc says at least 1 but predict accepts 0; `WIN_LAG` / `WIN_LEAD` say float64 output but copy the raw cell; `GROUP_RANGE` / `ROUNDED` declare `interval` required but default to 1; `op-feat-target-encode` and `op-feat-train-test-split` and several `op-synth-*` skills are over the 1200-char soft budget; `op-agg-mode` is 1208/1200.
- **[U24](U24-matrix-operators.md).** The glossary `factor` term's owner (latent-factor meaning).
- **Roadmap decisions.** Rename `AGG_FREQUENCY` (for example `AGG_MODE_COUNT` plus an alias) before the v1 freeze. Decide whether `AGG_MODE` is admitted to ProcessChain: it returns a float64 modal value or dictionary index and is still excluded as non-scalar, and the exclusion-reason text (`capabilities_chain.go`, the chain.go header, `process-chain.md`) is false as written, so fix it together with the admission call.
- **[U33](U33-v1-release.md).** Human sign-off of the U09 review section is part of #206 (already extended).
