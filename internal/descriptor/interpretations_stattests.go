package descriptor

import "github.com/frankbardon/pulse/descriptor"

// statTestInterpretations is the Interpretation registry for the TEST_*
// families, keyed by family (one entry covers both tiers: post-test
// twins emit the same statistic, p-value and details keys).
// builtinInterpretations assembles it with the other category maps.
//
// Every reading states the test as Pulse computes it — see
// internal/processing/test_*.go. In particular: two-group tests order
// their groups alphabetically (Details.groups), so a positive signed
// statistic means groups[0] is higher; paired tests difference Field −
// Field2; every p-value is two-sided; bands come only from the
// convention registry (conventions.go). Each details.* path declared
// here is proved emitted, on every registered tier, by
// internal/processing's interpretationProbes.
var statTestInterpretations = map[string][]descriptor.Interpretation{
	"TEST_ANOVA_F":        interpTestAnovaF,
	"TEST_ANOVA_RM":       interpTestAnovaRM,
	"TEST_ANOVA_WELCH":    interpTestAnovaWelch,
	"TEST_BROWN_FORSYTHE": interpTestBrownForsythe,
	"TEST_CHISQ":          interpTestChiSq,
	"TEST_FISHER_EXACT":   interpTestFisherExact,
	"TEST_KENDALL_TAU":    interpTestKendallTau,
	"TEST_KRUSKAL_WALLIS": interpTestKruskalWallis,
	"TEST_KS":             interpTestKS,
	"TEST_MANN_WHITNEY_U": interpTestMannWhitneyU,
	"TEST_PAIRED_T":       interpTestPairedT,
	"TEST_PEARSON_R":      interpTestPearsonR,
	"TEST_PROP_Z":         interpTestPropZ,
	"TEST_SHAPIRO_WILK":   interpTestShapiroWilk,
	"TEST_SPEARMAN_R":     interpTestSpearmanR,
	"TEST_T":              interpTestT,
	"TEST_TREND":          interpTestTrend,
	"TEST_TUKEY_HSD":      interpTestTukeyHSD,
	"TEST_WELCH":          interpTestWelch,
	"TEST_WILCOXON_SR":    interpTestWilcoxonSR,
	"TEST_Z_TWO_SAMPLE":   interpTestZTwoSample,
}

// interpPValue is the p-value reading every family cites.
var interpPValue = descriptor.Interpretation{Field: "p_value", Shared: SharedPValue}

// bandedBy returns in with the bands, Abs flag and citation of
// convention id applied.
func bandedBy(id string, in descriptor.Interpretation) descriptor.Interpretation {
	in.Bands = conventionBands(id)
	in.Abs = conventionOf(id).Abs
	in.Convention = conventionCitation(id)
	return in
}

// Shared readings reused across families.
var (
	// Two-sample Cohen's d with the pooled SD (TEST_T two-sample,
	// TEST_WELCH, TEST_Z_TWO_SAMPLE).
	cohensDPooledCaveat = "This d divides by the pooled standard deviation of the two groups, even though the test itself does not assume equal variances; when spreads differ a lot, d describes neither group exactly."
	cohensDSign         = map[string]string{
		"+": "the first group in details.groups has the higher average",
		"-": "the second group in details.groups has the higher average",
	}
	cohensDOmitted = "When the standard deviation is zero, d is undefined and the key is left out rather than reported as 0."

	// Signed two-group statistic / difference.
	twoGroupSign = map[string]string{
		"+": "the first group in details.groups (alphabetical order) is higher",
		"-": "the second group in details.groups (alphabetical order) is higher",
	}
	// pairedMeanSign signs the paired t family by the MEAN difference;
	// pairedRankSign signs the Wilcoxon family, whose rank statistic is
	// a per-pair tendency (U08 statistics review S-11).
	pairedMeanSign = map[string]string{
		"+": "Field is larger than Field2 on average",
		"-": "Field2 is larger than Field on average",
	}
	pairedRankSign = map[string]string{
		"+": "Field tends to be larger than Field2",
		"-": "Field2 tends to be larger than Field",
	}
	correlationSign = map[string]string{
		"+": "the two fields tend to rise together",
		"-": "one field tends to fall as the other rises",
	}
	causationCaveatText = "Correlation is not causation: a third factor may drive both fields."

	ciTwoGroupMeans = "Interval for the difference between the two group averages (first minus second) at the 1 - alpha level (95% by default)."
	ciCaveat        = "An interval that excludes 0 matches a p-value below alpha; its width shows how precisely the difference is pinned down."
)

