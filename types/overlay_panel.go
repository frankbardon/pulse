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
// Every field is optional, and the zero value means "exactly what the
// panel did before the field existed" — that is what keeps `params`
// absent and `params: {}` byte-identical to the pre-params baseline.
//
// Deliberately a SEPARATE type from PairwiseOverlayParams even though
// the two families share mode NAMES. The hosts differ (MATRIX axis
// indexes vs Compose slots), so the accepted mode SET differs, and one
// shared struct would silently offer each host the other's modes.
// Shared vocabulary belongs in shared constants, not a shared struct.
//
// Scope note: this is the OVERLAY_PROP_Z_PANEL params shape only.
// OVERLAY_PANEL_INDEX_VS_REF is the other multi-reference COMPOSE kind
// and shares the MaxPanelTargets cap, but it is descriptive rather than
// inferential and has no sample-size leg, so it is NOT covered here.
type PanelOverlayParams struct {
	// NSource selects where each SLOT's sample-size leg is read. One
	// of the PanelNSource* constants. Empty = row_margin_n, which is
	// the leg the panel has always used.
	NSource string `json:"n_source,omitempty"`
}

// Panel sample-size source modes (PanelOverlayParams.NSource).
//
// NAMING. "row" here means each SLOT's own row axis. The panel pairs
// across SLOTS, so — unlike the OVERLAY_PAIRWISE_* family, where the
// pair axis IS rows or columns depending on Scope — no panel mode name
// ever refers to a pair axis. Every mode is read once per slot, at the
// coordinate being tested, and the pairing happens afterwards.
const (
	// PanelNSourceRowMarginN is the legacy default (empty means this):
	// the slot's per-row margin, read off the MatrixPayload's
	// RowMargins by row key.
	//
	// Same slot as the pairwise family's row_margin_n — the row margin
	// read as the sample size — but a different CARRIER, and that
	// difference is load-bearing. The MATRIX host reads
	// CrosstabComponents.RowMarginCounts, a record count. The COMPOSE
	// host reads the margin CELL's value off the payload, because the
	// panel predates any components channel on this host. The two
	// coincide when the cell aggregator counts records (AGG_COUNT) and
	// diverge when it does not.
	//
	// It is the ONLY mode carrying the historical `<= 0` fall back to
	// the cell value; see PanelNSourceFallsBackToCellValue.
	PanelNSourceRowMarginN = "row_margin_n"

	// PanelNSourceCellNUnweighted is the COUNTED per-cell leg: the
	// universal-floor "n" the slot's cell aggregator emitted at this
	// coordinate, read out of
	// CrosstabComponents.CellComponents[r][c]["n"] through the
	// per-slot components view. Same meaning as the pairwise family's
	// cell_n_unweighted.
	//
	// Requires components on EVERY slot
	// (PULSE_OVERLAY_COMPONENTS_REQUIRED) and has NO cell-value
	// fallback: an unreadable leg skips the coordinate.
	PanelNSourceCellNUnweighted = "cell_n_unweighted"
)

// panelNSources is the ordered valid set, empty excluded. Diagnostics
// name it so a caller who mistypes a mode sees what was available.
var panelNSources = []string{
	PanelNSourceRowMarginN,
	PanelNSourceCellNUnweighted,
}

// PanelNSources returns the valid PanelOverlayParams.NSource modes in
// declaration order. The empty string is omitted: it is not a mode, it
// is the absence of one, and listing it as a suggestion would tell a
// caller to delete the key when they meant to fix it. Returns a copy —
// callers must not be able to mutate the enum.
func PanelNSources() []string {
	out := make([]string, len(panelNSources))
	copy(out, panelNSources)
	return out
}

// ValidPanelNSource reports whether s names a supported panel NSource
// mode. Empty counts as valid (defaults to row_margin_n).
func ValidPanelNSource(s string) bool {
	switch s {
	case "", PanelNSourceRowMarginN, PanelNSourceCellNUnweighted:
		return true
	}
	return false
}

// PanelNSourceReadsComponents reports whether s reads
// Response.Components.Crosstab on each slot rather than the
// MatrixPayload. The handler's up-front per-slot components gate keys
// off this predicate, so the gate is NOT applied to the legacy
// payload-read default — a panel that has never needed components must
// not start refusing when a caller spells its existing behaviour out.
func PanelNSourceReadsComponents(s string) bool {
	return s == PanelNSourceCellNUnweighted
}

// PanelNSourceFallsBackToCellValue reports whether s substitutes the
// CELL VALUE for a non-positive sample size.
//
// True for the legacy default ONLY, and deliberately not extended to
// any mode added after it. The substitution is a degenerate-input
// crutch from before the panel could count anything: a cell value
// standing in for a sample size is the exact class of silent
// substitution this family now refuses. It survives on row_margin_n
// because removing it would change the default path's output, and the
// default must stay byte-identical to the pre-params baseline.
//
// Every COUNTED mode instead answers ok=false and SKIPS the
// coordinate with a warning — the same posture the MATRIX arm's
// RowMarginDistinctN takes ("an unemitted margin is not a zero-sized
// one"). A genuine counted zero is still a zero, and the prop-Z kernel
// reports it as a degenerate pair.
func PanelNSourceFallsBackToCellValue(s string) bool {
	return s == "" || s == PanelNSourceRowMarginN
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
