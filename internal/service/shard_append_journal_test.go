package service

import (
	"archive/zip"
	"bytes"
	"context"
	stderrors "errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// shard_append_journal_test.go pins the crash story of the in-place
// tail append (U73 E2-S2): the append journals the archive's old length
// and tail checksum in `<archive>.append` before its first byte and
// removes it after the append is synced; a surviving journal makes the
// archive refused (PULSE_SHARD_APPEND_INCOMPLETE) until
// RepairShardArchive truncates it back — byte-identical to before the
// append — and a journal that does not match the archive is refused
// (PULSE_SHARD_REPAIR_REFUSED) rather than truncated blindly.

// osService is a Service over a real directory.
func osService(t *testing.T, dir string) (*Service, afero.Fs) {
	t.Helper()
	cfg, err := fs.New(fs.WithFs(afero.NewBasePathFs(afero.NewOsFs(), dir)))
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg), cfg.Fs()
}

// crashPointWriter passes every byte through and, the moment the bytes
// written reach one of points (offsets from the append's start), calls
// snap with that point's phase: the file then holds exactly the bytes a
// crash at that offset would leave.
type crashPointWriter struct {
	w      io.Writer
	n      int64
	points []crashPoint
	snap   func(phase string)
}

type crashPoint struct {
	at    int64
	phase string
}

func (c *crashPointWriter) Write(p []byte) (int, error) {
	written := 0
	for len(c.points) > 0 && c.points[0].at <= c.n+int64(len(p)) {
		cut := int(c.points[0].at - c.n)
		m, err := c.w.Write(p[:cut])
		written += m
		c.n += int64(m)
		if err != nil {
			return written, err
		}
		p = p[cut:]
		c.snap(c.points[0].phase)
		c.points = c.points[1:]
	}
	m, err := c.w.Write(p)
	c.n += int64(m)
	return written + m, err
}

type crashSnapshot struct {
	archive, journal []byte
}

func requireCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("err = %v, want %s", err, code)
	}
	return ce
}

