package descriptor

import "github.com/frankbardon/pulse/descriptor"

// statTestPurposes is the Purpose registry for the TEST_* families, keyed
// by family (one purpose covers both tiers). builtinPurposes assembles it
// with the other category maps.
//
// Each Purpose states the test as Pulse implements it (see the
// internal/processing/test_*.go sources): TEST_T's two-sample arm is
// Welch, every p-value is two-sided, the rank tests use large-sample
// normal or chi-square approximations, and TEST_SHAPIRO_WILK computes
// the Shapiro-Francia form. Assumptions always name independence (or,
// for the paired families, the pairing) — TestGuidanceProseLint's
// ASSUME-INDEP rule.
var statTestPurposes = map[string]descriptor.Purpose{
	"TEST_ANOVA_F":        purposeTestAnovaF,
	"TEST_ANOVA_RM":       purposeTestAnovaRM,
	"TEST_ANOVA_WELCH":    purposeTestAnovaWelch,
	"TEST_BROWN_FORSYTHE": purposeTestBrownForsythe,
	"TEST_CHISQ":          purposeTestChiSq,
	"TEST_FISHER_EXACT":   purposeTestFisherExact,
	"TEST_KENDALL_TAU":    purposeTestKendallTau,
	"TEST_KRUSKAL_WALLIS": purposeTestKruskalWallis,
	"TEST_KS":             purposeTestKS,
	"TEST_MANN_WHITNEY_U": purposeTestMannWhitneyU,
	"TEST_PAIRED_T":       purposeTestPairedT,
	"TEST_PEARSON_R":      purposeTestPearsonR,
	"TEST_PROP_Z":         purposeTestPropZ,
	"TEST_SHAPIRO_WILK":   purposeTestShapiroWilk,
	"TEST_SPEARMAN_R":     purposeTestSpearmanR,
	"TEST_T":              purposeTestT,
	"TEST_TREND":          purposeTestTrend,
	"TEST_TUKEY_HSD":      purposeTestTukeyHSD,
	"TEST_WELCH":          purposeTestWelch,
	"TEST_WILCOXON_SR":    purposeTestWilcoxonSR,
	"TEST_Z_TWO_SAMPLE":   purposeTestZTwoSample,
}

// --- Comparing averages ----------------------------------------------

