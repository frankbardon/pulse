package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// RegressionRegistration.Compare (U68 E5-S1, D10): a registration that
// declares it can be compared as nested models by a Compose `compare`
// entry — the model reports its figures (extend.CompareRegression), the
// engine runs the likelihood ratio and Wald tests; one that does not is
// refused type_unsupported at predict and at runtime alike; a declared
// model without the interface, or with figures off its spec, fails
// pulse.New's probe.

const (
	regCompareOLS        types.RegressionType = "REG_PARITY_COMPARE"
	regCompareUndeclared types.RegressionType = "REG_PARITY_NOCOMPARE"
)

// compareOLS is the buffered REG_OLS clone reporting comparison figures
// over its coefficients: an identity covariance and the deviance
// 100 − 10·(predictors), so the likelihood ratio of one added predictor
// is 10 and the Wald statistic the squared estimate. shape, when set,
// rewrites the figures (a broken model).
type compareOLS struct {
	bufferedOLS
	last  *extend.RegressionOutput
	shape func(*extend.CompareOutput)
	calls *int
}

func (m *compareOLS) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := m.bufferedOLS.Fit(rows)
	if err == nil {
		m.last = &out
	}
	return out, err
}

func (m *compareOLS) CompareFigures() (extend.CompareOutput, error) {
	if m.calls != nil {
		*m.calls++
	}
	out := extend.CompareOutput{Deviance: 100 - float64(10*len(m.spec.Predictors)), DFResidual: 7}
	for i, c := range m.last.Coefficients {
		out.Terms = append(out.Terms, c.Name)
		out.Coefficients = append(out.Coefficients, c.Estimate)
		row := make([]float64, len(m.last.Coefficients))
		row[i] = 1
		out.Covariance = append(out.Covariance, row)
	}
	if m.shape != nil {
		m.shape(&out)
	}
	return out, nil
}

// compareRegistration registers the clone under name, declaring Compare
// when declare; calls, when set, counts CompareFigures calls.
func compareRegistration(name types.RegressionType, declare bool, calls *int) pulse.RegressionRegistration {
	return pulse.RegressionRegistration{Name: name, Description: "Test-only REG_OLS clone reporting comparison figures.", PValues: true, Compare: declare,
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return &compareOLS{bufferedOLS: bufferedOLS{spec: spec}, calls: calls}, nil
		}}
}

func compareBatch(cohort *types.Cohort, typ types.RegressionType, test types.ComparisonTest) *pulse.ComposedRequest {
	fit := func(preds ...string) *types.Request {
		return regressionRequest(cohort, &types.RegressionSpec{Type: typ, Target: "score", Predictors: preds})
	}
	return &pulse.ComposedRequest{
		Fits: []types.FitSpec{{Name: "m0", Request: fit("qty")}, {Name: "m1", Request: fit("qty", "day")}},
		Requests: []*types.Request{{Label: "rows", Cohort: cohort,
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}}},
		Compare: []types.FitComparisonSpec{{Name: "c", Restricted: "m0", Full: "m1", Test: test}},
	}
}

// TestExtensions_RegressionCompareDeclaredRuns: two fits of a declared
// model compare through its figures — the likelihood ratio is the
// deviance change (the default test), the Wald statistic b_Sᵀ V_SS⁻¹ b_S
// over the full fit's tested predictor, both χ²(1) — and predict's
// summary is what the run reports. CompareFigures runs only on a fit a
// comparison names.
func TestExtensions_RegressionCompareDeclaredRuns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	calls := 0
	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{compareRegistration(regCompareOLS, true, &calls)}})
	probeCalls := calls
	for _, tc := range []struct {
		test types.ComparisonTest
		want func(full *types.RegressionResult) float64
	}{
		{"", func(*types.RegressionResult) float64 { return 10 }},
		{types.ComparisonTestWald, func(full *types.RegressionResult) float64 { return full.Coefficients["day"] * full.Coefficients["day"] }},
	} {
		req := compareBatch(cohort, regCompareOLS, tc.test)
		resp, err := p.Compose(ctx, req)
		if err != nil {
			t.Fatalf("%q: Compose: %v", tc.test, err)
		}
		c := resp.Comparisons[0]
		if c.Error != nil {
			t.Fatalf("%q: comparison failed: %+v", tc.test, c.Error)
		}
		want := tc.want(resp.Fits["m1"].RegressionResult)
		if math.Abs(c.Statistic-want) > 1e-12*math.Max(1, want) || c.DF != 1 || c.Distribution != types.ComparisonDistributionChiSquare ||
			!slices.Equal(c.Terms, []string{"day"}) || c.FullDFResidual != 7 {
			t.Fatalf("%q: comparison %+v, want statistic %v", tc.test, c, want)
		}
		env, err := p.PredictCompose(ctx, req)
		if err != nil || len(env.Errors) > 0 {
			t.Fatalf("%q: predict refused: %v %+v", tc.test, err, env.Errors)
		}
	}
	if calls-probeCalls != 4 {
		t.Fatalf("CompareFigures ran %d times over two compared batches, want 4", calls-probeCalls)
	}
	// A fit no comparison names is never asked.
	before := calls
	processE2E(t, p, regressionRequest(cohort, &types.RegressionSpec{Type: regCompareOLS, Target: "score", Predictors: []string{"qty"}}))
	if calls != before {
		t.Fatalf("CompareFigures ran on an uncompared fit")
	}
}

