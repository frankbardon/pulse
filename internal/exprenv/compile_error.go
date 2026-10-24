package exprenv

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/expr-lang/expr/vm"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprslots"
)

// The compile sites, the what of [CompileError]: the runtime build and
// predict both name a site through these, so their messages agree to the
// byte. Each is the shared expression-slot list's (internal/exprslots),
// which the request-template renderer reads to refuse a `{{token}}`
// spliced into an expression; a new site is declared there first
// (TestTemplateExprSplice_CompileSitesShared).
const (
	SiteFormula       = exprslots.SiteFormula       // an ATTR_FORMULA expression
	SiteFilter        = exprslots.SiteFilter        // a FILTER_EXPRESSION expression
	SiteTotalArgument = exprslots.SiteTotalArgument // one total(expr) argument
	SiteHoldout       = exprslots.SiteHoldout       // a RegressionSpec.Holdout predicate
)

// ResultRefusal is the compile-time result-type check of site: nil, or
// the CompileError an expression whose result the checker types as
// something its site cannot use raises — the runtime build (and a
// deferred first-record compile) and predict both call it on the
// compiled program, so both refuse the same source with the same code
// and sentence, before any row. A filter (SiteFilter: FILTER_EXPRESSION
// and every expression built through it, FEAT_TARGET_ENCODE's fit_on
// included) must yield a bool: "x + 1" is refused "expected bool, but
// got float64" (expr-lang's own AsBool wording), which the run used to
// discover only on the first row, as PROCESSING_RUNTIME. A result the
// checker cannot type (`any`: an extension function, a ref() lookup, a
// ternary mixing types) and a vector ([]float64 — the shared vector
// result refusal) stay judged per row. expr.AsBool itself is not used:
// it also casts an untyped result at run time, which would move those
// per-row refusals' codes. A regression holdout (SiteHoldout) is a
// predicate too and is judged exactly like a filter.
func ResultRefusal(site, src string, prog *vm.Program) *errors.CodedError {
	if (site != SiteFilter && site != SiteHoldout) || prog == nil {
		return nil
	}
	t := prog.Node().Type()
	if t == nil {
		return nil
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Interface:
		return nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Float64 {
			return nil
		}
	}
	return CompileError(site, src, fmt.Errorf("expected bool, but got %s", t))
}

// CompileError is the coded error an expression that expr-lang cannot
// compile raises — PULSE_FORMULA_COMPILE_ERROR, at every compile site
// (ATTR_FORMULA, FILTER_EXPRESSION, a total() argument; at build or on
// a deferred first-record compile) and, through this one constructor,
// at predict. what names the site ("formula", "filter", "formula
// total() argument"), src is the expression, err expr-lang's compile
// error, kept as the cause.
//
// The reason is the first line of expr-lang's error (the message and
// its line:column; the source snippet that follows is the expression
// itself, already under details.expression). It rides the message —
// CodedError.Error() prints code and message only — and
// details.compile_error.
func CompileError(what, src string, err error) *errors.CodedError {
	reason := CompileReason(err)
	return &errors.CodedError{
		Code:    errors.PULSE_FORMULA_COMPILE_ERROR,
		Message: "compiling " + what + " expression " + strconv.Quote(src) + ": " + reason,
		Details: map[string]any{
			"compile_error": reason,
			"expression":    src,
		},
		Cause: err,
	}
}

// CompileReason is the one-line reason of an expr-lang compile error:
// its first line, trimmed.
func CompileReason(err error) string {
	if err == nil {
		return ""
	}
	line, _, _ := strings.Cut(err.Error(), "\n")
	return strings.TrimSpace(line)
}
