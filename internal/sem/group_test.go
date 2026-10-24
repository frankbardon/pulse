package sem

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// lavaanGroupRef is a multi-group lavaan reference (fit_lavaan.R with a
// group column): every parameter row carries its group label, and the
// moments, n and chi-square are per group in level order.
type lavaanGroupRef struct {
	Lavaan    string              `json:"lavaan"`
	N         []float64           `json:"n"`
	NTotal    float64             `json:"n_total"`
	Converged bool                `json:"converged"`
	Labels    []string            `json:"group_labels"`
	Observed  []string            `json:"observed"`
	Params    []lavaanGroupParam  `json:"params"`
	Fit       map[string]*float64 `json:"fit"`
	Chi2Group []float64           `json:"chi2_group"`
	Groups    []struct {
		Label      string      `json:"label"`
		ImpliedCov [][]float64 `json:"implied_cov"`
		ImpliedMu  []float64   `json:"implied_mean"`
		SampleMu   []float64   `json:"sample_mean"`
	} `json:"groups"`
}

type lavaanGroupParam struct {
	lavaanParam
	Group string `json:"group"`
}

func loadLavaanGroupRef(t *testing.T, path string) lavaanGroupRef {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r lavaanGroupRef
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	if r.Lavaan == "" || !r.Converged || len(r.Labels) < 2 {
		t.Fatalf("%s: no lavaan release recorded, lavaan did not converge, or not a multi-group fit", path)
	}
	return r
}

// readCSVGroupMoments splits a CSV's rows by the group column's value
// (levels as the strings lavaan labels them, in the order labels gives)
// and returns each group's ML moments over cols.
func readCSVGroupMoments(t *testing.T, path, group string, labels []string, cols []string) []GroupMoments {
	t.Helper()
	all := append([]string{group}, cols...)
	_, rows := readCSVColumns(t, path, all)
	out := make([]GroupMoments, len(labels))
	for g, lab := range labels {
		var sub [][]float64
		for _, r := range rows {
			if formatLevel(r[0]) == lab {
				sub = append(sub, r[1:])
			}
		}
		mean, s := moments(sub)
		out[g] = GroupMoments{Label: lab, S: s, Raw: s, Mean: mean, N: float64(len(sub))}
	}
	return out
}

