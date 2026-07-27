package processing

import (
	"sort"
	"strconv"
	"strings"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// DateRangeSpec is the wire-level shape of a single labeled date range as
// authored inline in a request (GROUP_DATE_RANGES / FILTER_DATE_RANGES) or
// in a named range-table file. Start and End are ISO date literals
// (YYYY-MM-DD and the other encoding.DateFormats layouts). A nil, null, or
// empty Start means an open lower bound; a nil, null, or empty End means an
// open upper bound. Bounds are inclusive on both sides.
//
// This is the single shared model the grouper (E1-S2), filter (E2-S1), and
// named-table loader (E3-S1) all decode into — parse/validate/match logic
// is defined once here and never duplicated per operator.
type DateRangeSpec struct {
	Label string  `json:"label"`
	Start *string `json:"start,omitempty"`
	End   *string `json:"end,omitempty"`
}

// compiledRange is the resolved form of a DateRangeSpec: boundaries turned
// into u32 day-integers (whole days since the Unix epoch, matching the
// on-wire `date` field type) so a record's decoded day-integer can be
// compared directly without any time.Time round-trip.
type compiledRange struct {
	label    string
	hasStart bool
	start    uint32
	hasEnd   bool
	end      uint32
}

// DateRangeSet is a validated, ordered collection of labeled date ranges.
// It is produced by CompileDateRanges (which enforces the validation
// rules) and exposes Match for per-record label resolution. Ranges are
// stored sorted by lower bound (open-lower first) so Match returns
// deterministically; because the set is validated non-overlapping, at most
// one range can contain any given day.
type DateRangeSet struct {
	ranges []compiledRange
}

// Len reports the number of ranges in the set.
func (s *DateRangeSet) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ranges)
}

// Labels returns the range labels in stored (lower-bound-sorted) order.
func (s *DateRangeSet) Labels() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.ranges))
	for i, r := range s.ranges {
		out[i] = r.label
	}
	return out
}

// Match returns the label of the range containing day (a u32 day-integer,
// whole days since the Unix epoch) with inclusive comparison on both
// bounds, and ok=true. When no range contains day it returns "", false.
func (s *DateRangeSet) Match(day uint32) (string, bool) {
	if s == nil {
		return "", false
	}
	for _, r := range s.ranges {
		if r.hasStart && day < r.start {
			continue
		}
		if r.hasEnd && day > r.end {
			continue
		}
		return r.label, true
	}
	return "", false
}

// boundOpen reports whether a boundary pointer denotes an open bound
// (nil / null / empty string).
func boundOpen(raw *string) bool {
	return raw == nil || *raw == ""
}

// CompileDateRanges parses and validates a slice of labeled date-range
// specs into a DateRangeSet. It enforces, each with its own coded error:
//
//   - empty spec set                       → PULSE_RANGE_EMPTY
//   - unparseable boundary, or start > end
//     when both bounds are present         → PULSE_RANGE_INVALID
//   - two specs sharing a Label            → PULSE_RANGE_DUPLICATE_LABEL
//   - two ranges whose inclusive [start,
//     end] intervals overlap (accounting
//     for open/unbounded ends)             → PULSE_RANGE_OVERLAP
//
// On success the returned set is sorted by lower bound (open-lower first).
func CompileDateRanges(specs []DateRangeSpec) (*DateRangeSet, error) {
	if len(specs) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_RANGE_EMPTY,
			"labeled date-range set is empty; supply at least one {label, start, end} range")
	}

	ranges := make([]compiledRange, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))

	for i := range specs {
		spec := specs[i]

		if _, dup := seen[spec.Label]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_DUPLICATE_LABEL,
				"labeled date-range set contains a duplicate label",
				map[string]any{"label": spec.Label})
		}
		seen[spec.Label] = struct{}{}

		cr := compiledRange{label: spec.Label}

		if !boundOpen(spec.Start) {
			day, err := encoding.ParseDate(*spec.Start)
			if err != nil {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
					"labeled date-range start boundary is not a parseable date literal",
					map[string]any{"label": spec.Label, "start": *spec.Start})
			}
			cr.hasStart = true
			cr.start = day
		}

		if !boundOpen(spec.End) {
			day, err := encoding.ParseDate(*spec.End)
			if err != nil {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
					"labeled date-range end boundary is not a parseable date literal",
					map[string]any{"label": spec.Label, "end": *spec.End})
			}
			cr.hasEnd = true
			cr.end = day
		}

		if cr.hasStart && cr.hasEnd && cr.start > cr.end {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
				"labeled date-range start boundary is after its end boundary",
				map[string]any{"label": spec.Label, "start": *spec.Start, "end": *spec.End})
		}

		ranges = append(ranges, cr)
	}

	// Sort by lower bound: open-lower (unbounded start) first, then by
	// ascending start day. Adjacency in this order is sufficient to detect
	// any overlap because we return on the first offending pair.
	sort.SliceStable(ranges, func(i, j int) bool {
		a, b := ranges[i], ranges[j]
		if a.hasStart != b.hasStart {
			return !a.hasStart // open-lower sorts before any bounded start
		}
		if !a.hasStart {
			return false // both open-lower: keep stable order
		}
		return a.start < b.start
	})

	for i := 1; i < len(ranges); i++ {
		prev, cur := ranges[i-1], ranges[i]
		// prev sorts at or before cur. The two inclusive intervals are
		// disjoint only when prev has a bounded end, cur has a bounded
		// start, and prev.end strictly precedes cur.start (contiguous
		// Mar-31 → Apr-01 stays disjoint: 31 < 32). Any open boundary on
		// the touching side, or prev.end >= cur.start, means overlap.
		disjoint := prev.hasEnd && cur.hasStart && prev.end < cur.start
		if !disjoint {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_OVERLAP,
				"labeled date-range set contains overlapping ranges",
				map[string]any{"labels": []string{prev.label, cur.label}})
		}
	}

	return &DateRangeSet{ranges: ranges}, nil
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
// specs are compiled by the caller through the same CompileDateRanges path
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
