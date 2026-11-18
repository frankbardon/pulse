package encoding

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/temporal"
)

// zoneSchema: a datetime, a categorical zone field holding valid zones,
// a numeric field — with the zone field declared the cohort default.
func zoneSchema(entries ...string) *Schema {
	if len(entries) == 0 {
		entries = []string{"Europe/Berlin", "UTC", "America/New_York"}
	}
	d := NewDictionary()
	for _, e := range entries {
		if _, err := d.Add(e); err != nil {
			panic(err)
		}
	}
	return &Schema{Fields: []Field{
		{Name: "at", Type: FieldTypeDateTime},
		{Name: "tz", Type: FieldTypeCategoricalU16, Dictionary: d},
		{Name: "n", Type: FieldTypeU8},
	}, ZoneField: "tz"}
}

// TestZoneSection_Wire pins the section body and its framing: a schema
// whose only 0x02 feature is the default zone field writes one REQUIRED
// tag-3 section whose body is u16 length + the name.
func TestZoneSection_Wire(t *testing.T) {
	t.Parallel()

	s := zoneSchema()
	var body bytes.Buffer
	writeZoneSection(&body, s)
	if want := []byte{2, 0, 't', 'z'}; !bytes.Equal(body.Bytes(), want) {
		t.Fatalf("ZONE body = %v, want %v", body.Bytes(), want)
	}
	if got := s.RequiredFormatVersion(); got != FormatVersionV2 {
		t.Fatalf("RequiredFormatVersion = 0x%02x, want 0x02 with a default zone field", got)
	}

	var desc, block bytes.Buffer
	if err := writeFieldDescriptors(&desc, s); err != nil {
		t.Fatal(err)
	}
	if err := writeSchemaVersion(&block, s, FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	payload := le.AppendUint16(nil, 1)        // section_count
	payload = le.AppendUint16(payload, 3)     // tag ZONE
	payload = append(payload, 1)              // REQUIRED
	payload = le.AppendUint64(payload, 4)     // section_length
	payload = append(payload, 2, 0, 't', 'z') // body
	want := append(desc.Bytes(), le.AppendUint64(nil, uint64(len(payload)))...)
	want = append(want, payload...)
	if !bytes.Equal(block.Bytes(), want) {
		t.Fatalf("schema block =\n%v\nwant\n%v", block.Bytes(), want)
	}

	// Undeclared: no section, and the schema needs only 0x01.
	s.ZoneField = ""
	if got := s.RequiredFormatVersion(); got != FormatVersionV1 {
		t.Fatalf("undeclared RequiredFormatVersion = 0x%02x, want 0x01", got)
	}
}

// TestZoneSection_RoundTrip: the default zone field survives a full
// read and a layout-only read (which consumes the section and leaves
// the reader at the first record byte), alone and beside GROUPS and
// VECTORS, whose sections it follows in tag order.
func TestZoneSection_RoundTrip(t *testing.T) {
	t.Parallel()

	alone := zoneSchema()
	mixed := zoneSchema()
	mixed.Fields = append(mixed.Fields, Field{Name: "v", Type: FieldTypeVecF32})
	mixed.Vectors = []VectorField{{FieldIndex: 3, Dim: 2}}
	mixed.Groups = []Group{{Kind: GroupKindConstant, Members: []GroupMember{{Field: 2}}, Entries: []byte{7}}}

	for name, s := range map[string]*Schema{"alone": alone, "with groups and vectors": mixed} {
		t.Run(name, func(t *testing.T) {
			var b bytes.Buffer
			if err := writeSchemaVersion(&b, s, FormatVersionV2); err != nil {
				t.Fatal(err)
			}
			const sentinel = "RECORDS"
			b.WriteString(sentinel)

			r := bytes.NewReader(b.Bytes())
			got, err := ReadSchema(r, FormatVersionV2)
			if err != nil {
				t.Fatalf("ReadSchema: %v", err)
			}
			if got.ZoneField != "tz" || !got.HasZoneField() {
				t.Fatalf("ZoneField = %q, want tz", got.ZoneField)
			}
			if r.Len() != len(sentinel) {
				t.Fatalf("full read left %d bytes, want %d", r.Len(), len(sentinel))
			}
			if !reflect.DeepEqual(got.Fields[1].Dictionary.Values(), s.Fields[1].Dictionary.Values()) {
				t.Fatalf("zone dictionary = %v", got.Fields[1].Dictionary.Values())
			}
			if len(got.Groups) != len(s.Groups) || len(got.Vectors) != len(s.Vectors) {
				t.Fatalf("groups/vectors = %d/%d, want %d/%d", len(got.Groups), len(got.Vectors), len(s.Groups), len(s.Vectors))
			}
			if got.Logical().ZoneField != "tz" {
				t.Fatal("Logical() dropped the default zone field")
			}

			lr := bytes.NewReader(b.Bytes())
			lay, err := readSchemaLayout(lr, FormatVersionV2)
			if err != nil {
				t.Fatalf("readSchemaLayout: %v", err)
			}
			if lay.ZoneField != "tz" || lay.Fields[1].Dictionary != nil {
				t.Fatalf("layout read: ZoneField=%q dictionary=%v", lay.ZoneField, lay.Fields[1].Dictionary)
			}
			if lr.Len() != len(sentinel) {
				t.Fatalf("layout read left %d bytes, want %d", lr.Len(), len(sentinel))
			}

			// Re-writing the decoded schema reproduces the bytes.
			var again bytes.Buffer
			if err := writeSchemaVersion(&again, got, got.RequiredFormatVersion()); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again.Bytes(), b.Bytes()[:b.Len()-len(sentinel)]) {
				t.Fatal("decoded schema does not re-write byte-identically")
			}
		})
	}
}

