package encoding

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

// fenceReader serves src through Read only (no ReadByte, so readSchema
// buffers it as it would a file handle), counts the calls and the bytes
// fetched, and fails the test the moment a read reaches offset fence or
// beyond — the first record byte.
type fenceReader struct {
	t       *testing.T
	src     []byte
	pos     int
	fence   int
	calls   int
	crossed bool
}

func (f *fenceReader) Read(p []byte) (int, error) {
	f.calls++
	if f.pos >= len(f.src) {
		return 0, io.EOF
	}
	n := copy(p, f.src[f.pos:])
	if f.pos+n > f.fence {
		f.crossed = true
	}
	f.pos += n
	return n, nil
}

// fencedPreamble writes s's header + schema block at the version its
// content requires (or the given block for a hand-made one), then a
// payload of sentinel bytes, and returns the bytes and the preamble
// length.
func fencedPreamble(t *testing.T, s *Schema, block []byte) ([]byte, int) {
	t.Helper()
	var b bytes.Buffer
	v := s.RequiredFormatVersion()
	if block != nil {
		v = FormatVersionV2
	}
	if err := writeHeaderVersion(&b, v); err != nil {
		t.Fatal(err)
	}
	if block != nil {
		b.Write(block)
	} else if err := writeSchemaVersion(&b, s, v); err != nil {
		t.Fatal(err)
	}
	head := b.Len()
	b.Write(bytes.Repeat([]byte{0xA5}, 1<<16))
	return b.Bytes(), head
}