var (
	purposeTestT = descriptor.Purpose{
		Plain:   "Checks whether a numeric field's average differs from a target value, or between two groups (Welch's version).",
		Intents: []string{IntentCompareGroups, IntentBenchmark},
		Questions: []string{
			"Is the average order value different from our target of 50?",
			"Do new and returning customers spend different amounts on average?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean satisfaction between two regions, or against a target score.",
			descriptor.DomainOps:     "Check average handling time against a service-level target.",
			descriptor.DomainScience: "Compare the mean response of a treatment group with a control group.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the same subjects are measured twice, such as before and after", Use: "TEST_PAIRED_T"},
			{When: "the measure is heavily skewed, has extreme values or is only ordered", Use: "TEST_MANN_WHITNEY_U"},
			{When: "there are three or more groups", Use: "TEST_ANOVA_WELCH"},
			{When: "the outcome is a yes/no rate rather than a numeric measure", Use: "TEST_PROP_Z"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"The two-group version uses Welch's correction, so the groups need not have equal variances.",
			"The average is roughly normally distributed: safe for large groups, risky for small skewed ones.",
			"The p-value is two-sided: it looks for a difference in either direction.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"cohens-d", "confidence-interval", "independence", "normal-distribution",
			"p-value", "t-statistic", "two-tailed",
		},
	}

	purposeTestWelch = descriptor.Purpose{
		Plain:   "Checks whether the average of a numeric field differs between two groups, without assuming they have equal spread.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Do customers in the two pricing arms spend different amounts on average?",
			"Is average delivery time different between the two depots?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean satisfaction between two customer segments.",
			descriptor.DomainOps:     "Compare average order value between two sales channels.",
			descriptor.DomainScience: "Compare the mean outcome of treatment and control groups of different sizes.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the same subjects are measured twice, such as before and after", Use: "TEST_PAIRED_T"},
			{When: "the measure is heavily skewed, has extreme values or is only ordered", Use: "TEST_MANN_WHITNEY_U"},
			{When: "there are three or more groups", Use: "TEST_ANOVA_WELCH"},
			{When: "you compare one group's average with a fixed target value", Use: "TEST_T"},
			{When: "you care about the whole shape of the two distributions, not just the average", Use: "TEST_KS"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across the two groups.",
			"Each group's average is roughly normally distributed; large groups make this safe.",
			"Unequal variances are allowed: the degrees of freedom are adjusted (Welch-Satterthwaite).",
			"The p-value is two-sided.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"cohens-d", "degrees-of-freedom", "homogeneity-of-variance", "independence",
			"normal-distribution", "p-value", "t-statistic", "two-tailed",
		},
	}

	purposeTestZTwoSample = descriptor.Purpose{
		Plain:   "Large-sample check of whether two groups differ in average, with the p-value read from the normal distribution.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"In a large survey, do the two regions differ in average score?",
			"Across thousands of sessions, is average basket size different between the two app versions?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean ratings between two large respondent groups where reporting conventions call for z.",
			descriptor.DomainOps:     "Compare average spend between two high-traffic storefronts.",
			descriptor.DomainScience: "Compare means of two large samples where the t and normal tails agree.",
		},
		NotFor: []descriptor.Alternative{
			{When: "either group is small; the t-distribution p-value is more honest there", Use: "TEST_WELCH"},
			{When: "the outcome is a yes/no rate", Use: "TEST_PROP_Z"},
			{When: "the same subjects are measured twice", Use: "TEST_PAIRED_T"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across the two groups.",
			"Both groups are large; with small groups the normal p-value comes out too small.",
			"Unequal variances are allowed: the standard error is the same as Welch's t-test.",
			"The p-value is two-sided.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"independence", "normal-distribution", "p-value", "sample-size",
			"standard-error", "two-tailed", "z-score",
		},
	}

	purposeTestPairedT = descriptor.Purpose{
		Plain:   "Checks whether the average change between two measurements of the same rows, such as before and after, differs from zero.",
		Intents: []string{IntentCompareGroups, IntentChangeOverTime},
		Questions: []string{
			"Did each customer's spend change after the loyalty programme started?",
			"Do patients' scores differ between the first and second visit?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare each respondent's rating of two products in the same questionnaire.",
			descriptor.DomainOps:     "Compare each store's weekly sales before and after a layout change.",
			descriptor.DomainScience: "Compare each subject's measurement before and after treatment.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the two sets of values come from different, unrelated subjects", Use: "TEST_WELCH"},
			{When: "the per-row differences are skewed or have extreme values", Use: "TEST_WILCOXON_SR"},
			{When: "each subject is measured under three or more conditions", Use: "TEST_ANOVA_RM"},
		},
		Assumptions: []string{
			"Each row pairs two measurements of the same subject, held side by side in Field and Field2.",
			"Pairs are independent of each other.",
			"The per-row differences are roughly normally distributed; the two fields themselves need not be.",
			"Rows missing either value are dropped. The p-value is two-sided.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"cohens-d", "independence", "normal-distribution", "p-value",
			"paired-data", "t-statistic", "two-tailed",
		},
	}

	purposeTestAnovaF = descriptor.Purpose{
		Plain:   "Checks whether the average of a numeric measure differs across three or more groups.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Does average spend differ across regions?",
			"Do the treatment arms produce different average outcomes?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean satisfaction across age bands.",
			descriptor.DomainOps:     "Compare average delivery time across warehouses.",
			descriptor.DomainScience: "Compare mean yield across fertiliser treatments.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the groups have clearly unequal spread", Use: "TEST_ANOVA_WELCH"},
			{When: "the measure is heavily skewed or only ordered", Use: "TEST_KRUSKAL_WALLIS"},
			{When: "the same subjects are measured in every group", Use: "TEST_ANOVA_RM"},
			{When: "there are only two groups", Use: "TEST_WELCH"},
			{When: "you need to know which pairs of groups differ", Use: "TEST_TUKEY_HSD"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"The measure is roughly normally distributed within each group.",
			"Groups have similar variances; TEST_BROWN_FORSYTHE checks this.",
			"It is an overall test: it says whether any group average stands apart, not which one.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"effect-size", "eta-squared", "f-statistic", "homogeneity-of-variance",
			"normal-distribution", "null-hypothesis", "omega-squared", "p-value",
			"post-hoc-test", "variance",
		},
	}

	purposeTestAnovaWelch = descriptor.Purpose{
		Plain:   "Checks whether the average of a numeric measure differs across groups when the groups may have unequal spread.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Does average response time differ across regions whose variability is very different?",
			"Do the store formats differ in average basket size, given some formats are far more variable?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare mean scores across segments of very different sizes and spreads.",
			descriptor.DomainOps:     "Compare average processing time across sites with uneven variability.",
			descriptor.DomainScience: "Compare treatment means when the spread grows with the dose.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the measure is heavily skewed or only ordered", Use: "TEST_KRUSKAL_WALLIS"},
			{When: "the same subjects are measured in every group", Use: "TEST_ANOVA_RM"},
			{When: "there are only two groups, or you want pairwise follow-ups adjusted for multiple comparisons", Use: "TEST_WELCH"},
			{When: "spreads are similar and you want Tukey's pairwise follow-up", Use: "TEST_ANOVA_F"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"The measure is roughly normally distributed within each group.",
			"Groups may have unequal variances; each group needs some spread (a constant group is refused).",
			"It is an overall test: it says whether any group average stands apart, not which one.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"degrees-of-freedom", "f-statistic", "homogeneity-of-variance", "independence",
			"normal-distribution", "omega-squared", "p-value", "variance",
		},
	}

	purposeTestAnovaRM = descriptor.Purpose{
		Plain:   "Checks whether the average differs across conditions when every subject is measured under each condition.",
		Intents: []string{IntentCompareGroups, IntentChangeOverTime},
		Questions: []string{
			"Do the same panel members rate the three ad concepts differently on average?",
			"Does each patient's score change across the baseline, mid-point and final visits?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare each respondent's ratings of several brands asked in one survey.",
			descriptor.DomainOps:     "Compare each store's sales across three promotion periods.",
			descriptor.DomainScience: "Compare each subject's response across repeated measurement sessions.",
		},
		NotFor: []descriptor.Alternative{
			{When: "each group holds different, unrelated subjects", Use: "TEST_ANOVA_F"},
			{When: "there are only two conditions", Use: "TEST_PAIRED_T"},
		},
		Assumptions: []string{
			"The same subjects (SubjectField) are measured once under every condition; subjects missing a condition are dropped.",
			"Subjects are independent of each other.",
			"The measure is roughly normally distributed within each condition.",
			"Sphericity: differences between each pair of conditions are about equally variable. No correction is applied, so violations make the p-value too small.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"f-statistic", "independence", "normal-distribution", "p-value",
			"partial-eta-squared", "repeated-measures", "sphericity",
		},
	}

	purposeTestTukeyHSD = descriptor.Purpose{
		Plain:   "After an ANOVA, compares every pair of group averages to find which ones differ, keeping the overall false-alarm rate in check.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Which regions differ from each other in average spend?",
			"Which treatment arms have different average outcomes?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Find which age bands differ in mean satisfaction after an overall ANOVA.",
			descriptor.DomainOps:     "Find which warehouses differ in average delivery time.",
			descriptor.DomainScience: "Find which fertiliser treatments differ in mean yield.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you only need to know whether any group differs", Use: "TEST_ANOVA_F"},
			{When: "groups have clearly unequal spread: run pairwise Welch tests and adjust for multiple comparisons", Use: "TEST_WELCH"},
			{When: "the measure is heavily skewed or only ordered", Use: "TEST_KRUSKAL_WALLIS"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"Groups have similar variances, as in the standard ANOVA.",
			"It reads per-group averages and counts from result rows plus ms_within and df_within from a preceding TEST_ANOVA_F.",
			"Its p-values are already adjusted for multiple comparisons across every pair (family-wise), using the studentized range (Tukey-Kramer for unequal group sizes).",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"alpha", "homogeneity-of-variance", "independence", "multiple-comparisons",
			"p-value", "post-hoc-test", "studentized-range",
		},
	}

	purposeTestBrownForsythe = descriptor.Purpose{
		Plain:   "Checks whether the spread of a numeric field differs across groups: a robust test of equal variances.",
		Intents: []string{IntentCompareGroups, IntentDistributionShape},
		Questions: []string{
			"Is delivery time more variable at some warehouses than at others?",
			"Do the groups vary enough in spread that a standard ANOVA is a poor fit?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether some segments answer far more variably than others.",
			descriptor.DomainOps:     "Compare the consistency of processing times across sites.",
			descriptor.DomainScience: "Check whether measurement spread differs across treatment groups.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to compare the group averages themselves", Use: "TEST_ANOVA_WELCH"},
			{When: "you want to check whether a measure is bell-shaped", Use: "TEST_SHAPIRO_WILK"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"It measures distance from each group's median, so it stays reliable when the data are not normal.",
			"Very small groups make the medians unstable.",
			"A large p-value is not evidence that the spreads are equal; many analysts simply use Welch's tests rather than pre-testing.",
		},
		Level: descriptor.LevelAdvanced,
		Glossary: []string{
			"f-statistic", "homogeneity-of-variance", "independence", "median",
			"p-value", "variance",
		},
	}
)

