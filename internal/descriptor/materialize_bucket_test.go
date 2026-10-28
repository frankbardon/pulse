package descriptor

import (
	"encoding/json"
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/daterange"
	"github.com/frankbardon/pulse/internal/numrange"
	"github.com/frankbardon/pulse/types"
)

// bucketInstance registers the numeric table "band" (lo / hi) and the
// date table "q" (Q1 / Q2, a gap after Q2).
func bucketInstance() *InstanceSnapshot {
	ten := 10.0
	s1, e1, s2, e2 := "2024-01-01", "2024-03-31", "2024-04-01", "2024-06-30"
	return NewInstanceSnapshot(&ExtensionsSnapshot{
		RangeTables: []descriptor.RangeTableMeta{
			{Name: "band", Kind: daterange.KindNumeric, RangeCount: 2},
			{Name: "q", Kind: daterange.KindDate, RangeCount: 2},
		},
		RangeTableSpecs: map[string]RangeTableSpecs{
			"band": {Numeric: []numrange.Spec{{Label: "lo", End: &ten}, {Label: "hi", Start: &ten}}},
			"q":    {Dates: []daterange.Spec{{Label: "Q1", Start: &s1, End: &e1}, {Label: "Q2", Start: &s2, End: &e2}}},
		},
	}, FeatureSet{})
}

// TestMaterializeOutputLayout_Bucket: a bucket retypes its column to the
// narrowest categorical holding the labels (257 labels → categorical_u16)
// with the labels in declared order, marks it Rebuild, compiles the
// ranges (inline or the instance's table) for the run, and feeds a
// following `type` cast the bucketed categorical. MaterializePlan stamps
// a datetime bucket with the request zone. The source schema is never
// mutated.
func TestMaterializeOutputLayout_Bucket(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU16, Description: "Count of the source"},
		{Name: "ts", Type: encoding.FieldTypeDateTime, Nullable: true, Description: "Instant of the source"},
	}}
	many := make([]types.MaterializeBucketRange, 257)
	for i := range many {
		many[i] = types.MaterializeBucketRange{Label: "r" + strconv.Itoa(i), Start: json.RawMessage(strconv.Itoa(i)), End: json.RawMessage(strconv.Itoa(i + 1))}
	}
	req := &types.Request{TimeZone: "Europe/Berlin", Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "x", As: "inline", Bucket: &types.MaterializeBucket{Ranges: []types.MaterializeBucketRange{
			{Label: "hi", Start: json.RawMessage("10")}, {Label: "lo", End: json.RawMessage("10")}}}},
		{From: "x", As: "table", Bucket: &types.MaterializeBucket{RangeTable: "band"}},
		{From: "x", As: "wide", Bucket: &types.MaterializeBucket{Ranges: many}},
		{From: "x", As: "cast", Type: "categorical_u16", Bucket: &types.MaterializeBucket{RangeTable: "band"}},
		{From: "ts", Bucket: &types.MaterializeBucket{RangeTable: "q"}},
		{From: "x", As: "plain"},
	}}}
	_, layout, ce := MaterializePlan(req, schema, bucketInstance(), "src.pulse", "")
	if ce != nil {
		t.Fatal(ce)
	}
	for i, want := range []struct {
		typ    encoding.FieldType
		labels []string
	}{
		{encoding.FieldTypeCategoricalU8, []string{"hi", "lo"}},
		{encoding.FieldTypeCategoricalU8, []string{"lo", "hi"}},
		{encoding.FieldTypeCategoricalU16, nil},
		{encoding.FieldTypeCategoricalU16, []string{"lo", "hi"}},
		{encoding.FieldTypeCategoricalU8, []string{"Q1", "Q2"}},
	} {
		col := layout[i]
		if col.Field.Type != want.typ || !col.Rebuild || col.Bucket == nil {
			t.Errorf("%s = %s rebuild=%v bucket=%v, want a rebuilt %s bucket", col.Field.Name, col.Field.Type, col.Rebuild, col.Bucket != nil, want.typ)
			continue
		}
		if want.labels != nil && !slices.Equal(col.Field.Dictionary.Values(), want.labels) {
			t.Errorf("%s dictionary = %v, want %v", col.Field.Name, col.Field.Dictionary.Values(), want.labels)
		}
	}
	if n := layout[2].Field.Dictionary.Count(); n != 257 {
		t.Errorf("wide holds %d labels, want 257", n)
	}
	if c := layout[3].Cast; c == nil || c.From.Type != encoding.FieldTypeCategoricalU8 || layout[3].Bucket.To.Type != encoding.FieldTypeCategoricalU8 {
		t.Errorf("cast after bucket = %+v, want a categorical_u8 → categorical_u16 cast", c)
	}
	if layout[0].Cast != nil || layout[0].Bucket.Numeric == nil || layout[0].Bucket.Zone != "" {
		t.Errorf("inline bucket = %+v", layout[0].Bucket)
	}
	if b := layout[4].Bucket; b.Dates == nil || b.Zone != "Europe/Berlin" || !layout[4].Field.Nullable {
		t.Errorf("datetime bucket = %+v nullable=%v, want date ranges in Europe/Berlin, nullable inherited", b, layout[4].Field.Nullable)
	}
	if layout[5].Bucket != nil || layout[5].Rebuild || layout[5].Field.Type != encoding.FieldTypeU16 {
		t.Errorf("plain column touched: %+v", layout[5])
	}
	if schema.Fields[0].Type != encoding.FieldTypeU16 || schema.Fields[0].Dictionary != nil {
		t.Error("the layout mutated the source schema")
	}

	// Without an instance registering it, the table is unknown.
	_, ce = MaterializeOutputLayout(&types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "x", Bucket: &types.MaterializeBucket{RangeTable: "band"}}}}}, schema, nil, nil)
	if ce == nil || ce.Code != errors.PULSE_RANGE_TABLE_UNKNOWN || ce.Details["path"] != "output.columns[0].bucket.range_table" || ce.Details["column"] != "x" {
		t.Errorf("no instance: %v", ce)
	}
}

