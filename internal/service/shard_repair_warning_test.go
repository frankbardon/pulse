package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// shard_repair_warning_test.go pins U73 E5-S1: a repair that truncates
// an interrupted append rewrites the archive, so it reports ONE
// mandatory PULSE_SHARD_APPEND_ROLLED_BACK warning on the result — like
// every other unasked archive rewrite — and a repair that truncates
// nothing reports none.

// TestShardRepair_WarnsOnceWhenItTruncates: the torn tail is rolled
// back and the result carries exactly one PULSE_SHARD_APPEND_ROLLED_BACK
// naming the archive, the journal, the shard and the bytes dropped.
func TestShardRepair_WarnsOnceWhenItTruncates(t *testing.T) {
	t.Parallel()

	svc, _, before := repairFixture(t)
	res, err := svc.RepairShardArchive(context.Background(), "arch.pulse")
	if err != nil {
		t.Fatalf("RepairShardArchive: %v", err)
	}
	if !res.Repaired {
		t.Fatalf("result = %+v, want Repaired", res)
	}
	if n := countCode(res.Warnings, errors.PULSE_SHARD_APPEND_ROLLED_BACK); n != 1 || len(res.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want exactly one PULSE_SHARD_APPEND_ROLLED_BACK", res.Warnings)
	}
	d := res.Warnings[0].Details
	want := map[string]any{
		"archive":         "arch.pulse",
		"journal":         "arch.pulse.append",
		"shard":           "b.pulse",
		"size_before":     int64(len(before) + 300),
		"archive_size":    int64(len(before)),
		"truncated_bytes": int64(300),
	}
	for k, v := range want {
		if d[k] != v {
			t.Errorf("details[%q] = %v (%T), want %v", k, d[k], d[k], v)
		}
	}
}

// TestShardRepair_NoWarningWhenNothingTruncated: a repair with no
// journal, and one whose journal matches an archive the crash left
// untouched (no appended byte), truncate nothing and warn nothing; the
// Warnings slice is empty, never nil.
func TestShardRepair_NoWarningWhenNothingTruncated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	t.Run("no_journal", func(t *testing.T) {
		svc, fsys, before := repairFixture(t)
		if err := afero.WriteFile(fsys, "arch.pulse", before, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := fsys.Remove(encx.AppendJournalPath("arch.pulse")); err != nil {
			t.Fatal(err)
		}
		res, err := svc.RepairShardArchive(ctx, "arch.pulse")
		if err != nil || res.Repaired {
			t.Fatalf("repair = %+v, %v; want a no-op", res, err)
		}
		if res.Warnings == nil || len(res.Warnings) != 0 {
			t.Errorf("warnings = %#v, want an empty slice", res.Warnings)
		}
	})
	t.Run("journal_no_tail", func(t *testing.T) {
		svc, fsys, before := repairFixture(t)
		if err := afero.WriteFile(fsys, "arch.pulse", before, 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := svc.RepairShardArchive(ctx, "arch.pulse")
		if err != nil || !res.Repaired {
			t.Fatalf("repair = %+v, %v; want the journal cleared", res, err)
		}
		if len(res.Warnings) != 0 {
			t.Errorf("warnings = %+v, want none: nothing was truncated", res.Warnings)
		}
		if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), before) {
			t.Error("the archive changed")
		}
	})
}
