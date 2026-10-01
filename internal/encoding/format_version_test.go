package encoding

import (
	"bytes"
	stderrors "errors"
	"flag"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

var updateFormatGolden = flag.Bool("update", false, "rewrite encoding/testdata/format_v1.pulse from the generator")

// formatV1GoldenPath is a checked-in 0x01 cohort. It is read by every
// future binary, permanently: old cohorts stay readable forever, and a
// change that stops this file parsing — or changes a single byte a
// current writer emits for the same schema and rows — fails here.
const formatV1GoldenPath = "../../encoding/testdata/format_v1.pulse"

// formatGoldenSchema is a synthetic schema spanning the field-type
// families whose schema-block encodings differ: plain numerics, the two
// temporal types, both bit-packed types, decimal128 (precision/scale
// suffix), categorical and set (inline dictionaries), a wide set rung,
// and nullable fields (per-record bitmap).
func formatGoldenSchema() *encoding.Schema {
	cat := encoding.NewDictionary()
	for _, v := range []string{"alpha", "beta", "gamma"} {
		_, _ = cat.Add(v)
	}
	set := encoding.NewDictionary()
	for _, v := range []string{"red", "green", "blue"} {
		_, _ = set.Add(v)
	}
	wide := encoding.NewDictionary()
	for _, v := range []string{"w0", "w1"} {
		_, _ = wide.Add(v)
	}
	fields := []encoding.Field{
		{Name: "n_u8", Type: encoding.FieldTypeU8, Description: "an unsigned byte"},
		{Name: "n_u16", Type: encoding.FieldTypeU16},
		{Name: "n_u32", Type: encoding.FieldTypeU32, Nullable: true},
		{Name: "n_u64", Type: encoding.FieldTypeU64},
		{Name: "n_f32", Type: encoding.FieldTypeF32},
		{Name: "n_f64", Type: encoding.FieldTypeF64},
		{Name: "d_day", Type: encoding.FieldTypeDate},
		{Name: "d_sec", Type: encoding.FieldTypeDateTime},
		{Name: "flag", Type: encoding.FieldTypePackedBool},
		{Name: "nib", Type: encoding.FieldTypeU4},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 10, Scale: 2},
		{Name: "label", Type: encoding.FieldTypeCategoricalU8, Dictionary: cat, Description: "a categorical label"},
		{Name: "tags", Type: encoding.FieldTypeSetU16, Dictionary: set},
		{Name: "wide", Type: encoding.FieldTypeSetU128, Dictionary: wide},
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		if fields[i].Type.IsBitPacked() {
			off++
		} else {
			off += fields[i].Type.ByteSize()
		}
	}
	return &encoding.Schema{Fields: fields}
}

// formatGoldenRow is one record of the golden fixture.
type formatGoldenRow struct {
	u8        uint8
	u16       uint16
	u32       uint32
	u32Null   bool
	u64       uint64
	f32       float32
	f64       float64
	day       uint32
	sec       uint64
	flag      bool
	nib       uint8
	amount    int64 // mantissa at scale 2
	amtNull   bool
	label     uint32
	tags      uint64
	wideWords [encoding.SetMaskWords]uint64
}

var formatGoldenRows = []formatGoldenRow{
	{u8: 7, u16: 1000, u32: 70000, u64: 1 << 40, f32: 1.5, f64: -2.25, day: 19000, sec: 1641600000, flag: true, nib: 9, amount: 12345, label: 0, tags: 0b101, wideWords: [encoding.SetMaskWords]uint64{0b10}},
	{u8: 0, u16: 0, u32Null: true, u64: 0, f32: 0, f64: 0, day: 0, sec: 0, flag: false, nib: 0, amtNull: true, label: 2, tags: 0, wideWords: [encoding.SetMaskWords]uint64{}},
	{u8: 255, u16: 65535, u32: 4294967295, u64: 1<<63 + 5, f32: -0.125, f64: 1e300, day: 1, sec: 86399, flag: true, nib: 15, amount: -99, label: 1, tags: 0b111, wideWords: [encoding.SetMaskWords]uint64{0b11}},
}

