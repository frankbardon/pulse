package processing

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"sync"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/exprenv"
	"github.com/frankbardon/pulse/internal/numrange"
)

// rangeLabeler is range_label(table, x[, unmatched])'s implementation
// over one registry: the label of the registered range table's range
// holding x. Tables resolve through LookupRangeTable (so a hidden
// capability:range_tables answers every name as unknown) and compile
// once, on first use, into the same numrange / daterange sets the range
// groupers match with; safe for concurrent use (a compiled program runs
// on every decode worker).
//
// Semantics:
//   - a numeric table matches half-open [start, end) — exactly for an
//     integer past 2^53 and a decimal (numrange.Set.MatchRat);
//   - a date table matches inclusive epoch DAYS (a date field binds as
//     one; a datetime needs to_day() first — its epoch seconds are not
//     a day). A fractional day floors;
//   - a null x is null; a NaN, or a value no range holds, is the
//     unmatched label when given, else null;
//   - an unknown table is PULSE_RANGE_TABLE_UNKNOWN (checked before the
//     value, so an all-null column cannot hide a misspelt name); a value
//     the table's kind cannot match (a string, a bool, a list) is
//     PULSE_RANGE_TABLE_KIND_MISMATCH.
//
// A hit returns the table's own boxed label and a miss the caller's
// boxed unmatched argument, so a matched call allocates nothing.
type rangeLabeler struct {
	reg  *ExtensionRegistry
	sets sync.Map // table name (string) → *rangeLabelSet
}

// rangeLabelSet is one compiled table: exactly one of num / date, and
// every label boxed once.
type rangeLabelSet struct {
	num   *numrange.Set
	date  *daterange.Set
	boxed map[string]any
}

// newRangeLabeler is the range_label implementation over reg (nil: no
// table resolves).
func newRangeLabeler(reg *ExtensionRegistry) *rangeLabeler {
	return &rangeLabeler{reg: reg}
}

// call is the expr function body.
func (l *rangeLabeler) call(args ...any) (any, error) {
	if len(args) < 2 || len(args) > 3 {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("range_label() takes (table, value[, unmatched]), got %d argument(s)", len(args)))
	}
	set, err := l.resolve(args[0])
	if err != nil {
		return nil, err
	}
	x := args[1]
	if x == nil {
		return nil, nil
	}
	var (
		label string
		ok    bool
	)
	if set.num != nil {
		label, ok, err = matchNumeric(set.num, x)
	} else {
		label, ok, err = matchDay(set.date, x)
	}
	if err != nil {
		return nil, rangeLabelKindMismatch(args[0].(string), set, x)
	}
	if ok {
		return set.boxed[label], nil
	}
	if len(args) == 3 {
		switch args[2].(type) {
		case nil, string:
			return args[2], nil
		default:
			return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
				fmt.Sprintf("range_label() unmatched label must be a string, got %T", args[2]))
		}
	}
	return nil, nil
}

// resolve is the compiled table named by the table argument.
func (l *rangeLabeler) resolve(table any) (*rangeLabelSet, error) {
	name, ok := table.(string)
	if !ok {
		return nil, errors.NewCodedError(errors.PROCESSING_RUNTIME,
			fmt.Sprintf("range_label() table argument must be a string, got %T", table))
	}
	// Keyed by the argument as passed (already boxed): no allocation.
	if s, ok := l.sets.Load(table); ok {
		return s.(*rangeLabelSet), nil
	}
	rt, err := lookupRangeTable(exprenv.RangeLabelFunc, name, l.reg)
	if err != nil {
		return nil, err
	}
	set := &rangeLabelSet{}
	var labels []string
	if rt.Kind() == daterange.KindNumeric {
		if set.num, err = numrange.Compile(rt.NumericRanges); err != nil {
			return nil, err
		}
		for _, r := range rt.NumericRanges {
			labels = append(labels, r.Label)
		}
	} else {
		if set.date, err = daterange.Compile(rt.Ranges); err != nil {
			return nil, err
		}
		labels = set.date.Labels()
	}
	set.boxed = make(map[string]any, len(labels))
	for _, lb := range labels {
		set.boxed[lb] = lb
	}
	got, _ := l.sets.LoadOrStore(name, set)
	return got.(*rangeLabelSet), nil
}

// errRangeLabelKind marks a value the table's kind cannot match.
var errRangeLabelKind = fmt.Errorf("range_label: value kind")

// exactFloatInt is the largest integer magnitude a float64 holds
// exactly (2^53).
const exactFloatInt = 1 << 53