// --- Rank-based comparisons --------------------------------------------

var (
	purposeTestMannWhitneyU = descriptor.Purpose{
		Plain:   "Rank-based check of whether values in one of two groups tend to be larger than in the other.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Do customers on the new plan tend to rate the service higher?",
			"Do response times at one site tend to run longer than at the other?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare 1-5 ratings between two segments.",
			descriptor.DomainOps:     "Compare skewed resolution times between two support teams.",
			descriptor.DomainScience: "Compare a non-normal outcome between treatment and control.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the same subjects are measured twice", Use: "TEST_WILCOXON_SR"},
			{When: "there are three or more groups", Use: "TEST_KRUSKAL_WALLIS"},
			{When: "the data are roughly normal and you want the difference in averages", Use: "TEST_WELCH"},
			{When: "you care about any difference in the shape of the two distributions", Use: "TEST_KS"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across the two groups.",
			"It tests whether a value from one group tends to exceed one from the other; it reads as a difference in medians only when both groups have the same shape.",
			"The p-value is a two-sided large-sample approximation with tie and continuity corrections, not an exact value.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"continuity-correction", "independence", "median", "non-parametric",
			"p-value", "rank", "rank-biserial", "ties", "two-tailed",
		},
	}

	purposeTestWilcoxonSR = descriptor.Purpose{
		Plain:   "Rank-based check of whether paired before/after values tend to shift in one direction, without assuming a bell curve.",
		Intents: []string{IntentCompareGroups, IntentChangeOverTime},
		Questions: []string{
			"Did each customer's rating tend to go up after the redesign?",
			"Do patients' skewed symptom scores tend to fall between visits?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare each respondent's ratings of two concepts on a short scale.",
			descriptor.DomainOps:     "Compare each machine's skewed downtime before and after maintenance.",
			descriptor.DomainScience: "Compare each subject's non-normal measurement before and after treatment.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the two sets of values come from different, unrelated subjects", Use: "TEST_MANN_WHITNEY_U"},
			{When: "the per-row differences are roughly normal and you want the average change", Use: "TEST_PAIRED_T"},
		},
		Assumptions: []string{
			"Each row pairs two measurements of the same subject, held side by side in Field and Field2.",
			"Pairs are independent of each other.",
			"The differences are roughly symmetric around their centre; the test asks whether that centre is zero.",
			"Pairs whose values are equal are dropped; the p-value is a two-sided large-sample approximation and needs at least 6 pairs that differ.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"continuity-correction", "independence", "non-parametric", "normal-distribution",
			"p-value", "paired-data", "rank", "rank-biserial", "ties",
		},
	}

	purposeTestKruskalWallis = descriptor.Purpose{
		Plain:   "Rank-based check of whether values tend to be larger in some groups than in others, across two or more groups.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Do satisfaction ratings tend to differ across the four regions?",
			"Do skewed repair times differ across product lines?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare 1-5 ratings across several customer segments.",
			descriptor.DomainOps:     "Compare skewed wait times across branches.",
			descriptor.DomainScience: "Compare a non-normal outcome across several treatment groups.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the data are roughly normal and you want to compare averages", Use: "TEST_ANOVA_WELCH"},
			{When: "there are only two groups", Use: "TEST_MANN_WHITNEY_U"},
			{When: "the same subjects are measured in every group", Use: "TEST_ANOVA_RM"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across groups.",
			"It tests whether values tend to be larger in some groups; it reads as a difference in medians only when all groups have the same shape.",
			"It is an overall test: to find which groups differ, follow up with pairwise Mann-Whitney tests and adjust for multiple comparisons.",
			"The p-value comes from a chi-square approximation that is shaky for very small groups.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "epsilon-squared", "independence", "median",
			"multiple-comparisons", "non-parametric", "p-value", "rank", "ties",
		},
	}
)

