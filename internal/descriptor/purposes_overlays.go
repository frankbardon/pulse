package descriptor

import "github.com/frankbardon/pulse/descriptor"

// overlayPurposes is the Purpose registry for the OVERLAY_* kinds, keyed
// by kind. builtinPurposes assembles it with the other category maps.
//
// Each Purpose states the overlay as Pulse computes it (see the
// internal/processing/overlay*.go sources) and names the host it rides
// where that matters for routing: a crosstab (Request.Crosstab), a
// grouped Process result (series), a facet (FacetRequest.Overlays), a
// Compose request (reference and target slots) or a process chain
// (stages). Every inferential kind states its null hypothesis and that
// its p-values are raw; every per-cell or pairwise kind carries the
// multiple-comparisons caveat (TestGuidanceProseLint's MULTI-COMP rule),
// and the z-score kinds say they describe rather than test.
var overlayPurposes = map[string]descriptor.Purpose{
	"OVERLAY_CHISQ_COL":                     purposeOverlayChiSqCol,
	"OVERLAY_CHISQ_MATRIX":                  purposeOverlayChiSqMatrix,
	"OVERLAY_CHISQ_ROW":                     purposeOverlayChiSqRow,
	"OVERLAY_CHISQ_VS_POP":                  purposeOverlayChiSqVsPop,
	"OVERLAY_CHISQ_VS_REF":                  purposeOverlayChiSqVsRef,
	"OVERLAY_DELTA_VS_BASELINE":             purposeOverlayDeltaVsBaseline,
	"OVERLAY_DELTA_VS_MARGIN":               purposeOverlayDeltaVsMargin,
	"OVERLAY_DELTA_VS_PRIOR":                purposeOverlayDeltaVsPrior,
	"OVERLAY_DELTA_VS_REF":                  purposeOverlayDeltaVsRef,
	"OVERLAY_DELTA_VS_SIBLING":              purposeOverlayDeltaVsSibling,
	"OVERLAY_DELTA_VS_STAGE":                purposeOverlayDeltaVsStage,
	"OVERLAY_FISHER_EXACT_CELL":             purposeOverlayFisherExactCell,
	"OVERLAY_FORMULA":                       purposeOverlayFormula,
	"OVERLAY_INDEX_VS_BASELINE":             purposeOverlayIndexVsBaseline,
	"OVERLAY_INDEX_VS_MARGIN":               purposeOverlayIndexVsMargin,
	"OVERLAY_INDEX_VS_POP":                  purposeOverlayIndexVsPop,
	"OVERLAY_INDEX_VS_PRIOR":                purposeOverlayIndexVsPrior,
	"OVERLAY_INDEX_VS_REF":                  purposeOverlayIndexVsRef,
	"OVERLAY_INDEX_VS_ROLLING_MEAN":         purposeOverlayIndexVsRollingMean,
	"OVERLAY_INDEX_VS_SIBLING":              purposeOverlayIndexVsSibling,
	"OVERLAY_INDEX_VS_STAGE":                purposeOverlayIndexVsStage,
	"OVERLAY_INDEX_VS_TOTAL":                purposeOverlayIndexVsTotal,
	"OVERLAY_KS_VS_POP":                     purposeOverlayKSVsPop,
	"OVERLAY_PAIRWISE_PROBIT_T":             purposeOverlayPairwiseProbitT,
	"OVERLAY_PAIRWISE_PROP_Z":               purposeOverlayPairwisePropZ,
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          purposeOverlayPairwiseTwoMeansZ,
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": purposeOverlayPairwiseWeightedTwoMeansZ,
	"OVERLAY_PAIRWISE_WELCH_T":              purposeOverlayPairwiseWelchT,
	"OVERLAY_PANEL_INDEX_VS_REF":            purposeOverlayPanelIndexVsRef,
	"OVERLAY_PROP_Z_CELL":                   purposeOverlayPropZCell,
	"OVERLAY_PROP_Z_PANEL":                  purposeOverlayPropZPanel,
	"OVERLAY_RANK":                          purposeOverlayRank,
	"OVERLAY_SHARE_OF_COL":                  purposeOverlayShareOfCol,
	"OVERLAY_SHARE_OF_ROW":                  purposeOverlayShareOfRow,
	"OVERLAY_SHARE_OF_TOTAL":                purposeOverlayShareOfTotal,
	"OVERLAY_T_CELL":                        purposeOverlayTCell,
	"OVERLAY_T_VS_REF":                      purposeOverlayTVsRef,
	"OVERLAY_YOY":                           purposeOverlayYoY,
	"OVERLAY_Z_CELL":                        purposeOverlayZCell,
	"OVERLAY_Z_VS_REF":                      purposeOverlayZVsRef,
	"OVERLAY_ZSCORE_VS_MARGIN":              purposeOverlayZScoreVsMargin,
	"OVERLAY_ZSCORE_VS_POP":                 purposeOverlayZScoreVsPop,
	"OVERLAY_ZSCORE_VS_ROLLING":             purposeOverlayZScoreVsRolling,
	"OVERLAY_ZSCORE_VS_TOTAL":               purposeOverlayZScoreVsTotal,
}

// Shared assumption sentences, so sibling kinds say the same thing the
// same way.
const (
	overlayCountsOnly = "Cells must hold counts of independent rows (AGG_COUNT); Pulse runs it on any cell aggregator, " +
		"but sums, averages or weighted counts make the test meaningless."
	overlayRawPValues = "Pulse reports raw p-values: with many cells or pairs some small values turn up by luck alone, " +
		"so adjust for multiple comparisons yourself (for example Holm or Bonferroni)."
	overlayWelfordInputs = "Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n. Without it the test " +
		"uses one variance and n per side for every cell (params variance_target / variance_ref / sample_size_target / " +
		"sample_size_ref, default variance 1 and n 2). Those p-values describe the values you supplied, not each cell's own " +
		"spread, and they change with the measure's units."
	overlayDescriptiveZ = "It describes rather than tests: there is no p-value behind it, and a value of 2 or 3 is not a " +
		"probability statement about the data."
)

// --- Chi-square family -------------------------------------------------