// matchNumeric matches x against a numeric table: a float exactly as
// numrange does (NaN matches nothing), an integer within 2^53 through
// its exact float64, a larger integer or a decimal as an exact rational.
func matchNumeric(s *numrange.Set, x any) (string, bool, error) {
	var (
		label string
		ok    bool
	)
	switch v := x.(type) {
	case float64:
		label, ok = s.Match(v)
	case float32:
		label, ok = s.Match(float64(v))
	case int:
		label, ok = matchInt(s, int64(v))
	case int64:
		label, ok = matchInt(s, v)
	case int32:
		label, ok = s.Match(float64(v))
	case int16:
		label, ok = s.Match(float64(v))
	case int8:
		label, ok = s.Match(float64(v))
	case exprenv.U64:
		label, ok = matchUint(s, uint64(v))
	case uint64:
		label, ok = matchUint(s, v)
	case uint:
		label, ok = matchUint(s, uint64(v))
	case uint32:
		label, ok = s.Match(float64(v))
	case uint16:
		label, ok = s.Match(float64(v))
	case uint8:
		label, ok = s.Match(float64(v))
	case exprenv.Decimal:
		label, ok = s.MatchRat(decimalRat(v))
	default:
		return "", false, errRangeLabelKind
	}
	return label, ok, nil
}

// matchInt matches a signed integer exactly.
func matchInt(s *numrange.Set, v int64) (string, bool) {
	if v <= exactFloatInt && v >= -exactFloatInt {
		return s.Match(float64(v))
	}
	return s.MatchRat(new(big.Rat).SetInt64(v))
}

// matchUint matches an unsigned integer exactly.
func matchUint(s *numrange.Set, v uint64) (string, bool) {
	if v <= exactFloatInt {
		return s.Match(float64(v))
	}
	return s.MatchRat(new(big.Rat).SetUint64(v))
}

// decimalRat is a decimal's exact value, mantissa / 10^scale.
func decimalRat(d exprenv.Decimal) *big.Rat {
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.Scale)), nil)
	return new(big.Rat).SetFrac(d.Value.Mantissa(), den)
}

// matchDay matches x, an epoch-day number, against a date table
// (inclusive bounds). A fractional day floors; a non-finite one matches
// nothing; a magnitude past int64 clamps (it still orders past every
// finite bound, so an open-ended range holds it).
func matchDay(s *daterange.Set, x any) (string, bool, error) {
	var day int64
	switch v := x.(type) {
	case float64:
		d, ok := floorDay(v)
		if !ok {
			return "", false, nil
		}
		day = d
	case float32:
		d, ok := floorDay(float64(v))
		if !ok {
			return "", false, nil
		}
		day = d
	case int:
		day = int64(v)
	case int64:
		day = v
	case int32:
		day = int64(v)
	case int16:
		day = int64(v)
	case int8:
		day = int64(v)
	case exprenv.U64:
		day = clampUint(uint64(v))
	case uint64:
		day = clampUint(v)
	case uint:
		day = clampUint(uint64(v))
	case uint32:
		day = int64(v)
	case uint16:
		day = int64(v)
	case uint8:
		day = int64(v)
	case exprenv.Decimal:
		d, ok := floorDay(v.Float64())
		if !ok {
			return "", false, nil
		}
		day = d
	default:
		return "", false, errRangeLabelKind
	}
	label, ok := s.Match(day)
	return label, ok, nil
}

// floorDay is v's day (floored), false for a non-finite v.
func floorDay(v float64) (int64, bool) {
	switch {
	case math.IsNaN(v) || math.IsInf(v, 0):
		return 0, false
	case v >= math.MaxInt64:
		return math.MaxInt64, true
	case v <= math.MinInt64:
		return math.MinInt64, true
	}
	return int64(math.Floor(v)), true
}

// clampUint is v as an int64, clamped at MaxInt64.
func clampUint(v uint64) int64 {
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

// rangeLabelKindMismatch is PULSE_RANGE_TABLE_KIND_MISMATCH for a value
// the table's kind cannot match: details {operator, table, want, got},
// want the table's kind, got the value's.
func rangeLabelKindMismatch(table string, set *rangeLabelSet, x any) error {
	want := daterange.KindNumeric
	reads := "a number"
	if set.date != nil {
		want = daterange.KindDate
		reads = "an epoch-day number (a date field, or to_day(datetime))"
	}
	got := rangeLabelValueKind(x)
	return errors.NewCodedErrorWithDetails(errors.PULSE_RANGE_TABLE_KIND_MISMATCH,
		exprenv.RangeLabelFunc+": range table "+strconv.Quote(table)+" is a "+want+" table; it matches "+reads+", got a "+got,
		map[string]any{"operator": exprenv.RangeLabelFunc, "table": table, "want": want, "got": got})
}

// rangeLabelValueKind names a value's kind for the mismatch refusal.
func rangeLabelValueKind(x any) string {
	switch x.(type) {
	case string:
		return "string"
	case bool:
		return "bool"
	case []string, []any, []float64:
		return "list"
	}
	return fmt.Sprintf("%T", x)
}
