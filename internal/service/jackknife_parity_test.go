package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

// jackknife_parity_test.go: the jackknife knobs (jackknife_groups /
// jackknife_by, internal/regresample) are refused by Process and by
// Predict with the same code, message and details; an unknown
// jackknife_by is the field-reference refusal on both; a weighted
// jackknife (JK1) applies on both while a weighted bootstrap stays
// refused on both; and a delete-group count above the fitted rows fails
// the slot alone through the per-slot error path.

func jackknifeMtcars(t *testing.T) (svc *Service, cohort string, data []byte) {
	t.Helper()
	var golden struct {
		Cases []vcovOracle `json:"cases"`
	}
	dir := loadMV(t, "mv_vcov.json", &golden)
	cohort = filepath.Join(dir, "mtcars.pulse")
	data, err := os.ReadFile(cohort)
	if err != nil {
		t.Fatal(err)
	}
	return New(fixtureFS(t, dir)), cohort, data
}

func TestJackknife_PredictRuntimeParity(t *testing.T) {
	svc, cohort, data := jackknifeMtcars(t)
	ols := types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"}, Resample: "jackknife"}
	cases := []struct {
		name string
		mut  func(*types.RegressionSpec)
		code errors.Code
	}{
		{"groups and by", func(s *types.RegressionSpec) { s.JackknifeGroups, s.JackknifeBy = 4, "cyl" }, errors.PROCESSING_CONFIG},
		{"groups one", func(s *types.RegressionSpec) { s.JackknifeGroups = 1 }, errors.PROCESSING_CONFIG},
		{"groups negative", func(s *types.RegressionSpec) { s.JackknifeGroups = -2 }, errors.PROCESSING_CONFIG},
		{"groups on bootstrap", func(s *types.RegressionSpec) { s.Resample, s.JackknifeGroups = "bootstrap", 4 }, errors.PROCESSING_CONFIG},
		{"by without resample", func(s *types.RegressionSpec) { s.Resample, s.JackknifeBy = "", "cyl" }, errors.PROCESSING_CONFIG},
		{"by unknown field", func(s *types.RegressionSpec) { s.JackknifeBy = "nope" }, ""},
		{"weighted bootstrap", func(s *types.RegressionSpec) {
			s.Resample = "bootstrap"
			s.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})
		}, errors.PULSE_WEIGHT_UNSUPPORTED},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spec := ols
			c.mut(&spec)
			req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{&spec}}
			_, err := svc.Process(context.Background(), req)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || (c.code != "" && ce.Code != c.code) {
				t.Fatalf("Process err = %v, want %s", err, c.code)
			}
			pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
			found := false
			for _, e := range pred.Errors {
				if e.Code == string(ce.Code) && e.Message == ce.Message {
					found = true
				}
			}
			if !found {
				t.Errorf("predict errors %+v lack %s %q", pred.Errors, ce.Code, ce.Message)
			}
		})
	}
}

// TestJackknife_WeightedAndGroupedRun: a frequency-weighted
// delete-a-cluster jackknife runs (predict reports the weight applied,
// the result carries sum_weights and the cluster count), and
// jackknife_groups above the fitted rows fails the slot alone.
func TestJackknife_WeightedAndGroupedRun(t *testing.T) {
	svc, cohort, data := jackknifeMtcars(t)
	spec := &types.RegressionSpec{Name: "fit", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt", "hp"},
		Resample: "jackknife", JackknifeBy: "cyl",
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w_freq", Kind: types.WeightKindFrequency})}
	req := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{spec}}
	pred := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{})
	if len(pred.Errors) != 0 {
		t.Fatalf("predict refused: %+v", pred.Errors)
	}
	pr := pred.Data.(*descriptor.PredictResult)
	if len(pr.Weights) != 1 || pr.Weights[0].Status != descriptor.WeightStatusApplied {
		t.Fatalf("predict weights %+v, want applied", pr.Weights)
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	r := resp.Regressions[0]
	if r.Error != nil || r.SumWeights == 0 || r.JackknifeGroups != 3 || r.Resample != "jackknife" {
		t.Fatalf("weighted cluster jackknife: %+v", r)
	}

	many := &types.RegressionSpec{Name: "many", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt"},
		Resample: "jackknife", JackknifeGroups: 33}
	ok := &types.RegressionSpec{Name: "ok", Type: types.REG_OLS, Target: "mpg", Predictors: []string{"wt"}}
	resp, err = svc.Process(context.Background(), &types.Request{Cohort: &types.Cohort{Filename: cohort}, Regressions: []*types.RegressionSpec{many, ok}})
	if err != nil {
		t.Fatalf("a data refusal failed the request: %v", err)
	}
	if e := resp.Regressions[0].Error; e == nil || e.Code != string(errors.PROCESSING_REGRESSION_INSUFFICIENT_DATA) {
		t.Fatalf("jackknife_groups 33 over 32 rows: %+v", resp.Regressions[0])
	}
	if resp.Regressions[1].Error != nil || resp.Regressions[1].Coefficients == nil {
		t.Fatalf("the sibling slot did not survive: %+v", resp.Regressions[1])
	}
}
