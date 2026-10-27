package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestChainStageSchema: group key columns categorical_u32, one f64
// per aggregation column — an expanding vector aggregation's element
// columns in member order (the label absent), a plain one its label —
// and an aggregation column colliding with an earlier column is
// PROCESSING_CONFIG with the column listed once.
func TestChainStageSchema(t *testing.T) {
	t.Parallel()

	in := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "b1", Type: encoding.FieldTypeF64},
		{Name: "b2", Type: encoding.FieldTypeF64},
	}}
	expand := json.RawMessage(`{"expand": true}`)
	req := &types.Request{
		Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m", Params: expand},
			{Type: types.AGG_VEC_SUM, Fields: []string{"b1", "b2"}, Label: "s"},
			{Type: types.AGG_COUNT, Field: "b1", Label: "n"},
		},
	}
	schema, cerr := ChainStageSchema(req, in, AggregationKinds{})
	if cerr != nil {
		t.Fatalf("refused: %v", cerr)
	}
	type col struct {
		name string
		typ  encoding.FieldType
	}
	var got []col
	for _, f := range schema.Fields {
		got = append(got, col{f.Name, f.Type})
	}
	want := []col{
		{"region", encoding.FieldTypeCategoricalU32},
		{"m[b1]", encoding.FieldTypeF64},
		{"m[b2]", encoding.FieldTypeF64},
		{"s", encoding.FieldTypeF64},
		{"n", encoding.FieldTypeF64},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("columns = %v, want %v", got, want)
	}

	// Without the input schema the members cannot resolve: the label.
	bare, _ := ChainStageSchema(req, nil, AggregationKinds{})
	if bare.Field("m") == nil || bare.Field("m[b1]") != nil {
		t.Errorf("no input schema: fields %v, want the label m", bare.Fields)
	}

	// An element column colliding with a group key column.
	collide := &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "m[b2]"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Fields: []string{"b1", "b2"}, Label: "m", Params: expand}},
	}
	schema, cerr = ChainStageSchema(collide, in, AggregationKinds{})
	if cerr == nil || cerr.Code != errors.PROCESSING_CONFIG || cerr.Details["column"] != "m[b2]" || cerr.Details["aggregation_index"] != 0 {
		t.Fatalf("collision = %v, want PROCESSING_CONFIG column m[b2]", cerr)
	}
	if n := len(schema.Fields); n != 2 {
		t.Errorf("collision schema has %d fields, want 2 (the column once)", n)
	}
}

// TestResolveSpec_ExactFieldNameBeatsGlob: a fields entry naming a
// schema field exactly is that field even when it carries glob
// metacharacters — an expanded element column "m[b1]" a later chain
// stage reads — while the same entry over a schema without that field
// is still a glob ("m[b1]" matching mb and m1).
func TestResolveSpec_ExactFieldNameBeatsGlob(t *testing.T) {
	t.Parallel()

	f64 := func(n string) encoding.Field { return encoding.Field{Name: n, Type: encoding.FieldTypeF64} }
	spec := types.VectorSpec{Name: "v", Fields: []string{"m[b1]"}}
	r, cerr := ResolveSpec("vectors[0]", spec, &encoding.Schema{Fields: []encoding.Field{f64("mb"), f64("m[b1]"), f64("m1")}})
	if cerr != nil || !reflect.DeepEqual(r.Members, []string{"m[b1]"}) {
		t.Fatalf("exact name: members %v err %v, want [m[b1]]", r.Members, cerr)
	}
	r, cerr = ResolveSpec("vectors[0]", spec, &encoding.Schema{Fields: []encoding.Field{f64("mb"), f64("m1")}})
	if cerr != nil || !reflect.DeepEqual(r.Members, []string{"mb", "m1"}) {
		t.Fatalf("glob: members %v err %v, want [mb m1]", r.Members, cerr)
	}
}
