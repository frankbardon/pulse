package descriptor

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/descriptor"
)

// The glossary: the statistical vocabulary Pulse's guidance relies on,
// each term defined for a developer without a statistics background.
// Plain words come first and every definition is honest about what the
// number does NOT tell you. Conventional cut-offs ("0.2 is small") are
// deliberately absent — they belong to an output's Interpretation bands,
// not to the definition of the term.
//
// The glossary is static: it is not a feature, so a feature profile
// never prunes it. Read it through Glossary / GlossaryIDs, which hand
// out copies.

// Glossary limits, validated by TestGlossaryTermsResolve so the rendered
// glossary stays one reasonable fetch.
const (
	glossaryShortMax   = 200
	glossaryWhyCareMax = 300
)

var glossaryIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var glossaryRegistry = []descriptor.Term{
	// --- Testing and significance ---------------------------------------
	{
		ID:      "p-value",
		Short:   "The probability of a result at least this extreme if the test's null hypothesis (no difference, no link, a normal shape...) and its assumptions were all true.",
		WhyCare: "Small values (below your alpha) mean the data would be surprising if there were no difference, which counts as evidence against 'no difference'. It is not the chance that the difference is real, and it says nothing about how BIG it is.",
		SeeAlso: []string{"effect-size", "alpha", "multiple-comparisons"},
		Jargon:  true, Forms: []string{"p-value", "p-values", "p value", "p values"},
	},
	{
		ID:      "alpha",
		Short:   "The p-value threshold you pick before looking at the data, below which you call a result significant.",
		WhyCare: "It is the false-alarm rate you accept when there is truly no effect: the share of such tests you would wrongly call significant. Pick it up front; moving it after seeing results defeats its purpose.",
		SeeAlso: []string{"p-value", "statistical-significance", "multiple-comparisons"},
		Jargon:  true, Forms: []string{"alpha", "significance level"},
	},
	{
		ID:      "null-hypothesis",
		Short:   "The boring default a test assumes until the data argue otherwise, usually \"there is no difference\" or \"there is no relationship\".",
		WhyCare: "A test only measures how surprising your data would be if the null were true. Failing to reject it is not proof that it is true.",
		SeeAlso: []string{"p-value", "statistical-significance", "test-statistic"},
		Jargon:  true, Forms: []string{"null hypothesis", "null hypotheses"},
	},
	{
		ID:      "statistical-significance",
		Short:   "A result is statistically significant when its p-value falls below your chosen alpha.",
		WhyCare: "Significant means data this extreme would be unusual if there were truly no effect; it is not the chance the effect is real, nor that it is important. With enough rows a tiny difference becomes significant; check the effect size (for a shape test, the statistic itself).",
		SeeAlso: []string{"p-value", "alpha", "effect-size", "statistical-power"},
		Jargon:  true, Forms: []string{"statistically significant", "statistical significance"},
	},
	{
		ID:      "multiple-comparisons",
		Short:   "Running many tests at once, which raises the chance that at least one comes out significant by luck alone.",
		WhyCare: "Twenty tests at the usual alpha will on average flag one false positive even when nothing is going on. Adjust for it, or treat a lone hit with suspicion.",
		SeeAlso: []string{"p-value", "alpha", "post-hoc-test", "family-wise-error", "false-discovery-rate"},
		Jargon:  true, Forms: []string{"multiple comparisons", "multiple testing"},
	},
	{
		ID:      "family-wise-error",
		Short:   "The chance that a group of tests produces at least one false positive, counted over the whole group rather than test by test.",
		WhyCare: "Holding it at alpha (Bonferroni, Holm) makes any single significant hit trustworthy, at the cost of missing real but modest effects when the group is large.",
		SeeAlso: []string{"multiple-comparisons", "false-discovery-rate", "alpha"},
		Jargon:  true, Forms: []string{"family-wise error", "family-wise error rate", "familywise error", "fwer"},
	},
	{
		ID:      "false-discovery-rate",
		Short:   "The expected share of false positives among the results you call significant.",
		WhyCare: "Holding it at alpha (Benjamini-Hochberg, Benjamini-Yekutieli) keeps more power than family-wise control when many tests run, but accepts that a few of the hits may be false.",
		SeeAlso: []string{"multiple-comparisons", "family-wise-error", "alpha"},
		Jargon:  true, Forms: []string{"false discovery rate", "fdr"},
	},
	{
		ID:      "test-statistic",
		Short:   "The single number a test computes from your data to measure how far it sits from the null hypothesis.",
		WhyCare: "It is the raw ingredient of the p-value. Its scale differs from test to test, so compare effect sizes, not statistics or p-values, across tests; a p-value also depends on the sample size.",
		SeeAlso: []string{"p-value", "t-statistic", "f-statistic", "chi-square"},
		Jargon:  true, Forms: []string{"test statistic", "test statistics"},
	},
	{
		ID:      "t-statistic",
		Short:   "The test statistic of a t-test: a difference in means divided by its standard error.",
		WhyCare: "Bigger in absolute size means the difference is large relative to its standard error, which shrinks as rows are added, so a big t can come from a tiny difference in a large sample. Its sign tells you which side is higher.",
		SeeAlso: []string{"test-statistic", "standard-error", "degrees-of-freedom"},
		Jargon:  true, Forms: []string{"t-statistic", "t statistic"},
	},
	{
		ID:      "f-statistic",
		Short:   "The test statistic of an ANOVA: how much group means spread apart compared with how much rows spread within groups.",
		WhyCare: "Values near 1 mean the groups differ about as much as noise would make them; larger values are stronger evidence against all group means being equal. Read the p-value to judge how large is large, and an effect size for how big the difference is.",
		SeeAlso: []string{"test-statistic", "variance", "eta-squared", "post-hoc-test"},
		Jargon:  true, Forms: []string{"f-statistic", "f statistic", "f-ratio"},
	},
	{
		ID:      "chi-square",
		Short:   "A test statistic that adds up how far observed counts in each cell sit from the counts you would expect if nothing were going on.",
		WhyCare: "It measures the evidence that two categorical fields are related, but not how strongly; pair it with Cramer's V or phi for the size.",
		SeeAlso: []string{"test-statistic", "independence", "cramers-v", "cross-tabulation"},
		Jargon:  true, Forms: []string{"chi-square", "chi-squared", "chi square", "chi squared"},
	},
	{
		ID:      "degrees-of-freedom",
		Short:   "How many independent pieces of information a statistic was built from, usually the row or group count minus the things estimated along the way.",
		WhyCare: "Tests need it to turn a statistic into a p-value. Very few degrees of freedom means the result rests on thin evidence.",
		SeeAlso: []string{"test-statistic", "sample-size"},
		Jargon:  true, Forms: []string{"degrees of freedom", "degree of freedom"},
	},
	{
		ID:      "post-hoc-test",
		Short:   "A follow-up test run after an overall test is significant, to find which specific pairs of groups differ.",
		WhyCare: "An overall test only says \"some group differs\". Post-hoc tests say which ones, and they correct for running many pairwise comparisons.",
		SeeAlso: []string{"multiple-comparisons", "f-statistic"},
		Jargon:  true, Forms: []string{"post-hoc", "post hoc"},
	},
	{
		ID:      "statistical-power",
		Short:   "The chance a test detects a difference that really exists.",
		WhyCare: "Small samples have low power, so a non-significant result from them is weak evidence of no difference: the test may simply have been unable to see it.",
		SeeAlso: []string{"sample-size", "statistical-significance", "effect-size"},
		Jargon:  true, Forms: []string{"statistical power"},
	},
	{
		ID:      "non-parametric",
		Short:   "A method that works on ranks or counts instead of assuming the data follow a particular shape such as the normal distribution.",
		WhyCare: "Reach for one when data are skewed, have outliers or are ordinal ratings. It is robust, at the cost of some power when the data really are normal.",
		SeeAlso: []string{"rank", "normal-distribution", "outlier"},
		Jargon:  true, Forms: []string{"non-parametric", "nonparametric", "distribution-free"},
	},
	{
		ID:      "independence",
		Short:   "Two things are independent when knowing one tells you nothing about the other; rows are independent when no row influences another.",
		WhyCare: "Most tests assume independent rows. Repeated answers from the same person break it, and need a paired or repeated-measures test instead.",
		SeeAlso: []string{"chi-square", "correlation", "paired-data", "repeated-measures"},
	},
	{
		ID:      "two-tailed",
		Short:   "A two-tailed (two-sided) test looks for a difference in either direction; a one-tailed test looks in only one direction chosen in advance.",
		WhyCare: "Pulse's tests report two-sided p-values. Halving one to claim a one-sided result after seeing which way the data went inflates false alarms.",
		SeeAlso: []string{"p-value", "alpha"},
		Jargon:  true, Forms: []string{"two-tailed", "two-sided", "one-tailed", "one-sided"},
	},
	{
		ID:      "paired-data",
		Short:   "Two measurements that belong together, such as the same customer before and after a change, held on one row.",
		WhyCare: "Pairing removes the differences between subjects, so paired tests are more sensitive. Treating paired data as two separate groups wastes that and breaks the independence assumption.",
		SeeAlso: []string{"independence", "repeated-measures"},
		Jargon:  true, Forms: []string{"paired data", "paired samples", "paired measurements"},
	},
	{
		ID:      "repeated-measures",
		Short:   "A design where the same subjects are measured several times, under different conditions or at different moments.",
		WhyCare: "Measurements from one subject are related, so tests for independent groups misjudge the noise. Repeated-measures tests separate subject-to-subject differences from the effect of the condition.",
		SeeAlso: []string{"paired-data", "sphericity", "independence"},
		Jargon:  true, Forms: []string{"repeated measures", "repeated-measures", "within-subject"},
	},
	{
		ID:      "sphericity",
		Short:   "In a repeated-measures design, the assumption that the differences between every pair of conditions are about equally variable.",
		WhyCare: "When it fails, the repeated-measures F-test rejects too often. Pulse applies no correction for it, so the p-value can come out well below its true value; treat any result short of a very small p with caution.",
		SeeAlso: []string{"repeated-measures", "f-statistic", "homogeneity-of-variance"},
		Jargon:  true, Forms: []string{"sphericity"},
	},
	{
		ID:      "homogeneity-of-variance",
		Short:   "The assumption that every group has roughly the same spread (variance) around its own mean.",
		WhyCare: "Classic ANOVA and Tukey's test rely on it; when spreads differ, especially with unequal group sizes, their p-values drift. Welch's versions do not need it.",
		SeeAlso: []string{"variance", "f-statistic", "sphericity"},
		Jargon:  true, Forms: []string{"homogeneity of variance", "equal variances", "equal variance", "homoscedasticity"},
	},
	{
		ID:      "ties",
		Short:   "Values that are exactly equal, so they share a rank.",
		WhyCare: "Rank-based tests give tied values their average rank and correct for them, but many ties (as on short rating scales) weaken the test and make its large-sample p-value less accurate.",
		SeeAlso: []string{"rank", "non-parametric"},
		Jargon:  true, Forms: []string{"ties", "tied values", "tie correction"},
	},
	{
		ID:      "exact-test",
		Short:   "A test whose p-value is computed exactly from every possible arrangement of the data rather than from a large-sample approximation.",
		WhyCare: "It stays valid with tiny counts where approximations such as chi-square break down. The cost is computation, so it suits small tables.",
		SeeAlso: []string{"p-value", "chi-square", "continuity-correction"},
		Jargon:  true, Forms: []string{"exact test", "exact tests"},
	},
	{
		ID:      "continuity-correction",
		Short:   "A half-step adjustment used when a smooth curve approximates a statistic that moves in whole steps, such as a count or a rank sum.",
		WhyCare: "It keeps approximate p-values from coming out too small in small samples. Tools differ on whether they apply it, which explains small mismatches between them.",
		SeeAlso: []string{"p-value", "exact-test"},
		Jargon:  true, Forms: []string{"continuity correction", "yates correction", "yates' correction"},
	},
	{
		ID:      "studentized-range",
		Short:   "The distribution of the gap between the largest and smallest of several group means, measured in standard errors.",
		WhyCare: "Tukey's test reads its pairwise p-values from it, which is how it accounts for comparing every pair at once rather than one pair alone.",
		SeeAlso: []string{"post-hoc-test", "multiple-comparisons", "standard-error"},
		Jargon:  true, Forms: []string{"studentized range", "studentised range"},
	},
	{
		ID:      "goodness-of-fit",
		Short:   "How well observed data match a stated distribution or a set of expected counts.",
		WhyCare: "A goodness-of-fit test can only flag a mismatch. A large p-value does not show the data follow the distribution, and with few rows real departures go undetected.",
		SeeAlso: []string{"normal-distribution", "chi-square", "statistical-power"},
		Jargon:  true, Forms: []string{"goodness of fit", "goodness-of-fit"},
	},

	// --- Effect sizes ---------------------------------------------------
	{
		ID:      "effect-size",
		Short:   "A number that says how BIG a difference or relationship is, on a scale that does not grow just because you have more rows.",
		WhyCare: "It says how big the difference is, while the p-value says how surprising the data would be if there were no difference. Whether that size matters depends on your context. Report both.",
		SeeAlso: []string{"p-value", "statistical-significance", "cohens-d", "eta-squared", "cramers-v"},
		Jargon:  true, Forms: []string{"effect size", "effect sizes"},
	},
	{
		ID:      "cohens-d",
		Short:   "The difference between two means expressed in standard deviations.",
		WhyCare: "It makes differences comparable across measures with different units: a gap of half a standard deviation is the same standardized size on any scale, though whether it matters depends on the measure.",
		SeeAlso: []string{"effect-size", "standard-deviation", "t-statistic"},
		Jargon:  true, Forms: []string{"cohen's d", "cohens d", "cohens_d"},
	},
	{
		ID:      "cohens-h",
		Short:   "The difference between two proportions, put on a scale that treats a change near 0% or 100% as bigger than the same change near 50%.",
		WhyCare: "Comparing raw percentage-point gaps can mislead near the edges; this puts proportion differences on an even footing.",
		SeeAlso: []string{"effect-size", "cohens-d"},
		Jargon:  true, Forms: []string{"cohen's h", "cohens h", "cohens_h"},
	},
	{
		ID:      "eta-squared",
		Short:   "The share of the total variation in an outcome that is explained by which group a row belongs to.",
		WhyCare: "It turns an ANOVA into \"how much does group membership matter\". It runs a little high in small samples; omega-squared corrects for that.",
		SeeAlso: []string{"effect-size", "omega-squared", "partial-eta-squared", "f-statistic"},
		Jargon:  true, Forms: []string{"eta squared", "eta-squared", "eta_squared"},
	},
	{
		ID:      "partial-eta-squared",
		Short:   "Eta-squared computed after setting aside variation explained by other factors, such as differences between the people measured repeatedly.",
		WhyCare: "Used for repeated-measures designs. It is not comparable with plain eta-squared, so compare like with like.",
		SeeAlso: []string{"eta-squared", "effect-size"},
		Jargon:  true, Forms: []string{"partial eta squared", "partial eta-squared", "partial_eta_squared"},
	},
	{
		ID:      "omega-squared",
		Short:   "An estimate of the share of variation explained by groups, adjusted so it does not overstate the effect the way eta-squared does in small samples.",
		WhyCare: "Prefer it when groups are small; it is the less optimistic, more honest version of eta-squared.",
		SeeAlso: []string{"eta-squared", "effect-size"},
		Jargon:  true, Forms: []string{"omega squared", "omega-squared", "omega_squared"},
	},
	{
		ID:      "epsilon-squared",
		Short:   "The rank-based counterpart of eta-squared, used with Kruskal-Wallis: how much of the ordering of rows is explained by group.",
		WhyCare: "It gives a size to a non-parametric group comparison, which otherwise only yields a p-value.",
		SeeAlso: []string{"eta-squared", "non-parametric", "rank"},
		Jargon:  true, Forms: []string{"epsilon squared", "epsilon-squared", "epsilon_squared"},
	},
	{
		ID:      "cramers-v",
		Short:   "The strength of the relationship between two categorical fields, from 0 (unrelated) to 1 (one fully determines the other).",
		WhyCare: "A chi-square p-value weighs the evidence that the fields are related; Cramer's V says how strongly, and unlike chi-square it does not grow with the number of rows. Compare V only between tables of the same shape.",
		SeeAlso: []string{"chi-square", "phi", "effect-size", "cross-tabulation"},
		Jargon:  true, Forms: []string{"cramer's v", "cramers v", "cramers_v"},
	},
	{
		ID:      "phi",
		Short:   "The strength of the relationship in a two-by-two table, from 0 (unrelated) to 1 (perfectly related).",
		WhyCare: "It is the yes/no-by-yes/no special case of Cramer's V, and reads like a correlation between two binary fields.",
		SeeAlso: []string{"cramers-v", "chi-square", "correlation"},
		Jargon:  true, Forms: []string{"phi", "phi coefficient"},
	},
	{
		ID:      "rank-biserial",
		Short:   "An effect size for rank tests: how much more often a row from one group outranks a row from the other, from -1 to 1.",
		WhyCare: "It gives Mann-Whitney and Wilcoxon results a size and a direction; zero means neither side tends to rank higher.",
		SeeAlso: []string{"rank", "non-parametric", "effect-size"},
		Jargon:  true, Forms: []string{"rank-biserial", "rank biserial", "rank_biserial"},
	},
	{
		ID:      "odds-ratio",
		Short:   "How many times higher the odds of an outcome are in one group than in another; 1 means no difference.",
		WhyCare: "Logistic models report effects this way. Odds are not probabilities, so an odds ratio of 2 does not mean \"twice as likely\" unless the outcome is rare.",
		SeeAlso: []string{"regression-coefficient", "effect-size"},
		Jargon:  true, Forms: []string{"odds ratio", "odds ratios"},
	},

	// --- Describing a measure -------------------------------------------
	{
		ID:      "mean",
		Short:   "The average: add the values up and divide by how many there are.",
		WhyCare: "It is the usual \"typical value\", but a few extreme values can drag it far from where most rows sit; compare it with the median.",
		SeeAlso: []string{"median", "outlier", "skew"},
	},
	{
		ID:      "median",
		Short:   "The middle value once the values are sorted: half the rows sit below it, half above.",
		WhyCare: "It barely moves when a few extreme values appear, so it is a safer \"typical value\" for skewed things like income or response time.",
		SeeAlso: []string{"mean", "percentile", "skew"},
	},
	{
		ID:      "variance",
		Short:   "Roughly the average squared distance of values from their mean: divided by n in the population form, or by n - 1 in the sample form most tools print.",
		WhyCare: "A measure of spread in squared units. Most tests are built on the sample form, but its squared units are hard to read; the standard deviation is the same idea in the original units.",
		SeeAlso: []string{"standard-deviation", "covariance"},
		Jargon:  true, Forms: []string{"variance", "variances"},
	},
	{
		ID:      "standard-deviation",
		Short:   "The typical distance of a value from the mean, in the same units as the data.",
		WhyCare: "It tells you how spread out values are. Two groups with the same mean can behave very differently if their standard deviations differ.",
		SeeAlso: []string{"variance", "z-score", "standard-error"},
		Jargon:  true, Forms: []string{"standard deviation", "standard deviations"},
	},
	{
		ID:      "standard-error",
		Short:   "How much an estimate such as a mean would wobble from sample to sample; it shrinks as you add rows.",
		WhyCare: "It is the uncertainty of the estimate, not the spread of the data. Confidence intervals and most test statistics are built from it.",
		SeeAlso: []string{"standard-deviation", "confidence-interval", "sample-size"},
		Jargon:  true, Forms: []string{"standard error", "standard errors"},
	},
	{
		ID:      "confidence-interval",
		Short:   "A range built by a method that, over repeated samples, captures the true value a stated share of the time (such as 95%); any single interval either contains it or not.",
		WhyCare: "It shows the uncertainty in plain units. A wide interval means you know less than the single estimate suggests.",
		SeeAlso: []string{"standard-error", "p-value", "sample-size"},
		Jargon:  true, Forms: []string{"confidence interval", "confidence intervals"},
	},
	{
		ID:      "z-score",
		Short:   "How many standard deviations a value sits above or below the mean. A z test statistic is the same idea for an estimate: how many standard errors it sits from the null's value.",
		WhyCare: "It puts values from different scales on one footing and makes unusual values easy to spot.",
		SeeAlso: []string{"standard-deviation", "outlier", "normal-distribution"},
		Jargon:  true, Forms: []string{"z-score", "z-scores", "z score", "z scores"},
	},
	{
		ID:      "percentile",
		Short:   "The value at or below which about a given share of rows fall (about 90% sit at or below the 90th); a percentile rank is the reverse, a row's position as a percentage.",
		WhyCare: "Percentiles describe a spread without assuming any shape, and are the honest way to report things like \"most users wait less than X\".",
		SeeAlso: []string{"median", "outlier", "rank"},
	},
	{
		ID:      "outlier",
		Short:   "A value far away from the rest of the data.",
		WhyCare: "Outliers can be errors or the most interesting rows. Either way they can pull means and correlations badly, so look before you drop them.",
		SeeAlso: []string{"z-score", "percentile", "median", "non-parametric"},
	},
	{
		ID:      "normal-distribution",
		Short:   "The symmetric bell-shaped pattern many measurements roughly follow, with most values near the mean and fewer further out.",
		WhyCare: "Many tests assume it. Strong skew or heavy tails make those tests less trustworthy; a non-parametric test is the usual fallback.",
		SeeAlso: []string{"skew", "kurtosis", "non-parametric", "z-score"},
		Jargon:  true, Forms: []string{"normal distribution", "normally distributed", "bell curve"},
	},
	{
		ID:      "skew",
		Short:   "How lopsided a distribution is: a long tail to the right (positive) or to the left (negative).",
		WhyCare: "Skewed data pull the mean toward the tail, so the median often describes them better, and tests assuming a normal shape suffer.",
		SeeAlso: []string{"normal-distribution", "median", "kurtosis"},
		Jargon:  true, Forms: []string{"skew", "skewed", "skewness"},
	},
	{
		ID:      "kurtosis",
		Short:   "How heavy a distribution's tails are compared with the normal distribution: how prone it is to extreme values.",
		WhyCare: "Heavy tails mean outliers turn up more often than a bell curve would predict, which can mislead tests that assume normality.",
		SeeAlso: []string{"skew", "normal-distribution", "outlier"},
		Jargon:  true, Forms: []string{"kurtosis"},
	},
	{
		ID:      "rank",
		Short:   "A value's position once all values are sorted: 1 for the smallest, 2 for the next, and so on.",
		WhyCare: "Working on ranks instead of raw values makes a method far less sensitive to extreme values and usable on ordinal ratings.",
		SeeAlso: []string{"non-parametric", "percentile", "rank-biserial"},
	},
	{
		ID:      "sample-size",
		Short:   "How many rows (or respondents, or measurements) an estimate or test is based on.",
		WhyCare: "Small samples give noisy estimates and weak tests; very large ones make tiny, unimportant differences significant.",
		SeeAlso: []string{"standard-error", "statistical-power", "effective-sample-size"},
	},
	{
		ID:      "missing-value",
		Short:   "A field with no recorded value for a row (a null).",
		WhyCare: "How missing values are handled changes results: dropping them can bias a sample if they are not missing at random.",
		SeeAlso: []string{"listwise-deletion", "pairwise-deletion"},
	},
	{
		ID:      "listwise-deletion",
		Short:   "Dropping a whole row from an analysis when any of the fields it uses is missing.",
		WhyCare: "Every figure then comes from the same rows, which keeps them consistent, but you can lose many rows when several fields each have gaps.",
		SeeAlso: []string{"pairwise-deletion", "missing-value"},
		Jargon:  true, Forms: []string{"listwise deletion"},
	},
	{
		ID:      "pairwise-deletion",
		Short:   "Using every row that has both fields of a given pair, so each pair of fields may rest on a different set of rows.",
		WhyCare: "It keeps more data than listwise deletion, but figures computed from different rows may not fit together consistently.",
		SeeAlso: []string{"listwise-deletion", "missing-value", "correlation"},
		Jargon:  true, Forms: []string{"pairwise deletion"},
	},

	// --- Comparing with a reference ---------------------------------------
	{
		ID:      "baseline",
		Short:   "The reference a figure is compared with: a chosen period, a named group, a population, a total or another request's result.",
		WhyCare: "Every comparison is only as meaningful as its baseline. Say which one you used, and pick one that is stable and large enough to compare against.",
		SeeAlso: []string{"index-value", "margin"},
	},
	{
		ID:      "index-value",
		Short:   "A ratio to a baseline scaled so the baseline is 100: 120 is 20% above it, 80 is 20% below it.",
		WhyCare: "It puts groups of very different sizes on one scale, but it hides the absolute gap and swings wildly when the baseline is small.",
		SeeAlso: []string{"baseline", "percentage-point"},
		Jargon:  true, Forms: []string{"index value", "index values"},
	},
	{
		ID:      "percentage-point",
		Short:   "The plain difference between two percentages: 40% to 45% is a rise of 5 percentage points, which is a 12.5% relative rise.",
		WhyCare: "Mixing up points and percent changes overstates or understates a change; say which one a figure is.",
		SeeAlso: []string{"index-value"},
		Jargon:  true, Forms: []string{"percentage point", "percentage points"},
	},
	{
		ID:      "margin",
		Short:   "In a cross-tabulation, the figure for a whole row, a whole column or the whole table (the grand total), shown along its edges.",
		WhyCare: "Cells are often read against their margin. The margin is whatever the cell aggregator gives for the full row or column: a total for counts and sums, an overall average for means.",
		SeeAlso: []string{"cross-tabulation", "baseline"},
		Jargon:  true, Forms: []string{"margin", "margins", "marginal total", "marginal totals"},
	},
	{
		ID:      "rolling-mean",
		Short:   "The average of the last few points in a series (a moving average), recomputed at every step.",
		WhyCare: "It smooths out short-term swings, so a point far from its rolling mean stands out; a short window reacts fast but is jumpy.",
		SeeAlso: []string{"mean", "baseline"},
		Jargon:  true, Forms: []string{"rolling mean", "rolling average", "moving average"},
	},
	{
		ID:      "probit",
		Short:   "A transform that maps a proportion to the z-score at which the normal curve has that share below it: 0.5 maps to 0, 0.84 to about 1.",
		WhyCare: "It stretches proportions near 0 and 1, where small changes in share matter more; tests built on it are a convention some survey tools follow.",
		SeeAlso: []string{"z-score", "normal-distribution"},
		Jargon:  true, Forms: []string{"probit"},
	},

	// --- Relationships ----------------------------------------------------
	{
		ID:      "covariance",
		Short:   "How two numeric fields vary together: positive when they rise together, negative when one rises as the other falls.",
		WhyCare: "Its size depends on the units, so it is hard to read on its own; correlation is the unit-free version.",
		SeeAlso: []string{"correlation", "variance"},
		Jargon:  true, Forms: []string{"covariance", "covariances"},
	},
	{
		ID:      "correlation",
		Short:   "How closely two measures move together in a straight line, from -1 (opposite) through 0 (no straight-line link) to 1 (in lockstep).",
		WhyCare: "It shows association, not cause. It also misses curved relationships and can be distorted by a few outliers.",
		SeeAlso: []string{"covariance", "r-squared", "outlier", "independence"},
		Jargon:  true, Forms: []string{"correlation", "correlations", "correlation coefficient"},
	},
	{
		ID:      "partial-correlation",
		Short:   "The correlation between two measures after the straight-line part of chosen other measures has been taken out of both.",
		WhyCare: "It separates a direct link from one that appears only because both measures follow a third; like any correlation it shows association, not cause.",
		SeeAlso: []string{"correlation", "multicollinearity"},
		Jargon:  true, Forms: []string{"partial correlation", "partial correlations", "partial r"},
	},
	{
		ID:      "cross-tabulation",
		Short:   "A table that counts rows for every combination of two categorical fields, one along the rows and one along the columns.",
		WhyCare: "It is the simplest way to see whether two categories go together; a chi-square test then says how surprising the pattern would be if the two fields were unrelated.",
		SeeAlso: []string{"chi-square", "cramers-v"},
		Jargon:  true, Forms: []string{"cross-tabulation", "contingency table", "contingency tables"},
	},
	{
		ID:      "monotonic-trend",
		Short:   "A pattern that keeps going one way, always rising or always falling, though not necessarily in a straight line.",
		WhyCare: "Rank-based measures such as Spearman's rho, Kendall's tau and the Mann-Kendall trend test detect this kind of pattern; they can miss a link that rises and then falls.",
		SeeAlso: []string{"spearman-rho", "kendall-tau", "correlation"},
		Jargon:  true, Forms: []string{"monotonic", "monotone", "monotonic trend"},
	},
	{
		ID:      "spearman-rho",
		Short:   "A rank correlation from -1 to 1: Pearson's correlation computed on the ranks of the values instead of the values themselves.",
		WhyCare: "It captures any relationship that keeps going one way, straight or curved, and resists outliers; it misses U-shaped links.",
		SeeAlso: []string{"correlation", "kendall-tau", "rank", "monotonic-trend"},
		Jargon:  true, Forms: []string{"spearman's rho", "spearman rho", "spearman correlation"},
	},
	{
		ID:      "kendall-tau",
		Short:   "A rank correlation from -1 to 1 based on how many pairs of rows are in the same order on both fields versus the opposite order.",
		WhyCare: "It copes well with small samples and many ties. It usually comes out smaller than Spearman's rho on the same data, so do not compare the two directly.",
		SeeAlso: []string{"correlation", "spearman-rho", "rank", "ties"},
		Jargon:  true, Forms: []string{"kendall's tau", "kendall tau", "tau-b", "tau_b"},
	},

	// --- Regression and modelling -----------------------------------------
	{
		ID:      "regression-coefficient",
		Short:   "How much the predicted outcome changes when one predictor goes up by one unit, holding the other predictors fixed.",
		WhyCare: "It is the \"how much\" of a driver. Its size depends on the predictor's units, and with correlated predictors it can be unstable.",
		SeeAlso: []string{"standard-error", "multicollinearity", "r-squared", "odds-ratio"},
		Jargon:  true, Forms: []string{"regression coefficient", "regression coefficients"},
	},
	{
		ID:      "r-squared",
		Short:   "The share of the variation in an outcome that a model explains, from 0 (nothing) to 1 (everything).",
		WhyCare: "Higher means the model tracks the outcome more closely, but it always rises as you add predictors, even useless ones; watch for overfitting.",
		SeeAlso: []string{"residual", "overfitting", "correlation"},
		Jargon:  true, Forms: []string{"r-squared", "r squared", "r²", "r2", "coefficient of determination"},
	},
	{
		ID:      "residual",
		Short:   "The gap between an actual value and what a model predicted for it.",
		WhyCare: "Patterns in the residuals reveal what the model misses; large ones flag rows the model fits badly.",
		SeeAlso: []string{"r-squared", "outlier"},
		Jargon:  true, Forms: []string{"residual", "residuals"},
	},
	{
		ID:      "multicollinearity",
		Short:   "Predictors in a model that are strongly related to each other.",
		WhyCare: "The model can still predict well, but it cannot tell the overlapping predictors' effects apart, so their coefficients become unstable and hard to trust.",
		SeeAlso: []string{"regression-coefficient", "correlation"},
		Jargon:  true, Forms: []string{"multicollinearity", "collinearity", "collinear"},
	},
	{
		ID:      "overfitting",
		Short:   "When a model learns the noise in its data instead of the real pattern.",
		WhyCare: "An overfit model looks excellent on the data it was built from and disappoints on new data. Fewer predictors or more rows help.",
		SeeAlso: []string{"r-squared", "sample-size"},
		Jargon:  true, Forms: []string{"overfitting", "overfit", "over-fitting"},
	},
	{
		ID:      "adjusted-r-squared",
		Short:   "R-squared corrected for the number of predictors: it rises only when an added predictor's t value exceeds 1 in size, which a useless predictor still does about one time in three.",
		WhyCare: "Use it to compare models with different numbers of predictors on the same rows. It can fall below zero when the predictors fit worse than chance would predict.",
		SeeAlso: []string{"r-squared", "overfitting"},
		Jargon:  true, Forms: []string{"adjusted r-squared", "adjusted r squared", "adjusted r²", "adj_r2"},
	},
	{
		ID:      "heteroscedasticity",
		Short:   "Residuals whose spread changes across the data, for example growing as the predicted value grows, instead of staying constant.",
		WhyCare: "Classic regression standard errors assume a constant spread; when it changes, they and the p-values built on them can be off in either direction, even though the coefficients themselves stay unbiased.",
		SeeAlso: []string{"residual", "standard-error", "homogeneity-of-variance"},
		Jargon:  true, Forms: []string{"heteroscedasticity", "heteroskedasticity", "heteroscedastic", "non-constant variance"},
	},
	{
		ID:      "generalized-linear-model",
		Short:   "A regression for outcomes that are not plain numbers on a line, such as yes/no or counts: a link function ties the predictors to the outcome's expected value.",
		WhyCare: "It keeps predictions in range (probabilities between zero and one, counts above zero), but its coefficients are on the link scale, so read them through the link before talking about sizes.",
		SeeAlso: []string{"link-function", "logistic-regression", "deviance", "regression-coefficient"},
		Jargon:  true, Forms: []string{"generalized linear model", "generalized linear models", "glm", "glms"},
	},
	{
		ID:      "link-function",
		Short:   "The transformation a generalized linear model applies to the outcome's expected value before relating it to the predictors, such as the log or the log-odds.",
		WhyCare: "A coefficient moves the outcome on the link scale, not in the outcome's own units: under a log link it multiplies the expected value, under the logit it multiplies the odds.",
		SeeAlso: []string{"generalized-linear-model", "logistic-regression", "odds-ratio"},
		Jargon:  true, Forms: []string{"link function", "link functions"},
	},
	{
		ID:      "logistic-regression",
		Short:   "A regression for a yes/no outcome that models the log-odds of yes as a straight-line function of the predictors.",
		WhyCare: "Each coefficient is a change in log-odds; raised to the power e it becomes an odds ratio. Odds are not probabilities, so a constant odds ratio means different probability shifts at different starting points.",
		SeeAlso: []string{"generalized-linear-model", "odds-ratio", "link-function"},
		Jargon:  true, Forms: []string{"logistic regression", "logistic regressions", "logit", "log-odds"},
	},
	{
		ID:      "deviance",
		Short:   "A measure of how far a fitted model's predictions sit from the observed outcomes, on a log-likelihood scale; lower means a closer fit.",
		WhyCare: "On its own the number has no fixed scale. Compare it with the null deviance (the intercept-only model) or with another model fitted to the same rows and outcome.",
		SeeAlso: []string{"generalized-linear-model", "pseudo-r-squared"},
		Jargon:  true, Forms: []string{"deviance", "deviances", "null deviance"},
	},
	{
		ID:      "pseudo-r-squared",
		Short:   "A fit summary for models such as logistic regression where ordinary R-squared does not apply; Pulse's is one minus deviance over null deviance.",
		WhyCare: "It is not a share of variance explained and runs much lower than R-squared for an equally useful model, so R-squared rules of thumb do not carry over. Compare it only across models of the same outcome.",
		SeeAlso: []string{"deviance", "r-squared", "logistic-regression"},
		Jargon:  true, Forms: []string{"pseudo-r-squared", "pseudo r-squared", "pseudo r squared", "pseudo-r²", "pseudo_r2", "mcfadden"},
	},
	{
		ID:      "overdispersion",
		Short:   "Count data that vary more than a Poisson model allows, which assumes the variance equals the mean.",
		WhyCare: "It is common in real counts. A model that ignores it reports standard errors that are too small and p-values that look stronger than the data support.",
		SeeAlso: []string{"generalized-linear-model", "standard-error", "variance"},
		Jargon:  true, Forms: []string{"overdispersion", "overdispersed", "over-dispersion"},
	},
	{
		ID:      "prior",
		Short:   "In a Bayesian analysis, the distribution that states what values a parameter is believed to take before the data are seen.",
		WhyCare: "The result blends the prior with the data. A weak prior lets the data dominate; a strong or badly chosen one pulls estimates toward it, so always report which prior was used.",
		SeeAlso: []string{"posterior", "credible-interval"},
		Jargon:  true, Forms: []string{"prior distribution", "prior distributions", "prior belief", "prior beliefs"},
	},
	{
		ID:      "posterior",
		Short:   "In a Bayesian analysis, the distribution of a parameter after combining the prior with the data, given the model.",
		WhyCare: "Point estimates (such as the posterior mean) and credible intervals are summaries of it. It is only as trustworthy as the prior and the model that produced it.",
		SeeAlso: []string{"prior", "credible-interval"},
		Jargon:  true, Forms: []string{"posterior", "posterior distribution", "posterior mean", "posterior means"},
	},
	{
		ID:      "credible-interval",
		Short:   "A Bayesian range that holds a parameter with a stated probability, given the prior, the model and the data.",
		WhyCare: "Unlike a confidence interval it is a direct probability statement about the parameter, but that probability is conditional on the prior and the model; change either and the interval changes.",
		SeeAlso: []string{"posterior", "prior", "confidence-interval"},
		Jargon:  true, Forms: []string{"credible interval", "credible intervals"},
	},

	// --- Weighting --------------------------------------------------------
	{
		ID:      "weighting",
		Short:   "Giving some rows more say than others in a figure, usually so a sample better matches the population it stands for.",
		WhyCare: "Weights fix known imbalances, but heavy weights make results noisier; check the effective sample size.",
		SeeAlso: []string{"raking", "effective-sample-size"},
	},
	{
		ID:      "raking",
		Short:   "Adjusting weights step by step until the weighted sample matches known population totals on several characteristics at once.",
		WhyCare: "It is the standard way to make a survey sample look like the population on age, region and the like, without needing every combination's total.",
		SeeAlso: []string{"weighting", "effective-sample-size"},
		Jargon:  true, Forms: []string{"raking", "rake", "iterative proportional fitting"},
	},
	{
		ID:      "effective-sample-size",
		Short:   "The number of unweighted rows that would give the same precision as your weighted sample.",
		WhyCare: "Uneven weights cost precision: a weighted sample of 1,000 can carry the information of far fewer rows.",
		SeeAlso: []string{"weighting", "sample-size", "standard-error"},
		Jargon:  true, Forms: []string{"effective sample size", "effective n"},
	},

	// --- Measuring constructs and dimensions --------------------------------
	{
		ID:      "eigenvalue",
		Short:   "How much of the total variation in a set of fields one underlying dimension accounts for.",
		WhyCare: "It ranks the dimensions a principal component analysis finds; dimensions with small eigenvalues explain little and are usually dropped.",
		SeeAlso: []string{"principal-component", "variance"},
		Jargon:  true, Forms: []string{"eigenvalue", "eigenvalues"},
	},
	{
		ID:      "principal-component",
		Short:   "A new combined measure built from many correlated fields so that a few of them capture most of the variation.",
		WhyCare: "It shrinks dozens of overlapping ratings into a handful of summary scores, making patterns easier to see and model.",
		SeeAlso: []string{"eigenvalue", "loading", "factor"},
		Jargon:  true, Forms: []string{"principal component", "principal components", "pca"},
	},
	{
		ID:      "loading",
		Short:   "How strongly one original field is tied to a component or factor.",
		WhyCare: "Loadings are how you name a component: the fields with the largest loadings tell you what it represents.",
		SeeAlso: []string{"principal-component", "factor"},
		Jargon:  true, Forms: []string{"factor loading", "factor loadings", "component loading", "component loadings"},
	},
	{
		ID:      "factor",
		Short:   "An unobserved quality, such as satisfaction, that several measured fields are assumed to reflect together.",
		WhyCare: "Grouping questions into factors lets you measure something no single question captures well.",
		SeeAlso: []string{"loading", "principal-component", "reliability"},
		Jargon:  true, Forms: []string{"latent factor", "latent factors", "factor analysis"},
	},
	{
		ID:      "reliability",
		Short:   "How consistently a set of questions measures the same thing.",
		WhyCare: "A scale built from unreliable questions mixes signal with noise, so averages and comparisons based on it are weaker.",
		SeeAlso: []string{"factor", "correlation"},
	},

	// --- Segmentation --------------------------------------------------------
	{
		ID:      "centroid",
		Short:   "The centre point of a cluster: the average of every field across the rows in it.",
		WhyCare: "Comparing centroids is how you describe what makes one segment different from another.",
		SeeAlso: []string{"distance", "similarity"},
		Jargon:  true, Forms: []string{"centroid", "centroids"},
	},
	{
		ID:      "distance",
		Short:   "A number for how far apart two rows are across several fields; zero means identical.",
		WhyCare: "Clustering groups rows by distance, so fields with large units dominate unless you standardise them first.",
		SeeAlso: []string{"similarity", "centroid", "z-score"},
	},
	{
		ID:      "similarity",
		Short:   "A number for how alike two rows or items are; the mirror image of distance.",
		WhyCare: "It drives \"customers like this one\" style matching. Different measures weigh shared absences differently, so pick one that fits your data.",
		SeeAlso: []string{"distance", "correlation"},
	},

	// --- Flows ------------------------------------------------------------
	{
		ID:      "stochastic-matrix",
		Short:   "A table of probabilities of moving from each state (row) to each state (column), where every row adds up to 1.",
		WhyCare: "It describes flows such as customers switching brands or plans from one period to the next, and lets you project them forward.",
		SeeAlso: []string{"steady-state"},
		Jargon:  true, Forms: []string{"stochastic matrix", "transition matrix", "transition matrices"},
	},
	{
		ID:      "steady-state",
		Short:   "The mix of states a flow settles into if the same switching probabilities keep applying period after period.",
		WhyCare: "It shows where shares are heading in the long run, assuming nothing about the switching behaviour changes.",
		SeeAlso: []string{"stochastic-matrix"},
		Jargon:  true, Forms: []string{"steady state", "steady-state", "stationary distribution"},
	},
}

