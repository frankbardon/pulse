package processing

import (
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/temporal"
)

// coef(fit, term[, stat]) — the bound-model reader for ATTR_FORMULA and
// FILTER_EXPRESSION. It resolves fit in the request's bound models (a
// Compose `fits` entry; internal/model.Set) and returns the term's
// coefficient, or its lower / upper confidence bound or standard error,
// at full float64 precision — the very value RegressionResult carries.
// The rule is internal/model.Coef: PULSE_MODEL_UNKNOWN /
// _TERM_UNKNOWN / _STAT_UNKNOWN / _INTERVALS_UNAVAILABLE, the same
// codes predict raises for an all-literal call (coefcall.StaticRefusal,
// which the build also runs). An absent figure is NaN, never 0.
//
// It is registered with the request's set after the built-in set helpers
// and before the embedder's ExprFunctions, so an embedder function named
// "coef" shadows it exactly as one named has_any shadows that helper —
// and the static check stands down for it (coefShadowed).

// exprOptionsWithModels is ExprOptions plus coef() over models and
// to_day() in zone (nil: UTC; exprZone): the built-in set helpers,
// to_day, coef, then the embedder's functions and lookup.
func (r *ExtensionRegistry) exprOptionsWithModels(models *model.Set, zone *temporal.Zone) []expr.Option {
	b := r.exprBound()
	b.Coef = coefBuiltin(models)
	b.Zone = zone
	return b.Options()
}

// coefBuiltin is coef() bound to one request's models (nil: none).
func coefBuiltin(models *model.Set) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) < 2 || len(args) > 3 {
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("coef() takes 2 or 3 arguments (fit, term[, stat]), got %d", len(args)))
		}
		strs := make([]string, len(args))
		for i, a := range args {
			s, ok := a.(string)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("coef() argument %d must be a string, got %T", i+1, a))
			}
			strs[i] = s
		}
		stat := model.StatPoint
		if len(strs) == 3 {
			stat = strs[2]
		}
		v, cerr := model.Coef(models, strs[0], strs[1], stat)
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}
}

// coefShadowed reports whether the embedder registered its own "coef",
// which then owns the name (no static coef() check).
func (r *ExtensionRegistry) coefShadowed() bool {
	if r == nil {
		return false
	}
	for _, fn := range r.ExprFunctions {
		if fn.Name == model.CoefFunc && fn.Fn != nil {
			return true
		}
	}
	return false
}

// coefStaticRefusal is the build-time coef() check over src: the first
// all-literal call that model.CheckCoef refuses, nil otherwise (or when
// an embedder function shadows coef).
func (r *ExtensionRegistry) coefStaticRefusal(src string, models *model.Set) error {
	if r.coefShadowed() || !strings.Contains(src, model.CoefFunc) {
		return nil
	}
	if cerr := coefcall.StaticRefusal(src, models); cerr != nil {
		return cerr
	}
	return nil
}

// modelRefusal returns the bound-model refusal (a PULSE_MODEL_* coded
// error coef() raised) an expression evaluation error carries, or nil.
// Such a refusal is the request's fault, never the row's: it surfaces
// under its own code, and a filter never reads it as an UNKNOWN verdict.
func modelRefusal(err error) *errors.CodedError {
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		if strings.HasPrefix(string(ce.Code), "PULSE_MODEL_") {
			return ce
		}
		e = ce.Cause
	}
	return nil
}
