package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestPredict_GroupFitParams: predict refuses an unknown GROUP_QUANTILE
// params.fit through internal/groupfit (the reading the factory calls)
// on Request.Groups and both crosstab axes, accepts global / within
// (within on the outermost position too: a no-op), and leaves a
// GROUP_QUANTILE the instance hides to the unknown-type rule.
func TestPredict_GroupFitParams(t *testing.T) {
	t.Parallel()

	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	q := func(fit string) *types.Group {
		return &types.Group{Type: types.GROUP_QUANTILE, Field: "score", Params: json.RawMessage(`{"fit":"` + fit + `"}`)}
	}
	grade := func() *types.Group { return &types.Group{Type: types.GROUP_CATEGORY, Field: "grade"} }
	agg := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
	const bad = `GROUP_QUANTILE params.fit must be "global" (fit once over the whole filtered set, the default) or "within" (refit inside each bucket of the groupers before it)`

	if env := predictFromBytes(data, &types.Request{Aggregations: agg, Groups: []*types.Group{grade(), q("nope")}}, nil); !hasConfigError(env, bad) {
		t.Fatalf("groups: no fit refusal in %+v", env.Errors)
	}
	for _, axis := range []string{"rows", "columns"} {
		ct := &types.CrosstabSpec{Rows: []*types.Group{grade()}, Columns: []*types.Group{grade()}, Cell: &types.Aggregation{Type: types.AGG_COUNT, Field: "score"}}
		if axis == "rows" {
			ct.Rows = append(ct.Rows, q("nope"))
		} else {
			ct.Columns[0] = q("nope")
		}
		if env := predictFromBytes(data, &types.Request{Crosstab: ct}, nil); !hasConfigError(env, bad) {
			t.Fatalf("crosstab %s: no fit refusal in %+v", axis, env.Errors)
		}
	}
	for _, groups := range [][]*types.Group{{grade(), q("global")}, {grade(), q("within")}, {q("within"), grade()}} {
		if env := predictFromBytes(data, &types.Request{Aggregations: agg, Groups: groups}, nil); len(env.Errors) != 0 {
			t.Fatalf("%s: refused %+v", groups[1].Params, env.Errors)
		}
	}
	if env := predictFromBytes(data, &types.Request{Aggregations: agg, Groups: []*types.Group{q("nope")}}, hiddenPredictOpts("GROUP_QUANTILE")); hasConfigError(env, bad) {
		t.Fatalf("a hidden GROUP_QUANTILE was judged on its params: %+v", env.Errors)
	}
}
