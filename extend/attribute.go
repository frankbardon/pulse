package extend

import (
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// AttributeComputer derives one value per row. It is the base interface
// every extension attribute implements and the whole contract of a
// registration declaring Mode "buffered": the engine calls Compute once
// with every row that passed the request's filters.
//
// Compute returns exactly one value per row, aligned with rows (entry i
// belongs to rows.At(i)). To give a row no value (null), implement
// NullableAttribute and return NaN for it.
type AttributeComputer interface {
	Compute(rows Rows, field string) ([]float64, error)
}

// RowLocalAttribute is the optional streaming sibling for an attribute
// whose value depends only on the current row. A registration declaring
// Mode "row_local" MUST return a value implementing it; the engine then
// calls Row once per row inline instead of buffering.
type RowLocalAttribute interface {
	Row(rec Record, field string) (float64, error)
}

// TwoPassAttribute is the optional streaming sibling for an attribute
// that needs population statistics (a mean, a min/max) before it can
// emit a row's value. A registration declaring Mode "two_pass" MUST
// return a value implementing it. Lifecycle: PrePass once per
// filter-passing row, then Finalize exactly once (it MUST tolerate no
// prior PrePass), then Row once per row. State is per-instance; the
// factory builds a fresh instance per request.
type TwoPassAttribute interface {
	RowLocalAttribute
	// PrePass folds one row into the running population state.
	PrePass(rec Record, field string) error
	// Finalize closes the PrePass phase; Row may be called after it.
	Finalize() error
}

// NullableAttribute is the optional sibling for an attribute that can
// emit null — "no value on this row", the null-in, null-out rule the
// built-in scores follow. When EmitsNull reports true, every NaN the
// attribute returns from Compute or Row is written as a null under its
// label in every execution mode (buffered, streaming, two-pass,
// crosstab, shard archive): the row reads null in Data and a downstream
// aggregation skips it and counts it in n_null. Without it, or when
// EmitsNull reports false, a NaN stays an ordinary (undefined) value.
// The answer is per instance, so a factory may decide it from params.
type NullableAttribute interface {
	EmitsNull() bool
}

// AttributeFactory builds a fresh AttributeComputer for one attribute
// slot of one request. During probe-validation at pulse.New it is
// called once with a spec carrying only the operator name and an empty
// schema, and must tolerate both.
type AttributeFactory func(spec *types.Attribute, schema *encoding.Schema) (AttributeComputer, error)
