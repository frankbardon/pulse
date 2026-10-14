package overlayhost

import (
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabSpecRefusal_Rules: every rule passes its own well-formed
// spec, refuses a bad Ref before a bad Scope, and carries index only
// when >= 0. A kind without a rule (OVERLAY_FORMULA, a SERIES kind, the
// empty route of a hidden kind) is never refused here.
func TestCrosstabSpecRefusal_Rules(t *testing.T) {
	for _, kind := range CrosstabSpecKinds() {
		scopes := CrosstabSpecScopes(kind)
		if len(scopes) == 0 {
			t.Fatalf("%s: rule without a scope", kind)
		}
		good := &types.OverlaySpec{Kind: kind, Scope: scopes[0]}
		if specRules[kind].ref == refMargin {
			good.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand}}
		}
		for _, s := range scopes {
			ok := *good
			ok.Scope = s
			if cerr := CrosstabSpecRefusal(&ok, kind, 0); cerr != nil {
				t.Fatalf("%s scope %s refused: %v", kind, s, cerr)
			}
		}
		bad := *good
		bad.Scope = "nowhere"
		cerr := CrosstabSpecRefusal(&bad, kind, 2)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_SCOPE_UNSUPPORTED || cerr.Details["scope"] != "nowhere" || cerr.Details["index"] != 2 {
			t.Fatalf("%s: scope refusal %v", kind, cerr)
		}
		// A bad Ref is judged before the bad scope.
		if specRules[kind].ref == refMargin {
			bad.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: "diagonal"}}
			cerr = CrosstabSpecRefusal(&bad, kind, -1)
			if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || cerr.Details["axis"] != "diagonal" || cerr.Details["index"] != nil {
				t.Fatalf("%s: axis refusal %v", kind, cerr)
			}
			bad.Ref = types.OverlayRef{}
		} else {
			bad.Ref = types.OverlayRef{Matrix: &types.OverlayMatrixRef{Name: "m"}}
		}
		cerr = CrosstabSpecRefusal(&bad, kind, -1)
		if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE || cerr.Details["index"] != nil || cerr.Details["kind"] != string(kind) {
			t.Fatalf("%s: ref refusal %v", kind, cerr)
		}
	}
	for _, kind := range []types.OverlayKind{types.OverlayKindFormula, types.OverlayKindDeltaVsPrior, ""} {
		spec := &types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix, Scope: "nowhere",
			Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}}
		if cerr := CrosstabSpecRefusal(spec, kind, 0); cerr != nil {
			t.Fatalf("route %q refused: %v", kind, cerr)
		}
	}
}

// TestCrosstabSpecRefusal_CoversMatrixKinds: every Request-only kind on
// the MATRIX (crosstab) shape has a rule, OVERLAY_FORMULA excepted (no
// Ref / Scope contract on either side), and no rule names a kind outside
// that set — a new crosstab kind cannot skip the shared rule.
func TestCrosstabSpecRefusal_CoversMatrixKinds(t *testing.T) {
	ruled := map[types.OverlayKind]bool{}
	for _, k := range CrosstabSpecKinds() {
		ruled[k] = true
	}
	for _, kind := range types.AllOverlayKinds() {
		matrix := Accepts(Request, kind) && ShapeRefusal(types.OverlayShapeMatrix, kind, kind, 0) == nil
		switch {
		case kind == types.OverlayKindFormula:
			if ruled[kind] {
				t.Errorf("%s has a rule", kind)
			}
		case matrix && !ruled[kind]:
			t.Errorf("crosstab kind %s has no Ref / Scope rule", kind)
		case !matrix && ruled[kind]:
			t.Errorf("rule for %s, which does not run on a crosstab", kind)
		}
	}
}
