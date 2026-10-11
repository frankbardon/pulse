package pulse_test

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"sync/atomic"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/extend"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TODO #411's extension half: a registered extension grouper is a
// MAT_DISTANCE params.between like a built-in. It buckets through its
// own extend.Grouper.Group — the whole-set pass the engine already runs
// for a buffered between (GROUP_KMEANS, GROUP_QUANTILE) — so the spec is
// buffered and serial, its key order alphabetical; the matrix's weight
// applies to the centroids. Predict accepts and refuses what the run
// does. The oracle is the same MAT_DISTANCE with a GROUP_CATEGORY
// between over the column the extension reads.

// labelGrouper buckets each row by its categorical label.
type labelGrouper struct{ calls *atomic.Int32 }

func (g labelGrouper) Group(rows extend.Rows, field string) (map[string][]int, error) {
	g.calls.Add(1)
	out := map[string][]int{}
	for i := range rows.Len() {
		if l, ok := rows.At(i).StringValue(field); ok {
			out[l] = append(out[l], i)
		}
	}
	return out, nil
}

func openDistanceExtension(t *testing.T) (*pulse.Pulse, []byte, *atomic.Int32) {
	t.Helper()
	fsys := afero.NewMemMapFs()
	writeParityCohort(t, fsys, "dist.pulse", paritySchema(t), 0, paritySmallRows)
	data, err := afero.ReadFile(fsys, "dist.pulse")
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int32{}
	p, err := pulse.New(pulse.Options{FS: fsys, Extensions: pulse.Extensions{
		Groupers: []pulse.GrouperRegistration{{
			Name: "GROUP_ACME_LABEL",
			Factory: func(*types.Group, *encoding.Schema) (extend.Grouper, error) {
				return labelGrouper{calls: calls}, nil
			},
		}},
	}})
	if err != nil {
		t.Fatalf("pulse.New: %v", err)
	}
	calls.Store(0)
	return p, data, calls
}

func distanceBetweenReq(between string, weight *types.WeightSpec) *types.Request {
	spec := types.MatrixSpec{Name: "d", Type: types.MAT_DISTANCE, Fields: []string{"score", "qty"},
		Params: json.RawMessage(`{"between": ` + between + `, "metric": "mahalanobis", "standardize": true}`)}
	if weight != nil {
		spec.Weight = types.SlotWeightOf(*weight)
	}
	return &types.Request{Cohort: &types.Cohort{Filename: "dist.pulse"}, Matrices: []types.MatrixSpec{spec}}
}

// TestExtensions_DistanceBetweenMatchesCategory: an extension between
// gives the GROUP_CATEGORY answer over the same column, unweighted and
// under a probability weight, and predict reports the spec buffered,
// serial, with the run's shape bound unknown.
func TestExtensions_DistanceBetweenMatchesCategory(t *testing.T) {
	p, data, calls := openDistanceExtension(t)
	ctx := context.Background()
	w := &types.WeightSpec{Field: "qty", Kind: types.WeightKindProbability}
	for _, weight := range []*types.WeightSpec{nil, w} {
		ext := distanceBetweenReq(`{"type": "GROUP_ACME_LABEL", "field": "region"}`, weight)
		env, err := p.PredictBytes(ctx, data, ext)
		if err != nil {
			t.Fatal(err)
		}
		if len(env.Errors) != 0 {
			t.Fatalf("predict refused an extension between: %+v", env.Errors)
		}
		mp := env.Data.(*descriptor.PredictResult).Matrices[0]
		if mp.Streamable || mp.Mergeable || mp.Shape != [2]int{0, 0} || len(mp.AxisKeys) != 0 {
			t.Errorf("predict %+v", mp)
		}
		got, err := p.Process(ctx, ext)
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		if calls.Load() == 0 {
			t.Fatal("the extension grouper never ran")
		}
		want, err := p.Process(ctx, distanceBetweenReq(`{"type": "GROUP_CATEGORY", "field": "region"}`, weight))
		if err != nil {
			t.Fatal(err)
		}
		g, wj := mustMarshal(t, got.Matrices), mustMarshal(t, want.Matrices)
		if g != wj {
			t.Errorf("weight %v: extension between\n got %s\nwant %s", weight, g, wj)
		}
		if gk := got.Matrices[0].Primary.RowKeys; len(gk) < 2 {
			t.Errorf("buckets %v", gk)
		}
	}
}

// TestExtensions_DistanceBetweenRefusedLikePredict: an extension
// between's bad shapes are refused by predict and the run alike, before
// its factory runs — and an unregistered name stays unknown.
func TestExtensions_DistanceBetweenRefusedLikePredict(t *testing.T) {
	for _, c := range []struct {
		name, between string
		code          errors.Code
		reason        string
	}{
		{"no field", `{"type": "GROUP_ACME_LABEL"}`, errors.SERVICE_VALIDATION, "bad_params"},
		{"unknown field", `{"type": "GROUP_ACME_LABEL", "field": "nope"}`, errors.SERVICE_VALIDATION, "unknown_field"},
		{"own weight", `{"type": "GROUP_ACME_LABEL", "field": "region", "weight": null}`, errors.SERVICE_VALIDATION, "bad_params"},
		{"tz", `{"type": "GROUP_ACME_LABEL", "field": "region", "tz": "UTC"}`, errors.PROCESSING_CONFIG, ""},
		{"unknown vector", `{"type": "GROUP_ACME_LABEL", "field": "region", "vector": "nope"}`, errors.PULSE_VECTOR_UNKNOWN, ""},
		{"unregistered", `{"type": "GROUP_ACME_NOPE", "field": "region"}`, errors.SERVICE_VALIDATION, "unknown_between_type"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, data, calls := openDistanceExtension(t)
			req := distanceBetweenReq(c.between, nil)
			env, err := p.PredictBytes(context.Background(), data, req)
			if err != nil {
				t.Fatal(err)
			}
			if !hasEnvelopeError(env.Errors, c.code, c.reason) {
				t.Errorf("predict errors %+v, want %s %s", env.Errors, c.code, c.reason)
			}
			_, perr := p.Process(context.Background(), req)
			var coded *errors.CodedError
			if !stderrors.As(perr, &coded) || coded.Code != c.code || (c.reason != "" && coded.Details["reason"] != c.reason) {
				t.Errorf("Process error %v, want %s %s", perr, c.code, c.reason)
			}
			if calls.Load() != 0 {
				t.Error("the extension grouper ran on a refused request")
			}
		})
	}
}

func mustMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
