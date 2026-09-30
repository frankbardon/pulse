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
// A mode name is shared ONLY where the quantity is the same:
// cell_n_unweighted means a counted n on both hosts, while the panel's
// margin leg is spelled row_margin_value precisely because it is not
// the record count the pairwise family's row_margin_n reads.
//
// Scope note: this is the OVERLAY_PROP_Z_PANEL params shape only.
// OVERLAY_PANEL_INDEX_VS_REF is the other multi-reference COMPOSE kind
// and shares the MaxPanelTargets cap, but it is descriptive rather than
// inferential and has no sample-size leg, so it is NOT covered here.
type PanelOverlayParams struct {
	// NSource selects where each SLOT's sample-size leg is read. One
	// of the PanelNSource* constants. Empty = row_margin_value, which
	// is the leg the panel has always used.
	NSource string `json:"n_source,omitempty"`

	// NWithinDepth scopes a WITHIN-PREFIX leg
	// (PanelNSourceUsesWithinDepth — row_margin_value_within and
	// row_margin_distinct_within) to a
	// ROW-KEY PREFIX on each slot's OWN row axis. The panel pairs across SLOTS, which carry
	// no dim tuple, so — unlike the OVERLAY_PAIRWISE_* family, where
	// the depth indexes the pair axis — there is no pair axis to
	// index here.
	//
	// nil (omitted) means the EXACT per-slot row margin, with no
	// summing at all. A non-nil d sums that slot's row margins over
	// every row whose key agrees with the coordinate's row on the
	// first d+1 dim positions.
	//
	// A POINTER, not an int, and that is the whole point: depth 0 is
	// a meaningful value — the coarsest real prefix, one dim fixed —
	// so an int zero value cannot also carry "absent". Reading 0 as
	// "omitted" would silently give a caller who asked for the
	// first-dim slab the unsummed row margin instead, which is a
	// smaller number that looks entirely plausible.
	// types.PairwiseOverlayParams.NWithinDepth is a plain int and has
	// exactly this defect; it is not fixed here (its wire value
	// shipped) and must not be reproduced.
	//
	// Accepted ONLY with an n_source that consumes it
	// (PanelNSourceUsesWithinDepth). Set alongside any other mode it
	// would be inert, and an inert param the caller believes is
	// applied is the silent no-op this family refuses — so both arms
	// raise PULSE_OVERLAY_PARAM_MISSING instead. Negative values are
	// refused the same way.
	NWithinDepth *int `json:"n_within_depth,omitempty"`
}

