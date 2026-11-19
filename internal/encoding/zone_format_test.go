package encoding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// zoneCohortSchema: a datetime, a categorical zone field and a nullable
// u8, the zone field declared the cohort default.
func zoneCohortSchema() *encoding.Schema {
	d := encoding.NewDictionary()
	for _, z := range []string{"Europe/Berlin", "America/New_York"} {
		if _, err := d.Add(z); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "at", Type: encoding.FieldTypeDateTime},
		{Name: "tz", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
		{Name: "n", Type: encoding.FieldTypeU8, Nullable: true},
	}, ZoneField: "tz"}
}

// zoneCohortFile writes three rows under s through WritePreamble.
func zoneCohortFile(t *testing.T, s *encoding.Schema) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := WritePreamble(&b, s); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		at   int64
		zone uint64
		n    uint64
		null bool
	}{{1_700_000_000, 0, 3, false}, {1_700_003_600, 1, 0, true}, {1_700_007_200, 0, 9, false}} {
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeDateTime, uint64(row.at)))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeCategoricalU8, row.zone))
		must(encoding.WriteFieldValue(&b, encoding.FieldTypeU8, row.n))
		bm := make([]byte, s.BitmapByteSize())
		if row.null {
			encoding.BitmapSetNull(bm, 2)
		}
		must(encoding.WriteBitmap(&b, bm))
	}
	return b.Bytes()
}

// TestZoneCohort_RoundTrip: a cohort declaring a default zone field is
// written at 0x02, reads back with the default, decodes exactly like
// its undeclared (0x01) twin, and the layout-only read consumes the
// section — same schema end, same stride, same record count.
func TestZoneCohort_RoundTrip(t *testing.T) {
	t.Parallel()

	s := zoneCohortSchema()
	data := zoneCohortFile(t, s)
	if data[encoding.HeaderSize-1] != encoding.FormatVersionV2 {
		t.Fatalf("version = 0x%02x, want 0x02", data[encoding.HeaderSize-1])
	}
	r := bytes.NewReader(data)
	got, v, err := ReadPreamble(r)
	if err != nil {
		t.Fatalf("ReadPreamble: %v", err)
	}
	if v != encoding.FormatVersionV2 || got.ZoneField != "tz" {
		t.Fatalf("version/ZoneField = 0x%02x/%q", v, got.ZoneField)
	}
	recStart := len(data) - r.Len()

	flat := zoneCohortSchema()
	flat.ZoneField = ""
	twin := zoneCohortFile(t, flat)
	if twin[encoding.HeaderSize-1] != encoding.FormatVersionV1 {
		t.Fatalf("undeclared twin version = 0x%02x, want 0x01", twin[encoding.HeaderSize-1])
	}
	_, rows := decodeAll(t, data)
	_, twinRows := decodeAll(t, twin)
	if len(rows) != 3 || !reflect.DeepEqual(rows, twinRows) {
		t.Fatalf("declared rows %v differ from undeclared twin %v", rows, twinRows)
	}

	// Layout-only read: the section is consumed, the reader lands on
	// the first record byte, the default is still named.
	lr := bytes.NewReader(data[encoding.HeaderSize:])
	lay, err := ReadSchemaLayout(lr, v)
	if err != nil {
		t.Fatalf("ReadSchemaLayout: %v", err)
	}
	if lay.ZoneField != "tz" || lay.Fields[1].Dictionary != nil {
		t.Fatalf("layout: ZoneField=%q dictionary=%v", lay.ZoneField, lay.Fields[1].Dictionary)
	}
	if end := len(data) - lr.Len(); end != recStart {
		t.Fatalf("layout read ended at %d, full read at %d", end, recStart)
	}
	if lay.RecordByteSize() != got.RecordByteSize() {
		t.Fatalf("layout stride %d != full stride %d", lay.RecordByteSize(), got.RecordByteSize())
	}

	// The decoded schema re-writes byte-identically.
	var again bytes.Buffer
	if err := WritePreamble(&again, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again.Bytes(), data[:recStart]) {
		t.Fatal("decoded zone schema does not re-write byte-identically")
	}
}

// TestZoneCohort_OlderBinaryRefuses: ZONE is REQUIRED, so a binary that
// does not know tag 3 refuses the cohort rather than read it without
// its default. Re-tagging the section to a tag this binary does not
// know drives the exact path such a binary takes with the real tag 3.
// The same bytes flagged optional are skipped (the file then reads with
// no default) — which is why the writer never clears the flag.
func TestZoneCohort_OlderBinaryRefuses(t *testing.T) {
	t.Parallel()

	s := zoneCohortSchema()
	data := zoneCohortFile(t, s)
	pre, secs, recs := splitExtension(t, data, s)
	if len(secs) != 1 || secs[0].tag != 3 || secs[0].flags != 1 {
		t.Fatalf("sections = %+v, want one REQUIRED tag-3 section", secs)
	}
	retag := []extSection{{tag: 0x0300, flags: 1, body: secs[0].body}}
	_, _, err := ReadPreamble(bytes.NewReader(joinExtension(pre, retag, recs)))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.ENCODING_INVALID {
		t.Fatalf("err = %v, want ENCODING_INVALID", err)
	}
	if !strings.Contains(ce.Message, "required section") || ce.Details["tag"] != uint16(0x0300) {
		t.Fatalf("re-tagged REQUIRED: %v %v", ce.Message, ce.Details)
	}
	retag[0].flags = 0
	got, _, err := ReadPreamble(bytes.NewReader(joinExtension(pre, retag, recs)))
	if err != nil || got.ZoneField != "" {
		t.Fatalf("re-tagged optional: ZoneField=%v err=%v", got, err)
	}
}

// Pinned digests of the checked-in flat (0x01) and grouped (0x02)
// cohorts, taken BEFORE the ZONE section existed.
const (
	formatV1GoldenSHA256        = "91070de600a032a78882509cf9212e890a321f04c4b8fd584f05b279562a8690"
	formatV2GroupedGoldenSHA256 = "79fa4d0830f33bb95f7adc06dfd2ce39e165bc7089583839cf82911d5a8cf11b"
)

// TestZoneSection_UndeclaredByteIdentical: a cohort that declares no
// default zone is byte-identical to one written before the ZONE section
// existed — the checked-in flat and grouped cohorts still match their
// pre-ZONE digests, read back with no default, and the current writer
// re-emits their preambles byte-for-byte (the flat one still at 0x01).
func TestZoneSection_UndeclaredByteIdentical(t *testing.T) {
	t.Parallel()

	for path, digest := range map[string]string{
		formatV1GoldenPath:        formatV1GoldenSHA256,
		formatV2GroupedGoldenPath: formatV2GroupedGoldenSHA256,
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != digest {
			t.Fatalf("%s digest = %s, want %s (a compatibility break)", path, got, digest)
		}
		r := bytes.NewReader(data)
		s, v, err := ReadPreamble(r)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if s.HasZoneField() || s.RequiredFormatVersion() != v {
			t.Fatalf("%s: ZoneField=%q required=0x%02x header=0x%02x", path, s.ZoneField, s.RequiredFormatVersion(), v)
		}
		var again bytes.Buffer
		if err := WritePreamble(&again, s); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.Bytes(), data[:len(data)-r.Len()]) {
			t.Fatalf("%s: the current writer no longer re-emits the undeclared preamble", path)
		}
	}
}
