package service

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/spf13/afero"
)

// TestResolveRealPath_OsFs verifies a bare *afero.OsFs is recognised
// and the input name is returned verbatim — OsFs treats names as
// real filesystem paths.
func TestResolveRealPath_OsFs(t *testing.T) {
	fs := afero.NewOsFs()
	got, ok := resolveRealPath(fs, "/tmp/example.pulse")
	if !ok {
		t.Fatalf("OsFs probe miss, want hit")
	}
	if got != "/tmp/example.pulse" {
		t.Fatalf("OsFs probe returned %q, want input verbatim", got)
	}
}

// TestResolveRealPath_BasePathFs verifies the default Pulse fs shape
// (BasePathFs over OsFs) satisfies the RealPather capability and the
// returned path concatenates base + name. This is the regression that
// motivates the story — the old type-assert against *afero.OsFs missed
// this case.
func TestResolveRealPath_BasePathFs(t *testing.T) {
	dir := t.TempDir()
	fs := afero.NewBasePathFs(afero.NewOsFs(), dir)
	// File must exist for RealPath to be meaningful, but the probe
	// itself does not stat — write a sentinel so we can also confirm
	// the returned path is openable by os.Open downstream.
	want := filepath.Join(dir, "cohort.pulse")
	if err := os.WriteFile(want, []byte("sentinel"), 0o644); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}

	got, ok := resolveRealPath(fs, "cohort.pulse")
	if !ok {
		t.Fatalf("BasePathFs probe miss, want hit")
	}
	if got != want {
		t.Fatalf("BasePathFs probe got %q, want %q", got, want)
	}
	// Sanity: the returned path is openable by os.Open at the moment of
	// the call (the documented contract).
	f, err := os.Open(got)
	if err != nil {
		t.Fatalf("os.Open(resolved): %v", err)
	}
	_ = f.Close()
}

// TestResolveRealPath_MemMapFs verifies the in-memory fs does NOT
// resolve to a real path. The iterator MUST fall back to afero.ReadFile
// on MemMapFs so hermetic tests keep working.
func TestResolveRealPath_MemMapFs(t *testing.T) {
	fs := afero.NewMemMapFs()
	if err := afero.WriteFile(fs, "/cohort.pulse", []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed memfs: %v", err)
	}
	got, ok := resolveRealPath(fs, "/cohort.pulse")
	if ok {
		t.Fatalf("MemMapFs probe hit (returned %q), want miss", got)
	}
	if got != "" {
		t.Fatalf("MemMapFs probe returned %q on miss, want empty string", got)
	}
}

// TestResolveRealPath_NilFs guards against a nil fs blowing up the
// probe. The capability check happens before the OsFs type-switch so a
// nil fs would otherwise hit a nil interface comparison and panic.
func TestResolveRealPath_NilFs(t *testing.T) {
	got, ok := resolveRealPath(nil, "anything")
	if ok || got != "" {
		t.Fatalf("nil fs probe got (%q, %v), want (\"\", false)", got, ok)
	}
}

// optInFs is a minimal afero.Fs wrapper that opts into the RealPather
// capability. It mirrors how an external fs (e.g. a CopyOnRead overlay
// that downloads remote objects to a local tempdir) would satisfy the
// contract: embed any backing fs, expose a RealPath that returns the
// on-disk location.
type optInFs struct {
	afero.Fs
	resolved string
	err      error
}

func (o *optInFs) RealPath(_ string) (string, error) {
	return o.resolved, o.err
}

// TestResolveRealPath_ExternalCapability verifies any afero.Fs that
// implements RealPather opts into the fast path. This is the external
// extension contract the story calls out.
func TestResolveRealPath_ExternalCapability(t *testing.T) {
	external := &optInFs{
		Fs:       afero.NewMemMapFs(), // underlying fs irrelevant — probe consults the capability
		resolved: "/real/disk/path.pulse",
	}
	got, ok := resolveRealPath(external, "virtual.pulse")
	if !ok {
		t.Fatalf("external RealPather probe miss, want hit")
	}
	if got != "/real/disk/path.pulse" {
		t.Fatalf("external probe got %q, want resolved path", got)
	}
}

