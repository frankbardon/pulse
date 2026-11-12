package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/spf13/afero"
)

// archive_lookup_bloomset_test.go pins #721: a lookup over a shard
// archive reads the index's Bloom data in ONE artifact read — the
// archive Bloom set — however many shards survive the key-range check,
// and never a per-shard ".bloom" sidecar beside a set.

// bloomReadFs counts opens of Bloom files: the archive Bloom sets and
// per-shard Bloom sidecars.
type bloomReadFs struct {
	afero.Fs
	mu             sync.Mutex
	sets, perShard int
}

func (c *bloomReadFs) note(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.HasSuffix(name, encx.ArchiveBloomSetSuffix):
		c.sets++
	case strings.HasSuffix(name, encx.ShardBloomSuffix):
		c.perShard++
	}
}

func (c *bloomReadFs) Open(name string) (afero.File, error) {
	c.note(name)
	return c.Fs.Open(name)
}

func (c *bloomReadFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	c.note(name)
	return c.Fs.OpenFile(name, flag, perm)
}

func (c *bloomReadFs) take() (sets, perShard int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	sets, perShard = c.sets, c.perShard
	c.sets, c.perShard = 0, 0
	return sets, perShard
}

// TestArchiveLookupBloomSet_OneReadIndependentOfShardCount: id 6 lies in
// the key range of a, b and every added shard (ids 5..9), so the number
// of shards whose Bloom the lookup consults grows with N (2 at N=4, 4 at
// N=6) — yet each lookup opens the Bloom set exactly once and no
// per-shard Bloom sidecar.
func TestArchiveLookupBloomSet_OneReadIndependentOfShardCount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	counter := &bloomReadFs{Fs: afero.NewMemMapFs()}
	cfg, err := fs.New(fs.WithFs(counter))
	if err != nil {
		t.Fatal(err)
	}
	svc, fsys := setupArchiveLookupOn(t, cfg, []string{"id"})
	for n, add := range []string{"", "e.pulse", "f.pulse"} {
		if add != "" {
			writeUpkeepShard(t, fsys, add, float64(80+n))
			if _, err := svc.AddShard(ctx, "arch.pulse", add); err != nil {
				t.Fatalf("AddShard(%s): %v", add, err)
			}
		}
		counter.take()
		if got := lookupOutcome(t, svc, "arch.pulse", lk("id", "6"), ""); !strings.HasPrefix(got, "PULSE_LOOKUP_NOT_FOUND") {
			t.Fatalf("N=%d: lookup id 6 = %s, want a miss", 4+n, got)
		}
		if sets, perShard := counter.take(); sets != 1 || perShard != 0 {
			t.Errorf("N=%d: lookup opened the Bloom set %d times and %d per-shard Blooms; want 1 and 0", 4+n, sets, perShard)
		}
	}
}
