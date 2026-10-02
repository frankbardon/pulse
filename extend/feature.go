package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// FeatureOutput is one derived column: Values has one entry per input
// row and, when Nulls is non-nil, Nulls has the same length with
// Nulls[i] == true meaning row i is null (Values[i] is then ignored).
// The engine writes the column; a feature operator never mutates a
// Record.
type FeatureOutput struct {
	Values []float64
	Nulls  []bool
}

// FeatureComputer derives one or more columns from the rows of one
// buffered call, keyed by output column name. Every FeatureOutput must
// be aligned with rows (len(Values) == rows.Len()); a misaligned
// output is refused with PROCESSING_INTERNAL.
type FeatureComputer interface {
	Compute(rows Rows, field string) (map[string]FeatureOutput, error)
}

// StreamingFeatureComputer is the optional streaming sibling of
// FeatureComputer. The engine type-asserts it on the value the factory
// returns: present, a stream-eligible request keeps streaming; absent,
// the request falls back to the buffered path.
//
// Lifecycle: PrePass once per row on the first pass, Finalize once,
// then EmitRow once per row on the second pass. EmitRow returns
// single-row outputs (len(Values) == 1; Nulls nil or length 1).
// Stateless per-row operators implement PrePass and Finalize as no-ops.
type StreamingFeatureComputer interface {
	PrePass(rec Record, field string) error
	Finalize() error
	EmitRow(rec Record, field string) (map[string]FeatureOutput, error)
}

// FeatureFactory builds a fresh FeatureComputer for one feature slot
// of one request. The schema is the cohort schema before any feature
// output is added.
type FeatureFactory func(spec *types.Feature, schema *encoding.Schema) (FeatureComputer, error)
