package service

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

func TestVerifyIndex_Fresh_FallsThroughToFullHashConfirmation(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if !res.Fresh {
		t.Errorf("Fresh = false, want true (untouched cohort right after build)")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMatch)
	}
	// A matching size+mtime pair is NOT by itself trusted as proof of
	// freshness — VerifyIndex always falls through to a full hash
	// recompute for a Fresh=true verdict, so FastPath must be false
	// here even though the stat comparison also matched.
	if res.FastPath {
		t.Errorf("FastPath = true, want false — a fresh verdict must always be hash-confirmed")
	}
}

func TestVerifyIndex_StaleViaSizeMismatch_SkipsFullHash(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	if _, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"}); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	// Truncate the cohort file: its size now differs from the sidecar's
	// recorded Index.SourceSize.
	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", raw[:len(raw)-1], 0644); err != nil {
		t.Fatalf("WriteFile (truncated): %v", err)
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if res.Fresh {
		t.Error("Fresh = true, want false (truncated cohort)")
	}
	if res.Reason != IndexFreshnessReasonStatMismatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonStatMismatch)
	}
	if !res.FastPath {
		t.Error("FastPath = false, want true — size mismatch alone must be conclusive")
	}
}

// TestVerifyIndex_MTimeDriftFallsThroughToTheFingerprint replaced a
// test asserting the exact opposite, and the reversal IS the contract
// change: an mtime that moved while the size held used to short-circuit
// to stat_mismatch WITHOUT hashing, which refused an index whose source
// was byte-identical.
//
// A modification time is metadata the filesystem currently reporting a
// cohort may not carry at the resolution the index recorded — the
// snapshot is Unix NANOSECONDS, an S3 LastModified is whole SECONDS,
// and every copy, restore and cache hop truncates somewhere of its own
// choosing — so drift is not evidence of mutation. The fingerprint the
// sidecar already carries settles it, and ModTimeDrift is how a caller
// still sees that the cheap stat pair disagreed and the hash overruled
// it.
//
// The setup leaves content byte-identical, so a hash MUST say fresh:
// the old fast-path verdict could only disagree with that by not
// hashing at all.
func TestVerifyIndex_MTimeDriftFallsThroughToTheFingerprint(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	buildRes, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	fp, err := encoding.ComputeFingerprint(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("ComputeFingerprint: %v", err)
	}
	if fp != buildRes.Index.Fingerprint {
		t.Fatalf("test setup invariant broken: content hash does not match sidecar fingerprint before mutation")
	}

	// Force the mtime forward by an hour without touching a single byte
	// of content (size is untouched too).
	newMTime := time.Unix(0, buildRes.Index.SourceModTime).Add(time.Hour)
	if err := cfg.Fs().Chtimes("cohort.pulse", newMTime, newMTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if !res.Fresh {
		t.Error("Fresh = false, want true — mtime drift over byte-identical content is not staleness")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMatch)
	}
	if res.FastPath {
		t.Error("FastPath = true, want false — the verdict came from the content hash, not the stat pair")
	}
	if !res.ModTimeDrift {
		t.Error("ModTimeDrift = false, want true — a fresh verdict reached over a disagreeing mtime " +
			"must say so, or a caller cannot tell this deployment apart from one whose stats line up")
	}
}

// TestVerifyIndex_MTimeDriftWithChangedContentIsStale is the other half
// of the fall-through: it must not become a way to call a genuinely
// stale index fresh. Same size (so the size fast-path cannot answer),
// moved mtime, mutated content.
func TestVerifyIndex_MTimeDriftWithChangedContentIsStale(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	buildRes, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 0xFF
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", mutated, 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	newMTime := time.Unix(0, buildRes.Index.SourceModTime).Add(time.Hour)
	if err := cfg.Fs().Chtimes("cohort.pulse", newMTime, newMTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if res.Fresh {
		t.Error("Fresh = true, want false — the content changed at the same byte length")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMismatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMismatch)
	}
	if !res.ModTimeDrift {
		t.Error("ModTimeDrift = false, want true — the mtime did move, and a stale verdict reports it too")
	}
}

// TestVerifyIndex_MatchingStatButChangedContent_FullHashCatchesIt
// exercises the fall-through path's reason for existing: a matching
// size+mtime pair is not, by itself, trusted proof of freshness.
// Content is mutated in place (same byte length, same forced mtime) so
// the fast-path alone would wrongly call this fresh; only the
// full-hash confirmation the match case always performs catches the
// divergence.
func TestVerifyIndex_MatchingStatButChangedContent_FullHashCatchesIt(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	buildRes, err := svc.BuildIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}

	raw, err := afero.ReadFile(cfg.Fs(), "cohort.pulse")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	// Flip one byte deep in the record region — same length, different
	// content.
	mutated := append([]byte(nil), raw...)
	mutated[len(mutated)-1] ^= 0xFF
	if err := afero.WriteFile(cfg.Fs(), "cohort.pulse", mutated, 0644); err != nil {
		t.Fatalf("WriteFile (mutated): %v", err)
	}

	// Force the stat snapshot back to exactly what the sidecar recorded
	// (size is unchanged by construction; mtime is forced back).
	origMTime := time.Unix(0, buildRes.Index.SourceModTime)
	if err := cfg.Fs().Chtimes("cohort.pulse", origMTime, origMTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	info, err := cfg.Fs().Stat("cohort.pulse")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if uint64(info.Size()) != buildRes.Index.SourceSize {
		t.Fatalf("test setup invariant broken: size changed by the in-place mutation")
	}

	res, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err != nil {
		t.Fatalf("VerifyIndex: %v", err)
	}
	if res.Fresh {
		t.Error("Fresh = true, want false — content changed despite matching size+mtime")
	}
	if res.Reason != IndexFreshnessReasonFingerprintMismatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonFingerprintMismatch)
	}
	if res.FastPath {
		t.Error("FastPath = true, want false — this verdict requires the full-hash fallback, not the fast-path alone")
	}
}

func TestVerifyIndex_MissingIndex(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.VerifyIndex(context.Background(), "cohort.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error when no sidecar index has been built")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_MISSING) {
		t.Errorf("expected PULSE_INDEX_MISSING, got: %v", err)
	}
}

func TestVerifyIndex_EmptyKeyFieldsRejected(t *testing.T) {
	schema := indexTestSchema()
	cfg := setupTestFS(t, "cohort.pulse", schema, indexTestRecords())
	svc := New(cfg)

	_, err := svc.VerifyIndex(context.Background(), "cohort.pulse", nil)
	if err == nil {
		t.Fatal("expected error for empty key-fields list")
	}
	if !errors.HasCode(err, errors.SERVICE_VALIDATION) {
		t.Errorf("expected SERVICE_VALIDATION, got: %v", err)
	}
}

func TestVerifyIndex_ShardArchiveRejected(t *testing.T) {
	schema, shards, _ := canonicalThreeShards()
	svc, _ := setupShardArchive(t, "arch.pulse", schema, shards, [][]uint64{})

	_, err := svc.VerifyIndex(context.Background(), "arch.pulse", []string{"id"})
	if err == nil {
		t.Fatal("expected error verifying an index against a shard archive")
	}
	if !errors.HasCode(err, errors.PULSE_INDEX_UNSUPPORTED_SHARDED) {
		t.Errorf("expected PULSE_INDEX_UNSUPPORTED_SHARDED, got: %v", err)
	}
}
