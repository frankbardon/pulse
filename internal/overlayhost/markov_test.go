package overlayhost

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestMarkovStateName: a one-element string key is itself; any other
// key is its JSON, so numbers and multi-grouper keys stay distinct.
func TestMarkovStateName(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		key  types.AxisKey
		want string
	}{
		{types.AxisKey{"gold"}, "gold"},
		{types.AxisKey{3.0}, "3"},
		{types.AxisKey{true}, "true"},
		{types.AxisKey{"a", "b"}, `["a","b"]`},
		{types.AxisKey{}, `[]`},
	} {
		if got := MarkovStateName(c.key); got != c.want {
			t.Errorf("MarkovStateName(%v) = %q, want %q", c.key, got, c.want)
		}
	}
}

// TestParseMarkovParams_Decoded: the accepted forms decode to the
// documented values (defaults, given order, raw shares).
func TestParseMarkovParams_Decoded(t *testing.T) {
	t.Parallel()

	spec := &types.OverlaySpec{Kind: types.OverlayKindMarkov}
	p, cerr := ParseMarkovParams(spec, -1)
	if cerr != nil || !slices.Equal(p.Steps, MarkovDefaultSteps()) || p.Initial || p.Absorbing != nil {
		t.Fatalf("defaults %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"steps":[12,1],"absorbing":["churned"],"initial":{"gold":3,"silver":1}}`)
	p, cerr = ParseMarkovParams(spec, -1)
	if cerr != nil || !slices.Equal(p.Steps, []int{12, 1}) || !slices.Equal(p.Absorbing, []string{"churned"}) ||
		!p.Initial || p.InitialMargin || p.InitialShares["gold"] != 3 {
		t.Fatalf("decoded %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"initial":"margin"}`)
	if p, cerr = ParseMarkovParams(spec, -1); cerr != nil || !p.Initial || !p.InitialMargin {
		t.Fatalf("margin %+v %v", p, cerr)
	}
	// No index (-1) leaves it out of the details.
	spec.Params = json.RawMessage(`{"steps":[0]}`)
	if _, cerr = ParseMarkovParams(spec, -1); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["index"] != nil {
		t.Fatalf("refusal %v", cerr)
	}
}

// TestMarkovRefusals: each shared refusal is nil on a passing input and
// carries its code and sorted details otherwise.
func TestMarkovRefusals(t *testing.T) {
	t.Parallel()

	k := types.OverlayKindMarkov
	if MarkovArityRefusal(k, 1, 1, 0) != nil || MarkovKeySetRefusal(k, nil, nil, 0) != nil || MarkovStateRefusal(k, "absorbing", nil, nil, 0) != nil {
		t.Fatal("a passing input was refused")
	}
	if c := MarkovArityRefusal(k, 1, 2, 3); c == nil || c.Code != errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE || c.Details["column_groupers"] != 2 || c.Details["index"] != 3 {
		t.Fatalf("arity %v", c)
	}
	c := MarkovKeySetRefusal(k, []string{"z", "a"}, []string{"q"}, -1)
	if c == nil || c.Code != errors.PULSE_OVERLAY_MATRIX_NOT_SQUARE || !slices.Equal(c.Details["row_only"].([]string), []string{"a", "z"}) || c.Details["index"] != nil {
		t.Fatalf("key set %v", c)
	}
	c = MarkovStateRefusal(k, "initial", []string{"y", "x"}, []string{"a"}, 1)
	if c == nil || c.Code != errors.PULSE_OVERLAY_PARAM_MISSING || c.Details["param"] != "initial" || !slices.Equal(c.Details["unknown"].([]string), []string{"x", "y"}) {
		t.Fatalf("state %v", c)
	}
}