// encodeFormatGoldenRecords writes the fixed-stride record region.
func encodeFormatGoldenRecords(t *testing.T, s *encoding.Schema) []byte {
	t.Helper()
	var b bytes.Buffer
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range formatGoldenRows {
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeU8, uint64(r.u8)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeU16, uint64(r.u16)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeU32, uint64(r.u32)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeU64, r.u64))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeF32, uint64(math.Float32bits(r.f32))))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeF64, math.Float64bits(r.f64)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeDate, uint64(r.day)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeDateTime, r.sec))
		must(encoding.WriteBit(&b, 0, r.flag))
		must(encoding.WriteNibble(&b, false, r.nib))
		must(encoding.WriteDecimal128(&b, encoding.NewDecimal128FromInt(r.amount)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeCategoricalU8, uint64(r.label)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeSetU16, r.tags))
		must(encoding.WriteSetMask(&b, encoding.FieldTypeSetU128, encoding.SetMaskFromWords(r.wideWords)))
		bm := make([]byte, s.BitmapByteSize())
		if r.u32Null {
			encoding.BitmapSetNull(bm, 2)
		}
		if r.amtNull {
			encoding.BitmapSetNull(bm, 10)
		}
		must(encoding.WriteBitmap(&b, bm))
	}
	if b.Len() != len(formatGoldenRows)*s.RecordByteSize() {
		t.Fatalf("record region = %d bytes, want %d rows × stride %d", b.Len(), len(formatGoldenRows), s.RecordByteSize())
	}
	return b.Bytes()
}

// buildFormatFile writes header + schema at version v, then the records.
func buildFormatFile(t *testing.T, s *encoding.Schema, records []byte, v byte) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writePreambleVersion(&b, s, v); err != nil {
		t.Fatalf("writePreambleVersion(0x%02x): %v", v, err)
	}
	b.Write(records)
	return b.Bytes()
}

// schemasEqual compares two schemas field-for-field, dictionaries by
// their ordered values.
func schemasEqual(t *testing.T, got, want *encoding.Schema) {
	t.Helper()
	if len(got.Fields) != len(want.Fields) {
		t.Fatalf("field count = %d, want %d", len(got.Fields), len(want.Fields))
	}
	for i := range want.Fields {
		g, w := got.Fields[i], want.Fields[i]
		gd, wd := g.Dictionary, w.Dictionary
		g.Dictionary, w.Dictionary = nil, nil
		if !reflect.DeepEqual(g, w) {
			t.Errorf("field %d = %+v, want %+v", i, g, w)
		}
		if (gd == nil) != (wd == nil) {
			t.Errorf("field %d dictionary presence differs", i)
			continue
		}
		if wd != nil && !reflect.DeepEqual(gd.Values(), wd.Values()) {
			t.Errorf("field %d dictionary = %v, want %v", i, gd.Values(), wd.Values())
		}
	}
}

// decodeAll reads every record after the preamble into value/null/wide
// maps, returning one entry per record.
func decodeAll(t *testing.T, data []byte) (byte, []map[string]any) {
	t.Helper()
	r := bytes.NewReader(data)
	s, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	rr := NewRecordReader(r, s)
	var out []map[string]any
	for {
		vals, nulls, wide := map[string]float64{}, map[string]bool{}, map[string]any{}
		err := rr.ReadRecordWithWide(vals, nulls, wide)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("record %d: %v", len(out), err)
		}
		row := map[string]any{}
		for k, x := range vals {
			row[k] = x
		}
		for k := range nulls {
			row[k] = "NULL"
		}
		for k, x := range wide {
			row["wide:"+k] = x
		}
		out = append(out, row)
	}
	return v, out
}

