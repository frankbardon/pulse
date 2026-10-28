// Package exprreads is the parse-only read set of one expression: the
// free names it reads, the request parameters it names and the functions
// it calls, returned as descriptor.ExprReads. It composes the engine's
// own walks (internal/exprident for free names and callees,
// internal/exprenv.ReadParams for `params.<name>`) and applies the same
// params fold the compile sites apply (exprenv.FoldsParams), so the set
// it reports is the set the engine binds.
//
// It never compiles or runs an expression: a leaf over the expr-lang
// parser, descriptor, errors, exprident and exprenv, so a no-execute
// caller (the root facade, internal/descriptor's request read map) can
// use it (TestExprReads_ImportBoundary).
package exprreads

import (
	"strconv"

	exprparser "github.com/expr-lang/expr/parser"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/exprident"
)

// Read returns what src reads. A src that does not parse returns a
// zero-length read set and PULSE_FORMULA_COMPILE_ERROR (details
// compile_error, expression) — the code a compile of it raises — never a
// panic.
func Read(src string) (descriptor.ExprReads, *errors.CodedError) {
	empty := descriptor.ExprReads{Vars: []string{}, Params: []string{}, Funcs: []string{}}
	if _, err := exprparser.Parse(src); err != nil {
		reason := exprenv.CompileReason(err)
		return empty, &errors.CodedError{
			Code:    errors.PULSE_FORMULA_COMPILE_ERROR,
			Message: "parsing expression " + strconv.Quote(src) + ": " + reason,
			Details: map[string]any{"compile_error": reason, "expression": src},
			Cause:   err,
		}
	}
	names, usesEnv, _ := exprident.Free(src)
	funcs, _ := exprident.Callees(src)
	uses, _ := exprenv.ReadParams(src)
	out := empty
	out.UsesEnv = usesEnv
	// The compile sites' fold: when every `params` is a parameter read,
	// `params` is no env name (internal/processing expr_program.go,
	// internal/descriptor predict_formula.go).
	folds := exprenv.FoldsParams(src)
	for _, n := range names {
		if folds && n == exprenv.ParamsName {
			continue
		}
		out.Vars = append(out.Vars, n)
	}
	out.Params = append(out.Params, uses.Names...)
	out.Funcs = append(out.Funcs, funcs...)
	return out, nil
}
