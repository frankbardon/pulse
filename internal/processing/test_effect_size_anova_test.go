package processing

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// anovaGroup is one named group of raw observations, fed in order.
type anovaGroup struct {
	name   string
	values []float64
}

// plantGrowth is R datasets::PlantGrowth (Dobson 1983, Table 7.1):
// summary(aov(weight ~ group)) gives SS 3.76634 / 10.49209, F = 4.846
// on (2, 27); oneway.test(weight ~ group) gives Welch F* = 5.181 on
// (2, 17.128).
var plantGrowth = []anovaGroup{
	{"ctrl", []float64{4.17, 5.58, 5.18, 6.11, 4.50, 4.61, 5.17, 4.53, 5.33, 5.14}},
	{"trt1", []float64{4.81, 4.17, 4.41, 3.59, 5.87, 3.83, 6.03, 4.89, 4.32, 4.69}},
	{"trt2", []float64{6.31, 5.12, 5.54, 5.50, 5.37, 5.29, 4.92, 6.15, 5.80, 5.26}},
}

// sleepGroups is R datasets::sleep (Cushny & Peebles 1905) treated as two
// independent groups: oneway.test(extra ~ group) gives Welch F* = 3.4626
// on (1, 17.776) — R's own ?oneway.test example.
var sleepGroups = []anovaGroup{
	{"1", []float64{0.7, -1.6, -0.2, -1.2, -0.1, 3.4, 3.7, 0.8, 0.0, 2.0}},
	{"2", []float64{1.9, 0.8, 1.1, 0.1, -0.1, 4.4, 5.5, 1.6, 4.6, 3.4}},
}

// equalMeansGroups have identical means, so F (and Welch F*) is 0 and
// the unbiased ω² estimate is negative — the clamp-to-0 case.
var equalMeansGroups = []anovaGroup{
	{"A", []float64{8, 9, 10, 11, 12}},
	{"B", []float64{1, 5, 10, 15, 19}},
	{"C", []float64{9, 10, 10, 10, 11}},
}

// runRowTest feeds groups through a tier-1 row test and finalizes it.
func runRowTest(t *testing.T, rt RowTest, schema *encoding.Schema, groups []anovaGroup) *types.TestResult {
	t.Helper()
	for _, g := range groups {
		for _, v := range g.values {
			if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g.name)); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	return res
}

// summaryRows renders groups as the per-group {mean, n, variance} rows
// the tier-2 ANOVA twins consume.
func summaryRows(groups []anovaGroup) []map[string]any {
	rows := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		var b welfordBucket
		for _, v := range g.values {
			b.add(v)
		}
		rows = append(rows, map[string]any{"group": g.name, "mean_x": b.mean, "n": b.n, "var_x": b.sampleVariance()})
	}
	return rows
}

// assertEffectParity requires the post twin to emit the same effect_size
// keys as the row test, with values equal within tolerance.
func assertEffectParity(t *testing.T, row, post *types.TestResult) {
	t.Helper()
	re, pe := effectSizes(t, row), effectSizes(t, post)
	rk, pk := make([]string, 0, len(re)), make([]string, 0, len(pe))
	for k := range re {
		rk = append(rk, k)
	}
	for k := range pe {
		pk = append(pk, k)
	}
	if len(rk) != len(pk) {
		t.Fatalf("effect_size keys: row %v, post %v", re, pe)
	}
	for k, v := range re {
		wantEffect(t, pe, k, v.(float64))
	}
}

