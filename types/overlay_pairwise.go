package types

import (
	"encoding/json"
	"fmt"
)

// PairwiseOverlayParams is the decoded OverlaySpec.Params shape for the
// OVERLAY_PAIRWISE_* family. Every field is optional; the zero value
// (no params) means "every pair-axis index vs every other, cell n
// unweighted, cell value as a 0..100 percentage". Axis (row vs column
// pairing) rides on OverlaySpec.Scope, not here.
type PairwiseOverlayParams struct {
	// PairAlongDim, when non-nil, restricts pair generation to pair-axis
	// indexes whose key tuples agree on every dim position EXCEPT this
	// one ("all dims agree except this one" buckets). nil = every pair.
	// Must be >= 0 and < the pair-axis dim count.
	PairAlongDim *int `json:"pair_along_dim,omitempty"`

	// NSource selects where the sample-size leg is read. One of the
	// PairwiseNSource* constants. Empty = cell_n_unweighted. Ignored by
	// the Welford-input kinds (welch / two-means read n from the triple).
	NSource string `json:"n_source,omitempty"`

	// NWithinDepth, with NSource=n_within, fixes the first NWithinDepth+1
	// pair-axis dim positions in the denominator (mirrors
	// CrosstabSpec.NormalizeWithin). Must be >= 0.
	NWithinDepth int `json:"n_within_depth,omitempty"`

	// PSource selects how the proportion leg is derived for the
	// proportion-input kinds (prop-Z, probit-t). One of the
	// PairwisePSource* constants. Empty = cell_value_pct. Ignored by the
	// Welford-input kinds.
	PSource string `json:"p_source,omitempty"`
}

// Pairwise sample-size source modes (PairwiseOverlayParams.NSource).
const (
	PairwiseNSourceCellNUnweighted = "cell_n_unweighted"
	PairwiseNSourceCellValueWeight = "cell_value_weighted"
	PairwiseNSourceRowMarginN      = "row_margin_n"
	PairwiseNSourceColumnMarginN   = "column_margin_n"
	PairwiseNSourceNWithin         = "n_within"
	PairwiseNSourceCellWeightSum   = "cell_weight_sum"
)

// Pairwise proportion source modes (PairwiseOverlayParams.PSource).
const (
	PairwisePSourceCellValuePct = "cell_value_pct"
	PairwisePSourceCellValue    = "cell_value"
)

// ValidPairwiseNSource reports whether s names a supported NSource mode.
// Empty counts as valid (defaults to cell_n_unweighted).
func ValidPairwiseNSource(s string) bool {
	switch s {
	case "",
		PairwiseNSourceCellNUnweighted,
		PairwiseNSourceCellValueWeight,
		PairwiseNSourceRowMarginN,
		PairwiseNSourceColumnMarginN,
		PairwiseNSourceNWithin,
		PairwiseNSourceCellWeightSum:
		return true
	}
	return false
}

// ValidPairwisePSource reports whether s names a supported PSource mode.
// Empty counts as valid (defaults to cell_value_pct).
func ValidPairwisePSource(s string) bool {
	switch s {
	case "", PairwisePSourceCellValuePct, PairwisePSourceCellValue:
		return true
	}
	return false
}

// DecodePairwiseParams decodes a raw OverlaySpec.Params blob into a
// PairwiseOverlayParams. A nil / empty blob yields the zero value (all
// defaults). Malformed JSON returns an error so callers surface a clean
// predict-time / runtime diagnostic rather than panicking.
func DecodePairwiseParams(raw json.RawMessage) (PairwiseOverlayParams, error) {
	var p PairwiseOverlayParams
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("decode pairwise overlay params: %w", err)
	}
	return p, nil
}

// IsPairwiseOverlayKind reports whether kind is a member of the
// OVERLAY_PAIRWISE_* family.
func IsPairwiseOverlayKind(kind OverlayKind) bool {
	switch kind {
	case OverlayKindPairwisePropZ,
		OverlayKindPairwiseProbitT,
		OverlayKindPairwiseWelchT,
		OverlayKindPairwiseTwoMeansZ:
		return true
	}
	return false
}

// PairwiseKindUsesWelford reports whether kind reads the Welford triple
// {mean, variance, n} (welch / two-means) rather than a proportion + n.
func PairwiseKindUsesWelford(kind OverlayKind) bool {
	return kind == OverlayKindPairwiseWelchT || kind == OverlayKindPairwiseTwoMeansZ
}
