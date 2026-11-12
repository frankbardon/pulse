package encoding

import (
	"bytes"
	"encoding/binary"
	stderrors "errors"
	"io"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
)

// archive_tail_faults_test.go pins the refusals of the hand-written zip
// tail reader and writer (U73 #492) and of the dead-byte measure built
// on them (E2-S3): every malformed zip64 tail, unreadable region,
// failing writer and out-of-range record answers with its own coded
// error or NotAppendable reason — never a panic, never a silent
// mis-measure.

var errInjected = stderrors.New("injected storage fault")

// offsetFailReaderAt fails the one ReadAt that starts at fail.
type offsetFailReaderAt struct {
	r    io.ReaderAt
	fail int64
}

func (o offsetFailReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off == o.fail {
		return 0, errInjected
	}
	return o.r.ReadAt(p, off)
}

// nthFailWriter fails its n-th Write call (1-based), accepting the rest.
type nthFailWriter struct {
	calls, n int
	buf      bytes.Buffer
}

func (w *nthFailWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == w.n {
		return 0, errInjected
	}
	return w.buf.Write(p)
}

// zip64Tail writes two entries and a directory at a 5 GiB start, so the
// tail carries a zip64 end record + locator. It returns the tail bytes
// and the start.
func zip64Tail(t *testing.T) ([]byte, int64) {
	t.Helper()
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
	if _, err := ap.Finish([]DirectoryRecord{r2, r1}); err != nil {
		t.Fatal(err)
	}
	return tail.Bytes(), start
}

// TestReadArchiveDirectory_Zip64TailRefusals: each field of the zip64
// end record and locator that the appender cannot honour is refused —
// multi-disk and an extensible data sector as NotAppendable, a locator
// pointing nowhere valid as PULSE_ARCHIVE_CORRUPT — and the intact tail
// still parses.
func TestReadArchiveDirectory_Zip64TailRefusals(t *testing.T) {
	t.Parallel()

	tb, start := zip64Tail(t)
	locAt := len(tb) - zipEOCDLen - zip64LocatorLen
	recAt := locAt - zip64EOCDLen
	edit := func(f func(b []byte) []byte) []byte { return f(append([]byte(nil), tb...)) }

	for _, c := range []struct {
		name   string
		b      []byte
		reason string
		code   errors.Code
	}{
		{"intact", tb, "", ""},
		{"locator disk", edit(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[locAt+4:], 1); return b }), "multi_disk", ""},
		{"locator total disks", edit(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[locAt+16:], 2); return b }), "multi_disk", ""},
		{"locator offset past itself", edit(func(b []byte) []byte {
			binary.LittleEndian.PutUint64(b[locAt+8:], uint64(start)+uint64(len(tb)))
			return b
		}), "", errors.PULSE_ARCHIVE_CORRUPT},
		{"zip64 record signature", edit(func(b []byte) []byte { b[recAt] ^= 0xff; return b }), "", errors.PULSE_ARCHIVE_CORRUPT},
		{"zip64 record disk", edit(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[recAt+16:], 1); return b }), "multi_disk", ""},
		{"zip64 directory disk", edit(func(b []byte) []byte { binary.LittleEndian.PutUint32(b[recAt+20:], 3); return b }), "multi_disk", ""},
		{"extensible data sector", edit(func(b []byte) []byte {
			// Eight bytes between the zip64 end record and its locator:
			// the locator still points at the record, which no longer
			// ends where the locator begins.
			out := append([]byte(nil), b[:locAt]...)
			out = append(out, make([]byte, 8)...)
			return append(out, b[locAt:]...)
		}), "zip64_extensible_data", ""},
		{"no locator: the sentinels are taken literally", edit(func(b []byte) []byte { b[locAt] ^= 0xff; return b }), "directory_not_adjacent", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			file := &sparseReaderAt{size: start + int64(len(c.b)), segs: []sparseSeg{{start, c.b}}}
			dir, err := ReadArchiveDirectory(file, file.size)
			switch {
			case c.reason == "" && c.code == "":
				if err != nil || len(dir.Records) != 2 {
					t.Fatalf("intact zip64 tail = %+v, %v", dir, err)
				}
			case c.reason != "":
				var na *NotAppendableError
				if !stderrors.As(err, &na) || na.Reason != c.reason {
					t.Fatalf("err = %v, want NotAppendable %s", err, c.reason)
				}
				if !strings.Contains(err.Error(), c.reason) || !strings.Contains(err.Error(), "cannot be extended in place") {
					t.Errorf("message %q does not name the reason", err.Error())
				}
			default:
				if IsNotAppendable(err) || !errors.HasCode(err, c.code) {
					t.Fatalf("err = %v, want %s", err, c.code)
				}
			}
		})
	}
}

