package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestChiSquare_2x2_Independence verifies a hand-computed reference:
//
//	         A    B   total
//	type1   90   10   100
//	type2   80   20   100
//	total  170   30   200
//
// χ² = (25/85 + 25/15) * 2 = 3.9216, df=1, p ≈ 0.0477.
func TestChiSquare_2x2_Independence(t *testing.T) {
	schema := chiSquareFixtureSchema()
	spec := &types.Test{
		Type:  types.TEST_CHISQ,
		Rows:  "kind",
		Cols:  "outcome",
		Alpha: 0.05,
	}
	cs, err := newChiSqRow(spec, schema)
	if err != nil {
		t.Fatalf("newChiSqRow: %v", err)
	}
	feed := func(kind, outcome string, count int) {
		for range count {
			r := chiSquareTestRecord(t, schema, kind, outcome)
			if err := cs.UpdateRow(r); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	feed("type1", "A", 90)
	feed("type1", "B", 10)
	feed("type2", "A", 80)
	feed("type2", "B", 20)
	res, err := cs.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-3.9216) > 0.01 {
		t.Errorf("statistic = %g, want ~3.9216", res.Statistic)
	}
	if res.DF != 1 {
		t.Errorf("df = %g, want 1", res.DF)
	}
	if math.Abs(res.PValue-0.0477) > 0.01 {
		t.Errorf("p_value = %g, want ~0.0477", res.PValue)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false at alpha 0.05 (p=%g); expected true", res.PValue)
	}
	if res.Details["n"].(int64) != 200 {
		t.Errorf("n = %v, want 200", res.Details["n"])
	}
}

// TestChiSquare_DegenerateContingency rejects a 1x1 table.
func TestChiSquare_DegenerateContingency(t *testing.T) {
	schema := chiSquareFixtureSchema()
	spec := &types.Test{Type: types.TEST_CHISQ, Rows: "kind", Cols: "outcome"}
	cs, _ := newChiSqRow(spec, schema)
	r := chiSquareTestRecord(t, schema, "type1", "A")
	cs.UpdateRow(r)
	_, err := cs.Finalize()
	if err == nil {
		t.Fatal("expected PULSE_TEST_CONTINGENCY_DEGENERATE, got nil")
	}
	if !containsCode(err, "PULSE_TEST_CONTINGENCY_DEGENERATE") {
		t.Errorf("error = %v, want PULSE_TEST_CONTINGENCY_DEGENERATE", err)
	}
}

// TestAnovaF_KnownStatistic verifies a hand-computed reference:
// three groups [3..7], [4..8], [5..9] each n=5 with sample variance 2.5.
// Grand mean = 6, SSB = 10, SSW = 30, MSB/MSW = 5/2.5 = 2.0.
// df_between=2, df_within=12, p ≈ 0.178.
func TestAnovaF_KnownStatistic(t *testing.T) {
	schema := twoSampleFixtureSchema() // reused — revenue + treatment categorical
	spec := &types.Test{
		Type:    types.TEST_ANOVA_F,
		Field:   "revenue",
		SplitBy: "treatment",
		Alpha:   0.05,
	}
	rt, err := newAnovaRow(spec, schema)
	if err != nil {
		t.Fatalf("newAnovaRow: %v", err)
	}
	groupValues := map[string][]float64{
		"g1": {3, 4, 5, 6, 7},
		"g2": {4, 5, 6, 7, 8},
		"g3": {5, 6, 7, 8, 9},
	}
	for _, g := range []string{"g1", "g2", "g3"} {
		for _, v := range groupValues[g] {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			if err := rt.UpdateRow(r); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-2.0) > 1e-9 {
		t.Errorf("statistic = %g, want 2.0", res.Statistic)
	}
	if res.DF != 2 {
		t.Errorf("df_between = %g, want 2", res.DF)
	}
	if math.Abs(res.PValue-0.178) > 0.01 {
		t.Errorf("p_value = %g, want ~0.178", res.PValue)
	}
	if res.RejectNull {
		t.Error("reject_null = true at alpha 0.05; expected false")
	}
	ssb := res.Details["ss_between"].(float64)
	ssw := res.Details["ss_within"].(float64)
	if math.Abs(ssb-10) > 1e-9 || math.Abs(ssw-30) > 1e-9 {
		t.Errorf("SSB/SSW = %g / %g, want 10 / 30", ssb, ssw)
	}
}

// TestAnovaF_LargeEffectRejects: well-separated means → tiny p.
func TestAnovaF_LargeEffectRejects(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_ANOVA_F, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	rt, _ := newAnovaRow(spec, schema)
	for _, gs := range []struct {
		key    string
		values []float64
	}{
		{"low", []float64{1, 1.5, 2, 2.5, 3}},
		{"mid", []float64{10, 10.5, 11, 11.5, 12}},
		{"high", []float64{50, 50.5, 51, 51.5, 52}},
	} {
		for _, v := range gs.values {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", gs.key)
			rt.UpdateRow(r)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.PValue > 1e-12 {
		t.Errorf("p_value = %g, want ~0", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false, want true")
	}
}

// TestKS_FullySeparatedSamples: two disjoint ranges → D=1, p very small.
func TestKS_FullySeparatedSamples(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_KS, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	rt, err := newKSRow(spec, schema)
	if err != nil {
		t.Fatalf("newKSRow: %v", err)
	}
	for _, v := range []float64{1, 2, 3, 4, 5} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "a")
		rt.UpdateRow(r)
	}
	for _, v := range []float64{10, 11, 12, 13, 14} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "b")
		rt.UpdateRow(r)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-1.0) > 1e-9 {
		t.Errorf("statistic = %g, want 1.0", res.Statistic)
	}
	if res.PValue > 0.01 {
		t.Errorf("p_value = %g, want < 0.01", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false, want true")
	}
}

// TestKS_IdenticalDistributions: D close to 0, p close to 1.
func TestKS_IdenticalDistributions(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_KS, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	rt, _ := newKSRow(spec, schema)
	for _, v := range []float64{1, 2, 3, 4, 5} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "a")
		rt.UpdateRow(r)
	}
	for _, v := range []float64{1, 2, 3, 4, 5} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "b")
		rt.UpdateRow(r)
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if res.Statistic > 0.01 {
		t.Errorf("statistic = %g, want ~0", res.Statistic)
	}
	if res.PValue < 0.95 {
		t.Errorf("p_value = %g, want ~1", res.PValue)
	}
	if res.RejectNull {
		t.Error("reject_null = true on identical samples; want false")
	}
}

