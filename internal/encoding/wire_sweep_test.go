package encoding

import (
	"bytes"
	"io"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// Byte-level sweeps over the sidecar wire formats this package owns: a
// reader handed ANY strict prefix of a valid file must refuse it with a
// coded error (never accept a shorter document, never panic), and a
// writer whose sink fails at ANY byte must report the failure (never
// claim success over a short file). Spot cuts miss the per-field read
// arms; the sweep pins every one.

// budgetWriter accepts n bytes, then fails.
type budgetWriter struct{ n int }

func (w *budgetWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		k := w.n
		w.n = 0
		return k, io.ErrShortWrite
	}
	w.n -= len(p)
	return len(p), nil
}

// everyWriteFailureSurfaces runs write against a sink failing after each
// byte budget below total and requires an ENCODING_IO error every time.
func everyWriteFailureSurfaces(t *testing.T, total int, write func(io.Writer) error) {
	t.Helper()
	for budget := 0; budget < total; budget++ {
		err := write(&budgetWriter{n: budget})
		if err == nil {
			t.Fatalf("sink failing after %d/%d bytes: write reported success", budget, total)
		}
		if !errors.HasCode(err, errors.ENCODING_IO) {
			t.Fatalf("sink failing after %d/%d bytes: err = %v, want ENCODING_IO", budget, total, err)
		}
	}
}

func sweepIndex() *Index {
	return &Index{
		Fingerprint:   sampleFingerprint(0x5A),
		Keys:          []IndexKeySpec{{Name: "id", Type: encoding.FieldTypeU32}, {Name: "region", Type: encoding.FieldTypeCategoricalU8}},
		SourceSize:    4096,
		SourceModTime: 1_700_000_000_000_000_000,
		Buckets: []IndexBucket{
			{Entries: []IndexEntry{{Key: []byte{1, 0, 0, 0, 2}, RowIDs: []uint64{3, 9}}}},
			{Entries: nil},
			{Entries: []IndexEntry{
				{Key: []byte{7, 0, 0, 0, 1}, RowIDs: []uint64{0}},
				{Key: []byte{8, 0, 0, 0, 1}, RowIDs: []uint64{1, 2, 4}},
			}},
		},
	}
}

func TestIndex_EveryTruncationRefused(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteIndex(&buf, sweepIndex()); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	meta, err := ReadIndexMeta(bytes.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	for cut := 0; cut < len(full); cut++ {
		if _, err := ReadIndex(bytes.NewReader(full[:cut])); !errors.HasCode(err, errors.ENCODING_INVALID) {
			t.Fatalf("ReadIndex cut at %d/%d: err = %v, want ENCODING_INVALID", cut, len(full), err)
		}
		m, err := ReadIndexMeta(bytes.NewReader(full[:cut]))
		if int64(cut) < meta.OffsetTableStart {
			if !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Fatalf("ReadIndexMeta cut at %d (< %d): err = %v, want ENCODING_INVALID", cut, meta.OffsetTableStart, err)
			}
			continue
		}
		// The meta prefix ends at the bucket count: past it, the
		// bucket table is never read, so a cut there still yields the
		// full meta.
		if err != nil || !reflect.DeepEqual(m, meta) {
			t.Fatalf("ReadIndexMeta cut at %d (>= %d) = (%+v, %v), want %+v", cut, meta.OffsetTableStart, m, err, meta)
		}
	}
}

func TestWriteIndex_EveryWriteFailureSurfaces(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := WriteIndex(&buf, sweepIndex()); err != nil {
		t.Fatal(err)
	}
	everyWriteFailureSurfaces(t, buf.Len(), func(w io.Writer) error { return WriteIndex(w, sweepIndex()) })
}

func TestWriteIndexFile_CreateFailureIsCoded(t *testing.T) {
	t.Parallel()

	ro := afero.NewReadOnlyFs(afero.NewMemMapFs())
	if err := WriteIndexFile(ro, "c.pulse.idx", sweepIndex()); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("read-only fs: err = %v, want ENCODING_IO", err)
	}
}

// schemaDocBytes is a grouped-free canonical schema doc and the length
// of its header + schema block (where the SHRD trailer starts).
func schemaDocBytes(t *testing.T) (full []byte, trailerAt int) {
	t.Helper()
	d := encoding.NewDictionary()
	d.Add("a")
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "n", Type: encoding.FieldTypeU16, Nullable: true},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: d},
	}}
	var pre bytes.Buffer
	if err := WritePreamble(&pre, s); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := WriteSchemaDoc(&b, s, 12345, 7); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), pre.Len()
}