var (
	purposeOverlayChiSqMatrix = descriptor.Purpose{
		Plain:   "Chi-square test on a whole crosstab: checks whether the row and column categories are associated, with one p-value for the table.",
		Intents: []string{IntentRelationship},
		Questions: []string{
			"Is preferred channel associated with age band in this crosstab?",
			"Does the mix of answers differ across regions in the table?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether answer choice is associated with respondent segment in a banner table.",
			descriptor.DomainOps:     "Check whether ticket category is associated with support site.",
			descriptor.DomainScience: "Check whether outcome category is associated with treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to know which rows have a mix unlike the overall one", Use: "OVERLAY_CHISQ_ROW"},
			{When: "you want to know which single cells stand out", Use: "OVERLAY_FISHER_EXACT_CELL"},
			{When: "the table is 2x2 and some expected counts are below 5", Use: "TEST_FISHER_EXACT"},
			{When: "you also want how strongly the two fields are associated (Cramer's V), from raw rows", Use: "TEST_CHISQ"},
			{When: "you compare this table's mix with another Compose request's table", Use: "OVERLAY_CHISQ_VS_REF"},
		},
		Assumptions: []string{
			overlayCountsOnly,
			"The null hypothesis is that row and column categories are independent; the p-value comes from the chi-square approximation.",
			"Every expected count should be about 5 or more; a warning flags tables where some are lower.",
			"A small p-value says the table departs from independence, not how strongly or which cells drive it.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "cross-tabulation", "degrees-of-freedom", "independence",
			"null-hypothesis", "p-value", "sample-size",
		},
	}

	purposeOverlayChiSqRow = descriptor.Purpose{
		Plain:   "Chi-square test per crosstab row: checks whether each row's spread across the columns departs from the table's overall column mix.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which regions have an answer mix unlike the overall mix?",
			"Which product lines get a different spread of complaint types?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the segments whose answer mix departs from the total sample's mix.",
			descriptor.DomainOps:     "Flag the sites whose spread of ticket types departs from the company-wide spread.",
			descriptor.DomainScience: "Flag the sites whose outcome mix departs from the pooled mix.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the groups are the columns rather than the rows", Use: "OVERLAY_CHISQ_COL"},
			{When: "you want one test for the whole table", Use: "OVERLAY_CHISQ_MATRIX"},
			{When: "you want to know which single cells stand out", Use: "OVERLAY_FISHER_EXACT_CELL"},
		},
		Assumptions: []string{
			overlayCountsOnly,
			"The null hypothesis is that the row's mix matches the overall column mix. That overall mix includes the row itself, so a large row pulls it toward itself: the statistic is smaller than a row-versus-rest test's by the factor (N - row total) / N, and the p-value is too large (conservative), most of all for big rows.",
			"Each row is a separate test, so with many rows some small p-values turn up by luck; adjust for multiple comparisons yourself.",
			"Every expected count should be about 5 or more; a warning flags rows where some are lower.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "cross-tabulation", "degrees-of-freedom", "goodness-of-fit",
			"multiple-comparisons", "null-hypothesis", "p-value",
		},
	}

	purposeOverlayChiSqCol = descriptor.Purpose{
		Plain:   "Chi-square test per crosstab column: checks whether each column's spread across the rows departs from the table's overall row mix.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which banner columns have an answer mix unlike the total?",
			"Which months have a spread of order types unlike the whole year?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the banner columns whose answer mix departs from the total column.",
			descriptor.DomainOps:     "Flag the channels whose spread of order sizes departs from the overall spread.",
			descriptor.DomainScience: "Flag the treatment arms whose outcome mix departs from the pooled mix.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the groups are the rows rather than the columns", Use: "OVERLAY_CHISQ_ROW"},
			{When: "you want one test for the whole table", Use: "OVERLAY_CHISQ_MATRIX"},
			{When: "you want to know which single cells stand out", Use: "OVERLAY_FISHER_EXACT_CELL"},
		},
		Assumptions: []string{
			overlayCountsOnly,
			"The null hypothesis is that the column's mix matches the overall row mix. That overall mix includes the column itself, so a large column pulls it toward itself: the statistic is smaller than a column-versus-rest test's by the factor (N - column total) / N, and the p-value is too large (conservative), most of all for big columns.",
			"Each column is a separate test, so with many columns some small p-values turn up by luck; adjust for multiple comparisons yourself.",
			"Every expected count should be about 5 or more; a warning flags columns where some are lower.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "cross-tabulation", "degrees-of-freedom", "goodness-of-fit",
			"multiple-comparisons", "null-hypothesis", "p-value",
		},
	}

	purposeOverlayChiSqVsPop = descriptor.Purpose{
		Plain:   "Chi-square goodness-of-fit test on a facet: checks whether a subset's category mix departs from a comparison population's mix.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Does this segment's brand mix differ from the whole customer base's?",
			"Is the subset's spread of ticket types unlike the population's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether a filtered segment's answer mix departs from the full sample's.",
			descriptor.DomainOps:     "Check whether one region's order-type mix departs from the national mix.",
			descriptor.DomainScience: "Check whether a cohort's category mix departs from a reference population's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is numeric", Use: "OVERLAY_KS_VS_POP"},
			{When: "you want to see which categories are over- or under-represented rather than one test", Use: "OVERLAY_INDEX_VS_POP"},
			{When: "you compare two separate groups from raw rows rather than a subset with a population", Use: "TEST_CHISQ"},
		},
		Assumptions: []string{
			"The subset's figures are counts of independent rows.",
			"The null hypothesis is that the subset's mix matches the population's. The population mix is treated as known and fixed; when the subset is a large part of the population the two overlap and the p-value is only approximate.",
			"Only categories both sides show are compared, and the population's shares are rescaled over them, so population nulls and categories cut from a top-K listing do not distort the expected counts.",
			"With a very large subset even tiny differences in mix give small p-values; read an OVERLAY_INDEX_VS_POP layer on the same facet for how big each category's shift is.",
			"Every expected count should be about 5 or more; a warning flags layers where some are lower.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "degrees-of-freedom", "goodness-of-fit", "independence",
			"index-value", "null-hypothesis", "p-value", "sample-size",
		},
	}

	purposeOverlayChiSqVsRef = descriptor.Purpose{
		Plain:   "Chi-square test in a Compose request: checks whether a target request's crosstab mix departs from the reference request's mix.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Has the mix of answers in this wave shifted from last wave?",
			"Does the test market's category mix differ from the control market's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether this wave's answer mix departs from the previous wave's.",
			descriptor.DomainOps:     "Check whether this quarter's ticket mix departs from last quarter's.",
			descriptor.DomainScience: "Check whether a replication's category mix departs from the original study's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "both results are samples of similar size and you want a test that allows for uncertainty in both: stack the rows, label each source and cross them", Use: "TEST_CHISQ"},
			{When: "you want to see which cells moved rather than one overall test", Use: "OVERLAY_PROP_Z_CELL"},
			{When: "you want the size of each cell's change", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			overlayCountsOnly,
			"The null hypothesis is that the target's mix matches the reference's. The reference mix is treated as fixed expected shares and its own sampling uncertainty is ignored, so with a small reference the p-value is too small.",
			"Target and reference should be separate, independent sets of rows.",
			"The p-value is carried in the layer's scalar slot; the chi-square value is in summary.statistic.",
			"Every expected count should be about 5 or more; a warning flags layers where some are lower.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"baseline", "chi-square", "cross-tabulation", "degrees-of-freedom",
			"goodness-of-fit", "independence", "null-hypothesis", "p-value",
		},
	}
)

// --- Per-cell and pairwise tests ----------------------------------------

