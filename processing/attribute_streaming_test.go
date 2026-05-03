package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// TestAttribute_FormulaStreamingMatchesBuffered confirms the streaming
// path applies ATTR_FORMULA inline and produces the same aggregate as
// the buffered path with the same request.
func TestAttribute_FormulaStreamingMatchesBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "input score"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "score", 10),
		makeNumericRecord(t, schema, "score", 20),
		makeNumericRecord(t, schema, "score", 30),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Field: "score", Expression: "score * 2", Label: "doubled"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "doubled", Label: "sum_doubled"},
		},
	}

	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p.lastPath != PathStreaming {
		t.Fatalf("expected streaming path with row-local attribute, got %s", p.lastPath)
	}
	got, ok := resp.Data[0]["sum_doubled"].(float64)
	if !ok {
		t.Fatalf("sum_doubled type %T", resp.Data[0]["sum_doubled"])
	}
	want := 120.0 // (10+20+30)*2
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("sum_doubled = %v, want %v", got, want)
	}
}

// TestAttribute_PercentileForcesBuffered: ATTR_PERCENTILE has no
// streaming algorithm and remains buffered.
func TestAttribute_PercentileForcesBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeF64, Description: "input score"},
		},
	}
	records := []*Record{
		makeNumericRecord(t, schema, "score", 1),
		makeNumericRecord(t, schema, "score", 2),
		makeNumericRecord(t, schema, "score", 3),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_PERCENTILE, Field: "score", Label: "p"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "p", Label: "sum_p"},
		},
	}
	if _, err := p.Process(context.Background(), req, NewSliceIterator(records)); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p.lastPath != PathBuffered {
		t.Errorf("expected buffered path for ATTR_PERCENTILE, got %s", p.lastPath)
	}
}

// TestAttribute_DatePartStreamingMatchesBuffered: ATTR_DATE_PART is also
// row-local and should stream.
func TestAttribute_DatePartStreamingMatchesBuffered(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "d", Type: encoding.FieldTypeDate, Description: "date as days since epoch"},
		},
	}
	// Days 0, 31, 365 → years all 1970, 1970, 1971 → year sum = 5911.
	records := []*Record{
		NewRecord(schema, map[string]float64{"d": 0}),
		NewRecord(schema, map[string]float64{"d": 31}),
		NewRecord(schema, map[string]float64{"d": 365}),
	}

	p := NewProcessor(schema)
	req := &types.Request{
		Attributes: []*types.Attribute{
			{Type: types.ATTR_DATE_PART, Field: "d", Params: []byte(`{"part":"year"}`), Label: "yr"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "yr", Label: "sum_yr"},
		},
	}
	resp, err := p.Process(context.Background(), req, NewSliceIterator(records))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if p.lastPath != PathStreaming {
		t.Errorf("expected streaming path for ATTR_DATE_PART, got %s", p.lastPath)
	}
	got, ok := resp.Data[0]["sum_yr"].(float64)
	if !ok {
		t.Fatalf("sum_yr type %T", resp.Data[0]["sum_yr"])
	}
	want := 5911.0
	if got != want {
		t.Errorf("sum_yr = %v, want %v", got, want)
	}
}
