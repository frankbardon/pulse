package encoding_test

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// twoDictSchema is a schema with a large-ish "big" dictionary and a small
// "small" one, both categorical_u8.
func twoDictSchema(t *testing.T, big, small []string) *encoding.Schema {
	t.Helper()
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "big", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: dictOf(t, big...)},
		{Name: "small", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: dictOf(t, small...)},
	}}
}

// TestMergeDictUnion_ReorderSharesCanonical: an incoming dictionary of
// canonical's own values in another order needs a remap but no new
// schema, so the merge returns canonical itself. A create folding N such
// sources then retains ONE schema snapshot across their replay plans,
// not N copies of every dictionary.
func TestMergeDictUnion_ReorderSharesCanonical(t *testing.T) {
	t.Parallel()

	canonical := twoDictSchema(t, []string{"a", "b", "c"}, []string{"x", "y"})
	incoming := twoDictSchema(t, []string{"c", "a"}, []string{"x"})

	got, remap, err := encx.MergeDictUnion(canonical, incoming)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}
	if got != canonical {
		t.Fatalf("a reorder-only merge returned a new schema; want canonical itself (one shared snapshot)")
	}
	// c (incoming 0) -> 2, a (incoming 1) -> 0.
	want := encx.DictRemap{0: 2, 1: 0}
	if r := remap[0]; len(r) != len(want) || r[0] != 2 || r[1] != 0 {
		t.Fatalf("big remap = %v, want %v", r, want)
	}
	if _, ok := remap[1]; ok {
		t.Fatalf("small is a prefix and needs no remap, got %v", remap[1])
	}
	if v := got.Fields[0].Dictionary.Values(); len(v) != 3 {
		t.Fatalf("canonical big dict changed: %v", v)
	}
}

// TestMergeDictUnion_ExtensionSharesUntouchedDictionaries: when the
// union grows one field, the returned schema rebuilds THAT dictionary
// only; every other dictionary is the canonical one by pointer, and
// canonical itself is left unchanged.
func TestMergeDictUnion_ExtensionSharesUntouchedDictionaries(t *testing.T) {
	t.Parallel()

	canonical := twoDictSchema(t, []string{"a", "b", "c"}, []string{"x"})
	incoming := twoDictSchema(t, []string{"a"}, []string{"z"})

	got, remap, err := encx.MergeDictUnion(canonical, incoming)
	if err != nil {
		t.Fatalf("MergeDictUnion: %v", err)
	}
	if got == canonical {
		t.Fatalf("an extending merge returned canonical; want a new schema")
	}
	if got.Fields[0].Dictionary != canonical.Fields[0].Dictionary {
		t.Errorf("untouched dictionary was copied; want it shared by pointer")
	}
	if v := got.Fields[1].Dictionary.Values(); len(v) != 2 || v[0] != "x" || v[1] != "z" {
		t.Errorf("extended small dict = %v, want [x z]", v)
	}
	if v := canonical.Fields[1].Dictionary.Values(); len(v) != 1 || v[0] != "x" {
		t.Errorf("canonical small dict mutated: %v", v)
	}
	if r := remap[1]; len(r) != 1 || r[0] != 1 {
		t.Errorf("small remap = %v, want {0:1}", r)
	}
}
