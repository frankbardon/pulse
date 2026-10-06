package vectors

import (
	"slices"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// testSchema is a mixed schema; schema order is the declaration order.
func testSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "q_3", Type: encoding.FieldTypeU8},
		{Name: "q_1", Type: encoding.FieldTypeU16},
		{Name: "q_2", Type: encoding.FieldTypeF64},
		{Name: "score", Type: encoding.FieldTypeF32},
		{Name: "small", Type: encoding.FieldTypeU4},
		{Name: "big", Type: encoding.FieldTypeU64},
		{Name: "mid", Type: encoding.FieldTypeU32},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "region16", Type: encoding.FieldTypeCategoricalU16},
		{Name: "region32", Type: encoding.FieldTypeCategoricalU32},
		{Name: "tags", Type: encoding.FieldTypeSetU8},
		{Name: "tags_wide", Type: encoding.FieldTypeSetU256},
		{Name: "day", Type: encoding.FieldTypeDate},
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "amount", Type: encoding.FieldTypeDecimal128},
	}}
}

func TestResolve_Ordering(t *testing.T) {
	cases := []struct {
		name string
		spec types.VectorSpec
		want []string
	}{
		{"literals keep caller order", types.VectorSpec{Name: "v", Fields: []string{"q_2", "q_3", "q_1"}}, []string{"q_2", "q_3", "q_1"}},
		{"glob expands in schema order", types.VectorSpec{Name: "v", Fields: []string{"q_*"}}, []string{"q_3", "q_1", "q_2"}},
		{"glob expands in place among literals", types.VectorSpec{Name: "v", Fields: []string{"score", "q_?", "mid"}}, []string{"score", "q_3", "q_1", "q_2", "mid"}},
		{"bracket glob", types.VectorSpec{Name: "v", Fields: []string{"q_[12]"}}, []string{"q_1", "q_2"}},
		{"regex in schema order", types.VectorSpec{Name: "v", Pattern: `^q_\d+$`}, []string{"q_3", "q_1", "q_2"}},
		{"regex is unanchored", types.VectorSpec{Name: "v", Pattern: `_[12]`}, []string{"q_1", "q_2"}},
		{"every integer and float type", types.VectorSpec{Name: "v", Fields: []string{"q_3", "q_1", "mid", "big", "small", "score", "q_2"}}, []string{"q_3", "q_1", "mid", "big", "small", "score", "q_2"}},
		{"packed_bool under binary", types.VectorSpec{Name: "v", Fields: []string{"flag", "q_1"}, Coerce: types.VectorCoerceBinary}, []string{"flag", "q_1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve([]types.VectorSpec{c.spec}, testSchema())
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if !slices.Equal(got[0].Members, c.want) {
				t.Errorf("members = %v, want %v", got[0].Members, c.want)
			}
			if !slices.Equal(got[0].Labels, c.want) {
				t.Errorf("default labels = %v, want the member names %v", got[0].Labels, c.want)
			}
		})
	}
}

func TestResolve_LabelsKept(t *testing.T) {
	got, err := Resolve([]types.VectorSpec{{Name: "v", Pattern: `^q_`, Labels: []string{"A", "B", "C"}}}, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got[0].Labels, []string{"A", "B", "C"}) {
		t.Errorf("labels = %v", got[0].Labels)
	}
}