// --- Comparing averages ----------------------------------------------

var (
	interpTestT = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "t is the gap between averages measured in standard errors: the sample mean minus the target value (one-sample), " +
				"or the first group's mean minus the second's (two-sample, Welch). Values far from 0 in either direction are unlikely if the true gap is zero.",
			Sign: map[string]string{
				"+": "the sample mean is above the target, or the first group in details.groups has the higher mean",
				"-": "the sample mean is below the target, or the second group in details.groups has the higher mean",
			},
			Caveats: []string{
				"The two-sample form is Welch's: degrees of freedom (df) are adjusted for unequal variances and are usually not a whole number.",
			},
		},
		interpPValue,
		{
			Field: "details.diff",
			Means: "Two-sample only: the first group's mean minus the second's, in the field's own units.",
			Sign:  twoGroupSign,
		},
		{
			Field: "details.ci_low",
			Means: "Lower end of the confidence interval at the 1 - alpha level (95% by default): for the mean itself in the one-sample form, " +
				"for the difference in means (details.diff) in the two-sample form.",
			Caveats: []string{
				"In the one-sample form the interval is around the mean, not around mean minus target: compare it with the target value directly.",
			},
		},
		{
			Field: "details.ci_high",
			Means: "Upper end of the same confidence interval: for the mean (one-sample) or for the difference in means (two-sample).",
		},
		bandedBy(ConventionCohenD, descriptor.Interpretation{
			Field: "details.effect_size.cohens_d",
			Means: "The gap in standard-deviation units: (mean - target) / SD for one sample, " +
				"(first mean - second mean) / pooled SD for two groups. 0.5 means the averages sit half a standard deviation apart.",
			Sign: map[string]string{
				"+": "the mean is above the target, or the first group in details.groups is higher",
				"-": "the mean is below the target, or the second group in details.groups is higher",
			},
			Caveats: []string{cohensDPooledCaveat, cohensDOmitted},
		}),
	}

	interpTestWelch = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Welch's t: the first group's mean minus the second's, in standard errors computed from each group's own variance. " +
				"Values far from 0 in either direction are unlikely if the true gap is zero.",
			Sign: twoGroupSign,
			Caveats: []string{
				"Degrees of freedom (df) come from the Welch-Satterthwaite formula and are usually not a whole number.",
			},
		},
		interpPValue,
		{Field: "details.diff", Means: "The first group's mean minus the second's, in the field's own units.", Sign: twoGroupSign},
		{Field: "details.ci_low", Means: "Lower end of the " + ciTwoGroupMeans, Caveats: []string{ciCaveat}},
		{Field: "details.ci_high", Means: "Upper end of the " + ciTwoGroupMeans},
		bandedBy(ConventionCohenD, descriptor.Interpretation{
			Field:   "details.effect_size.cohens_d",
			Means:   "The gap between the two means in pooled-standard-deviation units; 0.5 means they sit half a standard deviation apart.",
			Sign:    cohensDSign,
			Caveats: []string{cohensDPooledCaveat, cohensDOmitted},
		}),
	}

	interpTestZTwoSample = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "z: the first group's mean minus the second's, in standard errors built from each group's sample variance, " +
				"read against the normal distribution. Values far from 0 in either direction are unlikely if the true gap is zero.",
			Sign: twoGroupSign,
			Caveats: []string{
				"The normal reference is only accurate for large groups; with small groups the p-value is too small, so prefer TEST_WELCH.",
			},
		},
		interpPValue,
		{Field: "details.diff", Means: "The first group's mean minus the second's, in the field's own units.", Sign: twoGroupSign},
		{Field: "details.ci_low", Means: "Lower end of the normal-theory " + ciTwoGroupMeans, Caveats: []string{ciCaveat}},
		{Field: "details.ci_high", Means: "Upper end of the normal-theory " + ciTwoGroupMeans},
		bandedBy(ConventionCohenD, descriptor.Interpretation{
			Field:   "details.effect_size.cohens_d",
			Means:   "The gap between the two means in pooled-standard-deviation units; 0.5 means they sit half a standard deviation apart.",
			Sign:    cohensDSign,
			Caveats: []string{cohensDPooledCaveat, cohensDOmitted},
		}),
	}

	interpTestPairedT = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "t is the average of the per-row differences (Field - Field2) measured in standard errors of that average. " +
				"Values far from 0 in either direction are unlikely if the true average difference is zero.",
			Sign: pairedMeanSign,
		},
		interpPValue,
		{
			Field: "details.mean_diff",
			Means: "The average of Field - Field2 over complete pairs, in the field's own units.",
			Sign:  pairedMeanSign,
		},
		{
			Field:   "details.ci_low",
			Means:   "Lower end of the confidence interval for the average difference (Field - Field2) at the 1 - alpha level (95% by default).",
			Caveats: []string{"An interval that excludes 0 matches a p-value below alpha."},
		},
		{
			Field: "details.ci_high",
			Means: "Upper end of the confidence interval for the average difference (Field - Field2).",
		},
		{
			Field: "details.effect_size.cohens_d",
			Means: "d_z: the average difference divided by the standard deviation of the differences (not the d_av of the two fields' own spreads).",
			Sign:  pairedMeanSign,
			Caveats: []string{
				"No sourced bands apply to d_z: Cohen's 0.2 / 0.5 / 0.8 were set for gaps between independent groups. " +
					"d_z = d / sqrt(2(1 - r)), so it reads larger than a between-group d when Field and Field2 correlate above 0.5 and smaller below it.",
				"Do not compare d_z directly with a two-group d; report the mean difference alongside it.",
				cohensDOmitted,
			},
		},
	}

	interpTestAnovaF = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "F compares how far apart the group averages are with how much rows vary inside each group; " +
				"larger values are stronger evidence that the groups differ by more than within-group noise; the p-value says whether this F is large enough to be surprising.",
			Caveats: []string{
				"F says whether some groups differ, not which ones: run TEST_TUKEY_HSD to find the pairs.",
				"The classic F assumes equal variances across groups; with unequal spreads and unequal group sizes prefer TEST_ANOVA_WELCH.",
			},
		},
		interpPValue,
		bandedBy(ConventionCohenEta2, descriptor.Interpretation{
			Field: "details.effect_size.eta_squared",
			Means: "The share of all variation in the measure that group membership accounts for in this sample, from 0 to 1.",
			Caveats: []string{
				"Eta squared overstates the effect in small samples; prefer omega squared when reporting.",
			},
		}),
		bandedBy(ConventionCohenEta2, descriptor.Interpretation{
			Field: "details.effect_size.omega_squared",
			Means: "A less biased estimate of the share of variation group membership accounts for, " +
				"adjusted for sample size and the number of groups.",
			Caveats: []string{
				"The raw estimate goes below zero when F is under 1; Pulse reports 0 instead: the sample shows no measurable share of variation, which does not show that the true effect is zero, especially with small groups.",
			},
		}),
	}

	interpTestAnovaWelch = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Welch's F compares the spread of the group averages with the noise inside groups, weighting each group by its own variance; " +
				"larger values are stronger evidence that the averages differ by more than that noise; the p-value says whether this F is large enough to be surprising.",
			Caveats: []string{
				"It says whether some groups differ, not which ones.",
				"The df slot holds the between-groups degrees of freedom; the adjusted within-groups df is in details.df_within and is usually not a whole number.",
			},
		},
		interpPValue,
		bandedBy(ConventionCohenEta2, descriptor.Interpretation{
			Field: "details.effect_size.omega_squared",
			Means: "Estimated share of variation group membership accounts for, from Welch's F plugged into the classic omega-squared formula; " +
				"it approximates TEST_ANOVA_F's omega squared and matches it exactly only when the variances are equal.",
			Caveats: []string{
				"The raw estimate goes below zero when F is under 1; Pulse reports 0 instead: the sample shows no measurable share of variation, which does not show that the true effect is zero, especially with small groups.",
			},
		}),
	}

	interpTestAnovaRM = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "F compares how far apart the condition averages are with the leftover noise once each subject's own level is removed; " +
				"larger values are stronger evidence that conditions differ by more than that noise; the p-value says whether this F is large enough to be surprising.",
			Caveats: []string{
				"No sphericity correction (such as Greenhouse-Geisser) is applied: with three or more conditions whose differences vary unevenly, the p-value runs too small.",
				"It says whether some conditions differ, not which ones.",
			},
		},
		interpPValue,
		{
			Field: "details.effect_size.partial_eta_squared",
			Means: "The share of the within-subject variation, after removing differences between subjects, that the conditions account for, from 0 to 1.",
			Caveats: []string{
				"There are no sourced bands for repeated-measures partial eta squared, so none are attached: Cohen's 0.01 / 0.06 / 0.14 were set for between-groups designs, " +
					"and excluding subject variance makes this figure read larger for the same shift; compare it only with other repeated-measures results.",
			},
		},
		{
			Field: "details.dropped_subjects",
			Means: "How many subjects were left out because they lacked a value for at least one condition; only complete subjects enter the test.",
			Caveats: []string{
				"If dropped subjects differ from the rest (for example, people who quit early), the result describes the completers only.",
			},
		},
	}

	interpTestTukeyHSD = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Not used: TEST_TUKEY_HSD reports no single statistic and leaves this slot at 0. Each pair's studentized range q is in details.comparisons.",
		},
		{
			Field:  "p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"This is the smallest Tukey-adjusted p-value across all pairs, already corrected for multiple comparisons; read details.comparisons for which pairs it belongs to.",
			},
		},
		{
			Field: "details.comparisons",
			Means: "One entry per pair of groups (a, b in alphabetical order): diff is mean a minus mean b, q the studentized range, " +
				"p_adj the family-wise adjusted p-value, and ci_low / ci_high a simultaneous confidence interval for diff.",
			Caveats: []string{
				"p_adj and the intervals already allow for the many comparisons made, so do not adjust them again.",
				"An interval that excludes 0 matches p_adj below alpha.",
			},
		},
	}
)

