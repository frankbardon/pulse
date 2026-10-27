package pulse_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/regresample"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestPredictCompose_JackknifeCostAdvisoryMatchesPredict: through the
// facade, a leave-one-out jackknife over a cohort of exactly
// LeaveOneOutCostRows records draws PULSE_ADVISORY_JACKKNIFE_COST from
// Process predict, from a Compose slot and from a Compose fit — the
// same figures, the Compose ones attributed (the slot path prefixed,
// details.request / details.fit). The Compose count is the cohort's
// header count (Service.CohortCounts), never a record read.
func TestPredictCompose_JackknifeCostAdvisoryMatchesPredict(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	var b strings.Builder
	b.WriteString("x,y\n")
	for i := range regresample.LeaveOneOutCostRows {
		fmt.Fprintf(&b, "%d,%d\n", i%97, (i*7)%13)
	}
	if err := afero.WriteFile(fs, "jk.csv", []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	imp, err := p.ImportFile(ctx, pulse.ImportSpec{SourcePath: "jk.csv"})
	if err != nil {
		t.Fatal(err)
	}
	request := func() *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: imp.Path}, Regressions: []*types.RegressionSpec{{
			Name: "m", Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}, Resample: "jackknife",
		}}}
	}
	single, err := p.Predict(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	want := jackknifeCost(single.Advisories)
	if len(want) != 1 {
		t.Fatalf("vacuous: predict advisories = %+v", single.Advisories)
	}
	env, err := p.PredictCompose(ctx, &types.ComposedRequest{
		Requests: []*types.Request{request()},
		Fits:     []types.FitSpec{{Name: "f", Request: request()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := jackknifeCost(env.Data.(*pulse.ComposePredictResult).Advisories)
	if len(got) != 2 {
		t.Fatalf("compose advisories = %+v, want slot + fit", got)
	}
	for i, w := range []struct{ prefix, key string }{{"requests[0].", "request"}, {"fits[0].request.", "fit"}} {
		details := map[string]any{w.key: 0}
		for k, v := range want[0].Details {
			details[k] = v
		}
		details["slot"] = w.prefix + "regressions[0]"
		if !reflect.DeepEqual(got[i].Details, details) {
			t.Errorf("%s: details %v, want %v", w.key, got[i].Details, details)
		}
	}
}

func jackknifeCost(as []descriptor.Advisory) []descriptor.Advisory {
	var out []descriptor.Advisory
	for _, a := range as {
		if a.Code == string(errors.PULSE_ADVISORY_JACKKNIFE_COST) {
			out = append(out, a)
		}
	}
	return out
}
