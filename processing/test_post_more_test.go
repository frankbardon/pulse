package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// ---------- Tier-2 TEST_PAIRED_T ----------

// TestPairedTPost_KnownDiff: constant lift of 2.0 per row → mean_diff = 2,
// variance = 0 (every diff is 2.0) actually surfaces zero-variance.
// Use slightly noisy lift so the test exercises the happy path.
func TestPairedTPost_KnownDiff(t *testing.T) {
	post, err := newPairedTPost(&types.Test{
		Type: types.TEST_PAIRED_T, Field: "post", Field2: "pre", Alpha: 0.05,
	}, nil)
	if err != nil {
		t.Fatalf("newPairedTPost: %v", err)
	}
	rows := []map[string]any{
		{"pre": 10.0, "post": 12.1},
		{"pre": 11.0, "post": 13.2},
		{"pre": 12.0, "post": 14.0},
		{"pre": 13.0, "post": 14.9},
		{"pre": 14.0, "post": 16.1},
		{"pre": 15.0, "post": 16.8},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "paired_two_sided_post" {
		t.Errorf("variant = %q, want paired_two_sided_post", res.Variant)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false; want true (p=%g, mean_diff=%v)", res.PValue, res.Details["mean_diff"])
	}
	if md, ok := res.Details["mean_diff"].(float64); !ok || md < 1.8 || md > 2.2 {
		t.Errorf("mean_diff = %v, want ≈ 2.0", res.Details["mean_diff"])
	}
}

func TestPairedTPost_InsufficientN(t *testing.T) {
	post, _ := newPairedTPost(&types.Test{
		Type: types.TEST_PAIRED_T, Field: "x", Field2: "y",
	}, nil)
	_, err := post.Run([]map[string]any{{"x": 1.0, "y": 2.0}})
	if err == nil {
		t.Fatal("expected error for n < 2")
	}
}

func TestPairedTPost_ZeroVariance(t *testing.T) {
	post, _ := newPairedTPost(&types.Test{
		Type: types.TEST_PAIRED_T, Field: "x", Field2: "y",
	}, nil)
	_, err := post.Run([]map[string]any{
		{"x": 1.0, "y": 0.0},
		{"x": 2.0, "y": 1.0},
		{"x": 3.0, "y": 2.0},
	})
	if err == nil {
		t.Fatal("expected PULSE_TEST_VARIANCE_ZERO for constant diff")
	}
}

// ---------- Tier-2 TEST_SPEARMAN_R ----------

func TestSpearmanRPost_PerfectMonotone(t *testing.T) {
	post, _ := newSpearmanRPost(&types.Test{
		Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y",
	}, nil)
	// Strictly increasing, non-linear: y = x³ — Pearson would be < 1,
	// Spearman ρ on ranks should be exactly 1.
	rows := []map[string]any{
		{"x": 1.0, "y": 1.0},
		{"x": 2.0, "y": 8.0},
		{"x": 3.0, "y": 27.0},
		{"x": 4.0, "y": 64.0},
		{"x": 5.0, "y": 125.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Statistic-1.0) > 1e-12 {
		t.Errorf("ρ = %g, want 1.0", res.Statistic)
	}
	if res.Variant != "rank_pearson_post" {
		t.Errorf("variant = %q, want rank_pearson_post", res.Variant)
	}
}

func TestSpearmanRPost_AgreesWithRowVariant(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
	}}
	row, _ := newSpearmanRRow(&types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y"}, schema)
	post, _ := newSpearmanRPost(&types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y"}, nil)
	pairs := [][2]float64{{1, 2}, {2, 3.5}, {3, 3.4}, {4, 5}, {5, 6}, {6, 8}, {7, 7.5}}
	rows := make([]map[string]any, 0, len(pairs))
	for _, p := range pairs {
		_ = row.UpdateRow(NewRecord(schema, map[string]float64{"x": p[0], "y": p[1]}))
		rows = append(rows, map[string]any{"x": p[0], "y": p[1]})
	}
	rRow, _ := row.Finalize()
	rPost, _ := post.Run(rows)
	if math.Abs(rRow.Statistic-rPost.Statistic) > 1e-12 {
		t.Errorf("ρ mismatch: row=%g post=%g", rRow.Statistic, rPost.Statistic)
	}
}

// ---------- Tier-2 TEST_KENDALL_TAU ----------

func TestKendallTauPost_PerfectMonotone(t *testing.T) {
	post, _ := newKendallTauPost(&types.Test{
		Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y",
	}, nil)
	rows := []map[string]any{
		{"x": 1.0, "y": 2.0},
		{"x": 2.0, "y": 4.0},
		{"x": 3.0, "y": 9.0},
		{"x": 4.0, "y": 16.0},
		{"x": 5.0, "y": 25.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Statistic-1.0) > 1e-12 {
		t.Errorf("τ = %g, want 1.0", res.Statistic)
	}
	if res.Variant != "tau_b_post" {
		t.Errorf("variant = %q, want tau_b_post", res.Variant)
	}
}

func TestKendallTauPost_RequiresFields(t *testing.T) {
	_, err := newKendallTauPost(&types.Test{Type: types.TEST_KENDALL_TAU, Field: "x"}, nil)
	if err == nil {
		t.Fatal("expected error for missing field2")
	}
}

// ---------- Tier-2 TEST_WILCOXON_SR ----------

func TestWilcoxonSRPost_AllPositiveDiffs(t *testing.T) {
	post, _ := newWilcoxonSRPost(&types.Test{
		Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before",
	}, nil)
	rows := []map[string]any{
		{"before": 10.0, "after": 12.0},
		{"before": 11.0, "after": 14.0},
		{"before": 12.0, "after": 13.5},
		{"before": 13.0, "after": 17.0},
		{"before": 14.0, "after": 19.0},
		{"before": 15.0, "after": 18.0},
		{"before": 16.0, "after": 21.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "asymptotic_post" {
		t.Errorf("variant = %q, want asymptotic_post", res.Variant)
	}
	wPlus, _ := res.Details["w_plus"].(float64)
	wMinus, _ := res.Details["w_minus"].(float64)
	if wMinus != 0 {
		t.Errorf("w_minus = %g, want 0 (all diffs positive)", wMinus)
	}
	if wPlus == 0 {
		t.Errorf("w_plus = 0, want > 0")
	}
}

func TestWilcoxonSRPost_DropsZeroDiffs(t *testing.T) {
	post, _ := newWilcoxonSRPost(&types.Test{
		Type: types.TEST_WILCOXON_SR, Field: "a", Field2: "b",
	}, nil)
	rows := []map[string]any{
		{"a": 1.0, "b": 1.0},
		{"a": 2.0, "b": 3.0},
		{"a": 4.0, "b": 2.0},
		{"a": 5.0, "b": 7.0},
		{"a": 6.0, "b": 4.0},
		{"a": 7.0, "b": 9.0},
		{"a": 8.0, "b": 5.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	zd, _ := res.Details["zero_diffs"].(int)
	if zd != 1 {
		t.Errorf("zero_diffs = %d, want 1", zd)
	}
}

// ---------- Tier-2 TEST_ANOVA_WELCH ----------

func TestAnovaWelchPost_KnownStatistic(t *testing.T) {
	post, err := newAnovaWelchPost(&types.Test{
		Type:    types.TEST_ANOVA_WELCH,
		Field:   "mean_x",
		SplitBy: "group",
		Params:  json.RawMessage(`{"n_col":"n","variance_col":"var_x"}`),
	}, nil)
	if err != nil {
		t.Fatalf("newAnovaWelchPost: %v", err)
	}
	// Three groups, balanced n=10 each, mean separation 0/1/2, σ²≈1.
	// F* should be > 1 and p < 0.05.
	rows := []map[string]any{
		{"group": "a", "mean_x": 0.0, "n": int64(10), "var_x": 1.0},
		{"group": "b", "mean_x": 1.0, "n": int64(10), "var_x": 1.0},
		{"group": "c", "mean_x": 2.0, "n": int64(10), "var_x": 1.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "welch_one_way_post" {
		t.Errorf("variant = %q, want welch_one_way_post", res.Variant)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false (F=%g, p=%g); want true", res.Statistic, res.PValue)
	}
}

func TestAnovaWelchPost_RequiresParams(t *testing.T) {
	_, err := newAnovaWelchPost(&types.Test{
		Type: types.TEST_ANOVA_WELCH, Field: "x", SplitBy: "g",
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing n_col/variance_col")
	}
}

// ---------- Tier-2 TEST_BROWN_FORSYTHE ----------

func TestBrownForsythePost_UnequalVariances(t *testing.T) {
	post, _ := newBrownForsythePost(&types.Test{
		Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g",
	}, nil)
	// Group a: low variance, group b: high variance.
	rows := []map[string]any{
		{"g": "a", "x": 10.0}, {"g": "a", "x": 10.1}, {"g": "a", "x": 10.2},
		{"g": "a", "x": 9.9}, {"g": "a", "x": 10.05}, {"g": "a", "x": 10.0},
		{"g": "b", "x": 5.0}, {"g": "b", "x": 15.0}, {"g": "b", "x": 0.0},
		{"g": "b", "x": 20.0}, {"g": "b", "x": -5.0}, {"g": "b", "x": 25.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "median_post" {
		t.Errorf("variant = %q, want median_post", res.Variant)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false (F=%g, p=%g); want true for clearly unequal variances", res.Statistic, res.PValue)
	}
}

func TestBrownForsythePost_EqualVariances(t *testing.T) {
	post, _ := newBrownForsythePost(&types.Test{
		Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g",
	}, nil)
	rows := []map[string]any{
		{"g": "a", "x": 1.0}, {"g": "a", "x": 2.0}, {"g": "a", "x": 3.0},
		{"g": "a", "x": 4.0}, {"g": "a", "x": 5.0},
		{"g": "b", "x": 10.0}, {"g": "b", "x": 11.0}, {"g": "b", "x": 12.0},
		{"g": "b", "x": 13.0}, {"g": "b", "x": 14.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RejectNull {
		t.Errorf("reject_null = true (F=%g, p=%g); want false for equal variances", res.Statistic, res.PValue)
	}
}

// ---------- Tier-2 TEST_SHAPIRO_WILK ----------

func TestShapiroWilkPost_NormalSample(t *testing.T) {
	post, _ := newShapiroWilkPost(&types.Test{
		Type: types.TEST_SHAPIRO_WILK, Field: "x",
	}, nil)
	// 12 values from a roughly symmetric distribution.
	vs := []float64{-1.5, -1.0, -0.7, -0.3, -0.1, 0.0, 0.2, 0.4, 0.7, 1.0, 1.3, 1.5}
	rows := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		rows = append(rows, map[string]any{"x": v})
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "shapiro_francia_post" {
		t.Errorf("variant = %q, want shapiro_francia_post", res.Variant)
	}
	if res.RejectNull {
		t.Errorf("reject_null = true (W=%g, p=%g); want false for symmetric sample", res.Statistic, res.PValue)
	}
}

func TestShapiroWilkPost_SkewedSample(t *testing.T) {
	post, _ := newShapiroWilkPost(&types.Test{
		Type: types.TEST_SHAPIRO_WILK, Field: "x", Alpha: 0.05,
	}, nil)
	// Strongly right-skewed (exponential-ish): should reject.
	vs := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.8, 1.0, 1.5, 2.5, 5.0, 12.0}
	rows := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		rows = append(rows, map[string]any{"x": v})
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false (W=%g, p=%g); want true for skewed sample", res.Statistic, res.PValue)
	}
}

func TestShapiroWilkPost_InsufficientN(t *testing.T) {
	post, _ := newShapiroWilkPost(&types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, nil)
	_, err := post.Run([]map[string]any{{"x": 1.0}, {"x": 2.0}})
	if err == nil {
		t.Fatal("expected error for n < 3")
	}
}

// ---------- Tier-2 TEST_KS ----------

func TestKSPost_FullySeparated(t *testing.T) {
	post, _ := newKSPost(&types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, nil)
	rows := []map[string]any{
		{"g": "a", "x": 1.0}, {"g": "a", "x": 2.0}, {"g": "a", "x": 3.0},
		{"g": "a", "x": 4.0}, {"g": "a", "x": 5.0},
		{"g": "b", "x": 100.0}, {"g": "b", "x": 200.0}, {"g": "b", "x": 300.0},
		{"g": "b", "x": 400.0}, {"g": "b", "x": 500.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Variant != "two_sample_post" {
		t.Errorf("variant = %q, want two_sample_post", res.Variant)
	}
	if math.Abs(res.Statistic-1.0) > 1e-9 {
		t.Errorf("D = %g, want 1.0 for fully separated samples", res.Statistic)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false; want true for D=1.0")
	}
}

func TestKSPost_IdenticalDistributions(t *testing.T) {
	post, _ := newKSPost(&types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, nil)
	rows := []map[string]any{
		{"g": "a", "x": 1.0}, {"g": "a", "x": 2.0}, {"g": "a", "x": 3.0},
		{"g": "a", "x": 4.0}, {"g": "a", "x": 5.0},
		{"g": "b", "x": 1.0}, {"g": "b", "x": 2.0}, {"g": "b", "x": 3.0},
		{"g": "b", "x": 4.0}, {"g": "b", "x": 5.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.RejectNull {
		t.Errorf("reject_null = true (D=%g, p=%g); want false for identical samples", res.Statistic, res.PValue)
	}
}

func TestKSPost_TooManyGroups(t *testing.T) {
	post, _ := newKSPost(&types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, nil)
	rows := []map[string]any{
		{"g": "a", "x": 1.0}, {"g": "a", "x": 2.0},
		{"g": "b", "x": 1.0}, {"g": "b", "x": 2.0},
		{"g": "c", "x": 1.0}, {"g": "c", "x": 2.0},
	}
	_, err := post.Run(rows)
	if err == nil {
		t.Fatal("expected error for > 2 groups")
	}
}