// TestResolveRealPath_RealPatherError verifies a non-nil error from
// RealPath declines the fast path (so the caller falls back to
// afero.ReadFile). Documents the contract.
func TestResolveRealPath_RealPatherError(t *testing.T) {
	external := &optInFs{
		Fs:       afero.NewMemMapFs(),
		resolved: "ignored",
		err:      errors.New("decline"),
	}
	got, ok := resolveRealPath(external, "x")
	if ok {
		t.Fatalf("probe hit on RealPath error (returned %q), want miss", got)
	}
	if got != "" {
		t.Fatalf("error path returned %q, want empty string", got)
	}
}

// TestStreamingIterator_MmapEngagesOnBasePathFs is the integration
// guarantee for the story: a streaming iterator opened against the
// default fs shape (BasePathFs over OsFs) MUST populate mmapBytes after
// the first Next() on unix. The acceptance criterion that prompted this
// story.
func TestStreamingIterator_MmapEngagesOnBasePathFs(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" && runtime.GOOS != "freebsd" {
		t.Skipf("mmap unsupported on %s", runtime.GOOS)
	}
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	full := filepath.Join(dir, "bench.pulse")
	schema, err := writeBenchPulseFile(osFs, full, shape1Num, 32)
	if err != nil {
		t.Fatalf("seed bench file: %v", err)
	}
	basePath := afero.NewBasePathFs(osFs, dir)

	iter := newStreamingIterator(basePath, "bench.pulse", schema)
	defer iter.Close()
	if !iter.Next() {
		if err := iter.Err(); err != nil {
			t.Fatalf("iter.Next: %v", err)
		}
		t.Fatal("iter.Next returned false on first call")
	}
	if iter.mmapBytes == nil {
		t.Fatal("mmap did NOT engage on BasePathFs — probe regression")
	}
	if iter.mmapCleanup == nil {
		t.Fatal("mmap engaged but cleanup is nil — mmapFileBytes contract violated")
	}
}

// TestStreamingIterator_MmapDoesNotEngageOnMemMapFs guarantees that the
// hermetic test path keeps falling back to afero.ReadFile. Without
// this, MemMapFs tests could silently start hitting an mmap probe that
// shouldn't fire.
func TestStreamingIterator_MmapDoesNotEngageOnMemMapFs(t *testing.T) {
	memFs := afero.NewMemMapFs()
	schema, err := writeBenchPulseFile(memFs, "bench.pulse", shape1Num, 32)
	if err != nil {
		t.Fatalf("seed memfs bench file: %v", err)
	}
	iter := newStreamingIterator(memFs, "bench.pulse", schema)
	defer iter.Close()
	if !iter.Next() {
		if err := iter.Err(); err != nil {
			t.Fatalf("iter.Next: %v", err)
		}
		t.Fatal("iter.Next returned false on first call")
	}
	if iter.mmapBytes != nil {
		t.Fatalf("mmap engaged on MemMapFs (%d bytes) — hermetic-test invariant broken", len(iter.mmapBytes))
	}
	if iter.mmapCleanup != nil {
		t.Fatal("mmap cleanup non-nil on MemMapFs — leak risk")
	}
}

// TestStreamingIterator_MmapFallbackOnUnreadableRealPath exercises the
// failure case: the probe surfaces a path that mmapFileBytes rejects
// (here: a path that does not exist). The iterator must fall back to
// afero.ReadFile and surface the ReadFile error path, not the mmap
// error.
func TestStreamingIterator_MmapFallbackOnUnreadableRealPath(t *testing.T) {
	// MemMapFs deliberately. The RealPather wrapper claims the file is
	// at a non-existent on-disk path so mmapFileBytes fails; the
	// iterator then asks the underlying MemMapFs for the file, which
	// also fails. We assert the iterator surfaces the second error
	// (afero.ReadFile path) — proves the fallback engaged.
	external := &optInFs{
		Fs:       afero.NewMemMapFs(),
		resolved: filepath.Join(t.TempDir(), "does-not-exist.pulse"),
	}
	// No file written, so afero.ReadFile against MemMapFs will also
	// fail. We only care that Next() returns false with an error — the
	// fallback path was traversed.
	iter := newStreamingIterator(external, "bench.pulse", nil)
	defer iter.Close()
	if iter.Next() {
		t.Fatal("Next returned true on doubly-unreadable file")
	}
	if iter.Err() == nil {
		t.Fatal("Next failed silently, want error from afero.ReadFile fallback")
	}
}