// Glossary returns the glossary in declaration order. The result is a
// deep copy: callers may mutate it freely.
func Glossary() []descriptor.Term {
	out := make([]descriptor.Term, len(glossaryRegistry))
	for i, t := range glossaryRegistry {
		t.SeeAlso = append([]string(nil), t.SeeAlso...)
		t.Forms = append([]string(nil), t.Forms...)
		out[i] = t
	}
	return out
}

// GlossaryIDs returns the sorted glossary term IDs.
func GlossaryIDs() []string {
	out := make([]string, len(glossaryRegistry))
	for i, t := range glossaryRegistry {
		out[i] = t.ID
	}
	sort.Strings(out)
	return out
}

// IsGlossaryTerm reports whether id names a glossary term.
func IsGlossaryTerm(id string) bool {
	for _, t := range glossaryRegistry {
		if t.ID == id {
			return true
		}
	}
	return false
}

// JargonForms maps every jargon term's surface form (lowercase) to its
// term ID — the vocabulary the jargon-link check scans plain-language
// guidance for. The map is a fresh copy.
func JargonForms() map[string]string {
	out := map[string]string{}
	for _, t := range glossaryRegistry {
		if !t.Jargon {
			continue
		}
		for _, f := range t.Forms {
			out[f] = t.ID
		}
	}
	return out
}

