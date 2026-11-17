package integration_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// RegressionRegistration.Margins (U68 E4-S1, D10): a registration that
// declares it gets the slot's margins block — the model computes each
// effect and its standard error (extend.MarginsRegression), the engine
// the statistic, p-value and interval on the coefficient table's rule —
// judged by the built-ins' shape and fit rules at predict and at runtime
// alike; one that does not is refused (requires declared_margins); a
// declared model without the interface, or with effects off the request,
// fails pulse.New's probe.

const (
	regMarginsOLS        types.RegressionType = "REG_PARITY_MARGINS"
	regMarginsUndeclared types.RegressionType = "REG_PARITY_NOMARGINS"
	regMarginsLevels     types.RegressionType = "REG_PARITY_MLEVELS"
)

// marginsOLS is the buffered REG_OLS clone reporting REG_OLS's own
// margins: on the identity link every effect is the coefficient, with its
// standard error. outcomes, when set, reports every effect once per
// outcome (a per-level model's shape); shape, when set, rewrites the
// effects (a broken model).
type marginsOLS struct {
	bufferedOLS
	last     *extend.RegressionOutput
	outcomes []string
	shape    func(*extend.MarginsOutput)
}

func (m *marginsOLS) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := m.bufferedOLS.Fit(rows)
	if err == nil {
		m.last = &out
	}
	return out, err
}

func (m *marginsOLS) Margins(req extend.MarginsRequest) (extend.MarginsOutput, error) {
	out := extend.MarginsOutput{Outcomes: m.outcomes}
	outcomes := m.outcomes
	if len(outcomes) == 0 {
		outcomes = []string{""}
	}
	for _, o := range outcomes {
		for _, term := range req.Terms {
			for _, c := range m.last.Coefficients {
				if c.Name == term {
					out.Effects = append(out.Effects, extend.MarginalEffect{Outcome: o, Term: term, Estimate: c.Estimate, StdErr: c.StdErr})
				}
			}
		}
	}
	if m.shape != nil {
		m.shape(&out)
	}
	return out, nil
}

// marginsRegistration registers the clone under name, declaring Margins
// when declare.
func marginsRegistration(name types.RegressionType, declare bool, outcomes []string, shape func(*extend.MarginsOutput)) pulse.RegressionRegistration {
	return pulse.RegressionRegistration{Name: name, Description: "Test-only REG_OLS clone reporting its margins.", PValues: true, Margins: declare,
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return &marginsOLS{bufferedOLS: bufferedOLS{spec: spec}, outcomes: outcomes, shape: shape}, nil
		}}
}

func marginsSpec(t types.RegressionType, m *types.RegressionMargins) *types.RegressionSpec {
	return &types.RegressionSpec{Type: t, Name: "fit", Target: "score", Predictors: []string{"qty", "day"}, Margins: m}
}

// TestExtensions_RegressionMarginsDeclaredMatchesBuiltin: a declared
// model's block carries REG_OLS's own figures — the model's effects and
// standard errors, the engine's t statistic, p-value and interval on the
// fit's reported df — keyed by term, with no outcomes.
func TestExtensions_RegressionMarginsDeclaredMatchesBuiltin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{marginsRegistration(regMarginsOLS, true, nil, nil)}})
	resp := processE2E(t, p, regressionRequest(cohort, marginsSpec(types.REG_OLS, &types.RegressionMargins{}), marginsSpec(regMarginsOLS, &types.RegressionMargins{})))
	want, got := resp.Regressions[0].Margins, resp.Regressions[1].Margins
	if want == nil || got == nil {
		t.Fatalf("margins built-in %v, extension %v", want, got)
	}
	if got.Outcomes != nil || !slices.Equal(got.Terms, want.Terms) || got.Type != want.Type || got.Scale != want.Scale || got.ConfLevel != want.ConfLevel || !closeRel(got.DF, want.DF) {
		t.Fatalf("echo %+v, built-in %+v", got, want)
	}
	for _, term := range want.Terms {
		for _, f := range []struct {
			what      string
			got, want float64
		}{
			{"estimate", got.Estimates[term], want.Estimates[term]}, {"std_error", got.StdErrors[term], want.StdErrors[term]},
			{"statistic", got.Statistics[term], want.Statistics[term]}, {"p_value", got.PValues[term], want.PValues[term]},
			{"ci_lower", got.CILower[term], want.CILower[term]}, {"ci_upper", got.CIUpper[term], want.CIUpper[term]},
		} {
			if !closeRel(f.got, f.want) {
				t.Errorf("%s[%s] = %v, REG_OLS %v", f.what, term, f.got, f.want)
			}
		}
	}
	pr, err := p.Predict(ctx, regressionRequest(cohort, marginsSpec(regMarginsOLS, &types.RegressionMargins{})))
	if err != nil || !pr.Valid {
		t.Fatalf("predict refused the declared block: %v %+v", err, pr.Errors)
	}
}

