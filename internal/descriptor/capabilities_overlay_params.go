package descriptor

import (
	"slices"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/seriesfield"
	"github.com/frankbardon/pulse/internal/seriespartition"
	"github.com/frankbardon/pulse/internal/twosample"
	"github.com/frankbardon/pulse/types"
)

// capabilities_overlay_params.go declares every overlay kind's params
// keys (OverlayCapability.Params) — the allowlist an overlay spec's
// params object is refused against (ParamsRefusal), held to the keys
// the runtime reads by TestParamsDeclaredMatchRead. A key a kind reads
// on ANY host (Request, Facet, Compose, chain) is listed: the
// host-generic `field` / `partition_by` rules come from the same
// predicates the runtime dispatches on (internal/seriesfield,
// internal/seriespartition, the facet capability), the kind-specific
// keys from each kind's reader.

// overlayFieldParam is params.field: the host aggregation (or facet
// field) a series / facet overlay scores.
var overlayFieldParam = descriptor.Param{
	Name:        "field",
	Type:        "string",
	Required:    false,
	Description: "On a SERIES host, the OUTPUT LABEL of the aggregation to score (explicit label, else TYPE_field) or one expanded element column \"<label>[<element label>]\"; absent, the first aggregation. On the facet host, the FacetRequest.Fields entry to score (required with two or more fields). A name matching nothing is PULSE_OVERLAY_FIELD_UNKNOWN.",
}

// overlayPartitionByParam is params.partition_by: the leading group key
// columns a per-partition series overlay runs within.
var overlayPartitionByParam = descriptor.Param{
	Name:        "partition_by",
	Type:        "list",
	Required:    false,
	Description: "Over two or more groupers, the leading group key columns each partition shares (default every key but the last; [] = one walk over every row; OVERLAY_YOY: exactly the non-date keys). Anything else is PULSE_OVERLAY_PARTITION_INVALID.",
}

// facetOverlayKind reports whether kind runs on the facet host, which
// reads params.field for every kind it offers.
func facetOverlayKind(kind types.OverlayKind) bool {
	return slices.Contains(facetCapability().SupportedOverlayKinds, string(kind))
}

// twoSampleParams are the explicit per-side inputs a two-sample cell
// overlay reads for a scalar side (internal/twosample).
func twoSampleParams() []descriptor.Param {
	side := func(key, what string) descriptor.Param {
		return descriptor.Param{Name: key, Type: "float", Required: false, Description: what + " Required (> 0) for a side whose cell carries no mean + variance components; no default."}
	}
	return []descriptor.Param{
		side(twosample.VarianceKey(twosample.Target), "Target side's sample variance."),
		side(twosample.VarianceKey(twosample.Reference), "Reference side's sample variance."),
		side(twosample.SymmetricVarianceKey, "One sample variance for both sides, read when a side's own key is absent."),
		side(twosample.SampleSizeKey(twosample.Target), "Target side's sample size."),
		side(twosample.SampleSizeKey(twosample.Reference), "Reference side's sample size."),
		side(twosample.SymmetricSampleSizeKey, "One sample size for both sides, read when a side's own key is absent."),
	}
}

// pairwiseParams is a pairwise kind's accepted params: every kind takes
// pair_along_dim, n_within_depth and letters; the proportion kinds add the
// n_source / p_source selectors, the weighted-moments kind n_basis.
// The selectors a kind does not take are refused by the kind itself
// (paramsOwnRefusals).
func pairwiseParams(kind types.OverlayKind) []descriptor.Param {
	ps := []descriptor.Param{
		{Name: "pair_along_dim", Type: "int", Required: false, Description: "Restrict pairs to pair-axis keys that agree on every dim position except this one; absent = every pair."},
		{Name: "n_within_depth", Type: "int", Required: false, Default: 0, Description: "With a within-prefix n_source, the pair-axis dim positions (depth + 1) the n denominator is summed within; inert otherwise."},
		{Name: "letters", Type: "object", Required: false, Description: "{} or {\"alpha\": a}, 0 < a < 1: significance letters (the letters part); reads significant_adjusted under a correction, else raw p < 0.05 (an explicit alpha applies to either)."},
	}
	switch {
	case types.PairwiseKindUsesWeightedMoments(kind):
		ps = append(ps, descriptor.Param{Name: "n_basis", Type: "enum", Required: true, EnumValues: []string{types.PairwiseNBasisKish, types.PairwiseNBasisWeights}, Description: "Variance / sample-size convention over the weighted moments; required, no default."})
	case !types.PairwiseKindUsesWelford(kind):
		ps = append(ps,
			descriptor.Param{Name: "n_source", Type: "enum", Required: false, Description: "Where each leg's sample size is read (default cell_n_unweighted)."},
			descriptor.Param{Name: "p_source", Type: "enum", Required: false, Description: "How each leg's proportion is derived (default cell_value on a normalized host, else cell_value_pct; cell_value_pct on a normalized host is refused)."},
		)
	}
	return ps
}

