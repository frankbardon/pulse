package model

import (
	stderrors "errors"
	"fmt"
	"math"

	"github.com/frankbardon/pulse/errors"
)

// builtins.go holds the expression implementations of the bound-model
// readers — coef(fit, term[, stat]), sweep_axis(fit, axis) and
// response(fit, channel, spend) — over one Set. The engine binds them
// over a request's models (internal/processing); a no-execute static
// judgement (predict's scalar slots, internal/scalarslot) binds them
// over the request's LOADED `models` entries only, through Folding, so
// predict folds a loaded model's figure to the very value the runtime
// reads. They live here, not in the engine, so both sides run one
// implementation.

// ErrNotFolded is the error a Folding implementation returns for a fit
// its Set does not bind: the call reads a model predict has no figures
// for (a Compose `fits` entry, which runs only at execution), so the
// expression is left to the runtime — never a refusal.
var ErrNotFolded = stderrors.New("bound-model read of a fit with no figures before execution")

// CoefBuiltin is coef(fit, term[, stat]) bound to set (nil: none) —
// Coef: PULSE_MODEL_UNKNOWN / _TERM_UNKNOWN / _STAT_UNKNOWN /
// _INTERVALS_UNAVAILABLE / _KIND_MISMATCH, a failed fit's own code.
func CoefBuiltin(set *Set) func(args ...any) (any, error) {
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
		stat := StatPoint
		if len(strs) == 3 {
			stat = strs[2]
		}
		v, cerr := Coef(set, strs[0], strs[1], stat)
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}
}

// SweepAxisBuiltin is sweep_axis(fit, axis) bound to set (nil: none) —
// SweepAxis: the sweep-bound fit's winning axis value, refused like
// coef() (PULSE_MODEL_UNKNOWN / _TERM_UNKNOWN reasons not_sweep_bound /
// axis_unknown, a failed fit's own code).
func SweepAxisBuiltin(set *Set) func(args ...any) (any, error) {
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
		v, cerr := SweepAxis(set, strs[0], strs[1])
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}
}

// ResponseBuiltin is response(fit, channel, spend) bound to set (nil:
// none) — Response: the channel's steady-state response
// beta · hill(m · spend, k, s), refused like coef() (PULSE_MODEL_UNKNOWN
// / _KIND_MISMATCH / _TERM_UNKNOWN reason adstock_unknown, a failed
// fit's own code). A null spend (nil) is a null result (NaN); an
// integer spend reads as its float64.
func ResponseBuiltin(set *Set) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) != 3 {
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("response() takes 3 arguments (fit, channel, spend), got %d", len(args)))
		}
		strs := make([]string, 2)
		for i, a := range args[:2] {
			s, ok := a.(string)
			if !ok {
				return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
					fmt.Sprintf("response() argument %d must be a string, got %T", i+1, a))
			}
			strs[i] = s
		}
		spend, ok := responseSpend(args[2])
		if !ok {
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("response() argument 3 must be a number, got %T", args[2]))
		}
		v, cerr := Response(set, strs[0], strs[1], spend)
		if cerr != nil {
			return nil, cerr
		}
		return v, nil
	}
}

// Folding wraps fn — one of the builtins above over set — for a static
// judgement: a call whose fit argument (the first) names a fit set
// binds runs fn unchanged, so it returns the runtime's value or refusal;
// any other call returns ErrNotFolded, leaving the expression to the
// runtime. A non-string fit argument runs fn, whose own argument fault
// is the runtime's.
func Folding(set *Set, fn func(args ...any) (any, error)) func(args ...any) (any, error) {
	return func(args ...any) (any, error) {
		if len(args) > 0 {
			if name, ok := args[0].(string); ok {
				if _, bound := set.Signature(name); !bound {
					return nil, ErrNotFolded
				}
			}
		}
		return fn(args...)
	}
}

// responseSpend is response()'s spend argument as a float64: a nil
// (null) spend is NaN; a non-numeric one is not ok.
func responseSpend(a any) (float64, bool) {
	switch v := a.(type) {
	case nil:
		return math.NaN(), true
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	}
	return 0, false
}
