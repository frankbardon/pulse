package sem

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"gonum.org/v1/gonum/mat"
)

// TestSEM_DefinedMatchesLavaan holds the := rows (estimate, delta-method
// SE, z, p, Wald interval, std.all) and the rest of the fit to lavaan's
// parameterEstimates on the brand-survey mediation: indirect, total and
// share effects plus the grammar rows, with a mean structure and without
// one — the covariance-only description switches to lavaan conventions
// once it defines a parameter.
func TestSEM_DefinedMatchesLavaan(t *testing.T) {
	for _, c := range []struct {
		name string
		opt  ModelOptions
	}{
		{"brand_def_ms", ModelOptions{MeanStructure: true}},
		{"brand_def_cov", ModelOptions{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ref := loadLavaanRef(t, "testdata/"+c.name+"_lavaan.json")
			desc, err := os.ReadFile("testdata/brand_def_model.txt")
			if err != nil {
				t.Fatal(err)
			}
			m, err := NewModelWithOptions(string(desc), c.opt)
			if err != nil {
				t.Fatal(err)
			}
			if !m.Lavaan() || m.MeanStructure != c.opt.MeanStructure || m.DefinedCount() != 5 {
				t.Fatalf("Lavaan %v MeanStructure %v defined %d", m.Lavaan(), m.MeanStructure, m.DefinedCount())
			}
			mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
			fit, err := FitMoments(m, s, s, mean, n, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if fit.Options.Solver != SolverFisher || !fit.Converged {
				t.Fatalf("solver %q converged %v, want lavaan's fisher to the optimum", fit.Options.Solver, fit.Converged)
			}
			if err := ComputeStats(fit, string(desc)); err != nil {
				t.Fatal(err)
			}
			nDef := 0
			for _, p := range ref.Params {
				if p.Op == OpDefined {
					nDef++
				}
			}
			if nDef != 5 || len(fit.Defined) != nDef {
				t.Fatalf("defined rows %d, lavaan %d", len(fit.Defined), nDef)
			}
			for _, e := range fit.Defined {
				if e.Op != OpDefined || e.Free || e.Label != e.LVal || e.Param != e.LVal {
					t.Errorf("defined row %+v: want op :=, not free, label and parameter the name", e)
				}
			}
			checkAgainstLavaan(t, fit, ref)
		})
	}
}

// TestSEM_DefinedMultiGroupMatchesLavaan holds a joint scalar-invariant
// fit's := rows to lavaan's: a path shared across waves times a latent
// mean change, per-wave labels (c(bs1, bs2)), a defined name read by a
// later line and a ratio — every SE from the joint covariance.
func TestSEM_DefinedMultiGroupMatchesLavaan(t *testing.T) {
	ref := loadLavaanGroupRef(t, "testdata/brand_mg_defined_lavaan.json")
	desc, err := os.ReadFile("testdata/brand_mgdef_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewGroupModel(string(desc), ModelOptions{GroupEqual: ref.Options.GroupEqual})
	if err != nil {
		t.Fatal(err)
	}
	groups := readCSVGroupMoments(t, "testdata/brand_data.csv", "wave", ref.Labels, m.Observed)
	for _, opt := range []Options{{}, {Information: InformationRank2}} {
		gf, err := FitGroups(m, groups, opt)
		if err != nil {
			t.Fatal(err)
		}
		for g, lab := range ref.Labels {
			sub := lavaanRef{Lavaan: ref.Lavaan}
			for _, p := range ref.Params {
				if p.Group == lab {
					sub.Params = append(sub.Params, p.lavaanParam)
				}
			}
			checkEstimatesAgainstLavaan(t, "group "+lab, gf.Groups[g], sub)
		}
		checkFitAgainstLavaan(t, gf.Stats, gf.Objective, ref.Fit)
		var defs []lavaanParam
		for _, p := range ref.Params {
			if p.Op == OpDefined {
				if p.Group != "" {
					t.Errorf("lavaan := row %s carries group %q", p.LHS, p.Group)
				}
				defs = append(defs, p.lavaanParam)
			}
		}
		if len(defs) != 4 || len(gf.Defined) != len(defs) {
			t.Fatalf("defined rows %d, lavaan %d", len(gf.Defined), len(defs))
		}
		for i, p := range defs {
			e := gf.Defined[i]
			if e.Key() != lavaanKey(p) {
				t.Fatalf("defined row %d key %q, lavaan %q", i, e.Key(), lavaanKey(p))
			}
			for what, pair := range map[string][2]float64{
				"estimate": {e.Value, *p.Est}, "std_err": {e.SE, *p.SE}, "z_value": {e.Z, *p.Z},
				"ci_lower": {e.CILower, *p.CILower}, "ci_upper": {e.CIUpper, *p.CIUpper}, "std_estimate": {e.StdValue, *p.StdAll},
			} {
				if d := relTol(pair[0], pair[1]); !(d <= lavaanEstTol) {
					t.Errorf("%s %s = %.10g, lavaan %.10g (rel %.2g)", e.Key(), what, pair[0], pair[1], d)
				}
			}
			if !(math.Abs(e.P-*p.P) <= lavaanPTol) {
				t.Errorf("%s p_value = %g, lavaan %g", e.Key(), e.P, *p.P)
			}
		}
	}
}

// TestSEM_DefinedGradientExact checks the dual-number gradient of every
// grammar form against a central difference, and the evaluation order
// rules R gives the operators (-a^2 = -(a^2), a^b^c = a^(b^c), a^-1).
func TestSEM_DefinedGradientExact(t *testing.T) {
	x := []float64{0.7, 1.3, -0.4}
	labels := map[string]labelValue{"a": {free: 0}, "b": {free: 1}, "c": {free: 2}, "k": {free: -1, value: 2.5}}
	cases := map[string]float64{
		"a*b + c":                     0.7*1.3 - 0.4,
		"a - b - c":                   0.7 - 1.3 + 0.4,
		"a / b / k":                   0.7 / 1.3 / 2.5,
		"-a^2":                        -0.49,
		"2^3^2":                       512,
		"b^-1":                        1 / 1.3,
		"sqrt(b) * exp(c) - log(a)":   math.Sqrt(1.3)*math.Exp(-0.4) - math.Log(0.7),
		"abs(c) + abs(a)":             1.1,
		"(a + b) * (b - c) / 1.5e-1":  (2.0) * (1.7) / 0.15,
		"a^b":                         math.Pow(0.7, 1.3),
		"+a * -k":                     -1.75,
		"k * (a + .5) - 1e2 * c ^ 2.": 2.5*1.2 - 100*0.16,
	}
	for src, want := range cases {
		d, err := parseDefinedLine("v := " + src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		got := evalDefined([]*definedParam{d}, labels, x, nil, waldZ975)[0]
		if relTol(got.Value, want) > 1e-12 {
			t.Errorf("%q = %.15g, want %.15g", src, got.Value, want)
		}
		env := func(xs []float64) func(string) dual {
			return func(n string) dual {
				lv := labels[n]
				if lv.free < 0 {
					return dual{v: lv.value}
				}
				g := make([]float64, len(xs))
				g[lv.free] = 1
				return dual{v: xs[lv.free], d: g}
			}
		}
		g := d.expr.eval(env(x), len(x))
		for k := range x {
			h := 1e-6
			up, dn := append([]float64(nil), x...), append([]float64(nil), x...)
			up[k] += h
			dn[k] -= h
			num := (d.expr.eval(env(up), 3).v - d.expr.eval(env(dn), 3).v) / (2 * h)
			an := 0.0
			if g.d != nil {
				an = g.d[k]
			}
			if math.Abs(an-num) > 1e-6*math.Max(1, math.Abs(num)) {
				t.Errorf("%q d/dx%d = %.10g, central difference %.10g", src, k, an, num)
			}
		}
	}
	// The delta method: SE² = gᵀVg.
	d, _ := parseDefinedLine("p := a*b")
	v := mat.NewSymDense(3, []float64{0.04, 0.01, 0, 0.01, 0.09, 0, 0, 0, 1})
	e := evalDefined([]*definedParam{d}, labels, x, v, waldZ975)[0]
	g := []float64{1.3, 0.7, 0}
	want := math.Sqrt(g[0]*g[0]*0.04 + 2*g[0]*g[1]*0.01 + g[1]*g[1]*0.09)
	if relTol(e.SE, want) > 1e-14 || relTol(e.Z, e.Value/want) > 1e-14 ||
		relTol(e.CILower, e.Value-waldZ975*want) > 1e-14 || relTol(e.P, 2*(1-normCDF(e.Value/want))) > 1e-12 {
		t.Errorf("delta method %+v, want SE %g", e, want)
	}
}

// TestSEM_DefinedUndefinedAtEstimate: a definition that is not finite
// at the estimate (a division by zero, the log of a negative value)
// nulls every figure of its row — and of every row reading it — while
// the others keep theirs.
func TestSEM_DefinedUndefinedAtEstimate(t *testing.T) {
	x := []float64{0.7, 0}
	labels := map[string]labelValue{"a": {free: 0, std: 0.5}, "b": {free: 1, std: 0}}
	var defs []*definedParam
	for _, l := range []string{"r := a/b", "q := r + a", "l := log(-a)", "ok := a*2"} {
		d, err := parseDefinedLine(l)
		if err != nil {
			t.Fatal(err)
		}
		defs = append(defs, d)
	}
	v := mat.NewSymDense(2, []float64{0.04, 0, 0, 0.09})
	rows := evalDefined(defs, labels, x, v, waldZ975)
	for _, e := range rows[:3] {
		for what, f := range map[string]float64{"estimate": e.Value, "std": e.StdValue, "se": e.SE, "z": e.Z, "p": e.P, "lo": e.CILower, "hi": e.CIUpper} {
			if !math.IsNaN(f) {
				t.Errorf("%s %s = %g, want NaN (undefined at the estimate)", e.LVal, what, f)
			}
		}
	}
	if ok := rows[3]; ok.Value != 1.4 || relTol(ok.SE, 0.4) > 1e-14 || ok.StdValue != 1 {
		t.Errorf("a defined row beside an undefined one: %+v", ok)
	}
}

// TestSEM_DefinedRefusals covers the coded refusals: a reference to no
// label, a forward / self / cyclic reference, a reused name, malformed
// expressions and unsupported functions, and lavaan's constraint lines
// (==, <, >), refused as out of scope.
func TestSEM_DefinedRefusals(t *testing.T) {
	base := "f =~ x1 + a*x2 + b*x3\n"
	for desc, reason := range map[string]string{
		base + "d := a*z":                 DefinedReasonUnknownLabel,
		base + "d := e + a\ne := a*b":     DefinedReasonForwardRef,
		base + "d := d + a":               DefinedReasonForwardRef,
		base + "d := e\ne := d":           DefinedReasonForwardRef,
		base + "d := a\nd := b":           DefinedReasonDuplicate,
		base + "a := b*2":                 DefinedReasonDuplicate,
		base + "d := a*":                  DefinedReasonSyntax,
		base + "d := (a + b":              DefinedReasonSyntax,
		base + "d := sin(a)":              DefinedReasonSyntax,
		base + "d := a $ b":               DefinedReasonSyntax,
		base + "d :=":                     DefinedReasonSyntax,
		base + "1d := a":                  DefinedReasonSyntax,
		base + "d := a b":                 DefinedReasonSyntax,
		base + "d := sqrt(a":              DefinedReasonSyntax,
		base + "d := .p2. * a":            DefinedReasonUnknownLabel,
		"f =~ x1 + x2 + x3\nd := 2 * 0.5": "",
	} {
		_, err := NewModel(desc, false)
		if reason == "" {
			if err != nil {
				t.Errorf("%q: %v", desc, err)
			}
			continue
		}
		var de *DefinedError
		if !errors.As(err, &de) || de.Reason != reason {
			t.Errorf("%q: err = %v, want a DefinedError %s", desc, err, reason)
		}
	}
	// A per-group label of any level is a label (it resolves per group).
	if _, err := NewGroupModel("f =~ x1 + c(l1, l2)*x2 + x3\nd := l2 - l1", ModelOptions{}); err != nil {
		t.Errorf("per-group labels: %v", err)
	}
	var de *DefinedError
	if _, err := NewGroupModel("f =~ x1 + c(l1, l2)*x2 + x3\nd := l3", ModelOptions{}); !errors.As(err, &de) || de.Reason != DefinedReasonUnknownLabel || de.Ref != "l3" {
		t.Errorf("unknown per-group label: %v", err)
	}
	for line, op := range map[string]string{"a == b": "==", "a < 1": "<", "b > 0.5": ">", "a == 2*b": "=="} {
		_, err := NewModel(base+line, false)
		var ce *ConstraintError
		if !errors.As(err, &ce) || ce.Op != op || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%q: err = %v, want a ConstraintError %s", line, err, op)
		}
	}
	// lavaan's formative <~ is unsupported syntax, not a constraint.
	_, err := NewModel(base+"g <~ x1 + x2", false)
	var ce *ConstraintError
	if err == nil || errors.As(err, &ce) {
		t.Errorf("<~: err = %v, want unsupported syntax", err)
	}
	if !HasDefined(base+"d := a*b") || HasDefined(base) || HasDefined("d := a") {
		t.Error("HasDefined misreads a description")
	}
}

// TestSEM_DefinedSwitchesConventions: a := line on a covariance-only
// description is lavaan's fit (no mean structure, Fisher scoring, Wald
// intervals), and SpecifiedDefined lists its rows with NaN figures.
func TestSEM_DefinedSwitchesConventions(t *testing.T) {
	m, err := NewModel("f =~ x1 + a*x2 + b*x3\nd := a*b", false)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Lavaan() || m.MeanStructure {
		t.Fatalf("Lavaan %v MeanStructure %v, want lavaan conventions without means", m.Lavaan(), m.MeanStructure)
	}
	rows := SpecifiedDefined(m)
	if len(rows) != 1 || rows[0].Key() != "d := a*b" || !math.IsNaN(rows[0].Value) || !math.IsNaN(rows[0].SE) {
		t.Errorf("SpecifiedDefined %+v", rows)
	}
}

// TestSEM_WaldLevelMatchesLavaan holds a non-default Wald level
// (Options.Level, lavaan's level = 0.90) to lavaan: every free and :=
// row's ci_lower / ci_upper at the 0.95 quantile's sibling, the
// estimates, SEs, z and p unchanged — and a 0.95 level keeps the
// default's bits.
func TestSEM_WaldLevelMatchesLavaan(t *testing.T) {
	ref := loadLavaanRef(t, "testdata/brand_def_l90_lavaan.json")
	desc, err := os.ReadFile("testdata/brand_def_model.txt")
	if err != nil {
		t.Fatal(err)
	}
	m, err := NewModelWithOptions(string(desc), ModelOptions{MeanStructure: true})
	if err != nil {
		t.Fatal(err)
	}
	mean, s, n := readCSVMoments(t, "testdata/brand_data.csv", m.Observed)
	fit, err := FitMoments(m, s, s, mean, n, Options{Level: 0.90})
	if err != nil {
		t.Fatal(err)
	}
	if err := ComputeStats(fit, string(desc)); err != nil {
		t.Fatal(err)
	}
	checkAgainstLavaan(t, fit, ref)
	def, err := FitMoments(m, s, s, mean, n, Options{})
	if err != nil {
		t.Fatal(err)
	}
	at95, err := FitMoments(m, s, s, mean, n, Options{Level: 0.95})
	if err != nil {
		t.Fatal(err)
	}
	rows := func(f *Fit) []Estimate { return append(append([]Estimate(nil), f.Estimates...), f.Defined...) }
	a, b, c := rows(def), rows(at95), rows(fit)
	for i := range a {
		if math.Float64bits(a[i].CILower) != math.Float64bits(b[i].CILower) || math.Float64bits(a[i].CIUpper) != math.Float64bits(b[i].CIUpper) {
			t.Errorf("%s: level 0.95 moved the default interval", a[i].Key())
		}
		if a[i].Value != c[i].Value || !sameNaN(a[i].SE, c[i].SE) || !sameNaN(a[i].P, c[i].P) {
			t.Errorf("%s: the level moved the estimate, SE or p", a[i].Key())
		}
	}
	if _, err := FitMoments(m, s, s, mean, n, Options{Level: 1}); err == nil {
		t.Error("level 1 accepted")
	}
}

func sameNaN(a, b float64) bool { return a == b || (math.IsNaN(a) && math.IsNaN(b)) }
