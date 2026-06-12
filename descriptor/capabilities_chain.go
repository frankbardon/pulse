package descriptor

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
	// Minimal additive surface — E6-S7 wires the predict-time validator
	// so this hint stays aligned with the runtime dispatch table at
	// processing/overlay_chain_dispatch.go. The full capability surface
	// (per-kind ref/scope contract, streamability echo) lands in E6-S11.
	OverlayKinds []string `json:"overlay_kinds"`
}

// processChainCapability returns the canonical ProcessChainCapability
// entry. Static today; bumps require updating
// skills/contributor-workflow.md and CLAUDE.md.
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
	}
}