// --- Spread and shape -------------------------------------------------

var (
	interpTestBrownForsythe = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "F on each row's distance from its group median: larger values are stronger evidence that the groups differ in spread; " +
				"the p-value says whether this F is large enough to be surprising.",
			Caveats: []string{
				"It tests spread only, not averages.",
			},
		},
		interpPValue,
	}

	interpTestKS = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "D is the largest vertical gap between the two groups' cumulative distributions, from 0 (identical) to 1 (no overlap at all).",
			Caveats: []string{
				"D is most sensitive to differences near the middle of the distributions and less to differences in the tails.",
				"D shows that the shapes differ, not how: plot both distributions to see whether it is the centre, the spread or the tails.",
			},
		},
		interpPValue,
	}

	interpTestShapiroWilk = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "W' (Shapiro-Francia) measures how closely the sorted values follow the straight line expected of normal data, up to 1; " +
				"values clearly below 1 point to skew, heavy tails or other departures from normal.",
			Caveats: []string{
				"With SplitBy, the headline W' and p-value belong to the group with the smallest p; read details.per_group for every group.",
			},
		},
		{
			Field:  "p_value",
			Shared: SharedPValue,
			Caveats: []string{
				"A small p-value is evidence against normality; a large one only means a departure was not detected, which is likely with few rows.",
				"With SplitBy the headline is the smallest p across groups, so it is more likely to be small than any single group's p.",
			},
		},
		{
			Field: "details.per_group",
			Means: "One entry per group (or one overall without SplitBy) with n, w, z and p_value, plus a warning when the p-value is advisory (fewer than 5 or more than 5000 rows, or a degenerate sample).",
		},
	}
)

