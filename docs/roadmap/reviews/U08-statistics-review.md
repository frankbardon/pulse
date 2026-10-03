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
- **U09 scope (appended at the end):** the descriptive guidance of roadmap
  [U09](../units/U09-guidance-backfill-descriptive.md): every descriptive
  Purpose and needs-reading Interpretation. The U33 sign-off covers both units.

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

### Method (E4)

The same layers as E3. The deterministic gates (convention registry, prose
lint, R oracle) cover every `OVERLAY_*` and `REG_*` Purpose and Interpretation,
and the runtime probes in `internal/service/interpretation_runtime_test.go` and
`internal/processing/regression/interpretation_runtime_test.go` hold every
declared output field to what the engine emits.

- **Model:** Claude Opus 5.5, run read-only on 2026-10-03, same two passes and
  finding rules as E3 (quoted span substring-checked, rule ID or named source).
- **Delta from E3:** each pass prompt names the overlay and regression scope,
  and pass 1 lists the claims the authors flagged for independent checking.
  The scope prompt points at the E4 files and tells the panel not to re-raise
  E3 findings.
- **Triage:** the maintainer accepted all 28 findings (OS-01..OS-16,
  ON-01..ON-12). OS-16 and ON-05 overlap, which leaves 27 distinct fixes. Two
  findings (OS-01, OS-11) were fixed in the engine rather than caveated, so
  the guidance describes the fixed behaviour.

#### E4 pass 1 prompt (statistician)

```text
You are an adversarial statistician reviewing the guidance metadata of a statistics engine (Pulse) — this pass covers result overlays (OVERLAY_*) and regressions (REG_*). Your job is to find statements that are statistically WRONG or incomplete: incorrect test descriptions, wrong or missing assumptions, wrong effect-size conventions or formulas, wrong reading of a statistic or p-value, NotFor routing that sends a user to an inappropriate alternative test, claims that disagree with what the code actually computes. Assume the author shares common misconceptions (e.g. "Mann-Whitney tests medians", "Welch assumes equal variances", "p is the probability the null is true", Cohen bands applied to the wrong statistic) and hunt for them.
Authors flagged these for independent checking (verify, do not assume): CHISQ_VS_POP drops population categories the subset never shows from both statistic and df; KS_VS_POP p-value uses full row counts while D comes from binned curves; ZSCORE_VS_TOTAL bound sqrt(N-1) attributed to Shiffler (1988) The American Statistician 42(1):79-80; REG_GLM pseudo_r2 = 1 - D/D0 described relative to McFadden; REG_BAYES_LINEAR prior precision on raw predictor units including intercept, StdErrors as Student-t scale; GLM sign reading under gamma's default inverse link; T/Z cell overlays' placeholder params (variance 1, n 2) without AGG_WELFORD.
```

#### E4 pass 2 prompt (novice reader)

```text
You are an adversarial novice-reader reviewer of the guidance metadata of a statistics engine (Pulse) — this pass covers result overlays (OVERLAY_*) and regressions (REG_*). Read every string as an analyst with no statistics training would, and find text that would MISLEAD them: phrasing that invites "significant = important", "not significant = no effect", causal readings of correlations, overconfident language, jargon used without being linked in the Purpose's Glossary list or defined, ambiguous referents, and plain-language summaries that oversimplify into something false. You are not judging statistical correctness in depth — a separate statistician pass does that — but anything a novice would act on wrongly is in scope.
```

#### E4 scope prompt (appended to both passes)

