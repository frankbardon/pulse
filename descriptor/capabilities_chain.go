package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/types"
)

// ProcessChainCapability describes the source-rooted linear chain
// endpoint surfaced by pulse.ProcessChain / pulse_process_chain. The
// manifest carries one ProcessChainCapability entry under
// Manifest.ProcessChain so LLM clients can detect the chain gate
// and choose between the chained or per-stage fallback path.
type ProcessChainCapability struct {
	// Name is the canonical entry identifier ("process_chain").
	Name string `json:"name"`

	// MaxStages is the upper bound on Stages length. Zero indicates
	// no compile-time cap; runtime memory is bounded by the largest
	// intermediate response.Data slice.
	MaxStages int `json:"max_stages"`

	// MergeableAggregators lists the aggregator names that pass the
	// chain gate (mergeable + single-scalar emit). Alphabetically
	// sorted, deterministic across calls.
	MergeableAggregators []string `json:"mergeable_aggregators"`

	// MergeableGroupers lists the grouper names that pass the chain
	// gate.
	MergeableGroupers []string `json:"mergeable_groupers"`

	// RowLocalAttributes lists the attribute names that pass the
	// chain gate (row-local only).
	RowLocalAttributes []string `json:"row_local_attributes"`

	// RejectionRules names the operator categories the chain gate
	// rejects today. Intended for LLM-side reasoning and fallback
	// routing; not a strict schema.
	RejectionRules []string `json:"rejection_rules"`

	// OverlayKinds lists the whole-chain overlay catalog entries
	// accepted on ChainRequest.Overlays today. Alphabetically sorted.
	// Minimal additive surface kept for backward compatibility; the
	// richer per-kind sub-block at Overlays carries the full surface
	// (shapes / scopes / ref kinds / buffered / description).
	OverlayKinds []string `json:"overlay_kinds"`

	// Overlays enumerates the per-kind capability rows for every
	// whole-chain overlay the catalog declares. Sourced from
	// descriptor.overlayCapabilityFor() so the chain capability stays
	// byte-equal with the corresponding entries on Manifest.Overlays —
	// single source of truth for kind metadata. Sorted alphabetically by
	// Kind for golden stability. Each row carries:
	//
	//   - Kind        — the OverlayKind constant (SCREAMING_SNAKE).
	//   - Shapes      — every OverlayShape the kind may emit; whole-chain
	//                   kinds inherit the target stage's host shape so
	//                   the catalog declares MATRIX + SCALAR + SERIES.
	//   - Scopes      — the OverlayScope values the kind supports;
	//                   whole-chain kinds decorate the entire chain
	//                   result so Scopes == [total].
	//   - RefKinds    — the OverlayRef union pointer-field names the kind
	//                   consumes; the chain family consumes Stage.
	//   - Buffered    — !types.OverlayStreamable(kind); whole-chain kinds
	//                   stay buffered today (the streamability flag is
	//                   the inverse of the table in
	//                   types/overlay_streamability.go).
	//   - Description — short human-readable summary; mirrors the prose
	//                   in skills/overlay-system.md.
	Overlays []OverlayCapability `json:"overlays"`
}

// processChainCapability returns the canonical ProcessChainCapability
// entry. Static today; bumps require updating
// skills/process-chain.md and CLAUDE.md.
func processChainCapability() ProcessChainCapability {
	return ProcessChainCapability{
		Name:      "process_chain",
		MaxStages: 0,
		MergeableAggregators: []string{
			"AGG_AVERAGE", "AGG_COUNT", "AGG_DISTINCT_COUNT",
			"AGG_MAX", "AGG_MIN", "AGG_NULL_COUNT",
			"AGG_RANGE", "AGG_STDDEV", "AGG_SUM",
			"AGG_VARIANCE",
		},
		MergeableGroupers: []string{
			"GROUP_CATEGORY", "GROUP_RANGE",
		},
		RowLocalAttributes: []string{
			"ATTR_DATE_PART", "ATTR_FORMULA",
		},
		RejectionRules: []string{
			"chain rejects windows, features, tier-1 row tests, tier-2 post tests, regressions",
			"chain rejects two-pass attributes (ZSCORE / TSCORE / NORMALIZED / REG_*)",
			"chain rejects AGG_FREQUENCY and AGG_MODE (non-scalar emit)",
			"chain rejects AGG_MEDIAN / AGG_PERCENTILE / AGG_ZSCORE / AGG_SKEWNESS / AGG_KURTOSIS (non-mergeable)",
			"chain rejects GROUP_ROUNDED / GROUP_QUANTILE / GROUP_DATE (non-mergeable)",
			"chain rejects decimal128 aggregation targets",
			"chain rejects extension aggregators (custom MergeOnline surface deferred)",
		},
		OverlayKinds: []string{
			"OVERLAY_DELTA_VS_STAGE",
			"OVERLAY_INDEX_VS_STAGE",
		},
		Overlays: chainOverlayCapabilities(),
	}
}

// chainOverlayCapabilities returns the per-kind capability rows for
// every whole-chain overlay kind. Pulls each row from the shared
// overlayCapabilityFor() catalog so the chain capability stays in lock-
// step with the corresponding entry on Manifest.Overlays — single source
// of truth for kind metadata.
//
// Buffered is filled from types.OverlayStreamable(kind) just like
// OverlayCapabilities() does for the top-level Manifest.Overlays surface
// so a future kind that flips to streamable automatically flips Buffered
// here too. The slice is sorted alphabetically by Kind for golden
// stability.
//
// The list of whole-chain kinds is intentionally local to this file —
// the chain family is a closed set (OVERLAY_INDEX_VS_STAGE +
// OVERLAY_DELTA_VS_STAGE). Future chain kinds extend this slice and
// the matching switch arm in overlayCapabilityFor().
func chainOverlayCapabilities() []OverlayCapability {
	kinds := []types.OverlayKind{
		types.OverlayKindDeltaVsStage,
		types.OverlayKindIndexVsStage,
	}
	caps := make([]OverlayCapability, 0, len(kinds))
	for _, k := range kinds {
		streamable, _ := types.OverlayStreamable(k)
		entry := overlayCapabilityFor(k)
		entry.Buffered = !streamable
		sort.Slice(entry.Shapes, func(i, j int) bool {
			return string(entry.Shapes[i]) < string(entry.Shapes[j])
		})
		sort.Slice(entry.Scopes, func(i, j int) bool {
			return string(entry.Scopes[i]) < string(entry.Scopes[j])
		})
		sort.Strings(entry.RefKinds)
		sort.Strings(entry.Fields)
		caps = append(caps, entry)
	}
	return caps
}
