package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// effectSizeTol absorbs amd64 FMA vs arm64 rounding drift; every
// reference below is quoted to ≥ 9 significant digits.
const effectSizeTol = 1e-9

// effectSizes returns Details["effect_size"] or fails the test.
func effectSizes(t *testing.T, res *types.TestResult) map[string]any {
	t.Helper()
	es, ok := res.Details[effectSizeDetailsKey].(map[string]any)
	if !ok {
		t.Fatalf("Details[%q] = %#v, want map[string]any", effectSizeDetailsKey, res.Details[effectSizeDetailsKey])
	}
	return es
}

func wantEffect(t *testing.T, es map[string]any, key string, want float64) {
	t.Helper()
	got, ok := es[key].(float64)
	if !ok {
		t.Fatalf("effect_size.%s = %#v, want float64 %v", key, es[key], want)
	}
	if math.Abs(got-want) > effectSizeTol {
		t.Errorf("effect_size.%s = %.12g, want %.12g", key, got, want)
	}
}

// TestChiSquare_EffectSizes checks Cramér's V (every table) and φ (2×2
// only) against published / closed-form references. Pulse's χ² carries
// no Yates correction, so the references are the uncorrected ones.
func TestChiSquare_EffectSizes(t *testing.T) {
	cases := []struct {
		name    string
		ref     string
		table   [][]int
		chi2    float64
		v       float64
		wantPhi bool
	}{
		{
			// R ?chisq.test example (Agresti 2007, An Introduction to
			// Categorical Data Analysis p.39, gender × party ID): X-squared =
			// 30.07, df = 2; DescTools::CramerV = 0.1044358.
			name: "agresti_2x3", ref: "R ?chisq.test (Agresti 2007 p.39)",
			table: [][]int{{762, 327, 468}, {484, 239, 477}},
			chi2:  30.070149095754672, v: 0.10443580235646777,
		},
		{
			// scipy.stats.contingency.association docstring example
			// (obs4x2, method="cramer") = 0.18617813077483678.
			name: "scipy_4x2", ref: "scipy association docstring",
			table: [][]int{{100, 150}, {203, 322}, {420, 700}, {320, 210}},
			chi2:  84.05606871861963, v: 0.18617813077483678,
		},
		{
			// Perfect association in a 3×3 table: χ² = N·(min(r,c)−1) and
			// V = 1 exactly (Cramér 1946 §21.9 — V reaches 1 only under
			// complete dependence). Pins the min(r,c)−1 divisor.
			name: "perfect_3x3", ref: "Cramér 1946 V=1 bound",
			table: [][]int{{10, 0, 0}, {0, 10, 0}, {0, 0, 10}},
			chi2:  60, v: 1,
		},
		{
			// 2×2: φ = |ad − bc| / √(r1·r2·c1·c2) = 1000/√51e6
			// (Cohen 1988 §7.2.3); for 2×2 Cramér's V ≡ φ.
			name: "closed_form_2x2", ref: "φ = |ad−bc|/√(r1r2c1c2)",
			table: [][]int{{90, 10}, {80, 20}},
			chi2:  3.9215686274509807, v: 1000 / math.Sqrt(51e6), wantPhi: true,
		},
	}
	rowNames := []string{"r0", "r1", "r2", "r3"}
	colNames := []string{"c0", "c1", "c2"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := chiSquareFixtureSchema()
			cs, err := newChiSqRow(&types.Test{Type: types.TEST_CHISQ, Rows: "kind", Cols: "outcome"}, schema)
			if err != nil {
				t.Fatalf("newChiSqRow: %v", err)
			}
			for ri, row := range tc.table {
				for ci, count := range row {
					for range count {
						if err := cs.UpdateRow(chiSquareTestRecord(t, schema, rowNames[ri], colNames[ci])); err != nil {
							t.Fatalf("UpdateRow: %v", err)
						}
					}
				}
			}
			res, err := cs.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			if math.Abs(res.Statistic-tc.chi2) > 1e-6 {
				t.Errorf("χ² = %.12g, want %.12g (%s)", res.Statistic, tc.chi2, tc.ref)
			}
			es := effectSizes(t, res)
			wantEffect(t, es, "cramers_v", tc.v)
			if tc.wantPhi {
				wantEffect(t, es, "phi", tc.v)
			} else if _, has := es["phi"]; has {
				t.Errorf("effect_size.phi present on a %dx%d table; want omitted", len(tc.table), len(tc.table[0]))
			}
			// Existing keys survive unchanged.
			for _, k := range []string{"row_labels", "col_labels", "contingency", "row_totals", "col_totals", "n", "expected_min"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("Details.%s missing", k)
				}
			}
		})
	}
}

