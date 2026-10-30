package integration_test

import (
	"encoding/json"
	"slices"
	"testing"

	stderrors "errors"
	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	perrors "github.com/frankbardon/pulse/errors"
)

// The facade reads through the instance: a registered extension
// aggregator's params come from its FieldInputs hook, a built-in's from
// the manifest, and an expression slot from the engine's walk.
func TestPulse_RequestReads(t *testing.T) {
	t.Parallel()
	ext := pulse.Extensions{Aggregators: []pulse.AggregatorRegistration{{
		Name:        "AGG_ACME_SPREAD",
		Description: "ACME spread between two columns.",
		Factory:     stubAggregatorFactory,
		Streamable:  true,
		Params:      []pulse.ParamMeta{{Name: "lo", JSONType: "string"}, {Name: "hi", JSONType: "string"}},
		FieldInputs: func(raw json.RawMessage) []string {
			var p struct{ Lo, Hi string }
			_ = json.Unmarshal(raw, &p)
			return []string{p.Lo, p.Hi}
		},
	}}}
	p, err := pulse.New(newTestOptions(ext))
	if err != nil {
		t.Fatal(err)
	}
	doc := `{"params":{"wave":"2"},
		"filterers":[{"type":"FILTER_EXPRESSION","expression":"wave == params.wave"}],
		"aggregations":[{"type":"AGG_ACME_SPREAD","params":{"lo":"p10","hi":"p90"}}]}`
	got, err := p.RequestReads([]byte(doc), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Root != descriptor.ReadsRootRequest || len(got.Slots) != 2 {
		t.Fatalf("got %+v", got)
	}
	agg, fil := got.Slots[0], got.Slots[1]
	if agg.Path != "aggregations[0]" || agg.Key != "params" || !slices.Equal(agg.Columns, []string{"p10", "p90"}) {
		t.Errorf("extension slot: %+v", agg)
	}
	if fil.Path != "filterers[0]" || fil.Key != "expression" || !slices.Equal(fil.Columns, []string{"wave"}) || !slices.Equal(fil.Params, []string{"wave"}) {
		t.Errorf("expression slot: %+v", fil)
	}

	_, err = p.RequestReads([]byte(`[]`), "")
	var ce *perrors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perrors.SERVICE_VALIDATION {
		t.Errorf("non-object doc: got %v, want SERVICE_VALIDATION", err)
	}
}
