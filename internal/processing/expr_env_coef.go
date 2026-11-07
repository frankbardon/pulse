package processing

import (
	stderrors "errors"
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
// The implementations (and sweep_axis(), response()) are internal/
// model's CoefBuiltin / SweepAxisBuiltin / ResponseBuiltin, which
// predict's scalar slots fold a loaded model through (model.Folding).
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
	b.Coef = model.CoefBuiltin(models)
	b.SweepAxis = model.SweepAxisBuiltin(models)
	b.Response = model.ResponseBuiltin(models)
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
	b.Coef = model.CoefBuiltin(models)
	b.SweepAxis = model.SweepAxisBuiltin(models)
	b.Response = model.ResponseBuiltin(models)
	return b
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

// coefStaticRefusal is the build-time coef() / sweep_axis() /
// response() check over src: the first literal call that
// model.CheckCoef / CheckSweepAxis / CheckResponse refuses, nil
// otherwise (an embedder function that shadows coef leaves only the
// reserved sweep_axis() and response() calls judged).
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

// mayReadModels reports whether src may call coef(), sweep_axis() or
// response().
func mayReadModels(src string) bool {
	return strings.Contains(src, model.CoefFunc) || strings.Contains(src, model.SweepAxisFunc) || strings.Contains(src, model.ResponseFunc)
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
