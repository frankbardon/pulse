package descriptor

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// wideSetPredictSchema carries one set_u256 column with a 206-member
// dictionary — past every narrow rung, so nothing here can be answered
// by a uint64 fast path.
func wideSetPredictSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU256, Nullable: true,
			Dictionary: wideSetDictionary(t, 206), Description: "Multi-select survey response tags"},
	}}
}

// TestPredict_Streamable_WideSetsMatchNarrow pins FR-25: predict's
// Streamable verdict for a set request does not depend on the rung.
// Streamability is an operator property (types/streamability.go), so a
// rung-sensitive answer would mean predict had grown field-type
// knowledge it has no business having.
func TestPredict_Streamable_WideSetsMatchNarrow(t *testing.T) {
	narrowSchema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU8, Nullable: true,
			Dictionary: wideSetDictionary(t, 6), Description: "Multi-select survey response tags"},
	}}
	wideSchema := wideSetPredictSchema(t)

	reqs := []struct {
		name string
		make func() *types.Request
	}{
		{"AGG_SET_FREQUENCY", func() *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SET_FREQUENCY, Field: "tags"}}}
		}},
		{"AGG_SET_UNION", func() *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SET_UNION, Field: "tags"}}}
		}},
		{"AGG_SET_DISTINCT_VALUES", func() *types.Request {
			return &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SET_DISTINCT_VALUES, Field: "tags"}}}
		}},
		{"GROUP_SET_VALUE + AGG_COUNT", func() *types.Request {
			return &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "tags"}},
				Groups:       []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags"}},
			}
		}},
		{"FILTER_SET_CONTAINS_ANY", func() *types.Request {
			return &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "tags"}},
				Filterers:    []*types.Filterer{{Type: types.FILTER_SET_CONTAINS_ANY, Field: "tags", Values: []string{"m3"}}},
			}
		}},
		{"ATTR_SET_POPCOUNT", func() *types.Request {
			return &types.Request{
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "tags"}},
				Attributes:   []*types.Attribute{{Type: types.ATTR_SET_POPCOUNT, Field: "tags", Label: "n_tags"}},
			}
		}},
	}

	for _, rc := range reqs {
		t.Run(rc.name, func(t *testing.T) {
			narrowEnv := PredictFromBytes(buildTestPulseFile(t, narrowSchema), rc.make(), nil)
			wideEnv := PredictFromBytes(buildTestPulseFile(t, wideSchema), rc.make(), nil)
			narrowRes := narrowEnv.Data.(*PredictResult)
			wideRes := wideEnv.Data.(*PredictResult)

			if narrowRes.Streamable != wideRes.Streamable {
				t.Errorf("Streamable: set_u8 = %v, set_u256 = %v — streamability is an operator property, not a width one\nnarrow reasons: %v\nwide reasons:   %v",
					narrowRes.Streamable, wideRes.Streamable, narrowRes.StreamableReasons, wideRes.StreamableReasons)
			}

			// Absolute verdict, not just parity: every set operator in
			// this matrix is declared streamable in
			// types/streamability.go. Asserting only narrow == wide
			// would stay green if both regressed to buffered together.
			if !wideRes.Streamable {
				t.Errorf("set_u256 Streamable = false, want true; reasons: %v", wideRes.StreamableReasons)
			}

			// Runtime parity: the same cross-package gate
			// TestPredict_Streamable_MatchesRuntime applies to the
			// numeric matrix, asserted here over the wide rung.
			runtimeWide := processing.CanStreamRequest(rc.make(), wideSchema)
			if runtimeWide != wideRes.Streamable {
				t.Errorf("predict Streamable = %v but processing.CanStreamRequest = %v for set_u256",
					wideRes.Streamable, runtimeWide)
			}
		})
	}
}

// TestPredict_WideSetRequestValidates confirms predict accepts a
// wide-set request without errors — the capability tables it reads are
// the ones this story widened, so a stale AcceptsTypes would surface
// here as a validation error against a request the runtime executes.
func TestPredict_WideSetRequestValidates(t *testing.T) {
	data := buildTestPulseFile(t, wideSetPredictSchema(t))
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "tag_freq"}},
		Groups:       []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
	}
	env := PredictFromBytes(data, req, nil)
	if len(env.Errors) != 0 {
		t.Errorf("predict errors on a wide-set request: %v", env.Errors)
	}
}
