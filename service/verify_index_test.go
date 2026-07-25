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

// TestVerifyIndex_StaleViaMTimeMismatch_ProvesFastPathSkipsHash builds a
// scenario deliberately engineered to prove the fast-path never
// computes the full hash: it rewrites the cohort file with
// byte-IDENTICAL content (so a full hash recompute would, if it were
// ever run, agree with the sidecar's fingerprint and say "fresh"), but
// forces the modification time to differ from the sidecar's recorded
// snapshot. If VerifyIndex still reports Stale, the only way that can
// happen is if it short-circuited on the mtime mismatch WITHOUT ever
// computing (or trusting) the full content hash — exactly the
// fast-path contract this story requires.
func TestVerifyIndex_StaleViaMTimeMismatch_ProvesFastPathSkipsHash(t *testing.T) {
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

	// Sanity: confirm the content really is byte-identical to what the
	// sidecar's fingerprint covers, so a full-hash recompute would
	// agree with the sidecar and call this "fresh" — the fast-path
	// verdict below can only disagree with that if it skipped hashing.
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
	if res.Fresh {
		t.Error("Fresh = true, want false — mtime-only mismatch must report stale via the fast-path")
	}
	if res.Reason != IndexFreshnessReasonStatMismatch {
		t.Errorf("Reason = %q, want %q", res.Reason, IndexFreshnessReasonStatMismatch)
	}
	if !res.FastPath {
		t.Error("FastPath = false, want true — the mtime mismatch alone must short-circuit before any hash is computed")
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
