package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
)

// headDict builds an n-entry dictionary for the head-read tests.
func headDict(t *testing.T, n int) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for i := range n {
		if _, err := d.Add(fmt.Sprintf("entry-%07d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// headFile writes s's preamble at the version its content requires,
// then records whole records of zero bytes. It returns the file bytes
// and the preamble length (where the payload starts).
func headFile(t *testing.T, s *encoding.Schema, records int) ([]byte, int64) {
	t.Helper()
	var buf bytes.Buffer
	if err := encx.WritePreamble(&buf, s); err != nil {
		t.Fatalf("WritePreamble: %v", err)
	}
	head := int64(buf.Len())
	buf.Write(make([]byte, records*s.RecordByteSize()))
	return buf.Bytes(), head
}

// TestOpen_PayloadOffsetFromBufferedHead (archive-storage E4-S2): Open
// decodes a single file's header + schema through headReader, so the
// handle's position is up to one chunk past the schema; the recorded
// payloadOffset must still be the exact preamble length, for 0x01 and
// 0x02 files, with and without a null bitmap and inline dictionaries
// (small ones inside the first chunk, a large one spanning many), and
// openedRecordCount must agree with CountRecords on it.
//
// Falsified by recording the handle position (f.Seek) instead of
// headReader.consumed: every case then reports an offset past the
// preamble.
func TestOpen_PayloadOffsetFromBufferedHead(t *testing.T) {
	t.Parallel()

	vecSchema := func(nullable bool, dict *encoding.Dictionary) *encoding.Schema {
		s := &encoding.Schema{Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, Nullable: nullable},
			{Name: "v", Type: encoding.FieldTypeVecF32, ByteOffset: 8},
		}}
		if dict != nil {
			s.Fields = append(s.Fields, encoding.Field{Name: "c", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 20, Dictionary: dict})
		}
		s.Vectors = []encoding.VectorField{{FieldIndex: 1, Dim: 3}}
		return s
	}
	cases := []struct {
		name    string
		schema  *encoding.Schema
		version byte
	}{
		{"v1 plain", &encoding.Schema{Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeU32, ByteOffset: 0},
			{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 4},
		}}, 0x01},
		{"v1 bitmap", &encoding.Schema{Fields: []encoding.Field{
			{Name: "a", Type: encoding.FieldTypeU32, ByteOffset: 0, Nullable: true},
			{Name: "b", Type: encoding.FieldTypeF64, ByteOffset: 4},
		}}, 0x01},
		{"v1 dictionaries", &encoding.Schema{Fields: []encoding.Field{
			{Name: "c", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: headDict(t, 5)},
			{Name: "d", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 1, Dictionary: headDict(t, 3)},
		}}, 0x01},
		{"v1 bitmap large dictionary", &encoding.Schema{Fields: []encoding.Field{
			{Name: "c", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 0, Dictionary: headDict(t, 50_000), Nullable: true},
			{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 4},
		}}, 0x01},
		{"v2 plain", vecSchema(false, nil), 0x02},
		{"v2 bitmap", vecSchema(true, nil), 0x02},
		{"v2 dictionaries", vecSchema(false, headDict(t, 7)), 0x02},
		{"v2 bitmap large dictionary", vecSchema(true, headDict(t, 50_000)), 0x02},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const records = 37
			data, head := headFile(t, tc.schema, records)
			if data[encoding.HeaderSize-1] != tc.version {
				t.Fatalf("fixture wrote version 0x%02x, want 0x%02x", data[encoding.HeaderSize-1], tc.version)
			}
			if got, want := tc.schema.HasBitmap(), strings.Contains(tc.name, "bitmap"); got != want {
				t.Fatalf("fixture HasBitmap = %v, want %v", got, want)
			}
			cfg := fs.NewMemMap()
			if err := afero.WriteFile(cfg.Fs(), "c.pulse", data, 0o644); err != nil {
				t.Fatal(err)
			}
			svc := New(cfg)
			cohort, err := svc.Open(ctx, "c.pulse")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if cohort.payloadOffset != head {
				t.Fatalf("payloadOffset = %d, want the preamble length %d (file %d bytes)", cohort.payloadOffset, head, len(data))
			}
			if len(cohort.Schema().Fields) != len(tc.schema.Fields) {
				t.Fatalf("decoded %d fields, want %d", len(cohort.Schema().Fields), len(tc.schema.Fields))
			}
			got, err := svc.openedRecordCount(ctx, cohort, "c.pulse")
			if err != nil {
				t.Fatal(err)
			}
			want, err := svc.CountRecords(ctx, "c.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if got != want || got != records {
				t.Fatalf("openedRecordCount %d, CountRecords %d, want %d", got, want, records)
			}
		})
	}
}

