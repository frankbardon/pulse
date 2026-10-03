# U08 statistics review

The committed record of the statistical review of Pulse's inferential guidance
metadata (roadmap [U08](../units/U08-guidance-backfill-inferential.md)). It is
the input for the human statistics reviewer at [U33](../units/U33-v1-release.md):
what was checked, how, what was found and fixed, and what is still open.

- **E3 scope (this section):** every `TEST_*` Purpose and Interpretation, the
  `AGG_AVERAGE` exemplar Purpose, the shared p-value rule set, the glossary, the
  effect-size convention registry, and the effect-size formula table in
  `.claude/reference/guided-analysis.md` checked against the code.
- **E4 scope (appended below):** overlays (`OVERLAY_*`) and regressions (`REG_*`).

## Method

The review runs in layers. The deterministic layers run on every `go test`; the
LLM panel ran once per scope and is recorded here so it can be reproduced.

### Deterministic layers (binding CI gates)

1. **Convention registry and fixture.** Every built-in effect-size band comes
   from `internal/descriptor/conventions.go`. That registry is held equal to the
   independently transcribed, source-cited `internal/descriptor/testdata/conventions.json`
   (`TestConventionRegistryMatchesFixture`). Each banded Interpretation is bound
   to exactly one registered convention that covers its statistic
   (`TestBuiltinBandsCiteRegisteredConvention`). Outputs left unbanded on purpose
   are listed under the fixture's `excluded` with a reason.
2. **Prose lint.** `TestGuidanceProseLint` (`internal/descriptor/guidance_lint_test.go`)
   scans every Purpose, Interpretation and shared rule set. Text rules ASA-PROOF,
   ASA-PNULL, ASA-NODIFF and ASA-IMPORTANT come from Wasserstein & Lazar (2016).
   Registry rules are PV-SHARED, ES-CONV, CORR-CAUSAL, ASSUME-INDEP, MULTI-COMP
   and NORM-POWER. Exemptions go through a justified, staleness-checked allowlist.
3. **R oracle.** `make reference` regenerates golden values from R 4.6.1 (it is a
   generator only; CI never runs R). The shared statistical primitives and test
   p-values are pinned to those goldens with relative tolerances
   (`internal/processing/reference_oracle_test.go`).

### LLM panel (advisory, maintainer-triaged)

- **Model:** Claude Opus 5.5 (the session model), run read-only on 2026-10-03.
- **Passes:** two independent passes over the same scope. Pass 1 is a
  statistician adversary (correctness, conventions, assumptions, NotFor routing).
  Pass 2 is a novice-reader adversary (misleading phrasing, unlinked jargon).
- **Finding rules:** every finding had to quote the exact span (checked as a
  substring of the cited file) and cite a lint rule ID or a named source.
  Findings without both were discarded.
- **Triage:** the maintainer accepted all 52 findings. Seven pairs overlap
  (S-02/N-02, S-03/N-03, S-06/N-01, S-05/N-20, S-09/N-08, S-17/N-16, S-24/N-05),
  which leaves 45 distinct fixes. All of them landed in commit `f4ea6131`.

The prompts are reproduced verbatim below.

#### Pass 1 prompt (statistician)

```text
You are an adversarial statistician reviewing the guidance metadata of a statistics engine (Pulse). Your job is to find statements that are statistically WRONG or incomplete: incorrect test descriptions, wrong or missing assumptions, wrong effect-size conventions or formulas, wrong reading of a statistic or p-value, NotFor routing that sends a user to an inappropriate alternative test, claims that disagree with what the code actually computes. Assume the author shares common misconceptions (e.g. "Mann-Whitney tests medians", "Welch assumes equal variances", "p is the probability the null is true", Cohen bands applied to the wrong statistic) and hunt for them.
```

#### Pass 2 prompt (novice reader)

