package fieldaccept

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

func TestReferenceRefusal(t *testing.T) {
	t.Parallel()

	dict := encoding.NewDictionary()
	for _, v := range []string{"control", "variant"} {
		if _, err := dict.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict},
		{Name: "x", Type: encoding.FieldTypeF64},
	}}
	for _, tc := range []struct {
		name, split, ref string
		schema           *encoding.Schema
		reason           string // "" = accepted
	}{
		{"absent", "arm", "", schema, ""},
		{"absent without split", "", "", schema, ""},
		{"dictionary level", "arm", "control", schema, ""},
		{"packed_bool level", "flag", "1", schema, ""},
		{"packed_bool zero", "flag", "0", schema, ""},
		{"no schema", "arm", "anything", nil, ""},
		{"derived split", "derived", "anything", schema, ""},
		{"non-level type is the type table's", "x", "1", schema, ""},
		{"without split_by", "", "control", schema, ReferenceReasonWithoutSplitBy},
		{"without split_by, no schema", "", "control", nil, ReferenceReasonWithoutSplitBy},
		{"typo", "arm", "Control", schema, ReferenceReasonNotALevel},
		{"packed_bool alias", "flag", "true", schema, ReferenceReasonNotALevel},
	} {
		cerr := ReferenceRefusal("TEST_WELCH", tc.split, tc.ref, tc.schema)
		if tc.reason == "" {
			if cerr != nil {
				t.Errorf("%s: refused %v", tc.name, cerr)
			}
			continue
		}
		if cerr == nil || cerr.Code != errors.PULSE_TEST_REFERENCE_INVALID || cerr.Details["reason"] != tc.reason {
			t.Errorf("%s: %v, want PULSE_TEST_REFERENCE_INVALID reason %s", tc.name, cerr, tc.reason)
		}
	}
}

func TestTakesReference(t *testing.T) {
	t.Parallel()

	var got []types.TestType
	for _, op := range types.AllTestTypes() {
		if TakesReference(op) {
			got = append(got, op)
		}
	}
	if len(got) != 5 {
		t.Fatalf("reference tests %v, want the five two-group tests", got)
	}
}
