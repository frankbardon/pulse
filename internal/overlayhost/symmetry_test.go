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
	for _, bad := range []string{`{"correct":"yes"}`, `{"correct":null}`, `{"correct":1}`, `{"exact":true}`, `[]`} {
		spec.Params = json.RawMessage(bad)
		_, cerr := ParseSymmetryParams(spec, 2)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["index"] != 2 || cerr.Details["kind"] != "OVERLAY_SYMMETRY" {
			t.Fatalf("%s: refusal %v", bad, cerr)
		}
	}
	spec.Params = json.RawMessage(`{"correct":0}`)
	if _, cerr := ParseSymmetryParams(spec, -1); cerr == nil || cerr.Details["index"] != nil || cerr.Details["param"] != "correct" {
		t.Fatalf("no-index refusal %v", cerr)
	}
}

// TestSymmetrySpecRefusal: a set Ref refuses first, then a non-matrix
// scope; a matrix spec with an empty Ref passes. index rides only when
// >= 0.
func TestSymmetrySpecRefusal(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindSymmetry, Scope: types.OverlayScopeMatrix}
	if cerr := SymmetrySpecRefusal(spec, 0); cerr != nil {
		t.Fatalf("matrix spec refused: %v", cerr)
	}
	spec.Scope = types.OverlayScopeCell
	if cerr := SymmetrySpecRefusal(spec, 1); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED || cerr.Details["scope"] != "cell" || cerr.Details["index"] != 1 {
		t.Fatalf("scope refusal %v", cerr)
	}
	spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	if cerr := SymmetrySpecRefusal(spec, -1); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || cerr.Details["index"] != nil {
		t.Fatalf("ref refusal %v", cerr)
	}
}
