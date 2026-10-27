package sem

import (
	"math"
	"slices"
	"testing"
)

// ebmTol holds an EBM score to lavaan's mode (its EBM objective
// minimised to reltol 1e-15): the fits agree to ~1e-6 (TestWLSMV_*), and
// the mode is a smooth function of the parameters.
const ebmTol = 1e-5

// ebmDefaultTol holds it to lavPredict's own output, whose BFGS stop
// (reltol 1e-8) sits up to ~4e-4 off the mode on these fixtures.
const ebmDefaultTol = 1e-3

// ebmCases are the EBM references (predict_lavaan.R with ordered): the
// measurement model, mixed ordered / numeric indicators (recommend), the
// latent regressions with residual covariances, the mixed model across
// the waves under thresholds, loadings and intercepts, and an observed
// mediator (lavaan's dummy latents) single-group and across the waves.
var ebmCases = []struct{ name, model string }{
	{"brand_score_ord", "brand_ord"},
	{"brand_score_ordmx", "brand_ms"},
	{"brand_score_ordres", "brand_res"},
	{"brand_score_ordmg", "brand_ms"},
	{"brand_score_ordmed", "brand_ordmed"},
	{"brand_score_ordmedmg", "brand_ordmed"},
}

// ebmRef is lavaanScoreRef plus the ordinal fields.
type ebmRef struct {
	lavaanScoreRef
	Options struct {
		Ordered    []string `json:"ordered"`
		Group      string   `json:"group"`
		GroupEqual []string `json:"group_equal"`
	} `json:"options"`
	Groups []struct {
		Label      string      `json:"label"`
		N          int         `json:"n"`
		Rows       []int       `json:"rows"`
		Regression [][]float64 `json:"regression"`
		EBMDefault [][]float64 `json:"ebm_default"`
	} `json:"groups"`
}

