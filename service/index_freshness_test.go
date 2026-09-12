package service

import (
	"context"
	stderrors "errors"
	"math"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// freshnessCohort writes a cohort wide enough that a whole-file hash is
// unmistakable against a point read, and returns the Service, the
// counting filesystem and the cohort's byte length.
func freshnessCohort(t *testing.T, rows int) (*Service, *byteCountingFs, int64) {
	t.Helper()

	records := make([][]uint64, 0, rows)
	for i := 0; i < rows; i++ {
		records = append(records, []uint64{uint64(i), math.Float64bits(float64(i) * 1.5), uint64(i % 3)})
	}
	data := writePulseFile(t, indexTestSchema(), records)

	mem := afero.NewMemMapFs()
	if err := afero.WriteFile(mem, "cohort.pulse", data, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	counting := &byteCountingFs{Fs: mem}
	cfg, err := fs.New(fs.WithFs(counting))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	return New(cfg), counting, int64(len(data))
}

// driftMTime moves the cohort's modification time without touching a
// byte of its content, reproducing what a filesystem that does not
// preserve nanosecond resolution does to a cohort the index recorded at
// full precision. Truncating to a whole second is the shape an S3
// LastModified actually has.
func driftMTime(t *testing.T, fsys afero.Fs, path string) time.Time {
	t.Helper()
	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	drifted := info.ModTime().Truncate(time.Second)
	if drifted.Equal(info.ModTime()) {
		// Already whole-second (possible on a coarse clock) — force a
		// difference so the test measures what it claims to.
		drifted = drifted.Add(-time.Second)
	}
	if err := fsys.Chtimes(path, drifted, drifted); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return drifted
}

// TestLookup_MTimeDriftServesRowsWhenContentIsIdentical is the P1
// reproduction: an index whose source cohort is byte-identical must
// serve rows even when the filesystem now reporting that cohort has
// truncated the modification time the index recorded at nanosecond
// resolution. Before the fingerprint fall-through this returned
// PULSE_INDEX_STALE, which made point lookup unusable through any
// object-storage backend (an S3 LastModified is whole seconds).
func TestLookup_MTimeDriftServesRowsWhenContentIsIdentical(t *testing.T) {
	svc, counting, _ := freshnessCohort(t, 400)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	drifted := driftMTime(t, counting, "cohort.pulse")

	meta, err := encoding.ReadIndexMetaFile(counting, encoding.SidecarIndexPath("cohort.pulse", []string{"id"}))
	if err != nil {
		t.Fatalf("ReadIndexMetaFile: %v", err)
	}
	if drifted.UnixNano() == meta.SourceModTime {
		t.Fatal("test setup invariant broken: the mtime did not drift, so nothing is being measured")
	}

	res, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort:        &types.Cohort{Filename: "cohort.pulse"},
		Field:         "id",
		Value:         "7",
		ReturnColumns: []string{"score"},
	})
	if err != nil {
		t.Fatalf("Lookup over an mtime-drifted but byte-identical cohort must serve rows, got: %v", err)
	}
	if got := res.Rows[0]["score"].(float64); got != 10.5 {
		t.Errorf("score = %v, want 10.5", got)
	}
}

// TestLookup_MTimeDriftWithChangedContentIsStillStale is the other
// half: the fall-through must not become a way to serve a genuinely
// stale index. Same size (so the cheap size check cannot answer) plus a
// moved mtime plus mutated content is exactly the case the fingerprint
// has to catch.
func TestLookup_MTimeDriftWithChangedContentIsStillStale(t *testing.T) {
	svc, counting, _ := freshnessCohort(t, 400)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(counting, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 0xFF
	if err := afero.WriteFile(counting, "cohort.pulse", mutated, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	driftMTime(t, counting, "cohort.pulse")

	_, err = svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "7",
	})
	if !errors.HasCode(err, errors.PULSE_INDEX_STALE) {
		t.Fatalf("want PULSE_INDEX_STALE for a same-size content rewrite, got: %v", err)
	}
	var coded *errors.CodedError
	if !stderrors.As(err, &coded) {
		t.Fatalf("error is not a CodedError: %v", err)
	}
	if coded.Details["fingerprint_checked"] != true {
		t.Errorf("details = %v, want fingerprint_checked=true — a hash-derived refusal must say so, "+
			"because the remedy for it differs from a size mismatch", coded.Details)
	}
}

// TestLookup_MTimeDriftHashesOnceAcrossManyLookups is the gate on the
// memo, and on the reason it exists. mtime drift through a bucket
// backend is PERMANENT, not transient, so a fall-through that hashed
// per call would turn every lookup into an O(cohort) read — trading a
// hard failure for the silent loss of the O(1) property the sidecar is
// built for. Measured in bytes off the cohort file, which is the only
// way to tell the two apart from outside.
func TestLookup_MTimeDriftHashesOnceAcrossManyLookups(t *testing.T) {
	svc, counting, size := freshnessCohort(t, 4000)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	driftMTime(t, counting, "cohort.pulse")

	doLookup := func(i int) {
		if _, err := svc.Lookup(context.Background(), &types.LookupRequest{
			Cohort:        &types.Cohort{Filename: "cohort.pulse"},
			Field:         "id",
			Value:         "11",
			ReturnColumns: []string{"score"},
		}); err != nil {
			t.Fatalf("Lookup %d: %v", i, err)
		}
	}

	before := counting.bytes
	doLookup(0)
	firstCall := counting.bytes - before
	if firstCall < size {
		t.Fatalf("the first drifted lookup read %d bytes of a %d-byte cohort — it cannot have "+
			"hashed the file, so this test is not measuring the memo", firstCall, size)
	}

	const extra = 9
	mid := counting.bytes
	for i := 1; i <= extra; i++ {
		doLookup(i)
	}
	rest := counting.bytes - mid

	// Nine further lookups must not cost another whole-file hash between
	// them, let alone one each.
	if rest >= size {
		t.Errorf("%d further lookups read %d bytes against a %d-byte cohort (first call: %d) — the "+
			"fingerprint is being recomputed per lookup rather than memoised per (path, size, mtime)",
			extra, rest, size, firstCall)
	}
}

// TestVerifyIndex_NeverServesItsHashFromTheLookupMemo locks the one
// asymmetry in the memo: Lookup may trust a digest computed under the
// same stat pair, VerifyIndex may not, because its whole job is the
// answer that does not rest on a stat pair. The memo is primed by a
// drifted lookup, the content is then rewritten at the same size and
// the same drifted mtime, and verify must still catch it.
func TestVerifyIndex_NeverServesItsHashFromTheLookupMemo(t *testing.T) {
	svc, counting, _ := freshnessCohort(t, 400)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	drifted := driftMTime(t, counting, "cohort.pulse")

	// Prime the memo: this lookup hashes the file and records the digest
	// under (cohort.pulse, size, drifted).
	if _, err := svc.Lookup(context.Background(), &types.LookupRequest{
		Cohort: &types.Cohort{Filename: "cohort.pulse"},
		Field:  "id",
		Value:  "7",
	}); err != nil {
		t.Fatalf("priming Lookup: %v", err)
	}

	raw, err := afero.ReadFile(counting, "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 0xFF
	if err := afero.WriteFile(counting, "cohort.pulse", mutated, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Put the stat pair back to exactly what the memo holds.
	if err := counting.Chtimes("cohort.pulse", drifted, drifted); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if res.Fresh {
		t.Error("VerifyIndex reported Fresh over rewritten content — it served its hash from the " +
			"Lookup memo, which is keyed on the very stat pair verify exists not to trust")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMismatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMismatch)
	}
}
