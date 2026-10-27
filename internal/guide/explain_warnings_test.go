package guide

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

func leakingRequest() *types.Request {
	return &types.Request{Cohort: fixtureCohort,
		Features:     []*types.Feature{{Type: types.FEAT_TARGET_ENCODE, Field: "region", Params: json.RawMessage(`{"target":"score"}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}}}
}

func leakage(es []descriptor.EnvelopeEntry) []descriptor.EnvelopeEntry {
	var out []descriptor.EnvelopeEntry
	for _, e := range es {
		if e.Code == string(errors.PULSE_FEAT_TARGET_LEAKAGE_RISK) {
			out = append(out, e)
		}
	}
	return out
}

// TestExplain_CarriesPredictWarnings pins #309: Explain relays predict's
// warnings on ExplainResult.Warnings — the request root's exactly as
// predict reported them, and a Compose slot's once (the Compose check
// locates it details.request; the slot's own predict repeats it),
// details.slot naming the slot.
func TestExplain_CarriesPredictWarnings(t *testing.T) {
	t.Parallel()

	c := explainChecks(t, nil)
	res := explain(t, nil, descriptor.ExplainRequest{Request: leakingRequest()}, c)
	data := cohortBytes(t, boundFixture(t))
	env := descx.Predict(bytes.NewReader(data), leakingRequest(), &descx.PredictOptions{})
	if len(env.Warnings) == 0 || len(res.Warnings) != len(env.Warnings) {
		t.Fatalf("explain warnings %+v, predict %d", res.Warnings, len(env.Warnings))
	}
	for i, w := range env.Warnings {
		got := res.Warnings[i]
		if got.Code != w.Code || got.Message != w.Message || !reflect.DeepEqual(got.Details, w.Details) {
			t.Errorf("warning %d = %+v, predict %+v", i, got, *w)
		}
	}
	if res.Valid == nil || !*res.Valid {
		t.Errorf("a warning is not a refusal: valid = %v", res.Valid)
	}

	comp := explain(t, nil, descriptor.ExplainRequest{Composed: &types.ComposedRequest{
		Requests: []*types.Request{{Cohort: fixtureCohort, Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "score"}}}, leakingRequest()},
	}}, c)
	if got := leakage(comp.Warnings); len(got) != 1 || got[0].Details["request"] != 1 {
		t.Errorf("compose leakage warnings = %+v, want one at request 1", got)
	}
}
