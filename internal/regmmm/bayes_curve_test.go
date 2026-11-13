package regmmm

import (
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	pmodel "github.com/frankbardon/pulse/internal/model"
)

// bayes_curve_test.go holds REG_MMM_BAYES's saturation curves
// (bayes_curve.go) to their contract: each curve is its closed form,
// its analytic partials and elasticity are the finite-difference ones,
// the Hill curve is hillGrad bit for bit, the factory accepts exactly
// the three curves with their own prior keys and coefficient names, and
// the coefficient table is ModelRegressionTerms' under every kernel ×
// curve.

// curveCases are each curve at parameter points across its range.
var curveCases = []struct {
	curve  string
	params [][]float64
}{
	{"hill", [][]float64{{0.4, 0.8}, {1, 1.5}, {2.5, 3}}},
	{"logistic", [][]float64{{0.3}, {1.2}, {6}}},
	{"michaelis_menten", [][]float64{{0.05}, {0.9}, {4}}},
}

// curveAt is the closed form of each curve, written with math.*.
func curveAt(curve string, a float64, q []float64) float64 {
	switch curve {
	case "hill":
		if a == 0 {
			return 0
		}
		return 1 / (1 + math.Pow(q[0]/a, q[1]))
	case "logistic":
		return (1 - math.Exp(-q[0]*a)) / (1 + math.Exp(-q[0]*a))
	case "michaelis_menten":
		return a / (q[0] + a)
	}
	panic(curve)
}

// curveAdstocks are scaled adstock values across the curves' ranges.
var curveAdstocks = []float64{0, 1e-6, 0.03, 0.4, 1, 1.7, 3.5, 12}

// TestMMMBayes_CurvesMatchClosedForm: every curve's value is its closed
// form, and the Hill curve's value and partials are hillGrad's bits.
func TestMMMBayes_CurvesMatchClosedForm(t *testing.T) {
	t.Parallel()

	for _, cc := range curveCases {
		c := curveByName(cc.curve)
		if c == nil || c.Name() != cc.curve {
			t.Fatalf("curve %q: %v", cc.curve, c)
		}
		for _, q := range cc.params {
			for _, a := range curveAdstocks {
				dq := make([]float64, len(q))
				h, dha := c.Eval(a, q, dq)
				if want := curveAt(cc.curve, a, q); math.Abs(h-want) > 1e-14 {
					t.Errorf("%s %v at %v: %.17g, closed form %.17g", cc.curve, q, a, h, want)
				}
				if h2, _ := c.Eval(a, q, nil); math.Float64bits(h2) != math.Float64bits(h) {
					t.Errorf("%s %v at %v: value moves with the gradient request", cc.curve, q, a)
				}
				if cc.curve == "hill" {
					wh, wa, wk, ws := hillGrad(a, q[0], q[1])
					for i, p := range [][2]float64{{h, wh}, {dha, wa}, {dq[0], wk}, {dq[1], ws}} {
						if math.Float64bits(p[0]) != math.Float64bits(p[1]) {
							t.Errorf("hill %v at %v: figure %d %v, hillGrad %v", q, a, i, p[0], p[1])
						}
					}
				}
			}
		}
	}
	if curveByName("") == nil || curveByName("").Name() != "hill" || curveByName("gompertz") != nil {
		t.Error("curveByName: the default is not hill, or an unknown name resolves")
	}
}

