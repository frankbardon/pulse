package encoding

import (
	"bytes"
	"strings"
	"testing"
)

// archive_space_test.go pins the dead-byte measure behind the `shard
// add` rewrite fallback and `shard verify` (U73 E2-S3): a freshly
// written archive has none, each tail append leaves exactly the
// superseded `_schema.pulse` + the old directory tail dead (one dead
// copy per append), the measure resolves zip64 offsets, and
// AppendLength is the byte count the appender really writes.

func measure(t *testing.T, archive []byte) (*ArchiveDirectory, *ArchiveSpace) {
	t.Helper()
	r := bytes.NewReader(archive)
	dir, err := ReadArchiveDirectory(r, int64(len(archive)))
	if err != nil {
		t.Fatalf("ReadArchiveDirectory: %v", err)
	}
	sp, err := MeasureArchiveSpace(r, dir)
	if err != nil {
		t.Fatalf("MeasureArchiveSpace: %v", err)
	}
	if got := sp.ShardBytes + sp.SchemaBytes + sp.TailBytes + sp.DeadBytes; got != sp.Size {
		t.Fatalf("space %+v does not add up to the archive size (%d)", sp, got)
	}
	return dir, sp
}

func TestMeasureArchiveSpace_AppendsLeaveOneDeadCopyEach(t *testing.T) {
	t.Parallel()

	created := goZipArchive(t, [2]string{ReservedSchemaName, "schema-v1"},
		[2]string{"a.pulse", strings.Repeat("a", 300)}, [2]string{"b.pulse", strings.Repeat("b", 200)})
	_, sp0 := measure(t, created)
	if sp0.DeadBytes != 0 || sp0.DeadSchemaCopies != 0 {
		t.Fatalf("created archive space = %+v, want no dead bytes", sp0)
	}

	once := appendTail(t, created, ReservedSchemaName,
		[2]string{"c.pulse", strings.Repeat("c", 100)}, [2]string{ReservedSchemaName, "schema-v2"})
	_, sp1 := measure(t, once)
	if sp1.DeadSchemaCopies != 1 {
		t.Errorf("one append: dead copies = %d, want 1", sp1.DeadSchemaCopies)
	}
	if sp1.DeadBytes != sp0.DeadAfterAppend() || sp1.DeadBytes != sp0.SchemaBytes+sp0.TailBytes {
		t.Errorf("one append: dead = %d, want the superseded schema (%d) + old tail (%d)",
			sp1.DeadBytes, sp0.SchemaBytes, sp0.TailBytes)
	}

	twice := appendTail(t, once, ReservedSchemaName,
		[2]string{"d.pulse", strings.Repeat("d", 100)}, [2]string{ReservedSchemaName, "schema-v3"})
	_, sp2 := measure(t, twice)
	if sp2.DeadSchemaCopies != 2 || sp2.DeadBytes != sp1.DeadAfterAppend() {
		t.Errorf("two appends: space %+v, want 2 dead copies and %d dead bytes", sp2, sp1.DeadAfterAppend())
	}
	if sp2.ShardBytes <= sp1.ShardBytes {
		t.Errorf("live shard bytes did not grow: %d -> %d", sp1.ShardBytes, sp2.ShardBytes)
	}
}

// TestMeasureArchiveSpace_Zip64Offsets: entries whose local headers sit
// past 4 GiB resolve through the zip64 extra; everything before them
// (zeros here) is dead, and holds no schema copy.
func TestMeasureArchiveSpace_Zip64Offsets(t *testing.T) {
	t.Parallel()

	const start = int64(5) << 30
	var tail bytes.Buffer
	ap := NewArchiveAppender(&tail, start)
	ra, err := ap.WriteEntry("a.pulse", []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	rs, err := ap.WriteEntry(ReservedSchemaName, []byte("schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ap.Finish([]DirectoryRecord{rs, ra}); err != nil {
		t.Fatal(err)
	}
	file := &sparseReaderAt{size: start + int64(tail.Len()), segs: []sparseSeg{{start, tail.Bytes()}}}
	dir, err := ReadArchiveDirectory(file, file.size)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := MeasureArchiveSpace(file, dir)
	if err != nil {
		t.Fatalf("MeasureArchiveSpace over zip64 offsets: %v", err)
	}
	if sp.DeadBytes != start || sp.DeadSchemaCopies != 0 {
		t.Errorf("space = %+v, want %d dead bytes and no copy", sp, start)
	}
	if want := int64(zipLocalHeaderLen + len("a.pulse") + len("first") + 16); sp.ShardBytes != want {
		t.Errorf("shard bytes = %d, want %d", sp.ShardBytes, want)
	}
}

// TestAppendLength_MatchesAppender: AppendLength equals the bytes the
// appender writes — an ordinary append, one past 4 GiB (zip64 records)
// and one whose directory passes 65535 records.
func TestAppendLength_MatchesAppender(t *testing.T) {
	t.Parallel()

	write := func(dir *ArchiveDirectory, names []string, payloads [][]byte) int64 {
		var tail bytes.Buffer
		ap := NewArchiveAppender(&tail, dir.Size)
		var recs []DirectoryRecord
		for _, r := range dir.Records {
			if r.Name != ReservedSchemaName {
				recs = append(recs, r)
			}
		}
		for i, n := range names {
			rec, err := ap.WriteEntry(n, payloads[i])
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, rec)
		}
		if _, err := ap.Finish(recs); err != nil {
			t.Fatal(err)
		}
		return ap.Written()
	}
	check := func(label string, dir *ArchiveDirectory) {
		names := []string{"new.pulse", ReservedSchemaName}
		payloads := [][]byte{bytes.Repeat([]byte{7}, 321), []byte("schema-doc")}
		sizes := []uint64{321, 10}
		if got, want := AppendLength(dir, ReservedSchemaName, names, sizes), write(dir, names, payloads); got != want {
			t.Errorf("%s: AppendLength = %d, appender wrote %d", label, got, want)
		}
	}

	created := goZipArchive(t, [2]string{ReservedSchemaName, "s"}, [2]string{"a.pulse", "aaaa"})
	dir, err := ReadArchiveDirectory(bytes.NewReader(created), int64(len(created)))
	if err != nil {
		t.Fatal(err)
	}
	check("ordinary", dir)
	check("past 4 GiB", &ArchiveDirectory{Size: 5 << 30, Records: dir.Records})

	many := &ArchiveDirectory{Size: 1 << 20}
	for i := range zipUint16Max {
		many.Records = append(many.Records, CentralRecord("s.pulse", 0, 0, 1, uint64(i)))
	}
	check("65535+ records", many)
}
