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
// belongs to rows.At(i)).
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

// AttributeFactory builds a fresh AttributeComputer for one attribute
// slot of one request. During probe-validation at pulse.New it is
// called once with a spec carrying only the operator name and an empty
// schema, and must tolerate both.
type AttributeFactory func(spec *types.Attribute, schema *encoding.Schema) (AttributeComputer, error)
