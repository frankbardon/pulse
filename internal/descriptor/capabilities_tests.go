package descriptor

import (
	"github.com/frankbardon/pulse/types"

	"github.com/frankbardon/pulse/descriptor"
)

// alphaParam is the shared significance-level parameter used by every
// statistical test. The wire shape lives on the top-level Test struct
// (not in Params — Slot), but it is the most-asked parameter so we
// surface it in the manifest entry alongside Params for ergonomics.
var alphaParam = descriptor.Param{
	Name:        "alpha",
	Type:        "float",
	Required:    false,
	Default:     0.05,
	Description: "Significance level in (0, 1); RejectNull is true when PValue < alpha.",
	Slot:        true,
}

// partitionByParam is every post-test's partition key list (U46 E3-S2,
// internal/groupedtests.PostTestDepth): over two or more groupers a
// post-test runs once per partition of the result rows.
// testReferenceParam is the two-group tests' params.reference (U48): the
// split_by level every signed figure is read against.
var testReferenceParam = descriptor.Param{
	Name:        "reference",
	Type:        "string",
	Required:    false,
	Description: "SplitBy level read second, so diff, its interval, the statistic and the signed effect size read other − reference (default: alphabetical). A split_by level (\"0\" / \"1\" on a packed_bool) and needs SplitBy, else PULSE_TEST_REFERENCE_INVALID; neither observed group is PULSE_TEST_REFERENCE_MISSING.",
}

// testCovariateParam is the two-sample mean tests' params.covariate
// (U48, CUPED): the pre-period column the difference of means is
// adjusted by, one pass from per-group moments.
var testCovariateParam = descriptor.Param{
	Name:        "covariate",
	Type:        "string",
	Required:    false,
	Description: "Numeric pre-period column (CUPED): the test runs on y − θ·(x − x̄), θ the slope of Field on it pooled over both groups, so diff, its interval, the statistic, p and cohens_d are adjusted; details add diff_unadjusted, theta, covariate_mean, variance_reduction, n_covariate_dropped (null-covariate rows dropped). Needs SplitBy; else, or non-numeric, PULSE_TEST_COVARIATE_INVALID — as on any other test.",
}

var partitionByParam = descriptor.Param{
	Name:        "partition_by",
	Type:        "list",
	Required:    false,
	Description: "Over two or more groupers the post-test runs once per partition: the rows sharing these leading group key columns (default every key but the last; [] = one run over every row). Every post-test of a request partitions alike; PULSE_TEST_PARTITION_INVALID otherwise.",
}

// withPartitionBy appends partitionByParam to every tier-2 entry.
func withPartitionBy(ms []descriptor.TestMeta) []descriptor.TestMeta {
	for i := range ms {
		ms[i].Params = append(ms[i].Params, partitionByParam)
	}
	return ms
}

