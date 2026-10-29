// Package numrange is the single compiled {label, start, end} model of
// GROUP_NUMERIC_RANGES and the numeric RangeTables the facade validates at
// pulse.New time, and the one reading of the grouper's params — shared by
// the operator factory (internal/processing) and predict
// (internal/descriptor) so both refuse the same request with the same code
// and message.
//
// Bounds are HALF-OPEN: a range covers [start, end). It is the only
// semantics that cannot misplace a float — a value is never "between" two
// adjacent ranges and never in both — so [0, 91) and [91, 104) are
// adjacent, not overlapping, and the integer idiom {0, 91}, {91, 104}
// puts 90 in the first range and 91 in the second. An omitted (or null)
// bound is open: -inf below, +inf above.
//
// Leaf: stdlib + the public errors / types packages + paramread +
// daterange (the shared source-ambiguity and table-kind refusals).
package numrange

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/paramread"
	"github.com/frankbardon/pulse/types"
)

// Operator is the type name every refusal names.
const Operator = string(types.GROUP_NUMERIC_RANGES)

// DefaultUnmatchedLabel is the bucket label of a row outside every range.
const DefaultUnmatchedLabel = "unmatched"

// Spec is the wire-level shape of a single labeled numeric range, authored
// inline on a GROUP_NUMERIC_RANGES grouper or in a numeric range table.
// Start and End are JSON numbers; nil (omitted or null) is an open bound.
// The range covers [Start, End).
type Spec struct {
	Label string   `json:"label"`
	Start *float64 `json:"start,omitempty"`
	End   *float64 `json:"end,omitempty"`
}

type compiledRange struct {
	label    string
	hasStart bool
	start    float64
	hasEnd   bool
	end      float64
}

// Set is a validated collection of labeled numeric ranges, stored sorted
// by lower bound (open-lower first). Validated non-overlapping, so at most
// one range contains any value.
type Set struct {
	ranges []compiledRange
}

// Len reports the number of ranges in the set.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ranges)
}

// Match returns the label of the range containing v (start <= v < end)
// and true; "", false when no range contains it or v is NaN.
func (s *Set) Match(v float64) (string, bool) {
	if s == nil || math.IsNaN(v) {
		return "", false
	}
	for _, r := range s.ranges {
		if r.hasStart && v < r.start {
			continue
		}
		if r.hasEnd && v >= r.end {
			continue
		}
		return r.label, true
	}
	return "", false
}

// MatchRat is Match for an exact value: v is compared with each bound
// exactly (a float64 bound is an exact rational), so an integer past 2^53
// or a decimal is never rounded onto the wrong side of a boundary.
func (s *Set) MatchRat(v *big.Rat) (string, bool) {
	if s == nil || v == nil {
		return "", false
	}
	for _, r := range s.ranges {
		if r.hasStart && v.Cmp(new(big.Rat).SetFloat64(r.start)) < 0 {
			continue
		}
		if r.hasEnd && v.Cmp(new(big.Rat).SetFloat64(r.end)) >= 0 {
			continue
		}
		return r.label, true
	}
	return "", false
}

// Gapless reports whether every finite value lies in some range: the
// lowest range is open below, the highest open above, and each range
// ends exactly where the next begins.
func (s *Set) Gapless() bool {
	if s.Len() == 0 {
		return false
	}
	rs := s.ranges
	if rs[0].hasStart || rs[len(rs)-1].hasEnd {
		return false
	}
	for i := 1; i < len(rs); i++ {
		if rs[i-1].end != rs[i].start {
			return false
		}
	}
	return true
}

