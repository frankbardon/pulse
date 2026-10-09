# Attributes

The attributes this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`ATTR_CODE_IN`](#op-attr_code_in) | Adds a 1 / 0 column saying whether each row's code is one of a listed set, keeping every row in the base. | What share of all respondents gave a top-two-box answer (codes 4 or 5)? | basic | [`FILTER_INCLUDE`](filterer.md#op-filter_include) when you want to keep only the rows with those codes.<br>[`AGG_FREQUENCY`](aggregator.md#op-agg_frequency) when you want how often a single value occurs.<br>[`ATTR_SET_HAS`](#op-attr_set_has) when the field is a multi-select. |
| [`ATTR_DATE_PART`](#op-attr_date_part) | Adds a calendar part of a date or timestamp to every row, such as the year, the month, a year-month like 202403 or the hour. | Which month of the year does each order fall in, so seasons can be compared across years? | basic | [`GROUP_DATE`](grouper.md#op-group_date) when you only want rows grouped by day, week, month or year.<br>[`FEAT_DATE_FEATURES`](feature.md#op-feat_date_features) when you want several calendar columns at once, including weekday. |
| [`ATTR_FORMULA`](#op-attr_formula) | Adds a number to every row computed from the row's own fields with an expression, such as price * qty. | What is each order's line total from its price and quantity? | intermediate | [`FILTER_EXPRESSION`](filterer.md#op-filter_expression) when you want to keep or drop rows by a rule rather than add a column.<br>[`FEAT_LOG`](feature.md#op-feat_log) when you want the logarithm of a field. |
| [`ATTR_NORMALIZED`](#op-attr_normalized) | Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest. | How can fields on very different scales be put on one 0-to-1 scale before combining them? | basic | [`ATTR_PERCENTILE`](#op-attr_percentile) when the field has extreme values that would squeeze the rest of the range.<br>[`ATTR_ZSCORE`](#op-attr_zscore) when you want distance from the mean in standard deviations.<br>[`ATTR_FORMULA`](#op-attr_formula) when you want a composite on the scales' fixed endpoints (1-5, 0-10), such as (x - 1) / 4. |
| [`ATTR_PERCENTILE`](#op-attr_percentile) | Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. | Where does each store's revenue rank among all stores, as a percentage? | basic | [`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the value at a chosen percentile, such as the 90th.<br>[`WIN_RANK`](window.md#op-win_rank) when you want ranks within partitions, or tied values to share a rank.<br>[`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands. |
| [`ATTR_REG_FITTED`](#op-attr_reg_fitted) | Adds to every row the value a straight-line model predicts for its target from its predictors. | What order value would we expect for each customer given their visits and tenure? | advanced | [`REG_OLS`](regression.md#op-reg_ols) when you want the coefficients, R-squared and p-values of the model.<br>[`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want how far each row sits from its prediction.<br>[`REG_GLM`](regression.md#op-reg_glm) when the target is yes/no or a count. |
| [`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) | Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit. | Which rows have predictor values so far from the rest that they could steer the model? | advanced | [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) when you want rows the model predicts badly.<br>[`REG_OLS`](regression.md#op-reg_ols) when you want the model itself. |
| [`ATTR_REG_RESIDUAL`](#op-attr_reg_residual) | Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. | Which stores sell far more or less than their size and footfall predict? | advanced | [`REG_OLS`](regression.md#op-reg_ols) when you want the model's summary (coefficients, R-squared).<br>[`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) when you want how unusual each row's predictor values are.<br>[`ATTR_ZSCORE`](#op-attr_zscore) when you want how far a value is from the mean, with no model. |
| [`ATTR_SET_HAS`](#op-attr_set_has) | Adds a 1 / 0 column saying whether each row's multi-select field includes one named option. | Which respondents ticked 'price' among their reasons, as a column to cross with other answers? | basic | [`FILTER_SET_CONTAINS_ANY`](filterer.md#op-filter_set_contains_any) when you want to keep only rows that include any of several options.<br>[`AGG_SET_FREQUENCY`](aggregator.md#op-agg_set_frequency) when you want how often each option was chosen. |
| [`ATTR_SET_POPCOUNT`](#op-attr_set_popcount) | Adds to every row the number of options its multi-select field has selected. | How many reasons did each respondent tick? | basic | [`AGG_SET_CARDINALITY_AVG`](aggregator.md#op-agg_set_cardinality_avg) when you want the average number of selections per group.<br>[`ATTR_SET_HAS`](#op-attr_set_has) when you want whether one particular option was chosen. |
| [`ATTR_TSCORE`](#op-attr_tscore) | Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10. | How does each candidate's test result compare with the group, on a 50-centred scale? | intermediate | [`ATTR_ZSCORE`](#op-attr_zscore) when you want plain standard-deviation units centred on 0.<br>[`ATTR_PERCENTILE`](#op-attr_percentile) when you want each row's percentile rank (the share at or below it when values are untied). |
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
- A missing date reads 0.

**Use something else:**

- [`GROUP_DATE`](grouper.md#op-group_date) when you only want rows grouped by day, week, month or year.
- [`FEAT_DATE_FEATURES`](feature.md#op-feat_date_features) when you want several calendar columns at once, including weekday.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-date-part`](../skills/op-attr-date-part.md)

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
- A missing value reads 0, the same as the minimum, as does every row when all values are equal; in a composite that pulls the score down.

**Use something else:**

- [`ATTR_PERCENTILE`](#op-attr_percentile) when the field has extreme values that would squeeze the rest of the range.
- [`ATTR_ZSCORE`](#op-attr_zscore) when you want distance from the mean in standard deviations.
- [`ATTR_FORMULA`](#op-attr_formula) when you want a composite on the scales' fixed endpoints (1-5, 0-10), such as (x - 1) / 4.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-attr-normalized`](../skills/op-attr-normalized.md)

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
- Tied values do not share a percentile: each tied row takes its own rank in an arbitrary order, so equal values can read tens of points apart.
- Rows tied at a cut-off are split across it arbitrarily and can change between runs; cut on the value (AGG_PERCENTILE, then a filter) for a reproducible top tenth.
- Not streamable: every value is sorted before the first row is ready.

**Use something else:**

- [`AGG_PERCENTILE`](aggregator.md#op-agg_percentile) when you want the value at a chosen percentile, such as the 90th.
- [`WIN_RANK`](window.md#op-win_rank) when you want ranks within partitions, or tied values to share a rank.
- [`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want rows split into equal-sized bands.

**Glossary:** [`percentile`](../glossary.md#term-percentile), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-attr-percentile`](../skills/op-attr-percentile.md)

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
- A row missing any predictor reads 0, which is not a prediction.

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
- A row missing the target or any predictor reads 0, the same as a row the model fits exactly.

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want the model's summary (coefficients, R-squared).
- [`ATTR_REG_LEVERAGE`](#op-attr_reg_leverage) when you want how unusual each row's predictor values are.
- [`ATTR_ZSCORE`](#op-attr_zscore) when you want how far a value is from the mean, with no model.

**Glossary:** [`residual`](../glossary.md#term-residual), [`heteroscedasticity`](../glossary.md#term-heteroscedasticity), [`outlier`](../glossary.md#term-outlier), [`listwise-deletion`](../glossary.md#term-listwise-deletion)

**Skill:** [`op-attr-reg-residual`](../skills/op-attr-reg-residual.md)

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

- An empty selection reads 0, and so does a row with a missing value.

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
- A missing value reads 50, as does every row when all values are equal.

**Use something else:**

- [`ATTR_ZSCORE`](#op-attr_zscore) when you want plain standard-deviation units centred on 0.
- [`ATTR_PERCENTILE`](#op-attr_percentile) when you want each row's percentile rank (the share at or below it when values are untied).

**Glossary:** [`z-score`](../glossary.md#term-z-score), [`standard-deviation`](../glossary.md#term-standard-deviation), [`mean`](../glossary.md#term-mean)

**Skill:** [`op-attr-tscore`](../skills/op-attr-tscore.md)

<a id="op-attr_zscore"></a>

### `ATTR_ZSCORE`

Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean.

**Level:** intermediate

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
- A missing value reads 0, as does every row when all values are equal.

**Use something else:**

- [`AGG_ZSCORE`](aggregator.md#op-agg_zscore) when you want the centre and spread of each group, not a score per row.
- [`ATTR_PERCENTILE`](#op-attr_percentile) when the field is skewed or has extreme values and you want a position they cannot distort.
- [`OVERLAY_ZSCORE_VS_TOTAL`](overlay.md#op-overlay_zscore_vs_total) when you want each group's value against all groups.

**Glossary:** [`z-score`](../glossary.md#term-z-score), [`standard-deviation`](../glossary.md#term-standard-deviation), [`mean`](../glossary.md#term-mean), [`outlier`](../glossary.md#term-outlier)

**Skill:** [`op-attr-zscore`](../skills/op-attr-zscore.md)