// TestFormatV1Golden_ReadableForever reads the checked-in 0x01 cohort
// with the current binary and pins both halves of backward
// compatibility: the old bytes still decode to the known values, and the
// current writer still emits those exact bytes for the same schema and
// rows (a schema using no 0x02 feature must stay 0x01, byte-identical).
func TestFormatV1Golden_ReadableForever(t *testing.T) {
	s := formatGoldenSchema()
	fresh := buildFormatFile(t, s, encodeFormatGoldenRecords(t, s), encoding.FormatVersionV1)

	var viaPreamble bytes.Buffer
	if err := WritePreamble(&viaPreamble, s); err != nil {
		t.Fatal(err)
	}
	viaPreamble.Write(encodeFormatGoldenRecords(t, s))
	if !bytes.Equal(viaPreamble.Bytes(), fresh) {
		t.Fatal("WritePreamble output differs from an explicit 0x01 write")
	}

	if *updateFormatGolden {
		if err := os.MkdirAll(filepath.Dir(formatV1GoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(formatV1GoldenPath, fresh, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(formatV1GoldenPath)
	if err != nil {
		t.Fatalf("reading %s (regenerate with -update): %v", formatV1GoldenPath, err)
	}
	if golden[encoding.HeaderSize-1] != 0x01 {
		t.Fatalf("golden version byte = 0x%02x, want 0x01", golden[encoding.HeaderSize-1])
	}
	if !bytes.Equal(fresh, golden) {
		t.Fatal("the current writer no longer emits the checked-in 0x01 bytes for the golden schema and rows")
	}

	got, err := encoding.ReadSchema(bytes.NewReader(golden[encoding.HeaderSize:]), encoding.FormatVersionV1)
	if err != nil {
		t.Fatalf("ReadSchema(golden): %v", err)
	}
	schemasEqual(t, got, s)

	v, rows := decodeAll(t, golden)
	if v != 0x01 {
		t.Fatalf("ReadPreamble version = 0x%02x, want 0x01", v)
	}
	if len(rows) != 3 {
		t.Fatalf("decoded %d rows, want 3", len(rows))
	}
	// Hard-coded expectations — never derived from the writer under test.
	checks := []struct {
		row   int
		field string
		want  any
	}{
		{0, "n_u8", 7.0}, {0, "n_u16", 1000.0}, {0, "n_u32", 70000.0}, {0, "n_u64", float64(1 << 40)},
		{0, "n_f32", 1.5}, {0, "n_f64", -2.25}, {0, "d_day", 19000.0}, {0, "d_sec", 1641600000.0},
		{0, "flag", 1.0}, {0, "nib", 9.0}, {0, "amount", 123.45}, {0, "label", 0.0}, {0, "tags", 5.0},
		{1, "n_u32", "NULL"}, {1, "amount", "NULL"}, {1, "label", 2.0}, {1, "flag", 0.0},
		{2, "n_u8", 255.0}, {2, "n_u32", 4294967295.0}, {2, "nib", 15.0}, {2, "amount", -0.99},
		{2, "n_f32", -0.125}, {2, "d_sec", 86399.0}, {2, "tags", 7.0},
	}
	for _, c := range checks {
		if g := rows[c.row][c.field]; g != c.want {
			t.Errorf("row %d %s = %v, want %v", c.row, c.field, g, c.want)
		}
	}
	if m, ok := rows[2]["wide:wide"].(encoding.SetMask); !ok || !m.Equal(encoding.SetMaskFromUint64(0b11)) {
		t.Errorf("row 2 wide = %#v, want mask 0b11", rows[2]["wide:wide"])
	}
}

// TestFormatV2_RoundTrip: a 0x02 file reads back with the version, the
// schema and every record identical to its 0x01 twin, and its bytes are
// exactly the 0x01 bytes with the version byte bumped and an empty
// length-prefixed extension block (u64 length 2, then u16
// section_count 0) between the last descriptor and the first record.
func TestFormatV2_RoundTrip(t *testing.T) {
	s := formatGoldenSchema()
	recs := encodeFormatGoldenRecords(t, s)
	v1 := buildFormatFile(t, s, recs, encoding.FormatVersionV1)
	v2 := buildFormatFile(t, s, recs, encoding.FormatVersionV2)

	schemaEnd := len(v1) - len(recs)
	want := append([]byte{}, v1[:encoding.HeaderSize-1]...)
	want = append(want, 0x02)
	want = append(want, v1[encoding.HeaderSize:schemaEnd]...)
	want = append(want, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	want = append(want, recs...)
	if !bytes.Equal(v2, want) {
		t.Fatal("0x02 layout drifted: want 0x01 descriptors + u64(2) extension length + u16(0) section count before the records")
	}

	r := bytes.NewReader(v2)
	got, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble(v2): %v", err)
	}
	if v != 0x02 {
		t.Fatalf("version = 0x%02x, want 0x02", v)
	}
	schemasEqual(t, got, s)
	if rest := r.Len(); rest != len(recs) {
		t.Fatalf("reader left at %d remaining bytes, want the %d-byte record region", rest, len(recs))
	}

	_, rows1 := decodeAll(t, v1)
	_, rows2 := decodeAll(t, v2)
	if !reflect.DeepEqual(rows1, rows2) {
		t.Fatalf("0x02 records differ from 0x01:\n v1=%v\n v2=%v", rows1, rows2)
	}

	// The record locator (record_at) derives the region start from the
	// version-aware read, never from a v1 assumption.
	loc, err := encoding.NewRecordLocator(bytes.NewReader(v2), got)
	if err != nil {
		t.Fatalf("NewRecordLocator(v2): %v", err)
	}
	if loc.RecordRegionStart != int64(schemaEnd+10) || loc.TotalRecords != 3 {
		t.Fatalf("locator start/total = %d/%d, want %d/3", loc.RecordRegionStart, loc.TotalRecords, schemaEnd+10)
	}

	// Writing the decoded schema back out lands at 0x01: the version is a
	// function of content, and nothing in this schema needs 0x02.
	if got.RequiredFormatVersion() != 0x01 {
		t.Fatalf("RequiredFormatVersion = 0x%02x, want 0x01 for a schema with no 0x02 feature", got.RequiredFormatVersion())
	}
}

// TestFormatV2_ReadAsV1Misplaces documents why the version must be
// threaded: parsing a 0x02 schema block with the 0x01 layout leaves the
// reader 10 bytes early, inside the extension block.
func TestFormatV2_ReadAsV1Misplaces(t *testing.T) {
	s := formatGoldenSchema()
	recs := encodeFormatGoldenRecords(t, s)
	v2 := buildFormatFile(t, s, recs, encoding.FormatVersionV2)
	r := bytes.NewReader(v2[encoding.HeaderSize:])
	if _, err := encoding.ReadSchema(r, encoding.FormatVersionV1); err != nil {
		t.Fatal(err)
	}
	if r.Len() != len(recs)+10 {
		t.Fatalf("remaining = %d, want %d (record region + unread extension)", r.Len(), len(recs)+10)
	}
}

func v2PreambleBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := writePreambleVersion(&b, formatGoldenSchema(), encoding.FormatVersionV2); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestReadSchema_V2ExtensionPayloadRefused: extension bytes that are
// not a well-formed section list are refused loud, never skipped.
func TestReadSchema_V2ExtensionPayloadRefused(t *testing.T) {
	data := v2PreambleBytes(t)
	data = data[:len(data)-10]
	data = append(data, 3, 0, 0, 0, 0, 0, 0, 0, 0xAA, 0xBB, 0xCC)
	_, _, err := ReadPreamble(bytes.NewReader(data))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
}

func TestReadSchema_V2TruncatedExtension(t *testing.T) {
	data := v2PreambleBytes(t)
	for cut := 1; cut <= 10; cut++ {
		_, _, err := ReadPreamble(bytes.NewReader(data[:len(data)-cut]))
		if !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Fatalf("cut %d: err = %v, want ENCODING_INVALID", cut, err)
		}
	}
}

// TestReadSchema_UnknownTypeByteEveryVersion: an unknown field-type byte
// still fails loud at parse time under both versions.
func TestReadSchema_UnknownTypeByteEveryVersion(t *testing.T) {
	for _, v := range encoding.SupportedFormatVersions() {
		var b bytes.Buffer
		s := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeU8}}}
		if err := writePreambleVersion(&b, s, v); err != nil {
			t.Fatal(err)
		}
		data := b.Bytes()
		data[encoding.HeaderSize+2] = 0xEE // first descriptor's type byte, after u16 field_count
		_, _, err := ReadPreamble(bytes.NewReader(data))
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID || ce.Details["byte"] != uint8(0xEE) {
			t.Fatalf("version 0x%02x: err = %v, want ENCODING_INVALID naming byte 0xEE", v, err)
		}
	}
}

