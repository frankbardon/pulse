package model

import (
	"fmt"
	"math"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CoefFunc is the expression function that reads a bound model's
// figures: coef(fit, term[, stat]) in ATTR_FORMULA and FILTER_EXPRESSION.
const CoefFunc = "coef"

// The stat values coef() reads (its optional third argument).
const (
	// StatPoint is the coefficient (REG_BAYES_LINEAR: the posterior
	// mean). The default.
	StatPoint = "point"
	// StatLower is the coefficient's lower confidence bound.
	StatLower = "lower"
	// StatUpper is the coefficient's upper confidence bound.
	StatUpper = "upper"
	// StatSE is the coefficient's standard error.
	StatSE = "se"
)

// CoefStats are the stats coef() reads, in documentation order.
func CoefStats() []string { return []string{StatPoint, StatLower, StatUpper, StatSE} }

// CheckCoef is the static rule over one coef(fit, term, stat) call —
// what can be judged from the names alone, so the runtime (every call)
// and predict (every all-literal call) refuse the same call the same
// way. In order: fit resolves in cat (PULSE_MODEL_UNKNOWN), term is
// InterceptKey or one of the fit's terms (PULSE_MODEL_TERM_UNKNOWN),
// stat is one of CoefStats (PULSE_MODEL_STAT_UNKNOWN). The first fault
// wins. Whether the fit reports the stat is the runtime's call (Coef):
// predict cannot see a fit's numbers.
func CheckCoef(cat Catalog, fit, term, stat string) *errors.CodedError {
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(fit)
	}
	if !ok {
		return UnknownModel(fit, NamesOfKind(cat, types.FitKindRegression))
	}
	if sig.EffectiveKind() != types.FitKindRegression {
		return KindMismatch(fit, types.FitKindRegression, sig.EffectiveKind())
	}
	if term != InterceptKey && !slices.Contains(sig.Terms, term) {
		return TermUnknown(fit, term, sig.Terms)
	}
	if !slices.Contains(CoefStats(), stat) {
		return StatUnknown(fit, stat)
	}
	return nil
}

// Coef is coef(fit, term, stat) over the request's bound models, at
// full float64 precision: CheckCoef, then the figure. stat lower / upper
// on a fit that reports no intervals, or se on one that reports no
// standard errors, is PULSE_MODEL_INTERVALS_UNAVAILABLE. A figure the
// fit reports for other terms but not this one — a lasso-dropped
// predictor's interval, an undefined (df ≤ 0) bound — is NaN, an
// undefined figure (null on the wire), never 0. A candidate predictor
// the fit's selection dropped has point coefficient 0 (it scores 0 in
// ATTR_REG_PREDICT) and NaN for every other stat.
func Coef(set *Set, fit, term, stat string) (float64, *errors.CodedError) {
	if cerr := CheckCoef(set, fit, term, stat); cerr != nil {
		return math.NaN(), cerr
	}
	if se, failed := set.Failed(fit); failed {
		return math.NaN(), FailedFitRefusal(fit, se)
	}
	b := set.byName[fit]
	switch stat {
	case StatLower, StatUpper:
		if !b.ReportsIntervals() {
			return math.NaN(), IntervalsUnavailable(fit, "stat", stat)
		}
	case StatSE:
		if !b.ReportsStdErrors() {
			return math.NaN(), IntervalsUnavailable(fit, "stat", stat)
		}
	}
	if term == InterceptKey {
		switch stat {
		case StatLower:
			return b.InterceptCILower, nil
		case StatUpper:
			return b.InterceptCIUpper, nil
		case StatSE:
			return b.InterceptStdErr, nil
		}
		return b.Intercept, nil
	}
	i := slices.Index(b.Predictors, term)
	if i < 0 {
		// A candidate the selection dropped: no figure but a zero
		// coefficient.
		if stat == StatPoint {
			return 0, nil
		}
		return math.NaN(), nil
	}
	switch stat {
	case StatLower:
		return b.CILower[i], nil
	case StatUpper:
		return b.CIUpper[i], nil
	case StatSE:
		return b.StdErrors[i], nil
	}
	return b.Coefficients[i], nil
}

// ReportsIntervals reports whether the fit carries coefficient
// confidence intervals (RegressionResult.ConfLevel set). A fit without
// standard errors — a degenerate intercept-only GLM — has none.
func (b *Bound) ReportsIntervals() bool { return b.ConfLevel > 0 }

// ReportsStdErrors reports whether the fit carries any standard error.
func (b *Bound) ReportsStdErrors() bool {
	if !math.IsNaN(b.InterceptStdErr) {
		return true
	}
	for _, se := range b.StdErrors {
		if !math.IsNaN(se) {
			return true
		}
	}
	return false
}

// StatUnknown is the PULSE_MODEL_STAT_UNKNOWN refusal: a coef() stat
// other than point, lower, upper or se.
func StatUnknown(fit, stat string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_STAT_UNKNOWN,
		fmt.Sprintf("%s(): stat %q of model %q is not one of point, lower, upper, se", CoefFunc, stat, fit),
		map[string]any{"fit": fit, "stat": stat, "valid": CoefStats()})
}

// IntervalsUnavailable is the PULSE_MODEL_INTERVALS_UNAVAILABLE refusal:
// a reference reads a coefficient interval (or standard error) from a
// fit that reports none. key names the reference's knob — "stat" for
// coef(), "bound" for ATTR_REG_PREDICT — and value its setting.
func IntervalsUnavailable(fit, key, value string) *errors.CodedError {
	return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_INTERVALS_UNAVAILABLE,
		fmt.Sprintf("model %q reports no coefficient intervals or standard errors, so %s %q has nothing to read", fit, key, value),
		map[string]any{"fit": fit, key: value})
}