```text
SCOPE (read these files; the code is the ground truth for what each operator computes):
- internal/descriptor/purposes_overlays.go — every OVERLAY_* Purpose; internal/descriptor/purposes_regressions.go — every REG_* Purpose
- internal/descriptor/capabilities_overlay.go + capabilities_regressions.go — Inferential flags and declared params/outputs
- internal/descriptor/interpretations_overlays.go + interpretations_regressions.go — every OVERLAY_* / REG_* Interpretation (shared p-value set in interpretations.go is already reviewed; only flag its misuse)
- internal/descriptor/glossary.go — ONLY glossary terms added since the E3 review (baseline, index-value, percentage-point, margin, rolling-mean, probit, adjusted-r-squared, heteroscedasticity, generalized-linear-model, link-function, logistic-regression, deviance, pseudo-r-squared, overdispersion, prior, posterior, credible-interval)
- internal/descriptor/conventions.go + internal/descriptor/testdata/conventions.json — effect-size convention registry
- internal/descriptor/conventions.go cohen1988_r2 entry + testdata/conventions.json ZSCORE_* / regression exclusions
- internal/processing/overlay*.go and internal/processing/regression/*.go — implementation, read-only, to verify claims (which slot holds p, df, pooled vs unpooled, binned vs raw, prior form, pseudo-R2 definition, link defaults)
- docs/roadmap/reviews/U08-statistics-review.md — the E3 review; do NOT re-raise findings already fixed there
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

### Findings

Spans are quoted as they read at `25c51611`, before the fix.

| id | pass | severity | operator / field | quoted span | rule / source | verdict | fix commit |
|---|---|---|---|---|---|---|---|
| OS-01 | 1 statistician | high | OVERLAY_CHISQ_VS_POP / scalar (Means) | `the subset's category counts against the counts the population's shares` | Pulse implementation internal/processing/overlay_chisq_vs_pop.go (expected = popFreq * subsetN) + overlay_facet_pop.go:253-268 (DiscreteFrequency = count / FilteredRecords); Pearson GOF definition (Agresti, *Categorical Data Analysis*) | accepted (engine fix) | `9483eced`, `b2de8f63` |
| OS-02 | 1 statistician | medium | OVERLAY_CHISQ_VS_POP / summary.parameters.df | `Degrees of freedom: the number of categories in the subset's facet values, minus 1.` | Pulse implementation overlay_chisq_vs_pop.go (observedN++ runs for every subset value; the stat adds only where expected > 0) | accepted (engine treatment) | `9483eced`, `b2de8f63` |
| OS-03 | 1 statistician | medium | OVERLAY_CHISQ_VS_REF / summary.statistic | `The chi-square statistic: the target's cell counts against the counts the reference's cell shares predict at the target's total.` | Pulse implementation internal/processing/overlay_compose_handlers.go:applyChiSqVsRef (targetN sums every paired cell; cells with ref = 0 are `continue`d) | accepted | `b2de8f63` |
| OS-04 | 1 statistician | high | OVERLAY_PAIRWISE_PROBIT_T / Assumptions | `It treats each probit value as having spread 1/sqrt(n), a simplification that differs from the textbook two-proportion test; use it to match a tool that applies it.` | Delta method (Agresti, *Categorical Data Analysis*): Var(Φ⁻¹(p̂)) ≈ p(1-p) / (n·φ(Φ⁻¹(p))²) ≥ (π/2)/n, with equality at p = 0.5; Pulse implementation overlay_pairwise.go:612-637 | accepted | `b2de8f63` |
| OS-05 | 1 statistician | medium | OVERLAY_PAIRWISE_PROBIT_T / Assumptions | `Shares of exactly 0 or 1 are clipped just inside that range before the transform.` | Pulse implementation overlay_pairwise.go:620-622 (eps = 1e-10, so Φ⁻¹ ≈ ±6.36) | accepted | `b2de8f63` |
| OS-06 | 1 statistician | medium | OVERLAY_KS_VS_POP / Assumptions | `Coarse bins understate the gap and make the test conservative.` | Pulse implementation overlay_ks_vs_pop.go:73-76 and ksDistancePercentiles / interpolatePercentileCDF (linear interpolation, clamped tails) | accepted | `b2de8f63` |
| OS-07 | 1 statistician | low | OVERLAY_KS_VS_POP / Assumptions | `when the subset is part of the population they overlap and the p-value is only approximate.` | Two-sample KS algebra: F_pop = f·F_sub + (1-f)·F_rest, so D(sub, pop) = (1-f)·D(sub, rest), with f = n_subset/n_pop (Conover, *Practical Nonparametric Statistics*) | accepted | `b2de8f63` |
| OS-08 | 1 statistician | low | OVERLAY_CHISQ_ROW (also _COL, line 161) / Assumptions | `so a large row pulls it toward itself and the p-value is approximate.` | Algebra of Pearson's X²: row i's contribution = X²(row i vs rest) × (N - n_i)/N (Agresti, *Categorical Data Analysis*); Pulse implementation overlay.go applyChiSqRow | accepted | `b2de8f63` |
| OS-09 | 1 statistician | low | OVERLAY_CHISQ_MATRIX / summary.parameters.df | `Degrees of freedom of the chi-square reference curve: (rows - 1) x (columns - 1) of the host crosstab.` | Pulse implementation overlay.go:689 (df fixed at (r-1)(c-1); zero-margin cells skipped in the statistic at overlay.go:680-684) | accepted | `b2de8f63` |
| OS-10 | 1 statistician | low | OVERLAY_ZSCORE_VS_TOTAL / summary.statistic | `With N groups no value can sit further than sqrt(N - 1) from 0 (Shiffler 1988)` | Shiffler (1988), *The American Statistician* 42(1):79-80, states the bound as (n - 1)/sqrt(n) for the sample (n - 1 divisor) SD | accepted | `b2de8f63` |
| OS-11 | 1 statistician | medium | OVERLAY_PROP_Z_CELL / Description | `margin, etc.) produce NaN p-values with one PULSE_OVERLAY_REF_ZERO warning per affected cell.` | Pulse implementation overlay_compose_handlers.go:applyPropZCell (`if nTarget <= 0 { nTarget = targetVal }`, same for nRef) | accepted (engine fix) | `91f34658`, `b2de8f63` |
| OS-12 | 1 statistician | low | OVERLAY_T_CELL / OVERLAY_Z_CELL (and T/Z_VS_REF lines 561, 591) / Assumptions | `falls back to placeholder params (variance 1, n 2), and its p-values then describe those placeholders, not your data.` | Pulse implementation overlay_compose_handlers.go:applyTCell (varianceFromParams / sampleSizeFromParams on variance_target, variance_ref, sample_size_target, sample_size_ref) | accepted | `b2de8f63` |
| OS-13 | 1 statistician | medium | REG_BAYES_LINEAR / Assumptions (prior) | `with one shared precision for every coefficient, intercept included, and the residual variance is inverse-gamma.` | Pulse implementation internal/processing/regression/bayes_linear.go:17 (β given σ² ~ Normal(μ₀, σ²·Λ₀⁻¹)); conjugate NIG model (Gelman et al., *Bayesian Data Analysis*, 3rd ed., §14.8) | accepted | `b2de8f63` |
| OS-14 | 1 statistician | high | REG_BAYES_LINEAR / credible_intervals.* | `With the default weak prior it is numerically close to the OLS confidence interval; with an informative prior it also reflects that prior.` | Pulse implementation bayes_linear.go:271-322 (a_n = a₀ + n/2, scale² = (b_n/a_n)(Λ_n⁻¹)_jj, t on 2·a_n df); OLS uses RSS/(n - p - 1) and t on n - p - 1 df (regression/stats.go) | accepted | `b2de8f63` |
| OS-15 | 1 statistician | low | REG_BAYES_LINEAR / residual_std_err | `Here it is a posterior estimate of the residual standard deviation, combining the data with the prior on the variance.` | Pulse implementation bayes_linear.go:309-310 (residualStdErr = sqrt(b_n / a_n)); inverse-gamma moments (Gelman et al., *Bayesian Data Analysis*) | accepted | `b2de8f63` |
| OS-16 | 1 statistician | medium | glossary adjusted-r-squared / Short | `so adding a predictor that helps no more than chance would does not raise it.` | Wooldridge, *Introductory Econometrics* (adjusted R² rises when an added predictor's \|t\| > 1); Greene, *Econometric Analysis* | accepted (fixed once with ON-05) | `b2de8f63` |
| ON-01 | 2 novice | medium | REG_OLS / p_values.* | `{Field: "p_values.*", Shared: SharedPValue},` | Pulse implementation internal/processing/regression/spec.go:19-23 (REG_OLS `Alpha` = penalty strength); ASA Statement principle 3 (Wasserstein & Lazar 2016) | accepted | `b2de8f63` |
| ON-02 | 2 novice | medium | REG_OLS / coefficients.* | `The change in the outcome's expected value for a one-unit increase in this predictor` | CORR-CAUSAL | accepted | `b2de8f63` |
| ON-03 | 2 novice | low | REG_BAYES_LINEAR / coefficients.* | `the expected change in the outcome for a one-unit increase in this predictor` | CORR-CAUSAL | accepted | `b2de8f63` |
| ON-04 | 2 novice | low | REG_OLS / UseCases.survey | `Relate overall satisfaction to several attribute ratings at once (key-driver analysis).` | CORR-CAUSAL | accepted | `b2de8f63` |
| ON-05 | 2 novice | medium | glossary adjusted-r-squared / Short | `so adding a predictor that helps no more than chance would does not raise it.` | Greene, *Econometric Analysis* (standard textbook): adjusted R-squared rises whenever the added predictor's t-statistic exceeds 1 in absolute value | accepted (fixed once with OS-16) | `b2de8f63` |
| ON-06 | 2 novice | low | REG_GLM / p_values.* | `{Field: "p_values.*", Shared: SharedPValue},` | ASA Statement principle 4 (Wasserstein & Lazar 2016, assumptions behind a p-value) | accepted | `b2de8f63` |
| ON-07 | 2 novice | medium | OVERLAY_ZSCORE_VS_ROLLING / summary.statistic | `In each series entry: how far the point sits from the mean of the W points before it, in sample standard deviations of those W points.` | Pulse implementation internal/processing/overlay_zscore_vs_rolling.go:59-63,197 (emits once carrier count >= 2, not == W) | accepted | `b2de8f63` |
| ON-08 | 2 novice | medium | OVERLAY_ZSCORE_VS_POP / Plain | `Places each facet category's share (or histogram bin) on a z-score scale against a comparison population: which values stand out?` | Pulse implementation internal/processing/overlay_zscore_vs_pop.go:35-46 (numeric arm: (bin_center - pop_mean)/pop_sd, subset counts not used) | accepted | `b2de8f63` |
| ON-09 | 2 novice | low | OVERLAY_ZSCORE_VS_POP / Questions | `Which answers does this segment pick far more or less often than everyone else?` | Pulse implementation internal/processing/overlay_zscore_vs_pop.go:24-26 (pop_freq from the comparison population, which may include the subset) | accepted | `b2de8f63` |
| ON-10 | 2 novice | medium | OVERLAY_PROP_Z_PANEL / cells.value | `Each cell holds a list of two-sided p-values, one per pair of requests, in upper-triangle order with the reference at index 0: ` | ASA Statement principle 5 (Wasserstein & Lazar 2016: a p-value does not measure the size of an effect) | accepted | `b2de8f63` |
| ON-11 | 2 novice | low | OVERLAY_FISHER_EXACT_CELL / cells.value | `It does not say which way the cell departs: compare the cell's count with what its row and column totals predict.` | ASA Statement principle 5 (Wasserstein & Lazar 2016) | accepted | `b2de8f63` |
| ON-12 | 2 novice | low | OVERLAY_CHISQ_VS_POP / Assumptions | `read the index values for size.` | ASA Statement principle 5 (Wasserstein & Lazar 2016) | accepted | `b2de8f63` |

### Engine fixes

**OS-01 / OS-02: `OVERLAY_CHISQ_VS_POP`** (`9483eced`). The population
shares were `count / FilteredRecords`. That denominator includes the
population's null rows and every value truncated out of a `DiscreteTopK`
listing, so the expected counts summed to less than the subset and chi-square
was inflated. The handler now rescales the population shares over the
categories both sides show, so the expected counts sum to the compared subset
total. This is R's `chisq.test(x, p, rescale.p = TRUE)`, and the tests pin
it to 1e-10 relative. A subset category the population never shows (OS-02) is
impossible under the population's mix. It is now left out of the statistic,
the subset total and df, with one `PULSE_OVERLAY_REF_ZERO` warning per
category, the same contract `OVERLAY_INDEX_VS_POP` uses for an absent
category. Before, it added nothing to chi-square but still counted in df.

| fixture | before | after (= R) |
|---|---|---|
| 25 population nulls in 125 rows; subset {30,30,30,10} vs population {40,30,20,10} | χ² 14.375, df 3, p 0.00244 | χ² 7.5, df 3, p 0.05756 |
| 40 population nulls + a 20-row top-K tail; subset {a:30, b:20, c:10, z:5} with z absent from the population | χ² 6.526, df 3 | χ² 1.444, df 2, p 0.4857, plus one REF_ZERO naming `z` |

**OS-11: `OVERLAY_PROP_Z_CELL` and `OVERLAY_PROP_Z_PANEL`** (`91f34658`).
When a row margin was missing or zero, the cell handler used the cell value as
n. That forced the side's share to 1, so against a side with a real margin
it returned a finite, usually tiny p-value with no warning. The capability
description already promised NaN. Now a missing or zero margin on EITHER side
gives a NaN cell with one `PULSE_OVERLAY_REF_ZERO` (`margin_missing: true`).
The panel's default `row_margin_value` leg had the same substitution. It now
reads a missing margin as n = 0, so each pair involving that slot is NaN with
`REF_ZERO` and the slot's other pairs are unaffected.
`types.PanelNSourceFallsBackToCellValue` keeps its signature and now reports
false for every mode. `OVERLAY_PAIRWISE_PROP_Z` never had the substitution.

| fixture (target 50/100 vs reference 60 with its margin missing) | before | after |
|---|---|---|
| PROP_Z_CELL | p 3.95e-11, no warning | NaN + REF_ZERO |
| PROP_Z_PANEL, reference pairs | p 3.95e-11 / 1.54e-08 | NaN + REF_ZERO; the target-vs-target pair is unchanged (0.1552) |

Both are behaviour changes. A fully margined PROP_Z panel and a CHISQ_VS_POP
over a null-free, untruncated population give byte-identical output.

### Notes on how fixes were applied

- OS-14's "about 18% narrower" is recomputed from the formulas. At n = 20 and
  p = 5 the default-prior interval is sqrt(14/20) x t(0.975, 20.002) /
  t(0.975, 14) = 0.814 of the OLS interval, so the text says about 19%.
- OS-16 / ON-05 merge both proposals. The text says adjusted R-squared rises
  only when the added predictor's |t| exceeds 1, which a pure-noise predictor
  does about one time in three (0.317 for large df). It fits the 200-character
  glossary limit.
- ON-02 / ON-03 keep the phrase "holding the other predictors fixed", because
  `TestRegressionInterpretations_Readings` requires it. They reword the
  intervention framing to a difference between rows and add "an association,
  not the effect of changing it".
- ON-08's proposed Plain is longer than the 140-character limit, so it is
  shortened. The numeric-bin limitation is stated in full in the Assumptions.
- OS-03: the expected counts do sum to the target total. The shortfall is on
  the observed side, because target counts in reference-zero cells set the
  scale but are skipped. The caveat is worded that way.
- OS-07 is applied to the KS_VS_POP Purpose and to the `n_pop` Interpretation.
  OS-08 is applied to both CHISQ_ROW and CHISQ_COL. OS-12 is applied to
  T_CELL, Z_CELL, T_VS_REF and Z_VS_REF.
- OS-10 also updates the reason for the `OVERLAY_ZSCORE_VS_TOTAL` exclusion in
  `testdata/conventions.json`.
- Checked by hand before applying: the CHISQ_ROW (N - n)/N factor (a numeric
  identity check), the KS nested-subset direction (λ shrinks by
  sqrt((1 - f)/(1 + f))), the probit spread floor sqrt(π/2) ≈ 1.25 at p = 0.5,
  the clip at 1e-10 → Φ⁻¹ ≈ -6.36, `sqrt(b_n / a_n)` as the reported residual
  scale, and the rolling window emitting from 2 prior points.

## E4 skill and capability contradictions fixed

Edited only where the skill, capability or doc comment contradicted the code.
These are in `b2de8f63`, except the engine-fix rows.

| surface | contradiction | fix |
|---|---|---|
| `op-overlay-chisq-vs-ref` | Said `Payload.Scalar` carries χ² | The scalar carries the p-value (= `Summary.PValue`). χ² is in `Summary.Statistic` |
| `op-overlay-index-vs-margin`, `op-overlay-delta-vs-margin` | "`Level`/`Within` must be `0`" | Both are honoured as nested-axis prefix truncation of the margin denominator, range-checked with `PULSE_OVERLAY_LEVEL_OUT_OF_RANGE` (matches the capability) |
| `op-overlay-delta-vs-stage`, `op-overlay-index-vs-stage` | `Scope` must be `chain`; `Level`/`Within` must be zero; `Ref.Stage` | There is no `chain` scope. Scope is echoed and the capability declares `total`. `ChainOverlaySpec` has no Level/Within. `Ref` / `Target` are `StageRef`s |
| `op-overlay-zscore-vs-pop` | An absent population entry gives REF_ZERO and is skipped | It reads as `pop_freq = 0` and is emitted with no warning (matches the capability) |
| `op-overlay-t-cell`, `-z-cell`, `-t-vs-ref`, `-z-vs-ref` | Placeholder params were presented as a convenience | They now warn that the params apply one value to every cell or group, so the p-values describe the supplied values |
| `op-overlay-chisq-vs-pop` (+ capability) | `expected = pop_freq × subset_N`, `df = len(observed) - 1` | Rescaled shares over the compared categories, with df = compared − 1 (`9483eced`) |
| `op-overlay-prop-z-cell`, `op-overlay-prop-z-panel` (+ capabilities, `types/overlay_panel.go`, `.claude/reference/update-demand.md`) | Panel default "keeps the legacy `<= 0` fall back to the cell value" | No mode borrows the cell value (`91f34658`) |
| `op-reg-glm` | `PseudoR2` (McFadden) | 1 − D/D₀, which equals McFadden only for 0/1 binomial |
| `op-reg-bayes-linear` | `prior_mu` length = predictor count; `SERVICE_VALIDATION`; StdErrors "from the posterior"; resample/selection "accepted at the spec level" | Length is predictors + 1 with the intercept first. The error is `PROCESSING_CONFIG`. StdErrors is the Student-t scale, not the posterior SD. Resample/selection are refused. `prior_precision` is relative to σ² |
| `capabilities_regressions.go` REG_BAYES_LINEAR | Advertised `resample`, `bootstrap_iters`, `rng_seed`, `selection`, `criterion` and both modifiers | `validateBayesLinearSpec` refuses them, so they are removed (manifest golden regenerated) |
| `types.RegressionSpec.PriorMu` / `.Link` doc comments | PriorMu length = predictor count; the gamma link defaults to "log" | Predictors + 1 with the intercept first; gamma defaults to "inverse", the only gamma link implemented (comment-only) |
| `bayes_linear.go` header comment | sqrt(b_n/a_n) called "the posterior-mean estimate of σ" | It is neither the posterior mean of σ nor of σ²; under the default prior it is about sqrt(RSS/n) |

## Open items for the U33 human reviewer (final, E3 + E4)

Everything listed under the E3 open items above still stands. E4 adds:

- **Shiffler (1988) not fetched.** The ZSCORE_VS_TOTAL bound is attributed to
  Shiffler, *The American Statistician* 42(1):79-80, and is restated for the
  divide-by-N SD. The (N − 1)/sqrt(N) form comes from the panel and from
  algebra, not from the paper. Check the citation against the source.
- **`OVERLAY_PAIRWISE_PROBIT_T` is anti-conservative by design.** Its
  1/sqrt(n) spread understates the probit SE at every share (by at least
  sqrt(π/2)). It ships to reproduce a tool that applies it, and the guidance
  now says its p-values are too small. Decide whether it should stay
  inferential-flagged, gain a warning, or move behind a feature profile.
- **GLM gamma path not externally verified.** `REG_GLM` gamma (inverse link,
  the only one implemented) has no R-oracle pin. Its standard errors and
  p-values fix the dispersion at 1, which the guidance flags as unreliable.
- **Regression outputs not verified against an external oracle.** OLS, GLM and
  Bayes coefficients, SEs, p-values and intervals are checked by in-repo
  goldens and analytic tests, not by R. This is owned by U36.
- **CHISQ_VS_POP compares only the listed categories.** After OS-01 the test is
  a goodness-of-fit over the categories both sides show. A population category
  missing from the subset's listing is still left out (caveated). Consider a
  full-dictionary comparison when `DiscreteTopK` does not truncate.
- **OVERLAY_INDEX_VS_MARGIN declared scopes.** The skill lists `row` / `column`
  scopes and a SERIES output, but the capability declares only `cell` / MATRIX.
  This was not verified against the scope gate in this pass.
- **Penalised OLS p-values.** l1 and elasticnet p-values are plug-in
  approximations over the kept predictors. The guidance says so, but there is
  no reference to sign off against.

## U09 — descriptive guidance review

Roadmap [U09](../units/U09-guidance-backfill-descriptive.md) gave every
descriptive built-in (aggregators, attributes, windows, features, filters,
groupers, synth distributions) a Purpose, and every needs-reading one a
`value` / `value.*` Interpretation. This section records the statistical review
of that guidance, run as story E6-S2. It follows the E3 / E4 pattern and feeds
the same U33 sign-off.

### Method (U09)

The deterministic layers are the same as E3 / E4 and cover the new guidance
too: the convention registry (no descriptive reading carries bands; the
unbanded scale-free outputs such as `AGG_SKEWNESS`, `AGG_KURTOSIS`,
`AGG_ZSCORE` and the `ATTR_*` z / T scores sit under the fixture's `excluded`
with reasons), the prose lint (`TestGuidanceProseLint`), the Purpose limits and
jargon rule, the reading-list gate (`TestDescriptiveReadingLists`) and the now
binding coverage gates. The value paths are static, so there is no runtime
probe; the per-category `reading_semantics*_test.go` files pin the behaviour
the readings describe.

- **Model:** Claude Opus 5.5, run read-only on 2026-10-03.
- **Scope A (two passes):** the statistically loaded guidance: every
  needs-reading Interpretation, the Purposes of `AGG_CI_*`, `AGG_SKEWNESS`,
  `AGG_KURTOSIS`, `AGG_STDDEV`, `AGG_VARIANCE`, `AGG_PERCENTILE`, `ATTR_REG_*`,
  `ATTR_ZSCORE`, `ATTR_TSCORE`, `ATTR_PERCENTILE` and `FEAT_TARGET_ENCODE`, the
  `TEST_*` spans edited in E6-S1 (`437eb100`) and the glossary terms they link.
  Pass 1 is the statistician adversary (AS-NN), pass 2 the novice-reader
  adversary (AN-NN), run independently.
- **Scope B (one combined pass):** every other descriptive Purpose (the
  self-reading operators and all 15 synth distributions) plus intent honesty,
  with both adversary briefs in one pass (B-NN).
- **Finding rules:** as E3 / E4. Every finding quotes the exact span and gives
  file:line, and cites a lint rule ID or a named source. The source list adds
  Hyndman & Fan (1996), Joanes & Gill (1998), Hoaglin & Welsch (1978) and
  Micci-Barreca (2001). All 64 quotes were substring-checked against the files
  at `437eb100`; none was discarded.
- **Triage:** the maintainer accepted all 64 findings (AS-01..AS-23,
  AN-01..AN-15, B-01..B-26), with the rule that the docs must match the code
  and no operator behaviour changes. Ten groups overlap (AS-01/AN-04,
  AS-02/AN-03, AS-03/AN-05, AS-05/AN-07/B-04, AS-10/AN-09, AS-12/AN-12,
  AS-15/AN-13/B-16, AS-18/AN-14, AS-22/AN-15, AS-23/AN-06), which leaves 52
  distinct fixes, each applied once. Runtime issues the findings exposed are
  logged for U36 below and not fixed here.

#### U09 pass 1 prompt (statistician)

```text
You are an adversarial statistician reviewing the guidance metadata of a statistics engine (Pulse) — this time its DESCRIPTIVE operators (aggregators, attributes, windows, features, filters, groupers, synth distributions). Your job is to find statements that are statistically WRONG or incomplete: incorrect descriptions of what the operator computes (population vs sample, estimator choice, null/tie/degenerate handling), wrong or missing assumptions, wrong effect-size conventions or formulas, wrong reading of a statistic or p-value, NotFor routing that sends a user to an inappropriate alternative test, claims that disagree with what the code actually computes. Assume the author shares common misconceptions (e.g. "a 95% CI means 95% of rows fall inside", "skewness of 0 means normal", "|z|>1.96 means significant for a row", "percentile rank equals the share below", "target encoding is leakage-safe after a split", bands applied to unbanded statistics) and hunt for them.
```

#### U09 pass 2 prompt (novice reader)

```text
You are an adversarial novice-reader reviewer of the guidance metadata of a statistics engine (Pulse) — this time its DESCRIPTIVE operators (aggregators, attributes, windows, features, filters, groupers, synth distributions). Read every string as an analyst with no statistics training would, and find text that would MISLEAD them: phrasing that invites "significant = important", "not significant = no effect", causal readings of correlations, overconfident language, jargon used without being linked in the Purpose's Glossary list or defined, ambiguous referents, and plain-language summaries that oversimplify into something false. You are not judging statistical correctness in depth — a separate statistician pass does that — but anything a novice would act on wrongly is in scope.
```

Scope B ran both briefs together in one pass.

#### U09 rules block (appended to every pass)

```text
Lint rule IDs already enforced by TestGuidanceProseLint (internal/descriptor/guidance_lint_test.go) — read that file for the IDs (ASA-*, MULTI-COMP, NORM-POWER, ASSUME-INDEP, ES-CONV, CORR-CAUSAL, ...). Named sources you may cite: ASA Statement on p-values (Wasserstein & Lazar 2016), APA JARS-Quant (Appelbaum et al. 2018), SAMPL (Lang & Altman), Cohen (1988/1992), a standard textbook by name, Hyndman & Fan (1996) for quantiles, Joanes & Gill (1998) for skewness/kurtosis estimators, Hoaglin & Welsch (1978) for leverage, Micci-Barreca (2001) for target encoding, or the Pulse implementation file:line.

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

#### U09 scope A prompt

```text
SCOPE A — statistically loaded descriptive guidance (U09). Read these files; the CODE is the ground truth for what each operator computes.
- internal/descriptor/interpretations_descriptive.go — needs-reading AGGREGATOR Interpretations (AGG_SKEWNESS, AGG_KURTOSIS, AGG_ZSCORE, AGG_CI_LOWER/UPPER, AGG_STDDEV, AGG_VARIANCE, AGG_WELFORD, AGG_PERCENTILE, AGG_RATIO, AGG_WEIGHTED_MEAN)
- internal/descriptor/interpretations_attributes.go — ATTR_ZSCORE, ATTR_TSCORE, ATTR_PERCENTILE, ATTR_NORMALIZED, ATTR_REG_FITTED/RESIDUAL/LEVERAGE
- internal/descriptor/interpretations_window.go — WIN_PCT_CHANGE, WIN_EWMA, WIN_MOVING_AVG, WIN_RUNNING_AVG, WIN_RANK, WIN_DENSE_RANK, WIN_DELTA
- internal/descriptor/interpretations_features.go — FEAT_LOG, FEAT_SQRT, FEAT_POLY, FEAT_TARGET_ENCODE, FEAT_FREQUENCY_ENCODE
- Purposes of the statistically loaded operators: AGG_CI_LOWER, AGG_CI_UPPER, AGG_SKEWNESS, AGG_KURTOSIS, AGG_STDDEV, AGG_VARIANCE, AGG_PERCENTILE (internal/descriptor/purposes_aggregators.go); ATTR_REG_* , ATTR_ZSCORE, ATTR_TSCORE, ATTR_PERCENTILE (purposes_attributes.go); FEAT_TARGET_ENCODE (purposes_features.go)
- TEST_* Purposes edited in commit 437eb100 (run `git show 437eb100 -- internal/descriptor/purposes_stattests.go` to see exactly which spans changed — TEST_PEARSON_R's new Assumption, Glossary links on MANN_WHITNEY_U, WILCOXON_SR, TREND, SHAPIRO_WILK, KS). Review only the changed spans + whether the linked glossary terms fit.
- internal/descriptor/glossary.go — the terms these strings link
- internal/descriptor/conventions.go + internal/descriptor/testdata/conventions.json — the convention registry and `excluded` list (no new bands may be added; flag any band applied wrongly or any excluded reason that is false)
- .claude/reference/guided-analysis.md — the `value` / `value.*` path rule and the needs-reading rule
- Implementation (read-only, to verify claims): internal/processing/aggregator*.go, internal/processing/attribute*.go, internal/processing/window/, internal/processing/feature/, internal/processing/test_*.go
```

#### U09 scope B prompt

```text
SCOPE B — the self-reading descriptive Purposes (U09). Read these files; the CODE is the ground truth.
- internal/descriptor/purposes_aggregators.go — all AGG_* Purposes EXCEPT those listed in scope A (AGG_CI_*, SKEWNESS, KURTOSIS, STDDEV, VARIANCE, PERCENTILE)
- internal/descriptor/purposes_attributes.go — ATTR_DATE_PART, ATTR_FORMULA, ATTR_SET_HAS, ATTR_SET_POPCOUNT, ATTR_NORMALIZED
- internal/descriptor/purposes_window.go — all WIN_*
- internal/descriptor/purposes_features.go — all FEAT_* except FEAT_TARGET_ENCODE
- internal/descriptor/purposes_filterers.go, purposes_groupers.go — all FILTER_* / GROUP_*
- internal/descriptor/purposes_synth.go — all 15 synth distributions
- internal/descriptor/intents.go — the intent taxonomy (check each Purpose's intents are honest for what the operator does)
- internal/descriptor/glossary.go — linked terms
- Implementation (read-only): internal/processing/ (aggregator*, attribute*, filter*, group*, window/, feature/), internal/synth/
Things to check per Purpose: Plain says what the operator really computes (verify in code); Questions are ones it actually answers; NotFor routes to an appropriate alternative; Assumptions/UseCases are true; intents are honest; no novice-misleading phrasing.
```

### Findings (U09)

Spans are quoted as they read at `437eb100`, before the fix. Severity count:
1 high, 42 medium, 21 low.

| id | pass | severity | operator / field | verdict | fix commit |
|---|---|---|---|---|---|
| AS-01 | A1 statistician | medium | AGG_KURTOSIS / value Sign "-" | accepted (overlaps AN-04) | `c63e96df` |
| AS-02 | A1 statistician | medium | AGG_SKEWNESS / value Sign | accepted (overlaps AN-03) | `c63e96df` |
| AS-03 | A1 statistician | medium | AGG_CI_LOWER / AGG_CI_UPPER caveat (descCINormal) | accepted (overlaps AN-05) | `c63e96df` |
| AS-04 | A1 statistician | medium | AGG_VARIANCE / value caveat (also purpose UseCase purposes_aggregators.go:531) | accepted | `c63e96df` |
| AS-05 | A1 statistician | medium | AGG_ZSCORE / Purpose Question + Ops UseCase (line 627) | accepted (overlaps AN-07, B-04) | `c63e96df` |
| AS-06 | A1 statistician | low | AGG_STDDEV / Purpose Question | accepted | `c63e96df` |
| AS-07 | A1 statistician | medium | ATTR_PERCENTILE / Purpose Plain | accepted | `e3c7b841` |
| AS-08 | A1 statistician | medium | ATTR_PERCENTILE / value Means | accepted | `e3c7b841` |
| AS-09 | A1 statistician | low | ATTR_PERCENTILE / Purpose Question | accepted | `e3c7b841` |
| AS-10 | A1 statistician | medium | ATTR_REG_LEVERAGE / Purpose Question (+ Ops UseCase line 310) | accepted (overlaps AN-09) | `e3c7b841` |
| AS-11 | A1 statistician | low | ATTR_REG_RESIDUAL / value caveat | accepted | `e3c7b841` |
| AS-12 | A1 statistician | low | ATTR_REG_* / attrRegPenalty | accepted (overlaps AN-12) | `e3c7b841` |
| AS-13 | A1 statistician | low | ATTR_TSCORE / Purpose Science UseCase | accepted | `e3c7b841` |
| AS-14 | A1 statistician | low | WIN_MOVING_AVG / value caveat | accepted | `e3c7b841` |
| AS-15 | A1 statistician | medium | FEAT_LOG / value caveat | accepted (overlaps AN-13, B-16) | `e3c7b841` |
| AS-16 | A1 statistician | medium | FEAT_TARGET_ENCODE / value caveat (remedy) | accepted | `e3c7b841` |
| AS-17 | A1 statistician | medium | FEAT_TARGET_ENCODE / Purpose Science UseCase | accepted | `e3c7b841` |
| AS-18 | A1 statistician | medium | TEST_PEARSON_R / Assumption (new in 437eb100) | accepted (overlaps AN-14) | `e3c7b841` |
| AS-19 | A1 statistician | medium | glossary test-statistic / WhyCare (newly linked from MANN_WHITNEY_U, WILCOXON_SR, TREND, SHAPIRO_WILK, KS) | accepted | `e3c7b841` |
| AS-20 | A1 statistician | low | glossary statistical-significance / WhyCare (newly linked from SHAPIRO_WILK, KS) | accepted | `e3c7b841` |
| AS-21 | A1 statistician | low | glossary variance / Short (linked from AGG_VARIANCE, AGG_STDDEV) | accepted | `e3c7b841`, `218441a9` |
| AS-22 | A1 statistician | medium | glossary percentile / Short (linked from AGG_PERCENTILE and ATTR_PERCENTILE) | accepted (overlaps AN-15) | `e3c7b841` |
| AS-23 | A1 statistician | medium | AGG_WEIGHTED_MEAN / value caveat (n_eff) | accepted (overlaps AN-06) | `c63e96df` |
| AN-01 | A2 novice | medium | AGG_SKEWNESS / value.means | accepted | `c63e96df` |
| AN-02 | A2 novice | medium | AGG_KURTOSIS / value.means | accepted | `c63e96df` |
| AN-03 | A2 novice | medium | AGG_SKEWNESS / value.sign | accepted (overlaps AS-02) | `c63e96df` |
| AN-04 | A2 novice | medium | AGG_KURTOSIS / value.sign "-" | accepted (overlaps AS-01) | `c63e96df` |
| AN-05 | A2 novice | medium | AGG_CI_LOWER / AGG_CI_UPPER / caveats (descCINormal) | accepted (overlaps AS-03) | `c63e96df` |
| AN-06 | A2 novice | medium | AGG_WEIGHTED_MEAN / value.caveats[1] | accepted (overlaps AS-23) | `c63e96df` |
| AN-07 | A2 novice | medium | AGG_ZSCORE / questions[1] | accepted (overlaps AS-05, B-04) | `c63e96df` |
| AN-08 | A2 novice | medium | AGG_CI_UPPER / use_cases.ops | accepted | `c63e96df` |
| AN-09 | A2 novice | medium | ATTR_REG_LEVERAGE / questions[1] | accepted (overlaps AS-10) | `e3c7b841` |
| AN-10 | A2 novice | low | ATTR_REG_LEVERAGE / value.means | accepted | `e3c7b841` |
| AN-11 | A2 novice | low | ATTR_TSCORE / use_cases.survey | accepted | `e3c7b841` |
| AN-12 | A2 novice | low | ATTR_REG_FITTED / ATTR_REG_RESIDUAL / caveat (attrRegPenalty) | accepted (overlaps AS-12) | `e3c7b841` |
| AN-13 | A2 novice | low | FEAT_LOG / value.caveats[1] | accepted (overlaps AS-15, B-16) | `e3c7b841` |
| AN-14 | A2 novice | low | TEST_PEARSON_R / assumptions[3] (437eb100) | accepted (overlaps AS-18) | `e3c7b841` |
| AN-15 | A2 novice | low | glossary percentile / Short | accepted (overlaps AS-22) | `e3c7b841` |
| B-01 | B combined | medium | AGG_AVERAGE / Assumptions | accepted | `82f78ba2` |
| B-02 | B combined | medium | AGG_MIN (also AGG_MAX, AGG_RANGE, AGG_MEDIAN, AGG_MODE) / Assumptions | accepted | `82f78ba2` |
| B-03 | B combined | medium | AGG_WEIGHTED_MEAN / Assumptions | accepted | `82f78ba2` |
| B-04 | B combined | medium | AGG_ZSCORE / Questions | accepted (overlaps AS-05, AN-07) | `c63e96df` |
| B-05 | B combined | medium | AGG_ZSCORE / Intents | accepted | `c63e96df` |
| B-06 | B combined | medium | AGG_SET_DISTINCT_VALUES / Questions | accepted | `82f78ba2` |
| B-07 | B combined | low | AGG_RATIO / NotFor | accepted | `82f78ba2` |
| B-08 | B combined | medium | ATTR_FORMULA / Assumptions | accepted | `82f78ba2` |
| B-09 | B combined | medium | ATTR_SET_HAS / UseCases | accepted | `82f78ba2` |
| B-10 | B combined | medium | ATTR_NORMALIZED / UseCases | accepted | `82f78ba2` |
| B-11 | B combined | medium | WIN_ROW_NUMBER / Questions + Assumptions | accepted | `82f78ba2` |
| B-12 | B combined | low | WIN_DENSE_RANK / Questions | accepted | `82f78ba2` |
| B-13 | B combined | low | WIN_EWMA / Glossary | accepted | `82f78ba2` |
| B-14 | B combined | medium | FEAT_TRAIN_TEST_SPLIT / Plain + Assumptions | accepted | `82f78ba2` |
| B-15 | B combined | medium | FEAT_ONE_HOT / Questions + Assumptions | accepted | `82f78ba2` |
| B-16 | B combined | low | FEAT_LOG / Questions | accepted (overlaps AS-15, AN-13) | `e3c7b841` |
| B-17 | B combined | low | FEAT_BUCKETIZE / Questions | accepted | `82f78ba2` |
| B-18 | B combined | high | FILTER_TRUE / Assumptions | accepted | `82f78ba2` |
| B-19 | B combined | medium | FILTER_FALSE / Assumptions | accepted | `82f78ba2` |
| B-20 | B combined | medium | FILTER_NULL / Questions | accepted | `82f78ba2` |
| B-21 | B combined | low | FILTER_INCLUDE / FILTER_EXCLUDE (filterLabelsKnown) | accepted | `82f78ba2` |
| B-22 | B combined | low | FILTER_SET_CONTAINS_ANY (and _ALL, _NONE, _EQUALS) / Intents | accepted | `82f78ba2` |
| B-23 | B combined | medium | GROUP_RANGE / UseCases | accepted | `82f78ba2` |
| B-24 | B combined | medium | GROUP_QUANTILE / Questions + Assumptions | accepted | `82f78ba2` |
| B-25 | B combined | medium | uniform (synth) / Assumptions | accepted | `82f78ba2` |
| B-26 | B combined | medium | pareto (synth) / Assumptions | accepted | `82f78ba2` |

### Notes on how fixes were applied (U09)

- Each proposed fix was checked against the code before it was applied, and
  reworded where needed to fit the Purpose limits (`Plain` ≤ 140 characters),
  the glossary limits (`Short` ≤ 200, `WhyCare` ≤ 300) and the prose lint.
  AS-07's Plain and AS-20's WhyCare are shortened for that reason.
- AN-01 / AN-02: the skewness and kurtosis Means now write the formula over n,
  `(m3/n) / (m2/n)^1.5` and `(m4/n) / (m2/n)^2 - 3`, because the `m2` / `m3` /
  `m4` components are sums of powered deviations (`aggregator.go`
  `Components`).
- AS-02 / AN-03: the sign readings drop the mean-versus-median clause. A
  separate caveat says the sign does not fix that order and routes the
  question to `AGG_MEDIAN` beside `AGG_AVERAGE`.
- AS-05 / AN-07 / B-04: B-04 put the self-inclusion bound at (n − 1)/sqrt(n),
  which holds for the n − 1 standard deviation. `AGG_ZSCORE` uses the
  population SD, so the text uses AS-05's sqrt(n − 1). The first question
  ("what centre and scale") is kept. The second says "last row in row order,
  that row included", and the Purpose and Interpretation route a reading
  against the past to `OVERLAY_ZSCORE_VS_ROLLING`.
- B-05 / B-22: `AGG_ZSCORE` moves from distribution_shape + benchmark to
  describe + prepare, and the four set filters drop composition. Intent
  coverage was rechecked: benchmark, composition and distribution_shape each
  keep at least 3 declarers (`TestPurposeQuestionsResolve`, binding). The
  manifest golden was regenerated for both.
- AS-04: a UseCase names a use of the operator itself, so the science UseCase
  becomes a population use (a fully measured batch), not a pointer elsewhere.
  Pooling and study-size planning go to `AGG_WELFORD` through the caveat and
  the NotFor.
- AS-11: the panel's "divided by sqrt(1 − leverage)" is written as comparing
  the residual with `residual_std_err` × sqrt(1 − leverage), the residual's
  own spread (the internally studentized form).
- AS-18 / AN-14: the assumption also names `details.variance_x` /
  `variance_y`, both n − 1 (`test_pearson.go`), as the matching spreads.
- AS-21: the first wording named `AGG_VARIANCE` / `AGG_WELFORD` in the
  glossary. The virtual glossary skill is served through instance discovery,
  which drops sentences naming operators a feature profile hides, so the body
  stopped matching `skills.Get` (`TestGuidanceSkills_NeverPrunedByFeatureProfile`).
  Follow-up `218441a9` names the population (n) and sample (n − 1) forms only;
  the operator guidance says which operator computes which.
- B-13: the `WIN_EWMA` Plain calls the share "w", not "alpha", because "alpha"
  is a jargon form of the significance-level glossary term.
- B-21: `FILTER_INCLUDE` / `FILTER_EXCLUDE` get their own shared sentence
  (`filterValuesTyped`). The set filters keep `filterLabelsKnown`, because a
  set field always carries a dictionary.
- B-25 also fixes the `op-synth-uniform` skill's `max` row ("exclusive"), the
  one atomic-skill contradiction this pass found.
- No band and no `excluded` reason changed, so `testdata/conventions.json` is
  untouched.

## U09 skill and capability contradictions fixed

Reading the code while writing the U09 guidance contradicted these docs. Each
was fixed to match the code, docs only unless noted.

| surface | contradiction | fix commit |
|---|---|---|
| `FEAT_DATE_FEATURES` capability | Promised `<label>_day_of_week` / `_is_weekend`; it writes `<label>_dow` and `<label>_quarter` | `1da721c9` |
| `AGG_FREQUENCY` capability, skill, Purpose, example, `label-display` | Described a per-value map and a first-seen tie-break; it returns one float64 (the modal count) and keeps the smallest tied value | `adde0834` |
| ProcessChain gate (engine) | Refused `AGG_FREQUENCY` as map-emitting; it emits a mergeable scalar, so the chain now admits it | `7cc68a25` |
| `AGG_MODE` capability and skill | `EmitsTypeNote` said "string" and ties went to the first seen; it is float64 (a dictionary index on a category field) and the smallest value wins | `c85f7e05` |
| `AGG_SKEWNESS` / `AGG_KURTOSIS` capability and skills; `op-agg-zscore` | "Bias-corrected", NaN below n = 3 / 4; the code computes population g1 / excess g2 and returns 0 for n ≤ 1 or zero variance. The z-score skill claimed NaN on zero spread | `a666149e` |
| Attribute skills and capability notes | Missing input and constant fields documented as null / NaN; they read 0 (50 for `ATTR_TSCORE`). `ATTR_PERCENTILE` ties take distinct ranks, not a shared percentile | `a56a2f8b` |
| Window capability notes and `op-win-pct-change` | `WIN_MOVING_AVG` NaN on an empty frame (it is null); `WIN_PCT_CHANGE` a "percent" (it is a fraction) | `af507d6b` |
| `FEAT_LOG` capability; `feature-engineering`, `op-feat-target-encode`, `op-feat-train-test-split` skills | "Natural log of Field" (it is log1p); a preceding split was said to make target encoding train-only | `7e681766` |
| `PULSE_FEAT_TARGET_LEAKAGE_RISK` predict warning + fixup, examples 07 / 08 / 10 | The warning went silent after a split and the fixup prescribed one; the encoder ignores the split. The warning now always fires and the `leakage-safe` tag is gone | `46c96c04` |
| `op-group-rounded`, `op-group-range`, grouper capability | `GROUP_ROUNDED` said nearest multiple (it floors); `GROUP_RANGE` keys said `[10, 20)` (they read `10-20`) | `dff0e32e` |
| Synth skills and capability (regex, uniform_date, constant, uniform, lognormal) | Finite-only regex repeats, `end` must exceed `start`, a non-existent error code, integer truncation, sigma direction, no pre-1970 dates | `339af52f` |
| `op-synth-uniform` `max` row | "Exclusive"; on u8–u64 rounding can produce it | `82f78ba2` |

## Open items for the U33 human reviewer (U09)

The E3 / E4 open items above still stand. U09 adds the following. The guidance
now describes each runtime behaviour honestly; the behaviour itself is logged
for U36 (reference oracles and runtime fixes) and is not changed here.

- **`FILTER_TRUE` / `FILTER_FALSE` truthy mode tests category label text**
  (B-18 / B-19). A label such as "No" is non-empty text, so it counts as yes.
  The guidance routes Yes/No categories to `FILTER_INCLUDE`; decide whether
  truthy mode should refuse category fields.
- **Empty groups read 0, not empty** (B-01 / B-02 / B-03) for `AGG_AVERAGE`,
  `AGG_MIN`, `AGG_MAX`, `AGG_RANGE`, `AGG_MEDIAN`, `AGG_MODE` and
  `AGG_WEIGHTED_MEAN`, while `AGG_RATIO` and the CI bounds return NaN. Decide
  whether these should return null / NaN.
- **Grouped rows come back in text order of their keys** (B-23 / B-24):
  `GROUP_RANGE` puts `100-150` before `50-100`, and `GROUP_QUANTILE` reads
  `D1, D10, D2, …`. Empty bands are left out. Consider numeric-aware ordering.
- **`AGG_ZSCORE`'s value is always 0, and its components score the last row in
  scan order** against a mean and SD that include that row (AS-05 / AN-07 /
  B-04). Decide whether the operator should be kept, renamed or reshaped.
- **`AGG_CI_LOWER` / `AGG_CI_UPPER` use the normal critical value, not
  Student's t** (AS-03 / AN-05), so small groups get intervals that are too
  narrow. A switch to t would change results.
- **`ATTR_PERCENTILE` breaks ties in an unstable order** (`sort.Slice`), so
  tied rows can read different percentiles from run to run (AS-07 / AS-09).
- **Attributes write 0 (50 for `ATTR_TSCORE`) for a missing input** instead of
  null, so a missing row looks like an average or minimum one.
- **`FEAT_TARGET_ENCODE` ignores `FEAT_TRAIN_TEST_SPLIT`** and includes each
  row's own outcome (AS-16 / AS-17). There is no split-aware, out-of-fold or
  leave-one-out encoder; the guidance describes the manual workaround.
- **Synth `u4` fields wrap around** on out-of-range draws, where u8–u64
  saturate.
- **`GROUP_QUANTILE`, `GROUP_RANGE` and `GROUP_ROUNDED` accept category
  fields** and bin their dictionary indices.
- **Skewness and kurtosis stay unbanded.** No graded convention met the fixture
  bar (the 0.5 / 1 skewness rule of thumb is secondary-sourced only). Confirm
  that leaving them unbanded is right.
- **No external oracle for the descriptive estimators.** Population g1 / g2,
  type-7 percentiles, Kish n_eff and the hat-matrix leverage are pinned by
  in-repo tests, not by R. This is owned by U36.
