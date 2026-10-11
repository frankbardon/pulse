package vectors

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func testInputSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a1", Type: encoding.FieldTypeF64},
		{Name: "a2", Type: encoding.FieldTypeU8},
		{Name: "a3", Type: encoding.FieldTypeF32},
		{Name: "b1", Type: encoding.FieldTypeF64},
		{Name: "b2", Type: encoding.FieldTypeF64},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
	}}
}

func hot(mod func(*types.Test)) *types.Test {
	t := &types.Test{Type: types.TEST_HOTELLING_T2}
	mod(t)
	return t
}

// TestResolveTests_Rules pins every vector-input rule: the members a
// valid slot resolves to, and the code + details.reason of each refusal.
func TestResolveTests_Rules(t *testing.T) {
	vecs := []types.VectorSpec{
		{Name: "va", Fields: []string{"a*"}},
		{Name: "vb", Fields: []string{"b1", "b2"}},
		{Name: "vbool", Fields: []string{"flag", "a1"}, Coerce: types.VectorCoerceBinary},
	}
	cases := []struct {
		name    string
		test    *types.Test
		post    *types.Test
		members []string
		code    errors.Code
		reason  string
	}{
		{name: "vector", test: hot(func(t *types.Test) { t.Vector = "va" }), members: []string{"a1", "a2", "a3"}},
		{name: "inline glob in place", test: hot(func(t *types.Test) { t.Fields = []string{"b2", "a[12]"} }), members: []string{"b2", "a1", "a2"}},
		{name: "coerced packed_bool via vector", test: hot(func(t *types.Test) { t.Vector = "vbool" }), members: []string{"flag", "a1"}},
		{name: "listwise ok", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"missing": "listwise"}`)
		}), members: []string{"a1", "a2"}},
		{name: "mu of length p", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"mu": [1, 2]}`)
		}), members: []string{"a1", "a2"}},
		{name: "plain test reads none", test: &types.Test{Type: types.TEST_T, Field: "a1"}},
		{name: "extension test not judged", test: &types.Test{Type: "TEST_ACME_X", Vector: "nope"}},

		{name: "vector and fields", test: hot(func(t *types.Test) { t.Vector, t.Fields = "va", []string{"a1"} }), code: errors.SERVICE_VALIDATION, reason: "vector_and_fields"},
		{name: "neither", test: hot(func(t *types.Test) {}), code: errors.SERVICE_VALIDATION, reason: "no_vector_or_fields"},
		{name: "field on vector test", test: hot(func(t *types.Test) { t.Vector, t.Field = "va", "a1" }), code: errors.SERVICE_VALIDATION, reason: "field_on_vector_test"},
		{name: "vector2 not accepted", test: hot(func(t *types.Test) { t.Vector, t.Vector2 = "va", "vb" }), code: errors.SERVICE_VALIDATION, reason: "vector2_not_accepted"},
		{name: "vector2 and fields2", test: hot(func(t *types.Test) { t.Vector, t.Vector2, t.Fields2 = "va", "vb", []string{"b1"} }), code: errors.SERVICE_VALIDATION, reason: "vector2_and_fields2"},
		{name: "pairwise refused", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"missing": "pairwise"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "pairwise_unsupported"},
		{name: "unknown missing mode", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"missing": "everything"}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "mu wrong length", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"mu": [1, 2, 3]}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown param key", test: hot(func(t *types.Test) {
			t.Fields = []string{"a1", "a2"}
			t.Params = json.RawMessage(`{"muu": [1, 2]}`)
		}), code: errors.SERVICE_VALIDATION, reason: "bad_params"},
		{name: "unknown vector", test: hot(func(t *types.Test) { t.Vector = "zz" }), code: errors.PULSE_VECTOR_UNKNOWN},
		{name: "non-numeric member", test: hot(func(t *types.Test) { t.Fields = []string{"a1", "region"} }), code: errors.PULSE_VECTOR_MEMBER_TYPE},
		{name: "packed_bool inline needs coerce", test: hot(func(t *types.Test) { t.Fields = []string{"a1", "flag"} }), code: errors.PULSE_VECTOR_MEMBER_TYPE},
		{name: "unknown literal", test: hot(func(t *types.Test) { t.Fields = []string{"a1", "nope"} }), code: errors.SERVICE_VALIDATION},
		{name: "empty glob", test: hot(func(t *types.Test) { t.Fields = []string{"z*"} }), code: errors.PULSE_VECTOR_EMPTY},
		{name: "vector on a scalar test", test: &types.Test{Type: types.TEST_T, Field: "a1", Vector: "va"}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
		{name: "fields on a post-test", post: &types.Test{Type: types.TEST_ANOVA_F, Fields: []string{"a1"}}, code: errors.SERVICE_VALIDATION, reason: "vector_not_accepted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: vecs}
			if c.test != nil {
				req.Tests = []*types.Test{c.test}
			}
			if c.post != nil {
				req.PostTests = []*types.Test{c.post}
			}
			got, err := ResolveTests(req, testInputSchema(), nil)
			if c.code != "" {
				if err == nil {
					t.Fatalf("want %s, resolved %+v", c.code, got)
				}
				if err.Code != c.code {
					t.Fatalf("code = %s (%s), want %s", err.Code, err.Message, c.code)
				}
				if c.reason != "" && err.Details["reason"] != c.reason {
					t.Fatalf("reason = %v, want %s (%s)", err.Details["reason"], c.reason, err.Message)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if c.test == nil {
				return
			}
			if !reflect.DeepEqual(got[0].Members.Members, c.members) {
				t.Fatalf("members = %v, want %v", got[0].Members.Members, c.members)
			}
			if got[0].Has() != (len(c.members) > 0) {
				t.Fatalf("Has() = %v", got[0].Has())
			}
		})
	}
}

