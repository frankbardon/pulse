package descriptor

import "github.com/frankbardon/pulse/descriptor"

// overlayInterpretations is the Interpretation registry for the
// OVERLAY_* kinds, keyed by kind: every Inferential kind (the set
// TestOverlayCapabilities_InferentialFlag pins) plus the four descriptive
// OVERLAY_ZSCORE_* kinds. builtinInterpretations assembles it with the
// other category maps.
//
// Field paths follow where each kind really puts its numbers (see the
// internal/processing/overlay*.go handlers): a SCALAR kind fills
// payload.scalar plus the layer summary; a MATRIX kind fills
// cells.value; a SERIES kind fills each series entry's summary, which is
// what summary.<key> names on a series kind. Slot quirks:
// OVERLAY_CHISQ_VS_REF carries its p-value in scalar (the chi-square is
// in summary.statistic) and OVERLAY_T_VS_REF / OVERLAY_Z_VS_REF carry
// theirs in summary.statistic. Every field that holds a p-value cites
// the shared rule set; the guidance lint's pValueFieldsByOperator names
// the slots whose path does not say p_value.
//
// The ZSCORE_* readings carry no bands on purpose: no published
// convention bands a descriptive z-score of this kind, and the
// familiar 1.96 / 2.58 cut-offs are critical values of a test statistic
// whose standard error is known, which these are not
// (testdata/conventions.json lists them under excluded). Each deferred
// summary.parameters.* path is proved emitted by an
// overlayInterpretationProbes host fixture in internal/service.
var overlayInterpretations = map[string][]descriptor.Interpretation{
	"OVERLAY_CHISQ_COL":                     interpOverlayChiSqCol,
	"OVERLAY_CHISQ_MATRIX":                  interpOverlayChiSqMatrix,
	"OVERLAY_CHISQ_ROW":                     interpOverlayChiSqRow,
	"OVERLAY_CHISQ_VS_POP":                  interpOverlayChiSqVsPop,
	"OVERLAY_CHISQ_VS_REF":                  interpOverlayChiSqVsRef,
	"OVERLAY_FISHER_EXACT_CELL":             interpOverlayFisherExactCell,
	"OVERLAY_KS_VS_POP":                     interpOverlayKSVsPop,
	"OVERLAY_PAIRWISE_PROBIT_T":             interpOverlayPairwiseProbitT,
	"OVERLAY_PAIRWISE_PROP_Z":               interpOverlayPairwisePropZ,
	"OVERLAY_PAIRWISE_TWO_MEANS_Z":          interpOverlayPairwiseTwoMeansZ,
	"OVERLAY_PAIRWISE_WEIGHTED_TWO_MEANS_Z": interpOverlayPairwiseWeightedTwoMeansZ,
	"OVERLAY_PAIRWISE_WELCH_T":              interpOverlayPairwiseWelchT,
	"OVERLAY_PROP_Z_CELL":                   interpOverlayPropZCell,
	"OVERLAY_PROP_Z_PANEL":                  interpOverlayPropZPanel,
	"OVERLAY_T_CELL":                        interpOverlayTCell,
	"OVERLAY_T_VS_REF":                      interpOverlayTVsRef,
	"OVERLAY_Z_CELL":                        interpOverlayZCell,
	"OVERLAY_Z_VS_REF":                      interpOverlayZVsRef,
	"OVERLAY_ZSCORE_VS_MARGIN":              interpOverlayZScoreVsMargin,
	"OVERLAY_ZSCORE_VS_POP":                 interpOverlayZScoreVsPop,
	"OVERLAY_ZSCORE_VS_ROLLING":             interpOverlayZScoreVsRolling,
	"OVERLAY_ZSCORE_VS_TOTAL":               interpOverlayZScoreVsTotal,
}

