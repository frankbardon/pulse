package vectors

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func refTestTables() ReferenceTables {
	return NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t":   {Labels: []string{"c", "a", "b"}, Profiles: map[string][]float64{"k": {3, 1, 2}, "j": {0, 0, 0}}},
		"abc": {Labels: []string{"a", "b", "c"}, Profiles: map[string][]float64{"k": {1, 2, 3}}},
		"ab":  {Labels: []string{"a", "b"}, Profiles: map[string][]float64{"k": {1, 2}}},
	})
}

func TestReferenceTable_MatchAndProfile(t *testing.T) {
	t.Parallel()

	rt := refTestTables()["t"]
	cases := []struct {
		name    string
		labels  []string
		perm    []int
		missing []string
		extra   []string
	}{
		{"same order", []string{"c", "a", "b"}, []int{0, 1, 2}, nil, nil},
		{"permuted", []string{"a", "b", "c"}, []int{1, 2, 0}, nil, nil},
		{"vector has an extra label", []string{"a", "b", "c", "d"}, nil, []string{"d"}, []string{}},
		{"table has an extra label", []string{"a", "b"}, nil, []string{}, []string{"c"}},
		{"duplicate vector label", []string{"a", "a", "b"}, nil, []string{"a"}, []string{"c"}},
		{"disjoint", []string{"x"}, nil, []string{"x"}, []string{"c", "a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perm, cerr := rt.Match("t", "v", tc.labels)
			if tc.perm != nil {
				if cerr != nil || !reflect.DeepEqual(perm, tc.perm) {
					t.Fatalf("Match = %v, %v; want %v", perm, cerr, tc.perm)
				}
				return
			}
			if cerr == nil || cerr.Code != errors.PULSE_VECTOR_REFERENCE_MISMATCH {
				t.Fatalf("Match = %v, %v; want PULSE_VECTOR_REFERENCE_MISMATCH", perm, cerr)
			}
			d := cerr.Details
			if d["reason"] != ReferenceReasonLabelsDiffer || d["table"] != "t" || d["vector"] != "v" ||
				!reflect.DeepEqual(d["missing"], tc.missing) || !reflect.DeepEqual(d["extra"], tc.extra) {
				t.Errorf("details %v, want missing %v extra %v", d, tc.missing, tc.extra)
			}
		})
	}
	got, ok := rt.Profile("k", []int{1, 2, 0})
	if !ok || !reflect.DeepEqual(got, []float64{1, 2, 3}) {
		t.Errorf("Profile(k, perm) = %v, %v; want [1 2 3]", got, ok)
	}
	got[0] = 99
	if again, _ := rt.Profile("k", nil); !reflect.DeepEqual(again, []float64{3, 1, 2}) {
		t.Errorf("Profile shares the table's storage: %v", again)
	}
	if _, ok := rt.Profile("nope", nil); ok {
		t.Error("Profile(nope) found a profile")
	}
	if !reflect.DeepEqual(rt.Keys, []string{"j", "k"}) {
		t.Errorf("Keys %v, want sorted [j k]", rt.Keys)
	}
	view := ReferenceTable{Labels: rt.Labels, Keys: rt.Keys}
	if !view.HasKey("j") || view.HasKey("x") || !rt.HasKey("k") || rt.HasKey("x") {
		t.Error("HasKey disagrees between the predict view and the runtime table")
	}
}