// testCapabilities returns the metadata for every entry in
// types.AllTestTypes(). Order is irrelevant; manifest assembly sorts by
// Name.
func testCapabilities() []descriptor.TestMeta {
	return []descriptor.TestMeta{
		{
			Name:        string(types.TEST_T),
			Family:      string(types.TEST_T),
			Tier:        1,
			Description: "One-sample or two-sample Welch t-test on a numeric Field; SplitBy switches to the two-sample variant.",
			Streamable:  types.TEST_T.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "mu", Type: "float", Required: false, Default: 0.0, Description: "Hypothesized mean for the one-sample variant (ignored when SplitBy is set)."},
				testReferenceParam,
				testCovariateParam,
			},
			Requires: []string{"Field"},
		},
		{
			Name:        string(types.TEST_WELCH),
			Family:      string(types.TEST_WELCH),
			Tier:        1,
			Description: "Explicit two-sample Welch t-test alias; identical to TEST_T with SplitBy.",
			Streamable:  types.TEST_WELCH.Streamable(),
			Params:      []descriptor.Param{alphaParam, testReferenceParam, testCovariateParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_CHISQ),
			Family:      string(types.TEST_CHISQ),
			Tier:        1,
			Description: "Chi-square independence test on a 2D Rows×Cols contingency table; with params.expected and no Cols, a goodness-of-fit test of Rows' level counts against expected proportions.",
			Streamable:  types.TEST_CHISQ.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "expected", Type: "object", Required: false, Description: "Goodness of fit: map of Rows level to expected proportion (each > 0, summing to 1 within 1e-9; never renormalised). Selects Variant goodness_of_fit (df = labels − 1) and requires Cols absent; an observed level it omits is refused, a listed level never observed counts as 0."},
			},
			Requires: []string{"Rows", "Cols"},
		},
		{
			Name:        string(types.TEST_ANOVA_F),
			Family:      string(types.TEST_ANOVA_F),
			Tier:        1,
			Description: "One-way ANOVA F-test comparing Field means across the categorical SplitBy partition.",
			Streamable:  types.TEST_ANOVA_F.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_KS),
			Family:      string(types.TEST_KS),
			Tier:        1,
			Description: "Kolmogorov-Smirnov two-sample distribution test on Field partitioned by SplitBy.",
			Streamable:  types.TEST_KS.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_PAIRED_T),
			Family:      string(types.TEST_PAIRED_T),
			Tier:        1,
			Description: "Paired-sample t-test on the per-row difference d = Field − Field2.",
			Streamable:  types.TEST_PAIRED_T.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_PROP_Z),
			Family:      string(types.TEST_PROP_Z),
			Tier:        1,
			Description: "Two-proportion z-test on the success rate of Field across two SplitBy groups; with params.p0 and no SplitBy, a one-proportion z-test against p0.",
			Streamable:  types.TEST_PROP_Z.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "success", Type: "string", Required: true, Description: "Dictionary value of Field treated as a success; on a packed_bool Field exactly \"1\" (true) or \"0\" (false)."},
				{Name: "p0", Type: "float", Required: false, Description: "Hypothesised success rate in (0, 1) for the one-proportion variant (Variant one_proportion); requires SplitBy absent."},
				testReferenceParam,
			},
			Requires: []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_PEARSON_R),
			Family:      string(types.TEST_PEARSON_R),
			Tier:        1,
			Description: "Parametric Pearson correlation between Field and Field2; reports r and a t-test p-value.",
			Streamable:  types.TEST_PEARSON_R.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_MANN_WHITNEY_U),
			Family:      string(types.TEST_MANN_WHITNEY_U),
			Tier:        1,
			Description: "Nonparametric two-sample location test; buffered tie-corrected rank sum.",
			Streamable:  types.TEST_MANN_WHITNEY_U.Streamable(),
			Params:      []descriptor.Param{alphaParam, testReferenceParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_WILCOXON_SR),
			Family:      string(types.TEST_WILCOXON_SR),
			Tier:        1,
			Description: "Wilcoxon signed-rank test on per-row d = Field − Field2; buffered tie-corrected sign-rank sum.",
			Streamable:  types.TEST_WILCOXON_SR.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_KRUSKAL_WALLIS),
			Family:      string(types.TEST_KRUSKAL_WALLIS),
			Tier:        1,
			Description: "Nonparametric k-group location test; ANOVA-like rank sums under tie correction.",
			Streamable:  types.TEST_KRUSKAL_WALLIS.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_SPEARMAN_R),
			Family:      string(types.TEST_SPEARMAN_R),
			Tier:        1,
			Description: "Rank-based correlation between Field and Field2 (monotonic association).",
			Streamable:  types.TEST_SPEARMAN_R.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_KENDALL_TAU),
			Family:      string(types.TEST_KENDALL_TAU),
			Tier:        1,
			Description: "Concordance-based correlation between Field and Field2; buffered O(n²) pair count.",
			Streamable:  types.TEST_KENDALL_TAU.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_ANOVA_WELCH),
			Family:      string(types.TEST_ANOVA_WELCH),
			Tier:        1,
			Description: "Heteroscedasticity-robust one-way ANOVA with Welch-Satterthwaite df correction.",
			Streamable:  types.TEST_ANOVA_WELCH.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_ANOVA_RM),
			Family:      string(types.TEST_ANOVA_RM),
			Tier:        1,
			Description: "Repeated-measures one-way ANOVA; rows pivoted on SubjectField × SplitBy condition.",
			Streamable:  types.TEST_ANOVA_RM.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy", "SubjectField"},
		},
		{
			Name:        string(types.TEST_BROWN_FORSYTHE),
			Family:      string(types.TEST_BROWN_FORSYTHE),
			Tier:        1,
			Description: "Homogeneity-of-variance test; one-way ANOVA on absolute deviations from per-group medians.",
			Streamable:  types.TEST_BROWN_FORSYTHE.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_FISHER_EXACT),
			Family:      string(types.TEST_FISHER_EXACT),
			Tier:        1,
			Description: "Exact two-sided p-value for a 2×2 Rows×Cols contingency table; small-sample CHISQ alternative.",
			Streamable:  types.TEST_FISHER_EXACT.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Rows", "Cols"},
		},
		{
			Name:        string(types.TEST_SHAPIRO_WILK),
			Family:      string(types.TEST_SHAPIRO_WILK),
			Tier:        1,
			Description: "Shapiro-Francia (W′) normality test on Field; runs per-group when SplitBy is set (p-value advisory outside 5..5000).",
			Streamable:  types.TEST_SHAPIRO_WILK.Streamable(),
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field"},
		},
		{
			Name:        string(types.TEST_Z_TWO_SAMPLE),
			Family:      string(types.TEST_Z_TWO_SAMPLE),
			Tier:        1,
			Description: "Two-sample z-test on Field means across two SplitBy groups; identical SE to TEST_WELCH but p-value via standard normal Φ.",
			Streamable:  types.TEST_Z_TWO_SAMPLE.Streamable(),
			Params:      []descriptor.Param{alphaParam, testReferenceParam, testCovariateParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_HOTELLING_T2),
			Family:      string(types.TEST_HOTELLING_T2),
			Tier:        1,
			Description: "Hotelling's T² on a mean vector (exactly one of Vector / Fields, listwise): one-sample against params.mu, paired (Vector2 / Fields2, same length; the row-wise difference vector against params.mu), or two-sample with a 2-level SplitBy — pooled covariance (exact F) or params.variance unequal (Krishnamoorthy–Yu, fractional df2).",
			Streamable:  types.TEST_HOTELLING_T2.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "mu", Type: "list", Required: false, Description: "Hypothesized mean vector (one-sample) or mean difference vector (paired), one entry per member (default zeros; ignored when SplitBy is set)."},
				{Name: "variance", Type: "enum", Required: false, Default: "pooled", EnumValues: []string{"pooled", "unequal"}, Description: "Two-sample covariance: pooled (one shared covariance, exact F) or unequal (Krishnamoorthy–Yu modified Nel–Van der Merwe F, fractional df2); unequal is refused on the one-sample and paired forms."},
				{Name: "missing", Type: "enum", Required: false, Default: "listwise", EnumValues: []string{"listwise"}, Description: "Missing-data mode; only listwise (a row with any null member is dropped)."},
			},
		},
		{
			Name:        string(types.TEST_MANOVA),
			Family:      string(types.TEST_MANOVA),
			Tier:        1,
			Description: "One-way MANOVA of a mean vector (exactly one of Vector / Fields, listwise) across a SplitBy of ≥ 2 levels: statistic, df and p are Pillai's trace; details carry Pillai, Wilks (Rao F), Hotelling–Lawley and Roy (an upper bound), each with f, df1, df2 and p_value.",
			Streamable:  types.TEST_MANOVA.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "missing", Type: "enum", Required: false, Default: "listwise", EnumValues: []string{"listwise"}, Description: "Missing-data mode; only listwise (a row with any null member is dropped)."},
			},
			Requires: []string{"SplitBy"},
		},
		{
			Name:        string(types.TEST_BARTLETT_SPHERICITY),
			Family:      string(types.TEST_BARTLETT_SPHERICITY),
			Tier:        1,
			Description: "Bartlett's sphericity test on a vector's correlation matrix (exactly one of Vector / Fields, ≥ 2 members, listwise; no SplitBy): χ² = −ln|R|·(N* − 1 − (2p + 5)/6) on p(p − 1)/2 df, bit-equal to MAT_PCA's bartlett_* scalars on the same rows. A small p says the members correlate enough to factor.",
			Streamable:  types.TEST_BARTLETT_SPHERICITY.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "missing", Type: "enum", Required: false, Default: "listwise", EnumValues: []string{"listwise"}, Description: "Missing-data mode; only listwise (a row with any null member is dropped)."},
			},
		},
		{
			Name:        string(types.TEST_BOX_M),
			Family:      string(types.TEST_BOX_M),
			Tier:        1,
			Description: "Box's M test of one shared covariance matrix across a SplitBy of ≥ 2 levels (exactly one of Vector / Fields, listwise): statistic, df and p are Box's χ² approximation; details carry M and the F approximation (f, df1, df2, f_p_value). The equal-covariance check for TEST_MANOVA and the pooled TEST_HOTELLING_T2.",
			Streamable:  types.TEST_BOX_M.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "missing", Type: "enum", Required: false, Default: "listwise", EnumValues: []string{"listwise"}, Description: "Missing-data mode; only listwise (a row with any null member is dropped)."},
			},
			Requires: []string{"SplitBy"},
		},
		{
			Name:        string(types.TEST_MARDIA),
			Family:      string(types.TEST_MARDIA),
			Tier:        1,
			Description: "Mardia's multivariate normality test on a vector (exactly one of Vector / Fields, ≥ 1 member, listwise; no SplitBy). Skewness b1p gives χ² = N·b1p/6 on p(p + 1)(p + 2)/6 df, small-sample corrected when N < 20. Kurtosis b2p gives a z. params.statistic picks the headline; both ride details. Buffered: needs the fitted mean and covariance before any row's products.",
			Streamable:  types.TEST_MARDIA.Streamable(),
			Params: []descriptor.Param{
				alphaParam,
				{Name: "statistic", Type: "enum", Required: false, Default: "skewness", EnumValues: []string{"skewness", "kurtosis"}, Description: "Headline statistic: the skewness χ² (default) or the kurtosis z; both always appear in details."},
				{Name: "missing", Type: "enum", Required: false, Default: "listwise", EnumValues: []string{"listwise"}, Description: "Missing-data mode; only listwise (a row with any null member is dropped)."},
			},
		},
		// Tier-2 natives: TREND and TUKEY_HSD are exclusively post-tests.
		{
			Name:        string(types.TEST_TREND),
			Family:      string(types.TEST_TREND),
			Tier:        2,
			Variant:     "mann_kendall",
			Description: "Mann-Kendall trend test on an ordered numeric series; tier-2 over windowed result rows.",
			Streamable:  false,
			Params: []descriptor.Param{
				alphaParam,
				{Name: "variant", Type: "enum", Required: false, Default: "mann_kendall", EnumValues: []string{"mann_kendall"}, Description: "Trend test variant; only mann_kendall in v1."},
				partitionByParam,
			},
			Requires: []string{"Field", "OrderBy"},
		},
		{
			Name:        string(types.TEST_TUKEY_HSD),
			Family:      string(types.TEST_TUKEY_HSD),
			Tier:        2,
			Variant:     "tukey_kramer",
			Description: "Post-hoc pairwise comparison of group means using Tukey's Honestly Significant Difference.",
			Streamable:  false,
			Params: []descriptor.Param{
				alphaParam,
				{Name: "ms_within", Type: "float", Required: true, Description: "Within-group mean square from a prior ANOVA aggregate."},
				{Name: "df_within", Type: "float", Required: true, Description: "Within-group degrees of freedom from a prior ANOVA aggregate."},
				{Name: "n_column", Type: "string", Required: false, Default: "n", Description: "Result-row column holding each group's size (the per-group n Tukey-Kramer weighs a pair by)."},
				partitionByParam,
			},
			Requires: []string{"Field", "SplitBy"},
		},
	}
}