// TestValidateZoneField_Refusals: each declare-time fault is coded, names
// the field, and is refused before a byte is written.
func TestValidateZoneField_Refusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		s    *Schema
		code errors.Code
		msg  string
	}{
		{"missing field", func() *Schema { s := zoneSchema(); s.ZoneField = "nope"; return s }(), errors.ENCODING_INVALID, "names no field"},
		{"non-categorical field", func() *Schema { s := zoneSchema(); s.ZoneField = "n"; return s }(), errors.ENCODING_INVALID, "must be a categorical field"},
		{"datetime field", func() *Schema { s := zoneSchema(); s.ZoneField = "at"; return s }(), errors.ENCODING_INVALID, "must be a categorical field"},
		{"abbreviation entry", zoneSchema("Europe/Berlin", "EST"), errors.PULSE_TIMEZONE_UNKNOWN, "entry 1"},
		{"unknown zone entry", zoneSchema("Mars/Olympus_Mons"), errors.PULSE_TIMEZONE_UNKNOWN, "entry 0"},
		{"empty entry", zoneSchema("UTC", ""), errors.PULSE_TIMEZONE_UNKNOWN, "entry 1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var ce *errors.CodedError
			err := c.s.ValidateZoneField()
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if !strings.Contains(ce.Message, c.msg) {
				t.Fatalf("message %q does not mention %q", ce.Message, c.msg)
			}
			if ce.Details[DetailZoneField] != c.s.ZoneField {
				t.Fatalf("details %v do not name the zone field %q", ce.Details, c.s.ZoneField)
			}
			if c.code == errors.PULSE_TIMEZONE_UNKNOWN {
				if _, ok := ce.Details[temporal.DetailDictionaryIndex]; !ok {
					t.Fatalf("details %v carry no dictionary index", ce.Details)
				}
				if _, ok := ce.Details[errors.DetailTimeZone]; !ok {
					t.Fatalf("details %v carry no zone name", ce.Details)
				}
			}
			for _, v := range []byte{FormatVersionV1, FormatVersionV2} {
				var b bytes.Buffer
				if err := writeSchemaVersion(&b, c.s, v); !errors.HasCode(err, c.code) {
					t.Fatalf("write 0x%02x: err = %v, want %s", v, err, c.code)
				}
				if b.Len() != 0 {
					t.Fatalf("write 0x%02x: %d bytes written before the refusal", v, b.Len())
				}
			}
		})
	}
	if err := zoneSchema().ValidateZoneField(); err != nil {
		t.Fatalf("valid default refused: %v", err)
	}
	if err := (&Schema{Fields: zoneSchema().Fields}).ValidateZoneField(); err != nil {
		t.Fatalf("undeclared default refused: %v", err)
	}
}

