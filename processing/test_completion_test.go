package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestFisherExact_KnownPValue verifies a hand-computed Fisher exact
// p-value on the classic teaching example:
//
//	         coffee  tea  total
//	men        1     9   10
//	women      11    3   14
//	total      12    12   24
//
// scipy.stats.fisher_exact returns p ≈ 0.0027 (two-sided).
func TestFisherExact_KnownPValue(t *testing.T) {
	schema := chiSquareFixtureSchema() // reuses kind × outcome fixture
	spec := &types.Test{
		Type:  types.TEST_FISHER_EXACT,
		Rows:  "kind",
		Cols:  "outcome",
		Alpha: 0.05,
	}
	test, err := newFisherExactRow(spec, schema)
	if err != nil {
		t.Fatalf("newFisherExactRow: %v", err)
	}
	feed := func(k, o string, count int) {
		for range count {
			r := chiSquareTestRecord(t, schema, k, o)
			_ = test.UpdateRow(r)
		}
	}
	feed("type1", "A", 1)
	feed("type1", "B", 9)
	feed("type2", "A", 11)
	feed("type2", "B", 3)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.PValue-0.0027) > 0.001 {
		t.Errorf("PValue: got %g, want ~0.0027", res.PValue)
	}
	if !res.RejectNull {
		t.Errorf("RejectNull should be true")
	}
}

// TestFisherExact_Rejects4x2 verifies the r>2 guard.
func TestFisherExact_RejectsLargerTable(t *testing.T) {
	schema := chiSquareFixtureSchema()
	spec := &types.Test{
		Type:  types.TEST_FISHER_EXACT,
		Rows:  "kind",
		Cols:  "outcome",
		Alpha: 0.05,
	}
	test, _ := newFisherExactRow(spec, schema)
	feed := func(k, o string, count int) {
		for range count {
			r := chiSquareTestRecord(t, schema, k, o)
			_ = test.UpdateRow(r)
		}
	}
	feed("type1", "A", 1)
	feed("type1", "B", 1)
	feed("type2", "A", 1)
	feed("type2", "B", 1)
	feed("type3", "A", 1)
	feed("type3", "B", 1)
	if _, err := test.Finalize(); err == nil {
		t.Fatalf("expected CONTINGENCY_DEGENERATE for 3×2 table")
	}
}

// TestShapiroWilk_AcceptsNormal verifies the test does not reject on
// a clearly normal sample (n=20 from a fixed grid).
func TestShapiroWilk_AcceptsNormal(t *testing.T) {
	schema := numericFixtureSchema("metric")
	spec := &types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "metric", Alpha: 0.05}
	test, err := newShapiroWilkRow(spec, schema)
	if err != nil {
		t.Fatalf("newShapiroWilkRow: %v", err)
	}
	// Symmetric near-normal sample: equally spaced quantiles of N(0,1).
	vs := []float64{-1.8, -1.4, -1.0, -0.7, -0.5, -0.3, -0.15, 0, 0.15, 0.3,
		0.5, 0.7, 1.0, 1.4, 1.8, -1.0, 0, 1.0, -0.5, 0.5}
	for _, v := range vs {
		r := NewRecord(schema, map[string]float64{"metric": v})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.RejectNull {
		t.Errorf("Shapiro-Wilk rejected a near-normal sample at alpha=0.05; p=%g", res.PValue)
	}
}

// TestShapiroWilk_RejectsSkewed verifies the test rejects a clearly
// non-normal (heavy right-tail exponential) sample.
func TestShapiroWilk_RejectsSkewed(t *testing.T) {
	schema := numericFixtureSchema("metric")
	spec := &types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "metric", Alpha: 0.05}
	test, _ := newShapiroWilkRow(spec, schema)
	// 30 values from a hand-built right-skewed (exponential-shaped)
	// sample so Shapiro-Francia z lands clearly above the 0.05 critical
	// value.
	vs := []float64{0.05, 0.1, 0.12, 0.18, 0.22, 0.25, 0.28, 0.32, 0.35, 0.4,
		0.45, 0.5, 0.55, 0.62, 0.7, 0.82, 0.95, 1.1, 1.3, 1.6,
		2.0, 2.5, 3.2, 4.1, 5.3, 7.0, 9.5, 13, 18, 25}
	for _, v := range vs {
		r := NewRecord(schema, map[string]float64{"metric": v})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if !res.RejectNull {
		t.Errorf("Shapiro-Wilk failed to reject a right-skewed sample; W=%g, p=%g", res.Statistic, res.PValue)
	}
}

