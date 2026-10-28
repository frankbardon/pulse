package pulse

import (
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/internal/exprreads"
)

// ExpressionReads returns what one expression reads — its free names
// (columns, derived labels, vectors), the request parameters it names
// as `params.<name>`, and the functions it calls — from a parse alone,
// with no instance, cohort or request. An editor wiring a formula to the
// columns it uses, a linter flagging a filter that reads an unknown
// field, or an agent planning which columns a draft depends on reads the
// same names the engine would bind, without re-implementing expr-lang
// scoping (let bindings, closures, member chains, $env, string
// literals). The rules are on [descriptor.ExprReads].
//
// It takes the expression source as the engine compiles it: a templated
// `{{token}}` must be rendered first. A source that does not parse
// returns an empty read set and a *errors.CodedError
// PULSE_FORMULA_COMPILE_ERROR (details compile_error, expression); it
// never panics.
func ExpressionReads(src string) (descriptor.ExprReads, error) {
	r, ce := exprreads.Read(src)
	if ce != nil {
		return r, ce
	}
	return r, nil
}
