// Package scalarslot is the ONE evaluator of a scalar-expression slot:
// a request slot whose value is a number, or an expression string read
// once before anything executes — never per record. An optimize
// variable's lower / upper / start is one; every later scalar slot (a
// window param, an extension param, an aggregate constant) reuses the
// same Compile / Run / Eval, so predict and the runtime judge every slot
// by one rule.
//
// The environment a scalar expression reads:
//
//   - literals and the expression built-ins (hill(), exp(), log(), …);
//   - the request's params as params.<name>, folded into constants at
//     compile (exprenv.Bound.Params);
//   - the request-bound functions the caller's exprenv.Bound declares:
//     coef(fit, term[, stat]) and sweep_axis(fit, axis) over Env.Fits,
//     lookup() / range_label() over the instance's tables, and the
//     embedder's ExprFunctions.
//
// No identifier other than params is in reach: there is no record, and
// no decision variable.
//
// An expression that calls no request-bound function closes over
// literals: Eval runs it even on a declaration-only compile (predict's
// exprenv.Placeholder bound), so predict reports its value and runs the
// slot's range checks statically. One that calls a request-bound
// function is NOT statically resolved: predict reports it (Value.
// Resolved false, Value.Reads naming the functions) and the range
// checks run at runtime, on the value the bound implementations return.
//
// No-execute leaf: it imports types, errors and the no-execute
// expression and model leaves only, never the engine
// (TestScalarSlot_ImportBoundary), so internal/descriptor may import it.
package scalarslot

import (
	stderrors "errors"
	"math"
	"reflect"
	"slices"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/tablecall"
	"github.com/frankbardon/pulse/types"
)

// Kind is what a slot's value must be.
type Kind int

const (
	// Real is any finite number.
	Real Kind = iota
	// Integer is a finite number with no fractional part, at most 2^53
	// in magnitude (exact in a float64).
	Integer
)

// The slot-neutral refusal reasons. A caller names its own reason from
// one (an optimize bound prefixes `bound_`: bound_invalid, …) under its
// own slot code.
const (
	// ReasonInvalid: the expression does not compile (details
	// compile_error) or reads an identifier other than params (details
	// identifier).
	ReasonInvalid = "invalid"
	// ReasonNotNumeric: the expression yields something other than a
	// number — a string, a bool, null (details result_type).
	ReasonNotNumeric = "not_numeric"
	// ReasonNotFinite: the value is NaN or ±Inf (details value, a
	// string: "NaN", "+Inf", "-Inf").
	ReasonNotFinite = "not_finite"
	// ReasonNotInteger: an Integer slot's value has a fractional part
	// or exceeds 2^53 in magnitude (details value).
	ReasonNotInteger = "not_integer"
)

// maxExactInteger is the largest magnitude every integer up to which a
// float64 holds exactly.
const maxExactInteger = 1 << 53

// Env is what a scalar expression is judged against beyond its literals.
type Env struct {
	// Params are the request's normalized params
	// (exprenv.NormalizeParams), folded as constants.
	Params exprenv.Params
	// Fits are the names every all-literal coef() / sweep_axis() call is
	// judged against (model.CheckCoef / CheckSweepAxis); nil binds none,
	// so any such call is PULSE_MODEL_UNKNOWN.
	Fits model.Catalog
	// Tables are the named tables every literal lookup() / range_label()
	// call is judged against (internal/tablecall); nil judges none.
	Tables tablecall.Catalog
	// Extensions are the embedder's ExprFunction names; Lookup reports a
	// registered LookupTable (lookup() is declared).
	Extensions []string
	Lookup     bool
}

// Declared is the declaration-only bound over env — every request-bound
// function an exprenv.Placeholder, never run — that a static judgement
// (predict, a runtime pre-check) compiles against.
func (env Env) Declared() exprenv.Bound {
	b := exprenv.Bound{Coef: exprenv.Placeholder, SweepAxis: exprenv.Placeholder, RangeLabel: exprenv.Placeholder}
	for _, n := range env.Extensions {
		b.Extensions = append(b.Extensions, exprenv.NamedFunc{Name: n, Fn: exprenv.Placeholder})
	}
	if env.Lookup {
		b.Lookup = exprenv.Placeholder
	}
	return b
}

// boundFuncs are the request-bound function names in reach of env: a
// call of any of them makes an expression not statically resolved.
func (env Env) boundFuncs() map[string]bool {
	out := map[string]bool{exprenv.CoefFunc: true, exprenv.SweepAxisFunc: true, exprenv.RangeLabelFunc: true, exprenv.LookupFunc: true}
	for _, n := range env.Extensions {
		out[n] = true
	}
	return out
}

