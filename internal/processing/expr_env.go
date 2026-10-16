package processing

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
)

// ExprOptions returns the expr-lang Option slice an ExtensionRegistry
// contributes to ATTR_FORMULA / FILTER_EXPRESSION compilation. A nil
// receiver still returns the built-in set helpers; non-nil receivers
// layer their custom ExprFunctions and (when LookupTables are
// registered) the `lookup` builtin on top.
//
// Contributions:
//   - set helpers (`has_any`, `has_all`, `has_none`,
//     `popcount`, `set_union`, `set_intersect`, `set_diff`, `set_xor`)
//     are always registered. Record.AllValues() resolves set fields to
//     []string of labels, so the helpers consume []string operands.
//   - One expr.Function per ExprFunctions entry, registered under its
//     declared Name.
//   - The built-in `lookup(table, keys...)` function when at least one
//     LookupTable is registered.
//
// Expressions in cohorts with no lookup tables registered still
// reference `lookup` legally — the function returns
// PULSE_LOOKUP_TABLE_UNKNOWN at evaluation time. The compile-time
// typecheck catches static misuse.
func (r *ExtensionRegistry) ExprOptions() []expr.Option {
	return append(exprenv.Options(), r.embedderExprOptions()...)
}

// embedderExprOptions are the registry's own contributions: one
// expr.Function per ExprFunctions entry and, with any LookupTable, the
// lookup builtin. Nil-receiver-safe.
func (r *ExtensionRegistry) embedderExprOptions() []expr.Option {
	if r == nil {
		return nil
	}
	fns := make([]exprenv.NamedFunc, len(r.ExprFunctions))
	for i, fn := range r.ExprFunctions {
		fns[i] = exprenv.NamedFunc{Name: fn.Name, Fn: fn.Fn}
	}
	out := exprenv.ExtensionFunctions(fns)
	if len(r.LookupTables) > 0 {
		out = append(out, expr.Function("lookup", r.lookupBuiltin))
	}
	return out
}

// lookupBuiltin is the closure registered as the `lookup` function in
// the expr environment whenever any LookupTables are configured. It
// expects (table_name, key1, key2, ...) all as strings or values that
// can be stringified.
func (r *ExtensionRegistry) lookupBuiltin(args ...any) (any, error) {
	if len(args) == 0 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			"lookup() requires at least 1 argument (table name)")
	}
	tableName, ok := args[0].(string)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("lookup() table argument must be string, got %T", args[0]))
	}
	table, ok := r.LookupTables[tableName]
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_LOOKUP_TABLE_UNKNOWN,
			fmt.Sprintf("lookup table %q is not registered", tableName),
			map[string]any{"table": tableName},
		)
	}
	keys := make([]string, 0, len(args)-1)
	for _, a := range args[1:] {
		keys = append(keys, fmt.Sprintf("%v", a))
	}
	value, found, err := resolveLookup(table, keys)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_LOOKUP_MISS,
			fmt.Sprintf("lookup(%q, %s) failed", tableName, strings.Join(keys, ", ")))
	}
	if !found {
		return nil, errors.NewCodedErrorWithDetails(
			errors.PULSE_LOOKUP_MISS,
			fmt.Sprintf("lookup(%q, %s) returned no result", tableName, strings.Join(keys, ", ")),
			map[string]any{"table": tableName, "keys": keys},
		)
	}
	return value, nil
}

// resolveLookup dispatches to whichever accessor the table provided.
// Rows-backed tables join the keys with "|" before indexing; embedders
// can override the join policy by switching to the Lookup func.
func resolveLookup(table LookupTable, keys []string) (float64, bool, error) {
	if table.Lookup != nil {
		return table.Lookup(keys...)
	}
	joined := strings.Join(keys, "|")
	v, ok := table.Rows[joined]
	return v, ok, nil
}
