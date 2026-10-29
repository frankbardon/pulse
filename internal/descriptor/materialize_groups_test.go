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

// TestMaterializeGroupPlan_DeclaredRefusals: a declaration the cohort
// writer would refuse is refused with the writer's code, details.path
// naming the (last) group it involves — output.groups[i] — and the
// writer's own details kept.
func TestMaterializeGroupPlan_DeclaredRefusals(t *testing.T) {
	t.Parallel()

	s := groupedSourceSchema()
	layout := []MaterializeOutputField{srcCol(s, 0, "k"), srcCol(s, 1, "a"), srcCol(s, 5, "x")}
	for _, tc := range []struct {
		name   string
		groups []types.MaterializeGroup
		code   errors.Code
		path   string
	}{
		{"unknown member", []types.MaterializeGroup{{Key: []string{"k"}, Members: []string{"a"}}, {Members: []string{"nope"}}},
			errors.PULSE_GROUP_FIELD_UNKNOWN, "output.groups[1]"},
		{"member in two groups", []types.MaterializeGroup{{Members: []string{"k", "a"}}, {Members: []string{"x"}}, {Members: []string{"a"}}},
			errors.PULSE_GROUP_FIELD_CONFLICT, "output.groups[2]"},
		{"empty group", []types.MaterializeGroup{{Members: []string{}}},
			errors.PULSE_GROUP_DECLARATION_INVALID, "output.groups[0]"},
		{"key repeated as a member", []types.MaterializeGroup{{Members: []string{"x"}}, {Key: []string{"k"}, Members: []string{"k", "a"}}},
			errors.PULSE_GROUP_DECLARATION_INVALID, "output.groups[1]"},
	} {
		g, ce := MaterializeGroupPlan(&types.MaterializeOutput{Groups: tc.groups}, s, layout)
		if ce == nil {
			t.Errorf("%s: planned %+v, want %s", tc.name, g, tc.code)
			continue
		}
		if ce.Code != tc.code || ce.Details["path"] != tc.path || ce.Details["group_label"] == nil && ce.Details["group_labels"] == nil {
			t.Errorf("%s: %s %v, want %s at %s with the writer's details", tc.name, ce.Code, ce.Details, tc.code, tc.path)
		}
	}
}

// TestMaterializeGroupPlan_DeclaredRisksAndNarrow: a declared keyed
// group with determined members is planned and carries one
// PULSE_GROUP_MEMBER_NOT_CONSTANT risk at its output.groups path; a
// tuple group carries none; a group no wider than its row index is
// reported dropped_too_narrow with a PULSE_GROUP_TOO_NARROW finding and
// no risk. An inherited group never carries a risk.
func TestMaterializeGroupPlan_DeclaredRisksAndNarrow(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU32},
		{Name: "a", Type: encoding.FieldTypeF64},
		{Name: "t1", Type: encoding.FieldTypeF64},
		{Name: "t2", Type: encoding.FieldTypeF64},
		{Name: "tiny", Type: encoding.FieldTypeU8},
		{Name: "tk", Type: encoding.FieldTypeU8},
	}}
	layout := []MaterializeOutputField{srcCol(s, 0, "k"), srcCol(s, 1, "a"), srcCol(s, 2, "t1"), srcCol(s, 3, "t2"), srcCol(s, 4, "tiny"), srcCol(s, 5, "tk")}
	out := &types.MaterializeOutput{Groups: []types.MaterializeGroup{
		{Key: []string{"k"}, Members: []string{"a"}},
		{Members: []string{"t1", "t2"}},
		{Key: []string{"tk"}, Members: []string{"tiny"}},
	}}
	g, ce := MaterializeGroupPlan(out, s, layout)
	if ce != nil {
		t.Fatal(ce)
	}
	if len(g.Plan) != 3 || g.Plan[0].Verdict != MaterializeGroupPlanned || g.Plan[0].Origin != "declared" ||
		g.Plan[1].Verdict != MaterializeGroupPlanned || g.Plan[2].Verdict == MaterializeGroupPlanned {
		t.Fatalf("Plan = %+v, want two planned groups then one dropped too narrow", g.Plan)
	}
	if len(g.TooNarrow) != 1 || g.TooNarrow[0].Code != errors.PULSE_GROUP_TOO_NARROW {
		t.Errorf("TooNarrow = %v, want one PULSE_GROUP_TOO_NARROW", g.TooNarrow)
	}
	risks := MaterializeGroupRisks(g)
	if len(risks) != 1 || risks[0].Path != "output.groups[0]" || risks[0].Column != g.Plan[0].Label ||
		risks[0].Code != string(errors.PULSE_GROUP_MEMBER_NOT_CONSTANT) {
		t.Errorf("risks = %+v, want one member-not-constant risk on group 0", risks)
	}

	if r := MaterializeGroupRisks(nil); r == nil || len(r) != 0 {
		t.Errorf("nil plan risks = %#v, want empty, never nil", r)
	}
	// The same keyed group inherited from the source carries no risk.
	src := groupedSourceSchema()
	inh, ce := MaterializeGroupPlan(&types.MaterializeOutput{}, src, []MaterializeOutputField{srcCol(src, 0, "k"), srcCol(src, 1, "a")})
	if ce != nil {
		t.Fatal(ce)
	}
	if len(inh.Decls) != 1 || len(MaterializeGroupRisks(inh)) != 0 {
		t.Errorf("inherited plan %+v risks %v, want one inherited group and no risk", inh.Plan, MaterializeGroupRisks(inh))
	}
}