// --- Rank-based comparisons -------------------------------------------

var (
	interpTestMannWhitneyU = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "U counts, over every pair of rows from the two groups, how often one group's value is larger (ties count half). " +
				"Pulse reports the smaller of the two counts, from 0 to half of n_A times n_B: the smaller U, the more the groups separate.",
			Caveats: []string{
				"U carries no direction; read details.z or the rank-biserial for which group tends to be larger.",
				"R's wilcox.test reports the first group's count instead of the smaller one, so the two can disagree while the p-values match.",
			},
		},
		interpPValue,
		{
			Field: "details.z",
			Means: "The normal score the p-value is read from, with a continuity correction of one half.",
			Sign:  twoGroupSign,
			Caveats: []string{
				"The p-value always uses this large-sample approximation, even for small groups where an exact test would be more accurate.",
			},
		},
		{
			Field: "details.effect_size.rank_biserial",
			Means: "Rank-biserial correlation, from -1 to +1: the chance that a random row from the first group beats one from the second, minus the reverse.",
			Sign:  twoGroupSign,
			Caveats: []string{
				"There are no sourced bands for rank-biserial r that Pulse could verify, so none are attached; (r + 1) / 2 is the probability that a first-group row is larger, ties counting half.",
				"It measures how often one group's values exceed the other's, not a difference in medians.",
			},
		},
	}

	interpTestWilcoxonSR = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "The smaller of the two signed-rank sums: rank the non-zero differences (Field - Field2) by size, " +
				"add the ranks of the positive ones and of the negative ones, and keep the smaller. The smaller it is, the more one direction dominates.",
			Caveats: []string{
				"It carries no direction; read details.z or the rank-biserial for which field tends to be larger.",
			},
		},
		interpPValue,
		{
			Field: "details.z",
			Means: "The normal score the p-value is read from, with a continuity correction of one half.",
			Sign:  pairedRankSign,
			Caveats: []string{
				"The p-value always uses this large-sample approximation, even for few pairs where an exact test would be more accurate.",
			},
		},
		{
			Field: "details.zero_diffs",
			Means: "How many pairs had Field equal to Field2; they are dropped before ranking, and details.n counts only the pairs that remain.",
			Caveats: []string{
				"Many zero differences shrink the test to the pairs that changed; report how many were set aside.",
			},
		},
		{
			Field: "details.effect_size.rank_biserial",
			Means: "Matched-pairs rank-biserial correlation, from -1 to +1: the rank mass of positive differences minus that of negative ones, as a share of the total.",
			Sign:  pairedRankSign,
			Caveats: []string{
				"There are no sourced bands for rank-biserial r that Pulse could verify, so none are attached.",
				"Computed over non-zero differences only, matching the test.",
			},
		},
	}

	interpTestKruskalWallis = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "H measures how far each group's average rank sits from the overall average rank, corrected for ties; " +
				"larger values are stronger evidence that some groups tend to have higher values than others; the p-value says whether this H is large enough to be surprising.",
			Caveats: []string{
				"It says whether some groups tend to be higher, not which ones: follow up with pairwise rank tests adjusted for multiple comparisons.",
				"The p-value reads H against a chi-square distribution with groups minus 1 degrees of freedom, which needs roughly five or more rows per group.",
			},
		},
		interpPValue,
		{
			Field: "details.effect_size.epsilon_squared",
			Means: "The share of variation in the ranks that group membership accounts for, from 0 to 1.",
			Caveats: []string{
				"There are no sourced bands for rank-based epsilon squared, so none are attached; Cohen's eta-squared benchmarks were set for raw-value variance.",
				"It is computed on ranks, so it describes how well groups order the values, not the share of variation in the raw values.",
				"Left out when every value is tied.",
			},
		},
	}
)

