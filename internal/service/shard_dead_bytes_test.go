package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// shard_dead_bytes_test.go pins the dead-byte lifecycle of the in-place
// `shard add` (U73 E2-S3): appends that leave at most half the archive
// dead stay appends; the add that would cross half rewrites instead —
// one `_schema.pulse`, zero dead bytes, ONE PULSE_SHARD_DEAD_BYTES_RECLAIMED
// warning; AddShardOptions.Rewrite forces the rewrite; verify reports the
// dead copies and bytes, and compact reclaims them.

// schemaNameCount counts `_schema.pulse` names in the archive bytes: two
// per copy (its local header and its directory record), the dead
// directories' records included.
func schemaNameCount(b []byte) int { return bytes.Count(b, []byte(encx.ReservedSchemaName)) }

func countCode(ws []encx.CohesionWarning, code errors.Code) int {
	n := 0
	for _, w := range ws {
		if w.Code == string(code) {
			n++
		}
	}
	return n
}

func TestShardDeadBytes_AppendsUntilHalfDeadThenRewrites(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	adminWriteShardFile(t, fsys, "base.pulse", schema, streamShardRecords(0, 40))
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"base.pulse"}); err != nil {
		t.Fatal(err)
	}

	appends, fellBack := 0, false
	for i := 0; i < 64 && !fellBack; i++ {
		name := fmt.Sprintf("s%02d.pulse", i)
		adminWriteShardFile(t, fsys, name, schema, streamShardRecords(1000+i, 1))
		before := gReadFile(t, fsys, "arch.pulse")
		res, err := svc.AddShard(ctx, "arch.pulse", name)
		if err != nil {
			t.Fatalf("AddShard %s: %v", name, err)
		}
		after := gReadFile(t, fsys, "arch.pulse")
		v := gVerifyClean(t, svc, "arch.pulse")
		switch countCode(res.Warnings, errors.PULSE_SHARD_DEAD_BYTES_RECLAIMED) {
		case 0:
			appends++
			if !bytes.HasPrefix(after, before) {
				t.Fatalf("add %d: no fallback warning, but the archive was rewritten", i)
			}
			if v.DeadSchemaCopies != appends || v.DeadBytes*2 > int64(len(after)) {
				t.Fatalf("add %d appended: verify reports %d copies / %d dead of %d bytes, want %d copies, at most half dead",
					i, v.DeadSchemaCopies, v.DeadBytes, len(after), appends)
			}
		case 1:
			fellBack = true
			if len(res.Warnings) != 1 {
				t.Errorf("fallback warnings = %+v, want exactly one", res.Warnings)
			}
			d := res.Warnings[0].Details
			dead, size := d["dead_bytes"].(int64), d["archive_size"].(int64)
			if dead*2 <= size || d["dead_schema_copies"] != appends+1 || d["shard"] != name {
				t.Errorf("fallback details = %v, want over half dead and %d copies", d, appends+1)
			}
			if bytes.HasPrefix(after, before) {
				t.Error("the fallback appended instead of rewriting")
			}
			if got := schemaNameCount(after); got != 2 {
				t.Errorf("after the fallback the archive names _schema.pulse %d times, want 2 (one entry + its record)", got)
			}
			if v.DeadSchemaCopies != 0 || v.DeadBytes != 0 {
				t.Errorf("after the fallback verify reports %d copies / %d dead bytes, want none", v.DeadSchemaCopies, v.DeadBytes)
			}
			if n, err := svc.CountRecords(ctx, "arch.pulse"); err != nil || n != uint64(40+appends+1) {
				t.Errorf("CountRecords = %d, %v; want %d", n, err, 40+appends+1)
			}
		default:
			t.Fatalf("add %d warned more than once: %+v", i, res.Warnings)
		}
	}
	if appends == 0 || !fellBack {
		t.Fatalf("appends = %d, fell back = %v; want appends first, then the fallback", appends, fellBack)
	}

	// After the rewrite the next add appends again, silently.
	adminWriteShardFile(t, fsys, "next.pulse", schema, streamShardRecords(5000, 1))
	before := gReadFile(t, fsys, "arch.pulse")
	res, err := svc.AddShard(ctx, "arch.pulse", "next.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 0 || !bytes.HasPrefix(gReadFile(t, fsys, "arch.pulse"), before) {
		t.Errorf("the add after a fallback did not append silently: %+v", res.Warnings)
	}
}

