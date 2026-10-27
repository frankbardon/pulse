package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// TestRegPenalty_InferenceAdvisoryPredictRuntimeParity: an l1 /
// elasticnet REG_OLS or REG_GLM fit without resample reports its
// standard errors, p-values and intervals NaN on every coefficient and
// carries PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED on the result —
// the code, message and details predict raises for the same request.
// Ridge, unpenalized, resampled fits carry none in either place. Never a
// warning; Strict still runs; Options.SuppressAdvisories drops it from
// both.
func TestRegPenalty_InferenceAdvisoryPredictRuntimeParity(t *testing.T) {
	t.Parallel()

	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	code := string(errors.PULSE_ADVISORY_PENALIZED_INFERENCE_UNDEFINED)
	ols := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Name: "ols", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp", "qsec"}}
		mut(s)
		return s
	}
	glm := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Name: "glm", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}}
		mut(s)
		return s
	}
	cases := []struct {
		name string
		regs []*types.RegressionSpec
		null []bool // per slot: inference undefined + advisory expected
	}{
		{"ols l1 beside glm elasticnet", []*types.RegressionSpec{
			ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.5 }),
			glm(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.01, 0.5 }),
		}, []bool{true, true}},
		{"ols elasticnet beside ridge", []*types.RegressionSpec{
			ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.5 }),
			ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.5, 0.3 }),
		}, []bool{false, true}},
		{"glm l1 under selection", []*types.RegressionSpec{
			glm(func(s *types.RegressionSpec) {
				s.Penalty, s.Alpha, s.Selection, s.Criterion = "l1", 0.01, "backward", "aic"
			}),
		}, []bool{true}},
		{"l1 under jackknife, unpenalized glm", []*types.RegressionSpec{
			ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Resample = "l1", 0.5, "jackknife" }),
			glm(func(*types.RegressionSpec) {}),
		}, []bool{false, false}},
	}
	for _, strict := range []bool{false, true} {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: c.regs}
				svc := New(cfg)
				svc.SetStrict(strict)
				resp, err := svc.Process(context.Background(), req)
				if err != nil {
					t.Fatalf("Process (strict %v): %v", strict, err)
				}
				for _, w := range resp.Warnings {
					if w.Code == code {
						t.Errorf("advisory surfaced as a warning: %+v", w)
					}
				}
				env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{Strict: strict})
				var predicted []descriptor.Advisory
				for _, a := range env.Data.(*descriptor.PredictResult).Advisories {
					if a.Code == code {
						predicted = append(predicted, a)
					}
				}
				var got []descriptor.Advisory
				for i, r := range resp.Regressions {
					if r.Error != nil {
						t.Fatalf("slot %d failed: %+v", i, r.Error)
					}
					if (len(r.Advisories) == 1) != c.null[i] || len(r.Advisories) > 1 {
						t.Errorf("slot %d advisories %+v, want inference-undefined %v", i, r.Advisories, c.null[i])
					}
					got = append(got, r.Advisories...)
					for key, se := range r.StdErrors {
						p := r.PValues[key]
						if c.null[i] != (math.IsNaN(se) && math.IsNaN(p) && math.IsNaN(r.CILower[key]) && math.IsNaN(r.CIUpper[key])) {
							t.Errorf("slot %d %s: se %v p %v ci [%v, %v]; want null %v", i, key, se, p, r.CILower[key], r.CIUpper[key], c.null[i])
						}
					}
				}
				if !reflect.DeepEqual(got, predicted) {
					t.Errorf("runtime advisories %+v\npredict advisories %+v", got, predicted)
				}
			})
		}
	}

	// Suppressed on the instance: neither predict nor the result carries it.
	suppressed := (*descx.InstanceSnapshot)(nil).WithSuppressedAdvisories([]string{code})
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: cases[0].regs}
	svc := New(cfg)
	svc.SetInstanceSnapshot(suppressed)
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resp.Regressions {
		if len(r.Advisories) != 0 {
			t.Errorf("suppressed: result carries %+v", r.Advisories)
		}
		if !math.IsNaN(r.StdErrors["(intercept)"]) {
			t.Errorf("suppression moved a figure: intercept SE %v", r.StdErrors["(intercept)"])
		}
	}
	env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{Instance: suppressed})
	for _, a := range env.Data.(*descriptor.PredictResult).Advisories {
		if a.Code == code {
			t.Errorf("suppressed: predict carries %+v", a)
		}
	}
}

