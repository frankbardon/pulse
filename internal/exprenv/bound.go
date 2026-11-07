package exprenv

import (
	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
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

// SweepAxisFunc is the sweep-bound fit's winning-axis reader,
// sweep_axis(fit, axis) (internal/model.SweepAxisFunc; internal/
// processing pins the two equal). Unlike coef it is reserved: no
// embedder function may take the name.
const SweepAxisFunc = "sweep_axis"

// ResponseFunc is the media-mix steady-state response reader,
// response(fit, channel, spend) (internal/model.ResponseFunc; internal/
// processing pins the two equal). Like sweep_axis it is reserved.
const ResponseFunc = "response"

// ResultFunc is the Compose slot-value reader, result(slot,
// aggregation) (internal/resultcall.Func): declared only in a Compose
// optimize block's compile, and reserved like sweep_axis.
const ResultFunc = "result"

// LookupFunc is the lookup-table reader's name, lookup(table, keys...),
// declared only when the instance registers a LookupTable.
const LookupFunc = "lookup"

// RangeLabelFunc is the range-table reader's name,
// range_label(table, x[, unmatched]): the label of the registered range
// table's range holding x (numeric half-open, date inclusive epoch
// days), null for a null x, the unmatched label (else null) when no
// range holds it. Unlike lookup it is declared on every runtime compile
// (an unknown or hidden table is a run-time PULSE_RANGE_TABLE_UNKNOWN)
// and, like sweep_axis, reserved: no embedder function may take it.
const RangeLabelFunc = "range_label"

// Bound is the request-bound half of the env, layered over [Options] by
// [Bound.Options] in the order the runtime always registered it: coef,
// then the embedder's functions (one of which may shadow coef or an
// unreserved helper), then lookup, then range_label.
type Bound struct {
	// Coef is coef()'s implementation; nil leaves coef undeclared (the
	// overlay formula env, which binds no models).
	Coef func(args ...any) (any, error)
	// SweepAxis is sweep_axis()'s implementation; nil leaves it
	// undeclared. Declared as (string, string) → any: an axis value is
	// a number (float64) or a string / bool `values` entry.
	SweepAxis func(args ...any) (any, error)
	// Response is response()'s implementation; nil leaves it
	// undeclared. Declared typed — (string, string, float64) → float64,
	// an integer spend admitted as is — so a non-string fit or channel,
	// or a string spend, fails compile.
	Response func(args ...any) (any, error)
	// Result is result()'s implementation (internal/resultcall.Reader);
	// nil leaves it undeclared — every compile but a Compose optimize
	// block's. Declared typed — (string, string) → float64.
	Result func(args ...any) (any, error)
	// Extensions are the embedder's ExprFunctions ([ExtensionFunctions]).
	Extensions []NamedFunc
	// Lookup is lookup()'s implementation; nil when no LookupTable is
	// registered, which leaves lookup undeclared.
	Lookup func(args ...any) (any, error)
	// RangeLabel is range_label()'s implementation; nil leaves it
	// undeclared. Declared typed — (string, any) and (string, any,
	// string), any result (a label string, or nil) — so a non-string
	// table name or unmatched label fails compile.
	RangeLabel func(args ...any) (any, error)
	// Zone is the zone to_day() reads an instant's local day in — the
	// request's inherited zone (request `time_zone`, then
	// Options.DefaultTimeZone), resolved by the engine. Nil keeps the
	// always-on UTC registration; predict leaves it nil (it never runs
	// the function, and the declaration is the same).
	Zone *temporal.Zone
	// Params are the request's normalized parameters
	// (types.Request.Params through [NormalizeParams]), folded as
	// constants wherever an expression reads params.<name> (params.go).
	// Nil declares none; predict and the runtime fold the same values.
	Params Params
}

// Options returns [Options] plus the bound declarations: to_day() over
// [Bound.Zone] when it is not UTC-equivalent (the same typed declaration,
// re-registered with the zone's body, so it replaces the UTC one). coef carries
// its typed overloads — (fit, term) and (fit, term, stat), strings to a
// float64 — so `coef("m", "x") + "a"` fails to compile; the embedder
// functions and lookup are untyped (any arguments, any result).
func (b Bound) Options() []expr.Option {
	// The params fold runs first, so every later patcher and the type
	// check see the constant's own type.
	out := append(paramsOption(b.Params), Options()...)
	if b.Zone != nil && !b.Zone.IsUTC() {
		out = append(out, expr.Function(FuncToDay, ToDayFunc(b.Zone), calendarSignatures[FuncToDay]...))
	}
	if b.Coef != nil {
		out = append(out, expr.Function(CoefFunc, b.Coef,
			new(func(string, string) float64),
			new(func(string, string, string) float64),
		))
	}
	if b.SweepAxis != nil {
		out = append(out, expr.Function(SweepAxisFunc, b.SweepAxis, new(func(string, string) any)))
	}
	if b.Response != nil {
		out = append(out, expr.Function(ResponseFunc, b.Response, new(func(string, string, float64) float64)))
	}
	if b.Result != nil {
		out = append(out, expr.Function(ResultFunc, b.Result, new(func(string, string) float64)))
	}
	out = append(out, ExtensionFunctions(b.Extensions)...)
	if b.Lookup != nil {
		out = append(out, expr.Function(LookupFunc, b.Lookup))
	}
	if b.RangeLabel != nil {
		out = append(out, expr.Function(RangeLabelFunc, b.RangeLabel,
			new(func(string, any) any),
			new(func(string, any, string) any),
		))
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

// DerivedU64Type is the declared type of a derived column that is a u64
// field's alias — an ATTR_FORMULA whose expression IS a bare read of one
// (internal/prevstate.U64Aliases): a [U64], so a comparison over it is
// exact like one over the field; every other use reads the float64 echo
// it holds. Predict and the runtime declare it from the same aliases.
var DerivedU64Type = exprtypes.TypeOf(U64(0))

// VectorType is the declared type of a named vector (Request.Vectors) an
// expression reads: its members as a []float64.
var VectorType = exprtypes.TypeOf([]float64(nil))
