package processing

import (
	"encoding/json"
	stderrors "errors"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// panelBaselineLayerJSON is the marshalled OVERLAY_PROP_Z_PANEL layer
// for the reference + two-target 3×3 fixture below under a params-free
// spec. It was captured by running the identical generator against a
// `git archive` of the pre-PanelOverlayParams tree (HEAD ba8370b) in a
// scratch checkout, then verified byte-equal against this tree.
//
// It is the additive-inertness pin for the whole E3/E4 params arc:
// PanelOverlayParams is empty today, and every field added to it must
// keep this literal reachable from a spec that names no params. A
// field whose zero value quietly changes the fold breaks this first.
const panelBaselineLayerJSON = `{"name":"test_panel","kind":"OVERLAY_PROP_Z_PANEL","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":null,"types":["GROUP_CATEGORY"]},"column_header":{"fields":null,"types":["GROUP_CATEGORY"]},"row_keys":[["r0"],["r1"],["r2"]],"column_keys":[["c0"],["c1"],["c2"]],"cells":[[{"value":[0.1552184896846842,0.1552184896846842,0.004677734981047177],"present":true},{"value":[0.4789500234203581,0.031905255236596375,0.004473649256611756],"present":true},{"value":[0.47895002342035853,0.0319052552365966,0.004473649256611756],"present":true}],[{"value":[1,0.0038924171227785465,0.0038924171227785465],"present":true},{"value":[0.1552184896846842,0.1552184896846842,0.004677734981047177],"present":true},{"value":[0.0038924171227785465,0.000008687711676946819,1.1886047701636926e-12],"present":true}],[{"value":[0.0038924171227785465,0.4789500234203581,0.0003489013882651548],"present":true},{"value":[0.0319052552365966,0.0002607296328553943,1.3054316294613955e-8],"present":true},{"value":[1,0.7772540327312343,0.7772540327312343],"present":true}]],"grand_total":{"present":false},"cell_label":"test_panel","normalize_applied":"none"}},"summary":{"count":9}}`

// panelBaselineSlots returns the reference + two target responses the
// baseline literal was captured from. Values are deliberately off the
// degenerate edges (no 0 % / 100 % cell, every row margin 100) so every
// pair produces a finite p-value and the layer marshals without NaN.
func panelBaselineSlots() (*types.Response, []*types.Response) {
	ref := makeMatrixWithRowMargins(
		[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
		[3]float64{100, 100, 100},
	)
	t0 := makeMatrixWithRowMargins(
		[3][3]float64{{60, 55, 45}, {50, 40, 70}, {30, 65, 50}},
		[3]float64{100, 100, 100},
	)
	t1 := makeMatrixWithRowMargins(
		[3][3]float64{{40, 35, 65}, {70, 60, 20}, {55, 25, 48}},
		[3]float64{100, 100, 100},
	)
	return ref, []*types.Response{t0, t1}
}

// TestApplyPropZPanel_ParamsInertWhenUnset is the byte-identity half of
// E3-S1: introducing types.PanelOverlayParams must not move a single
// byte of the panel's output for a spec that carries no params, or an
// empty params object. Both forms are asserted against the SAME
// pre-change literal, because "absent" and "{}" must not be
// distinguishable downstream either.
func TestApplyPropZPanel_ParamsInertWhenUnset(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"params absent", nil},
		{"params empty object", map[string]any{}},
		{"params unknown key", map[string]any{"not_a_panel_param": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, targets := panelBaselineSlots()
			spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
			spec.Params = tc.params
			layer, warns, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
			if err != nil {
				t.Fatalf("applyPropZPanel: %v", err)
			}
			if len(warns) != 0 {
				t.Fatalf("expected no warnings on the baseline fixture, got %v", warns)
			}
			got, err := json.Marshal(layer)
			if err != nil {
				t.Fatalf("marshal layer: %v", err)
			}
			if string(got) != panelBaselineLayerJSON {
				t.Fatalf("panel layer JSON drifted from the pre-params baseline:\n got %s\nwant %s",
					got, panelBaselineLayerJSON)
			}
		})
	}
}

// The params carrier and the cap knob are different slots, and the cap
// is the one the runtime reads. Pinned here so a later story that
// starts reading params cannot quietly migrate MaxPanelTargets onto
// them: the cap must keep firing from Options with params absent.
func TestApplyPropZPanel_CapRidesOptionsNotParams(t *testing.T) {
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, &types.OverlayOptions{MaxPanelTargets: 1})
	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err == nil {
		t.Fatal("expected the MaxPanelTargets cap to refuse a 2-target panel at cap 1")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP {
		t.Fatalf("error code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
}