// Panel sample-size source modes (PanelOverlayParams.NSource).
//
// NAMING. "row" here means each SLOT's own row axis. The panel pairs
// across SLOTS, so — unlike the OVERLAY_PAIRWISE_* family, where the
// pair axis IS rows or columns depending on Scope — no panel mode name
// ever refers to a pair axis. Every mode is read once per slot, at the
// coordinate being tested, and the pairing happens afterwards.
const (
	// PanelNSourceRowMarginValue is the legacy default (empty means
	// this): the slot's per-row margin VALUE, read off the
	// MatrixPayload's RowMargins by row key.
	//
	// The name says `value`, not `n`, deliberately. It occupies the
	// same semantic slot as the pairwise family's `row_margin_n` — the
	// row margin read as the sample size — but a different CARRIER,
	// and that difference is load-bearing. The MATRIX host reads
	// CrosstabComponents.RowMarginCounts, a record COUNT. The COMPOSE
	// host reads the margin CELL's value off the payload, because the
	// panel predates any components channel on this host; under a
	// percentage normalization that value is not a count at all. The
	// two coincide only when the cell aggregator counts records
	// (AGG_COUNT) and diverge when it does not.
	//
	// The divergence is real either way — distinct spellings do not
	// remove it, they stop DISGUISING it. One wire name meaning two
	// quantities is the hazard this family exists to refuse, so
	// `row_margin_n` is NOT a panel mode: it is an unknown value here
	// and is refused like any other.
	//
	// It is the ONLY mode carrying the historical `<= 0` fall back to
	// the cell value; see PanelNSourceFallsBackToCellValue.
	PanelNSourceRowMarginValue = "row_margin_value"

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

	// PanelNSourceRowMarginValueWithin is the WITHIN-PREFIX margin
	// leg: the slot's row-margin VALUE, optionally summed over a
	// row-key prefix slab.
	//
	// CARRIER, and it is in the name. It reads exactly where
	// PanelNSourceRowMarginValue reads — the margin CELL's value off
	// MatrixPayload.RowMargins — so with NWithinDepth omitted it IS
	// the legacy leg's number and the two agree byte-for-byte
	// wherever that margin is present. The `_within` suffix denotes
	// the SCOPE (a within-prefix denominator, the same Pulse-W
	// `normalize_within` notion CrosstabHostView.RowSlabN serves on
	// the MATRIX arm).
	//
	// It was spelled `n_within` on this branch and is NOT any more.
	// The axis-pairing family's PairwiseNSourceNWithin owns that
	// string and means something else: it sums
	// CrosstabComponents.CellCounts — record COUNTS — over a slab of
	// the PAIR axis at ONE fixed opposite index. This one sums a
	// slot's ROW margins, i.e. ALL columns. The two therefore differ
	// by roughly the column count, silently, which is precisely the
	// one-name-two-quantities hazard `row_margin_value` was split out
	// of `row_margin_n` to remove one story earlier. `n_within` is an
	// UNKNOWN mode on the panel and must never be re-admitted as an
	// alias: accepting both spellings would restore the ambiguity the
	// rename exists to destroy. The PARAM keeps its name —
	// `n_within_depth` scopes a within-prefix leg on both families
	// and denotes the same thing on each.
	//
	// E4-S3's distinct-key sibling is spelled
	// `row_margin_distinct_within` for the same reason;
	// `n_within_distinct` stays the crosstab family's spelling.
	//
	// It does NOT carry the legacy <= 0 cell-value fallback; see
	// PanelNSourceFallsBackToCellValue. A row margin that was never
	// emitted skips the coordinate with a warning rather than
	// substituting the cell value, and — once a depth is set — a
	// single coordinate's cell value is not even the same dimension
	// as a slab-wide sample size.
	//
	// With an explicit NWithinDepth it SUMS across rows, which is
	// gated by CheckPanelSlabPartitionWith.
	PanelNSourceRowMarginValueWithin = "row_margin_value_within"

	// PanelNSourceRowMarginDistinctWithin is the DISTINCT-KEY sibling
	// of PanelNSourceRowMarginValueWithin: the slot's row margin read
	// as the cell aggregator's distinct-KEY cardinality, optionally
	// summed over a row-key prefix slab.
	//
	// CARRIER, again, and again it is in the name. The `_value_`
	// sibling reads MatrixPayload.RowMargins — a margin CELL's VALUE,
	// whatever the slot's aggregator emitted. This one reads
	// CrosstabComponents.RowMarginComponents through the per-slot
	// components view. A different CARRIER is not a variant of the
	// same leg, which is why it is `row_margin_distinct_within` and
	// not `row_margin_value_distinct_within`.
	//
	// It is deliberately NOT spelled `n_within_distinct`. That string
	// belongs to the axis-pairing family
	// (PairwiseNSourceNWithinDistinct), where it sums per-CELL
	// distinct cardinalities over a slab of the PAIR axis at ONE
	// fixed opposite index. This one reads a slot's ROW margins, i.e.
	// ALL columns — the two differ by roughly the column count,
	// silently, the same one-name-two-quantities hazard the
	// `row_margin_value_within` rename destroyed one story earlier.
	// `n_within_distinct` is an UNKNOWN mode on the panel and must
	// never be re-admitted as an alias.
	//
	// ADMISSION. The figure exists only for a cell aggregator that
	// counts distinct KEYS, and only two do: AGG_DISTINCT_SUM (on
	// component key `distinct_count`) and AGG_DISTINCT_COUNT (on
	// `cardinality`). AGG_FREQUENCY and AGG_MODE also emit a key
	// literally spelled `distinct_count`, but theirs counts distinct
	// VALUES of the measure field — answer codes, not respondents —
	// so admission is by EXACT aggregator-identity signature, never by
	// key presence. Every slot is judged UP FRONT and one unadmitted
	// slot refuses the WHOLE spec; see
	// processing.applyPropZPanel.
	//
	// Requires components on every slot, exactly as
	// PanelNSourceCellNUnweighted does, and carries no cell-value
	// fallback: an unemitted margin skips the coordinate.
	//
	// With an explicit NWithinDepth it SUMS across rows, so it is
	// gated by CheckPanelSlabPartitionWith on the same terms as its
	// `_value_` sibling. It inherits that gate by joining
	// PanelNSourceUsesWithinDepth — there is deliberately no narrower
	// panel-side "sums distinct cells" predicate, because the panel
	// cannot observe additivity for any margin it reads.
	PanelNSourceRowMarginDistinctWithin = "row_margin_distinct_within"
)

