# Matrix operators

The matrix operators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`MAT_COLLINEARITY`](#op-mat_collinearity) | Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled. | Before I model satisfaction on these eight ratings, are any of them so related that their effects cannot be told apart? | intermediate | [`REG_OLS`](regression.md#op-reg_ols) when you want the fitted effects of the predictors on an outcome.<br>[`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which measures go together, pair by pair. |
| [`MAT_CORRELATION`](#op-mat_correlation) | How closely every pair of numeric fields moves together, in a line or in rank order, as one square table of values from -1 to 1. | Which of these ten rating items go together most strongly? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value or confidence interval for one pair.<br>[`MAT_COVARIANCE`](#op-mat_covariance) when you want the joint spread in the fields' own units.<br>[`TEST_SPEARMAN_R`](test.md#op-test_spearman_r) when you need a p-value for one pair's rank correlation.<br>[`TEST_KENDALL_TAU`](test.md#op-test_kendall_tau) when you need a p-value for one pair's Kendall tau.<br>[`MAT_PARTIAL_CORRELATION`](#op-mat_partial_correlation) when you want each pair's link with other fields held fixed. |
| [`MAT_COVARIANCE`](#op-mat_covariance) | How every pair in a set of numeric fields varies together, as one square table with each field's variance on the diagonal. | How do these five rating scales vary together across respondents? | intermediate | [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want how closely two numeric fields follow a straight line together, on a scale from -1 to 1.<br>[`AGG_WELFORD`](aggregator.md#op-agg_welford) when you want the spread of one field on its own. |
| [`MAT_DISTANCE`](#op-mat_distance) | How far apart segments are across several numbers at once: the gap between each pair of segment centres. | Which customer segments are most alike on spend, visits and tenure, and which stand furthest apart? | advanced | [`TEST_MANOVA`](test.md#op-test_manova) when you want to test whether the centres differ.<br>[`ATTR_MAHALANOBIS`](attribute.md#op-attr_mahalanobis) when you want how unusual each row is. |
| [`MAT_FACTOR`](#op-mat_factor) | Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores. | Do these twelve attitude questions reflect two or three underlying qualities, and which questions go with which? | advanced | [`MAT_PCA`](#op-mat_pca) when you want summaries of all the spread, not just what the measures share.<br>[`MAT_RELIABILITY`](#op-mat_reliability) when you already know the items measure one quality and want its reliability. |
| [`MAT_PARTIAL_CORRELATION`](#op-mat_partial_correlation) | How closely each pair of numeric fields moves together once other fields are held fixed, as one square table of values from -1 to 1. | Does satisfaction still track price once delivery time is held fixed? | advanced | [`MAT_CORRELATION`](#op-mat_correlation) when you want each pair's link with nothing held fixed.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want how much each field moves the outcome, in its own units.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you need a p-value for one pair. |
| [`MAT_PCA`](#op-mat_pca) | How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many. | How many distinct things do these twelve rating questions actually measure? | advanced | [`MAT_RELIABILITY`](#op-mat_reliability) when you want to check that one set of items measures a single quality reliably.<br>[`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which measures go together, pair by pair. |
| [`MAT_RELIABILITY`](#op-mat_reliability) | How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest. | Are these five satisfaction questions reliable enough to average into one score? | intermediate | [`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which items go together, pair by pair.<br>[`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want a p-value for one pair of items. |
| [`MAT_SEM`](#op-mat_sem) | Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits. | Do these brand-image items load on the five personality dimensions we designed them for, and does the model fit? | advanced | [`MAT_FACTOR`](#op-mat_factor) when you do not yet know which measures belong to which quality.<br>[`REG_OLS`](regression.md#op-reg_ols) when every variable is measured directly and you want one outcome's drivers.<br>[`MAT_RELIABILITY`](#op-mat_reliability) when you want one battery's reliability before scoring it. |

## Operators

<a id="op-mat_collinearity"></a>

### `MAT_COLLINEARITY`

Whether candidate predictors overlap so much that a regression on them would give unstable coefficients, and which ones are tangled.

**Level:** intermediate

**Also known as:** `vif`, `variance inflation`, `multicollinearity`, `condition index`

**Questions it answers:**

- Before I model satisfaction on these eight ratings, are any of them so related that their effects cannot be told apart?
- Which of these predictors are near-duplicates of the others?

**Use cases by domain:**

- *survey:* Screening a driver-analysis battery for questions that say almost the same thing before regressing the overall score on them.
- *science:* Checking that measured covariates are not near-linear combinations of each other before fitting a model.

**Assumptions:**

- The fields are the predictors only; the outcome plays no part, so the check is the same whatever you later model.
- It reads straight-line overlap among the predictors; it does not test any relationship with the outcome.
- A predictor that is an exact combination of others is refused, naming the fields involved.
- A row with any predictor missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want the fitted effects of the predictors on an outcome.
- [`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which measures go together, pair by pair.

**Glossary:** [`multicollinearity`](../glossary.md#term-multicollinearity), [`correlation`](../glossary.md#term-correlation), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-mat-collinearity`](../skills/op-mat-collinearity.md)

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

<a id="op-mat_distance"></a>

### `MAT_DISTANCE`

How far apart segments are across several numbers at once: the gap between each pair of segment centres.

**Level:** advanced

**Also known as:** `centroid distance`, `distance matrix`, `segment separation`, `dist`

**Questions it answers:**

- Which customer segments are most alike on spend, visits and tenure, and which stand furthest apart?
- How far apart are the regions' average profiles across the rating battery?

**Use cases by domain:**

- *survey:* Comparing the average rating profiles of segments or regions.
- *ops:* Checking whether found clusters are well apart before naming them.

**Assumptions:**

- A centre is the segment's (weighted) average of every number; a distance compares centres, not the spread inside each segment.
- Numbers in large units dominate unless standardize is on; mahalanobis also allows for how the numbers vary together.
- A row missing any number, or landing in no segment, is left out and counted.

**Use something else:**

- [`TEST_MANOVA`](test.md#op-test_manova) when you want to test whether the centres differ.
- [`ATTR_MAHALANOBIS`](attribute.md#op-attr_mahalanobis) when you want how unusual each row is.

**Follow up with:**

- [`GROUP_KMEANS`](grouper.md#op-group_kmeans) when the segments still need finding.

**Glossary:** [`centroid`](../glossary.md#term-centroid), [`distance`](../glossary.md#term-distance), [`z-score`](../glossary.md#term-z-score), [`covariance`](../glossary.md#term-covariance), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-mat-distance`](../skills/op-mat-distance.md)

<a id="op-mat_factor"></a>

### `MAT_FACTOR`

Which hidden qualities a set of related measures reflects, and how strongly each measure is tied to each, before building scale scores.

**Level:** advanced

**Also known as:** `exploratory factor analysis`, `efa`, `principal axis factoring`, `minres`, `maximum likelihood factor analysis`, `factanal`

**Questions it answers:**

- Do these twelve attitude questions reflect two or three underlying qualities, and which questions go with which?
- Which items in this battery tie weakly to every quality and could be dropped?

**Use cases by domain:**

- *survey:* Finding the themes behind a long attitude battery and which questions belong to each, before averaging them into scale scores.
- *science:* Checking whether a set of test scores reflects one general ability or several separate ones.

**Assumptions:**

- The measures are numeric and related by straight-line links; a factor is a pattern in the shared spread; it does not show that an underlying cause exists.
- You choose how many factors to keep; read the variance each accounts for and the residual figures, and try one more and one fewer.
- Read KMO and Bartlett's test first: weakly related measures give unstable factors.
- A row with any measure missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.

**Use something else:**

- [`MAT_PCA`](#op-mat_pca) when you want summaries of all the spread, not just what the measures share.
- [`MAT_RELIABILITY`](#op-mat_reliability) when you already know the items measure one quality and want its reliability.

**Follow up with:**

- [`MAT_RELIABILITY`](#op-mat_reliability) when the items of one factor are to be added up into a score.

**Glossary:** [`factor`](../glossary.md#term-factor), [`loading`](../glossary.md#term-loading), [`eigenvalue`](../glossary.md#term-eigenvalue), [`correlation`](../glossary.md#term-correlation), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-mat-factor`](../skills/op-mat-factor.md)

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

<a id="op-mat_pca"></a>

### `MAT_PCA`

How many underlying dimensions a set of related measures covers and which measures belong to each, so a few summaries can stand in for many.

**Level:** advanced

**Also known as:** `principal components`, `dimension reduction`, `kmo`, `bartlett's test`

**Questions it answers:**

- How many distinct things do these twelve rating questions actually measure?
- Is this set of measures correlated enough to be worth summarising into components?

**Use cases by domain:**

- *survey:* Checking how many themes a long attitude battery covers, and which questions load on each, before building scale scores.
- *science:* Reducing many correlated measurements of the same specimens to a few summary dimensions.

**Assumptions:**

- The measures are numeric and related by straight-line links; components summarise shared spread, they are not proven causes.
- On a covariance the measures with the largest spread dominate; use the correlation unless the units are shared and comparable.
- KMO and Bartlett's test say whether the correlations are strong enough to summarise; read them before the components.
- A row with any measure missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.

**Use something else:**

- [`MAT_RELIABILITY`](#op-mat_reliability) when you want to check that one set of items measures a single quality reliably.
- [`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which measures go together, pair by pair.

**Glossary:** [`principal-component`](../glossary.md#term-principal-component), [`eigenvalue`](../glossary.md#term-eigenvalue), [`loading`](../glossary.md#term-loading), [`correlation`](../glossary.md#term-correlation), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-mat-pca`](../skills/op-mat-pca.md)

<a id="op-mat_reliability"></a>

### `MAT_RELIABILITY`

How consistently a set of rating items measures one thing, before you add them up into a score, with a check of how each item fits the rest.

**Level:** intermediate

**Also known as:** `cronbach's alpha`, `scale reliability`, `internal consistency`, `mcdonald's omega`

**Questions it answers:**

- Are these five satisfaction questions reliable enough to average into one score?
- Which item in this battery fits the others worst and should be dropped?

**Use cases by domain:**

- *survey:* Checking a battery of agree-disagree items, some worded in reverse, before summing it into a scale score.
- *science:* Checking that several measured indicators of one trait agree well enough to be combined.

**Assumptions:**

- The items are meant to measure one shared quality; the figures do not check that there is only one.
- Items worded in reverse must be named in params.reverse with the scale's range, or they pull the figures down.
- Alpha treats every item as equally tied to the shared quality; omega lets each item's tie differ and needs at least 3 items.
- A row with any item missing is dropped from every figure (listwise deletion) unless pairwise deletion is chosen.

**Use something else:**

- [`MAT_CORRELATION`](#op-mat_correlation) when you only want to see which items go together, pair by pair.
- [`TEST_PEARSON_R`](test.md#op-test_pearson_r) when you want a p-value for one pair of items.

**Glossary:** [`reliability`](../glossary.md#term-reliability), [`correlation`](../glossary.md#term-correlation), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-mat-reliability`](../skills/op-mat-reliability.md)

<a id="op-mat_sem"></a>

### `MAT_SEM`

Whether a theory of how measures hang together holds: the hidden qualities they reflect, how those drive each other, how well it fits.

**Level:** advanced

**Also known as:** `structural equation model`, `sem`, `confirmatory factor analysis`, `cfa`, `path analysis`, `lavaan`, `semopy`, `second-order factor`, `latent variable model`, `measurement invariance`, `multi-group cfa`, `latent mean comparison`

**Questions it answers:**

- Do these brand-image items load on the five personality dimensions we designed them for, and does the model fit?
- How strongly does the overall quality we measure through these three sub-scales drive satisfaction and loyalty?
- Does the battery measure the same qualities in both survey waves, and did perceived value move between them?

**Use cases by domain:**

- *survey:* Testing a designed battery's factor structure (which items belong to which quality, and how the qualities group into higher ones) and how the qualities drive outcome questions, in one model.
- *science:* Fitting a measurement model and the paths between latent constructs, and reporting chi-square, CFI, TLI and RMSEA alongside the paths.

**Assumptions:**

- You write the model: which measures reflect which quality, which qualities drive which, and any covariances; the fit says how well that theory reproduces the measures' covariance, not whether it is the only theory that would.
- Maximum likelihood assumes roughly normal measures; with few rating levels the chi-square and standard errors are optimistic.
- With few rating levels (say five or fewer) or skewed ratings, estimator wlsmv over the ordered measures fits them as ordinal categories through polychoric correlations instead; it needs more rows than maximum likelihood and reports no log-likelihood.
- The default reproduces semopy's results exactly, including where its optimiser stops short of the best fit; choose solver fisher for the converged optimum.
- A row with any measure missing is dropped from every figure (listwise deletion).
- A mean structure or a group switches to lavaan's conventions (converged optimum, lavaan's chi-square and indices); a multi-group fit treats the groups as independent samples and compares latent means only as far as the equality constraints you set make the measures comparable.

**Use something else:**

- [`MAT_FACTOR`](#op-mat_factor) when you do not yet know which measures belong to which quality.
- [`REG_OLS`](regression.md#op-reg_ols) when every variable is measured directly and you want one outcome's drivers.
- [`MAT_RELIABILITY`](#op-mat_reliability) when you want one battery's reliability before scoring it.

**Follow up with:**

- [`MAT_FACTOR`](#op-mat_factor) when the fit is poor and the structure needs exploring.

**Glossary:** [`factor`](../glossary.md#term-factor), [`loading`](../glossary.md#term-loading), [`covariance`](../glossary.md#term-covariance), [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`chi-square`](../glossary.md#term-chi-square), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`standard-error`](../glossary.md#term-standard-error), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-mat-sem`](../skills/op-mat-sem.md)
