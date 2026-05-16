package service

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// sortRowsByKey returns a copy of rows sorted by the value at key. Used
// to compare grouped query results that come back in non-deterministic
// map-iteration order on both projection paths.
func sortRowsByKey(rows []map[string]any, key string) []map[string]any {
	out := make([]map[string]any, len(rows))
	copy(out, rows)
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i][key]) < fmt.Sprint(out[j][key])
	})
	return out
}

// wideSchema returns a 6-field schema covering scalar numeric and
// categorical types. Used to exercise projection across heterogeneous
// field-type advancement on the wire.
func wideSchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", "green", "blue", "yellow"} {
		_, _ = dict.Add(v)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "v", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1},
			{Name: "color", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 12, CsvColumnIdx: 2, Dictionary: dict},
			{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 13, CsvColumnIdx: 3},
			{Name: "y", Type: encoding.FieldTypeU16, ByteOffset: 21, CsvColumnIdx: 4},
			{Name: "z", Type: encoding.FieldTypeF32, ByteOffset: 23, CsvColumnIdx: 5},
		},
	}
}

func wideRecords() [][]uint64 {
	out := make([][]uint64, 0, 12)
	for i := uint64(0); i < 12; i++ {
		out = append(out, []uint64{
			i + 1,                            // id
			math.Float64bits(float64(i)),     // v
			i % 4,                            // color
			math.Float64bits(float64(i * 2)), // x
			i % 65535,                        // y
			uint64(math.Float32bits(float32(i) * 0.5)),
		})
	}
	return out
}

// runBoth runs the request twice — once with projection disabled, once with
// it enabled — and returns the two responses. The caller compares them.
func runBoth(t *testing.T, req *types.Request) (full, proj *types.Response) {
	t.Helper()

	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())

	svcOff := New(cfg)
	respOff, err := svcOff.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process (projection off): %v", err)
	}

	svcOn := New(cfg)
	svcOn.SetProjectBufferedFields(true)
	respOn, err := svcOn.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process (projection on): %v", err)
	}

	return respOff, respOn
}

func TestProjection_BufferedMatchesFullDecode_Aggregation(t *testing.T) {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "v", Label: "sum_v"},
			{Type: types.AGG_AVERAGE, Field: "x", Label: "avg_x"},
		},
	}
	full, proj := runBoth(t, req)
	if !reflect.DeepEqual(full.Data, proj.Data) {
		t.Errorf("data differs:\n full: %v\n proj: %v", full.Data, proj.Data)
	}
}

func TestProjection_BufferedMatchesFullDecode_Grouped(t *testing.T) {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Groups: []*types.Group{
			{Type: types.GROUP_CATEGORY, Field: "color"},
		},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_AVERAGE, Field: "v", Label: "avg_v"},
			{Type: types.AGG_COUNT, Field: "v", Label: "n"},
		},
	}
	full, proj := runBoth(t, req)
	fullSorted := sortRowsByKey(full.Data, "color")
	projSorted := sortRowsByKey(proj.Data, "color")
	if !reflect.DeepEqual(fullSorted, projSorted) {
		t.Errorf("grouped data differs:\n full: %v\n proj: %v", fullSorted, projSorted)
	}
}

func TestProjection_BufferedMatchesFullDecode_FilterExpression(t *testing.T) {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Filterers: []*types.Filterer{{
			Type:       types.FILTER_EXPRESSION,
			Expression: "v > 3 && x < 18",
		}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "v", Label: "n"},
		},
	}
	full, proj := runBoth(t, req)
	if !reflect.DeepEqual(full.Data, proj.Data) {
		t.Errorf("filter-expr data differs:\n full: %v\n proj: %v", full.Data, proj.Data)
	}
}

func TestProjection_BufferedMatchesFullDecode_AttributeFormula(t *testing.T) {
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Attributes: []*types.Attribute{{
			Type:       types.ATTR_FORMULA,
			Field:      "v",
			Expression: "v + x",
			Label:      "sum_vx",
		}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "sum_vx", Label: "total"},
		},
	}
	full, proj := runBoth(t, req)
	if !reflect.DeepEqual(full.Data, proj.Data) {
		t.Errorf("attribute-formula data differs:\n full: %v\n proj: %v", full.Data, proj.Data)
	}
}

func TestProjection_ByteCursorAlignmentWhenSkipping(t *testing.T) {
	// Request reads only y (16-bit field positioned AFTER a heterogeneous
	// run of u32/f64/categorical/f64). Projection skips id/v/color/x but
	// bytes for them still advance — y decodes correctly only when the
	// byte cursor stays aligned.
	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "y", Label: "sum_y"},
		},
	}
	full, proj := runBoth(t, req)
	if !reflect.DeepEqual(full.Data, proj.Data) {
		t.Fatalf("byte-cursor alignment broke when fields were skipped:\n full: %v\n proj: %v", full.Data, proj.Data)
	}
}

func TestProjection_RespectsDisableFlag(t *testing.T) {
	// With projection disabled, even narrow requests should decode every
	// field — verified indirectly by stream iterator returning records
	// whose values map contains every schema field.
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())
	svc := New(cfg)
	// projection NOT enabled.

	iter := newStreamingIterator(cfg.Fs(), "wide.pulse", schema)
	defer iter.Close()
	svc.applyProjection(iter, &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "v"},
		},
	}, schema)
	if iter.project != nil {
		t.Errorf("expected iter.project == nil when ProjectBufferedFields is off")
	}
}

func TestProjection_ProjectsToNarrowSet(t *testing.T) {
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())
	svc := New(cfg)
	svc.SetProjectBufferedFields(true)

	iter := newStreamingIterator(cfg.Fs(), "wide.pulse", schema)
	defer iter.Close()
	svc.applyProjection(iter, &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "v"},
		},
	}, schema)
	if iter.project == nil {
		t.Fatalf("expected projection filter installed on iter")
	}
	if !iter.project("v") {
		t.Error("filter should accept 'v'")
	}
	if iter.project("id") {
		t.Error("filter should reject 'id'")
	}
	if !iter.Next() {
		t.Fatalf("iter.Next: %v", iter.Err())
	}
	rec := iter.Record()
	if _, ok := rec.NumericValue("v"); !ok {
		t.Error("record should have 'v' populated")
	}
	if _, ok := rec.NumericValue("id"); ok {
		t.Error("record should NOT have 'id' populated under projection")
	}
}

func TestProjection_WideRequestSkipsProjection(t *testing.T) {
	// A request touching every field shouldn't install projection (no
	// point — full decode would do the same work without the filter
	// indirection).
	schema := wideSchema()
	cfg := setupTestFS(t, "wide.pulse", schema, wideRecords())
	svc := New(cfg)
	svc.SetProjectBufferedFields(true)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "wide.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "id"},
			{Type: types.AGG_SUM, Field: "v"},
			{Type: types.AGG_SUM, Field: "color"},
			{Type: types.AGG_SUM, Field: "x"},
			{Type: types.AGG_SUM, Field: "y"},
			{Type: types.AGG_SUM, Field: "z"},
		},
	}

	iter := newStreamingIterator(cfg.Fs(), "wide.pulse", schema)
	defer iter.Close()
	svc.applyProjection(iter, req, schema)
	if iter.project != nil {
		t.Errorf("expected no projection installed when request touches every field")
	}
}
