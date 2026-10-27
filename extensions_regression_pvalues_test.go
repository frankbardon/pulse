package pulse_test

import (
	"context"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
)

// RegressionRegistration.PValues (owner decision #1): a model that does
// not declare PValues emits no p-values — a non-none multiplicity block
// is refused identically at predict and at runtime, predict counts none,
// p_values is null on every coefficient (under resample too), and a fit
// that reports a finite p anyway is
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (undeclared_p_value).

const (
	regNoPValues types.RegressionType = "REG_PARITY_NOP"
	regLateP     types.RegressionType = "REG_PARITY_LATEP"
)

// noPValueOLS is the buffered REG_OLS clone with every p-value dropped
// (estimates, standard errors and statistics kept). lateAfter > 0 keeps
// the p-values on a fit over more than lateAfter rows — past the
// pulse.New probe's eight, so only a run reports them.
type noPValueOLS struct {
	bufferedOLS
	lateAfter int
}

func (m noPValueOLS) Fit(rows extend.Rows) (extend.RegressionOutput, error) {
	out, err := m.bufferedOLS.Fit(rows)
	if err != nil || (m.lateAfter > 0 && rows.Len() > m.lateAfter) {
		return out, err
	}
	for i := range out.Coefficients {
		out.Coefficients[i].PValue = nil
	}
	return out, nil
}

func noPValueRegistration(name types.RegressionType, lateAfter int) pulse.RegressionRegistration {
	return pulse.RegressionRegistration{Name: name, Description: "Test-only REG_OLS clone reporting no p-values.",
		Factory: func(spec *types.RegressionSpec, _ *encoding.Schema) (extend.Regression, error) {
			return noPValueOLS{bufferedOLS: bufferedOLS{spec: spec}, lateAfter: lateAfter}, nil
		}}
}

func noPValueSpec(t types.RegressionType, mult *types.RegressionMultiplicity) *types.RegressionSpec {
	return &types.RegressionSpec{Type: t, Name: "fit", Target: "score", Predictors: []string{"qty", "day"}, Multiplicity: mult}
}

// TestExtensions_RegressionPValuesUndeclaredRefusesMultiplicity: a
// non-none block on a PValues=false model is PULSE_MULTIPLICITY_INVALID
// with the same message and details at predict and at runtime; method
// none passes both.
func TestExtensions_RegressionPValuesUndeclaredRefusesMultiplicity(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{noPValueRegistration(regNoPValues, 0)}})
	for _, method := range []types.MultiplicityMethod{types.MultiplicityMethodHolm, types.MultiplicityMethodBH} {
		t.Run(string(method), func(t *testing.T) {
			req := regressionRequest(cohort, noPValueSpec(regNoPValues, &types.RegressionMultiplicity{Method: method}))
			_, err := p.Process(ctx, req)
			ce := requireCodedError(t, err, perr.PULSE_MULTIPLICITY_INVALID)
			if ce.Details["key"] != "method" || ce.Details["value"] != string(method) ||
				!reflect.DeepEqual(ce.Details["valid"], []string{"none"}) {
				t.Fatalf("runtime details = %v", ce.Details)
			}
			pr, err := p.Predict(ctx, req)
			if err != nil || pr.Valid || len(pr.Errors) == 0 {
				t.Fatalf("Predict = %+v / %v, want the refusal", pr, err)
			}
			e := pr.Errors[0]
			if e.Code != string(ce.Code) || e.Message != ce.Message || !reflect.DeepEqual(e.Details, ce.Details) {
				t.Fatalf("predict %s %q %v, runtime %s %q %v", e.Code, e.Message, e.Details, ce.Code, ce.Message, ce.Details)
			}
		})
	}
	t.Run("none", func(t *testing.T) {
		req := regressionRequest(cohort, noPValueSpec(regNoPValues, &types.RegressionMultiplicity{Method: types.MultiplicityMethodNone}))
		if pr, err := p.Predict(ctx, req); err != nil || !pr.Valid {
			t.Fatalf("Predict = %+v / %v, want valid", pr, err)
		}
		processE2E(t, p, req)
	})
}

