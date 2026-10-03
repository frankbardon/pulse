---
id: U08
slug: guidance-backfill-inferential
title: "Every test, overlay and regression explains what it is for and how to read it"
track: Guided analysis
size: L
status: done
depends_on: [U07]
soft_depends_on: []
blocks: [U09, U36]
todo_items: [43, 44, 45]
branch: guidance-backfill-inferential
---

# U08 — guidance-backfill-inferential

**Outcome:** Every test, overlay and regression explains what it is for and how to read it.

**Track:** Guided analysis · **Size:** L · **Depends on:** [U07](U07-guidance-metadata.md) · **Unblocks:** [U09](U09-guidance-backfill-descriptive.md), [U36](U36-reference-oracles.md)

## Summary

Write `Purpose` + `Interpretation` for the highest-value operators first: all `TEST_*`, `OVERLAY_*` and `REG_*`. Content work, reviewed by the statistics reviewer. Existing skill `## Gotchas` text is raw material.

**As built.** No human statistics reviewer was available, so the unit made guidance quality machine-checked first (a sourced effect-size convention registry, a binding statistical-prose lint) and verified the shared p-value primitives against R before writing any guidance. Every Purpose and Interpretation then went through two advisory LLM-panel reviews whose findings were all triaged and applied. The review is recorded in [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md); human sign-off is a release-blocking [U33](U33-v1-release.md) item (#206). Per-output numeric oracles and the runtime bugs the review found moved to [U36](U36-reference-oracles.md).

## References

**Theme documents (read before starting):**
- [guided-analysis 01 — Purpose metadata & intent taxonomy](../v1.0.0-guided-analysis/01-purpose-metadata.md) — P2 Purpose rules, P3 Interpretation
- [guided-analysis 04 — Phasing](../v1.0.0-guided-analysis/04-phasing.md) — G2 — Back-fill

**Contract:** `.claude/reference/guided-analysis.md` (Convention registry; Interpretation → Prose lint; Statistical primitives and the R oracle; Review record).

**Review record:** [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md) — method, 52 (E3) + 28 (E4) findings with dispositions and fixing commits, engine fixes, open items for the U33 reviewer.

**TODO items delivered by this unit** (ticked in [`TODO.md`](../TODO.md) in this unit's PR):

- [x] **#43** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Tests (`TEST_*`)
- [x] **#44** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Overlays (`OVERLAY_*`)
- [x] **#45** (3. Guided analysis — metadata core › G2 — Back-fill (statistics reviewer signs off before the gates flip)) Regressions (`REG_*`)

## Scope

**In scope (as built)**
- Purpose (plain line, ≥2 questions, ≥1 NotFor, assumptions, level, glossary) for all 21 `TEST_*` families, every `OVERLAY_*` kind and every `REG_*` type
- Interpretation for every inferential output: every test, every `Inferential` overlay kind plus the four descriptive `OVERLAY_ZSCORE_*` kinds, every regression (statistic, p, effect size, bands with named conventions)
- Effect-size convention registry `internal/descriptor/conventions.go` + independently sourced fixture `testdata/conventions.json` (Cohen 1988 d / η² / r / w, Chinn 2000 OR, Cohen 1992 f² → R²)
- Binding prose lint `TestGuidanceProseLint` (ASA / APA JARS / SAMPL-derived rules, justified allowlist); glossary size gate loosened to 55..150; report-only `TestGlossary_OrphanReport`
- R oracle: `scripts/reference/gen_reference.R`, `make reference`, goldens under `internal/processing/testdata/reference/`, `TestReferenceOracle_*` at relative `1e-10`
- Primitive fixes found by the oracle; tail-accurate normal p-values (`normalTwoSidedP` / `normalUpperTailP`); one Student-t implementation in the new leaf `internal/statdist`
- Engine fixes found by the review: `OVERLAY_CHISQ_VS_POP` renormalisation, `OVERLAY_PROP_Z_CELL` / `_PANEL` missing-margin NaN
- Atomic-skill, capability and doc-comment contradictions with the code, fixed where found

**Out of scope**
- Descriptive operators and flipping the coverage gates (U09)
- Per-output numeric oracles and the runtime bugs the review found (U36)
- Human statistics sign-off (U33, #206)

## Epics & stories (as built)

Commit with `feat|fix|refactor|test|docs(guidance-backfill-inferential/E<n>-S<m>): …`; each epic closed with `milestone(guidance-backfill-inferential/E<n>): vertical slice complete — <epic title>`.

### E1 — Guidance quality is machine-checked
- S1: bind effect-size bands to a sourced convention registry (`219458cc`)
- S2: bind the statistical prose lint; loosen the glossary size gate (`67ef9cb7`)

### E2 — P-values verified against R
- S1: shared stat primitives match R to `1e-10` (`6746f29d`)
- S2: one Student-t implementation in `internal/statdist` (`25344a16`, `0a1cff0e`)
- S3: tail-accurate normal p-values; omit-on-undefined two-sample `cohens_d` (`9f810778`)

### E3 — Tests explain themselves
- S1: a Purpose for every `TEST_*` family (`a9ad7c44`)
- S2: an Interpretation for every test output (`84c254e7`)
- S3: statistics review of test guidance; 52 findings applied (`f4ea6131`, `fc6e8c73`)

### E4 — Overlays and regressions explain themselves
- S1: a Purpose for every overlay kind (`6e2af9cd`)
- S2: Interpretations for every significance overlay (`8d385a67`)
- S3: Purpose + Interpretation for every regression (`25c51611`)
- S4: statistics review of overlay and regression guidance; 28 findings applied; two engine fixes (`9483eced`, `91f34658`, `b2de8f63`, `c58aba41`)

### E5 — Close-out
- S1: contract docs (`.claude/reference/guided-analysis.md`, `update-demand.md`, mdBook)
- S2: roadmap bookkeeping (this doc, U09 / U12 / U32 / U33, new U36, TODO)

## Acceptance criteria

- [x] Report-only gates show zero missing entries for TEST / OVERLAY / REG (`TestInterpretationCoversOutputs`: 0 of 42 inferential built-ins lack an Interpretation; no TEST / OVERLAY / REG entry in `TestSkillsCoverAllPurposes`' missing list)
- [x] Every NotFor alternative resolves to a registered operator (`TestPurposeAlternativesResolve`)
- [x] Every band names its convention, from the registry; no Interpretation says "no difference" for a non-significant result (`TestBuiltinBandsCiteRegisteredConvention`, `TestGuidanceProseLint` rule `ASA-NODIFF`)
- [ ] ~~Statistics reviewer sign-off recorded in the PR~~ — replaced: the automated review is recorded in [`reviews/U08-statistics-review.md`](../reviews/U08-statistics-review.md); human sign-off is [U33](U33-v1-release.md) #206 (release-blocking)
- [x] Unit Definition of Done met (see [units index](README.md#definition-of-done-every-unit))

## Gates & tests

- New binding: `TestConventionRegistryMatchesFixture`, `TestBuiltinBandsCiteRegisteredConvention`, `TestGuidanceProseLint` (+ arms), `TestReferenceOracle_*`, `TestStatdistImportBoundary`, `TestStudentTFormsAgree`, `TestNormalTwoSidedP_MatchesR` + the `*_TinyP` family, `TestOverlayPurposes_CoverEveryKind`, `TestRegressionPurposes_CoverEveryType`, `TestRegressionInterpretations_MatchApplicability`, the overlay / regression runtime probes
- Report-only: `TestGlossary_OrphanReport` (U09 flips it); the U07 coverage tiers, now clean for these categories
- Listed in `.claude/reference/update-demand.md` (Other load-bearing contract gates); none is prefix-matched, so CLAUDE.md is unchanged

## Update Demand companions

- `.claude/reference/guided-analysis.md`, `update-demand.md`, `architecture.md` (`internal/statdist`); `docs/src/library/guided-analysis.md`, `docs/src/internals/regenerating-goldens.md`
- Atomic skills corrected where they contradicted the code (listed in the review record); manifest golden regenerated for capability text changes
- CLAUDE.md untouched (49,869 / 50,000 bytes)

## Human inputs & decisions

- **Statistics reviewer:** none available during U08. Decided: automate the review (deterministic gates + advisory LLM panel, maintainer-triaged) and move human sign-off to U33 (#206), release-blocking.

## Behaviour changes (consumer-visible)

- Primitive p-values / critical values move onto R where they were wrong (far tails, large df, Tukey); e.g. the two-sided t critical value at α = .001, df = 1 was capped at 200 (R 636.62), and every z-based p past |z| ≈ 8.3 was exactly 0
- `OVERLAY_CHISQ_VS_POP`: shares renormalised over the compared categories; subset-only categories dropped with `PULSE_OVERLAY_REF_ZERO`
- `OVERLAY_PROP_Z_CELL` / `_PANEL`: a missing row margin yields NaN + `PULSE_OVERLAY_REF_ZERO` (was a spurious tiny p); `types.PanelNSourceFallsBackToCellValue` returns false for every mode
- Undefined two-sample / paired `cohens_d` is omitted (was 0); lowest band label `negligible` → `very small`; RM partial η² and Kruskal–Wallis ε² unbanded; `TEST_SHAPIRO_WILK` warns for n < 5
- Manifest: `TEST_KS` no longer advertises the ignored `alternative` param; `REG_BAYES_LINEAR` no longer advertises the resample / selection params it refuses

## Inherited from U07

**Status:** all addressed (band list, exemplar review, coverage, overlay probes, two-sample `cohens_d` via `setEffectSize`, CLAUDE.md untouched) except **CODEOWNERS**, which moved to [U33](U33-v1-release.md) with the human reviewer.

- **The theme's band list overstated existing effect sizes.** [guided-analysis 01](../v1.0.0-guided-analysis/01-purpose-metadata.md) lists bands for Cohen's d, η², Cramér's V, r, odds ratio, Cronbach's α and KMO; before U07 only `cohens_d` and `eta_squared` were emitted. U07 added, under `details.effect_size`: `cramers_v` + `phi` (2×2) on `TEST_CHISQ`, `cohens_h` on `TEST_PROP_Z`, one-sample `cohens_d` on `TEST_T`, `omega_squared` on `TEST_ANOVA_F` and `TEST_ANOVA_WELCH`, `partial_eta_squared` on `TEST_ANOVA_RM`, `epsilon_squared` on `TEST_KRUSKAL_WALLIS`, `rank_biserial` on `TEST_MANN_WHITNEY_U` and `TEST_WILCOXON_SR` (post-test twins identical). `r` is `TEST_PEARSON_R`'s statistic; `TEST_FISHER` emits `details.odds_ratio` (outside `effect_size`); Cronbach's α and KMO do not exist yet. The declared set is `EffectSizeKeysByTest()`.
- **Review the exemplars.** The statistical phrasing of the three U07 exemplar Purposes (`AGG_AVERAGE`, `TEST_ANOVA_F`, `TEST_PEARSON_R`), the two exemplar Interpretations, the glossary and the shared p-value rules shipped before the reviewer existed — include them in the reviewer pass.
- **Starting coverage:** 40 of 42 inferential built-ins lack an Interpretation; 160 of 163 built-ins lack a Purpose; no example is tagged with `_meta.intents`. The report-only logs of `TestSkillsCoverAllPurposes` and `TestInterpretationCoversOutputs` print the lists.
- **Overlay Interpretations on `summary.parameters.*`** each need an `overlayInterpretationProbes` fixture (`internal/service/interpretation_runtime_test.go`). Contract: `.claude/reference/guided-analysis.md`.
- **Fix two-sample `TEST_T` / `TEST_WELCH` `cohens_d` on zero pooled SD.** `internal/processing/test_t.go` emits `cohens_d = 0` when the pooled standard deviation is 0 (pre-dates U07). Every U07 effect size omits an undefined value instead (`setEffectSize` drops NaN/±Inf); route the two-sample `cohens_d` (and any post-tier twin sharing it) through the same helper so the key is omitted, and add a degenerate-input case to `TestEffectSizeKeysHoldAtRuntime`'s fixtures. Do this before writing the `TEST_T` Interpretation, whose bands would otherwise read a 0 as "no effect".
- **CODEOWNERS for statistical review** lands with the reviewer (U07 hand-off). Point it at the landed registries — `internal/descriptor/purposes.go`, `interpretations.go`, `glossary.go`, `intents.go` and the effect-size code in `internal/processing/test_*.go` — not the `descriptor/purpose_*.go` path in [guided-analysis 04](../v1.0.0-guided-analysis/04-phasing.md), which predates U07. `.github/CODEOWNERS` today carries only the default `*` owner; add the reviewer's entries there.
- **CLAUDE.md has 131 bytes of headroom** (49,869 / 50,000 after U07). Displace long form into `.claude/reference/` before adding any CLAUDE.md line (candidates: the "Shard archive variant" and field-types paragraphs under Byte-layout invariants → `byte-layout.md`). `TestClaudeMdSizeBudget`; tracked release-wide by TODO #178.

## Notes

- Parallelised by category. The automated review method is reusable: U09's reviewer pass should append to the same record.
