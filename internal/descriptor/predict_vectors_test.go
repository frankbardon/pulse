package descriptor

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func vectorPredictSchema(t *testing.T) []byte {
	t.Helper()
	return buildTestPulseFile(t, &encoding.Schema{Fields: []encoding.Field{
		{Name: "q_3", Type: encoding.FieldTypeF64, Description: "Third battery item score"},
		{Name: "q_1", Type: encoding.FieldTypeF64, Description: "First battery item score"},
		{Name: "q_2", Type: encoding.FieldTypeU8, Description: "Second battery item score"},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Description: "Sales region of the account", Dictionary: makeDictionary(t, "N", "S")},
	}})
}

// TestPredict_ResolvedVectors: predict echoes every vector's resolved
// members (literals in caller order, patterns in schema order) and warns
// once per vector no slot references, in request order.
func TestPredict_ResolvedVectors(t *testing.T) {
	req := &types.Request{
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}},
		Vectors: []types.VectorSpec{
			{Name: "lit", Fields: []string{"q_2", "q_1"}},
			{Name: "pat", Pattern: `^q_\d$`},
			{Name: "glob", Fields: []string{"q_[23]"}, Labels: []string{"Three", "Two"}},
		},
	}
	env := predictFromBytes(vectorPredictSchema(t), req, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", env.Errors)
	}
	res := env.Data.(*descriptor.PredictResult)
	if !res.Valid {
		t.Error("a warning must not invalidate the request")
	}
	want := map[string][]string{
		"lit":  {"q_2", "q_1"},
		"pat":  {"q_3", "q_1", "q_2"},
		"glob": {"q_3", "q_2"},
	}
	if !reflect.DeepEqual(res.ResolvedVectors, want) {
		t.Errorf("resolved_vectors = %v, want %v", res.ResolvedVectors, want)
	}
	var names []string
	for _, w := range env.Warnings {
		if w.Code == string(errors.PULSE_VECTOR_UNREFERENCED) {
			names = append(names, w.Details["name"].(string))
		}
	}
	if !slices.Equal(names, []string{"lit", "pat", "glob"}) {
		t.Errorf("unreferenced warnings = %v, want [lit pat glob]", names)
	}
}

// TestPredict_NoVectorsNoEcho: a vector-free request carries no
// resolved_vectors key and no vector warning.
func TestPredict_NoVectorsNoEcho(t *testing.T) {
	req := &types.Request{Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "q_1", Label: "s"}}}
	env := predictFromBytes(vectorPredictSchema(t), req, nil)
	if res := env.Data.(*descriptor.PredictResult); res.ResolvedVectors != nil {
		t.Errorf("resolved_vectors = %v, want omitted", res.ResolvedVectors)
	}
	for _, w := range env.Warnings {
		if strings.HasPrefix(w.Code, "PULSE_VECTOR_") {
			t.Errorf("unexpected warning %s", w.Code)
		}
	}
}

// TestPredict_VectorRefusals: a vector the resolver refuses is a predict
// error carrying the resolver's code and details, and nothing is echoed.
// The same refusal reaches the runtime through FieldRefRefusal.
func TestPredict_VectorRefusals(t *testing.T) {
	cases := []struct {
		name string
		spec types.VectorSpec
		code errors.Code
	}{
		{"categorical member", types.VectorSpec{Name: "v", Fields: []string{"q_1", "region"}}, errors.PULSE_VECTOR_MEMBER_TYPE},
		{"empty pattern", types.VectorSpec{Name: "v", Pattern: "^zz"}, errors.PULSE_VECTOR_EMPTY},
		{"labels mismatch", types.VectorSpec{Name: "v", Pattern: "^q_", Labels: []string{"a"}}, errors.PULSE_VECTOR_LABELS_MISMATCH},
		{"both fields and pattern", types.VectorSpec{Name: "v", Fields: []string{"q_1"}, Pattern: "q"}, errors.PULSE_VECTOR_INVALID},
		{"duplicate member", types.VectorSpec{Name: "v", Fields: []string{"q_1", "q_*"}}, errors.PULSE_VECTOR_DUPLICATE},
		{"unknown literal", types.VectorSpec{Name: "v", Fields: []string{"q_9"}}, errors.SERVICE_VALIDATION},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := &types.Request{Vectors: []types.VectorSpec{c.spec}}
			env := predictFromBytes(vectorPredictSchema(t), req, nil)
			if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
				t.Fatalf("errors = %v, want %s", env.Errors, c.code)
			}
			res := env.Data.(*descriptor.PredictResult)
			if res.Valid || res.ResolvedVectors != nil {
				t.Errorf("valid=%v resolved=%v, want invalid and no echo", res.Valid, res.ResolvedVectors)
			}
			// The runtime's form of the same rule.
			schema := &encoding.Schema{Fields: []encoding.Field{
				{Name: "q_3", Type: encoding.FieldTypeF64}, {Name: "q_1", Type: encoding.FieldTypeF64},
				{Name: "q_2", Type: encoding.FieldTypeU8}, {Name: "region", Type: encoding.FieldTypeCategoricalU8},
			}}
			rt := asCoded(t, FieldRefRefusal(req, schema, nil))
			if rt.Code != c.code || rt.Message != env.Errors[0].Message {
				t.Errorf("runtime refusal %s %q, predict %s %q", rt.Code, rt.Message, env.Errors[0].Code, env.Errors[0].Message)
			}
		})
	}
}

// TestSlotRefusal_VectorsHidden: without capability:matrices a set
// `vectors` slot is an unknown field; with it, nothing is refused.
func TestSlotRefusal_VectorsHidden(t *testing.T) {
	req := &types.Request{Vectors: []types.VectorSpec{{Name: "v", Fields: []string{"x"}}}}
	ce := asCoded(t, SlotRefusal(req, scopedOnly(featProcess)))
	if ce.Code != errors.PULSE_REQUEST_UNKNOWN_FIELD || !reflect.DeepEqual(ce.Details["unknown_keys"], []string{"vectors"}) {
		t.Fatalf("got %s %v", ce.Code, ce.Details)
	}
	if err := SlotRefusal(req, scopedOnly(featProcess, featMatrices)); err != nil {
		t.Errorf("enabled instance refuses: %v", err)
	}
}

// TestPayloadSchema_MatricesHidden: the `vectors` slot and its defs are
// absent from the payload schema of an instance without
// capability:matrices.
func TestPayloadSchema_MatricesHidden(t *testing.T) {
	base := []string{featProcess}
	for _, c := range []struct {
		name string
		inst *InstanceSnapshot
		want bool
	}{
		{"full registry", nil, true},
		{"enabled", scopedOnly(append(base, featMatrices)...), true},
		{"hidden", scopedOnly(base...), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b, err := PayloadSchemaForInstance(c.inst)
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			for _, tok := range []string{`"vectors"`, `"VectorSpec"`, `"VectorCoerce"`} {
				if got := strings.Contains(s, tok); got != c.want {
					t.Errorf("%s present = %v, want %v", tok, got, c.want)
				}
			}
		})
	}
}
