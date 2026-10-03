package processing

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"io"
	"math"
	"strconv"
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
//
// Re-pinned once for E2-S1 (guidance-backfill-inferential): one cell
// moved by an ulp (0.031905255236596375 → 0.0319052552365966) when
// standardNormalCDF switched from ½(1+erf) to ½·erfc.
//
// Re-pinned for E2-S3 (guidance-backfill-inferential): twoProportionZ
// moved from 2·(1 − Φ(|z|)) to the tail-accurate erfc(|z|/√2). The
// small-p cells moved toward R's 2*pnorm(-|z|), e.g. 70/100 vs 20/100
// 1.1886047701636926e-12 → 1.1885852395646834e-12 (R
// 1.1885852395646815e-12); cells near 0.03 moved by a few ulps.
//
// The literal is the darwin/arm64 capture. Compare it ONLY through
// assertPanelBaselineBytes: math.Exp (reached via math.Erfc in
// normalTwoSidedP) is per-arch assembly in the Go stdlib and rounds the
// last ulp differently on amd64 vs arm64 for the SAME input bits, so
// three p-values in this literal differ by one ulp on linux/amd64.
const panelBaselineLayerJSON = `{"name":"test_panel","kind":"OVERLAY_PROP_Z_PANEL","scope":"cell","ref":{},"payload":{"shape":"matrix","matrix":{"row_header":{"fields":null,"types":["GROUP_CATEGORY"]},"column_header":{"fields":null,"types":["GROUP_CATEGORY"]},"row_keys":[["r0"],["r1"],["r2"]],"column_keys":[["c0"],["c1"],["c2"]],"cells":[[{"value":[0.1552184896846842,0.1552184896846842,0.004677734981047279],"present":true},{"value":[0.4789500234203581,0.031905255236596514,0.004473649256611758],"present":true},{"value":[0.47895002342035853,0.03190525523659656,0.004473649256611767],"present":true}],[{"value":[1,0.0038924171227786427,0.0038924171227786427],"present":true},{"value":[0.1552184896846842,0.1552184896846842,0.004677734981047279],"present":true},{"value":[0.0038924171227786427,0.000008687711677000068,1.1885852395646834e-12],"present":true}],[{"value":[0.003892417122778627,0.4789500234203581,0.00034890138826504613],"present":true},{"value":[0.03190525523659656,0.0002607296328553168,1.3054316211489304e-8],"present":true},{"value":[1,0.7772540327312343,0.7772540327312343],"present":true}]],"grand_total":{"present":false},"cell_label":"test_panel","normalize_applied":"none"}},"summary":{"count":9}}`

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
		free := panelParamsFreeLayerJSON(t)
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
			assertPanelBaselineBytes(t, got, "panel layer JSON drifted from the pre-params baseline")
			if string(got) != string(free) {
				t.Fatalf("params %v is distinguishable from a params-free spec on this arch:\n got %s\nwant %s",
					tc.params, got, free)
			}
		})
	}
}

// panelParamsFreeLayerJSON runs the baseline fixture under a spec that
// carries no params at all and returns the marshalled layer — the
// same-arch, same-binary twin every default-path variant must equal
// BYTE FOR BYTE. Together with assertPanelBaselineBytes this keeps the
// full byte-identity contract: exact against the params-free run here,
// and exact-structure / ulp-tolerant-number against the frozen literal.
func panelParamsFreeLayerJSON(t *testing.T) []byte {
	t.Helper()
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	layer, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPropZPanel (params-free): %v", err)
	}
	got, err := json.Marshal(layer)
	if err != nil {
		t.Fatalf("marshal params-free layer: %v", err)
	}
	return got
}

// panelBaselineRelTol bounds the per-number drift tolerated against the
// frozen literal. It exists for ONE reason: Go's math.Exp is per-arch
// assembly (exp_amd64.s vs the arm64 path) and differs in the last ulp
// for identical input bits, which math.Erfc — and so every overlay
// p-value — inherits. Observed drift is 1–2 ulp (~2e-16 relative); a
// real params regression moves a p-value by orders of magnitude more.
const panelBaselineRelTol = 1e-12

// assertPanelBaselineBytes compares got to panelBaselineLayerJSON token
// by token: every delimiter, key, string, bool and null must match
// EXACTLY and in order, every number must either be the identical
// literal or agree to panelBaselineRelTol. An exact byte match
// short-circuits.
func assertPanelBaselineBytes(t *testing.T, got []byte, msg string) {
	t.Helper()
	if string(got) == panelBaselineLayerJSON {
		return
	}
	fail := func(why string) {
		t.Helper()
		t.Fatalf("%s (%s):\n got %s\nwant %s", msg, why, got, panelBaselineLayerJSON)
	}
	gd := json.NewDecoder(bytes.NewReader(got))
	wd := json.NewDecoder(bytes.NewReader([]byte(panelBaselineLayerJSON)))
	gd.UseNumber()
	wd.UseNumber()
	for i := 0; ; i++ {
		gt, gerr := gd.Token()
		wt, werr := wd.Token()
		if gerr == io.EOF && werr == io.EOF {
			return
		}
		if gerr != nil || werr != nil {
			fail("token stream length differs")
		}
		gn, gIsNum := gt.(json.Number)
		wn, wIsNum := wt.(json.Number)
		if gIsNum != wIsNum {
			fail("token " + strconv.Itoa(i) + " kind differs")
		}
		if !gIsNum {
			if gt != wt {
				fail("token " + strconv.Itoa(i) + " differs")
			}
			continue
		}
		if gn == wn {
			continue
		}
		gf, err1 := gn.Float64()
		wf, err2 := wn.Float64()
		if err1 != nil || err2 != nil {
			fail("token " + strconv.Itoa(i) + " is not a float")
		}
		if math.Abs(gf-wf) > panelBaselineRelTol*math.Abs(wf) {
			fail("number " + string(gn) + " vs " + string(wn) + " beyond relative 1e-12")
		}
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
