package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	pulseerrors "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The panel's selectable n leg (E3-S3).
//
// Two properties carry the story. The DEFAULT path — absent params,
// `{}`, and the explicit `row_margin_value` spelling — must stay
// byte-identical to the pre-params baseline, cell-value fallback
// included. And the counted mode must be observably a DIFFERENT
// number, refuse a components-disabled slot instead of degrading, and
// never substitute a cell value for a sample size it could not read.

// withCellN attaches a CrosstabComponents block carrying the
// universal-floor "n" counter on every cell. `ns` mirrors the
// matrix coordinate-for-coordinate. A NaN entry emits a components map
// with NO "n" key at all — the coordinate the counted mode cannot
// read, which is the one the legacy fallback would have papered over.
func withCellN(resp *types.Response, ns [3][3]float64) *types.Response {
	cells := make([][]map[string]any, 3)
	for i := 0; i < 3; i++ {
		row := make([]map[string]any, 3)
		for j := 0; j < 3; j++ {
			if math.IsNaN(ns[i][j]) {
				row[j] = map[string]any{}
				continue
			}
			row[j] = map[string]any{"n": ns[i][j]}
		}
		cells[i] = row
	}
	resp.Components = &types.ResponseComponents{
		Crosstab: &types.CrosstabComponents{CellComponents: cells},
	}
	return resp
}

// panelCountedSlots is the counted-mode fixture: the same three
// matrices the baseline uses, but each slot's per-cell counted n is
// DELIBERATELY different from its payload row margin (100), so a
// handler that ignored n_source would produce the baseline numbers and
// the assertions below would catch it.
func panelCountedSlots() (*types.Response, []*types.Response) {
	ref, targets := panelBaselineSlots()
	withCellN(ref, [3][3]float64{{80, 80, 80}, {80, 80, 80}, {80, 80, 80}})
	withCellN(targets[0], [3][3]float64{{90, 90, 90}, {90, 90, 90}, {90, 90, 90}})
	withCellN(targets[1], [3][3]float64{{70, 70, 70}, {70, 70, 70}, {70, 70, 70}})
	return ref, targets
}

// --- Byte identity of the default path -------------------------------

// The story's safety property. E3-S1 pinned "absent" and "{}"; the
// explicit `row_margin_value` spelling joins them, because the whole point
// of naming the default is that writing it down changes nothing.
//
// Byte-identity is asserted against the SAME pre-change literal
// E3-S1 captured from a git archive of ba8370b, not against a
// re-derivation — a re-derived expectation would move with the code it
// is meant to pin.
func TestApplyPropZPanel_DefaultNSourceByteIdentical(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"params absent", nil},
		{"params empty object", map[string]any{}},
		{"n_source empty string", map[string]any{"n_source": ""}},
		{"n_source row_margin_value", map[string]any{"n_source": types.PanelNSourceRowMarginValue}},
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