// Shared caveat sentences, so sibling kinds say the same thing the same
// way.
const (
	overlayCellMultiComp = "Every cell is a separate test and Pulse reports the p-values raw: with many cells some small ones turn up by luck, " +
		"so correct for multiple comparisons (for example Holm or Bonferroni) before flagging cells."
	overlayPairMultiComp = "Every cell is one pair at one level of the other axis, and Pulse reports the p-values raw: with many pairs some small ones " +
		"turn up by luck, so correct for multiple comparisons (for example Holm or Bonferroni) before flagging pairs."
	overlayGroupMultiComp = "Every group is a separate test and Pulse reports the p-values raw: with many groups some small ones turn up by luck, " +
		"so correct for multiple comparisons yourself."
	overlayPlaceholderP = "Without AGG_WELFORD values (or explicit variance and sample-size params) Pulse computes the p-value from placeholder " +
		"inputs (variance 1, n 2), and it then describes those placeholders, not your data."
	overlayNoEffectSize = "The layer does not report an effect size: read the gap itself (for example OVERLAY_DELTA_VS_REF) for how big a difference is."
	overlayPairNoEffect = "The layer does not report an effect size: read the two cell values for how big each gap is."
	overlayChiSqScale   = "Chi-square grows with the counts and with the number of cells, so it does not measure how strongly the categories are associated; " +
		"read the p-value for the test and the cell values (or TEST_CHISQ's Cramer's V on raw rows) for strength."
	overlayZScoreNoBands = "There are no sourced bands for reading this z-score: the familiar 1.96 / 2.58 cut-offs are critical values of a test " +
		"statistic built from a standard error, and this value is built from a spread of values instead, so it has no p-value behind it."
)

// zScoreSign is the direction reading every ZSCORE_* kind shares.
var zScoreSign = map[string]string{
	"+": "the value sits above its centre",
	"-": "the value sits below its centre",
}

// --- Chi-square family -------------------------------------------------

