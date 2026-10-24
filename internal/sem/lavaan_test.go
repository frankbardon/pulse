package sem

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// The lavaan references (testdata/*_lavaan.json) are lavaan ML fits —
// expected information, standard SEs, fixed.x — written by
// testdata/fit_lavaan.R (testdata/gen_lavaan.sh), each recording the
// lavaan release and R version that produced it. Every lavaan-convention
// feature (Model.Lavaan) is held to them within tolerance, never bit
// for bit: lavaan's nlminb stop (rel.tol 1e-10) and BLAS order differ
// from Fisher scoring's, and the fixtures are platform-independent.
type lavaanRef struct {
	Lavaan     string `json:"lavaan"`
	R          string `json:"r"`
	N          float64
	Converged  bool     `json:"converged"`
	Observed   []string `json:"observed"`
	Params     []lavaanParam
	Fit        map[string]*float64 `json:"fit"`
	ImpliedCov [][]float64         `json:"implied_cov"`
	ImpliedMu  []float64           `json:"implied_mean"`
	SampleMu   []float64           `json:"sample_mean"`
}

type lavaanParam struct {
	LHS     string   `json:"lhs"`
	Op      string   `json:"op"`
	RHS     string   `json:"rhs"`
	Label   string   `json:"label"`
	Free    bool     `json:"free"`
	Est     *float64 `json:"est"`
	SE      *float64 `json:"se"`
	Z       *float64 `json:"z"`
	P       *float64 `json:"p"`
	CILower *float64 `json:"ci_lower"`
	CIUpper *float64 `json:"ci_upper"`
	StdAll  *float64 `json:"std_all"`
}

func loadLavaanRef(t *testing.T, path string) lavaanRef {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r lavaanRef
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Lavaan == "" || !r.Converged {
		t.Fatalf("%s: no lavaan release recorded, or lavaan did not converge", path)
	}
	return r
}

// readCSVMoments returns the sample mean and the biased (divisor N)
// covariance of the named columns and the row count — lavaan's ML
// sample statistics.
func readCSVMoments(t *testing.T, path string, cols []string) ([]float64, *mat.SymDense, float64) {
	t.Helper()
	_, rows := readCSVColumns(t, path, cols)
	p, n := len(cols), float64(len(rows))
	mean := make([]float64, p)
	for _, r := range rows {
		for c := range r {
			mean[c] += r[c]
		}
	}
	for c := range mean {
		mean[c] /= n
	}
	s := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			acc := 0.0
			for _, r := range rows {
				acc += (r[i] - mean[i]) * (r[j] - mean[j])
			}
			s.SetSym(i, j, acc/n)
		}
	}
	return mean, s, n
}

// lavaanKey maps a lavaan parameter row to the estimate key: lavaan's
// "f =~ x" is the Lambda row "x ~ f"; "~1" rows have no rval.
func lavaanKey(p lavaanParam) string {
	switch p.Op {
	case OpMeasurement:
		return p.RHS + " " + OpRegression + " " + p.LHS
	case OpIntercept:
		return p.LHS + " " + OpIntercept
	}
	return p.LHS + " " + p.Op + " " + p.RHS
}

// relTol is |a − b| / |b| (absolute below |b| = 1e-8).
func relTol(a, b float64) float64 { return math.Abs(a-b) / math.Max(math.Abs(b), 1e-8) }

// lavaanTol are the parity tolerances: estimates, SEs, z and the Wald
// bounds relative; p-values absolute (a p of 1e-80 is held to 0). The
// residual is lavaan's stop, not the model: nlminb's default rel.tol
// 1e-10 leaves its gradient near 1e-6, so a weakly determined slope
// (brand_mx's recommend ~ wave, 0.0365, z 0.46) sits ~8e-5 relative
// from the optimum Fisher scoring reaches — and a tighter rel.tol makes
// nlminb report non-convergence.
const (
	lavaanEstTol = 1e-4
	lavaanFitTol = 1e-3
	lavaanPTol   = 1e-4
)

