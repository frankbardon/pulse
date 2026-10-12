package service

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// vector_kind_test.go: a vector's `kind` against the metric MAT_DISTANCE
// runs (U27 E1-S3) — predict's DefaultsApplied is the metric the run
// executes, and the PULSE_VECTOR_METRIC_UNSUITED issue rides the same
// channel predict reports it on, with and without strict.

func kindDistanceReq(kind types.VectorKind, metric string) *types.Request {
	params := `{"between": {"type": "GROUP_CATEGORY", "field": "region"}`
	if metric != "" {
		params += `, "metric": "` + metric + `"`
	}
	params += `}`
	return &types.Request{
		Cohort:   &types.Cohort{Filename: "g.pulse"},
		Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"x1", "x3"}, Kind: kind}},
		Matrices: []types.MatrixSpec{{Name: "d", Type: types.MAT_DISTANCE, Vector: "v", Params: json.RawMessage(params)}},
	}
}

func unsuitedWarnings(ws []*types.ResponseWarning) int {
	n := 0
	for _, w := range ws {
		if w.Code == string(errors.PULSE_VECTOR_METRIC_UNSUITED) {
			n++
		}
	}
	return n
}

// TestVectorKind_MetricDefaultMatchesRuntime: for every kind (and none,
// and an explicit metric) the metric predict reports filling — or the
// implicit euclidean when it fills none — is the metric the run's
// components carry; under DisableDefaults neither fills. The run's
// normalized request carries the fill; the caller's matrices backing
// array is never written.
func TestVectorKind_MetricDefaultMatchesRuntime(t *testing.T) {
	cfg := groupedMatrixCohort(t, 120)
	data, err := afero.ReadFile(cfg.Fs(), "g.pulse")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind    types.VectorKind
		metric  string
		disable bool
		want    string
	}{
		{types.VectorKindMeasure, "", false, "euclidean"},
		{types.VectorKindScale, "", false, "euclidean"},
		{types.VectorKindComposition, "", false, "aitchison"},
		{types.VectorKindBinary, "", false, "cosine"},
		{"", "", false, "euclidean"},
		{types.VectorKindComposition, "manhattan", false, "manhattan"},
		{types.VectorKindComposition, "", true, "euclidean"},
	}
	for _, c := range cases {
		name := string(c.kind) + "/" + c.metric
		if c.disable {
			name += "/no-defaults"
		}
		t.Run(name, func(t *testing.T) {
			req := kindDistanceReq(c.kind, c.metric)
			orig := req.Matrices
			before := string(orig[0].Params)
			env := descx.Predict(bytes.NewReader(data), req, &descx.PredictOptions{DisableDefaults: c.disable})
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %v", env.Errors)
			}
			predicted := "euclidean"
			if c.metric != "" {
				predicted = c.metric
			}
			for _, d := range env.Data.(*descriptor.PredictResult).DefaultsApplied {
				if d.Category == "metric" && !c.disable {
					predicted = d.Type
				}
			}
			if predicted != c.want {
				t.Errorf("predict metric %s, want %s", predicted, c.want)
			}
			svc := New(cfg)
			svc.SetDisableDefaults(c.disable)
			resp, err := svc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if got := resp.Components.Matrices[0].Operator["metric"]; got != predicted {
				t.Errorf("runtime metric %v, predict %s", got, predicted)
			}
			// The run executes (and echoes) the filled request, written on
			// a copy of the Matrices slice: the caller's backing array
			// keeps its params.
			if string(orig[0].Params) != before {
				t.Errorf("the run wrote into the caller's matrices: %s", orig[0].Params)
			}
			var p map[string]any
			_ = json.Unmarshal(req.Matrices[0].Params, &p)
			if filled, _ := p["metric"].(string); (c.kind != "" && c.metric == "" && !c.disable) != (filled == c.want && c.metric == "") {
				t.Errorf("normalized request params %s", req.Matrices[0].Params)
			}
		})
	}
}

