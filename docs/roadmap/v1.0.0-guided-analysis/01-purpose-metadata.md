# 01 — Purpose metadata & intent taxonomy

Everything else in this theme projects from two registries: the **intent taxonomy** (what kinds of questions exist) and per-operator **purpose metadata** (which questions each operator answers, and how to read its answer).

---

## P1. Intent taxonomy (Committed)

This is a small, closed set of question types. It is deliberately coarse: an intent has to be something a non-statistician recognises in their own question.

| Intent | The question sounds like… | Typical operators |
|---|---|---|
| `describe` | "What's the typical / total / spread of X?" | `AGG_SUM`, `AGG_AVERAGE`, `AGG_MEDIAN`, `AGG_PERCENTILE`, `AGG_STDDEV` |
| `compare_groups` | "Do A and B differ?" "Is this segment different from the rest?" | `TEST_T`, `TEST_WELCH`, `TEST_ANOVA_F`, `TEST_MANN_WHITNEY_U`, `TEST_KRUSKAL_WALLIS`, `TEST_CHISQ`, `TEST_PROP_Z`, `OVERLAY_*_CELL`, `TEST_MANOVA` |
| `relationship` | "Do X and Y move together?" | `TEST_PEARSON_R`, `TEST_SPEARMAN_R`, `TEST_KENDALL_TAU`, `MAT_CORRELATION`, `TEST_CHISQ` (two categoricals) |
| `drivers` | "What explains / predicts Y?" "Which factors matter most?" | `REG_OLS`, `REG_GLM`, `MAT_PARTIAL_CORRELATION`, `MAT_COLLINEARITY` |
| `change_over_time` | "Is it going up?" "How does this month compare to last?" | `GROUP_DATE`, `WIN_*`, `TEST_TREND`, `OVERLAY_YOY`, `OVERLAY_DELTA_VS_PRIOR` |
| `composition` | "What's the mix / share?" "Which attributes go with which brand?" | `AGG_FREQUENCY`, `OVERLAY_SHARE_OF_*`, `OVERLAY_CORRESPONDENCE`, `MAT_SET_AFFINITY` |
| `benchmark` | "How does this compare to the total / population / last wave?" | `OVERLAY_INDEX_VS_*`, `*_VS_POP`, `*_VS_REF` |
| `distribution_shape` | "Is it normal?" "Are there outliers?" | `AGG_SKEWNESS`, `AGG_KURTOSIS`, `TEST_SHAPIRO_WILK`, `TEST_KS`, `ATTR_ZSCORE`, `ATTR_MAHALANOBIS` |
| `segment` | "Are there natural groups of customers?" "Split into tiers" | `GROUP_KMEANS`, `GROUP_QUANTILE`, `GROUP_RANGE` |
| `measure_construct` | "Do these questions measure one thing?" "Combine items into a score" | `MAT_RELIABILITY`, `MAT_PCA`, `MAT_FACTOR`, `ATTR_SCALE_SCORE` |
| `flows` | "Where do customers move between states?" | crosstab + `OVERLAY_MARKOV` |
| `data_quality` | "Is this data trustworthy?" "Who answered carelessly?" | `AGG_NULL_COUNT`, `FILTER_NULL`, `ATTR_MAHALANOBIS`, `vcount`/`vsd` expr functions |

Plus non-analytic intents that route to tooling rather than operators: `prepare` (features, filters), `simulate` (synth), `lookup` (point lookup).

**Shape descriptors.** Each intent declares the *data shapes* it applies to, in schema-type terms. `compare_groups` needs one outcome (numeric or categorical) plus one grouping (categorical, or two filters). `relationship` needs two-or-more numeric fields, or two categoricals. This is what lets `Recommend` (03) bind a question to real cohort fields without understanding language.

Lives in: `descriptor/intents.go`, projected into the manifest as `intents[]`.

---

## P2. `Purpose` block per operator (Committed)

Every registered operator gets a `Purpose`: built-ins, overlay kinds, regressions, synth distributions, and, optionally, extensions. It is declared next to the existing `descriptor/capabilities_*.go` entries.

```go
Purpose{
    Plain:     "Checks whether the average of a measure differs across three or more groups.",
    Intents:   []Intent{IntentCompareGroups},
    Questions: []string{
        "Do average satisfaction scores differ by region?",
        "Is spend different across the four store formats?",
    },
    UseCases: map[Domain]string{
        DomainSurvey: "Compare mean ratings across segments or waves.",
        DomainOps:    "Compare average order value across channels.",
    },
    NotFor: []Alternative{
        {When: "only two groups", Use: "TEST_WELCH"},
        {When: "skewed data or ordinal ratings with small groups", Use: "TEST_KRUSKAL_WALLIS"},
        {When: "the outcome is a yes/no or category", Use: "TEST_CHISQ"},
        {When: "you need to know WHICH groups differ", Use: "TEST_TUKEY_HSD"},
    },
    Assumptions: []string{
        "Groups are independent (different people in each).",
        "Spread is similar across groups — check with TEST_BROWN_FORSYTHE, or use TEST_ANOVA_WELCH.",
    },
    Level:    LevelIntermediate,
    Glossary: []string{"p-value", "effect-size", "f-statistic"},
}
```