// TestShardAppend_CrashPhasesRefusedAndRepaired: a crash at every phase
// of the append — the journal durable and nothing appended, mid-shard,
// mid-`_schema.pulse`, mid-directory, and every byte synced but the
// journal not yet removed — leaves a journal, so Open refuses the
// archive with PULSE_SHARD_APPEND_INCOMPLETE; the repair truncates it
// to the bytes it had before the append, removes the journal, and the
// archive opens again. The appended shard is larger than the 64 KiB a
// zip reader searches, so a torn directory leaves the archive
// unopenable — the hazard the journal exists for.
func TestShardAppend_CrashPhasesRefusedAndRepaired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	schema := adminScoreSchema()
	fixture := func(dir string) (*Service, afero.Fs) {
		svc, fsys := osService(t, dir)
		adminWriteShardFile(t, fsys, "a.pulse", schema, streamShardRecords(0, 10))
		adminWriteShardFile(t, fsys, "b.pulse", schema, streamShardRecords(100, 12))
		adminWriteShardFile(t, fsys, "c.pulse", schema, streamShardRecords(1000, 8000))
		if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
			t.Fatal(err)
		}
		return svc, fsys
	}

	// A dry run of the same append fixes where each phase's bytes sit.
	drySvc, dryFs := fixture(t.TempDir())
	before := gReadFile(t, dryFs, "arch.pulse")
	if _, err := drySvc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatal(err)
	}
	after := gReadFile(t, dryFs, "arch.pulse")
	if len(after)-len(before) <= 1<<16 {
		t.Fatalf("fixture: append of %d bytes is inside the EOCD search window", len(after)-len(before))
	}
	zr, err := zip.NewReader(bytes.NewReader(after), int64(len(after)))
	if err != nil {
		t.Fatal(err)
	}
	var points []crashPoint
	for _, f := range zr.File {
		off, err := f.DataOffset()
		if err != nil {
			t.Fatal(err)
		}
		switch f.Name {
		case "c.pulse":
			points = append(points, crashPoint{off + int64(f.UncompressedSize64)/2, "mid-shard"})
		case encx.ReservedSchemaName:
			points = append(points, crashPoint{off + int64(f.UncompressedSize64)/2, "mid-schema"})
		}
	}
	d, err := encx.ReadArchiveDirectory(bytes.NewReader(after), int64(len(after)))
	if err != nil {
		t.Fatal(err)
	}
	points = append(points, crashPoint{d.DirOffset + d.DirSize/2, "mid-directory"})
	for i := range points {
		points[i].at -= int64(len(before))
	}
	sort.Slice(points, func(i, j int) bool { return points[i].at < points[j].at })

	dir := t.TempDir()
	svc, fsys := fixture(dir)
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), before) {
		t.Fatal("fixture: the two archives differ")
	}
	snaps := map[string]crashSnapshot{}
	snap := func(phase string) {
		a, err := os.ReadFile(filepath.Join(dir, "arch.pulse"))
		if err != nil {
			t.Errorf("%s: %v", phase, err)
		}
		j, _ := os.ReadFile(filepath.Join(dir, "arch.pulse"+encx.AppendJournalSuffix))
		snaps[phase] = crashSnapshot{archive: a, journal: j}
	}
	svc.appendSeam = &appendSeam{
		wrap: func(w io.Writer) io.Writer { return &crashPointWriter{w: w, points: points, snap: snap} },
		at:   snap,
	}
	if _, err := svc.AddShard(ctx, "arch.pulse", "c.pulse"); err != nil {
		t.Fatalf("AddShard: %v", err)
	}
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), after) {
		t.Fatal("the observed append differs from the dry run")
	}
	if ok, _ := afero.Exists(fsys, "arch.pulse"+encx.AppendJournalSuffix); ok {
		t.Fatal("a completed append left its journal behind")
	}

	for _, phase := range []string{"journalled", "mid-shard", "mid-schema", "mid-directory", "synced"} {
		t.Run(phase, func(t *testing.T) {
			s, ok := snaps[phase]
			if !ok {
				t.Fatalf("no crash snapshot at %s", phase)
			}
			if len(s.journal) == 0 {
				t.Fatalf("no journal on disk at %s", phase)
			}
			crash := t.TempDir()
			for name, b := range map[string][]byte{"arch.pulse": s.archive, "arch.pulse.append": s.journal} {
				if err := os.WriteFile(filepath.Join(crash, name), b, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "mid-directory" {
				if _, err := zip.NewReader(bytes.NewReader(s.archive), int64(len(s.archive))); err == nil {
					t.Error("a torn directory past the search window still opened: the fixture shows no hazard")
				}
			}
			csvc, cfs := osService(t, crash)
			_, err := csvc.Open(ctx, "arch.pulse")
			ce := requireCode(t, err, errors.PULSE_SHARD_APPEND_INCOMPLETE)
			if ce.Details["journal"] != "arch.pulse.append" {
				t.Errorf("details = %v", ce.Details)
			}
			_, err = csvc.AddShard(ctx, "arch.pulse", "arch.pulse")
			requireCode(t, err, errors.PULSE_SHARD_APPEND_INCOMPLETE)

			res, err := csvc.RepairShardArchive(ctx, "arch.pulse")
			if err != nil {
				t.Fatalf("RepairShardArchive: %v", err)
			}
			if !res.Repaired || res.Shard != "c.pulse" || res.ArchiveSize != int64(len(before)) || res.SizeBefore != int64(len(s.archive)) {
				t.Errorf("result = %+v", res)
			}
			if !bytes.Equal(gReadFile(t, cfs, "arch.pulse"), before) {
				t.Error("the repaired archive is not byte-identical to the pre-append archive")
			}
			if ok, _ := afero.Exists(cfs, "arch.pulse.append"); ok {
				t.Error("the repair left the journal")
			}
			shards, err := csvc.ListShards(ctx, "arch.pulse")
			if err != nil || len(shards) != 2 {
				t.Errorf("after repair: %d shards, %v; want 2", len(shards), err)
			}
		})
	}
}

// repairFixture is an archive on a MemMap fs with a journal written as
// an append would, and junk appended as a crash would leave.
func repairFixture(t *testing.T) (*Service, afero.Fs, []byte) {
	t.Helper()
	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	adminWriteShardFile(t, fsys, "a.pulse", schema, streamShardRecords(0, 10))
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	j, err := encx.NewAppendJournal(bytes.NewReader(before), int64(len(before)), "b.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if err := encx.WriteAppendJournal(fsys, "arch.pulse", j); err != nil {
		t.Fatal(err)
	}
	torn := append(append([]byte{}, before...), bytes.Repeat([]byte{0x50}, 300)...)
	if err := afero.WriteFile(fsys, "arch.pulse", torn, 0o644); err != nil {
		t.Fatal(err)
	}
	return svc, fsys, before
}

// TestShardRepair_RefusesAMismatchedJournal: a journal that does not
// describe the archive — garbage, a shorter archive, a changed tail, a
// journalled prefix that is not an archive — is refused with
// PULSE_SHARD_REPAIR_REFUSED and its reason; the archive and the
// journal are left exactly as they were.
func TestShardRepair_RefusesAMismatchedJournal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cases := []struct {
		name   string
		reason string
		mutate func(t *testing.T, fsys afero.Fs, before []byte)
	}{
		{"journal_invalid", "journal_invalid", func(t *testing.T, fsys afero.Fs, _ []byte) {
			if err := afero.WriteFile(fsys, "arch.pulse.append", []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"archive_shorter", "archive_shorter_than_journal", func(t *testing.T, fsys afero.Fs, before []byte) {
			if err := afero.WriteFile(fsys, "arch.pulse", before[:len(before)-1], 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"archive_missing", "archive_shorter_than_journal", func(t *testing.T, fsys afero.Fs, _ []byte) {
			if err := fsys.Remove("arch.pulse"); err != nil {
				t.Fatal(err)
			}
		}},
		{"tail_changed", "tail_checksum_mismatch", func(t *testing.T, fsys afero.Fs, before []byte) {
			b := gReadFile(t, fsys, "arch.pulse")
			b[len(before)-30] ^= 0xFF
			if err := afero.WriteFile(fsys, "arch.pulse", b, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"not_an_archive", "tail_not_an_archive", func(t *testing.T, fsys afero.Fs, _ []byte) {
			junk := bytes.Repeat([]byte("not a zip "), 40)
			j, err := encx.NewAppendJournal(bytes.NewReader(junk), int64(len(junk)), "b.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if err := afero.WriteFile(fsys, "arch.pulse", append(junk, 1, 2, 3), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := encx.WriteAppendJournal(fsys, "arch.pulse", j); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fsys, before := repairFixture(t)
			tc.mutate(t, fsys, before)
			pre := snapshotFiles(t, fsys)
			_, err := svc.RepairShardArchive(ctx, "arch.pulse")
			ce := requireCode(t, err, errors.PULSE_SHARD_REPAIR_REFUSED)
			if ce.Details["reason"] != tc.reason || ce.Details["journal"] != "arch.pulse.append" {
				t.Errorf("details = %v, want reason %s", ce.Details, tc.reason)
			}
			if d := diffSnapshots(pre, snapshotFiles(t, fsys)); len(d) != 0 {
				t.Errorf("a refused repair changed %v", d)
			}
		})
	}
}

// TestShardRepair_NoJournalChangesNothing: without a journal the repair
// reports Repaired false and touches nothing — a stale marker stage (a
// crash while the marker itself was written, before any archive byte)
// is the one thing it clears.
func TestShardRepair_NoJournalChangesNothing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, before := repairFixture(t)
	if err := afero.WriteFile(fsys, "arch.pulse", before, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.Remove("arch.pulse.append"); err != nil {
		t.Fatal(err)
	}
	if err := afero.WriteFile(fsys, "arch.pulse.append.tmp", []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := svc.RepairShardArchive(ctx, "arch.pulse")
	if err != nil || res.Repaired {
		t.Fatalf("repair = %+v, %v; want a no-op", res, err)
	}
	if ok, _ := afero.Exists(fsys, "arch.pulse.append.tmp"); ok {
		t.Error("the stale marker stage was kept")
	}
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), before) {
		t.Error("a no-op repair changed the archive")
	}
	if _, err := svc.Open(ctx, "arch.pulse"); err != nil {
		t.Errorf("Open after a no-op repair: %v", err)
	}
}

// TestShardAppend_JournalRefusedOnEveryOpen: with a journal beside it,
// every read path of the service refuses the archive with
// PULSE_SHARD_APPEND_INCOMPLETE — Open, an anchored shard, CountRecords,
// ListShards, verify, compact and the record reader — and none of them
// clears it.
func TestShardAppend_JournalRefusedOnEveryOpen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	svc, fsys, _ := repairFixture(t)
	calls := map[string]func() error{
		"Open":       func() error { _, err := svc.Open(ctx, "arch.pulse"); return err },
		"OpenAnchor": func() error { _, err := svc.OpenAnchor(ctx, "arch.pulse", "a.pulse"); return err },
		"CountRecords": func() error {
			_, err := svc.CountRecords(ctx, "arch.pulse")
			return err
		},
		"ListShards": func() error { _, err := svc.ListShards(ctx, "arch.pulse"); return err },
		"Verify":     func() error { _, err := svc.VerifyShardArchive(ctx, "arch.pulse"); return err },
		"Compact":    func() error { return svc.CompactShardArchive(ctx, "arch.pulse") },
		"RecordReader": func() error {
			_, err := openArchiveRecordReader(fsys, "arch.pulse", nil, nil, "a.pulse")
			return err
		},
	}
	for name, call := range calls {
		requireCode(t, call(), errors.PULSE_SHARD_APPEND_INCOMPLETE)
		if ok, _ := afero.Exists(fsys, "arch.pulse.append"); !ok {
			t.Fatalf("%s removed the journal: an open never repairs", name)
		}
	}
	lr, ok := errors.Lookup(string(errors.PULSE_SHARD_APPEND_INCOMPLETE))
	if !ok || len(lr.Fixups) == 0 || !bytes.Contains([]byte(lr.Fixups[0].Hint), []byte("pulse shard verify --repair")) {
		t.Errorf("fixups = %+v, want one naming `pulse shard verify --repair`", lr.Fixups)
	}
}

// TestShardAppend_RollbackRemovesJournal: a write failure mid-append
// truncates the archive back and, that rollback durable, removes the
// journal — the archive opens again with no repair.
func TestShardAppend_RollbackRemovesJournal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	svc, fsys := osService(t, dir)
	schema := adminScoreSchema()
	adminWriteShardFile(t, fsys, "a.pulse", schema, streamShardRecords(0, 10))
	adminWriteShardFile(t, fsys, "b.pulse", schema, streamShardRecords(100, 10))
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	before := gReadFile(t, fsys, "arch.pulse")
	boom := stderrors.New("disk full")
	svc.appendSeam = &appendSeam{wrap: func(io.Writer) io.Writer { return failWriter{boom} }}
	if _, err := svc.AddShard(ctx, "arch.pulse", "b.pulse"); err == nil {
		t.Fatal("AddShard succeeded through a failing writer")
	}
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), before) {
		t.Error("a failed append did not roll the archive back")
	}
	if ok, _ := afero.Exists(fsys, "arch.pulse.append"); ok {
		t.Error("a rolled-back append left its journal")
	}
	if _, err := svc.Open(ctx, "arch.pulse"); err != nil {
		t.Errorf("Open after a rolled-back append: %v", err)
	}
}

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }
