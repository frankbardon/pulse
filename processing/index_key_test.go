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

func compositeTestFields() (*encoding.Field, *encoding.Field) {
	dict := encoding.NewDictionary()
	dict.Add("north") // id 0
	dict.Add("south") // id 1
	region := &encoding.Field{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict}
	period := &encoding.Field{Name: "period", Type: encoding.FieldTypeU16}
	return region, period
}

func TestCompositeKeyFieldOnWireBytes_TwoColumn_ConcatenatesInOrder(t *testing.T) {
	region, period := compositeTestFields()
	schema := &encoding.Schema{Fields: []encoding.Field{*region, *period}}
	rec := NewRecord(schema, map[string]float64{"region": 1, "period": 2022})

	got, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{region, period})
	if !ok {
		t.Fatal("CompositeKeyFieldOnWireBytes: not ok")
	}
	// region (1 byte, dict id 1) + period (2 bytes LE, 2022).
	period16 := uint16(2022)
	want := []byte{1, byte(period16), byte(period16 >> 8)}
	if string(got) != string(want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestCompositeKeyFieldOnWireBytes_OrderSignificant(t *testing.T) {
	region, period := compositeTestFields()
	schema := &encoding.Schema{Fields: []encoding.Field{*region, *period}}
	rec := NewRecord(schema, map[string]float64{"region": 1, "period": 2022})

	ab, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{region, period})
	if !ok {
		t.Fatal("CompositeKeyFieldOnWireBytes(region,period): not ok")
	}
	ba, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{period, region})
	if !ok {
		t.Fatal("CompositeKeyFieldOnWireBytes(period,region): not ok")
	}
	if string(ab) == string(ba) {
		t.Fatalf("reversed-order composite keys must not be byte-equal: AB=% x BA=% x", ab, ba)
	}
	if len(ab) != len(ba) {
		t.Fatalf("reversed-order composite keys should still be same total width: AB=%d BA=%d", len(ab), len(ba))
	}
}

func TestCompositeKeyFieldOnWireBytes_AnyNullComponentSkipsRow(t *testing.T) {
	region, period := compositeTestFields()
	region.Nullable = true
	schema := &encoding.Schema{Fields: []encoding.Field{*region, *period}}
	rec := NewRecordWithWide(schema, map[string]float64{"period": 2022}, map[string]bool{"region": true}, nil)

	_, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{region, period})
	if ok {
		t.Fatal("expected not-ok when any composite key component is null")
	}
}

func TestCompositeKeyFieldOnWireBytes_EmptyFieldsNotOK(t *testing.T) {
	_, ok := CompositeKeyFieldOnWireBytes(&Record{}, nil)
	if ok {
		t.Fatal("expected not-ok for an empty fields slice")
	}
}

func TestCompositeKeyFieldOnWireBytes_SingleColumnMatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "id", Type: encoding.FieldTypeU32}
	schema := &encoding.Schema{Fields: []encoding.Field{*field}}
	rec := NewRecord(schema, map[string]float64{"id": 3})

	composite, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{field})
	if !ok {
		t.Fatal("CompositeKeyFieldOnWireBytes: not ok")
	}
	single, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatal("KeyFieldOnWireBytes: not ok")
	}
	if string(composite) != string(single) {
		t.Errorf("1-element composite key = % x, want byte-identical single key = % x", composite, single)
	}
}

func TestResolveCompositeLookupKeyBytes_MatchesRecordResolvedBytes(t *testing.T) {
	region, period := compositeTestFields()
	schema := &encoding.Schema{Fields: []encoding.Field{*region, *period}}
	rec := NewRecord(schema, map[string]float64{"region": 1, "period": 2022})

	fromRecord, ok := CompositeKeyFieldOnWireBytes(rec, []*encoding.Field{region, period})
	if !ok {
		t.Fatal("CompositeKeyFieldOnWireBytes: not ok")
	}

	fromLiteral, err := ResolveCompositeLookupKeyBytes([]*encoding.Field{region, period}, []string{"south", "2022"})
	if err != nil {
		t.Fatalf("ResolveCompositeLookupKeyBytes: %v", err)
	}

	if string(fromRecord) != string(fromLiteral) {
		t.Errorf("record-resolved bytes % x != literal-resolved bytes % x; build-time and lookup-time composite keys must be byte-equal", fromRecord, fromLiteral)
	}
}

func TestResolveCompositeLookupKeyBytes_OrderSignificant(t *testing.T) {
	region, period := compositeTestFields()

	ab, err := ResolveCompositeLookupKeyBytes([]*encoding.Field{region, period}, []string{"south", "2022"})
	if err != nil {
		t.Fatalf("ResolveCompositeLookupKeyBytes(region,period): %v", err)
	}
	ba, err := ResolveCompositeLookupKeyBytes([]*encoding.Field{period, region}, []string{"2022", "south"})
	if err != nil {
		t.Fatalf("ResolveCompositeLookupKeyBytes(period,region): %v", err)
	}
	if string(ab) == string(ba) {
		t.Fatalf("reversed-order composite keys must not be byte-equal: AB=% x BA=% x", ab, ba)
	}
}

func TestResolveCompositeLookupKeyBytes_ArityMismatch(t *testing.T) {
	region, period := compositeTestFields()

	_, err := ResolveCompositeLookupKeyBytes([]*encoding.Field{region, period}, []string{"south"})
	if err == nil {
		t.Fatal("expected error when literals count does not match fields count")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestResolveCompositeLookupKeyBytes_PropagatesPerFieldError(t *testing.T) {
	region, period := compositeTestFields()

	_, err := ResolveCompositeLookupKeyBytes([]*encoding.Field{region, period}, []string{"nowhere", "2022"})
	if err == nil {
		t.Fatal("expected error for a categorical literal absent from the dictionary")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}
