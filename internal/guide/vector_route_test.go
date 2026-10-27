package guide

import (
	"bytes"
	"encoding/json"
	stderrors "errors"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// nativeNames are vectorFixture's native vector fields.
var nativeNames = []string{"emb", "emb32", "mix", "likert"}

// vectorFixture is boundFixture plus four native vector fields: vec_f64
// "emb" (dim 3, kind measure), vec_f32 "emb32" (dim 2, no kind), vec_f64
// "mix" (dim 3, kind composition) and vec_f32 "likert" (dim 4, kind
// scale on 1..5). Every scalar slot has a vector candidate it must
// never take; every vector slot has natives of each kind to judge.
func vectorFixture(t testing.TB) *encoding.Schema {
	s := boundFixture(t)
	s.Fields = append(s.Fields,
		encoding.Field{Name: "emb", Type: encoding.FieldTypeVecF64, Description: "Embedding of the student's essay"},
		encoding.Field{Name: "emb32", Type: encoding.FieldTypeVecF32, Description: "Compact embedding of the student's essay"},
		encoding.Field{Name: "mix", Type: encoding.FieldTypeVecF64, Description: "Share of study time by subject"},
		encoding.Field{Name: "likert", Type: encoding.FieldTypeVecF32, Description: "Course rating items on a five point scale"},
	)
	n := len(s.Fields)
	s.Vectors = []encoding.VectorField{
		{FieldIndex: n - 4, Dim: 3, Kind: encoding.VectorKindMeasure},
		{FieldIndex: n - 3, Dim: 2},
		{FieldIndex: n - 2, Dim: 3, Kind: encoding.VectorKindComposition},
		{FieldIndex: n - 1, Dim: 4, Kind: encoding.VectorKindScale, HasScale: true, ScaleMin: 1, ScaleMax: 5},
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

// nativeUses returns every (key, native name) pair in v where a JSON
// string value equal to a native name sits; key is the object key that
// holds it ("" inside an array is reported with the array's key).
func nativeUses(v any, key string, out *[][2]string) {
	switch x := v.(type) {
	case string:
		if slices.Contains(nativeNames, x) {
			*out = append(*out, [2]string{key, x})
		}
	case []any:
		for _, e := range x {
			nativeUses(e, key, out)
		}
	case map[string]any:
		for k, e := range x {
			nativeUses(e, k, out)
		}
	}
}

// draftNatives decodes a draft and returns where it names a native.
func draftNatives(t *testing.T, r descriptor.Recommendation) [][2]string {
	t.Helper()
	var v any
	if err := json.Unmarshal(r.Request, &v); err != nil {
		t.Fatalf("%s: draft does not decode: %v", r.Operator, err)
	}
	var out [][2]string
	nativeUses(v, "", &out)
	return out
}

// hasVectorSlot reports whether op's plan under intent in has a vector
// slot.
func hasVectorSlot(t *testing.T, op string, in descriptor.Intent) bool {
	t.Helper()
	info, ok := catalog(descx.BuildManifestForInstance(nil))[op]
	if !ok {
		return false
	}
	slots, _, ok := plan(info, in)
	return ok && slices.ContainsFunc(slots, func(s bslot) bool { return s.vector })
}

// TestRecommendBound_PrefersNativeVector: an operator that drafts a
// native vector drafts it ahead of any member-column list, and every
// operator with a vector slot drafts the cohort's natives in schema
// order (the first fitting native first).
func TestRecommendBound_PrefersNativeVector(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	checked := 0
	for _, in := range descx.Intents() {
		res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Limit: 1000}, b)
		if err != nil {
			t.Fatal(err)
		}
		first := map[string]descriptor.Recommendation{}
		natives := map[string]bool{}
		for _, r := range res.Recommendations {
			if _, seen := first[r.Operator]; !seen {
				first[r.Operator] = r
			}
			if len(draftNatives(t, r)) > 0 {
				natives[r.Operator] = true
			}
		}
		for op := range natives {
			checked++
			uses := draftNatives(t, first[op])
			if len(uses) == 0 {
				t.Errorf("%s %s: first draft %s uses no native vector, a later one does", in.ID, op, first[op].Request)
			}
		}
		for op, r := range first {
			if hasVectorSlot(t, op, in) && !natives[op] && op != string(types.MAT_DISTANCE) {
				t.Errorf("%s %s: vector-slot operator drafts no native: %s", in.ID, op, r.Request)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no operator drafted a native vector")
	}
}

// TestRecommendBound_NativeKindSuitability: the native's schema kind
// decides what it is drafted into. An item-battery operator
// (MAT_RELIABILITY, ATTR_SCALE_SCORE) takes a scale, binary or
// kind-less native but never a measure or composition one; a
// comparison attribute over a kinded native leaves the metric to the
// kind's default (no metric need), and over a kind-less native still
// asks for it.
func TestRecommendBound_NativeKindSuitability(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	got := map[string][]string{} // operator -> natives drafted
	needs := map[string][]string{}
	for _, in := range descx.Intents() {
		res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Limit: 1000}, b)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res.Recommendations {
			for _, u := range draftNatives(t, r) {
				got[r.Operator] = append(got[r.Operator], u[1])
				for _, n := range r.Needs {
					needs[r.Operator+"/"+u[1]] = append(needs[r.Operator+"/"+u[1]], n.Param)
				}
			}
		}
	}
	for _, op := range []string{string(types.MAT_RELIABILITY), string(types.ATTR_SCALE_SCORE)} {
		if !slices.Contains(got[op], "likert") || !slices.Contains(got[op], "emb32") {
			t.Errorf("%s: drafts %v, want the scale and kind-less natives", op, got[op])
		}
		if slices.Contains(got[op], "mix") || slices.Contains(got[op], "emb") {
			t.Errorf("%s: drafts %v, a composition / measure native is not an item battery", op, got[op])
		}
	}
	defaulted := 0
	for _, op := range []string{string(types.ATTR_VEC_DISTANCE), string(types.ATTR_PROFILE_SIMILARITY)} {
		if len(got[op]) == 0 {
			t.Fatalf("%s: drafts no native", op)
		}
		c, f, _ := vectors.ComparisonConsumer(types.AttributeType(op))
		for _, n := range got[op] {
			_, hasDefault := vectors.KindDefault(c, vectors.KindIn(nil, b.Schema, n), f, nil)
			if asks := slices.Contains(needs[op+"/"+n], "metric"); asks == hasDefault {
				t.Errorf("%s over %s: metric need %v, kind default %v; want the need exactly when no default applies", op, n, asks, hasDefault)
			}
			if hasDefault {
				defaulted++
			}
		}
		if !slices.Contains(got[op], "emb32") {
			t.Errorf("%s: drafts %v, want the kind-less native too (metric left to the caller)", op, got[op])
		}
	}
	if defaulted == 0 {
		t.Error("no comparison draft took its metric from a native's kind")
	}
}

// TestRecommendBound_VectorHint: a native vector hinted for an intent
// some serving operator drafts a vector slot for is accepted, and pins
// every draft to that native in a vector slot; for an intent with no
// vector slot it is refused as kind "vector" (SERVICE_VALIDATION).
func TestRecommendBound_VectorHint(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	accepted, refused := 0, 0
	for _, in := range descx.Intents() {
		res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: in.ID, Fields: []string{"emb"}, Limit: 1000}, b)
		vec := false
		if in.Analytic {
			vec = takesVector(nil, (*descx.InstanceSnapshot)(nil).Ontology(), catalog(descx.BuildManifestForInstance(nil)), in)
		}
		if !vec {
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.SERVICE_VALIDATION || ce.Details["kind"] != string(descriptor.FieldKindVector) {
				t.Errorf("%s: vector hint err = %v, want SERVICE_VALIDATION kind vector", in.ID, err)
			}
			refused++
			continue
		}
		if err != nil {
			t.Fatalf("%s: vector hint refused: %v", in.ID, err)
		}
		accepted++
		if len(res.Recommendations) == 0 {
			t.Errorf("%s: a hinted native drafts nothing", in.ID)
		}
		for _, r := range res.Recommendations {
			if uses := draftNatives(t, r); !slices.Equal(uses, [][2]string{{"vector", "emb"}}) {
				t.Errorf("%s %s: hinted draft names natives %v, want only vector emb: %s", in.ID, r.Operator, uses, r.Request)
			}
		}
	}
	if accepted == 0 || refused == 0 {
		t.Fatalf("accepted %d, refused %d intents; want both arms exercised", accepted, refused)
	}
}

