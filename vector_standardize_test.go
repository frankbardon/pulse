package pulse

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestProcess_StandardizeRefusesLikePredict: through the facade, a
// standardized vector read by a consumer that cannot standardize it is
// refused by Process and Predict alike (PULSE_VECTOR_INVALID
// standardize_unsupported, the same slot and message); a listwise
// co-moment matrix over it runs, and predict reports the option with
// the unstandardized twin's streamability and mergeability.
func TestProcess_StandardizeRefusesLikePredict(t *testing.T) {
	fsys := afero.NewMemMapFs()
	var rows [][]string
	for i := 0; i < 30; i++ {
		rows = append(rows, []string{
			jsonNumber(1.5 + float64((i*7)%11)),
			jsonNumber(2.25*float64(i%5) + 0.5),
			jsonNumber(float64((i * 13) % 17)),
			[]string{"north", "south"}[i%2],
		})
	}
	createTestPulseFile(t, fsys, "s.pulse", []string{"a", "b", "c", "region"}, rows)
	data, err := afero.ReadFile(fsys, "s.pulse")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := New(Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	build := func(std types.VectorStandardize) *types.Request {
		return &types.Request{
			Cohort:  &types.Cohort{Filename: "s.pulse"},
			Vectors: []types.VectorSpec{{Name: "z", Fields: []string{"a", "b", "c"}, Standardize: std}},
		}
	}

	good := build(types.VectorStandardizeZScore)
	good.Matrices = []types.MatrixSpec{{Name: "cov", Type: types.MAT_COVARIANCE, Vector: "z", Params: json.RawMessage(`{"ddof": 0}`)}}
	resp, err := p.Process(ctx, good)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if v := resp.Matrices[0].Primary.Values[1][1]; v < 1-1e-12 || v > 1+1e-12 {
		t.Errorf("standardized variance = %v, want 1", v)
	}
	pred := func(req *types.Request) *descriptor.MatrixPredict {
		t.Helper()
		env, _ := p.PredictBytes(ctx, data, req)
		if env == nil || len(env.Errors) != 0 {
			t.Fatalf("predict errors: %v", env)
		}
		res := env.Data.(*descriptor.PredictResult)
		return &res.Matrices[0]
	}
	twin := build("")
	twin.Matrices = good.Matrices
	ps, pt := pred(good), pred(twin)
	if ps.Standardize != types.VectorStandardizeZScore || pt.Standardize != "" {
		t.Errorf("predict standardize = %q / twin %q", ps.Standardize, pt.Standardize)
	}
	if ps.Streamable != pt.Streamable || ps.Mergeable != pt.Mergeable || !ps.Streamable || !ps.Mergeable {
		t.Errorf("tier standardized %v/%v, twin %v/%v", ps.Streamable, ps.Mergeable, pt.Streamable, pt.Mergeable)
	}

	for _, c := range []struct {
		name string
		edit func(*types.Request)
		slot string
	}{
		{"pairwise", func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "z", Params: json.RawMessage(`{"missing": "pairwise"}`)}}
		}, "matrices[0].vector"},
		{"rank method", func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "z", Params: json.RawMessage(`{"method": "kendall"}`)}}
		}, "matrices[0].vector"},
		{"distance", func(r *types.Request) {
			r.Matrices = []types.MatrixSpec{{Type: types.MAT_DISTANCE, Vector: "z", Params: json.RawMessage(`{"between": {"type": "GROUP_CATEGORY", "field": "region"}}`)}}
		}, "matrices[0].vector"},
		{"test", func(r *types.Request) {
			r.Tests = []*types.Test{{Type: types.TEST_HOTELLING_T2, Vector: "z"}}
		}, "tests[0].vector"},
		{"attribute", func(r *types.Request) {
			r.Attributes = []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "z", Label: "s"}}
		}, "attributes[0].vector"},
		{"aggregation", func(r *types.Request) {
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Vector: "z", Label: "m"}}
		}, "aggregations[0].vector"},
		{"grouper", func(r *types.Request) {
			r.Groups = []*types.Group{{Type: types.GROUP_KMEANS, Vector: "z", Params: json.RawMessage(`{"k": 2}`)}}
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}
		}, "groups[0].vector"},
		{"expression", func(r *types.Request) {
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "len(z) > 0"}}
			r.Aggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "region", Label: "n"}}
		}, "filterers[0].expression"},
	} {
		t.Run(c.name, func(t *testing.T) {
			req := build(types.VectorStandardizeZScore)
			c.edit(req)
			_, err := p.Process(ctx, req)
			var ce *perr.CodedError
			if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_VECTOR_INVALID || ce.Details["reason"] != "standardize_unsupported" || ce.Details["slot"] != c.slot {
				t.Fatalf("Process err = %v (%v), want PULSE_VECTOR_INVALID standardize_unsupported at %s", err, ce, c.slot)
			}
			env, _ := p.PredictBytes(ctx, data, req)
			if env == nil || len(env.Errors) == 0 || env.Errors[0].Code != string(perr.PULSE_VECTOR_INVALID) || env.Errors[0].Message != ce.Message {
				t.Errorf("predict errors = %v, want the runtime's %s %q", env.Errors, ce.Code, ce.Message)
			}
			// The unstandardized twin is admitted by the same slot.
			twin := build("")
			c.edit(twin)
			if env, _ := p.PredictBytes(ctx, data, twin); env == nil || len(env.Errors) != 0 {
				t.Errorf("twin refused: %v", env.Errors)
			}
		})
	}
}