// TestMMMBayes_CurveGradientsMatchFiniteDifferences: every curve's
// analytic ∂h/∂a, ∂h/∂q_i and elasticity Σ a·∂h/∂a match fourth-order
// central differences to a relative 1e-7.
func TestMMMBayes_CurveGradientsMatchFiniteDifferences(t *testing.T) {
	t.Parallel()

	fd := func(f func(float64) float64, x float64) float64 {
		h := float64(1e-5 * max(1e-3, math.Abs(x)))
		return (float64(8*(f(x+h)-f(x-h))) - (f(x+2*h) - f(x-2*h))) / (12 * h)
	}
	check := func(what string, got, want float64) {
		if tol := 1e-7 * max(1e-3, math.Abs(got)); math.Abs(got-want) > tol {
			t.Errorf("%s: analytic %.12g, finite difference %.12g", what, got, want)
		}
	}
	for _, cc := range curveCases {
		c := curveByName(cc.curve)
		for _, q := range cc.params {
			// Away from 0: Hill below s = 1 has an unbounded slope there.
			for _, a := range curveAdstocks[2:] {
				dq := make([]float64, len(q))
				_, dha := c.Eval(a, q, dq)
				check(fmt.Sprintf("%s %v ∂h/∂a at %v", cc.curve, q, a), dha,
					fd(func(x float64) float64 { h, _ := c.Eval(x, q, nil); return h }, a))
				for i := range q {
					check(fmt.Sprintf("%s %v ∂h/∂%s at %v", cc.curve, q, c.Params()[i].key, a), dq[i],
						fd(func(x float64) float64 {
							p := slices.Clone(q)
							p[i] = x
							h, _ := c.Eval(a, p, nil)
							return h
						}, q[i]))
				}
			}
			as := curveAdstocks[1:]
			hs := make([]float64, len(as))
			for i, a := range as {
				hs[i], _ = c.Eval(a, q, nil)
			}
			check(fmt.Sprintf("%s %v elasticity", cc.curve, q), c.Elasticity(as, hs, q),
				fd(func(lam float64) float64 {
					sum := 0.0
					for _, a := range as {
						h, _ := c.Eval(float64(lam*a), q, nil)
						sum += h
					}
					return sum
				}, 1))
		}
	}
}