// --- Counts and rates -------------------------------------------------

var (
	interpTestChiSq = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Pearson's chi-square adds up, over every cell, how far the observed count is from the count expected if the two fields were unrelated. " +
				"Larger values mean the table departs more from that pattern.",
			Caveats: []string{
				"Chi-square grows with the number of rows, so it does not measure the strength of the association: read Cramer's V.",
				"No continuity (Yates) correction is applied, so on a 2x2 table the p-value is smaller than R's chisq.test default.",
			},
		},
		interpPValue,
		{
			Field: "details.expected_min",
			Means: "The smallest expected count across all cells.",
			Caveats: []string{
				"Below 5 the chi-square approximation becomes unreliable (Pulse also warns); for a 2x2 table use TEST_FISHER_EXACT instead.",
			},
		},
		{
			Field: "details.effect_size.cramers_v",
			Means: "Cramer's V, the strength of the association from 0 (unrelated) to 1 (one field fully determines the other), comparable across sample sizes.",
			Caveats: []string{
				"There are no standard bands that hold for every table size: Cohen's small / medium / large benchmarks for V are 0.1 / 0.3 / 0.5 divided by the square root of df*, the smaller of rows - 1 and columns - 1.",
				"V carries no direction; inspect the table to see which cells drive it.",
			},
		},
		bandedBy(ConventionCohenW, descriptor.Interpretation{
			Field: "details.effect_size.phi",
			Means: "Phi, reported for 2x2 tables only: the strength of the association from 0 to 1, equal to Cramer's V and to Cohen's w there.",
			Caveats: []string{
				"Pulse reports phi unsigned; inspect the table to see which way the association runs.",
			},
		}),
	}

	interpTestFisherExact = []descriptor.Interpretation{
		bandedBy(ConventionCohenOR, descriptor.Interpretation{
			Field: "statistic",
			Means: "The sample odds ratio (a*d)/(b*c) of the 2x2 table in details.contingency, also in details.odds_ratio: " +
				"1 means the odds of the first column are the same in both rows, above 1 higher in the first row, below 1 lower.",
			Caveats: []string{
				"Bands apply to max(OR, 1/OR), so an odds ratio of 0.25 reads like one of 4.",
				"Rows and columns follow the order values were first seen (details.row_labels / col_labels), so the ratio can come out inverted from what you expect: check the labels.",
				"A zero in b or c gives an infinite ratio and a zero in a or d gives 0; with zero cells read the p-value and the table, not the ratio.",
				"This is the plain cross-product ratio, not the conditional estimate R's fisher.test reports, so the two differ slightly.",
			},
		}),
		interpPValue,
	}

	interpTestPropZ = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "z: the first group's success rate minus the second's, in standard errors computed from the pooled rate. " +
				"Values far from 0 in either direction are unlikely if the true rates are equal.",
			Sign: twoGroupSign,
			Caveats: []string{
				"The normal approximation needs at least 10 successes and 10 failures in each group.",
			},
		},
		interpPValue,
		{Field: "details.diff", Means: "The first group's success rate minus the second's, as a proportion (0.05 = 5 percentage points).", Sign: twoGroupSign},
		{
			Field: "details.ci_low",
			Means: "Lower end of the confidence interval for details.diff at the 1 - alpha level (95% by default).",
			Caveats: []string{
				"The interval uses the unpooled (Wald) standard error while the test uses the pooled one, so near the alpha boundary the two can disagree.",
				"The Wald interval is poor with few successes or failures, or rates near 0 or 1.",
			},
		},
		{Field: "details.ci_high", Means: "Upper end of the confidence interval for details.diff."},
		bandedBy(ConventionCohenD, descriptor.Interpretation{
			Field: "details.effect_size.cohens_h",
			Means: "Cohen's h, the gap between the two rates on an arcsine scale that treats a change from 0.01 to 0.05 as larger than one from 0.45 to 0.49.",
			Sign:  twoGroupSign,
			Caveats: []string{
				"Cohen's 0.2 / 0.5 / 0.8 benchmarks for h are the same as for d, applied to |h|.",
			},
		}),
	}
)

