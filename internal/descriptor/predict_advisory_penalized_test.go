package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func penalizedAdvisories(t *testing.T, regs []*types.RegressionSpec, opts *PredictOptions) (*descriptor.Envelope, []descriptor.Advisory) {
	t.Helper()
	env := predictFromBytes(jackknifeCohort(t, 40), &types.Request{Regressions: regs}, opts)
	res := env.Data.(*descriptor.PredictResult)
	var out []descriptor.Advisory
	for _, a := range res.Advisories {
		if a.Code == string(errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED) {
			out = append(out, a)
		}
	}
	return env, out
}

// TestPredict_AdvisoryPenalizedInference: an l1 / elasticnet REG_OLS or
// REG_GLM slot without resample — the fits whose standard errors and
// p-values the engines report null — fires once per slot, under
// selection too; resample, l2, an unpenalized fit and a hidden type
// never fire. The message and details are regpenalty.InferenceAdvisory's
// (shared with RegressionResult.Advisories), and the old
// PROCESSING_REGRESSION_APPROXIMATE_SE warning is gone.
func TestPredict_AdvisoryPenalizedInference(t *testing.T) {
	t.Parallel()

	ols := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		r := &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1}
		if mut != nil {
			mut(r)
		}
		return r
	}
	glm := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		r := &types.RegressionSpec{Type: types.REG_GLM, Family: "poisson", Target: "y", Predictors: []string{"x"}, Penalty: "elasticnet", Alpha: 0.1, L1Ratio: 0.5}
		if mut != nil {
			mut(r)
		}
		return r
	}
	cases := []struct {
		name  string
		regs  []*types.RegressionSpec
		inst  *InstanceSnapshot
		slots []int
	}{
		{"ols l1", []*types.RegressionSpec{ols(nil)}, nil, []int{0}},
		{"ols elasticnet", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Penalty, r.L1Ratio = "elasticnet", 0.4 })}, nil, []int{0}},
		{"glm elasticnet beside ridge", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Penalty = "l2" }), glm(nil)}, nil, []int{1}},
		{"glm l1", []*types.RegressionSpec{glm(func(r *types.RegressionSpec) { r.Penalty, r.L1Ratio = "l1", 0 })}, nil, []int{0}},
		{"l1 under selection", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Selection, r.Criterion = "forward", "aic" })}, nil, []int{0}},
		{"bootstrap", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Resample = "bootstrap" })}, nil, nil},
		{"jackknife", []*types.RegressionSpec{glm(func(r *types.RegressionSpec) { r.Resample = "jackknife" })}, nil, nil},
		{"ridge", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Penalty = "l2" })}, nil, nil},
		{"unpenalized", []*types.RegressionSpec{ols(func(r *types.RegressionSpec) { r.Penalty, r.Alpha = "", 0 })}, nil, nil},
		{"hidden REG_OLS", []*types.RegressionSpec{ols(nil)}, scopedExcept("REG_OLS"), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			env, got := penalizedAdvisories(t, c.regs, &PredictOptions{Instance: c.inst})
			for _, w := range env.Warnings {
				if w.Code == "PROCESSING_REGRESSION_APPROXIMATE_SE" {
					t.Errorf("the retired APPROXIMATE_SE warning fired: %+v", w)
				}
			}
			if len(got) != len(c.slots) {
				t.Fatalf("advisories = %+v, want %d", got, len(c.slots))
			}
			for k, i := range c.slots {
				reg := c.regs[i]
				slot := "regressions[" + string(rune('0'+i)) + "]"
				want := map[string]any{"slot": slot, "operator": string(reg.Type), "penalty": reg.Penalty,
					"suggested": map[string]any{"resample": "bootstrap"}}
				if !reflect.DeepEqual(got[k].Details, want) {
					t.Errorf("details = %v, want %v", got[k].Details, want)
				}
				if !strings.Contains(got[k].Message, slot) || !strings.Contains(got[k].Message, "null") {
					t.Errorf("message %q names neither the slot nor the nulls", got[k].Message)
				}
				if hits := LintGuidanceText(got[k].Message); len(hits) > 0 {
					t.Errorf("lint hits %+v", hits)
				}
			}
		})
	}
}

// TestPredict_AdvisoryPenalizedInferenceNeverEscalated: strict predict
// keeps the advisory out of errors and warnings (valid stays true), and
// Options.SuppressAdvisories drops it.
func TestPredict_AdvisoryPenalizedInferenceNeverEscalated(t *testing.T) {
	t.Parallel()

	code := string(errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED)
	regs := []*types.RegressionSpec{{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Penalty: "l1", Alpha: 0.1}}
	env, got := penalizedAdvisories(t, regs, &PredictOptions{Strict: true})
	res := env.Data.(*descriptor.PredictResult)
	if len(got) != 1 {
		t.Fatalf("vacuous: advisories = %+v", res.Advisories)
	}
	if !res.Valid || len(env.Errors) != 0 || len(env.Warnings) != 0 {
		t.Errorf("strict: valid=%v errors=%v warnings=%v", res.Valid, env.Errors, env.Warnings)
	}
	suppressed := (*InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})
	if _, got := penalizedAdvisories(t, regs, &PredictOptions{Instance: suppressed}); len(got) != 0 {
		t.Errorf("suppressed: %+v", got)
	}
}
