package descriptor

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/sweep/sweeptest"
	"github.com/frankbardon/pulse/types"
)

// TestSweepAdaptive_HiddenRefusal: an instance offering the sampling
// sweep but not capability:compose_sweep_adaptive refuses a set
// `budget` as an unknown key at the sweep (valid_keys never naming it)
// and an adaptive mode as an unknown mode whose `valid` names every
// other mode; the enabled instance refuses nothing, and the sampling
// modes pass the hiding one.
func TestSweepAdaptive_HiddenRefusal(t *testing.T) {
	t.Parallel()

	lo, hi := 0.0, 1.0
	adaptive := func(edit func(*types.SweepSpec)) *types.ComposedRequest {
		spec := &types.SweepSpec{
			Mode: sweeptest.Mode, Budget: 4,
			Axes:    []types.SweepAxis{{Name: "k", Range: &types.SweepRange{Min: &lo, Max: &hi}}},
			Request: json.RawMessage(`{}`),
			Rank:    &types.SweepRank{By: "a.b"},
		}
		if edit != nil {
			edit(spec)
		}
		return &types.ComposedRequest{Requests: []*types.Request{{}}, Sweep: spec}
	}
	hidden := scopedOnly(featCompose, featComposeSweep, featSweepSampling)
	enabled := scopedOnly(featCompose, featComposeSweep, featSweepSampling, featSweepAdaptive)

	ce := asCoded(t, SlotRefusal(adaptive(nil), hidden))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"budget"}) ||
		slices.Contains(ce.Details["valid_keys"].([]string), "budget") {
		t.Fatalf("budget: got %s %v", ce.Code, ce.Details)
	}
	ce = asCoded(t, SlotRefusal(adaptive(func(s *types.SweepSpec) { s.Budget = 0 }), hidden))
	valid, _ := ce.Details["valid"].([]string)
	if ce.Code != errors.PULSE_SWEEP_INVALID || ce.Details["reason"] != "mode_unknown" ||
		slices.Contains(valid, string(sweeptest.Mode)) || !slices.Contains(valid, "lhs") {
		t.Fatalf("mode: got %s %v", ce.Code, ce.Details)
	}
	if err := SlotRefusal(adaptive(func(s *types.SweepSpec) { s.Mode, s.Budget, s.Samples = types.SweepModeLHS, 0, 3 }), hidden); err != nil {
		t.Errorf("lhs refused on the hiding instance: %v", err)
	}
	if err := SlotRefusal(adaptive(nil), enabled); err != nil {
		t.Errorf("enabled instance refuses: %v", err)
	}
	// Hiding the sampling modes hides the adaptive ones with them (the
	// dependency edge), and the mode reads as unknown there too.
	ce = asCoded(t, SlotRefusal(adaptive(func(s *types.SweepSpec) {
		s.Budget = 0
		s.Axes = []types.SweepAxis{{Name: "k", Values: []any{1}}}
	}), scopedOnly(featCompose, featComposeSweep)))
	if ce.Details["reason"] != "mode_unknown" || !reflect.DeepEqual(ce.Details["valid"], []string{"grid", "zip"}) {
		t.Fatalf("sampling hidden: got %s %v", ce.Code, ce.Details)
	}
}

// TestSweepAdaptive_HiddenSchema: `budget` is on the SweepSpec def of
// the full registry and of an instance offering the adaptive modes, and
// absent from one hiding them.
func TestSweepAdaptive_HiddenSchema(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(featCompose, featComposeSweep, featSweepSampling, featSweepAdaptive), true},
		{"hidden", scopedOnly(featCompose, featComposeSweep, featSweepSampling), false},
	} {
		b, err := PayloadSchemaForInstance(c.inst)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Defs map[string]struct {
				Properties map[string]any `json:"properties"`
			} `json:"$defs"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
		if _, got := doc.Defs["SweepSpec"].Properties["budget"]; got != c.want {
			t.Errorf("%s: budget present %v, want %v", c.name, got, c.want)
		}
	}
}