func TestExprReferenceIssues(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeF64},
		{Name: "c", Type: encoding.FieldTypeF64}, {Name: "shadow", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeF64},
	}}
	resolved, err := Resolve([]types.VectorSpec{
		{Name: "v", Fields: []string{"a", "b", "c"}},
		{Name: "w", Fields: []string{"a", "b"}, Labels: []string{"a", "b"}},
	}, schema)
	if err != nil {
		t.Fatal(err)
	}
	scope := RefScope{Tables: refTestTables(), LabelsOf: func(name string) ([]string, bool) {
		if schema.Field(name) != nil {
			return nil, false
		}
		r, ok := Find(resolved, name)
		return r.Labels, ok
	}}
	cases := []struct {
		name   string
		src    string
		code   errors.Code
		reason string
	}{
		{"named vector, permuted table", `cosine(v, ref("t", "k"))`, "", ""},
		{"reference first", `dot(ref("t", "k"), v)`, "", ""},
		{"computed key", `dist(v, ref("t", region > 0 ? "k" : "j"))`, "", ""},
		{"under transforms both sides", `cosine(vcenter(v), vzscore(ref("t", "k")))`, "", ""},
		{"two references, same labels", `dot(ref("t", "k"), ref("abc", "k"))`, "", ""},
		{"outside a pair", `vsum(ref("ab", "k")) + vmax(v)`, "", ""},
		{"unknown table", `dot(v, ref("zz", "k"))`, errors.PULSE_VECTOR_REFERENCE_UNKNOWN, ReferenceReasonTableUnknown},
		{"computed table", `vsum(ref(region > 0 ? "t" : "ab", "k"))`, errors.PULSE_VECTOR_REFERENCE_UNKNOWN, ReferenceReasonTableNotLiteral},
		{"unknown literal key", `vsum(ref("t", "zz"))`, errors.PULSE_VECTOR_REFERENCE_UNKNOWN, ReferenceReasonKeyUnknown},
		{"labels differ", `dot(w, ref("t", "k"))`, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ReferenceReasonLabelsDiffer},
		{"two references differ", `dot(ref("t", "k"), ref("ab", "k"))`, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ReferenceReasonLabelsDiffer},
		{"literal array partner", `dot([a, b, c], ref("abc", "k"))`, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ReferenceReasonUnlabeledPartner},
		{"computed partner", `cosine(vcenter([a, b, c]), ref("abc", "k"))`, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ReferenceReasonUnlabeledPartner},
		{"let shadows a vector", `let v = [a, b, c]; dot(v, ref("abc", "k"))`, errors.PULSE_VECTOR_REFERENCE_MISMATCH, ReferenceReasonUnlabeledPartner},
		{"unparsable", `dot(v, ref("t", "k")`, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ExprReferenceIssues(tc.src, scope)
			if tc.code == "" {
				if len(issues) != 0 {
					t.Fatalf("issues %v, want none", issues[0])
				}
				return
			}
			if len(issues) != 1 || issues[0].Code != tc.code || issues[0].Details["reason"] != tc.reason {
				t.Fatalf("issues %v, want one %s reason %s", issues, tc.code, tc.reason)
			}
		})
	}

	// A schema field named like a vector shadows it, as at bind time.
	shadowScope := scope
	shadowScope.LabelsOf = func(name string) ([]string, bool) {
		if name == "shadow" {
			return nil, false
		}
		return scope.LabelsOf(name)
	}
	if issues := ExprReferenceIssues(`dot(shadow, ref("ab", "k"))`, shadowScope); len(issues) != 1 || issues[0].Details["reason"] != ReferenceReasonUnlabeledPartner {
		t.Errorf("a field partner: %v, want unlabeled_partner", issues)
	}
}

// TestReferenceIssues_RequestSlots: the request walk reports each
// expression slot's issues with its path.
func TestReferenceIssues_RequestSlots(t *testing.T) {
	t.Parallel()

	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeF64}, {Name: "b", Type: encoding.FieldTypeF64},
	}}
	req := &types.Request{
		Filterers: []*types.Filterer{{Type: types.FILTER_EXPRESSION, Expression: `vsum(ref("nope", "k")) > 0`}},
		Attributes: []*types.Attribute{
			{Type: types.ATTR_FORMULA, Expression: `dot([a, b], ref("ab", "k"))`, Label: "x"},
		},
	}
	issues := ReferenceIssues(req, schema, nil, refTestTables())
	if len(issues) != 2 {
		t.Fatalf("issues %v, want 2", issues)
	}
	if issues[0].Details["slot"] != "filterers[0].expression" || issues[0].Code != errors.PULSE_VECTOR_REFERENCE_UNKNOWN {
		t.Errorf("first issue %v", issues[0])
	}
	if issues[1].Details["slot"] != "attributes[0].expression" || issues[1].Code != errors.PULSE_VECTOR_REFERENCE_MISMATCH {
		t.Errorf("second issue %v", issues[1])
	}
	if ReferenceIssues(&types.Request{}, schema, nil, nil) != nil {
		t.Error("a request without expressions has issues")
	}
}