var (
	purposeOverlayFisherExactCell = descriptor.Purpose{
		Plain:   "Fisher's exact test for every crosstab cell: checks whether being in that row goes with being in that column more or less than expected.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which answer-by-segment cells stand out in this banner table?",
			"In a small table, which cells hold more cases than the margins predict?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the cells of a banner table where a segment picks an answer more or less often than the rest.",
			descriptor.DomainOps:     "Flag the site-by-fault cells that occur more or less often than the totals predict.",
			descriptor.DomainScience: "Flag the arm-by-outcome cells of a small table without relying on a large-sample approximation.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want one test for the whole table", Use: "OVERLAY_CHISQ_MATRIX"},
			{When: "you compare the same cell across two Compose requests", Use: "OVERLAY_PROP_Z_CELL"},
			{When: "you only have one 2x2 table of raw rows", Use: "TEST_FISHER_EXACT"},
		},
		Assumptions: []string{
			overlayCountsOnly,
			"Each cell is tested as its own 2x2 table: this row versus the rest, by this column versus the rest. The null hypothesis is that the two are independent; the p-value is two-sided.",
			"Every cell is a separate test and the tables overlap, so with many cells some small p-values turn up by luck; " +
				"Pulse does not adjust them, so correct for multiple comparisons yourself (for example Holm or Bonferroni).",
			"It stays valid with small counts, which makes it the backstop when chi-square expected counts are low; the low-count warning is advisory only.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"chi-square", "cross-tabulation", "exact-test", "independence", "margin",
			"multiple-comparisons", "null-hypothesis", "p-value", "two-tailed",
		},
	}

	purposeOverlayPropZCell = descriptor.Purpose{
		Plain:   "Two-proportion z-test for every crosstab cell in a Compose request: does the target's share in that cell differ from the reference's?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which answer shares changed between this wave and last wave?",
			"Where does the test market's share differ from the control market's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the answer shares that moved between two survey waves.",
			descriptor.DomainOps:     "Flag the defect rates per line and shift that differ between two plants.",
			descriptor.DomainScience: "Compare response rates per subgroup between a treatment and a control cohort.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you compare three or more requests at once", Use: "OVERLAY_PROP_Z_PANEL"},
			{When: "the groups are rows or columns of one crosstab", Use: "OVERLAY_PAIRWISE_PROP_Z"},
			{When: "a cell has only a handful of successes or failures: stack both sources' rows and run an exact 2x2 test", Use: "TEST_FISHER_EXACT"},
			{When: "you want the size of the change rather than a p-value", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			"Cell values must be counts and each row margin is that row's sample size: the share tested is cell / row margin. A cell whose row margin is missing or zero on either side gets no test (NaN, with a warning).",
			"Target and reference are separate, independent samples; rows that appear in both make the p-value wrong.",
			"The null hypothesis is that the two shares are equal. The normal approximation needs roughly 10 successes and 10 failures on each side; the p-value is two-sided.",
			"Every cell is a separate test, so with many cells some small p-values turn up by luck; " +
				"Pulse does not adjust them, so correct for multiple comparisons yourself.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"cross-tabulation", "independence", "margin", "multiple-comparisons",
			"null-hypothesis", "p-value", "sample-size", "standard-error", "two-tailed",
		},
	}

	purposeOverlayPropZPanel = descriptor.Purpose{
		Plain:   "Two-proportion z-tests between every pair of requests in a Compose panel, for each crosstab cell: which waves or markets differ in share?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Across four waves, which pairs of waves differ in this answer's share?",
			"Which of five markets differ from each other in brand share?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare answer shares between every pair of waves in a tracking study.",
			descriptor.DomainOps:     "Compare defect rates between every pair of plants.",
			descriptor.DomainScience: "Compare response rates between every pair of study sites.",
		},
		NotFor: []descriptor.Alternative{
			{When: "there is only one target", Use: "OVERLAY_PROP_Z_CELL"},
			{When: "you want index values against one reference rather than tests", Use: "OVERLAY_PANEL_INDEX_VS_REF"},
			{When: "the groups are rows or columns of one crosstab", Use: "OVERLAY_PAIRWISE_PROP_Z"},
		},
		Assumptions: []string{
			"Cell values must be counts; by default each slot's row margin is its sample size (n_source picks another). A pair involving a slot whose row margin is missing or zero gets no test (NaN, with a warning).",
			"The slots are separate, independent samples; rows that appear in more than one slot make the p-values wrong.",
			"The null hypothesis for each pair is that the two shares are equal; each p-value is two-sided and needs roughly 10 successes and 10 failures per side.",
			"Each cell gets one test per pair of slots, so the count grows fast. " + overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"cross-tabulation", "independence", "margin", "multiple-comparisons",
			"null-hypothesis", "p-value", "sample-size", "two-tailed",
		},
	}

	purposeOverlayPairwisePropZ = descriptor.Purpose{
		Plain:   "Two-proportion z-tests between every pair of rows (or columns) of one crosstab, per column (or row): which segments differ in share?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which age bands differ from each other in the share choosing each brand?",
			"Which pairs of regions differ in their share of late deliveries?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Letter-test banner columns: which segments differ in the share giving each answer.",
			descriptor.DomainOps:     "Compare late-delivery shares between every pair of depots.",
			descriptor.DomainScience: "Compare response shares between every pair of dose groups.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the cells are averages of a numeric measure rather than shares", Use: "OVERLAY_PAIRWISE_WELCH_T"},
			{When: "the groups are separate Compose requests", Use: "OVERLAY_PROP_Z_PANEL"},
			{When: "you want one overall test of whether the rows differ at all", Use: "OVERLAY_CHISQ_MATRIX"},
		},
		Assumptions: []string{
			"The two groups in each pair are independent samples; a multi-select (set) grouper can put one row in both, which breaks that.",
			"Choose n_source to match how the cell value was made and p_source to match a percentage or a proportion; a mismatch silently skips every pair.",
			"The null hypothesis for each pair is that the two shares are equal; each p-value is two-sided and needs roughly 10 successes and 10 failures per side.",
			overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"cross-tabulation", "independence", "multiple-comparisons", "null-hypothesis",
			"p-value", "sample-size", "two-tailed",
		},
	}

	purposeOverlayPairwiseProbitT = descriptor.Purpose{
		Plain:   "Pairwise t-tests on probit-transformed shares between rows (or columns) of one crosstab, matching a convention some survey tools use.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which segments differ in share when tested the way our survey tool does?",
			"Do the probit-scale shares of each region differ from each other?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Reproduce the pairwise share tests of a survey tool that works on the probit scale.",
			descriptor.DomainOps:    "Match a legacy report's pairwise rate tests that used the probit transform.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the standard test of two shares", Use: "OVERLAY_PAIRWISE_PROP_Z"},
			{When: "the cells are averages of a numeric measure", Use: "OVERLAY_PAIRWISE_WELCH_T"},
		},
		Assumptions: []string{
			"It treats each probit value as having spread 1/sqrt(n). The true spread is at least about 1.25/sqrt(n) and larger near 0% or 100%, so its p-values come out too small at every share. Use it only to reproduce a tool that applies it, never as evidence on its own.",
			"Shares of exactly 0 or 1 are clipped to 1e-10 from the edge, which maps them to about ±6.4 on the probit scale. Any pair involving a 0% or 100% share then gets a huge t and a near-zero p-value whatever the n; treat those pairs as unreadable.",
			"The two groups in each pair are independent samples. The null hypothesis is equal probit values; each p-value is two-sided, from a t distribution with n_i + n_j - 2 degrees of freedom.",
			overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"degrees-of-freedom", "independence", "multiple-comparisons", "null-hypothesis",
			"p-value", "probit", "t-statistic", "two-tailed",
		},
	}

	purposeOverlayPairwiseWelchT = descriptor.Purpose{
		Plain:   "Welch t-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which age bands differ from each other in average spend, product by product?",
			"Which pairs of depots differ in average delivery time per month?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Letter-test banner columns on a mean rating.",
			descriptor.DomainOps:     "Compare average handling time between every pair of teams, per queue.",
			descriptor.DomainScience: "Compare mean outcomes between every pair of dose groups, per site.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every cell is large and you want the normal-curve version", Use: "OVERLAY_PAIRWISE_TWO_MEANS_Z"},
			{When: "the cells are weighted averages", Use: "OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z"},
			{When: "the cells are shares rather than averages", Use: "OVERLAY_PAIRWISE_PROP_Z"},
			{When: "you compare groups from raw rows and want p-values already adjusted across every pair", Use: "TEST_TUKEY_HSD"},
		},
		Assumptions: []string{
			"Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n; any other cell aggregator is refused.",
			"The two groups in each pair are independent samples. Equal variances are not assumed, and each mean should be roughly normal: safe for large cells, risky for small skewed ones.",
			"The null hypothesis for each pair is equal means; each p-value is two-sided.",
			overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"degrees-of-freedom", "homogeneity-of-variance", "independence", "mean",
			"multiple-comparisons", "normal-distribution", "null-hypothesis", "p-value",
			"t-statistic", "two-tailed", "variance",
		},
	}

	purposeOverlayPairwiseTwoMeansZ = descriptor.Purpose{
		Plain:   "Large-sample z-tests between every pair of rows (or columns) of one crosstab of averages: which segments differ in mean?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"With thousands of respondents per cell, which segments differ in average rating?",
			"Which pairs of high-volume stores differ in average basket size?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Letter-test large banner columns on a mean rating where reporting calls for z.",
			descriptor.DomainOps:     "Compare average order value between every pair of high-traffic channels.",
			descriptor.DomainScience: "Compare means between every pair of large groups where the t and normal tails agree.",
		},
		NotFor: []descriptor.Alternative{
			{When: "any cell is small; the t-based version is more honest there", Use: "OVERLAY_PAIRWISE_WELCH_T"},
			{When: "the cells are weighted averages", Use: "OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z"},
			{When: "the cells are shares rather than averages", Use: "OVERLAY_PAIRWISE_PROP_Z"},
		},
		Assumptions: []string{
			"Cells must use AGG_WELFORD, which supplies each cell's mean, variance and n; any other cell aggregator is refused.",
			"The two groups in each pair are independent samples. It reads p from the normal curve, so with small cells the p-values come out too small.",
			"The null hypothesis for each pair is equal means; each p-value is two-sided.",
			overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"independence", "mean", "multiple-comparisons", "normal-distribution",
			"null-hypothesis", "p-value", "sample-size", "standard-error", "two-tailed",
		},
	}

	purposeOverlayPairwiseWeightedTwoMeansZ = descriptor.Purpose{
		Plain:   "Large-sample z-tests between every pair of rows (or columns) of one crosstab of weighted averages, with a sample-size basis you choose.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which weighted segments differ from each other in average satisfaction?",
			"Which regions differ in weighted average spend once survey weights are applied?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Letter-test banner columns on a weighted mean rating.",
			descriptor.DomainOps:     "Compare volume-weighted average prices between every pair of suppliers.",
			descriptor.DomainScience: "Compare design-weighted means between every pair of strata.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the cells are unweighted averages", Use: "OVERLAY_PAIRWISE_WELCH_T"},
			{When: "you only need the weighted averages themselves", Use: "AGG_WEIGHTED_MEAN"},
		},
		Assumptions: []string{
			"Cells must use AGG_WEIGHTED_MEAN; n_basis is required. With weights the sum of weights is the sample size, which overstates precision when weights are scaled up to a population; kish uses the effective sample size and suits survey weights.",
			"It accounts for weighting only, not for clustering or other design effects, so with such designs the p-values come out too small.",
			"The two groups in each pair are independent samples. The null hypothesis for each pair is equal weighted means; each p-value is two-sided from the normal curve.",
			overlayRawPValues,
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"effective-sample-size", "independence", "mean", "multiple-comparisons",
			"null-hypothesis", "p-value", "sample-size", "two-tailed", "weighting",
		},
	}

	purposeOverlayTCell = descriptor.Purpose{
		Plain:   "Welch t-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which segment-by-product cells changed in average rating since last wave?",
			"Where does the new site's average handling time differ from the old site's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the mean ratings per segment and question that moved between two waves.",
			descriptor.DomainOps:     "Compare average cycle time per line and shift between two plants.",
			descriptor.DomainScience: "Compare mean outcomes per subgroup between a treatment and a control cohort.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every cell is large and you want the normal-curve version", Use: "OVERLAY_Z_CELL"},
			{When: "the results are grouped series rather than crosstabs", Use: "OVERLAY_T_VS_REF"},
			{When: "the cells hold shares rather than averages", Use: "OVERLAY_PROP_Z_CELL"},
			{When: "the groups are rows or columns of one crosstab", Use: "OVERLAY_PAIRWISE_WELCH_T"},
		},
		Assumptions: []string{
			overlayWelfordInputs,
			"Target and reference are separate, independent samples. Equal variances are not assumed, and each mean should be roughly normal.",
			"The null hypothesis is equal means in the cell; the p-value is two-sided.",
			"Every cell is a separate test, so with many cells some small p-values turn up by luck; " +
				"Pulse does not adjust them, so correct for multiple comparisons yourself.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"cross-tabulation", "homogeneity-of-variance", "independence", "mean",
			"multiple-comparisons", "null-hypothesis", "p-value", "t-statistic",
			"two-tailed", "variance",
		},
	}

	purposeOverlayZCell = descriptor.Purpose{
		Plain:   "Large-sample z-test for every crosstab cell in a Compose request: does the target's mean in that cell differ from the reference's?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"With large samples, which segment-by-question means moved since last wave?",
			"Which store-by-category average baskets differ between two high-traffic regions?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag mean ratings that moved between two large survey waves.",
			descriptor.DomainOps:     "Compare average spend per category between two high-volume storefronts.",
			descriptor.DomainScience: "Compare subgroup means of two large cohorts where the t and normal tails agree.",
		},
		NotFor: []descriptor.Alternative{
			{When: "any cell is small; the t-based version is more honest there", Use: "OVERLAY_T_CELL"},
			{When: "the results are grouped series rather than crosstabs", Use: "OVERLAY_Z_VS_REF"},
			{When: "the cells hold shares rather than averages", Use: "OVERLAY_PROP_Z_CELL"},
		},
		Assumptions: []string{
			overlayWelfordInputs,
			"Target and reference are separate, independent samples. It reads p from the normal curve, so with small cells the p-values come out too small.",
			"The null hypothesis is equal means in the cell; the p-value is two-sided.",
			"Every cell is a separate test, so with many cells some small p-values turn up by luck; " +
				"Pulse does not adjust them, so correct for multiple comparisons yourself.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"cross-tabulation", "independence", "mean", "multiple-comparisons",
			"normal-distribution", "null-hypothesis", "p-value", "standard-error", "two-tailed",
		},
	}

	purposeOverlayTVsRef = descriptor.Purpose{
		Plain:   "Welch t-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which regions' average spend changed between this quarter and last?",
			"In which age bands does the test cohort's mean score differ from the control's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag the segments whose mean rating moved between two waves.",
			descriptor.DomainOps:     "Compare average resolution time per team between two periods.",
			descriptor.DomainScience: "Compare mean outcome per site between two cohorts.",
		},
		NotFor: []descriptor.Alternative{
			{When: "every group is large and you want the normal-curve version", Use: "OVERLAY_Z_VS_REF"},
			{When: "the results are crosstabs rather than grouped series", Use: "OVERLAY_T_CELL"},
			{When: "you want the size of the change rather than a p-value", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			"Group values must come from AGG_WELFORD, which supplies each group's mean, variance and n. Without it the test uses one variance and n per side for every group (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2); those p-values describe the values you supplied, not each group's own spread, and they change with the measure's units.",
			"Target and reference are separate, independent samples. Equal variances are not assumed, and each mean should be roughly normal.",
			"The null hypothesis is equal means in the group; the two-sided p-value is carried in summary.statistic.",
			"Each group is a separate test, so with many groups some small p-values turn up by luck; adjust for multiple comparisons yourself.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"homogeneity-of-variance", "independence", "mean", "multiple-comparisons",
			"null-hypothesis", "p-value", "t-statistic", "two-tailed", "variance",
		},
	}

	purposeOverlayZVsRef = descriptor.Purpose{
		Plain:   "Large-sample z-test for every group of a grouped result in a Compose request: does the target's mean differ from the reference's?",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"With large samples, which regions' average spend changed since last quarter?",
			"Which high-volume channels' average basket differs between the two periods?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Flag segment mean ratings that moved between two large waves.",
			descriptor.DomainOps:     "Compare average order value per channel between two high-traffic periods.",
			descriptor.DomainScience: "Compare per-site means of two large cohorts.",
		},
		NotFor: []descriptor.Alternative{
			{When: "any group is small; the t-based version is more honest there", Use: "OVERLAY_T_VS_REF"},
			{When: "the results are crosstabs rather than grouped series", Use: "OVERLAY_Z_CELL"},
			{When: "you want the size of the change rather than a p-value", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			"Group values must come from AGG_WELFORD, which supplies each group's mean, variance and n. Without it the test uses one variance and n per side for every group (params variance_target / variance_ref / sample_size_target / sample_size_ref, default variance 1 and n 2); those p-values describe the values you supplied, not each group's own spread, and they change with the measure's units.",
			"Target and reference are separate, independent samples. It reads p from the normal curve, so with small groups the p-values come out too small.",
			"The null hypothesis is equal means in the group; the two-sided p-value is carried in summary.statistic.",
			"Each group is a separate test, so with many groups some small p-values turn up by luck; adjust for multiple comparisons yourself.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"independence", "mean", "multiple-comparisons", "normal-distribution",
			"null-hypothesis", "p-value", "standard-error", "two-tailed",
		},
	}

	purposeOverlayKSVsPop = descriptor.Purpose{
		Plain:   "Kolmogorov-Smirnov test on a numeric facet: checks whether a subset's distribution departs from a comparison population's.",
		Intents: []string{IntentDistributionShape, IntentBenchmark},
		Questions: []string{
			"Does this segment's spend distribution differ from all customers'?",
			"Has the shape of response times in this region drifted from the fleet-wide shape?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether a segment's score distribution departs from the full sample's.",
			descriptor.DomainOps:     "Check whether one site's latency distribution departs from the fleet's.",
			descriptor.DomainScience: "Check whether a cohort's measurement distribution departs from a reference population's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the field is categorical", Use: "OVERLAY_CHISQ_VS_POP"},
			{When: "you have two separate groups of raw rows", Use: "TEST_KS"},
			{When: "you want to see where the shapes differ rather than one test", Use: "OVERLAY_INDEX_VS_POP"},
		},
		Assumptions: []string{
			"The curves are rebuilt from histograms or percentiles, not raw values: request matching histograms (or percentiles) on both arms. With matching histograms, coarse bins understate the gap and make the test conservative. When Pulse falls back to percentiles it interpolates between them, so D can come out too large or too small and the p-value has no guaranteed direction; prefer matching histograms.",
			"It treats subset and population as two independent samples. When the subset is part of the population they overlap: D shrinks by the subset's share of the population, so the p-value is too large (conservative), badly so when the subset is a big part of the population. Compare against the rest of the population instead when you can.",
			"It compares two observed distributions; it is not a test against a named distribution with parameters estimated from the data, which would need the Lilliefors correction.",
			"With few rows it has low power; with very large groups even trivial differences in shape give small p-values.",
			"The null hypothesis is that both share one distribution; the p-value is a two-sided large-sample approximation.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"goodness-of-fit", "independence", "non-parametric", "null-hypothesis",
			"p-value", "percentile", "statistical-power", "two-tailed",
		},
	}
)