// checkAgainstLavaan holds a fit to a lavaan reference: every free
// lavaan row (estimate, SE, z, p, Wald interval, std.all) has the
// matching estimate row, every fixed visible row its value, and the
// fit's free-row count is lavaan's.
func checkAgainstLavaan(t *testing.T, fit *Fit, ref lavaanRef) {
	t.Helper()
	byKey := map[string]Estimate{}
	free := 0
	for _, e := range fit.Estimates {
		byKey[e.Key()] = e
		if e.LVal != e.RVal && e.Op == OpCovariance {
			byKey[e.RVal+" "+e.Op+" "+e.LVal] = e
		}
		if e.Free {
			free++
		}
	}
	for _, e := range fit.Defined {
		byKey[e.Key()] = e
	}
	refFree := 0
	worst := 0.0
	for _, p := range ref.Params {
		key := lavaanKey(p)
		e, ok := byKey[key]
		if !ok {
			if !p.Free && (fit.Model.isObsExo(p.LHS)) {
				continue // a fixed.x sample moment: hidden, as semopy hides it
			}
			t.Errorf("lavaan row %q has no estimate row", key)
			continue
		}
		if e.Free != p.Free {
			t.Errorf("%s: free = %v, lavaan %v", key, e.Free, p.Free)
			continue
		}
		check := func(what string, got float64, want *float64, tol float64) {
			if want == nil {
				t.Errorf("%s %s: lavaan reports none", key, what)
				return
			}
			d := relTol(got, *want)
			worst = math.Max(worst, d)
			if !(d <= tol) {
				t.Errorf("%s %s = %.10g, lavaan %.10g (rel %.2g)", key, what, got, *want, d)
			}
		}
		check("estimate", e.Value, p.Est, lavaanEstTol)
		check("std_estimate", e.StdValue, p.StdAll, lavaanEstTol)
		if p.Label != "" && e.Label != p.Label {
			t.Errorf("%s label = %q, lavaan %q", key, e.Label, p.Label)
		}
		// A := row is no free parameter, but carries the delta
		// method's SE, z, p and interval.
		if !p.Free && p.Op != OpDefined {
			continue
		}
		if p.Free {
			refFree++
		}
		check("std_err", e.SE, p.SE, lavaanEstTol)
		check("z_value", e.Z, p.Z, lavaanEstTol)
		check("ci_lower", e.CILower, p.CILower, lavaanEstTol)
		check("ci_upper", e.CIUpper, p.CIUpper, lavaanEstTol)
		if p.P == nil {
			t.Errorf("%s p_value: lavaan reports none", key)
		} else if !(math.Abs(e.P-*p.P) <= lavaanPTol) {
			t.Errorf("%s p_value = %.10g, lavaan %.10g", key, e.P, *p.P)
		}
	}
	if free != refFree {
		t.Errorf("free rows = %d, lavaan %d", free, refFree)
	}
	st := fit.Stats
	for name, got := range map[string]float64{
		"chisq": st.Chi2, "df": st.DoF, "pvalue": st.Chi2P, "baseline.chisq": st.Chi2Baseline,
		"baseline.df": st.DoFBaseline, "cfi": st.CFI, "tli": st.TLI, "nfi": st.NFI, "rmsea": st.RMSEA,
		"aic": st.AIC, "bic": st.BIC, "logl": st.LogLik, "gfi_lisrel": st.GFI, "agfi_lisrel": st.AGFI,
		"npar": float64(st.NParams), "fmin": fit.Objective / 2,
	} {
		want := ref.Fit[name]
		if want == nil {
			t.Errorf("fit measure %s: lavaan reports none", name)
			continue
		}
		if d := relTol(got, *want); !(d <= lavaanFitTol) {
			t.Errorf("fit measure %s = %.10g, lavaan %.10g (rel %.2g)", name, got, *want, d)
		}
	}
	t.Logf("worst relative estimate difference %.2g (lavaan %s)", worst, ref.Lavaan)
}

// orderTo returns the observed-variable order of ref mapped to m's.
func orderTo(t *testing.T, m *Model, names []string) []int {
	t.Helper()
	pos := map[string]int{}
	for i, v := range names {
		pos[v] = i
	}
	out := make([]int, len(m.Observed))
	for k, v := range m.Observed {
		i, ok := pos[v]
		if !ok {
			t.Fatalf("lavaan reference lacks observed variable %q", v)
		}
		out[k] = i
	}
	return out
}

