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
- Set params.inference for each pair's p-value and interval (every method).

#### `auxiliary.p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `auxiliary.ci_lower`

The lower bound of each pair's 1 − alpha confidence interval for its correlation (Fisher's z, so the interval is lopsided toward 0): the range of correlations the data are consistent with. An interval that excludes 0 matches a p-value below alpha.

**Caveats:**

- Null on the diagonal and where the pair is too thin for an interval (PULSE_MATRIX_INSUFFICIENT_N, scope inference), never collapsed to r.
- Each interval covers its own pair; with many pairs some intervals miss by chance, and no multiple-comparison correction is applied.
- Under a row weight it rests on the effective sample size (sum of weights for frequency, Kish n_eff for probability), unequal weights only, not a survey design.
- Under method spearman or kendall the interval's standard error is an approximation (Bonett–Wright, Fieller) while the p-value can be exact, so near alpha the two can disagree.

#### `auxiliary.ci_upper`

The upper bound of each pair's 1 − alpha confidence interval for its correlation (Fisher's z, so the interval is lopsided toward 0): the range of correlations the data are consistent with. An interval that excludes 0 matches a p-value below alpha.

**Caveats:**

- Null on the diagonal and where the pair is too thin for an interval (PULSE_MATRIX_INSUFFICIENT_N, scope inference), never collapsed to r.
- Each interval covers its own pair; with many pairs some intervals miss by chance, and no multiple-comparison correction is applied.
- Under a row weight it rests on the effective sample size (sum of weights for frequency, Kish n_eff for probability), unequal weights only, not a survey design.
- Under method spearman or kendall the interval's standard error is an approximation (Bonett–Wright, Fieller) while the p-value can be exact, so near alpha the two can disagree.

#### `scalars.determinant`

The determinant of the correlation table: 1 when no member is linearly related to the others, falling toward 0 as members become linear combinations of one another.

**Caveats:**

- Null when the table is not positive definite, including when any cell is null.
- No published convention bands it; a value near 0 flags near-redundant members.

<a id="op-mat_factor"></a>

