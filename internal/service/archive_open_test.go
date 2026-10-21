package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
)

// allocatedBytes is the fewest heap bytes fn allocated over three runs
// (runtime.MemStats.TotalAlloc delta) — the minimum filters out a
// background allocation landing inside one window.
func allocatedBytes(t *testing.T, fn func()) uint64 {
	t.Helper()
	var best uint64
	for i := range 3 {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		fn()
		runtime.ReadMemStats(&after)
		if d := after.TotalAlloc - before.TotalAlloc; i == 0 || d < best {
			best = d
		}
	}
	return best
}

// TestArchiveOpen_AllocsIndependentOfBytes (FR-7): every archive read
// path that needs only the central directory and headers allocates in
// proportion to the SHARD COUNT, never the archive's byte size. Two
// archives with the same four shards, the second carrying 10x the
// payload: each surface allocates the same order of bytes on both, and
// far less than the large archive's size (a whole-archive read — the
// pre-U51 afero.ReadFile — allocates at least its length).
func TestArchiveOpen_AllocsIndependentOfBytes(t *testing.T) {
	mem := afero.NewMemMapFs()
	smallSize := writeDictArchive(t, mem, "small.pulse", 4, 2_000, 4)
	largeSize := writeDictArchive(t, mem, "large.pulse", 4, 20_000, 4)
	if largeSize < 9*smallSize {
		t.Fatalf("fixture: large archive %d B is not ~10x the small %d B", largeSize, smallSize)
	}
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	ctx := context.Background()
	canonical := dictArchiveSchema(4)

	surfaces := map[string]func(path string) error{
		"Open": func(p string) error {
			_, err := svc.Open(ctx, p)
			return err
		},
		"ListShards": func(p string) error {
			_, err := svc.ListShards(ctx, p)
			return err
		},
		"OpenAnchor": func(p string) error {
			_, err := svc.Open(ctx, p+"#s01.pulse")
			return err
		},
		"CohortCounts": func(p string) error {
			_, err := svc.CohortCounts(ctx, p)
			return err
		},
		"CountRecords": func(p string) error {
			_, err := svc.CountRecords(ctx, p)
			return err
		},
		"CountRecords anchor": func(p string) error {
			_, err := svc.CountRecords(ctx, p+"#s02.pulse")
			return err
		},
		"PrecheckShardCohesion": func(p string) error {
			return svc.PrecheckShardCohesion(ctx, p, canonical)
		},
		"ResolveCanonicalSchema": func(p string) error {
			_, err := svc.ResolveCanonicalSchema(ctx, p)
			return err
		},
		"ResolveCanonicalSchema anchor": func(p string) error {
			_, err := svc.ResolveCanonicalSchema(ctx, p+"#s00.pulse")
			return err
		},
		"VerifyShardArchive": func(p string) error {
			res, err := svc.VerifyShardArchive(ctx, p)
			if err == nil && len(res.Errors) > 0 {
				return res.Errors[0]
			}
			return err
		},
		"ExtractShard (open + close)": func(p string) error {
			rc, err := svc.ExtractShard(ctx, p, "s03.pulse")
			if err != nil {
				return err
			}
			return rc.Close()
		},
	}
	for name, fn := range surfaces {
		t.Run(name, func(t *testing.T) {
			var runErr error
			measure := func(p string) uint64 {
				return allocatedBytes(t, func() {
					if err := fn(p); err != nil && runErr == nil {
						runErr = err
					}
				})
			}
			small, large := measure("small.pulse"), measure("large.pulse")
			if runErr != nil {
				t.Fatal(runErr)
			}
			// O(shards): the 10x payload moves the figure by at most a
			// small constant (a map or slice growing differently), never
			// in proportion to the bytes.
			if large > small+(64<<10) {
				t.Errorf("allocated %d B on the 10x archive vs %d B on the small one: grows with the archive's bytes", large, small)
			}
			if large*4 > uint64(largeSize) {
				t.Errorf("allocated %d B opening a %d B archive: not header-only", large, largeSize)
			}
		})
	}
}

// TestArchiveOpen_ScanReadsByRandomAccess (FR-2): a Process over an
// archive — Open plus the scan, on the serial iterator and the
// per-shard parallel reducer — reads the archive's bytes ONCE: Open
// touches only the magic, the central directory and the headers, and
// the scan reads each shard's payload through its own section. The
// pre-U51 path slurped the archive in Open and again in the scan (2x).
func TestArchiveOpen_ScanReadsByRandomAccess(t *testing.T) {
	mem := afero.NewMemMapFs()
	size := writeDictArchive(t, mem, "large.pulse", 4, 20_000, 4)
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "large.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x0", Label: "n"}},
	}
	for _, workers := range []int{1, 4} {
		cfs := newCountingFs(mem)
		cfg, err := fs.New(fs.WithFs(cfs))
		if err != nil {
			t.Fatal(err)
		}
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		resp, err := svc.Process(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if n := fmt.Sprint(resp.Data[0]["n"]); n != "80000" {
			t.Fatalf("ShardWorkers %d: count %v, want 80000", workers, n)
		}
		read := cfs.snapshot().BytesRead["large.pulse"]
		if read < size*9/10 || read > size*11/10 {
			t.Errorf("ShardWorkers %d: read %d B of a %d B archive, want the archive once", workers, read, size)
		}
	}
}

