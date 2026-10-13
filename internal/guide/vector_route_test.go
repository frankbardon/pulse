package guide

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/types"
)

// vectorFixture is boundFixture plus two native vector fields (vec_f64
// "emb", vec_f32 "emb32"), so every scalar slot has a vector candidate
// it must never take.
func vectorFixture(t testing.TB) *encoding.Schema {
	s := boundFixture(t)
	s.Fields = append(s.Fields,
		encoding.Field{Name: "emb", Type: encoding.FieldTypeVecF64, Description: "Embedding of the student's essay"},
		encoding.Field{Name: "emb32", Type: encoding.FieldTypeVecF32, Description: "Compact embedding of the student's essay"},
	)
	n := len(s.Fields)
	s.Vectors = []encoding.VectorField{
		{FieldIndex: n - 2, Dim: 3, Kind: encoding.VectorKindMeasure},
		{FieldIndex: n - 1, Dim: 2},
	}
	return s
}

// vectorBound is bound over a schema that needs the 0x02 header.
func vectorBound(t testing.TB, s *encoding.Schema) Bound {
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, s); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	return Bound{
		Cohort: &types.Cohort{Filename: "fixture.pulse"},
		Schema: s,
		Predict: func(r *types.Request) *descriptor.Envelope {
			return descx.Predict(bytes.NewReader(data), r, &descx.PredictOptions{})
		},
	}
}

// namesAny reports whether any JSON string value in v equals one of
// names.
func namesAny(v any, names ...string) bool {
	switch x := v.(type) {
	case string:
		for _, n := range names {
			if x == n {
				return true
			}
		}
	case []any:
		for _, e := range x {
			if namesAny(e, names...) {
				return true
			}
		}
	case map[string]any:
		for _, e := range x {
			if namesAny(e, names...) {
				return true
			}
		}
	}
	return false
}

// TestRecommendBound_NeverBindsVectorFields is the FieldKindVector
// routing proof: over a cohort carrying native vector fields, no
// intent's recommendation binds one into any slot (a scalar slot
// refuses it with PULSE_VECTOR_FIELD_NOT_SCALAR; the vector slots draft
// numeric member lists), every draft still passes predict, and the
// vector fields cost the scalar recommendations nothing.
func TestRecommendBound_NeverBindsVectorFields(t *testing.T) {
	s := vectorFixture(t)
	b := vectorBound(t, s)
	for _, in := range descx.Intents() {
		res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Limit: 1000}, b)
		if err != nil {
			t.Fatalf("RecommendBound(%s): %v", in.ID, err)
		}
		plain := boundAll(t, nil, in.ID)
		if len(res.Recommendations) != len(plain.Recommendations) {
			t.Errorf("%s: %d recommendations with vector fields present, %d without", in.ID, len(res.Recommendations), len(plain.Recommendations))
		}
		for _, r := range res.Recommendations {
			var v any
			if err := json.Unmarshal(r.Request, &v); err != nil {
				t.Fatalf("%s %s: draft does not decode: %v", in.ID, r.Operator, err)
			}
			if namesAny(v, "emb", "emb32") {
				t.Errorf("%s %s: draft binds a vector field: %s", in.ID, r.Operator, r.Request)
			}
		}
	}
}

// TestRecommendBound_VectorHintRefused: a vector field hinted for an
// intent is refused as a kind no role takes, naming kind "vector".
func TestRecommendBound_VectorHintRefused(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	for _, in := range descx.Intents() {
		_, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Fields: []string{"emb"}}, b)
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["kind"] != string(descriptor.FieldKindVector) {
			t.Errorf("%s: vector hint err = %v, want SERVICE_VALIDATION kind vector", in.ID, err)
		}
	}
}

// TestBind_SkipsVectorFields: even a slot that accepts any field (an
// "any" field param, a field-name param) never takes a native vector
// field — the binder, not only the predict probe, keeps vectors out of
// scalar slots.
func TestBind_SkipsVectorFields(t *testing.T) {
	s := vectorFixture(t)
	anyField := func(*encoding.Field) bool { return true }
	slots := []bslot{{key: "numerator_field", accept: anyField}, {key: "fields", list: true, min: 1, accept: anyField}}
	for _, b := range bind(slots, s, nil, 1000) {
		for _, names := range b.fields {
			for _, n := range names {
				if n == "emb" || n == "emb32" {
					t.Fatalf("binding %v takes vector field %s", b.fields, n)
				}
			}
		}
	}
	if got := bind(slots, s, []string{"emb"}, 10); got != nil {
		t.Errorf("a vector hint pinned a slot: %v", got)
	}
}
