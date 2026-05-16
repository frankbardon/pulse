package encoding_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/service"
	"github.com/spf13/afero"
)

// --- helpers ---

// buildSinglePulse returns the bytes of a minimal valid single-file
// .pulse cohort (header + schema, zero records).
func buildSinglePulse(t *testing.T) []byte {
	t.Helper()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	return buf.Bytes()
}

// buildArchive builds a Pulse shard archive in memory. entries is a list
// of (name, payload). When schemaPayload is non-nil it is stored under
// encoding.ReservedSchemaName before the rest of the entries.
func buildArchive(t *testing.T, schemaPayload []byte, entries map[string][]byte, order []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, payload []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Store,
		})
		if err != nil {
			t.Fatalf("zip CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatalf("zip write(%q): %v", name, err)
		}
	}
	if schemaPayload != nil {
		write(encoding.ReservedSchemaName, schemaPayload)
	}
	for _, name := range order {
		write(name, entries[name])
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip Close: %v", err)
	}
	return buf.Bytes()
}

// --- IsArchive tests ---

func TestArchive_IsArchive_DetectsZipMagic(t *testing.T) {
	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"a.pulse": buildSinglePulse(t),
	}, []string{"a.pulse"})

	r := bytes.NewReader(data)
	ok, err := encoding.IsArchive(r, int64(len(data)))
	if err != nil {
		t.Fatalf("IsArchive: %v", err)
	}
	if !ok {
		t.Errorf("IsArchive on zip = false, want true")
	}
}

func TestArchive_IsArchive_DetectsSingleFilePulse(t *testing.T) {
	data := buildSinglePulse(t)
	r := bytes.NewReader(data)
	ok, err := encoding.IsArchive(r, int64(len(data)))
	if err != nil {
		t.Fatalf("IsArchive: %v", err)
	}
	if ok {
		t.Errorf("IsArchive on single-file .pulse = true, want false")
	}
}

func TestArchive_IsArchive_ShortFile(t *testing.T) {
	data := []byte{0x50, 0x4b}
	ok, err := encoding.IsArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("IsArchive on short file: %v", err)
	}
	if ok {
		t.Errorf("IsArchive on short file = true, want false")
	}
}

func TestArchive_IsArchive_EmptyFile(t *testing.T) {
	ok, err := encoding.IsArchive(bytes.NewReader(nil), 0)
	if err != nil {
		t.Fatalf("IsArchive: %v", err)
	}
	if ok {
		t.Errorf("IsArchive on empty file = true, want false")
	}
}

// --- OpenArchive tests ---

func TestArchive_OpenArchive_ParsesCentralDirectory(t *testing.T) {
	schema := buildSinglePulse(t)
	shard1 := buildSinglePulse(t)
	shard2 := buildSinglePulse(t)

	data := buildArchive(t, schema, map[string][]byte{
		"20190101.pulse": shard1,
		"20190107.pulse": shard2,
	}, []string{"20190101.pulse", "20190107.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}

	entries := arch.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}

	// Check insertion order: schema first, then shards.
	wantNames := []string{encoding.ReservedSchemaName, "20190101.pulse", "20190107.pulse"}
	for i, want := range wantNames {
		if entries[i].Name != want {
			t.Errorf("entries[%d].Name = %q, want %q", i, entries[i].Name, want)
		}
		if entries[i].Size != int64(len(schema)) {
			t.Errorf("entries[%d].Size = %d, want %d", i, entries[i].Size, len(schema))
		}
		if entries[i].Offset <= 0 {
			t.Errorf("entries[%d].Offset = %d, want >0", i, entries[i].Offset)
		}
	}
}

func TestArchive_OpenArchive_MagicInvalid(t *testing.T) {
	data := []byte("not a zip and not a pulse file at all here")
	_, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err == nil {
		t.Fatal("expected error for non-archive data")
	}
	if !errors.HasCode(err, errors.PULSE_ARCHIVE_MAGIC_INVALID) {
		t.Errorf("expected PULSE_ARCHIVE_MAGIC_INVALID, got: %v", err)
	}
}

func TestArchive_OpenArchive_CorruptEOCD(t *testing.T) {
	// Start with a valid archive, then truncate to destroy the EOCD.
	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"a.pulse": buildSinglePulse(t),
	}, []string{"a.pulse"})

	// Keep the zip magic header but corrupt the bytes near the tail
	// where the EOCD lives.
	corrupted := append([]byte(nil), data[:4]...) // PK\x03\x04
	corrupted = append(corrupted, bytes.Repeat([]byte{0xFF}, 32)...)

	_, err := encoding.OpenArchive(bytes.NewReader(corrupted), int64(len(corrupted)))
	if err == nil {
		t.Fatal("expected error for corrupt EOCD")
	}
	if !errors.HasCode(err, errors.PULSE_ARCHIVE_CORRUPT) {
		t.Errorf("expected PULSE_ARCHIVE_CORRUPT, got: %v", err)
	}
}

// --- Entry access tests ---

func TestArchive_Open_MissingEntry(t *testing.T) {
	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"a.pulse": buildSinglePulse(t),
	}, []string{"a.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if _, err := arch.Open("nonexistent.pulse"); err == nil {
		t.Fatal("expected error for missing entry")
	} else if !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("expected PULSE_SHARD_MISSING, got: %v", err)
	}
}

