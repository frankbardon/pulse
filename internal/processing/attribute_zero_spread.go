package processing

import (
	"strconv"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// The zero-spread rule of the scoring attributes (ATTR_ZSCORE,
// ATTR_TSCORE, ATTR_NORMALIZED): a spread of exactly 0 (the population
// sd, or max − min) makes every score 0/0, so every row scores null.
// That stays as it is; zeroSpread makes it visible — one
// PULSE_ATTR_ZERO_SPREAD warning per slot (attributeWarner) and, under
// Strict, the run's error (attributeStrictRefuser). The verdict is read
// after Finalize, the one point both the buffered Compute and the
// streaming two-pass drive pass through, so the two arms agree by
// construction. It is data-dependent: predict cannot raise it.
//
// A slot whose pre-pass read no non-null source value at all (an empty
// or all-null base) does not warn: no row was nulled BY the spread —
// every row is already a null input, counted in n_null.

// Spread kinds and reasons of a PULSE_ATTR_ZERO_SPREAD warning.
const (
	zeroSpreadStdDev = "stddev"
	zeroSpreadRange  = "range"

	// zeroSpreadConstant: two or more contributing values, all equal.
	zeroSpreadConstant = "constant"
	// zeroSpreadSingleValue: exactly one contributing value (the
	// population spread of one value is 0).
	zeroSpreadSingleValue = "single_value"
	// zeroSpreadNoMass: non-null values were read but none carried a
	// valid positive weight (weighted ATTR_ZSCORE / ATTR_TSCORE only).
	zeroSpreadNoMass = "no_mass"
)

// zeroSpread is a scoring slot's zero-spread tally: its identity, the
// non-null values its pre-pass read and, at Finalize, the verdict.
type zeroSpread struct {
	label  string
	typ    types.AttributeType
	field  string
	spread string
	// seen counts the non-null source values the pre-pass read (before
	// any weight test).
	seen int64
	// n is the contributing values (a weighted slot: those with a valid
	// positive weight) and reason the verdict; reason is "" when the
	// spread is positive or nothing was read.
	n      int64
	reason string
}

func newZeroSpread(attr *types.Attribute, spread string) zeroSpread {
	return zeroSpread{label: attributeSlotLabel(attr), typ: attr.Type, field: attr.Field, spread: spread}
}

// settle records the Finalize verdict: zero is the operator's own
// null-every-row predicate (sd == 0, range == 0) and n its contributing
// values.
func (z *zeroSpread) settle(zero bool, n int64) {
	z.n, z.reason = n, ""
	if !zero || z.seen == 0 {
		return
	}
	switch {
	case n == 0:
		z.reason = zeroSpreadNoMass
	case n == 1:
		z.reason = zeroSpreadSingleValue
	default:
		z.reason = zeroSpreadConstant
	}
}

// zeroSpreadFigures is the slot's message and details (nil details when
// the spread is positive).
func (z *zeroSpread) zeroSpreadFigures() (string, map[string]any) {
	if z.reason == "" {
		return "", nil
	}
	what := "population standard deviation"
	if z.spread == zeroSpreadRange {
		what = "range (max − min)"
	}
	var why string
	switch z.reason {
	case zeroSpreadNoMass:
		why = "no non-null value carries a valid positive weight"
	case zeroSpreadSingleValue:
		why = "one value contributes"
	default:
		why = "all " + strconv.FormatInt(z.n, 10) + " contributing values are equal"
	}
	return z.label + ": " + string(z.typ) + " over " + z.field + " has a zero " + what + " (" + why +
			"), so every row's score is 0/0 and null",
		map[string]any{
			"label":  z.label,
			"type":   string(z.typ),
			"field":  z.field,
			"n":      z.n,
			"spread": z.spread,
			"reason": z.reason,
		}
}

// attributeWarnings satisfies attributeWarner: one
// PULSE_ATTR_ZERO_SPREAD warning when the spread was zero.
func (z *zeroSpread) attributeWarnings() []*types.ResponseWarning {
	msg, details := z.zeroSpreadFigures()
	if details == nil {
		return nil
	}
	return []*types.ResponseWarning{{Code: string(errors.PULSE_ATTR_ZERO_SPREAD), Message: msg, Details: details}}
}

// strictRefusal satisfies attributeStrictRefuser: under Strict a zero
// spread fails the run with the warning's own code and details.
func (z *zeroSpread) strictRefusal() *errors.CodedError {
	msg, details := z.zeroSpreadFigures()
	if details == nil {
		return nil
	}
	return errors.NewCodedErrorWithDetails(errors.PULSE_ATTR_ZERO_SPREAD, msg, details)
}

var (
	_ attributeWarner        = (*zscoreAttribute)(nil)
	_ attributeStrictRefuser = (*zscoreAttribute)(nil)
	_ attributeWarner        = (*tscoreAttribute)(nil)
	_ attributeStrictRefuser = (*tscoreAttribute)(nil)
	_ attributeWarner        = (*normalizedAttribute)(nil)
	_ attributeStrictRefuser = (*normalizedAttribute)(nil)
)