// --- Counts and rates ---------------------------------------------------

var (
	purposeTestChiSq = descriptor.Purpose{
		Plain:   "Checks whether two categorical fields are associated by comparing a cross-tabulation with the counts expected if unrelated.",
		Intents: []string{IntentRelationship, IntentCompareGroups},
		Questions: []string{
			"Does preferred channel depend on age band?",
			"Is the mix of plan types different across regions?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether brand preference varies by region.",
			descriptor.DomainOps:     "Check whether ticket category depends on the support channel.",
			descriptor.DomainScience: "Check whether outcome category depends on treatment arm.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the table is 2x2 and some expected counts are small", Use: "TEST_FISHER_EXACT"},
			{When: "you compare a yes/no rate between exactly two groups and want a confidence interval", Use: "TEST_PROP_Z"},
			{When: "both fields are numeric", Use: "TEST_SPEARMAN_R"},
			{When: "you compare one subgroup's mix with the whole population", Use: "OVERLAY_CHISQ_VS_POP"},
		},
		Assumptions: []string{
			"Each row is counted once and rows are independent of each other.",
			"It works on raw counts, never on percentages or averages.",
			"Every expected count should be about 5 or more (Cochran's rule of thumb); below that the p-value is unreliable and Pulse warns.",
			"No continuity correction is applied.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"chi-square", "continuity-correction", "cramers-v", "cross-tabulation",
			"degrees-of-freedom", "independence", "p-value", "phi",
		},
	}

	purposeTestFisherExact = descriptor.Purpose{
		Plain:   "Exact test of whether two yes/no fields are associated, built for small 2x2 tables.",
		Intents: []string{IntentRelationship, IntentCompareGroups},
		Questions: []string{
			"In a small pilot, did the treated group recover more often than the control group?",
			"With only a few dozen responses, does opting in depend on the sign-up channel?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare a yes/no answer between two small subgroups.",
			descriptor.DomainOps:     "Compare a rare failure rate between two production lines.",
			descriptor.DomainScience: "Compare a binary outcome between two small treatment arms.",
		},
		NotFor: []descriptor.Alternative{
			{When: "either field has more than two categories", Use: "TEST_CHISQ"},
			{When: "counts are large and you want the difference in rates with a confidence interval", Use: "TEST_PROP_Z"},
		},
		Assumptions: []string{
			"Rows are independent of each other, and each row is counted once.",
			"Strictly 2x2: both fields must have exactly two levels.",
			"Row and column totals are treated as fixed, which makes the test somewhat conservative.",
			"The two-sided p-value sums every table no more likely than the one observed; other tools sometimes double a one-sided tail and disagree slightly.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"cross-tabulation", "exact-test", "independence", "odds-ratio",
			"p-value", "two-tailed",
		},
	}

	purposeTestPropZ = descriptor.Purpose{
		Plain:   "Checks whether the rate of one outcome, such as conversion, differs between two groups.",
		Intents: []string{IntentCompareGroups},
		Questions: []string{
			"Does the new checkout convert at a different rate from the old one?",
			"Is the share of promoters different between the two regions?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare the share answering yes between two segments.",
			descriptor.DomainOps:     "Compare conversion rates between two experiment arms.",
			descriptor.DomainScience: "Compare response rates between treatment and control.",
		},
		NotFor: []descriptor.Alternative{
			{When: "some groups have only a handful of successes or failures", Use: "TEST_FISHER_EXACT"},
			{When: "there are more than two groups or more than two outcomes", Use: "TEST_CHISQ"},
			{When: "the outcome is a numeric measure rather than a yes/no rate", Use: "TEST_WELCH"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across the two groups.",
			"Each group needs enough successes and failures (at least 5 to 10 of each) for the normal approximation.",
			"The p-value is two-sided; the confidence interval on the rate difference is the simple Wald interval, which is rough near 0% or 100%.",
		},
		Level: descriptor.LevelBasic,
		Glossary: []string{
			"cohens-h", "confidence-interval", "independence", "normal-distribution",
			"p-value", "two-tailed", "z-score",
		},
	}
)