// deflateEntry rewrites the archive at path on mem with entry name
// re-encoded as a DEFLATED zip entry — what a third-party zip tool
// produces; Pulse writes only store-only entries.
func deflateEntry(t *testing.T, mem afero.Fs, path, name string) {
	t.Helper()
	data, err := afero.ReadFile(mem, path)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		method := zip.Store
		if f.Name == name {
			method = zip.Deflate
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: method})
		if err != nil {
			t.Fatal(err)
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(mem, path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestArchiveOpen_DeflatedShardRefused (FR-6): the serial shard
// iterator and the per-shard parallel reducer refuse a deflated
// (non-store) shard entry with PULSE_ARCHIVE_CORRUPT naming it — the
// record reader's rule — instead of decoding its compressed bytes as
// records.
func TestArchiveOpen_DeflatedShardRefused(t *testing.T) {
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "d.pulse", 3, 50, 4)
	deflateEntry(t, mem, "d.pulse", "s01.pulse")
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, workers := range []int{1, 3} {
		svc := New(cfg)
		svc.SetShardWorkers(workers)
		_, err := svc.Process(ctx, dictArchiveRequest("d.pulse", false))
		var ce *errors.CodedError
		if !asCodedError(err, &ce) || ce.Code != errors.PULSE_ARCHIVE_CORRUPT {
			t.Fatalf("ShardWorkers %d: err = %v, want PULSE_ARCHIVE_CORRUPT", workers, err)
		}
		if ce.Details["entry"] != "s01.pulse" {
			t.Errorf("ShardWorkers %d: details %v, want entry s01.pulse", workers, ce.Details)
		}
	}
}

// TestArchiveOpen_LockedReaderAtShortReadAtEOF (FR-5): the in-memory
// file answers a ReadAt straddling end-of-file with a short count and a
// NIL error; the concurrency-safe wrapper restores the io.ReaderAt
// contract (n < len(p) ⇒ err != nil, io.EOF at the end).
func TestArchiveOpen_LockedReaderAtShortReadAtEOF(t *testing.T) {
	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "f", []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := mem.Open("f")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ra := concurrentReaderAt(f)
	if _, ok := ra.(*lockedReaderAt); !ok {
		t.Fatalf("MemMapFs file wrapped as %T, want *lockedReaderAt", ra)
	}
	cases := []struct {
		off   int64
		n     int
		want  string
		isEOF bool
	}{
		{off: 0, n: 4, want: "0123"},
		{off: 6, n: 4, want: "6789"},
		{off: 6, n: 8, want: "6789", isEOF: true},
		{off: 10, n: 2, want: "", isEOF: true},
	}
	for _, c := range cases {
		p := make([]byte, c.n)
		n, err := ra.ReadAt(p, c.off)
		if string(p[:n]) != c.want {
			t.Errorf("ReadAt(%d bytes @%d) = %q, want %q", c.n, c.off, p[:n], c.want)
		}
		if c.isEOF != (err == io.EOF) || (!c.isEOF && err != nil) {
			t.Errorf("ReadAt(%d bytes @%d) err = %v, want EOF %v", c.n, c.off, err, c.isEOF)
		}
	}
}

// TestArchiveOpen_SectionScannerSeeksForward: the shard reader's
// forward Seek (a projected decode's SkipBytes) discards from the
// buffer and, like the *bytes.Reader it replaces, treats a seek past
// the end as positioning at EOF rather than an error.
func TestArchiveOpen_SectionScannerSeeksForward(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefghij"), 50_000) // 500 KB > one buffer
	sc := newSectionScanner(io.NewSectionReader(bytes.NewReader(payload), 0, int64(len(payload))))
	if pos, err := sc.Seek(300_003, io.SeekCurrent); err != nil || pos != 300_003 {
		t.Fatalf("Seek = %d, %v", pos, err)
	}
	var b [2]byte
	if _, err := io.ReadFull(sc, b[:]); err != nil || string(b[:]) != "de" {
		t.Fatalf("after seek read %q, %v; want \"de\"", b, err)
	}
	if _, err := sc.Seek(-1, io.SeekCurrent); err == nil {
		t.Error("a backward seek must be refused")
	}
	if _, err := sc.Seek(1<<20, io.SeekCurrent); err != nil {
		t.Fatalf("seek past end: %v", err)
	}
	if n, err := sc.Read(b[:]); n != 0 || err != io.EOF {
		t.Fatalf("read past end = %d, %v; want 0, EOF", n, err)
	}
}

// TestArchiveOpen_AnchorServesSection: an anchor-opened stored shard
// is served from a section of the archive (no shard bytes copied at
// open) and scans to the same answer as the shard alone.
func TestArchiveOpen_AnchorServesSection(t *testing.T) {
	mem := afero.NewMemMapFs()
	writeDictArchive(t, mem, "a.pulse", 3, 40, 4)
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	ctx := context.Background()
	cohort, err := svc.Open(ctx, "a.pulse#s02.pulse")
	if err != nil {
		t.Fatal(err)
	}
	ov, ok := cohort.fs.(*anchorOverlay)
	if !ok {
		t.Fatalf("anchor fs = %T, want *anchorOverlay", cohort.fs)
	}
	if ov.payload != nil || ov.archivePath != "a.pulse" {
		t.Fatalf("anchor overlay holds %d payload bytes over archive %q, want a section of a.pulse and no copy", len(ov.payload), ov.archivePath)
	}
	req := &types.Request{
		Cohort:       &types.Cohort{Filename: "a.pulse#s02.pulse"},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x0", Label: "n"}},
	}
	resp, err := svc.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if n := fmt.Sprint(resp.Data[0]["n"]); n != "40" {
		t.Fatalf("anchored count %v, want 40", n)
	}
	// The anchored entry, extracted, is the same bytes the section serves.
	rc, err := svc.ExtractShard(ctx, "a.pulse", "s02.pulse")
	if err != nil {
		t.Fatal(err)
	}
	want, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, err := afero.ReadFile(cohort.fs, cohort.path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("anchor section serves %d B, the entry holds %d B", len(got), len(want))
	}
}
