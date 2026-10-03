package processing

import (
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Rank-test references below were generated with base R 4.x
// (options(digits = 17)); effect sizes follow the cited closed forms:
//   - ε² = H / ((n² − 1)/(n + 1)) over kruskal.test()$statistic
//     (Tomczak & Tomczak 2014, eq. 7) — H is tie-corrected in R and Pulse.
//   - independent rank-biserial = 2·W/(n_x·n_y) − 1 with W =
//     wilcox.test(x, y)$statistic = U_x (Kerby 2014 simple difference).
//   - matched-pairs rank-biserial = 2·V/(m(m+1)/2) − 1 with V =
//     wilcox.test(d[d != 0])$statistic = W⁺ over the m non-zero
//     differences (Kerby 2014), i.e. (W⁺ − W⁻)/(W⁺ + W⁻).

// insectSprays is R datasets::InsectSprays (Beall 1942) — heavily tied.
var insectSprays = []anovaGroup{
	{"A", []float64{10, 7, 20, 14, 14, 12, 10, 23, 17, 20, 14, 13}},
	{"B", []float64{11, 17, 21, 11, 16, 14, 17, 17, 19, 21, 7, 13}},
	{"C", []float64{0, 1, 7, 2, 3, 1, 2, 1, 3, 0, 1, 4}},
	{"D", []float64{3, 5, 12, 6, 4, 3, 5, 5, 5, 5, 2, 4}},
	{"E", []float64{3, 5, 3, 5, 3, 6, 1, 1, 3, 2, 6, 4}},
	{"F", []float64{11, 9, 15, 22, 15, 16, 13, 10, 26, 26, 24, 13}},
}

func TestKruskalWallis_EpsilonSquared(t *testing.T) {
	cases := []struct {
		name   string
		groups []anovaGroup
		h, eps float64
	}{
		{
			// R ?kruskal.test (Hollander & Wolfe 1973 p.116): H = 27/35,
			// n = 14 → ε² = (27/35)/13 = 27/455.
			name: "R_hollander_wolfe", h: 27.0 / 35, eps: 27.0 / 455,
			groups: []anovaGroup{
				{"x", []float64{2.9, 3.0, 2.5, 2.6, 3.2}},
				{"y", []float64{3.8, 2.7, 4.0, 2.4}},
				{"z", []float64{2.8, 3.4, 3.7, 2.2, 2.0}},
			},
		},
		{
			// kruskal.test(count ~ spray, InsectSprays): tie-corrected
			// H = 54.691344622371446, n = 72 → ε² = 0.77030062848410485.
			name: "R_InsectSprays_ties", groups: insectSprays,
			h: 54.691344622371446, eps: 0.77030062848410485,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := twoSampleFixtureSchema()
			rt, err := newKruskalWallisRow(&types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "revenue", SplitBy: "treatment"}, schema)
			if err != nil {
				t.Fatalf("newKruskalWallisRow: %v", err)
			}
			res := runRowTest(t, rt, schema, tc.groups)
			if math.Abs(res.Statistic-tc.h) > 1e-9 {
				t.Fatalf("H = %.17g, want %.17g", res.Statistic, tc.h)
			}
			wantEffect(t, effectSizes(t, res), "epsilon_squared", tc.eps)
			for _, k := range []string{"groups", "n", "rank_sums", "n_total", "tie_factor"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("pre-existing Details.%s missing", k)
				}
			}
		})
	}
}

// TestKruskalWallis_EpsilonSquared_AllTiedOmitted: when every value is
// tied the tie correction is undefined, so ε² is omitted.
func TestKruskalWallis_EpsilonSquared_AllTiedOmitted(t *testing.T) {
	schema := twoSampleFixtureSchema()
	rt, err := newKruskalWallisRow(&types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "revenue", SplitBy: "treatment"}, schema)
	if err != nil {
		t.Fatalf("newKruskalWallisRow: %v", err)
	}
	res := runRowTest(t, rt, schema, []anovaGroup{{"a", []float64{5, 5, 5}}, {"b", []float64{5, 5, 5}}})
	if _, has := res.Details[effectSizeDetailsKey]; has {
		t.Errorf("Details.effect_size = %#v, want omitted for all-tied input", res.Details[effectSizeDetailsKey])
	}
}

