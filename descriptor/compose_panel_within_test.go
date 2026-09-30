package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time n_within_depth verdict for OVERLAY_PROP_Z_PANEL
// (E4-S1). The runtime twin lives in
// processing/overlay_prop_z_panel_within_test.go — pulse.Compose does
// not run predict, so a predict-only refusal stops nothing.
//
// Only the SHAPE of the depth is judged here. The range check against
// a slot's actual row-axis dim count needs materialised row keys,
// which a no-execute validator cannot see; the MATRIX arm draws the
// same line (descriptor checks `< 0`, buildPairwisePairs checks the
// range against the live host).

// n_within with a depth — and n_within with no depth — both predict
// clean. The accept side has to be asserted or the refusals below
// would pass against a validator that rejects the mode outright.
func TestValidateCompose_PanelNWithinDepthAccepted(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"omitted depth", map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin}},
		{"depth 0", map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 0}},
		{"depth 3", map[string]any{"n_source": types.PanelNSourceRowMarginValueWithin, "n_within_depth": 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := ValidateCompose(composePanelRequest(tc.params))
			if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
				t.Fatalf("refused at predict: %+v", env.Errors)
			}
		})
	}
}

// A depth set alongside a mode that does not read it is INERT, and an
// inert param the caller believes is applied is the silent no-op this
// family refuses. Predict can only make this call because
// PanelOverlayParams.NWithinDepth is a *int — with a plain int it
// could not tell "written 0" from "never written" and would refuse
// every params blob that names a non-within mode.
func TestValidateCompose_PanelNWithinDepthRefusedWithoutAWithinMode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"legacy default", map[string]any{"n_within_depth": 0}},
		{"row_margin_value", map[string]any{"n_source": types.PanelNSourceRowMarginValue, "n_within_depth": 1}},
		{"cell_n_unweighted", map[string]any{"n_source": types.PanelNSourceCellNUnweighted, "n_within_depth": 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := ValidateCompose(composePanelRequest(tc.params))
			if !envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
				t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
			}
			var found bool
			for _, e := range env.Errors {
				if e.Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
					continue
				}
				found = true
				// The diagnostic must name the mode that WOULD read
				// it, or the caller's only remaining move is to
				// delete the key they deliberately wrote.
				if !strings.Contains(e.Message, types.PanelNSourceRowMarginValueWithin) {
					t.Errorf("message does not name %q: %q", types.PanelNSourceRowMarginValueWithin, e.Message)
				}
				if e.Details["n_within_depth"] == nil {
					t.Errorf("Details omit n_within_depth: %+v", e.Details)
				}
			}
			if !found {
				t.Fatal("no PULSE_OVERLAY_PARAM_MISSING entry to inspect")
			}
			if result := env.Data.(*ComposeValidationResult); result.Valid {
				t.Error("expected Valid=false")
			}
		})
	}

	// The same modes WITHOUT the key stay clean. The refusal keys off
	// the pointer being set, never the mode alone.
	for _, mode := range []string{"", types.PanelNSourceRowMarginValue, types.PanelNSourceCellNUnweighted} {
		params := map[string]any{}
		if mode != "" {
			params["n_source"] = mode
		}
		env := ValidateCompose(composePanelRequest(params))
		if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
			t.Fatalf("mode %q without n_within_depth must predict clean: %+v", mode, env.Errors)
		}
	}
}

// A negative depth refuses under the same code. Mirrors the MATRIX
// arm's `params.NWithinDepth < 0` guard in validateOverlayPairwise, so
// one renderer branch handles both families.
func TestValidateCompose_PanelNegativeNWithinDepthRefused(t *testing.T) {
	env := ValidateCompose(composePanelRequest(map[string]any{
		"n_source":       types.PanelNSourceRowMarginValueWithin,
		"n_within_depth": -1,
	}))
	if !envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
	}
	var found bool
	for _, e := range env.Errors {
		if e.Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			continue
		}
		found = true
		if e.Details["n_within_depth"] != -1 {
			t.Errorf("Details[n_within_depth] = %v, want -1", e.Details["n_within_depth"])
		}
		if !strings.Contains(e.Message, ">= 0") {
			t.Errorf("message does not state the bound: %q", e.Message)
		}
	}
	if !found {
		t.Fatal("no PULSE_OVERLAY_PARAM_MISSING entry to inspect")
	}
}

// An unknown n_source still wins over a depth fault. The mode is what
// decides whether the depth is read at all, so reporting the depth
// first would tell the caller to fix a key whose verdict depends on
// the one they mistyped.
func TestValidateCompose_PanelUnknownNSourceBeatsDepthFault(t *testing.T) {
	env := ValidateCompose(composePanelRequest(map[string]any{
		"n_source":       "nonsense",
		"n_within_depth": -1,
	}))
	var msgs []string
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			msgs = append(msgs, e.Message)
		}
	}
	if len(msgs) == 0 {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
	}
	for _, m := range msgs {
		if strings.Contains(m, "n_within_depth") {
			t.Errorf("depth fault reported alongside the unknown mode: %q", m)
		}
	}
	if !strings.Contains(strings.Join(msgs, " "), "nonsense") {
		t.Errorf("the unknown n_source is not named: %v", msgs)
	}
}
