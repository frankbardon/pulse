package extend

import "github.com/frankbardon/pulse/types"

// WindowComputer adds one window column to the materialized result
// rows, mutating them in place. partitions holds each partition's row
// indices, already sorted by the window's OrderBy keys; label is the
// output column name to write. Window operators always run buffered.
type WindowComputer interface {
	Compute(rows []map[string]any, partitions [][]int, label string) error
}

// WindowOptions is reserved for future schema-aware window operators.
// It is empty today; accept it and ignore it.
type WindowOptions struct{}

// WindowFactory builds a fresh WindowComputer for one window slot of
// one request.
type WindowFactory func(spec *types.Window, opts WindowOptions) (WindowComputer, error)
