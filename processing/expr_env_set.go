package processing

import (
	"fmt"
	"sort"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/errors"
)

// Built-in set expression helpers for ATTR_FORMULA / FILTER_EXPRESSION.
//
// Record.AllValues() resolves a set field's bitmask into a sorted
// []string of dictionary labels, so these helpers consume []string
// operands. They are always available to expr-lang expressions
// regardless of whether any extensions are registered.
//
// The helpers favor set semantics over slice semantics: ordering of
// the returned slice is deterministic (alphabetical) and duplicate
// labels are de-duplicated.

// setExprOptions returns the always-on set helper Options. Call sites
// prepend this to extension-supplied options so the helpers are
// uniformly visible.
func setExprOptions() []expr.Option {
	return []expr.Option{
		expr.Function("contains", setContainsBuiltin),
		expr.Function("has_any", setHasAnyBuiltin),
		expr.Function("has_all", setHasAllBuiltin),
		expr.Function("has_none", setHasNoneBuiltin),
		expr.Function("popcount", setPopcountBuiltin),
		expr.Function("set_union", setUnionBuiltin),
		expr.Function("set_intersect", setIntersectBuiltin),
		expr.Function("set_diff", setDiffBuiltin),
		expr.Function("set_xor", setXorBuiltin),
	}
}

// argToStringSet coerces a single argument into a string-set
// representation. Accepts []string (set field surfaced by
// Record.AllValues), []any (expr-lang's untyped slice), or a single
// string (treated as a singleton). nil returns an empty set.
func argToStringSet(a any) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	switch v := a.(type) {
	case nil:
		return out, nil
	case []string:
		for _, s := range v {
			out[s] = struct{}{}
		}
	case []any:
		for i, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("set helper: slice element %d is %T, not string", i, e))
			}
			out[s] = struct{}{}
		}
	case string:
		out[v] = struct{}{}
	default:
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("set helper: argument %T is not a set / string / []string", a))
	}
	return out, nil
}

// labelsFromArgs flattens variadic string args into a slice. Used by
// has_any / has_all / has_none which take (set, ...labels).
func labelsFromArgs(args []any) ([]string, error) {
	out := make([]string, 0, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		case []any:
			for j, e := range v {
				s, ok := e.(string)
				if !ok {
					return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
						fmt.Sprintf("set helper: label arg %d[%d] is %T, not string", i, j, e))
				}
				out = append(out, s)
			}
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("set helper: label arg %d is %T, not string", i, a))
		}
	}
	return out, nil
}

// sortedSetSlice materializes a string set as a deterministic
// alphabetical slice so expr expressions consuming the result see a
// stable iteration order.
func sortedSetSlice(s map[string]struct{}) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// contains(set, "label") → bool — single-label membership test.
func setContainsBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("contains() takes 2 arguments (set, label); got %d", len(args)))
	}
	set, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	label, ok := args[1].(string)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("contains(): label argument must be string, got %T", args[1]))
	}
	_, hit := set[label]
	return hit, nil
}

// has_any(set, "a", "b", ...) → bool — at least one label present.
func setHasAnyBuiltin(args ...any) (any, error) {
	if len(args) < 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"has_any() takes at least 2 arguments (set, label, ...)")
	}
	set, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	labels, err := labelsFromArgs(args[1:])
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if _, ok := set[l]; ok {
			return true, nil
		}
	}
	return false, nil
}

// has_all(set, "a", "b", ...) → bool — every label present.
func setHasAllBuiltin(args ...any) (any, error) {
	if len(args) < 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"has_all() takes at least 2 arguments (set, label, ...)")
	}
	set, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	labels, err := labelsFromArgs(args[1:])
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if _, ok := set[l]; !ok {
			return false, nil
		}
	}
	return true, nil
}

// has_none(set, "a", "b", ...) → bool — no label present.
func setHasNoneBuiltin(args ...any) (any, error) {
	if len(args) < 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"has_none() takes at least 2 arguments (set, label, ...)")
	}
	set, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	labels, err := labelsFromArgs(args[1:])
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if _, ok := set[l]; ok {
			return false, nil
		}
	}
	return true, nil
}

// popcount(set) → int — number of distinct labels in the set.
func setPopcountBuiltin(args ...any) (any, error) {
	if len(args) != 1 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("popcount() takes 1 argument (set); got %d", len(args)))
	}
	set, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	return len(set), nil
}

// set_union(a, b) → []string — union of two sets (deduplicated, sorted).
func setUnionBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("set_union() takes 2 arguments; got %d", len(args)))
	}
	a, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	b, err := argToStringSet(args[1])
	if err != nil {
		return nil, err
	}
	for k := range b {
		a[k] = struct{}{}
	}
	return sortedSetSlice(a), nil
}

// set_intersect(a, b) → []string — intersection (sorted).
func setIntersectBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("set_intersect() takes 2 arguments; got %d", len(args)))
	}
	a, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	b, err := argToStringSet(args[1])
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(a))
	for k := range a {
		if _, ok := b[k]; ok {
			out[k] = struct{}{}
		}
	}
	return sortedSetSlice(out), nil
}

// set_diff(a, b) → []string — elements in a not in b (sorted).
func setDiffBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("set_diff() takes 2 arguments; got %d", len(args)))
	}
	a, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	b, err := argToStringSet(args[1])
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(a))
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return sortedSetSlice(out), nil
}

// set_xor(a, b) → []string — symmetric difference (sorted).
func setXorBuiltin(args ...any) (any, error) {
	if len(args) != 2 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("set_xor() takes 2 arguments; got %d", len(args)))
	}
	a, err := argToStringSet(args[0])
	if err != nil {
		return nil, err
	}
	b, err := argToStringSet(args[1])
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		if _, ok := b[k]; !ok {
			out[k] = struct{}{}
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			out[k] = struct{}{}
		}
	}
	return sortedSetSlice(out), nil
}
