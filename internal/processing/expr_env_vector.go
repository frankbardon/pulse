package processing

import (
	stderrors "errors"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/vecmath"
	"github.com/frankbardon/pulse/internal/vectors"
)

// The reserved vector and scalar-maths built-ins (vsum, dot, cosine,
// vcenter, sqrt, ...) and their registration live in internal/exprenv
// (vector.go), the shared formula environment. What stays here is the
// request-bound half: the build-time static refusal and the reading of
// an evaluation error back into the request's own refusal.

// vectorResultError refuses a vector as an expression's top-level
// result (vectors.VectorResultRefusal).
func vectorResultError(what string) *errors.CodedError {
	return vectors.VectorResultRefusal(what)
}

// vectorStaticRefusal is the compile-time check of the reserved
// built-ins a parse can judge without a row — argument counts, a literal
// vnormalize mode, a known unequal-length dot / dist / cosine pair, a
// vector-returning call as the whole expression: the shared leaf rule
// vectors.ExprStaticIssue, which predict reports through
// vectors.StaticIssues. A source that does not parse returns nil — its
// compile reports the syntax error.
func vectorStaticRefusal(src, what string, lenOf func(string) (int, bool)) error {
	if ce := vectors.ExprStaticIssue(src, what, lenOf); ce != nil {
		return ce
	}
	return nil
}

// exprRefusal returns the request's own refusal inside an expression
// evaluation error — a coef() model refusal, an unequal-length vector
// pair, an invalid vnormalize mode or date_trunc unit, or a decimal
// operator's zero divisor
// or overflow (PULSE_DECIMAL_*) — so the run fails under that
// code rather than the generic PROCESSING_RUNTIME wrapper, and a
// null-input row never swallows it.
func exprRefusal(err error) *errors.CodedError {
	if mr := modelRefusal(err); mr != nil {
		return mr
	}
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		if ce.Code == errors.PROCESSING_VECTOR_LENGTH_MISMATCH || refRefusalCode(ce.Code) ||
			ce.Code == errors.PULSE_DECIMAL_DIVIDE_BY_ZERO || ce.Code == errors.PULSE_DECIMAL_OVERFLOW ||
			(ce.Code == errors.PROCESSING_CONFIG && stderrors.Is(ce.Cause, vecmath.ErrInvalidNormalizeMode)) ||
			(ce.Code == errors.PROCESSING_CONFIG && stderrors.Is(ce.Cause, exprenv.ErrUnknownTruncUnit)) {
			return ce
		}
		e = ce.Cause
	}
	return nil
}
