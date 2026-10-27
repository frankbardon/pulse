package sem

import (
	"errors"
	"math"
	"slices"
	"sort"
	"strings"
	"testing"
)

// wlsGroupRef is a multi-group fit_lavaan_wlsmv.R reference
// (fit_lavaan_wlsmv_mg.R): every parameter row and free parameter carries
// its group label, the estimator intermediates are per group, vcov the
// joint one over lavaan's free parameters.
type wlsGroupRef struct {
	Lavaan  string `json:"lavaan"`
	Options struct {
		Estimator  string   `json:"estimator"`
		Ordered    []string `json:"ordered"`
		Group      string   `json:"group"`
		GroupEqual []string `json:"group_equal"`
	} `json:"options"`
	N          float64             `json:"n"`
	Labels     []string            `json:"group_labels"`
	Converged  bool                `json:"converged"`
	Params     []lavaanGroupParam  `json:"params"`
	Fit        map[string]*float64 `json:"fit"`
	FreeParams []string            `json:"free_params"`
	Groups     []struct {
		Label    string      `json:"label"`
		N        float64     `json:"n"`
		WLSNames []string    `json:"wls_names"`
		WLSObs   []float64   `json:"wls_obs"`
		WLSEst   []float64   `json:"wls_est"`
		WLSV     []float64   `json:"wls_v"`
		Gamma    [][]float64 `json:"gamma"`
		Delta    [][]float64 `json:"delta"`
	} `json:"groups"`
	VCov   [][]float64 `json:"vcov"`
	Scaled *struct {
		Stat           float64 `json:"stat"`
		TraceUGamma    float64 `json:"trace_ugamma"`
		TraceUGamma2   float64 `json:"trace_ugamma2"`
		ScalingFactor  float64 `json:"scaling_factor"`
		ShiftParameter float64 `json:"shift_parameter"`
	} `json:"scaled_shifted"`
}

// wlsGroupFixtures are the multi-group ordinal references: the brand
// survey's measurement model across the two waves, configural and under
// lavaan's threshold + loading invariance (the wave-2 scale factors and
// ordered intercepts free — the τ − μ term with μ ≠ 0), and the
// structural model with the numeric recommend outcome on the unequal
// waves (600 / 400) under thresholds, loadings and intercepts (the
// wave-2 latent means free).
var wlsGroupFixtures = []struct{ ref, data, model string }{
	{"brand_ordmg_configural", "brand", "brand_ord"},
	{"brand_ordmg_thload", "brand", "brand_ord"},
	{"brand_ordmg_scalar", "brand_uneq", "brand_ms"},
}

// ordinalGroupsFromCSV splits a CSV's rows by the group column (levels
// sorted, as the R script factors them) into OrdinalGroups over m's
// observed order, each ordered column coded by its sorted distinct
// values over the POOLED rows.
func ordinalGroupsFromCSV(t *testing.T, path, group string, m *Model) []OrdinalGroup {
	t.Helper()
	cols := readCSV(t, path)
	gv := cols[group]
	levels := slices.Clone(gv)
	slices.Sort(levels)
	levels = slices.Compact(levels)
	p := len(m.Observed)
	kinds, nlev := make([]OrdinalKind, p), make([]int, p)
	codes := make([][]float64, p)
	for v, name := range m.Observed {
		if !m.IsOrdered(name) {
			continue
		}
		c := slices.Clone(cols[name])
		slices.Sort(c)
		codes[v] = slices.Compact(c)
		kinds[v], nlev[v] = KindOrdered, len(codes[v])
	}
	out := make([]OrdinalGroup, len(levels))
	for g, lv := range levels {
		out[g] = OrdinalGroup{Label: formatLevel(lv), Level: g, In: TwoStepInput{Kinds: kinds, Levels: nlev}}
	}
	for i, x := range gv {
		g := slices.Index(levels, x)
		row := make([]float64, p)
		for v, name := range m.Observed {
			row[v] = cols[name][i]
			if codes[v] != nil {
				row[v] = float64(slices.Index(codes[v], row[v]))
			}
		}
		out[g].In.Rows = append(out[g].In.Rows, row)
	}
	return out
}

