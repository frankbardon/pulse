package sem

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
	"gonum.org/v1/gonum/mat"
)

// The references are semopy 2.3.11 fits (scripts/fit_semopy.py):
// *_semopy_default.json at semopy's defaults (SLSQP, ftol 1e-6, which
// stops short of the optimum) and *_semopy_tight.json at ftol 1e-14
// (at the optimum).
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

var statKeys = map[string]string{
	"DoF": "dof", "DoF Baseline": "dof_baseline", "chi2": "chi2", "chi2 p-value": "chi2_p_value",
	"chi2 Baseline": "chi2_baseline", "CFI": "cfi", "GFI": "gfi", "AGFI": "agfi", "NFI": "nfi",
	"TLI": "tli", "RMSEA": "rmsea", "AIC": "aic", "BIC": "bic", "LogLik": "loglik",
}

func newSEMPulse(t *testing.T) (*pulse.Pulse, afero.Fs) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{MatrixOps: []pulse.MatrixOpRegistration{Registration()}}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	return p, fsys
}

// runSEM imports csvFile into the instance and runs one MAT_LAB_SEM
// slot over the model's observed variables.
func runSEM(t *testing.T, p *pulse.Pulse, fsys afero.Fs, csvFile, desc string, opts map[string]any, mutate func(*types.Request)) *types.Response {
	t.Helper()
	b, err := os.ReadFile(csvFile)
	if err != nil {
		t.Fatal(err)
	}
	name := strings.ReplaceAll(csvFile, "/", "_")
	cohort := "imports/" + strings.TrimSuffix(name, ".csv") + ".pulse"
	if ok, _ := afero.Exists(fsys, cohort); !ok {
		if err := afero.WriteFile(fsys, name, b, 0o644); err != nil {
			t.Fatal(err)
		}
		imp, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: name})
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		cohort = imp.Path
	}
	m, err := NewModel(desc, false)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]any{"model": desc}
	for k, v := range opts {
		all[k] = v
	}
	params, _ := json.Marshal(all)
	req := &types.Request{
		Cohort:   &types.Cohort{Filename: cohort},
		Matrices: []types.MatrixSpec{{Type: TypeName, Name: "sem", Fields: m.Observed, Params: params}},
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp
}

func rel(a, b float64) float64 { return math.Abs(a-b) / math.Max(1, math.Abs(b)) }

var parityCases = []struct{ name, data string }{
	{"hier", "hier"},     // a synthetic three-level hierarchy: 13 factors, 57 observed, 126 free
	{"poldem", "poldem"}, // latent regressions + residual covariances (Bollen 1989)
	{"hz", "hz"},         // three covarying exogenous latents (Holzinger–Swineford)
	{"hz_ext", "hz"},     // equality labels, observed covariates, fixed and labelled covariances
}

// TestOperator_MatchesSemopy runs every configuration against the
// semopy reference it should reproduce: the default setup (a port of
// semopy's SLSQP) reproduces semopy's default fit, early stop included,
// to rounding; a converging setup reproduces the tight fit.
func TestOperator_MatchesSemopy(t *testing.T) {
	configs := []struct {
		name string
		opts map[string]any
		ref  string
		tol  float64
	}{
		{"default", nil, "default", 1e-9},
		{"slsqp_rank2", map[string]any{"information": "rank2"}, "default", 1e-9},
		{"slsqp_tight", map[string]any{"ftol": 1e-14, "max_iter": 100000}, "tight", 1e-6},
		{"fisher_dense", map[string]any{"solver": "fisher"}, "tight", 1e-6},
		{"fisher_rank2", map[string]any{"solver": "fisher", "information": "rank2"}, "tight", 1e-6},
	}
	p, fsys := newSEMPulse(t)
	for _, cfg := range configs {
		for _, tc := range parityCases {
			t.Run(cfg.name+"/"+tc.name, func(t *testing.T) {
				desc, err := os.ReadFile("testdata/" + tc.name + "_model.txt")
				if err != nil {
					t.Fatal(err)
				}
				var ref semopyRef
				b, err := os.ReadFile("testdata/" + tc.name + "_semopy_" + cfg.ref + ".json")
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(b, &ref); err != nil {
					t.Fatal(err)
				}
				resp := runSEM(t, p, fsys, "testdata/"+tc.data+"_data.csv", string(desc), cfg.opts, nil)
				checkAgainstSemopy(t, resp, ref, cfg.tol)
			})
		}
	}
}

