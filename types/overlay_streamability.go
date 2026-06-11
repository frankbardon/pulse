package types

// Overlay streamability table — source of truth for whether each
// OverlayKind can run inside the streaming Process path or forces the
// orchestrator down a buffered route. Mirrors the per-aggregator,
// per-attribute, per-test tables in streamability.go.
//
// Today every registered overlay rides on the buffered crosstab
// orchestrator (margins are recomputed from raw rows, see CLAUDE.md
// "Execution modes" → Crosstab and skills/crosstab-guide.md), so every
// row is false. Later epics that add streamable overlay families (e.g.
// a row-share overlay that can be folded into the streaming fused
// crosstab pass) flip the matching row to true and add the gate-side
// plumbing alongside.
//
// Adding a new OverlayKind:
//
//  1. Declare the constant + AllOverlayKinds() entry in overlay.go.
//  2. Add a row to overlayStreamability below.
//  3. TestStreamability_OverlaysKnown enforces table completeness — a
//     missing row fails the gate.
//
// The default branch of OverlayStreamable returns (false, false) so an
// unknown kind cannot accidentally stream — the validator (E1-S2) and
// the predict layer both treat "unknown" as "buffered".

// OverlayStreamability declares whether each overlay kind can be
// computed inside the streaming Process path. Keyed by the on-wire
// SCREAMING_SNAKE OverlayKind value.
//
// Per kind-catalog-v1.md "Streaming-capable subset", INDEX_VS_MARGIN
// is NOT in the streamable set today — its host crosstab is always
// buffered.
var OverlayStreamability = map[OverlayKind]bool{
	// OVERLAY_CHISQ_COL is inherently buffered — mechanical column-axis
	// twin of CHISQ_ROW. Each per-column goodness-of-fit test consumes
	// the column's observed row distribution AND every row margin (for
	// the expected-count recurrence), all of which the buffered host
	// crosstab orchestrator already materialised. Inferential overlays
	// as a family stay buffered until a streamable-test path is plumbed.
	OverlayKindChiSqCol: false,
	// OVERLAY_CHISQ_MATRIX is inherently buffered — the χ² independence
	// test consumes the whole-matrix observed × expected contingency
	// table, every margin recurrence is buffered on the host crosstab
	// path, and the runtime handler walks the materialised matrix
	// directly. Inferential overlays as a family stay buffered until a
	// future streamable-test path is plumbed.
	OverlayKindChiSqMatrix: false,
	// OVERLAY_CHISQ_ROW is inherently buffered — each per-row goodness-
	// of-fit test consumes the row's observed column distribution AND
	// every column margin (for the expected-count recurrence), all of
	// which the buffered host crosstab orchestrator already materialised.
	// Inferential overlays as a family stay buffered until a streamable-
	// test path is plumbed.
	OverlayKindChiSqRow:      false,
	OverlayKindDeltaVsMargin: false,
	// OVERLAY_FISHER_EXACT_CELL is inherently buffered — the per-cell 2×2
	// Fisher's exact test reads each cell value AND its row + column
	// margins to build the 2×2 contingency, all of which the buffered
	// host crosstab orchestrator already materialised. Inferential
	// overlays as a family stay buffered until a streamable-test path
	// is plumbed (PRD § 4.C FR-C2 — the canonical low-count contingency
	// overlay closing the E2 inferential family).
	OverlayKindFisherExactCell: false,
	OverlayKindIndexVsMargin:   false,
	OverlayKindShareOfCol:     false,
	OverlayKindShareOfRow:     false,
	OverlayKindShareOfTotal:   false,
	OverlayKindZScoreVsMargin: false,
}

// OverlayStreamable reports whether the given overlay kind streams and
// whether it is a known entry in OverlayStreamability. An unknown kind
// returns (false, false) — callers that want to treat "unknown" as
// "buffered" can ignore the known bit; callers wiring the validator or
// the manifest capability block can branch on `known` to surface a
// PULSE_OVERLAY_UNKNOWN-style diagnostic.
//
// Source of truth for predict.Streamable on overlay-bearing requests;
// cross-checked at test time by TestStreamability_OverlaysKnown which
// enumerates AllOverlayKinds() and demands every kind has a row here.
func OverlayStreamable(kind OverlayKind) (streamable, known bool) {
	streamable, known = OverlayStreamability[kind]
	return streamable, known
}