// TestExtensions_RegressionMarginsManifest: the manifest row lists the
// margins slot param exactly when the registration declares Margins.
func TestExtensions_RegressionMarginsManifest(t *testing.T) {
	t.Parallel()

	p, _ := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		marginsRegistration(regMarginsOLS, true, nil, nil), marginsRegistration(regMarginsUndeclared, false, nil, nil)}})
	lists := map[string]bool{}
	for _, row := range p.Manifest(context.Background()).Extensions.Regressions {
		for _, prm := range row.Params {
			if prm.Slot && prm.Name == "margins" {
				lists[row.Name] = true
			}
		}
	}
	if !lists[string(regMarginsOLS)] || lists[string(regMarginsUndeclared)] {
		t.Fatalf("margins slot param listed on %v, want %s only", lists, regMarginsOLS)
	}
}

// TestExtensions_RegressionMarginsPerOutcome: a model reporting
// Outcomes gets "<outcome>:<term>" keys, outcomes in its order, and a
// multiplicity family over every effect.
func TestExtensions_RegressionMarginsPerOutcome(t *testing.T) {
	t.Parallel()

	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{marginsRegistration(regMarginsLevels, true, []string{"lo", "hi"}, nil)}})
	spec := marginsSpec(regMarginsLevels, &types.RegressionMargins{Terms: []string{"day", "qty"}})
	spec.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
	mg := processE2E(t, p, regressionRequest(cohort, spec)).Regressions[0].Margins
	if mg == nil || !slices.Equal(mg.Outcomes, []string{"lo", "hi"}) || !slices.Equal(mg.Terms, []string{"day", "qty"}) {
		t.Fatalf("margins %+v", mg)
	}
	keys := make([]string, 0, len(mg.Estimates))
	for k := range mg.Estimates {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"hi:day", "hi:qty", "lo:day", "lo:qty"}) {
		t.Fatalf("keys %v", keys)
	}
	if mg.Multiplicity == nil || mg.Multiplicity.M != 4 || len(mg.PAdjusted) != 4 {
		t.Fatalf("multiplicity %+v, adjusted %v", mg.Multiplicity, mg.PAdjusted)
	}
}

// TestExtensions_RegressionMarginsRefusalsPredictMatchesRuntime: an
// undeclared model is refused (slot margins, requires declared_margins),
// and a declared one's block meets the built-ins' shape and fit rules —
// the same code, message and details at predict and at runtime.
func TestExtensions_RegressionMarginsRefusalsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		marginsRegistration(regMarginsOLS, true, nil, nil), marginsRegistration(regMarginsUndeclared, false, nil, nil)}})
	for _, tc := range []struct {
		name     string
		spec     *types.RegressionSpec
		requires string
	}{
		{"undeclared", marginsSpec(regMarginsUndeclared, &types.RegressionMargins{}), "declared_margins"},
		{"type", marginsSpec(regMarginsOLS, &types.RegressionMargins{Type: "aie"}), "type ame or mem"},
		{"term", marginsSpec(regMarginsOLS, &types.RegressionMargins{Terms: []string{"score"}}), "a predictor"},
		{"resample", func() *types.RegressionSpec {
			s := marginsSpec(regMarginsOLS, &types.RegressionMargins{})
			s.Resample = "jackknife"
			return s
		}(), "no_resample"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := regressionRequest(cohort, tc.spec)
			_, err := p.Process(ctx, req)
			ce := requireCodedError(t, err, perr.PROCESSING_CONFIG)
			if ce.Details["slot"] != "margins" || ce.Details["requires"] != tc.requires {
				t.Fatalf("runtime details %v, want slot margins requires %q", ce.Details, tc.requires)
			}
			pr, err := p.Predict(ctx, req)
			if err != nil || pr.Valid || len(pr.Errors) == 0 {
				t.Fatalf("Predict = %+v / %v, want the refusal", pr, err)
			}
			got := pr.Errors[0]
			a, _ := json.Marshal(got.Details)
			b, _ := json.Marshal(ce.Details)
			if got.Code != string(ce.Code) || got.Message != ce.Message || string(a) != string(b) {
				t.Errorf("predict %s %q %s, runtime %s %q %s", got.Code, got.Message, a, ce.Code, ce.Message, b)
			}
		})
	}
}