// --- Against a margin or total (crosstab and grouped hosts) --------------

var (
	purposeOverlayDeltaVsMargin = descriptor.Purpose{
		Plain:   "Each crosstab cell minus its row, column or grand margin, in the cell's own units: how far a cell sits above or below its margin.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How many points above or below the overall average rating is each segment, per question?",
			"Which store-by-month cells sit furthest from their store's overall average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Show each segment's mean rating as a gap from the total-sample mean.",
			descriptor.DomainOps:     "Show each site's average cycle time as a gap from the company average.",
			descriptor.DomainScience: "Show each subgroup mean as a gap from the pooled mean.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a ratio rather than a gap", Use: "OVERLAY_INDEX_VS_MARGIN"},
			{When: "you want the gaps in standard deviations", Use: "OVERLAY_ZSCORE_VS_MARGIN"},
			{When: "you want a test of whether a cell's count departs from what the margins predict", Use: "OVERLAY_FISHER_EXACT_CELL"},
		},
		Assumptions: []string{
			"The margin is the host's own margin figure: a total for counts and sums (so every cell sits below it), an overall average for means. It reads most naturally with averages or shares.",
			"The gap keeps the cell's units; when cells are percentages it is in percentage points.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "cross-tabulation", "margin", "mean", "percentage-point"},
	}

	purposeOverlayIndexVsMargin = descriptor.Purpose{
		Plain:   "Index value of each crosstab cell against its row, column or grand margin (cell / margin x 100): which cells over- or under-index?",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which segments over-index on each answer compared with the total?",
			"Which product-by-region cells are well above their region's average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Index each segment's mean rating against the total-sample mean.",
			descriptor.DomainOps:     "Index each site's average cost against the company-wide average.",
			descriptor.DomainScience: "Index each subgroup mean against the pooled mean.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the raw share (0 to 1) of the row", Use: "OVERLAY_SHARE_OF_ROW"},
			{When: "you want the gap in the cell's own units", Use: "OVERLAY_DELTA_VS_MARGIN"},
			{When: "you compare a facet subset with a comparison population", Use: "OVERLAY_INDEX_VS_POP"},
		},
		Assumptions: []string{
			"The margin is the host's own margin figure. For averages 100 means the same as the overall average; for counts and sums the margin is a total, so the index is the cell's share x 100, not a comparison with a typical cell.",
			"A zero margin gives no value and a warning; a small margin makes the index swing widely.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "cross-tabulation", "index-value", "margin"},
	}

	purposeOverlayZScoreVsMargin = descriptor.Purpose{
		Plain:   "Each crosstab cell's gap from its row, column or grand margin, in standard deviations of the cells in that slice: which cells stand out?",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which segment-by-question averages sit unusually far from the question's overall average?",
			"Which store-by-month cells stand out from their store's typical month?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Highlight the stand-out cells of a table of mean ratings for a heatmap.",
			descriptor.DomainOps:     "Highlight the site-by-week averages that stand out from each site's own average.",
			descriptor.DomainScience: "Highlight subgroup means far from the pooled mean on a common scale.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a test of whether a cell's count departs from what the margins predict", Use: "OVERLAY_FISHER_EXACT_CELL"},
			{When: "the gap in the cell's own units is easier to explain", Use: "OVERLAY_DELTA_VS_MARGIN"},
			{When: "you want a ratio to the margin", Use: "OVERLAY_INDEX_VS_MARGIN"},
		},
		Assumptions: []string{
			overlayDescriptiveZ,
			"The standard deviation is the spread of the cell values across the slice (dividing by the number of cells), not the sampling error of a cell; slices with few cells give unstable values.",
			"The centre is the host's margin figure, so it reads naturally when that is an overall average; for counts or sums the margin is a total and every cell sits far below it.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"cross-tabulation", "margin", "outlier", "standard-deviation", "z-score"},
	}

	purposeOverlayShareOfRow = descriptor.Purpose{
		Plain:   "Each crosstab cell as a share of its row margin (0 to 1): the mix of columns within each row, as a 100% stacked bar shows it.",
		Intents: []string{IntentComposition},
		Questions: []string{
			"Within each region, what share of orders goes to each channel?",
			"For each age band, how are answers split across the options?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Row percentages: how each segment's answers split across the options.",
			descriptor.DomainOps:     "How each site's tickets split across categories.",
			descriptor.DomainScience: "How each arm's outcomes split across categories.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the mix within each column", Use: "OVERLAY_SHARE_OF_COL"},
			{When: "you want the same figure on a 0 to 100 index scale", Use: "OVERLAY_INDEX_VS_MARGIN"},
			{When: "you want to test whether the row mixes differ", Use: "OVERLAY_CHISQ_MATRIX"},
		},
		Assumptions: []string{
			"Shares add to 1 across a row only when the cell aggregator adds up, such as counts or sums; for averages the ratio is not a share.",
			"A zero row margin gives no value and a warning.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"cross-tabulation", "margin"},
	}

	purposeOverlayShareOfCol = descriptor.Purpose{
		Plain:   "Each crosstab cell as a share of its column margin (0 to 1): the mix of rows within each column, as a 100% stacked column shows it.",
		Intents: []string{IntentComposition},
		Questions: []string{
			"Within each channel, what share of orders comes from each region?",
			"In each banner column, how are answers split across the options?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Column percentages: how each banner column's answers split across the options.",
			descriptor.DomainOps:     "How each month's tickets split across categories.",
			descriptor.DomainScience: "How each outcome category splits across arms.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the mix within each row", Use: "OVERLAY_SHARE_OF_ROW"},
			{When: "you want each cell's share of the whole table", Use: "OVERLAY_SHARE_OF_TOTAL"},
			{When: "you want to test whether the column mixes differ", Use: "OVERLAY_CHISQ_MATRIX"},
		},
		Assumptions: []string{
			"Shares add to 1 down a column only when the cell aggregator adds up, such as counts or sums; for averages the ratio is not a share.",
			"A zero column margin gives no value and a warning.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"cross-tabulation", "margin"},
	}

	purposeOverlayShareOfTotal = descriptor.Purpose{
		Plain:   "Each crosstab cell or group of a grouped result as a share of the grand total (0 to 1): what part of the whole each piece makes up.",
		Intents: []string{IntentComposition},
		Questions: []string{
			"What share of all revenue does each region bring in?",
			"What part of all responses falls in each segment-by-answer cell?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Total percentages: each cell's share of all respondents.",
			descriptor.DomainOps:     "Each product line's share of total revenue.",
			descriptor.DomainScience: "Each category's share of all observations.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the share within each row of a crosstab", Use: "OVERLAY_SHARE_OF_ROW"},
			{When: "you want the same figure x 100 on a grouped result", Use: "OVERLAY_INDEX_VS_TOTAL"},
			{When: "you want each group's position against the average group", Use: "OVERLAY_ZSCORE_VS_TOTAL"},
		},
		Assumptions: []string{
			"Shares add to 1 only when the value adds up, such as counts or sums; for averages the ratio is not a share.",
			"A zero grand total gives no value and a warning.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"cross-tabulation", "margin"},
	}

	purposeOverlayIndexVsTotal = descriptor.Purpose{
		Plain:   "Each group's value on a grouped result as an index value against the sum over all groups (group / total x 100).",
		Intents: []string{IntentComposition},
		Questions: []string{
			"How big is each region relative to the whole, on a 0 to 100 scale?",
			"What part of total sales, scaled to 100, does each channel make up?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey: "Each segment's share of all responses, scaled to 100.",
			descriptor.DomainOps:    "Each product line's share of total revenue, scaled to 100.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the raw share (0 to 1)", Use: "OVERLAY_SHARE_OF_TOTAL"},
			{When: "you want each group against the average group", Use: "OVERLAY_ZSCORE_VS_TOTAL"},
			{When: "you want each group against one chosen group", Use: "OVERLAY_INDEX_VS_SIBLING"},
		},
		Assumptions: []string{
			"100 means the group equals the whole total, not the typical group, so values are usually far below 100.",
			"It makes sense only when the value adds up across groups, such as counts or sums. A zero total gives no value and a warning.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayZScoreVsTotal = descriptor.Purpose{
		Plain:   "How many standard deviations each group's value sits from the average of all groups on a grouped result: which groups stand out?",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which stores' sales are unusually high or low compared with the other stores?",
			"Which regions stand out from the rest on average rating?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Highlight segments whose mean rating is far from the average segment.",
			descriptor.DomainOps:     "Flag sites whose volume is far from the typical site's.",
			descriptor.DomainScience: "Put group means on a common scale before plotting.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to test whether group averages differ, from raw rows", Use: "TEST_ANOVA_WELCH"},
			{When: "you want each group's share of the total", Use: "OVERLAY_SHARE_OF_TOTAL"},
			{When: "you want each group against one chosen group", Use: "OVERLAY_DELTA_VS_SIBLING"},
		},
		Assumptions: []string{
			overlayDescriptiveZ,
			"The standard deviation is the spread of the group values themselves (dividing by the number of groups), not the spread of rows within a group; with few groups the values are unstable.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"mean", "outlier", "standard-deviation", "z-score"},
	}
)

