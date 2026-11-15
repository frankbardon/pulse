package regmmm

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/distlagparams"
	pmodel "github.com/frankbardon/pulse/internal/model"
)

// bayes_adstock_test.go holds REG_MMM_BAYES's adstock kernels
// (bayes_adstock.go) to their contract: every kernel's weights are
// WIN_DISTRIBUTED_LAG's (distlagparams.Resolve, normalize false) bit for
// bit, its analytic dw/dφ is the finite-difference one, the factory
// accepts exactly the four kernels with their own prior keys and
// coefficient names, and the coefficient table is ModelRegressionTerms'.
// The window run through the engine is bayes_engine_test.go's.

// kernelCases are each kernel at parameter points across its range.
var kernelCases = []struct {
	kernel string
	params [][]float64
}{
	{"geometric", [][]float64{{0.05}, {0.5}, {0.93}}},
	{"delayed", [][]float64{{0.3, 0}, {0.55, 2.3}, {0.8, 5.5}, {0.9, 9}}},
	{"weibull_cdf", [][]float64{{0.6, 2.3}, {1, 3}, {2.4, 4.5}, {4, 1.2}}},
	{"weibull_pdf", [][]float64{{0.6, 1.7}, {1.3, 4.2}, {2, 3}, {3.5, 6}}},
}

