package encoding_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// tagRow is one record of a tagged cohort: an id and a set_u16 mask.
type tagRow struct {
	id   uint32
	mask uint16
}

// tagSchema is the id (u32) + tags (set_* rung ft, dictionary labels)
// schema; nullable marks tags nullable.
func tagSchema(t *testing.T, ft encoding.FieldType, labels []string, nullable bool) *encoding.Schema {
	t.Helper()
	d := encoding.NewDictionary()
	for _, l := range labels {
		if _, err := d.Add(l); err != nil {
			t.Fatalf("dict add %q: %v", l, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "tags", Type: ft, ByteOffset: 4, CsvColumnIdx: 1, Dictionary: d, Nullable: nullable},
	}}
}

// tagCohort writes a single-file cohort of schema s (an id + set_u16
// layout) holding rows, then tail stray bytes (a truncated record).
func tagCohort(t *testing.T, s *encoding.Schema, rows []tagRow, tail int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, s); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for _, r := range rows {
		buf.Write(tagRecord(r))
	}
	buf.Write(bytes.Repeat([]byte{0xEE}, tail))
	return buf.Bytes()
}

func tagRecord(r tagRow) []byte {
	var rec [6]byte
	binary.LittleEndian.PutUint32(rec[:4], r.id)
	binary.LittleEndian.PutUint16(rec[4:], r.mask)
	return rec[:]
}

// zipEntry is one archive entry; deflate stores it compressed.
type zipEntry struct {
	name    string
	payload []byte
	deflate bool
}

