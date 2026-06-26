package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestMidRanks_NoTies verifies that unique values receive consecutive
// 1-based ranks in input order.
func TestMidRanks_NoTies(t *testing.T) {
	values := []float64{3.0, 1.0, 4.0, 1.5, 2.0}
	ranks, ties := midRanks(values)
	want := []float64{4, 1, 5, 2, 3}
	if len(ranks) != len(want) {
		t.Fatalf("ranks length: got %d, want %d", len(ranks), len(want))
	}
	for i, r := range ranks {
		if r != want[i] {
			t.Errorf("rank[%d]: got %g, want %g", i, r, want[i])
		}
	}
	if len(ties) != 0 {
		t.Errorf("expected no ties, got %v", ties)
	}
}

// TestMidRanks_Ties verifies that runs of equal values receive the
// average of the positions they would occupy.
func TestMidRanks_Ties(t *testing.T) {
	values := []float64{1, 2, 2, 3, 3, 3, 4}
	ranks, ties := midRanks(values)
	want := []float64{1, 2.5, 2.5, 5, 5, 5, 7}
	for i, r := range ranks {
		if r != want[i] {
			t.Errorf("rank[%d]: got %g, want %g", i, r, want[i])
		}
	}
	if len(ties) != 2 || ties[0] != 2 || ties[1] != 3 {
		t.Errorf("ties: got %v, want [2 3]", ties)
	}
}

// TestMannWhitney_KnownStatistic verifies the hand-computed Mann-Whitney
// statistic on the classic teaching example
//
//	A: 5, 8, 12, 13, 14
//	B: 6, 7, 9, 10, 11
//
// Combined ranks of A = {1, 4, 8, 9, 10} → R_A = 32; n_A = n_B = 5.
// U_A = 32 − 5·6/2 = 17; U_B = 25 − 17 = 8; U_min = 8.
// μ_U = 12.5, σ²_U = 5·5·11/12 ≈ 22.917, σ_U ≈ 4.787.
// z (with continuity correction toward μ) = (17 − 0.5 − 12.5) / 4.787 ≈ 0.836
// → p ≈ 0.4034.
func TestMannWhitney_KnownStatistic(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, err := newMannWhitneyRow(spec, schema)
	if err != nil {
		t.Fatalf("newMannWhitneyRow: %v", err)
	}
	feed := func(group string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", group)
			if err := test.UpdateRow(r); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	feed("A", 5, 8, 12, 13, 14)
	feed("B", 6, 7, 9, 10, 11)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-8) > 1e-9 {
		t.Errorf("U_min: got %g, want 8", res.Statistic)
	}
	if math.Abs(res.PValue-0.4034) > 0.005 {
		t.Errorf("PValue: got %g, want ~0.4034", res.PValue)
	}
	if res.RejectNull {
		t.Errorf("RejectNull should be false at alpha=0.05")
	}
}

// TestMannWhitney_SplitGroupsLT2 verifies the missing-group guard.
func TestMannWhitney_SplitGroupsLT2(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, _ := newMannWhitneyRow(spec, schema)
	for _, v := range []float64{1, 2, 3} {
		r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", "only")
		_ = test.UpdateRow(r)
	}
	if _, err := test.Finalize(); err == nil {
		t.Fatalf("expected SPLIT_GROUPS_LT_2 error")
	}
}

// TestWilcoxon_KnownStatistic verifies the hand-computed Wilcoxon
// signed-rank statistic on a small worked example:
//
//	pairs (before, after): (10, 12), (15, 14), (8, 11), (20, 25), (5, 6), (30, 27)
//	diffs                : -2, 1, -3, -5, -1, 3
//	|diffs|              :  2, 1, 3, 5, 1, 3
//	ranks (mid)          :  3, 1.5, 4.5, 6, 1.5, 4.5
//	W⁺ = ranks where diff > 0 = 1.5 + 4.5 = 6      (after − before > 0? wait)
//
// We use d = field − field2; spec sets field2 = "before", field = "after"
// to keep sign convention with scipy.wilcoxon(after, before).
//
// Concretely: scipy returns statistic = min(W⁺, W⁻) = 6, p ≈ 0.4631.
func TestWilcoxon_KnownStatistic(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "before", Type: encoding.FieldTypeF64},
			{Name: "after", Type: encoding.FieldTypeF64},
		},
	}
	spec := &types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before", Alpha: 0.05}
	test, err := newWilcoxonSRRow(spec, schema)
	if err != nil {
		t.Fatalf("newWilcoxonSRRow: %v", err)
	}
	pairs := [][2]float64{
		{10, 12}, {15, 14}, {8, 11}, {20, 25}, {5, 6}, {30, 27},
	}
	for _, p := range pairs {
		r := NewRecord(schema, map[string]float64{"before": p[0], "after": p[1]})
		if err := test.UpdateRow(r); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-6) > 1e-9 {
		t.Errorf("statistic: got %g, want 6", res.Statistic)
	}
}

// TestWilcoxon_DropsZeroDiff verifies that exact-zero diffs are excluded
// and reported in the result.
func TestWilcoxon_DropsZeroDiff(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "before", Type: encoding.FieldTypeF64},
			{Name: "after", Type: encoding.FieldTypeF64},
		},
	}
	spec := &types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before", Alpha: 0.05}
	test, _ := newWilcoxonSRRow(spec, schema)
	// 6 non-zero pairs + 2 zero pairs.
	pairs := [][2]float64{{1, 3}, {2, 5}, {3, 1}, {4, 9}, {5, 2}, {6, 10}, {7, 7}, {8, 8}}
	for _, p := range pairs {
		r := NewRecord(schema, map[string]float64{"before": p[0], "after": p[1]})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if got := res.Details["zero_diffs"].(int); got != 2 {
		t.Errorf("zero_diffs: got %d, want 2", got)
	}
	if got := res.Details["n"].(int); got != 6 {
		t.Errorf("n: got %d, want 6", got)
	}
}

