package regression

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/reglink"
	"github.com/frankbardon/pulse/types"
)

// glm_links_test.go: the non-canonical REG_GLM links (U65 E2-S1) beyond
// the R oracle (glm_links_oracle_test.go) — the family table agrees
// with internal/reglink, each link's g / g⁻¹ / μ'(η) agree with their
// closed forms, a fit lands on the score equations x·(y − μ)·μ'(η)/V(μ)
// = 0 (penalised: the KKT conditions), the first-step restart, the
// modifiers, and the canonical links' requests stay byte-identical.

// TestSelectFamily_MatchesRegLink: the engine fits exactly the pairs
// internal/reglink lists (the rule predict and the manifest read), each
// echoing its own family / link, Canonical only on the canonical link;
// an empty link resolves to the canonical family value itself.
func TestSelectFamily_MatchesRegLink(t *testing.T) {
	t.Parallel()

	n := 0
	for _, fam := range reglink.Families() {
		for _, link := range reglink.Links(fam) {
			f, err := selectFamily(fam, link)
			if err != nil {
				t.Fatalf("%s/%s: %v", fam, link, err)
			}
			if f.Name != fam || f.Link != link || f.Canonical != (link == reglink.Canonical(fam)) {
				t.Errorf("%s/%s resolved to %s/%s canonical=%v", fam, link, f.Name, f.Link, f.Canonical)
			}
			n++
		}
		def, err := selectFamily(fam, "")
		if err != nil || def.Link != reglink.Canonical(fam) {
			t.Errorf("%s default link %q (%v)", fam, def.Link, err)
		}
	}
	total := 0
	for _, links := range glmFamilies {
		total += len(links)
	}
	if total != n {
		t.Errorf("glmFamilies has %d pairs, reglink %d", total, n)
	}
}

// linkClosedForm is each link's g⁻¹ and μ'(η), written out with stdlib
// math independently of glm_families.go.
var linkClosedForm = map[string]struct{ inv, dmu func(float64) float64 }{
	"binomial/probit": {
		func(e float64) float64 { return 0.5 * math.Erfc(-e/math.Sqrt2) },
		func(e float64) float64 { return math.Exp(-e*e/2) / math.Sqrt(2*math.Pi) },
	},
	"binomial/cloglog": {
		func(e float64) float64 { return 1 - math.Exp(-math.Exp(e)) },
		func(e float64) float64 { return math.Exp(e - math.Exp(e)) },
	},
	"poisson/identity": {func(e float64) float64 { return e }, func(float64) float64 { return 1 }},
	"poisson/sqrt":     {func(e float64) float64 { return e * e }, func(e float64) float64 { return 2 * e }},
	"gamma/log":        {math.Exp, math.Exp},
	"gamma/identity":   {func(e float64) float64 { return e }, func(float64) float64 { return 1 }},
}

// TestGLMLinks_ClosedForms: on the links' interior, g⁻¹ and μ'(η) equal
// their closed forms, g inverts g⁻¹, and μ'(η) is g⁻¹'s derivative
// (central difference); probit / cloglog clamp at R's ε bounds.
func TestGLMLinks_ClosedForms(t *testing.T) {
	t.Parallel()

	for key, cf := range linkClosedForm {
		var fam, link string
		for i := range key {
			if key[i] == '/' {
				fam, link = key[:i], key[i+1:]
			}
		}
		f, err := selectFamily(fam, link)
		if err != nil {
			t.Fatal(err)
		}
		for _, eta := range []float64{0.15, 0.4, 0.9, 1.3, 2.2} {
			if fam == "binomial" {
				eta -= 1
			}
			if got, want := f.InverseLink(eta), cf.inv(eta); !oracleClose(got, want, 1e-14) {
				t.Errorf("%s g⁻¹(%v) = %v, want %v", key, eta, got, want)
			}
			if got, want := f.DmuDeta(eta), cf.dmu(eta); !oracleClose(got, want, 1e-14) {
				t.Errorf("%s μ'(%v) = %v, want %v", key, eta, got, want)
			}
			if got := f.LinkFunc(f.InverseLink(eta)); !oracleClose(got, eta, 1e-12) {
				t.Errorf("%s g(g⁻¹(%v)) = %v", key, eta, got)
			}
			h := 1e-6
			num := (f.InverseLink(eta+h) - f.InverseLink(eta-h)) / (2 * h)
			if !oracleClose(f.DmuDeta(eta), num, 1e-7) {
				t.Errorf("%s μ'(%v) = %v, numerical %v", key, eta, f.DmuDeta(eta), num)
			}
			if f.ValidEta != nil && !f.ValidEta(eta) {
				t.Errorf("%s: interior η %v judged invalid", key, eta)
			}
		}
	}
	probit, _ := selectFamily("binomial", "probit")
	cloglog, _ := selectFamily("binomial", "cloglog")
	for _, f := range []glmFamily{probit, cloglog} {
		for _, eta := range []float64{-50, 50} {
			if mu := f.InverseLink(eta); !(mu >= machineEps && mu <= 1-machineEps) {
				t.Errorf("%s g⁻¹(%v) = %v outside [ε, 1 − ε]", f.Link, eta, mu)
			}
			if d := f.DmuDeta(eta); !(d >= machineEps) {
				t.Errorf("%s μ'(%v) = %v below ε", f.Link, eta, d)
			}
		}
	}
	for _, link := range []string{"identity", "sqrt"} {
		f, _ := selectFamily("poisson", link)
		if f.ValidEta(0) || f.ValidEta(-0.5) {
			t.Errorf("poisson/%s accepts η ≤ 0", link)
		}
	}
	if g, _ := selectFamily("gamma", "identity"); g.ValidEta(-1) {
		t.Error("gamma/identity accepts η ≤ 0")
	}
	if g, _ := selectFamily("gamma", "log"); g.ValidEta != nil {
		t.Error("gamma/log restricts η")
	}
}

