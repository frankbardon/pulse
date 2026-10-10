package processing

import (
	"context"
	stderrors "errors"
	"math"
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
