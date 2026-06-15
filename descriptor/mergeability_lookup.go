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