var (
	interpOverlayChiSqMatrix = []descriptor.Interpretation{
		{
			Field: "scalar",
			Means: "The chi-square statistic for the whole crosstab, also in summary.statistic: the sum over cells of (observed - expected)^2 / expected, " +
				"where expected is the count the row and column totals predict if the two fields were independent. 0 means the table matches that " +
				"pattern exactly; how far above 0 is unusual depends on summary.parameters.df, so read summary.p_value rather than the raw value.",
			Caveats: []string{
				overlayChiSqScale,
				"No continuity (Yates) correction is applied.",
			},
		},
		{
			Field: "summary.statistic",
			Means: "The same chi-square statistic as scalar.",
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"It comes from the chi-square approximation, which is unreliable when expected counts fall below about 5; Pulse warns (PULSE_OVERLAY_EXPECTED_LOW) when they do.",
				"It is meaningful only when the cells count independent rows (AGG_COUNT); on sums or averages it is not a valid p-value.",
			},
		},
		{
			Field: "summary.parameters.df",
			Means: "Degrees of freedom of the chi-square reference curve: (rows - 1) x (columns - 1) of the host crosstab.",
			Caveats: []string{
				"It reflects the table's shape, not its number of rows of data.",
			},
		},
	}

	interpOverlayChiSqRow = []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry (one per crosstab row): chi-square for that row's counts against the counts its row total would give at the " +
				"table's overall column shares. 0 means the row's mix matches the overall mix; read the entry's p_value rather than the raw value.",
			Caveats: []string{
				overlayChiSqScale,
				"The overall column shares include the row itself, so a row that holds most of the table pulls them toward itself and reads closer to 0.",
			},
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"Every row is a separate test and Pulse reports the p-values raw: with many rows some small ones turn up by luck, so correct for multiple comparisons yourself.",
				"It comes from the chi-square approximation; Pulse warns once per row whose expected counts fall below 5. A row with a zero total gets no value (NaN).",
			},
		},
		{
			Field: "summary.parameters.df",
			Means: "In each series entry: degrees of freedom of the row's chi-square, columns - 1, the same for every row.",
		},
	}

	interpOverlayChiSqCol = []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry (one per crosstab column): chi-square for that column's counts against the counts its column total would give " +
				"at the table's overall row shares. 0 means the column's mix matches the overall mix; read the entry's p_value rather than the raw value.",
			Caveats: []string{
				overlayChiSqScale,
				"The overall row shares include the column itself, so a column that holds most of the table pulls them toward itself and reads closer to 0.",
			},
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"Every column is a separate test and Pulse reports the p-values raw: with many columns some small ones turn up by luck, so correct for multiple comparisons yourself.",
				"It comes from the chi-square approximation; Pulse warns once per column whose expected counts fall below 5. A column with a zero total gets no value (NaN).",
			},
		},
		{
			Field: "summary.parameters.df",
			Means: "In each series entry: degrees of freedom of the column's chi-square, rows - 1, the same for every column.",
		},
	}

	interpOverlayChiSqVsPop = []descriptor.Interpretation{
		{
			Field: "scalar",
			Means: "The chi-square statistic, also in summary.statistic: the subset's category counts against the counts the population's shares " +
				"predict at the subset's size. 0 means the subset's mix matches the population's; read summary.p_value rather than the raw value.",
			Caveats: []string{
				"It grows with the subset's size, so a very big subset gives a big value for a slight shift in mix; OVERLAY_INDEX_VS_POP shows which categories moved and by how much.",
			},
		},
		{
			Field: "summary.statistic",
			Means: "The same chi-square statistic as scalar.",
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"The population's shares are treated as known and fixed; when the subset is a big part of the population the two overlap and the p-value is only approximate.",
				"With a single category there is nothing to compare and the p-value is NaN.",
			},
		},
		{
			Field: "summary.parameters.df",
			Means: "Degrees of freedom: the number of categories in the subset's facet values, minus 1.",
			Caveats: []string{
				"A population category that is missing from the subset's facet values is left out of both the statistic and df, which understates a departure made of absent categories.",
			},
		},
	}

	interpOverlayChiSqVsRef = []descriptor.Interpretation{
		{
			Field:  "scalar",
			Shared: SharedPValue,
			Caveats: []string{
				"On this kind the scalar holds the p-value (the same value as summary.p_value), not the chi-square; the chi-square is in summary.statistic.",
				"The reference mix is treated as fixed, so its own sampling uncertainty is ignored and with a small reference the p-value comes out too small.",
			},
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"It is the same value as scalar; see that reading for why a small reference makes it too small.",
			},
		},
		{
			Field: "summary.statistic",
			Means: "The chi-square statistic: the target's cell counts against the counts the reference's cell shares predict at the target's total. " +
				"0 means the two tables have the same mix; read the p-value rather than the raw value.",
			Caveats: []string{
				overlayChiSqScale,
			},
		},
		{
			Field: "summary.parameters.df",
			Means: "Degrees of freedom: the number of cells present in both requests with a positive expected count, minus 1.",
			Caveats: []string{
				"Because the reference shares are taken as fixed, df is cells - 1, not the (rows - 1) x (columns - 1) of a test that treats both tables as samples (TEST_CHISQ on stacked rows).",
			},
		},
	}
)

// --- Per-cell and pairwise tests ----------------------------------------

