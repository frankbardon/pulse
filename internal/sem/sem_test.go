package sem

import (
	"encoding/csv"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// The references are semopy 2.3.11 fits (testdata/fit_semopy.py):
// *_semopy_default.json at semopy's defaults (SLSQP, ftol 1e-6, which
// stops short of the optimum) and *_semopy_tight.json at ftol 1e-14 (at
// the optimum). Regenerate with testdata/gen_semopy.sh.
type semopyRef struct {
	Fun    float64            `json:"fun"`
	Stats  map[string]float64 `json:"stats"`
	Params []struct {
		LVal string   `json:"lval"`
		Op   string   `json:"op"`
		RVal string   `json:"rval"`
		Est  float64  `json:"est"`
		Std  *float64 `json:"std"`
		SE   *float64 `json:"se"`
		Z    *float64 `json:"z"`
		P    *float64 `json:"p"`
	} `json:"params"`
}

var parityCases = []struct{ name, data string }{
	{"hier", "hier"},     // a synthetic three-level hierarchy: 13 factors, 57 observed, 126 free
	{"poldem", "poldem"}, // latent regressions + residual covariances (Bollen 1989)
	{"hz", "hz"},         // three covarying exogenous latents (Holzinger–Swineford)
	{"hz_ext", "hz"},     // equality labels, observed covariates, fixed and labelled covariances
}

// readCSVCov returns the biased (divisor N) covariance of the named
// columns, as semopy's utils.cov computes it, and the row count.
func readCSVCov(t *testing.T, path string, cols []string) (*mat.SymDense, float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	idx := map[string]int{}
	for i, h := range recs[0] {
		idx[h] = i
	}
	p, n := len(cols), len(recs)-1
	x := make([][]float64, n)
	mean := make([]float64, p)
	for r := 0; r < n; r++ {
		x[r] = make([]float64, p)
		for c, name := range cols {
			v, err := strconv.ParseFloat(recs[r+1][idx[name]], 64)
			if err != nil {
				t.Fatal(err)
			}
			x[r][c] = v
			mean[c] += v
		}
	}
	for c := range mean {
		mean[c] /= float64(n)
	}
	s := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := i; j < p; j++ {
			acc := 0.0
			for r := 0; r < n; r++ {
				acc += (x[r][i] - mean[i]) * (x[r][j] - mean[j])
			}
			s.SetSym(i, j, acc/float64(n))
		}
	}
	return s, float64(n)
}

func loadRef(t *testing.T, path string) semopyRef {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var ref semopyRef
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func rel(a, b float64) float64 { return math.Abs(a-b) / math.Max(1, math.Abs(b)) }

func checkAgainstSemopy(t *testing.T, fit *Fit, ref semopyRef, tol float64) {
	t.Helper()
	if !fit.Converged {
		t.Fatalf("did not converge: %s", fit.Status)
	}
	got := map[string]Estimate{}
	for _, e := range fit.Estimates {
		got[e.Key()] = e
	}
	if len(got) != len(ref.Params) {
		t.Errorf("%d estimate rows, semopy has %d", len(got), len(ref.Params))
	}
	for _, r := range ref.Params {
		k := r.LVal + " " + r.Op + " " + r.RVal
		e, ok := got[k]
		if !ok && r.Op == OpCovariance {
			e, ok = got[r.RVal+" ~~ "+r.LVal] // semopy orients exogenous-latent pairs by Python set order
		}
		if !ok {
			t.Errorf("missing row %q", k)
			continue
		}
		check := func(what string, g, w, tol float64) {
			if d := rel(g, w); d > tol {
				t.Errorf("%s %s = %.12g, semopy %.12g (rel %.1e)", k, what, g, w, d)
			}
		}
		check("estimate", e.Value, r.Est, tol)
		if r.Std != nil {
			check("std_estimate", e.StdValue, *r.Std, tol)
		}
		if r.SE == nil {
			if e.Free {
				t.Errorf("%s: fixed in semopy, free here", k)
			}
			continue
		}
		check("std_err", e.SE, *r.SE, tol)
		check("z_value", e.Z, *r.Z, 2*tol)
		if d := math.Abs(e.P - *r.P); d > tol {
			t.Errorf("%s p = %g, semopy %g", k, e.P, *r.P)
		}
	}
	st := fit.Stats
	for name, v := range map[string]float64{
		"DoF": st.DoF, "DoF Baseline": st.DoFBaseline, "chi2": st.Chi2, "chi2 p-value": st.Chi2P,
		"chi2 Baseline": st.Chi2Baseline, "CFI": st.CFI, "GFI": st.GFI, "AGFI": st.AGFI, "NFI": st.NFI,
		"TLI": st.TLI, "RMSEA": st.RMSEA, "AIC": st.AIC, "BIC": st.BIC, "LogLik": st.LogLik,
	} {
		w := ref.Stats[name]
		if math.IsNaN(w) != math.IsNaN(v) || (!math.IsNaN(w) && rel(v, w) > tol) {
			t.Errorf("stat %s = %.12g, semopy %.12g", name, v, w)
		}
	}
	if d := rel(fit.Objective, ref.Fun); d > tol {
		t.Errorf("objective %.14g, semopy %.14g", fit.Objective, ref.Fun)
	}
}

// TestRank2MatchesDense pins the rank-2 derivatives to the dense ones:
// gradient and information agree to rounding off the starting values.
func TestRank2MatchesDense(t *testing.T) {
	t.Parallel()

	desc, _ := os.ReadFile("testdata/hz_ext_model.txt")
	m, err := NewModel(string(desc), false)
	if err != nil {
		t.Fatal(err)
	}
	s := mat.NewSymDense(len(m.Observed), nil)
	for i := range m.Observed { // any PD matrix will do
		for j := i; j < len(m.Observed); j++ {
			v := 0.3 / float64(1+j-i)
			if i == j {
				v = 1.5 + 0.1*float64(i)
			}
			s.SetSym(i, j, v)
		}
	}
	st := newState(m, s)
	_, ld, _ := invLogDet(s)
	st.sInvLogDet = ld
	x := st.startValues()
	for i := range x {
		x[i] += 0.05 * float64(i%7) // off the starting point
	}
	st.info = InformationDense
	_, gd, hd, err := st.derivatives(x, true)
	if err != nil {
		t.Fatal(err)
	}
	st.info = InformationRank2
	_, gr, hr, err := st.derivatives(x, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := range gd {
		if rel(gr[i], gd[i]) > 1e-12 {
			t.Errorf("g[%d] rank2 %g dense %g", i, gr[i], gd[i])
		}
		for j := range gd {
			if rel(hr.At(i, j), hd.At(i, j)) > 1e-12 {
				t.Errorf("H[%d,%d] rank2 %g dense %g", i, j, hr.At(i, j), hd.At(i, j))
			}
		}
	}
}

func TestParse_Refusals(t *testing.T) {
	t.Parallel()

	for desc, want := range map[string]string{
		"":                     "no statements",
		"# only a comment":     "no statements",
		"visual => x1":         "unsupported syntax",
		"visual =~ x1 + ":      "unsupported syntax",
		"START(0.5) visual":    "unsupported syntax",
		"a ~~ b\nb =~ x1 + x2": "mixes inner and output",
	} {
		_, err := NewModel(desc, false)
		if err == nil || !contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", desc, err, want)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