// TestTukeyHSD_KnownStatistic verifies hand-computed Tukey HSD on a
// 3-group case. Per-group means (10, 15, 20) with n=10 each;
// ms_within = 4, df_within = 27. The studentized-range q for the
// (group_a, group_c) pair: q = |10-20| / sqrt(4 · (1/10+1/10) / 2) =
// 10 / 0.632 ≈ 15.81. p_adj should be < 0.001. The middle pair
// (group_b vs anything) has q = 5 / 0.632 ≈ 7.9 also < 0.001.
func TestTukeyHSD_KnownStatistic(t *testing.T) {
	params, _ := json.Marshal(map[string]any{"ms_within": 4.0, "df_within": 27.0})
	spec := &types.Test{
		Type:    types.TEST_TUKEY_HSD,
		Field:   "mean",
		SplitBy: "group",
		Alpha:   0.05,
		Params:  params,
	}
	test, err := newTukeyHSDPost(spec, nil)
	if err != nil {
		t.Fatalf("newTukeyHSDPost: %v", err)
	}
	rows := []map[string]any{
		{"group": "a", "mean": 10.0, "n": 10.0},
		{"group": "b", "mean": 15.0, "n": 10.0},
		{"group": "c", "mean": 20.0, "n": 10.0},
	}
	res, err := test.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	comparisons := res.Details["comparisons"].([]map[string]any)
	if len(comparisons) != 3 {
		t.Fatalf("expected 3 pairwise comparisons, got %d", len(comparisons))
	}
	for _, c := range comparisons {
		if got := c["p_adj"].(float64); got > 0.01 {
			t.Errorf("p_adj for %v vs %v: %g, want < 0.01", c["a"], c["b"], got)
		}
		if !c["reject_null"].(bool) {
			t.Errorf("expected reject_null for %v vs %v", c["a"], c["b"])
		}
	}
}

// TestTukeyHSD_RejectsKLT3 verifies the k<3 guard.
func TestTukeyHSD_RejectsKLT3(t *testing.T) {
	params, _ := json.Marshal(map[string]any{"ms_within": 4.0, "df_within": 10.0})
	spec := &types.Test{
		Type:    types.TEST_TUKEY_HSD,
		Field:   "mean",
		SplitBy: "group",
		Alpha:   0.05,
		Params:  params,
	}
	test, _ := newTukeyHSDPost(spec, nil)
	rows := []map[string]any{
		{"group": "a", "mean": 10.0, "n": 10.0},
		{"group": "b", "mean": 15.0, "n": 10.0},
	}
	if _, err := test.Run(rows); err == nil {
		t.Fatalf("expected SPLIT_GROUPS_LT_2 for k=2")
	}
}

// TestStudentizedRange_NormalRangeCDF verifies known reference values
// for the inner range-of-k-normals CDF. For k=2: R = |Z₂−Z₁|, which is
// a folded normal scaled by √2. The CDF at t = 1.96·√2 ≈ 2.77 should
// be close to P(|Z| ≤ 1.96) = 0.95.
func TestStudentizedRange_NormalRangeCDF(t *testing.T) {
	got := normalRangeCDF(2.77, 2)
	if math.Abs(got-0.95) > 0.01 {
		t.Errorf("normalRangeCDF(2.77, 2) = %g, want ~0.95", got)
	}
}

// TestStudentizedRange_SurvivalMonotone verifies the survival function
// decreases in q for fixed (k, df).
func TestStudentizedRange_SurvivalMonotone(t *testing.T) {
	const k = 4
	const df = 20.0
	prev := studentizedRangeSurvival(0.5, k, df)
	for _, q := range []float64{1, 2, 3, 4, 5, 6} {
		cur := studentizedRangeSurvival(q, k, df)
		if cur > prev {
			t.Errorf("survival not monotone at q=%g: prev=%g, cur=%g", q, prev, cur)
		}
		prev = cur
	}
}
