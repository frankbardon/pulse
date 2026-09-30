package types

import (
	"encoding/json"
	"strings"
	"testing"
)

// The decoder contract for PanelOverlayParams mirrors
// DecodePairwiseParams exactly: absent / empty / null / `{}` are the
// SAME zero value, unknown keys are tolerated, and anything that is not
// a JSON object is an error rather than a silent zero value.
//
// The zero-value assertions read as vacuous while the struct has no
// fields, and they are here for exactly that reason: they are the
// regression harness E3-S3's NSource and E4-S1's NWithinDepth land
// into. A field whose zero value stops meaning "pre-params behaviour"
// breaks these first.

func TestDecodePanelParams_AbsentFormsAreZeroValue(t *testing.T) {
	var zero PanelOverlayParams
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"nil", nil},
		{"empty", json.RawMessage{}},
		{"empty object", json.RawMessage(`{}`)},
		{"json null", json.RawMessage(`null`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodePanelParams(tc.raw)
			if err != nil {
				t.Fatalf("DecodePanelParams(%s): unexpected error %v", tc.raw, err)
			}
			if got != zero {
				t.Fatalf("DecodePanelParams(%s) = %+v, want the zero value %+v", tc.raw, got, zero)
			}
		})
	}
}

// Unknown keys must NOT be an error. `params` is an open object in
// descriptor.BuildPayloadSchema(), so a blob authored against a newer
// binary has to decode cleanly against an older one.
func TestDecodePanelParams_UnknownKeysAccepted(t *testing.T) {
	var zero PanelOverlayParams
	got, err := DecodePanelParams(json.RawMessage(`{"not_a_panel_param":42,"nested":{"a":[1,2]}}`))
	if err != nil {
		t.Fatalf("unknown keys should decode cleanly, got %v", err)
	}
	if got != zero {
		t.Fatalf("got %+v, want the zero value %+v", got, zero)
	}
}

// Malformed or non-object params are refused. The error is wrapped with
// a stable prefix so a predict envelope message names the surface.
func TestDecodePanelParams_MalformedRefused(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
	}{
		{"truncated object", json.RawMessage(`{`)},
		{"not json", json.RawMessage(`n_source=n_within`)},
		{"array", json.RawMessage(`[1,2]`)},
		{"bare number", json.RawMessage(`5`)},
		{"bare string", json.RawMessage(`"row_margin_value_within"`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := DecodePanelParams(tc.raw)
			if err == nil {
				t.Fatalf("DecodePanelParams(%s) = nil error, want a decode failure", tc.raw)
			}
			if !strings.Contains(err.Error(), "decode panel overlay params") {
				t.Fatalf("error %q does not name the panel params surface", err)
			}
		})
	}
}

// The COMPOSE arm carries map[string]any rather than json.RawMessage.
// Nil / empty must be the same zero value the raw arm produces.
func TestDecodePanelParamsMap_AbsentFormsAreZeroValue(t *testing.T) {
	var zero PanelOverlayParams
	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"nil map", nil},
		{"empty map", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodePanelParamsMap(tc.params)
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if got != zero {
				t.Fatalf("got %+v, want the zero value %+v", got, zero)
			}
		})
	}
}

func TestDecodePanelParamsMap_UnknownKeysAccepted(t *testing.T) {
	var zero PanelOverlayParams
	got, err := DecodePanelParamsMap(map[string]any{"not_a_panel_param": 42})
	if err != nil {
		t.Fatalf("unknown keys should decode cleanly, got %v", err)
	}
	if got != zero {
		t.Fatalf("got %+v, want the zero value %+v", got, zero)
	}
}

// A map value the JSON encoder cannot represent is the one malformed
// shape reachable through the COMPOSE carrier: the map already survived
// a JSON decode (or came from a Go literal), so syntax is settled, but
// an unrepresentable value must not pass silently.
func TestDecodePanelParamsMap_UnrepresentableValueRefused(t *testing.T) {
	_, err := DecodePanelParamsMap(map[string]any{"n_source": make(chan int)})
	if err == nil {
		t.Fatal("expected a decode failure for an unrepresentable params value, got nil")
	}
	if !strings.Contains(err.Error(), "decode panel overlay params") {
		t.Fatalf("error %q does not name the panel params surface", err)
	}
}

// The map arm must funnel through the raw arm — two decoders would
// drift the moment the struct gains a field. Probed by feeding both
// arms the same payload and requiring the same answer.
func TestDecodePanelParams_MapAndRawArmsAgree(t *testing.T) {
	params := map[string]any{"not_a_panel_param": "x"}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	fromRaw, errRaw := DecodePanelParams(raw)
	fromMap, errMap := DecodePanelParamsMap(params)
	if (errRaw == nil) != (errMap == nil) {
		t.Fatalf("arms disagree on error: raw=%v map=%v", errRaw, errMap)
	}
	if fromRaw != fromMap {
		t.Fatalf("arms disagree on value: raw=%+v map=%+v", fromRaw, fromMap)
	}
}

// IsPanelOverlayParamsKind is scoped to OVERLAY_PROP_Z_PANEL. The other
// multi-reference COMPOSE kind (OVERLAY_PANEL_INDEX_VS_REF) shares the
// MaxPanelTargets cap but is descriptive, has no sample-size leg, and
// must not silently inherit the inferential params shape.
func TestIsPanelOverlayParamsKind(t *testing.T) {
	if !IsPanelOverlayParamsKind(OverlayKindPropZPanel) {
		t.Error("OVERLAY_PROP_Z_PANEL should carry the panel params shape")
	}
	for _, kind := range []OverlayKind{
		OverlayKindPanelIndexVsRef,
		OverlayKindPairwisePropZ,
		OverlayKindPropZCell,
		OverlayKindRank,
	} {
		if IsPanelOverlayParamsKind(kind) {
			t.Errorf("%s should not carry the panel params shape", kind)
		}
	}
}