// TestMaterializeRisks_Bucket: a bucket whose ranges leave a gap, or
// that reads a float column (a NaN lies in no range), is a risk coded by
// on_cast_error at path .bucket; gapless ranges over an integer column
// are none.
func TestMaterializeRisks_Bucket(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU16, Description: "Count of the source"},
		{Name: "f", Type: encoding.FieldTypeF64, Nullable: true, Description: "Score of the source"},
		{Name: "d", Type: encoding.FieldTypeDate, Description: "Day of the source"},
	}}
	band, yes := &types.MaterializeBucket{RangeTable: "band"}, true
	req := &types.Request{Output: &types.MaterializeOutput{Columns: []types.MaterializeOutputColumn{
		{From: "x", Bucket: band},
		{From: "f", Bucket: band},
		{From: "d", OnCastError: "null", Nullable: &yes, Bucket: &types.MaterializeBucket{RangeTable: "q"}},
	}}}
	layout, ce := MaterializeOutputLayout(req, schema, nil, bucketInstance())
	if ce != nil {
		t.Fatal(ce)
	}
	risks := MaterializeRisks(req, schema, layout)
	if len(risks) != 2 {
		t.Fatalf("risks %+v, want f and d", risks)
	}
	if r := risks[0]; r.Column != "f" || r.Path != "output.columns[1].bucket" || r.Code != string(errors.PULSE_MATERIALIZE_CAST_FAILED) {
		t.Errorf("f risk %+v", r)
	}
	if r := risks[1]; r.Column != "d" || r.Path != "output.columns[2].bucket" || r.Code != string(errors.PULSE_MATERIALIZE_CAST_NULLED) {
		t.Errorf("d risk %+v", r)
	}
}