// TestMaterializeOutputSchema_Vectors: the flat output schema carries
// each kept vec_* source field's VECTORS entry re-indexed to its output
// position; a derived column, a non-vector field and a nil source carry
// none.
func TestMaterializeOutputSchema_Vectors(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32},
			{Name: "emb", Type: encoding.FieldTypeVecF32},
		},
		Vectors: []encoding.VectorField{{FieldIndex: 1, Dim: 3, Labels: []string{"x", "y", "z"}}},
	}
	layout := []MaterializeOutputField{
		{Field: encoding.Field{Name: "d", Type: encoding.FieldTypeF64, Nullable: true}, Source: -1, Derived: 0},
		srcCol(s, 1, "vec"),
		srcCol(s, 0, "id"),
	}
	out := MaterializeOutputSchema(s, layout)
	if len(out.Fields) != 3 || out.Fields[1].Name != "vec" {
		t.Fatalf("fields = %+v", out.Fields)
	}
	if len(out.Vectors) != 1 || out.Vectors[0].FieldIndex != 1 || out.Vectors[0].Dim != 3 || !reflect.DeepEqual(out.Vectors[0].Labels, []string{"x", "y", "z"}) {
		t.Errorf("vectors = %+v, want emb's entry at output index 1", out.Vectors)
	}
	if s.Vectors[0].FieldIndex != 1 {
		t.Error("the source schema's vector entry was mutated")
	}
	layout[0], layout[1] = layout[1], layout[0]
	if v := MaterializeOutputSchema(s, layout).Vectors; len(v) != 1 || v[0].FieldIndex != 0 {
		t.Errorf("reordered vectors = %+v, want index 0", v)
	}
	if v := MaterializeOutputSchema(nil, layout).Vectors; len(v) != 0 {
		t.Errorf("nil source vectors = %+v, want none", v)
	}
}

// TestUnchangedColumn_Changes: a source field no column writes is
// "dropped"; the first column reading it names the change — bucketed,
// recoded or retyped — and an unchanged copy anywhere wins.
func TestUnchangedColumn_Changes(t *testing.T) {
	t.Parallel()

	f := encoding.Field{Name: "v", Type: encoding.FieldTypeU32}
	col := func(mod func(*MaterializeOutputField)) MaterializeOutputField {
		c := MaterializeOutputField{Field: f, Source: 0, Derived: -1}
		if mod != nil {
			mod(&c)
		}
		return c
	}
	bucketed := col(func(c *MaterializeOutputField) { c.Rebuild, c.Bucket = true, &MaterializeBucket{} })
	recoded := col(func(c *MaterializeOutputField) { c.Rebuild, c.Recode = true, map[string]*string{} })
	retyped := col(func(c *MaterializeOutputField) { c.Field.Type = encoding.FieldTypeF64 })
	other := MaterializeOutputField{Field: f, Source: 1, Derived: -1}
	for _, tc := range []struct {
		name         string
		layout       []MaterializeOutputField
		written, how string
	}{
		{"dropped", []MaterializeOutputField{other}, "", "dropped"},
		{"bucketed first", []MaterializeOutputField{bucketed, recoded}, "", "bucketed"},
		{"recoded first", []MaterializeOutputField{recoded, retyped}, "", "recoded"},
		{"retyped", []MaterializeOutputField{retyped}, "", "retyped"},
		{"unchanged copy after a change", []MaterializeOutputField{retyped, col(func(c *MaterializeOutputField) { c.Field.Name = "kept" })}, "kept", ""},
	} {
		written, how := unchangedColumn(f, 0, tc.layout)
		if written != tc.written || how != tc.how {
			t.Errorf("%s: (%q, %q), want (%q, %q)", tc.name, written, how, tc.written, tc.how)
		}
	}
}
