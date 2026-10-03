package pulse_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestAggFrequency_PredictMessageMatchesRuntime: an AGG_FREQUENCY slot
// without params.value is refused by Process, ProcessStream and
// predict with one code and one message — the descriptor duplicates
// the runtime's prose (it may not import processing), and this pins
// the two copies together. A present value runs on every surface,
// counting the rows equal to it.
func TestAggFrequency_PredictMessageMatchesRuntime(t *testing.T) {
	fs, cohort := zoneCohort(t)
	p := zonePulse(t, fs, "")
	ctx := context.Background()
	mk := func(params string) *types.Request {
		a := &types.Aggregation{Type: types.AGG_FREQUENCY, Field: "cat", Label: "hits"}
		if params != "" {
			a.Params = json.RawMessage(params)
		}
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{a}}
	}

	_, rerr := p.Process(ctx, mk(""))
	ce := requireCode(t, rerr, errors.PROCESSING_CONFIG)
	_, serr := p.ProcessStream(ctx, mk(""))
	if se := requireCode(t, serr, errors.PROCESSING_CONFIG); se.Message != ce.Message {
		t.Fatalf("stream = %q, process = %q", se.Message, ce.Message)
	}
	env := predictEnvelope(t, p, fs, cohort, mk(""))
	found := false
	for _, e := range env.Errors {
		if e.Code == string(ce.Code) && e.Message == ce.Message {
			found = true
		}
	}
	if !found {
		t.Fatalf("predict errors %+v lack the runtime refusal %q", env.Errors, ce.Message)
	}

	// cat cycles a, b, c over 40 rows: 14 a's.
	resp, err := p.Process(ctx, mk(`{"value":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]["hits"]; got != 14.0 {
		t.Errorf("hits = %v, want 14", got)
	}
	if env := predictEnvelope(t, p, fs, cohort, mk(`{"value":"a"}`)); len(env.Errors) != 0 {
		t.Errorf("predict refused a present value: %+v", env.Errors)
	}
}