// TestReadArchiveDirectory_StorageAndShapeRefusals covers the plain
// (non-zip64) tail: a file shorter than an EOCD, a multi-disk EOCD, a
// count sentinel with no room for a locator, a malformed or overrunning
// directory record, and a read failure at each region the parser reads.
func TestReadArchiveDirectory_StorageAndShapeRefusals(t *testing.T) {
	t.Parallel()

	base := goZipArchive(t, [2]string{ReservedSchemaName, "s"}, [2]string{"a.pulse", "A"})
	eocdAt := len(base) - zipEOCDLen
	dirOff := int(binary.LittleEndian.Uint32(base[eocdAt+16:]))
	edit := func(f func(b []byte) []byte) []byte { return f(append([]byte(nil), base...)) }

	// A bare EOCD whose record count is the zip64 sentinel: there is no
	// room before it for a locator, so the sentinel is literal and the
	// (empty) directory disagrees with it.
	bareSentinel := make([]byte, zipEOCDLen)
	binary.LittleEndian.PutUint32(bareSentinel, zipEOCDSig)
	binary.LittleEndian.PutUint16(bareSentinel[10:], zipUint16Max)

	for _, c := range []struct {
		name   string
		r      io.ReaderAt
		size   int
		reason string
		code   errors.Code
	}{
		{"shorter than an EOCD", bytes.NewReader(base[:zipEOCDLen-1]), zipEOCDLen - 1, "", errors.PULSE_ARCHIVE_CORRUPT},
		{"multi-disk EOCD", bytes.NewReader(edit(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[eocdAt+4:], 1)
			return b
		})), len(base), "multi_disk", ""},
		{"count sentinel with no locator room", bytes.NewReader(bareSentinel), zipEOCDLen, "", errors.PULSE_ARCHIVE_CORRUPT},
		{"directory record signature", bytes.NewReader(edit(func(b []byte) []byte { b[dirOff] ^= 0xff; return b })), len(base), "", errors.PULSE_ARCHIVE_CORRUPT},
		{"directory record overruns", bytes.NewReader(edit(func(b []byte) []byte {
			binary.LittleEndian.PutUint16(b[dirOff+32:], 0xffff) // comment length
			return b
		})), len(base), "", errors.PULSE_ARCHIVE_CORRUPT},
		{"tail unreadable", offsetFailReaderAt{bytes.NewReader(base), 0}, len(base), "", errors.ENCODING_IO},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := ReadArchiveDirectory(c.r, int64(c.size))
			if c.reason != "" {
				var na *NotAppendableError
				if !stderrors.As(err, &na) || na.Reason != c.reason {
					t.Fatalf("err = %v, want NotAppendable %s", err, c.reason)
				}
				return
			}
			if IsNotAppendable(err) || !errors.HasCode(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
		})
	}

	// The tail read starts at 0 for an archive under 64 KiB, so the
	// directory read is a second, separately failing region.
	if _, err := ReadArchiveDirectory(offsetFailReaderAt{bytes.NewReader(base), int64(dirOff)}, int64(len(base))); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errInjected) {
		t.Errorf("unreadable directory: %v, want ENCODING_IO wrapping the fault", err)
	}

	// zip64 regions: the locator, then the zip64 end record, each
	// unreadable on its own.
	tb, start := zip64Tail(t)
	file := &sparseReaderAt{size: start + int64(len(tb)), segs: []sparseSeg{{start, tb}}}
	locOff := file.size - zipEOCDLen - zip64LocatorLen
	for name, at := range map[string]int64{"locator": locOff, "zip64 end record": locOff - zip64EOCDLen} {
		if _, err := ReadArchiveDirectory(offsetFailReaderAt{file, at}, file.size); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errInjected) {
			t.Errorf("unreadable %s: %v, want ENCODING_IO wrapping the fault", name, err)
		}
	}
}