// postTestCapabilities returns metadata for the tier-2 post-test variants
// registered in internal/processing/test_post*.go. Each entry has Tier=2 and a
// non-empty Variant; the natively-tier-2 TREND / TUKEY_HSD entries live in
// testCapabilities() above with Tier=2.
func postTestCapabilities() []descriptor.TestMeta {
	return withPartitionBy([]descriptor.TestMeta{
		{
			Name:        string(types.TEST_ANOVA_F) + "/one_way_from_summary",
			Family:      string(types.TEST_ANOVA_F),
			Tier:        2,
			Variant:     "one_way_from_summary",
			Description: "One-way ANOVA on per-group summary statistics (mean, variance, n) materialized by upstream aggregators.",
			Streamable:  false,
			Params: []descriptor.Param{
				alphaParam,
				{Name: "n_col", Type: "string", Required: true, Description: "Result-row column holding each group's size n."},
				{Name: "variance_col", Type: "string", Required: true, Description: "Result-row column holding each group's sample variance."},
			},
			Requires: []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_PEARSON_R) + "/pearson_post",
			Family:      string(types.TEST_PEARSON_R),
			Tier:        2,
			Variant:     "pearson_post",
			Description: "Pearson correlation between two output columns of the result row set.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_PAIRED_T) + "/paired_two_sided_post",
			Family:      string(types.TEST_PAIRED_T),
			Tier:        2,
			Variant:     "paired_two_sided_post",
			Description: "Paired t-test on the per-row difference of two output columns.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_SPEARMAN_R) + "/rank_pearson_post",
			Family:      string(types.TEST_SPEARMAN_R),
			Tier:        2,
			Variant:     "rank_pearson_post",
			Description: "Spearman rank correlation between two output columns of the result row set.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_KENDALL_TAU) + "/tau_b_post",
			Family:      string(types.TEST_KENDALL_TAU),
			Tier:        2,
			Variant:     "tau_b_post",
			Description: "Kendall tau-b correlation between two output columns of the result row set.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_WILCOXON_SR) + "/asymptotic_post",
			Family:      string(types.TEST_WILCOXON_SR),
			Tier:        2,
			Variant:     "asymptotic_post",
			Description: "Wilcoxon signed-rank test on the per-row difference of two output columns (asymptotic p-value).",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
		{
			Name:        string(types.TEST_ANOVA_WELCH) + "/welch_one_way_post",
			Family:      string(types.TEST_ANOVA_WELCH),
			Tier:        2,
			Variant:     "welch_one_way_post",
			Description: "Welch one-way ANOVA on per-group summary statistics materialized by upstream aggregators.",
			Streamable:  false,
			Params: []descriptor.Param{
				alphaParam,
				{Name: "n_col", Type: "string", Required: true, Description: "Result-row column holding each group's size n."},
				{Name: "variance_col", Type: "string", Required: true, Description: "Result-row column holding each group's sample variance."},
			},
			Requires: []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_BROWN_FORSYTHE) + "/median_post",
			Family:      string(types.TEST_BROWN_FORSYTHE),
			Tier:        2,
			Variant:     "median_post",
			Description: "Brown-Forsythe homogeneity-of-variance test on output columns; absolute deviations from medians.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "SplitBy"},
		},
		{
			Name:        string(types.TEST_SHAPIRO_WILK) + "/shapiro_francia_post",
			Family:      string(types.TEST_SHAPIRO_WILK),
			Tier:        2,
			Variant:     "shapiro_francia_post",
			Description: "Shapiro-Francia normality test on an output column (Shapiro-Wilk variant for the post-row set).",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field"},
		},
		{
			Name:        string(types.TEST_KS) + "/two_sample_post",
			Family:      string(types.TEST_KS),
			Tier:        2,
			Variant:     "two_sample_post",
			Description: "Kolmogorov-Smirnov two-sample test between two output columns of the result row set.",
			Streamable:  false,
			Params:      []descriptor.Param{alphaParam},
			Requires:    []string{"Field", "Field2"},
		},
	})
}
