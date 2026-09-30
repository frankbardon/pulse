package descriptor

import (
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Predict-time n_source verdict for OVERLAY_PROP_Z_PANEL. The runtime
// twin lives in processing/overlay_prop_z_panel_nsource_test.go —
// pulse.Compose does not run predict, so a predict-only refusal would
// let the legacy number escape under a mode name the caller chose.

// Every mode the enum accepts predicts clean, INCLUDING the explicit
// spelling of the default. A caller who writes their existing
// behaviour down must not be told it is wrong.
func TestValidateCompose_PanelKnownNSourcesAccepted(t *testing.T) {
	cases := append([]string{""}, types.PanelNSources()...)
	for _, mode := range cases {
		name := mode
		if name == "" {
			name = "(absent)"
		}
		t.Run(name, func(t *testing.T) {
			params := map[string]any{}
			if mode != "" {
				params["n_source"] = mode
			}
			env := ValidateCompose(composePanelRequest(params))
			if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
				t.Fatalf("mode %q refused at predict: %+v", mode, env.Errors)
			}
		})
	}
}

// An unrecognised n_source is refused, and the diagnostic names BOTH
// the offending value and the valid set. Naming only the failure would
// leave the caller guessing at a two-entry enum they cannot see from
// the payload schema (operator params are an open object).
func TestValidateCompose_PanelUnknownNSourceRefused(t *testing.T) {
	const bogus = "row_margin_distinct" // a real mode — on the OTHER family
	env := ValidateCompose(composePanelRequest(map[string]any{"n_source": bogus}))
	if !envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("expected PULSE_OVERLAY_PARAM_MISSING, got %+v", env.Errors)
	}

	var found bool
	for _, e := range env.Errors {
		if e.Code != string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			continue
		}
		found = true
		if !strings.Contains(e.Message, bogus) {
			t.Errorf("message does not name the offending value: %q", e.Message)
		}
		for _, valid := range types.PanelNSources() {
			if !strings.Contains(e.Message, valid) {
				t.Errorf("message does not name valid mode %q: %q", valid, e.Message)
			}
		}
		if e.Details["n_source"] != bogus {
			t.Errorf("Details[n_source] = %v, want %q", e.Details["n_source"], bogus)
		}
		got, ok := e.Details["valid_n_sources"].([]string)
		if !ok {
			t.Fatalf("Details[valid_n_sources] = %#v, want []string", e.Details["valid_n_sources"])
		}
		if len(got) != len(types.PanelNSources()) {
			t.Errorf("Details[valid_n_sources] = %v, want the full enum", got)
		}
		if e.Details["kind"] != string(types.OverlayKindPropZPanel) {
			t.Errorf("Details[kind] = %v, want %s", e.Details["kind"], types.OverlayKindPropZPanel)
		}
		if e.Details["index"] != 0 {
			t.Errorf("Details[index] = %v, want 0", e.Details["index"])
		}
	}
	if !found {
		t.Fatal("no PULSE_OVERLAY_PARAM_MISSING entry to inspect")
	}
	if result := env.Data.(*ComposeValidationResult); result.Valid {
		t.Error("expected Valid=false for an unknown n_source")
	}
}

// The cap keeps firing FIRST, now against an UNKNOWN MODE rather than
// a malformed blob. E3-S1 pinned the ordering for the decode failure;
// the n_source check is a second gate inside the same helper and must
// sit behind the cap too, or an over-cap spec would send the caller to
// the params when the structural failure is the cap.
func TestValidateCompose_PanelOverCapFiresBeforeUnknownNSource(t *testing.T) {
	requests := []*types.Request{matrixCrosstabRequest("ref", "row", "col")}
	targets := make([]string, 0, 4)
	for i := 1; i <= 4; i++ {
		label := "t" + composeDescriptorItoa(i)
		requests = append(requests, matrixCrosstabRequest(label, "row", "col"))
		targets = append(targets, label)
	}
	req := &types.ComposedRequest{
		Requests: requests,
		Overlays: []types.ComposeOverlaySpec{
			{
				Name:      "panel",
				Kind:      types.OverlayKindPropZPanel,
				Scope:     types.OverlayScopeCell,
				Reference: "ref",
				Targets:   targets,
				Options:   &types.OverlayOptions{MaxPanelTargets: 3},
				Params:    map[string]any{"n_source": "nonsense"},
			},
		},
	}
	env := ValidateCompose(req)
	if !envHasCode(env, errors.PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP) {
		t.Fatalf("expected PULSE_OVERLAY_PANEL_TARGETS_OVER_CAP, got %+v", env.Errors)
	}
	if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("the cap gate must short-circuit the n_source gate, got %+v", env.Errors)
	}
}

// The n_source gate is scoped to the kind that owns the params shape.
// OVERLAY_PANEL_INDEX_VS_REF shares the cap and the multi-reference
// resolution but is descriptive — it has no sample-size leg, so an
// n_source on it is an unknown KEY (accepted) and not an unknown mode.
func TestValidateCompose_PanelNSourceGateScopedToPropZPanel(t *testing.T) {
	req := composePanelRequest(map[string]any{"n_source": "nonsense"})
	req.Overlays[0].Kind = types.OverlayKindPanelIndexVsRef
	env := ValidateCompose(req)
	if envHasCode(env, errors.PULSE_OVERLAY_PARAM_MISSING) {
		t.Fatalf("OVERLAY_PANEL_INDEX_VS_REF must not inherit the panel n_source gate, got %+v", env.Errors)
	}
}

// A malformed blob reports the DECODE failure, not an unknown mode: at
// that point no mode was read at all, and "unknown n_source: " with an
// empty value would name a field the caller may never have written.
func TestValidateCompose_PanelMalformedBeatsUnknownMode(t *testing.T) {
	env := ValidateCompose(composePanelRequest(map[string]any{"n_source": make(chan int)}))
	var msgs []string
	for _, e := range env.Errors {
		if e.Code == string(errors.PULSE_OVERLAY_PARAM_MISSING) {
			msgs = append(msgs, e.Message)
		}
	}
	if len(msgs) != 1 {
		t.Fatalf("expected exactly one params error, got %v", msgs)
	}
	if !strings.Contains(msgs[0], "malformed Params") {
		t.Errorf("expected the decode diagnostic, got %q", msgs[0])
	}
	if strings.Contains(msgs[0], "unknown n_source") {
		t.Errorf("a decode failure must not be reported as an unknown mode: %q", msgs[0])
	}
}