// noMarginsOLS declares Margins (in its registration) without
// implementing extend.MarginsRegression.
type noMarginsOLS struct{ bufferedOLS }

// panicMarginsOLS panics in Margins.
type panicMarginsOLS struct{ marginsOLS }

func (*panicMarginsOLS) Margins(extend.MarginsRequest) (extend.MarginsOutput, error) {
	panic("margins exploded")
}

// TestExtensions_RegressionMarginsProbed: pulse.New fits a declared
// registration with a default margins block — a model without the
// interface is missing_margins, effects off the request margins_shape
// (an unknown term, a repeated effect, a missing one, an outcome its
// Outcomes do not list), a Margins panic FACTORY_PANIC (stage margins).
func TestExtensions_RegressionMarginsProbed(t *testing.T) {
	t.Parallel()

	reg := func(f func(spec *types.RegressionSpec) extend.Regression) pulse.RegressionRegistration {
		return pulse.RegressionRegistration{Name: regMarginsOLS, Description: "Test-only broken margins model.", PValues: true, Margins: true,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) { return f(spec), nil }}
	}
	shaped := func(shape func(*extend.MarginsOutput)) pulse.RegressionRegistration {
		return reg(func(spec *types.RegressionSpec) extend.Regression {
			return &marginsOLS{bufferedOLS: bufferedOLS{spec: spec}, shape: shape}
		})
	}
	for _, tc := range []struct {
		name   string
		reg    pulse.RegressionRegistration
		code   perr.Code
		reason string
	}{
		{"no interface", reg(func(spec *types.RegressionSpec) extend.Regression { return noMarginsOLS{bufferedOLS{spec: spec}} }),
			perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "missing_margins"},
		{"unknown term", shaped(func(o *extend.MarginsOutput) { o.Effects[0].Term = "nope" }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "margins_shape"},
		{"repeated", shaped(func(o *extend.MarginsOutput) { o.Effects[1] = o.Effects[0] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "margins_shape"},
		{"missing", shaped(func(o *extend.MarginsOutput) { o.Effects = o.Effects[:1] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "margins_shape"},
		{"unlisted outcome", shaped(func(o *extend.MarginsOutput) { o.Effects[0].Outcome = "a" }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "margins_shape"},
		{"panic", reg(func(spec *types.RegressionSpec) extend.Regression {
			return &panicMarginsOLS{marginsOLS{bufferedOLS: bufferedOLS{spec: spec}}}
		}), perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{tc.reg}}})
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("details %v, want reason %s", ce.Details, tc.reason)
			}
			if tc.reason == "" && ce.Details["stage"] != "margins" {
				t.Fatalf("details %v, want stage margins", ce.Details)
			}
		})
	}
	// An undeclared registration is never asked: no interface needed.
	undeclared := reg(func(spec *types.RegressionSpec) extend.Regression { return noMarginsOLS{bufferedOLS{spec: spec}} })
	undeclared.Margins = false
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{undeclared}}}); err != nil {
		t.Fatalf("pulse.New refused an undeclared registration: %v", err)
	}
}