// windowParams is the WIN_DISTRIBUTED_LAG params of kernel at p.
func windowParams(t *testing.T, kernel string, p []float64, maxLag int) json.RawMessage {
	t.Helper()
	m := map[string]any{"kernel": kernel, "max_lag": maxLag, "normalize": false}
	for i, kp := range kernelByName(kernel).Params() {
		m[kp.key] = p[i]
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestMMMBayes_KernelWeightsMatchDistributedLag: every kernel's weights,
// with and without the gradient, are distlagparams.Resolve's for the
// same params with normalize false, bit for bit.
func TestMMMBayes_KernelWeightsMatchDistributedLag(t *testing.T) {
	t.Parallel()

	const maxLag = 10
	for _, kc := range kernelCases {
		k := kernelByName(kc.kernel)
		if k == nil || k.Name() != kc.kernel {
			t.Fatalf("kernel %q: %v", kc.kernel, k)
		}
		for _, p := range kc.params {
			spec, ref := distlagparams.Resolve(windowParams(t, kc.kernel, p, maxLag))
			if ref != nil {
				t.Fatalf("%s %v: %s", kc.kernel, p, ref.Message)
			}
			for _, grad := range []bool{false, true} {
				w, dw := k.Weights(p, maxLag, grad)
				if len(w) != maxLag+1 || (grad != (dw != nil)) {
					t.Fatalf("%s %v grad %v: %d weights, dw %v", kc.kernel, p, grad, len(w), dw != nil)
				}
				for l := range w {
					if math.Float64bits(w[l]) != math.Float64bits(spec.Weights[l]) {
						t.Errorf("%s %v lag %d: model %v (%#x), window %v (%#x)", kc.kernel, p, l,
							w[l], math.Float64bits(w[l]), spec.Weights[l], math.Float64bits(spec.Weights[l]))
					}
				}
			}
		}
	}
}

// TestMMMBayes_KernelGradientsMatchFiniteDifferences: every kernel's
// analytic ∂w_l/∂φ_i matches a fourth-order central difference of its
// own weights to a relative 1e-7.
func TestMMMBayes_KernelGradientsMatchFiniteDifferences(t *testing.T) {
	t.Parallel()

	const maxLag = 10
	for _, kc := range kernelCases {
		k := kernelByName(kc.kernel)
		for _, p := range kc.params {
			_, dw := k.Weights(p, maxLag, true)
			if len(dw) != len(k.Params()) {
				t.Fatalf("%s: %d derivative rows for %d params", kc.kernel, len(dw), len(k.Params()))
			}
			for i := range p {
				h := float64(1e-5 * max(1, math.Abs(p[i])))
				at := func(step float64) []float64 {
					q := slices.Clone(p)
					q[i] += step
					w, _ := k.Weights(q, maxLag, false)
					return w
				}
				h2 := float64(2 * h)
				p1, m1, p2, m2 := at(h), at(-h), at(h2), at(-h2)
				for l := range maxLag + 1 {
					fd := (float64(8*(p1[l]-m1[l])) - (p2[l] - m2[l])) / (12 * h)
					if tol := 1e-7 * max(1e-3, math.Abs(dw[i][l])); math.Abs(dw[i][l]-fd) > tol {
						t.Errorf("%s %v ∂w_%d/∂%s: analytic %.12g, finite difference %.12g",
							kc.kernel, p, l, k.Params()[i].key, dw[i][l], fd)
					}
				}
			}
		}
	}
}

// TestMMMBayes_KernelFactory: params.adstock accepts the four kernels
// (absent and "geometric" the default); a kernel other than geometric
// needs params.max_lag; a channel prior is accepted for the kernel's
// own parameters only, a peak prior only inside [0, max_lag]; a
// predictor collides with exactly the chosen kernel's coefficient
// prefixes. Every refusal is PROCESSING_CONFIG naming its slot, from
// the spec alone (BayesSpecRefusal raises it).
func TestMMMBayes_KernelFactory(t *testing.T) {
	t.Parallel()

	ok := []string{
		`{"channels":["tv"],"time":"week"}`,
		`{"channels":["tv"],"time":"week","adstock":"geometric"}`,
		`{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8}`,
		`{"channels":["tv"],"time":"week","adstock":"weibull_cdf","max_lag":8}`,
		`{"channels":["tv"],"time":"week","adstock":"weibull_pdf","max_lag":8}`,
		`{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8,"priors":{"tv":{"decay":{"family":"beta","alpha":3,"beta":3},"peak":{"family":"truncnormal","mu":2,"sigma":1,"lower":0,"upper":8}}}}`,
		`{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8,"priors":{"tv":{"peak":{"family":"beta","alpha":2,"beta":2}}}}`,
		`{"channels":["tv"],"time":"week","adstock":"weibull_pdf","max_lag":8,"priors":{"tv":{"shape":{"family":"lognormal","mu":0,"sigma":1},"scale":{"family":"halfnormal","sigma":4}}}}`,
	}
	for _, params := range ok {
		if ce := BayesSpecRefusal(BayesSpec(params)); ce != nil {
			t.Errorf("%s: refused %v", params, ce)
		}
	}
	for _, tc := range []struct {
		name, params, slot, msg string
	}{
		{"unknown kernel", `{"channels":["tv"],"time":"week","adstock":"hyperbolic"}`, "params.adstock", "not one of: geometric, delayed, weibull_cdf, weibull_pdf"},
		{"delayed without max_lag", `{"channels":["tv"],"time":"week","adstock":"delayed"}`, "params.max_lag", "needs params.max_lag"},
		{"weibull without max_lag", `{"channels":["tv"],"time":"week","adstock":"weibull_cdf"}`, "params.max_lag", "needs params.max_lag"},
		{"peak on geometric", `{"channels":["tv"],"time":"week","priors":{"tv":{"peak":{"family":"beta","alpha":1,"beta":1}}}}`, "params.priors", "has no peak parameter"},
		{"decay on weibull", `{"channels":["tv"],"time":"week","adstock":"weibull_cdf","max_lag":8,"priors":{"tv":{"decay":{"family":"beta","alpha":1,"beta":1}}}}`, "params.priors", "has no decay parameter"},
		{"shape on delayed", `{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8,"priors":{"tv":{"shape":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors", "has no shape parameter"},
		{"peak past max_lag", `{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8,"priors":{"tv":{"peak":{"family":"truncnormal","mu":2,"sigma":1,"lower":0,"upper":9}}}}`, "params.priors", "inside [0, max_lag = 8]"},
		{"peak unbounded", `{"channels":["tv"],"time":"week","adstock":"delayed","max_lag":8,"priors":{"tv":{"peak":{"family":"lognormal","mu":0,"sigma":1}}}}`, "params.priors", "inside [0, max_lag = 8]"},
		{"shape on the real line", `{"channels":["tv"],"time":"week","adstock":"weibull_pdf","max_lag":8,"priors":{"tv":{"shape":{"family":"normal","mu":1,"sigma":1}}}}`, "params.priors", "not inside"},
	} {
		ce := BayesSpecRefusal(BayesSpec(tc.params))
		if ce == nil {
			t.Errorf("%s: accepted, want PROCESSING_CONFIG slot %s with %q", tc.name, tc.slot, tc.msg)
			continue
		}
		if ce.Code != perr.PROCESSING_CONFIG || ce.Details["slot"] != tc.slot || !strings.Contains(ce.Message, tc.msg) {
			t.Errorf("%s: %v (details %v), want PROCESSING_CONFIG slot %s with %q", tc.name, ce, ce.Details, tc.slot, tc.msg)
		}
	}

	for _, tc := range []struct {
		kernel, predictor string
		collides          bool
	}{
		{"", "peak:x", false},
		{"", "shape:x", false},
		{"", "decay:x", true},
		{"delayed", "peak:x", true},
		{"delayed", "decay:x", true},
		{"delayed", "scale:x", false},
		{"weibull_cdf", "shape:x", true},
		{"weibull_pdf", "scale:x", true},
		{"weibull_pdf", "decay:x", false},
		{"weibull_pdf", "k:x", true},
	} {
		params := `{"channels":["tv"],"time":"week","max_lag":8}`
		if tc.kernel != "" {
			params = `{"channels":["tv"],"time":"week","max_lag":8,"adstock":"` + tc.kernel + `"}`
		}
		s := BayesSpec(params)
		s.Predictors = []string{"tv", tc.predictor}
		_, err := BayesFactory(s, nil)
		var ce *perr.CodedError
		got := stderrors.As(err, &ce) && ce.Details["slot"] == "predictors"
		if got != tc.collides || (err != nil && !got) {
			t.Errorf("adstock %q predictor %q: %v, want collision %v", tc.kernel, tc.predictor, err, tc.collides)
		}
	}
}

// TestMMMBayes_KernelCoefficientNames: under every kernel the
// coefficient table's names beyond the intercept and the predictors are
// ModelRegressionTerms' — the static catalog coef() and response() read
// — in the same order, and the θ layout gives each kernel parameter its
// own coordinate.
func TestMMMBayes_KernelCoefficientNames(t *testing.T) {
	t.Parallel()

	for _, kernel := range kernelNames() {
		s := BayesSpec(fmt.Sprintf(`{"channels":["tv"],"time":"week","max_lag":8,"adstock":%q}`, kernel))
		s.Predictors = []string{"tv", "radio", "price"}
		s.Params = json.RawMessage(strings.Replace(string(s.Params), `["tv"]`, `["tv","radio"]`, 1))
		reg, err := BayesFactory(s, nil)
		if err != nil {
			t.Fatal(err)
		}
		names := reg.(*bayesRegression).coefNames()
		terms, ok := pmodel.ModelRegressionTerms(s)
		want := append([]string{interceptName, "tv", "radio", "price"}, terms...)
		if !ok || !slices.Equal(names, want) {
			t.Errorf("%s: names %v, want %v", kernel, names, want)
		}
		np := len(kernelByName(kernel).Params())
		if got, wantDim := len(names), 2+1+(np+3)*2; got != wantDim {
			t.Errorf("%s: %d coefficients, want 2 + controls + (np + 3)·channels = %d", kernel, got, wantDim)
		}
	}
}