// TestBind_NativeVectorSlots: a slot that is not a vector slot — even
// one that accepts any field — never takes a native vector field, and a
// native hinted for it pins nothing; a vector slot takes each fitting
// native whole (in schema order) before its member-column list, a
// hinted native pins it alone, and a native shorter than the slot's
// least member count does not fit.
func TestBind_NativeVectorSlots(t *testing.T) {
	s := vectorFixture(t)
	anyField := func(*encoding.Field) bool { return true }
	scalar := []bslot{{key: "numerator_field", accept: anyField}, {key: "fields", list: true, min: 1, accept: anyField}}
	for _, b := range bind(scalar, s, nil, 1000) {
		for _, names := range b.fields {
			for _, n := range names {
				if slices.Contains(nativeNames, n) {
					t.Fatalf("binding %v takes vector field %s", b.fields, n)
				}
			}
		}
	}
	if got := bind(scalar, s, []string{"emb"}, 10); got != nil {
		t.Errorf("a vector hint pinned a scalar slot: %v", got)
	}

	numeric := kindIs(descriptor.FieldKindNumeric)
	order := func(slots []bslot) []string {
		var out []string
		for _, b := range bind(slots, s, nil, 10) {
			if b.vector[0] {
				out = append(out, b.fields[0][0])
				continue
			}
			if slices.ContainsFunc(b.fields[0], func(n string) bool { return slices.Contains(nativeNames, n) }) {
				t.Errorf("member list %v takes a native", b.fields[0])
			}
			out = append(out, "<members>")
		}
		return out
	}
	vec := []bslot{{key: "fields", list: true, min: 2, vector: true, accept: numeric}}
	if got, want := order(vec), []string{"emb", "emb32", "mix", "likert", "<members>"}; !slices.Equal(got, want) {
		t.Errorf("vector slot bindings %v, want %v", got, want)
	}
	// emb32 (dim 2) is below min 3, and two numeric columns are too few.
	vec = []bslot{{key: "fields", list: true, min: 3, vector: true, accept: numeric}}
	if got, want := order(vec), []string{"emb", "mix", "likert"}; !slices.Equal(got, want) {
		t.Errorf("min-3 vector slot bindings %v, want %v", got, want)
	}

	pinned := bind(vec, s, []string{"likert"}, 10)
	if len(pinned) != 1 || !pinned[0].vector[0] || pinned[0].fields[0][0] != "likert" || pinned[0].hints != 1 {
		t.Errorf("hinted native bindings %+v, want likert alone", pinned)
	}
	if got := bind(vec, s, []string{"emb32"}, 10); got != nil {
		t.Errorf("a native below the slot's min pinned it: %+v", got)
	}

	battery := []bslot{{key: "fields", list: true, min: 2, vector: true, accept: numeric, kindOK: itemBattery}}
	for _, b := range bind(battery, s, nil, 10) {
		if b.vector[0] && (b.fields[0][0] == "emb" || b.fields[0][0] == "mix") {
			t.Errorf("item-battery slot took %s", b.fields[0][0])
		}
	}
}