func checkAgainstSemopy(t *testing.T, resp *types.Response, ref semopyRef, tol float64) {
	t.Helper()
	if len(resp.Matrices) != 1 {
		t.Fatalf("%d matrix results", len(resp.Matrices))
	}
	res := resp.Matrices[0]
	if res.Scalars["converged"] != 1 {
		t.Fatalf("did not converge: %v", res.Scalars)
	}
	est := res.Auxiliary["estimates"]
	got := map[string][]float64{}
	for i, k := range est.RowKeys {
		got[k] = est.Values[i]
	}
	if len(got) != len(ref.Params) {
		t.Errorf("%d estimate rows, semopy has %d", len(got), len(ref.Params))
	}
	for _, r := range ref.Params {
		k := r.LVal + " " + r.Op + " " + r.RVal
		row, ok := got[k]
		if !ok && r.Op == OpCovariance {
			row, ok = got[r.RVal+" ~~ "+r.LVal] // semopy orients exogenous-latent pairs by set order
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
		check("estimate", row[0], r.Est, tol)
		if r.Std != nil {
			check("std_estimate", row[1], *r.Std, tol)
		}
		if r.SE == nil {
			if !math.IsNaN(row[2]) {
				t.Errorf("%s: fixed in semopy, has an SE here", k)
			}
			continue
		}
		check("std_err", row[2], *r.SE, tol)
		check("z_value", row[3], *r.Z, 2*tol)
		if d := math.Abs(row[4] - *r.P); d > tol {
			t.Errorf("%s p = %g, semopy %g", k, row[4], *r.P)
		}
	}
	for sk, pk := range statKeys {
		w, g := ref.Stats[sk], res.Scalars[pk]
		if math.IsNaN(w) != math.IsNaN(g) || (!math.IsNaN(w) && rel(g, w) > tol) {
			t.Errorf("stat %s = %.12g, semopy %.12g", sk, g, w)
		}
	}
	if d := rel(res.Scalars["objective"], ref.Fun); d > tol {
		t.Errorf("objective %.14g, semopy %.14g", res.Scalars["objective"], ref.Fun)
	}
}

// TestRank2MatchesDense pins the rank-2 derivatives to the dense ones:
// gradient and information agree to rounding at the starting values.
func TestRank2MatchesDense(t *testing.T) {
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

func TestOperator_GroupedFitsEachBucket(t *testing.T) {
	p, fsys := newSEMPulse(t)
	desc, _ := os.ReadFile("testdata/hz_model.txt")
	resp := runSEM(t, p, fsys, "testdata/hz_data.csv", string(desc), nil, func(r *types.Request) {
		r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "sex"}}
		r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "sex", Label: "n"}}
	})
	if len(resp.Matrices) != 2 {
		t.Fatalf("%d results, want one per sex", len(resp.Matrices))
	}
	total := 0.0
	for _, m := range resp.Matrices {
		if m.Scalars["converged"] != 1 || m.Scalars["dof"] != 24 {
			t.Errorf("bucket %v: %v", m.GroupKey, m.Scalars)
		}
		total += m.Scalars["n"]
	}
	if total != 300 {
		t.Errorf("bucket n sum %v, want 300", total)
	}
}

func TestOperator_Refusals(t *testing.T) {
	p, fsys := newSEMPulse(t)
	b, _ := os.ReadFile("testdata/hz_data.csv")
	_ = afero.WriteFile(fsys, "hz.csv", b, 0o644)
	imp, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "hz.csv"})
	if err != nil {
		t.Fatal(err)
	}
	run := func(fields []string, params string) error {
		_, err := p.Process(context.Background(), &types.Request{
			Cohort:   &types.Cohort{Filename: imp.Path},
			Matrices: []types.MatrixSpec{{Type: TypeName, Name: "sem", Fields: fields, Params: json.RawMessage(params)}},
		})
		return err
	}
	for name, tc := range map[string]struct {
		fields []string
		params string
		want   string
	}{
		"bad syntax":     {[]string{"x1", "x2", "x3"}, `{"model":"visual => x1"}`, "unsupported syntax"},
		"unknown param":  {[]string{"x1", "x2", "x3"}, `{"modle":"visual =~ x1 + x2 + x3"}`, `did you mean "model"`},
		"missing member": {[]string{"x1", "x2"}, `{"model":"visual =~ x1 + x2 + x3"}`, "not matrix members: x3"},
		"bad solver":     {[]string{"x1", "x2", "x3"}, `{"solver":"newton"}`, `unknown solver "newton"`},
		"bad info":       {[]string{"x1", "x2", "x3"}, `{"information":"sparse"}`, `unknown information "sparse"`},
	} {
		err := run(tc.fields, tc.params)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	// No model: a one-factor model over the members.
	resp, err := p.Process(context.Background(), &types.Request{
		Cohort:   &types.Cohort{Filename: imp.Path},
		Matrices: []types.MatrixSpec{{Type: TypeName, Name: "sem", Fields: []string{"x1", "x2", "x3", "x4"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if s := resp.Matrices[0].Scalars; s["dof"] != 2 || s["converged"] != 1 {
		t.Errorf("default model: %v", s)
	}
}
