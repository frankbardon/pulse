package processing

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
)

// DateRangeSpec is the wire-level shape of a single labeled date range as
// authored inline in a request (GROUP_DATE_RANGES / FILTER_DATE_RANGES) or
// in a named range-table file. The compiled model lives in
// internal/daterange so the facade can validate RangeTables without an
// exported compile entry point here.
type DateRangeSpec = daterange.Spec

// dateRangeSet is a validated, ordered collection of labeled date ranges.
type dateRangeSet = daterange.Set

// compileDateRanges parses and validates specs; see daterange.Compile for
// the PULSE_RANGE_* code each failure carries.
func compileDateRanges(specs []DateRangeSpec) (*dateRangeSet, error) {
	return daterange.Compile(specs)
}

// dateRangeSourceAmbiguity enforces the inline-XOR-table source contract
// shared by GROUP_DATE_RANGES and FILTER_DATE_RANGES: exactly one of an
// inline `ranges` array or a named `table` reference must be present.
// Both present, or neither present, returns PULSE_RANGE_SOURCE_AMBIGUOUS;
// an exactly-one selection returns nil. The check needs no registry, so
// callers can surface it at construction time before any table lookup.
func dateRangeSourceAmbiguity(operator string, hasInline, hasTable bool) error {
	switch {
	case hasInline && hasTable:
		return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_SOURCE_AMBIGUOUS,
			operator+": specify exactly one of inline `ranges` or a named `table`, not both",
			map[string]any{"operator": operator})
	case !hasInline && !hasTable:
		return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_SOURCE_AMBIGUOUS,
			operator+": one of inline `ranges` or a named `table` is required",
			map[string]any{"operator": operator})
	default:
		return nil
	}
}

// resolveDateRangeSpecs selects the labeled date-range specs for a
// GROUP_DATE_RANGES / FILTER_DATE_RANGES operator from exactly one of an
// inline `ranges` array or a named `table` reference. It enforces the
// inline-XOR-table source contract (dateRangeSourceAmbiguity) and, for a
// named source, resolves the table against the live ExtensionRegistry —
// an unregistered name returns PULSE_RANGE_TABLE_UNKNOWN. The returned
// specs are compiled by the caller through the same compileDateRanges path
// used for the inline source, so match/validate behaviour is identical
// regardless of which source was named.
//
// exts is nil-safe: a nil registry (or one without the named table) yields
// PULSE_RANGE_TABLE_UNKNOWN rather than a panic.
func resolveDateRangeSpecs(operator string, inline []DateRangeSpec, table string, exts *ExtensionRegistry) ([]DateRangeSpec, error) {
	table = strings.TrimSpace(table)
	hasInline := len(inline) > 0
	hasTable := table != ""
	if err := dateRangeSourceAmbiguity(operator, hasInline, hasTable); err != nil {
		return nil, err
	}
	if hasInline {
		return inline, nil
	}
	rt, ok := exts.LookupRangeTable(table)
	if !ok {
		return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_UNKNOWN,
			operator+": unknown range table "+strconv.Quote(table),
			map[string]any{"operator": operator, "table": table})
	}
	return rt.Ranges, nil
}
