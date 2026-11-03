package service

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/afero"
	"golang.org/x/sys/windows"

	"github.com/frankbardon/pulse/internal/fs"
)

// mmap_windows_test.go is the Windows-specific coverage of
// mmap_windows.go (U77 E2-S1): the CreateFileMappingW + MapViewOfFile
// fast path maps the file's exact bytes, its cleanup releases the file
// so a rename over it and a delete of it succeed, the streaming
// iterator and the parallel decode both engage it, and the mapped
// region is counted by noteMapped. The unix twin of the engagement
// checks is fs_probe_test.go / fs_counting_test.go, which run here too
// (mmapSupported is true on this build).

func winWriteFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestMmapWindows_MapsBytesAndCleanupReleases: the view holds the file's
// exact bytes, and after cleanup the file is free — a rename over it
// and a remove of it both succeed.
//
// Falsified by dropping UnmapViewOfFile (or the section CloseHandle)
// from the cleanup: Windows then refuses the rename over the
// still-mapped file (TestMmapWindows_HeldViewOutcome records that
// refusal directly).
func TestMmapWindows_MapsBytesAndCleanupReleases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "c.pulse")
	want := bytes.Repeat([]byte("pulse-mmap-"), 4096)
	winWriteFile(t, path, want)

	data, cleanup, err := mmapFileBytes(path)
	if err != nil {
		t.Fatalf("mmapFileBytes: %v", err)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("mapped %d bytes, want the file's %d bytes", len(data), len(want))
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	next := filepath.Join(dir, "next.pulse")
	winWriteFile(t, next, []byte("replacement"))
	if err := os.Rename(next, path); err != nil {
		t.Fatalf("rename over the file after cleanup: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove after cleanup: %v", err)
	}
}

// TestMmapWindows_EmptyAndSpecialFilesFallBack: an empty file maps to a
// nil slice with a no-op cleanup (the unix contract); a directory is
// refused so the caller falls back to afero.ReadFile.
func TestMmapWindows_EmptyAndSpecialFilesFallBack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.pulse")
	winWriteFile(t, empty, nil)
	data, cleanup, err := mmapFileBytes(empty)
	if err != nil || data != nil || cleanup == nil {
		t.Fatalf("empty file: data=%v cleanup-nil=%v err=%v, want nil, a no-op cleanup, nil", data, cleanup == nil, err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("no-op cleanup: %v", err)
	}
	if err := os.Remove(empty); err != nil {
		t.Fatalf("an empty file stays unheld: %v", err)
	}
	if _, _, err := mmapFileBytes(dir); err == nil {
		t.Fatal("mapping a directory succeeded, want an error (the caller falls back)")
	}
	if _, _, err := mmapFileBytes(filepath.Join(dir, "missing.pulse")); err == nil {
		t.Fatal("mapping a missing file succeeded")
	}
}

// TestMmapWindows_IteratorMapsAndNoteMappedCounts: over a real-path fs
// the streaming iterator serves the cohort from a view, and the counting
// wrapper charges exactly the mapped region through noteMapped (no Read
// went through an afero handle). Closing the iterator releases the view.
//
// Falsified by deleting the noteMapped call in stream.go (bytes read
// stays 0) or by reverting mmap_windows.go (mmapBytes stays nil).
func TestMmapWindows_IteratorMapsAndNoteMappedCounts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := filepath.Join(dir, "bench.pulse")
	schema, err := writeBenchPulseFile(osFs, path, shape1Num, 64)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ei := &ExecInfo{}
	counting := &execCountingFs{Fs: afero.NewBasePathFs(osFs, dir), ei: ei}
	iter := newStreamingIterator(counting, "bench.pulse", schema)
	n := 0
	for iter.Next() {
		n++
	}
	if err := iter.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if n != 64 {
		t.Errorf("iterated %d records, want 64", n)
	}
	if iter.mmapBytes == nil || iter.mmapCleanup == nil {
		t.Fatal("the iterator did not map the cohort on Windows")
	}
	if got := ei.bytesRead.Load(); got != info.Size() {
		t.Errorf("noteMapped charged %d bytes, want the mapped file's %d", got, info.Size())
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove after the iterator closed (view released): %v", err)
	}
}

// TestMmapWindows_ParallelDecodeContextEngages: the parallel decode's
// precondition — a view of the whole cohort — now holds on Windows, so
// canParallelDecode's mergeable requests take the parallel_decode arm
// (the R3 inventory class); its cleanup releases the view.
func TestMmapWindows_ParallelDecodeContextEngages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	schema, err := writeBenchPulseFile(osFs, filepath.Join(dir, "bench.pulse"), shape1Num, 128)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(afero.NewBasePathFs(osFs, dir)))
	if err != nil {
		t.Fatal(err)
	}
	svc := New(cfg)
	pctx, cleanup, ok, err := buildParallelDecodeContext(svc, "bench.pulse", schema, nil, nil, len(schema.Fields))
	if err != nil || !ok {
		t.Fatalf("buildParallelDecodeContext: ok=%v err=%v, want a mapped context", ok, err)
	}
	if pctx.totalRecords != 128 {
		t.Errorf("totalRecords = %d, want 128", pctx.totalRecords)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "bench.pulse")); err != nil {
		t.Fatalf("remove after cleanup: %v", err)
	}
}

// TestMmapWindows_HeldViewOutcome pins what Windows does to a rename
// over, and a delete of, a file whose view is still mapped — the
// situation of another reader holding a view while Pulse rewrites the
// cohort — for each share mode the view's file could have been opened
// with (Pulse's own mmapShareMode; Go's os.Open default; a read-only
// opener). A rename over it (MoveFileEx, what os.Rename and every afero
// rename call) is refused in every mode, so a rewrite fails with its
// coded rename error; a delete succeeds only when the view's file
// allows FILE_SHARE_DELETE (Pulse's views do), and the view keeps the
// old bytes either way.
func TestMmapWindows_HeldViewOutcome(t *testing.T) {
	t.Parallel()

	modes := []struct {
		name        string
		share       uint32
		removeLives bool
	}{
		{"pulse_read_write_delete", mmapShareMode, true},
		{"go_default_read_write", windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE, false},
		{"read_only", windows.FILE_SHARE_READ, false},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "c.pulse")
			old := bytes.Repeat([]byte("old!"), 1024)
			winWriteFile(t, path, old)
			data, cleanup, err := mmapFileShared(path, m.share)
			if err != nil {
				t.Fatalf("map: %v", err)
			}
			next := filepath.Join(dir, "next.pulse")
			winWriteFile(t, next, []byte("new"))
			renameErr := os.Rename(next, path)
			removeErr := os.Remove(path)
			viewIntact := bytes.Equal(data, old)
			if err := cleanup(); err != nil {
				t.Errorf("cleanup: %v", err)
			}
			if renameErr == nil {
				t.Error("a rename over a mapped file succeeded; the documented contract says Windows refuses it")
			}
			if (removeErr == nil) != m.removeLives {
				t.Errorf("remove of a mapped file = %v, want success %v", removeErr, m.removeLives)
			}
			if !viewIntact {
				t.Error("the view stopped reading the old bytes")
			}
		})
	}
}