// --- Relationships between measures --------------------------------------

var (
	purposeTestPearsonR = descriptor.Purpose{
		Plain:   "Measures how strongly two numeric fields rise and fall together along a straight line.",
		Intents: []string{IntentRelationship},
		Questions: []string{
			"Do customers who spend more also visit more often?",
			"Does study time go with higher test scores?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether satisfaction tracks likelihood to recommend.",
			descriptor.DomainOps:     "See whether ad spend moves with weekly revenue.",
			descriptor.DomainScience: "Relate dose to measured response.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the link is consistent but curved, or the values are ranks", Use: "TEST_SPEARMAN_R"},
			{When: "the sample is small or has many tied values", Use: "TEST_KENDALL_TAU"},
			{When: "both fields are categories", Use: "TEST_CHISQ"},
			{When: "you want to predict one field from several others", Use: "REG_OLS"},
			{When: "you want to know whether one ordered series keeps rising or falling", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"The relationship is roughly a straight line.",
			"Pairs of values are independent of each other.",
			"A few extreme values can dominate the result.",
			"The p-value assumes both fields are roughly normal; with small samples that matters most.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"confidence-interval", "correlation", "independence", "normal-distribution",
			"outlier", "p-value", "r-squared",
		},
	}

	purposeTestSpearmanR = descriptor.Purpose{
		Plain:   "Measures how consistently two numeric fields move in the same direction, using ranks, so the link need not be a straight line.",
		Intents: []string{IntentRelationship},
		Questions: []string{
			"Do higher-ranked products also tend to sell more, even if not in proportion?",
			"Does satisfaction tend to rise with tenure?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Relate two rating-scale answers to each other.",
			descriptor.DomainOps:     "Check whether skewed order size tends to grow with customer age.",
			descriptor.DomainScience: "Relate dose to a response that levels off.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the strength of a straight-line link", Use: "TEST_PEARSON_R"},
			{When: "the sample is small or many values are tied", Use: "TEST_KENDALL_TAU"},
			{When: "both fields are categories", Use: "TEST_CHISQ"},
			{When: "you want to know whether one ordered series keeps rising or falling", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"Pairs of values are independent of each other.",
			"It detects monotonic links (one keeps rising as the other rises, or falls); a U-shaped link can score near zero.",
			"The p-value uses a t approximation that weakens with small samples or many tied values.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"correlation", "independence", "monotonic-trend", "outlier",
			"p-value", "rank", "spearman-rho", "ties",
		},
	}

	purposeTestKendallTau = descriptor.Purpose{
		Plain:   "Measures how often pairs of rows agree in order on two numeric fields: a rank-based link suited to small samples and ties.",
		Intents: []string{IntentRelationship},
		Questions: []string{
			"In a small panel, do judges who score one entry higher also score the other higher?",
			"Do two coarse rating scales tend to agree?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Relate two short rating scales with many tied answers.",
			descriptor.DomainOps:     "Check whether two priority rankings agree.",
			descriptor.DomainScience: "Relate two measures in a small study.",
		},
		NotFor: []descriptor.Alternative{
			{When: "the data are large; its cost grows with the square of the row count", Use: "TEST_SPEARMAN_R"},
			{When: "you want the strength of a straight-line link", Use: "TEST_PEARSON_R"},
			{When: "you want to know whether one ordered series keeps rising or falling", Use: "TEST_TREND"},
		},
		Assumptions: []string{
			"Pairs of values are independent of each other.",
			"It detects monotonic links; ties in either field are corrected for (tau-b).",
			"The p-value is a two-sided normal approximation.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"correlation", "independence", "kendall-tau", "monotonic-trend",
			"p-value", "rank", "ties",
		},
	}

	purposeTestTrend = descriptor.Purpose{
		Plain:   "Checks whether an ordered series, such as monthly totals, tends to keep rising or keep falling.",
		Intents: []string{IntentChangeOverTime},
		Questions: []string{
			"Is monthly churn creeping up?",
			"Has weekly average wait time been falling over the year?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether wave-on-wave satisfaction keeps moving one way.",
			descriptor.DomainOps:     "Check whether a smoothed daily error rate is drifting.",
			descriptor.DomainScience: "Check whether yearly readings show a steady drift.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want the size of a straight-line slope", Use: "REG_OLS"},
			{When: "you compare the averages of two specific periods", Use: "TEST_WELCH"},
			{When: "you relate two numeric fields rather than one series in order", Use: "TEST_KENDALL_TAU"},
		},
		Assumptions: []string{
			"It runs on result rows (a post-test) in OrderBy order, such as a grouped or windowed series.",
			"Successive points are independent; seasonality or autocorrelation makes the p-value too small.",
			"It detects a monotonic trend, not a straight-line slope and not which periods differ.",
			"The p-value is a two-sided normal approximation, unreliable below about 8 points.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"independence", "kendall-tau", "monotonic-trend", "non-parametric",
			"p-value", "two-tailed",
		},
	}
)

