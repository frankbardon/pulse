package pulse

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func TestWatch_CreatedAndModified(t *testing.T) {
	t.Parallel()

	memFs := afero.NewMemMapFs()
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := p.WatchWithOptions(ctx, "watched.bin", WatchOptions{
		PollInterval:   25 * time.Millisecond,
		CoalesceWindow: 5 * time.Millisecond,
	})

	// Initial create.
	_ = afero.WriteFile(memFs, "watched.bin", []byte("first"), 0o644)
	got := waitForEvent(t, ch, 2*time.Second)
	if got.Kind != ChangeCreated || got.Path != "watched.bin" || got.Hash == "" {
		t.Fatalf("first event = %+v, want Created with hash", got)
	}

	// Modification.
	_ = afero.WriteFile(memFs, "watched.bin", []byte("second"), 0o644)
	got = waitForEvent(t, ch, 2*time.Second)
	if got.Kind != ChangeModified || got.Hash == "" {
		t.Fatalf("second event = %+v, want Modified with hash", got)
	}

	// Removal.
	_ = memFs.Remove("watched.bin")
	got = waitForEvent(t, ch, 2*time.Second)
	if got.Kind != ChangeRemoved || got.Hash != "" {
		t.Fatalf("third event = %+v, want Removed", got)
	}
}

func TestWatchDir_OnlyPulseSuffix(t *testing.T) {
	t.Parallel()

	memFs := afero.NewMemMapFs()
	_ = memFs.MkdirAll("d", 0o755)
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := p.WatchDirWithOptions(ctx, "d", WatchOptions{
		PollInterval:   25 * time.Millisecond,
		CoalesceWindow: 5 * time.Millisecond,
		Suffix:         ".pulse",
	})

	// Non-matching file: must not surface.
	_ = afero.WriteFile(memFs, "d/notes.txt", []byte("ignored"), 0o644)
	// Matching file: must surface.
	_ = afero.WriteFile(memFs, "d/cohort.pulse", []byte("x"), 0o644)

	got := waitForEvent(t, ch, 2*time.Second)
	// Dir-mode paths are OS paths: the root joined (filepath.Join) with
	// each entry name — d\cohort.pulse on Windows.
	if want := filepath.Join("d", "cohort.pulse"); got.Path != want || got.Kind != ChangeCreated {
		t.Fatalf("event = %+v, want %s Created", got, want)
	}
}

func waitForEvent(t *testing.T, ch <-chan ChangeEvent, timeout time.Duration) ChangeEvent {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatalf("channel closed before event arrived")
		}
		return e
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for change event")
	}
	return ChangeEvent{}
}

// TestLooksLikeTempSibling_OSPaths: the rename coalescer compares base
// names of the OS paths the dir-mode walk builds (filepath.Join), so a
// dot-prefixed editor temp file is recognised whatever the separator.
// With path.Base a Windows path (d\.cohort.pulse) kept its directory and
// the "." prefix never matched.
func TestLooksLikeTempSibling_OSPaths(t *testing.T) {
	t.Parallel()

	target := filepath.Join("d", "cohort.pulse")
	for _, tc := range []struct {
		temp string
		want bool
	}{
		{filepath.Join("d", ".cohort.pulse"), true},
		{filepath.Join("d", "cohort.pulse.tmp"), true},
		{filepath.Join("d", "cohort.pulse.1234"), true},
		{filepath.Join("d", "other.pulse"), false},
		{target, false},
	} {
		if got := looksLikeTempSibling(tc.temp, target); got != tc.want {
			t.Errorf("looksLikeTempSibling(%q, %q) = %v, want %v", tc.temp, target, got, tc.want)
		}
	}
}
