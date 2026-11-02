package processing

import (
	"fmt"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/tablecall"
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
//   - The built-in `range_label(table, x[, unmatched])`, always
//     (expr_env_range_label.go).
//
// Expressions in cohorts with no lookup tables registered still
// reference `lookup` legally — the function returns
// PULSE_LOOKUP_TABLE_UNKNOWN at evaluation time. The compile-time
// typecheck catches static misuse.
func (r *ExtensionRegistry) ExprOptions() []expr.Option {
	return r.exprBound().Options()
}

// exprBound is the registry's request-bound declaration
// (exprenv.Bound, the one predict declares too): one expr.Function per
// ExprFunctions entry, with any LookupTable the lookup builtin, and
// always range_label (rangeLabeler — an unknown or hidden table is its
// run-time PULSE_RANGE_TABLE_UNKNOWN, never an undeclared name). coef
// is the caller's (exprOptionsWithModels). Nil-receiver-safe.
func (r *ExtensionRegistry) exprBound() exprenv.Bound {
	if r == nil {
		return exprenv.Bound{RangeLabel: newRangeLabeler(nil).call}
	}
	b := exprenv.Bound{
		Extensions: make([]exprenv.NamedFunc, len(r.ExprFunctions)),
		Params:     r.params,
		RangeLabel: newRangeLabeler(r).call,
	}
	for i, fn := range r.ExprFunctions {
		b.Extensions[i] = exprenv.NamedFunc{Name: fn.Name, Fn: fn.Fn}
	}
	if len(r.LookupTables) > 0 {
		b.Lookup = r.lookupBuiltin
	}
	return b
}

// lookupBuiltin is the closure registered as the `lookup` function in
// the expr environment whenever any LookupTables are configured. It
// expects (table_name, key1, key2, ...) all as strings or values that
// can be stringified; a null key is a PULSE_LOOKUP_MISS naming its
// position (details.null_key, 0-based among the keys).
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
		return nil, tablecall.UnknownLookup(tableName)
	}
	keys := make([]string, 0, len(args)-1)
	for i, a := range args[1:] {
		// A null key (a null field binds nil) would stringify to
		// "<nil>" and miss confusingly; name its 0-based position among
		// the key args (the table name is not counted) instead. Runs
		// before a Lookup-func table's callback too.
		if a == nil {
			return nil, tablecall.LookupNullKey(tableName, i)
		}
		keys = append(keys, fmt.Sprintf("%v", a))
	}
	value, found, err := resolveLookup(table, keys)
	if err != nil {
		return nil, errors.WrapCodedError(err, errors.PULSE_LOOKUP_MISS,
			fmt.Sprintf("lookup(%q, %s) failed", tableName, strings.Join(keys, ", ")))
	}
	if !found {
		return nil, tablecall.LookupMiss(tableName, keys)
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
	joined := strings.Join(keys, tablecall.KeySeparator)
	v, ok := table.Rows[joined]
	return v, ok, nil
}