// TestVectorKind_UnsuitedPredictRuntimeParity: an explicit poor fit is a
// predict warning and a Response warning; under strict a predict error
// and a refused run carrying the code; a fitting metric is neither.
func TestVectorKind_UnsuitedPredictRuntimeParity(t *testing.T) {
	cfg := groupedMatrixCohort(t, 120)
	data, err := afero.ReadFile(cfg.Fs(), "g.pulse")
	if err != nil {
		t.Fatal(err)
	}
	formula := &types.Request{
		Cohort:       &types.Cohort{Filename: "g.pulse"},
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"x1", "x3"}, Kind: types.VectorKindScale}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "sim", Label: "m"}},
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "sim", Expression: "cosine(v, v)"}},
	}
	cases := []struct {
		name string
		req  *types.Request
		hits int
	}{
		{"matrix cosine on measure", kindDistanceReq(types.VectorKindMeasure, "cosine"), 1},
		{"matrix euclidean on composition", kindDistanceReq(types.VectorKindComposition, "euclidean"), 1},
		{"formula cosine on scale", formula, 1},
		{"fitting metric", kindDistanceReq(types.VectorKindComposition, "manhattan"), 0},
	}
	for _, c := range cases {
		for _, strict := range []bool{false, true} {
			name := c.name
			if strict {
				name += "/strict"
			}
			t.Run(name, func(t *testing.T) {
				env := descx.Predict(bytes.NewReader(data), c.req, &descx.PredictOptions{Strict: strict})
				var pw, pe int
				for _, w := range env.Warnings {
					if w.Code == string(errors.PULSE_VECTOR_METRIC_UNSUITED) {
						pw++
					}
				}
				for _, e := range env.Errors {
					if e.Code == string(errors.PULSE_VECTOR_METRIC_UNSUITED) {
						pe++
					}
				}
				svc := New(cfg)
				svc.SetStrict(strict)
				resp, err := svc.Process(context.Background(), c.req)
				if strict {
					if pe != c.hits || pw != 0 {
						t.Fatalf("predict strict: %d errors %d warnings, want %d", pe, pw, c.hits)
					}
					var ce *errors.CodedError
					if c.hits > 0 {
						if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_VECTOR_METRIC_UNSUITED {
							t.Fatalf("strict run err = %v, want PULSE_VECTOR_METRIC_UNSUITED", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("strict run of a fitting metric: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("Process: %v", err)
				}
				if pw != c.hits || pe != 0 {
					t.Fatalf("predict: %d warnings %d errors, want %d", pw, pe, c.hits)
				}
				if got := unsuitedWarnings(resp.Warnings); got != pw {
					t.Errorf("runtime warnings %d, predict %d", got, pw)
				}
			})
		}
	}
}

// TestVectorKind_ReliabilityTakesVectorBounds: MAT_RELIABILITY over a
// kind "scale" vector range-checks every member against the vector's
// declared bounds when it sets none of its own (a value outside is
// PROCESSING_CONFIG, never clamped); explicit params win.
func TestVectorKind_ReliabilityTakesVectorBounds(t *testing.T) {
	cfg := groupedMatrixCohort(t, 120)
	lo, hi := 0.0, 1e-9
	run := func(params string) error {
		req := &types.Request{
			Cohort:   &types.Cohort{Filename: "g.pulse"},
			Vectors:  []types.VectorSpec{{Name: "v", Fields: []string{"x1", "x3"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &hi}},
			Matrices: []types.MatrixSpec{{Name: "a", Type: types.MAT_RELIABILITY, Vector: "v", Params: json.RawMessage(params)}},
		}
		_, err := New(cfg).Process(context.Background(), req)
		return err
	}
	var ce *errors.CodedError
	if err := run(`{}`); !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
		t.Errorf("vector bounds not applied: err %v", err)
	}
	if err := run(`{"scale_min": -1e12, "scale_max": 1e12}`); err != nil {
		t.Errorf("explicit params did not win: %v", err)
	}
}
