package model

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// sweep_axis.go is sweep_axis(fit, axis) — the expression function that
// reads a sweep-bound fit's WINNING axis value (the rank-1 slot's
// RankEntry.Axes[axis]): the value a decay or a penalty took in the
// slot the fit was refitted over, which no coefficient carries. Its
// static rule (CheckSweepAxis) mirrors CheckCoef's: the runtime judges
// every call and predict every all-literal one by the same names.

// The sweep_axis() PULSE_MODEL_TERM_UNKNOWN reasons (details.reason).
const (
	// ReasonNotSweepBound: the fit is bound, but not from a sweep
	// (`from_sweep`), so it has no winning axis values.
	ReasonNotSweepBound = "not_sweep_bound"
	// ReasonAxisUnknown: the sweep has no axis of that name;
	// details.axes lists the ones it has.
	ReasonAxisUnknown = "axis_unknown"
)

// CheckSweepAxis is the static rule over one sweep_axis(fit, axis)
// call. In order: fit resolves in cat (PULSE_MODEL_UNKNOWN, the bound
// regressions under details.available), fit is bound from a sweep
// (PULSE_MODEL_TERM_UNKNOWN reason not_sweep_bound), axis is one of
// the sweep's axes (same code, reason axis_unknown, details.axes the
// sorted axis names) — judged only when the catalog knows them
// (Signature.SweepAxesKnown). The first fault wins.
func CheckSweepAxis(cat Catalog, fit, axis string) *errors.CodedError {
	var sig Signature
	ok := false
	if cat != nil {
		sig, ok = cat.Signature(fit)
	}
	if !ok {
		return UnknownModel(fit, NamesOfKind(cat, types.FitKindRegression))
	}
	if !sig.SweepBound {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
			fmt.Sprintf("%s(): fit %q is not bound from a sweep (fits[].from_sweep), so it has no winning axis %q", SweepAxisFunc, fit, axis),
			map[string]any{"fit": fit, "axis": axis, "reason": ReasonNotSweepBound})
	}
	if sig.SweepAxesKnown && !slices.Contains(sig.SweepAxes, axis) {
		return errors.NewCodedErrorWithDetails(errors.PULSE_MODEL_TERM_UNKNOWN,
			fmt.Sprintf("%s(): the sweep fit %q binds from has no axis %q", SweepAxisFunc, fit, axis),
			map[string]any{"fit": fit, "axis": axis, "reason": ReasonAxisUnknown, "axes": slices.Clone(sig.SweepAxes)})
	}
	return nil
}

// SweepAxis is sweep_axis(fit, axis) over the request's bound models:
// CheckSweepAxis, then — on a fit that failed (an empty ranking binds
// it failed) — the fit's own refusal (FailedFitRefusal), else the
// winning slot's value of axis. A number comes back as a float64
// whatever form the expansion carried it in (json.Number, a Go
// integer); any other value (a string or bool `values` entry) as is.
func SweepAxis(set *Set, fit, axis string) (any, *errors.CodedError) {
	if cerr := CheckSweepAxis(set, fit, axis); cerr != nil {
		return nil, cerr
	}
	if se, failed := set.Failed(fit); failed {
		return nil, FailedFitRefusal(fit, se)
	}
	b := set.byName[fit]
	return sweepAxisValue(b.SweepAxes[axis]), nil
}

// sweepAxisValue is v with every numeric form as a float64.
func sweepAxisValue(v any) any {
	switch n := v.(type) {
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f
		}
		return n.String()
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int8:
		return float64(n)
	case int16:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	case uint:
		return float64(n)
	case uint8:
		return float64(n)
	case uint16:
		return float64(n)
	case uint32:
		return float64(n)
	case uint64:
		return float64(n)
	}
	return v
}

// WithSweepAxes is c with every sweep-bound entry's axis names set to
// axes (the batch's one sweep's, sorted copy; Signature.SweepAxesKnown)
// — the static catalog a sweep_axis() axis is judged against. A nil
// axes leaves c as is (the axes stay unknown: any axis passes
// statically and the runtime judges it). c is modified in place and
// returned.
func (c FitCatalog) WithSweepAxes(axes []string) FitCatalog {
	if axes == nil {
		return c
	}
	sorted := slices.Clone(axes)
	slices.Sort(sorted)
	for name, sig := range c {
		if sig.SweepBound {
			sig.SweepAxes, sig.SweepAxesKnown = slices.Clone(sorted), true
			c[name] = sig
		}
	}
	return c
}

// sortedAxisNames are m's keys, sorted (non-nil).
func sortedAxisNames(m map[string]any) []string { return sortedKeys(m) }
