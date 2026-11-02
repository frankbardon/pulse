package collinearity

import (
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/linalg"
	"github.com/frankbardon/pulse/types"
)

// corr4 is a positive-definite 4 × 4 correlation.
func corr4(t *testing.T) *linalg.Sym {
	t.Helper()
	r, err := linalg.NewSymFromRows([][]float64{
		{1, 0.6, 0.3, -0.2},
		{0.6, 1, 0.45, 0.1},
		{0.3, 0.45, 1, 0.25},
		{-0.2, 0.1, 0.25, 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// det is a cofactor-free determinant by Gaussian elimination (test
// reference only).
func det(m [][]float64) float64 {
	n := len(m)
	a := make([][]float64, n)
	for i := range m {
		a[i] = slices.Clone(m[i])
	}
	d := 1.0
	for k := 0; k < n; k++ {
		d *= a[k][k]
		for i := k + 1; i < n; i++ {
			f := a[i][k] / a[k][k]
			for j := k; j < n; j++ {
				a[i][j] -= f * a[k][j]
			}
		}
	}
	return d
}

func sub(r *linalg.Sym, keep func(int) bool) [][]float64 {
	var out [][]float64
	for i := 0; i < r.N(); i++ {
		if !keep(i) {
			continue
		}
		var row []float64
		for j := 0; j < r.N(); j++ {
			if keep(j) {
				row = append(row, r.At(i, j))
			}
		}
		out = append(out, row)
	}
	return out
}

// TestGVIF: a one-column term's figure is diag(R⁻¹) bit for bit (the
// value MAT_COLLINEARITY reports), a wider one's equals Fox & Monette's
// det(R_SS)·det(R_−S,−S)/det(R) to rounding, and GVIF of every column
// together is 1.
func TestGVIF(t *testing.T) {
	t.Parallel()

	r := corr4(t)
	inv, err := linalg.InverseSPD(r)
	if err != nil {
		t.Fatal(err)
	}
	single, err := GVIF(r, Singletons(4))
	if err != nil {
		t.Fatal(err)
	}
	for j, v := range single {
		if v != inv.At(j, j) {
			t.Errorf("VIF[%d] = %v, diag(R⁻¹) %v", j, v, inv.At(j, j))
		}
	}
	terms := [][]int{{0}, {1, 2}, {3}, {0, 1, 2, 3}}
	got, err := GVIF(r, terms)
	if err != nil {
		t.Fatal(err)
	}
	all := det(sub(r, func(int) bool { return true }))
	for ti, cols := range terms[:3] {
		in := func(i int) bool { return slices.Contains(cols, i) }
		want := det(sub(r, in)) * det(sub(r, func(i int) bool { return !in(i) })) / all
		if math.Abs(got[ti]-want) > 1e-13*want {
			t.Errorf("GVIF%v = %.17g, Fox & Monette %.17g", cols, got[ti], want)
		}
	}
	if math.Abs(got[3]-1) > 1e-14 {
		t.Errorf("GVIF of every column = %v, want 1", got[3])
	}
	if s := Scaled(got[1], 2); math.Abs(s-math.Pow(got[1], 0.25)) > 1e-15*s {
		t.Errorf("Scaled(df 2) = %v", s)
	}
	if s := Scaled(single[0], 1); s != math.Sqrt(single[0]) {
		t.Errorf("Scaled(df 1) = %v, want √", s)
	}
}

// TestCorrelation: a cross-product scales to unit diagonal; a zero
// variance refuses.
func TestCorrelation(t *testing.T) {
	t.Parallel()

	c, _ := linalg.NewSymFromRows([][]float64{{4, 2}, {2, 9}})
	r, ok := Correlation(c)
	if !ok || r.At(0, 0) != 1 || r.At(1, 1) != 1 || r.At(0, 1) != 2.0/6 {
		t.Errorf("Correlation = %v %v", r, ok)
	}
	z, _ := linalg.NewSymFromRows([][]float64{{0, 0}, {0, 9}})
	if _, ok := Correlation(z); ok {
		t.Error("a zero-variance column correlated")
	}
}

// TestTerms: ungrouped predictors are their own terms; a group is one
// term at its first predictor's position, its columns ascending.
func TestTerms(t *testing.T) {
	t.Parallel()

	spec := &types.RegressionSpec{Predictors: []string{"a", "g1", "b", "g2", "c"},
		Collinearity: &types.RegressionCollinearity{Groups: []types.RegressionTermGroup{{Name: "g", Predictors: []string{"g2", "g1"}}}}}
	got := Terms(spec)
	want := []Term{{"a", []int{0}}, {"g", []int{1, 3}}, {"b", []int{2}}, {"c", []int{4}}}
	if len(got) != len(want) {
		t.Fatalf("Terms = %v", got)
	}
	for i := range want {
		if got[i].Name != want[i].Name || !slices.Equal(got[i].Columns, want[i].Columns) {
			t.Errorf("term %d = %v, want %v", i, got[i], want[i])
		}
	}
	if Terms(&types.RegressionSpec{Predictors: []string{"a"}}) != nil {
		t.Error("no block, terms")
	}
}

// TestCheck_Refusals: every refusal is PROCESSING_CONFIG with
// details.slot collinearity and the requires word; the supported fits
// pass.
func TestCheck_Refusals(t *testing.T) {
	t.Parallel()

	xs := []string{"x1", "x2", "x3"}
	mk := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Type: types.REG_GLM, Name: "m", Family: "binomial", Target: "y", Predictors: xs,
			Collinearity: &types.RegressionCollinearity{}}
		mut(s)
		return s
	}
	group := func(name string, ps ...string) []types.RegressionTermGroup {
		return []types.RegressionTermGroup{{Name: name, Predictors: ps}}
	}
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"basis", mk(func(s *types.RegressionSpec) { s.Collinearity.Basis = "design" }), "basis model or predictors"},
		{"unnamed group", mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("", "x1", "x2") }), "a group name"},
		{"empty group", mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("g") }), "a predictor"},
		{"unknown predictor", mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("g", "x1", "x9") }), "a predictor"},
		{"predictor in two groups", mk(func(s *types.RegressionSpec) {
			s.Collinearity.Groups = []types.RegressionTermGroup{{Name: "g", Predictors: []string{"x1"}}, {Name: "h", Predictors: []string{"x1", "x2"}}}
		}), "a predictor"},
		{"name clashes with a predictor", mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("x3", "x1", "x2") }), "a unique term name"},
		{"bayes", mk(func(s *types.RegressionSpec) { s.Type, s.Family = types.REG_BAYES_LINEAR, "" }), "type REG_OLS or REG_GLM"},
		{"no intercept", mk(func(s *types.RegressionSpec) { s.Type, s.Family, s.Intercept = types.REG_OLS, "", new(bool) }), "intercept"},
		{"selection", mk(func(s *types.RegressionSpec) { s.Selection, s.Criterion = "forward", "aic" }), "no_selection"},
		{"glm penalty on model basis", mk(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.1 }), "unpenalized"},
		{"one term", mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("all", "x1", "x2", "x3") }), "two_or_more_terms"},
		{"one predictor", mk(func(s *types.RegressionSpec) { s.Predictors = []string{"x1"} }), "two_or_more_terms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cerr := Check(tc.spec)
			if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["requires"] != tc.requires || cerr.Details["slot"] != "collinearity" {
				t.Fatalf("Check = %v, want PROCESSING_CONFIG requires %q", cerr, tc.requires)
			}
		})
	}
	for _, s := range []*types.RegressionSpec{
		mk(func(*types.RegressionSpec) {}),
		mk(func(s *types.RegressionSpec) { s.Collinearity.Groups = group("g", "x2", "x3") }),
		mk(func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.Collinearity.Basis = "l2", 0.1, types.CollinearityBasisPredictors
		}),
		mk(func(s *types.RegressionSpec) { s.Type, s.Family, s.Penalty, s.Alpha = types.REG_OLS, "", "l1", 0.1 }),
		mk(func(s *types.RegressionSpec) { s.Resample = "jackknife" }),
		mk(func(s *types.RegressionSpec) { s.Collinearity = nil; s.Type = types.REG_BAYES_LINEAR }),
		mk(func(s *types.RegressionSpec) { s.Type = types.REG_MMM_HILL }),
	} {
		if cerr := Check(s); cerr != nil {
			t.Errorf("Check(%+v) = %v, want nil", s, cerr)
		}
	}
}
