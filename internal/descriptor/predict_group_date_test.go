package descriptor

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func hasConfigError(env *descriptor.Envelope, msg string) bool {
	for _, e := range env.Errors {
		if e.Code == string(errors.PROCESSING_CONFIG) && e.Message == msg {
			return true
		}
	}
	return false
}

// TestPredict_GroupDateParams: predict refuses GROUP_DATE params and
// fields through dategroup (the reading the factory calls) on
// Request.Groups and on both crosstab axes, accepts the valid
// spellings, and leaves a GROUP_DATE the instance hides to the
// unknown-type rule.
func TestPredict_GroupDateParams(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	g := func(field, params string) *types.Group {
		return &types.Group{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(params)}
	}
	agg := []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score", Label: "n"}}
	const hourOnDate = `GROUP_DATE component=hour requires a datetime field; field "day" is of type date`
	const noWeek = `GROUP_DATE week_start only applies to component=week, got "day"`

	env := predictFromBytes(data, &types.Request{Aggregations: agg, Groups: []*types.Group{g("day", `{"component":"hour"}`)}}, nil)
	if !hasConfigError(env, hourOnDate) {
		t.Fatalf("groups: no hour-on-date refusal in %+v", env.Errors)
	}
	for _, axis := range []string{"rows", "columns"} {
		ct := &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "score"},
		}
		if axis == "rows" {
			ct.Rows[0] = g("ts", `{"component":"day","week_start":"sunday"}`)
		} else {
			ct.Columns[0] = g("ts", `{"component":"day","week_start":"sunday"}`)
		}
		if env := predictFromBytes(data, &types.Request{Crosstab: ct}, nil); !hasConfigError(env, noWeek) {
			t.Fatalf("crosstab %s: no week_start refusal in %+v", axis, env.Errors)
		}
	}
	for _, ok := range []*types.Group{g("ts", `{"component":"hour"}`), g("day", `{"component":"week","week_start":"sunday"}`)} {
		env := predictFromBytes(data, &types.Request{Aggregations: agg, Groups: []*types.Group{ok}}, nil)
		if len(env.Errors) != 0 {
			t.Fatalf("%s: refused %+v", ok.Params, env.Errors)
		}
	}
	env = predictFromBytes(data, &types.Request{Aggregations: agg, Groups: []*types.Group{g("day", `{"component":"hour"}`)}}, hiddenPredictOpts("GROUP_DATE"))
	if hasConfigError(env, hourOnDate) {
		t.Fatalf("a hidden GROUP_DATE was judged on its params: %+v", env.Errors)
	}
}
