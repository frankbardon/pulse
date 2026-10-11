package metric

import (
	stderrors "errors"
	"fmt"
	"strings"

	perr "github.com/frankbardon/pulse/errors"
)

// ErrLengthMismatch is the sentinel every *LengthMismatchError matches
// under errors.Is.
var ErrLengthMismatch = stderrors.New("metric: vector length mismatch")

// ErrDomain is the sentinel every *DomainError matches under errors.Is.
var ErrDomain = stderrors.New("metric: value outside the metric's domain")

// ErrStats is the sentinel every *StatsError matches under errors.Is.
var ErrStats = stderrors.New("metric: invalid fitted statistics")

// LengthMismatchError reports two vectors of unequal length handed to a
// kernel. It is never answered with NaN.
type LengthMismatchError struct {
	Left, Right int
}

func (e *LengthMismatchError) Error() string {
	return fmt.Sprintf("metric: vector length mismatch: %d vs %d", e.Left, e.Right)
}

// Is makes errors.Is(err, ErrLengthMismatch) true.
func (e *LengthMismatchError) Is(target error) bool { return target == ErrLengthMismatch }

// DomainError reasons.
const (
	// ReasonNegative: a negative value where the metric reads
	// non-negative values (hellinger, aitchison, chi_square).
	ReasonNegative = "negative"
	// ReasonZero: an aitchison zero part; its log-ratio is undefined.
	ReasonZero = "zero"
	// ReasonNotBinary: a set-metric element other than 0 or 1.
	ReasonNotBinary = "not_binary"
	// ReasonSetCounts: SetCounts that cannot describe two sets (a
	// negative count, or an intersection larger than either set).
	ReasonSetCounts = "set_counts"
)

// DomainError reports a value outside a metric's domain. The kernel
// returns NaN beside it; the consumer nulls the row and counts it, or
// refuses. Index is the offending element (-1 for SetCounts).
type DomainError struct {
	Metric string
	Index  int
	Value  float64
	Reason string
}

func (e *DomainError) Error() string {
	return fmt.Sprintf("metric: %s: element %d (%v) outside the domain: %s", e.Metric, e.Index, e.Value, e.Reason)
}

// Is makes errors.Is(err, ErrDomain) true.
func (e *DomainError) Is(target error) bool { return target == ErrDomain }

// StatsError reasons.
const (
	// ReasonMissing: a fitted metric got no Stats, or not the one it
	// reads.
	ReasonMissing = "missing"
	// ReasonLength: the statistic's length (or order) is not the vector
	// length.
	ReasonLength = "length"
	// ReasonInvalid: an entry is not finite and positive (an SD or a
	// column mass).
	ReasonInvalid = "invalid"
)

// StatsError reports fitted statistics a metric cannot use. Stat names
// the Fit the metric reads; Index is the offending entry (-1 when the
// whole statistic is at fault).
type StatsError struct {
	Metric string
	Stat   Fit
	Index  int
	Reason string
}

func (e *StatsError) Error() string {
	return fmt.Sprintf("metric: %s: fitted %s: %s (entry %d)", e.Metric, e.Stat, e.Reason, e.Index)
}

// Is makes errors.Is(err, ErrStats) true.
func (e *StatsError) Is(target error) bool { return target == ErrStats }

// refusal builds the PULSE_VECTOR_METRIC_UNKNOWN coded error.
func refusal(name, reason string, f Form, c Consumer, valid []string) *perr.CodedError {
	details := map[string]any{
		"metric": name,
		"reason": reason,
		"form":   string(f),
		"valid":  valid,
	}
	if c != "" {
		details["consumer"] = string(c)
	}
	var msg string
	switch reason {
	case reasonUnregistered:
		msg = fmt.Sprintf("unknown metric %q; valid %s metrics: %s", name, f, strings.Join(valid, ", "))
	case reasonNoForm:
		msg = fmt.Sprintf("metric %q has no %s form and is never converted to one; valid %s metrics: %s", name, f, f, strings.Join(valid, ", "))
	default:
		msg = fmt.Sprintf("metric %q is not accepted by %s; valid: %s", name, c, strings.Join(valid, ", "))
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_VECTOR_METRIC_UNKNOWN, msg, details)
}

// required builds the PULSE_VECTOR_METRIC_REQUIRED coded error.
func required(f Form, c Consumer, valid []string) *perr.CodedError {
	details := map[string]any{
		"form":  string(f),
		"valid": valid,
	}
	if c != "" {
		details["consumer"] = string(c)
	}
	return perr.NewCodedErrorWithDetails(perr.PULSE_VECTOR_METRIC_REQUIRED,
		fmt.Sprintf("a %s metric is required; valid: %s", f, strings.Join(valid, ", ")), details)
}

// Refusal reasons carried in PULSE_VECTOR_METRIC_UNKNOWN details.
const (
	reasonUnregistered = "unregistered"
	reasonNoForm       = "no_form"
	reasonNotAccepted  = "not_accepted"
)
