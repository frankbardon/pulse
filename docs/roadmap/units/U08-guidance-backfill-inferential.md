---
id: U08
slug: guidance-backfill-inferential
title: "Every test, overlay and regression explains what it is for and how to read it"
track: Guided analysis
size: L
status: not-started
depends_on: [U07]
soft_depends_on: []
blocks: [U09]
todo_items: [43, 44, 45]
branch: guidance-backfill-inferential
---

# U08 — guidance-backfill-inferential

**Outcome:** Every test, overlay and regression explains what it is for and how to read it.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U07](U07-guidance-metadata.md) · **Unblocks:** [U09](U09-guidance-backfill-descriptive.md)

## Summary

Write `Purpose` + `Interpretation` for the highest-value operators first: all `TEST_*`, `OVERLAY_*` and `REG_*`. Content work, reviewed by the statistics reviewer. Existing skill `## Gotchas` text is raw material.

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P2 Purpose rules, P3 Interpretation
- [guided-analysis 04 — Phasing](../v1.0.0-guided-analysis/04-phasing.md) — G2 — Back-fill

**TODO items delivered by this unit** (tick them in [`TODO.md`](../TODO.md) in this unit's PR):

- [ ] **#43** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Tests (`TEST_*`)
- [ ] **#44** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Overlays (`OVERLAY_*`)
- [ ] **#45** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Regressions (`REG_*`)

## Scope

**In scope**
- Purpose (plain line, ≥2 questions, ≥1 NotFor, assumptions, level, glossary) for every test, overlay, regression
- Interpretation for every inferential output (statistic, p, effect size, bands with named conventions)

**Out of scope**
- Descriptive operators (U09)
- Flipping gates (U09)

## Epics & stories

Each epic is a vertical slice. Commit with `feat|fix|perf|test(guidance-backfill-inferential/E<n>-S<m>): …`; close each epic with `milestone(guidance-backfill-inferential/E<n>): vertical slice complete — <epic title>`.

### E1 — Tests explain themselves
- S1: `TEST_*` Purpose + Interpretation
- S2: reviewer pass on tests

### E2 — Overlays and regressions explain themselves
- S1: `OVERLAY_*` Purpose + Interpretation (by family)
- S2: `REG_*` Purpose + Interpretation
- S3: reviewer pass

## Acceptance criteria

- [ ] Report-only gates show zero missing entries for TEST / OVERLAY / REG
- [ ] Every NotFor alternative resolves to a registered operator
- [ ] Every band names its convention; no Interpretation says "no difference" for a non-significant result
- [ ] Statistics reviewer sign-off recorded in the PR
- [ ] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- The U07 gates, still report-only, now clean for these categories

## Update Demand companions

- None beyond the metadata files (rendering into skills happens in U21)

## Human inputs & decisions

- **Statistics reviewer** (decided: maintainer sources one) reviews and signs off

## Inherited from U07

- **The theme's band list overstated existing effect sizes.** [guided-analysis 01](../v1.0.0-guided-analysis/01-purpose-metadata.md) lists bands for Cohen's d, η², Cramér's V, r, odds ratio, Cronbach's α and KMO; before U07 only `cohens_d` and `eta_squared` were emitted. U07 added, under `details.effect_size`: `cramers_v` + `phi` (2×2) on `TEST_CHISQ`, `cohens_h` on `TEST_PROP_Z`, one-sample `cohens_d` on `TEST_T`, `omega_squared` on `TEST_ANOVA_F` and `TEST_ANOVA_WELCH`, `partial_eta_squared` on `TEST_ANOVA_RM`, `epsilon_squared` on `TEST_KRUSKAL_WALLIS`, `rank_biserial` on `TEST_MANN_WHITNEY_U` and `TEST_WILCOXON_SR` (post-test twins identical). `r` is `TEST_PEARSON_R`'s statistic; `TEST_FISHER` emits `details.odds_ratio` (outside `effect_size`); Cronbach's α and KMO do not exist yet. The declared set is `EffectSizeKeysByTest()`.
- **Review the exemplars.** The statistical phrasing of the three U07 exemplar Purposes (`AGG_AVERAGE`, `TEST_ANOVA_F`, `TEST_PEARSON_R`), the two exemplar Interpretations, the glossary and the shared p-value rules shipped before the reviewer existed — include them in the reviewer pass.
- **Starting coverage:** 40 of 42 inferential built-ins lack an Interpretation; 160 of 163 built-ins lack a Purpose; no example is tagged with `_meta.intents`. The report-only logs of `TestSkillsCoverAllPurposes` and `TestInterpretationCoversOutputs` print the lists.
- **Overlay Interpretations on `summary.parameters.*`** each need an `overlayInterpretationProbes` fixture (`internal/service/interpretation_runtime_test.go`). Contract: `.claude/reference/guided-analysis.md`.

## Notes

- Parallelises well by category. The `pulse-docs-skills` agent can draft, and the human reviews.