// TestExtensions_RegressionPValuesUndeclaredEmitsNone: predict counts
// no p-value for a PValues=false model (the declared clone beside it
// still counts its two), and the run's p_values is NaN (null) on every
// coefficient — under a bootstrap and a jackknife too, whose replicate
// standard errors stay.
func TestExtensions_RegressionPValuesUndeclaredEmitsNone(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	regs := append(regressionParityRegistrations(), noPValueRegistration(regNoPValues, 0))
	p, cohort := openE2E(t, pulse.Extensions{Regressions: regs})

	pr, err := p.Predict(ctx, regressionRequest(cohort, noPValueSpec(regNoPValues, nil)))
	if err != nil || !pr.Valid || pr.PValues != nil {
		t.Fatalf("Predict p_values = %+v (valid %v, err %v), want none", pr.PValues, pr.Valid, err)
	}
	both := regressionRequest(cohort, noPValueSpec(regNoPValues, nil), noPValueSpec(regParityOLS, nil))
	both.Regressions[1].Name = "declared"
	if pr, err := p.Predict(ctx, both); err != nil || pr.PValues == nil || pr.PValues.Total != 2 {
		t.Fatalf("Predict p_values = %+v / %v, want the declared model's 2 only", pr.PValues, err)
	}

	for _, resample := range []string{"", "bootstrap", "jackknife"} {
		t.Run("resample_"+resample, func(t *testing.T) {
			spec := noPValueSpec(regNoPValues, nil)
			spec.Resample = resample
			if resample == "bootstrap" {
				spec.BootstrapIters, spec.RNGSeed = 40, 7
			}
			res := processE2E(t, p, regressionRequest(cohort, spec)).Regressions[0]
			if res == nil || res.Error != nil || len(res.PValues) != 3 {
				t.Fatalf("result = %+v", res)
			}
			for k, v := range res.PValues {
				if !math.IsNaN(v) {
					t.Errorf("p_values[%s] = %v, want NaN (null)", k, v)
				}
				if se := res.StdErrors[k]; math.IsNaN(se) || se <= 0 {
					t.Errorf("std_errors[%s] = %v, want a finite standard error", k, se)
				}
			}
		})
	}
}

// TestExtensions_RegressionPValuesUndeclaredOutputMismatch: a
// PValues=false model reporting a finite p-value is
// PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH (undeclared_p_value) — at
// pulse.New's probe, and on a run when it reports one only over the
// real rows.
func TestExtensions_RegressionPValuesUndeclaredOutputMismatch(t *testing.T) {
	t.Parallel()

	probe := noPValueRegistration(regLateP, 1) // every fit past one row keeps its p
	_, err := pulse.New(newTestOptions(pulse.Extensions{Regressions: []pulse.RegressionRegistration{probe}}))
	ce := requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
	if ce.Details["reason"] != "undeclared_p_value" || ce.Details["coefficient"] != "(intercept)" {
		t.Fatalf("probe details = %v", ce.Details)
	}

	p, cohort := openE2E(t, pulse.Extensions{Regressions: []pulse.RegressionRegistration{noPValueRegistration(regLateP, 8)}})
	_, err = p.Process(context.Background(), regressionRequest(cohort, noPValueSpec(regLateP, nil)))
	ce = requireCodedError(t, err, perr.PULSE_EXTENSION_REGRESSION_OUTPUT_MISMATCH)
	if ce.Details["reason"] != "undeclared_p_value" || ce.Details["name"] != string(regLateP) {
		t.Fatalf("runtime details = %v", ce.Details)
	}
}

// TestExtensions_RegressionPValuesManifest: the manifest row surfaces
// the declaration — a PValues model lists the multiplicity slot and an
// emits note naming the correction; a model without lists no
// multiplicity slot and says its p_values are null.
func TestExtensions_RegressionPValuesManifest(t *testing.T) {
	t.Parallel()

	regs := append(regressionParityRegistrations(), noPValueRegistration(regNoPValues, 0))
	p, _ := openE2E(t, pulse.Extensions{Regressions: regs})
	rows := map[string]bool{}
	for _, row := range p.Manifest(context.Background()).Extensions.Regressions {
		listed := false
		for _, prm := range row.Params {
			listed = listed || prm.Slot && prm.Name == "multiplicity"
		}
		rows[row.Name] = listed
		declared := row.Name != string(regNoPValues)
		if listed != declared {
			t.Errorf("%s: multiplicity slot listed %v, want %v", row.Name, listed, declared)
		}
		if note := strings.Contains(row.EmitsTypeNote, "declares no PValues"); note == declared {
			t.Errorf("%s: emits note %q", row.Name, row.EmitsTypeNote)
		}
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %v", rows)
	}
}