// Refusal is a scalar slot's refusal before the caller locates it: one
// of the Reason* reasons with its message clause and details, or a
// shared coded error (Coded) — a params refusal (PULSE_PARAMS_*), a
// model refusal (PULSE_MODEL_*, a failed fit's own code with Cause
// model.ErrFitFailed), a table refusal, or an evaluation fault — that
// the caller locates keeping its code, message, details and Cause.
type Refusal struct {
	// Reason is one of the Reason* constants; "" when Coded is set.
	Reason string
	// What is the message clause after the slot's subject, e.g.
	// `does not compile: unexpected token`.
	What string
	// Details are the reason's own keys (compile_error, identifier,
	// result_type, value); nil when none.
	Details map[string]any
	// Coded is the shared refusal; nil for a Reason refusal.
	Coded *errors.CodedError
}

// Error renders the refusal for logs; a caller locates it instead.
func (r *Refusal) Error() string {
	if r.Coded != nil {
		return r.Coded.Error()
	}
	return r.Reason + ": " + r.What
}

// Program is one compiled scalar expression.
type Program struct {
	src   string
	kind  Kind
	prog  *vm.Program
	reads []string
}

// Source is the expression as written.
func (p *Program) Source() string { return p.src }

// Reads are the request-bound functions the expression calls, sorted
// (nil when it closes over literals and params).
func (p *Program) Reads() []string { return slices.Clone(p.reads) }

// Static reports whether the expression calls no request-bound
// function: its value is known without any model, table or embedder
// function, so a declaration-only compile may run it.
func (p *Program) Static() bool { return len(p.reads) == 0 }

// Compile judges src as a slot of kind against env, in order: the
// params it reads (exprenv.ParamsRefusal), every free identifier params,
// the compile over bound.Options() (bound.Params replaced by
// env.Params), a numeric result type, every all-literal coef() /
// sweep_axis() call against env.Fits and every literal lookup() /
// range_label() call against env.Tables. bound carries the request-bound
// implementations: env.Declared() for a static judgement, the runtime's
// bound models otherwise.
func Compile(src string, kind Kind, env Env, bound exprenv.Bound) (*Program, *Refusal) {
	if cerr := exprenv.ParamsRefusal(src, env.Params, false); cerr != nil {
		return nil, &Refusal{Coded: cerr}
	}
	if refs, _, parsed := exprident.Free(src); parsed {
		folds := exprenv.FoldsParams(src)
		for _, ref := range refs {
			if folds && ref == exprenv.ParamsName {
				continue
			}
			return nil, &Refusal{Reason: ReasonInvalid,
				What:    "reads " + strconv.Quote(ref) + ", which is not params.<name>; a scalar expression reads only literals, params.<name> and functions",
				Details: map[string]any{"identifier": ref}}
		}
	}
	bound.Params = env.Params
	// An empty declared env makes every name the expression reads or
	// calls a declared one: an undeclared function fails to compile.
	opts := append([]expr.Option{expr.Env(map[string]any{})}, bound.Options()...)
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		reason := exprenv.CompileReason(err)
		return nil, &Refusal{Reason: ReasonInvalid, What: "does not compile: " + reason,
			Details: map[string]any{"compile_error": reason}}
	}
	if t := prog.Node().Type(); t != nil && !numericKind(t.Kind()) {
		return nil, &Refusal{Reason: ReasonNotNumeric, What: "yields " + t.String() + ", not a number",
			Details: map[string]any{"result_type": t.String()}}
	}
	if cerr := coefcall.StaticRefusal(src, env.Fits); cerr != nil {
		return nil, &Refusal{Coded: cerr}
	}
	if cerr := tablecall.Refusal(prog.Node(), env.Tables); cerr != nil {
		return nil, &Refusal{Coded: cerr}
	}
	return &Program{src: src, kind: kind, prog: prog, reads: reads(src, env)}, nil
}

