# Reading overlay results

How to read the figures each overlay kind this instance offers lays over its host. Each operator lists its output fields: what the value means, the labelled bands of a published convention where one applies, what its sign says, and the caveats to keep in mind.

## Overlays

<a id="op-overlay_chisq_col"></a>

### `OVERLAY_CHISQ_COL`

Chi-square test per crosstab column: checks whether each column's spread across the rows departs from the table's overall row mix. See its [catalog entry](../catalog/overlay.md#op-overlay_chisq_col).

#### `summary.statistic`

In each series entry (one per crosstab column): chi-square for that column's counts against the counts its column total would give at the table's overall row shares. 0 means the column's mix matches the overall mix; read the entry's p_value rather than the raw value.

**Caveats:**

- Chi-square grows with the counts and with the number of cells, so it does not measure how strongly the categories are associated; read the p-value for the test and the cell values (or TEST_CHISQ's Cramer's V on raw rows) for strength.
- The overall row shares include the column itself, so a column that holds most of the table pulls them toward itself and reads closer to 0.

#### `summary.p_value`

**Caveats:**

- Every column is a separate test and Pulse reports the p-values raw: with many columns some small ones turn up by luck, so adjust for multiple comparisons: set multiplicity on the overlay and read summary.p_adjusted.
- It comes from the chi-square approximation; Pulse warns once per column whose expected counts fall below 5. A column with a zero total gets no value (NaN).

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.parameters.df`

In each series entry: degrees of freedom of the column's chi-square, rows - 1, the same for every column.

<a id="op-overlay_chisq_matrix"></a>

### `OVERLAY_CHISQ_MATRIX`

Chi-square test on a whole crosstab: checks whether the row and column categories are associated, with one p-value for the table. See its [catalog entry](../catalog/overlay.md#op-overlay_chisq_matrix).

#### `scalar`

The chi-square statistic for the whole crosstab, also in summary.statistic: the sum over cells of (observed - expected)^2 / expected, where expected is the count the row and column totals predict if the two fields were independent. 0 means the table matches that pattern exactly; how far above 0 is unusual depends on summary.parameters.df, so read summary.p_value rather than the raw value.

**Caveats:**

- Chi-square grows with the counts and with the number of cells, so it does not measure how strongly the categories are associated; read the p-value for the test and the cell values (or TEST_CHISQ's Cramer's V on raw rows) for strength.
- No continuity (Yates) correction is applied.

#### `summary.statistic`

The same chi-square statistic as scalar.

#### `summary.p_value`

**Caveats:**

- It comes from the chi-square approximation, which is unreliable when expected counts fall below about 5; Pulse warns (PULSE_OVERLAY_EXPECTED_LOW) when they do.
- It is meaningful only when the cells count independent rows (AGG_COUNT); on sums or averages it is not a valid p-value.
- On a probability-weighted host the table is first scaled to its effective sample size (summary.parameters.n_eff): a first-order Kish approximation, not the Rao-Scott correction survey software applies.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.parameters.df`

Degrees of freedom of the chi-square reference curve: (rows - 1) x (columns - 1) of the host crosstab.

**Caveats:**

- It reflects the table's shape, not its number of rows of data.
- A row or column whose total is 0 adds nothing to the statistic but still counts in df, which makes the p-value too large; drop empty rows and columns from the host first.

<a id="op-overlay_chisq_row"></a>

### `OVERLAY_CHISQ_ROW`

Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix. See its [catalog entry](../catalog/overlay.md#op-overlay_chisq_row).

#### `summary.statistic`

In each series entry (one per crosstab row): chi-square for that row's counts against the counts its row total would give at the table's overall column shares. 0 means the row's mix matches the overall mix; read the entry's p_value rather than the raw value.

**Caveats:**

- Chi-square grows with the counts and with the number of cells, so it does not measure how strongly the categories are associated; read the p-value for the test and the cell values (or TEST_CHISQ's Cramer's V on raw rows) for strength.
- The overall column shares include the row itself, so a row that holds most of the table pulls them toward itself and reads closer to 0.

#### `summary.p_value`

**Caveats:**

- Every row is a separate test and Pulse reports the p-values raw: with many rows some small ones turn up by luck, so adjust for multiple comparisons: set multiplicity on the overlay and read summary.p_adjusted.
- It comes from the chi-square approximation; Pulse warns once per row whose expected counts fall below 5. A row with a zero total gets no value (NaN).

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.parameters.df`

In each series entry: degrees of freedom of the row's chi-square, columns - 1, the same for every row.

<a id="op-overlay_chisq_vs_pop"></a>

### `OVERLAY_CHISQ_VS_POP`

Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix. See its [catalog entry](../catalog/overlay.md#op-overlay_chisq_vs_pop).

#### `scalar`

The chi-square statistic, also in summary.statistic: the subset's category counts against the counts the population's shares predict at the subset's size, with the shares rescaled over the categories both sides show (population nulls and categories cut from a top-K listing are left out). 0 means the subset's mix matches the population's; read summary.p_value rather than the raw value.

**Caveats:**

- It grows with the subset's size, so a very big subset gives a big value for a slight shift in mix; OVERLAY_INDEX_VS_POP shows which categories moved and by how much.

#### `summary.statistic`

The same chi-square statistic as scalar.

#### `summary.p_value`

**Caveats:**

- The population's shares are treated as known and fixed; when the subset is a big part of the population the two overlap and the p-value is only approximate.
- With a single category there is nothing to compare and the p-value is NaN.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.parameters.df`

Degrees of freedom: the number of compared categories (the subset's facet values that the population also shows), minus 1.

**Caveats:**

- A population category that is missing from the subset's facet values is left out of both the statistic and df, which understates a departure made of absent categories.
- A subset category the population never shows is impossible under the population's mix, so it cannot be tested: it is left out of the statistic, the subset's total and df, and Pulse warns (PULSE_OVERLAY_REF_ZERO) once per such category. Read those warnings as departures in their own right.

<a id="op-overlay_chisq_vs_ref"></a>

### `OVERLAY_CHISQ_VS_REF`

Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix. See its [catalog entry](../catalog/overlay.md#op-overlay_chisq_vs_ref).

#### `scalar`

**Caveats:**

- On this kind the scalar holds the p-value (the same value as summary.p_value), not the chi-square; the chi-square is in summary.statistic.
- The reference mix is treated as fixed, so its own sampling uncertainty is ignored and with a small reference the p-value comes out too small.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.p_value`

**Caveats:**

- It is the same value as scalar; see that reading for why a small reference makes it too small.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.statistic`

The chi-square statistic: the target's cell counts against the counts the reference's cell shares predict at the target's total. 0 means the two tables have the same mix; read the p-value rather than the raw value.

**Caveats:**

- Chi-square grows with the counts and with the number of cells, so it does not measure how strongly the categories are associated; read the p-value for the test and the cell values (or TEST_CHISQ's Cramer's V on raw rows) for strength.
- Cells the reference never shows (reference count 0) are left out of the statistic and df, even when the target has counts there; those counts still set the scale. A shift into new cells is not detected, so check them directly (OVERLAY_DELTA_VS_REF).

#### `summary.parameters.df`

Degrees of freedom: the number of cells present in both requests with a positive expected count, minus 1.

**Caveats:**

- Because the reference shares are taken as fixed, df is cells - 1, not the (rows - 1) x (columns - 1) of a test that treats both tables as samples (TEST_CHISQ on stacked rows).

<a id="op-overlay_fisher_exact_cell"></a>

### `OVERLAY_FISHER_EXACT_CELL`

Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected. See its [catalog entry](../catalog/overlay.md#op-overlay_fisher_exact_cell).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided exact p-value of its own 2x2 table: this row versus the rest, by this column versus the rest.
- It does not say which way or how far the cell departs: compare the cell's count with what its row and column totals predict (or an OVERLAY_INDEX_VS_MARGIN layer) for direction and size.
- Every cell is a separate test and Pulse reports the p-values raw: with many cells some small ones turn up by luck, so before flagging cells adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_ks_vs_pop"></a>

### `OVERLAY_KS_VS_POP`

Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's. See its [catalog entry](../catalog/overlay.md#op-overlay_ks_vs_pop).

#### `scalar`

D, also in summary.statistic: the largest vertical gap between the subset's and the population's cumulative distributions, from 0 (the same curve) to 1 (no overlap at all).

**Caveats:**

- Pulse rebuilds both curves from histograms or percentiles, not raw values, so D is measured only at bin edges or percentile points and can understate the true gap.
- D shows that the shapes differ, not how: compare the histograms to see whether it is the centre, the spread or the tails.

#### `summary.statistic`

The same D as scalar.

#### `summary.p_value`

**Caveats:**

- With few rows the test has low power and misses real differences; with very many rows even trivial differences in shape give small p-values.
- It compares two observed distributions. It is not valid for testing against a named distribution with parameters estimated from the data, which needs the Lilliefors correction.
- It is a two-sided large-sample approximation built from the full row counts in summary.parameters, while D comes from the binned curves.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

#### `summary.parameters.n_subset`

The number of rows behind the subset's numeric facet; one of the two counts the p-value's large-sample formula uses.

#### `summary.parameters.n_pop`

The number of rows behind the population's numeric summary; the other count the p-value uses.

**Caveats:**

- When the subset is part of the population these rows include the subset's, so the two samples overlap: D shrinks and this count overstates the comparison sample, and the p-value comes out too large (conservative).

<a id="op-overlay_pairwise_probit_t"></a>

### `OVERLAY_PAIRWISE_PROBIT_T`

Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use. See its [catalog entry](../catalog/overlay.md#op-overlay_pairwise_probit_t).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided p-value for one pair, from a t curve on n_i + n_j - 2 degrees of freedom after a probit transform of each share; it is absent when a leg is unreadable or the test is degenerate.
- The probit simplification differs from the textbook two-proportion test (OVERLAY_PAIRWISE_PROP_Z), so the two give different p-values for the same cells.
- Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones turn up by luck, so before flagging pairs adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the two cell values for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_pairwise_prop_z"></a>

### `OVERLAY_PAIRWISE_PROP_Z`

Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share? See its [catalog entry](../catalog/overlay.md#op-overlay_pairwise_prop_z).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided pooled two-proportion z-test p-value for one pair of rows (or columns), named by the pair-axis key; it is absent when a leg is unreadable or the test is degenerate.
- Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones turn up by luck, so before flagging pairs adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The normal approximation needs roughly 10 successes and 10 failures per leg, and an n_source or p_source that does not match the cell values skips pairs.
- The layer does not report an effect size: read the two cell values for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_pairwise_two_means_z"></a>

### `OVERLAY_PAIRWISE_TWO_MEANS_Z`

Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? See its [catalog entry](../catalog/overlay.md#op-overlay_pairwise_two_means_z).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided z-test p-value for one pair of means, read from the normal curve; it is absent when a leg is unreadable or the test is degenerate.
- With small cells the normal curve gives p-values that are too small; OVERLAY_PAIRWISE_WELCH_T is the safer reading there.
- Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones turn up by luck, so before flagging pairs adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the two cell values for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_pairwise_weighted_two_means_z"></a>

### `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`

Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose. See its [catalog entry](../catalog/overlay.md#op-overlay_pairwise_weighted_two_means_z).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided z-test p-value for one pair of weighted means; params.n_basis sets the sample size (sum of weights, or Kish's effective sample size).
- It accounts for weighting only, not clustering or other design effects, so with such designs the p-values come out too small.
- Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones turn up by luck, so before flagging pairs adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the two cell values for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_pairwise_welch_t"></a>

### `OVERLAY_PAIRWISE_WELCH_T`

Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? See its [catalog entry](../catalog/overlay.md#op-overlay_pairwise_welch_t).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided Welch t-test p-value for one pair of means, with Welch-Satterthwaite degrees of freedom; it is absent when a leg is unreadable or the test is degenerate.
- Each mean should be roughly normal: safe for big cells, risky for small skewed ones.
- Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones turn up by luck, so before flagging pairs adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the two cell values for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_prop_z_cell"></a>

### `OVERLAY_PROP_Z_CELL`

Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's? See its [catalog entry](../catalog/overlay.md#op-overlay_prop_z_cell).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided p-value comparing the target's share (cell / row total) with the reference's share in the same cell.
- A cell whose row total is missing or zero on either side is NaN, with a warning (PULSE_OVERLAY_REF_ZERO): it gets no test.
- The normal approximation needs roughly 10 successes and 10 failures on each side; with fewer the p-value is unreliable.
- Every cell is a separate test and Pulse reports the p-values raw: with many cells some small ones turn up by luck, so before flagging cells adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_prop_z_panel"></a>

### `OVERLAY_PROP_Z_PANEL`

Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share? See its [catalog entry](../catalog/overlay.md#op-overlay_prop_z_panel).

#### `cells.value`

**Caveats:**

- Each cell holds a list of two-sided p-values, one per pair of requests, in upper-triangle order with the reference at index 0: (0,1), (0,2), ..., (1,2), ...; M requests give M(M-1)/2 entries.
- The number of tests is cells times pairs, so it grows fast: with many cells and pairs some small p-values turn up by luck. Pulse reports them raw; to adjust for multiple comparisons set multiplicity on the overlay, which adds payload.p_adjusted (every list entry joins the family).
- The normal approximation needs roughly 10 successes and 10 failures per side.
- A pair involving a slot whose row margin is missing or zero is NaN, with a warning: it gets no test.
- The layer does not report an effect size: compare the slots' cell shares (cell / row total) for how big each gap is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_t_cell"></a>

### `OVERLAY_T_CELL`

Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? See its [catalog entry](../catalog/overlay.md#op-overlay_t_cell).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided Welch t-test p-value comparing the target's mean in that cell with the reference's.
- Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.
- Every cell is a separate test and Pulse reports the p-values raw: with many cells some small ones turn up by luck, so before flagging cells adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_t_vs_ref"></a>

### `OVERLAY_T_VS_REF`

Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? See its [catalog entry](../catalog/overlay.md#op-overlay_t_vs_ref).

#### `summary.statistic`

**Caveats:**

- Despite the field name, each series entry's summary.statistic holds that group's two-sided Welch t-test p-value, not a t value; Pulse does not emit t.
- Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.
- Every group is a separate test and Pulse reports the p-values raw: with many groups some small ones turn up by luck, so adjust for multiple comparisons: set multiplicity on the overlay and read summary.p_adjusted.
- The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_zscore_vs_margin"></a>

### `OVERLAY_ZSCORE_VS_MARGIN`

Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out? See its [catalog entry](../catalog/overlay.md#op-overlay_zscore_vs_margin).

#### `cells.value`

How far each cell sits from its row, column or grand margin figure, in standard deviations of the cell values across that slice (dividing by the number of cells). 0 means the cell equals the margin figure.

**Sign:**

- `+`: the value sits above its centre
- `-`: the value sits below its centre

**Caveats:**

- There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.
- The spread is that of the cells in the slice, not the sampling error of one cell, so a cell built from few rows can sit far out without being unusual.
- For counts or sums the margin is a total, so every cell sits far below it; the reading suits averages.

<a id="op-overlay_zscore_vs_pop"></a>

### `OVERLAY_ZSCORE_VS_POP`

Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset. See its [catalog entry](../catalog/overlay.md#op-overlay_zscore_vs_pop).

#### `summary.statistic`

In each series entry, for a categorical field: the category's share in the subset minus its share in the population, divided by the standard deviation of the population's shares across categories. For a numeric field: a histogram bin's centre against the population's mean, in population standard deviations.

**Sign:**

- `+`: the value sits above its centre
- `-`: the value sits below its centre

**Caveats:**

- There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.
- For categories the divisor is the spread of shares across categories, not a standard error: it does not shrink as the subset grows, and it changes with how many categories there are.
- For a numeric field it says where each bin sits on the population's scale, not how the subset differs; use OVERLAY_KS_VS_POP for that.

<a id="op-overlay_zscore_vs_rolling"></a>

### `OVERLAY_ZSCORE_VS_ROLLING`

How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods. See its [catalog entry](../catalog/overlay.md#op-overlay_zscore_vs_rolling).

#### `summary.statistic`

In each series entry: how far the point sits from the mean of up to W points before it, in sample standard deviations of those points. 0 means the point equals its recent average.

**Sign:**

- `+`: the value sits above its centre
- `-`: the value sits below its centre

**Caveats:**

- There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.
- A short window gives a jumpy spread, and a trend or seasonal pattern makes many points look unusual; points stay empty (NaN) until the window holds 2 values.
- Until W prior points exist the window is partial (from 2 points up), so the first values rest on a very unstable spread; treat them with caution.

<a id="op-overlay_zscore_vs_total"></a>

### `OVERLAY_ZSCORE_VS_TOTAL`

How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out? See its [catalog entry](../catalog/overlay.md#op-overlay_zscore_vs_total).

#### `summary.statistic`

In each series entry: how far the group's value sits from the average of all groups, in standard deviations of the group values (dividing by the number of groups). 0 means the group equals that average.

**Sign:**

- `+`: the value sits above its centre
- `-`: the value sits below its centre

**Caveats:**

- There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it.
- With N groups no value can sit further than sqrt(N - 1) from 0 (Shiffler's 1988 bound, (N - 1)/sqrt(N), restated for the divide-by-N spread Pulse uses), so with 5 groups nothing passes 2; compare values within a layer, not against a fixed cut-off.
- The spread is between groups, not between rows within a group.

<a id="op-overlay_z_cell"></a>

### `OVERLAY_Z_CELL`

Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? See its [catalog entry](../catalog/overlay.md#op-overlay_z_cell).

#### `cells.value`

**Caveats:**

- Each cell holds the two-sided z-test p-value comparing the target's mean in that cell with the reference's, read from the normal curve; with small cells it comes out too small.
- Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.
- Every cell is a separate test and Pulse reports the p-values raw: with many cells some small ones turn up by luck, so before flagging cells adjust for multiple comparisons: set multiplicity on the overlay and read payload.p_adjusted.
- The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).

<a id="op-overlay_z_vs_ref"></a>

### `OVERLAY_Z_VS_REF`

Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? See its [catalog entry](../catalog/overlay.md#op-overlay_z_vs_ref).

#### `summary.statistic`

**Caveats:**

- Despite the field name, each series entry's summary.statistic holds that group's two-sided z-test p-value, not a z value; Pulse does not emit z.
- It is read from the normal curve, so with small groups it comes out too small.
- Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder inputs (variance 1, n 2), and it then describes those placeholders, not your data.
- Every group is a separate test and Pulse reports the p-values raw: with many groups some small ones turn up by luck, so adjust for multiple comparisons: set multiplicity on the overlay and read summary.p_adjusted.
- The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is.

Follows the shared reading: see [Reading a p-value](test.md#shared-p-value).