// --- Association and trend --------------------------------------------

var (
	interpTestPearsonR = []descriptor.Interpretation{
		bandedBy(ConventionCohenR, descriptor.Interpretation{
			Field: "statistic",
			Means: "r measures how closely the two fields follow a straight line together, from -1 to +1; 0 means no straight-line link.",
			Sign:  correlationSign,
			Caveats: []string{
				causationCaveatText,
				"An r near zero rules out only a straight-line link; a curved relationship can still be strong.",
				"A few extreme rows, or a narrow range of either field, can move r a lot.",
			},
		}),
		interpPValue,
		{
			Field: "details.ci_low",
			Means: "Lower end of the confidence interval for r at the 1 - alpha level (95% by default).",
			Caveats: []string{
				"With fewer than four pairs, or a perfect r, no interval can be computed and both ends are set to r: this is not a precise estimate. Treat r as highly uncertain and do not report the interval.",
			},
		},
		{
			Field: "details.ci_high",
			Means: "Upper end of the confidence interval for r at the 1 - alpha level (95% by default).",
		},
	}

	interpTestSpearmanR = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Spearman's rho is Pearson's r computed on ranks, from -1 to +1: how consistently one field rises (or falls) as the other rises, whether or not in a straight line.",
			Sign:  correlationSign,
			Caveats: []string{
				causationCaveatText,
				"There are no sourced bands for rho, so none are attached: Cohen's r benchmarks were set for Pearson's r.",
				"The p-value uses a t approximation with n - 2 degrees of freedom; many ties make it unreliable (Pulse warns).",
			},
		},
		interpPValue,
	}

	interpTestKendallTau = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "Kendall's tau-b, from -1 to +1: the share of row pairs that are ordered the same way on both fields minus the share ordered the opposite way, adjusted for ties.",
			Sign:  correlationSign,
			Caveats: []string{
				causationCaveatText,
				"There are no sourced bands for tau, so none are attached; tau usually comes out smaller than Pearson's r or Spearman's rho for the same data, so do not read it on their scale.",
			},
		},
		interpPValue,
	}

	interpTestTrend = []descriptor.Interpretation{
		{
			Field: "statistic",
			Means: "The Mann-Kendall Z score: the trend count S (details.s) in standard errors, with a continuity correction. Values far from 0 point to a consistent tendency to rise or fall (not necessarily at an even rate).",
			Sign: map[string]string{
				"+": "the series tends to rise over the OrderBy sequence",
				"-": "the series tends to fall over the OrderBy sequence",
			},
		},
		interpPValue,
		{
			Field: "details.s",
			Means: "S counts every pair of points: +1 when the later point is higher, -1 when it is lower, 0 when tied.",
			Sign: map[string]string{
				"+": "more later points are higher than earlier ones",
				"-": "more later points are lower than earlier ones",
			},
		},
		{
			Field: "details.tau",
			Means: "S as a share of all pairs of points, from -1 (always falling) to +1 (always rising): how consistent the trend is, not how steep.",
			Sign: map[string]string{
				"+": "a rising trend",
				"-": "a falling trend",
			},
			Caveats: []string{
				"Tied points count as neither rising nor falling, so many ties pull tau toward 0.",
			},
		},
	}
)
