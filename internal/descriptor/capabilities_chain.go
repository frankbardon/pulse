package descriptor

import (
	"sort"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/types"
)

// processChainCapability returns the canonical ProcessChainCapability
// entry. Static today; bumps require updating
// skills/process-chain.md and CLAUDE.md.
func processChainCapability() descriptor.ProcessChainCapability {
	return descriptor.ProcessChainCapability{
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
			"chain rejects built-in aggregators over decimal128 targets",
			"chain rejects extension aggregators and groupers not declared Mergeable",
			"chain rejects non-streamable filterers",
			"chain rejects Joins on any stage after stage 0 (PULSE_CHAIN_STAGE_JOIN)",
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
func chainOverlayCapabilities() []descriptor.OverlayCapability {
	kinds := []types.OverlayKind{
		types.OverlayKindDeltaVsStage,
		types.OverlayKindIndexVsStage,
	}
	caps := make([]descriptor.OverlayCapability, 0, len(kinds))
	for _, k := range kinds {
		streamable, _ := types.OverlayStreamable(k)
		entry := overlayCapabilityFor(k)
		entry.Buffered = !streamable
		entry.Zone = ZoneCapabilityOf(string(k))
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
