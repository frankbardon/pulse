package encoding

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// archive_append_test.go pins the hand-written zip tail writer (U73
// #492): what it writes reads back through Go's archive/zip AND the
// Pulse archive reader, Zip64 is emitted for offsets, sizes and record
// counts past the 32/16-bit fields, and the tail parser refuses what it
// cannot extend.

// sparseReaderAt is a file of the given size that is zero everywhere
// except its segments: a way to place zip records past 4 GiB without
// writing 4 GiB.
type sparseReaderAt struct {
	size int64
	segs []sparseSeg
}

type sparseSeg struct {
	off int64
	b   []byte
}

func (s *sparseReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= s.size {
		return 0, io.EOF
	}
	n := len(p)
	if rem := s.size - off; int64(n) > rem {
		n = int(rem)
	}
	clear(p[:n])
	for _, sg := range s.segs {
		lo, hi := max(off, sg.off), min(off+int64(n), sg.off+int64(len(sg.b)))
		if lo < hi {
			copy(p[lo-off:hi-off], sg.b[lo-sg.off:hi-sg.off])
		}
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// goZipArchive writes entries with zip.Writer the way the rewrite path
// does (CreateHeader, store-only: data descriptors included).
func goZipArchive(t *testing.T, entries ...[2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e[0], Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, e[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// readAllEntries reads every entry through archive/zip (CRC-checked)
// and returns name -> payload plus the directory order.
func readAllEntries(t *testing.T, r io.ReaderAt, size int64) (map[string]string, []string) {
	t.Helper()
	zr, err := zip.NewReader(r, size)
	if err != nil {
		t.Fatalf("archive/zip: %v", err)
	}
	got := map[string]string{}
	var order []string
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		got[f.Name] = string(b)
		order = append(order, f.Name)
	}
	return got, order
}

// appendTail appends entries to archive and writes a directory of
// lead + the old records (minus drop) + the new non-lead records.
func appendTail(t *testing.T, archive []byte, drop string, entries ...[2]string) []byte {
	t.Helper()
	dir, err := ReadArchiveDirectory(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		t.Fatalf("ReadArchiveDirectory: %v", err)
	}
	var tail bytes.Buffer
	ap := NewArchiveAppender(&tail, dir.Size)
	var lead, rest []DirectoryRecord
	for _, e := range entries {
		rec, err := ap.WriteEntry(e[0], []byte(e[1]))
		if err != nil {
			t.Fatal(err)
		}
		if e[0] == drop {
			lead = append(lead, rec)
		} else {
			rest = append(rest, rec)
		}
	}
	recs := append([]DirectoryRecord(nil), lead...)
	for _, r := range dir.Records {
		if r.Name != drop {
			recs = append(recs, r)
		}
	}
	recs = append(recs, rest...)
	if _, err := ap.Finish(recs); err != nil {
		t.Fatal(err)
	}
	if ap.Written() != int64(tail.Len()) {
		t.Errorf("Written = %d, wrote %d", ap.Written(), tail.Len())
	}
	return append(append([]byte(nil), archive...), tail.Bytes()...)
}

// TestArchiveAppender_AppendsToAGoWrittenArchive: the tail append over
// a zip.Writer archive reads identically through archive/zip and
// OpenArchive — old entries where they were, the superseded
// `_schema.pulse` gone from the directory, the new one first, the new
// shard last — and the original bytes are a prefix of the result.
func TestArchiveAppender_AppendsToAGoWrittenArchive(t *testing.T) {
	t.Parallel()

	old := goZipArchive(t, [2]string{ReservedSchemaName, "schema-v1"}, [2]string{"a.pulse", "AAAA"}, [2]string{"b.pulse", "BBBBBB"})
	out := appendTail(t, old, ReservedSchemaName, [2]string{"c.pulse", "CCC"}, [2]string{ReservedSchemaName, "schema-v2"})

	if !bytes.HasPrefix(out, old) {
		t.Fatal("append rewrote bytes of the original archive")
	}
	got, order := readAllEntries(t, bytes.NewReader(out), int64(len(out)))
	want := map[string]string{ReservedSchemaName: "schema-v2", "a.pulse": "AAAA", "b.pulse": "BBBBBB", "c.pulse": "CCC"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if fmt.Sprint(order) != fmt.Sprint([]string{ReservedSchemaName, "a.pulse", "b.pulse", "c.pulse"}) {
		t.Errorf("directory order = %v", order)
	}

	a, err := OpenArchive(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if n := len(a.Entries()); n != 4 {
		t.Errorf("Entries = %d, want 4 (no superseded _schema.pulse)", n)
	}
	sect, err := a.OpenAt(ReservedSchemaName)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(&sect)
	if string(b) != "schema-v2" {
		t.Errorf("_schema.pulse = %q, want the appended one", b)
	}
	// The directory is all that follows the last entry's data.
	if a.DirectoryBytes() != int64(len(out))-int64(bytes.LastIndex(out, []byte("schema-v2"))+len("schema-v2")) {
		t.Errorf("DirectoryBytes = %d", a.DirectoryBytes())
	}

	// A second append stacks on the first.
	out2 := appendTail(t, out, ReservedSchemaName, [2]string{"d.pulse", "D"}, [2]string{ReservedSchemaName, "schema-v3"})
	got, _ = readAllEntries(t, bytes.NewReader(out2), int64(len(out2)))
	if got["d.pulse"] != "D" || got[ReservedSchemaName] != "schema-v3" || got["a.pulse"] != "AAAA" || len(got) != 5 {
		t.Errorf("second append entries = %v", got)
	}
}

// TestArchive_DuplicateSchemaEntryResolvesLastWins pins the reader's
// rule for a directory that DOES list `_schema.pulse` twice (an
// archive another tool appended to): Entries keeps both, every by-name
// read resolves to the later record.
func TestArchive_DuplicateSchemaEntryResolvesLastWins(t *testing.T) {
	t.Parallel()

	old := goZipArchive(t, [2]string{ReservedSchemaName, "schema-v1"}, [2]string{"a.pulse", "AAAA"})
	out := appendTail(t, old, "", [2]string{ReservedSchemaName, "schema-v2"})
	a, err := OpenArchive(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	var schemas int
	for _, e := range a.Entries() {
		if e.Name == ReservedSchemaName {
			schemas++
		}
	}
	if schemas != 2 {
		t.Fatalf("directory lists %d _schema.pulse records, want 2", schemas)
	}
	rc, err := a.Open(ReservedSchemaName)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "schema-v2" {
		t.Errorf("by-name read = %q, want the LAST record's payload", b)
	}
}

// TestArchiveAppender_Zip64Offsets: an append that starts past 4 GiB
// writes zip64 offsets in its records and a zip64 end record + locator;
// archive/zip reads every entry at its true offset, and the tail parser
// reads the result back — so a second append carries the old zip64
// records verbatim.
func TestArchiveAppender_Zip64Offsets(t *testing.T) {
	t.Parallel()

	const start = int64(5) << 30
	var tail bytes.Buffer
	ap := NewArchiveAppender(&tail, start)
	r1, err := ap.WriteEntry("a.pulse", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	r2, err := ap.WriteEntry(ReservedSchemaName, []byte("schema"))
	if err != nil {
		t.Fatal(err)
	}
	dirOff, err := ap.Finish([]DirectoryRecord{r2, r1})
	if err != nil {
		t.Fatal(err)
	}
	if dirOff <= start {
		t.Fatalf("directory offset %d not past the start", dirOff)
	}
	tb := tail.Bytes()
	// zip64 end record + locator present, EOCD sentinels set.
	if !bytes.Contains(tb, binary.LittleEndian.AppendUint32(nil, zip64EOCDSig)) ||
		!bytes.Contains(tb, binary.LittleEndian.AppendUint32(nil, zip64LocatorSig)) {
		t.Fatal("no zip64 end record / locator past 4 GiB")
	}
	eocd := tb[len(tb)-zipEOCDLen:]
	if binary.LittleEndian.Uint32(eocd[16:]) != zipUint32Max {
		t.Errorf("EOCD directory offset = %#x, want the zip64 sentinel", binary.LittleEndian.Uint32(eocd[16:]))
	}

	file := &sparseReaderAt{size: start + int64(len(tb)), segs: []sparseSeg{{start, tb}}}
	zr, err := zip.NewReader(file, file.size)
	if err != nil {
		t.Fatalf("archive/zip over a >4 GiB tail: %v", err)
	}
	for _, f := range zr.File {
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		if off < start {
			t.Errorf("%s data offset %d, want past %d (zip64 offset lost)", f.Name, off, start)
		}
	}
	got, order := readAllEntries(t, file, file.size)
	if got["a.pulse"] != "first" || got[ReservedSchemaName] != "schema" || order[0] != ReservedSchemaName {
		t.Errorf("entries = %v order %v", got, order)
	}

	// Parse and append again: the old zip64 records ride verbatim.
	dir, err := ReadArchiveDirectory(file, file.size)
	if err != nil {
		t.Fatalf("ReadArchiveDirectory over zip64: %v", err)
	}
	if dir.DirOffset != dirOff || len(dir.Records) != 2 {
		t.Fatalf("parsed %+v, want directory at %d with 2 records", dir, dirOff)
	}
	var tail2 bytes.Buffer
	ap2 := NewArchiveAppender(&tail2, file.size)
	r3, err := ap2.WriteEntry("b.pulse", []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ap2.Finish(append(dir.Records, r3)); err != nil {
		t.Fatal(err)
	}
	file2 := &sparseReaderAt{size: file.size + int64(tail2.Len()),
		segs: []sparseSeg{{start, tb}, {file.size, tail2.Bytes()}}}
	got, _ = readAllEntries(t, file2, file2.size)
	if got["a.pulse"] != "first" || got["b.pulse"] != "second" || len(got) != 3 {
		t.Errorf("second zip64 append entries = %v", got)
	}
}

// TestCentralRecord_Zip64SizesAndOffset: a record for an entry of
// 4 GiB or more at an offset past 4 GiB carries both in its zip64
// extra field, which archive/zip resolves to the true sizes and data
// offset. No 4 GiB payload is written: the local header sits in a
// sparse file and only the directory is read.
func TestCentralRecord_Zip64SizesAndOffset(t *testing.T) {
	t.Parallel()

	const (
		size   = uint64(5) << 30
		offset = int64(6) << 30
		name   = "big.pulse"
	)
	local := localFileHeader(name, zipFlagDataDescriptor)
	desc := dataDescriptor(0xdeadbeef, size)
	if len(desc) != zipDataDescriptor64Len || binary.LittleEndian.Uint64(desc[8:]) != size {
		t.Errorf("data descriptor of a >4 GiB entry = %x, want 64-bit sizes", desc)
	}
	rec := CentralRecord(name, zipFlagDataDescriptor, 0xdeadbeef, size, uint64(offset))
	dirStart := offset + int64(len(local)) + int64(size) + int64(len(desc))
	var tail bytes.Buffer
	ap := NewArchiveAppender(&tail, dirStart)
	if _, err := ap.Finish([]DirectoryRecord{rec}); err != nil {
		t.Fatal(err)
	}
	file := &sparseReaderAt{size: dirStart + int64(tail.Len()),
		segs: []sparseSeg{{offset, local}, {dirStart, tail.Bytes()}}}
	zr, err := zip.NewReader(file, file.size)
	if err != nil {
		t.Fatalf("archive/zip: %v", err)
	}
	f := zr.File[0]
	if f.UncompressedSize64 != size || f.CompressedSize64 != size || f.CRC32 != 0xdeadbeef {
		t.Errorf("sizes %d/%d crc %#x, want %d", f.UncompressedSize64, f.CompressedSize64, f.CRC32, size)
	}
	off, err := f.DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	if want := offset + int64(len(local)); off != want {
		t.Errorf("data offset = %d, want %d", off, want)
	}

	// Below the limits no zip64 extra is written at all.
	small := CentralRecord(name, 0, 1, 10, 20)
	if small.Len() != zipCentralHeaderLen+len(name) {
		t.Errorf("small record carries an extra field (%d bytes)", small.Len())
	}
	// A small entry past 4 GiB: sizes stay readable through the extra.
	far := CentralRecord(name, 0, 1, 10, 7<<30)
	if far.Len() != zipCentralHeaderLen+len(name)+28 {
		t.Errorf("record past 4 GiB lacks its zip64 extra (%d bytes)", far.Len())
	}
}

// TestArchiveAppender_MatchesZipWriterBytes: from offset 0 the
// appender writes the very bytes zip.Writer writes for the same
// store-only entries — names that need the UTF-8 flag included — so an
// appended entry is indistinguishable from a rewritten one.
func TestArchiveAppender_MatchesZipWriterBytes(t *testing.T) {
	t.Parallel()

	entries := [][2]string{
		{ReservedSchemaName, "schema"},
		{"a.pulse", "AAAA"},
		{"empty.pulse", ""},
		{"zoë-2026.pulse", "unicode name"},
		{"back\\slash~.pulse", "cp437-unsafe ascii"},
	}
	want := goZipArchive(t, entries...)
	var got bytes.Buffer
	ap := NewArchiveAppender(&got, 0)
	var recs []DirectoryRecord
	for _, e := range entries {
		rec, err := ap.WriteEntry(e[0], []byte(e[1]))
		if err != nil {
			t.Fatal(err)
		}
		recs = append(recs, rec)
	}
	if _, err := ap.Finish(recs); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("appender bytes differ from zip.Writer's\n got %x\nwant %x", got.Bytes(), want)
	}
}

// TestArchiveAppender_RecordCountPastU16: a directory of 65535 records
// or more — the 65535-shard cap plus `_schema.pulse` is 65536 — moves
// its count into the zip64 end record; archive/zip and the tail parser
// both read every record.
func TestArchiveAppender_RecordCountPastU16(t *testing.T) {
	t.Parallel()

	for _, n := range []int{zipUint16Max - 1, zipUint16Max, MaxArchiveShards + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			ap := NewArchiveAppender(&buf, 0)
			recs := make([]DirectoryRecord, 0, n)
			for i := range n {
				rec, err := ap.WriteEntry(fmt.Sprintf("%05d.pulse", i), nil)
				if err != nil {
					t.Fatal(err)
				}
				recs = append(recs, rec)
			}
			if _, err := ap.Finish(recs); err != nil {
				t.Fatal(err)
			}
			b := buf.Bytes()
			hasZip64 := bytes.Contains(b[len(b)-zipEOCDLen-zip64LocatorLen-zip64EOCDLen:], binary.LittleEndian.AppendUint32(nil, zip64EOCDSig))
			if hasZip64 != (n >= zipUint16Max) {
				t.Errorf("zip64 end record present = %v for %d records", hasZip64, n)
			}
			zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(zr.File) != n {
				t.Errorf("archive/zip reads %d records, want %d", len(zr.File), n)
			}
			dir, err := ReadArchiveDirectory(bytes.NewReader(b), int64(len(b)))
			if err != nil {
				t.Fatal(err)
			}
			if len(dir.Records) != n {
				t.Errorf("tail parser reads %d records, want %d", len(dir.Records), n)
			}
		})
	}
}

// TestReadArchiveDirectory_Refusals: layouts the appender does not
// extend are NotAppendable (the caller rewrites); a broken tail is
// PULSE_ARCHIVE_CORRUPT.
func TestReadArchiveDirectory_Refusals(t *testing.T) {
	t.Parallel()

	base := goZipArchive(t, [2]string{ReservedSchemaName, "s"}, [2]string{"a.pulse", "A"})
	for _, c := range []struct {
		name   string
		b      []byte
		reason string
		code   errors.Code
	}{
		{"prepended data", append([]byte("SFX-STUB"), base...), "directory_not_adjacent", ""},
		{"trailing bytes", append(append([]byte(nil), base...), 0, 0, 0), "bytes_after_eocd", ""},
		{"no eocd", base[:len(base)-zipEOCDLen], "", errors.PULSE_ARCHIVE_CORRUPT},
		{"truncated directory", func() []byte {
			b := append([]byte(nil), base...)
			eocd := b[len(b)-zipEOCDLen:]
			binary.LittleEndian.PutUint16(eocd[10:], 3) // claims 3 records
			return b
		}(), "", errors.PULSE_ARCHIVE_CORRUPT},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadArchiveDirectory(bytes.NewReader(c.b), int64(len(c.b)))
			if err == nil {
				t.Fatal("want a refusal")
			}
			if c.reason != "" {
				na, ok := err.(*NotAppendableError)
				if !ok || na.Reason != c.reason || !IsNotAppendable(err) {
					t.Errorf("err = %v, want NotAppendable %s", err, c.reason)
				}
				return
			}
			if IsNotAppendable(err) || !errors.HasCode(err, c.code) {
				t.Errorf("err = %v, want %s", err, c.code)
			}
		})
	}

	// A comment ending at EOF is still appendable.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_ = zw.SetComment("hello")
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "a.pulse", Method: zip.Store})
	_, _ = w.Write([]byte("A"))
	_ = zw.Close()
	if _, err := ReadArchiveDirectory(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err != nil {
		t.Errorf("archive with a comment: %v", err)
	}
}
