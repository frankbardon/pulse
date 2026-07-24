package processing

import (
	"encoding/binary"
	stderrors "errors"
	"strings"
	"testing"
	"time"

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

// TestIsIndexKeyableFieldType_Matrix is the settled E2-S3 keyable-type
// policy, enumerated exhaustively: every registered encoding.FieldType
// maps to an explicit allow/reject expectation. Extending
// encoding.FieldType without updating this table is the intended
// failure mode — a new case falls through to the switch's implicit
// "not in either list" branch below and fails the test, forcing an
// explicit policy decision for the new type.
func TestIsIndexKeyableFieldType_Matrix(t *testing.T) {
	tests := []struct {
		ft   encoding.FieldType
		want bool
	}{
		{encoding.FieldTypeU4, true},
		{encoding.FieldTypeU8, true},
		{encoding.FieldTypeU16, true},
		{encoding.FieldTypeU32, true},
		{encoding.FieldTypeU64, true},
		{encoding.FieldTypeF32, true},
		{encoding.FieldTypeF64, true},
		{encoding.FieldTypeDate, true},
		{encoding.FieldTypePackedBool, true},
		{encoding.FieldTypeCategoricalU8, true},
		{encoding.FieldTypeCategoricalU16, true},
		{encoding.FieldTypeCategoricalU32, true},
		{encoding.FieldTypeDecimal128, true},
		{encoding.FieldTypeSetU8, false},
		{encoding.FieldTypeSetU16, false},
		{encoding.FieldTypeSetU32, false},
		{encoding.FieldTypeSetU64, false},
	}
	for _, tt := range tests {
		t.Run(tt.ft.String(), func(t *testing.T) {
			if got := IsIndexKeyableFieldType(tt.ft); got != tt.want {
				t.Errorf("IsIndexKeyableFieldType(%s) = %v, want %v", tt.ft, got, tt.want)
			}
		})
	}
}

// TestResolveLookupKeyBytes_DateMatchesKeyFieldOnWireBytes proves date
// keys resolve to the EXACT on-wire epoch-day uint32 (no lossy
// round-trip), and that the literal-resolved bytes and the
// record-resolved bytes for the same logical date are byte-identical.
// The expected epoch-day value is computed independently of
// encoding.ParseDate (via the standard library directly) so the test
// does not just check the resolver against itself.
func TestResolveLookupKeyBytes_DateMatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "created", Type: encoding.FieldTypeDate}

	got, err := ResolveLookupKeyBytes(field, "2024-01-01")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}

	wantDays := uint32(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix() / 86400)
	if len(got) != 4 || binary.LittleEndian.Uint32(got) != wantDays {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want 4-byte LE encoding of %d", "2024-01-01", got, wantDays)
	}

	schema := &encoding.Schema{Fields: []encoding.Field{*field}}
	rec := NewRecord(schema, map[string]float64{"created": float64(wantDays)})
	want, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatalf("KeyFieldOnWireBytes: not ok")
	}
	if string(got) != string(want) {
		t.Errorf("literal-resolved date bytes % x != record-resolved bytes % x", got, want)
	}
}

func TestResolveLookupKeyBytes_DateParseError(t *testing.T) {
	field := &encoding.Field{Name: "created", Type: encoding.FieldTypeDate}

	_, err := ResolveLookupKeyBytes(field, "not-a-date")
	if err == nil {
		t.Fatal("expected error for an unparseable date literal")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

// TestResolveLookupKeyBytes_Decimal128ExactMatchesKeyFieldOnWireBytes
// proves decimal128 keys resolve to the EXACT 16-byte on-wire mantissa
// (no float64 round-trip): the expected bytes are computed
// independently via encoding.NewDecimal128FromInt +
// encoding.EncodeDecimal128 (mantissa 1234 at scale 2 == "12.34"), and
// the literal-resolved bytes must match both that independent
// expectation AND the record-resolved bytes for a Record carrying the
// same Decimal128 in its wide value map.
func TestResolveLookupKeyBytes_Decimal128ExactMatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2}

	got, err := ResolveLookupKeyBytes(field, "12.34")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}
	if len(got) != 16 {
		t.Fatalf("decimal128 key width = %d, want 16", len(got))
	}

	wantDec := encoding.NewDecimal128FromInt(1234) // mantissa 1234, scale 2 -> "12.34"
	wantBytes := encoding.EncodeDecimal128(wantDec)
	if string(got) != string(wantBytes[:]) {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want exact mantissa bytes % x (no float round-trip)", "12.34", got, wantBytes)
	}

	schema := &encoding.Schema{Fields: []encoding.Field{*field}}
	rec := NewRecordWithWide(schema, map[string]float64{"amount": 12.34}, nil, map[string]any{"amount": wantDec})
	want, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatalf("KeyFieldOnWireBytes: not ok")
	}
	if string(got) != string(want) {
		t.Errorf("literal-resolved decimal bytes % x != record-resolved bytes % x", got, want)
	}
}

// TestResolveLookupKeyBytes_Decimal128RescalesToFieldScale proves a
// literal parsed at a different implicit scale than the field's
// declared scale is rescaled BEFORE encoding — "12.3" (parsed scale 1)
// against a scale-2 field must resolve to mantissa 1230, matching what
// io/import.go's convertValueWide would persist for the same cell.
func TestResolveLookupKeyBytes_Decimal128RescalesToFieldScale(t *testing.T) {
	field := &encoding.Field{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2}

	got, err := ResolveLookupKeyBytes(field, "12.3")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}

	wantBytes := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(1230))
	if string(got) != string(wantBytes[:]) {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want rescaled mantissa bytes % x", "12.3", got, wantBytes)
	}
}

