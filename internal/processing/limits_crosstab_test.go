package processing

import (
	"context"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// crosstabLimitsFixture: a 3-key row field r and a 2-key column field c.
// pairs lists the (r, c) dictionary indexes of each record, in order.
func crosstabLimitsFixture(t *testing.T, pairs [][2]float64) (*encoding.Schema, []*Record) {
	t.Helper()
	dict := func(vs ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vs {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "r", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict("r0", "r1", "r2")},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: dict("c0", "c1")},
		{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 2},
	}}
	recs := make([]*Record, len(pairs))
	for i, p := range pairs {
		recs[i] = NewRecord(schema, map[string]float64{"r": p[0], "c": p[1], "v": float64(i)})
	}
	return schema, recs
}

func rByC() *types.Request {
	return &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c"}},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "v", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
	}}
}

func maxCrosstabCells(n int64) limits.Limits {
	l := limits.Defaults()
	l.MaxCrosstabCells = n
	return l
}

// requireMaxCrosstabCells asserts err is PULSE_LIMIT_EXCEEDED on
// max_crosstab_cells at configured with the observed grid.
func requireMaxCrosstabCells(t *testing.T, err error, configured, observed int64) {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_LIMIT_EXCEEDED {
		t.Fatalf("err = %v, want PULSE_LIMIT_EXCEEDED", err)
	}
	if ce.Details["limit"] != string(limits.MaxCrosstabCells) || ce.Details["configured"] != configured || ce.Details["observed"] != observed {
		t.Fatalf("details = %v, want limit=max_crosstab_cells configured=%d observed=%d", ce.Details, configured, observed)
	}
}

// TestMaxCrosstabCells_Arms: a 3 x 2 grid under MaxCrosstabCells=5
// trips on the buffered arm after partitioning, and on the fused arm at
// whichever interner would grow the grid past the limit — the row
// interner when the last new key is a row, the column interner when it
// is a column. Each runs at 6; a processor without SetLimits enforces
// nothing.
func TestMaxCrosstabCells_Arms(t *testing.T) {
	rowLast := [][2]float64{{0, 0}, {0, 1}, {1, 0}, {2, 0}} // grid 1x1, 1x2, 2x2, 3x2
	colLast := [][2]float64{{0, 0}, {1, 0}, {2, 0}, {0, 1}} // grid 1x1, 2x1, 3x1, 3x2
	run := func(fused bool, pairs [][2]float64, l *limits.Limits) (*types.Response, error) {
		schema, recs := crosstabLimitsFixture(t, pairs)
		p := NewProcessor(schema)
		if l != nil {
			p.SetLimits(*l)
		}
		if fused {
			return p.RunCrosstabFused(context.Background(), rByC(), NewSliceIterator(recs))
		}
		return p.RunCrosstab(context.Background(), rByC(), recs)
	}
	for _, c := range []struct {
		name  string
		fused bool
		pairs [][2]float64
	}{
		{"buffered", false, rowLast},
		{"fused row interner", true, rowLast},
		{"fused column interner", true, colLast},
	} {
		t.Run(c.name, func(t *testing.T) {
			over, at := maxCrosstabCells(5), maxCrosstabCells(6)
			resp, err := run(c.fused, c.pairs, &over)
			if resp != nil {
				t.Fatalf("a trip returned a partial result: %+v", resp)
			}
			requireMaxCrosstabCells(t, err, 5, 6)
			if _, err := run(c.fused, c.pairs, &at); err != nil {
				t.Fatalf("at the limit: %v", err)
			}
			if _, err := run(c.fused, c.pairs, nil); err != nil {
				t.Fatalf("no limits installed: %v", err)
			}
		})
	}
}