// --- Distribution shape --------------------------------------------------

var (
	purposeTestShapiroWilk = descriptor.Purpose{
		Plain:   "Checks whether a numeric field looks normally distributed, overall or within each group.",
		Intents: []string{IntentDistributionShape},
		Questions: []string{
			"Is response time roughly bell-shaped, or should I use a rank-based test?",
			"Are scores in each group close enough to normal for an ANOVA?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Check whether an index score is close to normal before a t-test.",
			descriptor.DomainOps:     "Check whether processing times are bell-shaped or heavily skewed.",
			descriptor.DomainScience: "Check the normality assumption per treatment group.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you compare the distributions of two groups with each other", Use: "TEST_KS"},
			{When: "you want to check whether groups have equal spread", Use: "TEST_BROWN_FORSYTHE"},
		},
		Assumptions: []string{
			"Rows are independent of each other.",
			"Needs at least 3 values per group; above 5000 the p-value is advisory.",
			"With few rows it has low power and can miss real departures; with very large samples even trivial departures are flagged, so look at the shape too.",
			"Pulse computes the Shapiro-Francia form, a close approximation to Shapiro-Wilk; with SplitBy the headline is the group that departs most.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"goodness-of-fit", "independence", "kurtosis", "normal-distribution",
			"null-hypothesis", "p-value", "skew", "statistical-power",
		},
	}

	purposeTestKS = descriptor.Purpose{
		Plain:   "Checks whether a numeric field's values follow the same distribution in two groups, comparing their whole shape.",
		Intents: []string{IntentDistributionShape, IntentCompareGroups},
		Questions: []string{
			"Do order values in the two regions have the same overall distribution?",
			"Has the shape of response times changed between the old and new system?",
		},
		UseCases: map[descriptor.Domain]string{
			descriptor.DomainSurvey:  "Compare the full spread of scores between two survey waves.",
			descriptor.DomainOps:     "Detect a change in the shape of latency between two releases.",
			descriptor.DomainScience: "Compare measurement distributions between two instruments.",
		},
		NotFor: []descriptor.Alternative{
			{When: "you want to check one field against the normal distribution", Use: "TEST_SHAPIRO_WILK"},
			{When: "you only care whether one group's values tend to be larger", Use: "TEST_MANN_WHITNEY_U"},
			{When: "you compare a subgroup with the whole population", Use: "OVERLAY_KS_VS_POP"},
		},
		Assumptions: []string{
			"Rows are independent of each other, within and across the two groups.",
			"It compares two observed groups; it is not a normality test with parameters estimated from the data (that needs the Lilliefors correction).",
			"With few rows per group it has low power; with very large groups even trivial differences in shape are flagged.",
			"The p-value is a two-sided large-sample approximation; many tied values make it conservative.",
		},
		Level: descriptor.LevelIntermediate,
		Glossary: []string{
			"goodness-of-fit", "independence", "non-parametric", "p-value",
			"statistical-power", "ties", "two-tailed",
		},
	}
)
