package processing

import (
	stderrors "errors"
	"fmt"
	"slices"
	"strings"

	"github.com/expr-lang/expr"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/internal/model/coefcall"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
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
	b.SweepAxis = sweepAxisBuiltin(models)
	b.Zone = zone
	return b.Options()
}

// ExprBoundWithModels is the request-bound expression declarations a
// Compose optimize block compiles against at runtime: the embedder's
// ExprFunctions, lookup() over the LookupTables and coef() over models
// (the batch's fits plus the block's own loaded `models`). The block's
// rule set (internal/optblock) sets the params.
func (r *ExtensionRegistry) ExprBoundWithModels(models *model.Set) exprenv.Bound {
	b := r.exprBound()
	b.Coef = coefBuiltin(models)
	b.SweepAxis = sweepAxisBuiltin(models)
	return b
}

// sweepAxisBuiltin is sweep_axis(fit, axis) bound to one request's
// models (nil: none) — internal/model.SweepAxis: the sweep-bound fit's
// winning axis value, refused like coef() (PULSE_MODEL_UNKNOWN /
// _TERM_UNKNOWN reasons not_sweep_bound / axis_unknown, a failed fit's
// own code).
func sweepAxisBuiltin(models *model.Set) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) != 2 {
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("sweep_axis() takes 2 arguments (fit, axis), got %d", len(args)))
		}
		strs := make([]string, 2)
		for i, a := range args {
			s, ok := a.(string)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("sweep_axis() argument %d must be a string, got %T", i+1, a))
			}
			strs[i] = s
		}
		v, cerr := model.SweepAxis(models, strs[0], strs[1])
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}
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

// coefStaticRefusal is the build-time coef() / sweep_axis() check over
// src: the first all-literal call that model.CheckCoef /
// CheckSweepAxis refuses, nil otherwise (an embedder function that
// shadows coef leaves only the sweep_axis() calls judged).
func (r *ExtensionRegistry) coefStaticRefusal(src string, models *model.Set) error {
	if !mayReadModels(src) {
		return nil
	}
	check := coefcall.StaticRefusal
	if r.coefShadowed() {
		check = coefcall.SweepAxisRefusal
	}
	if cerr := check(src, models); cerr != nil {
		return cerr
	}
	return nil
}

// mayReadModels reports whether src may call coef() or sweep_axis().
func mayReadModels(src string) bool {
	return strings.Contains(src, model.CoefFunc) || strings.Contains(src, model.SweepAxisFunc)
}

// failedCoefFits are the failed fits (model.Set.FailedOfKind — a
// Compose regression fit that failed on its data, a sweep-bound one
// with an empty ranking included) src names in a literal coef() call
// (unless an embedder function shadows coef) or sweep_axis() call,
// each once in first-call order with its error; nil when there is
// none. A non-literal fit name is not judged here: a row that
// evaluates it to a failed fit is refused with the fit's own code
// (modelRefusal).
func (r *ExtensionRegistry) failedCoefFits(src string, models *model.Set) []failedCoefFit {
	if models == nil || !mayReadModels(src) {
		return nil
	}
	var out []failedCoefFit
	for _, name := range coefcall.LiteralFits(src, !r.coefShadowed()) {
		se, failed := models.FailedOfKind(name, types.FitKindRegression)
		if !failed || slices.ContainsFunc(out, func(f failedCoefFit) bool { return f.name == name }) {
			continue
		}
		out = append(out, failedCoefFit{name: name, err: se})
	}
	return out
}

// failedCoefFit is one failed fit a literal coef() call names.
type failedCoefFit struct {
	name string
	err  *types.SlotError
}

// coefFailedRefusal is the build-time refusal of an expression that
// cannot degrade (FILTER_EXPRESSION: a filter's verdict is never null)
// naming a failed fit in a literal coef() call: the first such fit's
// own code plus details.fit (model.FailedFitRefusal) — raised before
// any row, so it fires even when no row reaches the filter.
func (r *ExtensionRegistry) coefFailedRefusal(src string, models *model.Set) error {
	if fs := r.failedCoefFits(src, models); len(fs) > 0 {
		return model.FailedFitRefusal(fs[0].name, fs[0].err)
	}
	return nil
}

// modelRefusal returns the bound-model refusal (a PULSE_MODEL_* coded
// error coef() raised, or its refusal of a failed fit — the fit's own
// code, model.ErrFitFailed) an expression evaluation error carries, or
// nil.
// Such a refusal is the request's fault, never the row's: it surfaces
// under its own code, and a filter never reads it as an UNKNOWN verdict.
func modelRefusal(err error) *errors.CodedError {
	for e := err; e != nil; {
		var ce *errors.CodedError
		if !stderrors.As(e, &ce) {
			return nil
		}
		if strings.HasPrefix(string(ce.Code), "PULSE_MODEL_") || stderrors.Is(ce.Cause, model.ErrFitFailed) {
			return ce
		}
		e = ce.Cause
	}
	return nil
}
