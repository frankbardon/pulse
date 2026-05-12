// Annotate-examples rewrites every request example JSON under examples/
// to carry a structured _meta block in place of the legacy _comment
// field. Run once from the repo root:
//
//	go run ./cmd/annotate-examples
//
// The tool is idempotent: re-running against already-annotated files
// is a no-op so long as the in-source annotations table is in sync.
// Operators on each annotation are checked against the file body; the
// run fails fast if the declared operator list is wrong, so the table
// can never drift silently.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type annotation struct {
	File        string // path relative to examples/
	Name        string
	Tags        []string
	Description string
}

// Hand-curated taxonomy assignments per example. Topical tags need
// human judgment; operators are auto-derived from the request body.
var annotations = []annotation{
	// aggregations
	{File: "aggregations/01_decimal_sum.json", Name: "decimal_sum", Tags: []string{"financial", "streaming-friendly"}, Description: "Decimal-aware SUM, AVERAGE, MIN, and MAX on a money column with banker-rounded precision propagation."},
	{File: "aggregations/02_decimal_variance.json", Name: "decimal_variance", Tags: []string{"financial", "distribution-shape", "buffered-pipeline"}, Description: "Population variance and standard deviation computed entirely in decimal128 arithmetic, falling back to f64 only on intermediate overflow."},
	{File: "aggregations/03_geo_centroid_bbox.json", Name: "geo_centroid_bbox", Tags: []string{"geo-analysis", "buffered-pipeline"}, Description: "Compute the unit-sphere centroid and bounding box of a point_f64 cohort, rejecting input sets that straddle the antimeridian."},
	{File: "aggregations/04_all_narrow_types.json", Name: "all_narrow_types", Tags: []string{"data-quality", "streaming-friendly"}, Description: "Exercise every narrow numeric type (u8 through f64 plus all three nullable variants) in one request via per-column SUM and AVERAGE."},
	{File: "aggregations/05_categorical_breakdown.json", Name: "categorical_breakdown", Tags: []string{"cardinality-analysis", "cross-tabulation", "streaming-friendly"}, Description: "Run DISTINCT_COUNT and MODE across categorical_u8/u16/u32 widths and a date column to exercise every dictionary lookup path."},

	// attributes
	{File: "attributes/01_zscore.json", Name: "zscore", Tags: []string{"outlier-detection", "distribution-shape", "buffered-pipeline"}, Description: "Compute per-record z-scores on income and summarize the distribution of the standardized values."},
	{File: "attributes/02_tscore.json", Name: "tscore", Tags: []string{"distribution-shape", "buffered-pipeline"}, Description: "Convert age to a t-score (z*10 + 50) so the output centers at 50 with stddev 10."},
	{File: "attributes/03_normalized.json", Name: "normalized_min_max", Tags: []string{"feature-engineering", "buffered-pipeline"}, Description: "Min-max normalize income to [0, 1] for visualization or downstream model input."},
	{File: "attributes/04_percentile.json", Name: "attr_percentile_by_region", Tags: []string{"cohort-analysis", "distribution-shape", "buffered-pipeline"}, Description: "Replace each income with its percentile rank, then average those ranks within each region for cohort-relative scoring."},
	{File: "attributes/05_formula.json", Name: "attr_formula_monthly", Tags: []string{"feature-engineering", "streaming-friendly"}, Description: "Derive monthly income via the expr-lang formula engine and aggregate the resulting per-record value."},
	{File: "attributes/06_date_part.json", Name: "attr_date_part_year_month", Tags: []string{"time-series", "feature-engineering", "streaming-friendly"}, Description: "Extract a YYYYMM key from order_date and group on it to drive a monthly revenue trend."},

	// features
	{File: "features/01_log_transform.json", Name: "feat_log_transform", Tags: []string{"feature-engineering", "distribution-shape", "pre-filter", "streaming-friendly"}, Description: "Log-transform a skewed numeric column and summarize the resulting distribution."},
	{File: "features/02_bucketize_quantile.json", Name: "feat_bucketize_quantile", Tags: []string{"feature-engineering", "pre-filter", "feature-pipeline"}, Description: "Discretize income into ten equal-frequency buckets and count rows per decile."},
	{File: "features/03_bucketize_explicit.json", Name: "feat_bucketize_explicit", Tags: []string{"feature-engineering", "pre-filter"}, Description: "Discretize age into four explicit life-stage buckets and aggregate income per stage."},
	{File: "features/04_one_hot_filter.json", Name: "feat_one_hot_and_filter", Tags: []string{"feature-engineering", "pre-filter", "feature-pipeline"}, Description: "Expand a categorical region column into one-hot indicators, then filter on a derived indicator before counting survivors."},
	{File: "features/05_date_features_seasonality.json", Name: "feat_date_features_seasonality", Tags: []string{"feature-engineering", "time-series", "pre-filter"}, Description: "Decompose order_date into calendar features and group by quarter to surface seasonality."},
	{File: "features/06_frequency_encode.json", Name: "feat_frequency_encode_city", Tags: []string{"feature-engineering", "cardinality-analysis", "pre-filter"}, Description: "Replace a high-cardinality categorical with its empirical frequency so it can feed a numeric model without column blow-up."},
	{File: "features/07_train_test_split.json", Name: "feat_train_test_split_stratified", Tags: []string{"feature-engineering", "feature-pipeline", "leakage-safe", "pre-filter"}, Description: "Apply a stratified 70/15/15 train/val/test split and verify partition sizes via group counts."},
	{File: "features/08_target_encode_safe.json", Name: "feat_target_encode_safe", Tags: []string{"feature-engineering", "feature-pipeline", "leakage-safe", "pre-filter"}, Description: "Leakage-safe target encoding that runs FEAT_TRAIN_TEST_SPLIT before FEAT_TARGET_ENCODE so the leakage gate stays silent."},
	{File: "features/09_target_encode_leaky.json", Name: "feat_target_encode_leaky", Tags: []string{"feature-engineering", "leakage-risk", "pre-filter"}, Description: "Intentional leakage example: target-encode without a preceding train/test split so PULSE_FEAT_TARGET_LEAKAGE_RISK fires."},
	{File: "features/10_full_ml_pipeline.json", Name: "feat_full_ml_pipeline", Tags: []string{"feature-engineering", "feature-pipeline", "leakage-safe", "pre-filter"}, Description: "End-to-end ML preprocessing in one request: stratified split, log transform, bucketize, one-hot, date features, and target encode, then aggregate label rate by income decile."},

	// filterers
	{File: "filterers/01_include.json", Name: "filter_include_regions", Tags: []string{"comparison", "streaming-friendly"}, Description: "Keep only customers in the north or south regions, then count survivors by region."},
	{File: "filterers/02_exclude.json", Name: "filter_exclude_occupations", Tags: []string{"comparison", "streaming-friendly"}, Description: "Drop driver and artist rows, then count the remaining occupations."},
	{File: "filterers/03_range.json", Name: "filter_range_age", Tags: []string{"cohort-analysis", "streaming-friendly"}, Description: "Numeric range filter keeping rows whose age falls between 25 and 55 inclusive, then summarize the kept cohort."},
	{File: "filterers/04_expression.json", Name: "filter_expression_compound", Tags: []string{"cohort-analysis", "streaming-friendly"}, Description: "Compound expr-lang predicate keeping high-income northerners only, with categorical fields auto-resolving to their dictionary strings."},
	{File: "filterers/05_geo_within.json", Name: "filter_geo_within_polygon", Tags: []string{"geo-analysis", "streaming-friendly"}, Description: "Spatial containment filter retaining rows whose location falls inside a North American bounding polygon."},
	{File: "filterers/06_geo_within_radius.json", Name: "filter_geo_within_radius", Tags: []string{"geo-analysis", "streaming-friendly"}, Description: "Distance filter retaining rows within 50 km of San Francisco using a WKT anchor and a radius in meters."},

	// groupers
	{File: "groupers/01_category.json", Name: "group_category_region", Tags: []string{"cohort-analysis", "streaming-friendly"}, Description: "Partition customers by region and aggregate count and average income per partition."},
	{File: "groupers/02_date.json", Name: "group_date_month", Tags: []string{"time-series", "streaming-friendly"}, Description: "Bucket orders by ISO month using GROUP_DATE and aggregate count and revenue per bucket."},
	{File: "groupers/03_quantile.json", Name: "group_quantile_income", Tags: []string{"cohort-analysis", "distribution-shape", "buffered-pipeline"}, Description: "Group income into quartiles and report count, mean, min, and max per quartile."},
	{File: "groupers/04_range.json", Name: "group_range_income", Tags: []string{"cohort-analysis", "streaming-friendly"}, Description: "Bucket income into 25 000-wide ranges and aggregate per bucket."},
	{File: "groupers/05_rounded.json", Name: "group_rounded_decade", Tags: []string{"cohort-analysis", "streaming-friendly"}, Description: "Bucket age into decades via floor(age/10)*10 and report count and average income per decade."},
	{File: "groupers/06_h3_cell_from_point.json", Name: "group_h3_cell_from_point", Tags: []string{"geo-analysis", "streaming-friendly"}, Description: "Group rows into H3 cells at resolution 5, converting each point_f64 to its cell on the fly."},
	{File: "groupers/07_h3_cell_native.json", Name: "group_h3_cell_native", Tags: []string{"geo-analysis", "streaming-friendly"}, Description: "Group by an h3_cell field at its native resolution with no extra projection."},

	// tests
	{File: "tests/01_t_test_one_sample.json", Name: "t_test_one_sample", Tags: []string{"hypothesis-test", "t-test", "tier-1-test", "parametric", "one-sample", "streaming-friendly"}, Description: "One-sample t-test comparing revenue mean against the hypothesized mu=100."},
	{File: "tests/02_t_test_two_sample.json", Name: "t_test_two_sample", Tags: []string{"hypothesis-test", "t-test", "tier-1-test", "parametric", "two-sample", "experiment-analysis", "comparison", "streaming-friendly"}, Description: "Two-sample Welch t-test on revenue split by treatment arm, detecting the planted variant lift."},
	{File: "tests/03_welch_alias.json", Name: "welch_alias_two_sample", Tags: []string{"hypothesis-test", "t-test", "tier-1-test", "parametric", "two-sample", "experiment-analysis", "streaming-friendly"}, Description: "Two-sample Welch t-test invoked through the TEST_WELCH alias to make the unequal-variance assumption explicit."},
	{File: "tests/04_chi_square_independence.json", Name: "chi_square_independence", Tags: []string{"hypothesis-test", "tier-1-test", "cross-tabulation", "proportion-analysis", "streaming-friendly"}, Description: "Chi-square independence tests on segment-by-conversion and region-by-conversion two-way tables."},
	{File: "tests/05_anova_f_streaming.json", Name: "anova_f_streaming", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "k-sample", "experiment-analysis", "comparison", "streaming-friendly"}, Description: "One-way streaming ANOVA on revenue across regions to detect planted between-region differences."},
	{File: "tests/06_ks_two_sample.json", Name: "ks_two_sample", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "two-sample", "distribution-shape", "experiment-analysis", "buffered-pipeline"}, Description: "Two-sample Kolmogorov-Smirnov on session minutes between treatment arms to detect distribution-shape shifts."},
	{File: "tests/07_combined_battery.json", Name: "combined_test_battery", Tags: []string{"hypothesis-test", "tier-1-test", "experiment-analysis", "comparison", "streaming-friendly"}, Description: "Standard A/B-test battery: Welch t on revenue, ANOVA across regions, and chi-square on segment-by-conversion in one Process call."},
	{File: "tests/08_post_anova_on_grouped_means.json", Name: "post_anova_on_grouped_means", Tags: []string{"hypothesis-test", "tier-2-test", "parametric", "k-sample", "post-hoc", "comparison", "buffered-pipeline"}, Description: "Tier-2 ANOVA reconstructed from per-region summary stats (mean, n, variance) produced by an upstream GROUP_CATEGORY aggregation."},
	{File: "tests/09_post_trend_on_dates.json", Name: "post_trend_on_dates", Tags: []string{"hypothesis-test", "tier-2-test", "nonparametric", "trend-detection", "time-series", "buffered-pipeline"}, Description: "Tier-2 Mann-Kendall trend test on daily revenue means bucketed by GROUP_DATE."},
	{File: "tests/10_post_trend_on_window.json", Name: "post_trend_on_window", Tags: []string{"hypothesis-test", "tier-2-test", "nonparametric", "trend-detection", "time-series", "window-operator", "buffered-pipeline"}, Description: "Tier-2 Mann-Kendall trend test applied to a 7-day windowed moving average of daily revenue."},
	{File: "tests/11_pearson_correlation.json", Name: "pearson_correlation", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "correlation-analysis", "streaming-friendly"}, Description: "Pearson r between post-treatment revenue and the pre-treatment baseline, computed streaming via an extended Welford recurrence."},
	{File: "tests/12_paired_t_before_after.json", Name: "paired_t_before_after", Tags: []string{"hypothesis-test", "t-test", "tier-1-test", "parametric", "paired", "before-after", "experiment-analysis", "streaming-friendly"}, Description: "Paired t-test on the per-unit revenue lift (revenue vs revenue_before) to detect the average treatment effect."},
	{File: "tests/13_two_proportion_z.json", Name: "two_proportion_z", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "proportion-analysis", "two-sample", "experiment-analysis", "streaming-friendly"}, Description: "Two-proportion z-test on conversion rate by treatment, using params.success to name the positive outcome."},
	{File: "tests/14_parametric_battery.json", Name: "parametric_battery", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "experiment-analysis", "streaming-friendly"}, Description: "Standard parametric A/B battery: Welch t on revenue, paired t on per-unit lift, two-proportion z on conversion, and Pearson r on revenue versus session minutes."},
	{File: "tests/15_mann_whitney_revenue.json", Name: "mann_whitney_revenue", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "two-sample", "experiment-analysis", "comparison", "buffered-pipeline"}, Description: "Mann-Whitney U on revenue by treatment as a nonparametric counterpart to the two-sample t-test."},
	{File: "tests/16_kruskal_wallis_regions.json", Name: "kruskal_wallis_regions", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "k-sample", "experiment-analysis", "buffered-pipeline"}, Description: "Kruskal-Wallis H on revenue across four regions as a nonparametric counterpart to one-way ANOVA."},
	{File: "tests/17_wilcoxon_paired.json", Name: "wilcoxon_paired", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "paired", "before-after", "experiment-analysis", "buffered-pipeline"}, Description: "Wilcoxon signed-rank on the revenue pre/post pair as a nonparametric counterpart to the paired t-test."},
	{File: "tests/18_spearman_correlation.json", Name: "spearman_correlation", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "correlation-analysis", "buffered-pipeline"}, Description: "Spearman rho between revenue and its pre-treatment baseline, robust to non-linear monotone relationships."},
	{File: "tests/19_kendall_tau.json", Name: "kendall_tau", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "correlation-analysis", "buffered-pipeline"}, Description: "Kendall tau-b between revenue and its pre-treatment baseline as a concordance-based alternative to Spearman rho."},
	{File: "tests/20_nonparametric_battery.json", Name: "nonparametric_battery", Tags: []string{"hypothesis-test", "tier-1-test", "nonparametric", "experiment-analysis", "buffered-pipeline"}, Description: "Standard nonparametric battery: Mann-Whitney, Kruskal-Wallis, Wilcoxon signed-rank, Spearman, and Kendall over the experiment cohort."},
	{File: "tests/21_welch_anova_unequal_var.json", Name: "welch_anova_unequal_var", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "k-sample", "experiment-analysis", "streaming-friendly"}, Description: "Welch ANOVA on revenue across regions to handle heteroscedasticity without inflating type-I error."},
	{File: "tests/22_brown_forsythe_homogeneity.json", Name: "brown_forsythe_homogeneity", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "homogeneity-test", "k-sample", "buffered-pipeline"}, Description: "Brown-Forsythe variance-homogeneity check on revenue across regions; pair with Welch ANOVA when it rejects."},
	{File: "tests/23_rm_anova_three_conditions.json", Name: "rm_anova_three_conditions", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "repeated-measures", "k-sample", "before-after", "buffered-pipeline"}, Description: "One-way repeated-measures ANOVA on a 40-subject by 3-condition design where the subject effect dominates the residual."},
	{File: "tests/24_anova_robustness_battery.json", Name: "anova_robustness_battery", Tags: []string{"hypothesis-test", "tier-1-test", "parametric", "k-sample", "homogeneity-test", "experiment-analysis", "buffered-pipeline"}, Description: "ANOVA robustness battery: equal-variance F, Welch's F, and Brown-Forsythe on the same revenue-by-region split."},
	{File: "tests/25_tukey_hsd_after_anova.json", Name: "tukey_hsd_after_anova", Tags: []string{"hypothesis-test", "tier-2-test", "parametric", "post-hoc", "k-sample", "experiment-analysis", "buffered-pipeline"}, Description: "Tier-2 Tukey HSD pairwise comparisons over per-region means after a tier-1 one-way ANOVA on the same Process call."},
	{File: "tests/26_fisher_exact_small_sample.json", Name: "fisher_exact_small_sample", Tags: []string{"hypothesis-test", "tier-1-test", "exact-test", "small-sample", "cross-tabulation", "proportion-analysis", "buffered-pipeline"}, Description: "Fisher exact test alongside chi-square on a 2x2 contingency table that has been filtered down to a small subgroup."},
	{File: "tests/27_shapiro_wilk_normality.json", Name: "shapiro_wilk_normality", Tags: []string{"hypothesis-test", "tier-1-test", "normality-test", "distribution-shape", "experiment-analysis", "buffered-pipeline"}, Description: "Shapiro-Wilk (Shapiro-Francia variant) normality check on revenue split by treatment, used as a pre-flight gate for parametric tests."},

	// windows
	{File: "windows/01_lag.json", Name: "win_lag", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Look back one row in the ordered partition to surface the previous transaction amount."},
	{File: "windows/02_lead.json", Name: "win_lead", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Look ahead two rows in the ordered partition so each row sees the value two positions later."},
	{File: "windows/03_row_number.json", Name: "win_row_number", Tags: []string{"top-n", "window-operator", "buffered-pipeline"}, Description: "Emit a 1-based row number after sorting by amount descending so callers can pluck the top N."},
	{File: "windows/04_rank.json", Name: "win_rank_in_region", Tags: []string{"cohort-analysis", "top-n", "window-operator", "buffered-pipeline"}, Description: "Rank customers by income within each region with gap-on-ties semantics (1, 2, 2, 4, ...)."},
	{File: "windows/05_dense_rank.json", Name: "win_dense_rank_in_region", Tags: []string{"cohort-analysis", "top-n", "window-operator", "buffered-pipeline"}, Description: "Dense-rank customers by age within each region with no gap on ties (1, 2, 2, 3, ...)."},
	{File: "windows/06_running_sum.json", Name: "win_running_sum", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Cumulative revenue ordered by date using an unbounded-preceding to current-row frame."},
	{File: "windows/07_running_avg.json", Name: "win_running_avg", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Running mean of revenue across the partition under an unbounded-preceding to current-row frame."},
	{File: "windows/08_moving_avg.json", Name: "win_moving_avg_7day", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Centered 7-day moving average of revenue (3 preceding plus current plus 3 following) over an ordered date partition."},
	{File: "windows/09_ewma.json", Name: "win_ewma", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Exponentially weighted moving average of revenue with alpha=0.3 trading smoothness against lag."},
	{File: "windows/10_pct_change.json", Name: "win_pct_change", Tags: []string{"time-series", "window-operator", "buffered-pipeline"}, Description: "Percent change versus the row one period earlier in the date-ordered partition."},
}

// operatorRe captures uppercase prefixed type strings inside each
// request body. The expression is anchored on the JSON `"type": "..."`
// shape so unrelated string fields cannot match.
var operatorRe = regexp.MustCompile(`"type"\s*:\s*"((?:AGG|ATTR|FILTER|GROUP|WIN|FEAT|TEST)_[A-Z0-9_]+)"`)

func main() {
	root := "examples"
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	annByFile := make(map[string]annotation, len(annotations))
	for _, a := range annotations {
		annByFile[a.File] = a
	}
	failed := false
	visited := make(map[string]bool, len(annotations))

	walk := func() error {
		return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				switch filepath.Base(p) {
				case "fixtures", "synth":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(p, ".json") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			ann, ok := annByFile[rel]
			if !ok {
				fmt.Fprintf(os.Stderr, "skip (no annotation): %s\n", rel)
				return nil
			}
			visited[rel] = true
			if err := rewriteFile(p, ann); err != nil {
				fmt.Fprintf(os.Stderr, "FAIL %s: %v\n", rel, err)
				failed = true
			}
			return nil
		})
	}
	if err := walk(); err != nil {
		fmt.Fprintf(os.Stderr, "walk: %v\n", err)
		os.Exit(2)
	}
	for f := range annByFile {
		if !visited[f] {
			fmt.Fprintf(os.Stderr, "annotation references missing file: %s\n", f)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func rewriteFile(path string, ann annotation) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Parse the body so we can scan for operators and drop _comment.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	delete(raw, "_comment")
	delete(raw, "_meta")

	// Derive operators from the body text directly so synthetic fields
	// inside attributes / aggregations / features get scanned without
	// having to mirror the request type system.
	bodyBytes, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("re-marshal body: %w", err)
	}
	operators := deriveOperators(bodyBytes)
	if len(operators) == 0 {
		return fmt.Errorf("no operators derived from body — check the regex / file")
	}

	category := strings.Split(filepath.ToSlash(filepath.Dir(path)), "/")
	cat := category[len(category)-1]

	// Build the _meta block as the canonical first-key entry by writing
	// the output ourselves: JSON marshaling for map is alphabetical, so
	// we serialize meta first then the rest of the body in a single
	// composed buffer.
	metaPayload := map[string]any{
		"name":        ann.Name,
		"category":    cat,
		"tags":        ann.Tags,
		"operators":   operators,
		"description": ann.Description,
	}
	metaBytes, err := json.MarshalIndent(metaPayload, "  ", "  ")
	if err != nil {
		return fmt.Errorf("encode _meta: %w", err)
	}

	// Re-marshal the rest of the body with two-space indent. The map
	// iteration order is alphabetical inside encoding/json so output is
	// deterministic.
	rest, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("encode body: %w", err)
	}

	out := composeJSON(metaBytes, rest)
	return os.WriteFile(path, out, 0o644)
}

// deriveOperators scans bodyBytes for `"type": "PREFIX_..."` matches,
// dedupes, and returns the sorted list.
func deriveOperators(body []byte) []string {
	matches := operatorRe.FindAllStringSubmatch(string(body), -1)
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		seen[m[1]] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for op := range seen {
		out = append(out, op)
	}
	sort.Strings(out)
	return out
}

// composeJSON splices the _meta block into a body that was marshaled as
// {"...":...,"...":...}. We strip the body's opening `{`, prepend our
// meta entry, and re-attach. Adds a comma after _meta unless the body
// is empty.
func composeJSON(meta, body []byte) []byte {
	meta = bytes.TrimSpace(meta)
	body = bytes.TrimSpace(body)
	var buf bytes.Buffer
	buf.WriteString("{\n  \"_meta\": ")
	buf.Write(meta)
	// Open the body content. body looks like `{` ... `}`. Strip the
	// outer braces to splice contents.
	inner := body
	if len(inner) >= 2 && inner[0] == '{' && inner[len(inner)-1] == '}' {
		inner = inner[1 : len(inner)-1]
	}
	if bytes.TrimSpace(inner) != nil && len(bytes.TrimSpace(inner)) > 0 {
		buf.WriteString(",\n")
		// inner is indented two spaces already from MarshalIndent; trim
		// leading whitespace on the first line and keep the rest as-is.
		inner = bytes.TrimLeft(inner, "\n")
		buf.Write(inner)
		buf.WriteString("\n}\n")
	} else {
		buf.WriteString("\n}\n")
	}
	return mustValid(buf.Bytes())
}

// mustValid sanity-checks the assembled JSON and panics with the
// offending output if it doesn't parse — that means the splice routine
// has a bug that would corrupt the example library.
func mustValid(out []byte) []byte {
	var v any
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&v); err != nil {
		fmt.Fprintln(os.Stderr, "INVALID JSON PRODUCED:")
		_, _ = io.Copy(os.Stderr, bytes.NewReader(out))
		panic("annotate-examples produced invalid JSON: " + err.Error())
	}
	return out
}
