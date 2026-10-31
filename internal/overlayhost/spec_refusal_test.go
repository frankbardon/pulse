package overlayhost

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestCrosstabSpecRefusal_Rules: every rule passes its own well-formed
// spec, refuses a bad Ref before a bad Scope, and carries index only
// when >= 0. A kind without a rule (OVERLAY_FORMULA, a SERIES kind, the
// empty route of a hidden kind) is never refused here.
func TestCrosstabSpecRefusal_Rules(t *testing.T) {
	t.Parallel()

	for _, kind := range CrosstabSpecKinds() {
		scopes := CrosstabSpecScopes(kind)
		if len(scopes) == 0 {
			t.Fatalf("%s: rule without a scope", kind)
		}
		good := &types.OverlaySpec{Kind: kind, Scope: scopes[0]}
		if specRules[kind].ref == refMargin {
			good.Ref = types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisGrand}}
			if a := CrosstabSpecMarginAxis(kind); a != "" {
				good.Ref.Margin.Axis = a
			}
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
	t.Parallel()

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

// TestCrosstabSpecRefusal_AxisLockedShares: OVERLAY_SHARE_OF_ROW accepts
// only the row axis and OVERLAY_SHARE_OF_COL only the column axis; any
// other known axis is PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE carrying
// the axis (before it the spec ran silently as the locked share).
// OVERLAY_SHARE_OF_TOTAL documents its axis as ignored and stays open.
func TestCrosstabSpecRefusal_AxisLockedShares(t *testing.T) {
	t.Parallel()

	axes := []types.MarginAxis{types.MarginAxisRow, types.MarginAxisColumn, types.MarginAxisGrand}
	for kind, want := range map[types.OverlayKind]types.MarginAxis{
		types.OverlayKindShareOfRow:   types.MarginAxisRow,
		types.OverlayKindShareOfCol:   types.MarginAxisColumn,
		types.OverlayKindShareOfTotal: "",
	} {
		if got := CrosstabSpecMarginAxis(kind); got != want {
			t.Fatalf("%s: locked axis %q, want %q", kind, got, want)
		}
		for _, a := range axes {
			spec := &types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: a}}}
			cerr := CrosstabSpecRefusal(spec, kind, 1)
			if want == "" || a == want {
				if cerr != nil {
					t.Fatalf("%s axis %s refused: %v", kind, a, cerr)
				}
				continue
			}
			if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE ||
				cerr.Details["axis"] != string(a) || cerr.Details["index"] != 1 || cerr.Details["kind"] != string(kind) {
				t.Fatalf("%s axis %s: %v, want the axis-locked refusal", kind, a, cerr)
			}
		}
	}
}

// TestCrosstabSpecRefusal_MarginKindsRefuseOtherRefArms: a margin kind
// (DELTA / INDEX / ZSCORE_VS_MARGIN, SHARE_OF_ROW / _COL / _TOTAL) reads
// only Ref.Margin, so any other populated Ref arm beside a valid margin is
// PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE {index, kind} — before it
// the arm was silently ignored. Every OverlayRef pointer field is
// populated in turn (reflection), so a new arm is covered on arrival.
func TestCrosstabSpecRefusal_MarginKindsRefuseOtherRefArms(t *testing.T) {
	t.Parallel()

	refType := reflect.TypeOf(types.OverlayRef{})
	arms := 0
	for _, kind := range CrosstabSpecKinds() {
		if specRules[kind].ref != refMargin {
			continue
		}
		axis := types.MarginAxisGrand
		if a := CrosstabSpecMarginAxis(kind); a != "" {
			axis = a
		}
		for i := 0; i < refType.NumField(); i++ {
			f := refType.Field(i)
			if f.Name == "Margin" {
				continue
			}
			if f.Type.Kind() != reflect.Pointer {
				t.Fatalf("OverlayRef.%s is not a pointer arm", f.Name)
			}
			spec := &types.OverlaySpec{Kind: kind, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: axis}}}
			reflect.ValueOf(&spec.Ref).Elem().Field(i).Set(reflect.New(f.Type.Elem()))
			cerr := CrosstabSpecRefusal(spec, kind, 3)
			if cerr == nil || cerr.Code != errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE ||
				cerr.Details["index"] != 3 || cerr.Details["kind"] != string(kind) || len(cerr.Details) != 2 {
				t.Fatalf("%s with Ref.%s: %v, want the other-arm refusal", kind, f.Name, cerr)
			}
			arms++
		}
	}
	if arms == 0 {
		t.Fatal("no margin kind × arm judged")
	}
}