// TestArchiveAppender_RefusesBadNamesAndWriteFaults: an empty or
// over-long name is refused before a byte is written, and a failing
// writer at each write of WriteEntry and Finish surfaces as ENCODING_IO
// — never a record for an entry that was not written.
func TestArchiveAppender_RefusesBadNamesAndWriteFaults(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", strings.Repeat("n", zipUint16Max+1)} {
		var buf bytes.Buffer
		ap := NewArchiveAppender(&buf, 0)
		if _, err := ap.WriteEntry(name, []byte("x")); !errors.HasCode(err, errors.ENCODING_IO) {
			t.Errorf("name of %d bytes: err = %v, want ENCODING_IO", len(name), err)
		}
		if buf.Len() != 0 || ap.Written() != 0 {
			t.Errorf("name of %d bytes: wrote %d bytes before refusing", len(name), buf.Len())
		}
	}

	// WriteEntry writes local header, payload, data descriptor.
	for n := 1; n <= 3; n++ {
		w := &nthFailWriter{n: n}
		rec, err := NewArchiveAppender(w, 0).WriteEntry("a.pulse", []byte("payload"))
		if !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errInjected) {
			t.Errorf("WriteEntry failing at write %d: %v, want ENCODING_IO", n, err)
		}
		if rec.Len() != 0 {
			t.Errorf("WriteEntry failing at write %d returned a record", n)
		}
	}

	// Finish writes each record, then (past 4 GiB) the zip64 end record
	// + locator, then the EOCD.
	rec := CentralRecord("a.pulse", 0, 0, 1, 0)
	for _, c := range []struct {
		name  string
		start int64
		n     int
	}{
		{"record", 0, 1},
		{"eocd", 0, 2},
		{"zip64 end record", 5 << 30, 2},
		{"eocd after zip64", 5 << 30, 3},
	} {
		w := &nthFailWriter{n: c.n}
		if _, err := NewArchiveAppender(w, c.start).Finish([]DirectoryRecord{rec}); !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errInjected) {
			t.Errorf("Finish failing at the %s: %v, want ENCODING_IO", c.name, err)
		}
	}
}

// placedEntry writes one store-only entry as if at archive offset off
// and returns its record and bytes.
func placedEntry(t *testing.T, off int64, name string, payload []byte) (DirectoryRecord, []byte) {
	t.Helper()
	var b bytes.Buffer
	rec, err := NewArchiveAppender(&b, off).WriteEntry(name, payload)
	if err != nil {
		t.Fatal(err)
	}
	return rec, b.Bytes()
}

// TestMeasureArchiveSpace_Refusals: a record whose entry lies outside,
// lacks its local header, overruns the directory or carries a broken
// zip64 extra is PULSE_ARCHIVE_CORRUPT; an unreadable local header,
// data descriptor or gap is ENCODING_IO.
func TestMeasureArchiveSpace_Refusals(t *testing.T) {
	t.Parallel()

	const at = 100
	rec, b := placedEntry(t, at, "a.pulse", []byte("payload"))
	end := int64(at + len(b))
	descAt := end - 12 - 4 // the descriptor signature: CRC + two u32 sizes follow it
	file := func(size int64, bs []byte) *sparseReaderAt {
		return &sparseReaderAt{size: size, segs: []sparseSeg{{at, bs}}}
	}
	dirAt := func(dirOff int64, recs ...DirectoryRecord) *ArchiveDirectory {
		return &ArchiveDirectory{Size: dirOff, DirOffset: dirOff, Records: recs}
	}
	withRaw := func(r DirectoryRecord, f func(raw []byte) []byte) DirectoryRecord {
		return DirectoryRecord{Name: r.Name, raw: f(append([]byte(nil), r.raw...))}
	}
	offsetSentinel := func(raw []byte) []byte { binary.LittleEndian.PutUint32(raw[42:], zipUint32Max); return raw }
	// withExtra appends an extra field to an offset-sentinel record.
	withExtra := func(extra []byte) DirectoryRecord {
		return withRaw(rec, func(raw []byte) []byte {
			raw = offsetSentinel(raw)
			binary.LittleEndian.PutUint16(raw[30:], uint16(len(extra)))
			return append(raw, extra...)
		})
	}
	field := func(id uint16, body []byte) []byte {
		f := binary.LittleEndian.AppendUint16(nil, id)
		f = binary.LittleEndian.AppendUint16(f, uint16(len(body)))
		return append(f, body...)
	}

	for _, c := range []struct {
		name string
		r    io.ReaderAt
		dir  *ArchiveDirectory
		code errors.Code
	}{
		{"entry past the directory", file(end, b), dirAt(at, rec), errors.PULSE_ARCHIVE_CORRUPT},
		{"no local header", file(end, append([]byte{0}, b[1:]...)), dirAt(end, rec), errors.PULSE_ARCHIVE_CORRUPT},
		{"entry overruns the directory", file(end, b), dirAt(end-1, rec), errors.PULSE_ARCHIVE_CORRUPT},
		{"offset sentinel without zip64 extra", file(end, b), dirAt(end, withRaw(rec, offsetSentinel)), errors.PULSE_ARCHIVE_CORRUPT},
		{"zip64 extra too short for the offset", file(end, b), dirAt(end, withExtra(field(zip64ExtraID, []byte{1, 2, 3}))), errors.PULSE_ARCHIVE_CORRUPT},
		{"extra field length overruns", file(end, b), dirAt(end, withExtra([]byte{0x01, 0x00, 0x40, 0x00, 0})), errors.PULSE_ARCHIVE_CORRUPT},
		{"local header unreadable", offsetFailReaderAt{file(end, b), at}, dirAt(end, rec), errors.ENCODING_IO},
		{"data descriptor unreadable", offsetFailReaderAt{file(end, b), descAt}, dirAt(end, rec), errors.ENCODING_IO},
		{"leading gap unreadable", offsetFailReaderAt{file(end, b), 0}, dirAt(end, rec), errors.ENCODING_IO},
		{"trailing gap unreadable", offsetFailReaderAt{file(end+200, b), end}, dirAt(end+200, rec), errors.ENCODING_IO},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if _, err := MeasureArchiveSpace(c.r, c.dir); !errors.HasCode(err, c.code) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
		})
	}

	// A foreign extra field before the zip64 one is skipped: the offset
	// resolves from the zip64 field and the entry measures in full.
	resolved := withExtra(append(field(0xcafe, []byte("skip")), field(zip64ExtraID, binary.LittleEndian.AppendUint64(nil, at))...))
	sp, err := MeasureArchiveSpace(file(end, b), dirAt(end, resolved))
	if err != nil {
		t.Fatalf("foreign extra before zip64: %v", err)
	}
	if sp.ShardBytes != int64(len(b)) || sp.DeadBytes != at || sp.DeadSchemaCopies != 0 {
		t.Errorf("space = %+v, want %d shard bytes and %d dead", sp, len(b), at)
	}
}

