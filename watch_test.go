package pulse

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/afero"
)

func TestWatch_CreatedAndModified(t *testing.T) {
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
	if got.Path != "d/cohort.pulse" || got.Kind != ChangeCreated {
		t.Fatalf("event = %+v, want d/cohort.pulse Created", got)
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