// TestSchemaDoc_V2RoundTrip: the shard archive's canonical `_schema.pulse`
// participates in versioning — a 0x02 doc's SHRD trailer is found after
// the extension block.
func TestSchemaDoc_V2RoundTrip(t *testing.T) {
	var b bytes.Buffer
	b.Write(v2PreambleBytes(t))
	b.Write(SchemaDocMagic[:])
	b.Write([]byte{42, 0, 0, 0, 0, 0, 0, 0}) // u64 aggregate_record_count
	b.Write([]byte{3, 0})                    // u16 shard_count
	doc, err := ReadSchemaDoc(bytes.NewReader(b.Bytes()))
	if err != nil {
		t.Fatalf("ReadSchemaDoc(v2): %v", err)
	}
	if doc.AggregateRecordCount != 42 || doc.ShardCount != 3 {
		t.Fatalf("doc agg/shards = %d/%d, want 42/3", doc.AggregateRecordCount, doc.ShardCount)
	}
	schemasEqual(t, doc.Schema, formatGoldenSchema())

	// And WriteSchemaDoc emits 0x01 for a schema that needs nothing newer.
	var w bytes.Buffer
	if err := WriteSchemaDoc(&w, formatGoldenSchema(), 42, 3); err != nil {
		t.Fatal(err)
	}
	if w.Bytes()[encoding.HeaderSize-1] != 0x01 {
		t.Fatalf("WriteSchemaDoc version = 0x%02x, want 0x01", w.Bytes()[encoding.HeaderSize-1])
	}
}