func TestMannWhitney_RankBiserial(t *testing.T) {
	rWilcoxX := []float64{0.80, 0.83, 1.89, 1.04, 1.45, 1.38, 1.91, 1.64, 0.73, 1.46}
	rWilcoxY := []float64{1.15, 0.88, 0.90, 0.74, 1.21}
	sleep1 := []float64{0.7, -1.6, -0.2, -1.2, -0.1, 3.4, 3.7, 0.8, 0.0, 2.0}
	sleep2 := []float64{1.9, 0.8, 1.1, 0.1, -0.1, 4.4, 5.5, 1.6, 4.6, 3.4}
	cases := []struct {
		name   string
		groups []anovaGroup // sorted name order == Details.groups order
		r      float64
	}{
		// R ?wilcox.test (Hollander & Wolfe p.69): W = 35, 10×5 → r = 0.4.
		{"R_wilcox_example", []anovaGroup{{"a", rWilcoxX}, {"b", rWilcoxY}}, 0.39999999999999991},
		// Same data, group order swapped: the sign flips, magnitude holds.
		{"R_wilcox_example_swapped", []anovaGroup{{"a", rWilcoxY}, {"b", rWilcoxX}}, -0.4},
		// R sleep as independent groups (ties): W = 25.5 → r = −0.49.
		{"R_sleep_ties", []anovaGroup{{"1", sleep1}, {"2", sleep2}}, -0.49},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := twoSampleFixtureSchema()
			rt, err := newMannWhitneyRow(&types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment"}, schema)
			if err != nil {
				t.Fatalf("newMannWhitneyRow: %v", err)
			}
			res := runRowTest(t, rt, schema, tc.groups)
			es := effectSizes(t, res)
			wantEffect(t, es, "rank_biserial", tc.r)
			// Magnitude identity from the story: |r| = 1 − 2·U_min/(n_A·n_B).
			n := res.Details["n"].([]int)
			uMin := res.Details["u_min"].(float64)
			if got := 1 - 2*uMin/float64(n[0]*n[1]); math.Abs(math.Abs(tc.r)-got) > effectSizeTol {
				t.Errorf("|r| = %g, want 1 − 2·U_min/(n_A·n_B) = %g", math.Abs(tc.r), got)
			}
			// Sign convention: same direction as z.
			if z := res.Details["z"].(float64); math.Signbit(z) != math.Signbit(es["rank_biserial"].(float64)) {
				t.Errorf("sign(rank_biserial) != sign(z = %g)", z)
			}
		})
	}
}

// wilcoxonPair is one (after, before) observation; d = after − before.
type wilcoxonPair struct{ after, before float64 }

