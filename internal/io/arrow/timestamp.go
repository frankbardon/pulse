package arrow

import (
	"fmt"
	"sort"
	"time"

	"github.com/apache/arrow-go/v18/arrow"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// Native Arrow / Parquet timestamps.
//
// A `datetime` cell holds whole UTC epoch seconds, and the import path
// is text: the reader renders a cell and internal/io/import.go's
// convertValue (or, under an import source zone, temporal.ParseLocal) is
// the single parse authority. A native timestamp therefore leaves the
// reader as one of two literals, and which one is the whole zone
// contract:
//
//   - An INSTANT — an Arrow Timestamp with a non-empty TimeZone, a
//     Parquet TIMESTAMP with isAdjustedToUTC=true — renders as the
//     canonical UTC literal (encoding.FormatDateTime, `…Z`). It already
//     names its instant, so an import source zone never reinterprets it.
//     The column's own zone is irrelevant to the stored value and is not
//     loaded (no host tzdata dependency).
//   - A NAIVE wall clock — an empty TimeZone, isAdjustedToUTC=false, and
//     legacy Parquet INT96 — renders as a naive literal (no `Z`, no
//     offset), so an import source zone and its DST policy apply exactly
//     as they do to a CSV cell; with no source zone it reads as UTC.
//
// Every unit (s / ms / µs / ns) floors to whole seconds toward the PAST,
// so a pre-1970 value with a fraction lands on the earlier second, never
// on the one toward the epoch. A floored fraction is data loss, so the
// readers tally it and surface PULSE_IMPORT_TIMESTAMP_TRUNCATED rather
// than drop it silently.

// naiveDateTimeLayout is encoding.DateTimeFormats[1]: a wall clock with
// no zone designator, which encoding.ParseDateTime reads as UTC and
// temporal.ParseLocal reads in the import source zone.
const naiveDateTimeLayout = "2006-01-02T15:04:05"

// TimestampIsNaive reports whether an Arrow timestamp type is a naive
// wall clock (no TimeZone) rather than a UTC-adjusted instant.
func TimestampIsNaive(dt *arrow.TimestampType) bool {
	return dt.TimeZone == ""
}

// FormatTimestamp renders one timestamp value of the given unit as the
// literal the import path parses: the canonical UTC literal for an
// instant, a naive literal for a wall clock (see the package notes
// above). truncated reports that a non-zero sub-second fraction was
// floored away.
func FormatTimestamp(v arrow.Timestamp, unit arrow.TimeUnit, naive bool) (s string, truncated bool) {
	sec, truncated := floorSeconds(int64(v), unit)
	if naive {
		return time.Unix(sec, 0).UTC().Format(naiveDateTimeLayout), truncated
	}
	return encoding.FormatDateTime(uint64(sec)), truncated
}

// floorSeconds converts a count of unit ticks to whole seconds, flooring
// toward the past (Go's / truncates toward zero, which would move a
// pre-1970 fractional value one second LATER).
func floorSeconds(v int64, unit arrow.TimeUnit) (int64, bool) {
	var per int64
	switch unit {
	case arrow.Second:
		return v, false
	case arrow.Millisecond:
		per = 1_000
	case arrow.Microsecond:
		per = 1_000_000
	default:
		per = 1_000_000_000
	}
	sec, rem := v/per, v%per
	if rem < 0 {
		sec--
	}
	return sec, rem != 0
}

// TimestampTally counts the timestamp cells one ReadRows pass floored to
// whole seconds, per column, for the one PULSE_IMPORT_TIMESTAMP_TRUNCATED
// warning. The zero value is ready; Reset at the top of every pass so a
// re-read (inference sample, then the row pass) reports the last pass
// only, never a doubled count.
type TimestampTally struct {
	n    int
	cols map[string]int
}

// Reset forgets every count.
func (t *TimestampTally) Reset() {
	t.n = 0
	t.cols = nil
}

// Note records one truncated cell of column col.
func (t *TimestampTally) Note(col string) {
	if t.cols == nil {
		t.cols = map[string]int{}
	}
	t.n++
	t.cols[col]++
}

// Warnings returns the one PULSE_IMPORT_TIMESTAMP_TRUNCATED warning when
// any cell was truncated, nil otherwise. A fresh slice each call.
func (t *TimestampTally) Warnings() []*errors.CodedError {
	if t.n == 0 {
		return nil
	}
	cols := make([]string, 0, len(t.cols))
	for c := range t.cols {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	return []*errors.CodedError{errors.NewCodedErrorWithDetails(errors.PULSE_IMPORT_TIMESTAMP_TRUNCATED,
		fmt.Sprintf("%d native timestamp value(s) carried a sub-second fraction that a datetime (whole seconds) cannot hold; each was floored to the earlier second (columns: %v)", t.n, cols),
		map[string]any{"truncated_n": t.n, "columns": cols})}
}