// TestShardDeadBytes_RewriteOptionForcesTheRewrite: Rewrite takes the
// staged rewrite on an archive the add would append to — no warning,
// the bytes a create of the same shards writes — and it is the answer
// to a refused write-open: it never opens the archive for writing.
func TestShardDeadBytes_RewriteOptionForcesTheRewrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	schema := adminScoreSchema()
	for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*100, 10+i))
	}
	svc := New(cfg)
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse", "b.pulse"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateShardArchive(ctx, "created.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}

	refusing, err := fs.New(fs.WithFs(&failingWriteFs{Fs: fsys, fail: func(n string) bool { return n == "arch.pulse" }}))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(refusing).AddShard(ctx, "arch.pulse", "c.pulse")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_SHARD_APPEND_REFUSED {
		t.Fatalf("plain add over a refused write-open: %v, want PULSE_SHARD_APPEND_REFUSED", err)
	}

	res, err := New(refusing).AddShardWithOptions(ctx, "arch.pulse", "c.pulse", AddShardOptions{Rewrite: true})
	if err != nil {
		t.Fatalf("AddShardWithOptions(Rewrite): %v", err)
	}
	if len(res.Warnings) != 0 || res.ShardCount != 3 {
		t.Errorf("forced rewrite result = %+v, want 3 shards and no warning", res)
	}
	if !bytes.Equal(gReadFile(t, fsys, "arch.pulse"), gReadFile(t, fsys, "created.pulse")) {
		t.Error("the forced rewrite did not write create's bytes")
	}
}

// TestShardDeadBytes_VerifyReportsAndCompactReclaims: two appends leave
// two dead `_schema.pulse` copies, which verify counts with their bytes
// (the archive minus a create of the same shards); compact leaves none.
func TestShardDeadBytes_VerifyReportsAndCompactReclaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	schema := adminScoreSchema()
	for i, n := range []string{"a.pulse", "b.pulse", "c.pulse"} {
		adminWriteShardFile(t, fsys, n, schema, streamShardRecords(i*100, 200))
	}
	if _, err := svc.CreateShardArchive(ctx, "arch.pulse", []string{"a.pulse"}); err != nil {
		t.Fatal(err)
	}
	if v := gVerifyClean(t, svc, "arch.pulse"); v.DeadSchemaCopies != 0 || v.DeadBytes != 0 {
		t.Fatalf("a created archive reports %d copies / %d dead bytes", v.DeadSchemaCopies, v.DeadBytes)
	}
	for _, n := range []string{"b.pulse", "c.pulse"} {
		res, err := svc.AddShard(ctx, "arch.pulse", n)
		if err != nil || len(res.Warnings) != 0 {
			t.Fatalf("AddShard %s: %v %+v", n, err, res)
		}
	}
	if _, err := svc.CreateShardArchive(ctx, "created.pulse", []string{"a.pulse", "b.pulse", "c.pulse"}); err != nil {
		t.Fatal(err)
	}
	appended := gReadFile(t, fsys, "arch.pulse")
	v := gVerifyClean(t, svc, "arch.pulse")
	if want := int64(len(appended) - len(gReadFile(t, fsys, "created.pulse"))); v.DeadSchemaCopies != 2 || v.DeadBytes != want {
		t.Errorf("verify reports %d copies / %d dead bytes, want 2 / %d", v.DeadSchemaCopies, v.DeadBytes, want)
	}

	if err := svc.CompactShardArchive(ctx, "arch.pulse"); err != nil {
		t.Fatal(err)
	}
	if v := gVerifyClean(t, svc, "arch.pulse"); v.DeadSchemaCopies != 0 || v.DeadBytes != 0 {
		t.Errorf("compact left %d copies / %d dead bytes", v.DeadSchemaCopies, v.DeadBytes)
	}
	compacted, err := afero.ReadFile(fsys, "arch.pulse")
	if err != nil {
		t.Fatal(err)
	}
	if got := schemaNameCount(compacted); got != 2 {
		t.Errorf("compacted archive names _schema.pulse %d times, want 2", got)
	}
}
