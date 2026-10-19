package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// prevStatePredictSchema: a long panel — subject id, date order, a
// categorical state.
func prevStatePredictSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	_, _ = dict.Add("free")
	_, _ = dict.Add("paid")
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, Description: "Customer identifier of the panel row"},
		{Name: "seen", Type: encoding.FieldTypeDate, Description: "Calendar day the plan was observed"},
		{Name: "plan", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict, Description: "Subscription plan observed that day"},
	}}
}

// TestPredict_PreviousState_StreamableMatchesRuntime: ATTR_PREVIOUS_STATE
// is buffered only on both sides — predict reports Streamable false
// with its own reason, processing.CanStreamRequest agrees — alone,
// beside a grouper and beside a streamable attribute.
func TestPredict_PreviousState_StreamableMatchesRuntime(t *testing.T) {
	schema := prevStatePredictSchema()
	prev := &types.Attribute{Type: types.ATTR_PREVIOUS_STATE, Field: "plan", SubjectField: "id", OrderField: "seen", Label: "prev"}
	for _, c := range []struct {
		name string
		req  *types.Request
	}{
		{"alone", &types.Request{
			Attributes:   []*types.Attribute{prev},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "prev"}},
		}},
		{"grouped", &types.Request{
			Attributes:   []*types.Attribute{prev},
			Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "plan"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "prev"}},
		}},
		{"beside_a_row_local_attribute", &types.Request{
			Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Expression: "id * 2", Label: "twice"}, prev},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "twice"}},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := predictFromBytes(buildTestPulseFile(t, schema), c.req, nil)
			if len(env.Errors) != 0 {
				t.Fatalf("predict errors %+v", env.Errors)
			}
			result := env.Data.(*descriptor.PredictResult)
			if runtime := processing.CanStreamRequest(c.req, schema); result.Streamable || runtime {
				t.Fatalf("predict Streamable=%v runtime=%v, want both false", result.Streamable, runtime)
			}
			found := false
			for _, r := range result.StreamableReasons {
				if strings.Contains(r, "ATTR_PREVIOUS_STATE") && strings.Contains(r, "buffered") {
					found = true
				}
			}
			if !found {
				t.Errorf("no ATTR_PREVIOUS_STATE buffered reason in %q", result.StreamableReasons)
			}
		})
	}
}
