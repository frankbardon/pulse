package encoding_test

import (
	"archive/zip"
	"bytes"
	stderrors "errors"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// failingReaderAt fails every ReadAt — storage that cannot be read.
type failingReaderAt struct{}

var errArchiveIO = stderrors.New("injected archive read failure")

func (failingReaderAt) ReadAt([]byte, int64) (int, error) { return 0, errArchiveIO }

// TestArchive_StorageAndCorruptionRefusals: an unreadable archive is an
// I/O error (never "not an archive"), a size that overstates the bytes
// present is "not an archive" (never a panic), and a damaged or
// unreadable entry is PULSE_ARCHIVE_CORRUPT naming the entry.
func TestArchive_StorageAndCorruptionRefusals(t *testing.T) {
	t.Parallel()

	if ok, err := encx.IsArchive(failingReaderAt{}, 100); ok || !errors.HasCode(err, errors.ENCODING_IO) || !stderrors.Is(err, errArchiveIO) {
		t.Errorf("IsArchive(unreadable) = (%v, %v), want ENCODING_IO", ok, err)
	}
	if _, err := encx.OpenArchive(failingReaderAt{}, 100); !errors.HasCode(err, errors.ENCODING_IO) {
		t.Errorf("OpenArchive(unreadable): err = %v, want ENCODING_IO", err)
	}
	if ok, err := encx.IsArchive(bytes.NewReader([]byte("PK")), 64); ok || err != nil {
		t.Errorf("IsArchive(2 bytes, size 64) = (%v, %v), want (false, nil)", ok, err)
	}

	shard := buildSinglePulse(t)
	data := buildArchive(t, shard, map[string][]byte{"a.pulse": shard}, []string{"a.pulse"})

	// The second local file header belongs to a.pulse: break its
	// signature and the central directory points at garbage.
	sig := []byte("PK\x03\x04")
	second := bytes.Index(data[1:], sig) + 1
	if second <= 0 {
		t.Fatal("fixture has a single local header")
	}
	broken := bytes.Clone(data)
	broken[second] = 'X'
	if _, err := encx.OpenArchive(bytes.NewReader(broken), int64(len(broken))); !errors.HasCode(err, errors.PULSE_ARCHIVE_CORRUPT) {
		t.Errorf("broken local header: err = %v, want PULSE_ARCHIVE_CORRUPT", err)
	}

	// An entry stored with a compression method no reader supports opens
	// in the directory but cannot be read.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "odd.pulse", Method: 99, CompressedSize64: 3, UncompressedSize64: 3})
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("abc"))
	zw.Close()
	a, err := encx.OpenArchive(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("OpenArchive(method 99): %v", err)
	}
	if _, err := a.Open("odd.pulse"); !errors.HasCode(err, errors.PULSE_ARCHIVE_CORRUPT) {
		t.Errorf("Open(method 99): err = %v, want PULSE_ARCHIVE_CORRUPT", err)
	}
	if err := a.PeekShardHeader("odd.pulse"); !errors.HasCode(err, errors.PULSE_ARCHIVE_CORRUPT) {
		t.Errorf("PeekShardHeader(method 99): err = %v, want PULSE_ARCHIVE_CORRUPT", err)
	}
	if err := a.PeekShardHeader("absent.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Errorf("PeekShardHeader(absent): err = %v, want PULSE_SHARD_MISSING", err)
	}
}

// TestArchive_PeekShardHeader_ShortShard: a shard shorter than the
// 9-byte Pulse header is PULSE_SHARD_HEADER_INVALID, not an I/O error.
func TestArchive_PeekShardHeader_ShortShard(t *testing.T) {
	t.Parallel()

	shard := buildSinglePulse(t)
	data := buildArchive(t, shard, map[string][]byte{"short.pulse": shard[:5]}, []string{"short.pulse"})
	a, err := encx.OpenArchive(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.PeekShardHeader("short.pulse"); !errors.HasCode(err, errors.PULSE_SHARD_HEADER_INVALID) {
		t.Errorf("err = %v, want PULSE_SHARD_HEADER_INVALID", err)
	}
	if err := a.PeekShardHeader(encx.ReservedSchemaName); err != nil {
		t.Errorf("valid schema entry refused: %v", err)
	}
}