func TestArchive_Open_ReadsPayload(t *testing.T) {
	payload := buildSinglePulse(t)
	data := buildArchive(t, payload, map[string][]byte{
		"a.pulse": payload,
	}, []string{"a.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	rc, err := arch.Open("a.pulse")
	if err != nil {
		t.Fatalf("Open(a.pulse): %v", err)
	}
	defer rc.Close()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestArchive_OpenAt_SectionReader(t *testing.T) {
	payload := buildSinglePulse(t)
	data := buildArchive(t, payload, map[string][]byte{
		"a.pulse": payload,
	}, []string{"a.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if !arch.IsStored("a.pulse") {
		t.Fatal("expected a.pulse to be stored (Method 0)")
	}
	sr, err := arch.OpenAt("a.pulse")
	if err != nil {
		t.Fatalf("OpenAt(a.pulse): %v", err)
	}
	got, err := io.ReadAll(&sr)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("payload mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

func TestArchive_OpenAt_MissingEntry(t *testing.T) {
	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"a.pulse": buildSinglePulse(t),
	}, []string{"a.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if _, err := arch.OpenAt("missing.pulse"); err == nil {
		t.Fatal("expected error for missing entry")
	} else if !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("expected PULSE_SHARD_MISSING, got: %v", err)
	}
}

func TestArchive_PeekShardHeader_Valid(t *testing.T) {
	payload := buildSinglePulse(t)
	data := buildArchive(t, payload, map[string][]byte{
		"a.pulse": payload,
	}, []string{"a.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	if err := arch.PeekShardHeader("a.pulse"); err != nil {
		t.Errorf("PeekShardHeader on valid shard: %v", err)
	}
}

func TestArchive_PeekShardHeader_InvalidMagic(t *testing.T) {
	badPayload := bytes.Repeat([]byte{0xAA}, 32)
	data := buildArchive(t, buildSinglePulse(t), map[string][]byte{
		"bad.pulse": badPayload,
	}, []string{"bad.pulse"})

	arch, err := encoding.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	err = arch.PeekShardHeader("bad.pulse")
	if err == nil {
		t.Fatal("expected error for invalid shard header")
	}
	if !errors.HasCode(err, errors.PULSE_SHARD_HEADER_INVALID) {
		t.Errorf("expected PULSE_SHARD_HEADER_INVALID, got: %v", err)
	}
}

// --- ReservedSchemaName constant ---

func TestArchive_ReservedSchemaName(t *testing.T) {
	if encoding.ReservedSchemaName != "_schema.pulse" {
		t.Errorf("ReservedSchemaName = %q, want %q",
			encoding.ReservedSchemaName, "_schema.pulse")
	}
}

// --- TestShardArchiveMagicDispatch: non-skippable CI gate ---

// TestShardArchiveMagicDispatch is a non-skippable CI gate verifying
// that service.Service.Open (the same dispatch behind pulse.Open)
// distinguishes a zip-archive cohort from a single-file .pulse cohort
// via the leading magic bytes. A single-file cohort opens with an
// empty Shards slice; an archive cohort opens with Shards populated
// from the central directory (excluding the reserved _schema.pulse
// entry).
func TestShardArchiveMagicDispatch(t *testing.T) {
	cfg := fs.NewMemMap()

	// Single-file cohort path.
	single := buildSinglePulse(t)
	if err := afero.WriteFile(cfg.Fs(), "single.pulse", single, 0644); err != nil {
		t.Fatalf("WriteFile single: %v", err)
	}

	// Archive cohort path.
	archiveBytes := buildArchive(t, single, map[string][]byte{
		"20190101.pulse": single,
		"20190107.pulse": single,
	}, []string{"20190101.pulse", "20190107.pulse"})
	if err := afero.WriteFile(cfg.Fs(), "archive.pulse", archiveBytes, 0644); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}

	svc := service.New(cfg)
	ctx := context.Background()

	// Single-file: Shards is empty.
	c1, err := svc.Open(ctx, "single.pulse")
	if err != nil {
		t.Fatalf("Open(single.pulse): %v", err)
	}
	if got := c1.Shards(); len(got) != 0 {
		t.Errorf("single-file cohort Shards = %d, want 0", len(got))
	}
	if c1.Schema() == nil || len(c1.Schema().Fields) == 0 {
		t.Errorf("single-file cohort schema is missing or empty")
	}

	// Archive: Shards lists the two .pulse shards in insertion order;
	// _schema.pulse is excluded.
	c2, err := svc.Open(ctx, "archive.pulse")
	if err != nil {
		t.Fatalf("Open(archive.pulse): %v", err)
	}
	shards := c2.Shards()
	if len(shards) != 2 {
		t.Fatalf("archive cohort Shards = %d, want 2", len(shards))
	}
	want := []string{"20190101.pulse", "20190107.pulse"}
	for i, s := range shards {
		if s.Filename != want[i] {
			t.Errorf("shards[%d].Filename = %q, want %q", i, s.Filename, want[i])
		}
		// S1 leaves RecordCount at zero — S2 will populate it.
		if s.RecordCount != 0 {
			t.Errorf("shards[%d].RecordCount = %d, want 0 (S1 stub)", i, s.RecordCount)
		}
	}
	if c2.Schema() == nil || len(c2.Schema().Fields) == 0 {
		t.Errorf("archive cohort schema is missing or empty")
	}

	// Ensure the reserved entry is never surfaced as a shard.
	for _, s := range shards {
		if s.Filename == encoding.ReservedSchemaName {
			t.Errorf("Shards must exclude %q", encoding.ReservedSchemaName)
		}
	}
}