// The <= 0 cell-value fallback SURVIVES on the default path. It is a
// degenerate-input crutch, but removing it would change the default's
// output, and the default must not move. The assertion is on the
// NUMBER, not on the absence of a warning: a fallback that quietly
// became a skip would leave the cell absent and this catches it.
func TestApplyPropZPanel_DefaultKeepsCellValueFallback(t *testing.T) {
	// Row 0 carries NO margin on the REFERENCE only, so exactly one
	// leg falls back. Both legs falling back would make pooled == 1 by
	// construction and the kernel would return NaN — indistinguishable
	// from the n = 0 the fallback's removal would produce. One leg
	// keeps the result finite and specific.
	ref := makeMatrixWithRowMargins(
		[3][3]float64{{30, 50, 50}, {50, 50, 50}, {50, 50, 50}},
		[3]float64{0, 100, 100},
	)
	target := makeMatrixWithRowMargins(
		[3][3]float64{{40, 55, 45}, {40, 40, 40}, {30, 65, 50}},
		[3]float64{100, 100, 100},
	)
	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)

	// The fallback substitutes the reference's own cell VALUE (30) for
	// its missing sample size. Dropping the fallback would leave n = 0
	// and the kernel would refuse the pair with NaN, so the two
	// outcomes are separable.
	want, ok := twoProportionZ(30, 30, 40, 100)
	if !ok {
		t.Fatal("fixture no longer exercises the fallback with a finite result")
	}
	if withoutFallback, ok := twoProportionZ(30, 0, 40, 100); ok || !math.IsNaN(withoutFallback) {
		t.Fatal("fixture cannot distinguish the fallback from a zero n")
	}

	for _, mode := range []string{"", types.PanelNSourceRowMarginValue} {
		spec.Params = map[string]any{"n_source": mode}
		layer, _, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
		if err != nil {
			t.Fatalf("mode %q: applyPropZPanel: %v", mode, err)
		}
		got := pairs(t, layer, 0, 0)
		if len(got) != 1 {
			t.Fatalf("mode %q: pair slice length = %d, want 1", mode, len(got))
		}
		// NaN-safe: math.Abs(NaN - want) > tol is FALSE, so a bare
		// inequality would silently pass when the fallback is removed
		// and n becomes 0 (which the kernel answers with NaN).
		if math.IsNaN(got[0]) {
			t.Errorf("mode %q: row 0 p = NaN, want %v; the cell-value fallback is gone and n went to 0",
				mode, want)
		} else if math.Abs(got[0]-want) > 1e-12 {
			t.Errorf("mode %q: row 0 p = %v, want %v (cell value 30 standing in for the missing margin)",
				mode, got[0], want)
		}
		// Row 1 keeps a real margin of 100 on both slots — no fallback.
		wantRow1, ok := twoProportionZ(50, 100, 40, 100)
		if !ok {
			t.Fatal("fixture row 1 is degenerate; pick different values")
		}
		approxEqual(t, "mode "+mode+" row 1 p", pairs(t, layer, 1, 0)[0], wantRow1, 1e-12)
	}
}

// --- The counted mode ------------------------------------------------

// cell_n_unweighted reads each slot's counted per-cell n out of
// components, and the number it produces is NOT the number the legacy
// margin leg produces on the same fixture. The inequality assertion is
// the one that matters: without it a handler that silently ignored
// n_source would pass every other check here.
func TestApplyPropZPanel_CellNUnweightedReadsComponents(t *testing.T) {
	ref, targets := panelCountedSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}

	layer, warns, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}

	// Coordinate (0, 0): ref value 50 / n 80, t0 value 60 / n 90,
	// t1 value 40 / n 70. Pair order is (0,1), (0,2), (1,2).
	got := pairs(t, layer, 0, 0)
	wants := []struct {
		sa, na, sb, nb float64
	}{
		{50, 80, 60, 90},
		{50, 80, 40, 70},
		{60, 90, 40, 70},
	}
	if len(got) != len(wants) {
		t.Fatalf("pair slice length = %d, want %d", len(got), len(wants))
	}
	for k, w := range wants {
		want, ok := twoProportionZ(w.sa, w.na, w.sb, w.nb)
		if !ok {
			t.Fatalf("pair %d expectation is degenerate; pick different fixture values", k)
		}
		approxEqual(t, "pair "+itoaPanel(k)+" p", got[k], want, 1e-12)
	}

	// And it differs from the legacy leg on the same fixture.
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValue}
	legacyLayer, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err != nil {
		t.Fatalf("legacy applyPropZPanel: %v", err)
	}
	legacy := pairs(t, legacyLayer, 0, 0)
	for k := range got {
		if got[k] == legacy[k] {
			t.Fatalf("pair %d: counted mode returned the legacy p-value %v; n_source is a no-op", k, got[k])
		}
	}
}

