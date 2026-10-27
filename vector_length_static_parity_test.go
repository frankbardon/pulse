package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestVectorPair_StaticLengthMatchesRuntime (#419): dot / dist / cosine
// over two arguments whose lengths the request fixes — bare named
// vectors (a Request.Vectors member count, a native vec_* dim) or
// literal arrays — that differ is PROCESSING_VECTOR_LENGTH_MISMATCH
// {function, left, right} from predict, with the runtime's message and
// details, in an ATTR_FORMULA and a FILTER_EXPRESSION; the runtime
// refuses it at build. Predict used to accept it. Matching lengths,
// non-vector identifiers, nested calls, slices and let bindings are
// unknown or equal and never refused by predict.
func TestVectorPair_StaticLengthMatchesRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	vecs := []types.VectorSpec{
		{Name: "xy", Fields: []string{"x", "y"}},
		{Name: "three", Fields: []string{"x", "y", "t_f64"}},
	}
	attrReq := func(expr string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Vectors:      vecs,
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "d", Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "d", Label: "s"}},
		}
	}
	filterReq := func(expr string) *types.Request {
		return &types.Request{
			Cohort:       &types.Cohort{Filename: cohort},
			Vectors:      vecs,
			Filterers:    []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: expr}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Label: "n"}},
		}
	}
	for _, c := range []struct {
		name        string
		req         *types.Request
		fn          string
		left, right int
	}{
		{"named vs named", attrReq("dot(xy, three)"), "dot", 2, 3},
		{"named vs native", attrReq("cosine(three, t_vec_f64)"), "cosine", 3, 2},
		{"named vs literal", attrReq("dist(xy, [1, 2, 3])"), "dist", 2, 3},
		{"literal vs native", attrReq("dot([1, 2, 3], t_vec_f32)"), "dot", 3, 2},
		{"filter", filterReq("dot(three, xy) > 0"), "dot", 3, 2},
		// No row reaches the formula: only the build's static refusal
		// can agree with predict.
		{"no row evaluated", func() *types.Request {
			r := attrReq("dot(xy, three)")
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x < -1000000"}}
			return r
		}(), "dot", 2, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := predictEnvelope(t, p, fs, cohort, c.req)
			if len(env.Errors) != 1 {
				t.Fatalf("predict %v, want one refusal", envCodes(env))
			}
			pe := env.Errors[0]
			want := map[string]any{"function": c.fn, "left": c.left, "right": c.right}
			wb, _ := json.Marshal(want)
			pb, _ := json.Marshal(pe.Details)
			if pe.Code != string(errors.PROCESSING_VECTOR_LENGTH_MISMATCH) || string(pb) != string(wb) {
				t.Fatalf("predict %s %s, want PROCESSING_VECTOR_LENGTH_MISMATCH %s", pe.Code, pb, wb)
			}
			_, err := p.Process(ctx, c.req)
			sameRefusal(t, "process", pe, err, errors.PROCESSING_VECTOR_LENGTH_MISMATCH)
		})
	}

	// No false positive: equal known lengths run; an identifier that is
	// no vector, a nested call, a slice and a let binding are unknown, so
	// predict never refuses them (whatever the row-time verdict).
	for _, expr := range []string{
		"dot(xy, t_vec_f64)",
		"cosine(t_vec_f32, t_vec_f64)",
		"dist(xy, [1, 2])",
		"dot(three[0:2], xy)",
		"let xy = [1, 2, 3]; dot(xy, three)",
	} {
		req := attrReq(expr)
		if env := predictEnvelope(t, p, fs, cohort, req); len(env.Errors) != 0 {
			t.Errorf("%s: predict %v", expr, envCodes(env))
		}
		if _, err := p.Process(ctx, attrReq(expr)); err != nil {
			t.Errorf("%s: process %v", expr, err)
		}
	}
	for _, expr := range []string{"dot(vcenter(xy), three)", "dot(x, three)", "dot(vnormalize(three, \"l2\"), xy)"} {
		env := predictEnvelope(t, p, fs, cohort, attrReq(expr))
		for _, e := range env.Errors {
			if e.Code == string(errors.PROCESSING_VECTOR_LENGTH_MISMATCH) {
				t.Errorf("%s: predict false positive %s %q", expr, e.Code, e.Message)
			}
		}
	}
}
