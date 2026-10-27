package vectors

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// nativeSchema has two native vector fields — emb (vec_f64, dim 3,
// labels, kind scale 1..7) and raw (vec_f32, dim 2, no metadata) —
// beside scalar columns.
func nativeSchema() *encoding.Schema {
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32},
			{Name: "emb", Type: encoding.FieldTypeVecF64, Nullable: true},
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "raw", Type: encoding.FieldTypeVecF32},
			{Name: "emb1", Type: encoding.FieldTypeF64},
			{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		},
		Vectors: []encoding.VectorField{
			{FieldIndex: 1, Dim: 3, Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 1, ScaleMax: 7, Labels: []string{"lo", "mid", "hi"}},
			{FieldIndex: 3, Dim: 2},
		},
	}
}

func TestResolve_NativeNamedDirectly(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	got, err := Resolve(nil, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "emb" || got[1].Name != "raw" {
		t.Fatalf("natives = %+v, want emb then raw", got)
	}
	emb := got[0]
	if emb.Native == nil || emb.Native.FieldIndex != 1 || emb.Native.Dim != 3 || emb.Native.Type != encoding.FieldTypeVecF64 || !emb.Native.HasLabels {
		t.Errorf("emb native marker = %+v", emb.Native)
	}
	if !slices.Equal(emb.Members, []string{"emb[1]", "emb[2]", "emb[3]"}) {
		t.Errorf("emb members = %v", emb.Members)
	}
	if !slices.Equal(emb.Labels, []string{"lo", "mid", "hi"}) {
		t.Errorf("emb labels = %v, want the schema labels", emb.Labels)
	}
	if emb.Kind != types.VectorKindScale || !emb.HasScale || emb.ScaleMin != 1 || emb.ScaleMax != 7 {
		t.Errorf("emb kind/scale = %q %v %v..%v, want the schema's", emb.Kind, emb.HasScale, emb.ScaleMin, emb.ScaleMax)
	}
	if want := []MemberRef{{"emb", 0}, {"emb", 1}, {"emb", 2}}; !reflect.DeepEqual(emb.Refs, want) {
		t.Errorf("emb refs = %v, want %v", emb.Refs, want)
	}
	raw := got[1]
	if raw.Kind != "" || raw.HasScale || raw.Native.HasLabels {
		t.Errorf("raw carries metadata it does not declare: %+v", raw)
	}
	if !slices.Equal(raw.Labels, []string{"raw_1", "raw_2"}) {
		t.Errorf("raw labels = %v, want the f_<i> defaults", raw.Labels)
	}
	if !slices.Equal(emb.SourceFields(), []string{"emb"}) {
		t.Errorf("emb source fields = %v", emb.SourceFields())
	}

	// After the spec-backed vectors, index alignment kept.
	got, err = Resolve([]types.VectorSpec{{Name: "v", Fields: []string{"x"}}}, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "v" || got[0].Native != nil || got[1].Name != "emb" {
		t.Errorf("resolved = %+v, want v then the natives", got)
	}
	if r, ok := Find(got, "raw"); !ok || r.Native == nil {
		t.Errorf("Find(raw) = %+v, %v", r, ok)
	}

	// A vector-free schema resolves nothing extra (byte-identical).
	if got, err := Resolve(nil, testSchema()); err != nil || got != nil {
		t.Errorf("vector-free schema: %v, %v", got, err)
	}
}

func TestResolve_NativeCollisionRefused(t *testing.T) {
	t.Parallel()

	_, err := Resolve([]types.VectorSpec{{Name: "emb", Fields: []string{"x"}}}, nativeSchema())
	if err == nil || err.Code != errors.PULSE_VECTOR_NAME_COLLISION || err.Details["native"] != true || err.Details["field"] != "emb" {
		t.Fatalf("err = %v, want PULSE_VECTOR_NAME_COLLISION {native: true, field: emb}", err)
	}
	// A plain field collision is not marked native.
	_, err = Resolve([]types.VectorSpec{{Name: "x", Fields: []string{"emb1"}}}, nativeSchema())
	if err == nil || err.Code != errors.PULSE_VECTOR_NAME_COLLISION || err.Details["native"] != nil {
		t.Fatalf("err = %v, want an unmarked PULSE_VECTOR_NAME_COLLISION", err)
	}
}

func TestResolveSpec_NativeExpansion(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	cases := []struct {
		name    string
		spec    types.VectorSpec
		members []string
		labels  []string
	}{
		{"literal expands in place, labels carried", types.VectorSpec{Fields: []string{"x", "emb", "emb1"}},
			[]string{"x", "emb[1]", "emb[2]", "emb[3]", "emb1"}, []string{"x", "lo", "mid", "hi", "emb1"}},
		{"element member re-scopes", types.VectorSpec{Fields: []string{"emb[3]", "raw[1]"}},
			[]string{"emb[3]", "raw[1]"}, []string{"hi", "raw_1"}},
		{"glob matches the vector field", types.VectorSpec{Fields: []string{"ra*", "x"}},
			[]string{"raw[1]", "raw[2]", "x"}, []string{"raw_1", "raw_2", "x"}},
		{"pattern matches the vector field", types.VectorSpec{Pattern: "^emb"},
			[]string{"emb[1]", "emb[2]", "emb[3]", "emb1"}, []string{"lo", "mid", "hi", "emb1"}},
		{"spec labels win", types.VectorSpec{Fields: []string{"raw"}, Labels: []string{"p", "q"}},
			[]string{"raw[1]", "raw[2]"}, []string{"p", "q"}},
		// Out of range / non-canonical: not an element member, so the
		// entry keeps its glob meaning ([1] matches emb1).
		{"bracket glob when not an element", types.VectorSpec{Fields: []string{"emb[01]"}}, []string{"emb1"}, []string{"emb1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := ResolveSpec("vectors[0]", c.spec, s)
			if err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !slices.Equal(r.Members, c.members) {
				t.Errorf("members = %v, want %v", r.Members, c.members)
			}
			if c.labels != nil && !slices.Equal(r.Labels, c.labels) {
				t.Errorf("labels = %v, want %v", r.Labels, c.labels)
			}
		})
	}

	// Mixed refs locate every member; scalar-only resolutions carry none.
	r, _ := ResolveSpec("v", types.VectorSpec{Fields: []string{"x", "emb[2]"}}, s)
	if want := []MemberRef{{"x", -1}, {"emb", 1}}; !reflect.DeepEqual(r.Refs, want) {
		t.Errorf("refs = %v, want %v", r.Refs, want)
	}
	if !slices.Equal(r.SourceFields(), []string{"x", "emb"}) {
		t.Errorf("source fields = %v", r.SourceFields())
	}
	r, _ = ResolveSpec("v", types.VectorSpec{Fields: []string{"x", "emb1"}}, s)
	if r.Refs != nil || r.Ref(1) != (MemberRef{"emb1", -1}) {
		t.Errorf("scalar-only refs = %v / %v", r.Refs, r.Ref(1))
	}

	// Refusals: a repeated element, an element past dim, and a
	// non-numeric neighbour still judged after the elements expand.
	if _, err := ResolveSpec("v", types.VectorSpec{Name: "v", Fields: []string{"r*"}}, s); err == nil ||
		err.Code != errors.PULSE_VECTOR_MEMBER_TYPE || err.Details["field"] != "region" {
		t.Errorf("categorical beside a native: %v", err)
	}
	if _, err := ResolveSpec("v", types.VectorSpec{Name: "v", Fields: []string{"emb", "emb[2]"}}, s); err == nil ||
		err.Code != errors.PULSE_VECTOR_DUPLICATE || err.Details["field"] != "emb[2]" {
		t.Errorf("duplicate element: %v", err)
	}
	if _, err := ResolveSpec("v", types.VectorSpec{Name: "v", Fields: []string{"emb[4]"}}, s); err == nil || err.Code != errors.PULSE_VECTOR_EMPTY {
		t.Errorf("element past dim: %v, want PULSE_VECTOR_EMPTY (a glob matching nothing)", err)
	}
}

// TestResolve_ReKindLeavesSchema: a virtual vector over a native field
// carries the SPEC's kind and scale; the schema's metadata is never
// mutated, and the native named directly keeps it.
func TestResolve_ReKindLeavesSchema(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	before := nativeSchema()
	lo, hi := 0.0, 10.0
	got, err := Resolve([]types.VectorSpec{
		{Name: "m", Fields: []string{"emb"}, Kind: types.VectorKindMeasure},
		{Name: "sc", Fields: []string{"emb"}, Kind: types.VectorKindScale, ScaleMin: &lo, ScaleMax: &hi, Labels: []string{"a", "b", "c"}},
	}, s)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Kind != types.VectorKindMeasure || got[0].HasScale || got[0].Native != nil {
		t.Errorf("re-kinded vector = %+v", got[0])
	}
	if got[1].ScaleMin != 0 || got[1].ScaleMax != 10 || !slices.Equal(got[1].Labels, []string{"a", "b", "c"}) {
		t.Errorf("re-scaled vector = %+v", got[1])
	}
	if emb, _ := Find(got, "emb"); emb.Kind != types.VectorKindScale || emb.ScaleMin != 1 || !slices.Equal(emb.Labels, []string{"lo", "mid", "hi"}) {
		t.Errorf("native after re-kind = %+v", emb)
	}
	if !reflect.DeepEqual(s, before) {
		t.Errorf("schema mutated:\n%+v\nwant\n%+v", s, before)
	}
}

// TestSlots_AcceptNativeName: every vector-name slot resolves a native
// field named directly, with no Request.Vectors entry.
func TestSlots_AcceptNativeName(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	want := []string{"emb[1]", "emb[2]", "emb[3]"}
	t.Run("matrix", func(t *testing.T) {
		ms, err := ResolveMatrices(&types.Request{Matrices: []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "emb"}}}, s, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ms[0].Members.Members, want) || !ms[0].ExplicitLabels || !slices.Equal(ms[0].Members.Labels, []string{"lo", "mid", "hi"}) {
			t.Errorf("matrix members = %+v (explicit labels %v)", ms[0].Members, ms[0].ExplicitLabels)
		}
	})
	t.Run("test", func(t *testing.T) {
		ins, err := ResolveTests(&types.Request{Tests: []*types.Test{{Type: types.TEST_BARTLETT_SPHERICITY, Vector: "emb"}}}, s, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ins[0].Members.Members, want) {
			t.Errorf("test members = %v", ins[0].Members.Members)
		}
	})
	t.Run("attribute", func(t *testing.T) {
		ins, err := ResolveAttributes(&types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "emb"}}}, s, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ins[0].Members.Members, want) {
			t.Errorf("attribute members = %v", ins[0].Members.Members)
		}
	})
	t.Run("group", func(t *testing.T) {
		g := &types.Group{Type: types.GROUP_KMEANS, Vector: "emb", Params: json.RawMessage(`{"k":2}`)}
		ins, err := ResolveGroups(&types.Request{Groups: []*types.Group{g}}, s, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ins[g].Members.Members, want) {
			t.Errorf("group members = %v", ins[g].Members.Members)
		}
	})
	t.Run("aggregation", func(t *testing.T) {
		a := &types.Aggregation{Type: types.AGG_VEC_MEAN, Vector: "emb"}
		ins, err := ResolveAggregations(&types.Request{Aggregations: []*types.Aggregation{a}}, s, nil, AggregationKinds{})
		if err != nil {
			t.Fatal(err)
		}
		in := ins[a]
		if !slices.Equal(in.Members.Members, want) {
			t.Errorf("aggregation members = %v", in.Members.Members)
		}
		// The literal-member rewrite re-resolves to the same elements
		// and labels (the factory's ResolveSpec).
		cp := AggregationSlotRewrite(a, in)
		p, perr := DecodeVecAggParams("aggregation", cp.Type, cp.Params)
		if perr != nil {
			t.Fatal(perr)
		}
		r, rerr := ResolveSpec("aggregation.fields", types.VectorSpec{Fields: cp.Fields, Labels: p.Labels}, s)
		if rerr != nil || !slices.Equal(r.Members, want) || !slices.Equal(r.Labels, []string{"lo", "mid", "hi"}) || !reflect.DeepEqual(r.Refs, in.Members.Refs) {
			t.Errorf("rewrite re-resolves to %+v, %v", r, rerr)
		}
	})
}