// TestKruskalWallis_KnownStatistic uses the classic 3-group example:
//
//	A: 17, 16, 20, 28, 41
//	B: 13, 25, 18, 25, 21
//	C: 35, 51, 30, 26, 30
//
// Combined ranks split per group: R_A = 34, R_B = 26, R_C = 60. N=15.
// H = 12/(15·16)·(34² + 26² + 60²)/5 − 3·16 = 6.32. Two tie groups of
// size 2 (the two 25s, the two 30s) → tie factor 1 − 12/(15³−15) = 0.9964,
// so H_corrected ≈ 6.34, df=2, p ≈ 0.042.
func TestKruskalWallis_KnownStatistic(t *testing.T) {
	schema := twoSampleFixtureSchema()
	spec := &types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
	test, err := newKruskalWallisRow(spec, schema)
	if err != nil {
		t.Fatalf("newKruskalWallisRow: %v", err)
	}
	feed := func(g string, vs ...float64) {
		for _, v := range vs {
			r := newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)
			_ = test.UpdateRow(r)
		}
	}
	feed("A", 17, 16, 20, 28, 41)
	feed("B", 13, 25, 18, 25, 21)
	feed("C", 35, 51, 30, 26, 30)
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-6.34) > 0.05 {
		t.Errorf("H: got %g, want ~6.34", res.Statistic)
	}
	if res.DF != 2 {
		t.Errorf("DF: got %g, want 2", res.DF)
	}
	if math.Abs(res.PValue-0.042) > 0.005 {
		t.Errorf("PValue: got %g, want ~0.042", res.PValue)
	}
}

// TestSpearman_PerfectRank verifies ρ=1 on a strictly increasing pair.
func TestSpearman_PerfectRank(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "y", Type: encoding.FieldTypeF64},
		},
	}
	spec := &types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y", Alpha: 0.05}
	test, _ := newSpearmanRRow(spec, schema)
	for i := range 6 {
		r := NewRecord(schema, map[string]float64{"x": float64(i), "y": float64(i * i)})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-1) > 1e-9 {
		t.Errorf("ρ: got %g, want 1", res.Statistic)
	}
	if res.PValue > 1e-6 {
		t.Errorf("PValue: got %g, want ~0", res.PValue)
	}
}

// TestSpearman_KnownTies verifies a worked example with tied ranks.
// scipy.stats.spearmanr([1,2,2,4,5], [1,3,2,4,5]) returns ρ ≈ 0.9747, p ≈ 0.0048.
func TestSpearman_KnownTies(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "y", Type: encoding.FieldTypeF64},
		},
	}
	spec := &types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y", Alpha: 0.05}
	test, _ := newSpearmanRRow(spec, schema)
	pairs := [][2]float64{{1, 1}, {2, 3}, {2, 2}, {4, 4}, {5, 5}}
	for _, p := range pairs {
		r := NewRecord(schema, map[string]float64{"x": p[0], "y": p[1]})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-0.9747) > 0.005 {
		t.Errorf("ρ: got %g, want ~0.9747", res.Statistic)
	}
}

// TestKendallTau_NoTies verifies a hand-computed τ:
//
//	x = [1, 2, 3, 4, 5]
//	y = [2, 1, 4, 3, 5]
//
// pairs: (1,2)(2,1)D (1,2)(3,4)C (1,2)(4,3)C (1,2)(5,5)C
//
//	(2,1)(3,4)C (2,1)(4,3)C (2,1)(5,5)C (3,4)(4,3)D (3,4)(5,5)C (4,3)(5,5)C
//
// C = 7, D = 2 (note (1,2)(5,5) has y=2 vs y=5 — concordant; recount).
// Let's recount methodically:
//
//	indices in y: 2,1,4,3,5
//	pairs (i,j) for i<j: count C if (x_i-x_j)*(y_i-y_j) > 0:
//	(0,1): -1*1 = -1 D
//	(0,2): -1*-2= 2 C
//	(0,3): -1*-1= 1 C
//	(0,4): -1*-3= 3 C
//	(1,2): -1*-3= 3 C
//	(1,3): -1*-2= 2 C
//	(1,4): -1*-4= 4 C
//	(2,3): -1*1 = -1 D
//	(2,4): -1*-1= 1 C
//	(3,4): -1*-2= 2 C
//	→ C=8, D=2, no ties; τ = (8-2)/10 = 0.6.
func TestKendallTau_NoTies(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "y", Type: encoding.FieldTypeF64},
		},
	}
	spec := &types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y", Alpha: 0.05}
	test, _ := newKendallTauRow(spec, schema)
	xs := []float64{1, 2, 3, 4, 5}
	ys := []float64{2, 1, 4, 3, 5}
	for i := range xs {
		r := NewRecord(schema, map[string]float64{"x": xs[i], "y": ys[i]})
		_ = test.UpdateRow(r)
	}
	res, err := test.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-0.6) > 1e-9 {
		t.Errorf("τ: got %g, want 0.6", res.Statistic)
	}
	if c, _ := res.Details["concordant"].(int64); c != 8 {
		t.Errorf("concordant: got %v, want 8", c)
	}
	if d, _ := res.Details["discordant"].(int64); d != 2 {
		t.Errorf("discordant: got %v, want 2", d)
	}
}