func fitWLSGroupFixture(t *testing.T, ref wlsGroupRef, data, model string) *GroupFit {
	t.Helper()
	m, err := NewGroupModel(readModel(t, "testdata/"+model+"_model.txt"),
		ModelOptions{Ordered: ref.Options.Ordered, GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	groups := ordinalGroupsFromCSV(t, "testdata/"+data+"_data.csv", ref.Options.Group, m)
	fit, err := FitOrdinalGroups(m, groups, Estimator(strings.ToLower(ref.Options.Estimator)), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !fit.Converged {
		t.Fatalf("not converged: %s", fit.Status)
	}
	return fit
}

// wlsGroupKey is a lavaan "lhs op rhs group" free-parameter key split
// into wlsKey form and its group label.
func wlsGroupKey(k string) (string, string) {
	i := strings.LastIndex(k, " ")
	return wlsKey(k[:i]), k[i+1:]
}

// TestWLSMV_GroupsMatchLavaan holds every multi-group DWLS / WLSMV
// reference to lavaan, as TestWLSMV_MatchesLavaan does a single group:
// per group WLS.obs, the weight, Γ, WLS.est at the estimate and Δ; the
// joint robust vcov (lavaan's free parameters, an equal one at its
// shared joint index); the scaled-shifted traces; every parameter row
// of every group (thresholds, scale factors, the delta residual
// variances, freed ordered intercepts and latent means) and every fit
// measure. Tolerances are inside the effort's (1e-4 relative on
// estimates and SEs, 1e-3 on fit indices): 5e-5 relative on estimates,
// WLS.est and Δ (the unequal-wave fit stops ~1e-5 off lavaan's on the
// numeric outcome's variance), 1e-6 on SEs, 1e-4 on fit measures; the
// observed maxima are logged.
func TestWLSMV_GroupsMatchLavaan(t *testing.T) {
	t.Parallel()

	for _, fx := range wlsGroupFixtures {
		t.Run(fx.ref, func(t *testing.T) {
			t.Parallel()

			var ref wlsGroupRef
			readJSON(t, "testdata/"+fx.ref+"_lavaan.json", &ref)
			if ref.Lavaan == "" || !ref.Converged || len(ref.Groups) < 2 {
				t.Fatal("no lavaan release recorded, lavaan did not converge, or not a multi-group fit")
			}
			fit := fitWLSGroupFixture(t, ref, fx.data, fx.model)
			worst := map[string]float64{}
			track := func(what string, got, want, scale float64) float64 {
				d := math.Abs(got-want) / scale
				if !(d <= worst[what]) {
					worst[what] = d
				}
				return d
			}
			if !slices.Equal(fit.Labels, ref.Labels) {
				t.Fatalf("groups %v, lavaan %v", fit.Labels, ref.Labels)
			}
			// lavaan's free parameters → (group, the group's local index).
			type loc struct{ g, k int }
			flocs := make([]loc, len(ref.FreeParams))
			for i, key := range ref.FreeParams {
				k, lab := wlsGroupKey(key)
				g := slices.Index(fit.Labels, lab)
				if g < 0 {
					t.Fatalf("free parameter %q: unknown group", key)
				}
				of := fit.Groups[g].Ordinal
				found := -1
				for c, fk := range of.FreeKeys {
					if wlsKey(fk) == k {
						found = c
					}
				}
				if found < 0 {
					t.Fatalf("lavaan free parameter %q has no counterpart (group %s: %v)", key, lab, of.FreeKeys)
				}
				flocs[i] = loc{g, found}
			}
			nlocal := 0
			for _, gf := range fit.Groups {
				nlocal += len(gf.Ordinal.FreeKeys)
			}
			if nlocal != len(ref.FreeParams) {
				t.Fatalf("%d per-group free entries, lavaan %d", nlocal, len(ref.FreeParams))
			}
			for g, rg := range ref.Groups {
				gf := fit.Groups[g]
				of := gf.Ordinal
				names := wlsStatNames(gf)
				pos := map[string]int{}
				for i, nm := range names {
					pos[nm] = i
				}
				idx := make([]int, len(rg.WLSNames))
				for i, nm := range rg.WLSNames {
					j, ok := pos[wlsKey(nm)]
					if !ok {
						t.Fatalf("group %s: lavaan statistic %q has no counterpart", rg.Label, nm)
					}
					idx[i] = j
				}
				for i, j := range idx {
					if d := track("wls.obs", of.Obs[j], rg.WLSObs[i], 1); d > 1e-6 {
						t.Errorf("[%s] WLS.obs %s = %.10g, lavaan %.10g", rg.Label, rg.WLSNames[i], of.Obs[j], rg.WLSObs[i])
					}
					if d := track("wls.v", of.Weight[j], rg.WLSV[i], rg.WLSV[i]); d > 1e-5 {
						t.Errorf("[%s] WLS.V %s = %.10g, lavaan %.10g", rg.Label, rg.WLSNames[i], of.Weight[j], rg.WLSV[i])
					}
					if d := track("wls.est", of.Est[j], rg.WLSEst[i], math.Max(1, math.Abs(rg.WLSEst[i]))); d > 5e-5 {
						t.Errorf("[%s] WLS.est %s = %.10g, lavaan %.10g", rg.Label, rg.WLSNames[i], of.Est[j], rg.WLSEst[i])
					}
					for c, fl := range flocs {
						want := rg.Delta[i][c]
						if fl.g != g {
							continue // another group's parameter (lavaan's 0 column)
						}
						if d := track("delta", of.Delta[j][fl.k], want, math.Max(1, math.Abs(want))); d > 5e-5 {
							t.Errorf("[%s] Delta[%s][%s] = %.10g, lavaan %.10g", rg.Label, rg.WLSNames[i], ref.FreeParams[c], of.Delta[j][fl.k], want)
						}
					}
				}
				nacov := of.Gamma.NACOV()
				for a, ja := range idx {
					for b, jb := range idx {
						sc := math.Sqrt(rg.Gamma[a][a] * rg.Gamma[b][b])
						if d := track("gamma", nacov[ja][jb], rg.Gamma[a][b], sc); d > 1e-5 {
							t.Errorf("[%s] Gamma[%s][%s] = %.10g, lavaan %.10g", rg.Label, rg.WLSNames[a], rg.WLSNames[b], nacov[ja][jb], rg.Gamma[a][b])
						}
					}
				}
			}
			joint := func(l loc) int { return fit.Groups[l.g].Ordinal.Joint[l.k] }
			for a, la := range flocs {
				for b, lb := range flocs {
					sc := math.Sqrt(ref.VCov[a][a] * ref.VCov[b][b])
					got := fit.VCov.At(joint(la), joint(lb))
					if d := track("vcov", got, ref.VCov[a][b], sc); d > 1e-4 {
						t.Errorf("vcov[%s][%s] = %.10g, lavaan %.10g", ref.FreeParams[a], ref.FreeParams[b], got, ref.VCov[a][b])
					}
				}
			}
			if ref.Scaled != nil {
				if d := track("trace", fit.TraceUG, ref.Scaled.TraceUGamma, ref.Scaled.TraceUGamma); d > 1e-4 {
					t.Errorf("tr(UΓ) = %.10g, lavaan %.10g", fit.TraceUG, ref.Scaled.TraceUGamma)
				}
				if d := track("trace", fit.TraceUG2, ref.Scaled.TraceUGamma2, ref.Scaled.TraceUGamma2); d > 1e-4 {
					t.Errorf("tr((UΓ)²) = %.10g, lavaan %.10g", fit.TraceUG2, ref.Scaled.TraceUGamma2)
				}
			}
			rows := map[string]Estimate{}
			for g, gf := range fit.Groups {
				for _, e := range gf.Estimates {
					rows[wlsKey(e.Key())+"@"+fit.Labels[g]] = e
				}
			}
			for _, e := range fit.Defined {
				rows[wlsKey(e.Key())+"@"] = e
			}
			seen := map[string]bool{}
			for _, p := range ref.Params {
				key := wlsKey(p.LHS+p.Op+p.RHS) + "@" + p.Group
				e, ok := rows[key]
				if !ok {
					t.Errorf("lavaan row %q has no counterpart", key)
					continue
				}
				seen[key] = true
				if e.Free != p.Free {
					t.Errorf("%s: free %v, lavaan %v", key, e.Free, p.Free)
				}
				check := func(what string, got float64, want *float64, tol float64) {
					w := refFloat(want)
					if !p.Free && (what == "se" || what == "z" || what == "p" || what == "ci") {
						return // lavaan reports 0 / NA for a fixed row's SE; the port NaN
					}
					if d := track(what, got, w, math.Max(1, math.Abs(w))); !(d <= tol) && !(math.IsNaN(got) && math.IsNaN(w)) {
						t.Errorf("%s %s = %.10g, lavaan %.10g", key, what, got, w)
					}
				}
				check("est", e.Value, p.Est, 5e-5)
				check("std_all", e.StdValue, p.StdAll, 5e-5)
				check("se", e.SE, p.SE, 1e-6)
				check("z", e.Z, p.Z, 1e-4)
				check("p", e.P, p.P, 1e-5)
				check("ci", e.CILower, p.CILower, 5e-5)
				check("ci", e.CIUpper, p.CIUpper, 5e-5)
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

// brandOrdinalGroups is the brand survey's two waves over the
// measurement model, ordered ratings, with group_equal geq and
// group_partial partial (wave 1 the reference).
func brandOrdinalGroups(t *testing.T, geq, partial []string) (*Model, []OrdinalGroup) {
	t.Helper()
	q9 := []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}
	m, err := NewGroupModel(readModel(t, "testdata/brand_ord_model.txt"), ModelOptions{Ordered: q9, GroupEqual: geq, GroupPartial: partial})
	if err != nil {
		t.Fatal(err)
	}
	groups := ordinalGroupsFromCSV(t, "testdata/brand_data.csv", "wave", m)
	groups[0].Reference = true
	return m, groups
}

// TestWLSMV_GroupBinaryScaleFixed: under group_equal thresholds a
// binary ordered variable's scale factor stays fixed at 1 outside the
// reference group (lavaan's ov_names_ord_notbinary), every other
// ordered variable's is free there and fixed in the reference.
func TestWLSMV_GroupBinaryScaleFixed(t *testing.T) {
	t.Parallel()

	m, groups := brandOrdinalGroups(t, []string{"thresholds", "loadings"}, nil)
	q1 := slices.Index(m.Observed, "q1")
	for g := range groups {
		for _, r := range groups[g].In.Rows {
			r[q1] = math.Min(r[q1], 1) // codes 0 / 1: binary
		}
		groups[g].In.Levels = slices.Clone(groups[g].In.Levels)
		groups[g].In.Levels[q1] = 2
	}
	fit, err := FitOrdinalGroups(m, groups, EstimatorWLSMV, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for g, gf := range fit.Groups {
		n := 0
		for _, e := range gf.Estimates {
			if e.Op != OpScale {
				continue
			}
			n++
			want := g == 1 && e.LVal != "q1"
			if e.Free != want || (!e.Free && e.Value != 1) {
				t.Errorf("group %s %s: free %v value %v, want free %v", fit.Labels[g], e.Key(), e.Free, e.Value, want)
			}
		}
		if n != 9 {
			t.Errorf("group %s: %d scale rows, want one per ordered variable", fit.Labels[g], n)
		}
	}
}

// TestWLSMV_GroupPartialThresholds: group_partial "x|tk" frees one
// threshold from group_equal thresholds (its own joint parameter per
// group), the others stay one value across groups; a threshold the
// data do not hold is a *GroupSpecError at the fit, a malformed one at
// specification, and group_equal thresholds without ordered variables
// is refused.
func TestWLSMV_GroupPartialThresholds(t *testing.T) {
	t.Parallel()

	_, groups := brandOrdinalGroups(t, nil, nil)
	m, _ := brandOrdinalGroups(t, []string{"thresholds", "loadings"}, []string{"q1 | t2"})
	fit, err := FitOrdinalGroups(m, groups, EstimatorDWLS, Options{})
	if err != nil {
		t.Fatal(err)
	}
	joint := func(g int, key string) int {
		of := fit.Groups[g].Ordinal
		k := slices.Index(of.FreeKeys, key)
		if k < 0 {
			t.Fatalf("group %d: no free %q", g, key)
		}
		return of.Joint[k]
	}
	if joint(0, "q1 | t2") == joint(1, "q1 | t2") {
		t.Error("q1 | t2 is shared across groups; group_partial frees it")
	}
	if joint(0, "q1 | t1") != joint(1, "q1 | t1") || joint(0, "s3 | t4") != joint(1, "s3 | t4") {
		t.Error("an equal threshold is not one joint parameter")
	}
	m9, _ := brandOrdinalGroups(t, []string{"thresholds"}, []string{"q1|t9"})
	var ge *GroupSpecError
	if _, err := FitOrdinalGroups(m9, groups, EstimatorDWLS, Options{}); !errors.As(err, &ge) || ge.Param != "group_partial" {
		t.Errorf("q1|t9 on a 5-category rating: %v, want a group_partial GroupSpecError", err)
	}
	q9 := []string{"q1", "q2", "q3", "v1", "v2", "v3", "s1", "s2", "s3"}
	desc := readModel(t, "testdata/brand_ord_model.txt")
	for _, bad := range []string{"q1|tx", "q1|t0", "zz|t1"} {
		if _, err := NewGroupModel(desc, ModelOptions{Ordered: q9, GroupEqual: []string{"thresholds"}, GroupPartial: []string{bad}}); !errors.As(err, &ge) {
			t.Errorf("group_partial %q: %v, want a GroupSpecError", bad, err)
		}
	}
	if _, err := NewGroupModel(desc, ModelOptions{GroupEqual: []string{"thresholds"}}); !errors.As(err, &ge) || ge.Value != "thresholds" {
		t.Errorf("ML group_equal thresholds: %v, want a GroupSpecError", err)
	}
}

// TestWLSMV_GroupEmptyCategory: the categories are the POOLED data's,
// so a category one wave lacks is that group's ReasonEmptyCategory
// refusal (lavaan's "some categories are empty in group g"), naming the
// group.
func TestWLSMV_GroupEmptyCategory(t *testing.T) {
	t.Parallel()

	m, groups := brandOrdinalGroups(t, nil, nil)
	v2 := slices.Index(m.Observed, "v2")
	for _, r := range groups[1].In.Rows {
		if r[v2] == 0 {
			r[v2] = 1
		}
	}
	_, err := FitOrdinalGroups(m, groups, EstimatorWLSMV, Options{})
	var oe *OrdinalError
	if !errors.As(err, &oe) || oe.Reason != ReasonEmptyCategory || oe.Group != 1 || oe.Var != v2 {
		t.Fatalf("err %v (%+v), want empty_category on v2 in group 1", err, oe)
	}
}
