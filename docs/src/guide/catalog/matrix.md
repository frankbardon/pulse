# Matrix operators

The matrix operators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`MAT_CORRELATION`](#op-mat_correlation) | How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. | Which of these ten rating items go together most strongly? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value or confidence interval for one pair.<br>[`MAT_COVARIANCE`](#op-mat_covariance) when you want the joint spread in the fields' own units.<br>[`TEST_SPEARMAN_R`](test.md#op-test_spearman_r) when you need a p-value for one pair's rank correlation.<br>[`TEST_KENDALL_TAU`](test.md#op-test_kendall_tau) when you need a p-value for one pair's Kendall tau.<br>[`MAT_PARTIAL_CORRELATION`](#op-mat_partial_correlation) when you want each pair's link with other fields held fixed. |
| [`MAT_COVARIANCE`](#op-mat_covariance) | How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal. | How do these five rating scales vary together across respondents? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1.<br>[`AGG_WELFORD`](aggregator.md#op-agg_welford) when you want the spread of one field on its own. |
| [`MAT_PARTIAL_CORRELATION`](#op-mat_partial_correlation) | How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. | Does satisfaction still track price once delivery time is held fixed? | advanced | [`MAT_CORRELATION`](#op-mat_correlation) when you want each pair's link with nothing held fixed.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want how much each field moves the outcome, in its own units.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value for one pair. |

## Operators

<a id="op-mat_correlation"></a>

### `MAT_CORRELATION`

How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1.

**Level:** intermediate

**Also known as:** `correlation matrix`, `cor`, `rank correlation`, `spearman correlation matrix`, `kendall correlation matrix`

**Questions it answers:**

- Which of these ten rating items go together most strongly?
- How do the sensor readings line up with one another, pair by pair?

**Use cases by domain:**

- *survey:* Correlation table of a battery of rating items, to see which items hang together before building a scale.
- *ops:* Which weekly metrics move together across stores, in one table instead of one request per pair.
- *science:* Pairwise straight-line links among several measured responses across units.

**Assumptions:**

- A row with any member missing is dropped from every cell (listwise deletion).
- Each r reads only a straight-line link; a curved relationship can still be strong.
- A few extreme rows can move an r a lot.
- r is unit-free: each pair's co-moment divided by both members' spreads, the same arithmetic as TEST_PEARSON_R.
- The spearman and kendall methods rank each member first, so they read any steady rise or fall, curved or not, and resist extreme rows; each cell is the TEST_SPEARMAN_R or TEST_KENDALL_TAU figure for its pair.

**Use something else:**

- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value or confidence interval for one pair.
- [`MAT_COVARIANCE`](#op-mat_covariance) when you want the joint spread in the fields' own units.
- [`TEST_SPEARMAN_R`](test.md#op-test_spearman_r) when you need a p-value for one pair's rank correlation.
- [`TEST_KENDALL_TAU`](test.md#op-test_kendall_tau) when you need a p-value for one pair's Kendall tau.
- [`MAT_PARTIAL_CORRELATION`](#op-mat_partial_correlation) when you want each pair's link with other fields held fixed.

**Glossary:** [`correlation`](../glossary.md#term-correlation), [`covariance`](../glossary.md#term-covariance), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`outlier`](../glossary.md#term-outlier), [`rank`](../glossary.md#term-rank), [`spearman-rho`](../glossary.md#term-spearman-rho), [`kendall-tau`](../glossary.md#term-kendall-tau)

**Skill:** [`op-mat-correlation`](../skills/op-mat-correlation.md)

<a id="op-mat_covariance"></a>

### `MAT_COVARIANCE`

How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal.

**Level:** intermediate

**Also known as:** `covariance matrix`, `cov`

**Questions it answers:**

- How do these five rating scales vary together across respondents?
- What is the covariance table of the sensor readings, to feed a later model?

**Use cases by domain:**

- *survey:* Covariance table of a battery of rating items, the input a scale or factor analysis starts from.
- *science:* Covariance of several measured responses across units, for a multivariate summary.

**Assumptions:**

- A row with any member missing is dropped from every cell (listwise deletion).
- Sample form by default: the squared deviations are divided by n - 1 (ddof 0 divides by n).
- The figures depend on each field's units: a field in larger units gets larger entries.

**Use something else:**

- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1.
- [`AGG_WELFORD`](aggregator.md#op-agg_welford) when you want the spread of one field on its own.

**Glossary:** [`covariance`](../glossary.md#term-covariance), [`variance`](../glossary.md#term-variance), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-mat-covariance`](../skills/op-mat-covariance.md)

<a id="op-mat_partial_correlation"></a>

### `MAT_PARTIAL_CORRELATION`

How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1.

**Level:** advanced

**Also known as:** `partial r`, `controlling for`, `partial correlation matrix`, `partial correlations`

**Questions it answers:**

- Does satisfaction still track price once delivery time is held fixed?
- Which of these ratings are linked directly, rather than only through the overall score?

**Use cases by domain:**

- *survey:* Driver analysis: which rating items stay linked to the overall score once the other items are held fixed.
- *ops:* Whether two store metrics still move together once foot traffic, which drives both, is held fixed.
- *science:* Separating direct links among measured responses from links that run through a shared third measure.

**Assumptions:**

- Each pair is held fixed for the other members by default, or for exactly the fields named in params.control.
- Only straight-line links are removed and measured; a curved link to a held-fixed field leaves a trace.
- A row with any member or control missing is dropped from every cell (listwise deletion) unless pairwise deletion is chosen, which can leave the input inconsistent; that input is refused unless it is repaired.
- A field that is an exact straight-line mix of the others leaves nothing to compare, so the matrix is refused.

**Use something else:**

- [`MAT_CORRELATION`](#op-mat_correlation) when you want each pair's link with nothing held fixed.
- [`REG_OLS`](regression.md#op-reg_ols) when you want how much each field moves the outcome, in its own units.
- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value for one pair.

**Glossary:** [`partial-correlation`](../glossary.md#term-partial-correlation), [`correlation`](../glossary.md#term-correlation), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion), [`multicollinearity`](../glossary.md#term-multicollinearity)

**Skill:** [`op-mat-partial-correlation`](../skills/op-mat-partial-correlation.md)
