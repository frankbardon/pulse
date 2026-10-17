package fieldaccept

import (
	"math"
	"sort"
	"strconv"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// ExpectedSumTolerance is how far params.expected's proportions may sum
// from 1: a fit is never renormalised, so a share list that does not
// add up is a typo, not a rounding difference.
const ExpectedSumTolerance = 1e-9

// The Details["reason"] values of PULSE_TEST_EXPECTED_INVALID.
const (
	ExpectedReasonWithCols    = "with_cols"
	ExpectedReasonTooFew      = "too_few_labels"
	ExpectedReasonEmptyLabel  = "empty_label"
	ExpectedReasonNotPositive = "not_positive"
	ExpectedReasonSum         = "sum_not_one"
	ExpectedReasonNotALevel   = "not_a_level"
)

// The Details["reason"] values of PULSE_TEST_P0_INVALID.
const (
	P0ReasonRange       = "out_of_range"
	P0ReasonWithSplitBy = "with_split_by"
)

// SortedLabels returns expected's labels sorted byte-wise: the order a
// goodness-of-fit result lists its categories in, and the order the
// proportions are summed in (so the sum is a function of the set).
func SortedLabels(expected map[string]float64) []string {
	out := make([]string, 0, len(expected))
	for k := range expected {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ExpectedRefusal is TEST_CHISQ's goodness-of-fit params rule: with
// params.expected set (a non-nil map) the test reads rows alone, so
// cols must be empty; the map needs at least two labels, none empty,
// every proportion a finite number above 0, summing to 1 within
// ExpectedSumTolerance; and on a packed_bool rows field every label is
// a level ("0" / "1") — any other label can never be observed. A
// categorical's labels are not judged against its dictionary: a label
// the data never shows is a legitimate observed 0. Predict and the
// runtime constructor both raise it (PULSE_TEST_EXPECTED_INVALID),
// after the rows requirement and before the axis type check. Nil when
// the params are usable.
func ExpectedRefusal(op, rows, cols string, expected map[string]float64, schema *encoding.Schema) *errors.CodedError {
	refuse := func(reason, msg string, extra map[string]any) *errors.CodedError {
		details := map[string]any{"type": op, "reason": reason}
		for k, v := range extra {
			details[k] = v
		}
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_EXPECTED_INVALID, op+" params.expected "+msg, details)
	}
	if cols != "" {
		return refuse(ExpectedReasonWithCols,
			"selects the goodness-of-fit test, which reads rows alone; drop cols (or drop params.expected for the independence test)",
			map[string]any{"cols": cols})
	}
	labels := SortedLabels(expected)
	if len(labels) < 2 {
		return refuse(ExpectedReasonTooFew,
			"needs at least two labels, got "+strconv.Itoa(len(labels)),
			map[string]any{"labels": labels})
	}
	var sum float64
	for _, l := range labels {
		p := expected[l]
		if l == "" {
			return refuse(ExpectedReasonEmptyLabel, "has an empty label", nil)
		}
		if math.IsNaN(p) || math.IsInf(p, 0) || p <= 0 {
			return refuse(ExpectedReasonNotPositive,
				"proportion for "+strconv.Quote(l)+" is "+strconv.FormatFloat(p, 'g', -1, 64)+"; every proportion must be a finite number above 0",
				map[string]any{"label": l})
		}
		sum += p
	}
	if math.Abs(sum-1) > ExpectedSumTolerance {
		return refuse(ExpectedReasonSum,
			"proportions sum to "+strconv.FormatFloat(sum, 'g', -1, 64)+", not 1 (tolerance 1e-9); Pulse never renormalises them",
			map[string]any{"sum": sum})
	}
	if schema == nil || rows == "" {
		return nil
	}
	if f := schema.Field(rows); f != nil && f.Type == encoding.FieldTypePackedBool {
		for _, l := range labels {
			if l != PackedBoolFalse && l != PackedBoolTrue {
				return refuse(ExpectedReasonNotALevel,
					"label "+strconv.Quote(l)+" is not a level of packed_bool field "+rows+"; use \"1\" (true) or \"0\" (false)",
					map[string]any{"label": l, "rows": rows, "valid_levels": PackedBoolLevels()})
			}
		}
	}
	return nil
}

// P0Refusal is TEST_PROP_Z's one-proportion params rule: p0 must lie
// strictly between 0 and 1 (at 0 or 1 the null standard error is 0),
// and is refused beside split_by — a two-proportion test has no
// hypothesised rate, and ignoring it would hide the mistake. Predict
// and the runtime constructor both raise it (PULSE_TEST_P0_INVALID).
func P0Refusal(op, splitBy string, p0 float64) *errors.CodedError {
	if splitBy != "" {
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_P0_INVALID,
			op+" params.p0 selects the one-proportion test, which has no split_by; drop split_by (or drop p0 for the two-proportion test)",
			map[string]any{"type": op, "reason": P0ReasonWithSplitBy, "p0": p0, "split_by": splitBy})
	}
	if math.IsNaN(p0) || p0 <= 0 || p0 >= 1 {
		return errors.NewCodedErrorWithDetails(errors.PULSE_TEST_P0_INVALID,
			op+" params.p0 "+strconv.FormatFloat(p0, 'g', -1, 64)+" is not strictly between 0 and 1",
			map[string]any{"type": op, "reason": P0ReasonRange, "p0": p0})
	}
	return nil
}