// TestRecommendBound_HotellingSplitTwoLevels: the two-sample
// TEST_HOTELLING_T2 refuses a split without exactly two levels at run
// time, which predict cannot see, so the binder splits it only by a
// field with exactly two declared levels: a categorical whose dictionary
// holds two values, or a packed_bool ("0" / "1"). The 4-level region
// never.
func TestRecommendBound_HotellingSplitTwoLevels(t *testing.T) {
	b := vectorBound(t, vectorFixture(t))
	res, err := RecommendBound(nil, descriptor.RecommendRequest{Intent: descx.IntentCompareGroups, Limit: 1000}, b)
	if err != nil {
		t.Fatal(err)
	}
	split := 0
	seen := map[string]bool{}
	for _, r := range res.Recommendations {
		if r.Operator != string(types.TEST_HOTELLING_T2) {
			continue
		}
		var req types.Request
		if err := json.Unmarshal(r.Request, &req); err != nil {
			t.Fatal(err)
		}
		if sb := req.Tests[0].SplitBy; sb != "" {
			split++
			seen[sb] = true
			if sb != "arm" && sb != "passed" {
				t.Errorf("TEST_HOTELLING_T2 split by %q (not two levels): %s", sb, r.Request)
			}
		}
	}
	if split == 0 {
		t.Fatal("no two-sample TEST_HOTELLING_T2 draft")
	}
	if !seen["passed"] {
		t.Error("no TEST_HOTELLING_T2 draft split by the packed_bool")
	}
}