func fenceDict(t *testing.T, n int) *Dictionary {
	t.Helper()
	d := NewDictionary()
	for i := range n {
		if _, err := d.Add(fmt.Sprintf("entry-%07d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// TestReadSchema_NeverReadsPastSchemaEnd (archive-storage E5-S4, TODO
// #722): readSchema buffers its source, but only up to the bytes the
// decoded lengths so far prove belong to the schema block — so on every
// shape (0x01 / 0x02, bitmap, descriptions, decimals, sets, small and
// large dictionaries, vectors, parent groups, a skipped optional
// section) and both decodes (full and layout-only) not one byte at or
// past the first record byte is fetched, the source is left exactly at
// the payload, and the decode still matches the written schema.
//
// Falsified by dropping the bound in schemaReader.fill (always fetching
// a whole chunk): every case then reads into the sentinel payload.
func TestReadSchema_NeverReadsPastSchemaEnd(t *testing.T) {
	t.Parallel()

	grouped := &Schema{
		Fields: []Field{
			{Name: "a", Type: FieldTypeU8},
			{Name: "b", Type: FieldTypeU16, Nullable: true},
			{Name: "c", Type: FieldTypeCategoricalU8, Dictionary: fenceDict(t, 4)},
		},
		Groups: []Group{{
			Kind:    GroupKindIndexed,
			Members: []GroupMember{{Field: 0, Key: true}, {Field: 1}},
			Entries: []byte{7, 0x34, 0x12, 0, 9, 0, 0, 0b10},
		}},
	}
	unknown := &Schema{Fields: []Field{{Name: "x", Type: FieldTypeF64}}}
	cases := []struct {
		name   string
		schema *Schema
		block  []byte
	}{
		{"v1 plain", &Schema{Fields: []Field{{Name: "a", Type: FieldTypeU32}, {Name: "b", Type: FieldTypeF64, ByteOffset: 4}}}, nil},
		{"v1 bitmap descriptions", &Schema{Fields: []Field{
			{Name: "a", Type: FieldTypeU32, Nullable: true, Description: "a described field"},
			{Name: "b", Type: FieldTypeF64, ByteOffset: 4, Description: "another one"},
		}}, nil},
		{"v1 decimal and set", &Schema{Fields: []Field{
			{Name: "d", Type: FieldTypeDecimal128, Precision: 12, Scale: 2, Nullable: true},
			{Name: "s", Type: FieldTypeSetU8, ByteOffset: 16, Dictionary: fenceDict(t, 5)},
		}}, nil},
		{"v1 small dictionaries", &Schema{Fields: []Field{
			{Name: "c", Type: FieldTypeCategoricalU8, Dictionary: fenceDict(t, 3)},
			{Name: "e", Type: FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: NewDictionary()},
			{Name: "x", Type: FieldTypeF64, ByteOffset: 2},
		}}, nil},
		{"v1 large dictionary", &Schema{Fields: []Field{
			{Name: "c", Type: FieldTypeCategoricalU32, Nullable: true, Dictionary: fenceDict(t, 40_000)},
			{Name: "x", Type: FieldTypeF64, ByteOffset: 4},
		}}, nil},
		{"v2 vectors", vecSchema(), nil},
		{"v2 groups", grouped, nil},
		{"v2 skipped optional section", unknown, vecSchemaBlock(t, unknown, 0x7777, 0, []byte("opaque section body"))},
	}
	for _, tc := range cases {
		for _, layout := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/layout=%v", tc.name, layout), func(t *testing.T) {
				t.Parallel()
				data, head := fencedPreamble(t, tc.schema, tc.block)
				r := &fenceReader{t: t, src: data, fence: head}
				v, err := ReadHeader(r)
				if err != nil {
					t.Fatal(err)
				}
				got, err := readSchema(r, v, layout)
				if err != nil {
					t.Fatalf("readSchema: %v", err)
				}
				if r.crossed || r.pos != head {
					t.Fatalf("fetched %d bytes (crossed=%v); the schema ends at %d", r.pos, r.crossed, head)
				}
				if len(got.Fields) != len(tc.schema.Fields) || len(got.Groups) != len(tc.schema.Groups) || len(got.Vectors) != len(tc.schema.Vectors) {
					t.Fatalf("decoded %d fields / %d groups / %d vectors", len(got.Fields), len(got.Groups), len(got.Vectors))
				}
				for i, f := range got.Fields {
					want := tc.schema.Fields[i]
					if f.Name != want.Name || f.Description != want.Description || f.Scale != want.Scale {
						t.Fatalf("field %d decoded %+v, want %+v", i, f, want)
					}
					if !layout && want.Dictionary != nil && f.Dictionary.Count() != want.Dictionary.Count() {
						t.Fatalf("field %d dictionary %d entries, want %d", i, f.Dictionary.Count(), want.Dictionary.Count())
					}
				}
				// The source is left at the first record byte.
				var b [1]byte
				if _, err := io.ReadFull(r, b[:]); err != nil || b[0] != 0xA5 {
					t.Fatalf("next byte %x (%v), want the payload's first", b[0], err)
				}
			})
		}
	}
}

// TestReadSchema_HugeDictionaryReadCount (archive-storage E5-S4, TODO
// #722): a 600k-entry inline dictionary decoded straight off an
// unbuffered source cost two reads per entry (~1.2M). readSchema's
// bounded buffer makes it a few hundred at most — O(log entries) as the
// provable bound (2 bytes per entry still to come) shrinks over the
// dictionary's tail — while still stopping exactly at the payload.
//
// Falsified by returning r unwrapped from newSchemaReader: the count
// returns to ~2 per entry.
func TestReadSchema_HugeDictionaryReadCount(t *testing.T) {
	t.Parallel()

	const entries = 600_000
	s := &Schema{Fields: []Field{
		{Name: "c", Type: FieldTypeCategoricalU32, Dictionary: fenceDict(t, entries)},
		{Name: "x", Type: FieldTypeF64, ByteOffset: 4},
	}}
	data, head := fencedPreamble(t, s, nil)
	for _, layout := range []bool{false, true} {
		r := &fenceReader{t: t, src: data, fence: head}
		v, err := ReadHeader(r)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := readSchema(r, v, layout); err != nil {
			t.Fatal(err)
		}
		if r.crossed || r.pos != head {
			t.Fatalf("layout=%v: fetched %d bytes; the schema ends at %d", layout, r.pos, head)
		}
		// The layout read's fills are capped small (it must not allocate
		// for the dictionaries it skips): ~head/16 KiB more reads.
		if limit := 512 + int(head/schemaReaderLayoutChunk); r.calls > limit {
			t.Fatalf("layout=%v: %d reads for a %d-entry dictionary (%d-byte head); want O(log entries + head / fill cap)", layout, r.calls, entries, head)
		}
		t.Logf("layout=%v: %d reads, %d-byte head", layout, r.calls, head)
	}
}

// TestSchemaReader_ByteReaderPassThrough: a source that already serves
// cheap small reads (an in-memory io.ByteReader such as bytes.Reader or
// bufio.Reader) is decoded directly, never double-buffered.
func TestSchemaReader_ByteReaderPassThrough(t *testing.T) {
	t.Parallel()
	br := bytes.NewReader(nil)
	if got := newSchemaReader(br, schemaReaderMaxChunk); got != io.Reader(br) {
		t.Fatalf("newSchemaReader wrapped a %T", br)
	}
	fr := &fenceReader{}
	if _, ok := newSchemaReader(fr, schemaReaderMaxChunk).(*schemaReader); !ok {
		t.Fatalf("newSchemaReader did not buffer a plain reader")
	}
}