| Field | Rule | Why |
|---|---|---|
| `Plain` | ≤ 140 chars; no term from the glossary's `jargon` list unless it is linked | the one sentence a developer reads |
| `Intents` | ≥ 1, from P1 | routing for Recommend, docs and examples |
| `Questions` | ≥ 2 natural-language questions | the search corpus for agents and examples, plus doc copy |
| `UseCases` | keyed by the four audience domains, ≥ 1 | "is this for me?" |
| `NotFor` | ≥ 1 `{When, Use}`, where `Use` must be a registered operator | the most valuable guidance; gate-checked so it can't point at a removed operator |
| `Assumptions` | plain sentences | feed predict advisories (03) |
| `Level` | `basic` / `intermediate` / `advanced` | lets docs and Recommend prefer simpler tools first |
| `Glossary` | term IDs from P4 | linked definitions everywhere |

**Where it lives — decision.** Go declarations, not skill frontmatter. The manifest, the gates and the facade (Recommend, Explain) need typed access, and `descriptor/` is already the declaration layer. Skills receive a **rendered** section (P5) instead of duplicating the text.

**Extensions.** `pulse.Options.Extensions` registrations accept an optional `Purpose`. Without one, the operator appears in the catalog as "no guidance provided", and Recommend never proposes it.

---

## P3. `Interpretation` rules (Committed)

Purpose says *when* to use an operator. Interpretation says *how to read what came back*. It is declared per output field:

```go
Interpretation{
    Field: "statistic",                      // for TEST_PEARSON_R: r
    Means: "Strength and direction of a straight-line relationship, from -1 to +1.",
    Bands: []Band{                           // |r|, labelled convention, never authoritative
        {Max: 0.1, Label: "negligible"}, {Max: 0.3, Label: "weak"},
        {Max: 0.5, Label: "moderate"},   {Max: 1.0, Label: "strong"},
    },
    Convention: "Cohen (1988) — domain norms differ; survey data rarely exceeds 0.5.",
    Sign: map[string]string{"+": "as one rises the other tends to rise", "-": "as one rises the other tends to fall"},
    Caveats: []string{"Correlation is not causation.", "Only detects straight-line patterns; check TEST_SPEARMAN_R for curved-but-consistent ones."},
}
```

- **p-value interpretation is shared.** One rule set for every `TEST_*` and inferential overlay: what α means, the "significant ≠ important" caveat, and when a multiple-comparison warning applies.
- **Effect sizes get bands**, each labelled with its convention: Cohen's d, η², Cramér's V, r, odds ratio, Cronbach's α (≥ .7 "acceptable" per Nunnally), KMO.
- **Matrix results** (vector & matrix theme) get interpretation per declared output key, e.g. PCA `explained_variance` or Mahalanobis `p`.

Interpretation feeds `Explain` (03), the "Reading your results" docs (02) and the skill section (P5).

---

## P4. Glossary registry (Committed)

`descriptor/glossary.go` is a flat list of terms:

```go
Term{ID: "p-value",
     Short: "The probability of seeing a difference at least this large if there were truly no difference.",
     WhyCare: "Small values (below your alpha, usually 0.05) suggest the difference is not just noise. It says nothing about how BIG the difference is.",
     SeeAlso: []string{"effect-size", "alpha", "multiple-comparisons"}}
```

- The starter set is about 60 terms: p-value, alpha, effect size, confidence interval, degrees of freedom, variance, standard deviation, covariance, correlation, z-score, percentile, outlier, normal distribution, skew, null hypothesis, weighting, listwise / pairwise deletion, eigenvalue, principal component, loading, factor, reliability, centroid, distance, similarity, stochastic matrix, steady state, raking, overfitting, multicollinearity, residual, R², and so on.
- Each term carries a `jargon` flag. When a jargon term appears in a `Plain` sentence it must be glossary-linked; the gate checks this.
- Projected to: a docs glossary page, `pulse-skill://glossary` (an MCP resource, one fetch), and `glossary` entries in Explain output.

---

## P5. Skill pack integration (Committed)

- New required section `## Use when` on every `op-*` skill. It is rendered from `Purpose`: the plain line, two questions, the not-for list, about 400 chars.
- New required section `## Reading the output` on `op-test-*`, `op-reg-*`, `op-mat-*` and inferential `op-overlay-*`, rendered from `Interpretation`.
- **Rendered, then checked.** `go generate ./skills/...` writes the sections between `<!-- purpose:begin -->` / `<!-- purpose:end -->` markers. `TestSkillPurposeSectionsCurrent` fails if they are stale. Authors never hand-edit them, which mirrors the golden-file discipline.
- **Budget.** The rendered sections fall outside the `op-*` 1200-char body budget, but get their own 700-char cap so agents' context stays bounded. This needs a `skill-pack.md` update.
- Topical skills gain an `intent:` frontmatter key, so `pulse_skills_list {intent}` can filter.

---

## P6. Gates (Committed)

| Gate | Asserts |
|---|---|
| `TestSkillsCoverAllPurposes` | every registered built-in operator has a `Purpose` with ≥ 1 intent, ≥ 2 questions and ≥ 1 not-for |
| `TestPurposeAlternativesResolve` | every `NotFor.Use` names a registered operator |
| `TestPurposeQuestionsResolve` | every intent has ≥ 3 operators and ≥ 1 example tagged with it |
| `TestGlossaryTermsResolve` | every glossary ID referenced anywhere exists; every jargon word in a `Plain` sentence is linked |
| `TestSkillPurposeSectionsCurrent` | rendered skill sections match the metadata |
| `TestInterpretationCoversOutputs` | every inferential operator declares interpretation for `statistic` and `p_value`, and effect size where emitted |

The `TestSkillsCover*` prefix means CLAUDE.md's self-expanding gate list picks the first one up automatically, so it must be listed by name. The others either get the prefix or join the `update-demand.md` "other load-bearing gates" list.
