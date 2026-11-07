package descriptor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// TestComposePredict_SlotSizesAbsent: a slot whose executing schema
// predict cannot resolve — no SchemaLoader, or a cohort it cannot
// read — reports no sizes, and the `sizes` key is absent on the wire
// (the entry stays byte-identical).
// With a readable cohort the slot is sized, its `label` section first.
func TestComposePredict_SlotSizesAbsent(t *testing.T) {
	t.Parallel()

	agg := func(cohort string) *types.Request {
		return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Aggregations: []*types.Aggregation{{Type: types.AGG_AVERAGE, Field: "y", Label: "m"}}}
	}
	sized := composeResult(t, &types.ComposedRequest{Requests: []*types.Request{agg("e.pulse")}}, experimentOpts(t))
	if len(sized.Slots) != 1 || len(sized.Slots[0].Sizes) == 0 || sized.Slots[0].Sizes[0].Section != "label" {
		t.Fatalf("readable cohort: slots %+v, want sizes headed by label", sized.Slots)
	}

	for name, c := range map[string]struct {
		req  *types.ComposedRequest
		opts *PredictOptions
	}{
		"no schema loader":  {&types.ComposedRequest{Requests: []*types.Request{agg("e.pulse")}}, nil},
		"unreadable cohort": {&types.ComposedRequest{Requests: []*types.Request{agg("missing.pulse")}}, experimentOpts(t)},
	} {
		res := composeResult(t, c.req, c.opts)
		if len(res.Slots) != 1 {
			t.Fatalf("%s: slots %+v, want one", name, res.Slots)
		}
		if res.Slots[0].Sizes != nil {
			t.Errorf("%s: sizes %+v, want none", name, res.Slots[0].Sizes)
		}
		raw, err := json.Marshal(res.Slots[0])
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"sizes"`) {
			t.Errorf("%s: wire entry %s carries sizes", name, raw)
		}
	}
}
