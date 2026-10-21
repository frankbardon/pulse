package processing

import (
	"context"
	"fmt"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing/window"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
)

// DerivedColumn names one derived column DeriveRecords reads back per
// filtered record: a feature or attribute output off the record, or —
// Window set — a window output off the windowed row set.
type DerivedColumn struct {
	Name   string
	Window bool
}

// DeriveWarnings returns the Response-level warnings the last
// DeriveRecords run's attributes raised, in declared order (nil when
// none).
func (p *Processor) DeriveWarnings() []*types.ResponseWarning {
	return p.deriveWarnings
}

// DerivedEmit receives one filtered record: its 0-based position in
// the iterator's order (the source record index) and one value per
// requested column, in column order — a float64, or nil for a null
// cell. values is freshly allocated per call and owned by the callee.
type DerivedEmit func(source int64, values []any) error

// DeriveRecords is the record-level half of the buffered pipeline — the
// materialize path. It collects every record iter yields (polling ctx),
// then runs exactly the steps processRecords runs before grouping:
// features over the unfiltered set, filters, attributes over the
// filtered set, and — when req carries windows — the windows-only row
// materialisation plus every window over the filtered rows. It then
// calls emit once per filter-passing record with that record's source
// index and its derived values: in iteration order, or — req.Sort set —
// in the order Request.Sort gives Process's row set (window.SortOrder:
// the same comparator, stable ties in iteration order, nulls last in
// both directions). A sort key may name a source field, a feature
// output, an attribute label or a window label. Sorting changes only
// the emit order, never a value: every window has already run under
// its own partition_by / order_by. Only the permutation and each
// survivor's source index are held beside the records (plus one
// key-only cell map per survivor when there is no window row set to
// read the keys from); the rows themselves are never copied.
//
// Every value is stored as a float64: an integer window output
// (WIN_ROW_NUMBER / RANK / DENSE_RANK) widens losslessly, NaN stays
// NaN, an absent or null value is nil. A value of any other type (a
// lag/lead copying a categorical label, a decimal, a string default)
// fails with PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC before emit sees the
// record.
//
// The caller has validated req (no groups, aggregations, tests,
// regressions, matrices). Weight stamping matches Process. The
// attributes' Response-level warnings (a fitted vector attribute's
// singular or insufficient fit) are read back through DeriveWarnings.
func (p *Processor) DeriveRecords(ctx context.Context, req *types.Request, iter RecordIterator, cols []DerivedColumn, emit DerivedEmit) error {
	req = p.prepare(req)
	p.deriveWarnings = nil
	var all []*Record
	poll := NewCtxPoller(ctx)
	for iter.Next() {
		if err := poll.Poll(); err != nil {
			return err
		}
		all = append(all, iter.Record())
	}
	p.lap(observe.PhaseDecode)

	if err := p.applyFeatures(req.Features, all); err != nil {
		return err
	}
	// A before-filters attribute (ATTR_PREVIOUS_STATE) runs over every
	// record ahead of the filters; the rest over the survivors.
	filtered, _, warns, _, err := p.filterThenAttributes(req, all)
	if err != nil {
		return err
	}
	p.deriveWarnings = warns
	var rows []map[string]any
	if len(req.Windows) > 0 {
		// Only window outputs leave this row set; no u64 render needed.
		if rows, _, err = recordsToRows(filtered, nil); err != nil {
			return err
		}
		if err := p.applyWindows(ctx, rows, req.Windows, nil, nil); err != nil {
			return err
		}
	}

	// filtered is an order-preserving subsequence of all (the filter
	// pass appends survivors in input order), so one forward walk
	// recovers each survivor's source index — taken BEFORE the sort,
	// which then carries it explicitly through the permutation.
	srcOf := make([]int64, len(filtered))
	src := 0
	for i, rec := range filtered {
		for all[src] != rec {
			src++
		}
		srcOf[i] = int64(src)
		src++
	}
	var order []int
	if len(req.Sort) > 0 {
		keyRows := rows
		if keyRows == nil {
			if keyRows, err = sortKeyRows(filtered, req.Sort); err != nil {
				return err
			}
		}
		order = window.SortOrder(keyRows, req.Sort)
	}
	p.lap(observe.PhaseScan)

	for j := range filtered {
		if err := poll.Poll(); err != nil {
			return err
		}
		i := j
		if order != nil {
			i = order[j]
		}
		rec := filtered[i]
		values := make([]any, len(cols))
		for c, col := range cols {
			if col.Window {
				v, err := derivedFloat(col.Name, rows[i][col.Name])
				if err != nil {
					return err
				}
				values[c] = v
				continue
			}
			if v, ok := rec.NumericValue(col.Name); ok {
				values[c] = v
			}
		}
		if err := emit(srcOf[i], values); err != nil {
			return err
		}
	}
	return nil
}

// sortKeyRows builds, per record, the cells recordsToRows would hold
// under each sort key — nothing else — so a run with no window row set
// sorts on exactly the values Process's row set carries.
func sortKeyRows(records []*Record, keys []types.OrderKey) ([]map[string]any, error) {
	out := make([]map[string]any, len(records))
	for i, r := range records {
		m := make(map[string]any, len(keys))
		for _, k := range keys {
			v, ok, err := r.exprValue(k.Field)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			if _, isDec := v.(encoding.Decimal128); isDec {
				v = rowCell(r.fieldNamed(k.Field), v)
			}
			m[k.Field] = v
		}
		out[i] = m
	}
	return out, nil
}

// derivedFloat converts one window output cell to the materialized
// column's value: nil stays nil, a number becomes float64.
func derivedFloat(column string, v any) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case int32:
		return float64(x), nil
	case uint64:
		return float64(x), nil
	case uint32:
		return float64(x), nil
	}
	return nil, errors.NewCodedErrorWithDetails(errors.PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC,
		"derived column "+column+" holds a non-numeric value, which an f64 column cannot store",
		map[string]any{"column": column, "value_type": fmt.Sprintf("%T", v)})
}