func formatLevel(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func moments(rows [][]float64) ([]float64, *mat.SymDense) {
	p, n := len(rows[0]), float64(len(rows))
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
	return mean, s
}

var lavaanGroupCases = []struct{ name, data, model string }{
	// configural: every parameter free per group, equal groups.
	{"brand_mg_configural", "brand", "brand_ms"},
	// unequal groups (600 / 400), a misfit (RMSEA > 0, CFI < 1) and a
	// label shared within and across groups.
	{"brand_mg_misfit", "brand_uneq", "brand_mgmis"},
}

// TestSEM_MultiGroupMatchesLavaan holds the joint multi-group fit
// (FitGroups) to lavaan's group = "wave" fit: every group's estimates,
// SEs, z, p, Wald bounds, std.all and moments, the per-group
// chi-squares and the joint fit measures — under both derivative
// computations and both solvers.
func TestSEM_MultiGroupMatchesLavaan(t *testing.T) {
	for _, c := range lavaanGroupCases {
		ref := loadLavaanGroupRef(t, "testdata/"+c.name+"_lavaan.json")
		desc, err := os.ReadFile("testdata/" + c.model + "_model.txt")
		if err != nil {
			t.Fatal(err)
		}
		m, err := NewGroupModel(string(desc), ModelOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if !m.MeanStructure || !m.Lavaan() {
			t.Fatal("a multi-group model must carry a mean structure under lavaan conventions")
		}
		groups := readCSVGroupMoments(t, "testdata/"+c.data+"_data.csv", "wave", ref.Labels, m.Observed)
		for _, opt := range []Options{
			{},
			{Information: InformationRank2},
			{Solver: SolverSLSQP, Ftol: 1e-14, MaxIter: 100000},
		} {
			t.Run(c.name+"/"+string(opt.Solver)+string(opt.Information), func(t *testing.T) {
				gf, err := FitGroups(m, groups, opt)
				if err != nil {
					t.Fatal(err)
				}
				if opt.Solver == "" && (gf.Options.Solver != SolverFisher || !gf.Converged) {
					t.Fatalf("default solver %q converged %v, want fisher to the optimum", gf.Options.Solver, gf.Converged)
				}
				if gf.N != ref.NTotal {
					t.Fatalf("n = %v, lavaan %v", gf.N, ref.NTotal)
				}
				ord := orderTo(t, m, ref.Observed)
				for g, lab := range ref.Labels {
					fit := gf.Groups[g]
					if gf.Labels[g] != lab || fit.N != ref.N[g] {
						t.Fatalf("group %d: label %q n %v, lavaan %q %v", g, gf.Labels[g], fit.N, lab, ref.N[g])
					}
					sub := lavaanRef{Lavaan: ref.Lavaan}
					for _, p := range ref.Params {
						if p.Group == lab {
							sub.Params = append(sub.Params, p.lavaanParam)
						}
					}
					checkEstimatesAgainstLavaan(t, "group "+lab, fit, sub)
					if d := relTol(gf.Chi2Group[g], ref.Chi2Group[g]); d > lavaanFitTol {
						t.Errorf("group %s chi2 = %g, lavaan %g", lab, gf.Chi2Group[g], ref.Chi2Group[g])
					}
					rg := ref.Groups[g]
					for a := range m.Observed {
						if d := relTol(fit.ImpliedMean[a], rg.ImpliedMu[ord[a]]); d > lavaanEstTol {
							t.Errorf("group %s implied mean %s = %g, lavaan %g", lab, m.Observed[a], fit.ImpliedMean[a], rg.ImpliedMu[ord[a]])
						}
						if d := relTol(fit.SampleMean[a], rg.SampleMu[ord[a]]); d > 1e-12 {
							t.Errorf("group %s sample mean %s = %g, lavaan %g", lab, m.Observed[a], fit.SampleMean[a], rg.SampleMu[ord[a]])
						}
						for b := range m.Observed {
							if d := relTol(fit.Implied.At(a, b), rg.ImpliedCov[ord[a]][ord[b]]); d > lavaanEstTol {
								t.Errorf("group %s implied cov %s,%s = %g, lavaan %g", lab, m.Observed[a], m.Observed[b], fit.Implied.At(a, b), rg.ImpliedCov[ord[a]][ord[b]])
							}
						}
					}
				}
				checkFitAgainstLavaan(t, gf.Stats, gf.Objective, ref.Fit)
			})
		}
	}
}

// TestSEM_MultiGroupSharedLabel pins the label rule: a labelled
// parameter is one value in every group (lavaan's reading), an
// unlabelled one is free per group.
func TestSEM_MultiGroupSharedLabel(t *testing.T) {
	desc, err := os.ReadFile("testdata/brand_mgmis_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewGroupModel(string(desc), ModelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	groups := readCSVGroupMoments(t, "testdata/brand_uneq_data.csv", "wave", []string{"1", "2"}, m.Observed)
	gf, err := FitGroups(m, groups, Options{})
	if err != nil {
		t.Fatal(err)
	}
	val := func(g int, key string) Estimate {
		for _, e := range gf.Groups[g].Estimates {
			if e.Key() == key {
				return e
			}
		}
		t.Fatalf("group %d has no row %q", g, key)
		return Estimate{}
	}
	for _, key := range []string{"v2 ~ value", "s2 ~ value"} {
		a, b := val(0, key), val(1, key)
		if a.Value != b.Value || a.Label != "l" || b.Label != "l" {
			t.Errorf("%s: groups %g / %g labels %q / %q, want one shared value labelled l", key, a.Value, b.Value, a.Label, b.Label)
		}
	}
	if a, b := val(0, "v3 ~ value"), val(1, "v3 ~ value"); a.Value == b.Value || a.Label != "" {
		t.Errorf("v3 ~ value: groups %g / %g label %q, want free per group and unlabelled", a.Value, b.Value, a.Label)
	}
	if _, err := FitGroups(m, groups[:1], Options{}); err == nil || !strings.Contains(err.Error(), "two groups") {
		t.Errorf("one group: err %v, want a two-group refusal", err)
	}
}

// checkEstimatesAgainstLavaan is checkAgainstLavaan's parameter half.
func checkEstimatesAgainstLavaan(t *testing.T, where string, fit *Fit, ref lavaanRef) {
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
	refFree := 0
	for _, p := range ref.Params {
		key := lavaanKey(p)
		e, ok := byKey[key]
		if !ok {
			if !p.Free && fit.Model.isObsExo(p.LHS) {
				continue
			}
			t.Errorf("%s: lavaan row %q has no estimate row", where, key)
			continue
		}
		if e.Free != p.Free {
			t.Errorf("%s %s: free = %v, lavaan %v", where, key, e.Free, p.Free)
			continue
		}
		check := func(what string, got float64, want *float64) {
			if want == nil {
				t.Errorf("%s %s %s: lavaan reports none", where, key, what)
				return
			}
			if d := relTol(got, *want); !(d <= lavaanEstTol) {
				t.Errorf("%s %s %s = %.10g, lavaan %.10g (rel %.2g)", where, key, what, got, *want, d)
			}
		}
		check("estimate", e.Value, p.Est)
		check("std_estimate", e.StdValue, p.StdAll)
		if !p.Free {
			continue
		}
		refFree++
		check("std_err", e.SE, p.SE)
		check("z_value", e.Z, p.Z)
		check("ci_lower", e.CILower, p.CILower)
		check("ci_upper", e.CIUpper, p.CIUpper)
		if p.P == nil || !(math.Abs(e.P-*p.P) <= lavaanPTol) {
			t.Errorf("%s %s p_value = %.10g, lavaan %v", where, key, e.P, p.P)
		}
		if p.Label != "" && e.Label != p.Label {
			t.Errorf("%s %s label = %q, lavaan %q", where, key, e.Label, p.Label)
		}
	}
	if free != refFree {
		t.Errorf("%s: free rows = %d, lavaan %d", where, free, refFree)
	}
}

// checkFitAgainstLavaan is checkAgainstLavaan's fit-measure half.
func checkFitAgainstLavaan(t *testing.T, st Stats, objective float64, ref map[string]*float64) {
	t.Helper()
	for name, got := range map[string]float64{
		"chisq": st.Chi2, "df": st.DoF, "pvalue": st.Chi2P, "baseline.chisq": st.Chi2Baseline,
		"baseline.df": st.DoFBaseline, "cfi": st.CFI, "tli": st.TLI, "nfi": st.NFI, "rmsea": st.RMSEA,
		"aic": st.AIC, "bic": st.BIC, "logl": st.LogLik, "gfi_lisrel": st.GFI, "agfi_lisrel": st.AGFI,
		"npar": float64(st.NParams), "fmin": objective / 2,
	} {
		want := ref[name]
		if want == nil {
			t.Errorf("fit measure %s: lavaan reports none", name)
			continue
		}
		if d := relTol(got, *want); !(d <= lavaanFitTol) {
			t.Errorf("fit measure %s = %.10g, lavaan %.10g (rel %.2g)", name, got, *want, d)
		}
	}
}
