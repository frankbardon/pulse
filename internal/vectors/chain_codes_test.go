package vectors

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// chainCodeSchema: region (categorical, the grouper), color
// (categorical_u8), flag (packed_bool), x (f64).
func chainCodeSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "color", Type: encoding.FieldTypeCategoricalU8},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
}

// chainCodeNext is a synthesised-schema builder: group keys
// categorical_u32, aggregation outputs f64.
func chainCodeNext(r *types.Request) *encoding.Schema {
	s := &encoding.Schema{}
	for _, g := range r.Groups {
		s.Fields = append(s.Fields, encoding.Field{Name: g.Field, Type: encoding.FieldTypeCategoricalU32})
	}
	for _, a := range r.Aggregations {
		s.Fields = append(s.Fields, encoding.Field{Name: types.AggregationLabelOf(a), Type: encoding.FieldTypeF64})
	}
	return s
}

func TestChainCodeColumns(t *testing.T) {
	var k AggregationKinds
	g := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}}
	agg := func(typ types.AggregationType, field, label string) *types.Aggregation {
		return &types.Aggregation{Type: typ, Field: field, Label: label}
	}
	stages := []*types.Request{
		{Groups: g, Aggregations: []*types.Aggregation{agg(types.AGG_MODE, "color", "mc"), agg(types.AGG_MODE, "flag", "mf"),
			agg(types.AGG_MODE, "x", "mx"), agg(types.AGG_SUM, "x", "s"), agg(types.AGG_MODE_COUNT, "color", "n")}},
		{Groups: g, Aggregations: []*types.Aggregation{agg(types.AGG_MODE, "mc", "m2"), agg(types.AGG_SUM, "mf", "s2"),
			agg(types.AGG_MODE, "region", "mr"), agg(types.AGG_MODE, "mx", "m3")}},
		{Groups: g, Aggregations: []*types.Aggregation{agg(types.AGG_MODE, "m2", "m4"), agg(types.AGG_MODE, "s2", "m5")}},
	}
	s0 := chainCodeSchema()
	cases := []struct {
		idx  int
		want map[string]encoding.FieldType
	}{
		{0, nil},
		// Stage 0's picks over a categorical / packed_bool; not over x,
		// and no count / sum.
		{1, map[string]encoding.FieldType{"mc": encoding.FieldTypeCategoricalU8, "mf": encoding.FieldTypePackedBool}},
		// A pick over a forwarded code stays a code of the origin
		// type; a pick over a forwarded group key is a code of the key
		// column; a sum or a pick over a magnitude is not.
		{2, map[string]encoding.FieldType{"m2": encoding.FieldTypeCategoricalU8, "mr": encoding.FieldTypeCategoricalU32}},
		{3, map[string]encoding.FieldType{"m4": encoding.FieldTypeCategoricalU8}},
	}
	for _, c := range cases {
		if got := k.ChainCodeColumns(stages, c.idx, s0, chainCodeNext); !reflect.DeepEqual(got, c.want) {
			t.Errorf("stage %d: %v, want %v", c.idx, got, c.want)
		}
	}
	if got := k.ChainCodeColumns(stages, 2, nil, chainCodeNext); got != nil {
		t.Errorf("nil stage0 judged %v", got)
	}
	if got := k.ChainCodeColumns(stages, 4, s0, chainCodeNext); got != nil {
		t.Errorf("out-of-range stage judged %v", got)
	}

	// CodeFieldTypes: a code column answers its origin type, every other
	// field its own, an unknown field nothing.
	look := CodeFieldTypes(chainCodeNext(stages[0]), map[string]encoding.FieldType{"mc": encoding.FieldTypeCategoricalU8})
	if ft, ok := look("mc"); !ok || ft != encoding.FieldTypeCategoricalU8 {
		t.Errorf("mc = %v %v", ft, ok)
	}
	if ft, ok := look("mx"); !ok || ft != encoding.FieldTypeF64 {
		t.Errorf("mx = %v %v", ft, ok)
	}
	if _, ok := look("nope"); ok {
		t.Error("unknown field typed")
	}
	if CodeFieldTypes(nil, nil) != nil {
		t.Error("nil schema lookup")
	}
}