func runWilcoxonRow(t *testing.T, pairs []wilcoxonPair) *types.TestResult {
	t.Helper()
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "before", Type: encoding.FieldTypeF64},
		{Name: "after", Type: encoding.FieldTypeF64},
	}}
	rt, err := newWilcoxonSRRow(&types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before"}, schema)
	if err != nil {
		t.Fatalf("newWilcoxonSRRow: %v", err)
	}
	for _, p := range pairs {
		if err := rt.UpdateRow(NewRecord(schema, map[string]float64{"before": p.before, "after": p.after})); err != nil {
			t.Fatalf("UpdateRow: %v", err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return res
}

func wilcoxonPairs(after, before []float64) []wilcoxonPair {
	out := make([]wilcoxonPair, len(after))
	for i := range after {
		out[i] = wilcoxonPair{after[i], before[i]}
	}
	return out
}

func TestWilcoxonSR_RankBiserial(t *testing.T) {
	hwX := []float64{1.83, 0.50, 1.62, 2.48, 1.68, 1.88, 1.55, 3.06, 1.30}
	hwY := []float64{0.878, 0.647, 0.598, 2.05, 1.06, 1.29, 1.06, 3.14, 1.29}
	tieA := []float64{5, 7, 3, 9, 4, 6, 8, 2, 10, 6}
	tieB := []float64{3, 9, 1, 7, 4, 8, 5, 4, 6, 3}
	cases := []struct {
		name  string
		pairs []wilcoxonPair
		r     float64
	}{
		// R ?wilcox.test paired (Hollander & Wolfe p.29): V = 40, m = 9
		// → r = (40 − 5)/45 = 7/9.
		{"R_hollander_wolfe", wilcoxonPairs(hwX, hwY), 7.0 / 9},
		// Reversed pairing flips the sign.
		{"R_hollander_wolfe_reversed", wilcoxonPairs(hwY, hwX), -7.0 / 9},
		// Tied |d| and one zero diff. Pulse drops zeros BEFORE ranking
		// (Wilcoxon convention, scipy zero_method="wilcox"); this R build's
		// paired wilcox.test ranks the zero first (Pratt, V = 40.5), so the
		// reference is wilcox.test(d[d != 0]): V = 34.5, m = 9 →
		// r = (34.5 − 10.5)/45 = 8/15.
		{"R_ties_and_zero", wilcoxonPairs(tieA, tieB), 8.0 / 15},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runWilcoxonRow(t, tc.pairs)
			es := effectSizes(t, res)
			wantEffect(t, es, "rank_biserial", tc.r)
			for _, k := range []string{"n", "w_plus", "w_minus", "mu_w", "var_w", "z", "zero_diffs"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("pre-existing Details.%s missing", k)
				}
			}
			if z := res.Details["z"].(float64); math.Signbit(z) != math.Signbit(tc.r) {
				t.Errorf("sign(rank_biserial) != sign(z = %g)", z)
			}

			// Post twin parity: same keys, same values.
			rows := make([]map[string]any, len(tc.pairs))
			for i, p := range tc.pairs {
				rows[i] = map[string]any{"after": p.after, "before": p.before}
			}
			post, err := newWilcoxonSRPost(&types.Test{Type: types.TEST_WILCOXON_SR, Field: "after", Field2: "before"}, nil)
			if err != nil {
				t.Fatalf("newWilcoxonSRPost: %v", err)
			}
			pres, err := post.Run(rows)
			if err != nil {
				t.Fatalf("post Run: %v", err)
			}
			assertEffectParity(t, res, pres)
		})
	}
}

// TestRankEffectSizes_Undefined pins the NaN contract of the rank
// helpers; setEffectSize drops NaN, so each one omits the key.
func TestRankEffectSizes_Undefined(t *testing.T) {
	cases := []struct {
		name string
		v    float64
	}{
		{"epsilon_n1", epsilonSquared(3, 1)},
		{"epsilon_negative_h", epsilonSquared(-1, 10)},
		{"epsilon_nan_h", epsilonSquared(math.NaN(), 10)},
		{"epsilon_inf_h", epsilonSquared(math.Inf(1), 10)},
		{"rb_independent_zero_n", rankBiserialIndependent(3, 0, 5)},
		{"rb_independent_nan_u", rankBiserialIndependent(math.NaN(), 4, 5)},
		{"rb_paired_no_ranks", rankBiserialPaired(0, 0)},
		{"rb_paired_inf", rankBiserialPaired(math.Inf(1), 1)},
		{"rb_paired_negative_total", rankBiserialPaired(-3, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !math.IsNaN(tc.v) {
				t.Fatalf("helper = %v, want NaN", tc.v)
			}
			details := map[string]any{}
			setEffectSize(details, "x", tc.v)
			if _, has := details[effectSizeDetailsKey]; has {
				t.Errorf("undefined value produced Details.effect_size")
			}
		})
	}
}