// ebmFits fits ref's ordinal model to brand_data.csv as lavaan did and
// returns each group's model and fit and every data row over the model's
// observed order (ordered columns coded by their pooled sorted values),
// with each row's group index.
func ebmFits(t *testing.T, ref ebmRef, model string) ([]*Model, []*Fit, [][]float64, []int) {
	t.Helper()
	desc := readModel(t, "testdata/"+model+"_model.txt")
	cols := readCSV(t, "testdata/brand_data.csv")
	if ref.Options.Group == "" {
		m, err := NewModelWithOptions(desc, ModelOptions{Ordered: ref.Options.Ordered})
		if err != nil {
			t.Fatal(err)
		}
		fit := fitWLSFixture(t, wlsRef{Options: struct {
			Estimator string   `json:"estimator"`
			Ordered   []string `json:"ordered"`
			SE        string   `json:"se"`
		}{Estimator: "WLSMV", Ordered: ref.Options.Ordered}}, model)
		rows := codedRows(cols, m)
		return []*Model{fit.Model}, []*Fit{fit}, rows, make([]int, len(rows))
	}
	m, err := NewGroupModel(desc, ModelOptions{Ordered: ref.Options.Ordered, GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	groups := ordinalGroupsFromCSV(t, "testdata/brand_data.csv", ref.Options.Group, m)
	gf, err := FitOrdinalGroups(m, groups, EstimatorWLSMV, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !gf.Converged {
		t.Fatalf("not converged: %s", gf.Status)
	}
	gv := cols[ref.Options.Group]
	levels := slices.Clone(gv)
	slices.Sort(levels)
	levels = slices.Compact(levels)
	idx := make([]int, len(gv))
	for i, x := range gv {
		idx[i] = slices.Index(levels, x)
	}
	models := make([]*Model, len(gf.Groups))
	for g, f := range gf.Groups {
		models[g] = f.Model
	}
	return models, gf.Groups, codedRows(cols, m), idx
}

// codedRows are the CSV's rows over m's observed order, each ordered
// column coded by its sorted distinct values.
func codedRows(cols map[string][]float64, m *Model) [][]float64 {
	n := len(cols[m.Observed[0]])
	rows := make([][]float64, n)
	for i := range rows {
		rows[i] = make([]float64, len(m.Observed))
	}
	for v, name := range m.Observed {
		col := cols[name]
		var levels []float64
		if m.IsOrdered(name) {
			levels = slices.Clone(col)
			slices.Sort(levels)
			levels = slices.Compact(levels)
		}
		for i, x := range col {
			rows[i][v] = x
			if levels != nil {
				rows[i][v] = float64(slices.Index(levels, x))
			}
		}
	}
	return rows
}

// TestSEMScore_EBMMatchesLavaan holds every recorded row's EBM score on
// every latent, per group, to lavaan's mode (1e-5) and to lavPredict's
// own output (1e-3: its BFGS stop).
func TestSEMScore_EBMMatchesLavaan(t *testing.T) {
	t.Parallel()

	for _, c := range ebmCases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var ref ebmRef
			readJSON(t, "testdata/"+c.name+"_lavaan.json", &ref)
			if ref.Lavaan == "" || len(ref.Options.Ordered) == 0 || len(ref.Groups) == 0 {
				t.Fatal("not an ordinal score reference")
			}
			models, fits, rows, idx := ebmFits(t, ref, c.model)
			worst := 0.0
			for g, gr := range ref.Groups {
				params, ok := fits[g].Params()
				if !ok || params.Thresholds == nil {
					t.Fatal("the ordinal fit carries no thresholds")
				}
				n := 0
				for r := range rows {
					if idx[r] == g {
						n++
					}
				}
				if n != gr.N {
					t.Fatalf("group %q has %d rows, lavaan %d", gr.Label, n, gr.N)
				}
				for l, lat := range ref.Latents {
					sc, err := NewEBMScorer(models[g], params, lat)
					if err != nil {
						t.Fatalf("%s: %v", lat, err)
					}
					for k, r := range gr.Rows {
						if idx[r] != g {
							t.Fatalf("row %d is not in group %q", r, gr.Label)
						}
						got := sc.Score(rows[r])
						d := math.Abs(got - gr.Regression[k][l])
						worst = math.Max(worst, d)
						if !(d <= ebmTol) {
							t.Errorf("group %q row %d %s = %.10g, lavaan's mode %.10g (diff %.2g)", gr.Label, r, lat, got, gr.Regression[k][l], d)
						}
						if d := math.Abs(got - gr.EBMDefault[k][l]); !(d <= ebmDefaultTol) {
							t.Errorf("group %q row %d %s = %.10g, lavPredict %.10g (diff %.2g)", gr.Label, r, lat, got, gr.EBMDefault[k][l], d)
						}
					}
				}
			}
			t.Logf("worst |Δ| to lavaan's mode: %.3g", worst)
		})
	}
}

// TestSEMScore_EBMIsTheMode: at the score the posterior's gradient
// vanishes and every coordinate is a minimum (a step either way raises
// the objective) — the scorer solves the problem it states.
func TestSEMScore_EBMIsTheMode(t *testing.T) {
	t.Parallel()

	var ref ebmRef
	readJSON(t, "testdata/brand_score_ordmx_lavaan.json", &ref)
	models, fits, rows, _ := ebmFits(t, ref, "brand_ms")
	params, _ := fits[0].Params()
	sc, err := NewEBMScorer(models[0], params, "quality")
	if err != nil {
		t.Fatal(err)
	}
	n := sc.nLat
	g := make([]float64, n)
	h := make([][]float64, n)
	for a := range h {
		h[a] = make([]float64, n)
	}
	for r := 0; r < 60; r++ {
		eta := make([]float64, n)
		if steps := sc.Mode(rows[r], eta); steps == 0 || steps >= ebmMaxIter {
			t.Fatalf("row %d: %d Newton steps (the start is never the mode; the cap is never reached)", r, steps)
		}
		f := sc.eval(rows[r], eta, g, h)
		for a := 0; a < n; a++ {
			if math.Abs(g[a]) > 1e-9 {
				t.Errorf("row %d: gradient %d = %.3g at the score", r, a, g[a])
			}
			for _, d := range []float64{-1e-4, 1e-4} {
				x := slices.Clone(eta)
				x[a] += d
				if sc.objective(rows[r], x) <= f {
					t.Errorf("row %d: a step %g on latent %d does not raise the objective", r, d, a)
				}
			}
		}
	}
}