// TestAnovaPost_MatchesRowAnova: feeding the per-group summary stats
// from the row-test fixture into anovaPost reproduces the same F/p.
func TestAnovaPost_MatchesRowAnova(t *testing.T) {
	post, err := newAnovaPost(&types.Test{
		Type:    types.TEST_ANOVA_F,
		Field:   "mean_x",
		SplitBy: "group",
		Alpha:   0.05,
		Params:  json.RawMessage(`{"n_col": "n", "variance_col": "var_x"}`),
	}, nil)
	if err != nil {
		t.Fatalf("newAnovaPost: %v", err)
	}
	rows := []map[string]any{
		{"group": "g1", "mean_x": 5.0, "n": int64(5), "var_x": 2.5},
		{"group": "g2", "mean_x": 6.0, "n": int64(5), "var_x": 2.5},
		{"group": "g3", "mean_x": 7.0, "n": int64(5), "var_x": 2.5},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Statistic-2.0) > 1e-9 {
		t.Errorf("statistic = %g, want 2.0", res.Statistic)
	}
	if math.Abs(res.PValue-0.178) > 0.01 {
		t.Errorf("p_value = %g, want ~0.178", res.PValue)
	}
	if res.Variant != "one_way_from_summary" {
		t.Errorf("variant = %s, want one_way_from_summary", res.Variant)
	}
}

