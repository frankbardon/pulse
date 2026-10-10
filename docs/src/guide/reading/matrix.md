# Reading matrix results

How to read the standardised results of the matrix operators this instance offers. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Matrix operators

<a id="op-mat_collinearity"></a>

### `MAT_COLLINEARITY`

Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled. See its [catalog entry](../catalog/matrix.md#op-mat_collinearity).

#### `scalars.max_vif`

The largest variance inflation factor among the predictors (each one's is also listed): how many times larger that predictor's coefficient variance is than it would be if it were unrelated to the other predictors. 1 means no overlap; tolerance, its reciprocal, is the share of the predictor's spread the others do not explain.

**Caveats:**

- No convention bands it. Cut-offs of 5 or 10 are rules of thumb only: O'Brien (2007) shows they are arbitrary, and a large sample can make a high value harmless while a small one can make a modest value matter.
- It depends only on the predictors, not on any outcome; it says how unstable coefficients would be, not whether a predictor matters.
- Under params.repair it is read off the nearest consistent correlation table, not the observed one.

#### `scalars.condition_number`

The largest of Belsley's condition indices: how close the predictors (with the intercept, unless params.center is true) come to an exact straight-line dependency. 1 means none; large values mean some combination of predictors nearly cancels out.

**Caveats:**

- No convention bands it. Belsley, Kuh and Welsch's guideline (an index above about 30 where two or more variables put a large share of their variance on that dimension) is a rule of thumb; read the variance-decomposition proportions to see which variables are involved.
- With the intercept included (the default) a predictor whose values sit far from zero relative to their spread raises it even when the predictors are unrelated; params.center true removes that part.

#### `primary.values`

The predictors' correlation table that the variance inflation factors and the centered diagnostics are read from: each off-diagonal cell is the correlation of its row and column predictors, from -1 to +1.

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- A predictor can be heavily inflated with no single large pairwise correlation, when it is close to a combination of several others; read the variance inflation factors, not just this table.

<a id="op-mat_correlation"></a>

### `MAT_CORRELATION`

How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. See its [catalog entry](../catalog/matrix.md#op-mat_correlation).

#### `primary.values`

Each off-diagonal cell is the correlation of its row and column members under params.method, from -1 to +1. pearson (default): r, how closely the two follow a straight line together; spearman: rho, r on the members' ranks, how steadily one rises or falls with the other; kendall: tau-b, the share of agreeing minus disagreeing row pairs, tie-adjusted. 0 means no such link. The diagonal is 1.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- A null cell means a member had no spread (constant, or too few rows), so its correlation is undefined, not 0.
- A Pearson r near zero rules out only a straight-line link; a curved relationship can still be strong.
- The bands are Cohen's for r; Kendall's tau-b runs smaller than r or rho for the same strength (about two thirds of rho), so read a kendall cell against lower cut-offs.
- The matrix reports no p-values; run TEST_PEARSON_R, TEST_SPEARMAN_R or TEST_KENDALL_TAU on a pair to test it.

#### `scalars.determinant`

The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.

**Caveats:**

- Null when the table is not positive definite, including when any cell is null.
- No published convention bands it; a value near 0 flags near-redundant members.

<a id="op-mat_partial_correlation"></a>

### `MAT_PARTIAL_CORRELATION`

How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. See its [catalog entry](../catalog/matrix.md#op-mat_partial_correlation).

#### `primary.values`

Each off-diagonal cell is the correlation of its row and column members once the held-fixed fields are taken out of both (every other member, or the params.control fields), from -1 to +1. 0 means no straight-line link is left after holding them fixed. The diagonal is 1.

**Bands** (convention: Cohen (1988); a labelled convention, not a rule):

| Absolute value | Label |
|---|---|
| below 0.1 | very small |
| from 0.1 to below 0.3 | small |
| from 0.3 to below 0.5 | medium |
| 0.5 and above | large |

**Sign:**

- `+`: the two fields tend to rise together
- `-`: one field tends to fall as the other rises

**Caveats:**

- Correlation is not causation: a third factor may drive both fields.
- A partial r can be much smaller than, or even opposite in sign to, the plain r when the held-fixed fields drive both members; holding fixed a field that is itself an outcome of the pair can also create a link that is not there.
- A null cell means the input had an undefined correlation (a member with no spread, or too few rows), so no cell can be computed.
- Under params.repair the cells come from the nearest consistent correlation table, not the observed one; the PULSE_MATRIX_NOT_PSD warning says how far it moved.
- The matrix reports no p-values.

<a id="op-mat_pca"></a>

### `MAT_PCA`

How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many. See its [catalog entry](../catalog/matrix.md#op-mat_pca).

#### `primary.values`

Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.

**Sign:**

- `+`: the measure rises with the component
- `-`: the measure falls as the component rises; a component's overall sign is a convention (its largest loading is made positive)

**Caveats:**

- Components are not rotated; a measure loading on several components at once is common and does not mean it measures several things.
- Components summarise shared spread; they do not show that an underlying cause exists.
- On a covariance input the loadings are in the measures' own units, so they are not comparable across measures.

#### `scalars.kmo`

The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a component summary is more worthwhile.

**Bands** (convention: Kaiser (1974); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | miserable |
| from 0.6 to below 0.7 | mediocre |
| from 0.7 to below 0.8 | middling |
| from 0.8 to below 0.9 | meritorious |
| 0.9 and above | marvelous |

**Caveats:**

- Null when the correlation table is singular (a measure is an exact combination of others).
- The per-measure version of this reading is also reported; a measure with a low value fits the set poorly.

#### `scalars.bartlett_p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `scalars.bartlett_chisq`

Bartlett's test statistic: how far the correlation table is from one in which no measure is related to any other. Large values mean the measures are related enough to summarise.

**Caveats:**

- With many rows it is almost always large; read KMO for how worthwhile a summary is.
- It rests on the inference size: the rows (listwise), the summed weight or the effective sample size under a weight, or the smallest pair under pairwise deletion.

<a id="op-mat_reliability"></a>

### `MAT_RELIABILITY`

How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest. See its [catalog entry](../catalog/matrix.md#op-mat_reliability).

#### `scalars.alpha`

Cronbach's alpha: how consistently the items measure one shared quality, from the share of the summed score's spread that the items share. 1 means the items move in lockstep; it can fall below 0 when items pull against each other, which usually means a reverse-worded item was not reversed.

**Bands** (convention: George & Mallery (2003); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | poor |
| from 0.6 to below 0.7 | questionable |
| from 0.7 to below 0.8 | acceptable |
| from 0.8 to below 0.9 | good |
| 0.9 and above | excellent |

**Caveats:**

- Alpha rises with the number of items, so a long battery can reach a high alpha with weakly related items.
- A high alpha does not show the items measure only one thing.
- Alpha assumes every item is equally tied to the shared quality; when the ties differ it understates reliability, which omega allows for.

#### `scalars.alpha_standardized`

Alpha computed on the items' correlations instead of their raw spreads: the alpha the battery would have if every item were first put on the same scale.

**Bands** (convention: George & Mallery (2003); a labelled convention, not a rule):

| Value | Label |
|---|---|
| below 0.5 | unacceptable |
| from 0.5 to below 0.6 | poor |
| from 0.6 to below 0.7 | questionable |
| from 0.7 to below 0.8 | acceptable |
| from 0.8 to below 0.9 | good |
| 0.9 and above | excellent |

**Caveats:**

- It differs from alpha when the items' spreads differ; report the one that matches how the score is built (raw sums or standardised items).

#### `scalars.omega`

McDonald's omega: the share of the summed score's spread due to the one shared quality, from a one-factor fit that lets each item's tie to it differ. Read on the same 0 to 1 scale as alpha; it is usually at least as high.

**Caveats:**

- No published convention bands omega; it is not read against the alpha bands.
- Null with a warning when the battery has 2 items, when the fit puts an item's leftover spread at or below zero (a Heywood case), or when a pairwise table is inconsistent and params.repair is not set. When the fit does not converge, omega is kept as the last iterate's value and a warning says so; treat it as provisional.
- The fit assumes one shared quality; when the items reflect several, omega from one factor misstates reliability.

#### `scalars.mean_inter_item_r`

The average correlation among the items, the strength of the typical pair; standardized alpha is built from it and the item count.

**Caveats:**

- An average hides spread: one item unrelated to the rest pulls it down; check the table and item_total_r.
- Like any correlation it shows the items move together, not that one causes another.
