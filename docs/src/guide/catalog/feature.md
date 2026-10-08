# Feature operators

The feature operators this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`FEAT_BUCKETIZE`](#op-feat_bucketize) | Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number. | Which income band is each customer in? | basic | [`GROUP_RANGE`](grouper.md#op-group_range) when you want results grouped by fixed-width bands.<br>[`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want results grouped by equal-count bins. |
| [`FEAT_DATE_FEATURES`](#op-feat_date_features) | Splits a date into year, month, day, day of week and quarter columns, so each part can be filtered, grouped or modelled. | Do orders differ by day of the week? | basic | [`ATTR_DATE_PART`](attribute.md#op-attr_date_part) when you need just one part of the date.<br>[`GROUP_DATE`](grouper.md#op-group_date) when you want results grouped by day, month or year. |
| [`FEAT_FREQUENCY_ENCODE`](#op-feat_frequency_encode) | Replaces each category with the share of records that carry it, one number that says how common the category is. | How common is each customer's city, as a single number a model can use? | intermediate | [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want the count table itself, one row per category: group by the field, then count.<br>[`FEAT_ONE_HOT`](#op-feat_one_hot) when the field has few categories and each should be its own column. |
| [`FEAT_LOG`](#op-feat_log) | Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model. | Can I tame the long tail of customer spend before fitting a model on it? | intermediate | [`FEAT_SQRT`](#op-feat_sqrt) when the tail is only moderate and a gentler squeeze is enough.<br>[`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want each value in standard units from the mean, not a squeezed scale.<br>[`FEAT_BUCKETIZE`](#op-feat_bucketize) when you want ordered bins instead of a continuous transform. |
| [`FEAT_ONE_HOT`](#op-feat_one_hot) | Turns a category field into one 0/1 column per category, so a model or a sum can treat each category as its own yes/no. | How do I feed region into a regression as separate yes/no predictors? | basic | [`FEAT_FREQUENCY_ENCODE`](#op-feat_frequency_encode) when the field has hundreds of categories and one column each is too many.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want how many rows fall in each category: group by the field, then count. |
| [`FEAT_POLY`](#op-feat_poly) | Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve. | Does satisfaction rise with tenure and then level off? | advanced | [`REG_OLS`](regression.md#op-reg_ols) when you want to fit the curve itself, not just build its columns.<br>[`ATTR_FORMULA`](attribute.md#op-attr_formula) when you want one custom transform of the value. |
| [`FEAT_SQRT`](#op-feat_sqrt) | Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail. | Can I soften the few very large complaint counts without a full log? | intermediate | [`FEAT_LOG`](#op-feat_log) when the tail is very long and needs a stronger squeeze.<br>[`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want each value in standard units from the mean. |
| [`FEAT_TARGET_ENCODE`](#op-feat_target_encode) | Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average. | How do I give a model a single number for each of a thousand postcodes? | advanced | [`AGG_AVERAGE`](aggregator.md#op-agg_average) when you want to report each category's average outcome.<br>[`FEAT_ONE_HOT`](#op-feat_one_hot) when the field has few categories and each should be its own column.<br>[`FEAT_FREQUENCY_ENCODE`](#op-feat_frequency_encode) when you want how common each category is, without using the outcome. |
| [`FEAT_TRAIN_TEST_SPLIT`](#op-feat_train_test_split) | Labels every record train (0), validation (1) or test (2) by a seeded shuffle, so a model can be checked on records it never learned from. | How do I hold back 20% of records to test a model on? | intermediate | `capability:sample` when you just want a random subset of rows to look at.<br>[`GROUP_CATEGORY`](grouper.md#op-group_category) when you want to compare groups that already exist. |

## Operators

<a id="op-feat_bucketize"></a>

### `FEAT_BUCKETIZE`

Puts each value into an ordered bin, using cut points you give or bins holding about equal numbers of records, and adds the bin number.

**Level:** basic

**Questions it answers:**

- Which income band is each customer in?
- Which spend decile (tenth of orders ranked by spend) does each order fall in?

**Use cases by domain:**

- *survey:* Age bands for each respondent, from fixed cut points.
- *ops:* Order value tenths, as a column later steps can filter on.
- *science:* Exposure bands for each subject, for a banded analysis.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Bin 0 is the lowest; a value equal to a cut point goes in the bin below it.
- Equal-count bins use cut points from every record of the cohort, and repeated values can leave bins uneven. A missing input value gives a missing output.

**Use something else:**

- [`GROUP_RANGE`](grouper.md#op-group_range) when you want results grouped by fixed-width bands.
- [`GROUP_QUANTILE`](grouper.md#op-group_quantile) when you want results grouped by equal-count bins.

**Glossary:** [`percentile`](../glossary.md#term-percentile), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-bucketize`](../skills/op-feat-bucketize.md)

<a id="op-feat_date_features"></a>

### `FEAT_DATE_FEATURES`

Splits a date into year, month, day, day of week and quarter columns, so each part can be filtered, grouped or modelled.

**Level:** basic

**Questions it answers:**

- Do orders differ by day of the week?
- Is there a seasonal pattern by month across several years?

**Use cases by domain:**

- *survey:* Quarter and weekday of each interview, to check fieldwork timing.
- *ops:* Weekday and month of each order, as model inputs for demand.
- *science:* Month of each sample, to look for seasonal effects.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Accepts a date or datetime field; a datetime is read on the local clock of its time zone and adds an hour column. Day of week is 0 for Sunday through 6 for Saturday.
- A missing date gives missing values in every column.

**Use something else:**

- [`ATTR_DATE_PART`](attribute.md#op-attr_date_part) when you need just one part of the date.
- [`GROUP_DATE`](grouper.md#op-group_date) when you want results grouped by day, month or year.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-date-features`](../skills/op-feat-date-features.md)

<a id="op-feat_frequency_encode"></a>

### `FEAT_FREQUENCY_ENCODE`

Replaces each category with the share of records that carry it, one number that says how common the category is.

**Level:** intermediate

**Questions it answers:**

- How common is each customer's city, as a single number a model can use?
- Which records belong to rare product codes?

**Use cases by domain:**

- *survey:* How common each respondent's free-text employer code is.
- *ops:* How common each SKU is, to flag rare items.
- *science:* How common each species label is in the sample.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Its figures come from every record of the cohort, before any filter: filtering afterwards does not recompute them.
- The share is out of records with a category; records missing it read missing and are left out of the total.

**Use something else:**

- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want the count table itself, one row per category: group by the field, then count.
- [`FEAT_ONE_HOT`](#op-feat_one_hot) when the field has few categories and each should be its own column.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-frequency-encode`](../skills/op-feat-frequency-encode.md)

<a id="op-feat_log"></a>

### `FEAT_LOG`

Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model.

**Level:** intermediate

**Questions it answers:**

- Can I tame the long tail of customer spend before fitting a model on it?
- How does income look on a log-like scale, where for large values doubling counts about the same everywhere?

**Use cases by domain:**

- *survey:* Log of reported household income before using it as a predictor.
- *ops:* Log of order value, where a few huge orders dwarf the rest.
- *science:* Log of a concentration that spans several orders of magnitude.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Computes ln(1 + x), not ln(x): 0 maps to 0, and values between -1 and 0 map below 0.
- A value of -1 or less has no log and reads missing, with no error; A missing input value gives a missing output.

**Use something else:**

- [`FEAT_SQRT`](#op-feat_sqrt) when the tail is only moderate and a gentler squeeze is enough.
- [`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want each value in standard units from the mean, not a squeezed scale.
- [`FEAT_BUCKETIZE`](#op-feat_bucketize) when you want ordered bins instead of a continuous transform.

**Glossary:** [`skew`](../glossary.md#term-skew), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-log`](../skills/op-feat-log.md)

<a id="op-feat_one_hot"></a>

### `FEAT_ONE_HOT`

Turns a category field into one 0/1 column per category, so a model or a sum can treat each category as its own yes/no.

**Level:** basic

**Questions it answers:**

- How do I feed region into a regression as separate yes/no predictors?
- Which rows are in each plan tier, as columns I can sum?

**Use cases by domain:**

- *survey:* One yes/no column per answer to a single-choice question.
- *ops:* One column per warehouse for a model of delivery time.
- *science:* One column per treatment arm in a design table.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Columns come from the field's dictionary, so a category with no rows still gets an all-zero column.
- A missing category reads 0 in every column; there is no separate unknown column.
- In a model with an intercept, leave one category's column out as the reference: all of them together are perfectly collinear and the fit is refused.

**Use something else:**

- [`FEAT_FREQUENCY_ENCODE`](#op-feat_frequency_encode) when the field has hundreds of categories and one column each is too many.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want how many rows fall in each category: group by the field, then count.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value), [`multicollinearity`](../glossary.md#term-multicollinearity)

**Skill:** [`op-feat-one-hot`](../skills/op-feat-one-hot.md)

<a id="op-feat_poly"></a>

### `FEAT_POLY`

Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve.

**Level:** advanced

**Questions it answers:**

- Does satisfaction rise with tenure and then level off?
- Is the effect of temperature on yield curved rather than straight?

**Use cases by domain:**

- *survey:* Age and age squared as predictors, for an effect that peaks in mid-life.
- *ops:* Order size and its square, for a cost curve that steepens.
- *science:* Dose, dose squared and dose cubed for a curved dose-response fit.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Emits powers 2 up to the degree (at most 10); the power-1 term is the original column, which you add yourself.
- Raw powers grow fast and move together; centre or standardise the field first. A missing input value gives a missing output.

**Use something else:**

- [`REG_OLS`](regression.md#op-reg_ols) when you want to fit the curve itself, not just build its columns.
- [`ATTR_FORMULA`](attribute.md#op-attr_formula) when you want one custom transform of the value.

**Glossary:** [`regression-coefficient`](../glossary.md#term-regression-coefficient), [`multicollinearity`](../glossary.md#term-multicollinearity), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-poly`](../skills/op-feat-poly.md)

<a id="op-feat_sqrt"></a>

### `FEAT_SQRT`

Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail.

**Level:** intermediate

**Questions it answers:**

- Can I soften the few very large complaint counts without a full log?
- What does the field look like with its big values pulled in a little?

**Use cases by domain:**

- *survey:* Square root of the number of open-text mentions per respondent.
- *ops:* Square root of daily ticket counts before modelling them.
- *science:* Square root of event counts, a common step for count data.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- A negative value has no real square root and reads missing, with no error; A missing input value gives a missing output.

**Use something else:**

- [`FEAT_LOG`](#op-feat_log) when the tail is very long and needs a stronger squeeze.
- [`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want each value in standard units from the mean.

**Glossary:** [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-sqrt`](../skills/op-feat-sqrt.md)

<a id="op-feat_target_encode"></a>

### `FEAT_TARGET_ENCODE`

Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average.

**Level:** advanced

**Questions it answers:**

- How do I give a model a single number for each of a thousand postcodes?
- What is the average sale price of each product category, on every record?

**Use cases by domain:**

- *survey:* Average satisfaction of each respondent's employer, as a predictor.
- *ops:* Average delivery delay of each carrier route, on every shipment.
- *science:* Average response of each batch from earlier or held-out runs, as a covariate (an average that includes a row's own response leaks it).

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Its figures come from every record of the cohort, before any filter: filtering afterwards does not recompute them.
- It reads no split column: placing FEAT_TRAIN_TEST_SPLIT first changes nothing, and test rows and each row's own outcome still feed every average.
- Smoothing s gives (n * category average + s * overall average) / (n + s); 0 means no pull.

**Use something else:**

- [`AGG_AVERAGE`](aggregator.md#op-agg_average) when you want to report each category's average outcome.
- [`FEAT_ONE_HOT`](#op-feat_one_hot) when the field has few categories and each should be its own column.
- [`FEAT_FREQUENCY_ENCODE`](#op-feat_frequency_encode) when you want how common each category is, without using the outcome.

**Glossary:** [`mean`](../glossary.md#term-mean), [`overfitting`](../glossary.md#term-overfitting), [`missing-value`](../glossary.md#term-missing-value)

**Skill:** [`op-feat-target-encode`](../skills/op-feat-target-encode.md)

<a id="op-feat_train_test_split"></a>

### `FEAT_TRAIN_TEST_SPLIT`

Labels every record train (0), validation (1) or test (2) by a seeded shuffle, so a model can be checked on records it never learned from.

**Level:** intermediate

**Questions it answers:**

- How do I hold back 20% of records to test a model on?
- Can I split records so each class keeps its share in train and test?

**Use cases by domain:**

- *survey:* Hold out a share of respondents to check a scoring model.
- *ops:* Train, validation and test sets for a churn model.
- *harness:* A repeatable split so two runs compare models on the same rows.

**Assumptions:**

- Runs on every record before filters, so a filter can use the new column but never narrows what the feature sees.
- Each share is the ratio times the record count, rounded; with stratify it is applied within each category.
- The same seed on the same records in the same order gives the same labels; adding or reordering records reshuffles them.
- The split does not isolate other features: frequency encoding, target encoding and equal-count bucketing in the same request still learn from test rows, so build those from the train rows in a separate request.

**Use something else:**

- `capability:sample` when you just want a random subset of rows to look at.
- [`GROUP_CATEGORY`](grouper.md#op-group_category) when you want to compare groups that already exist.

**Glossary:** [`overfitting`](../glossary.md#term-overfitting)

**Skill:** [`op-feat-train-test-split`](../skills/op-feat-train-test-split.md)