func zipArchive(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		m := zip.Store
		if e.deflate {
			m = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: m})
		if err != nil {
			t.Fatalf("CreateHeader(%q): %v", e.name, err)
		}
		if _, err := w.Write(e.payload); err != nil {
			t.Fatalf("write(%q): %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

// TestOpenArchiveRecords_SequentialPass: the pass yields every shard's
// WHOLE records back to back in archive order under the canonical
// (widened) schema — a shard whose set dictionary is a strict prefix of
// the canonical one decodes unchanged, and a truncated shard tail is
// dropped there rather than shifting the next shard off its stride.
func TestOpenArchiveRecords_SequentialPass(t *testing.T) {
	t.Parallel()

	canonical := tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b", "c"}, false)
	first := []tagRow{{1, 0b001}, {2, 0b011}}
	second := []tagRow{{3, 0b100}, {4, 0b111}, {5, 0}}
	data := zipArchive(t,
		zipEntry{name: encx.ReservedSchemaName, payload: tagCohort(t, canonical, nil, 0)},
		zipEntry{name: "part-00001.pulse", payload: tagCohort(t, tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b"}, false), first, 4)},
		zipEntry{name: "part-00002.pulse", payload: tagCohort(t, canonical, second, 0)},
	)
	ar, err := encx.OpenArchiveRecords(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchiveRecords: %v", err)
	}
	if ar.Records != 5 {
		t.Errorf("Records = %d, want 5 (the truncated tail dropped)", ar.Records)
	}
	if len(ar.Shards) != 2 || ar.Shards[0] != "part-00001.pulse" || ar.Shards[1] != "part-00002.pulse" {
		t.Errorf("Shards = %v", ar.Shards)
	}
	if ar.Schema.Fields[1].Type != encoding.FieldTypeSetU16 || ar.Schema.Fields[1].Dictionary.Count() != 3 {
		t.Errorf("Schema = %+v, want the canonical set_u16 with three labels", ar.Schema.Fields[1])
	}
	got, err := io.ReadAll(ar.Reader)
	if err != nil {
		t.Fatalf("read records: %v", err)
	}
	var want []byte
	for _, r := range append(append([]tagRow(nil), first...), second...) {
		want = append(want, tagRecord(r)...)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("records\n got %x\nwant %x", got, want)
	}
}

// TestOpenArchiveRecords_Refusals: a shard the canonical schema cannot
// decode is refused with the verify code and details.entry naming it; a
// deflated shard is PULSE_ARCHIVE_CORRUPT (entry named); an unreadable
// shard preamble is PULSE_SHARD_HEADER_INVALID; the canonical entry's
// absence is PULSE_SHARD_MISSING and its corruption ENCODING_INVALID;
// a non-archive is refused before any entry is read.
func TestOpenArchiveRecords_Refusals(t *testing.T) {
	t.Parallel()

	canonical := tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b", "c"}, false)
	head := zipEntry{name: encx.ReservedSchemaName, payload: tagCohort(t, canonical, nil, 0)}
	good := zipEntry{name: "part-00001.pulse", payload: tagCohort(t, canonical, []tagRow{{1, 1}}, 0)}
	shard := func(s *encoding.Schema) []byte { return tagCohort(t, s, []tagRow{{2, 1}}, 0) }
	for _, tc := range []struct {
		name    string
		data    []byte
		code    errors.Code
		entry   string
		details map[string]any
	}{
		{"dictionary longer than canonical", zipArchive(t, head, good,
			zipEntry{name: "part-00002.pulse", payload: shard(tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b", "c", "d"}, false))}),
			errors.PULSE_SHARD_DICT_DIVERGENCE, "part-00002.pulse", nil},
		{"dictionary diverging from canonical", zipArchive(t, head,
			zipEntry{name: "part-00001.pulse", payload: shard(tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "z"}, false))}),
			errors.PULSE_SHARD_DICT_DIVERGENCE, "part-00001.pulse", nil},
		{"set rung differs", zipArchive(t, head, good,
			zipEntry{name: "part-00002.pulse", payload: shard(tagSchema(t, encoding.FieldTypeSetU8, []string{"a"}, false))}),
			errors.PULSE_SHARD_SCHEMA_MISMATCH, "part-00002.pulse", nil},
		{"nullable shard under a non-nullable canonical", zipArchive(t, head,
			zipEntry{name: "part-00001.pulse", payload: shard(tagSchema(t, encoding.FieldTypeSetU16, []string{"a"}, true))}),
			errors.PULSE_SHARD_SCHEMA_MISMATCH, "part-00001.pulse", map[string]any{"dimension": "nullable"}},
		{"deflated shard", zipArchive(t, head, good, zipEntry{name: "part-00002.pulse", payload: shard(canonical), deflate: true}),
			errors.PULSE_ARCHIVE_CORRUPT, "part-00002.pulse", nil},
		{"shard preamble unreadable", zipArchive(t, head, zipEntry{name: "part-00001.pulse", payload: []byte("PULSE\x00\x00\x00\x09garbage")}),
			errors.PULSE_SHARD_HEADER_INVALID, "", nil},
		{"shard schema block unreadable", zipArchive(t, head, zipEntry{name: "part-00001.pulse", payload: []byte("PULSE\x00\x00\x00\x01\xff\xff")}),
			errors.PULSE_SHARD_HEADER_INVALID, "", nil},
		{"canonical entry missing", zipArchive(t, good),
			errors.PULSE_SHARD_MISSING, encx.ReservedSchemaName, nil},
		{"canonical entry corrupt", zipArchive(t, zipEntry{name: encx.ReservedSchemaName, payload: []byte("not a schema")}, good),
			errors.ENCODING_INVALID, "", nil},
		{"not an archive", tagCohort(t, canonical, nil, 0), errors.PULSE_ARCHIVE_MAGIC_INVALID, "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ar, err := encx.OpenArchiveRecords(bytes.NewReader(tc.data), int64(len(tc.data)))
			if err == nil {
				t.Fatalf("opened %+v, want %s", ar, tc.code)
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("error %v is not coded", err)
			}
			if ce.Code != tc.code {
				t.Fatalf("code %s (%v), want %s", ce.Code, err, tc.code)
			}
			if tc.entry != "" && ce.Details["entry"] != tc.entry {
				t.Errorf("details.entry = %v, want %q (details %v)", ce.Details["entry"], tc.entry, ce.Details)
			}
			for k, want := range tc.details {
				if ce.Details[k] != want {
					t.Errorf("details[%q] = %v, want %v", k, ce.Details[k], want)
				}
			}
		})
	}
}

// TestShardDecodeCompatible: the one decode rule every whole-archive
// read applies — an equal or prefix dictionary passes; a longer one is
// PULSE_SHARD_DICT_DIVERGENCE; a structural difference is refused
// before the dictionaries are compared.
func TestShardDecodeCompatible(t *testing.T) {
	t.Parallel()

	canonical := tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b"}, false)
	for _, tc := range []struct {
		name string
		own  *encoding.Schema
		code errors.Code
	}{
		{"identical", tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b"}, false), ""},
		{"prefix", tagSchema(t, encoding.FieldTypeSetU16, []string{"a"}, false), ""},
		{"empty dictionary", tagSchema(t, encoding.FieldTypeSetU16, nil, false), ""},
		{"longer", tagSchema(t, encoding.FieldTypeSetU16, []string{"a", "b", "c"}, false), errors.PULSE_SHARD_DICT_DIVERGENCE},
		{"rung", tagSchema(t, encoding.FieldTypeSetU32, []string{"a"}, false), errors.PULSE_SHARD_SCHEMA_MISMATCH},
	} {
		err := encx.ShardDecodeCompatible(canonical, tc.own)
		var ce *errors.CodedError
		switch {
		case tc.code == "" && err != nil:
			t.Errorf("%s: %v, want compatible", tc.name, err)
		case tc.code != "" && (!stderrors.As(err, &ce) || ce.Code != tc.code):
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}
}