// linkScore is the log-likelihood score Σ w·x·(y − μ)·μ'(η)/V(μ) at
// beta, from linkClosedForm and each family's variance written out.
func linkScore(fam, link string, recs []Record, beta []float64) []float64 {
	cf := linkClosedForm[fam+"/"+link]
	v := map[string]func(float64) float64{
		"binomial": func(m float64) float64 { return m * (1 - m) },
		"poisson":  func(m float64) float64 { return m },
		"gamma":    func(m float64) float64 { return m * m },
	}[fam]
	u := make([]float64, len(beta))
	for _, r := range recs {
		rec := r.(mockRecord).values
		x := []float64{1, rec["x1"], rec["x2"]}
		eta := beta[0] + beta[1]*x[1] + beta[2]*x[2]
		mu := cf.inv(eta)
		s := (rec["y"] - mu) * cf.dmu(eta) / v(mu)
		for j := range u {
			u[j] += s * x[j]
		}
	}
	return u
}

func betaOf(res *types.RegressionResult) []float64 {
	return []float64{res.Coefficients[InterceptKey], res.Coefficients["x1"], res.Coefficients["x2"]}
}

// TestGLMLinks_ScoreEquations: every oracle case's unweighted fit is a
// root of its own score equations x·(y − μ)·μ'/V, computed from the
// closed forms (not the canonical shortcut x·(y − μ)).
func TestGLMLinks_ScoreEquations(t *testing.T) {
	t.Parallel()

	for _, c := range loadGLMLinks(t) {
		if c.Weighted {
			continue
		}
		res, err := asBuffered(t, newGLM(t, c.spec())).FitBuffered(c.records())
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		for j, s := range linkScore(c.Family, c.Link, c.records(), betaOf(res)) {
			if math.Abs(s) > 1e-9 {
				t.Errorf("%s: score[%d] = %g at the fit", c.Name, j, s)
			}
		}
	}
}

// TestGLMLinks_FirstStepRestart: a poisson identity fit whose first
// IRLS step drives a fitted mean through zero — R 4.6.1's glm stops
// there, "no valid set of coefficients has been found: please supply
// starting values" — restarts from the intercept-only fit, halves into
// the valid region and converges to the MLE: every η positive, the
// score equations zero, and β equal to R's glm given that start
// (start = c(mean(y), 0, 0), then refitted to its fixed point).
func TestGLMLinks_FirstStepRestart(t *testing.T) {
	t.Parallel()

	xs := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	ys := []float64{1, 1, 2, 1, 3, 6, 8, 9, 10, 13}
	rBeta := []float64{0.56416184506199896, 1.17075782341455725, -0.48063561158611834}
	recs := make([]Record, len(xs))
	for i := range xs {
		recs[i] = mockRecord{values: map[string]float64{"x1": xs[i], "x2": float64(i % 3), "y": ys[i]}}
	}
	fam, _ := selectFamily("poisson", "identity")
	x := make([]float64, 0, 3*len(xs))
	for _, r := range recs {
		v := r.(mockRecord).values
		x = append(x, 1, v["x1"], v["x2"])
	}
	firstHalved := -1
	pr := &irlsProblem{X: x, Y: ys, K: 3, Family: fam, Tol: 1e-13, MaxIters: 500,
		onStep: func(iter int, _ float64, halvings int) {
			if iter == 0 {
				firstHalved = halvings
			}
		}}
	sol, err := irlsFit(pr)
	if err != nil {
		t.Fatalf("irlsFit: %v", err)
	}
	if firstHalved <= 0 {
		t.Fatalf("first step halved %d times; the fixture no longer lands invalid", firstHalved)
	}
	for i, e := range sol.Eta {
		if !(e > 0) {
			t.Errorf("row %d: η = %v", i, e)
		}
	}
	for j, s := range linkScore("poisson", "identity", recs, sol.Beta) {
		if math.Abs(s) > 1e-8 {
			t.Errorf("score[%d] = %g", j, s)
		}
	}
	for j, b := range rBeta {
		if !oracleClose(sol.Beta[j], b, 1e-9) {
			t.Errorf("β[%d] = %.17g, R %.17g", j, sol.Beta[j], b)
		}
	}
}

