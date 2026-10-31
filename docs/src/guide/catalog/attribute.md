# Attributes

The attributes this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`ATTR_CODE_IN`](#op-attr_code_in) | Adds a 1 / 0 column saying whether each row's code is one of a listed set, keeping every row in the base. | What share of all respondents gave a top-two-box answer (codes 4 or 5)? | basic | [`FILTER_INCLUDE`](filterer.md#op-filter_include) when you want to keep only the rows with those codes.<br>[`AGG_FREQUENCY`](aggregator.md#op-agg_frequency) when you want how often a single value occurs.<br>[`ATTR_SET_HAS`](#op-attr_set_has) when the field is a multi-select. |
| [`ATTR_DATE_PART`](#op-attr_date_part) | Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour. | Which month of the year does each order fall in, so seasons can be compared across years? | basic | [`GROUP_DATE`](grouper.md#op-group_date) when you only want rows grouped by day, week, month or year.<br>[`FEAT_DATE_FEATURES`](feature.md#op-feat_date_features) when you want several calendar columns at once, including weekday. |
| [`ATTR_FACTOR_SCORE`](#op-attr_factor_score) | Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together. | What is each respondent's score on the satisfaction factor behind these rating questions? | advanced | [`MAT_FACTOR`](matrix.md#op-mat_factor) when you want how many factors there are and which fields load on each.<br>[`ATTR_PC_SCORE`](#op-attr_pc_score) when you want a summary of all the fields' variance rather than a common trait. |
| [`ATTR_FORMULA`](#op-attr_formula) | Adds a number to every row computed from the row's own fields with an expression, such as price * qty. | What is each order's line total from its price and quantity? | intermediate | [`FILTER_EXPRESSION`](filterer.md#op-filter_expression) when you want to keep or drop rows by a rule rather than add a column.<br>[`FEAT_LOG`](feature.md#op-feat_log) when you want the logarithm of a field. |
| [`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) | Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together. | Which respondents gave an unusual pattern of answers across the whole battery, even if no single answer is extreme? | advanced | [`ATTR_ZSCORE`](#op-attr_zscore) when only one field matters.<br>[`TEST_HOTELLING_T2`](test.md#op-test_hotelling_t2) when you want how far apart groups are, not single rows. |
| [`ATTR_NORMALIZED`](#op-attr_normalized) | Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest. | How can fields on very different scales be put on one 0-to-1 scale before combining them? | basic | [`ATTR_PERCENTILE`](#op-attr_percentile) when the field has extreme values that would squeeze the rest of the range.<br>[`ATTR_ZSCORE`](#op-attr_zscore) when you want distance from the mean in standard deviations.<br>[`ATTR_FORMULA`](#op-attr_formula) when you want a composite on the scales' fixed endpoints (1-5, 0-10), such as (x - 1) / 4. |
| [`ATTR_PC_SCORE`](#op-attr_pc_score) | Adds to every row its score on one principal component of several related numeric fields: one number standing in for what they share. | What is each respondent's score on the main dimension behind these rating questions? | advanced | [`MAT_PCA`](matrix.md#op-mat_pca) when you want how many dimensions there are and which fields load on each.<br>[`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when you want how unusual a row is across the fields. |
| [`ATTR_PERCENTILE`](#op-attr_percentile) | Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. | Where does each store's revenue rank among all stores, as a percentage? | basic | [`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the value at a chosen percentile, such as the 90th.<br>[`WIN_RANK`](window.md#op-win_rank) when you want ranks within partitions, or tied values to share a rank.<br>[`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands. |
| [`ATTR_PREVIOUS_STATE`](#op-attr_previous_state) | Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state. | How many customers moved from each plan to each other plan between snapshots, when the data has one row per customer per snapshot? | intermediate | [`GROUP_CATEGORY`](grouper.md#op-group_category) when the data already holds one row per subject with a from column and a to column.<br>[`WIN_LAG`](window.md#op-win_lag) when you want the previous value of a number on result rows. |
| [`ATTR_PROFILE_SIMILARITY`](#op-attr_profile_similarity) | Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target. | How closely does each respondent's brand ratings follow the ideal loyalist profile? | intermediate | [`ATTR_VEC_DISTANCE`](#op-attr_vec_distance) when you want how far apart the values are rather than how alike the pattern is.<br>[`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when you want how unusual a row is against the whole cohort.<br>[`MAT_DISTANCE`](matrix.md#op-mat_distance) when you want how far apart whole groups are. |
| [`ATTR_REG_FITTED`](#op-attr_reg_fitted) | Adds to every row the value a straight-line model predicts for its target from its predictors. | What order value would we expect for each customer given their visits and tenure? | advanced | [`REG_OLS`](regression.md#op-reg_ols) when you want the coefficients, R-squared and p-values of the model.<br>[`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want how far each row sits from its prediction.<br>[`REG_GLM`](regression.md#op-reg_glm) when the target is yes/no or a count. |
| [`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) | Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit. | Which rows have predictor values so far from the rest that they could steer the model? | advanced | [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want rows the model predicts badly.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want the model itself. |
| [`ATTR_REG_PREDICT`](#op-attr_reg_predict) | Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values. | How close are the model's predictions on weeks it was not fitted to (a holdout)? | advanced | [`ATTR_REG_FITTED`](#op-attr_reg_fitted) when the model should be fitted to these same rows inside the slot.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want the coefficients, their standard errors and p-values. |
| [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) | Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. | Which stores sell far more or less than their size and footfall predict? | advanced | [`REG_OLS`](regression.md#op-reg_ols) when you want the model's summary (coefficients, R-squared).<br>[`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) when you want how unusual each row's predictor values are.<br>[`ATTR_ZSCORE`](#op-attr_zscore) when you want how far a value is from the mean, with no model. |
| [`ATTR_SCALE_SCORE`](#op-attr_scale_score) | Adds to every row its score on a rating battery: the average of its answered items or a prorated total, reverse-worded items flipped. | What is each respondent's satisfaction score across the five satisfaction items, counting only people who answered at least four? | intermediate | [`MAT_RELIABILITY`](matrix.md#op-mat_reliability) when you want how consistently the items measure one thing.<br>[`ATTR_FACTOR_SCORE`](#op-attr_factor_score) when the items should be weighted by how much each reflects a common trait.<br>[`ATTR_FORMULA`](#op-attr_formula) when you want an arbitrary formula of the items. |
| [`ATTR_SEM_SCORE`](#op-attr_sem_score) | Adds to every row its score on one hidden quality of a structural equation model you write, estimated from the measures it drives. | What is each respondent's perceived-value score under our brand model, to crosstab it by segment? | advanced | [`ATTR_FACTOR_SCORE`](#op-attr_factor_score) when you do not yet know which measures belong to which quality.<br>[`MAT_SEM`](matrix.md#op-mat_sem) when you want the model's fit, paths and latent means rather than per-row scores.<br>[`ATTR_SCALE_SCORE`](#op-attr_scale_score) when every item counts equally and you want a plain battery average. |
| [`ATTR_SET_HAS`](#op-attr_set_has) | Adds a 1 / 0 column saying whether each row's multi-select field includes one named option. | Which respondents ticked 'price' among their reasons, as a column to cross with other answers? | basic | [`FILTER_SET_CONTAINS_ANY`](filterer.md#op-filter_set_contains_any) when you want to keep only rows that include any of several options.<br>[`AGG_SET_FREQUENCY`](aggregator.md#op-agg_set_frequency) when you want how often each option was chosen. |
| [`ATTR_SET_POPCOUNT`](#op-attr_set_popcount) | Adds to every row the number of options its multi-select field has selected. | How many reasons did each respondent tick? | basic | [`AGG_SET_CARDINALITY_AVG`](aggregator.md#op-agg_set_cardinality_avg) when you want the average number of selections per group.<br>[`ATTR_SET_HAS`](#op-attr_set_has) when you want whether one particular option was chosen. |
| [`ATTR_TSCORE`](#op-attr_tscore) | Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10. | How does each candidate's test result compare with the group, on a 50-centred scale? | intermediate | [`ATTR_ZSCORE`](#op-attr_zscore) when you want plain standard-deviation units centred on 0.<br>[`ATTR_PERCENTILE`](#op-attr_percentile) when you want each row's percentile rank (the share at or below it when values are untied). |
| [`ATTR_VEC_DISTANCE`](#op-attr_vec_distance) | Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread. | How far is each store's KPI profile from the benchmark store, with every KPI on its own scale? | intermediate | [`ATTR_PROFILE_SIMILARITY`](#op-attr_profile_similarity) when you want how alike the pattern is rather than how far apart the values are.<br>[`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when the target is the cohort's own centre.<br>[`MAT_DISTANCE`](matrix.md#op-mat_distance) when you want how far apart whole groups are. |
| [`ATTR_ZSCORE`](#op-attr_zscore) | Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean. | Which orders are unusually large compared with all orders? | intermediate | [`AGG_ZSCORE`](aggregator.md#op-agg_zscore) when you want the centre and spread of each group, not a score per row.<br>[`ATTR_PERCENTILE`](#op-attr_percentile) when the field is skewed or has extreme values and you want a position they cannot distort.<br>[`OVERLAY_ZSCORE_VS_TOTAL`](overlay.md#op-overlay_zscore_vs_total) when you want each group's value against all groups. |

## Operators

<a id="op-attr_code_in"></a>

### `ATTR_CODE_IN`

Adds a 1 / 0 column saying whether each row's code is one of a listed set, keeping every row in the base.

**Level:** basic

**Questions it answers:**

- What share of all respondents gave a top-two-box answer (codes 4 or 5)?
- Which orders carry one of the priority status codes, as a column to average by region?

**Use cases by domain:**

- *survey:* Top-box flag to average as a share of the whole weighted base: non-answers stay in the denominator unless FILTER_NULL drops them first.
- *ops:* Flag for orders whose status code is in a chosen group.

**Assumptions:**

- On a categorical field a code is a dictionary label; one absent from the dictionary matches nothing.
- A row with a missing value reads 0, the same as a row with another code.

**Use something else:**

- [`FILTER_INCLUDE`](filterer.md#op-filter_include) when you want to keep only the rows with those codes.
- [`AGG_FREQUENCY`](aggregator.md#op-agg_frequency) when you want how often a single value occurs.
- [`ATTR_SET_HAS`](#op-attr_set_has) when the field is a multi-select.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-code-in`](../skills/op-attr-code-in.md)

<a id="op-attr_date_part"></a>

### `ATTR_DATE_PART`

Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour.

**Level:** basic

**Questions it answers:**

- Which month of the year does each order fall in, so seasons can be compared across years?
- What year-month label should each response carry for a monthly breakdown?

**Use cases by domain:**

- *survey:* Tag each response with its fieldwork year-month.
- *ops:* Month-of-year column for comparing seasonal order volume.
- *science:* Collection year per sample for a by-year breakdown.

**Assumptions:**

- Reads a date field (epoch days) or a datetime field; a datetime is read on the local clock of its time zone, and the hour part needs a datetime.
- The part is an encoded number (YYYYMM for year_month), so arithmetic on it is meaningless.
- A missing date gives a missing part, never 0.

**Use something else:**

- [`GROUP_DATE`](grouper.md#op-group_date) when you only want rows grouped by day, week, month or year.
- [`FEAT_DATE_FEATURES`](feature.md#op-feat_date_features) when you want several calendar columns at once, including weekday.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-date-part`](../skills/op-attr-date-part.md)

<a id="op-attr_factor_score"></a>

### `ATTR_FACTOR_SCORE`

Adds to every row its score on one factor behind several related numeric fields: an estimate of the hidden trait they measure together.

**Level:** advanced

**Also known as:** `factor score`, `regression factor score`, `Thurstone score`, `Bartlett score`, `latent score`

**Questions it answers:**

- What is each respondent's score on the satisfaction factor behind these rating questions?
- Can I score every row on the two constructs a factor analysis found, to compare segments on them?

**Use cases by domain:**

- *survey:* Score each respondent on the constructs an attitude battery measures, then crosstab or test the scores by segment.
- *science:* Estimate each subject's standing on a latent trait from several noisy indicators before a later model.

**Assumptions:**

- The factors come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.
- The fit is MAT_FACTOR's with the same settings, so factor numbers and directions match its loadings: read them before naming a score.
- A row missing any of the fields is left out of the fit and gets no score (listwise deletion).
- There must be more complete rows than fields, the factor count must be identified, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.

**Use something else:**

- [`MAT_FACTOR`](matrix.md#op-mat_factor) when you want how many factors there are and which fields load on each.
- [`ATTR_PC_SCORE`](#op-attr_pc_score) when you want a summary of all the fields' variance rather than a common trait.

**Glossary:** [`factor`](../glossary.md#term-factor), [`loading`](../glossary.md#term-loading), [`correlation`](../glossary.md#term-correlation), [`z-score`](../glossary.md#term-z-score), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-factor-score`](../skills/op-attr-factor-score.md)

<a id="op-attr_formula"></a>

### `ATTR_FORMULA`

Adds a number to every row computed from the row's own fields with an expression, such as price * qty.

**Level:** intermediate

**Questions it answers:**

- What is each order's line total from its price and quantity?
- Which rows meet a combined rule, as a 0/1 column for counting?

**Use cases by domain:**

- *survey:* Sum of several rating items into one score per respondent.
- *ops:* Margin per order from revenue and cost.
- *science:* Body-mass index from recorded weight and height.

**Assumptions:**

- A missing field enters the expression as nil, and an operator that cannot take nil fails the request. A guard such as x ?? 0 turns each missing value into a real 0, which pulls averages down, so prefer dropping those rows with FILTER_NULL or pick a fallback that means something.
- True / false results become 1 / 0.
- It cannot read another attribute's column from the same request.

**Use something else:**

- [`FILTER_EXPRESSION`](filterer.md#op-filter_expression) when you want to keep or drop rows by a rule rather than add a column.
- [`FEAT_LOG`](feature.md#op-feat_log) when you want the logarithm of a field.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-formula`](../skills/op-attr-formula.md)

<a id="op-attr_mahalanobis"></a>

### `ATTR_MAHALANOBIS`

Adds to every row how unusual its mix of several numeric fields is, allowing for how those fields usually vary together.

**Level:** advanced

**Also known as:** `mahalanobis distance`, `multivariate outlier score`, `d-squared`

**Questions it answers:**

- Which respondents gave an unusual pattern of answers across the whole battery, even if no single answer is extreme?
- Which transactions have an odd combination of amount, frequency and basket size?

**Use cases by domain:**

- *survey:* Flag straight-liners and odd answer patterns across a rating battery before analysis.
- *ops:* Screen orders whose size, discount and delivery time together look unlike the rest.
- *science:* Find samples whose set of measurements is unusual as a whole.

**Assumptions:**

- The typical row and the spread come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.
- A row missing any of the fields is left out of the fit and gets no score (listwise deletion).
- The p output reads the score against a chi-square curve, which holds only when the fields are roughly jointly normal.
- There must be more complete rows than fields, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.

**Use something else:**

- [`ATTR_ZSCORE`](#op-attr_zscore) when only one field matters.
- [`TEST_HOTELLING_T2`](test.md#op-test_hotelling_t2) when you want how far apart groups are, not single rows.

**Glossary:** [`distance`](../glossary.md#term-distance), [`outlier`](../glossary.md#term-outlier), [`covariance`](../glossary.md#term-covariance), [`mean`](../glossary.md#term-mean), [`listwise-deletion`](../glossary.md#term-listwise-deletion), [`chi-square`](../glossary.md#term-chi-square), [`p-value`](../glossary.md#term-p-value), [`normal-distribution`](../glossary.md#term-normal-distribution), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom)

**Skill:** [`op-attr-mahalanobis`](../skills/op-attr-mahalanobis.md)

<a id="op-attr_normalized"></a>

### `ATTR_NORMALIZED`

Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest.

**Level:** basic

**Questions it answers:**

- How can fields on very different scales be put on one 0-to-1 scale before combining them?
- Where does each row's value sit between the lowest and highest seen?

**Use cases by domain:**

- *survey:* Rescale items to 0-1 by their observed lowest and highest answers, before comparing their spread.
- *ops:* Scale each site's load between its observed minimum and maximum.
- *harness:* Bound an input to 0..1 before handing it to a scoring model.

**Assumptions:**

- Computed over every row that passed the filters, before grouping: a group never re-centres it.
- It uses the OBSERVED lowest and highest values, not a scale's endpoints: if nobody answered 1 on a 1-5 item, 2 maps to 0, so the same raw score lands at different positions across items, filtered subsets or waves.
- A missing value gets no value (null) and is left out of the minimum and maximum; every row gets no value (null) when all values are equal.

**Use something else:**

- [`ATTR_PERCENTILE`](#op-attr_percentile) when the field has extreme values that would squeeze the rest of the range.
- [`ATTR_ZSCORE`](#op-attr_zscore) when you want distance from the mean in standard deviations.
- [`ATTR_FORMULA`](#op-attr_formula) when you want a composite on the scales' fixed endpoints (1-5, 0-10), such as (x - 1) / 4.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-normalized`](../skills/op-attr-normalized.md)

<a id="op-attr_pc_score"></a>

### `ATTR_PC_SCORE`

Adds to every row its score on one principal component of several related numeric fields: one number standing in for what they share.

**Level:** advanced

**Also known as:** `principal component score`, `pc score`, `component score`, `index score`

**Questions it answers:**

- What is each respondent's score on the main dimension behind these rating questions?
- Can I replace these five correlated measures with one summary column for a later model?

**Use cases by domain:**

- *survey:* Score each respondent on the leading dimension of an attitude battery, to segment or chart by it.
- *science:* Reduce many correlated measurements of each specimen to a few component columns before further analysis.

**Assumptions:**

- The components come from one fit over every row that passed the filters (or the subset fit_on selects), never per group; every filtered row is scored.
- A component's direction (which end is high) follows MAT_PCA's rule, not the meaning of the fields: read the loadings before naming a score.
- A row missing any of the fields is left out of the fit and gets no score (listwise deletion).
- There must be more complete rows than fields, and no field may be constant or a sum of the others; otherwise every score is empty, with a warning.

**Use something else:**

- [`MAT_PCA`](matrix.md#op-mat_pca) when you want how many dimensions there are and which fields load on each.
- [`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when you want how unusual a row is across the fields.

**Glossary:** [`principal-component`](../glossary.md#term-principal-component), [`eigenvalue`](../glossary.md#term-eigenvalue), [`loading`](../glossary.md#term-loading), [`correlation`](../glossary.md#term-correlation), [`covariance`](../glossary.md#term-covariance), [`z-score`](../glossary.md#term-z-score), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-pc-score`](../skills/op-attr-pc-score.md)

<a id="op-attr_percentile"></a>

### `ATTR_PERCENTILE`

Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied.

**Level:** basic

**Questions it answers:**

- Where does each store's revenue rank among all stores, as a percentage?
- Which respondents are in the top tenth for spend?

**Use cases by domain:**

- *survey:* Percentile of each respondent's household income.
- *ops:* Percentile rank of each order's delivery time.
- *science:* Percentile of each sample's reading within the batch.

**Assumptions:**

- Computed over every row that passed the filters, before grouping: a group never re-centres it.
- Tied values do not share a percentile: each tied row takes its own rank in scan order (the earlier record lower), so equal values can read tens of points apart.
- Rows tied at a cut-off are split across it by record order, not by anything in the data; cut on the value (AGG_PERCENTILE, then a filter) for a top tenth that ignores row order.
- Not streamable: every value is sorted before the first row is ready.
- A missing value gets no percentile (null) and is not counted in n.

**Use something else:**

- [`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the value at a chosen percentile, such as the 90th.
- [`WIN_RANK`](window.md#op-win_rank) when you want ranks within partitions, or tied values to share a rank.
- [`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands.

**Glossary:** [`percentile`](../glossary.md#term-percentile), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-percentile`](../skills/op-attr-percentile.md)

<a id="op-attr_previous_state"></a>

### `ATTR_PREVIOUS_STATE`

Adds to each row of a long panel the state the same subject held at its previous date, so moves can be counted from-state by to-state.

**Level:** intermediate

**Also known as:** `previous state`, `lagged state`

**Questions it answers:**

- How many customers moved from each plan to each other plan between snapshots, when the data has one row per customer per snapshot?
- Which tier was each member in at the previous monthly snapshot?

**Use cases by domain:**

- *survey:* Pair each respondent's answer with their answer in the previous wave of a panel.
- *ops:* Turn a plan-history log into a last-plan by current-plan table for switching and churn analysis.
- *science:* Pair each visit's disease stage with the stage at the same patient's previous visit.

**Assumptions:**

- Each subject's rows are put in order by a date or date-time, never by row order; rows on the same date each read the date before it.
- A subject's first date has no previous state, and neither does a row after a missing state: both are missing, never a guessed earlier state, so they drop out of a from-state by to-state table.
- When rows on one date disagree on the state, the next date has no single previous state: it is missing, and a warning counts it.
- It reads each subject's whole history across the cohort, and no weight applies: it is a value copied from another row, not an estimate.

**Use something else:**

- [`GROUP_CATEGORY`](grouper.md#op-group_category) when the data already holds one row per subject with a from column and a to column.
- [`WIN_LAG`](window.md#op-win_lag) when you want the previous value of a number on result rows.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-previous-state`](../skills/op-attr-previous-state.md)

<a id="op-attr_profile_similarity"></a>

### `ATTR_PROFILE_SIMILARITY`

Adds to every row how closely its related fields match a target profile: an ideal pattern, a benchmark, or its own segment target.

**Level:** intermediate

**Also known as:** `profile similarity`, `profile match`, `pattern similarity`, `fit to target`

**Questions it answers:**

- How closely does each respondent's brand ratings follow the ideal loyalist profile?
- How similar is each store's channel mix to its region's target mix?

**Use cases by domain:**

- *survey:* Score each respondent's fit to an ideal rating profile, the pattern rather than the level (correlation).
- *ops:* Compare each account's spend mix with the target mix for its segment, kept in a registered reference table.

**Assumptions:**

- The target is matched to the fields by label, never by position; a target whose labels differ is refused before any row is read.
- Each row is compared from its own values only; a row weight does not apply.
- A field missing on a row drops that pair; a row with fewer complete pairs than min_valid gets no value.
- A row whose segment has no target profile gets no value and is counted, rather than refusing the run.

**Use something else:**

- [`ATTR_VEC_DISTANCE`](#op-attr_vec_distance) when you want how far apart the values are rather than how alike the pattern is.
- [`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when you want how unusual a row is against the whole cohort.
- [`MAT_DISTANCE`](matrix.md#op-mat_distance) when you want how far apart whole groups are.

**Glossary:** [`similarity`](../glossary.md#term-similarity), [`correlation`](../glossary.md#term-correlation), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-attr-profile-similarity`](../skills/op-attr-profile-similarity.md)

<a id="op-attr_reg_fitted"></a>

### `ATTR_REG_FITTED`

Adds to every row the value a straight-line model predicts for its target from its predictors.

**Level:** advanced

**Questions it answers:**

- What order value would we expect for each customer given their visits and tenure?
- Which stores sell above or below what their size and footfall predict?

**Use cases by domain:**

- *survey:* Expected satisfaction per respondent from their attribute ratings.
- *ops:* Expected handling time per ticket from queue length and agent tenure.
- *science:* Expected response per subject from dose and body weight.

**Assumptions:**

- The model is fitted to the rows that passed the filters and have the target and every predictor (listwise deletion).
- Each ATTR_REG_* slot fits its own straight-line model; the request's regressions are not reused.
- A prediction reflects association in these rows, never what would happen if a predictor were changed.
- A row missing any predictor gets no value (null), never 0.

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want the coefficients, R-squared and p-values of the model.
- [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want how far each row sits from its prediction.
- [`REG_GLM`](regression.md#op-reg_glm) when the target is yes/no or a count.

**Glossary:** [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`overfitting`](../glossary.md#term-overfitting), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-reg-fitted`](../skills/op-attr-reg-fitted.md)

<a id="op-attr_reg_leverage"></a>

### `ATTR_REG_LEVERAGE`

Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit.

**Level:** advanced

**Questions it answers:**

- Which rows have predictor values so far from the rest that they could steer the model?
- Which customers have predictor values extreme enough that they could pull the fitted line (read their residuals to see whether they do)?

**Use cases by domain:**

- *ops:* Find the few very large accounts that could dominate a revenue model; read their residuals before concluding they do.
- *science:* Screen subjects with extreme dose or weight before trusting a fit.

**Assumptions:**

- The model is fitted to the rows that passed the filters and have the target and every predictor (listwise deletion).
- Each ATTR_REG_* slot fits its own straight-line model; the request's regressions are not reused.
- Unpenalized least squares only: any penalty is refused.
- It looks at the predictors only, never at the target.

**Use something else:**

- [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want rows the model predicts badly.
- [`REG_OLS`](regression.md#op-reg_ols) when you want the model itself.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`residual`](../glossary.md#term-residual), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-reg-leverage`](../skills/op-attr-reg-leverage.md)

<a id="op-attr_reg_predict"></a>

### `ATTR_REG_PREDICT`

Adds to every row the value a model fitted once, elsewhere in the request, predicts for it from the row's own predictor values.

**Level:** advanced

**Questions it answers:**

- How close are the model's predictions on weeks it was not fitted to (a holdout)?
- How much of each week's sales does the TV term of the fitted model account for?

**Use cases by domain:**

- *survey:* Score this wave's respondents with the satisfaction model fitted on last wave.
- *ops:* Predict handling time for new tickets from a model fitted on last quarter's.
- *science:* Predict the held-out subjects' response from the dose model fitted on the rest.

**Assumptions:**

- The model is fitted once, on the fit request's own rows, before any slot runs; scoring never refits.
- The scoring rows must carry every predictor column the fit used, built the same way (same features); an absent one-hot column is the reference level.
- A row missing any predictor the score reads gets no value (null), never 0.
- A prediction reflects association in the fitted rows, never what would happen if a predictor were changed.

**Use something else:**

- [`ATTR_REG_FITTED`](#op-attr_reg_fitted) when the model should be fitted to these same rows inside the slot.
- [`REG_OLS`](regression.md#op-reg_ols) when you want the coefficients, their standard errors and p-values.

**Glossary:** [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`overfitting`](../glossary.md#term-overfitting), [`link-function`](../glossary.md#term-link-function), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-reg-predict`](../skills/op-attr-reg-predict.md)

<a id="op-attr_reg_residual"></a>

### `ATTR_REG_RESIDUAL`

Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it.

**Level:** advanced

**Questions it answers:**

- Which stores sell far more or less than their size and footfall predict?
- Do the model's misses grow with the predicted value, or bend in a curve?

**Use cases by domain:**

- *survey:* Respondents far more or less satisfied than their ratings predict.
- *ops:* Tickets that took far longer than queue length and tenure predict.
- *science:* Check a dose-response fit for curvature or growing spread.

**Assumptions:**

- The model is fitted to the rows that passed the filters and have the target and every predictor (listwise deletion).
- Each ATTR_REG_* slot fits its own straight-line model; the request's regressions are not reused.
- A row missing the target or any predictor gets no value (null), never 0.

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want the model's summary (coefficients, R-squared).
- [`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) when you want how unusual each row's predictor values are.
- [`ATTR_ZSCORE`](#op-attr_zscore) when you want how far a value is from the mean, with no model.

**Glossary:** [`residual`](../glossary.md#term-residual), [`heteroscedasticity`](../glossary.md#term-heteroscedasticity), [`outlier`](../glossary.md#term-outlier), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-reg-residual`](../skills/op-attr-reg-residual.md)

<a id="op-attr_scale_score"></a>

### `ATTR_SCALE_SCORE`

Adds to every row its score on a rating battery: the average of its answered items or a prorated total, reverse-worded items flipped.

**Level:** intermediate

**Also known as:** `scale score`, `summated rating scale`, `composite score`

**Questions it answers:**

- What is each respondent's satisfaction score across the five satisfaction items, counting only people who answered at least four?
- What is each patient's symptom total when two of the items are worded the other way round?

**Use cases by domain:**

- *survey:* Turn an agree-disagree battery into one score per respondent, flipping the negatively worded items.
- *science:* Score a questionnaire instrument per participant, prorating the total when an item is skipped.

**Assumptions:**

- Each row is scored from its own answers only; no other row changes it, and a row weight does not apply.
- A skipped item is left out, not counted as 0: the sum is the average of the answered items times the item count.
- A row with fewer answered items than min_valid gets no score rather than a low one.
- Flipping an item needs the scale's true range, which is declared and never guessed from the data; an answer outside it is treated as skipped and reported.

**Use something else:**

- [`MAT_RELIABILITY`](matrix.md#op-mat_reliability) when you want how consistently the items measure one thing.
- [`ATTR_FACTOR_SCORE`](#op-attr_factor_score) when the items should be weighted by how much each reflects a common trait.
- [`ATTR_FORMULA`](#op-attr_formula) when you want an arbitrary formula of the items.

**Glossary:** [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value), [`reliability`](../glossary.md#term-reliability)

**Skill:** [`op-attr-scale-score`](../skills/op-attr-scale-score.md)

<a id="op-attr_sem_score"></a>

### `ATTR_SEM_SCORE`

Adds to every row its score on one hidden quality of a structural equation model you write, estimated from the measures it drives.

**Level:** advanced

**Also known as:** `sem factor score`, `latent variable score`, `lavPredict`, `sem score`

**Questions it answers:**

- What is each respondent's perceived-value score under our brand model, to crosstab it by segment?
- Can every row carry its service-quality score, with each survey wave scored by its own wave's fit, to track the shift?

**Use cases by domain:**

- *survey:* Score each respondent on the qualities a confirmed brand model measures, then compare waves or segments on the scores.
- *science:* Estimate each subject's standing on a latent construct of a fitted measurement model for a later analysis.

**Assumptions:**

- The model is MAT_SEM's with the same settings, fitted once over every row that passed the filters (or the subset fit_on selects), never per request group; every filtered row is scored.
- With a group field each row is scored with its own group's fit; a group without fitted rows gets no score, with a warning.
- With a mean structure (or a group) the scores sit on the latent-mean scale, so group averages compare; without one they are centred at the fitted rows.
- A row missing any of the measures is left out of the fit and gets no score (listwise deletion).
- A fit that fails (too few rows, a redundant measure under singular refuse, a model with more parameters than moments) leaves every score empty, with a warning.

**Use something else:**

- [`ATTR_FACTOR_SCORE`](#op-attr_factor_score) when you do not yet know which measures belong to which quality.
- [`MAT_SEM`](matrix.md#op-mat_sem) when you want the model's fit, paths and latent means rather than per-row scores.
- [`ATTR_SCALE_SCORE`](#op-attr_scale_score) when every item counts equally and you want a plain battery average.

**Glossary:** [`factor`](../glossary.md#term-factor), [`loading`](../glossary.md#term-loading), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-sem-score`](../skills/op-attr-sem-score.md)

<a id="op-attr_set_has"></a>

### `ATTR_SET_HAS`

Adds a 1 / 0 column saying whether each row's multi-select field includes one named option.

**Level:** basic

**Questions it answers:**

- Which respondents ticked 'price' among their reasons, as a column to cross with other answers?
- Which tickets carry the 'urgent' tag?

**Use cases by domain:**

- *survey:* Flag for one option of a multiple-choice question, to average as a share among those who answered: drop missing answers with FILTER_NULL first, since they read 0.
- *ops:* Flag for orders tagged with a given promotion.

**Assumptions:**

- The option must be in the field's dictionary; an unknown one is refused.
- A row with a missing value reads 0, the same as a row that did not choose the option.

**Use something else:**

- [`FILTER_SET_CONTAINS_ANY`](filterer.md#op-filter_set_contains_any) when you want to keep only rows that include any of several options.
- [`AGG_SET_FREQUENCY`](aggregator.md#op-agg_set_frequency) when you want how often each option was chosen.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-set-has`](../skills/op-attr-set-has.md)

<a id="op-attr_set_popcount"></a>

### `ATTR_SET_POPCOUNT`

Adds to every row the number of options its multi-select field has selected.

**Level:** basic

**Questions it answers:**

- How many reasons did each respondent tick?
- Which customers selected three or more interests?

**Use cases by domain:**

- *survey:* Number of brands each respondent is aware of.
- *ops:* Number of tags on each support ticket.

**Assumptions:**

- An empty selection reads 0; a row with a missing value gets no count (null), never 0.

**Use something else:**

- [`AGG_SET_CARDINALITY_AVG`](aggregator.md#op-agg_set_cardinality_avg) when you want the average number of selections per group.
- [`ATTR_SET_HAS`](#op-attr_set_has) when you want whether one particular option was chosen.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-set-popcount`](../skills/op-attr-set-popcount.md)

<a id="op-attr_tscore"></a>

### `ATTR_TSCORE`

Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10.

**Level:** intermediate

**Questions it answers:**

- How does each candidate's test result compare with the group, on a 50-centred scale?
- Which respondents score more than one standard deviation above average?

**Use cases by domain:**

- *survey:* Report standardized scale scores on a 50-centred scale, where negative values are rare.
- *science:* Express assessment scores on a mean-50, SD-10 scale relative to the rows analysed (not to a published norm group).

**Assumptions:**

- Computed over every row that passed the filters, before grouping: a group never re-centres it.
- Uses the population standard deviation (dividing by n).
- A missing value gets no score (null) and is left out of the mean and standard deviation; every row gets no score (null) when all values are equal (no spread).

**Use something else:**

- [`ATTR_ZSCORE`](#op-attr_zscore) when you want plain standard-deviation units centred on 0.
- [`ATTR_PERCENTILE`](#op-attr_percentile) when you want each row's percentile rank (the share at or below it when values are untied).

**Glossary:** [`z-score`](../glossary.md#term-z-score), [`standard-deviation`](../glossary.md#term-standard-deviation), [`mean`](../glossary.md#term-mean), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-tscore`](../skills/op-attr-tscore.md)

<a id="op-attr_vec_distance"></a>

### `ATTR_VEC_DISTANCE`

Adds to every row how far its set of related fields lies from a target profile, optionally in units of each field's spread.

**Level:** intermediate

**Also known as:** `distance to target`, `gap to benchmark`, `profile distance`, `standardized distance`

**Questions it answers:**

- How far is each store's KPI profile from the benchmark store, with every KPI on its own scale?
- Which respondents' ratings sit furthest from the target profile?

**Use cases by domain:**

- *ops:* Rank stores by their standardized gap to a benchmark KPI profile.
- *science:* Measure each specimen's distance from a reference composition.

**Assumptions:**

- The target is matched to the fields by label, never by position; a target whose labels differ is refused before any row is read.
- A field missing on a row drops that pair and the sum is scaled up for the dropped pairs; a row with fewer complete pairs than min_valid gets no value.
- The standardized and covariance-based distances learn each field's spread from one fit over the filtered rows (weighted when a weight is set), never per group; the plain ones read each row alone.
- A row whose segment has no target profile gets no value and is counted, rather than refusing the run.

**Use something else:**

- [`ATTR_PROFILE_SIMILARITY`](#op-attr_profile_similarity) when you want how alike the pattern is rather than how far apart the values are.
- [`ATTR_MAHALANOBIS`](#op-attr_mahalanobis) when the target is the cohort's own centre.
- [`MAT_DISTANCE`](matrix.md#op-mat_distance) when you want how far apart whole groups are.

**Glossary:** [`distance`](../glossary.md#term-distance), [`standard-deviation`](../glossary.md#term-standard-deviation), [`covariance`](../glossary.md#term-covariance), [`pairwise-deletion`](../glossary.md#term-pairwise-deletion)

**Skill:** [`op-attr-vec-distance`](../skills/op-attr-vec-distance.md)

<a id="op-attr_zscore"></a>

### `ATTR_ZSCORE`

Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean.

**Level:** intermediate

**Also known as:** `standard score`, `standardized score`

**Questions it answers:**

- Which orders are unusually large compared with all orders?
- How do scores measured on different scales compare once put on one footing?

**Use cases by domain:**

- *survey:* Put rating items with different spreads on a common scale before combining them.
- *ops:* Flag deliveries far slower than typical for review.
- *science:* Standardize a measurement before comparing it with another.

**Assumptions:**

- Computed over every row that passed the filters, before grouping: a group never re-centres it.
- Uses the population standard deviation (dividing by n).
- A missing value gets no score (null) and is left out of the mean and standard deviation; every row gets no score (null) when all values are equal (no spread).

**Use something else:**

- [`AGG_ZSCORE`](aggregator.md#op-agg_zscore) when you want the centre and spread of each group, not a score per row.
- [`ATTR_PERCENTILE`](#op-attr_percentile) when the field is skewed or has extreme values and you want a position they cannot distort.
- [`OVERLAY_ZSCORE_VS_TOTAL`](overlay.md#op-overlay_zscore_vs_total) when you want each group's value against all groups.

**Glossary:** [`z-score`](../glossary.md#term-z-score), [`standard-deviation`](../glossary.md#term-standard-deviation), [`mean`](../glossary.md#term-mean), [`outlier`](../glossary.md#term-outlier), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-zscore`](../skills/op-attr-zscore.md)
