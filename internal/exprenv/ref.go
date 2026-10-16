package exprenv

import (
	"fmt"
	"strconv"

	"github.com/frankbardon/pulse/errors"
)

// ref("table", key): a registered reference vector
// (pulse.Options.Extensions.ReferenceVectors) as a numeric vector.
//
// The table rules are internal/vectors' (reference.go,
// reference_expr.go); the request-bound half — the build's static
// refusal and the expr.Patch visitor that gives every ref() call a
// constant third argument, its resolved [RefPlan] — is
// internal/processing's (expr_env_reference.go). This file holds only
// the registered built-in, so the env compiles a ref() call without the
// engine.
//
// The plan is supplied by the patcher only, so an expression cannot
// spell one: a three-argument ref() in a request is refused by the
// arity check (vectors.ExprStaticIssue) before the patch runs.

// RefPlan is the constant third argument the ref() patcher appends: a
// call's resolved table (and alignment). Resolve returns the key's
// profile, or the coded key-unknown refusal.
type RefPlan interface {
	Resolve(key string) (any, error)
}

// RefReasonTableUnknown is the PULSE_VECTOR_REFERENCE_UNKNOWN
// details.reason of a table name no registration carries
// (vectors.ReferenceReasonTableUnknown).
const RefReasonTableUnknown = "table_unknown"

// RefTableUnknown is the PULSE_VECTOR_REFERENCE_UNKNOWN refusal of a
// table name no registration carries, valid the registered names
// (vectors.ReferenceTables.Table raises it through here).
func RefTableUnknown(name string, valid []string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
		"reference vector table "+strconv.Quote(name)+" is not registered (Options.Extensions.ReferenceVectors)",
		map[string]any{"reason": RefReasonTableUnknown, "table": name, "valid": valid})
}

// refBuiltin is the reserved ref() implementation. The patched form
// reads its plan; an unpatched call (an expression compiled outside an
// expression program, where nothing is registered) is refused.
func refBuiltin(args ...any) (any, error) {
	if len(args) != 3 {
		if len(args) != 2 {
			return nil, builtinArityError(RefFunc, 2, len(args))
		}
		table, _ := args[0].(string)
		return nil, RefTableUnknown(table, []string{})
	}
	plan, ok := args[2].(RefPlan)
	if !ok {
		return nil, builtinArityError(RefFunc, 2, len(args))
	}
	if args[1] == nil {
		// A null key field: the row's null input, not a refusal.
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME, "ref(): key is null")
	}
	key, ok := args[1].(string)
	if !ok {
		key = fmt.Sprintf("%v", args[1])
	}
	return plan.Resolve(key)
}
