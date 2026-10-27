package encoding_test

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
)

// TestShardStream_StripZip64Extra: a raw-copied entry's zip64 extra
// block is dropped (zip.Writer.Close appends a fresh one sized to the
// NEW offset; a copied one would sit beside it as a duplicate) while
// every other block survives in order, and a malformed tail is kept.
func TestShardStream_StripZip64Extra(t *testing.T) {
	t.Parallel()

	block := func(tag uint16, data ...byte) []byte {
		return append([]byte{byte(tag), byte(tag >> 8), byte(len(data)), 0}, data...)
	}
	ts := block(0x5455, 1, 2, 3, 4, 5)
	z64 := block(0x0001, 1, 2, 3, 4, 5, 6, 7, 8)
	other := block(0x7875, 9)
	cases := []struct {
		name     string
		in, want []byte
	}{
		{"nil", nil, nil},
		{"only_zip64", z64, nil},
		{"zip64_between", bytes.Join([][]byte{ts, z64, other}, nil), bytes.Join([][]byte{ts, other}, nil)},
		{"no_zip64", bytes.Join([][]byte{ts, other}, nil), bytes.Join([][]byte{ts, other}, nil)},
		{"malformed_tail", append(slicesClone(z64), 0x01, 0x00, 0xFF), []byte{0x01, 0x00, 0xFF}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := slicesClone(tc.in)
			got := encx.StripZip64Extra(in)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("got %x, want %x", got, tc.want)
			}
			if !bytes.Equal(in, tc.in) {
				t.Fatal("the input was modified")
			}
		})
	}
}

func slicesClone(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

// TestShardStream_CopyStoredEntryRaw: a store-only entry copied with
// CopyStoredEntry reads back identically (same bytes, same CRC) without
// the copy re-hashing it, and a deflated entry is refused rather than
// carried as compressed bytes.
func TestShardStream_CopyStoredEntryRaw(t *testing.T) {
	t.Parallel()

	payload := bytes.Repeat([]byte("pulse-shard-"), 1000)
	var src bytes.Buffer
	zw := zip.NewWriter(&src)
	for _, e := range []struct {
		name   string
		method uint16
	}{{"s.pulse", zip.Store}, {"d.pulse", zip.Deflate}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: e.method})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	arch, err := encx.OpenArchive(bytes.NewReader(src.Bytes()), int64(src.Len()))
	if err != nil {
		t.Fatal(err)
	}

	var dst bytes.Buffer
	out := zip.NewWriter(&dst)
	if err := arch.CopyStoredEntry(out, "s.pulse", make([]byte, 64)); err != nil {
		t.Fatalf("CopyStoredEntry: %v", err)
	}
	err = arch.CopyStoredEntry(out, "d.pulse", nil)
	if !errors.HasCode(err, errors.PULSE_ARCHIVE_CORRUPT) {
		t.Fatalf("deflated entry: err = %v, want PULSE_ARCHIVE_CORRUPT", err)
	}
	if err := arch.CopyStoredEntry(out, "missing.pulse", nil); !errors.HasCode(err, errors.PULSE_SHARD_MISSING) {
		t.Fatalf("missing entry: err = %v, want PULSE_SHARD_MISSING", err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(dst.Bytes()), int64(dst.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "s.pulse" || zr.File[0].Method != zip.Store {
		t.Fatalf("copied directory = %+v", zr.File)
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc) // verifies the copied CRC at EOF
	if err != nil {
		t.Fatalf("reading the copy: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("copied payload differs")
	}
	// The copy's stored region is the source's, byte for byte.
	srcOff, _ := arch.OpenAt("s.pulse")
	off, err := zr.File[0].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	a := make([]byte, srcOff.Size())
	if _, err := srcOff.ReadAt(a, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dst.Bytes()[off:off+int64(len(payload))], a) {
		t.Fatal("stored region not copied verbatim")
	}
}
