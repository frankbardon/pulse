package processing

import (
	"encoding/binary"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

func TestResolveLookupKeyBytes_NumericMatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "id", Type: encoding.FieldTypeU32}

	got, err := ResolveLookupKeyBytes(field, "3")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}

	rec := NewRecord(&encoding.Schema{Fields: []encoding.Field{*field}}, map[string]float64{"id": 3})
	want, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatalf("KeyFieldOnWireBytes: not ok")
	}
	if len(got) != 4 || binary.LittleEndian.Uint32(got) != 3 {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want 4-byte LE encoding of 3", "3", got)
	}
	if string(got) != string(want) {
		t.Errorf("literal-resolved bytes % x != record-resolved bytes % x; build-time and lookup-time keys must be byte-equal", got, want)
	}
}

func TestResolveLookupKeyBytes_Categorical(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("north") // id 0
	dict.Add("south") // id 1
	field := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}

	got, err := ResolveLookupKeyBytes(field, "south")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want [1] (dictionary id for \"south\")", "south", got)
	}
}

func TestResolveLookupKeyBytes_CategoricalMiss(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("north")
	field := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}

	_, err := ResolveLookupKeyBytes(field, "nowhere")
	if err == nil {
		t.Fatal("expected error for a value absent from the dictionary")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestResolveLookupKeyBytes_NumericParseError(t *testing.T) {
	field := &encoding.Field{Name: "id", Type: encoding.FieldTypeU32}

	_, err := ResolveLookupKeyBytes(field, "not-a-number")
	if err == nil {
		t.Fatal("expected error for an unparseable numeric literal")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

// TestResolveLookupKeyBytes_DateRejected documents the E2-S3 FOLLOWUP:
// date is excluded from IsIndexKeyableFieldType today even though it
// could key exactly, pending the full keyable-type policy call. See
// IsIndexKeyableFieldType's doc comment.
func TestResolveLookupKeyBytes_DateRejected(t *testing.T) {
	field := &encoding.Field{Name: "created", Type: encoding.FieldTypeDate}

	_, err := ResolveLookupKeyBytes(field, "2024-01-01")
	if err == nil {
		t.Fatal("expected error for a date key field (E2-S3 scope)")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

// TestResolveLookupKeyBytes_Decimal128Rejected documents the same
// E2-S3 FOLLOWUP for decimal128.
func TestResolveLookupKeyBytes_Decimal128Rejected(t *testing.T) {
	field := &encoding.Field{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2}

	_, err := ResolveLookupKeyBytes(field, "12.34")
	if err == nil {
		t.Fatal("expected error for a decimal128 key field (E2-S3 scope)")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestResolveLookupKeyBytes_NilField(t *testing.T) {
	_, err := ResolveLookupKeyBytes(nil, "1")
	if err == nil {
		t.Fatal("expected error for a nil field")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}