// TestPropZ_CohensH checks Cohen's h = 2·asin√p1 − 2·asin√p2 (Cohen
// 1988 §6.2) on exact arcsine anchors and on the existing 150/200 vs
// 170/200 fixture (pwr::ES.h(0.75, 0.85) = −0.2517987).
func TestPropZ_CohensH(t *testing.T) {
	cases := []struct {
		name         string
		succA, nA    int
		succB, nB    int
		wantH        float64
		wantStatSign float64
	}{
		// φ(0.75) − φ(0.25) = 2π/3 − π/3 = π/3 exactly.
		{"three_quarters_vs_quarter", 3, 4, 1, 4, math.Pi / 3, 1},
		// φ(0.5) − φ(0) = π/2 exactly.
		{"half_vs_zero", 5, 10, 0, 10, math.Pi / 2, 1},
		// pwr::ES.h(0.75, 0.85).
		{"textbook_075_vs_085", 150, 200, 170, 200, -0.2517987210124546, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := &encoding.Schema{Fields: []encoding.Field{
				{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
				{Name: "converted", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			}}
			rt, err := newPropZRow(&types.Test{
				Type: types.TEST_PROP_Z, Field: "converted", SplitBy: "treatment",
				Params: json.RawMessage(`{"success":"yes"}`),
			}, schema)
			if err != nil {
				t.Fatalf("newPropZRow: %v", err)
			}
			feed := func(group, outcome string, count int) {
				g := dictIDOrAdd(schema, "treatment", group)
				o := dictIDOrAdd(schema, "converted", outcome)
				for range count {
					_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"treatment": float64(g), "converted": float64(o)}))
				}
			}
			feed("a", "yes", tc.succA)
			feed("a", "no", tc.nA-tc.succA)
			feed("b", "yes", tc.succB)
			feed("b", "no", tc.nB-tc.succB)
			res, err := rt.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			wantEffect(t, effectSizes(t, res), "cohens_h", tc.wantH)
			if math.Signbit(res.Statistic) != math.Signbit(tc.wantStatSign) {
				t.Errorf("z = %g; cohens_h must share the sign of the p_a − p_b statistic", res.Statistic)
			}
			for _, k := range []string{"success", "groups", "n", "successes", "proportion", "diff", "pooled", "ci_low", "ci_high"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("Details.%s missing", k)
				}
			}
		})
	}
}

// TestTTest_OneSample_CohensD checks the one-sample Cohen's d = (mean −
// mu) / sd against R's sleep data (group 1): t.test gives t = 1.3257,
// df = 9; effectsize::cohens_d(extra, mu = 0) = 0.42 (0.4192264).
func TestTTest_OneSample_CohensD(t *testing.T) {
	sleep1 := []float64{0.7, -1.6, -0.2, -1.2, -0.1, 3.4, 3.7, 0.8, 0.0, 2.0}
	cases := []struct {
		name  string
		xs    []float64
		mu    float64
		wantT float64
		wantD float64
	}{
		{"r_sleep_group1_mu0", sleep1, 0, 1.3257101407138216, 0.4192263561837996},
		// 1..5 vs mu 1: mean 3, sd √2.5 → d = 2/√2.5.
		{"closed_form_1to5_mu1", []float64{1, 2, 3, 4, 5}, 1, 2 / (math.Sqrt(2.5) / math.Sqrt(5)), 2 / math.Sqrt(2.5)},
		// mean == mu → d = 0 exactly (defined, emitted).
		{"zero_effect", []float64{1, 2, 3, 4, 5}, 3, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := numericFixtureSchema("x")
			params, _ := json.Marshal(map[string]float64{"mu": tc.mu})
			rt, err := newTTestRow(&types.Test{Type: types.TEST_T, Field: "x", Params: params}, schema)
			if err != nil {
				t.Fatalf("newTTestRow: %v", err)
			}
			for _, v := range tc.xs {
				_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"x": v}))
			}
			res, err := rt.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			if math.Abs(res.Statistic-tc.wantT) > effectSizeTol {
				t.Errorf("t = %.12g, want %.12g", res.Statistic, tc.wantT)
			}
			wantEffect(t, effectSizes(t, res), "cohens_d", tc.wantD)
			for _, k := range []string{"mu", "n", "mean", "variance", "ci_low", "ci_high"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("Details.%s missing", k)
				}
			}
		})
	}
}

