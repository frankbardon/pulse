package sem

import (
	"errors"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// wlsRef is a fit_lavaan_wlsmv.R reference: a lavaan DWLS / WLSMV fit
// (delta parameterization) with every estimator intermediate.
type wlsRef struct {
	Lavaan  string `json:"lavaan"`
	Options struct {
		Estimator string   `json:"estimator"`
		Ordered   []string `json:"ordered"`
		SE        string   `json:"se"`
	} `json:"options"`
	N          float64             `json:"n"`
	Converged  bool                `json:"converged"`
	Observed   []string            `json:"observed"`
	Params     []lavaanParam       `json:"params"`
	Fit        map[string]*float64 `json:"fit"`
	FreeParams []string            `json:"free_params"`
	WLSNames   []string            `json:"wls_names"`
	WLSObs     []float64           `json:"wls_obs"`
	WLSEst     []float64           `json:"wls_est"`
	WLSV       []float64           `json:"wls_v"`
	Gamma      [][]float64         `json:"gamma"`
	Delta      [][]float64         `json:"delta"`
	VCov       [][]float64         `json:"vcov"`
	Scaled     *struct {
		Stat           float64 `json:"stat"`
		TraceUGamma    float64 `json:"trace_ugamma"`
		TraceUGamma2   float64 `json:"trace_ugamma2"`
		ScalingFactor  float64 `json:"scaling_factor"`
		ShiftParameter float64 `json:"shift_parameter"`
	} `json:"scaled_shifted"`
}

// wlsFixtures are the lavaan ordinal references: the brand survey's
// measurement model under WLSMV and DWLS, the structural model with the
// numeric recommend outcome (polyserial correlations, a numeric mean and
// variance), the mediation model with defined parameters, and lavaan's
// single-group rules (a freed first loading, a residual covariance
// between two ordered ratings, endogenous factors).
var wlsFixtures = []struct{ ref, model string }{
	{"brand_ord_wlsmv", "brand_ord"},
	{"brand_ord_dwls", "brand_ord"},
	{"brand_ordms_wlsmv", "brand_ms"},
	{"brand_orddef_wlsmv", "brand_def"},
	{"brand_ordres_wlsmv", "brand_res"},
}

// fitWLSFixture fits a reference's model to the brand survey with the
// reference's estimator: the ordered columns coded 0..K-1 by their
// sorted distinct values (lavaan's ordered()).
func fitWLSFixture(t *testing.T, ref wlsRef, model string) *Fit {
	t.Helper()
	desc := readModel(t, "testdata/"+model+"_model.txt")
	m, err := NewModelWithOptions(desc, ModelOptions{Ordered: ref.Options.Ordered})
	if err != nil {
		t.Fatal(err)
	}
	cols := readCSV(t, "testdata/brand_data.csv")
	in := TwoStepInput{Kinds: make([]OrdinalKind, len(m.Observed)), Levels: make([]int, len(m.Observed))}
	n := len(cols[m.Observed[0]])
	in.Rows = make([][]float64, n)
	for i := range in.Rows {
		in.Rows[i] = make([]float64, len(m.Observed))
	}
	for v, name := range m.Observed {
		col := cols[name]
		if !m.IsOrdered(name) {
			for i, x := range col {
				in.Rows[i][v] = x
			}
			continue
		}
		levels := slices.Clone(col)
		slices.Sort(levels)
		levels = slices.Compact(levels)
		in.Kinds[v], in.Levels[v] = KindOrdered, len(levels)
		for i, x := range col {
			in.Rows[i][v] = float64(slices.Index(levels, x))
		}
	}
	fit, err := FitOrdinal(m, in, Estimator(strings.ToLower(ref.Options.Estimator)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !fit.Converged {
		t.Fatalf("not converged: %s", fit.Status)
	}
	return fit
}

// wlsKey normalises a row key for matching: no spaces, a loading as its
// Lambda row (lavaanKey), and a ~~ row's two names sorted (the port
// lists a covariance in its own orientation).
func wlsKey(k string) string {
	k = strings.ReplaceAll(k, " ", "")
	if a, b, ok := strings.Cut(k, "=~"); ok {
		return b + "~" + a // lavaan's "f =~ x" is the Lambda row "x ~ f"
	}
	if a, b, ok := strings.Cut(k, "~~"); ok && a > b {
		return b + "~~" + a
	}
	return k
}

// wlsStatNames are the port's WLS.obs entries named as lavaan names
// them ("x|t1", "x~1" for a numeric mean, "x~~x", "a~~b").
func wlsStatNames(fit *Fit) []string {
	s := fit.Ordinal.Stats
	l := s.Layout()
	out := make([]string, l.Size)
	obs := fit.Model.Observed
	for v, kind := range s.Kinds {
		if kind == KindOrdered {
			for k := 0; k < l.NTh[v]; k++ {
				out[l.Th[v]+k] = obs[v] + "|t" + strconv.Itoa(k+1)
			}
			continue
		}
		out[l.Th[v]] = obs[v] + "~1"
		out[l.VarIdx[v]] = obs[v] + "~~" + obs[v]
	}
	for k, p := range s.Pairs {
		out[l.Cor+k] = wlsKey(obs[p.J] + "~~" + obs[p.I])
	}
	return out
}

func refFloat(p *float64) float64 {
	if p == nil {
		return math.NaN()
	}
	return *p
}

// TestWLSMV_MatchesLavaan holds every DWLS / WLSMV reference to lavaan:
// each intermediate first (WLS.obs, the weight 1/diag(Γ), Γ itself,
// WLS.est at the estimate, Δ, the robust vcov and the scaled-shifted
// traces), then every parameter row (estimate, robust SE, z, p, Wald
// interval, std.all — thresholds, the delta-parameterization residual
// variances and := rows included) and every fit measure. Tolerances
// are tighter than the effort's (1e-4 relative on estimates and SEs,
// 1e-3 on fit indices) — 1e-5 on estimates, 1e-6 on SEs, 1e-4 on fit
// measures — so a slip of the (N − 1)/N factors bites; the observed
// maxima (≤ 2.3e-6 on estimates, 2.2e-5 on fit measures) are logged.
func TestWLSMV_MatchesLavaan(t *testing.T) {
	for _, fx := range wlsFixtures {
		t.Run(fx.ref, func(t *testing.T) {
			var ref wlsRef
			readJSON(t, "testdata/"+fx.ref+"_lavaan.json", &ref)
			if ref.Lavaan == "" || !ref.Converged {
				t.Fatal("no lavaan release recorded, or lavaan did not converge")
			}
			fit := fitWLSFixture(t, ref, fx.model)
			of := fit.Ordinal
			// Statistic coordinates: lavaan's name → the port's index.
			names := wlsStatNames(fit)
			pos := map[string]int{}
			for i, nm := range names {
				pos[nm] = i
			}
			idx := make([]int, len(ref.WLSNames))
			for i, nm := range ref.WLSNames {
				j, ok := pos[wlsKey(nm)]
				if !ok {
					t.Fatalf("lavaan statistic %q has no counterpart (port: %v)", nm, names)
				}
				idx[i] = j
			}
			if len(idx) != len(names) {
				t.Fatalf("%d statistics, lavaan %d", len(names), len(idx))
			}
			worst := map[string]float64{}
			track := func(what string, got, want, scale float64) float64 {
				d := math.Abs(got-want) / scale
				if d > worst[what] {
					worst[what] = d
				}
				return d
			}
			for i, j := range idx {
				if d := track("wls.obs", of.Obs[j], ref.WLSObs[i], 1); d > 1e-6 {
					t.Errorf("WLS.obs %s = %.10g, lavaan %.10g", ref.WLSNames[i], of.Obs[j], ref.WLSObs[i])
				}
				if d := track("wls.v", of.Weight[j], ref.WLSV[i], ref.WLSV[i]); d > 1e-5 {
					t.Errorf("WLS.V %s = %.10g, lavaan %.10g", ref.WLSNames[i], of.Weight[j], ref.WLSV[i])
				}
				if d := track("wls.est", of.Est[j], ref.WLSEst[i], 1); d > 1e-5 {
					t.Errorf("WLS.est %s = %.10g, lavaan %.10g", ref.WLSNames[i], of.Est[j], ref.WLSEst[i])
				}
			}
			nacov := of.Gamma.NACOV()
			for a, ja := range idx {
				for b, jb := range idx {
					sc := math.Sqrt(ref.Gamma[a][a] * ref.Gamma[b][b])
					if d := track("gamma", nacov[ja][jb], ref.Gamma[a][b], sc); d > 1e-5 {
						t.Errorf("Gamma[%s][%s] = %.10g, lavaan %.10g", ref.WLSNames[a], ref.WLSNames[b], nacov[ja][jb], ref.Gamma[a][b])
					}
				}
			}
			// Free-parameter coordinates.
			fpos := map[string]int{}
			for k, key := range of.FreeKeys {
				fpos[wlsKey(key)] = k
			}
			if len(of.FreeKeys) != len(ref.FreeParams) {
				t.Fatalf("%d free parameters %v, lavaan %d %v", len(of.FreeKeys), of.FreeKeys, len(ref.FreeParams), ref.FreeParams)
			}
			fidx := make([]int, len(ref.FreeParams))
			for i, key := range ref.FreeParams {
				k, ok := fpos[wlsKey(key)]
				if !ok {
					t.Fatalf("lavaan free parameter %q has no counterpart (port: %v)", key, of.FreeKeys)
				}
				fidx[i] = k
			}
			for i, j := range idx {
				for c, k := range fidx {
					if d := track("delta", of.Delta[j][k], ref.Delta[i][c], 1); d > 1e-5 {
						t.Errorf("Delta[%s][%s] = %.10g, lavaan %.10g", ref.WLSNames[i], ref.FreeParams[c], of.Delta[j][k], ref.Delta[i][c])
					}
				}
			}
			for a, ka := range fidx {
				for b, kb := range fidx {
					sc := math.Sqrt(ref.VCov[a][a] * ref.VCov[b][b])
					if d := track("vcov", of.VCov.At(ka, kb), ref.VCov[a][b], sc); d > 1e-4 {
						t.Errorf("vcov[%s][%s] = %.10g, lavaan %.10g", ref.FreeParams[a], ref.FreeParams[b], of.VCov.At(ka, kb), ref.VCov[a][b])
					}
				}
			}
			if ref.Scaled != nil {
				if d := track("trace", of.TraceUG, ref.Scaled.TraceUGamma, ref.Scaled.TraceUGamma); d > 1e-4 {
					t.Errorf("tr(UΓ) = %.10g, lavaan %.10g", of.TraceUG, ref.Scaled.TraceUGamma)
				}
				if d := track("trace", of.TraceUG2, ref.Scaled.TraceUGamma2, ref.Scaled.TraceUGamma2); d > 1e-4 {
					t.Errorf("tr((UΓ)²) = %.10g, lavaan %.10g", of.TraceUG2, ref.Scaled.TraceUGamma2)
				}
			}
			// The parameter table, row for row.
			rows := map[string]Estimate{}
			for _, e := range append(append([]Estimate(nil), fit.Estimates...), fit.Defined...) {
				rows[wlsKey(e.Key())] = e
			}
			seen := map[string]bool{}
			for _, p := range ref.Params {
				if p.Op == "~*~" {
					continue // the delta parameterization's scale factors (all 1)
				}
				key := wlsKey(p.LHS + p.Op + p.RHS)
				if p.Op == ":=" {
					key = wlsKey(p.LHS + ":=" + p.RHS)
				}
				e, ok := rows[key]
				if !ok {
					t.Errorf("lavaan row %q has no counterpart", key)
					continue
				}
				seen[key] = true
				if p.Op != ":=" && e.Free != p.Free {
					t.Errorf("%s: free %v, lavaan %v", key, e.Free, p.Free)
				}
				check := func(what string, got float64, want *float64, tol float64) {
					w := refFloat(want)
					if !p.Free && p.Op != ":=" && (what == "se" || what == "z" || what == "p" || what == "ci") {
						return // lavaan reports 0 / NA for a fixed row's SE; the port NaN
					}
					if d := track(what, got, w, math.Max(1, math.Abs(w))); !(d <= tol) && !(math.IsNaN(got) && math.IsNaN(w)) {
						t.Errorf("%s %s = %.10g, lavaan %.10g", key, what, got, w)
					}
				}
				check("est", e.Value, p.Est, 1e-5)
				check("std_all", e.StdValue, p.StdAll, 1e-5)
				check("se", e.SE, p.SE, 1e-6)
				check("z", e.Z, p.Z, 1e-4)
				check("p", e.P, p.P, 1e-5)
				check("ci", e.CILower, p.CILower, 1e-5)
				check("ci", e.CIUpper, p.CIUpper, 1e-5)
			}
			for key, e := range rows {
				if !seen[key] && e.Op != OpThreshold {
					t.Errorf("the port's row %q has no lavaan counterpart", key)
				}
			}
			st := fit.Stats
			measures := map[string]float64{
				"npar": float64(st.NParams), "fmin": fit.Objective / 2, "chisq": st.Chi2, "df": st.DoF,
				"pvalue": st.Chi2P, "baseline.chisq": st.Chi2Baseline, "baseline.df": st.DoFBaseline,
				"cfi": st.CFI, "tli": st.TLI, "rmsea": st.RMSEA, "nfi": st.NFI, "gfi_lisrel": st.GFI, "agfi_lisrel": st.AGFI,
				"logl": st.LogLik, "aic": st.AIC, "bic": st.BIC, "ntotal": fit.N,
			}
			if sc := st.Scaled; sc != nil {
				for k, v := range map[string]float64{
					"chisq.scaled": sc.Chi2, "df.scaled": st.DoF, "pvalue.scaled": sc.Chi2P,
					"chisq.scaling.factor": sc.ScalingFactor, "baseline.chisq.scaled": sc.Chi2Baseline,
					"baseline.chisq.scaling.factor": sc.BaselineScalingFactor,
					"cfi.scaled":                    sc.CFI, "tli.scaled": sc.TLI, "rmsea.scaled": sc.RMSEA,
				} {
					measures[k] = v
				}
				if d := track("fit", sc.ShiftParameter, ref.Scaled.ShiftParameter, math.Max(1, math.Abs(ref.Scaled.ShiftParameter))); d > 1e-4 {
					t.Errorf("shift parameter = %.10g, lavaan %.10g", sc.ShiftParameter, ref.Scaled.ShiftParameter)
				}
			} else if ref.Options.Estimator == "WLSMV" {
				t.Error("a WLSMV fit carries no scaled test")
			}
			keys := make([]string, 0, len(ref.Fit))
			for k := range ref.Fit {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				got, ok := measures[k]
				if !ok {
					t.Errorf("fit measure %s not compared", k)
					continue
				}
				w := refFloat(ref.Fit[k])
				if math.IsNaN(w) || math.IsNaN(got) {
					if math.IsNaN(w) != math.IsNaN(got) {
						t.Errorf("%s = %v, lavaan %v", k, got, w)
					}
					continue
				}
				if d := track("fit", got, w, math.Max(1, math.Abs(w))); d > 1e-4 {
					t.Errorf("%s = %.10g, lavaan %.10g", k, got, w)
				}
			}
			t.Logf("max deviations: %v", worst)
		})
	}
}

// readModel reads a model description file.
func readModel(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestWLSMV_OrdinalSpecRefusals: an ordered name the model does not
// observe, an ordered variable with an outgoing arrow, an observed
// exogenous covariate (lavaan's conditional.x) and an intercept or
// residual-variance line on an ordered variable are each a typed
// *OrdinalSpecError naming the variable.
func TestWLSMV_OrdinalSpecRefusals(t *testing.T) {
	base := "f =~ x1 + x2 + x3\n"
	for _, c := range []struct {
		desc    string
		ordered []string
		reason  string
		param   string
	}{
		{base, []string{"x1", "z"}, OrdinalSpecNotObserved, "ordered"},
		{base + "y ~ x1\n", []string{"x1"}, OrdinalSpecNotOutput, "ordered"},
		{base + "x3 ~ w\n", []string{"x1"}, OrdinalSpecExogenous, "model"},
		{base + "x2 ~ 1\n", []string{"x1", "x2"}, OrdinalSpecIntercept, "model"},
		{base + "x2 ~~ x2\n", []string{"x2"}, OrdinalSpecVariance, "model"},
	} {
		_, err := NewModelWithOptions(c.desc, ModelOptions{Ordered: c.ordered})
		var oe *OrdinalSpecError
		if !errors.As(err, &oe) || oe.Reason != c.reason || oe.Param != c.param || oe.Var == "" {
			t.Errorf("%q ordered %v: %v, want an OrdinalSpecError %s on %s", c.desc, c.ordered, err, c.reason, c.param)
		}
	}
	m, err := NewModelWithOptions(base+"x1 ~~ x2\ny ~ f\n", ModelOptions{Ordered: []string{"x1", "x2", "x3"}})
	if err != nil {
		t.Fatal(err)
	}
	if !m.Lavaan() || !m.MeanStructure || !slices.Equal(m.Ordered(), []string{"x1", "x2", "x3"}) {
		t.Errorf("ordinal model: lavaan %v, means %v, ordered %v", m.Lavaan(), m.MeanStructure, m.Ordered())
	}
	for _, e := range SpecifiedRows(m) {
		switch {
		case e.Op == OpCovariance && e.LVal == e.RVal && m.IsOrdered(e.LVal), e.Op == OpIntercept && m.IsOrdered(e.LVal):
			if e.Free {
				t.Errorf("%s is free; an ordered variable's residual variance and intercept are fixed", e.Key())
			}
		case e.Key() == "x1 ~~ x2" || e.Key() == "y ~~ y" || e.Key() == "y ~1":
			if !e.Free {
				t.Errorf("%s is fixed", e.Key())
			}
		}
	}
}

// TestWLSMV_TwoStepRefusalPassesThrough: FitOrdinal hands a two-step
// refusal back as the typed *OrdinalError, and refuses a data kind
// that disagrees with the model's ordered set or a non-ordinal
// estimator.
func TestWLSMV_TwoStepRefusalPassesThrough(t *testing.T) {
	m, err := NewModelWithOptions("f =~ x1 + x2 + x3", ModelOptions{Ordered: []string{"x1", "x2", "x3"}})
	if err != nil {
		t.Fatal(err)
	}
	in := TwoStepInput{Kinds: []OrdinalKind{KindOrdered, KindOrdered, KindOrdered}, Levels: []int{2, 2, 2}}
	for i := 0; i < 40; i++ {
		in.Rows = append(in.Rows, []float64{float64(i % 2), float64((i / 2) % 2), 0})
	}
	_, err = FitOrdinal(m, in, EstimatorWLSMV, Options{})
	var oe *OrdinalError
	if !errors.As(err, &oe) || oe.Reason != ReasonConstant || oe.Var != 2 {
		t.Errorf("constant column: %v, want OrdinalError constant on column 2", err)
	}
	in.Kinds[0] = KindNumeric
	if _, err := FitOrdinal(m, in, EstimatorWLSMV, Options{}); err == nil || errors.As(err, &oe) {
		t.Errorf("kind mismatch: %v, want a plain error", err)
	}
	if _, err := FitOrdinal(m, in, EstimatorML, Options{}); err == nil {
		t.Error("estimator ml: want an error")
	}
}
