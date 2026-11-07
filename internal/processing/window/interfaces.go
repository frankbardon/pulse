package window

import (
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/types"
)

// WindowComputer mutates rows in place to add a window operator's output column.
//
// partitions is a slice of partition row-index slices already sorted by the
// window's OrderBy keys. label is the pre-computed output column name.
type WindowComputer interface {
	Compute(rows []map[string]any, partitions [][]int, label string) error
}

// WindowFactory creates a WindowComputer from a Window spec.
type WindowFactory func(w *types.Window, opts WindowOptions) (WindowComputer, error)

// WindowOptions carries the run's inputs a factory needs beyond the
// slot itself. The zero value is a run with no request-bound
// expression declarations.
type WindowOptions struct {
	// ExprBound is the request-bound half of a WIN_FORMULA compile
	// (internal/winformula): the request's params, the embedder's
	// ExprFunctions, lookup() and range_label() — the engine's
	// ExtensionRegistry declaration, the one predict declares with
	// placeholders. coef(), sweep_axis() and response() are never set
	// on it: a window row reads no fitted model.
	ExprBound exprenv.Bound
	// Columns is the typed half of a WIN_FORMULA compile: the Go type
	// of each column the rows carry, as predict's field-reference walk
	// declares it — over the record rows every schema field
	// (winformula.Columns), over the grouped or aggregated output rows
	// each built-in group key and aggregation label
	// (winformula.OutputColumns). A name it omits is untyped. Nil:
	// every name untyped.
	Columns exprtypes.Map
}
