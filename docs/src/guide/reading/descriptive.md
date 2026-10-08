# Reading descriptive results

How to read the descriptive results whose number a name alone does not explain: standardised, scale-free, model-based, transformed or relative figures. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Aggregators

<a id="op-agg_ci_lower"></a>

### `AGG_CI_LOWER`

Lower end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. See its [catalog entry](../catalog/aggregator.md#op-agg_ci_lower).

#### `value`

The lower end of a confidence interval for the group's mean at the requested confidence (95% by default): mean - z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).

**Caveats:**

- The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it.
- The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. Any one interval either contains it or not.
- Pulse uses the normal critical value (1.96 at 95%), not Student's t, so on small groups (under about 30 rows) the interval is too narrow; on a strongly skewed or heavy-tailed field it can stay too narrow, and lopsided, on much larger groups, so check AGG_SKEWNESS first. The components key t_critical holds that normal value.
- It assumes rows are independent draws. A row weight is honoured, its sample size read as the sum of weights (frequency) or Kish's effective sample size (probability, components n_eff); clustered or repeated rows, or design-based (strata / cluster) variance, are not modelled, so the interval understates the uncertainty there.
- Overlapping intervals do not show two means are equal: means can differ even when their intervals overlap, so compare groups with TEST_WELCH, not by eye.
- Empty (NaN) when fewer than two rows have a value (weighted: when the sample size is 1 or less).

<a id="op-agg_ci_upper"></a>

### `AGG_CI_UPPER`

Upper end of a confidence interval for the mean of a numeric field, showing how precisely the average is pinned down. See its [catalog entry](../catalog/aggregator.md#op-agg_ci_upper).

#### `value`

The upper end of a confidence interval for the group's mean at the requested confidence (95% by default): mean + z * standard error, where the standard error is the sample standard deviation (dividing by n - 1) over sqrt(n).

**Caveats:**

- The interval is about the MEAN, not about individual rows: most rows can, and usually do, fall outside it.
- The confidence level describes the method: over repeated samples, intervals built this way capture the true mean that share of the time. Any one interval either contains it or not.
- Pulse uses the normal critical value (1.96 at 95%), not Student's t, so on small groups (under about 30 rows) the interval is too narrow; on a strongly skewed or heavy-tailed field it can stay too narrow, and lopsided, on much larger groups, so check AGG_SKEWNESS first. The components key t_critical holds that normal value.
- It assumes rows are independent draws. A row weight is honoured, its sample size read as the sum of weights (frequency) or Kish's effective sample size (probability, components n_eff); clustered or repeated rows, or design-based (strata / cluster) variance, are not modelled, so the interval understates the uncertainty there.
- Overlapping intervals do not show two means are equal: means can differ even when their intervals overlap, so compare groups with TEST_WELCH, not by eye.
- Empty (NaN) when fewer than two rows have a value (weighted: when the sample size is 1 or less).

<a id="op-agg_kurtosis"></a>

### `AGG_KURTOSIS`

How heavy a numeric field's tails are next to a normal distribution: positive when extreme values are more common. See its [catalog entry](../catalog/aggregator.md#op-agg_kurtosis).

#### `value`

How heavy the tails are next to a normal distribution. Pulse computes the population EXCESS kurtosis g2: the average fourth-power z-score minus 3, (m4/n) / (m2/n)^2 - 3, where the components m2 and m4 are SUMS of squared and fourth-power distances from the mean (not yet divided by n), so a normal distribution scores 0. It can never fall below -2.

**Sign:**

- `+`: heavier tails than a normal distribution: extreme values turn up more often than the spread suggests
- `-`: lighter tails than a normal distribution: values far from the mean turn up less often than the spread suggests, as in a flat (uniform) or two-humped spread; it does not mean the values are bounded

**Caveats:**

- There are no sourced bands for kurtosis: the cut-offs quoted for it (such as 2 or 7) are pass/fail normality screens from particular fields, disagree on whether 3 has been subtracted, and are not graded readings.
- Small-n bias: g2 differs from the adjusted G2 that Excel KURT and SPSS print, and on small groups the gap is large; neither is stable on a handful of rows.
- It is 0 when n is 0 or 1 or the variance is zero (every value the same), so a 0 from a tiny or constant group says nothing about shape: check components n.
- It measures the tails, not how pointed the peak looks, and a single extreme value can dominate it.

<a id="op-agg_percentile"></a>

### `AGG_PERCENTILE`

Value of a numeric field below which a chosen share of rows fall, such as the 90th percentile of delivery time. See its [catalog entry](../catalog/aggregator.md#op-agg_percentile).

#### `value`

The value below which the requested share of the group's values fall, in the field's own units: at percentile 90 about 90% of values sit at or below it. Pulse interpolates linearly between the two nearest sorted values (R's default, type 7), so it may be a value no row holds.

**Caveats:**

- Tools use different interpolation rules, so on small groups another tool can return a somewhat different value for the same percentile.
- A percentile near 0 or 100 on a small group rests on one or two rows and moves a lot when they change.
- An empty group reads 0, not empty: check components n.

<a id="op-agg_ratio"></a>

### `AGG_RATIO`

Total of one field divided by the total of another, over all rows or per group, such as revenue per order. See its [catalog entry](../catalog/aggregator.md#op-agg_ratio).

#### `value`

The total of the numerator field divided by the total of the denominator field over the group's rows (a ratio of totals), such as revenue per order. Its units are the numerator's per unit of the denominator's.

**Caveats:**

- It is not the average of each row's own ratio: rows with larger denominators count for more, which is usually what a rate should do.
- A row missing either field is left out of BOTH totals.
- Empty (NaN) when the denominator total is 0.
- Read it as a share (times 100 for a percentage) only when the numerator counts a part of what the denominator counts.

<a id="op-agg_skewness"></a>

### `AGG_SKEWNESS`

How lopsided a numeric field is: positive when a long tail runs to high values, negative when it runs to low ones. See its [catalog entry](../catalog/aggregator.md#op-agg_skewness).

#### `value`

How lopsided the values are around their mean. Pulse computes the population moment coefficient g1: the average cubed z-score, (m3/n) / (m2/n)^1.5, where the components m2 and m3 are SUMS of squared and cubed distances from the mean (not yet divided by n). 0 means the values balance around the mean.

**Sign:**

- `+`: a longer or heavier tail to the right: a few values sit far above the rest
- `-`: a longer or heavier tail to the left: a few values sit far below the rest

**Caveats:**

- The sign does not fix whether the mean sits above or below the median: the two often disagree on whole-number or many-peaked fields, so read AGG_MEDIAN beside AGG_AVERAGE for that.
- There are no sourced bands for skewness: the 0.5 / 1 rules of thumb in circulation trace to secondary quotations that could not be checked against their source, so compare values with each other rather than with a fixed cut-off.
- Small-n bias: g1 runs smaller in size than the adjusted G1 that Excel SKEW, SPSS and SAS print (G1 = g1 * sqrt(n(n-1)) / (n-2)), so the two disagree on small groups and converge as n grows; on a handful of rows neither is stable.
- It is 0 when n is 0 or 1 or the variance is zero (every value the same), so a 0 from a tiny or constant group says nothing about shape: check components n.
- Deviations are cubed, so one extreme value can set the sign and size on its own.

<a id="op-agg_stddev"></a>

### `AGG_STDDEV`

Typical distance of a numeric field's values from their average, in the field's own units, over all rows or per group. See its [catalog entry](../catalog/aggregator.md#op-agg_stddev).

#### `value`

The typical distance of a value from the group's mean, in the field's own units. 0 means every value is the same; for a roughly bell-shaped field about two thirds of the values sit within one standard deviation of the mean.

**Caveats:**

- It is the spread of the rows, not the precision of the mean: for how precisely the average is known, read AGG_CI_LOWER / AGG_CI_UPPER.
- Pulse divides by n (the population form); most tools report the sample form, dividing by n - 1, which is slightly larger. The gap matters only on small groups; AGG_WELFORD gives the sample form.
- Each distance is squared before averaging, so a few extreme values inflate it; for a skewed field a percentile range describes the spread better.
- A group with a single value reads 0: no spread can be seen in one value, which is not the same as a group whose values agree.

<a id="op-agg_variance"></a>

### `AGG_VARIANCE`

Spread of a numeric field as the average squared distance from its mean, in squared units, over all rows or per group. See its [catalog entry](../catalog/aggregator.md#op-agg_variance).

#### `value`

The average squared distance of a value from the group's mean, in the field's units squared (dollars squared, minutes squared). Its square root is AGG_STDDEV, which is in the field's own units and easier to read.

**Caveats:**

- Squared units make the size hard to judge directly. For pooling or planning a study's size use AGG_WELFORD's variance (dividing by n - 1), which those formulas expect.
- Pulse divides by n (the population form); most tools report the sample form, dividing by n - 1, which is slightly larger. The gap matters only on small groups; AGG_WELFORD gives the sample form.
- Extreme values weigh heavily, since each distance is squared.
- A group with a single value reads 0: no spread can be seen in one value, which is not the same as a group whose values agree.

<a id="op-agg_weighted_mean"></a>

### `AGG_WEIGHTED_MEAN`

Average of a numeric field where each row counts in proportion to a weight field, such as a survey weight. See its [catalog entry](../catalog/aggregator.md#op-agg_weighted_mean).

#### `value`

The average of the field with each row counted in proportion to its weight: sum(value * weight) / sum(weight), in the field's own units. With equal weights it equals AGG_AVERAGE.

**Caveats:**

- Rows missing the value or the weight, or with weight 0, are left out of the average yet still count in components n; read sum_weights for the base.
- With survey or other sampling weights, uneven weights cost precision: components n_eff (Kish's effective sample size) is the row count the estimate is roughly worth, never more than the rows that count. It does not apply to frequency weights (a weight counting repeated units, such as a quantity, where the base is sum_weights) or to inverse-variance weights.
- It reads 0 when no row has a usable weight; check components sum_weights before trusting a 0.
- Negative, NaN and infinite weights are excluded and counted in components n_weight_invalid, with a warning, so the result stays within the range of the values.

<a id="op-agg_welford"></a>

### `AGG_WELFORD`

Mean, sample variance and row count of a numeric field in one result: what a comparison of group means needs. See its [catalog entry](../catalog/aggregator.md#op-agg_welford).

#### `value.*`

Three columns per group: mean (the average), variance (the SAMPLE variance, dividing by n - 1, in the field's units squared) and n (the rows with a value). Together they are what a t or z comparison of group means needs; the components add stddev, the square root of variance.

**Caveats:**

- variance is 0 when n is below 2: no spread could be estimated, which is not the same as values that agree.
- variance here divides by n - 1, so on the same rows it is larger than AGG_VARIANCE (which divides by n).
- With no rows the result is empty, not zeros.

<a id="op-agg_zscore"></a>

### `AGG_ZSCORE`

Population mean and standard deviation of a numeric field, for standardizing it; the value itself is always 0. See its [catalog entry](../catalog/aggregator.md#op-agg_zscore).

#### `value`

The average of every row's z-score within the group, which is 0 by construction (up to rounding): the value carries no information about the data. The usable numbers are in the components: pop_mean and pop_stddev (the centre and the population spread, dividing by n), target_value (the LAST row's value) and zscore (that row's z-score).

**Caveats:**

- For a z-score on every row use ATTR_ZSCORE; for each group's value against all groups use OVERLAY_ZSCORE_VS_TOTAL.
- There are no sourced bands for reading a z-score of this kind: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this one is built from a spread of values instead, so it has no p-value behind it.
- Which row is last depends on row order (not on date), so the components zscore is reproducible only on a fixed order.
- The last row is part of the mean and spread it is scored against, which damps its zscore (never beyond sqrt(n - 1) in size), so it understates how unusual that row is; for a reading against its own past use OVERLAY_ZSCORE_VS_ROLLING.
- The value and the components zscore read 0 when every value is the same (no spread); every component reads 0 when the group is empty.

## Attributes

<a id="op-attr_normalized"></a>

### `ATTR_NORMALIZED`

Rescales a numeric field to 0 to 1 on every row: 0 is the smallest value, 1 the largest. See its [catalog entry](../catalog/attribute.md#op-attr_normalized).

#### `value`

Where the row's value sits between the smallest and largest value, on a 0-to-1 scale: (value - min) / (max - min). 0 is the minimum, 1 the maximum and 0.5 halfway between them, which is not the median.

**Caveats:**

- One extreme value sets the minimum or maximum and squeezes every other row into a narrow part of the scale; ATTR_PERCENTILE is not affected that way.
- A row with a missing value reads 0, the same as the minimum, and every row reads 0 when all values are equal (max = min).
- The scale comes from every row that passed the filters, not from the row's group: averaging it per group shows how each group sits against the whole, and a different filter gives different values.

<a id="op-attr_percentile"></a>

### `ATTR_PERCENTILE`

Adds to every row its percentile rank, rank / n * 100: the share of rows at or below it when values are untied. See its [catalog entry](../catalog/attribute.md#op-attr_percentile).

#### `value`

The row's position among the sorted values, as a percentage: rank / n * 100, where the smallest value has rank 1 and n counts the rows with a value. The largest value reads 100 and the smallest 100 / n, so for a value no other row shares it is the share of rows at or below this one; tied values break that (see below).

**Caveats:**

- Tied values do not share a percentile: each tied row takes its own rank in an arbitrary order, so equal values can read different percentiles, far apart on a field with few distinct values (a 1-5 rating); use WIN_RANK when ties must share a rank.
- A row with a missing value reads 0, which no row with a value can: treat 0 as missing.
- Other tools define it differently (smallest at 0, or ties at their midpoint), so the same row can read a few points apart elsewhere.
- Equal steps in percentile are not equal steps in the value: near the middle of a bell-shaped field a small change in value moves the percentile a lot.
- The scale comes from every row that passed the filters, not from the row's group: averaging it per group shows how each group sits against the whole, and a different filter gives different values.

<a id="op-attr_reg_fitted"></a>

### `ATTR_REG_FITTED`

Adds to every row the value a straight-line model predicts for its target from its predictors. See its [catalog entry](../catalog/attribute.md#op-attr_reg_fitted).

#### `value`

The target value the fitted straight-line model predicts for the row from its predictors: intercept + sum(coefficient * predictor), in the target's units. The model is fitted by least squares to the rows that passed the filters and have the target and every predictor.

**Caveats:**

- The model describes association in these rows: a fitted value is what rows with these predictor values look like here, not what would happen to a row if a predictor were changed; a causal reading needs a design that supports it.
- The model is fitted and read on the same rows, so it matches them more closely than it will match new rows (overfitting), most of all with many predictors and few rows.
- A straight-line model: where the target bends with a predictor, fitted values miss in a pattern over parts of its range; read ATTR_REG_RESIDUAL to see it.
- A row missing the target still gets a fitted value when its predictors are present; a row missing any predictor reads 0, which is not a prediction.
- With a penalty (l1, l2, elasticnet) the coefficients are deliberately shrunk toward 0, so fitted values sit closer to the target's mean and the residuals are larger overall (their sum of squares rises) than an unpenalized fit's, though a single row's can go either way; use no penalty for diagnostics.

<a id="op-attr_reg_leverage"></a>

### `ATTR_REG_LEVERAGE`

Adds to every row its leverage: how unusual its predictor values are, and so how hard it can pull a straight-line fit. See its [catalog entry](../catalog/attribute.md#op-attr_reg_leverage).

#### `value`

How far the row's predictor values sit from the other rows' (from the predictors' means, scaled by their spread and correlation), and so how hard the row can pull the fitted line toward itself: the hat-matrix diagonal 1/n + (x - mean)' M^-1 (x - mean), where M is the predictors' centred sum-of-squares-and-cross-products matrix. Over the n rows in the fit it runs from 1/n to 1 and averages p / n, where p counts the coefficients including the intercept.

**Caveats:**

- A common rule of thumb flags leverage above 2p / n, twice the average (Hoaglin & Welsch 1978); it is a screening threshold, not a graded convention, and with few rows per coefficient it flags many rows.
- It reads the predictors only, never the target: a high-leverage row can sit right on the line. Whether it moved the fit depends on its residual too (ATTR_REG_RESIDUAL); influence measures that combine the two, such as Cook's distance, are not computed.
- A row outside the fit (missing the target) still gets a leverage from its predictors, which can exceed 1 when it lies beyond the fitted rows; a row missing any predictor reads 0, below the 1/n floor, so 0 marks a missing row.
- Unpenalized least squares only; a request with a penalty is refused.

<a id="op-attr_reg_residual"></a>

### `ATTR_REG_RESIDUAL`

Adds to every row its residual: the actual target value minus the value a straight-line model predicts for it. See its [catalog entry](../catalog/attribute.md#op-attr_reg_residual).

#### `value`

Actual minus predicted: the row's target value minus the value the straight-line model predicts for it, in the target's units. Over the rows used in an unpenalized fit the residuals average 0.

**Sign:**

- `+`: the row's target is above what the model predicts from its predictors
- `-`: the row's target is below what the model predicts from its predictors

**Caveats:**

- A residual is what this model leaves unexplained, not a measurement error and not the effect of any one thing left out: another set of predictors gives other residuals.
- It is in the target's units, so there are no sourced cut-offs for a large one; compare it with the model's typical miss (REG_OLS residual_std_err) times sqrt(1 - leverage) from ATTR_REG_LEVERAGE, since a high-leverage row's residual varies less, for a row-fair comparison.
- A row with high leverage (ATTR_REG_LEVERAGE) pulls the line toward itself, so its residual can be small even when the row is unusual.
- Patterns across rows (a curve, or spread that grows with the predicted value) point to a missing term or heteroscedasticity, not to rows that are wrong.
- The model describes association in these rows: a fitted value is what rows with these predictor values look like here, not what would happen to a row if a predictor were changed; a causal reading needs a design that supports it.
- A row missing the target or any predictor reads 0, the same as a row the model fits exactly.
- With a penalty (l1, l2, elasticnet) the coefficients are deliberately shrunk toward 0, so fitted values sit closer to the target's mean and the residuals are larger overall (their sum of squares rises) than an unpenalized fit's, though a single row's can go either way; use no penalty for diagnostics.

<a id="op-attr_tscore"></a>

### `ATTR_TSCORE`

Adds to every row its T-score: the z-score rescaled so the mean is 50 and one standard deviation is 10. See its [catalog entry](../catalog/attribute.md#op-attr_tscore).

#### `value`

The z-score rescaled to a mean of 50 and a standard deviation of 10: 50 + 10 * z, with the population standard deviation (dividing by n). 60 is one standard deviation above the mean, 40 one below.

**Caveats:**

- Unrelated to Student's t or a t test: the name comes from educational testing, and the value carries no p-value.
- There are no sourced bands for reading a z-score of this kind: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this one is built from the spread of the values instead, so it has no p-value behind it.
- It is not a percentile: 70 does not mean the 70th percentile. Only on a roughly normal field does 60 sit near the 84th percentile.
- One extreme row inflates the standard deviation and pulls every other score toward the centre; on n rows no z-score can exceed sqrt(n - 1) in size (Shiffler 1988), so on a small set a modest score may be the most extreme possible.
- The scale comes from every row that passed the filters, not from the row's group: averaging it per group shows how each group sits against the whole, and a different filter gives different values.
- Under a row weight the mean and standard deviation are weighted (dividing by the sum of weights), the same for frequency and probability weights; a row with no usable weight adds nothing to them but is still scored.
- A row with a missing value reads 50, the same as an exactly average row, and every row reads 50 when all values are equal; an extreme row can fall below 0 or above 100.

<a id="op-attr_zscore"></a>

### `ATTR_ZSCORE`

Adds to every row its z-score: how many standard deviations the row's value sits above or below the mean. See its [catalog entry](../catalog/attribute.md#op-attr_zscore).

#### `value`

How many standard deviations the row's value sits from the mean: (value - mean) / standard deviation, with the population standard deviation (dividing by n). 0 is exactly average, 1 is one standard deviation above it.

**Sign:**

- `+`: the row's value is above the mean
- `-`: the row's value is below the mean

**Caveats:**

- There are no sourced bands for reading a z-score of this kind: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this one is built from the spread of the values instead, so it has no p-value behind it.
- Rules such as 'about 95% of rows lie within 2 standard deviations' hold only for a roughly bell-shaped (normal) field; on a skewed field far more rows can sit beyond 2 on the long side.
- One extreme row inflates the standard deviation and pulls every other score toward the centre; on n rows no z-score can exceed sqrt(n - 1) in size (Shiffler 1988), so on a small set a modest score may be the most extreme possible.
- The scale comes from every row that passed the filters, not from the row's group: averaging it per group shows how each group sits against the whole, and a different filter gives different values.
- Under a row weight the mean and standard deviation are weighted (dividing by the sum of weights), the same for frequency and probability weights; a row with no usable weight adds nothing to them but is still scored.
- A row with a missing value reads 0, the same as an exactly average row, and every row reads 0 when all values are equal (no spread); check the source field before reading a 0 as average.

## Window operators

<a id="op-win_delta"></a>

### `WIN_DELTA`

Adds the change from a set number of rows earlier in the field's own units, such as orders this month minus last month. See its [catalog entry](../catalog/window.md#op-win_delta).

#### `value`

The change since the row periods rows earlier, in the field's own units: current - earlier. A delta of 12 on an order count is 12 more orders than the earlier row.

**Sign:**

- `+`: the current value is above the earlier one
- `-`: the current value is below the earlier one

**Caveats:**

- It is in the field's units, so a change of 10 is large on a small field and trivial on a large one; read WIN_PCT_CHANGE for the change relative to the earlier value.
- On a field that is itself a percentage the delta is in percentage points: 40% to 45% reads 5, a 12.5% relative rise.
- An earlier value of 0 is a real change (the delta is the current value), unlike WIN_PCT_CHANGE, which reads null there.
- A step is one row of the result in order_by order, not a calendar period: when a period has no row (a month with no sales), the comparison silently spans the gap, so check the series has a row for every period.
- One period's change mixes real movement with ordinary noise; judge it against how much the series usually moves from period to period.
- The first periods rows of each partition, and any row where either value is missing, read null.

<a id="op-win_dense_rank"></a>

### `WIN_DENSE_RANK`

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next follows on: 1, 2, 2, 3. See its [catalog entry](../catalog/window.md#op-win_dense_rank).

#### `value`

The row's position among the distinct order_by values within its partition, starting at 1. Rows equal on every order_by key share a rank, and the next rank follows on with no gap: 1, 2, 2, 3. The largest rank is the number of distinct values, not the number of rows.

**Caveats:**

- Rank 1 is the first row in order_by order: the smallest value with an ascending key, the largest with desc. Say which when reporting a 'top' rank.
- A rank is a position, not a distance: rank 1 and rank 2 can be almost equal or far apart, so read the values beside the ranks. Rank 3 of 5 rows is not rank 3 of 500; compare positions across partitions of different size with care.
- Rows tie only when equal on every order_by key; rows with a missing order_by value sort last and tie with each other, so they share the last rank.
- Dense rank 3 does not mean two rows came first: many rows can share ranks 1 and 2. Use WIN_RANK when the count of rows ahead matters.

<a id="op-win_ewma"></a>

### `WIN_EWMA`

Adds a smoothed series: each new value counts for a fixed share w, the previous smoothed level for 1 - w, so older values fade. See its [catalog entry](../catalog/window.md#op-win_ewma).

#### `value`

A smoothed level of the field: s = alpha * current + (1 - alpha) * previous s, seeded with the partition's first value present. A value k rows back carries weight alpha * (1 - alpha)^k, so alpha near 1 follows the latest value closely and alpha near 0 changes slowly.

**Caveats:**

- The seed enters at full weight, so the start of each partition leans on the first value: with alpha 0.1 it still carries about 35% of the weight ten rows later (0.9^10). Read the early rows with care.
- It lags behind a trend: on a steadily rising series the smoothed value sits below the latest value.
- alpha is given directly; some tools set it from a span (alpha = 2 / (span + 1)), which is not accepted here, so convert first.
- A missing value reads null and the smoothing carries over it: the next value is blended in as if the gap were one step.
- The required frame is not used: the smoothing always runs from the partition's first row.
- A step is one row of the result in order_by order, not a calendar period: when a period has no row (a month with no sales), the comparison silently spans the gap, so check the series has a row for every period.

<a id="op-win_moving_avg"></a>

### `WIN_MOVING_AVG`

Adds a moving average: the mean of a field over a fixed number of neighbouring rows, smoothing short-term swings in a series. See its [catalog entry](../catalog/window.md#op-win_moving_avg).

#### `value`

The mean of the field over the rows in the frame around this one: from preceding rows before it to following rows after it, within the partition and in order_by order. preceding 6, following 0 is a trailing average of 7 rows.

**Caveats:**

- Near the start and end of each partition the frame runs off the edge and the mean covers fewer rows, so the first values are noisier and not comparable with the rest.
- A trailing frame (following 0) lags behind turns in the series by about half its width; a centred frame (preceding equal to following) does not lag, and an uneven one lags by half the difference, but any frame with following above 0 uses later rows, so it cannot be computed until they exist.
- Missing values are skipped, so the mean is over the values present, not the frame width; a frame with no value reads null.
- With groups, each row is one group's figure and counts once whatever the group's size: an average over rows is an average of group figures, not the average over all records.
- A step is one row of the result in order_by order, not a calendar period: when a period has no row (a month with no sales), the comparison silently spans the gap, so check the series has a row for every period.

<a id="op-win_pct_change"></a>

### `WIN_PCT_CHANGE`

Adds the change from a set number of rows earlier as a fraction of the earlier value: 0.05 is a 5% rise on last month. See its [catalog entry](../catalog/window.md#op-win_pct_change).

#### `value`

The change since the row periods rows earlier, as a fraction of that earlier value: (current - earlier) / earlier. 0.05 is a 5% rise, -0.2 a 20% fall and 1 a doubling; the value is not multiplied by 100.

**Sign:**

- `+`: the current value is above the earlier one (when the earlier value is positive)
- `-`: the current value is below the earlier one (when the earlier value is positive)

**Caveats:**

- A small earlier value makes the fraction large and jumpy: a move from 0.5 to 3 reads 5 (a 500% rise), so a big percentage on a tiny base says little; read WIN_DELTA beside it. An earlier value of exactly 0 reads null.
- On a field that can be negative the sign follows the division, not the direction: a rise from -10 to -5 reads -0.5. Use WIN_DELTA for such a field.
- Rises and falls are not symmetric: after a fall of 0.5 (50%) it takes a rise of 1 (100%) to get back, so averaging or adding period changes misstates the overall change.
- On a field that is itself a percentage, 40% to 45% reads 0.125 (a 12.5% relative rise), not 5 percentage points; WIN_DELTA gives the points.
- A step is one row of the result in order_by order, not a calendar period: when a period has no row (a month with no sales), the comparison silently spans the gap, so check the series has a row for every period.
- One period's change mixes real movement with ordinary noise; judge it against how much the series usually moves from period to period.
- The first periods rows of each partition, and any row where either value is missing, read null.

<a id="op-win_rank"></a>

### `WIN_RANK`

Ranks rows within each partition in the chosen order; rows with equal values share a rank and the next skips: 1, 2, 2, 4. See its [catalog entry](../catalog/window.md#op-win_rank).

#### `value`

The row's position in order_by order within its partition, starting at 1. Rows equal on every order_by key share a rank, and the next rank skips past them: 1, 2, 2, 4. A row's rank is 1 plus the number of rows strictly before it.

**Caveats:**

- Rank 1 is the first row in order_by order: the smallest value with an ascending key, the largest with desc. Say which when reporting a 'top' rank.
- A rank is a position, not a distance: rank 1 and rank 2 can be almost equal or far apart, so read the values beside the ranks. Rank 3 of 5 rows is not rank 3 of 500; compare positions across partitions of different size with care.
- Rows tie only when equal on every order_by key; rows with a missing order_by value sort last and tie with each other, so they share the last rank.
- Tied rows make the ranks skip numbers, so the ranks used are fewer than the rows; to count distinct values use WIN_DENSE_RANK.

<a id="op-win_running_avg"></a>

### `WIN_RUNNING_AVG`

Adds the average of a field over the rows so far in the ordered series, such as the average order value to date. See its [catalog entry](../catalog/window.md#op-win_running_avg).

#### `value`

The mean of the field over the rows in the frame. With the usual frame (no preceding bound, following 0) it is the cumulative average: the mean of every row from the partition's start up to and including this one.

**Caveats:**

- Early values rest on few rows and swing; later ones rest on the whole history and barely move, so a late cumulative average says little about the recent level (use WIN_MOVING_AVG for that).
- A frame with a following bound above 0, or none, takes in later rows too.
- Missing values are skipped, so the mean is over the values present; a frame with no value reads null.
- With groups, each row is one group's figure and counts once whatever the group's size: an average over rows is an average of group figures, not the average over all records.

## Feature operators

<a id="op-feat_frequency_encode"></a>

### `FEAT_FREQUENCY_ENCODE`

Replaces each category with the share of records that carry it, one number that says how common the category is. See its [catalog entry](../catalog/feature.md#op-feat_frequency_encode).

#### `value`

The share of records carrying this record's category, as a fraction between 0 and 1: 0.25 means one in four records with a category have this one. Every record in a category gets the same value.

**Caveats:**

- The share is out of records WITH a category: records missing the field read missing and are left out of the total, so shares across categories add up to 1 among those records only.
- Computed over every record of the cohort before any filter runs, so a filtered result still carries figures from the records it filtered out.
- Two different categories with the same count get the same value, so the column cannot tell them apart.
- It is a share, not a count: the same category reads differently on a larger or smaller cohort.

<a id="op-feat_log"></a>

### `FEAT_LOG`

Adds a column holding the natural log of 1 + the value, which pulls in a long tail of large values so a skewed field is easier to model. See its [catalog entry](../catalog/feature.md#op-feat_log).

#### `value`

The natural log of 1 + the value: ln(1 + x). 0 reads 0, e - 1 (about 1.72) reads 1, 9 reads about 2.3 and 99 about 4.6; each step of about 0.69 is a doubling of 1 + x, so equal steps are equal ratios of 1 + x, not equal amounts.

**Sign:**

- `+`: the original value is above 0
- `-`: the original value is between -1 and 0

**Caveats:**

- A value of -1 or less has no log and reads missing, with no error raised: count the missing rows before trusting a model built on the column.
- The + 1 shift keeps 0 usable, but equal steps are equal ratios of 1 + x, not of x, and the shape depends on the units: on values much smaller than 1 the result is close to the value itself, on values much larger than 1 it is close to ln(x), so rescaling the field (dollars vs thousands) changes the shape of the result.
- Averages, sums and differences of the transformed column are on the new scale: transforming them back does not give the average of the original values.
- A missing input gives a missing output.

<a id="op-feat_poly"></a>

### `FEAT_POLY`

Adds columns holding the value squared, cubed and so on up to a chosen power, so a straight-line model can bend into a curve. See its [catalog entry](../catalog/feature.md#op-feat_poly).

#### `value.*`

One column per power k from 2 up to the degree, named &lt;prefix&gt;_&lt;k&gt; (prefix is the label, by default &lt;field&gt;_poly), each holding the value raised to that power: x_poly_2 is x squared, x_poly_3 is x cubed. There is no power-1 column; the original field is it.

**Caveats:**

- A single power column means little alone: the curve shows in a model's coefficients on x and its powers together, and those change when the field is centred first.
- Powers of a raw field move almost in step with each other and with x, which makes a model's coefficients unstable; centre or standardise the field before expanding it.
- Values grow fast: x = 100 to the power 10 is 1e20, and very large inputs can overflow to infinity rather than read missing.
- Even powers lose the sign: x = -3 and x = 3 both give 9 in the squared column.
- A missing input gives a missing value in every power column.

<a id="op-feat_sqrt"></a>

### `FEAT_SQRT`

Adds a column holding the square root of the value, a gentler squeeze than a log for counts with a moderate long tail. See its [catalog entry](../catalog/feature.md#op-feat_sqrt).

#### `value`

The square root of the value: 4 reads 2, 100 reads 10, 10,000 reads 100. Large values are pulled in more than small ones, but less sharply than a log.

**Caveats:**

- A negative value has no real square root and reads missing, with no error raised; 0 reads 0.
- Between 0 and 1 the root is LARGER than the value (0.25 reads 0.5), so a field of fractions is stretched, not squeezed.
- Averages, sums and differences of the transformed column are on the new scale: transforming them back does not give the average of the original values.
- A missing input gives a missing output.

<a id="op-feat_target_encode"></a>

### `FEAT_TARGET_ENCODE`

Replaces each category with the average outcome of the records in that category, optionally pulled toward the overall average. See its [catalog entry](../catalog/feature.md#op-feat_target_encode).

#### `value`

The average of the outcome field over records in this record's category, in the outcome's units; with smoothing s, (n * category average + s * overall average) / (n + s), so a category with few records sits nearer the overall average.

**Caveats:**

- Target leakage: every average includes the record's own outcome and the outcomes of validation and test records, because the encoder reads no split column. Placing FEAT_TRAIN_TEST_SPLIT first does not change a single value, and filtering to split 0 afterwards keeps the leaked figures.
- A model trained on this column will look better than it is: with no smoothing, a category seen once encodes exactly its own outcome. To encode from the training rows only, compute each category's AGG_AVERAGE of the outcome on split 0 in a separate request and map those averages onto the validation and test records yourself. For the training records, leave each record's own outcome out (out-of-fold or leave-one-out averages) and smooth, since a split-0 average still contains it.
- Computed over every record of the cohort before any filter runs, so a filtered result still carries figures from the records it filtered out.
- Only records with both a category and an outcome count; a category whose outcomes are all missing reads the overall average, and a record missing the category reads missing.