// overlayParamsFor is kind's declared params list.
func overlayParamsFor(kind types.OverlayKind) []descriptor.Param {
	var ps []descriptor.Param
	if seriesfield.Applies(kind) || seriesfield.AppliesCompose(kind) || seriesfield.AppliesChain(kind) || facetOverlayKind(kind) {
		ps = append(ps, overlayFieldParam)
	}
	if seriespartition.Applies(kind) || kind == types.OverlayKindYoY {
		ps = append(ps, overlayPartitionByParam)
	}
	switch kind {
	case types.OverlayKindFormula:
		ps = append(ps,
			descriptor.Param{Name: "formula", Type: "string", Required: true, Description: "The expr-lang expression evaluated per coordinate over the host's namespace; absent, empty or not a string is PULSE_OVERLAY_PARAM_MISSING."},
			descriptor.Param{Name: "baseline_position", Type: "int", Required: false, Description: "SERIES host: the 0-based row of each partition `baseline` reads; a non-negative integer."},
		)
	case types.OverlayKindIndexVsRollingMean, types.OverlayKindZScoreVsRolling:
		ps = append(ps, descriptor.Param{Name: "window", Type: "int", Required: true, Description: "Rolling window width in rows, a positive integer."})
	case types.OverlayKindYoY:
		ps = append(ps, descriptor.Param{Name: "frequency", Type: "enum", Required: false, EnumValues: []string{"annual", "daily", "hourly", "monthly", "quarterly", "weekly"}, Description: "Period the year-over-year lag is counted in; absent, the host GROUP_DATE grouper's params.frequency."})
	case types.OverlayKindIndexVsRef, types.OverlayKindPanelIndexVsRef:
		ps = append(ps, descriptor.Param{Name: "scale", Type: "float", Required: false, Default: 100.0, Description: "Multiplier on target / reference; 1 gives a raw ratio."})
	case types.OverlayKindRank:
		ps = append(ps, descriptor.Param{Name: "population", Type: "enum", Required: false, Default: "matrix", EnumValues: []string{"column", "matrix", "row"}, Description: "Cells each target cell is ranked within."})
	case types.OverlayKindTCell, types.OverlayKindZCell, types.OverlayKindTVsRef, types.OverlayKindZVsRef:
		ps = append(ps, twoSampleParams()...)
	case types.OverlayKindPropZPanel:
		ps = append(ps,
			descriptor.Param{Name: "n_source", Type: "enum", Required: false, Default: types.PanelNSourceRowMarginValue, Description: "Where each slot's sample size is read (types.PanelNSources)."},
			descriptor.Param{Name: "n_within_depth", Type: "int", Required: false, Description: "With a within-prefix n_source, the row-key prefix depth the margin is summed within; required there, refused otherwise."},
		)
	case types.OverlayKindSymmetry:
		ps = append(ps, descriptor.Param{Name: "correct", Type: "bool", Required: false, Default: true, Description: "2 x 2 continuity correction (|n_12 - n_21| - 1)^2; inert on k x k."})
	case types.OverlayKindMarkov:
		ps = append(ps,
			descriptor.Param{Name: "steps", Type: "list", Required: false, Description: "Distinct step counts 1-1000 (at most 64) to project; default [1, 3, 6, 12]."},
			descriptor.Param{Name: "absorbing", Type: "list", Required: false, Description: "State keys treated as absorbing."},
			descriptor.Param{Name: "initial", Type: "object", Required: false, Description: "\"margin\" or {state: share}: the starting distribution."},
		)
	case types.OverlayKindRetention:
		ps = append(ps, descriptor.Param{Name: "absorbing", Type: "list", Required: false, Description: "Distinct state keys whose arrivals count as churn; omitted, no churn split."})
	case types.OverlayKindCorrespondence:
		ps = append(ps,
			descriptor.Param{Name: "dimensions", Type: "int", Required: false, Default: 2, Description: "Dimensions to report, capped at min(rows, columns) - 1."},
			descriptor.Param{Name: "normalization", Type: "enum", Required: false, Default: "symmetric", EnumValues: []string{"column_principal", "row_principal", "symmetric"}, Description: "Coordinate scaling."},
		)
	case types.OverlayKindRake:
		ps = append(ps,
			descriptor.Param{Name: "row_targets", Type: "object", Required: true, Description: "{row key: target}, one per host row key."},
			descriptor.Param{Name: "col_targets", Type: "object", Required: true, Description: "{column key: target}, one per host column key."},
			descriptor.Param{Name: "targets_as", Type: "enum", Required: false, Default: "shares", EnumValues: []string{"counts", "shares"}, Description: "shares are normalized and scaled to the host total; counts must agree in total."},
			descriptor.Param{Name: "max_iter", Type: "int", Required: false, Default: 100, Description: "Iteration cap."},
			descriptor.Param{Name: "tolerance", Type: "float", Required: false, Default: 1e-8, Description: "Convergence threshold on the maximum relative margin error."},
		)
	}
	if types.IsPairwiseOverlayKind(kind) {
		ps = append(ps, pairwiseParams(kind)...)
	}
	return ps
}