func TestAnovaF_OmegaSquared(t *testing.T) {
	cases := []struct {
		name   string
		groups []anovaGroup
		f      float64 // published F, anchors the fixture
		omega  float64
		eta    float64
	}{
		{
			// ω² = (3.76634 − 2·0.388596) / (14.25843 + 0.388596), from
			// the R aov table above (Hays ω², Olejnik & Algina 2003).
			name: "R_PlantGrowth", groups: plantGrowth, f: 4.8460878623801342,
			omega: 0.20407884598997084, eta: 0.26414829683211966,
		},
		{
			// SSB = 10, SSW = 30, df = (2, 12), MSW = 2.5:
			// ω² = (10 − 5) / (40 + 2.5) = 2/17.
			name: "hand_ssb10_ssw30", f: 2,
			groups: []anovaGroup{
				{"g1", []float64{3, 4, 5, 6, 7}},
				{"g2", []float64{4, 5, 6, 7, 8}},
				{"g3", []float64{5, 6, 7, 8, 9}},
			},
			omega: 2.0 / 17, eta: 0.25,
		},
		{
			// F = 0: unbiased ω² = (0 − 2·MSW)/(SST + MSW) < 0 → clamped 0.
			name: "equal_means_clamped", groups: equalMeansGroups, f: 0,
			omega: 0, eta: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := twoSampleFixtureSchema()
			rt, err := newAnovaRow(&types.Test{Type: types.TEST_ANOVA_F, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
			if err != nil {
				t.Fatalf("newAnovaRow: %v", err)
			}
			res := runRowTest(t, rt, schema, tc.groups)
			if math.Abs(res.Statistic-tc.f) > 1e-9 {
				t.Fatalf("F = %.12g, want %.12g", res.Statistic, tc.f)
			}
			es := effectSizes(t, res)
			wantEffect(t, es, "omega_squared", tc.omega)
			wantEffect(t, es, "eta_squared", tc.eta)

			post, err := newAnovaPost(&types.Test{
				Type: types.TEST_ANOVA_F, Field: "mean_x", SplitBy: "group", Alpha: 0.05,
				Params: json.RawMessage(`{"n_col":"n","variance_col":"var_x"}`),
			}, nil)
			if err != nil {
				t.Fatalf("newAnovaPost: %v", err)
			}
			pres, err := post.Run(summaryRows(tc.groups))
			if err != nil {
				t.Fatalf("post Run: %v", err)
			}
			assertEffectParity(t, res, pres)
		})
	}
}

func TestAnovaWelch_OmegaSquared(t *testing.T) {
	cases := []struct {
		name   string
		groups []anovaGroup
		f      float64 // published Welch F*
		omega  float64
	}{
		{
			// est. ω² = 2·(F*−1) / (2·(F*−1) + 30), F* from R oneway.test.
			name: "R_PlantGrowth", groups: plantGrowth,
			f: 5.1809724081131883, omega: 0.21797499726055161,
		},
		{
			// est. ω² = (F*−1) / ((F*−1) + 20), F* from R ?oneway.test.
			name: "R_sleep", groups: sleepGroups,
			f: 3.462626760780446, omega: 0.10963218091128021,
		},
		{
			// F* = 0 → negative estimate clamped to 0.
			name: "equal_means_clamped", groups: equalMeansGroups, f: 0, omega: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := twoSampleFixtureSchema()
			rt, err := newAnovaWelchRow(&types.Test{Type: types.TEST_ANOVA_WELCH, Field: "revenue", SplitBy: "treatment", Alpha: 0.05}, schema)
			if err != nil {
				t.Fatalf("newAnovaWelchRow: %v", err)
			}
			res := runRowTest(t, rt, schema, tc.groups)
			if math.Abs(res.Statistic-tc.f) > 1e-9 {
				t.Fatalf("F* = %.12g, want %.12g", res.Statistic, tc.f)
			}
			for _, k := range []string{"weights", "weighted_mean", "df_within"} {
				if _, ok := res.Details[k]; !ok {
					t.Errorf("pre-existing Details[%q] missing", k)
				}
			}
			wantEffect(t, effectSizes(t, res), "omega_squared", tc.omega)

			post, err := newAnovaWelchPost(&types.Test{
				Type: types.TEST_ANOVA_WELCH, Field: "mean_x", SplitBy: "group",
				Params: json.RawMessage(`{"n_col":"n","variance_col":"var_x"}`),
			}, nil)
			if err != nil {
				t.Fatalf("newAnovaWelchPost: %v", err)
			}
			pres, err := post.Run(summaryRows(tc.groups))
			if err != nil {
				t.Fatalf("post Run: %v", err)
			}
			assertEffectParity(t, res, pres)
		})
	}
}

