package window

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/windowparams"
	"github.com/frankbardon/pulse/types"
)

// windowRegistry maps window types to their factory functions. Operators are
// registered in their per-operator file via init() functions to keep this map
// open for extension without a single point of contention.
var windowRegistry = map[types.WindowType]WindowFactory{}

// register installs a factory in the registry. Called from per-operator init().
// The installed factory first judges the slot's configuration through the
// reading predict shares (internal/windowparams), so every built-in
// refuses at construction exactly what predict refuses — order_by, field,
// frame and params — with the same PULSE_WINDOW_INVALID rule sentence.
func register(t types.WindowType, f WindowFactory) {
	if _, exists := windowRegistry[t]; exists {
		panic("window: duplicate registration for " + string(t))
	}
	windowRegistry[t] = func(w *types.Window, o WindowOptions) (WindowComputer, error) {
		if ref := windowparams.First(w); ref != nil {
			return nil, ref.Coded()
		}
		return f(w, o)
	}
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

// Resolver resolves a window type to its factory. The engine passes
// processing.ExtensionRegistry.LookupWindow, which consults the
// embedder overlay and the instance feature set before the built-in
// registry. A nil Resolver is the built-in registry alone (Lookup).
type Resolver func(t types.WindowType) (WindowFactory, bool)

func (r Resolver) lookup(t types.WindowType) (WindowFactory, bool) {
	if r == nil {
		return Lookup(t)
	}
	return r(t)
}

// Apply runs every window in windows over rows. Equivalent to
// ApplyWithExt with a nil Resolver; preserved so existing callers and
// per-operator unit tests stay shape-compatible.
func Apply(ctx context.Context, rows []map[string]any, windows []*types.Window) error {
	return ApplyWithExt(ctx, rows, windows, nil)
}

// ApplyWithExt runs every window in windows over rows. Each window:
//  1. Resolves a (partitionBy, orderBy) sort over the row slice. Sorts are
//     cached by tuple key so multiple windows sharing a partition+order pay
//     one sort cost.
//  2. Builds partitions from the sorted index slice.
//  3. Invokes the operator's Compute to mutate rows with the output column.
//
// resolve is the operator resolver (see Resolver). Nil takes the
// built-in-only path; an unresolved type fails with the canonical
// unknown-window error.
func ApplyWithExt(ctx context.Context, rows []map[string]any, windows []*types.Window, resolve Resolver) error {
	return ApplyKeyed(ctx, rows, windows, resolve, nil)
}

// ApplyKeyed is ApplyWithExt under grouper key orders: an order_by key
// whose column orders names (a grouped row's grouper key column) sorts
// by that grouper's intrinsic key order (CompareKeyOrdered), and a
// keyOrderedComputer (WIN_RANK / WIN_DENSE_RANK) judges ties through the
// same comparator. Nil orders is ApplyWithExt, byte-identically.
func ApplyKeyed(ctx context.Context, rows []map[string]any, windows []*types.Window, resolve Resolver, orders KeyOrders) error {
	return ApplyObserved(ctx, rows, windows, resolve, orders, nil)
}

// ApplyObserved is ApplyKeyed with a CopyObserver stamped on every
// built-in copy window (copyingComputer: WIN_LAG / WIN_LEAD), so the
// caller can follow each copied cell back to its source row. The rows
// themselves are written exactly as ApplyKeyed writes them; a nil
// observer is ApplyKeyed, byte-identically. An extension window is
// never observed.
func ApplyObserved(ctx context.Context, rows []map[string]any, windows []*types.Window, resolve Resolver, orders KeyOrders, observe CopyObserver) error {
	_ = ctx
	if len(windows) == 0 || len(rows) == 0 {
		return nil
	}

	cache := newSortCache(rows, orders)

	for _, w := range windows {
		factory, ok := resolve.lookup(w.Type)
		if !ok {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("unknown window type: %s", w.Type))
		}
		computer, err := factory(w, WindowOptions{})
		if err != nil {
			return err
		}
		if ko, ok := computer.(keyOrderedComputer); ok && orders != nil {
			ko.setKeyOrders(orders)
		}
		if cc, ok := computer.(copyingComputer); ok && observe != nil {
			cc.setCopyObserver(observe)
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

// keyOrderedComputer is a built-in window computer whose output reads
// the order_by comparator beyond the sorted partitions (WIN_RANK /
// WIN_DENSE_RANK tie detection): ApplyKeyed stamps the run's key orders
// on it before Compute, so a tie is exactly a CompareRows tie.
type keyOrderedComputer interface {
	setKeyOrders(orders KeyOrders)
}

// CopyObserver hears every cell a built-in copy window writes: dst's
// label cell was set to a verbatim copy of src's field cell, or — src
// nil — to the window's default / null (the source fell outside the
// partition). Called in row-write order, after the write.
type CopyObserver func(dst, src map[string]any, label, field string)

// copyingComputer is a built-in window computer whose output cells are
// verbatim copies of an input cell (WIN_LAG / WIN_LEAD): ApplyObserved
// stamps the run's CopyObserver on it before Compute.
type copyingComputer interface {
	setCopyObserver(observe CopyObserver)
}

// windowLabel returns the effective output column name for a window:
// windowparams.Label, the one derivation predict and the computed-column
// model read too.
func windowLabel(w *types.Window) string { return windowparams.Label(w) }
