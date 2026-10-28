package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// groupedSourceSchema: a keyed group (k key, a determined), a tuple
// group (t1, t2: every member key) and a constant group (c, elided).
func groupedSourceSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "k", Type: encoding.FieldTypeU32},
			{Name: "a", Type: encoding.FieldTypeF64},
			{Name: "t1", Type: encoding.FieldTypeU32},
			{Name: "t2", Type: encoding.FieldTypeU32},
			{Name: "c", Type: encoding.FieldTypeF64},
			{Name: "x", Type: encoding.FieldTypeF64},
		},
		Groups: []encoding.Group{
			{Kind: encoding.GroupKindIndexed, Members: []encoding.GroupMember{{Field: 0, Key: true}, {Field: 1}}},
			{Kind: encoding.GroupKindIndexed, Members: []encoding.GroupMember{{Field: 2, Key: true}, {Field: 3, Key: true}}},
			{Kind: encoding.GroupKindConstant, Members: []encoding.GroupMember{{Field: 4, Key: true}}},
		},
	}
}

// srcCol is a layout column copying source field i under name.
func srcCol(s *encoding.Schema, i int, name string) MaterializeOutputField {
	f := s.Fields[i]
	f.Name = name
	return MaterializeOutputField{Field: f, Source: i, Derived: -1}
}

// TestMaterializeGroupPlan_Inherit: with an output block and no
// output.groups, a keyed group is inherited keyed and a tuple group as a
// tuple, under the written names — a member written twice by its first
// unchanged copy — while a constant group is never inherited and a
// group whose member is re-coded is left out with one warning.
func TestMaterializeGroupPlan_Inherit(t *testing.T) {
	t.Parallel()

	s := groupedSourceSchema()
	retyped := srcCol(s, 3, "t2")
	retyped.Field.Type, retyped.Rebuild, retyped.Cast = encoding.FieldTypeF64, true, &MaterializeCast{From: s.Fields[3]}
	layout := []MaterializeOutputField{
		retyped,
		srcCol(s, 1, "a_copy"), // a rebuilt-free copy comes first
		srcCol(s, 0, "key"),
		srcCol(s, 1, "attr"),
		srcCol(s, 2, "t1"),
		srcCol(s, 4, "c"),
		srcCol(s, 5, "x"),
	}
	g, ce := MaterializeGroupPlan(&types.MaterializeOutput{}, s, layout)
	if ce != nil {
		t.Fatal(ce)
	}
	if want := []types.MaterializeGroup{{Key: []string{"key"}, Members: []string{"a_copy"}}}; !reflect.DeepEqual(g.Decls, want) {
		t.Errorf("Decls = %+v, want %+v", g.Decls, want)
	}
	if len(g.Plan) != 2 {
		t.Fatalf("Plan = %+v, want the inherited group then the left-out one", g.Plan)
	}
	if p := g.Plan[0]; p.Label != "group 1 [key: key]" || p.Origin != "inherited" || p.Verdict != MaterializeGroupPlanned ||
		!reflect.DeepEqual(p.SourceFields, []string{"k", "a"}) {
		t.Errorf("Plan[0] = %+v", p)
	}
	if p := g.Plan[1]; p.Label != "source group 2 [t1,t2]" || p.Verdict != MaterializeGroupNotInherited || p.Member != "t2" ||
		p.Change != "retyped" || p.Key != nil || !reflect.DeepEqual(p.Members, []string{"t1", "t2"}) {
		t.Errorf("Plan[1] = %+v", p)
	}
	if len(g.Warnings) != 1 || g.Warnings[0].Code != errors.PULSE_MATERIALIZE_GROUP_NOT_INHERITED ||
		g.Warnings[0].Details["group"] != "source group 2 [t1,t2]" {
		t.Errorf("Warnings = %v, want one PULSE_MATERIALIZE_GROUP_NOT_INHERITED for source group 2", g.Warnings)
	}

	// A tuple group kept whole is inherited as a tuple (no key).
	layout[0] = srcCol(s, 3, "t2")
	g, ce = MaterializeGroupPlan(&types.MaterializeOutput{}, s, layout)
	if ce != nil {
		t.Fatal(ce)
	}
	if len(g.Decls) != 2 || g.Decls[1].Key != nil || !reflect.DeepEqual(g.Decls[1].Members, []string{"t1", "t2"}) || len(g.Warnings) != 0 {
		t.Errorf("Decls = %+v, warnings %v, want the tuple group inherited keyless", g.Decls, g.Warnings)
	}

	// No output block, or groups: [] — nothing planned.
	for _, out := range []*types.MaterializeOutput{nil, {Groups: []types.MaterializeGroup{}}} {
		g, ce := MaterializeGroupPlan(out, s, layout)
		if ce != nil || len(g.Decls) != 0 || len(g.Plan) != 0 || len(g.Warnings) != 0 {
			t.Errorf("output %+v: plan %+v (%v), want nothing", out, g, ce)
		}
	}
}