// readCallFs counts Read / ReadAt calls on every handle it opens.
type readCallFs struct {
	afero.Fs
	mu    sync.Mutex
	calls int
	bytes int64
}

func (c *readCallFs) Open(name string) (afero.File, error) {
	f, err := c.Fs.Open(name)
	if err != nil {
		return nil, err
	}
	return &readCallFile{File: f, fs: c}, nil
}

func (c *readCallFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := c.Fs.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &readCallFile{File: f, fs: c}, nil
}

func (c *readCallFs) add(n int) {
	c.mu.Lock()
	c.calls++
	c.bytes += int64(n)
	c.mu.Unlock()
}

func (c *readCallFs) snapshot() (int, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls, c.bytes
}

type readCallFile struct {
	afero.File
	fs *readCallFs
}

func (f *readCallFile) Read(p []byte) (int, error) {
	n, err := f.File.Read(p)
	f.fs.add(n)
	return n, err
}

func (f *readCallFile) ReadAt(p []byte, off int64) (int, error) {
	n, err := f.File.ReadAt(p, off)
	f.fs.add(n)
	return n, err
}

// TestOpen_SchemaReadsCollapse (archive-storage E4-S2, TODO #496): a
// single file with a 600k-entry inline dictionary used to cost Open
// two reads per entry off the handle (~1.2M reads, ~0.45 s). Through
// headReader the reads are a bounded handful, and the over-read past
// the schema end stays within one chunk (headReaderMaxChunk). Counts
// reads, not wall time.
//
// Falsified by handing ReadSchema the unbuffered MultiReader instead of
// headReader: the read count returns to ~2 per entry.
func TestOpen_SchemaReadsCollapse(t *testing.T) {
	t.Parallel()

	const entries = 600_000
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "c", Type: encoding.FieldTypeCategoricalU32, ByteOffset: 0, Dictionary: headDict(t, entries)},
		{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 4},
	}}
	data, head := headFile(t, s, 200_000) // a payload well past one max chunk
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "big.pulse", data, 0o644); err != nil {
		t.Fatal(err)
	}
	cfs := &readCallFs{Fs: mem}
	cfg, err := fs.New(fs.WithFs(cfs))
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := New(cfg).Open(context.Background(), "big.pulse")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if cohort.payloadOffset != head {
		t.Fatalf("payloadOffset = %d, want %d", cohort.payloadOffset, head)
	}
	calls, read := cfs.snapshot()
	// OpenExact (predict's loader) reads not one byte past the head.
	exact, err := New(cfg).OpenExact(context.Background(), "big.pulse")
	if err != nil {
		t.Fatalf("OpenExact: %v", err)
	}
	bothCalls, both := cfs.snapshot()
	if exact.payloadOffset != head || both-read != head {
		t.Fatalf("OpenExact: payloadOffset %d, read %d bytes; want exactly the %d-byte head", exact.payloadOffset, both-read, head)
	}
	// ...yet is buffered too (E5-S4): ReadSchema's bounded buffer, a few
	// hundred reads, never two per entry.
	if exactCalls := bothCalls - calls; exactCalls > 512 {
		t.Fatalf("OpenExact made %d reads for a %d-entry dictionary; want O(log entries)", exactCalls, entries)
	}
	// head is ~9 MB: ~14 doubling fills to the 1 MiB cap, then one
	// per MiB. 64 leaves room for short reads, not for one per entry.
	if calls > 64 {
		t.Fatalf("Open made %d reads for a %d-entry dictionary (%d-byte head); want a bounded handful", calls, entries, head)
	}
	if over := read - head; over < 0 || over > headReaderMaxChunk {
		t.Fatalf("Open read %d bytes for a %d-byte head: over-read %d, want within one chunk (%d)", read, head, over, headReaderMaxChunk)
	}
}