// TestTwoSample_CohensD pins the pooled-SD two-sample Cohen's d emitted
// by TEST_T / TEST_WELCH (two-sample arm) and TEST_Z_TWO_SAMPLE against
// R 4.6.1 effectsize 1.0.3:
//
//	effectsize::cohens_d(c(10,12,14,16,18), c(11,13,19,22,25),
//	                     pooled_sd = TRUE)$Cohens_d = -0.84327404271156781
//
// Groups sort alphabetically, so "control" is the first (minuend) group,
// matching R's x/y order.
func TestTwoSample_CohensD(t *testing.T) {
	const want = -0.84327404271156781
	for _, typ := range []types.TestType{types.TEST_T, types.TEST_WELCH, types.TEST_Z_TWO_SAMPLE} {
		t.Run(string(typ), func(t *testing.T) {
			schema := twoSampleFixtureSchema()
			spec := &types.Test{Type: typ, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}
			factory := rowTestRegistry[typ]
			rt, err := factory(spec, schema)
			if err != nil {
				t.Fatalf("factory: %v", err)
			}
			feed := func(group string, vs ...float64) {
				for _, v := range vs {
					if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", group)); err != nil {
						t.Fatalf("UpdateRow: %v", err)
					}
				}
			}
			feed("control", 10, 12, 14, 16, 18)
			feed("variant", 11, 13, 19, 22, 25)
			res, err := rt.Finalize()
			if err != nil {
				t.Fatalf("Finalize: %v", err)
			}
			got, ok := effectSizes(t, res)["cohens_d"].(float64)
			if !ok {
				t.Fatalf("effect_size.cohens_d missing")
			}
			if rel := math.Abs(got-want) / math.Abs(want); rel > 1e-12 {
				t.Errorf("cohens_d = %.17g, want %.17g (R effectsize), rel err %.3g", got, want, rel)
			}
		})
	}
}

// TestEffectSize_DegenerateInputsOmitKey pins the NaN-safe contract:
// an undefined effect size is OMITTED (never NaN / ±Inf on the wire),
// and the effect_size map is not created when nothing lands in it.
func TestEffectSize_DegenerateInputsOmitKey(t *testing.T) {
	cases := []struct {
		name string
		v    float64
	}{
		{"cramers_v_n0", cramersV(4, 0, 2, 2)},
		{"cramers_v_one_wide", cramersV(4, 10, 1, 3)},
		{"cramers_v_nan_chi2", cramersV(math.NaN(), 10, 2, 2)},
		{"cramers_v_inf_chi2", cramersV(math.Inf(1), 10, 2, 2)},
		{"phi_n0", phiCoefficient(4, 0)},
		{"phi_negative_chi2", phiCoefficient(-1, 10)},
		{"cohens_h_out_of_range", cohensH(1.5, 0.5)},
		{"cohens_h_nan", cohensH(math.NaN(), 0.5)},
		{"cohens_d_zero_sd", cohensDOneSample(3, 1, 0)},
		{"cohens_d_nan_sd", cohensDOneSample(3, 1, math.NaN())},
		{"cohens_d_inf_sd", cohensDOneSample(3, 1, math.Inf(1))},
		{"cohens_d_two_sample_zero_pooled_sd", cohensDTwoSample(2, 5, 0, 5, 0)},
		{"cohens_d_two_sample_no_dof", cohensDTwoSample(2, 1, 4, 1, 4)},
		{"cohens_d_two_sample_nan_variance", cohensDTwoSample(2, 5, math.NaN(), 5, 1)},
		{"cohens_d_two_sample_inf_variance", cohensDTwoSample(2, 5, math.Inf(1), 5, 1)},
		{"raw_inf", math.Inf(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			details := map[string]any{"n": int64(0)}
			setEffectSize(details, "x", tc.v)
			if _, has := details[effectSizeDetailsKey]; has {
				t.Errorf("value %v produced Details.effect_size = %#v; want key omitted", tc.v, details[effectSizeDetailsKey])
			}
		})
	}
	// A defined value lands, and a later undefined one does not evict it.
	details := map[string]any{}
	setEffectSize(details, "cramers_v", cramersV(4, 100, 2, 2))
	setEffectSize(details, "phi", phiCoefficient(4, 0))
	es := details[effectSizeDetailsKey].(map[string]any)
	if len(es) != 1 || es["cramers_v"] != 0.2 {
		t.Errorf("effect_size = %#v, want {cramers_v: 0.2}", es)
	}
	// Existing effect_size entries are merged into, not replaced.
	details = map[string]any{effectSizeDetailsKey: map[string]any{"eta_squared": 0.5}}
	setEffectSize(details, "omega_squared", 0.25)
	es = details[effectSizeDetailsKey].(map[string]any)
	if es["eta_squared"] != 0.5 || es["omega_squared"] != 0.25 {
		t.Errorf("effect_size = %#v, want both keys", es)
	}
}
