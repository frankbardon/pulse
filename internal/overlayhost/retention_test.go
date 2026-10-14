package overlayhost

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestParseRetentionParams: absent / null params mean no absorbing
// states; absorbing decodes as OVERLAY_MARKOV's list; a malformed,
// empty, repeated or unknown key is PULSE_OVERLAY_PARAM_MISSING with the
// index when given.
func TestParseRetentionParams(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindRetention}
	if p, cerr := ParseRetentionParams(spec, -1); cerr != nil || p.Absorbing != nil {
		t.Fatalf("default %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`null`)
	if p, cerr := ParseRetentionParams(spec, -1); cerr != nil || p.Absorbing != nil {
		t.Fatalf("null params %+v %v", p, cerr)
	}
	spec.Params = json.RawMessage(`{"absorbing":["churned","closed"]}`)
	if p, cerr := ParseRetentionParams(spec, -1); cerr != nil || !reflect.DeepEqual(p.Absorbing, []string{"churned", "closed"}) {
		t.Fatalf("absorbing %+v %v", p, cerr)
	}
	for _, bad := range []string{`{"absorbing":[]}`, `{"absorbing":"churned"}`, `{"absorbing":["a","a"]}`, `{"absorbing":[""]}`, `{"steps":[1]}`, `[]`} {
		spec.Params = json.RawMessage(bad)
		_, cerr := ParseRetentionParams(spec, 3)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_PARAM_MISSING || cerr.Details["index"] != 3 || cerr.Details["kind"] != "OVERLAY_RETENTION" {
			t.Fatalf("%s: refusal %v", bad, cerr)
		}
	}
	spec.Params = json.RawMessage(`{"absorbing":[]}`)
	if _, cerr := ParseRetentionParams(spec, -1); cerr == nil || cerr.Details["index"] != nil || cerr.Details["param"] != "absorbing" {
		t.Fatalf("no-index refusal %v", cerr)
	}
}

// TestRetentionSpecRefusal: a set Ref refuses first, then a non-row
// scope; a row spec with an empty Ref passes. index rides only when
// >= 0.
func TestRetentionSpecRefusal(t *testing.T) {
	spec := &types.OverlaySpec{Kind: types.OverlayKindRetention, Scope: types.OverlayScopeRow}
	if cerr := RetentionSpecRefusal(spec, 0); cerr != nil {
		t.Fatalf("row spec refused: %v", cerr)
	}
	spec.Scope = types.OverlayScopeMatrix
	if cerr := RetentionSpecRefusal(spec, 1); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED || cerr.Details["scope"] != "matrix" || cerr.Details["index"] != 1 {
		t.Fatalf("scope refusal %v", cerr)
	}
	spec.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}
	if cerr := RetentionSpecRefusal(spec, -1); cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || cerr.Details["index"] != nil {
		t.Fatalf("ref refusal %v", cerr)
	}
}