// TestAnovaRM_PartialEtaSquared: 5 subjects × 3 conditions; R
// summary(aov(y ~ c + Error(s/c))) gives SS_c = 86.8, SS_error = 41.2,
// F = 8.42718 on (2, 8), so partial η² = 86.8 / 128 = 0.678125.
func TestAnovaRM_PartialEtaSquared(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "metric", Type: encoding.FieldTypeF64},
			{Name: "condition", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "subject_id", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		},
	}
	rt, err := newAnovaRMRow(&types.Test{
		Type: types.TEST_ANOVA_RM, Field: "metric", SplitBy: "condition", SubjectField: "subject_id", Alpha: 0.05,
	}, schema)
	if err != nil {
		t.Fatalf("newAnovaRMRow: %v", err)
	}
	table := [][]float64{{45, 50, 55}, {42, 42, 45}, {36, 41, 43}, {39, 35, 40}, {51, 55, 59}}
	for i, row := range table {
		for j, v := range row {
			r := NewRecord(schema, map[string]float64{
				"metric":     v,
				"condition":  float64(dictIDOrAdd(schema, "condition", []string{"a", "b", "c"}[j])),
				"subject_id": float64(dictIDOrAdd(schema, "subject_id", []string{"s1", "s2", "s3", "s4", "s5"}[i])),
			})
			if err := rt.UpdateRow(r); err != nil {
				t.Fatalf("UpdateRow: %v", err)
			}
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if math.Abs(res.Statistic-8.427184466019417) > 1e-9 {
		t.Fatalf("F = %.12g, want 8.42718", res.Statistic)
	}
	if _, ok := res.Details["ss_treatment"]; !ok {
		t.Error("pre-existing Details.ss_treatment missing")
	}
	wantEffect(t, effectSizes(t, res), "partial_eta_squared", 0.678125)
}

// TestAnovaEffectSizes_Undefined: the formula helpers return NaN on a
// degenerate denominator (so setEffectSize omits the key) and clamp a
// negative ω² estimate to 0.
func TestAnovaEffectSizes_Undefined(t *testing.T) {
	nan := []struct {
		name string
		v    float64
	}{
		{"omega_zero_total", omegaSquared(0, 0, 2, 0)},
		{"omega_inf", omegaSquared(math.Inf(1), 1, 1, 1)},
		{"welch_nan_f", welchOmegaSquared(math.NaN(), 2, 30)},
		{"welch_inf_f", welchOmegaSquared(math.Inf(1), 2, 30)},
		{"welch_zero_n", welchOmegaSquared(3, 2, 0)},
		{"welch_nonpositive_den", welchOmegaSquared(0, 5, 4)},
		{"partial_eta_zero", partialEtaSquared(0, 0)},
		{"partial_eta_inf", partialEtaSquared(math.Inf(1), 1)},
	}
	for _, c := range nan {
		if !math.IsNaN(c.v) {
			t.Errorf("%s = %g, want NaN", c.name, c.v)
		}
	}
	if got := omegaSquared(0, 4, 1, 1); got != 0 {
		t.Errorf("negative ω² estimate = %g, want clamped 0", got)
	}
	if got := welchOmegaSquared(0.5, 2, 30); got != 0 {
		t.Errorf("negative Welch ω² estimate = %g, want clamped 0", got)
	}

	// Runtime omission: identical constant groups on the ANOVA_F post
	// twin give SS_between = SS_within = 0 → ω² omitted, η² kept.
	post, err := newAnovaPost(&types.Test{
		Type: types.TEST_ANOVA_F, Field: "mean_x", SplitBy: "group",
		Params: json.RawMessage(`{"n_col":"n","variance_col":"var_x"}`),
	}, nil)
	if err != nil {
		t.Fatalf("newAnovaPost: %v", err)
	}
	res, err := post.Run([]map[string]any{
		{"group": "a", "mean_x": 1.0, "n": int64(3), "var_x": 0.0},
		{"group": "b", "mean_x": 1.0, "n": int64(3), "var_x": 0.0},
	})
	if err != nil {
		t.Fatalf("post Run: %v", err)
	}
	es := effectSizes(t, res)
	if _, ok := es["omega_squared"]; ok {
		t.Errorf("omega_squared = %v, want omitted", es["omega_squared"])
	}
	if !reflect.DeepEqual(es, map[string]any{"eta_squared": 0.0}) {
		t.Errorf("effect_size = %v, want only eta_squared", es)
	}
}
