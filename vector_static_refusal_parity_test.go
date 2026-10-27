package pulse_test

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestVectorBuiltin_StaticRefusalsMatchRuntime (E4-S3): the reserved
// expression built-ins' faults a parse can judge without a row — a
// wrong argument count, a literal vnormalize mode other than "l1" /
// "l2", a vector-returning call as the whole expression — are refused
// by the runtime build (PROCESSING_CONFIG, before any row) and now by
// predict with the same code, message and details, as the only error,
// in an ATTR_FORMULA, a FILTER_EXPRESSION and a total() argument, on
// Process and in a Compose slot. Predict used to accept them. A
// non-literal mode and a vector built-in nested inside a reduction are
// unknown or legal and never refused.
func TestVectorBuiltin_StaticRefusalsMatchRuntime(t *testing.T) {
	t.Parallel()

	p, fs, cohort := acceptanceCohort(t)
	ctx := context.Background()
	vecs := []types.VectorSpec{{Name: "xy", Fields: []string{"x", "y"}}}
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
		name string
		req  *types.Request
	}{
		{"arity too few", attrReq("dot(xy)")},
		{"arity too many", attrReq("vsum(xy, xy)")},
		{"arity nested", attrReq("x + vmean(xy, 2, 3)")},
		{"literal mode", attrReq(`vsum(vnormalize(xy, "l3"))`)},
		{"literal mode case", filterReq(`vsum(vnormalize(xy, "L2")) > 0`)},
		{"filter arity", filterReq("cosine(xy) > 0")},
		{"vector result formula", attrReq("vcenter(xy)")},
		{"vector result filter", filterReq(`vnormalize(xy, "l2")`)},
		{"total() argument", attrReq("x / total(vsum(xy, 1))")},
		{"total() argument vector result", attrReq("x / total(vzscore(xy))")},
		// U58: the trig set and the zero-argument constants share the
		// same static arity rule.
		{"sin arity", attrReq("sin(x, y)")},
		{"atan2 arity", attrReq("atan2(x)")},
		{"pi arity", attrReq("x * pi(2)")},
		{"e arity in a filter", filterReq("e(x) > 1")},
		{"cos arity in a total() argument", attrReq("x / total(cos())")},
		// No row reaches the formula: only the build's static refusal
		// can agree with predict.
		{"no row evaluated", func() *types.Request {
			r := attrReq("dot(xy)")
			r.Filterers = []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "x < -1000000"}}
			return r
		}()},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := predictEnvelope(t, p, fs, cohort, c.req)
			if len(env.Errors) != 1 {
				t.Fatalf("predict %v, want one refusal", envCodes(env))
			}
			pe := env.Errors[0]
			_, err := p.Process(ctx, c.req)
			sameRefusal(t, "process", pe, err, errors.PROCESSING_CONFIG)
			_, err = p.Compose(ctx, &types.ComposedRequest{Requests: []*types.Request{c.req}})
			sameRefusal(t, "compose slot", pe, unlocated(t, err), errors.PROCESSING_CONFIG)
		})
	}

	// No false positive: legal arities, valid literal modes, a
	// non-literal mode and a vector built-in reduced to a scalar.
	for _, expr := range []string{
		"dot(xy, xy)",
		`vsum(vnormalize(xy, "l1"))`,
		`vsum(vnormalize(xy, x > 0 ? "l1" : "l2"))`,
		"vmean(vcenter(xy))",
		"x / total(vsum(xy))",
		"sin(x) + cos(y) + tan(x) + atan2(y, x) + pi() + e()",
	} {
		if env := predictEnvelope(t, p, fs, cohort, attrReq(expr)); len(env.Errors) != 0 {
			t.Errorf("%s: predict %v", expr, envCodes(env))
		}
		if _, err := p.Process(ctx, attrReq(expr)); err != nil {
			t.Errorf("%s: process %v", expr, err)
		}
	}
}