// TestResolveTests_SecondSet pins the paired second member set on a
// type that reads one: same length resolves, a different length is
// vector2_length_mismatch.
func TestResolveTests_SecondSet(t *testing.T) {
	old := secondVectorTypes
	secondVectorTypes = []types.TestType{types.TEST_HOTELLING_T2}
	defer func() { secondVectorTypes = old }()
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "vb", Fields: []string{"b1", "b2"}}},
		Tests:   []*types.Test{hot(func(t *types.Test) { t.Fields, t.Vector2 = []string{"a1", "a2"}, "vb" })},
	}
	got, err := ResolveTests(req, testInputSchema(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got[0].HasSecond || !reflect.DeepEqual(got[0].Members2.Members, []string{"b1", "b2"}) {
		t.Fatalf("second set = %+v", got[0])
	}
	req.Tests[0].Fields = []string{"a1", "a2", "a3"}
	_, err = ResolveTests(req, testInputSchema(), nil)
	if err == nil || err.Details["reason"] != "vector2_length_mismatch" || err.Details["p"] != 3 || err.Details["p2"] != 2 {
		t.Fatalf("mismatch refusal = %v", err)
	}
}

// TestResolveTests_HiddenRoutesUnjudged: a type the instance hides
// routes to an unknown name, whose slots this leaf leaves to the
// unknown-type refusal.
func TestResolveTests_HiddenRoutesUnjudged(t *testing.T) {
	req := &types.Request{Tests: []*types.Test{hot(func(t *types.Test) {})}}
	if _, err := ResolveTests(req, testInputSchema(), nil); err == nil {
		t.Fatal("visible type: want no_vector_or_fields")
	}
	got, err := ResolveTests(req, testInputSchema(), func(types.TestType) types.TestType { return "" })
	if err != nil || got[0].Has() {
		t.Fatalf("hidden type judged: %v %+v", err, got)
	}
}

// TestReferenced_CountsTestVectors: a vector a tier-1 test names (vector
// or vector2) is referenced, so it raises no unreferenced warning.
func TestReferenced_CountsTestVectors(t *testing.T) {
	req := &types.Request{
		Vectors: []types.VectorSpec{{Name: "va", Fields: []string{"a1"}}, {Name: "vb", Fields: []string{"b1"}}, {Name: "vc", Fields: []string{"b2"}}},
		Tests:   []*types.Test{nil, {Type: types.TEST_HOTELLING_T2, Vector: "va", Vector2: "vb"}},
	}
	if got := Unreferenced(req); !reflect.DeepEqual(got, []string{"vc"}) {
		t.Fatalf("unreferenced = %v, want [vc]", got)
	}
}

// TestTestInputMembers: projection reads every inline fields / fields2
// member (globs expanded); a member list that fails to resolve reports
// ok == false so projection decodes wide.
func TestTestInputMembers(t *testing.T) {
	req := &types.Request{Tests: []*types.Test{
		{Type: types.TEST_HOTELLING_T2, Fields: []string{"a[12]"}, Fields2: []string{"b1", "b2"}},
		{Type: types.TEST_HOTELLING_T2, Vector: "named"},
	}}
	got, ok := TestInputMembers(req, testInputSchema())
	if !ok || !reflect.DeepEqual(got, []string{"a1", "a2", "b1", "b2"}) {
		t.Fatalf("members = %v ok=%v", got, ok)
	}
	req.Tests[0].Fields = []string{"nope"}
	if _, ok := TestInputMembers(req, testInputSchema()); ok {
		t.Fatal("unresolvable inline fields must report ok == false")
	}
}
