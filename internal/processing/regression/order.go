package regression

import (
	"cmp"
	"slices"

	"github.com/frankbardon/pulse/internal/regorder"
	"github.com/frankbardon/pulse/types"
)

// orderedRow is one placeable row of an ordered slot: its partition key
// and order value.
type orderedRow struct {
	rec      Record
	label    string  // the partition's categorical label (isLabel)
	num      float64 // the partition's numeric value (!isLabel)
	isLabel  bool
	value    int64 // the order.field value
	admitted bool  // target and every predictor present
}

// Arrange is the engine half of types.RegressionSpec.Order, run before
// any fit reads a row (FitEachBufferedWithContext, so every buffered
// driver — the run, a Compose fit, a holdout's train rows): records
// sorted by order.field — within each order.partition series, the
// series in ascending key order (labels lexicographically, numbers
// numerically) — stably, so equal keys keep record order. A row whose
// order.field or order.partition is null cannot be placed and drops
// listwise. The rows the fit admits (target and every predictor present
// and finite, regression.InputValue) then pass the gap policy
// (regorder.CheckSeries): a repeat or a gap is
// PROCESSING_REGRESSION_ORDER, the slot's own data refusal. A spec
// without an order returns records unchanged (byte-identical).
func Arrange(spec *types.RegressionSpec, records []Record) ([]Record, error) {
	return arrange(spec, records, true)
}

// SortOrdered is Arrange without the gap policy and without the drop:
// the placeable records sorted by spec's order, then every unplaceable
// one (a null order.field or order.partition) in record order, never
// refused — the order a holdout's held-out rows are scored in, every
// one still counted. A spec without an order
// returns records unchanged.
func SortOrdered(spec *types.RegressionSpec, records []Record) []Record {
	out, _ := arrange(spec, records, false)
	return out
}

func arrange(spec *types.RegressionSpec, records []Record, check bool) ([]Record, error) {
	if spec == nil || spec.Order == nil {
		return records, nil
	}
	o := spec.Order
	rows := make([]orderedRow, 0, len(records))
	var unplaced []Record
	for _, rec := range records {
		v, ok := InputValue(rec, o.Field)
		if !ok {
			unplaced = append(unplaced, rec)
			continue
		}
		r := orderedRow{rec: rec, value: int64(v)}
		if o.Partition != "" {
			key, ok := clusterKey(rec, o.Partition)
			if !ok {
				unplaced = append(unplaced, rec)
				continue
			}
			if key[0] == 's' {
				r.label, r.isLabel = key[2:], true
			} else {
				r.num, _ = InputValue(rec, o.Partition)
			}
		}
		r.admitted = admitted(spec, rec)
		rows = append(rows, r)
	}
	slices.SortStableFunc(rows, func(a, b orderedRow) int {
		if c := comparePartition(a, b); c != 0 {
			return c
		}
		return cmp.Compare(a.value, b.value)
	})
	out := make([]Record, len(rows))
	var series []regorder.Series
	last := -1 // the previous admitted row
	for i, r := range rows {
		out[i] = r.rec
		if !r.admitted {
			continue
		}
		if last < 0 || comparePartition(rows[last], r) != 0 {
			series = append(series, regorder.Series{Partition: partitionKey(o.Partition, r)})
		}
		last = i
		s := &series[len(series)-1]
		s.Values = append(s.Values, r.value)
	}
	if !check {
		return append(out, unplaced...), nil
	}
	if cerr := regorder.CheckSeries(spec, series); cerr != nil {
		return nil, cerr
	}
	return out, nil
}

// admitted reports whether the fit admits rec: its target and every
// predictor present and finite (the listwise rule every engine applies
// before its own model-specific ones).
func admitted(spec *types.RegressionSpec, rec Record) bool {
	if _, ok := InputValue(rec, spec.Target); !ok {
		return false
	}
	for _, p := range spec.Predictors {
		if _, ok := InputValue(rec, p); !ok {
			return false
		}
	}
	return true
}

// comparePartition orders two rows' partition keys: labels before
// numbers, labels lexicographically, numbers numerically.
func comparePartition(a, b orderedRow) int {
	switch {
	case a.isLabel && b.isLabel:
		return cmp.Compare(a.label, b.label)
	case a.isLabel:
		return -1
	case b.isLabel:
		return 1
	}
	return cmp.Compare(a.num, b.num)
}

// partitionKey is r's series key as the refusal names it: nil on an
// unpartitioned order, else the label or the number.
func partitionKey(partition string, r orderedRow) any {
	if partition == "" {
		return nil
	}
	if r.isLabel {
		return r.label
	}
	return r.num
}