// --- Against a named group, a baseline point or the prior point ----------

var (
	purposeOverlayIndexVsSibling = descriptor.Purpose{
		Plain:   "Each group's value as an index value against one named group (group / sibling x 100), such as every store against the flagship.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How does each store's revenue compare with the flagship store's?",
			"How does each region's average rating compare with the home region's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Index every segment against a chosen reference segment.",
			descriptor.DomainOps:     "Index every site against the best-run site.",
			descriptor.DomainScience: "Index every arm's mean against the control arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the gap in the value's own units", Use: "OVERLAY_DELTA_VS_SIBLING"},
			{When: "the reference is a fixed position in an ordered series, such as the first month", Use: "OVERLAY_INDEX_VS_BASELINE"},
			{When: "you want to test whether two groups' averages differ, from raw rows", Use: "TEST_WELCH"},
		},
		Assumptions: []string{
			"The named group must exist in the result; an unknown group gives no values and a warning.",
			"A zero or small reference value makes every index swing widely; a zero one gives no values.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayDeltaVsSibling = descriptor.Purpose{
		Plain:   "Each group's value minus one named group's value, in the value's own units: how far every group sits from, say, the control group.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How much more or less revenue than the flagship store does each store bring in?",
			"How many points above or below the control arm is each arm's average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Show every segment's mean rating as a gap from a reference segment.",
			descriptor.DomainOps:     "Show every site's cost as a gap from the best-run site.",
			descriptor.DomainScience: "Show every arm's mean as a gap from the control arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a ratio rather than a gap", Use: "OVERLAY_INDEX_VS_SIBLING"},
			{When: "you want to test whether two groups' averages differ, from raw rows", Use: "TEST_WELCH"},
			{When: "the reference is a fixed position in an ordered series", Use: "OVERLAY_DELTA_VS_BASELINE"},
		},
		Assumptions: []string{
			"The named group must exist in the result; an unknown group gives no values and a warning.",
			"When the values are percentages the gap is in percentage points, not percent.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "percentage-point"},
	}

	purposeOverlayIndexVsBaseline = descriptor.Purpose{
		Plain:   "Each point of an ordered series as an index value against a chosen baseline point (point / baseline x 100), e.g. growth since launch.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"How have monthly sales grown relative to the launch month?",
			"How does each wave's score compare with the first wave, scaled to 100?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Track a tracker metric relative to the first wave.",
			descriptor.DomainOps:     "Track weekly volume relative to a reference week.",
			descriptor.DomainScience: "Express each time point relative to the pre-treatment measurement.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the gap in the value's own units", Use: "OVERLAY_DELTA_VS_BASELINE"},
			{When: "you want each point against the one before it", Use: "OVERLAY_INDEX_VS_PRIOR"},
			{When: "you want each period against the same period a year earlier", Use: "OVERLAY_YOY"},
			{When: "you want to test for a steady rise or fall across the series", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"The baseline is a position in the host's order, so the series must be ordered (for example by GROUP_DATE).",
			"A zero baseline gives no values and a warning; a small baseline makes every index swing widely.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayDeltaVsBaseline = descriptor.Purpose{
		Plain:   "Each point of an ordered series minus a chosen baseline point, in the value's own units: how much it has moved since then.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"How many more orders a week do we get than in the launch week?",
			"How many points has satisfaction moved since the first wave?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Show each wave's score as a gap from the first wave.",
			descriptor.DomainOps:     "Show each week's volume as a gap from a reference week.",
			descriptor.DomainScience: "Show each time point as a change from the pre-treatment measurement.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a ratio rather than a gap", Use: "OVERLAY_INDEX_VS_BASELINE"},
			{When: "you want each point against the one before it", Use: "OVERLAY_DELTA_VS_PRIOR"},
			{When: "you want to test for a steady rise or fall across the series", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"The baseline is a position in the host's order, so the series must be ordered (for example by GROUP_DATE).",
			"When the values are percentages the gap is in percentage points, not percent.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "percentage-point"},
	}

	purposeOverlayIndexVsPrior = descriptor.Purpose{
		Plain:   "Each point of an ordered series as an index value against the point before it (point / prior x 100): period-on-period change.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"By how much did sales grow or shrink from each month to the next?",
			"Is this week's volume above or below last week's, scaled to 100?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Wave-on-wave change of a tracker metric.",
			descriptor.DomainOps:     "Month-on-month growth of orders.",
			descriptor.DomainScience: "Step-to-step change of a repeated measurement.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the change in the value's own units", Use: "OVERLAY_DELTA_VS_PRIOR"},
			{When: "the data are seasonal and you want the same period a year earlier", Use: "OVERLAY_YOY"},
			{When: "you want each point against its recent average rather than one point", Use: "OVERLAY_INDEX_VS_ROLLING_MEAN"},
		},
		Assumptions: []string{
			"The series must be ordered. The first point has no prior and gets no value; a missing point is skipped and the next one compares with the last present point, so a gap can span more than one period.",
			"A zero prior gives no value and a warning.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayDeltaVsPrior = descriptor.Purpose{
		Plain:   "Each point of an ordered series minus the point before it, in the value's own units: how much it changed from the previous period.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"How many more or fewer orders did we get than the month before?",
			"How many points did satisfaction move from each wave to the next?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Wave-on-wave change of a tracker metric in points.",
			descriptor.DomainOps:     "Month-on-month change in ticket volume.",
			descriptor.DomainScience: "Step-to-step change of a repeated measurement.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the change as a ratio", Use: "OVERLAY_INDEX_VS_PRIOR"},
			{When: "you want the change as a window column on the result rather than an overlay", Use: "WIN_DELTA"},
			{When: "you want each point against a fixed starting point", Use: "OVERLAY_DELTA_VS_BASELINE"},
		},
		Assumptions: []string{
			"The series must be ordered. The first point has no prior and gets no value; a missing point is skipped and the next one compares with the last present point.",
			"When the values are percentages the change is in percentage points, not percent.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "percentage-point"},
	}

	purposeOverlayIndexVsRollingMean = descriptor.Purpose{
		Plain:   "Each point of an ordered series as an index value against the rolling mean of the previous W points: is this period above its recent run?",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"Is this week's volume above or below the average of the last four weeks?",
			"Which days ran well above their recent average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Compare each day's orders with the average of the previous week.",
			descriptor.DomainSurvey:  "Compare each wave with the average of the last few waves.",
			descriptor.DomainScience: "Compare each reading with the average of the preceding readings.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the gap in standard deviations of the window", Use: "OVERLAY_ZSCORE_VS_ROLLING"},
			{When: "you want each point against just the one before it", Use: "OVERLAY_INDEX_VS_PRIOR"},
			{When: "you want the smoothed series itself as a column", Use: "WIN_MOVING_AVG"},
		},
		Assumptions: []string{
			"The series must be ordered and params.window set. The first W points get no value while the window fills; a missing point does not advance the window.",
			"A short window reacts fast but is jumpy; a zero rolling mean gives no value and a warning.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "index-value", "rolling-mean"},
	}

	purposeOverlayZScoreVsRolling = descriptor.Purpose{
		Plain:   "How many standard deviations each point sits from the rolling mean of the previous W points: a simple flag for unusual periods.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"Which days had unusually high or low orders compared with the previous two weeks?",
			"Which readings jumped far from their recent run?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Flag days whose volume is far from the recent run.",
			descriptor.DomainSurvey:  "Flag waves whose score is far from the last few waves.",
			descriptor.DomainScience: "Flag readings far from the preceding readings.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to test for a steady rise or fall across the whole series", Use: "TEST_TREND"},
			{When: "you want the ratio to the rolling mean", Use: "OVERLAY_INDEX_VS_ROLLING_MEAN"},
			{When: "you want a standardized column for each row rather than per period", Use: "ATTR_ZSCORE"},
		},
		Assumptions: []string{
			overlayDescriptiveZ,
			"The standard deviation is the sample spread of the window (dividing by n - 1); a short window gives a jumpy one, and a trend or seasonality makes many points look unusual.",
			"The series must be ordered and params.window set; points get no value until the window holds at least 2 values.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"outlier", "rolling-mean", "standard-deviation", "z-score"},
	}

	purposeOverlayYoY = descriptor.Purpose{
		Plain:   "Each period of a date-grouped series as an index value against the same period one year earlier (x 100): year-over-year change.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"How do this year's monthly sales compare with the same months last year?",
			"Is this quarter's volume up or down on the same quarter last year?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Compare monthly orders with the same month last year, removing seasonality.",
			descriptor.DomainSurvey:  "Compare each quarter's tracker score with the same quarter a year before.",
			descriptor.DomainScience: "Compare seasonal readings with the same season a year earlier.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want each period against the one before", Use: "OVERLAY_INDEX_VS_PRIOR"},
			{When: "you want each period against one fixed starting period", Use: "OVERLAY_INDEX_VS_BASELINE"},
			{When: "you want to test for a steady rise or fall across the series", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"The host's single grouper must be GROUP_DATE. For weekly and coarser periods the comparison steps back a fixed number of positions (12 for months), so the series must have no missing periods.",
			"The first year has no value. A zero prior-year value gives no value and a warning, and 29 February has no counterpart in a non-leap year.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value"},
	}
)