```text
You are an adversarial novice-reader reviewer of the guidance metadata of a statistics engine (Pulse). Read every string as an analyst with no statistics training would, and find text that would MISLEAD them: phrasing that invites "significant = important", "not significant = no effect", causal readings of correlations, overconfident language, jargon used without being linked in the Purpose's Glossary list or defined, ambiguous referents, and plain-language summaries that oversimplify into something false. You are not judging statistical correctness in depth — a separate statistician pass does that — but anything a novice would act on wrongly is in scope.
```

#### Scope prompt (appended to both passes)

```text
SCOPE (read these files; the code is the ground truth for what each operator computes):
- internal/descriptor/purposes_stattests.go — every TEST_* Purpose
- internal/descriptor/purposes.go — AGG_AVERAGE exemplar Purpose
- internal/descriptor/interpretations_stattests.go + internal/descriptor/interpretations.go — every TEST_* Interpretation and the shared p-value rule set
- internal/descriptor/glossary.go — every glossary term
- internal/descriptor/conventions.go + internal/descriptor/testdata/conventions.json — effect-size convention registry
- .claude/reference/guided-analysis.md — effect-size formula table (compare against the code that computes each effect size in internal/processing/test_*.go and internal/processing/test_effect_size*.go)
- internal/processing/test_*.go — implementation, read-only, to verify claims (Welch vs pooled, tails, tie handling, df, which statistic is emitted)
Lint rule IDs already enforced by TestGuidanceProseLint (internal/descriptor/guidance_lint_test.go) — read that file for the IDs (ASA-*, MULTI-COMP, NORM-POWER, ASSUME-INDEP, ES-CONV, CORR-CAUSAL, ...). Named sources you may cite: ASA Statement on p-values (Wasserstein & Lazar 2016), APA JARS-Quant (Appelbaum et al. 2018), SAMPL (Lang & Altman), Cohen (1988/1992), a standard textbook by name, or the Pulse implementation file:line.

FINDING RULES (non-negotiable):
- Every finding MUST quote the exact offending span verbatim (it will be substring-checked against the file; a non-matching quote is discarded) and give file:line.
- Every finding MUST cite either a lint rule ID or a named source (above). Findings without both are discarded.
- Judge one criterion at a time; do not pad with style nits. No finding for wording you merely prefer.
- Severity: high (statistically wrong / would mislead a decision), medium (imprecise, missing caveat that matters), low (clarity).
- Propose a concrete replacement text for each finding.
- Do NOT edit any repository file. Read-only.

OUTPUT: write a markdown file at the path given below containing one table:
| id | severity | operator/field | file:line | quoted span | rule/source | problem | proposed fix |
ids are <PASS>-NN. Then return ONLY: the file path and the finding count by severity.
```

## E3 findings — tests, glossary, shared p-value rules, conventions

Spans are quoted as they read at `22b062ce`, before the fix. The
operator/field column locates each one in the files named by the scope prompt.