var (
	interpOverlayFisherExactCell = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided exact p-value of its own 2x2 table: this row versus the rest, by this column versus the rest.",
				"It does not say which way the cell departs: compare the cell's count with what its row and column totals predict.",
				overlayCellMultiComp,
			},
		},
	}

	interpOverlayPropZCell = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided p-value comparing the target's share (cell / row total) with the reference's share in the same cell.",
				"The normal approximation needs roughly 10 successes and 10 failures on each side; with fewer the p-value is unreliable.",
				overlayCellMultiComp,
				overlayNoEffectSize,
			},
		},
	}

	interpOverlayPropZPanel = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds a list of two-sided p-values, one per pair of requests, in upper-triangle order with the reference at index 0: " +
					"(0,1), (0,2), ..., (1,2), ...; M requests give M(M-1)/2 entries.",
				"The number of tests is cells times pairs, so it grows fast: with many cells and pairs some small p-values turn up by luck. " +
					"Pulse reports them raw, so correct for multiple comparisons yourself.",
				"The normal approximation needs roughly 10 successes and 10 failures per side.",
			},
		},
	}

	interpOverlayPairwisePropZ = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided pooled two-proportion z-test p-value for one pair of rows (or columns), named by the pair-axis key; it is absent when a leg is unreadable or the test is degenerate.",
				overlayPairMultiComp,
				"The normal approximation needs roughly 10 successes and 10 failures per leg, and an n_source or p_source that does not match the cell values skips pairs.",
				overlayPairNoEffect,
			},
		},
	}

	interpOverlayPairwiseProbitT = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided p-value for one pair, from a t curve on n_i + n_j - 2 degrees of freedom after a probit transform of each share; it is absent when a leg is unreadable or the test is degenerate.",
				"The probit simplification differs from the textbook two-proportion test (OVERLAY_PAIRWISE_PROP_Z), so the two give different p-values for the same cells.",
				overlayPairMultiComp,
				overlayPairNoEffect,
			},
		},
	}

	interpOverlayPairwiseWelchT = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided Welch t-test p-value for one pair of means, with Welch-Satterthwaite degrees of freedom; it is absent when a leg is unreadable or the test is degenerate.",
				"Each mean should be roughly normal: safe for big cells, risky for small skewed ones.",
				overlayPairMultiComp,
				overlayPairNoEffect,
			},
		},
	}

	interpOverlayPairwiseTwoMeansZ = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided z-test p-value for one pair of means, read from the normal curve; it is absent when a leg is unreadable or the test is degenerate.",
				"With small cells the normal curve gives p-values that are too small; OVERLAY_PAIRWISE_WELCH_T is the safer reading there.",
				overlayPairMultiComp,
				overlayPairNoEffect,
			},
		},
	}

	interpOverlayPairwiseWeightedTwoMeansZ = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided z-test p-value for one pair of weighted means; params.n_basis sets the sample size (sum of weights, or Kish's effective sample size).",
				"It accounts for weighting only, not clustering or other design effects, and n_basis = weights overstates precision when weights are scaled to a population; either way the p-values then come out too small.",
				overlayPairMultiComp,
				overlayPairNoEffect,
			},
		},
	}

	interpOverlayTCell = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided Welch t-test p-value comparing the target's mean in that cell with the reference's.",
				overlayPlaceholderP,
				overlayCellMultiComp,
				overlayNoEffectSize,
			},
		},
	}

	interpOverlayZCell = []descriptor.Interpretation{
		{
			Field:  "cells.value",
			Shared: SharedPValue,
			Caveats: []string{
				"Each cell holds the two-sided z-test p-value comparing the target's mean in that cell with the reference's, read from the normal curve; with small cells it comes out too small.",
				overlayPlaceholderP,
				overlayCellMultiComp,
				overlayNoEffectSize,
			},
		},
	}

	interpOverlayTVsRef = []descriptor.Interpretation{
		{
			Field:  "summary.statistic",
			Shared: SharedPValue,
			Caveats: []string{
				"Despite the field name, each series entry's summary.statistic holds that group's two-sided Welch t-test p-value, not a t value; Pulse does not emit t.",
				overlayPlaceholderP,
				overlayGroupMultiComp,
				overlayNoEffectSize,
			},
		},
	}

	interpOverlayZVsRef = []descriptor.Interpretation{
		{
			Field:  "summary.statistic",
			Shared: SharedPValue,
			Caveats: []string{
				"Despite the field name, each series entry's summary.statistic holds that group's two-sided z-test p-value, not a z value; Pulse does not emit z.",
				"It is read from the normal curve, so with small groups it comes out too small.",
				overlayPlaceholderP,
				overlayGroupMultiComp,
				overlayNoEffectSize,
			},
		},
	}

	interpOverlayKSVsPop = []descriptor.Interpretation{
		{
			Field: "scalar",
			Means: "D, also in summary.statistic: the largest vertical gap between the subset's and the population's cumulative distributions, " +
				"from 0 (the same curve) to 1 (no overlap at all).",
			Caveats: []string{
				"Pulse rebuilds both curves from histograms or percentiles, not raw values, so D is measured only at bin edges or percentile points and can understate the true gap.",
				"D shows that the shapes differ, not how: compare the histograms to see whether it is the centre, the spread or the tails.",
			},
		},
		{
			Field: "summary.statistic",
			Means: "The same D as scalar.",
		},
		{
			Field:  "summary.p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"With few rows the test has low power and misses real differences; with very many rows even trivial differences in shape give small p-values.",
				"It compares two observed distributions. It is not valid for testing against a named distribution with parameters estimated from the data, which needs the Lilliefors correction.",
				"It is a two-sided large-sample approximation built from the full row counts in summary.parameters, while D comes from the binned curves.",
			},
		},
		{
			Field: "summary.parameters.n_subset",
			Means: "The number of rows behind the subset's numeric facet; one of the two counts the p-value's large-sample formula uses.",
		},
		{
			Field: "summary.parameters.n_pop",
			Means: "The number of rows behind the population's numeric summary; the other count the p-value uses.",
			Caveats: []string{
				"When the subset is part of the population these rows include the subset's, so the two samples overlap and the p-value is only approximate.",
			},
		},
	}
)

