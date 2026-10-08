# Matrix operators

The matrix operators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`MAT_CORRELATION`](#op-mat_correlation) | How closely every pair in a set of numeric fields follows a straight line together, as one square table of r values from -1 to 1. | Which of these ten rating items go together most strongly? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value or confidence interval for one pair.<br>[`MAT_COVARIANCE`](#op-mat_covariance) when you want the joint spread in the fields' own units.<br>[`TEST_SPEARMAN_R`](test.md#op-test_spearman_r) when the links are consistent but curved, or the values are ranks. |
| [`MAT_COVARIANCE`](#op-mat_covariance) | How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal. | How do these five rating scales vary together across respondents? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1.<br>[`AGG_WELFORD`](aggregator.md#op-agg_welford) when you want the spread of one field on its own. |

## Operators

<a id="op-mat_correlation"></a>

### `MAT_CORRELATION`

How closely every pair in a set of numeric fields follows a straight line together, as one square table of r values from -1 to 1.

**Level:** intermediate

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

**Use something else:**

- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value or confidence interval for one pair.
- [`MAT_COVARIANCE`](#op-mat_covariance) when you want the joint spread in the fields' own units.
- [`TEST_SPEARMAN_R`](test.md#op-test_spearman_r) when the links are consistent but curved, or the values are ranks.

**Glossary:** [`correlation`](../glossary.md#term-correlation), [`covariance`](../glossary.md#term-covariance), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`outlier`](../glossary.md#term-outlier)

**Skill:** [`op-mat-correlation`](../skills/op-mat-correlation.md)

<a id="op-mat_covariance"></a>

### `MAT_COVARIANCE`

How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal.

**Level:** intermediate

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