// panelNSources is the ordered valid set, empty excluded. Diagnostics
// name it so a caller who mistypes a mode sees what was available.
var panelNSources = []string{
	PanelNSourceRowMarginValue,
	PanelNSourceCellNUnweighted,
	PanelNSourceRowMarginValueWithin,
	PanelNSourceRowMarginDistinctWithin,
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
// mode. Empty counts as valid (defaults to row_margin_value).
func ValidPanelNSource(s string) bool {
	switch s {
	case "", PanelNSourceRowMarginValue, PanelNSourceCellNUnweighted,
		PanelNSourceRowMarginValueWithin, PanelNSourceRowMarginDistinctWithin:
		return true
	}
	return false
}

// PanelNSourceUsesWithinDepth reports whether s CONSUMES
// PanelOverlayParams.NWithinDepth. It is also the SUMMING predicate:
// every panel mode that reads a depth accumulates across rows when
// one is set, so CheckPanelSlabPartitionWith keys its gate off this
// same predicate rather than a second, narrower one. (The
// axis-pairing family DOES need the narrower
// PairwiseNSourceSumsDistinctCells, because its n_within sums record
// COUNTS, which are additive under a fan-out grouper. A panel margin
// is whatever the slot's cell aggregator emitted — a distinct count,
// a percentage, a weighted sum — so the panel cannot claim
// additivity for it and does not try.) Both arms key their
// "n_within_depth without a mode that reads it" refusal off this
// predicate, so the accepted combination is stated once.
//
// Why refuse at all rather than ignore the field: an ignored depth is
// a silent no-op, and the caller reading a denominator they never got
// cannot tell. The panel can afford the refusal precisely because
// NWithinDepth is a POINTER here — "set" is distinguishable from
// "zero", so the check cannot misfire on a caller who never wrote the
// key. (The axis-pairing family's int field cannot make that
// distinction, which is why its equivalent no-op is still open.)
func PanelNSourceUsesWithinDepth(s string) bool {
	return s == PanelNSourceRowMarginValueWithin ||
		s == PanelNSourceRowMarginDistinctWithin
}

// panelNSourcesUsingWithinDepth is PanelNSourceUsesWithinDepth's set
// form, in declaration order, for the diagnostic that must NAME the
// modes a depth applies to. Derived from the enum and filtered through
// the predicate itself so a new within-prefix mode cannot be added to
// one without appearing in the other.
func panelNSourcesUsingWithinDepth() []string {
	out := make([]string, 0, 2)
	for _, m := range panelNSources {
		if PanelNSourceUsesWithinDepth(m) {
			out = append(out, m)
		}
	}
	return out
}

// PanelNSourcesUsingWithinDepth returns the modes that CONSUME
// NWithinDepth, for the "n_within_depth is not read by n_source X"
// refusal both arms raise. Returns a copy.
func PanelNSourcesUsingWithinDepth() []string {
	return panelNSourcesUsingWithinDepth()
}

// PanelNSourceReadsComponents reports whether s reads
// Response.Components.Crosstab on each slot rather than the
// MatrixPayload. The handler's up-front per-slot components gate keys
// off this predicate, so the gate is NOT applied to the legacy
// payload-read default — a panel that has never needed components must
// not start refusing when a caller spells its existing behaviour out.
func PanelNSourceReadsComponents(s string) bool {
	return s == PanelNSourceCellNUnweighted ||
		s == PanelNSourceRowMarginDistinctWithin
}

// PanelNSourceReadsDistinctKeys reports whether s reads a DISTINCT-KEY
// cardinality rather than a record count or a payload value, and is
// therefore subject to the cell-aggregator admission gate.
//
// A separate predicate from PanelNSourceReadsComponents on purpose:
// cell_n_unweighted reads components too, but it reads the universal
// FLOOR counter "n", which every aggregator emits and which means the
// same thing under all of them. Only a distinct-key figure can be
// confused with a different quantity of the same name, so only the
// distinct modes are admitted on the cell aggregator's identity.
func PanelNSourceReadsDistinctKeys(s string) bool {
	return s == PanelNSourceRowMarginDistinctWithin
}

// PanelNSourceFallsBackToCellValue reports whether s substitutes the
// CELL VALUE for a non-positive sample size.
//
// True for the legacy default ONLY, and deliberately not extended to
// any mode added after it. The substitution is a degenerate-input
// crutch from before the panel could count anything: a cell value
// standing in for a sample size is the exact class of silent
// substitution this family now refuses. It survives on row_margin_value
// because removing it would change the default path's output, and the
// default must stay byte-identical to the pre-params baseline.
//
// row_margin_value_within is excluded too, even though it reads the
// SAME payload margin the legacy leg does. Two reasons, and the second is the
// decisive one. A margin that was never emitted is not a zero-sized
// one — the posture RowMarginDistinctN already takes. And once
// NWithinDepth is set the leg is a SUM ACROSS ROWS, so substituting
// one coordinate's cell value for a whole slab's sample size is not
// the same dimension; a fallback that applied only at omitted depth
// would make one mode two behaviours.
//
// Every COUNTED mode instead answers ok=false and SKIPS the
// coordinate with a warning — the same posture the MATRIX arm's
// RowMarginDistinctN takes ("an unemitted margin is not a zero-sized
// one"). A genuine counted zero is still a zero, and the prop-Z kernel
// reports it as a degenerate pair.
func PanelNSourceFallsBackToCellValue(s string) bool {
	return s == "" || s == PanelNSourceRowMarginValue
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