// TestTrendPost_MonotonicIncreasing: strict monotone series → S = n(n-1)/2.
// For n=8: S = 28, Var(S) = 65.33, Z = 27/sqrt(65.33) ≈ 3.34, p ≈ 0.0008.
func TestTrendPost_MonotonicIncreasing(t *testing.T) {
	post, err := newTrendPost(&types.Test{
		Type:    types.TEST_TREND,
		Field:   "value",
		Alpha:   0.05,
		OrderBy: []types.OrderKey{{Field: "period"}},
	}, nil)
	if err != nil {
		t.Fatalf("newTrendPost: %v", err)
	}
	rows := []map[string]any{
		{"period": float64(1), "value": 1.0},
		{"period": float64(2), "value": 2.0},
		{"period": float64(3), "value": 3.0},
		{"period": float64(4), "value": 4.0},
		{"period": float64(5), "value": 5.0},
		{"period": float64(6), "value": 6.0},
		{"period": float64(7), "value": 7.0},
		{"period": float64(8), "value": 8.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Details["s"].(float64)-28.0) > 1e-9 {
		t.Errorf("S = %g, want 28", res.Details["s"])
	}
	if math.Abs(res.Statistic-3.34) > 0.05 {
		t.Errorf("Z = %g, want ~3.34", res.Statistic)
	}
	if res.PValue > 0.005 {
		t.Errorf("p_value = %g, want < 0.005", res.PValue)
	}
	if !res.RejectNull {
		t.Error("reject_null = false; want true on monotone series")
	}
}

// TestTrendPost_FlatSeries: constant values → S=0, p=1.
func TestTrendPost_FlatSeries(t *testing.T) {
	post, _ := newTrendPost(&types.Test{Type: types.TEST_TREND, Field: "value", Alpha: 0.05}, nil)
	rows := []map[string]any{
		{"value": 5.0}, {"value": 5.0}, {"value": 5.0}, {"value": 5.0},
		{"value": 5.0}, {"value": 5.0}, {"value": 5.0}, {"value": 5.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Details["s"].(float64) != 0 {
		t.Errorf("S = %g, want 0", res.Details["s"])
	}
	if res.RejectNull {
		t.Error("reject_null = true on flat series; want false")
	}
}

// TestTrendPost_OrderingApplied: rows shuffled with explicit OrderBy
// produce the same Z as a pre-sorted input.
func TestTrendPost_OrderingApplied(t *testing.T) {
	post, _ := newTrendPost(&types.Test{
		Type:    types.TEST_TREND,
		Field:   "value",
		Alpha:   0.05,
		OrderBy: []types.OrderKey{{Field: "period"}},
	}, nil)
	rows := []map[string]any{
		{"period": float64(4), "value": 4.0},
		{"period": float64(1), "value": 1.0},
		{"period": float64(7), "value": 7.0},
		{"period": float64(2), "value": 2.0},
		{"period": float64(6), "value": 6.0},
		{"period": float64(3), "value": 3.0},
		{"period": float64(5), "value": 5.0},
		{"period": float64(8), "value": 8.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Details["s"].(float64)-28.0) > 1e-9 {
		t.Errorf("S = %g, want 28 (after OrderBy)", res.Details["s"])
	}
}

func chiSquareFixtureSchema() *encoding.Schema {
	kindDict := encoding.NewDictionary()
	outcomeDict := encoding.NewDictionary()
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "kind", Type: encoding.FieldTypeCategoricalU8, Dictionary: kindDict},
			{Name: "outcome", Type: encoding.FieldTypeCategoricalU8, Dictionary: outcomeDict},
		},
	}
}

func chiSquareTestRecord(t *testing.T, schema *encoding.Schema, kind, outcome string) *Record {
	t.Helper()
	kindID := dictIDOrAdd(schema, "kind", kind)
	outcomeID := dictIDOrAdd(schema, "outcome", outcome)
	return NewRecord(schema, map[string]float64{
		"kind":    float64(kindID),
		"outcome": float64(outcomeID),
	})
}

// TestPearsonRPost_PerfectPositive: y = 2x + 3 over 5 result rows; r must
// be exactly 1 (clamped from float drift). p ≈ 0, df = n − 2.
func TestPearsonRPost_PerfectPositive(t *testing.T) {
	post, err := newPearsonRPost(&types.Test{
		Type:   types.TEST_PEARSON_R,
		Field:  "x_col",
		Field2: "y_col",
		Alpha:  0.05,
	}, nil)
	if err != nil {
		t.Fatalf("newPearsonRPost: %v", err)
	}
	rows := []map[string]any{
		{"x_col": 1.0, "y_col": 5.0},
		{"x_col": 2.0, "y_col": 7.0},
		{"x_col": 3.0, "y_col": 9.0},
		{"x_col": 4.0, "y_col": 11.0},
		{"x_col": 5.0, "y_col": 13.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if math.Abs(res.Statistic-1.0) > 1e-12 {
		t.Errorf("r = %g, want 1.0", res.Statistic)
	}
	if res.PValue != 0 {
		t.Errorf("p = %g, want 0 for r = 1", res.PValue)
	}
	if res.DF != 3 {
		t.Errorf("df = %g, want 3", res.DF)
	}
	if res.Variant != "pearson_post" {
		t.Errorf("variant = %q, want pearson_post", res.Variant)
	}
}

// TestPearsonRPost_NegativeCorrelation: known r ≈ -0.949 on a 5-point
// negatively associated series. Confirms the sign carries through and
// the p-value rejects at α = 0.05.
func TestPearsonRPost_NegativeCorrelation(t *testing.T) {
	post, err := newPearsonRPost(&types.Test{
		Type:   types.TEST_PEARSON_R,
		Field:  "x",
		Field2: "y",
	}, nil)
	if err != nil {
		t.Fatalf("newPearsonRPost: %v", err)
	}
	rows := []map[string]any{
		{"x": 1.0, "y": 10.0},
		{"x": 2.0, "y": 8.0},
		{"x": 3.0, "y": 7.0},
		{"x": 4.0, "y": 4.0},
		{"x": 5.0, "y": 3.0},
	}
	res, err := post.Run(rows)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Statistic > -0.9 {
		t.Errorf("r = %g, want strongly negative (< -0.9)", res.Statistic)
	}
	if !res.RejectNull {
		t.Errorf("reject_null = false; want true (p = %g)", res.PValue)
	}
}

// TestPearsonRPost_AgreesWithRowVariant: same data fed via Records to
// the tier-1 variant and via rows to the tier-2 variant must produce
// identical r, df, and p (the math path is shared).
func TestPearsonRPost_AgreesWithRowVariant(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "y", Type: encoding.FieldTypeF64},
		},
	}
	row, err := newPearsonRRow(&types.Test{
		Type: types.TEST_PEARSON_R, Field: "x", Field2: "y",
	}, schema)
	if err != nil {
		t.Fatalf("newPearsonRRow: %v", err)
	}
	post, err := newPearsonRPost(&types.Test{
		Type: types.TEST_PEARSON_R, Field: "x", Field2: "y",
	}, nil)
	if err != nil {
		t.Fatalf("newPearsonRPost: %v", err)
	}
	pairs := [][2]float64{
		{1.2, 3.4}, {2.0, 5.1}, {3.5, 6.7}, {4.0, 8.2},
		{5.5, 9.0}, {6.1, 11.0}, {7.0, 12.5}, {8.3, 14.1},
	}
	rows := make([]map[string]any, 0, len(pairs))
	for _, p := range pairs {
		if err := row.UpdateRow(NewRecord(schema, map[string]float64{
			"x": p[0], "y": p[1],
		})); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
		rows = append(rows, map[string]any{"x": p[0], "y": p[1]})
	}
	rRow, err := row.Finalize()
	if err != nil {
		t.Fatalf("row Finalize: %v", err)
	}
	rPost, err := post.Run(rows)
	if err != nil {
		t.Fatalf("post Run: %v", err)
	}
	if math.Abs(rRow.Statistic-rPost.Statistic) > 1e-12 {
		t.Errorf("r mismatch: row=%g post=%g", rRow.Statistic, rPost.Statistic)
	}
	if math.Abs(rRow.PValue-rPost.PValue) > 1e-12 {
		t.Errorf("p mismatch: row=%g post=%g", rRow.PValue, rPost.PValue)
	}
	if rRow.DF != rPost.DF {
		t.Errorf("df mismatch: row=%g post=%g", rRow.DF, rPost.DF)
	}
}