// --- Descriptive z-scores -----------------------------------------------

var (
	interpOverlayZScoreVsMargin = []descriptor.Interpretation{
		{
			Field: "cells.value",
			Means: "How far each cell sits from its row, column or grand margin figure, in standard deviations of the cell values across that slice " +
				"(dividing by the number of cells). 0 means the cell equals the margin figure.",
			Sign: zScoreSign,
			Caveats: []string{
				overlayZScoreNoBands,
				"The spread is that of the cells in the slice, not the sampling error of one cell, so a cell built from few rows can sit far out without being unusual.",
				"For counts or sums the margin is a total, so every cell sits far below it; the reading suits averages.",
			},
		},
	}

	interpOverlayZScoreVsTotal = []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry: how far the group's value sits from the average of all groups, in standard deviations of the group values " +
				"(dividing by the number of groups). 0 means the group equals that average.",
			Sign: zScoreSign,
			Caveats: []string{
				overlayZScoreNoBands,
				"With N groups no value can sit further than sqrt(N - 1) from 0 (Shiffler 1988), so with 5 groups nothing passes 2; compare values within a layer, not against a fixed cut-off.",
				"The spread is between groups, not between rows within a group.",
			},
		},
	}

	interpOverlayZScoreVsRolling = []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry: how far the point sits from the mean of the W points before it, in sample standard deviations of those W points. " +
				"0 means the point equals its recent average.",
			Sign: zScoreSign,
			Caveats: []string{
				overlayZScoreNoBands,
				"A short window gives a jumpy spread, and a trend or seasonal pattern makes many points look unusual; points stay empty (NaN) until the window holds 2 values.",
			},
		},
	}

	interpOverlayZScoreVsPop = []descriptor.Interpretation{
		{
			Field: "summary.statistic",
			Means: "In each series entry, for a categorical field: the category's share in the subset minus its share in the population, divided by the " +
				"standard deviation of the population's shares across categories. For a numeric field: a histogram bin's centre against the " +
				"population's mean, in population standard deviations.",
			Sign: zScoreSign,
			Caveats: []string{
				overlayZScoreNoBands,
				"For categories the divisor is the spread of shares across categories, not a standard error: it does not shrink as the subset grows, and it changes with how many categories there are.",
				"For a numeric field it says where each bin sits on the population's scale, not how the subset differs; use OVERLAY_KS_VS_POP for that.",
			},
		},
	}
)
