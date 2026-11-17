package pulse

import (
	"context"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestMultinomial_RobustFitSavesAndReloads (U68 E3-S1 hand-off, E3-S2):
// a REG_MULTINOMIAL fit under a cluster-robust covariance (with vcov
// and exponentiate) saves a model file listing features categories and
// robust_covariance; the file decodes and binds back to the fit's
// coefficients, sandwich standard errors, intervals, vcov and
// covariance echo bit for bit, never carries the exponentiated
// reporting block, and the model reloaded through `models` scores every
// row exactly as the bound fit does.
func TestMultinomial_RobustFitSavesAndReloads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var rows [][]string
	for i := 0; i < mnRowsN; i++ {
		rows = append(rows, mnRow(i))
	}
	fsys := afero.NewMemMapFs()
	createTestPulseFile(t, fsys, mnCohort, mnColumns, rows)
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}

	const save = "models/mf-robust.pulse-model.json"
	spec := mnSpec(func(s *types.RegressionSpec) {
		s.Vcov, s.Exponentiate = true, true
		s.Covariance = &types.RegressionCovariance{Type: types.CovarianceCluster, Cluster: "f"}
	})
	creq := &ComposedRequest{
		Fits:     []types.FitSpec{{Name: "mf", Request: mnReq(mnCohort, spec), Save: save}},
		Requests: []*Request{mnScoreSlot(t, mnCohort, "")},
	}
	if env, err := p.PredictCompose(ctx, creq); err != nil || len(env.Errors) != 0 {
		t.Fatalf("predict %v %+v", err, env)
	}
	out, err := p.Compose(ctx, creq)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	fit := out.Fits["mf"]
	if fit == nil || fit.RegressionResult == nil || fit.RegressionResult.Error != nil {
		t.Fatalf("fit %+v", fit)
	}
	res := fit.RegressionResult
	if res.Covariance == nil || res.Covariance.Type != types.CovarianceCluster || res.Covariance.Clusters != 3 || res.Vcov == nil || res.Exponentiated == nil {
		t.Fatalf("robust fit: covariance %+v vcov %v exponentiated %v", res.Covariance, res.Vcov != nil, res.Exponentiated != nil)
	}

	data, err := afero.ReadFile(fsys, save)
	if err != nil {
		t.Fatal(err)
	}
	f, err := model.DecodeFile(save, data)
	if err != nil {
		t.Fatalf("DecodeFile: %v", err)
	}
	if !slices.Contains(f.Features, model.FeatureRobustCovariance) || !slices.Contains(f.Features, "categories") {
		t.Errorf("features %v, want categories and %s", f.Features, model.FeatureRobustCovariance)
	}
	if strings.Contains(string(data), "exponentiated") {
		t.Errorf("the model file carries the exponentiated reporting block")
	}
	b, err := f.Bind("mf")
	if err != nil {
		t.Fatal(err)
	}
	got := b.Result
	if !reflect.DeepEqual(got.Covariance, res.Covariance) || got.Reference != res.Reference || !slices.Equal(got.Categories, res.Categories) {
		t.Errorf("bound covariance %+v reference %q categories %v, fit %+v %q %v", got.Covariance, got.Reference, got.Categories, res.Covariance, res.Reference, res.Categories)
	}
	same := func(what string, a, w map[string]float64) {
		t.Helper()
		if len(a) != len(w) || len(w) == 0 {
			t.Fatalf("%s: %d keys, fit %d", what, len(a), len(w))
		}
		for k, v := range w {
			if math.Float64bits(a[k]) != math.Float64bits(v) {
				t.Errorf("%s[%s] = %v, fit %v", what, k, a[k], v)
			}
		}
	}
	same("coefficients", got.Coefficients, res.Coefficients)
	same("std_errors", got.StdErrors, res.StdErrors)
	same("ci_lower", got.CILower, res.CILower)
	same("ci_upper", got.CIUpper, res.CIUpper)
	if got.Vcov == nil || !reflect.DeepEqual(got.Vcov.Values, res.Vcov.Values) {
		t.Errorf("bound vcov %v, fit %v", got.Vcov, res.Vcov)
	}

	bound := mustJSON(t, "bound", out.Responses[0].Data)
	loaded, err := p.Process(ctx, mnScoreSlot(t, mnCohort, save))
	if err != nil {
		t.Fatalf("models reload: %v", err)
	}
	if a := mustJSON(t, "reloaded", loaded.Data); a != bound {
		t.Error("the reloaded robust model scores differently from the bound fit")
	}
}
