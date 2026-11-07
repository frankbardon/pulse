// Package winformula is the one compile of a WIN_FORMULA window's
// params.expression, shared by the window engine
// (internal/processing/window) and predict's field-reference walk
// (internal/descriptor), so both refuse the same expression with the
// same code, sentence and details, and the engine evaluates exactly the
// program predict type-checked.
//
// The env. A WIN_FORMULA runs on the output rows (grouped rows, the
// single aggregated row, or the record rows when nothing aggregates),
// whose cells carry no declared type: a group key column may hold a
// label or a number, an aggregation a float or an integer. So every
// name the expression reads (Reads) is declared `any` on both sides —
// predict has no row to type it from, and the engine compiles before
// the first row. Which names exist is the field-reference walk's
// judgement (the row's columns plus every EARLIER window's label); the
// compile only types what it is given. The request-bound half is the
// caller's exprenv.Bound: the request's params, the embedder's
// ExprFunctions, lookup() and range_label(). coef(), sweep_axis(),
// response() and total() are never declared (an unknown function on
// both sides), and ref() — always registered as a built-in — is
// refused here: a window row is no record, it carries no reference key.
//
// The result. The checker's result type must be a number, or
// unknowable before a row (`any`); a bool, a string, a list or a map is
// PULSE_FORMULA_COMPILE_ERROR at site "window formula" with expr-lang's
// own "expected" wording. A row's result is judged again when it runs
// (Program.Eval): nil reads null, a non-finite number reads null (an
// undefined figure), any other non-number fails the run as
// PROCESSING_RUNTIME.
//
// Nulls. A row where any column the expression reads is null, absent or
// NaN reads null — the expression is not run for it (Program.Eval's
// caller judges the cells). So `x ?? 0` never sees a nil: a null input
// is a null output.
//
// Leaf: expr-lang + internal/exprenv / exprident + the public errors
// package. Never imports the engine.
package winformula

import (
	stderrors "errors"
	"fmt"
	"math"
	"reflect"

	"github.com/expr-lang/expr"
	exprtypes "github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
	"github.com/frankbardon/pulse/internal/exprslots"
)

// Param is the WIN_FORMULA params key carrying the expression.
const Param = exprslots.WindowFormulaParam

// Site is the compile site WIN_FORMULA's errors name.
const Site = exprenv.SiteWindowFormula

// Reads returns the column names src reads, in first-use order: its
// free identifiers (exprident.Free, `$env.name` members included),
// less the `params` identifier when every use of it is a parameter read
// (exprenv.FoldsParams — the fold makes it a constant, no column).
// parsed is false when src does not parse (the compile then reports the
// syntax error).
func Reads(src string) (names []string, parsed bool) {
	free, _, parsed := exprident.Free(src)
	if !parsed {
		return nil, false
	}
	folds := exprenv.FoldsParams(src)
	out := make([]string, 0, len(free))
	for _, n := range free {
		if folds && n == exprenv.ParamsName {
			continue
		}
		out = append(out, n)
	}
	return out, true
}

// Program is one compiled WIN_FORMULA expression.
type Program struct {
	src   string
	prog  *vm.Program
	reads []string
}

// Source returns the expression.
func (p *Program) Source() string { return p.src }

// Reads returns the column names the program reads (Reads).
func (p *Program) Reads() []string { return p.reads }

// Compile compiles src: every name it reads declared `any`, bound's
// declarations layered on top (exprenv.Bound.Options). A compile
// failure, a ref() call or a result type that is no number is
// PULSE_FORMULA_COMPILE_ERROR at Site (exprenv.CompileError), so
// predict and the engine word it alike. paramsColumn reports that
// `params` is a column the expression may read (the field-reference
// walk knows; the engine's factory, which the walk already judged,
// passes false): a params read is then PULSE_PARAMS_FIELD_COLLISION,
// an undeclared one PULSE_PARAMS_UNDECLARED (exprenv.ParamsRefusal).
func Compile(src string, bound exprenv.Bound, paramsColumn bool) (*Program, *errors.CodedError) {
	if ce := exprenv.ParamsRefusal(src, bound.Params, paramsColumn); ce != nil {
		return nil, ce
	}
	reads, parsed := Reads(src)
	decl := exprtypes.Map{}
	if parsed {
		for _, n := range reads {
			decl[n] = exprtypes.Any
		}
		if callee := exprident.FirstCallee(src, func(n string) bool { return n == exprenv.RefFunc }); callee != "" {
			return nil, exprenv.CompileError(Site, src, stderrors.New("ref() reads a reference vector by a record's key; a window formula reads only its row's columns"))
		}
	}
	opts := append([]expr.Option{expr.Env(decl)}, bound.Options()...)
	prog, err := expr.Compile(src, opts...)
	if err != nil {
		return nil, exprenv.CompileError(Site, src, err)
	}
	if ce := resultRefusal(src, prog); ce != nil {
		return nil, ce
	}
	return &Program{src: src, prog: prog, reads: reads}, nil
}

// VM returns the compiled program (the field-reference walk judges the
// literal lookup() / range_label() calls on it).
func (p *Program) VM() *vm.Program { return p.prog }

// resultRefusal is the compile-time result check: a number or `any`
// (unknowable before a row), or exprenv.Decimal (an exact decimal
// result downcasts to the float column). Anything else is refused.
func resultRefusal(src string, prog *vm.Program) *errors.CodedError {
	t := prog.Node().Type()
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Interface,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return nil
	}
	if t == reflect.TypeOf(exprenv.Decimal{}) {
		return nil
	}
	return exprenv.CompileError(Site, src, fmt.Errorf("expected number, but got %s", t))
}

// Eval runs the program over env (the row's cells for Reads, none null:
// the caller has already made a row with a null input read null). ok is
// false for a null result: nil, NaN or ±Inf. A non-number result is
// PROCESSING_RUNTIME; an evaluation fault is a lookup() / range_label()
// table's own code when one raised it, else PROCESSING_RUNTIME.
func (p *Program) Eval(env map[string]any) (v float64, ok bool, err error) {
	out, err := expr.Run(p.prog, env)
	if err != nil {
		if ce := tableRefusal(err); ce != nil {
			return 0, false, ce
		}
		return 0, false, errors.WrapCodedError(err, errors.PROCESSING_RUNTIME,
			"evaluating "+Site+" expression: "+p.src)
	}
	f, isNum := number(out)
	if !isNum {
		if out == nil {
			return 0, false, nil
		}
		return 0, false, errors.NewCodedErrorWithDetails(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("%s expression %q returned %T on a row; a window formula must return a number", Site, p.src, out),
			map[string]any{"expression": p.src, "returned_type": fmt.Sprintf("%T", out)})
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, nil
	}
	return f, true, nil
}

// number converts a numeric result to float64.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case exprenv.Decimal:
		return x.Float64(), true
	}
	return 0, false
}

// tableRefusal returns the lookup() / range_label() fault inside an
// evaluation error, under its own code (as an ATTR_FORMULA run reports
// it), or nil.
func tableRefusal(err error) *errors.CodedError {
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		switch ce.Code {
		case errors.PULSE_LOOKUP_MISS, errors.PULSE_LOOKUP_TABLE_UNKNOWN,
			errors.PULSE_RANGE_TABLE_UNKNOWN, errors.PULSE_RANGE_TABLE_KIND_MISMATCH:
			return ce
		}
		e = ce.Cause
	}
	return nil
}