### `MAT_FACTOR`

Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores. See its [catalog entry](../catalog/matrix.md#op-mat_factor).

#### `primary.values`

Each cell is a loading: how strongly the row's measure is tied to the column's factor. Without a rotation, or with an orthogonal one (varimax, quartimax, geomin_orthogonal, entropy), it is the correlation between the two; under an oblique one (promax, oblimin, geomin) it is the measure's own tie to that factor with the other factors held fixed. A factor is named by the measures with the largest loadings.

**Sign:**

- `+`: the measure rises with the factor
- `-`: the measure falls as the factor rises; a factor's overall sign is a convention (each column is made to sum positive)

**Caveats:**

- Unrotated loadings put most measures on the first factor; a rotation gives a cleaner pattern without changing how well the factors fit (the communalities).
- Under an oblique rotation the factors correlate (auxiliary.factor_correlation), so loadings on correlated factors overlap.
- A uniqueness at or below 0, or held at the maximum-likelihood floor (a Heywood case, flagged with a warning), means the fit is improper; try fewer factors or drop a near-duplicate measure.

#### `scalars.kmo`

The Kaiser-Meyer-Olkin measure of sampling adequacy: how much of the measures' correlation is shared across the whole set rather than tied to single pairs, from 0 to 1. Higher means a factor analysis is more worthwhile.

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

#### `scalars.bartlett_p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `scalars.bartlett_chisq`

Bartlett's test statistic: how far the correlation table is from one in which no measure is related to any other. Large values mean the measures are related enough to factor.

**Caveats:**

- With many rows it is almost always large; read KMO for how worthwhile the analysis is.

#### `scalars.rmsr`

The root-mean-square residual: the typical size of the correlations the factors leave unexplained, on the correlation scale. Smaller means the factors reproduce the correlations more closely.

**Caveats:**

- No convention bands it here; it always shrinks as factors are added, so weigh it against how readable the extra factor is.

#### `scalars.empirical_p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `scalars.empirical_chisq`

The empirical chi-square: the sum of the squared leftover correlations times the inference size. It measures the residual size, on df degrees of freedom, and is not a likelihood test of the model.

**Caveats:**

- It rests on the inference size: the rows (listwise), the summed weight or the effective sample size under a weight, or the smallest pair under pairwise deletion.
- Null p when df is 0 or below: the model then has no spare correlations to test against.

#### `scalars.likelihood_p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `scalars.likelihood_chisq`

Maximum likelihood only: the likelihood-ratio chi-square, with Bartlett's correction, of the k-factor model against a free correlation table, on df degrees of freedom. A small p says the factors leave correlation unexplained.

**Caveats:**

- With many rows it flags even trivial misfit; read tli and rmsea beside it.
- It assumes the measures are multivariate normal and rests on the inference size (the rows, the summed weight or the effective sample size).

#### `scalars.tli`

Maximum likelihood only: the Tucker-Lewis index, how much better the factor model fits per degree of freedom than a model of unrelated measures. Near 1 or above means a close fit; it is not capped at 1.

**Caveats:**

- No convention bands it here; cut-offs such as .95 come from confirmatory models under specific conditions.
- Null when df is 0 or below.

#### `scalars.rmsea`

Maximum likelihood only: the root-mean-square error of approximation, the misfit per degree of freedom after allowing for sampling, as a point estimate. 0 means no misfit beyond chance; smaller is better.

**Caveats:**

- No convention bands it here; cut-offs such as .05 or .08 are rules of thumb; read rmsea_lower and rmsea_upper for its uncertainty.
- Null when df is 0 or below; with few rows or few df it is unstable.

#### `scalars.rmsea_lower`

Maximum likelihood only: the lower bound of the RMSEA confidence interval at conf_level (90% by default, as psych reports it). 0 when even a perfectly fitting model would often give a chi-square this large.

**Caveats:**

- It inverts the noncentral chi-square and assumes multivariate normal measures; with few rows the interval is wide.
- Null when df is 0 or below, or when no bound exists on the searched range.

#### `scalars.rmsea_upper`

Maximum likelihood only: the upper bound of the RMSEA confidence interval at conf_level (90% by default). A small upper bound says the misfit per degree of freedom is plausibly small.

**Caveats:**

- The bounds divide by the inference size minus one where the point estimate divides by the inference size itself (psych's rule), so with very few rows the point can sit outside the interval; such a bound is null.
- Null when df is 0 or below.

#### `scalars.bic`

Maximum likelihood only: the Bayesian information criterion of the fit, the likelihood chi-square minus df times the log of the inference size. Compare it across factor counts on the same measures: the lower value is preferred.

**Caveats:**

- Only differences between fits of the same measures and rows mean anything; the value alone does not.
- Null when df is 0 or below.

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
- Its p-values (params.inference) test each partial against 0 on N* − 2 − g degrees of freedom, g the held-fixed fields; read them only for the pairs you meant to test.

#### `auxiliary.p`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `auxiliary.ci_lower`

The lower bound of each pair's 1 − alpha confidence interval for its correlation (Fisher's z, so the interval is lopsided toward 0): the range of correlations the data are consistent with. An interval that excludes 0 matches a p-value below alpha.

**Caveats:**

- Null on the diagonal and where the pair is too thin for an interval (PULSE_MATRIX_INSUFFICIENT_N, scope inference), never collapsed to r.
- Each interval covers its own pair; with many pairs some intervals miss by chance, and no multiple-comparison correction is applied.
- Under a row weight it rests on the effective sample size (sum of weights for frequency, Kish n_eff for probability), unequal weights only, not a survey design.

#### `auxiliary.ci_upper`

The upper bound of each pair's 1 − alpha confidence interval for its correlation (Fisher's z, so the interval is lopsided toward 0): the range of correlations the data are consistent with. An interval that excludes 0 matches a p-value below alpha.

**Caveats:**

- Null on the diagonal and where the pair is too thin for an interval (PULSE_MATRIX_INSUFFICIENT_N, scope inference), never collapsed to r.
- Each interval covers its own pair; with many pairs some intervals miss by chance, and no multiple-comparison correction is applied.
- Under a row weight it rests on the effective sample size (sum of weights for frequency, Kish n_eff for probability), unequal weights only, not a survey design.

<a id="op-mat_pca"></a>

### `MAT_PCA`

