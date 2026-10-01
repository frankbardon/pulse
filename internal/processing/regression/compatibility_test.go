package regression

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestRegression_CompatibilityMatrix exercises every cell of the
// modifier compatibility matrix locked in the Phase 5 plan:
//
//	                      | Jackknife | Bootstrap | Forward | Backward | Stepwise
//		REG_OLS no penalty    | yes       | yes       | yes     | yes      | yes
//		REG_OLS regularized   | yes       | yes       | warn    | warn     | warn
//		REG_GLM               | yes       | yes       | yes     | yes      | yes
//		REG_BAYES_LINEAR      | no        | no        | no      | no       | no
//
// "yes" cells must construct an engine without error. "warn" cells
// (regularized OLS + selection) build at the engine layer; the warning
// is emitted by descriptor.validateRegressions on the predict path —
// that interaction is tested separately in descriptor/. "no" cells
// (Bayes + any modifier) reject at spec validation with
// PROCESSING_CONFIG.
//
// We only construct the engine here; the fixtures above verify the
// fits run end-to-end on real data. This matrix is a contract test:
// the *factory* must accept or reject the right combinations.
func TestRegression_CompatibilityMatrix(t *testing.T) {
	type cell struct {
		name      string
		spec      *types.RegressionSpec
		wantError bool
		wantCode  errors.Code
	}

	build := func(spec *types.RegressionSpec) error {
		// Use a schema large enough to satisfy every predictor name we test.
		fields := []string{spec.Target}
		fields = append(fields, spec.Predictors...)
		schema := schemaWithFields(fields...)
		_, err := Build([]*types.RegressionSpec{spec}, schema)
		return err
	}

	cells := []cell{
		// REG_OLS no penalty × every modifier — accepted.
		{name: "OLS jackknife", spec: olsModSpec("", "jackknife", "", "")},
		{name: "OLS bootstrap", spec: olsModSpec("", "bootstrap", "", "")},
		{name: "OLS forward", spec: olsModSpec("", "", "forward", "aic")},
		{name: "OLS backward", spec: olsModSpec("", "", "backward", "aic")},
		{name: "OLS stepwise", spec: olsModSpec("", "", "stepwise", "aic")},

		// REG_OLS regularized × every modifier — accepted (warning is at predict layer).
		{name: "OLS l2 + jackknife", spec: olsModSpec("l2", "jackknife", "", "")},
		{name: "OLS l1 + bootstrap", spec: olsModSpec("l1", "bootstrap", "", "")},
		{name: "OLS l2 + forward (warn)", spec: olsModSpec("l2", "", "forward", "aic")},
		{name: "OLS l1 + stepwise (warn)", spec: olsModSpec("l1", "", "stepwise", "bic")},

		// REG_GLM × every modifier — accepted.
		{name: "GLM jackknife", spec: glmModSpec("jackknife", "", "")},
		{name: "GLM bootstrap", spec: glmModSpec("bootstrap", "", "")},
		{name: "GLM forward", spec: glmModSpec("", "forward", "aic")},
		{name: "GLM backward", spec: glmModSpec("", "backward", "aic")},
		{name: "GLM stepwise", spec: glmModSpec("", "stepwise", "aic")},

		// REG_BAYES_LINEAR × every modifier — rejected at validation.
		{name: "Bayes jackknife (rejected)", spec: bayesModSpec("jackknife", "", ""), wantError: true, wantCode: errors.PROCESSING_CONFIG},
		{name: "Bayes bootstrap (rejected)", spec: bayesModSpec("bootstrap", "", ""), wantError: true, wantCode: errors.PROCESSING_CONFIG},
		{name: "Bayes forward (rejected)", spec: bayesModSpec("", "forward", "aic"), wantError: true, wantCode: errors.PROCESSING_CONFIG},
		{name: "Bayes backward (rejected)", spec: bayesModSpec("", "backward", "bic"), wantError: true, wantCode: errors.PROCESSING_CONFIG},
		{name: "Bayes stepwise (rejected)", spec: bayesModSpec("", "stepwise", "aic"), wantError: true, wantCode: errors.PROCESSING_CONFIG},
	}

	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			err := build(c.spec)
			if c.wantError {
				if err == nil {
					t.Fatalf("expected error %v, got nil", c.wantCode)
				}
				if !errors.HasCode(err, c.wantCode) {
					t.Errorf("error code mismatch: got %v, want %v", err, c.wantCode)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected build error: %v", err)
			}
		})
	}
}

// olsModSpec is a small helper to build a REG_OLS spec with the requested
// modifier knobs. Predictors are fixed at {x1, x2, x3} so the schema is
// reusable.
func olsModSpec(penalty, resample, selection, criterion string) *types.RegressionSpec {
	spec := &types.RegressionSpec{
		Type:       types.REG_OLS,
		Target:     "y",
		Predictors: []string{"x1", "x2", "x3"},
		Resample:   resample,
		Selection:  selection,
		Criterion:  criterion,
	}
	if penalty != "" {
		spec.Penalty = penalty
		spec.Alpha = 0.1
		if penalty == "elasticnet" {
			spec.L1Ratio = 0.5
		}
	}
	return spec
}

func glmModSpec(resample, selection, criterion string) *types.RegressionSpec {
	return &types.RegressionSpec{
		Type:       types.REG_GLM,
		Target:     "y",
		Predictors: []string{"x1", "x2", "x3"},
		Family:     "binomial",
		Link:       "logit",
		Resample:   resample,
		Selection:  selection,
		Criterion:  criterion,
	}
}

func bayesModSpec(resample, selection, criterion string) *types.RegressionSpec {
	return &types.RegressionSpec{
		Type:       types.REG_BAYES_LINEAR,
		Target:     "y",
		Predictors: []string{"x1", "x2", "x3"},
		Resample:   resample,
		Selection:  selection,
		Criterion:  criterion,
	}
}