// TestGLMLinks_PenalizedKKT: an l2 / l1 penalised fit under a
// non-canonical link is the penalised MLE — glmnet's objective with the
// link's own likelihood: score_j = λ₂·β_j + λ₁·sign(β_j) for an active
// predictor, |score_j| ≤ λ₁ for a dropped one, score₀ = 0 (λ on the
// N*-scaled objective: λ₂ = N*·α·(1 − l1_ratio), λ₁ = N*·α·l1_ratio).
func TestGLMLinks_PenalizedKKT(t *testing.T) {
	t.Parallel()

	for _, c := range loadGLMLinks(t) {
		if c.Weighted || c.Family == "gamma" {
			continue // gamma refuses a penalty
		}
		for _, pen := range []struct {
			kind         string
			alpha, ratio float64
		}{{"l2", 0.05, 0}, {"l1", 0.02, 1}, {"elasticnet", 0.03, 0.5}} {
			spec := c.spec()
			spec.Vcov = pen.kind == "l2"
			spec.Penalty, spec.Alpha = pen.kind, pen.alpha
			if pen.kind == "elasticnet" {
				spec.L1Ratio = pen.ratio
			}
			res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("%s %s: %v", c.Name, pen.kind, err)
			}
			beta := betaOf(res)
			nStar := float64(len(c.Rows))
			l2, l1 := nStar*pen.alpha*(1-pen.ratio), nStar*pen.alpha*pen.ratio
			// The loglik score; the penalised objective on the deviance
			// scale is −2·loglik + l2·‖β‖² + 2·l1·‖β‖₁, so stationarity
			// reads score = l2·β + l1·sign(β).
			u := linkScore(c.Family, c.Link, c.records(), beta)
			if math.Abs(u[0]) > 1e-8 {
				t.Errorf("%s %s: intercept score %g", c.Name, pen.kind, u[0])
			}
			for j := 1; j < 3; j++ {
				switch b := beta[j]; {
				case b == 0:
					if math.Abs(u[j]) > l1*(1+1e-8) {
						t.Errorf("%s %s: dropped β[%d] score %g > λ₁ %g", c.Name, pen.kind, j, u[j], l1)
					}
				default:
					if want := l2*b + l1*math.Copysign(1, b); math.Abs(u[j]-want) > 1e-8*math.Max(1, math.Abs(want)) {
						t.Errorf("%s %s: β[%d] = %v score %g, want %g", c.Name, pen.kind, j, b, u[j], want)
					}
				}
			}
		}
	}
}

// TestGLMLinks_Modifiers: resample (jackknife, bootstrap) and selection
// run over a non-canonical fit and echo its link; the jackknife's
// replicates and selection's final refit converge.
func TestGLMLinks_Modifiers(t *testing.T) {
	t.Parallel()

	for _, c := range loadGLMLinks(t) {
		if c.Weighted {
			continue
		}
		for name, edit := range map[string]func(*types.RegressionSpec){
			"jackknife": func(s *types.RegressionSpec) { s.Resample = "jackknife" },
			"bootstrap": func(s *types.RegressionSpec) { s.Resample, s.BootstrapIters, s.RNGSeed = "bootstrap", 20, 7 },
			"selection": func(s *types.RegressionSpec) { s.Selection, s.Criterion = "backward", "aic" },
		} {
			spec := c.spec()
			spec.Vcov, spec.Tol, spec.MaxIters = false, 0, 0
			edit(spec)
			res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(c.records())
			if err != nil {
				t.Fatalf("%s %s: %v", c.Name, name, err)
			}
			if res.Link != c.Link || res.Family != c.Family {
				t.Errorf("%s %s: echo %s/%s", c.Name, name, res.Family, res.Link)
			}
		}
	}
}

// TestGLMLinks_CanonicalDefaultByteIdentical: an omitted link and the
// family's canonical link spelled out produce the same result byte for
// byte (JSON), for every family — the default stays the pre-U65
// canonical fit, whose bits the fingerprint golden pins
// (internal/fingerprint: process.mtcars.reg_glm_*).
func TestGLMLinks_CanonicalDefaultByteIdentical(t *testing.T) {
	t.Parallel()

	rows := wGLMFixture()
	for family, target := range wGLMTargets {
		fit := func(link string) []byte {
			spec := &types.RegressionSpec{Type: types.REG_GLM, Family: family, Link: link, Target: target, Predictors: []string{"x1", "x2"}, Vcov: true}
			res, err := asBuffered(t, newGLM(t, spec)).FitBuffered(wGLMRecords(rows))
			if err != nil {
				t.Fatalf("%s/%q: %v", family, link, err)
			}
			b, err := json.Marshal(res)
			if err != nil {
				t.Fatal(err)
			}
			return b
		}
		def, explicit := fit(""), fit(reglink.Canonical(family))
		if !reflect.DeepEqual(def, explicit) {
			t.Errorf("%s: omitted link\n%s\nexplicit %s\n%s", family, def, reglink.Canonical(family), explicit)
		}
	}
}
