package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// findFrequencyRefusal returns the predict error refusing an
// AGG_FREQUENCY slot without params.value, or nil.
func findFrequencyRefusal(env *descriptor.Envelope, slot string) *descriptor.EnvelopeEntry {
	for _, e := range env.Errors {
		if e.Code == string(errors.PROCESSING_CONFIG) && e.Details["slot"] == slot &&
			strings.Contains(e.Message, "AGG_FREQUENCY requires params.value") {
			return e
		}
	}
	return nil
}

// TestPredict_AggFrequencyMissingValue: predict refuses what the
// runtime refuses at construction — an AGG_FREQUENCY slot without
// params.value, top-level or in a crosstab cell / margin — with the
// runtime's code and message (internal/processing/aggregator_frequency.go
// frequencyValueMissingMessage, held equal by
// TestAggFrequency_PredictMessageMatchesRuntime at the root).
func TestPredict_AggFrequencyMissingValue(t *testing.T) {
	data := buildTestPulseFile(t, hiddenPredictSchema(t))
	freq := func(params string) *types.Aggregation {
		a := &types.Aggregation{Type: types.AGG_FREQUENCY, Field: "grade", Label: "hits"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return a
	}
	const want = "AGG_FREQUENCY requires params.value: it returns the count of rows equal to that value. " +
		"The count of the field's most common value is AGG_MODE_COUNT."

	for _, params := range []string{"", `{}`, `{"value":null}`, `{"value":""}`, `{"value":false}`} {
		env := predictFromBytes(data, &types.Request{Aggregations: []*types.Aggregation{freq(params)}}, nil)
		e := findFrequencyRefusal(env, "aggregations[0]")
		if e == nil {
			t.Fatalf("params %q: no refusal in %+v", params, env.Errors)
		}
		if e.Message != want {
			t.Errorf("params %q: message = %q, want %q", params, e.Message, want)
		}
	}
	for _, params := range []string{`{"value":"A"}`, `{"value":3}`, `{"value":"Z"}`} {
		env := predictFromBytes(data, &types.Request{Aggregations: []*types.Aggregation{freq(params)}}, nil)
		if e := findFrequencyRefusal(env, "aggregations[0]"); e != nil {
			t.Errorf("params %q: refused a present value: %+v", params, e)
		}
	}

	ct := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
		Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "grade"}},
		Cell:               freq(""),
		MarginAggregations: []*types.Aggregation{freq(`{}`)},
	}}
	env := predictFromBytes(data, ct, nil)
	for _, slot := range []string{"crosstab.cell", "crosstab.margin_aggregations[0]"} {
		if findFrequencyRefusal(env, slot) == nil {
			t.Errorf("no refusal for %s in %+v", slot, env.Errors)
		}
	}

	// A hidden AGG_MODE_COUNT drops the pointer, as the runtime's
	// ScopeRefusal does.
	env = predictFromBytes(data, &types.Request{Aggregations: []*types.Aggregation{freq("")}}, hiddenPredictOpts("AGG_MODE_COUNT"))
	e := findFrequencyRefusal(env, "aggregations[0]")
	if e == nil || strings.Contains(e.Message, "AGG_MODE_COUNT") {
		t.Errorf("hidden AGG_MODE_COUNT: refusal = %+v, want one not naming it", e)
	}
}
