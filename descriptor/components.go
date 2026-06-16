package descriptor

import "github.com/frankbardon/pulse/types"

// This file defines the descriptor-side typed surface for the
// per-operator components contract. The runtime sibling interface that
// emits the matching map[string]any payload lives in the processing
// package (see processing/interfaces.go MetaAggregator and the parallel
// MetaGrouper / MetaFilterer siblings introduced in later stories).
//
// IMPORTANT: descriptor/ is no-execute and MUST NOT import
// service/ or processing/. The TestPredictNoExecutionImports gate
// enforces this for predict-* files; the same structural ban applies
// across the whole descriptor package by convention. ComponentSchema
// keeps the type names of emitted values as plain strings (not
// reflect.Kind) precisely so this file can declare the contract
// without importing any execution-layer types.

// ComponentsMergeability is re-exported from types/ where the canonical
// enum lives (see types/streamability.go). Descriptor aliases the type
// and forwards the constants so existing capability-file declarations
// (descriptor.Mergeable / descriptor.Partial / descriptor.None) and
// predict-side switches keep compiling unchanged while the source of
// truth is the leaf-package types declaration. Wire JSON values are
// "mergeable" / "partial" / "none".
type ComponentsMergeability = types.ComponentsMergeability

const (
	// Mergeable forwards types.ComponentsMergeable. The components map
	// folds across chunks via the same associative/commutative path
	// as the scalar value (Welford-family, sums, counts, set masks,
	// weighted accumulators).
	Mergeable = types.ComponentsMergeable

	// Partial forwards types.ComponentsPartial. The components map
	// merges across chunks but at non-trivial allocation cost — map /
	// set unions where the fold is associative but not constant-
	// space (AGG_FREQUENCY, AGG_MODE, AGG_DISTINCT_COUNT,
	// AGG_SET_FREQUENCY). The orchestrator may stage the merge at
	// terminal flush.
	Partial = types.ComponentsPartial

	// None forwards types.ComponentsNone. The components map cannot
	// be computed from a per-chunk partial — the operator needs a
	// sorted view (or equivalent) of the full input (AGG_MEDIAN,
	// AGG_PERCENTILE, GROUP_QUANTILE). Streaming chunks omit
	// components for these operators; predict declares the slot as
	// buffered-components-only.
	None = types.ComponentsNone
)

// ComponentKey describes one named entry in an operator's components
// map. The triple (Name, Type, Description) is the manifest projection
// embedders consume to learn what shape of value to expect at runtime.
//
// Type is a human-readable kind string (e.g. "int", "float64",
// "map[string]int", "[]string", "WelfordTriple") rather than a
// reflect.Kind so the manifest can serialize it directly into JSON
// without an extra mapping table. Implementations may use anything
// that round-trips through manifest consumers — keep it short and
// unambiguous.
//
// Name is the wire-stable snake_case key the operator emits into the
// runtime components map (e.g. "mean", "variance", "mode_count",
// "range_min"). Description is a single sentence in plain prose used
// by the manifest, predict, and MCP surfaces. Both are required.
type ComponentKey struct {
	// Name is the snake_case key the operator emits into the runtime
	// map[string]any returned by MetaAggregator.Components.
	Name string `json:"name"`
	// Type is the human-readable kind name of the emitted value.
	Type string `json:"type"`
	// Description is a single-sentence prose description suitable for
	// manifest / predict / MCP surfaces.
	Description string `json:"description"`
}

// ComponentSchema is the per-operator declaration carried by every
// descriptor capability entry. The Keys slice enumerates the operator-
// specific components emitted at runtime (the universal floor of
// {"n", "n_null"} is filled unconditionally by the orchestrator and is
// NOT listed here); Mergeability classifies how those components fold
// across streaming chunks.
//
// An empty Keys slice paired with Mergeability == Mergeable is a valid
// schema — it declares an operator whose only contribution is the
// universal floor (AGG_COUNT is the canonical example). Consumers
// receiving an empty Keys slice still see {"n", "n_null"} in the
// runtime response under the orchestrator's universal-floor pass.
//
// Manifest serialization preserves Keys order — declare keys in a
// stable order matching the operator's emission for golden-test
// readability.
type ComponentSchema struct {
	// Keys enumerates the named operator-specific components, in
	// emission order. May be empty when only the universal floor
	// applies.
	Keys []ComponentKey `json:"keys,omitempty"`
	// Mergeability classifies how the components map folds across
	// streaming chunks. Required for every schema (no zero-value
	// default — embedders must declare intent explicitly).
	Mergeability ComponentsMergeability `json:"mergeability"`
}

// Compile-time-assertion pattern: per-aggregator MetaAggregator
// implementations should add a sentinel
// of the form
//
//	var _ processing.MetaAggregator = (*welfordAgg)(nil)
//
// adjacent to their factory or struct declaration. The sentinel keeps
// the wiring grep-discoverable, surfaces interface drift as a build
// error (instead of a silent runtime no-op), and pairs naturally with
// the existing RichAggregator / MergeableAggregator sentinels (see
// processing/aggregator_welford.go for the model). This file declares
// only the descriptor-side surface; no processing.MetaAggregator
// assertions live here — that would require importing processing/ and
// would break TestPredictNoExecutionImports.
