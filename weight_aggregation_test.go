package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// weightedCohort imports a cohort whose `w` column carries one
// negative weight (row 3), so a weighted run excludes and warns.
//
//	x: 2  4  6  8  10
//	w: 1  2  -1 3  0.5
func weightedCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := afero.NewMemMapFs()
	body := "x,w\n2,1\n4,2\n6,-1\n8,3\n10,0.5\n"
	if err := afero.WriteFile(fs, "wc.csv", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.ImportFile(context.Background(), pulse.ImportSpec{SourcePath: "wc.csv"})
	if err != nil {
		t.Fatal(err)
	}
	return fs, res.Path
}

// TestWeight_DefaultWeightsCoreAggregators: Options.DefaultWeight
// weights the core aggregators through the facade — Σw, Σwx, Σwx/Σw —
// excludes the negative weight with one PULSE_WEIGHT_INVALID_ROWS
// warning, reports `applied` from predict, and under Options.Strict the
// warning is the error.
func TestWeight_DefaultWeightsCoreAggregators(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	req := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: cohort},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_SUM, Field: "x", Label: "sum"},
				{Type: types.AGG_AVERAGE, Field: "x", Label: "mean"},
				{Type: types.AGG_MIN, Field: "x", Label: "min"},
			},
		}
	}
	p, err := pulse.New(pulse.Options{FS: fs, DefaultWeight: &types.WeightSpec{Field: "w"}})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	row := resp.Data[0]
	const sumW, sumWX = 1 + 2 + 3 + 0.5, 2*1 + 4*2 + 8*3 + 10*0.5
	if row["n"] != sumW || row["sum"] != sumWX || row["mean"] != sumWX/sumW || row["min"] != 2.0 {
		t.Fatalf("weighted row = %v, want n %v sum %v mean %v min 2 (MIN skipped under a default)", row, sumW, sumWX, sumWX/sumW)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0].Code != string(errors.PULSE_WEIGHT_INVALID_ROWS) {
		t.Fatalf("warnings = %+v, want one PULSE_WEIGHT_INVALID_ROWS", resp.Warnings)
	}
	if c := resp.Components.Aggregations; c[1].SumWeights == nil || *c[1].SumWeights != sumW || c[3].SumWeights != nil {
		t.Fatalf("floor: %+v / %+v", c[1], c[3])
	}

	pr, err := p.Predict(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, w := range pr.Weights {
		status[w.Operator] = w.Status
	}
	if status["AGG_SUM"] != descriptor.WeightStatusApplied || status["AGG_MIN"] != descriptor.WeightStatusSkippedNotWeightAware {
		t.Fatalf("predict statuses = %v", status)
	}

	strict, err := pulse.New(pulse.Options{FS: fs, DefaultWeight: &types.WeightSpec{Field: "w"}, Strict: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = strict.Process(ctx, req())
	if ce := requireCode(t, err, errors.PULSE_WEIGHT_INVALID_ROWS); ce.Details["field"] != "w" {
		t.Fatalf("strict details = %v", ce.Details)
	}
}

// TestWeight_ClassRefusalsMatchPredict: an explicit weight on a
// not-weightable aggregator is PROCESSING_CONFIG — identically at
// runtime and in predict. AGG_CI_* (refused until U12 E5-S1) now
// applies the instance default and reports the weighted floor.
func TestWeight_ClassRefusalsMatchPredict(t *testing.T) {
	fs, cohort := weightedCohort(t)
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs, DefaultWeight: &types.WeightSpec{Field: "w"}})
	if err != nil {
		t.Fatal(err)
	}
	one := func(a *types.Aggregation, reqW *types.WeightSpec) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Weight: reqW, Aggregations: []*types.Aggregation{a}}
	}
	cases := map[string]struct {
		req  *types.Request
		code errors.Code
	}{
		"explicit on MIN":   {one(&types.Aggregation{Type: types.AGG_MIN, Field: "x", Weight: types.SlotWeightField("w")}, nil), errors.PROCESSING_CONFIG},
		"request on MIN":    {one(&types.Aggregation{Type: types.AGG_MIN, Field: "x"}, &types.WeightSpec{Field: "w"}), errors.PROCESSING_CONFIG},
		"weighted mean nil": {one(&types.Aggregation{Type: types.AGG_WEIGHTED_MEAN, Field: "x", Weight: types.NullSlotWeight()}, nil), errors.PROCESSING_CONFIG},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, rerr := p.Process(ctx, tc.req)
			requireCode(t, rerr, tc.code)
			sameEntry(t, predictEnvelope(t, p, fs, cohort, tc.req), rerr)
		})
	}
	resp, err := p.Process(ctx, one(&types.Aggregation{Type: types.AGG_CI_LOWER, Field: "x"}, nil))
	if err != nil {
		t.Fatalf("CI under the default weight: %v", err)
	}
	if c := resp.Components; c == nil || len(c.Aggregations) != 1 || c.Aggregations[0].SumWeights == nil {
		t.Fatalf("CI under the default weight: no weighted floor (%+v)", c)
	}
}