// reads are the request-bound functions src calls, sorted.
func reads(src string, env Env) []string {
	callees, _ := exprident.Callees(src)
	bound := env.boundFuncs()
	var out []string
	for _, c := range callees {
		if bound[c] {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// Run evaluates the program once, with no record, and returns its value:
// a finite number (an Integer slot's exact integer). A function's coded
// error comes back as Coded, unchanged; any other evaluation fault as
// PROCESSING_RUNTIME.
func (p *Program) Run() (float64, *Refusal) {
	out, err := expr.Run(p.prog, nil)
	if err != nil {
		var ce *errors.CodedError
		if stderrors.As(err, &ce) {
			return 0, &Refusal{Coded: ce}
		}
		return 0, &Refusal{Coded: errors.NewCodedError(errors.PROCESSING_RUNTIME, "scalar expression "+strconv.Quote(p.src)+": "+err.Error())}
	}
	v, exact, ok := number(out)
	if !ok {
		typ := "null"
		if out != nil {
			typ = reflect.TypeOf(out).String()
		}
		return 0, &Refusal{Reason: ReasonNotNumeric, What: "yields " + typ + ", not a number",
			Details: map[string]any{"result_type": typ}}
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, NotFinite(v)
	}
	if p.kind == Integer && (!exact || math.Trunc(v) != v || math.Abs(v) > maxExactInteger) {
		return 0, &Refusal{Reason: ReasonNotInteger,
			What:    "yields " + strconv.FormatFloat(v, 'g', -1, 64) + ", not an integer (at most 2^53 in magnitude)",
			Details: map[string]any{"value": v}}
	}
	return v, nil
}

// NotFinite is the ReasonNotFinite refusal of v — also a literal slot
// value's, so a literal and an expression refuse alike.
func NotFinite(v float64) *Refusal {
	s := strconv.FormatFloat(v, 'g', -1, 64)
	return &Refusal{Reason: ReasonNotFinite, What: "yields " + s + ", not a finite number",
		Details: map[string]any{"value": s}}
}

// number is an evaluation result as a float64; exact reports whether an
// integer result converted without loss.
func number(v any) (f float64, exact, ok bool) {
	switch n := v.(type) {
	case float64:
		return n, true, true
	case float32:
		return float64(n), true, true
	case int:
		return float64(n), n >= -maxExactInteger && n <= maxExactInteger, true
	case int8:
		return float64(n), true, true
	case int16:
		return float64(n), true, true
	case int32:
		return float64(n), true, true
	case int64:
		return float64(n), n >= -maxExactInteger && n <= maxExactInteger, true
	case uint:
		return float64(n), n <= maxExactInteger, true
	case uint8:
		return float64(n), true, true
	case uint16:
		return float64(n), true, true
	case uint32:
		return float64(n), true, true
	case uint64:
		return float64(n), n <= maxExactInteger, true
	}
	return 0, false, false
}

// numericKind reports whether an expression result of kind k can be a
// number: a numeric kind, or an untyped (interface) result judged when
// it runs.
func numericKind(k reflect.Kind) bool {
	switch k {
	case reflect.Float64, reflect.Float32, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Interface:
		return true
	}
	return false
}

// Value is one evaluated slot.
type Value struct {
	// Number is the value; meaningful only when Resolved.
	Number float64
	// Resolved reports whether Number is known: always after a run
	// Eval, and on a static judgement only when the expression closes
	// over literals and params.
	Resolved bool
	// Reads are the request-bound functions the expression calls,
	// sorted (nil when none).
	Reads []string
}

// Eval is Compile then, when run is true (the runtime, bound carrying
// the request's implementations) or the program is Static, one Run. On
// a static judgement (run false, bound env.Declared()) an expression
// that calls a request-bound function compiles and is checked but not
// run: Value.Resolved false, its range checks deferred to the runtime.
func Eval(src string, kind Kind, env Env, bound exprenv.Bound, run bool) (Value, *Refusal) {
	p, r := Compile(src, kind, env, bound)
	if r != nil {
		return Value{}, r
	}
	out := Value{Reads: p.Reads()}
	if !run && !p.Static() {
		return out, nil
	}
	v, r := p.Run()
	if r != nil {
		return Value{}, r
	}
	out.Number, out.Resolved = v, true
	return out, nil
}

// InlineNumber is the model.InlineNumberFunc every `models` load hands
// model.LoadFiles: one number of an inline model written as an
// expression, judged and run as a scalar slot over the request's params
// (normalized here) with NO request-bound function in reach — no
// coef(), sweep_axis(), lookup(), range_label() or embedder function,
// so one model's numbers never read another model and the value is
// always known statically: predict resolves it exactly as the runtime
// does. integer asks for an Integer slot (n_obs).
func InlineNumber(params types.Params, src string, integer bool) (float64, *model.InlineNumberRefusal) {
	norm, cerr := exprenv.NormalizeParams(params)
	if cerr != nil {
		return 0, &model.InlineNumberRefusal{Coded: cerr}
	}
	kind := Real
	if integer {
		kind = Integer
	}
	v, r := Eval(src, kind, Env{Params: norm}, exprenv.Bound{}, true)
	if r != nil {
		return 0, &model.InlineNumberRefusal{Reason: r.Reason, What: r.What, Details: r.Details, Coded: r.Coded}
	}
	return v.Number, nil
}