// --- Facet subset against a comparison population ------------------------

var (
	purposeOverlayIndexVsPop = descriptor.Purpose{
		Plain:   "Each category's (or histogram bin's) share in a facet subset as an index value against its share in a comparison population (x 100).",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which brands over-index among young buyers compared with all buyers?",
			"Which price bands are over-represented in this segment?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Profile a segment: which answers it over- or under-indexes on against the full sample.",
			descriptor.DomainOps:     "Show which product categories a region buys more or less of than the whole business.",
			descriptor.DomainScience: "Show which categories are over-represented in a cohort against the reference population.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want one test of whether the subset's mix differs", Use: "OVERLAY_CHISQ_VS_POP"},
			{When: "you want the differences on a standardized scale", Use: "OVERLAY_ZSCORE_VS_POP"},
			{When: "the comparison is within a crosstab rather than a facet", Use: "OVERLAY_INDEX_VS_MARGIN"},
		},
		Assumptions: []string{
			"Small categories give unstable index values: a handful of rows can produce 300 or 20, so check the counts behind them.",
			"A category absent from the population gets no value and a warning; a numeric field needs IncludeHistogram for bins.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"baseline", "index-value", "sample-size"},
	}

	purposeOverlayZScoreVsPop = descriptor.Purpose{
		Plain:   "Puts each category's share gap with a comparison population on a z-score scale; for numeric bins it says nothing about the subset.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"Which answers does this segment pick more or less often than the comparison population, relative to how much answer shares vary?",
			"Which categories stand out in this region compared with the whole business?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Rank the answers a segment over- or under-picks, on one scale.",
			descriptor.DomainOps:     "Highlight the categories a region buys unusually often or rarely.",
			descriptor.DomainScience: "Highlight categories whose share in a cohort sits far from the population's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a test of whether the subset's category mix differs", Use: "OVERLAY_CHISQ_VS_POP"},
			{When: "you want a test of whether a numeric distribution differs", Use: "OVERLAY_KS_VS_POP"},
			{When: "a ratio is easier to explain", Use: "OVERLAY_INDEX_VS_POP"},
		},
		Assumptions: []string{
			overlayDescriptiveZ,
			"For categories it divides each share gap by the spread of the population's shares across categories, not by a standard error, so it does not shrink as the subset grows.",
			"For a numeric field it standardizes each histogram bin's centre against the population mean and standard deviation: the subset's counts never enter it, so the values are the same for any subset. Use OVERLAY_KS_VS_POP to compare a numeric subset.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "standard-deviation", "standard-error", "z-score"},
	}
)

