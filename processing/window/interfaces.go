package window

import (
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

// WindowOptions reserves room for future schema-aware operators.
type WindowOptions struct{}
