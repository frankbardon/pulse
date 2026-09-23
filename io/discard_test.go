package io

import (
	"errors"
	"testing"
)

type plainWriter struct{ closed bool }

func (w *plainWriter) WriteHeader([]string) error { return nil }
func (w *plainWriter) WriteRow([]any) error       { return nil }
func (w *plainWriter) Close() error               { w.closed = true; return nil }

type discardingWriter struct {
	plainWriter
	discarded bool
	err       error
}

func (w *discardingWriter) Discard() error { w.discarded = true; return w.err }

// TestDiscardWriter_OnlyTouchesDiscardableTargets pins the helper the io
// CLI leaves call on their error return. A writer with no Discard must be
// left ALONE — calling Close on it is exactly the close-on-error that
// would write a zero-row target next to a hard error.
func TestDiscardWriter_OnlyTouchesDiscardableTargets(t *testing.T) {
	plain := &plainWriter{}
	if err := DiscardWriter(plain); err != nil {
		t.Errorf("DiscardWriter(plain) = %v, want nil", err)
	}
	if plain.closed {
		t.Error("DiscardWriter closed a non-discardable target; that would emit its file")
	}

	d := &discardingWriter{}
	if err := DiscardWriter(d); err != nil {
		t.Errorf("DiscardWriter(discardable) = %v, want nil", err)
	}
	if !d.discarded {
		t.Error("DiscardWriter did not call Discard on a discardable target")
	}
	if d.closed {
		t.Error("DiscardWriter called Close; release must never emit")
	}

	boom := errors.New("boom")
	if err := DiscardWriter(&discardingWriter{err: boom}); !errors.Is(err, boom) {
		t.Errorf("DiscardWriter swallowed the release error: %v", err)
	}

	if err := DiscardWriter(nil); err != nil {
		t.Errorf("DiscardWriter(nil) = %v, want nil", err)
	}
}
