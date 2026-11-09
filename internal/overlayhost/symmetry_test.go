package overlayhost

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParseSymmetryParams: correct defaults to true (R's mcnemar.test),
// decodes a boolean, and refuses a non-boolean, null or unknown key with
// PULSE_OVERLAY_PARAM_MISSING carrying the index when given.
func TestParseSymmetryParams(t *testing.T) {
	t.Parallel()

	spec := &types.OverlaySpec{Kind: types.OverlayKindSymmetry}
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("default %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`null`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("null params %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"correct":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.Correct {
		t.Fatalf("correct false %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"correct":true}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Correct {
		t.Fatalf("correct true %+v %v", p, cerr)
	}
	for _, bad := range []string{`{"correct":"yes"}`, `{"correct":null}`, `{"correct":1}`, `{"exact":"yes"}`, `{"exact":null}`, `{"mid_p":true}`, `[]`} {
		spec.Params = json.RawMessage(bad)
		_, cerr := ParseSymmetryParams(spec, 2)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["index"] != 2 || cerr.Details["kind"] != "OVERLAY_SYMMETRY" {
			t.Fatalf("%s: refusal %v", bad, cerr)
		}
	}
	spec.Params = json.RawMessage(`{"exact":true,"correct":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || !p.Exact || p.Correct {
		t.Fatalf("exact true %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"exact":false}`)
	if p, cerr := ParseSymmetryParams(spec, -1); cerr != nil || p.Exact || !p.Correct {
		t.Fatalf("exact false %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"exact":1}`)
	if _, cerr := ParseSymmetryParams(spec, 4); cerr == nil || cerr.Details["param"] != "exact" || cerr.Details["index"] != 4 {
		t.Fatalf("non-boolean exact refusal %v", cerr)
	}
	spec.Params = json.RawMessage(`{"correct":0}`)
	if _, cerr := ParseSymmetryParams(spec, -1); cerr == nil || cerr.Details["index"] != nil || cerr.Details["param"] != "correct" {
		t.Fatalf("no-index refusal %v", cerr)
	}
}

// TestSymmetryExactRefusals: exact is refused on a table of other than
// 2 states, over a probability-weighted cell and on a non-integer
// discordant pair (binom.test's 1e-7 rounding rule); each is inert
// without exact.
func TestSymmetryExactRefusals(t *testing.T) {
	t.Parallel()

	kind := types.OverlayKindSymmetry
	exact, chi := SymmetryParams{Exact: true}, SymmetryParams{Correct: true}
	if cerr := SymmetryExactStatesRefusal(kind, exact, 2, 1); cerr != nil {
		t.Fatalf("2 states refused: %v", cerr)
	}
	if cerr := SymmetryExactStatesRefusal(kind, chi, 3, 1); cerr != nil {
		t.Fatalf("chi-square on 3 states refused: %v", cerr)
	}
	cerr := SymmetryExactStatesRefusal(kind, exact, 3, 1)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["param"] != "exact" ||
		cerr.Details["states"] != 3 || cerr.Details["index"] != 1 {
		t.Fatalf("3-state exact refusal %v", cerr)
	}

	if cerr := SymmetryExactWeightRefusal(kind, exact, false, 0); cerr != nil {
		t.Fatalf("unweighted exact refused: %v", cerr)
	}
	if cerr := SymmetryExactWeightRefusal(kind, chi, true, 0); cerr != nil {
		t.Fatalf("probability chi-square refused: %v", cerr)
	}
	cerr = SymmetryExactWeightRefusal(kind, exact, true, 2)
	if cerr == nil || cerr.Code != errors.PULSE_WEIGHT_UNSUPPORTED || cerr.Details["slot"] != "overlays[2]" ||
		cerr.Details["param"] != "exact" || cerr.Details["kind"] != "probability" || cerr.Details["index"] != 2 {
		t.Fatalf("probability exact refusal %v", cerr)
	}

	if a, b, cerr := SymmetryExactCountsRefusal(kind, 3.00000001, 5, 0); cerr != nil || a != 3 || b != 5 {
		t.Fatalf("near-integer pair: %d %d %v", a, b, cerr)
	}
	_, _, cerr = SymmetryExactCountsRefusal(kind, 3, 5.5, 0)
	if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_NONNEGATIVE_REQUIRED || cerr.Details["reason"] != "non_integer" || cerr.Details["index"] != 0 {
		t.Fatalf("non-integer pair refusal %v", cerr)
	}
}