// TestExtensions_RegressionCompareRefusalsPredictMatchesRuntime: an
// undeclared model is refused type_unsupported (requires naming the
// declared-compare rule), a declared one's F test estimated_dispersion —
// the same code, message and details at predict and at runtime.
func TestExtensions_RegressionCompareRefusalsPredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{
		compareRegistration(regCompareOLS, true, nil), compareRegistration(regCompareUndeclared, false, nil)}})
	for _, tc := range []struct {
		name     string
		req      *pulse.ComposedRequest
		reason   string
		requires string
	}{
		{"undeclared", compareBatch(cohort, regCompareUndeclared, ""), "type_unsupported", "type REG_OLS, REG_GLM, REG_MULTINOMIAL or declared_compare"},
		{"f", compareBatch(cohort, regCompareOLS, types.ComparisonTestF), "requires", "estimated_dispersion"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := p.Compose(ctx, tc.req)
			ce := requireCodedError(t, err, perr.PULSE_COMPARISON_INVALID)
			if ce.Details["reason"] != tc.reason || ce.Details["requires"] != tc.requires {
				t.Fatalf("runtime details %v, want reason %s requires %q", ce.Details, tc.reason, tc.requires)
			}
			env, err := p.PredictCompose(ctx, tc.req)
			if err != nil || len(env.Errors) == 0 {
				t.Fatalf("PredictCompose = %+v / %v, want the refusal", env, err)
			}
			got := env.Errors[0]
			a, _ := json.Marshal(got.Details)
			b, _ := json.Marshal(ce.Details)
			if got.Code != string(ce.Code) || got.Message != ce.Message || string(a) != string(b) {
				t.Errorf("predict %s %q %s, runtime %s %q %s", got.Code, got.Message, a, ce.Code, ce.Message, b)
			}
		})
	}
}

// noCompareOLS declares Compare (in its registration) without
// implementing extend.CompareRegression.
type noCompareOLS struct{ bufferedOLS }

// panicCompareOLS panics in CompareFigures.
type panicCompareOLS struct{ compareOLS }

func (*panicCompareOLS) CompareFigures() (extend.CompareOutput, error) {
	panic("figures exploded")
}

// TestExtensions_RegressionCompareProbed: pulse.New maps a declared
// registration's probe fit with a figures offer installed — a model
// without the interface is missing_compare; figures with no term, a
// repeated or empty term, a missing predictor, misaligned coefficients
// or a non-square covariance compare_shape; a CompareFigures panic
// FACTORY_PANIC (stage compare). An undeclared registration is never
// asked.
func TestExtensions_RegressionCompareProbed(t *testing.T) {
	t.Parallel()

	reg := func(f func(spec *types.RegressionSpec) extend.Regression) pulse.RegressionRegistration {
		return pulse.RegressionRegistration{Name: regCompareOLS, Description: "Test-only broken compare model.", PValues: true, Compare: true,
			Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) { return f(spec), nil }}
	}
	shaped := func(shape func(*extend.CompareOutput)) pulse.RegressionRegistration {
		return reg(func(spec *types.RegressionSpec) extend.Regression {
			return &compareOLS{bufferedOLS: bufferedOLS{spec: spec}, shape: shape}
		})
	}
	for _, tc := range []struct {
		name   string
		reg    pulse.RegressionRegistration
		code   perr.Code
		reason string
	}{
		{"no interface", reg(func(spec *types.RegressionSpec) extend.Regression { return noCompareOLS{bufferedOLS{spec: spec}} }),
			perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "missing_compare"},
		{"no term", shaped(func(o *extend.CompareOutput) { *o = extend.CompareOutput{} }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"empty term", shaped(func(o *extend.CompareOutput) { o.Terms[0] = "" }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"repeated term", shaped(func(o *extend.CompareOutput) { o.Terms[0] = o.Terms[1] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"missing predictor", shaped(func(o *extend.CompareOutput) { o.Terms[len(o.Terms)-1] = "other" }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"misaligned coefficients", shaped(func(o *extend.CompareOutput) { o.Coefficients = o.Coefficients[1:] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"short covariance", shaped(func(o *extend.CompareOutput) { o.Covariance = o.Covariance[1:] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"ragged covariance", shaped(func(o *extend.CompareOutput) { o.Covariance[0] = o.Covariance[0][1:] }), perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH, "compare_shape"},
		{"panic", reg(func(spec *types.RegressionSpec) extend.Regression {
			return &panicCompareOLS{compareOLS{bufferedOLS: bufferedOLS{spec: spec}}}
		}), perr.PULSE_EXTENSION_FACTORY_PANIC, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{tc.reg}}})
			ce := requireCodedError(t, err, tc.code)
			if tc.reason != "" && ce.Details["reason"] != tc.reason {
				t.Fatalf("details %v, want reason %s", ce.Details, tc.reason)
			}
			if tc.reason == "" && ce.Details["stage"] != "compare" {
				t.Fatalf("details %v, want stage compare", ce.Details)
			}
		})
	}
	undeclared := reg(func(spec *types.RegressionSpec) extend.Regression { return noCompareOLS{bufferedOLS{spec: spec}} })
	undeclared.Compare = false
	if _, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs(), Extensions: pulse.Extensions{Regressions: []pulse.RegressionRegistration{undeclared}}}); err != nil {
		t.Fatalf("pulse.New refused an undeclared registration: %v", err)
	}
}
