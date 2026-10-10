package processing

import (
	"context"
	"fmt"

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
// calls emit once per filter-passing record, in iteration order, with
// that record's source index and its derived values.
//
// Every value is stored as a float64: an integer window output
// (WIN_ROW_NUMBER / RANK / DENSE_RANK) widens losslessly, NaN stays
// NaN, an absent or null value is nil. A value of any other type (a
// lag/lead copying a categorical label, a decimal, a string default)
// fails with PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC before emit sees the
// record.
//
// The caller has validated req (no groups, aggregations, sort, tests,
// regressions, matrices). Weight stamping matches Process.
func (p *Processor) DeriveRecords(ctx context.Context, req *types.Request, iter RecordIterator, cols []DerivedColumn, emit DerivedEmit) error {
	req = p.stampWeights(req)
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
	filtered, _, err := p.applyFiltersWithCounters(req.Filterers, all)
	if err != nil {
		return err
	}
	if err := p.applyAttributes(req.Attributes, filtered); err != nil {
		return err
	}
	var rows []map[string]any
	if len(req.Windows) > 0 {
		rows = recordsToRows(filtered)
		if err := window.ApplyWithExt(ctx, rows, req.Windows, p.exts.LookupWindow); err != nil {
			return err
		}
	}
	p.lap(observe.PhaseScan)

	// filtered is an order-preserving subsequence of all (the filter
	// pass appends survivors in input order), so one forward walk
	// recovers each survivor's source index.
	src := 0
	for i, rec := range filtered {
		if err := poll.Poll(); err != nil {
			return err
		}
		for all[src] != rec {
			src++
		}
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
		if err := emit(int64(src), values); err != nil {
			return err
		}
		src++
	}
	return nil
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
