//go:build heavy

package service

import (
	"bytes"
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
)

// TestShardAppend_AtTheShardCountLimit: the append reaches the 65535
// shard cap exactly as the rewrite does. Adding the 65535th shard
// appends in place and writes a directory of 65536 records — past the
// EOCD's u16 count, so through the zip64 end record — which every
// reader lists in full; the next add is PULSE_SHARD_COUNT_LIMIT with
// the archive byte-identical.
func TestShardAppend_AtTheShardCountLimit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cfg := fs.NewMemMap()
	fsys := cfg.Fs()
	svc := New(cfg)
	writeWideArchive(t, fsys, "arch.pulse", encx.MaxArchiveShards-1, nil)
	adminWriteShardFile(t, fsys, "last.pulse", adminScoreSchema(), [][]uint64{{9, math.Float64bits(9)}})
	adminWriteShardFile(t, fsys, "over.pulse", adminScoreSchema(), [][]uint64{{10, math.Float64bits(10)}})
	before := gReadFile(t, fsys, "arch.pulse")

	res, err := svc.AddShard(ctx, "arch.pulse", "last.pulse")
	if err != nil {
		t.Fatalf("AddShard of shard 65535: %v", err)
	}
	if res.ShardCount != encx.MaxArchiveShards {
		t.Errorf("ShardCount = %d, want %d", res.ShardCount, encx.MaxArchiveShards)
	}
	after := gReadFile(t, fsys, "arch.pulse")
	if !bytes.HasPrefix(after, before) {
		t.Fatal("the add at the limit did not append in place")
	}
	if n := len(archiveEntriesByName(t, after)); n != encx.MaxArchiveShards+1 {
		t.Errorf("archive/zip lists %d entries, want %d", n, encx.MaxArchiveShards+1)
	}
	shards, err := svc.ListShards(ctx, "arch.pulse")
	if err != nil || len(shards) != encx.MaxArchiveShards {
		t.Fatalf("ListShards = %d, %v", len(shards), err)
	}
	if n, err := svc.CountRecords(ctx, "arch.pulse"); err != nil || n != encx.MaxArchiveShards {
		t.Errorf("CountRecords = %d, %v", n, err)
	}

	_, err = svc.AddShard(ctx, "arch.pulse", "over.pulse")
	if !errors.HasCode(err, errors.PULSE_SHARD_COUNT_LIMIT) {
		t.Fatalf("add past the limit = %v, want PULSE_SHARD_COUNT_LIMIT", err)
	}
	if !bytes.Equal(after, gReadFile(t, fsys, "arch.pulse")) {
		t.Error("the refused add changed the archive")
	}
}