func TestResolve_Refusals(t *testing.T) {
	cases := []struct {
		name   string
		specs  []types.VectorSpec
		code   errors.Code
		detail string // a details key the refusal must carry
		value  any    // its value
	}{
		{"empty name", []types.VectorSpec{{Fields: []string{"q_1"}}}, errors.PULSE_VECTOR_INVALID, "reason", "empty_name"},
		{"fields and pattern", []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}, Pattern: "q"}}, errors.PULSE_VECTOR_INVALID, "reason", "fields_and_pattern"},
		{"neither fields nor pattern", []types.VectorSpec{{Name: "v"}}, errors.PULSE_VECTOR_INVALID, "reason", "no_fields_or_pattern"},
		{"unknown coerce", []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}, Coerce: "ordinal"}}, errors.PULSE_VECTOR_INVALID, "reason", "unknown_coerce"},
		{"empty fields entry", []types.VectorSpec{{Name: "v", Fields: []string{"q_1", ""}}}, errors.PULSE_VECTOR_INVALID, "reason", "empty_field_entry"},
		{"bad glob", []types.VectorSpec{{Name: "v", Fields: []string{"q_["}}}, errors.PULSE_VECTOR_INVALID, "reason", "bad_glob"},
		{"bad regex", []types.VectorSpec{{Name: "v", Pattern: "q_("}}, errors.PULSE_VECTOR_INVALID, "reason", "bad_pattern"},
		{"unknown literal", []types.VectorSpec{{Name: "v", Fields: []string{"q_9"}}}, errors.SERVICE_VALIDATION, "field", "q_9"},
		{"duplicate name", []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}}, {Name: "v", Fields: []string{"q_2"}}}, errors.PULSE_VECTOR_DUPLICATE, "name", "v"},
		{"duplicate literal", []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_1"}}}, errors.PULSE_VECTOR_DUPLICATE, "field", "q_1"},
		{"literal repeated by a glob", []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_*"}}}, errors.PULSE_VECTOR_DUPLICATE, "field", "q_1"},
		{"empty pattern resolution", []types.VectorSpec{{Name: "v", Pattern: "^nope"}}, errors.PULSE_VECTOR_EMPTY, "pattern", "^nope"},
		{"empty glob resolution", []types.VectorSpec{{Name: "v", Fields: []string{"zz*"}}}, errors.PULSE_VECTOR_EMPTY, "name", "v"},
		{"labels too few", []types.VectorSpec{{Name: "v", Pattern: "^q_", Labels: []string{"a"}}}, errors.PULSE_VECTOR_LABELS_MISMATCH, "members", 3},
		{"labels too many", []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}, Labels: []string{"a", "b"}}}, errors.PULSE_VECTOR_LABELS_MISMATCH, "labels", 2},
		// One per refused member-type family, each naming the field.
		{"packed_bool without coerce", []types.VectorSpec{{Name: "v", Fields: []string{"flag"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "flag"},
		{"categorical_u8", []types.VectorSpec{{Name: "v", Fields: []string{"region"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "region"},
		{"categorical_u16", []types.VectorSpec{{Name: "v", Fields: []string{"region16"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "region16"},
		{"categorical_u32", []types.VectorSpec{{Name: "v", Fields: []string{"region32"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "region32"},
		{"set_u8", []types.VectorSpec{{Name: "v", Fields: []string{"tags"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "tags"},
		{"set_u256", []types.VectorSpec{{Name: "v", Fields: []string{"tags_wide"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "tags_wide"},
		{"date", []types.VectorSpec{{Name: "v", Fields: []string{"day"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "day"},
		{"datetime", []types.VectorSpec{{Name: "v", Fields: []string{"ts"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "ts"},
		{"decimal128", []types.VectorSpec{{Name: "v", Fields: []string{"amount"}}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "amount"},
		{"refused type reached by pattern", []types.VectorSpec{{Name: "v", Pattern: "^(q_1|region)$"}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field_type", "categorical_u8"},
		{"binary coerce does not admit categorical", []types.VectorSpec{{Name: "v", Fields: []string{"region"}, Coerce: types.VectorCoerceBinary}}, errors.PULSE_VECTOR_MEMBER_TYPE, "field", "region"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(c.specs, testSchema())
			if err == nil {
				t.Fatalf("resolved %+v, want %s", got, c.code)
			}
			if err.Code != c.code {
				t.Fatalf("code = %s (%s), want %s", err.Code, err.Message, c.code)
			}
			if v, ok := err.Details[c.detail]; !ok || !equalDetail(v, c.value) {
				t.Errorf("details[%q] = %v, want %v (details %v)", c.detail, v, c.value, err.Details)
			}
		})
	}
}

func equalDetail(got, want any) bool {
	switch w := want.(type) {
	case int:
		g, ok := got.(int)
		return ok && g == w
	case string:
		g, ok := got.(string)
		return ok && g == w
	}
	return false
}

func TestResolve_EmptyAndFind(t *testing.T) {
	if got, err := Resolve(nil, testSchema()); got != nil || err != nil {
		t.Fatalf("nil specs: %v %v", got, err)
	}
	got, err := Resolve([]types.VectorSpec{{Name: "a", Fields: []string{"q_1"}}, {Name: "b", Pattern: "^q_2$"}}, testSchema())
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := Find(got, "b"); !ok || !slices.Equal(r.Members, []string{"q_2"}) {
		t.Errorf("Find(b) = %+v %v", r, ok)
	}
	if _, ok := Find(got, "c"); ok {
		t.Error("Find(c) found a vector")
	}
}

func TestUnreferenced(t *testing.T) {
	if got := Unreferenced(nil); got != nil {
		t.Errorf("nil request: %v", got)
	}
	req := &types.Request{Vectors: []types.VectorSpec{{Name: "a"}, {Name: "b"}}}
	if got := Unreferenced(req); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("Unreferenced = %v, want [a b]", got)
	}
	w := UnreferencedWarning("a")
	if w.Code != errors.PULSE_VECTOR_UNREFERENCED || w.Details["name"] != "a" {
		t.Errorf("warning = %+v", w)
	}
}

func TestMembers(t *testing.T) {
	req := &types.Request{Vectors: []types.VectorSpec{
		{Name: "a", Fields: []string{"score", "q_1"}},
		{Name: "b", Pattern: "^q_[23]$"},
	}}
	got, ok := Members(req, testSchema())
	if !ok || !slices.Equal(got, []string{"score", "q_1", "q_3", "q_2"}) {
		t.Errorf("Members = %v %v", got, ok)
	}
	req.Vectors = append(req.Vectors, types.VectorSpec{Name: "c", Fields: []string{"region"}})
	if _, ok := Members(req, testSchema()); ok {
		t.Error("an unresolvable vector must report ok=false")
	}
	if got, ok := Members(&types.Request{}, testSchema()); got != nil || !ok {
		t.Errorf("no vectors: %v %v", got, ok)
	}
}

// TestNormalize_HashesResolvedMembers: the hash normalization rule — a
// pattern and the equivalent explicit field list hash identically once
// normalized, the caller's request is untouched, and a vector-free
// request is returned as is.
func TestNormalize_HashesResolvedMembers(t *testing.T) {
	schema := testSchema()
	byPattern := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Pattern: `^q_\d$`}}}
	byGlob := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_*"}}}}
	byFields := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_3", "q_1", "q_2"}}}}
	reordered := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"q_1", "q_2", "q_3"}}}}

	if byPattern.Hash() == byFields.Hash() {
		t.Fatal("vacuous: raw pattern and field list already hash alike")
	}
	h := Normalize(byFields, schema).Hash()
	if got := Normalize(byPattern, schema).Hash(); got != h {
		t.Errorf("pattern normalizes to %s, fields to %s", got, h)
	}
	if got := Normalize(byGlob, schema).Hash(); got != h {
		t.Errorf("glob normalizes to %s, fields to %s", got, h)
	}
	if Normalize(reordered, schema).Hash() == h {
		t.Error("a different member order must hash differently (order is axis order)")
	}
	if byPattern.Vectors[0].Pattern == "" || byPattern.Vectors[0].Fields != nil {
		t.Error("Normalize mutated the caller's request")
	}
	plain := &types.Request{Label: "x"}
	if Normalize(plain, schema) != plain {
		t.Error("a vector-free request must be returned as is")
	}
	bad := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Pattern: "^nope"}}}
	if Normalize(bad, schema) != bad {
		t.Error("an unresolvable request must be returned as is")
	}
}

// TestRequestHash_VectorFreeByteIdentical: adding the Vectors slot
// leaves every vector-free request's canonical hash unchanged — the
// slot is omitempty, so an empty slice and a nil one hash like the
// pre-Vectors request.
func TestRequestHash_VectorFreeByteIdentical(t *testing.T) {
	base := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}},
	}
	// Pinned from the request built against types.go before the Vectors
	// slot existed.
	const pinned = "8b9072ff0643090c359c534df94bd050"
	empty := *base
	empty.Vectors = []types.VectorSpec{}
	if base.Hash() != empty.Hash() {
		t.Errorf("empty Vectors moves the hash: %s vs %s", base.Hash(), empty.Hash())
	}
	if base.Hash() != pinned {
		t.Errorf("vector-free hash = %s, want the pre-slot %s", base.Hash(), pinned)
	}
	with := *base
	with.Vectors = []types.VectorSpec{{Name: "v", Fields: []string{"q_1"}}}
	if with.Hash() == base.Hash() {
		t.Error("a vector must participate in the hash")
	}
}

func TestMemberTypeAllowed(t *testing.T) {
	for ft := encoding.FieldTypeU8; ft <= encoding.FieldTypeSetU256; ft++ {
		want := false
		switch ft {
		case encoding.FieldTypeU4, encoding.FieldTypeU8, encoding.FieldTypeU16, encoding.FieldTypeU32,
			encoding.FieldTypeU64, encoding.FieldTypeF32, encoding.FieldTypeF64:
			want = true
		}
		if got := MemberTypeAllowed(ft, ""); got != want {
			t.Errorf("%s: allowed = %v, want %v", ft, got, want)
		}
	}
	if !MemberTypeAllowed(encoding.FieldTypePackedBool, types.VectorCoerceBinary) {
		t.Error("packed_bool under binary must be allowed")
	}
}
