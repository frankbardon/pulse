package exprenv

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
)

// The compile sites, the what of [CompileError]: the runtime build and
// predict both name a site through these, so their messages agree to the
// byte.
const (
	SiteFormula       = "formula"                  // an ATTR_FORMULA expression
	SiteFilter        = "filter"                   // a FILTER_EXPRESSION expression
	SiteTotalArgument = "formula total() argument" // one total(expr) argument
)

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