// TestMeasureArchiveSpace_SmallGapAndHugeEntry: a gap too short to hold
// a `_schema.pulse` local header is dead bytes but no dead copy; an
// entry of 4 GiB or more spans its 64-bit data descriptor (16 size
// bytes), resolved from the zip64 extra — without a 4 GiB payload.
func TestMeasureArchiveSpace_SmallGapAndHugeEntry(t *testing.T) {
	t.Parallel()

	const at = 5
	rec, b := placedEntry(t, at, ReservedSchemaName, []byte("s"))
	end := int64(at + len(b))
	sp, err := MeasureArchiveSpace(&sparseReaderAt{size: end, segs: []sparseSeg{{at, b}}},
		&ArchiveDirectory{Size: end, DirOffset: end, Records: []DirectoryRecord{rec}})
	if err != nil {
		t.Fatal(err)
	}
	if sp.DeadBytes != at || sp.DeadSchemaCopies != 0 || sp.SchemaBytes != int64(len(b)) {
		t.Errorf("small gap: space = %+v, want %d dead bytes, no copy", sp, at)
	}

	// A 5 GiB entry at offset 0: local header, payload (sparse zeros),
	// signed 64-bit descriptor.
	const size = uint64(5) << 30
	lh := localFileHeader("big.pulse", zipFlagDataDescriptor)
	payloadEnd := int64(len(lh)) + int64(size)
	desc := dataDescriptor(0, size)
	if len(desc) != zipDataDescriptor64Len {
		t.Fatalf("descriptor for a 5 GiB entry is %d bytes, want %d", len(desc), zipDataDescriptor64Len)
	}
	spanEnd := payloadEnd + int64(len(desc))
	big := CentralRecord("big.pulse", zipFlagDataDescriptor, 0, size, 0)
	f := &sparseReaderAt{size: spanEnd, segs: []sparseSeg{{0, lh}, {payloadEnd, desc}}}
	sp, err = MeasureArchiveSpace(f, &ArchiveDirectory{Size: spanEnd, DirOffset: spanEnd, Records: []DirectoryRecord{big}})
	if err != nil {
		t.Fatalf("5 GiB entry: %v", err)
	}
	if sp.ShardBytes != spanEnd || sp.DeadBytes != 0 {
		t.Errorf("5 GiB entry: space = %+v, want %d shard bytes and none dead", sp, spanEnd)
	}
}