// TestPearsonRPost_InsufficientN: n < 3 surfaces PULSE_TEST_INSUFFICIENT_N.
func TestPearsonRPost_InsufficientN(t *testing.T) {
	post, err := newPearsonRPost(&types.Test{
		Type: types.TEST_PEARSON_R, Field: "x", Field2: "y",
	}, nil)
	if err != nil {
		t.Fatalf("newPearsonRPost: %v", err)
	}
	_, err = post.Run([]map[string]any{
		{"x": 1.0, "y": 2.0},
		{"x": 2.0, "y": 4.0},
	})
	if err == nil {
		t.Fatal("expected error for n < 3")
	}
}

// TestPearsonRPost_ZeroVariance: constant y must surface
// PULSE_TEST_CORRELATION_UNDEFINED.
func TestPearsonRPost_ZeroVariance(t *testing.T) {
	post, err := newPearsonRPost(&types.Test{
		Type: types.TEST_PEARSON_R, Field: "x", Field2: "y",
	}, nil)
	if err != nil {
		t.Fatalf("newPearsonRPost: %v", err)
	}
	_, err = post.Run([]map[string]any{
		{"x": 1.0, "y": 5.0},
		{"x": 2.0, "y": 5.0},
		{"x": 3.0, "y": 5.0},
		{"x": 4.0, "y": 5.0},
	})
	if err == nil {
		t.Fatal("expected error for constant y")
	}
}

// TestPearsonRPost_RequiresField2: factory rejects missing field2.
func TestPearsonRPost_RequiresField2(t *testing.T) {
	_, err := newPearsonRPost(&types.Test{
		Type:  types.TEST_PEARSON_R,
		Field: "x",
	}, nil)
	if err == nil {
		t.Fatal("expected error for missing field2")
	}
}