var lavaanMeanCases = []struct {
	name, model string
	opt         ModelOptions
}{
	// meanstructure: true on a covariance-only description.
	{"brand_ms", "brand_ms", ModelOptions{MeanStructure: true}},
	// explicit ~ 1 lines (no option): a free latent mean, a fixed
	// zero intercept, equal labelled intercepts, an observed exogenous
	// covariate (fixed.x).
	{"brand_mx", "brand_mx", ModelOptions{}},
	// the same description with the option set too.
	{"brand_mx", "brand_mx", ModelOptions{MeanStructure: true}},
	// lavaan's marker rule (NA frees the first indicator, a labelled
	// one stays fixed at 1) and std.all of residual covariances
	// (observed and latent) over the residual variances.
	{"brand_res", "brand_res", ModelOptions{MeanStructure: true}},
}

// TestSEM_MeanStructureMatchesLavaan holds the mean-structure ML fit
// (FitMoments + ComputeStats under lavaan conventions) to lavaan's
// meanstructure = TRUE fit of the brand-survey fixture, under both
// derivative computations and both solvers at a converged setting.
func TestSEM_MeanStructureMatchesLavaan(t *testing.T) {
	for _, c := range lavaanMeanCases {
		ref := loadLavaanRef(t, "testdata/"+c.name+"_lavaan.json")
		desc, err := os.ReadFile("testdata/" + c.model + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModelWithOptions(string(desc), c.opt)
		if err != nil {
			t.Fatal(err)
		}
		if !m.MeanStructure || !m.Lavaan() {
			t.Fatalf("%s: MeanStructure %v Lavaan %v, want both", c.name, m.MeanStructure, m.Lavaan())
		}
		mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
		for _, opt := range []Options{
			{},
			{Information: InformationRank2},
			{Solver: SolverSLSQP, Ftol: 1e-14, MaxIter: 100000},
		} {
			t.Run(c.name+"/"+string(opt.Solver)+string(opt.Information), func(t *testing.T) {
				fit, err := FitMoments(m, s, s, mean, n, opt)
				if err != nil {
					t.Fatal(err)
				}
				if opt.Solver == "" && (fit.Options.Solver != SolverFisher || !fit.Converged) {
					t.Fatalf("default lavaan-convention solver %q converged %v, want fisher to the optimum", fit.Options.Solver, fit.Converged)
				}
				if err := ComputeStats(fit, string(desc)); err != nil {
					t.Fatal(err)
				}
				if fit.N != ref.N {
					t.Fatalf("n = %v, lavaan %v", fit.N, ref.N)
				}
				checkAgainstLavaan(t, fit, ref)
				ord := orderTo(t, m, ref.Observed)
				for a := range m.Observed {
					if d := relTol(fit.ImpliedMean[a], ref.ImpliedMu[ord[a]]); d > lavaanEstTol {
						t.Errorf("implied mean %s = %g, lavaan %g", m.Observed[a], fit.ImpliedMean[a], ref.ImpliedMu[ord[a]])
					}
					if d := relTol(fit.SampleMean[a], ref.SampleMu[ord[a]]); d > 1e-12 {
						t.Errorf("sample mean %s = %g, lavaan %g", m.Observed[a], fit.SampleMean[a], ref.SampleMu[ord[a]])
					}
					for b := range m.Observed {
						if d := relTol(fit.Implied.At(a, b), ref.ImpliedCov[ord[a]][ord[b]]); d > lavaanEstTol {
							t.Errorf("implied cov %s,%s = %g, lavaan %g", m.Observed[a], m.Observed[b], fit.Implied.At(a, b), ref.ImpliedCov[ord[a]][ord[b]])
						}
					}
				}
			})
		}
	}
}

// TestSEM_ParseIntercepts covers the `~ 1` syntax: free, fixed and
// labelled intercepts, an intercept beside predictors, the refusals,
// and the rows a mean structure adds (an observed exogenous mean is a
// hidden fixed.x moment).
func TestSEM_ParseIntercepts(t *testing.T) {
	desc := "f =~ x1 + x2 + x3\ny ~ 1 + f + w\nx1 ~ 0*1\nx2 ~ a*1\nx3 ~ a*1\nf ~ 1"
	if !HasIntercepts(desc) || HasIntercepts("f =~ x1 + x2") || HasIntercepts("f =~") {
		t.Fatal("HasIntercepts misreads a description")
	}
	m, err := NewModel(desc, false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.MeanStructure || !m.Lavaan() {
		t.Fatal("a ~ 1 statement must switch the mean structure and lavaan conventions on")
	}
	rows := map[string]Estimate{}
	for _, e := range SpecifiedRows(m) {
		rows[e.Key()] = e
	}
	for key, free := range map[string]bool{"y ~ f": true, "y ~ w": true, "y ~1": true, "x1 ~1": false, "x2 ~1": true, "x3 ~1": true, "f ~1": true} {
		e, ok := rows[key]
		if !ok || e.Free != free {
			t.Errorf("row %q: present %v free %v, want free %v", key, ok, e.Free, free)
		}
	}
	if rows["x2 ~1"].Param != "a" || rows["x3 ~1"].Param != "a" {
		t.Errorf("labelled intercepts: params %q %q, want a", rows["x2 ~1"].Param, rows["x3 ~1"].Param)
	}
	if e := rows["y ~1"]; e.Op != OpIntercept || e.RVal != "" || e.LVal != "y" {
		t.Errorf("intercept row %+v", e)
	}
	if _, ok := rows["w ~1"]; ok {
		t.Error("the observed exogenous mean (fixed.x) must be hidden, as its (co)variances are")
	}
	// Defaults: a latent mean is fixed to 0 unless a ~ 1 line frees it.
	ms, err := NewModelWithOptions("f =~ x1 + x2\ng =~ x3 + x4", ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	rows = map[string]Estimate{}
	for _, e := range SpecifiedRows(ms) {
		rows[e.Key()] = e
	}
	for key, free := range map[string]bool{"x1 ~1": true, "x4 ~1": true, "f ~1": false, "g ~1": false} {
		if e, ok := rows[key]; !ok || e.Free != free {
			t.Errorf("default row %q: present %v free %v, want free %v", key, ok, e.Free, free)
		}
	}
	for d, want := range map[string]string{
		"f =~ x1 + 1":            "belongs on a ~ statement",
		"a ~~ 1\nf =~ x1 + x2":   "belongs on a ~ statement",
		"f =~ x1 + x2\nz ~ 1":    "does not use",
		"f =~ x1 + x2\nx1 ~ 1*1": "",
	} {
		_, err := NewModel(d, false)
		if want == "" {
			if err != nil {
				t.Errorf("%q: %v", d, err)
			}
			continue
		}
		if err == nil || !contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", d, err, want)
		}
	}
}

// TestSEM_LegacyConventionsUnchanged pins the semopy path: a
// covariance-only description stays on semopy's conventions (no
// mean structure, SLSQP, no Wald interval), and FitMoments — the
// engine's one entry point — fits it bit for bit as FitCov, whatever
// mean it is handed. Numerical semopy parity is TestFit_MatchesSemopy.
func TestSEM_LegacyConventionsUnchanged(t *testing.T) {
	for _, tc := range parityCases {
		desc, err := os.ReadFile("testdata/" + tc.name + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewModel(string(desc), false)
		if err != nil {
			t.Fatal(err)
		}
		if m.Lavaan() || m.MeanStructure {
			t.Fatalf("%s: a covariance-only description switched to lavaan conventions", tc.name)
		}
		s, n := readCSVCov(t, "testdata/"+tc.data+"_data.csv", m.Observed)
		mean := make([]float64, len(m.Observed))
		for i := range mean {
			mean[i] = float64(i) + 0.5
		}
		a, err := FitCov(m, s, n, Options{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := FitMoments(m, s, s, mean, n, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if a.Options.Solver != SolverSLSQP || b.SampleMean != nil || b.ImpliedMean != nil {
			t.Fatalf("%s: solver %q, means %v", tc.name, b.Options.Solver, b.SampleMean)
		}
		same := func(x, y float64) bool {
			return math.Float64bits(x) == math.Float64bits(y) || (math.IsNaN(x) && math.IsNaN(y))
		}
		if a.Iterations != b.Iterations || !same(a.Objective, b.Objective) || len(a.Estimates) != len(b.Estimates) {
			t.Fatalf("%s: FitMoments differs from FitCov", tc.name)
		}
		for i, e := range b.Estimates {
			f := a.Estimates[i]
			if e.Key() != f.Key() || !same(e.Value, f.Value) || !same(e.SE, f.SE) || !same(e.Z, f.Z) || !same(e.P, f.P) {
				t.Fatalf("%s row %s: FitMoments differs from FitCov", tc.name, e.Key())
			}
			if !math.IsNaN(e.CILower) || !math.IsNaN(e.CIUpper) || e.Op == OpIntercept {
				t.Fatalf("%s row %s: a semopy fit carries a Wald interval or an intercept row", tc.name, e.Key())
			}
		}
	}
}
