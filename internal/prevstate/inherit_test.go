package prevstate

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

const (
	extCopy types.AttributeType = "ATTR_T_COPY"
	extFrom types.AttributeType = "ATTR_T_FROM"
)

// testInherits declares ATTR_T_COPY's source its Field and ATTR_T_FROM's
// its params.src.
var testInherits = Inherits{extCopy: "", extFrom: "src"}

// TestKeySchemaWith_ExtensionInherits (U67 E3-S2): an extension
// attribute declaring an inherited dictionary gets its output column
// typed as the source field and on its dictionary, from its Field or
// the named string param, exactly as an ATTR_PREVIOUS_STATE output is;
// nil Inherits (or an undeclared type) is the built-in view, the same
// pointer when nothing contributes.
func TestKeySchemaWith_ExtensionInherits(t *testing.T) {
	t.Parallel()

	s := keySchemaFixture(t)
	attrs := []*types.Attribute{
		{Type: extCopy, Field: "plan", Label: "copy"},
		{Type: extFrom, Field: "n", Label: "from", Params: json.RawMessage(`{"src": "active"}`)},
		{Type: extCopy, Field: "plan"}, // default label TYPE_field
	}
	view := KeySchemaWith(attrs, s, testInherits)
	if view == s {
		t.Fatal("no virtual field added")
	}
	if f := view.Field("copy"); f == nil || f.Type != encoding.FieldTypeCategoricalU8 || f.Dictionary != s.Field("plan").Dictionary || !f.Nullable {
		t.Fatalf("copy %+v, want a nullable categorical_u8 on plan's dictionary", f)
	}
	if f := view.Field("from"); f == nil || f.Type != encoding.FieldTypePackedBool {
		t.Fatalf("from %+v, want packed_bool (params.src names active)", f)
	}
	if view.Field(string(extCopy)+"_plan") == nil {
		t.Fatal("default-labelled output missing")
	}
	for name, inh := range map[string]Inherits{"nil": nil, "other type": {"ATTR_T_OTHER": ""}} {
		if got := KeySchemaWith(attrs, s, inh); got != s {
			t.Errorf("%s: an undeclared extension contributed %+v", name, got.Fields)
		}
	}
	if got := KeySchemaWith(attrs, s, testInherits); !reflect.DeepEqual(got.Fields, view.Fields) {
		t.Fatal("KeySchemaWith is not deterministic")
	}
	if got := KeySchemaWith([]*types.Attribute{prevAttr("plan", "p")}, s, nil); got.Field("p") == nil {
		t.Fatal("the built-in previous-state output lost its virtual field")
	}
	// A source the schema does not carry, or carries uncoded, adds
	// nothing (InheritedSource refuses it).
	for _, a := range []*types.Attribute{
		{Type: extCopy, Field: "n", Label: "c1"},
		{Type: extCopy, Field: "nope", Label: "c2"},
		{Type: extFrom, Field: "plan", Label: "c3"},
		{Type: extFrom, Field: "plan", Label: "c4", Params: json.RawMessage(`{"src": 3}`)},
	} {
		if got := KeySchemaWith([]*types.Attribute{a}, s, testInherits); got != s {
			t.Errorf("%s: contributed %+v", a.Label, got.Fields)
		}
	}
}

// TestInheritedSource (U67 E3-S2): the source field of a declared slot,
// or its coded refusal — the rule predict's field-reference walk and
// the runtime adapter both run.
func TestInheritedSource(t *testing.T) {
	t.Parallel()

	s := keySchemaFixture(t)
	if f, cerr := InheritedSource(&types.Attribute{Type: extCopy, Field: "plan"}, s, testInherits); cerr != nil || f == nil || f.Name != "plan" {
		t.Fatalf("Field source: %+v %v", f, cerr)
	}
	if f, cerr := InheritedSource(&types.Attribute{Type: extFrom, Params: json.RawMessage(`{"src": "active"}`)}, s, testInherits); cerr != nil || f == nil || f.Name != "active" {
		t.Fatalf("param source: %+v %v", f, cerr)
	}
	if f, cerr := InheritedSource(&types.Attribute{Type: types.ATTR_ZSCORE, Field: "n"}, s, testInherits); f != nil || cerr != nil {
		t.Fatalf("undeclared type: %+v %v", f, cerr)
	}
	cases := []struct {
		name   string
		attr   *types.Attribute
		code   errors.Code
		reason string
	}{
		{"field unset", &types.Attribute{Type: extCopy}, errors.PROCESSING_CONFIG, "source_unset"},
		{"param unset", &types.Attribute{Type: extFrom, Field: "plan"}, errors.PROCESSING_CONFIG, "source_unset"},
		{"param not a string", &types.Attribute{Type: extFrom, Params: json.RawMessage(`{"src": 1}`)}, errors.PROCESSING_CONFIG, "source_unset"},
		{"not a cohort field", &types.Attribute{Type: extCopy, Field: "nope"}, errors.PROCESSING_CONFIG, "source_not_cohort_field"},
		{"uncoded", &types.Attribute{Type: extCopy, Field: "n"}, errors.PULSE_FIELD_TYPE_UNSUPPORTED, "source_not_coded"},
	}
	for _, c := range cases {
		_, cerr := InheritedSource(c.attr, s, testInherits)
		if cerr == nil || cerr.Code != c.code || cerr.Details["reason"] != c.reason || cerr.Details["attribute"] != string(c.attr.Type) {
			t.Errorf("%s: %v, want %s reason %s", c.name, cerr, c.code, c.reason)
		}
	}
	if _, cerr := InheritedSource(&types.Attribute{Type: extFrom}, s, testInherits); cerr == nil || cerr.Details["source"] != "params.src" {
		t.Fatalf("param source named %v, want params.src", cerr)
	}
	if n := CodeCount(s.Field("plan")); n != 2 {
		t.Fatalf("CodeCount(plan) = %d, want 2", n)
	}
	if n := CodeCount(s.Field("active")); n != 2 {
		t.Fatalf("CodeCount(active) = %d, want 2 (packed_bool 0 / 1)", n)
	}
}

// TestCodeFault (U67 E3-S2): a value is a code of an n-entry dictionary
// iff it is an integer in [0, n); NaN (a null) is never a fault.
func TestCodeFault(t *testing.T) {
	t.Parallel()

	for _, v := range []float64{0, 1, 2} {
		if CodeFault(v, 3) {
			t.Errorf("%v faulted on 3 entries", v)
		}
	}
	for _, v := range []float64{3, -1, 0.5, 1e300} {
		if !CodeFault(v, 3) {
			t.Errorf("%v accepted on 3 entries", v)
		}
	}
	if CodeFault(math.NaN(), 3) {
		t.Error("NaN faulted")
	}
}