// glossaryProblems validates a glossary and returns one message per
// problem, nil when it is well formed: unique kebab IDs, a non-empty
// Short and WhyCare within their length limits, every SeeAlso resolving
// to another term, every jargon term carrying at least one Form, and
// every Form lowercase, trimmed and claimed by exactly one term.
func glossaryProblems(terms []descriptor.Term) []string {
	var problems []string
	ids := map[string]bool{}
	for _, t := range terms {
		if !glossaryIDPattern.MatchString(t.ID) {
			problems = append(problems, fmt.Sprintf("term %q: ID is not kebab-case", t.ID))
		}
		if ids[t.ID] {
			problems = append(problems, fmt.Sprintf("term %q: declared twice", t.ID))
		}
		ids[t.ID] = true
	}
	formOwner := map[string]string{}
	for _, t := range terms {
		if strings.TrimSpace(t.Short) == "" {
			problems = append(problems, fmt.Sprintf("term %q: empty Short", t.ID))
		}
		if strings.TrimSpace(t.WhyCare) == "" {
			problems = append(problems, fmt.Sprintf("term %q: empty WhyCare", t.ID))
		}
		if n := len([]rune(t.Short)); n > glossaryShortMax {
			problems = append(problems, fmt.Sprintf("term %q: Short is %d chars, limit %d", t.ID, n, glossaryShortMax))
		}
		if n := len([]rune(t.WhyCare)); n > glossaryWhyCareMax {
			problems = append(problems, fmt.Sprintf("term %q: WhyCare is %d chars, limit %d", t.ID, n, glossaryWhyCareMax))
		}
		seeAlso := map[string]bool{}
		for _, s := range t.SeeAlso {
			switch {
			case s == t.ID:
				problems = append(problems, fmt.Sprintf("term %q: SeeAlso cites itself", t.ID))
			case !ids[s]:
				problems = append(problems, fmt.Sprintf("term %q: SeeAlso %q does not resolve", t.ID, s))
			case seeAlso[s]:
				problems = append(problems, fmt.Sprintf("term %q: SeeAlso %q listed twice", t.ID, s))
			}
			seeAlso[s] = true
		}
		if t.Jargon && len(t.Forms) == 0 {
			problems = append(problems, fmt.Sprintf("term %q: jargon term has no Forms", t.ID))
		}
		for _, f := range t.Forms {
			if f == "" || f != strings.TrimSpace(f) || f != strings.ToLower(f) {
				problems = append(problems, fmt.Sprintf("term %q: Form %q must be non-empty, trimmed and lowercase", t.ID, f))
			}
			if owner, dup := formOwner[f]; dup {
				problems = append(problems, fmt.Sprintf("term %q: Form %q already belongs to term %q", t.ID, f, owner))
				continue
			}
			formOwner[f] = t.ID
		}
	}
	return problems
}
