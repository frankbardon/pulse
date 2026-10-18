package processing

import (
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
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
	return daterange.SourceAmbiguity(operator, hasInline, hasTable)
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
	rt, err := lookupRangeTable(operator, table, exts)
	if err != nil {
		return nil, err
	}
	if rt.Kind() != daterange.KindDate {
		return nil, daterange.KindMismatch(operator, table, daterange.KindDate, rt.Kind())
	}
	return rt.Ranges, nil
}

// lookupRangeTable resolves a named range table of either kind;
// an unregistered (or hidden) name is PULSE_RANGE_TABLE_UNKNOWN.
func lookupRangeTable(operator, table string, exts *ExtensionRegistry) (RangeTable, error) {
	rt, ok := exts.LookupRangeTable(table)
	if !ok {
		return RangeTable{}, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_UNKNOWN,
			operator+": unknown range table "+strconv.Quote(table),
			map[string]any{"operator": operator, "table": table})
	}
	return rt, nil
}

// resolveNumericRangeSpecs resolves a GROUP_NUMERIC_RANGES table source:
// an unknown name is PULSE_RANGE_TABLE_UNKNOWN, a date table
// PULSE_RANGE_TABLE_KIND_MISMATCH.
func resolveNumericRangeSpecs(table string, exts *ExtensionRegistry) ([]numrange.Spec, error) {
	rt, err := lookupRangeTable(numrange.Operator, table, exts)
	if err != nil {
		return nil, err
	}
	if rt.Kind() != daterange.KindNumeric {
		return nil, daterange.KindMismatch(numrange.Operator, table, daterange.KindNumeric, rt.Kind())
	}
	return rt.NumericRanges, nil
}
