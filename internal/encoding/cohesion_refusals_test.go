package encoding

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// TestDictCohesion_StandaloneRefusals: each dictionary-cohesion entry
// point refuses a missing schema, a different field count and a
// dictionary rung change on its own — none relies on the structural
// validator having run first.
func TestDictCohesion_StandaloneRefusals(t *testing.T) {
	t.Parallel()

	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			d.Add(v)
		}
		return d
	}
	canon := &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("east", "west")},
	}}
	fewer := &encoding.Schema{Fields: canon.Fields[:1]}
	wider := &encoding.Schema{Fields: []encoding.Field{
		canon.Fields[0],
		{Name: "region", Type: encoding.FieldTypeCategoricalU16, Dictionary: dict("east", "west")},
	}}

	prefix := func(a, b *encoding.Schema) error { _, err := ValidateDictPrefixRule(a, b); return err }
	union := func(a, b *encoding.Schema) error { _, _, err := MergeDictUnion(a, b); return err }
	structural := func(a, b *encoding.Schema) error { _, err := ValidateStructuralCohesion(a, b); return err }
	cases := []struct {
		name string
		fn   func(a, b *encoding.Schema) error
		a, b *encoding.Schema
		code errors.Code
	}{
		{"prefix nil canonical", prefix, nil, canon, errors.PULSE_SHARD_DICT_DIVERGENCE},
		{"prefix nil incoming", prefix, canon, nil, errors.PULSE_SHARD_DICT_DIVERGENCE},
		{"prefix field count", prefix, canon, fewer, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"prefix rung change", prefix, canon, wider, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"union nil", union, canon, nil, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"union field count", union, canon, fewer, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"union rung change", union, canon, wider, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"structural nil", structural, nil, canon, errors.PULSE_SHARD_SCHEMA_MISMATCH},
		{"structural rung change", structural, canon, wider, errors.PULSE_SHARD_SCHEMA_MISMATCH},
	}
	for _, tc := range cases {
		if err := tc.fn(tc.a, tc.b); !errors.HasCode(err, tc.code) {
			t.Errorf("%s: err = %v, want %s", tc.name, err, tc.code)
		}
	}

	// The happy path still merges: a prefix extends, a union remaps.
	ext := &encoding.Schema{Fields: []encoding.Field{canon.Fields[0], {Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("east")}}}
	if got, err := ValidateDictPrefixRule(canon, ext); err != nil || got.Fields[1].Dictionary.Count() != 2 {
		t.Errorf("prefix happy path = (%v, %v)", got, err)
	}
	swapped := &encoding.Schema{Fields: []encoding.Field{canon.Fields[0], {Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("north", "east")}}}
	merged, remap, err := MergeDictUnion(canon, swapped)
	if err != nil || merged.Fields[1].Dictionary.Count() != 3 || remap[1][0] != 2 || remap[1][1] != 0 {
		t.Errorf("union = (%v, %v, %v), want north→2, east→0", merged, remap, err)
	}
}
