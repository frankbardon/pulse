package descriptor

import "github.com/frankbardon/pulse/types"

// AggregatorMergeability returns the ComponentsMergeability declared in
// the built-in aggregator capability table for the operator named by
// name (e.g. "AGG_SUM"). Unknown names return the zero-value empty
// string — callers should treat that as "no streaming guarantee" and
// either suppress per-chunk emission or fall back to terminal-only.
//
// This is the one-shot lookup the streaming orchestrator
// (pulse.ProcessStreamResult) consults to decide whether to attach a
// per-chunk components map (Mergeable / Partial) or suppress it until
// the terminal chunk (None).
//
// Lives in the descriptor package because the capability table is the
// single source of truth and predict already aliases the same lookup
// (aggregatorComponentSchemaIndex). The descriptor package stays
// no-execute — this helper reads only the static capabilities slice.
func AggregatorMergeability(name string) ComponentsMergeability {
	for _, op := range aggregatorCapabilities() {
		if op.Name == name {
			return op.ComponentSchema.Mergeability
		}
	}
	return ""
}

// AggregationMergeability is the typed convenience that accepts a
// types.AggregationType directly so callers do not have to round-trip
// through string(...).
func AggregationMergeability(t types.AggregationType) ComponentsMergeability {
	return AggregatorMergeability(string(t))
}

// GrouperMergeability returns the ComponentsMergeability declared in
// the built-in grouper capability table for the operator named by name
// (e.g. "GROUP_CATEGORY"). Unknown names return the zero-value empty
// string with the same "no streaming guarantee" semantics as
// AggregatorMergeability.
//
// GROUP_QUANTILE is the canonical None grouper — quantile cutoffs need
// the sorted full input, so the streaming orchestrator suppresses the
// per-chunk Operator emission for that slot until the terminal chunk.
// Every other registered grouper today (CATEGORY, DATE, RANGE,
// ROUNDED, SET_VALUE, SET_PER_ELEMENT) is Mergeable.
//
// Lives alongside AggregatorMergeability in the descriptor package so
// the streaming projection helper has a single import path for both
// axes; the underlying lookup reads only the static
// grouperCapabilities() slice and never touches `service/` or
// `processing/`.
func GrouperMergeability(name string) ComponentsMergeability {
	for _, op := range grouperCapabilities() {
		if op.Name == name {
			return op.ComponentSchema.Mergeability
		}
	}
	return ""
}

// GroupMergeability is the typed convenience that accepts a
// types.GroupType directly so callers do not have to round-trip
// through string(...).
func GroupMergeability(t types.GroupType) ComponentsMergeability {
	return GrouperMergeability(string(t))
}