// TestMMMBayes_CurveFactory: params.saturation accepts the three curves
// (absent and "hill" the default); a channel prior is accepted for the
// curve's own parameters only; a predictor collides with exactly the
// chosen curve's coefficient prefixes. Every refusal is
// PROCESSING_CONFIG naming its slot, from the spec alone.
func TestMMMBayes_CurveFactory(t *testing.T) {
	t.Parallel()

	for _, params := range []string{
		`{"channels":["tv"],"time":"week","saturation":"hill"}`,
		`{"channels":["tv"],"time":"week","saturation":"logistic"}`,
		`{"channels":["tv"],"time":"week","saturation":"michaelis_menten"}`,
		`{"channels":["tv"],"time":"week","saturation":"logistic","priors":{"tv":{"lam":{"family":"lognormal","mu":1,"sigma":0.5}}}}`,
		`{"channels":["tv"],"time":"week","saturation":"michaelis_menten","priors":{"tv":{"k":{"family":"halfnormal","sigma":2}}}}`,
		`{"channels":["tv"],"time":"week","adstock":"weibull_pdf","max_lag":8,"saturation":"logistic","priors":{"tv":{"scale":{"family":"halfnormal","sigma":4},"lam":{"family":"halfnormal","sigma":3}}}}`,
	} {
		if ce := BayesSpecRefusal(BayesSpec(params)); ce != nil {
			t.Errorf("%s: refused %v", params, ce)
		}
	}
	for _, tc := range []struct {
		name, params, slot, msg, reason string
	}{
		{"unknown curve", `{"channels":["tv"],"time":"week","saturation":"gompertz"}`, "params.saturation", "not one of: hill, logistic, michaelis_menten", ""},
		{"lam on hill", `{"channels":["tv"],"time":"week","priors":{"tv":{"lam":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors", "the hill saturation curve has no lam parameter", "prior_not_in_curve"},
		{"k on logistic", `{"channels":["tv"],"time":"week","saturation":"logistic","priors":{"tv":{"k":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors", "the logistic saturation curve has no k parameter", "prior_not_in_curve"},
		{"s on michaelis_menten", `{"channels":["tv"],"time":"week","saturation":"michaelis_menten","priors":{"tv":{"s":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors", "has no s parameter", "prior_not_in_curve"},
		{"lam on the real line", `{"channels":["tv"],"time":"week","saturation":"logistic","priors":{"tv":{"lam":{"family":"normal","mu":1,"sigma":1}}}}`, "params.priors", "not inside", ""},
	} {
		ce := BayesSpecRefusal(BayesSpec(tc.params))
		if ce == nil {
			t.Errorf("%s: accepted, want PROCESSING_CONFIG slot %s with %q", tc.name, tc.slot, tc.msg)
			continue
		}
		if ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != tc.slot || !strings.Contains(ce.Message, tc.msg) ||
			(tc.reason != "" && ce.Details["reason"] != tc.reason) {
			t.Errorf("%s: %v (details %v), want PROCESSING_CONFIG slot %s with %q", tc.name, ce, ce.Details, tc.slot, tc.msg)
		}
	}

	for _, tc := range []struct {
		curve, predictor string
		collides         bool
	}{
		{"", "k:x", true},
		{"", "s:x", true},
		{"", "lam:x", false},
		{"logistic", "lam:x", true},
		{"logistic", "k:x", false},
		{"logistic", "s:x", false},
		{"logistic", "decay:x", true},
		{"michaelis_menten", "k:x", true},
		{"michaelis_menten", "s:x", false},
		{"gompertz", "k:x", true},
	} {
		params := `{"channels":["tv"],"time":"week"}`
		if tc.curve != "" {
			params = `{"channels":["tv"],"time":"week","saturation":"` + tc.curve + `"}`
		}
		s := BayesSpec(params)
		s.Predictors = []string{"tv", tc.predictor}
		_, err := BayesFactory(s, nil)
		var ce *perr.CodedError
		got := stderrors.As(err, &ce) && ce.Details["slot"] == "predictors"
		if got != tc.collides || (err != nil && !got && tc.curve != "gompertz") {
			t.Errorf("saturation %q predictor %q: %v, want collision %v", tc.curve, tc.predictor, err, tc.collides)
		}
	}
	// The Hill default's collision message is unchanged.
	s := BayesSpec(`{"channels":["tv"],"time":"week"}`)
	s.Predictors = []string{"tv", "s:x"}
	if _, err := BayesFactory(s, nil); err == nil || !strings.Contains(err.Error(), "collides with a coefficient name ((intercept), sigma, decay:/k:/s:)") {
		t.Errorf("hill collision: %v", err)
	}
}

// TestMMMBayes_CurveCoefficientNames: under every kernel × curve the
// coefficient table's names beyond the intercept and the predictors are
// ModelRegressionTerms' in the same order, and the θ layout gives each
// curve parameter its own coordinate.
func TestMMMBayes_CurveCoefficientNames(t *testing.T) {
	t.Parallel()

	for _, kernel := range kernelNames() {
		for _, curve := range curveNames() {
			s := BayesSpec(fmt.Sprintf(`{"channels":["tv","radio"],"time":"week","max_lag":8,"adstock":%q,"saturation":%q}`, kernel, curve))
			s.Predictors = []string{"tv", "radio", "price"}
			reg, err := BayesFactory(s, nil)
			if err != nil {
				t.Fatal(err)
			}
			names := reg.(*bayesRegression).coefNames()
			terms, ok := pmodel.ModelRegressionTerms(s)
			want := append([]string{interceptName, "tv", "radio", "price"}, terms...)
			if !ok || !slices.Equal(names, want) {
				t.Errorf("%s × %s: names %v, want %v", kernel, curve, names, want)
			}
			np, nq := len(kernelByName(kernel).Params()), len(curveByName(curve).Params())
			if got, wantDim := len(names), 2+1+(np+nq+1)*2; got != wantDim {
				t.Errorf("%s × %s: %d coefficients, want 2 + controls + (np + nq + 1)·channels = %d", kernel, curve, got, wantDim)
			}
			var lam bool
			for _, n := range names {
				lam = lam || strings.HasPrefix(n, "lam:")
			}
			if lam != (curve == "logistic") {
				t.Errorf("%s × %s: lam: terms %v", kernel, curve, lam)
			}
		}
	}
}

// TestMMMBayes_CurveBackTransforms: under the logistic and
// Michaelis–Menten curves, constrain's original-unit coefficients (λ per
// raw adstock unit, k in raw adstock units) and effects' β rebuild
// predict's mean from raw data alone, ROI is the summed contribution
// over the summed spend, and the marginal ROI is the finite-difference
// derivative of the contribution under a uniform scaling of the spend.
func TestMMMBayes_CurveBackTransforms(t *testing.T) {
	t.Parallel()

	y, spend, controls := jointModelData()
	for _, curve := range []string{"logistic", "michaelis_menten"} {
		m := jointModel(t, curvePriorSets()["geometric_"+curve])
		rng := rand.New(rand.NewPCG(13, 13^0x9E3779B97F4A7C15))
		theta := randomPoint(m, rng, 1)
		out := make([]float64, m.Dim())
		m.constrain(theta, out)
		beta, roi, mroi := m.effects(theta)
		mu := m.predict(theta, spend, controls, 0)
		raw := func(c int, a float64) float64 {
			p := out[m.iCurve(0, c)]
			if curve == "logistic" {
				return math.Tanh(float64(p*a) / 2)
			}
			return a / (p + a)
		}
		adstocked := make([][]float64, m.nc)
		for c := range m.nc {
			w, _ := geometricWeights(out[m.iDecay(c)], defaultMaxLag)
			adstocked[c] = make([]float64, len(y))
			adstock(spend[c], w, nil, adstocked[c], nil)
		}
		for i := range y {
			want := out[m.iIntercept()]
			for j := range controls {
				want += float64(out[m.iControl(j)] * controls[j][i])
			}
			for c := range m.nc {
				want += float64(beta[c] * raw(c, adstocked[c][i]))
			}
			if math.Abs(mu[i]-want) > float64(1e-9*math.Abs(want)) {
				t.Fatalf("%s week %d: predict %.12g, raw coefficients %.12g", curve, i+1, mu[i], want)
			}
		}
		for c := range m.nc {
			contrib := func(scale float64) float64 {
				sum := 0.0
				for _, a := range adstocked[c] {
					sum += float64(beta[c] * raw(c, float64(scale*a)))
				}
				return sum
			}
			total := 0.0
			for _, v := range spend[c] {
				total += v
			}
			if r := contrib(1) / total; math.Abs(r-roi[c]) > float64(1e-9*roi[c]) {
				t.Errorf("%s channel %d: ROI %v, contribution/spend %v", curve, c, roi[c], r)
			}
			const h = 1e-4
			fd := (float64(8*(contrib(1+h)-contrib(1-h))) - (contrib(1+2*h) - contrib(1-2*h))) / (12 * h) / total
			if math.Abs(fd-mroi[c]) > float64(1e-7*math.Abs(mroi[c])) {
				t.Errorf("%s channel %d: mROI %v, finite difference %v", curve, c, mroi[c], fd)
			}
		}
	}
}

// TestMMMBayes_CurveFitsItsCurve: a short fit under each non-Hill curve
// samples that curve — its coefficient table is the curve's (lam: or k:
// alone, no s:), every figure finite, and its marginal ROI below its ROI
// (a concave curve) — and spelling the default out changes nothing.
func TestMMMBayes_CurveFitsItsCurve(t *testing.T) {
	t.Parallel()

	const base = `{"channels":["tv"],"time":"week","chains":1,"warmup":40,"draws":30`
	for _, curve := range []string{"logistic", "michaelis_menten"} {
		spec := BayesSpec(base + `,"saturation":"` + curve + `"}`)
		spec.RNGSeed = 5
		reg, err := BayesFactory(spec, nil)
		if err != nil {
			t.Fatal(err)
		}
		out, err := reg.(extend.ContextRegression).FitContext(context.Background(), digestRows())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, c := range out.Coefficients {
			names = append(names, c.Name)
			if math.IsNaN(c.Estimate) || math.IsInf(c.Estimate, 0) {
				t.Errorf("%s: %s = %v", curve, c.Name, c.Estimate)
			}
		}
		if want := reg.(*bayesRegression).coefNames(); !slices.Equal(names, want) || len(out.Vcov) != len(want) {
			t.Errorf("%s: coefficients %v and a %d-row vcov (the sampled θ), want %v", curve, names, len(out.Vcov), want)
		}
		if slices.Contains(names, "s:tv") || slices.Contains(names, "lam:tv") != (curve == "logistic") {
			t.Errorf("%s: coefficients %v", curve, names)
		}
		roi := out.Outputs["roi"].(map[string]any)["tv"].(map[string]float64)["mean"]
		mroi := out.Outputs["mroi"].(map[string]any)["tv"].(map[string]float64)["mean"]
		if !(mroi > 0 && mroi < roi) {
			t.Errorf("%s: mROI %v, ROI %v: a concave curve's mROI is in (0, ROI)", curve, mroi, roi)
		}
	}
	if a, b := fitDigest(t, base+`}`, 99), fitDigest(t, base+`,"saturation":"hill"}`, 99); a != b {
		t.Errorf("saturation hill spelled out moves the fit: %s vs %s", a, b)
	}
}
