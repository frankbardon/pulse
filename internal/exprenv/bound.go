package exprenv

import (
	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/errors"
)

// The request-bound declarations. A runtime compile (internal/processing)
// and predict's no-engine compile (internal/descriptor) declare these
// through the one [Bound.Options] and the types below, so the two
// type-check every expression against the same env and cannot drift; only
// the implementations differ — the runtime binds the request's models,
// lookup tables and embedder functions, predict a [Placeholder] it never
// runs.

// CoefFunc is the bound-model reader's name, coef(fit, term[, stat])
// (internal/model.CoefFunc; internal/processing pins the two equal).
const CoefFunc = "coef"

// LookupFunc is the lookup-table reader's name, lookup(table, keys...),
// declared only when the instance registers a LookupTable.
const LookupFunc = "lookup"

// Bound is the request-bound half of the env, layered over [Options] by
// [Bound.Options] in the order the runtime always registered it: coef,
// then the embedder's functions (one of which may shadow coef or an
// unreserved helper), then lookup.
type Bound struct {
	// Coef is coef()'s implementation; nil leaves coef undeclared (the
	// overlay formula env, which binds no models).
	Coef func(args ...any) (any, error)
	// Extensions are the embedder's ExprFunctions ([ExtensionFunctions]).
	Extensions []NamedFunc
	// Lookup is lookup()'s implementation; nil when no LookupTable is
	// registered, which leaves lookup undeclared.
	Lookup func(args ...any) (any, error)
}

// Options returns [Options] plus the bound declarations. coef carries
// its typed overloads — (fit, term) and (fit, term, stat), strings to a
// float64 — so `coef("m", "x") + "a"` fails to compile; the embedder
// functions and lookup are untyped (any arguments, any result).
func (b Bound) Options() []expr.Option {
	out := Options()
	if b.Coef != nil {
		out = append(out, expr.Function(CoefFunc, b.Coef,
			new(func(string, string) float64),
			new(func(string, string, string) float64),
		))
	}
	out = append(out, ExtensionFunctions(b.Extensions)...)
	if b.Lookup != nil {
		out = append(out, expr.Function(LookupFunc, b.Lookup))
	}
	return out
}

// Placeholder is the implementation a declaration-only compile (predict)
// binds for a request-bound function: it type-checks like the real one
// (every request-bound function but coef is untyped; coef's overloads
// are [Bound.Options]') and is never run — calling it is an internal
// fault, never a figure.
func Placeholder(args ...any) (any, error) {
	_ = args
	return nil, errors.NewCodedError(errors.PROCESSING_INTERNAL,
		"expression function placeholder called: a declaration-only compile is never run")
}

// DerivedType is the declared type of a derived column an expression
// names — an earlier attribute's label or a feature output: every one
// is a float64 (a null one is absent, exactly like a null field).
// Predict declares such a name from the slot that produces it; the
// runtime's deferred first-record compile declares it from the record,
// and a derived column null on that record from this type.
var DerivedType = exprtypes.TypeOf(float64(0))

// VectorType is the declared type of a named vector (Request.Vectors) an
// expression reads: its members as a []float64.
var VectorType = exprtypes.TypeOf([]float64(nil))