// TestHeadReader_BoundsOverRead pins headReader's chunk rule directly:
// the first fill is headReaderMinChunk, later fills track the bytes
// consumed, a read at least a chunk wide passes straight through, and
// a short source surfaces io.EOF.
func TestHeadReader_BoundsOverRead(t *testing.T) {
	t.Parallel()

	// The replayed prefix is served first and counted as consumed.
	pre := newHeadReader(bytes.NewReader([]byte("CDEF")), []byte("AB"), false)
	if got, err := io.ReadAll(pre); err != nil || string(got) != "ABCDEF" || pre.consumed != 6 {
		t.Fatalf("prefix replay: %q, %v, consumed %d", got, err, pre.consumed)
	}
	// exact: every read past the prefix goes straight to the source, so
	// fetched == consumed at every step.
	exactSrc := &countingReader{r: bytes.NewReader(bytes.Repeat([]byte{1}, 1000))}
	ex := newHeadReader(exactSrc, nil, true)
	for range 10 {
		if _, err := io.ReadFull(ex, make([]byte, 3)); err != nil {
			t.Fatal(err)
		}
		if exactSrc.n != ex.consumed {
			t.Fatalf("exact mode fetched %d, consumed %d", exactSrc.n, ex.consumed)
		}
	}

	src := bytes.Repeat([]byte{7}, 10_000)
	under := &countingReader{r: bytes.NewReader(src)}
	h := newHeadReader(under, nil, false)
	one := make([]byte, 1)
	if _, err := io.ReadFull(h, one); err != nil {
		t.Fatal(err)
	}
	if under.n != headReaderMinChunk {
		t.Fatalf("first fill fetched %d bytes, want %d", under.n, headReaderMinChunk)
	}
	// Drain the first chunk byte by byte; the next fill is sized to the
	// 64 bytes consumed, then 128.
	for range headReaderMinChunk {
		if _, err := io.ReadFull(h, one); err != nil {
			t.Fatal(err)
		}
	}
	if under.n != 2*headReaderMinChunk || h.consumed != headReaderMinChunk+1 {
		t.Fatalf("fetched %d consumed %d, want %d / %d", under.n, h.consumed, 2*headReaderMinChunk, headReaderMinChunk+1)
	}
	// Exhaust the buffered chunk, then a wide read goes straight through.
	rest := make([]byte, headReaderMinChunk-1)
	if _, err := io.ReadFull(h, rest); err != nil {
		t.Fatal(err)
	}
	before := under.n
	wide := make([]byte, 4096)
	n, err := io.ReadFull(h, wide)
	if err != nil || n != len(wide) || under.n-before != int64(len(wide)) {
		t.Fatalf("wide read: n=%d err=%v fetched %d, want a %d-byte pass-through", n, err, under.n-before, len(wide))
	}
	if h.consumed != under.n {
		t.Fatalf("consumed %d != fetched %d after a pass-through", h.consumed, under.n)
	}
	if _, err := io.ReadAll(h); err != nil {
		t.Fatal(err)
	}
	if h.consumed != int64(len(src)) {
		t.Fatalf("consumed %d of %d", h.consumed, len(src))
	}
	if n, err := h.Read(one); n != 0 || err != io.EOF {
		t.Fatalf("read past end: %d, %v; want 0, EOF", n, err)
	}
}