func TestResolveLookupKeyBytes_Decimal128PrecisionOverflow(t *testing.T) {
	field := &encoding.Field{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 2, Scale: 0}

	_, err := ResolveLookupKeyBytes(field, "999")
	if err == nil {
		t.Fatal("expected error for a decimal literal exceeding the field's declared precision")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

func TestResolveLookupKeyBytes_Decimal128ParseError(t *testing.T) {
	field := &encoding.Field{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2}

	_, err := ResolveLookupKeyBytes(field, "not-a-decimal")
	if err == nil {
		t.Fatal("expected error for an unparseable decimal literal")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
}

// TestResolveLookupKeyBytes_PackedBoolMatchesKeyFieldOnWireBytes proves
// packed_bool keys resolve exactly and that the record-side and
// literal-side bytes agree, for both the true and false states.
func TestResolveLookupKeyBytes_PackedBoolMatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "active", Type: encoding.FieldTypePackedBool}
	schema := &encoding.Schema{Fields: []encoding.Field{*field}}

	for _, tt := range []struct {
		literal string
		numeric float64
	}{
		{"1", 1},
		{"0", 0},
	} {
		got, err := ResolveLookupKeyBytes(field, tt.literal)
		if err != nil {
			t.Fatalf("ResolveLookupKeyBytes(%q): %v", tt.literal, err)
		}
		if len(got) != 1 || got[0] != byte(tt.numeric) {
			t.Errorf("ResolveLookupKeyBytes(%q) = % x, want single byte %d", tt.literal, got, int(tt.numeric))
		}

		rec := NewRecord(schema, map[string]float64{"active": tt.numeric})
		want, ok := KeyFieldOnWireBytes(rec, field)
		if !ok {
			t.Fatalf("KeyFieldOnWireBytes(%v): not ok", tt.numeric)
		}
		if string(got) != string(want) {
			t.Errorf("literal-resolved packed_bool bytes % x != record-resolved bytes % x", got, want)
		}
	}

	// Distinct states must resolve to distinct bytes (injective encoding).
	trueBytes, _ := ResolveLookupKeyBytes(field, "1")
	falseBytes, _ := ResolveLookupKeyBytes(field, "0")
	if string(trueBytes) == string(falseBytes) {
		t.Fatal("true and false packed_bool states must not resolve to the same key bytes")
	}
}

// TestResolveLookupKeyBytes_U4MatchesKeyFieldOnWireBytes covers the
// other bit-packed keyable type: u4's decoded range 0-15 fits in the
// same synthetic 1-byte slot packed_bool uses.
func TestResolveLookupKeyBytes_U4MatchesKeyFieldOnWireBytes(t *testing.T) {
	field := &encoding.Field{Name: "tier", Type: encoding.FieldTypeU4}
	schema := &encoding.Schema{Fields: []encoding.Field{*field}}

	got, err := ResolveLookupKeyBytes(field, "9")
	if err != nil {
		t.Fatalf("ResolveLookupKeyBytes: %v", err)
	}
	if len(got) != 1 || got[0] != 9 {
		t.Errorf("ResolveLookupKeyBytes(%q) = % x, want [9]", "9", got)
	}

	rec := NewRecord(schema, map[string]float64{"tier": 9})
	want, ok := KeyFieldOnWireBytes(rec, field)
	if !ok {
		t.Fatalf("KeyFieldOnWireBytes: not ok")
	}
	if string(got) != string(want) {
		t.Errorf("literal-resolved u4 bytes % x != record-resolved bytes % x", got, want)
	}
}

// TestResolveLookupKeyBytes_SetRejectedWithClearMessage proves set_*
// fields are rejected — the REJECT half of the E2-S3 keyable-type
// policy — with a message that explains the ambiguous-equality
// rationale rather than a generic "not supported" string, so the
// caller understands WHY (not just that) a set_* key is disallowed.
func TestResolveLookupKeyBytes_SetRejectedWithClearMessage(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("VISA")
	dict.Add("MC")
	field := &encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict}

	_, err := ResolveLookupKeyBytes(field, "VISA")
	if err == nil {
		t.Fatal("expected error for a set_u8 key field")
	}
	if !errors.HasCode(err, errors.PROCESSING_CONFIG) {
		t.Errorf("expected PROCESSING_CONFIG, got: %v", err)
	}
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) {
		t.Fatalf("expected a *errors.CodedError in the chain, got: %v", err)
	}
	if !strings.Contains(ce.Message, "ambiguous") && !strings.Contains(ce.Message, "FILTER_SET") {
		t.Errorf("expected a set_*-specific explanation in the error message, got: %q", ce.Message)
	}
}

// TestKeyFieldOnWireBytes_SetRejected proves the build-side resolver
// rejects set_* fields too (KeyFieldOnWireBytes short-circuits via
// IsIndexKeyableFieldType before ever touching the record).
func TestKeyFieldOnWireBytes_SetRejected(t *testing.T) {
	dict := encoding.NewDictionary()
	dict.Add("VISA")
	field := &encoding.Field{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: dict}
	schema := &encoding.Schema{Fields: []encoding.Field{*field}}
	rec := NewRecordWithWide(schema, nil, nil, map[string]any{"tags": uint64(1)})

	_, ok := KeyFieldOnWireBytes(rec, field)
	if ok {
		t.Fatal("expected not-ok for a set_u8 key field")
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
