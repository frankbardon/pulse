package distlagparams

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// Every rule Resolve judges, one case each, with the sentence fragment
// the refusal must carry.
func TestResolve_Refusals(t *testing.T) {
	cases := []struct {
		name, params, want string
	}{
		{"no params", ``, "params.kernel is required"},
		{"empty", `{}`, "params.kernel is required"},
		{"malformed", `{"kernel": 3}`, "malformed params"},
		{"unknown kernel", `{"kernel": "exponential", "decay": 0.5, "max_lag": 4}`, "params.kernel must be"},
		{"no max_lag", `{"kernel": "geometric", "decay": 0.5}`, "params.max_lag is required"},
		{"max_lag zero", `{"kernel": "geometric", "decay": 0.5, "max_lag": 0}`, "params.max_lag must be"},
		{"max_lag over ceiling", `{"kernel": "geometric", "decay": 0.5, "max_lag": 10001}`, "params.max_lag must be"},
		{"max_lag fractional", `{"kernel": "geometric", "decay": 0.5, "max_lag": 2.5}`, "malformed params"},
		{"decay missing", `{"kernel": "geometric", "max_lag": 4}`, "params.decay is required"},
		{"decay one", `{"kernel": "geometric", "decay": 1, "max_lag": 4}`, "params.decay must be in [0, 1)"},
		{"decay negative", `{"kernel": "geometric", "decay": -0.01, "max_lag": 4}`, "params.decay must be in [0, 1)"},
		{"shape on geometric", `{"kernel": "geometric", "decay": 0.5, "shape": 2, "max_lag": 4}`, "apply only to the weibull"},
		{"scale on geometric", `{"kernel": "geometric", "decay": 0.5, "scale": 2, "max_lag": 4}`, "apply only to the weibull"},
		{"decay on weibull", `{"kernel": "weibull_cdf", "decay": 0.5, "shape": 2, "scale": 2, "max_lag": 4}`, "applies only to the geometric"},
		{"shape missing", `{"kernel": "weibull_pdf", "scale": 2, "max_lag": 4}`, "both required"},
		{"scale missing", `{"kernel": "weibull_cdf", "shape": 2, "max_lag": 4}`, "both required"},
		{"shape zero", `{"kernel": "weibull_pdf", "shape": 0, "scale": 2, "max_lag": 4}`, "params.shape must be > 0"},
		{"scale negative", `{"kernel": "weibull_cdf", "shape": 2, "scale": -1, "max_lag": 4}`, "params.scale must be > 0"},
		{"no mass", `{"kernel": "weibull_pdf", "shape": 1000, "scale": 0.001, "max_lag": 4}`, "no finite positive weight"},
		{"normalize not bool", `{"kernel": "geometric", "decay": 0.5, "max_lag": 4, "normalize": "yes"}`, "malformed params"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ref := Resolve(json.RawMessage(c.params))
			if ref == nil {
				t.Fatalf("%s accepted", c.params)
			}
			if !strings.Contains(ref.Message, c.want) {
				t.Errorf("message %q lacks %q", ref.Message, c.want)
			}
			if ce := ref.Coded(); ce.Code != errors.PULSE_WINDOW_INVALID || !strings.HasPrefix(ce.Message, "WIN_DISTRIBUTED_LAG: ") {
				t.Errorf("coded = %s %q", ce.Code, ce.Message)
			}
		})
	}
}

// The kernels by their closed forms, normalised by default.
func TestResolve_Kernels(t *testing.T) {
	sumTo := func(t *testing.T, w []float64, want float64) {
		t.Helper()
		s := 0.0
		for _, x := range w {
			s += x
		}
		if math.Abs(s-want) > 1e-12 {
			t.Errorf("weights sum to %v, want %v", s, want)
		}
	}
	spec, ref := Resolve(json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 3, "normalize": false}`))
	if ref != nil {
		t.Fatal(ref.Message)
	}
	for l, want := range []float64{1, 0.5, 0.25, 0.125} {
		if spec.Weights[l] != want {
			t.Errorf("geometric raw w[%d] = %v, want %v", l, spec.Weights[l], want)
		}
	}
	if spec.Normalize {
		t.Error("normalize: false resolved true")
	}

	spec, _ = Resolve(json.RawMessage(`{"kernel": "geometric", "decay": 0.5, "max_lag": 3}`))
	if !spec.Normalize {
		t.Error("normalize defaults to true")
	}
	sumTo(t, spec.Weights, 1)
	if want := 1 / 1.875; math.Abs(spec.Weights[0]-want) > 1e-15 {
		t.Errorf("geometric normalised w[0] = %v, want %v", spec.Weights[0], want)
	}

	spec, _ = Resolve(json.RawMessage(`{"kernel": "weibull_cdf", "shape": 2, "scale": 3, "max_lag": 4, "normalize": false}`))
	for l := range 5 {
		if want := math.Exp(-math.Pow(float64(l)/3, 2)); math.Abs(spec.Weights[l]-want) > 1e-15 {
			t.Errorf("weibull_cdf w[%d] = %v, want %v", l, spec.Weights[l], want)
		}
	}
	if spec.Weights[0] != 1 {
		t.Errorf("weibull_cdf w[0] = %v, want 1 (survival at lag 0)", spec.Weights[0])
	}

	spec, _ = Resolve(json.RawMessage(`{"kernel": "weibull_pdf", "shape": 2, "scale": 3, "max_lag": 4, "normalize": false}`))
	for l := range 5 {
		x := float64(l + 1)
		want := (2.0 / 3) * (x / 3) * math.Exp(-math.Pow(x/3, 2))
		if math.Abs(spec.Weights[l]-want) > 1e-14*want {
			t.Errorf("weibull_pdf w[%d] = %v, want %v (density at l + 1)", l, spec.Weights[l], want)
		}
	}

	spec, _ = Resolve(json.RawMessage(`{"kernel": "weibull_pdf", "shape": 1.5, "scale": 2.5, "max_lag": 6}`))
	sumTo(t, spec.Weights, 1)

	spec, _ = Resolve(json.RawMessage(`{"kernel": "geometric", "decay": 0, "max_lag": 2}`))
	if spec.Weights[0] != 1 || spec.Weights[1] != 0 || spec.Weights[2] != 0 {
		t.Errorf("decay 0 is the identity kernel, got %v", spec.Weights)
	}
}
