package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestGroupDeclarationRefusalsAreCoded pins the contract the materialize
// group plan relies on to have no uncoded fallback (cohort-reshape E6-S3):
// every refusal NewGroupEncoder and DedupGate.ScreenWidths return is a BARE
// *errors.CodedError — not wrapped, so a direct type assertion holds — with
// the code each class documents.
func TestGroupDeclarationRefusalsAreCoded(t *testing.T) {
	t.Parallel()

	flat := &encoding.Schema{Fields: []encoding.Field{
		{Name: "k", Type: encoding.FieldTypeU8}, {Name: "p", Type: encoding.FieldTypeU8},
	}}
	one := &encoding.Schema{Fields: []encoding.Field{{Name: "only", Type: encoding.FieldTypeU8}}}
	grouped := &encoding.Schema{Fields: flat.Fields, Groups: []encoding.Group{{Kind: encoding.GroupKindIndexed,
		Members: []encoding.GroupMember{{Field: 0, Key: true}}}}}

	check := func(name string, err error, want errors.Code) {
		t.Helper()
		ce, ok := err.(*errors.CodedError)
		if !ok {
			t.Errorf("%s: err = %T %v, want a bare *errors.CodedError", name, err, err)
			return
		}
		if ce.Code != want {
			t.Errorf("%s: code %s, want %s", name, ce.Code, want)
		}
	}

	for _, tc := range []struct {
		name   string
		schema *encoding.Schema
		specs  []GroupSpec
		code   errors.Code
	}{
		{"no members", flat, []GroupSpec{{}}, errors.PULSE_GROUP_DECLARATION_INVALID},
		{"unknown member", flat, []GroupSpec{{Members: []string{"zz"}}}, errors.PULSE_GROUP_FIELD_UNKNOWN},
		{"member twice", flat, []GroupSpec{{Members: []string{"k", "k"}}}, errors.PULSE_GROUP_DECLARATION_INVALID},
		{"member in two groups", flat, []GroupSpec{{Members: []string{"k"}}, {Members: []string{"k", "p"}}}, errors.PULSE_GROUP_FIELD_CONFLICT},
		{"key not a member", flat, []GroupSpec{{Members: []string{"k"}, Key: []string{"p"}}}, errors.PULSE_GROUP_DECLARATION_INVALID},
		{"grouped input", grouped, []GroupSpec{{Members: []string{"p"}}}, errors.ENCODING_INVALID},
		{"zero stride", one, []GroupSpec{{Kind: encoding.GroupKindConstant, Members: []string{"only"}}}, errors.ENCODING_INVALID},
	} {
		_, err := NewGroupEncoder(tc.schema, tc.specs)
		check("NewGroupEncoder "+tc.name, err, tc.code)
	}

	_, _, _, err := DedupGate{}.ScreenWidths(flat, []GroupSpec{{Members: []string{"zz"}}})
	check("ScreenWidths unknown member", err, errors.PULSE_GROUP_FIELD_UNKNOWN)
	_, _, _, err = DedupGate{Strict: true}.ScreenWidths(flat, []GroupSpec{{Members: []string{"k"}}})
	check("ScreenWidths strict too narrow", err, errors.PULSE_GROUP_TOO_NARROW)
}