// TestReadSchemaDoc_EveryTruncation: a cut inside the header or schema
// is refused; a cut inside (or right before) the 4-byte SHRD marker
// reads as a doc with no sharding metadata (the trailer is optional);
// a cut after the marker, inside the counts, is refused — a marker
// promises the counts.
func TestReadSchemaDoc_EveryTruncation(t *testing.T) {
	t.Parallel()

	full, at := schemaDocBytes(t)
	doc, err := ReadSchemaDoc(bytes.NewReader(full))
	if err != nil || doc.AggregateRecordCount != 12345 || doc.ShardCount != 7 {
		t.Fatalf("full doc = (%+v, %v), want agg 12345 / 7 shards", doc, err)
	}
	for cut := 0; cut < len(full); cut++ {
		doc, err := ReadSchemaDoc(bytes.NewReader(full[:cut]))
		switch {
		case cut < at:
			if err == nil {
				t.Fatalf("cut at %d inside the schema block accepted", cut)
			}
		case cut < at+len(SchemaDocMagic):
			if err != nil || doc.AggregateRecordCount != 0 || doc.ShardCount != 0 || len(doc.Schema.Fields) != 2 {
				t.Fatalf("cut at %d inside the marker = (%+v, %v), want a metadata-free doc", cut, doc, err)
			}
		default:
			if !errors.HasCode(err, errors.ENCODING_INVALID) {
				t.Fatalf("cut at %d inside the counts: err = %v, want ENCODING_INVALID", cut, err)
			}
		}
	}
	// A foreign 4-byte tail is not a marker: metadata-free, not refused.
	foreign := append(append([]byte(nil), full[:at]...), 'X', 'X', 'X', 'X', 1, 2)
	if doc, err := ReadSchemaDoc(bytes.NewReader(foreign)); err != nil || doc.ShardCount != 0 {
		t.Errorf("foreign tail = (%+v, %v), want a metadata-free doc", doc, err)
	}
}

func TestWriteSchemaDoc_EveryWriteFailureSurfaces(t *testing.T) {
	t.Parallel()

	full, _ := schemaDocBytes(t)
	doc, err := ReadSchemaDoc(bytes.NewReader(full))
	if err != nil {
		t.Fatal(err)
	}
	everyWriteFailureSurfaces(t, len(full), func(w io.Writer) error { return WriteSchemaDoc(w, doc.Schema, 1, 1) })
}

// TestReadPreamble_EveryTruncationRefused covers both halves of the
// one-call preamble read, at a 0x01 and a 0x02 schema.
func TestReadPreamble_EveryTruncationRefused(t *testing.T) {
	t.Parallel()

	v1 := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeF64}}}
	v2 := &encoding.Schema{
		Fields:  []encoding.Field{{Name: "v", Type: encoding.FieldTypeVecF32}},
		Vectors: []encoding.VectorField{{FieldIndex: 0, Dim: 3}},
	}
	for _, s := range []*encoding.Schema{v1, v2} {
		var b bytes.Buffer
		if err := WritePreamble(&b, s); err != nil {
			t.Fatal(err)
		}
		full := b.Bytes()
		got, v, err := ReadPreamble(bytes.NewReader(full))
		if err != nil || v != s.RequiredFormatVersion() || len(got.Fields) != 1 {
			t.Fatalf("full preamble = (%v, %#x, %v)", got, v, err)
		}
		for cut := 0; cut < len(full); cut++ {
			if _, _, err := ReadPreamble(bytes.NewReader(full[:cut])); err == nil {
				t.Fatalf("version %#x: preamble cut at %d/%d accepted", v, cut, len(full))
			}
		}
		everyWriteFailureSurfaces(t, len(full), func(w io.Writer) error { return WritePreamble(w, s) })
	}
	// A version older than the content requires is refused before a byte.
	if err := writePreambleVersion(&budgetWriter{}, v2, encoding.FormatVersionV1); !errors.HasCode(err, errors.ENCODING_INVALID) {
		t.Errorf("v2 schema at 0x01: err = %v, want ENCODING_INVALID", err)
	}
}

func TestIndexManifest_KeyHashAndShardEntries(t *testing.T) {
	t.Parallel()

	path := SidecarIndexPath("c.pulse", []string{"id", "region"})
	e := IndexManifestEntryFor(path, sweepIndex())
	if e.KeyHash == "" || e.KeyHash != path[len("c.pulse."):len(path)-len(".idx")] {
		t.Errorf("KeyHash = %q from %q", e.KeyHash, path)
	}
	if e.DistinctKeys != 3 || e.IndexedRecords != 6 {
		t.Errorf("counts = %d keys / %d records, want 3 / 6", e.DistinctKeys, e.IndexedRecords)
	}
	for _, base := range []string{"c.pulse.idx.bak", "nodotidx.idx"} {
		if h := indexKeyHashFromName(base); h != "" {
			t.Errorf("indexKeyHashFromName(%q) = %q, want \"\"", base, h)
		}
	}

	fsys := afero.NewMemMapFs()
	m := NewIndexManifest("c.pulse")
	bad := e
	bad.Shards = []IndexManifestShard{{Name: "s0.pulse"}} // no index_path
	m.Upsert(bad)
	if err := WriteIndexManifest(fsys, "c.pulse", m); err != nil {
		t.Fatal(err)
	}
	if _, present, err := ReadIndexManifest(fsys, "c.pulse"); !present || !errors.HasCode(err, errors.PULSE_INDEX_MANIFEST_INVALID) {
		t.Errorf("shard entry with no index_path: (present %v, %v), want PULSE_INDEX_MANIFEST_INVALID", present, err)
	}
	if err := WriteIndexManifest(afero.NewReadOnlyFs(fsys), "c.pulse", m); !errors.HasCode(err, errors.SERVICE_RESOURCE) {
		t.Errorf("read-only fs: err = %v, want SERVICE_RESOURCE", err)
	}
}
