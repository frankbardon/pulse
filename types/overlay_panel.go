package types

import (
	"encoding/json"
	"fmt"
)

// PanelOverlayParams is the decoded params shape for the COMPOSE-host
// OVERLAY_PROP_Z_PANEL overlay — the multi-reference sibling of the
// OVERLAY_PAIRWISE_* family that pairs across Compose SLOTS (reference
// plus every target) rather than across two indexes of one host axis.
//
// The struct is deliberately EMPTY today. The panel shipped with no
// per-kind configuration at all: the only knob it reads is
// ComposeOverlaySpec.Options.MaxPanelTargets, which rides Options and
// NOT params (a cap on combinatorial fan-out is an execution knob, not
// a statistical one), and the panel index universe comes from
// Reference + Targets. An empty params type is therefore the honest
// zero state, not a placeholder: the decoder, the predict-time gate and
// the byte-identity guarantee all exist and are testable before the
// first field lands.
//
// Every field added here will be optional, and the zero value must
// always mean "exactly what the panel did before the field existed" —
// that is what keeps `params` absent and `params: {}` byte-identical to
// the pre-params baseline.
//
// Deliberately a SEPARATE type from PairwiseOverlayParams even though
// the two families will converge on shared mode names. The hosts
// differ (MATRIX axis indexes vs Compose slots), so the accepted mode
// SET differs, and one shared struct would silently offer each host the
// other's modes. Shared vocabulary belongs in shared constants, not a
// shared struct.
//
// Scope note: this is the OVERLAY_PROP_Z_PANEL params shape only.
// OVERLAY_PANEL_INDEX_VS_REF is the other multi-reference COMPOSE kind
// and shares the MaxPanelTargets cap, but it is descriptive rather than
// inferential and has no sample-size leg, so it is NOT covered here.
type PanelOverlayParams struct {
	// Intentionally no fields yet. See the type doc.
}

// IsPanelOverlayParamsKind reports whether kind is an overlay kind
// whose Params slot decodes into PanelOverlayParams. Both the predict
// arms key off this predicate so a second panel kind adopting the
// shape does not need every call site rewritten.
func IsPanelOverlayParamsKind(kind OverlayKind) bool {
	return kind == OverlayKindPropZPanel
}

// DecodePanelParams decodes a raw OverlaySpec.Params blob into a
// PanelOverlayParams. A nil / empty blob yields the zero value (all
// defaults). Malformed JSON — or JSON that is not an object — returns
// an error so callers surface a clean predict-time diagnostic rather
// than silently ignoring configuration the caller believed was applied.
//
// Unknown keys are ACCEPTED, mirroring DecodePairwiseParams: params is
// an open object in descriptor.BuildPayloadSchema() and a strict decode
// here would make every forward-compatible authoring blob a hard
// failure against an older binary.
func DecodePanelParams(raw json.RawMessage) (PanelOverlayParams, error) {
	var p PanelOverlayParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("decode panel overlay params: %w", err)
	}
	return p, nil
}

// DecodePanelParamsMap is DecodePanelParams for the COMPOSE arm.
// ComposeOverlaySpec.Params is a map[string]any rather than the
// json.RawMessage the per-Request OverlaySpec carries, so the map is
// re-marshalled and funnelled through the SAME decoder — two
// hand-written decoders would drift the moment the struct gains a
// field.
//
// A nil / empty map yields the zero value. The re-marshal is also the
// only place a Compose-authored params slot can be rejected: the map
// arrived through a JSON decode (or a Go literal), so syntax is no
// longer in question, but a value the encoder cannot represent, or a
// value whose Go type does not fit the struct field, still must not
// pass silently.
func DecodePanelParamsMap(params map[string]any) (PanelOverlayParams, error) {
	var p PanelOverlayParams
	if len(params) == 0 {
		return p, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return p, fmt.Errorf("decode panel overlay params: %w", err)
	}
	return DecodePanelParams(raw)
}