// Components are indexed POSITIONALLY per slot, but the panel walks the
// REFERENCE matrix's key order and every other read here is by KEY.
// Slots are guaranteed the same key SET, never the same ORDER, so a
// positional components read would silently answer with a different
// cell's sample size. The number would be plausible and wrong.
func TestApplyPropZPanel_CountedModeResolvesSlotCoordinatesByKey(t *testing.T) {
	ref := withCellN(
		makeMatrixWithRowMargins(
			[3][3]float64{{50, 50, 50}, {50, 50, 50}, {50, 50, 50}},
			[3]float64{100, 100, 100}),
		[3][3]float64{{80, 80, 80}, {80, 80, 80}, {80, 80, 80}})

	// The target declares the SAME key set in REVERSE row order, and
	// its counted n varies by row so a positional read lands on a
	// different figure than a keyed one.
	target := withCellN(
		makeMatrixWithRowMargins(
			[3][3]float64{{45, 45, 45}, {55, 55, 55}, {65, 65, 65}},
			[3]float64{100, 100, 100}),
		[3][3]float64{{60, 60, 60}, {75, 75, 75}, {90, 90, 90}})
	tmx := target.Crosstab.Matrix
	tmx.RowKeys = []types.AxisKey{{"r2"}, {"r1"}, {"r0"}}

	spec := composeSpecMultiTargetPropZPanel([]string{"t0"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}
	layer, warns, err := applyPropZPanel(&spec, ref, []*types.Response{target}, 0, []int{1})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}

	// Reference row "r0" is the target's row INDEX 2: value 65,
	// counted n 90. A positional read would take index 0 (value 45,
	// n 60) — and the cell VALUE is already read by key, so a
	// positional n would pair value 65 with n 60.
	want, ok := twoProportionZ(50, 80, 65, 90)
	if !ok {
		t.Fatal("expectation is degenerate")
	}
	wrong, _ := twoProportionZ(50, 80, 65, 60)
	got := pairs(t, layer, 0, 0)[0]
	if math.IsNaN(got) || math.Abs(got-want) > 1e-12 {
		t.Fatalf("p = %v, want %v (keyed n=90); the positional misread would give %v", got, want, wrong)
	}
}

// The counted mode does NOT inherit the legacy cell-value fallback.
// An unreadable counted leg SKIPS the coordinate with a warning naming
// the mode — a cell VALUE standing in for a sample size is the exact
// substitution the counted mode exists to remove. The same fixture
// under the default mode still EMITS at that coordinate, which is what
// proves the fallback was dropped deliberately rather than never
// reached.
func TestApplyPropZPanel_CountedModeDoesNotFallBackToCellValue(t *testing.T) {
	ref, targets := panelCountedSlots()
	// Drop the "n" key at (0, 0) on the first target only.
	nan := math.NaN()
	withCellN(targets[0], [3][3]float64{{nan, 90, 90}, {90, 90, 90}, {90, 90, 90}})

	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}
	// Panel index != Compose slot index. The reference is authored at
	// slot 3 and the targets at 7 and 9, so a diagnostic that reported
	// the panel's internal ordering would send the caller to a slot
	// they did not write.
	layer, warns, err := applyPropZPanel(&spec, ref, targets, 3, []int{7, 9})
	if err != nil {
		t.Fatalf("applyPropZPanel: %v", err)
	}

	mx := layer.Payload.Matrix
	if mx == nil {
		t.Fatal("layer matrix nil")
	}
	if mx.Cells[0][0].Present {
		t.Fatalf("coordinate (0,0) emitted %v; an unreadable counted n must skip the cell, not fall back",
			mx.Cells[0][0].Value)
	}
	// Every other coordinate still emits — the skip is per coordinate.
	if !mx.Cells[0][1].Present || !mx.Cells[1][0].Present {
		t.Fatal("the skip leaked beyond the unreadable coordinate")
	}
	if summary := layer.Summary; summary == nil || summary.Count == nil || *summary.Count != 8 {
		t.Fatalf("summary count = %v, want 8 of 9 coordinates", layer.Summary)
	}

	// The warning distinguishes an unreadable N from an absent cell
	// VALUE, and names the mode and the slot the caller must fix.
	var found bool
	for _, w := range warns {
		if w.Details["n_missing"] != true {
			continue
		}
		found = true
		if w.Code != string(pulseerrors.PULSE_OVERLAY_REF_ZERO) {
			t.Errorf("warning code = %q, want %q", w.Code, pulseerrors.PULSE_OVERLAY_REF_ZERO)
		}
		if !strings.Contains(w.Message, types.PanelNSourceCellNUnweighted) {
			t.Errorf("warning does not name the mode: %q", w.Message)
		}
		if w.Details["n_source"] != types.PanelNSourceCellNUnweighted {
			t.Errorf("Details[n_source] = %v", w.Details["n_source"])
		}
		if w.Details["panel_index"] != 1 {
			t.Errorf("Details[panel_index] = %v, want 1 (the first target)", w.Details["panel_index"])
		}
		if w.Details["slot_index"] != 7 {
			t.Errorf("Details[slot_index] = %v, want the authored Compose slot 7", w.Details["slot_index"])
		}
	}
	if !found {
		t.Fatalf("no n_missing warning; got %+v", warns)
	}

	// The DEFAULT mode reads the payload margin and is untouched by
	// the missing component, so it still emits at (0, 0).
	spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValue}
	legacy, _, err := applyPropZPanel(&spec, ref, targets, 3, []int{7, 9})
	if err != nil {
		t.Fatalf("legacy applyPropZPanel: %v", err)
	}
	if !legacy.Payload.Matrix.Cells[0][0].Present {
		t.Fatal("the default mode must still emit at (0,0); the counted skip is mode-scoped")
	}
}