// --- Compose and process-chain comparisons -------------------------------

var (
	purposeOverlayIndexVsRef = descriptor.Purpose{
		Plain:   "Index value of each cell or group of a target Compose request against the same spot in the reference request (target / reference x 100).",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How does this wave's result per segment compare with last wave's, scaled to 100?",
			"How does each region's revenue this year compare with last year's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Index this wave's table against last wave's.",
			descriptor.DomainOps:     "Index this period's site figures against the prior period's.",
			descriptor.DomainScience: "Index a treatment cohort's subgroup means against the control cohort's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the gap in the value's own units", Use: "OVERLAY_DELTA_VS_REF"},
			{When: "you compare several targets with one reference", Use: "OVERLAY_PANEL_INDEX_VS_REF"},
			{When: "you want a test of whether the shares differ", Use: "OVERLAY_PROP_Z_CELL"},
		},
		Assumptions: []string{
			"Both requests must share a schema and line up on the same keys; a coordinate missing from the reference gets no value and a warning.",
			"A zero reference value gives no value; a small one makes the index swing widely. params.scale changes the 100.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayDeltaVsRef = descriptor.Purpose{
		Plain:   "Each cell or group of a target Compose request minus the same coordinate in the reference request, in the target's own units.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How many points did each segment's score move since last wave?",
			"How much more or less did each region sell this year than last?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Show this wave's table as point changes from last wave.",
			descriptor.DomainOps:     "Show this period's site figures as changes from the prior period.",
			descriptor.DomainScience: "Show a treatment cohort's subgroup means as gaps from the control cohort's.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a ratio rather than a gap", Use: "OVERLAY_INDEX_VS_REF"},
			{When: "you want a test of whether the means differ", Use: "OVERLAY_T_CELL"},
			{When: "you want a test of whether the shares differ", Use: "OVERLAY_PROP_Z_CELL"},
		},
		Assumptions: []string{
			"Both requests must share a schema and line up on the same keys; a coordinate missing from the reference gets no value and a warning.",
			"When the values are percentages the gap is in percentage points, not percent.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "percentage-point"},
	}

	purposeOverlayPanelIndexVsRef = descriptor.Purpose{
		Plain:   "Index values of several target Compose requests against one shared reference request, one layer per target (target / reference x 100).",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How do the last four waves each compare with the benchmark wave?",
			"How does each market compare with the reference market, cell by cell?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Index every wave of a tracker against a benchmark wave.",
			descriptor.DomainOps:     "Index every region's table against the national table.",
			descriptor.DomainScience: "Index several treatment cohorts against one control cohort.",
		},
		NotFor: []descriptor.Alternative{
			{When: "there is only one target", Use: "OVERLAY_INDEX_VS_REF"},
			{When: "you want tests between every pair of requests", Use: "OVERLAY_PROP_Z_PANEL"},
		},
		Assumptions: []string{
			"Every target must share a schema with the reference and line up on the same keys; OverlayOptions.MaxPanelTargets caps the number of targets.",
			"A zero reference value gives no value; a small one makes the index swing widely.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayIndexVsStage = descriptor.Purpose{
		Plain:   "Index value of a process-chain stage's result against an earlier stage's result at the same coordinate (target / reference x 100).",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"What share of the starting figure is left after each filtering stage, scaled to 100?",
			"How does the refined stage's result compare with the first stage's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Show how much of the starting volume each funnel stage keeps.",
			descriptor.DomainSurvey:  "Show how a cleaned sample's figures compare with the raw sample's.",
			descriptor.DomainHarness: "Compare a chain's final stage with its source stage in one response.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the gap in the value's own units", Use: "OVERLAY_DELTA_VS_STAGE"},
			{When: "the results come from separate requests rather than chain stages", Use: "OVERLAY_INDEX_VS_REF"},
		},
		Assumptions: []string{
			"Both stages must have the same result shape; otherwise the layer is empty with a warning.",
			"A zero reference value gives no value and a warning.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "index-value"},
	}

	purposeOverlayDeltaVsStage = descriptor.Purpose{
		Plain:   "A process-chain stage's result minus an earlier stage's result at the same coordinate, in the later stage's own units.",
		Intents: []string{IntentBenchmark},
		Questions: []string{
			"How many records or how much revenue does each filtering stage remove?",
			"How far does the refined stage's figure move from the first stage's?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainOps:     "Show how much volume each funnel stage drops.",
			descriptor.DomainSurvey:  "Show how cleaning the sample moves each figure.",
			descriptor.DomainHarness: "Diff a chain's final stage against its source stage in one response.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want a ratio rather than a gap", Use: "OVERLAY_INDEX_VS_STAGE"},
			{When: "the results come from separate requests rather than chain stages", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			"Both stages must have the same result shape; otherwise the layer is empty with a warning.",
			"When the values are percentages the gap is in percentage points, not percent.",
		},
		Level:    descriptor.LevelIntermediate,
		Glossary: []string{"baseline", "percentage-point"},
	}

	purposeOverlayRank = descriptor.Purpose{
		Plain:   "Ranks each cell of a target Compose request's crosstab (1 = largest) within its row, its column or the whole table.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"Which product is the top seller in each region?",
			"Where does each answer rank within its segment?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Rank answers within each segment for a top-box summary.",
			descriptor.DomainOps:     "Rank products within each region by revenue.",
			descriptor.DomainScience: "Rank conditions within each site by mean outcome.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want each cell's share of the whole", Use: "OVERLAY_SHARE_OF_TOTAL"},
			{When: "you want how far each cell sits from its row or column", Use: "OVERLAY_ZSCORE_VS_MARGIN"},
			{When: "you want the change from the reference request", Use: "OVERLAY_DELTA_VS_REF"},
		},
		Assumptions: []string{
			"A rank hides how far apart values are: first and second may be nearly equal or far apart.",
			"Tied values share the average rank, and missing cells are left out, so ranks cover only the cells present.",
			"The reference request only anchors alignment; its values are not used.",
		},
		Level:    descriptor.LevelBasic,
		Glossary: []string{"cross-tabulation", "rank", "ties"},
	}

	purposeOverlayFormula = descriptor.Purpose{
		Plain:   "Computes a custom figure for every cell, group or total from an expression over the value and its margins, totals or prior point.",
		Intents: []string{IntentDescribe},
		Questions: []string{
			"Can I show each cell as its gap from the row margin divided by the grand total?",
			"Can I flag each month whose value is more than 10% above the prior month?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "A house-style index that no built-in kind computes.",
			descriptor.DomainOps:     "A custom ratio of each cell to its margins for a dashboard.",
			descriptor.DomainHarness: "Prototype a new overlay before registering it as an extension kind.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you need a derived field on every row before aggregation", Use: "ATTR_FORMULA"},
			{When: "a built-in kind already computes it, such as an index against a margin", Use: "OVERLAY_INDEX_VS_MARGIN"},
			{When: "you want a share of a row", Use: "OVERLAY_SHARE_OF_ROW"},
		},
		Assumptions: []string{
			"Pulse checks the variable names, not the statistics: a formula that divides by a margin or compares values is only as sound as you make it.",
			"The variables depend on the host shape (cell and margins for a crosstab, value, total and prior for a series, value for a total).",
		},
		Level:    descriptor.LevelAdvanced,
		Glossary: []string{"margin"},
	}
)