// TestSEMScore_EBMRefusals: a non-positive residual variance (an
// observed mediator's Ψ_dd included) and a fit without thresholds are
// refused, never scored; an observed mediator alone scores (a dummy
// latent, no coordinate of the solve).
func TestSEMScore_EBMRefusals(t *testing.T) {
	t.Parallel()

	q := []string{"q1", "q2", "q3"}
	m, err := NewModelWithOptions("quality =~ q1 + q2 + q3\nrecommend ~ quality\ns1 ~ recommend", ModelOptions{Ordered: append(q, "s1")})
	if err != nil {
		t.Fatal(err)
	}
	p := zeroParams(m)
	p.Thresholds = make([][]float64, len(m.Observed))
	for i, v := range m.Observed {
		if m.IsOrdered(v) {
			p.Thresholds[i] = []float64{0}
		}
	}
	sc, err := NewEBMScorer(m, p, "quality")
	if err != nil {
		t.Fatalf("observed mediator: %v, want a scorer", err)
	}
	if sc.nLat != 1 {
		t.Errorf("observed mediator: %d solve coordinates, want 1 (the latent alone)", sc.nLat)
	}
	d := m.innerIndex["recommend"]
	p.Psi[d][d] = 0
	if _, err := NewEBMScorer(m, p, "quality"); err != ErrEBMResidualVariance {
		t.Errorf("zero mediator residual variance: %v, want ErrEBMResidualVariance", err)
	}
	m, err = NewModelWithOptions("quality =~ q1 + q2 + q3", ModelOptions{Ordered: q})
	if err != nil {
		t.Fatal(err)
	}
	p = zeroParams(m)
	if _, err := NewEBMScorer(m, p, "quality"); err == nil {
		t.Error("no thresholds: scored")
	}
	p.Thresholds = [][]float64{{0}, {0}, {0}}
	p.Theta[1][1] = 0
	if _, err := NewEBMScorer(m, p, "quality"); err != ErrEBMResidualVariance {
		t.Errorf("zero residual variance: %v, want ErrEBMResidualVariance", err)
	}
	p.Theta[1][1] = 0.5
	p.Psi[0][0] = 0
	if _, err := NewEBMScorer(m, p, "quality"); err != ErrScoreSingular {
		t.Errorf("zero latent variance: %v, want ErrScoreSingular", err)
	}
	p.Psi[0][0] = 1
	if _, err := NewEBMScorer(m, p, "quality"); err != nil {
		t.Errorf("a valid rule: %v", err)
	}
}

// zeroParams are m's parameter shapes, every residual and latent
// variance 1.
func zeroParams(m *Model) Params {
	sq := func(n int) [][]float64 {
		out := make([][]float64, n)
		for i := range out {
			out[i] = make([]float64, n)
			out[i][i] = 1
		}
		return out
	}
	nObs, nIn := len(m.Observed), len(m.Inner)
	lam := make([][]float64, nObs)
	for i := range lam {
		lam[i] = make([]float64, nIn)
		lam[i][0] = 1
	}
	beta := sq(nIn)
	for i := range beta {
		beta[i][i] = 0
	}
	return Params{Lambda: lam, Beta: beta, Psi: sq(nIn), Theta: sq(nObs), Nu: make([]float64, nObs),
		Alpha: make([]float64, nIn), SampleMean: make([]float64, nObs)}
}