// --- The components gate ---------------------------------------------

// A counted mode against a components-disabled slot refuses UP FRONT
// with PULSE_OVERLAY_COMPONENTS_REQUIRED — the same posture the MATRIX
// arm's HasComponents() gate takes — rather than skipping every
// coordinate and returning an empty layer the caller has to diagnose.
// The Details carry the ComposeComponentsState so the diagnostic says
// WHICH of the three ways the slot has no components.
func TestApplyPropZPanel_CountedModeRequiresComponents(t *testing.T) {
	for _, tc := range []struct {
		name      string
		breakSlot func(*types.Response)
		wantState string
	}{
		{
			name:      "components disabled",
			breakSlot: func(r *types.Response) { r.Components = nil },
			wantState: "components_disabled",
		},
		{
			name:      "components without a crosstab block",
			breakSlot: func(r *types.Response) { r.Components = &types.ResponseComponents{} },
			wantState: "slot_not_crosstab",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, targets := panelCountedSlots()
			tc.breakSlot(targets[1])
			spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
			spec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}

			_, _, err := applyPropZPanel(&spec, ref, targets, 3, []int{7, 9})
			if err == nil {
				t.Fatal("expected a refusal for a slot with no crosstab components")
			}
			var coded *pulseerrors.CodedError
			if !stderrors.As(err, &coded) {
				t.Fatalf("error is not a CodedError: %v", err)
			}
			if coded.Code != pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED {
				t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED)
			}
			if coded.Details["state"] != tc.wantState {
				t.Errorf("Details[state] = %v, want %q", coded.Details["state"], tc.wantState)
			}
			if coded.Details["panel_index"] != 2 {
				t.Errorf("Details[panel_index] = %v, want 2", coded.Details["panel_index"])
			}
			if coded.Details["slot_index"] != 9 {
				t.Errorf("Details[slot_index] = %v, want the authored Compose slot 9", coded.Details["slot_index"])
			}
			if coded.Details["n_source"] != types.PanelNSourceCellNUnweighted {
				t.Errorf("Details[n_source] = %v", coded.Details["n_source"])
			}

			// The SAME host under the default mode still succeeds. The
			// gate is scoped to the modes that read components, so a
			// panel that has never needed them cannot start refusing.
			spec.Params = map[string]any{"n_source": types.PanelNSourceRowMarginValue}
			if _, _, err := applyPropZPanel(&spec, ref, targets, 3, []int{7, 9}); err != nil {
				t.Fatalf("the default mode must not demand components: %v", err)
			}
		})
	}
}

// The components gate covers the REFERENCE slot too, not just targets.
// The reference is panel index 0 and its n leg is read the same way.
func TestApplyPropZPanel_CountedModeRequiresComponentsOnReference(t *testing.T) {
	ref, targets := panelCountedSlots()
	ref.Components = nil
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": types.PanelNSourceCellNUnweighted}

	_, _, err := applyPropZPanel(&spec, ref, targets, 3, []int{7, 9})
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != pulseerrors.PULSE_OVERLAY_COMPONENTS_REQUIRED {
		t.Fatalf("expected PULSE_OVERLAY_COMPONENTS_REQUIRED for the reference slot, got %v", err)
	}
	if coded.Details["panel_index"] != 0 {
		t.Errorf("Details[panel_index] = %v, want 0 (the reference)", coded.Details["panel_index"])
	}
	if coded.Details["slot_index"] != 3 {
		t.Errorf("Details[slot_index] = %v, want the authored Compose slot 3", coded.Details["slot_index"])
	}
}

