package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// TestFitBound_ParallelDecodeMatchesSerial (U30 E2-S5): a fitted vector
// attribute bound to a prebuilt fit is row-local and mergeable, so the
// parallel decode reducer runs it. The reducer resolves the slot's
// vector input like every other arm, raises the bound fit's warning
// ONCE (not once per worker) in the serial arm's place, and offers a
// Compose capture once — every worker count matches the serial run.
func TestFitBound_ParallelDecodeMatchesSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel reduce equivalence in -short mode")
	}
	path, cfg := writeParallelXYCohort(t, "fit_bound_parallel.pulse")
	fit := func(name string, n int64) *model.VectorFit {
		return &model.VectorFit{Name: name, Labels: []string{"x", "y"}, Members: []string{"x", "y"},
			Basis: model.VectorBasisUnweighted, N: n, NMass: n, SumWeights: float64(n), SumWeightsSq: float64(n),
			Mean: []float64{50, 31}, M2: [][]float64{{8000, 3500}, {3500, 3000}}}
	}
	set := &model.Set{}
	for _, vf := range []*model.VectorFit{fit("pop", 100), fit("tiny", 2)} {
		if err := set.AddVector(vf); err != nil {
			t.Fatal(err)
		}
	}
	mkReq := func(pop string, viaVector bool) *types.Request {
		attr := &types.Attribute{Type: types.ATTR_MAHALANOBIS, Label: "d", Fields: []string{"x", "y"}, Params: json.RawMessage(`{"fit":"` + pop + `"}`)}
		r := &types.Request{
			Cohort:     &types.Cohort{Filename: path},
			Attributes: []*types.Attribute{attr},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "d", Label: "n"},
				{Type: types.AGG_MIN, Field: "d", Label: "lo"},
				{Type: types.AGG_MAX, Field: "d", Label: "hi"},
			},
		}
		if viaVector {
			r.Vectors = []types.VectorSpec{{Name: "battery", Fields: []string{"x", "y"}}}
			attr.Fields, attr.Vector = nil, "battery"
		}
		return r
	}
	wire := func(resp *types.Response) string {
		b, err := types.MarshalFinite(map[string]any{"data": resp.Data, "warnings": resp.Warnings})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	for _, pop := range []string{"pop", "tiny"} {
		for _, viaVector := range []bool{false, true} {
			serial := New(cfg)
			serial.SetDecodeWorkers(1)
			want, err := serial.Process(withModelsBound(context.Background(), set), mkReq(pop, viaVector))
			if err != nil {
				t.Fatal(err)
			}
			if pop == "tiny" && (len(want.Warnings) != 1 || want.Warnings[0].Code != string(errors.PULSE_MATRIX_INSUFFICIENT_N)) {
				t.Fatalf("serial tiny fit: warnings %+v, want one PULSE_MATRIX_INSUFFICIENT_N", want.Warnings)
			}
			for _, w := range []int{1, 2, 4} {
				svc := New(cfg)
				svc.SetDecodeWorkers(w)
				cohort, err := svc.Open(context.Background(), path)
				if err != nil {
					t.Fatal(err)
				}
				pctx, cleanup, available, err := buildParallelDecodeContext(svc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
				if err != nil || !available {
					t.Fatalf("buildParallelDecodeContext: %v (available %v)", err, available)
				}
				capture := &model.VectorCapture{}
				ctx := withModelsBound(context.Background(), set.WithCapture(capture))
				got, err := svc.reduceParallelBuffered(ctx, mkReq(pop, viaVector), cohort.Schema(), pctx, w)
				if cleanup != nil {
					_ = cleanup()
				}
				if err != nil {
					t.Fatalf("%s vector=%v workers=%d: %v", pop, viaVector, w, err)
				}
				if g, wt := wire(got), wire(want); g != wt {
					t.Fatalf("%s vector=%v workers=%d: parallel differs from serial\n got %s\nwant %s", pop, viaVector, w, g, wt)
				}
				if n := len(capture.Fits()); n != 1 {
					t.Fatalf("%s vector=%v workers=%d: the battery offered %d states, want 1", pop, viaVector, w, n)
				}
			}
		}
	}
}

// writeParallelXYCohort writes a 512-row f64 (x, y) cohort to an OS temp
// dir (the parallel decoder mmaps a real file) and returns its path and
// a config rooted there.
func writeParallelXYCohort(t *testing.T, name string) (string, *fs.Config) {
	t.Helper()
	const rows = 512
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/" + name
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "y", Type: encoding.FieldTypeF64, ByteOffset: 8, CsvColumnIdx: 1},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < rows; r++ {
		x := float64((r*37)%101) + 0.25
		y := 0.5*x + float64((r*11)%13)
		for _, v := range []float64{x, y} {
			if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, float64ToBits(v)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	return path, cfg
}

// TestCoefFilter_ParallelDecodeMatchesSerial (#394): a FILTER_EXPRESSION
// reading a bound regression through coef() compiles against the
// request's bound-model set on every parallel-decode worker, so each
// worker count keeps exactly the rows the serial run keeps.
func TestCoefFilter_ParallelDecodeMatchesSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel reduce equivalence in -short mode")
	}
	path, cfg := writeParallelXYCohort(t, "coef_filter_parallel.pulse")
	b, err := model.Bind("m", &types.RegressionSpec{Type: types.REG_OLS, Target: "y", Predictors: []string{"x"}},
		&types.RegressionResult{Type: types.REG_OLS, Coefficients: map[string]float64{model.InterceptKey: 0, "x": 25}})
	if err != nil {
		t.Fatal(err)
	}
	set := &model.Set{}
	if err := set.Add(b); err != nil {
		t.Fatal(err)
	}
	mkReq := func() *types.Request {
		return &types.Request{
			Cohort:    &types.Cohort{Filename: path},
			Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `x > coef("m", "x") * 2`}},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_SUM, Field: "y", Label: "sum_y"},
			},
		}
	}
	wire := func(resp *types.Response) string {
		b, err := types.MarshalFinite(map[string]any{"data": resp.Data, "warnings": resp.Warnings})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	serial := New(cfg)
	serial.SetDecodeWorkers(1)
	want, err := serial.Process(withModelsBound(context.Background(), set), mkReq())
	if err != nil {
		t.Fatal(err)
	}
	if n := mustJSON(t, want.Data[0]["n"]); n == "0" || n == "512" {
		t.Fatalf("vacuous fixture: the coef() filter keeps %v of 512 rows", want.Data[0]["n"])
	}
	for _, w := range []int{1, 2, 4} {
		svc := New(cfg)
		svc.SetDecodeWorkers(w)
		cohort, err := svc.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		pctx, cleanup, available, err := buildParallelDecodeContext(svc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
		if err != nil || !available {
			t.Fatalf("buildParallelDecodeContext: %v (available %v)", err, available)
		}
		got, err := svc.reduceParallelBuffered(withModelsBound(context.Background(), set), mkReq(), cohort.Schema(), pctx, w)
		if cleanup != nil {
			_ = cleanup()
		}
		if err != nil {
			t.Fatalf("workers=%d: %v", w, err)
		}
		if g, wt := wire(got), wire(want); g != wt {
			t.Fatalf("workers=%d: parallel differs from serial\n got %s\nwant %s", w, g, wt)
		}
	}
}