// TestProjection_NativeSourceFields: the projection helpers name the
// vector FIELD an element member reads, and a native field a slot names
// directly.
func TestProjection_NativeSourceFields(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	req := &types.Request{
		Vectors:      []types.VectorSpec{{Name: "v", Fields: []string{"x", "raw[2]"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_SUM, Fields: []string{"emb[1]", "emb1"}}},
		Matrices:     []types.MatrixSpec{{Type: types.MAT_CORRELATION, Vector: "emb"}},
	}
	if got, ok := Members(req, s, nil); !ok || !slices.Equal(got, []string{"x", "raw"}) {
		t.Errorf("Members = %v, %v", got, ok)
	}
	if got, ok := AggregationInputMembers(req, s); !ok || !slices.Equal(got, []string{"emb", "emb1"}) {
		t.Errorf("AggregationInputMembers = %v, %v", got, ok)
	}
	if got := ReferencedNatives(req, s); !slices.Equal(got, []string{"emb"}) {
		t.Errorf("ReferencedNatives = %v", got)
	}
	if got := MemberSourceFields([]string{"emb[2]", "x", "emb[1]", "nope[1]"}, s); !slices.Equal(got, []string{"emb", "x", "nope[1]"}) {
		t.Errorf("MemberSourceFields = %v", got)
	}
}

func TestParseElementMember(t *testing.T) {
	t.Parallel()

	s := nativeSchema()
	cases := []struct {
		in    string
		field string
		elem  int
		ok    bool
	}{
		{"emb[1]", "emb", 0, true},
		{"emb[3]", "emb", 2, true},
		{"raw[2]", "raw", 1, true},
		{"emb[0]", "", 0, false},
		{"emb[4]", "", 0, false},
		{"emb[01]", "", 0, false},
		{"emb[+1]", "", 0, false},
		{"x[1]", "", 0, false},
		{"emb", "", 0, false},
		{"[1]", "", 0, false},
	}
	for _, c := range cases {
		f, e, ok := ParseElementMember(c.in, s)
		if f != c.field || e != c.elem || ok != c.ok {
			t.Errorf("ParseElementMember(%q) = %q %d %v, want %q %d %v", c.in, f, e, ok, c.field, c.elem, c.ok)
		}
	}
	if _, _, ok := ParseElementMember("emb[1]", testSchema()); ok {
		t.Error("a vector-free schema parses an element member")
	}
}