// --- Unknown modes ----------------------------------------------------

// The runtime TWIN of the predict gate. pulse.Compose does not run
// predict, so without this an unknown n_source would fall through to
// the legacy leg and hand back the default number under a mode name
// the caller chose — the silent no-op this family refuses.
func TestApplyPropZPanel_UnknownNSourceRefusedAtRuntime(t *testing.T) {
	// A real mode — on the OTHER family. The most likely typo is not a
	// nonsense string, it is a crosstab mode name on a panel spec.
	const bogus = types.PairwiseNSourceRowMarginDistinct
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": bogus}

	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err == nil {
		t.Fatal("applyPropZPanel accepted an unknown n_source; predict alone does not stop pulse.Compose")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
	}
	if coded.Details["n_source"] != bogus {
		t.Errorf("Details[n_source] = %v, want %q", coded.Details["n_source"], bogus)
	}
	valid, ok := coded.Details["valid_n_sources"].([]string)
	if !ok || len(valid) != len(types.PanelNSources()) {
		t.Errorf("Details[valid_n_sources] = %#v, want the full enum", coded.Details["valid_n_sources"])
	}
	for _, v := range types.PanelNSources() {
		if !strings.Contains(coded.Message, v) {
			t.Errorf("message does not name valid mode %q: %q", v, coded.Message)
		}
	}
}

// A malformed params blob refuses at runtime under the same code the
// predict arm uses.
func TestApplyPropZPanel_MalformedParamsRefusedAtRuntime(t *testing.T) {
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": make(chan int)}

	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) || coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING for a malformed blob, got %v", err)
	}
}

// The cap keeps firing FIRST at runtime too, now against an unknown
// mode. Both arms must agree on which failure a doubly-wrong spec
// reports, or a caller fixing what predict told them to fix would hit
// a different error at execution.
func TestApplyPropZPanel_CapFiresBeforeUnknownNSource(t *testing.T) {
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, &types.OverlayOptions{MaxPanelTargets: 1})
	spec.Params = map[string]any{"n_source": "nonsense"}

	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP {
		t.Fatalf("code = %q, want the cap to short-circuit the params gate (%q)",
			coded.Code, pulseerrors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP)
	}
}

// itoaPanel is a dependency-free small-int formatter for subtest and
// assertion labels. strconv would do, but the file already imports the
// minimum it needs and this keeps the import set honest.
func itoaPanel(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// Runtime twin of the predict refusal for the RETIRED panel spelling.
// pulse.Compose does not run predict, so a predict-only refusal would
// let `row_margin_n` fall through to the legacy leg and hand back the
// payload row-margin VALUE under a name that, on the crosstab family,
// means a record COUNT. That is the exact silent substitution this
// effort exists to remove, so the runtime must refuse it too.
func TestApplyPropZPanel_RetiredRowMarginNSpellingRefusedAtRuntime(t *testing.T) {
	const retired = types.PairwiseNSourceRowMarginN
	if retired != "row_margin_n" {
		t.Fatalf("the pairwise wire value moved to %q; this test pins the panel against the OLD panel spelling", retired)
	}
	ref, targets := panelBaselineSlots()
	spec := composeSpecMultiTargetPropZPanel([]string{"t0", "t1"}, nil)
	spec.Params = map[string]any{"n_source": retired}

	_, _, err := applyPropZPanel(&spec, ref, targets, 0, []int{1, 2})
	if err == nil {
		t.Fatal("applyPropZPanel accepted the retired spelling; it must be unknown, not an alias for the legacy leg")
	}
	var coded *pulseerrors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Code != pulseerrors.PULSE_OVERLAY_PARAM_MISSING {
		t.Fatalf("code = %q, want %q", coded.Code, pulseerrors.PULSE_OVERLAY_PARAM_MISSING)
	}
	if coded.Details["n_source"] != retired {
		t.Errorf("Details[n_source] = %v, want %q", coded.Details["n_source"], retired)
	}
	if !strings.Contains(coded.Message, types.PanelNSourceRowMarginValue) {
		t.Errorf("the diagnostic must point at the replacement %q: %q",
			types.PanelNSourceRowMarginValue, coded.Message)
	}
}
