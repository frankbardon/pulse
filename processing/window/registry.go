package window

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// windowRegistry maps window types to their factory functions. Operators are
// registered in their per-operator file via init() functions to keep this map
// open for extension without a single point of contention.
var windowRegistry = map[types.WindowType]WindowFactory{}

// register installs a factory in the registry. Called from per-operator init().
func register(t types.WindowType, f WindowFactory) {
	if _, exists := windowRegistry[t]; exists {
		panic("window: duplicate registration for " + string(t))
	}
	windowRegistry[t] = f
}

// Lookup returns the factory for a window type, if registered.
func Lookup(t types.WindowType) (WindowFactory, bool) {
	f, ok := windowRegistry[t]
	return f, ok
}

// RegisteredTypes returns all registered window types. Order is map iteration
// order; callers that need determinism must sort the result.
func RegisteredTypes() []types.WindowType {
	out := make([]types.WindowType, 0, len(windowRegistry))
	for k := range windowRegistry {
		out = append(out, k)
	}
	return out
}

// Apply runs every window in windows over rows. Each window:
//  1. Resolves a (partitionBy, orderBy) sort over the row slice. Sorts are
//     cached by tuple key so multiple windows sharing a partition+order pay
//     one sort cost.
//  2. Builds partitions from the sorted index slice.
//  3. Invokes the operator's Compute to mutate rows with the output column.
//
// Apply trusts that windows has been validated by predict; it returns coded
// errors for runtime failures (unknown type, factory error, compute error).
func Apply(ctx context.Context, rows []map[string]any, windows []*types.Window) error {
	_ = ctx
	if len(windows) == 0 || len(rows) == 0 {
		return nil
	}

	cache := newSortCache(rows)

	for _, w := range windows {
		factory, ok := Lookup(w.Type)
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown window type: %s", w.Type))
		}
		computer, err := factory(w, WindowOptions{})
		if err != nil {
			return err
		}

		sortedIdx, err := cache.get(w.PartitionBy, w.OrderBy)
		if err != nil {
			return err
		}
		partitions := partition(rows, sortedIdx, w.PartitionBy)

		label := windowLabel(w)
		if err := computer.Compute(rows, partitions, label); err != nil {
			return err
		}
	}
	return nil
}

// windowLabel returns the effective output column name for a window. Mirrors
// descriptor.windowLabel; both must match exactly.
func windowLabel(w *types.Window) string {
	if w.Label != "" {
		return w.Label
	}
	if w.Field == "" {
		return string(w.Type)
	}
	return string(w.Type) + "_" + w.Field
}
