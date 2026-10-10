package processing

import (
	"context"
	stderrors "errors"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func deriveSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "v", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

func deriveRecords(s *encoding.Schema) []*Record {
	var out []*Record
	for i := 0; i < 6; i++ {
		if i == 3 {
			out = append(out, NewRecordWithNulls(s, map[string]float64{"id": float64(i)}, map[string]bool{"v": true}))
			continue
		}
		out = append(out, NewRecord(s, map[string]float64{"id": float64(i), "v": float64(i * 10)}))
	}
	return out
}

// TestDeriveRecords_SourceIndexAndValues: survivors of a filter that
// skips records keep their source index; a feature value, a null
// feature value and an integer window value (row number) come back as
// float64 / nil in column order.
func TestDeriveRecords_SourceIndexAndValues(t *testing.T) {
	s := deriveSchema()
	req := &types.Request{
		Features:  []*types.Feature{{Type: types.FEAT_SQRT, Field: "v"}},
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: "id != 1 && id != 4"}},
		Windows:   []*types.Window{{Type: types.WIN_ROW_NUMBER, OrderBy: []types.OrderKey{{Field: "id"}}}},
	}
	p := NewProcessor(s)
	var srcs []int64
	var vals [][]any
	err := p.DeriveRecords(context.Background(), req, NewSliceIterator(deriveRecords(s)),
		[]DerivedColumn{{Name: "SQRT_v"}, {Name: "WIN_ROW_NUMBER", Window: true}},
		func(src int64, v []any) error {
			srcs = append(srcs, src)
			vals = append(vals, v)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{0, 2, 3, 5}; len(srcs) != len(want) || srcs[0] != 0 || srcs[1] != 2 || srcs[2] != 3 || srcs[3] != 5 {
		t.Fatalf("sources = %v, want %v", srcs, want)
	}
	if vals[1][0] != math.Sqrt(20) {
		t.Errorf("SQRT_v at source 2 = %v", vals[1][0])
	}
	if vals[2][0] != nil {
		t.Errorf("SQRT_v at the null source = %v, want nil", vals[2][0])
	}
	for i, v := range vals {
		if v[1] != float64(i+1) {
			t.Errorf("row %d WIN_ROW_NUMBER = %#v, want float64 %d", i, v[1], i+1)
		}
	}
}

// TestDeriveRecords_RefusesNonNumericAndStopsOnEmitError: a window
// copying a string is PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC; an emit
// error stops the walk and is returned unchanged.
func TestDeriveRecords_RefusesNonNumericAndStopsOnEmitError(t *testing.T) {
	s := deriveSchema()
	lag := &types.Request{Windows: []*types.Window{{Type: types.WIN_LAG, Field: "v",
		OrderBy: []types.OrderKey{{Field: "id"}}, Params: []byte(`{"default":"none"}`)}}}
	err := NewProcessor(s).DeriveRecords(context.Background(), lag, NewSliceIterator(deriveRecords(s)),
		[]DerivedColumn{{Name: "WIN_LAG_v", Window: true}}, func(int64, []any) error { return nil })
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC || ce.Details["value_type"] != "string" {
		t.Fatalf("err = %v, want PULSE_MATERIALIZE_OUTPUT_NOT_NUMERIC on a string", err)
	}

	stop := stderrors.New("stop")
	calls := 0
	req := &types.Request{Features: []*types.Feature{{Type: types.FEAT_LOG, Field: "v"}}}
	err = NewProcessor(s).DeriveRecords(context.Background(), req, NewSliceIterator(deriveRecords(s)),
		[]DerivedColumn{{Name: "LOG_v"}}, func(int64, []any) error { calls++; return stop })
	if err != stop || calls != 1 {
		t.Fatalf("err = %v after %d calls, want stop after 1", err, calls)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewProcessor(s).DeriveRecords(ctx, req, NewSliceIterator(deriveRecords(s)),
		[]DerivedColumn{{Name: "LOG_v"}}, func(int64, []any) error { return nil }); err == nil {
		t.Fatal("cancelled ctx: no error")
	}
}

// sortSchema / sortRecords: id 0..5, g = id%2, v = id*10 with v null
// at id 3.
func sortSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "g", Type: encoding.FieldTypeU8},
		{Name: "v", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

func sortRecords(s *encoding.Schema) []*Record {
	var out []*Record
	for i := 0; i < 6; i++ {
		vals := map[string]float64{"id": float64(i), "g": float64(i % 2)}
		if i == 3 {
			out = append(out, NewRecordWithNulls(s, vals, map[string]bool{"v": true}))
			continue
		}
		vals["v"] = float64(i * 10)
		out = append(out, NewRecord(s, vals))
	}
	return out
}

// derivedRun is one DeriveRecords run: the emitted source order and
// each source's values.
func derivedRun(t *testing.T, req *types.Request, cols []DerivedColumn) ([]int64, map[int64][]any) {
	t.Helper()
	s := sortSchema()
	var srcs []int64
	by := map[int64][]any{}
	err := NewProcessor(s).DeriveRecords(context.Background(), req, NewSliceIterator(sortRecords(s)), cols,
		func(src int64, v []any) error {
			srcs = append(srcs, src)
			by[src] = v
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return srcs, by
}

// TestDeriveRecords_SortMovesRowsNotValues: Request.Sort reorders the
// emitted records — on a source field (desc, nulls last), on a feature
// output, on a window label, with ties kept in source order — while
// every derived value, the running sum ordered by id included, stays
// the value of the unsorted run.
func TestDeriveRecords_SortMovesRowsNotValues(t *testing.T) {
	zero := 0
	base := func(sort ...types.OrderKey) *types.Request {
		return &types.Request{
			Features: []*types.Feature{{Type: types.FEAT_SQRT, Field: "v"}},
			Windows: []*types.Window{{Type: types.WIN_RUNNING_SUM, Field: "v", Label: "cum",
				OrderBy: []types.OrderKey{{Field: "id"}}, Frame: &types.FrameSpec{Mode: "rows", Following: &zero}}},
			Sort: sort,
		}
	}
	cols := []DerivedColumn{{Name: "SQRT_v"}, {Name: "cum", Window: true}}
	unsorted, want := derivedRun(t, base(), cols)
	if got := fmtSrcs(unsorted); got != "0,1,2,3,4,5" {
		t.Fatalf("unsorted order = %s", got)
	}
	if want[2][1] != 30.0 || want[5][1] != 120.0 {
		t.Fatalf("running sum = %v / %v, want 30 / 120", want[2][1], want[5][1])
	}
	for _, c := range []struct {
		name  string
		sort  []types.OrderKey
		order string
	}{
		{"source field desc, null last", []types.OrderKey{{Field: "v", Desc: true}}, "5,4,2,1,0,3"},
		{"ties keep source order", []types.OrderKey{{Field: "g"}}, "0,2,4,1,3,5"},
		{"feature output desc", []types.OrderKey{{Field: "SQRT_v", Desc: true}}, "5,4,2,1,0,3"},
		{"window label desc", []types.OrderKey{{Field: "cum", Desc: true}}, "5,4,2,3,1,0"},
		{"two keys", []types.OrderKey{{Field: "g", Desc: true}, {Field: "id", Desc: true}}, "5,3,1,4,2,0"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srcs, got := derivedRun(t, base(c.sort...), cols)
			if o := fmtSrcs(srcs); o != c.order {
				t.Fatalf("order = %s, want %s", o, c.order)
			}
			for src, v := range want {
				if !sameCells(got[src], v) {
					t.Errorf("source %d values %v, unsorted run %v", src, got[src], v)
				}
			}
		})
	}

	// No window: the keys are read off the records (sortKeyRows).
	req := &types.Request{Features: []*types.Feature{{Type: types.FEAT_SQRT, Field: "v"}},
		Sort: []types.OrderKey{{Field: "SQRT_v", Desc: true}}}
	if srcs, _ := derivedRun(t, req, []DerivedColumn{{Name: "SQRT_v"}}); fmtSrcs(srcs) != "5,4,2,1,0,3" {
		t.Fatalf("featureless-window sort order = %s", fmtSrcs(srcs))
	}
	req.Sort = []types.OrderKey{{Field: "g", Desc: true}, {Field: "v"}}
	if srcs, _ := derivedRun(t, req, []DerivedColumn{{Name: "SQRT_v"}}); fmtSrcs(srcs) != "1,5,3,0,2,4" {
		t.Fatalf("source-field sort order = %s", fmtSrcs(srcs))
	}
}

func fmtSrcs(s []int64) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = strconv.FormatInt(v, 10)
	}
	return strings.Join(parts, ",")
}

func sameCells(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		af, aok := a[i].(float64)
		bf, bok := b[i].(float64)
		switch {
		case a[i] == nil && b[i] == nil:
		case aok && bok && (af == bf || (math.IsNaN(af) && math.IsNaN(bf))):
		default:
			return false
		}
	}
	return true
}