How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many. See its [catalog entry](../catalog/matrix.md#op-mat_pca).

#### `primary.values`

Each cell is a loading: how strongly the row's measure is tied to the column's component, the correlation between the two on a correlation input. A component is named by the measures with the largest loadings; components are listed from the one that summarises the most spread down.

**Sign:**

- `+`: the measure rises with the component
- `-`: the measure falls as the component rises; a component's overall sign is a convention (its largest loading is made positive, or under a rotation its loadings sum positive)

**Caveats:**

- Unrotated, a measure loading on several components at once is common and does not mean it measures several things; params.rotation varimax redistributes the kept variance into a cleaner pattern without changing the communalities.
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

<a id="op-mat_sem"></a>

### `MAT_SEM`

Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits. See its [catalog entry](../catalog/matrix.md#op-mat_sem).

#### `primary.values`

Each cell is the covariance the fitted model implies between the row's and the column's measure, in their own units. Compared with the observed covariance (auxiliary.residual_covariance is the difference), it shows where the model reproduces the data and where it does not.

**Caveats:**

- Large residuals concentrated on a few pairs point at a missing path or covariance between those measures.

#### `auxiliary.estimates`

The parameter table, one row per semopy inspect row: lval ~ rval is a loading (an indicator on its quality) or a regression path, lval ~~ rval a variance or covariance, and with a mean structure lval ~1 an intercept (a measure) or a latent mean (a quality). estimate is in the variables' own units; std_estimate rescales it so loadings and paths compare across variables; std_err, z_value and p_value test it against zero, and on a lavaan-convention fit ci_lower / ci_upper bound its 95% Wald interval. On a multi-group fit every row is prefixed [level]; a quality's latent mean in a level other than the reference is its mean difference from the reference level, whose latent means are fixed at 0.

**Sign:**

- `+`: the left variable rises with the right one; for a latent mean, the level sits above the reference level
- `-`: the left variable falls as the right one rises; for a latent mean, the level sits below the reference level

**Caveats:**

- Each quality's first indicator is fixed to 1 to set its scale, so that row has no standard error.
- A variance at 0, or a standardized loading beyond 1, means an improper solution; simplify the model.
- Latent means compare across levels only when the loadings and intercepts are held equal (group_equal loadings, intercepts); without that the qualities need not be measured on the same scale in every level.

#### `scalars.chi2_p_value`

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `scalars.chi2`

The likelihood-ratio chi-square of the model against a free covariance (and free means, with a mean structure), on dof degrees of freedom: N times the minimised discrepancy. A small p says the model leaves covariance unexplained. On a multi-group fit it is the sum of each level's share, reported per level as chi2_group_&lt;level&gt; beside that level's n_group_&lt;level&gt;.

**Caveats:**

- With many rows it flags even trivial misfit; read cfi, tli and rmsea beside it.
- It assumes multivariate normal measures and rests on N, the rows (the summed weight under a frequency weight).
- Between two nested multi-group fits on the same rows (say equal loadings against none), the difference in chi2 on the difference in dof tests the added equality constraints.

#### `scalars.cfi`

The comparative fit index: how much better the model fits than a baseline in which no measure is related to any other, from 0 to 1. Higher is better.

**Caveats:**

- No convention bands it here; published cut-offs are rules of thumb that depend on the model's size and the data.

#### `scalars.tli`

The Tucker-Lewis index: CFI's comparison against the baseline with a penalty for every free parameter, so it can fall below 0 or rise above 1. Higher is better.

#### `scalars.rmsea`

The root mean square error of approximation: misfit per degree of freedom, adjusted for N, with 0 a perfect fit. Smaller is better.

**Caveats:**

- It is 0 whenever the chi-square falls below its degrees of freedom.

#### `scalars.aic`

semopy's AIC, 2(k - F) with k the free parameters and F the minimised discrepancy: for comparing models of the same measures, lower preferred. On a lavaan-convention fit (a mean structure or a group) it is lavaan's log-likelihood AIC, -2 loglik + 2k, instead.

#### `scalars.bic`

semopy's BIC, ln(N)k - 2F: as aic with a heavier penalty per parameter; for comparing models of the same measures, lower preferred. On a lavaan-convention fit it is lavaan's -2 loglik + k ln(N).

#### `scalars.loglik`

On a lavaan-convention fit, the model's log-likelihood (lavaan's logl), the basis of its aic and bic; on a semopy fit, semopy's minimised discrepancy F. For comparing models fitted to the same rows and measures; higher fits better.

#### `auxiliary.implied_means`

With a mean structure, the mean of each measure the fitted model implies: its intercept plus its loadings times the latent means. Compared with auxiliary.observed_means it shows where the model reproduces the measures' means and where it does not.

#### `auxiliary.observed_means`

With a mean structure, each measure's sample mean over the fitted rows (the weighted mean under a frequency weight): the means the model was fitted to.

#### `auxiliary.implied_means.*`

On a multi-group fit, one matrix per level of the group field: the means the fitted model implies within that level, read like auxiliary.implied_means.

#### `auxiliary.observed_means.*`

On a multi-group fit, one matrix per level: that level's sample means of the measures, read like auxiliary.observed_means.

#### `auxiliary.implied_covariance.*`

On a multi-group fit, one matrix per level: the covariance the fitted model implies within that level (the primary is the reference level's).

#### `auxiliary.residual_covariance.*`

On a multi-group fit, one matrix per level: that level's observed covariance minus its implied covariance, so large cells locate where the model misfits that group.
