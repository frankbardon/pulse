package io

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/afero"
)

// transferFS returns a memfs holding a small synthetic flat cohort at
// "c.pulse" and the committed synthetic shard archive at "arch.pulse".
func transferFS(t *testing.T) afero.Fs {
	t.Helper()
	cols, rows := joinFixture(400)
	_, raw, fs, err := runGroupImport(t, newMockReader(cols, rows), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "c.pulse", raw, 0o644); err != nil {
		t.Fatal(err)
	}
	arch, err := os.ReadFile("../testdata/sharding/two_shards.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fs, "arch.pulse", arch, 0o644); err != nil {
		t.Fatal(err)
	}
	return fs
}

func wantTransferReason(t *testing.T, err error, reason string) {
	t.Helper()
	ce, ok := err.(*perrors.CodedError)
	if !ok || ce.Code != perrors.PULSE_TRANSFER_INVALID || ce.Details["reason"] != reason {
		t.Fatalf("err = %v, want PULSE_TRANSFER_INVALID reason=%s", err, reason)
	}
}

// TestTransfer_ArtifactIsStandardZstd: the artifact is one plain zstd
// stream — a stock decoder (no Pulse code) reproduces the cohort bytes
// exactly, for a single-file cohort and a shard archive.
func TestTransfer_ArtifactIsStandardZstd(t *testing.T) {
	ctx := context.Background()
	fs := transferFS(t)
	for _, src := range []string{"c.pulse", "arch.pulse"} {
		if _, err := (&TransferExportJob{FS: fs, Source: src, Output: src + ".zst"}).Run(ctx); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		z, _ := afero.ReadFile(fs, src+".zst")
		if !encoding.IsZstdMagic(z) {
			t.Fatalf("%s.zst does not start with the zstd magic", src)
		}
		dec, _ := zstd.NewReader(nil)
		got, err := dec.DecodeAll(z, nil)
		dec.Close()
		if err != nil {
			t.Fatalf("stock decode of %s.zst: %v", src, err)
		}
		orig, _ := afero.ReadFile(fs, src)
		if !bytes.Equal(got, orig) {
			t.Fatalf("stock decode of %s.zst differs from the cohort", src)
		}
	}
}

// TestTransfer_ExportRefusals: an out-of-range level, a non-cohort source
// and an already-compressed source are refused before Output is created.
func TestTransfer_ExportRefusals(t *testing.T) {
	ctx := context.Background()
	fs := transferFS(t)
	_ = afero.WriteFile(fs, "rows.csv", []byte("a,b\n1,2\n"), 0o644)
	if _, err := (&TransferExportJob{FS: fs, Source: "c.pulse", Output: "ok.zst"}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, src, reason string
		level             int
	}{
		{"level too high", "c.pulse", "level", 23},
		{"level negative", "c.pulse", "level", -1},
		{"csv source", "rows.csv", "not_a_cohort", 0},
		{"already compressed", "ok.zst", "already_compressed", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&TransferExportJob{FS: fs, Source: tc.src, Output: "x.zst", Level: tc.level}).Run(ctx)
			wantTransferReason(t, err, tc.reason)
			if ok, _ := afero.Exists(fs, "x.zst"); ok {
				t.Fatal("a refused export left an output behind")
			}
		})
	}
	if _, err := (&TransferExportJob{FS: fs, Source: "missing.pulse", Output: "x.zst"}).Run(ctx); !perrors.HasCode(err, perrors.SERVICE_RESOURCE) {
		t.Fatalf("missing source: err = %v, want SERVICE_RESOURCE", err)
	}
}

// TestTransfer_ImportRefusals: a non-zstd source (including an
// uncompressed cohort), a truncated or bit-flipped artifact, a zstd
// stream of non-cohort bytes, and an existing output are all refused —
// and none leaves a staging file or a partial cohort behind.
func TestTransfer_ImportRefusals(t *testing.T) {
	ctx := context.Background()
	fs := transferFS(t)
	if _, err := (&TransferExportJob{FS: fs, Source: "c.pulse", Output: "c.zst"}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	z, _ := afero.ReadFile(fs, "c.zst")
	_ = afero.WriteFile(fs, "trunc.zst", z[:len(z)/2], 0o644)
	flipped := append([]byte(nil), z...)
	flipped[len(flipped)-6] ^= 0xFF // inside the last block / checksum
	_ = afero.WriteFile(fs, "flip.zst", flipped, 0o644)
	enc, _ := zstd.NewWriter(nil)
	_ = afero.WriteFile(fs, "csv.zst", enc.EncodeAll([]byte("a,b\n1,2\n"), nil), 0o644)
	_ = enc.Close()
	_ = afero.WriteFile(fs, "exists.pulse", []byte("keep"), 0o644)

	cases := []struct{ name, src, out, reason string }{
		{"uncompressed cohort", "c.pulse", "o1.pulse", "not_zstd"},
		{"truncated", "trunc.zst", "o2.pulse", "corrupt_stream"},
		{"bit flipped", "flip.zst", "o3.pulse", "corrupt_stream"},
		{"not a cohort inside", "csv.zst", "o4.pulse", "not_a_cohort"},
		{"output exists", "c.zst", "exists.pulse", "output_exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (&TransferImportJob{FS: fs, Source: tc.src, Output: tc.out}).Run(ctx)
			wantTransferReason(t, err, tc.reason)
		})
	}
	if b, _ := afero.ReadFile(fs, "exists.pulse"); string(b) != "keep" {
		t.Fatal("refused import touched the existing output")
	}
	entries, _ := afero.ReadDir(fs, ".")
	for _, e := range entries {
		switch e.Name() {
		case "c.pulse", "arch.pulse", "out.pulse", "c.zst", "trunc.zst", "flip.zst", "csv.zst", "exists.pulse":
		default:
			t.Fatalf("refused import left %q behind", e.Name())
		}
	}
	// Overwrite replaces the existing output with the exact cohort.
	if _, err := (&TransferImportJob{FS: fs, Source: "c.zst", Output: "exists.pulse", Overwrite: true}).Run(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := afero.ReadFile(fs, "exists.pulse")
	orig, _ := afero.ReadFile(fs, "c.pulse")
	if !bytes.Equal(got, orig) {
		t.Fatal("overwrite did not produce the cohort bytes")
	}
}

// TestTransfer_LevelChangesBytesNotContent: levels map onto distinct
// encoder classes (level 19 is strictly smaller than level 1 here) and every level
// round-trips to the same cohort.
func TestTransfer_LevelChangesBytesNotContent(t *testing.T) {
	ctx := context.Background()
	fs := transferFS(t)
	orig, _ := afero.ReadFile(fs, "c.pulse")
	var fastest, best int64
	for _, lvl := range []int{1, 3, 7, 19} {
		rep, err := (&TransferExportJob{FS: fs, Source: "c.pulse", Output: "l.zst", Level: lvl}).Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Level != lvl {
			t.Fatalf("report level %d, want %d", rep.Level, lvl)
		}
		switch lvl {
		case 1:
			fastest = rep.CompressedBytes
		case 19:
			best = rep.CompressedBytes
		}
		if _, err := (&TransferImportJob{FS: fs, Source: "l.zst", Output: "l.pulse", Overwrite: true}).Run(ctx); err != nil {
			t.Fatal(err)
		}
		got, _ := afero.ReadFile(fs, "l.pulse")
		if !bytes.Equal(got, orig) {
			t.Fatalf("level %d round trip differs", lvl)
		}
	}
	if best >= fastest {
		t.Fatalf("level 19 (%d B) not smaller than level 1 (%d B)", best, fastest)
	}
}