// TestRegPenalty_PValueCountPredictRuntimeParity (reference-oracles
// E2-S3): predict's PredictResult.PValues counts exactly the regression
// p-values the runtime reports DEFINED. An l1 / elasticnet REG_OLS or
// REG_GLM fit without resample reports every p-value null, so predict
// counts none of its predictors — neither uncorrected (the
// PULSE_ADVISORY_MANY_TESTS input) nor, under the fit's own multiplicity
// block, corrected: the runtime fold leaves a null p out of m (it is
// counted in excluded instead). Ridge, unpenalized and resampled l1 fits
// keep one defined p-value per predictor in both places.
func TestRegPenalty_PValueCountPredictRuntimeParity(t *testing.T) {
	t.Parallel()

	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cfg := fixtureFS(t, dir)
	cohort := filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	ols := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Name: "ols", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp", "qsec"}}
		mut(s)
		return s
	}
	glm := func(mut func(*types.RegressionSpec)) *types.RegressionSpec {
		s := &types.RegressionSpec{Name: "glm", Type: types.REG_GLM, Family: "binomial", Target: "am", Predictors: []string{"wt", "hp"}}
		mut(s)
		return s
	}
	block := func(s *types.RegressionSpec) {
		s.Multiplicity = &types.RegressionMultiplicity{Method: types.MultiplicityMethodHolm}
	}
	cases := []struct {
		name string
		regs []*types.RegressionSpec
	}{
		{"ols l1", []*types.RegressionSpec{ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.5 })}},
		{"glm elasticnet", []*types.RegressionSpec{glm(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.L1Ratio = "elasticnet", 0.01, 0.5 })}},
		{"glm l1 under selection", []*types.RegressionSpec{glm(func(s *types.RegressionSpec) {
			s.Penalty, s.Alpha, s.Selection, s.Criterion = "l1", 0.01, "backward", "aic"
		})}},
		{"ols ridge", []*types.RegressionSpec{ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l2", 0.5 })}},
		// alpha 0.05 keeps every coefficient off 0, so every replicate
		// p-value is defined (a coefficient the penalty zeroes everywhere
		// has a data-dependent NaN p that predict cannot see).
		{"ols l1 under jackknife", []*types.RegressionSpec{ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha, s.Resample = "l1", 0.05, "jackknife" })}},
		{"unpenalized glm", []*types.RegressionSpec{glm(func(*types.RegressionSpec) {})}},
		{"l1 beside unpenalized", []*types.RegressionSpec{
			ols(func(s *types.RegressionSpec) { s.Penalty, s.Alpha = "l1", 0.5 }),
			glm(func(*types.RegressionSpec) {}),
		}},
	}
	for _, withBlock := range []bool{false, true} {
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/block=%v", c.name, withBlock), func(t *testing.T) {
				regs := make([]*types.RegressionSpec, len(c.regs))
				for i, r := range c.regs {
					cp := *r
					if withBlock {
						block(&cp)
					}
					regs[i] = &cp
				}
				req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: regs}
				resp, err := New(cfg).Process(context.Background(), req)
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				defined, m := 0, 0
				for i, r := range resp.Regressions {
					if r.Error != nil {
						t.Fatalf("slot %d failed: %+v", i, r.Error)
					}
					for key, p := range r.PValues {
						if key != "(intercept)" && !math.IsNaN(p) {
							defined++
						}
					}
					// Every block here pools into the one request family,
					// so each member echoes the same family size m.
					if r.Multiplicity != nil {
						m = r.Multiplicity.M
					}
				}
				env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
				if len(env.Errors) > 0 {
					t.Fatalf("predict errors: %+v", env.Errors)
				}
				total, uncorrected := 0, 0
				if pv := env.Data.(*descriptor.PredictResult).PValues; pv != nil {
					total, uncorrected = pv.Total, pv.Uncorrected
				}
				if total != defined {
					t.Errorf("predict counts %d p-values, runtime reports %d defined", total, defined)
				}
				if withBlock {
					if uncorrected != 0 || total-uncorrected != m {
						t.Errorf("predict corrected %d / uncorrected %d, runtime family m %d", total-uncorrected, uncorrected, m)
					}
				} else if uncorrected != defined {
					t.Errorf("predict uncorrected %d, runtime defined %d", uncorrected, defined)
				}
			})
		}
	}
}