// Compile validates specs into a Set. Each failure carries its own code:
//
//   - empty spec set                         → PULSE_RANGE_EMPTY
//   - a non-finite bound, or start >= end
//     when both bounds are present           → PULSE_RANGE_INVALID
//   - two specs sharing a Label              → PULSE_RANGE_DUPLICATE_LABEL
//   - two half-open ranges that share any
//     value (open ends extend to ±inf)       → PULSE_RANGE_OVERLAP
func Compile(specs []Spec) (*Set, error) {
	if len(specs) == 0 {
		return nil, errors.NewCodedError(errors.PULSE_RANGE_EMPTY,
			"labeled numeric-range set is empty; supply at least one {label, start, end} range")
	}
	ranges := make([]compiledRange, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for i := range specs {
		spec := specs[i]
		if _, dup := seen[spec.Label]; dup {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_DUPLICATE_LABEL,
				"labeled numeric-range set contains a duplicate label",
				map[string]any{"label": spec.Label})
		}
		seen[spec.Label] = struct{}{}
		cr := compiledRange{label: spec.Label}
		if spec.Start != nil {
			if math.IsNaN(*spec.Start) || math.IsInf(*spec.Start, 0) {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
					"labeled numeric-range start boundary is not a finite number",
					map[string]any{"label": spec.Label})
			}
			cr.hasStart, cr.start = true, *spec.Start
		}
		if spec.End != nil {
			if math.IsNaN(*spec.End) || math.IsInf(*spec.End, 0) {
				return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
					"labeled numeric-range end boundary is not a finite number",
					map[string]any{"label": spec.Label})
			}
			cr.hasEnd, cr.end = true, *spec.End
		}
		if cr.hasStart && cr.hasEnd && cr.start >= cr.end {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_INVALID,
				"labeled numeric-range start boundary is not below its end boundary (ranges are half-open [start, end))",
				map[string]any{"label": spec.Label, "start": cr.start, "end": cr.end})
		}
		ranges = append(ranges, cr)
	}
	sort.SliceStable(ranges, func(i, j int) bool {
		a, b := ranges[i], ranges[j]
		if a.hasStart != b.hasStart {
			return !a.hasStart
		}
		if !a.hasStart {
			return false
		}
		return a.start < b.start
	})
	for i := 1; i < len(ranges); i++ {
		prev, cur := ranges[i-1], ranges[i]
		// Half-open: [a, b) and [c, d) with a <= c are disjoint iff b <= c.
		if !(prev.hasEnd && cur.hasStart && prev.end <= cur.start) {
			return nil, errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_OVERLAP,
				"labeled numeric-range set contains overlapping ranges",
				map[string]any{"labels": []string{prev.label, cur.label}})
		}
	}
	return &Set{ranges: ranges}, nil
}

// params is the wire shape of GROUP_NUMERIC_RANGES params.
type params struct {
	Ranges         []Spec `json:"ranges,omitempty"`
	Table          string `json:"table,omitempty"`
	UnmatchedLabel string `json:"unmatched_label,omitempty"`
}

// Config is a parsed GROUP_NUMERIC_RANGES configuration. Exactly one of
// an inline source (Set + Order resolved) or Table is set.
type Config struct {
	// Set is the compiled inline range set; nil for a table source until
	// Resolve runs.
	Set *Set
	// Order is the supplied range labels in author order (the emission
	// order); nil for a table source until Resolve runs.
	Order []string
	// Table names a numeric range table, resolved later against the
	// instance's registry; "" for an inline source.
	Table string
	// UnmatchedLabel is the bucket of a row outside every range.
	UnmatchedLabel string
}

// Parse validates raw GROUP_NUMERIC_RANGES params: malformed params
// (PROCESSING_CONFIG), not exactly one of ranges / table
// (PULSE_RANGE_SOURCE_AMBIGUOUS), and for an inline source every Compile
// refusal plus an unmatched label colliding with a range label
// (PROCESSING_CONFIG). A table source is resolved by Resolve.
func Parse(raw json.RawMessage) (Config, error) {
	var p params
	paramread.Declare(paramread.Grouper, Operator, &p)
	if len(raw) > 0 {
		if err := paramread.Decode(paramread.Grouper, Operator, raw, &p); err != nil {
			return Config{}, errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("invalid %s params: %v", Operator, err))
		}
	}
	cfg := Config{UnmatchedLabel: DefaultUnmatchedLabel}
	if p.UnmatchedLabel != "" {
		cfg.UnmatchedLabel = p.UnmatchedLabel
	}
	table := strings.TrimSpace(p.Table)
	if err := daterange.SourceAmbiguity(Operator, len(p.Ranges) > 0, table != ""); err != nil {
		return Config{}, err
	}
	if table != "" {
		cfg.Table = table
		return cfg, nil
	}
	if err := cfg.Resolve(p.Ranges); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Resolve compiles specs (the inline ranges, or a resolved numeric
// table's) into c.Set and records the author order, refusing an unmatched
// label that collides with a range label.
func (c *Config) Resolve(specs []Spec) error {
	set, err := Compile(specs)
	if err != nil {
		return err
	}
	order := make([]string, 0, len(specs))
	for i := range specs {
		if specs[i].Label == c.UnmatchedLabel {
			return errors.NewCodedError(errors.PROCESSING_CONFIG,
				fmt.Sprintf("%s unmatched_label %q collides with a range label", Operator, c.UnmatchedLabel))
		}
		order = append(order, specs[i].Label)
	}
	c.Set, c.Order = set, order
	return nil
}