| id | pass | severity | operator / field | quoted span | rule / source | verdict | fix commit |
|---|---|---|---|---|---|---|---|
| S-01 | 1 statistician | high | TEST_ANOVA_RM / details.effect_size.partial_eta_squared | partial eta squared reads larger than a between-groups eta squared for the same shift | ES-CONV; Cohen (1992) Table 1 row 7 (f for one-way between-groups ANOVA) | accepted | `f4ea6131` |
| S-02 | 1 statistician | high | glossary / statistical-significance | Significant means unlikely to be pure noise, not important. | ASA-PNULL; Wasserstein & Lazar (2016) principle 2 | accepted (fixed once with N-02) | `f4ea6131` |
| S-03 | 1 statistician | medium | glossary / p-value (WhyCare) | Small values (below your alpha) suggest the difference is not just noise. | ASA-PNULL; Wasserstein & Lazar (2016) principle 2 | accepted (fixed once with N-03) | `f4ea6131` |
| S-04 | 1 statistician | medium | glossary / p-value (Short) | The probability of seeing a difference at least this large if there were truly no difference. | Wasserstein & Lazar (2016) principle 1 | accepted | `f4ea6131` |
| S-05 | 1 statistician | medium | shared p-value rule set / means | The chance of seeing a result at least this extreme if there were truly no difference or link. | Wasserstein & Lazar (2016) principle 1 | accepted (fixed once with N-20) | `f4ea6131` |
| S-06 | 1 statistician | medium | glossary / effect-size (WhyCare) | where the p-value only answers | Wasserstein & Lazar (2016) principles 2 and 5 | accepted (fixed once with N-01) | `f4ea6131` |
| S-07 | 1 statistician | medium | glossary / alpha (WhyCare) | It is the false-alarm rate you accept: how often you would call noise a real effect. | Wasserstein & Lazar (2016) principle 2; Cohen (1988) ch. 1 (alpha is P(reject given H0 true)) | accepted | `f4ea6131` |
| S-08 | 1 statistician | medium | glossary / cramers-v (WhyCare) | it stays comparable across tables of different sizes | ES-CONV; Cohen (1992) Example 6 p. 158 (as transcribed in testdata/conventions.json) | accepted | `f4ea6131` |
| S-09 | 1 statistician | medium | glossary / correlation (Short) | 0 (unrelated) | CORR-CAUSAL-adjacent; Pulse interpretations_stattests.go:543 ("An r near zero rules out only a straight-line link") | accepted (fixed once with N-08) | `f4ea6131` |
| S-10 | 1 statistician | medium | glossary / z-score (Short) | How many standard deviations a value sits above or below the mean. | Pulse internal/processing/test_propz.go:151 (z := (pa - pb) / sePool) | accepted | `f4ea6131` |
| S-11 | 1 statistician | medium | TEST_PAIRED_T / statistic, details.mean_diff, cohens_d (pairedSign) | Field tends to be larger than Field2 | Pulse internal/processing/test_paired.go:101 (tstat := b.mean / se) | accepted | `f4ea6131` |
| S-12 | 1 statistician | medium | TEST_PAIRED_T / details.effect_size.cohens_d | d_z grows as Field and Field2 correlate, so it reads larger than a between-group d for the same shift | Cohen (1988) §2.3.5 (d_z = d / sqrt(2(1 − r))) | accepted | `f4ea6131` |
| S-13 | 1 statistician | medium | TEST_KRUSKAL_WALLIS / details.effect_size.epsilon_squared | details.effect_size.epsilon_squared | ES-CONV; Cohen (1992) Table 1 row 7 | accepted | `f4ea6131` |
| S-14 | 1 statistician | medium | convention cohen1988_or / Citation | Cohen (1988), via Chen, Cohen & Chen (2010) | ES-CONV; Cohen (1988) | accepted | `f4ea6131` |
| S-15 | 1 statistician | medium | TEST_TUKEY_HSD / NotFor | {When: "the measure is heavily skewed or only ordered", Use: "TEST_KRUSKAL_WALLIS"} | MULTI-COMP; Pulse purposes_stattests.go:401 (KW is "an overall test") | accepted | `f4ea6131` |
| S-16 | 1 statistician | medium | TEST_KRUSKAL_WALLIS / NotFor | {When: "the same subjects are measured in every group", Use: "TEST_ANOVA_RM"} | Pulse purposes_stattests.go:249 (TEST_ANOVA_RM assumes normality within each condition) | accepted | `f4ea6131` |
| S-17 | 1 statistician | medium | TEST_ANOVA_WELCH / NotFor | or you want pairwise follow-ups adjusted for multiple comparisons | MULTI-COMP | accepted (fixed once with N-16) | `f4ea6131` |
| S-18 | 1 statistician | medium | TEST_T / NotFor | {When: "the measure is heavily skewed, has extreme values or is only ordered", Use: "TEST_MANN_WHITNEY_U"} | Pulse internal/processing/test_t.go:104 (one-sample when SplitBy is empty) | accepted | `f4ea6131` |
| S-19 | 1 statistician | medium | TEST_SHAPIRO_WILK / Assumptions | Needs at least 3 values per group; above 5000 the p-value is advisory. | Pulse internal/processing/test_shapiro.go:173-174 ("Royston's transformation for Shapiro-Francia (n ≥ 5)") | accepted | `f4ea6131` |
| S-20 | 1 statistician | low | TEST_ANOVA_WELCH / details.effect_size.omega_squared | so it stays comparable with TEST_ANOVA_F's omega squared | Pulse internal/processing/test_effect_size.go:108-118 | accepted | `f4ea6131` |
| S-21 | 1 statistician | low | TEST_CHISQ / Assumptions | Every expected count should be about 5 or more (Cochran's rule of thumb) | Agresti, Categorical Data Analysis (Cochran 1954 rule) | accepted | `f4ea6131` |
| S-22 | 1 statistician | low | TEST_TREND / Assumptions | unreliable below about 8 points | Gilbert, Statistical Methods for Environmental Pollution Monitoring (1987), §16.4 | accepted | `f4ea6131` |
| S-23 | 1 statistician | low | glossary / confidence-interval (Short) | A range around an estimate that would capture the true value in a stated share of repeated samples. | APA JARS-Quant (Appelbaum et al. 2018) interval reporting; Cohen (1988) | accepted | `f4ea6131` |
| S-24 | 1 statistician | low | glossary / f-statistic (WhyCare) | larger values point to a real difference somewhere among the groups | ASA-PROOF; Wasserstein & Lazar (2016) principle 2 | accepted (fixed once with N-05) | `f4ea6131` |
| S-25 | 1 statistician | low | glossary / variance (Short) | The average squared distance of values from their mean | Pulse internal/processing/test_t.go:164 (sampleVariance, n − 1 divisor) | accepted | `f4ea6131` |
| N-01 | 2 novice | high | glossary effect-size.WhyCare | It answers \"does it matter?\" where the p-value only answers \"is it noise?\". Report both. | ASA Statement (Wasserstein & Lazar 2016) principles 2 and 5; Cohen (1988) on benchmarks being context-free | accepted (fixed once with S-06) | `f4ea6131` |
| N-02 | 2 novice | medium | glossary statistical-significance.WhyCare | Significant means unlikely to be pure noise, not important. | ASA-PNULL; ASA Statement principle 2 | accepted (fixed once with S-02) | `f4ea6131` |
| N-03 | 2 novice | medium | glossary p-value.WhyCare | Small values (below your alpha) suggest the difference is not just noise. | ASA-PNULL; ASA Statement principle 2 | accepted (fixed once with S-03) | `f4ea6131` |
| N-04 | 2 novice | medium | glossary t-statistic.WhyCare | Bigger in absolute size means the difference is large relative to the noise. | ASA-IMPORTANT; ASA Statement principle 5 | accepted | `f4ea6131` |
| N-05 | 2 novice | medium | glossary f-statistic.WhyCare | larger values point to a real difference somewhere among the groups. | ASA-PROOF; ASA Statement principle 2 | accepted (fixed once with S-24) | `f4ea6131` |
| N-06 | 2 novice | low | glossary chi-square.WhyCare | It tells you whether two categorical fields are related, but not how strongly | ASA-PROOF; ASA Statement principle 2 | accepted | `f4ea6131` |
| N-07 | 2 novice | medium | glossary cross-tabulation.WhyCare | a chi-square test then says whether the pattern is beyond chance. | ASA-PNULL; ASA Statement principle 2 | accepted | `f4ea6131` |
| N-08 | 2 novice | medium | glossary correlation.Short | from -1 (opposite) through 0 (unrelated) to 1 (in lockstep). | Standard textbook (Moore, McCabe & Craig, Introduction to the Practice of Statistics: r measures only linear association); contradicts Pulse interpretations_stattests.go:543 | accepted (fixed once with S-09) | `f4ea6131` |
| N-09 | 2 novice | low | glossary cohens-d.WhyCare | a gap of half a standard deviation means the same on any scale. | Cohen (1988) (benchmarks are context-dependent; the same d differs in practical importance across domains) | accepted | `f4ea6131` |
| N-10 | 2 novice | medium | convention labels (all banded effect sizes) | var cohenLabels = []string{"negligible", "small", "medium", "large"} | Cohen (1988) (benchmarks are a last resort and context-bound; Cohen names no band below "small"); R effectsize uses "very small" (conventions.json comment) | accepted | `f4ea6131` |
| N-11 | 2 novice | medium | TEST_ANOVA_F / TEST_ANOVA_WELCH details.effect_size.omega_squared | Pulse reports 0 instead, which reads as a negligible effect. | ASA-NODIFF; ASA Statement principle 6 | accepted | `f4ea6131` |
| N-12 | 2 novice | medium | TEST_ANOVA_F statistic | larger values mean the groups differ by more than within-group noise would explain. | ASA-PROOF; ASA Statement principle 2 | accepted | `f4ea6131` |
| N-13 | 2 novice | high | TEST_PEARSON_R details.ci_low | With fewer than four pairs, or a perfect r, the interval collapses to r itself. | Pulse implementation internal/processing/test_pearson.go:223-230 (n=3 allowed, ci_low = ci_high = r); APA JARS-Quant (report interval estimates honestly) | accepted | `f4ea6131` |
| N-14 | 2 novice | medium | TEST_SHAPIRO_WILK questions[1] | Are scores in each group close enough to normal for an ANOVA? | NORM-POWER; ASA Statement principle 6 | accepted | `f4ea6131` |
| N-15 | 2 novice | medium | TEST_BROWN_FORSYTHE questions[1] | Do the groups vary enough in spread that a standard ANOVA is a poor fit? | NORM-POWER; contradicts Pulse purposes_stattests.go:309 | accepted | `f4ea6131` |
| N-16 | 2 novice | medium | TEST_ANOVA_WELCH not_for[2].when | there are only two groups, or you want pairwise follow-ups adjusted for multiple comparisons | MULTI-COMP | accepted (fixed once with S-17) | `f4ea6131` |
| N-17 | 2 novice | medium | TEST_PAIRED_T questions[0] | Did each customer's spend change after the loyalty programme started? | APA JARS-Quant (causal claims require design support); ASA Statement principle 3 | accepted | `f4ea6131` |
| N-18 | 2 novice | low | TEST_CHISQ questions[0] | Does preferred channel depend on age band? | CORR-CAUSAL | accepted | `f4ea6131` |
| N-19 | 2 novice | low | TEST_SPEARMAN_R plain | how consistently two numeric fields move in the same direction | Pulse implementation interpretations_stattests.go:564 (correlationSign: "-" = one falls as the other rises) | accepted | `f4ea6131` |
| N-20 | 2 novice | low | shared p-value rule set Means | The chance of seeing a result at least this extreme if there were truly no difference or link. | ASA Statement principle 1 (the p-value is computed under a model, including its assumptions) | accepted (fixed once with S-05) | `f4ea6131` |
| N-21 | 2 novice | low | TEST_PROP_Z assumptions[1] | Each group needs enough successes and failures (at least 5 to 10 of each) for the normal approximation. | Pulse implementation interpretations_stattests.go:508 ("roughly 10 of each") | accepted | `f4ea6131` |
| N-22 | 2 novice | low | TEST_FISHER_EXACT assumptions[2] | Row and column totals are treated as fixed, which makes the test somewhat conservative. | Pulse implementation glossary.go (no term or Form for "conservative"); glossary_test.go:17 jargon-link intent | accepted | `f4ea6131` |
| N-23 | 2 novice | low | TEST_T assumptions[1] | The two-group version uses Welch's correction, so the groups need not have equal variances. | Pulse implementation glossary.go:164 ("equal variances" is a Form of homogeneity-of-variance) | accepted | `f4ea6131` |
| N-24 | 2 novice | low | TEST_WILCOXON_SR assumptions[3] | the p-value is a two-sided large-sample approximation and needs at least 6 pairs that differ. | Pulse implementation glossary.go:136 ("two-sided" is a Form of two-tailed) | accepted | `f4ea6131` |
| N-25 | 2 novice | low | glossary sphericity.WhyCare | Pulse applies no correction for it, so read borderline p-values with care. | Standard textbook (Maxwell & Delaney, Designing Experiments and Analyzing Data: uncorrected RM F can be substantially liberal) | accepted | `f4ea6131` |
| N-26 | 2 novice | low | TEST_TREND statistic | Values far from 0 point to a steady rise or fall. | Standard textbook (Hollander, Wolfe & Chicken, Nonparametric Statistical Methods: Mann-Kendall detects a monotonic tendency); Pulse purposes_stattests.go:619 | accepted | `f4ea6131` |
| N-27 | 2 novice | low | glossary rank.WhyCare | makes a method immune to extreme values | Standard textbook (Hollander, Wolfe & Chicken, Nonparametric Statistical Methods) | accepted | `f4ea6131` |

### Notes on how fixes were applied

- Where two passes proposed different text for the same span, the fix merges
  them: glossary `p-value` and `statistical-significance`, `effect-size`
  WhyCare, the shared p-value Means, and the TEST_ANOVA_WELCH pairwise NotFor.
- S-10's addition to `z-score` is shortened to fit the 200-character glossary
  `Short` limit.
- S-14 is written in ASCII (`d = ln(OR)*sqrt(3)/pi`). The new Chinn (2000)
  source entry in the fixture is marked as a formula citation that was not
  fetched. The thresholds are still verified against the R effectsize source.
- S-19 also changes runtime behaviour: `TEST_SHAPIRO_WILK` now adds a per-group
  advisory warning when a group has fewer than 5 values
  (`TestShapiroFrancia_SmallNAdvisory`).
- N-10 renames the band below "small" from "negligible" to "very small" in every
  convention. This is visible to consumers who read band labels.
- N-12 is applied to all five F/H statistic readings: TEST_ANOVA_F,
  TEST_ANOVA_WELCH, TEST_ANOVA_RM, TEST_BROWN_FORSYTHE and TEST_KRUSKAL_WALLIS.
- N-18 is applied to the three spans the finding names. TEST_CHISQ's science use
  case ("depends on treatment arm") is left as written, because in a randomised
  design that reading is correct.

## E3 atomic-skill contradictions fixed

Atomic skills (`internal/skills/op-test-*.md`) were edited only where they
contradicted the code or the reviewed metadata. All of these are in `f4ea6131`.

| skill | contradiction | fix |
|---|---|---|
| `op-test-ks` (+ `capabilities_tests.go`, `types/types.go` comment) | Declared an `alternative` param that `test_ks.go` ignores (always two-sided), and said Details carries "the alternative" | Param removed from capabilities (manifest golden regenerated). Details documented as `groups` + `n`. Small-n refusal stated as n < 2 per group |
| `op-test-shapiro-wilk` (+ capability description, `types/types.go` comment) | Said n > 5000 is skipped with `PULSE_TEST_SHAPIRO_N_BOUND`; suggested "`TEST_KS` vs fitted normal"; called the statistic Shapiro-Wilk W; framed the test as a pre-ANOVA gate | The code only warns. The statistic is Shapiro-Francia W′, and p is advisory outside 5..5000. The KS suggestion and the gate framing are removed |
| `op-test-anova-rm` | A missing condition raised `PULSE_TEST_RM_UNBALANCED` (no such code); DF = (k−1, (n−1)(k−1)) | Incomplete subjects are dropped (`dropped_subjects`). DF holds k−1 and `df_error` is in Details. `partial_eta_squared` is unbanded |
| `op-test-trend` | Said Statistic = S and n ≥ 10 gated | Statistic is Mann-Kendall Z, with S in `Details.s`. n < 3 is refused and n < 8 warns |
| `op-test-paired-t` | Listed `Details.sd_diff` and `Details.n_dropped`, which are not emitted | The real keys are `mean_diff`, `variance`, `n`, `ci_low`, `ci_high`. d_z is unbanded |
| `op-test-kendall-tau` | `n_ties_x` / `n_ties_y` | The real keys are `ties_x`, `ties_y` (plus `s`, `var_s`, `z`, `n`) |
| `op-test-brown-forsythe` | `per_group{n, median, mean_abs_dev}`; DF = (k−1, N−k); n_i < 3 gated; "pre-ANOVA gate" | The real Details are flat: `group_medians`, `abs_dev_means` and others. DF holds k−1. The code refuses n_i < 2 or N ≤ k. The gate framing is removed |
| `op-test-fisher-exact` | Did not say labels are in first-seen order, or what zero cells do to the OR; named a nonexistent `PULSE_TEST_FISHER_NOT_2X2` | Both behaviours are now stated: first-seen order, and no Haldane correction (zero b/c gives +Inf, zero a/d gives 0). The real error code is `PULSE_TEST_CONTINGENCY_DEGENERATE` |
| `op-test-anova-f` | Brown-Forsythe and Shapiro presented as gates | Now says to prefer Welch outright and that Shapiro describes shape |
| `op-test-kruskal-wallis` | Said n_i < 5 is gated | Only N < 2k is refused; small groups make the p-value shaky. ε² is unbanded |

## Open items for the U33 human reviewer

- **Panel independence.** The LLM panel is two passes of one model family. Work
  on multi-LLM judging suggests that even nine frontier models give about two
  effectively independent votes (arXiv 2605.29800). Shared misconceptions can
  therefore survive both passes. Treat "no finding" as weak evidence, and give
  priority to anything the deterministic layers cannot check, such as NotFor
  routing and assumption completeness.
- **Out-of-scope runtime bugs found during E3.** These were not fixed, because
  they need engine changes:
  - **Infinite values break `--json`.** `TEST_FISHER_EXACT` returns OR = +Inf
    (statistic and `details.odds_ratio`) when b or c is zero.
    `TEST_PEARSON_R` / `TEST_SPEARMAN_R` return `details.t = ±Inf` when |r| = 1.
    These values are not sanitised, so JSON encoding fails.
  - **`TEST_BROWN_FORSYTHE` with zero within-group spread.** When MS_within is 0
    the test reports F = 0 and p = 0. It should refuse, or report an undefined F.
  - **Confidence intervals are slightly too narrow.** The Winitzki inverse-erf
    approximation gives a CI critical z about 4.7e-4 (relative) too small at
    α = .05. This affects the TEST_Z_TWO_SAMPLE, TEST_PROP_Z and TEST_PEARSON_R
    intervals.
  - **Shapiro-Francia p-value for n < 5.** The p-value is uncalibrated there.
    It is now flagged with a warning (S-19), but the number itself is unchanged.
    Consider refusing, or switching to an exact method.
- **Convention choices for sign-off.** "very small" as the label below Cohen's
  small; the logistic OR conversion (Chinn 2000) as the odds-ratio band source;
  leaving rank-biserial, d_z, RM partial η², rank ε², Cramér's V (df-scaled),
  ρ and τ unbanded.

## E4 findings — overlays and regressions

<!-- E4 (OVERLAY_* + REG_*) panel: append its method deltas, findings table,
     skill contradictions fixed and open items here, in the same shape as E3. -->

_Pending: the E4 panel has not run yet._