// TestZoneSection_ReadRefusals: a hand-made ZONE section is held to the
// same rule at read; a layout-only read, whose dictionaries are skipped,
// checks the field's shape but not its entries.
func TestZoneSection_ReadRefusals(t *testing.T) {
	t.Parallel()

	body := func(name string) []byte {
		return append(binary.LittleEndian.AppendUint16(nil, uint16(len(name))), name...)
	}
	good := zoneSchema()
	badEntry := zoneSchema("Europe/Berlin", "Local")
	cases := []struct {
		name       string
		s          *Schema
		body       []byte
		code       errors.Code
		msg        string
		layoutPass bool
	}{
		{"empty name", good, body(""), errors.ENCODING_INVALID, "empty name", false},
		{"missing field", good, body("nope"), errors.ENCODING_INVALID, "names no field", false},
		{"non-categorical field", good, body("n"), errors.ENCODING_INVALID, "must be a categorical field", false},
		{"invalid zone entry", badEntry, body("tz"), errors.PULSE_TIMEZONE_UNKNOWN, "entry 1", true},
		{"truncated name", good, []byte{5, 0, 't', 'z'}, errors.ENCODING_INVALID, "zone field name", false},
		{"trailing bytes", good, append(body("tz"), 0), errors.ENCODING_INVALID, "trailing bytes", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			block := vecSchemaBlock(t, c.s, sectionTagZone, sectionFlagRequired, c.body)
			_, err := ReadSchema(bytes.NewReader(block), FormatVersionV2)
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != c.code {
				t.Fatalf("ReadSchema err = %v, want %s", err, c.code)
			}
			if !strings.Contains(ce.Message, c.msg) {
				t.Fatalf("message %q does not mention %q", ce.Message, c.msg)
			}
			_, lerr := readSchemaLayout(bytes.NewReader(block), FormatVersionV2)
			if c.layoutPass {
				if lerr != nil {
					t.Fatalf("layout read refused what only a dictionary check can see: %v", lerr)
				}
			} else if !errors.HasCode(lerr, c.code) {
				t.Fatalf("layout read err = %v, want %s", lerr, c.code)
			}
		})
	}
	// Duplicated ZONE section.
	var b bytes.Buffer
	if err := writeFieldDescriptors(&b, good); err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	payload := le.AppendUint16(nil, 2)
	for range 2 {
		payload = le.AppendUint16(payload, sectionTagZone)
		payload = append(payload, sectionFlagRequired)
		payload = le.AppendUint64(payload, uint64(len(body("tz"))))
		payload = append(payload, body("tz")...)
	}
	b.Write(le.AppendUint64(nil, uint64(len(payload))))
	b.Write(payload)
	if _, err := ReadSchema(bytes.NewReader(b.Bytes()), FormatVersionV2); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Fatalf("duplicate ZONE: err = %v, want ENCODING_INVALID", err)
	}
	// The happy path over the same helper reads.
	s, err := ReadSchema(bytes.NewReader(vecSchemaBlock(t, good, sectionTagZone, sectionFlagRequired, body("tz"))), FormatVersionV2)
	if err != nil || s.ZoneField != "tz" {
		t.Fatalf("valid hand-made section: ZoneField=%v err=%v", s, err)
	}
}
