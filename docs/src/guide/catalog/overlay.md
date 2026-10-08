# Overlays

The overlays this instance offers: what each is for, the questions it answers and when to reach for something else. Operators are sorted by name; each name links to its detail block below.

| Operator | In plain words | Answers questions like | Level | Instead, when… |
|---|---|---|---|---|
| [`OVERLAY_CHISQ_COL`](#op-overlay_chisq_col) | Chi-square test per crosstab column: checks whether each column's spread across the rows departs from the table's overall row mix. | Which banner columns have an answer mix unlike the total? | intermediate | [`OVERLAY_CHISQ_ROW`](#op-overlay_chisq_row) when the groups are the rows rather than the columns.<br>[`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.<br>[`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out. |
| [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) | Chi-square test on a whole crosstab: checks whether the row and column categories are associated, with one p-value for the table. | Is preferred channel associated with age band in this crosstab? | intermediate | [`OVERLAY_CHISQ_ROW`](#op-overlay_chisq_row) when you want to know which rows have a mix unlike the overall one.<br>[`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out.<br>[`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when the table is 2x2 and some expected counts are below 5.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when you also want how strongly the two fields are associated (Cramer's V), from raw rows.<br>[`OVERLAY_CHISQ_VS_REF`](#op-overlay_chisq_vs_ref) when you compare this table's mix with another Compose request's table. |
| [`OVERLAY_CHISQ_ROW`](#op-overlay_chisq_row) | Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix. | Which regions have an answer mix unlike the overall mix? | intermediate | [`OVERLAY_CHISQ_COL`](#op-overlay_chisq_col) when the groups are the columns rather than the rows.<br>[`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.<br>[`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out. |
| [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) | Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix. | Does this segment's brand mix differ from the whole customer base's? | intermediate | [`OVERLAY_KS_VS_POP`](#op-overlay_ks_vs_pop) when the field is numeric.<br>[`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you want to see which categories are over- or under-represented rather than one test.<br>[`TEST_CHISQ`](test.md#op-test_chisq) when you compare two separate groups from raw rows rather than a subset with a population. |
| [`OVERLAY_CHISQ_VS_REF`](#op-overlay_chisq_vs_ref) | Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix. | Has the mix of answers in this wave shifted from last wave? | advanced | [`TEST_CHISQ`](test.md#op-test_chisq) when both results are samples of similar size and you want a test that allows for uncertainty in both: stack the rows, label each source and cross them.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want to see which cells moved rather than one overall test.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of each cell's change. |
| [`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) | Each point of an ordered series minus a chosen baseline point, in the value's own units: how much it has moved since then. | How many more orders a week do we get than in the launch week? | basic | [`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when you want a ratio rather than a gap.<br>[`OVERLAY_DELTA_VS_PRIOR`](#op-overlay_delta_vs_prior) when you want each point against the one before it.<br>[`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series. |
| [`OVERLAY_DELTA_VS_MARGIN`](#op-overlay_delta_vs_margin) | Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin. | How many points above or below the overall average rating is each segment, per question? | basic | [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want a ratio rather than a gap.<br>[`OVERLAY_ZSCORE_VS_MARGIN`](#op-overlay_zscore_vs_margin) when you want the gaps in standard deviations.<br>[`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want a test of whether a cell's count departs from what the margins predict. |
| [`OVERLAY_DELTA_VS_PRIOR`](#op-overlay_delta_vs_prior) | Each point of an ordered series minus the point before it, in the value's own units: how much it changed from the previous period. | How many more or fewer orders did we get than the month before? | basic | [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want the change as a ratio.<br>[`WIN_DELTA`](window.md#op-win_delta) when you want the change as a window column on the result rather than an overlay.<br>[`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when you want each point against a fixed starting point. |
| [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) | Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units. | How many points did each segment's score move since last wave? | intermediate | [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when you want a ratio rather than a gap.<br>[`OVERLAY_T_CELL`](#op-overlay_t_cell) when you want a test of whether the means differ.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want a test of whether the shares differ. |
| [`OVERLAY_DELTA_VS_SIBLING`](#op-overlay_delta_vs_sibling) | Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group. | How much more or less revenue than the flagship store does each store bring in? | basic | [`OVERLAY_INDEX_VS_SIBLING`](#op-overlay_index_vs_sibling) when you want a ratio rather than a gap.<br>[`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two groups' averages differ, from raw rows.<br>[`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when the reference is a fixed position in an ordered series. |
| [`OVERLAY_DELTA_VS_STAGE`](#op-overlay_delta_vs_stage) | A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units. | How many records or how much revenue does each filtering stage remove? | intermediate | [`OVERLAY_INDEX_VS_STAGE`](#op-overlay_index_vs_stage) when you want a ratio rather than a gap.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when the results come from separate requests rather than chain stages. |
| [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) | Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected. | Which answer-by-segment cells stand out in this banner table? | advanced | [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you compare the same cell across two Compose requests.<br>[`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when you only have one 2x2 table of raw rows. |
| [`OVERLAY_FORMULA`](#op-overlay_formula) | Computes a custom figure for every cell, group or total from an expression over the value and its margins, totals or prior point. | Can I show each cell as its gap from the row margin divided by the grand total? | advanced | [`ATTR_FORMULA`](attribute.md#op-attr_formula) when you need a derived field on every row before aggregation.<br>[`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when a built-in kind already computes it, such as an index against a margin.<br>[`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want a share of a row. |
| [`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) | Each point of an ordered series as an index value against a chosen baseline point (point / baseline x 100), e.g. growth since launch. | How have monthly sales grown relative to the launch month? | basic | [`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when you want the gap in the value's own units.<br>[`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each point against the one before it.<br>[`OVERLAY_YOY`](#op-overlay_yoy) when you want each period against the same period a year earlier.<br>[`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series. |
| [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) | Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index? | Which segments over-index on each answer compared with the total? | basic | [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the raw share (0 to 1) of the row.<br>[`OVERLAY_DELTA_VS_MARGIN`](#op-overlay_delta_vs_margin) when you want the gap in the cell's own units.<br>[`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you compare a facet subset with a comparison population. |
| [`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) | Each category's (or histogram bin's) share in a facet subset as an index value against its share in a comparison population (x 100). | Which brands over-index among young buyers compared with all buyers? | basic | [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when you want one test of whether the subset's mix differs.<br>[`OVERLAY_ZSCORE_VS_POP`](#op-overlay_zscore_vs_pop) when you want the differences on a standardized scale.<br>[`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when the comparison is within a crosstab rather than a facet. |
| [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) | Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change. | By how much did sales grow or shrink from each month to the next? | basic | [`OVERLAY_DELTA_VS_PRIOR`](#op-overlay_delta_vs_prior) when you want the change in the value's own units.<br>[`OVERLAY_YOY`](#op-overlay_yoy) when the data are seasonal and you want the same period a year earlier.<br>[`OVERLAY_INDEX_VS_ROLLING_MEAN`](#op-overlay_index_vs_rolling_mean) when you want each point against its recent average rather than one point. |
| [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) | Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100). | How does this wave's result per segment compare with last wave's, scaled to 100? | intermediate | [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the gap in the value's own units.<br>[`OVERLAY_PANEL_INDEX_VS_REF`](#op-overlay_panel_index_vs_ref) when you compare several targets with one reference.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want a test of whether the shares differ. |
| [`OVERLAY_INDEX_VS_ROLLING_MEAN`](#op-overlay_index_vs_rolling_mean) | Each point of an ordered series as an index value against the rolling mean of the previous W points: is this period above its recent run? | Is this week's volume above or below the average of the last four weeks? | intermediate | [`OVERLAY_ZSCORE_VS_ROLLING`](#op-overlay_zscore_vs_rolling) when you want the gap in standard deviations of the window.<br>[`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each point against just the one before it.<br>[`WIN_MOVING_AVG`](window.md#op-win_moving_avg) when you want the smoothed series itself as a column. |
| [`OVERLAY_INDEX_VS_SIBLING`](#op-overlay_index_vs_sibling) | Each group's value as an index value against one named group (group / sibling x 100), such as every store against the flagship. | How does each store's revenue compare with the flagship store's? | basic | [`OVERLAY_DELTA_VS_SIBLING`](#op-overlay_delta_vs_sibling) when you want the gap in the value's own units.<br>[`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when the reference is a fixed position in an ordered series, such as the first month.<br>[`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two groups' averages differ, from raw rows. |
| [`OVERLAY_INDEX_VS_STAGE`](#op-overlay_index_vs_stage) | Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100). | What share of the starting figure is left after each filtering stage, scaled to 100? | intermediate | [`OVERLAY_DELTA_VS_STAGE`](#op-overlay_delta_vs_stage) when you want the gap in the value's own units.<br>[`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when the results come from separate requests rather than chain stages. |
| [`OVERLAY_INDEX_VS_TOTAL`](#op-overlay_index_vs_total) | Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100). | How big is each region relative to the whole, on a 0 to 100 scale? | basic | [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want the raw share (0 to 1).<br>[`OVERLAY_ZSCORE_VS_TOTAL`](#op-overlay_zscore_vs_total) when you want each group against the average group.<br>[`OVERLAY_INDEX_VS_SIBLING`](#op-overlay_index_vs_sibling) when you want each group against one chosen group. |
| [`OVERLAY_KS_VS_POP`](#op-overlay_ks_vs_pop) | Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's. | Does this segment's spend distribution differ from all customers'? | advanced | [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when the field is categorical.<br>[`TEST_KS`](test.md#op-test_ks) when you have two separate groups of raw rows.<br>[`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you want to see where the shapes differ rather than one test. |
| [`OVERLAY_PAIRWISE_PROBIT_T`](#op-overlay_pairwise_probit_t) | Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use. | Which segments differ in share when tested the way our survey tool does? | advanced | [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when you want the standard test of two shares.<br>[`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are averages of a numeric measure. |
| [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) | Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share? | Which age bands differ from each other in the share choosing each brand? | advanced | [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are averages of a numeric measure rather than shares.<br>[`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when the groups are separate Compose requests.<br>[`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one overall test of whether the rows differ at all. |
| [`OVERLAY_PAIRWISE_TWO_MEANS_Z`](#op-overlay_pairwise_two_means_z) | Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? | With thousands of respondents per cell, which segments differ in average rating? | advanced | [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when any cell is small; the t-based version is more honest there.<br>[`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](#op-overlay_pairwise_weighted_two_means_z) when the cells are weighted averages.<br>[`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the cells are shares rather than averages. |
| [`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](#op-overlay_pairwise_weighted_two_means_z) | Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose. | Which weighted segments differ from each other in average satisfaction? | advanced | [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are unweighted averages.<br>[`AGG_WEIGHTED_MEAN`](aggregator.md#op-agg_weighted_mean) when you only need the weighted averages themselves. |
| [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) | Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean? | Which age bands differ from each other in average spend, product by product? | advanced | [`OVERLAY_PAIRWISE_TWO_MEANS_Z`](#op-overlay_pairwise_two_means_z) when every cell is large and you want the normal-curve version.<br>[`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](#op-overlay_pairwise_weighted_two_means_z) when the cells are weighted averages.<br>[`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the cells are shares rather than averages.<br>[`TEST_TUKEY_HSD`](test.md#op-test_tukey_hsd) when you compare groups from raw rows and want a post-test whose p-values come already adjusted across every pair, with no multiplicity block. |
| [`OVERLAY_PANEL_INDEX_VS_REF`](#op-overlay_panel_index_vs_ref) | Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100). | How do the last four waves each compare with the benchmark wave? | intermediate | [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when there is only one target.<br>[`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when you want tests between every pair of requests. |
| [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) | Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's? | Which answer shares changed between this wave and last wave? | intermediate | [`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when you compare three or more requests at once.<br>[`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the groups are rows or columns of one crosstab.<br>[`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when a cell has only a handful of successes or failures: stack both sources' rows and run an exact 2x2 test.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value. |
| [`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) | Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share? | Across four waves, which pairs of waves differ in this answer's share? | advanced | [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when there is only one target.<br>[`OVERLAY_PANEL_INDEX_VS_REF`](#op-overlay_panel_index_vs_ref) when you want index values against one reference rather than tests.<br>[`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the groups are rows or columns of one crosstab. |
| [`OVERLAY_RANK`](#op-overlay_rank) | Ranks each cell of a target Compose request's crosstab (1 = largest) within its row, its column or the whole table. | Which product is the top seller in each region? | basic | [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each cell's share of the whole.<br>[`OVERLAY_ZSCORE_VS_MARGIN`](#op-overlay_zscore_vs_margin) when you want how far each cell sits from its row or column.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the change from the reference request. |
| [`OVERLAY_SHARE_OF_COL`](#op-overlay_share_of_col) | Each crosstab cell as a share of its column margin (0 to 1): the mix of rows within each column, as a 100% stacked column shows it. | Within each channel, what share of orders comes from each region? | basic | [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the mix within each row.<br>[`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each cell's share of the whole table.<br>[`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want to test whether the column mixes differ. |
| [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) | Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it. | Within each region, what share of orders goes to each channel? | basic | [`OVERLAY_SHARE_OF_COL`](#op-overlay_share_of_col) when you want the mix within each column.<br>[`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want the same figure on a 0 to 100 index scale.<br>[`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want to test whether the row mixes differ. |
| [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) | Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up. | What share of all revenue does each region bring in? | basic | [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the share within each row of a crosstab.<br>[`OVERLAY_INDEX_VS_TOTAL`](#op-overlay_index_vs_total) when you want the same figure x 100 on a grouped result.<br>[`OVERLAY_ZSCORE_VS_TOTAL`](#op-overlay_zscore_vs_total) when you want each group's position against the average group. |
| [`OVERLAY_T_CELL`](#op-overlay_t_cell) | Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? | Which segment-by-product cells changed in average rating since last wave? | advanced | [`OVERLAY_Z_CELL`](#op-overlay_z_cell) when every cell is large and you want the normal-curve version.<br>[`OVERLAY_T_VS_REF`](#op-overlay_t_vs_ref) when the results are grouped series rather than crosstabs.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when the cells hold shares rather than averages.<br>[`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the groups are rows or columns of one crosstab. |
| [`OVERLAY_T_VS_REF`](#op-overlay_t_vs_ref) | Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? | Which regions' average spend changed between this quarter and last? | advanced | [`OVERLAY_Z_VS_REF`](#op-overlay_z_vs_ref) when every group is large and you want the normal-curve version.<br>[`OVERLAY_T_CELL`](#op-overlay_t_cell) when the results are crosstabs rather than grouped series.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value. |
| [`OVERLAY_YOY`](#op-overlay_yoy) | Each period of a date-grouped series as an index value against the same period one year earlier (x 100): year-over-year change. | How do this year's monthly sales compare with the same months last year? | basic | [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each period against the one before.<br>[`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when you want each period against one fixed starting period.<br>[`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series. |
| [`OVERLAY_ZSCORE_VS_MARGIN`](#op-overlay_zscore_vs_margin) | Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out? | Which segment-by-question averages sit unusually far from the question's overall average? | intermediate | [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want a test of whether a cell's count departs from what the margins predict.<br>[`OVERLAY_DELTA_VS_MARGIN`](#op-overlay_delta_vs_margin) when the gap in the cell's own units is easier to explain.<br>[`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want a ratio to the margin. |
| [`OVERLAY_ZSCORE_VS_POP`](#op-overlay_zscore_vs_pop) | Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset. | Which answers does this segment pick more or less often than the comparison population, relative to how much answer shares vary? | intermediate | [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when you want a test of whether the subset's category mix differs.<br>[`OVERLAY_KS_VS_POP`](#op-overlay_ks_vs_pop) when you want a test of whether a numeric distribution differs.<br>[`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when a ratio is easier to explain. |
| [`OVERLAY_ZSCORE_VS_ROLLING`](#op-overlay_zscore_vs_rolling) | How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods. | Which days had unusually high or low orders compared with the previous two weeks? | intermediate | [`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the whole series.<br>[`OVERLAY_INDEX_VS_ROLLING_MEAN`](#op-overlay_index_vs_rolling_mean) when you want the ratio to the rolling mean.<br>[`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want a standardized column for each row rather than per period. |
| [`OVERLAY_ZSCORE_VS_TOTAL`](#op-overlay_zscore_vs_total) | How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out? | Which stores' sales are unusually high or low compared with the other stores? | intermediate | [`TEST_ANOVA_WELCH`](test.md#op-test_anova_welch) when you want to test whether group averages differ, from raw rows.<br>[`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each group's share of the total.<br>[`OVERLAY_DELTA_VS_SIBLING`](#op-overlay_delta_vs_sibling) when you want each group against one chosen group. |
| [`OVERLAY_Z_CELL`](#op-overlay_z_cell) | Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's? | With large samples, which segment-by-question means moved since last wave? | advanced | [`OVERLAY_T_CELL`](#op-overlay_t_cell) when any cell is small; the t-based version is more honest there.<br>[`OVERLAY_Z_VS_REF`](#op-overlay_z_vs_ref) when the results are grouped series rather than crosstabs.<br>[`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when the cells hold shares rather than averages. |
| [`OVERLAY_Z_VS_REF`](#op-overlay_z_vs_ref) | Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's? | With large samples, which regions' average spend changed since last quarter? | advanced | [`OVERLAY_T_VS_REF`](#op-overlay_t_vs_ref) when any group is small; the t-based version is more honest there.<br>[`OVERLAY_Z_CELL`](#op-overlay_z_cell) when the results are crosstabs rather than grouped series.<br>[`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value. |

## Operators

<a id="op-overlay_chisq_col"></a>

### `OVERLAY_CHISQ_COL`

Chi-square test per crosstab column: checks whether each column's spread across the rows departs from the table's overall row mix.

**Level:** intermediate

**Questions it answers:**

- Which banner columns have an answer mix unlike the total?
- Which months have a spread of order types unlike the whole year?

**Use cases by domain:**

- *survey:* Flag the banner columns whose answer mix departs from the total column.
- *ops:* Flag the channels whose spread of order sizes departs from the overall spread.
- *science:* Flag the treatment arms whose outcome mix departs from the pooled mix.

**Assumptions:**

- Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, but sums, averages or weighted counts make the test meaningless.
- The null hypothesis is that the column's mix matches the overall row mix. That overall mix includes the column itself, so a large column pulls it toward itself: the statistic is smaller than a column-versus-rest test's by the factor (N - column total) / N, and the p-value is too large (conservative), most of all for big columns.
- Each column is a separate test, so with many columns some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.
- Every expected count should be about 5 or more; a warning flags columns where some are lower.

**Use something else:**

- [`OVERLAY_CHISQ_ROW`](#op-overlay_chisq_row) when the groups are the rows rather than the columns.
- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.
- [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value)

**Skill:** [`op-overlay-chisq-col`](../skills/op-overlay-chisq-col.md)

<a id="op-overlay_chisq_matrix"></a>

### `OVERLAY_CHISQ_MATRIX`

Chi-square test on a whole crosstab: checks whether the row and column categories are associated, with one p-value for the table.

**Level:** intermediate

**Questions it answers:**

- Is preferred channel associated with age band in this crosstab?
- Does the mix of answers differ across regions in the table?

**Use cases by domain:**

- *survey:* Check whether answer choice is associated with respondent segment in a banner table.
- *ops:* Check whether ticket category is associated with support site.
- *science:* Check whether outcome category is associated with treatment arm.

**Assumptions:**

- Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, but sums, averages or weighted counts make the test meaningless.
- The null hypothesis is that row and column categories are independent; the p-value comes from the chi-square approximation.
- Every expected count should be about 5 or more; a warning flags tables where some are lower.
- A small p-value says the table departs from independence, not how strongly or which cells drive it.

**Use something else:**

- [`OVERLAY_CHISQ_ROW`](#op-overlay_chisq_row) when you want to know which rows have a mix unlike the overall one.
- [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out.
- [`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when the table is 2x2 and some expected counts are below 5.
- [`TEST_CHISQ`](test.md#op-test_chisq) when you also want how strongly the two fields are associated (Cramer's V), from raw rows.
- [`OVERLAY_CHISQ_VS_REF`](#op-overlay_chisq_vs_ref) when you compare this table's mix with another Compose request's table.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`independence`](../glossary.md#term-independence), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size)

**Skill:** [`op-overlay-chisq-matrix`](../skills/op-overlay-chisq-matrix.md)

<a id="op-overlay_chisq_row"></a>

### `OVERLAY_CHISQ_ROW`

Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix.

**Level:** intermediate

**Questions it answers:**

- Which regions have an answer mix unlike the overall mix?
- Which product lines get a different spread of complaint types?

**Use cases by domain:**

- *survey:* Flag the segments whose answer mix departs from the total sample's mix.
- *ops:* Flag the sites whose spread of ticket types departs from the company-wide spread.
- *science:* Flag the sites whose outcome mix departs from the pooled mix.

**Assumptions:**

- Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, but sums, averages or weighted counts make the test meaningless.
- The null hypothesis is that the row's mix matches the overall column mix. That overall mix includes the row itself, so a large row pulls it toward itself: the statistic is smaller than a row-versus-rest test's by the factor (N - row total) / N, and the p-value is too large (conservative), most of all for big rows.
- Each row is a separate test, so with many rows some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.
- Every expected count should be about 5 or more; a warning flags rows where some are lower.

**Use something else:**

- [`OVERLAY_CHISQ_COL`](#op-overlay_chisq_col) when the groups are the columns rather than the rows.
- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.
- [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want to know which single cells stand out.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value)

**Skill:** [`op-overlay-chisq-row`](../skills/op-overlay-chisq-row.md)

<a id="op-overlay_chisq_vs_pop"></a>

### `OVERLAY_CHISQ_VS_POP`

Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix.

**Level:** intermediate

**Questions it answers:**

- Does this segment's brand mix differ from the whole customer base's?
- Is the subset's spread of ticket types unlike the population's?

**Use cases by domain:**

- *survey:* Check whether a filtered segment's answer mix departs from the full sample's.
- *ops:* Check whether one region's order-type mix departs from the national mix.
- *science:* Check whether a cohort's category mix departs from a reference population's.

**Assumptions:**

- The subset's figures are counts of independent rows.
- The null hypothesis is that the subset's mix matches the population's. The population mix is treated as known and fixed; when the subset is a large part of the population the two overlap and the p-value is only approximate.
- Only categories both sides show are compared, and the population's shares are rescaled over them, so population nulls and categories cut from a top-K listing do not distort the expected counts.
- With a very large subset even tiny differences in mix give small p-values; read an OVERLAY_INDEX_VS_POP layer on the same facet for how big each category's shift is.
- Every expected count should be about 5 or more; a warning flags layers where some are lower.

**Use something else:**

- [`OVERLAY_KS_VS_POP`](#op-overlay_ks_vs_pop) when the field is numeric.
- [`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you want to see which categories are over- or under-represented rather than one test.
- [`TEST_CHISQ`](test.md#op-test_chisq) when you compare two separate groups from raw rows rather than a subset with a population.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`independence`](../glossary.md#term-independence), [`index-value`](../glossary.md#term-index-value), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size)

**Skill:** [`op-overlay-chisq-vs-pop`](../skills/op-overlay-chisq-vs-pop.md)

<a id="op-overlay_chisq_vs_ref"></a>

### `OVERLAY_CHISQ_VS_REF`

Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix.

**Level:** advanced

**Questions it answers:**

- Has the mix of answers in this wave shifted from last wave?
- Does the test market's category mix differ from the control market's?

**Use cases by domain:**

- *survey:* Check whether this wave's answer mix departs from the previous wave's.
- *ops:* Check whether this quarter's ticket mix departs from last quarter's.
- *science:* Check whether a replication's category mix departs from the original study's.

**Assumptions:**

- Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, but sums, averages or weighted counts make the test meaningless.
- The null hypothesis is that the target's mix matches the reference's. The reference mix is treated as fixed expected shares and its own sampling uncertainty is ignored, so with a small reference the p-value is too small.
- Target and reference should be separate, independent sets of rows.
- The p-value is carried in the layer's scalar slot; the chi-square value is in summary.statistic.
- Every expected count should be about 5 or more; a warning flags layers where some are lower.

**Use something else:**

- [`TEST_CHISQ`](test.md#op-test_chisq) when both results are samples of similar size and you want a test that allows for uncertainty in both: stack the rows, label each source and cross them.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want to see which cells moved rather than one overall test.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of each cell's change.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`chi-square`](../glossary.md#term-chi-square), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`independence`](../glossary.md#term-independence), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value)

**Skill:** [`op-overlay-chisq-vs-ref`](../skills/op-overlay-chisq-vs-ref.md)

<a id="op-overlay_delta_vs_baseline"></a>

### `OVERLAY_DELTA_VS_BASELINE`

Each point of an ordered series minus a chosen baseline point, in the value's own units: how much it has moved since then.

**Level:** basic

**Questions it answers:**

- How many more orders a week do we get than in the launch week?
- How many points has satisfaction moved since the first wave?

**Use cases by domain:**

- *survey:* Show each wave's score as a gap from the first wave.
- *ops:* Show each week's volume as a gap from a reference week.
- *science:* Show each time point as a change from the pre-treatment measurement.

**Assumptions:**

- The baseline is a position in the host's order, so the series must be ordered (for example by GROUP_DATE).
- When the values are percentages the gap is in percentage points, not percent.

**Use something else:**

- [`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when you want a ratio rather than a gap.
- [`OVERLAY_DELTA_VS_PRIOR`](#op-overlay_delta_vs_prior) when you want each point against the one before it.
- [`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-baseline`](../skills/op-overlay-delta-vs-baseline.md)

<a id="op-overlay_delta_vs_margin"></a>

### `OVERLAY_DELTA_VS_MARGIN`

Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin.

**Level:** basic

**Questions it answers:**

- How many points above or below the overall average rating is each segment, per question?
- Which store-by-month cells sit furthest from their store's overall average?

**Use cases by domain:**

- *survey:* Show each segment's mean rating as a gap from the total-sample mean.
- *ops:* Show each site's average cycle time as a gap from the company average.
- *science:* Show each subgroup mean as a gap from the pooled mean.

**Assumptions:**

- The margin is the host's own margin figure: a total for counts and sums (so every cell sits below it), an overall average for means. It reads most naturally with averages or shares.
- The gap keeps the cell's units; when cells are percentages it is in percentage points.

**Use something else:**

- [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want a ratio rather than a gap.
- [`OVERLAY_ZSCORE_VS_MARGIN`](#op-overlay_zscore_vs_margin) when you want the gaps in standard deviations.
- [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want a test of whether a cell's count departs from what the margins predict.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`margin`](../glossary.md#term-margin), [`mean`](../glossary.md#term-mean), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-margin`](../skills/op-overlay-delta-vs-margin.md)

<a id="op-overlay_delta_vs_prior"></a>

### `OVERLAY_DELTA_VS_PRIOR`

Each point of an ordered series minus the point before it, in the value's own units: how much it changed from the previous period.

**Level:** basic

**Questions it answers:**

- How many more or fewer orders did we get than the month before?
- How many points did satisfaction move from each wave to the next?

**Use cases by domain:**

- *survey:* Wave-on-wave change of a tracker metric in points.
- *ops:* Month-on-month change in ticket volume.
- *science:* Step-to-step change of a repeated measurement.

**Assumptions:**

- The series must be ordered. The first point has no prior and gets no value; a missing point is skipped and the next one compares with the last present point.
- When the values are percentages the change is in percentage points, not percent.

**Use something else:**

- [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want the change as a ratio.
- [`WIN_DELTA`](window.md#op-win_delta) when you want the change as a window column on the result rather than an overlay.
- [`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when you want each point against a fixed starting point.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-prior`](../skills/op-overlay-delta-vs-prior.md)

<a id="op-overlay_delta_vs_ref"></a>

### `OVERLAY_DELTA_VS_REF`

Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units.

**Level:** intermediate

**Questions it answers:**

- How many points did each segment's score move since last wave?
- How much more or less did each region sell this year than last?

**Use cases by domain:**

- *survey:* Show this wave's table as point changes from last wave.
- *ops:* Show this period's site figures as changes from the prior period.
- *science:* Show a treatment cohort's subgroup means as gaps from the control cohort's.

**Assumptions:**

- Both requests must share a schema and line up on the same keys; a coordinate missing from the reference gets no value and a warning.
- When the values are percentages the gap is in percentage points, not percent.

**Use something else:**

- [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when you want a ratio rather than a gap.
- [`OVERLAY_T_CELL`](#op-overlay_t_cell) when you want a test of whether the means differ.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want a test of whether the shares differ.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-ref`](../skills/op-overlay-delta-vs-ref.md)

<a id="op-overlay_delta_vs_sibling"></a>

### `OVERLAY_DELTA_VS_SIBLING`

Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group.

**Level:** basic

**Questions it answers:**

- How much more or less revenue than the flagship store does each store bring in?
- How many points above or below the control arm is each arm's average?

**Use cases by domain:**

- *survey:* Show every segment's mean rating as a gap from a reference segment.
- *ops:* Show every site's cost as a gap from the best-run site.
- *science:* Show every arm's mean as a gap from the control arm.

**Assumptions:**

- The named group must exist in the result; an unknown group gives no values and a warning.
- When the values are percentages the gap is in percentage points, not percent.

**Use something else:**

- [`OVERLAY_INDEX_VS_SIBLING`](#op-overlay_index_vs_sibling) when you want a ratio rather than a gap.
- [`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two groups' averages differ, from raw rows.
- [`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when the reference is a fixed position in an ordered series.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-sibling`](../skills/op-overlay-delta-vs-sibling.md)

<a id="op-overlay_delta_vs_stage"></a>

### `OVERLAY_DELTA_VS_STAGE`

A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units.

**Level:** intermediate

**Questions it answers:**

- How many records or how much revenue does each filtering stage remove?
- How far does the refined stage's figure move from the first stage's?

**Use cases by domain:**

- *survey:* Show how cleaning the sample moves each figure.
- *ops:* Show how much volume each funnel stage drops.
- *harness:* Diff a chain's final stage against its source stage in one response.

**Assumptions:**

- Both stages must have the same result shape; otherwise the layer is empty with a warning.
- When the values are percentages the gap is in percentage points, not percent.

**Use something else:**

- [`OVERLAY_INDEX_VS_STAGE`](#op-overlay_index_vs_stage) when you want a ratio rather than a gap.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when the results come from separate requests rather than chain stages.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`percentage-point`](../glossary.md#term-percentage-point)

**Skill:** [`op-overlay-delta-vs-stage`](../skills/op-overlay-delta-vs-stage.md)

<a id="op-overlay_fisher_exact_cell"></a>

### `OVERLAY_FISHER_EXACT_CELL`

Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected.

**Level:** advanced

**Questions it answers:**

- Which answer-by-segment cells stand out in this banner table?
- In a small table, which cells hold more cases than the margins predict?

**Use cases by domain:**

- *survey:* Flag the cells of a banner table where a segment picks an answer more or less often than the rest.
- *ops:* Flag the site-by-fault cells that occur more or less often than the totals predict.
- *science:* Flag the arm-by-outcome cells of a small table without relying on a large-sample approximation.

**Assumptions:**

- Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, but sums, averages or weighted counts make the test meaningless.
- Each cell is tested as its own 2x2 table: this row versus the rest, by this column versus the rest. The null hypothesis is that the two are independent; the p-value is two-sided.
- Every cell is a separate test and the tables overlap, so with many cells some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.
- It stays valid with small counts, which makes it the backstop when chi-square expected counts are low; the low-count warning is advisory only.

**Use something else:**

- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one test for the whole table.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you compare the same cell across two Compose requests.
- [`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when you only have one 2x2 table of raw rows.

**Glossary:** [`chi-square`](../glossary.md#term-chi-square), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`exact-test`](../glossary.md#term-exact-test), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`margin`](../glossary.md#term-margin), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-fisher-exact-cell`](../skills/op-overlay-fisher-exact-cell.md)

<a id="op-overlay_formula"></a>

### `OVERLAY_FORMULA`

Computes a custom figure for every cell, group or total from an expression over the value and its margins, totals or prior point.

**Level:** advanced

**Questions it answers:**

- Can I show each cell as its gap from the row margin divided by the grand total?
- Can I flag each month whose value is more than 10% above the prior month?

**Use cases by domain:**

- *survey:* A house-style index that no built-in kind computes.
- *ops:* A custom ratio of each cell to its margins for a dashboard.
- *harness:* Prototype a new overlay before registering it as an extension kind.

**Assumptions:**

- Pulse checks the variable names, not the statistics: a formula that divides by a margin or compares values is only as sound as you make it.
- The variables depend on the host shape (cell and margins for a crosstab, value, total and prior for a series, value for a total).

**Use something else:**

- [`ATTR_FORMULA`](attribute.md#op-attr_formula) when you need a derived field on every row before aggregation.
- [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when a built-in kind already computes it, such as an index against a margin.
- [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want a share of a row.

**Glossary:** [`margin`](../glossary.md#term-margin)

**Skill:** [`op-overlay-formula`](../skills/op-overlay-formula.md)

<a id="op-overlay_index_vs_baseline"></a>

### `OVERLAY_INDEX_VS_BASELINE`

Each point of an ordered series as an index value against a chosen baseline point (point / baseline x 100), e.g. growth since launch.

**Level:** basic

**Questions it answers:**

- How have monthly sales grown relative to the launch month?
- How does each wave's score compare with the first wave, scaled to 100?

**Use cases by domain:**

- *survey:* Track a tracker metric relative to the first wave.
- *ops:* Track weekly volume relative to a reference week.
- *science:* Express each time point relative to the pre-treatment measurement.

**Assumptions:**

- The baseline is a position in the host's order, so the series must be ordered (for example by GROUP_DATE).
- A zero baseline gives no values and a warning; a small baseline makes every index swing widely.

**Use something else:**

- [`OVERLAY_DELTA_VS_BASELINE`](#op-overlay_delta_vs_baseline) when you want the gap in the value's own units.
- [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each point against the one before it.
- [`OVERLAY_YOY`](#op-overlay_yoy) when you want each period against the same period a year earlier.
- [`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-baseline`](../skills/op-overlay-index-vs-baseline.md)

<a id="op-overlay_index_vs_margin"></a>

### `OVERLAY_INDEX_VS_MARGIN`

Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index?

**Level:** basic

**Questions it answers:**

- Which segments over-index on each answer compared with the total?
- Which product-by-region cells are well above their region's average?

**Use cases by domain:**

- *survey:* Index each segment's mean rating against the total-sample mean.
- *ops:* Index each site's average cost against the company-wide average.
- *science:* Index each subgroup mean against the pooled mean.

**Assumptions:**

- The margin is the host's own margin figure. For averages 100 means the same as the overall average; for counts and sums the margin is a total, so the index is the cell's share x 100, not a comparison with a typical cell.
- A zero margin gives no value and a warning; a small margin makes the index swing widely.

**Use something else:**

- [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the raw share (0 to 1) of the row.
- [`OVERLAY_DELTA_VS_MARGIN`](#op-overlay_delta_vs_margin) when you want the gap in the cell's own units.
- [`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you compare a facet subset with a comparison population.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`index-value`](../glossary.md#term-index-value), [`margin`](../glossary.md#term-margin)

**Skill:** [`op-overlay-index-vs-margin`](../skills/op-overlay-index-vs-margin.md)

<a id="op-overlay_index_vs_pop"></a>

### `OVERLAY_INDEX_VS_POP`

Each category's (or histogram bin's) share in a facet subset as an index value against its share in a comparison population (x 100).

**Level:** basic

**Questions it answers:**

- Which brands over-index among young buyers compared with all buyers?
- Which price bands are over-represented in this segment?

**Use cases by domain:**

- *survey:* Profile a segment: which answers it over- or under-indexes on against the full sample.
- *ops:* Show which product categories a region buys more or less of than the whole business.
- *science:* Show which categories are over-represented in a cohort against the reference population.

**Assumptions:**

- Small categories give unstable index values: a handful of rows can produce 300 or 20, so check the counts behind them.
- A category absent from the population gets no value and a warning; a numeric field needs IncludeHistogram for bins.

**Use something else:**

- [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when you want one test of whether the subset's mix differs.
- [`OVERLAY_ZSCORE_VS_POP`](#op-overlay_zscore_vs_pop) when you want the differences on a standardized scale.
- [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when the comparison is within a crosstab rather than a facet.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value), [`sample-size`](../glossary.md#term-sample-size)

**Skill:** [`op-overlay-index-vs-pop`](../skills/op-overlay-index-vs-pop.md)

<a id="op-overlay_index_vs_prior"></a>

### `OVERLAY_INDEX_VS_PRIOR`

Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change.

**Level:** basic

**Questions it answers:**

- By how much did sales grow or shrink from each month to the next?
- Is this week's volume above or below last week's, scaled to 100?

**Use cases by domain:**

- *survey:* Wave-on-wave change of a tracker metric.
- *ops:* Month-on-month growth of orders.
- *science:* Step-to-step change of a repeated measurement.

**Assumptions:**

- The series must be ordered. The first point has no prior and gets no value; a missing point is skipped and the next one compares with the last present point, so a gap can span more than one period.
- A zero prior gives no value and a warning.

**Use something else:**

- [`OVERLAY_DELTA_VS_PRIOR`](#op-overlay_delta_vs_prior) when you want the change in the value's own units.
- [`OVERLAY_YOY`](#op-overlay_yoy) when the data are seasonal and you want the same period a year earlier.
- [`OVERLAY_INDEX_VS_ROLLING_MEAN`](#op-overlay_index_vs_rolling_mean) when you want each point against its recent average rather than one point.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-prior`](../skills/op-overlay-index-vs-prior.md)

<a id="op-overlay_index_vs_ref"></a>

### `OVERLAY_INDEX_VS_REF`

Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100).

**Level:** intermediate

**Questions it answers:**

- How does this wave's result per segment compare with last wave's, scaled to 100?
- How does each region's revenue this year compare with last year's?

**Use cases by domain:**

- *survey:* Index this wave's table against last wave's.
- *ops:* Index this period's site figures against the prior period's.
- *science:* Index a treatment cohort's subgroup means against the control cohort's.

**Assumptions:**

- Both requests must share a schema and line up on the same keys; a coordinate missing from the reference gets no value and a warning.
- A zero reference value gives no value; a small one makes the index swing widely. params.scale changes the 100.

**Use something else:**

- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the gap in the value's own units.
- [`OVERLAY_PANEL_INDEX_VS_REF`](#op-overlay_panel_index_vs_ref) when you compare several targets with one reference.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when you want a test of whether the shares differ.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-ref`](../skills/op-overlay-index-vs-ref.md)

<a id="op-overlay_index_vs_rolling_mean"></a>

### `OVERLAY_INDEX_VS_ROLLING_MEAN`

Each point of an ordered series as an index value against the rolling mean of the previous W points: is this period above its recent run?

**Level:** intermediate

**Questions it answers:**

- Is this week's volume above or below the average of the last four weeks?
- Which days ran well above their recent average?

**Use cases by domain:**

- *survey:* Compare each wave with the average of the last few waves.
- *ops:* Compare each day's orders with the average of the previous week.
- *science:* Compare each reading with the average of the preceding readings.

**Assumptions:**

- The series must be ordered and params.window set. The first W points get no value while the window fills; a missing point does not advance the window.
- A short window reacts fast but is jumpy; a zero rolling mean gives no value and a warning.

**Use something else:**

- [`OVERLAY_ZSCORE_VS_ROLLING`](#op-overlay_zscore_vs_rolling) when you want the gap in standard deviations of the window.
- [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each point against just the one before it.
- [`WIN_MOVING_AVG`](window.md#op-win_moving_avg) when you want the smoothed series itself as a column.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value), [`rolling-mean`](../glossary.md#term-rolling-mean)

**Skill:** [`op-overlay-index-vs-rolling-mean`](../skills/op-overlay-index-vs-rolling-mean.md)

<a id="op-overlay_index_vs_sibling"></a>

### `OVERLAY_INDEX_VS_SIBLING`

Each group's value as an index value against one named group (group / sibling x 100), such as every store against the flagship.

**Level:** basic

**Questions it answers:**

- How does each store's revenue compare with the flagship store's?
- How does each region's average rating compare with the home region's?

**Use cases by domain:**

- *survey:* Index every segment against a chosen reference segment.
- *ops:* Index every site against the best-run site.
- *science:* Index every arm's mean against the control arm.

**Assumptions:**

- The named group must exist in the result; an unknown group gives no values and a warning.
- A zero or small reference value makes every index swing widely; a zero one gives no values.

**Use something else:**

- [`OVERLAY_DELTA_VS_SIBLING`](#op-overlay_delta_vs_sibling) when you want the gap in the value's own units.
- [`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when the reference is a fixed position in an ordered series, such as the first month.
- [`TEST_WELCH`](test.md#op-test_welch) when you want to test whether two groups' averages differ, from raw rows.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-sibling`](../skills/op-overlay-index-vs-sibling.md)

<a id="op-overlay_index_vs_stage"></a>

### `OVERLAY_INDEX_VS_STAGE`

Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100).

**Level:** intermediate

**Questions it answers:**

- What share of the starting figure is left after each filtering stage, scaled to 100?
- How does the refined stage's result compare with the first stage's?

**Use cases by domain:**

- *survey:* Show how a cleaned sample's figures compare with the raw sample's.
- *ops:* Show how much of the starting volume each funnel stage keeps.
- *harness:* Compare a chain's final stage with its source stage in one response.

**Assumptions:**

- Both stages must have the same result shape; otherwise the layer is empty with a warning.
- A zero reference value gives no value and a warning.

**Use something else:**

- [`OVERLAY_DELTA_VS_STAGE`](#op-overlay_delta_vs_stage) when you want the gap in the value's own units.
- [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when the results come from separate requests rather than chain stages.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-stage`](../skills/op-overlay-index-vs-stage.md)

<a id="op-overlay_index_vs_total"></a>

### `OVERLAY_INDEX_VS_TOTAL`

Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100).

**Level:** basic

**Questions it answers:**

- How big is each region relative to the whole, on a 0 to 100 scale?
- What part of total sales, scaled to 100, does each channel make up?

**Use cases by domain:**

- *survey:* Each segment's share of all responses, scaled to 100.
- *ops:* Each product line's share of total revenue, scaled to 100.

**Assumptions:**

- 100 means the group equals the whole total, not the typical group, so values are usually far below 100.
- It makes sense only when the value adds up across groups, such as counts or sums. A zero total gives no value and a warning.

**Use something else:**

- [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want the raw share (0 to 1).
- [`OVERLAY_ZSCORE_VS_TOTAL`](#op-overlay_zscore_vs_total) when you want each group against the average group.
- [`OVERLAY_INDEX_VS_SIBLING`](#op-overlay_index_vs_sibling) when you want each group against one chosen group.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-index-vs-total`](../skills/op-overlay-index-vs-total.md)

<a id="op-overlay_ks_vs_pop"></a>

### `OVERLAY_KS_VS_POP`

Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's.

**Level:** advanced

**Questions it answers:**

- Does this segment's spend distribution differ from all customers'?
- Has the shape of response times in this region drifted from the fleet-wide shape?

**Use cases by domain:**

- *survey:* Check whether a segment's score distribution departs from the full sample's.
- *ops:* Check whether one site's latency distribution departs from the fleet's.
- *science:* Check whether a cohort's measurement distribution departs from a reference population's.

**Assumptions:**

- The curves are rebuilt from histograms or percentiles, not raw values: request matching histograms (or percentiles) on both arms. With matching histograms, coarse bins understate the gap and make the test conservative. When Pulse falls back to percentiles it interpolates between them, so D can come out too large or too small and the p-value has no guaranteed direction; prefer matching histograms.
- It treats subset and population as two independent samples. When the subset is part of the population they overlap: D shrinks by the subset's share of the population, so the p-value is too large (conservative), badly so when the subset is a big part of the population. Compare against the rest of the population instead when you can.
- It compares two observed distributions; it is not a test against a named distribution with parameters estimated from the data, which would need the Lilliefors correction.
- With few rows it has low power; with very large groups even trivial differences in shape give small p-values.
- The null hypothesis is that both share one distribution; the p-value is a two-sided large-sample approximation.

**Use something else:**

- [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when the field is categorical.
- [`TEST_KS`](test.md#op-test_ks) when you have two separate groups of raw rows.
- [`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when you want to see where the shapes differ rather than one test.

**Glossary:** [`goodness-of-fit`](../glossary.md#term-goodness-of-fit), [`independence`](../glossary.md#term-independence), [`non-parametric`](../glossary.md#term-non-parametric), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`percentile`](../glossary.md#term-percentile), [`statistical-power`](../glossary.md#term-statistical-power), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-ks-vs-pop`](../skills/op-overlay-ks-vs-pop.md)

<a id="op-overlay_pairwise_probit_t"></a>

### `OVERLAY_PAIRWISE_PROBIT_T`

Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use.

**Level:** advanced

**Questions it answers:**

- Which segments differ in share when tested the way our survey tool does?
- Do the probit-scale shares of each region differ from each other?

**Use cases by domain:**

- *survey:* Reproduce the pairwise share tests of a survey tool that works on the probit scale.
- *ops:* Match a legacy report's pairwise rate tests that used the probit transform.

**Assumptions:**

- It treats each probit value as having spread 1/sqrt(n). The true spread is at least about 1.25/sqrt(n) and larger near 0% or 100%, so its p-values come out too small at every share. Use it only to reproduce a tool that applies it, never as evidence on its own.
- Shares of exactly 0 or 1 are clipped to 1e-10 from the edge, which maps them to about ±6.4 on the probit scale. Any pair involving a 0% or 100% share then gets a huge t and a near-zero p-value whatever the n; treat those pairs as unreadable.
- The two groups in each pair are independent samples. The null hypothesis is equal probit values; each p-value is two-sided, from a t distribution with n_i + n_j - 2 degrees of freedom.
- Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when you want the standard test of two shares.
- [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are averages of a numeric measure.

**Glossary:** [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`probit`](../glossary.md#term-probit), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-pairwise-probit-t`](../skills/op-overlay-pairwise-probit-t.md)

<a id="op-overlay_pairwise_prop_z"></a>

### `OVERLAY_PAIRWISE_PROP_Z`

Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share?

**Level:** advanced

**Questions it answers:**

- Which age bands differ from each other in the share choosing each brand?
- Which pairs of regions differ in their share of late deliveries?

**Use cases by domain:**

- *survey:* Letter-test banner columns: which segments differ in the share giving each answer.
- *ops:* Compare late-delivery shares between every pair of depots.
- *science:* Compare response shares between every pair of dose groups.

**Assumptions:**

- The two groups in each pair are independent samples; a multi-select (set) grouper can put one row in both, which breaks that.
- Choose n_source to match how the cell value was made and p_source to match a percentage or a proportion; a mismatch silently skips every pair.
- The null hypothesis for each pair is that the two shares are equal; each p-value is two-sided and needs roughly 10 successes and 10 failures per side.
- Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are averages of a numeric measure rather than shares.
- [`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when the groups are separate Compose requests.
- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want one overall test of whether the rows differ at all.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-pairwise-prop-z`](../skills/op-overlay-pairwise-prop-z.md)

<a id="op-overlay_pairwise_two_means_z"></a>

### `OVERLAY_PAIRWISE_TWO_MEANS_Z`

Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?

**Level:** advanced

**Questions it answers:**

- With thousands of respondents per cell, which segments differ in average rating?
- Which pairs of high-volume stores differ in average basket size?

**Use cases by domain:**

- *survey:* Letter-test large banner columns on a mean rating where reporting calls for z.
- *ops:* Compare average order value between every pair of high-traffic channels.
- *science:* Compare means between every pair of large groups where the t and normal tails agree.

**Assumptions:**

- Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n; any other cell aggregator is refused.
- The two groups in each pair are independent samples. It reads p from the normal curve, so with small cells the p-values come out too small.
- The null hypothesis for each pair is equal means; each p-value is two-sided.
- Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when any cell is small; the t-based version is more honest there.
- [`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](#op-overlay_pairwise_weighted_two_means_z) when the cells are weighted averages.
- [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the cells are shares rather than averages.

**Glossary:** [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`standard-error`](../glossary.md#term-standard-error), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-pairwise-two-means-z`](../skills/op-overlay-pairwise-two-means-z.md)

<a id="op-overlay_pairwise_weighted_two_means_z"></a>

### `OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`

Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose.

**Level:** advanced

**Questions it answers:**

- Which weighted segments differ from each other in average satisfaction?
- Which regions differ in weighted average spend once survey weights are applied?

**Use cases by domain:**

- *survey:* Letter-test banner columns on a weighted mean rating.
- *ops:* Compare volume-weighted average prices between every pair of suppliers.
- *science:* Compare design-weighted means between every pair of strata.

**Assumptions:**

- Cells must use AGG_WEIGHTED_MEAN or a weighted AGG_AVERAGE; n_basis is required. With weights the sum of weights is the sample size, which suits weights that count repeated rows and is refused when the cell carries sampling weights; kish uses the effective sample size and suits survey weights.
- It accounts for weighting only, not for clustering or other design effects, so with such designs the p-values come out too small.
- The two groups in each pair are independent samples. The null hypothesis for each pair is equal weighted means; each p-value is two-sided from the normal curve.
- Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the cells are unweighted averages.
- [`AGG_WEIGHTED_MEAN`](aggregator.md#op-agg_weighted_mean) when you only need the weighted averages themselves.

**Glossary:** [`effective-sample-size`](../glossary.md#term-effective-sample-size), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`two-tailed`](../glossary.md#term-two-tailed), [`weighting`](../glossary.md#term-weighting)

**Skill:** [`op-overlay-pairwise-weighted-two-means-z`](../skills/op-overlay-pairwise-weighted-two-means-z.md)

<a id="op-overlay_pairwise_welch_t"></a>

### `OVERLAY_PAIRWISE_WELCH_T`

Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?

**Level:** advanced

**Questions it answers:**

- Which age bands differ from each other in average spend, product by product?
- Which pairs of depots differ in average delivery time per month?

**Use cases by domain:**

- *survey:* Letter-test banner columns on a mean rating.
- *ops:* Compare average handling time between every pair of teams, per queue.
- *science:* Compare mean outcomes between every pair of dose groups, per site.

**Assumptions:**

- Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n; any other cell aggregator is refused.
- The two groups in each pair are independent samples. Equal variances are not assumed, and each mean should be roughly normal: safe for large cells, risky for small skewed ones.
- The null hypothesis for each pair is equal means; each p-value is two-sided.
- Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PAIRWISE_TWO_MEANS_Z`](#op-overlay_pairwise_two_means_z) when every cell is large and you want the normal-curve version.
- [`OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z`](#op-overlay_pairwise_weighted_two_means_z) when the cells are weighted averages.
- [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the cells are shares rather than averages.
- [`TEST_TUKEY_HSD`](test.md#op-test_tukey_hsd) when you compare groups from raw rows and want a post-test whose p-values come already adjusted across every pair, with no multiplicity block.

**Glossary:** [`degrees-of-freedom`](../glossary.md#term-degrees-of-freedom), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-overlay-pairwise-welch-t`](../skills/op-overlay-pairwise-welch-t.md)

<a id="op-overlay_panel_index_vs_ref"></a>

### `OVERLAY_PANEL_INDEX_VS_REF`

Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100).

**Level:** intermediate

**Questions it answers:**

- How do the last four waves each compare with the benchmark wave?
- How does each market compare with the reference market, cell by cell?

**Use cases by domain:**

- *survey:* Index every wave of a tracker against a benchmark wave.
- *ops:* Index every region's table against the national table.
- *science:* Index several treatment cohorts against one control cohort.

**Assumptions:**

- Every target must share a schema with the reference and line up on the same keys; OverlayOptions.MaxPanelTargets caps the number of targets.
- A zero reference value gives no value; a small one makes the index swing widely.

**Use something else:**

- [`OVERLAY_INDEX_VS_REF`](#op-overlay_index_vs_ref) when there is only one target.
- [`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when you want tests between every pair of requests.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-panel-index-vs-ref`](../skills/op-overlay-panel-index-vs-ref.md)

<a id="op-overlay_prop_z_cell"></a>

### `OVERLAY_PROP_Z_CELL`

Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's?

**Level:** intermediate

**Questions it answers:**

- Which answer shares changed between this wave and last wave?
- Where does the test market's share differ from the control market's?

**Use cases by domain:**

- *survey:* Flag the answer shares that moved between two survey waves.
- *ops:* Flag the defect rates per line and shift that differ between two plants.
- *science:* Compare response rates per subgroup between a treatment and a control cohort.

**Assumptions:**

- Cell values must be counts and each row margin is that row's sample size: the share tested is cell / row margin. A cell whose row margin is missing or zero on either side gets no test (NaN, with a warning).
- Target and reference are separate, independent samples; rows that appear in both make the p-value wrong.
- The null hypothesis is that the two shares are equal. The normal approximation needs roughly 10 successes and 10 failures on each side; the p-value is two-sided.
- Every cell is a separate test, so with many cells some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PROP_Z_PANEL`](#op-overlay_prop_z_panel) when you compare three or more requests at once.
- [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the groups are rows or columns of one crosstab.
- [`TEST_FISHER_EXACT`](test.md#op-test_fisher_exact) when a cell has only a handful of successes or failures: stack both sources' rows and run an exact 2x2 test.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`margin`](../glossary.md#term-margin), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`standard-error`](../glossary.md#term-standard-error), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-prop-z-cell`](../skills/op-overlay-prop-z-cell.md)

<a id="op-overlay_prop_z_panel"></a>

### `OVERLAY_PROP_Z_PANEL`

Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share?

**Level:** advanced

**Questions it answers:**

- Across four waves, which pairs of waves differ in this answer's share?
- Which of five markets differ from each other in brand share?

**Use cases by domain:**

- *survey:* Compare answer shares between every pair of waves in a tracking study.
- *ops:* Compare defect rates between every pair of plants.
- *science:* Compare response rates between every pair of study sites.

**Assumptions:**

- Cell values must be counts; by default each slot's row margin is its sample size (n_source picks another). A pair involving a slot whose row margin is missing or zero gets no test (NaN, with a warning).
- The slots are separate, independent samples; rows that appear in more than one slot make the p-values wrong.
- The null hypothesis for each pair is that the two shares are equal; each p-value is two-sided and needs roughly 10 successes and 10 failures per side.
- Each cell gets one test per pair of slots, so the count grows fast. Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, so adjust for multiple comparisons: set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when there is only one target.
- [`OVERLAY_PANEL_INDEX_VS_REF`](#op-overlay_panel_index_vs_ref) when you want index values against one reference rather than tests.
- [`OVERLAY_PAIRWISE_PROP_Z`](#op-overlay_pairwise_prop_z) when the groups are rows or columns of one crosstab.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`margin`](../glossary.md#term-margin), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`sample-size`](../glossary.md#term-sample-size), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-prop-z-panel`](../skills/op-overlay-prop-z-panel.md)

<a id="op-overlay_rank"></a>

### `OVERLAY_RANK`

Ranks each cell of a target Compose request's crosstab (1 = largest) within its row, its column or the whole table.

**Level:** basic

**Questions it answers:**

- Which product is the top seller in each region?
- Where does each answer rank within its segment?

**Use cases by domain:**

- *survey:* Rank answers within each segment for a top-box summary.
- *ops:* Rank products within each region by revenue.
- *science:* Rank conditions within each site by mean outcome.

**Assumptions:**

- A rank hides how far apart values are: first and second may be nearly equal or far apart.
- Tied values share the average rank, and missing cells are left out, so ranks cover only the cells present.
- The reference request only anchors alignment; its values are not used.

**Use something else:**

- [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each cell's share of the whole.
- [`OVERLAY_ZSCORE_VS_MARGIN`](#op-overlay_zscore_vs_margin) when you want how far each cell sits from its row or column.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the change from the reference request.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`rank`](../glossary.md#term-rank), [`ties`](../glossary.md#term-ties)

**Skill:** [`op-overlay-rank`](../skills/op-overlay-rank.md)

<a id="op-overlay_share_of_col"></a>

### `OVERLAY_SHARE_OF_COL`

Each crosstab cell as a share of its column margin (0 to 1): the mix of rows within each column, as a 100% stacked column shows it.

**Level:** basic

**Questions it answers:**

- Within each channel, what share of orders comes from each region?
- In each banner column, how are answers split across the options?

**Use cases by domain:**

- *survey:* Column percentages: how each banner column's answers split across the options.
- *ops:* How each month's tickets split across categories.
- *science:* How each outcome category splits across arms.

**Assumptions:**

- Shares add to 1 down a column only when the cell aggregator adds up, such as counts or sums; for averages the ratio is not a share.
- A zero column margin gives no value and a warning.

**Use something else:**

- [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the mix within each row.
- [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each cell's share of the whole table.
- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want to test whether the column mixes differ.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`margin`](../glossary.md#term-margin)

**Skill:** [`op-overlay-share-of-col`](../skills/op-overlay-share-of-col.md)

<a id="op-overlay_share_of_row"></a>

### `OVERLAY_SHARE_OF_ROW`

Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it.

**Level:** basic

**Questions it answers:**

- Within each region, what share of orders goes to each channel?
- For each age band, how are answers split across the options?

**Use cases by domain:**

- *survey:* Row percentages: how each segment's answers split across the options.
- *ops:* How each site's tickets split across categories.
- *science:* How each arm's outcomes split across categories.

**Assumptions:**

- Shares add to 1 across a row only when the cell aggregator adds up, such as counts or sums; for averages the ratio is not a share.
- A zero row margin gives no value and a warning.

**Use something else:**

- [`OVERLAY_SHARE_OF_COL`](#op-overlay_share_of_col) when you want the mix within each column.
- [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want the same figure on a 0 to 100 index scale.
- [`OVERLAY_CHISQ_MATRIX`](#op-overlay_chisq_matrix) when you want to test whether the row mixes differ.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`margin`](../glossary.md#term-margin)

**Skill:** [`op-overlay-share-of-row`](../skills/op-overlay-share-of-row.md)

<a id="op-overlay_share_of_total"></a>

### `OVERLAY_SHARE_OF_TOTAL`

Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up.

**Level:** basic

**Questions it answers:**

- What share of all revenue does each region bring in?
- What part of all responses falls in each segment-by-answer cell?

**Use cases by domain:**

- *survey:* Total percentages: each cell's share of all respondents.
- *ops:* Each product line's share of total revenue.
- *science:* Each category's share of all observations.

**Assumptions:**

- Shares add to 1 only when the value adds up, such as counts or sums; for averages the ratio is not a share.
- A zero grand total gives no value and a warning.

**Use something else:**

- [`OVERLAY_SHARE_OF_ROW`](#op-overlay_share_of_row) when you want the share within each row of a crosstab.
- [`OVERLAY_INDEX_VS_TOTAL`](#op-overlay_index_vs_total) when you want the same figure x 100 on a grouped result.
- [`OVERLAY_ZSCORE_VS_TOTAL`](#op-overlay_zscore_vs_total) when you want each group's position against the average group.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`margin`](../glossary.md#term-margin)

**Skill:** [`op-overlay-share-of-total`](../skills/op-overlay-share-of-total.md)

<a id="op-overlay_t_cell"></a>

### `OVERLAY_T_CELL`

Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?

**Level:** advanced

**Questions it answers:**

- Which segment-by-product cells changed in average rating since last wave?
- Where does the new site's average handling time differ from the old site's?

**Use cases by domain:**

- *survey:* Flag the mean ratings per segment and question that moved between two waves.
- *ops:* Compare average cycle time per line and shift between two plants.
- *science:* Compare mean outcomes per subgroup between a treatment and a control cohort.

**Assumptions:**

- Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n. Without it the test uses one variance and n per side for every cell (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2). Those p-values describe the values you supplied, not each cell's own spread, and they change with the measure's units.
- Target and reference are separate, independent samples. Equal variances are not assumed, and each mean should be roughly normal.
- The null hypothesis is equal means in the cell; the p-value is two-sided.
- Every cell is a separate test, so with many cells some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_Z_CELL`](#op-overlay_z_cell) when every cell is large and you want the normal-curve version.
- [`OVERLAY_T_VS_REF`](#op-overlay_t_vs_ref) when the results are grouped series rather than crosstabs.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when the cells hold shares rather than averages.
- [`OVERLAY_PAIRWISE_WELCH_T`](#op-overlay_pairwise_welch_t) when the groups are rows or columns of one crosstab.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-overlay-t-cell`](../skills/op-overlay-t-cell.md)

<a id="op-overlay_t_vs_ref"></a>

### `OVERLAY_T_VS_REF`

Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?

**Level:** advanced

**Questions it answers:**

- Which regions' average spend changed between this quarter and last?
- In which age bands does the test cohort's mean score differ from the control's?

**Use cases by domain:**

- *survey:* Flag the segments whose mean rating moved between two waves.
- *ops:* Compare average resolution time per team between two periods.
- *science:* Compare mean outcome per site between two cohorts.

**Assumptions:**

- Group values must come from AGG_WELFORD, which supplies each group's mean, variance and n. Without it the test uses one variance and n per side for every group (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2); those p-values describe the values you supplied, not each group's own spread, and they change with the measure's units.
- Target and reference are separate, independent samples. Equal variances are not assumed, and each mean should be roughly normal.
- The null hypothesis is equal means in the group; the two-sided p-value is carried in summary.statistic.
- Each group is a separate test, so with many groups some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_Z_VS_REF`](#op-overlay_z_vs_ref) when every group is large and you want the normal-curve version.
- [`OVERLAY_T_CELL`](#op-overlay_t_cell) when the results are crosstabs rather than grouped series.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value.

**Glossary:** [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`homogeneity-of-variance`](../glossary.md#term-homogeneity-of-variance), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`t-statistic`](../glossary.md#term-t-statistic), [`two-tailed`](../glossary.md#term-two-tailed), [`variance`](../glossary.md#term-variance)

**Skill:** [`op-overlay-t-vs-ref`](../skills/op-overlay-t-vs-ref.md)

<a id="op-overlay_yoy"></a>

### `OVERLAY_YOY`

Each period of a date-grouped series as an index value against the same period one year earlier (x 100): year-over-year change.

**Level:** basic

**Questions it answers:**

- How do this year's monthly sales compare with the same months last year?
- Is this quarter's volume up or down on the same quarter last year?

**Use cases by domain:**

- *survey:* Compare each quarter's tracker score with the same quarter a year before.
- *ops:* Compare monthly orders with the same month last year, removing seasonality.
- *science:* Compare seasonal readings with the same season a year earlier.

**Assumptions:**

- The host's single grouper must be GROUP_DATE. For weekly and coarser periods the comparison steps back a fixed number of positions (12 for months), so the series must have no missing periods.
- The first year has no value. A zero prior-year value gives no value and a warning, and 29 February has no counterpart in a non-leap year.

**Use something else:**

- [`OVERLAY_INDEX_VS_PRIOR`](#op-overlay_index_vs_prior) when you want each period against the one before.
- [`OVERLAY_INDEX_VS_BASELINE`](#op-overlay_index_vs_baseline) when you want each period against one fixed starting period.
- [`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the series.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`index-value`](../glossary.md#term-index-value)

**Skill:** [`op-overlay-yoy`](../skills/op-overlay-yoy.md)

<a id="op-overlay_zscore_vs_margin"></a>

### `OVERLAY_ZSCORE_VS_MARGIN`

Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out?

**Level:** intermediate

**Questions it answers:**

- Which segment-by-question averages sit unusually far from the question's overall average?
- Which store-by-month cells stand out from their store's typical month?

**Use cases by domain:**

- *survey:* Highlight the stand-out cells of a table of mean ratings for a heatmap.
- *ops:* Highlight the site-by-week averages that stand out from each site's own average.
- *science:* Highlight subgroup means far from the pooled mean on a common scale.

**Assumptions:**

- It describes rather than tests: there is no p-value behind it, and a value of 2 or 3 is not a probability statement about the data.
- The standard deviation is the spread of the cell values across the slice (dividing by the number of cells), not the sampling error of a cell; slices with few cells give unstable values.
- The centre is the host's margin figure, so it reads naturally when that is an overall average; for counts or sums the margin is a total and every cell sits far below it.

**Use something else:**

- [`OVERLAY_FISHER_EXACT_CELL`](#op-overlay_fisher_exact_cell) when you want a test of whether a cell's count departs from what the margins predict.
- [`OVERLAY_DELTA_VS_MARGIN`](#op-overlay_delta_vs_margin) when the gap in the cell's own units is easier to explain.
- [`OVERLAY_INDEX_VS_MARGIN`](#op-overlay_index_vs_margin) when you want a ratio to the margin.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`margin`](../glossary.md#term-margin), [`outlier`](../glossary.md#term-outlier), [`standard-deviation`](../glossary.md#term-standard-deviation), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-overlay-zscore-vs-margin`](../skills/op-overlay-zscore-vs-margin.md)

<a id="op-overlay_zscore_vs_pop"></a>

### `OVERLAY_ZSCORE_VS_POP`

Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset.

**Level:** intermediate

**Questions it answers:**

- Which answers does this segment pick more or less often than the comparison population, relative to how much answer shares vary?
- Which categories stand out in this region compared with the whole business?

**Use cases by domain:**

- *survey:* Rank the answers a segment over- or under-picks, on one scale.
- *ops:* Highlight the categories a region buys unusually often or rarely.
- *science:* Highlight categories whose share in a cohort sits far from the population's.

**Assumptions:**

- It describes rather than tests: there is no p-value behind it, and a value of 2 or 3 is not a probability statement about the data.
- For categories it divides each share gap by the spread of the population's shares across categories, not by a standard error, so it does not shrink as the subset grows.
- For a numeric field it standardizes each histogram bin's centre against the population mean and standard deviation: the subset's counts never enter it, so the values are the same for any subset. Use OVERLAY_KS_VS_POP to compare a numeric subset.

**Use something else:**

- [`OVERLAY_CHISQ_VS_POP`](#op-overlay_chisq_vs_pop) when you want a test of whether the subset's category mix differs.
- [`OVERLAY_KS_VS_POP`](#op-overlay_ks_vs_pop) when you want a test of whether a numeric distribution differs.
- [`OVERLAY_INDEX_VS_POP`](#op-overlay_index_vs_pop) when a ratio is easier to explain.

**Glossary:** [`baseline`](../glossary.md#term-baseline), [`standard-deviation`](../glossary.md#term-standard-deviation), [`standard-error`](../glossary.md#term-standard-error), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-overlay-zscore-vs-pop`](../skills/op-overlay-zscore-vs-pop.md)

<a id="op-overlay_zscore_vs_rolling"></a>

### `OVERLAY_ZSCORE_VS_ROLLING`

How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods.

**Level:** intermediate

**Questions it answers:**

- Which days had unusually high or low orders compared with the previous two weeks?
- Which readings jumped far from their recent run?

**Use cases by domain:**

- *survey:* Flag waves whose score is far from the last few waves.
- *ops:* Flag days whose volume is far from the recent run.
- *science:* Flag readings far from the preceding readings.

**Assumptions:**

- It describes rather than tests: there is no p-value behind it, and a value of 2 or 3 is not a probability statement about the data.
- The standard deviation is the sample spread of the window (dividing by n - 1); a short window gives a jumpy one, and a trend or seasonality makes many points look unusual.
- The series must be ordered and params.window set; points get no value until the window holds at least 2 values.

**Use something else:**

- [`TEST_TREND`](test.md#op-test_trend) when you want to test for a steady rise or fall across the whole series.
- [`OVERLAY_INDEX_VS_ROLLING_MEAN`](#op-overlay_index_vs_rolling_mean) when you want the ratio to the rolling mean.
- [`ATTR_ZSCORE`](attribute.md#op-attr_zscore) when you want a standardized column for each row rather than per period.

**Glossary:** [`outlier`](../glossary.md#term-outlier), [`rolling-mean`](../glossary.md#term-rolling-mean), [`standard-deviation`](../glossary.md#term-standard-deviation), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-overlay-zscore-vs-rolling`](../skills/op-overlay-zscore-vs-rolling.md)

<a id="op-overlay_zscore_vs_total"></a>

### `OVERLAY_ZSCORE_VS_TOTAL`

How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out?

**Level:** intermediate

**Questions it answers:**

- Which stores' sales are unusually high or low compared with the other stores?
- Which regions stand out from the rest on average rating?

**Use cases by domain:**

- *survey:* Highlight segments whose mean rating is far from the average segment.
- *ops:* Flag sites whose volume is far from the typical site's.
- *science:* Put group means on a common scale before plotting.

**Assumptions:**

- It describes rather than tests: there is no p-value behind it, and a value of 2 or 3 is not a probability statement about the data.
- The standard deviation is the spread of the group values themselves (dividing by the number of groups), not the spread of rows within a group; with few groups the values are unstable.

**Use something else:**

- [`TEST_ANOVA_WELCH`](test.md#op-test_anova_welch) when you want to test whether group averages differ, from raw rows.
- [`OVERLAY_SHARE_OF_TOTAL`](#op-overlay_share_of_total) when you want each group's share of the total.
- [`OVERLAY_DELTA_VS_SIBLING`](#op-overlay_delta_vs_sibling) when you want each group against one chosen group.

**Glossary:** [`mean`](../glossary.md#term-mean), [`outlier`](../glossary.md#term-outlier), [`standard-deviation`](../glossary.md#term-standard-deviation), [`z-score`](../glossary.md#term-z-score)

**Skill:** [`op-overlay-zscore-vs-total`](../skills/op-overlay-zscore-vs-total.md)

<a id="op-overlay_z_cell"></a>

### `OVERLAY_Z_CELL`

Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?

**Level:** advanced

**Questions it answers:**

- With large samples, which segment-by-question means moved since last wave?
- Which store-by-category average baskets differ between two high-traffic regions?

**Use cases by domain:**

- *survey:* Flag mean ratings that moved between two large survey waves.
- *ops:* Compare average spend per category between two high-volume storefronts.
- *science:* Compare subgroup means of two large cohorts where the t and normal tails agree.

**Assumptions:**

- Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n. Without it the test uses one variance and n per side for every cell (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2). Those p-values describe the values you supplied, not each cell's own spread, and they change with the measure's units.
- Target and reference are separate, independent samples. It reads p from the normal curve, so with small cells the p-values come out too small.
- The null hypothesis is equal means in the cell; the p-value is two-sided.
- Every cell is a separate test, so with many cells some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_T_CELL`](#op-overlay_t_cell) when any cell is small; the t-based version is more honest there.
- [`OVERLAY_Z_VS_REF`](#op-overlay_z_vs_ref) when the results are grouped series rather than crosstabs.
- [`OVERLAY_PROP_Z_CELL`](#op-overlay_prop_z_cell) when the cells hold shares rather than averages.

**Glossary:** [`cross-tabulation`](../glossary.md#term-cross-tabulation), [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`standard-error`](../glossary.md#term-standard-error), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-z-cell`](../skills/op-overlay-z-cell.md)

<a id="op-overlay_z_vs_ref"></a>

### `OVERLAY_Z_VS_REF`

Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?

**Level:** advanced

**Questions it answers:**

- With large samples, which regions' average spend changed since last quarter?
- Which high-volume channels' average basket differs between the two periods?

**Use cases by domain:**

- *survey:* Flag segment mean ratings that moved between two large waves.
- *ops:* Compare average order value per channel between two high-traffic periods.
- *science:* Compare per-site means of two large cohorts.

**Assumptions:**

- Group values must come from AGG_WELFORD, which supplies each group's mean, variance and n. Without it the test uses one variance and n per side for every group (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2); those p-values describe the values you supplied, not each group's own spread, and they change with the measure's units.
- Target and reference are separate, independent samples. It reads p from the normal curve, so with small groups the p-values come out too small.
- The null hypothesis is equal means in the group; the two-sided p-value is carried in summary.statistic.
- Each group is a separate test, so with many groups some small p-values turn up by luck; to adjust for multiple comparisons, set multiplicity on the overlay (holm or bonferroni hold the family-wise error, bh or by the false discovery rate) to add adjusted p-values beside the raw ones.

**Use something else:**

- [`OVERLAY_T_VS_REF`](#op-overlay_t_vs_ref) when any group is small; the t-based version is more honest there.
- [`OVERLAY_Z_CELL`](#op-overlay_z_cell) when the results are crosstabs rather than grouped series.
- [`OVERLAY_DELTA_VS_REF`](#op-overlay_delta_vs_ref) when you want the size of the change rather than a p-value.

**Glossary:** [`false-discovery-rate`](../glossary.md#term-false-discovery-rate), [`family-wise-error`](../glossary.md#term-family-wise-error), [`independence`](../glossary.md#term-independence), [`mean`](../glossary.md#term-mean), [`multiple-comparisons`](../glossary.md#term-multiple-comparisons), [`normal-distribution`](../glossary.md#term-normal-distribution), [`null-hypothesis`](../glossary.md#term-null-hypothesis), [`p-value`](../glossary.md#term-p-value), [`standard-error`](../glossary.md#term-standard-error), [`two-tailed`](../glossary.md#term-two-tailed)

**Skill:** [`op-overlay-z-vs-ref`](../skills/op-overlay-z-vs-ref.md)
