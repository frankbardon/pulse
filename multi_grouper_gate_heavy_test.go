//go:build heavy

package pulse_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/observe"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestMultiGrouper_BitExactAcrossWorkers: every mergeable #422 case
// answers the SAME BITS — rows, Components (per-slot, per-group,
// per-grouper) and grouped matrices — serially over a single file,
// under every DecodeWorkers count over that file, and over a shard
// archive of the same rows split unevenly (1-row shards, an empty
// shard, edges inside merge blocks) under every ShardWorkers ×
// DecodeWorkers count; weighted and unweighted. Each parallel run must
// take its parallel arm, so no knob compares serial against serial.
func TestMultiGrouper_BitExactAcrossWorkers(t *testing.T) {
	const big = mgDecodeFiller + 1234
	f := newMGFixture(t, pulse.Options{})
	dir := t.TempDir()
	osFs := afero.NewBasePathFs(afero.NewOsFs(), dir)
	serial := f.engine(t, osFs, 1, 1)
	mgWriteBitsCohort(t, serial, "big.pulse", 0, big)
	// The archive's twin: a smaller file (shard fan-out has no record
	// threshold) and the same rows as uneven shards.
	const small = 3*4096 + 777
	mgWriteBitsCohort(t, serial, "small.pulse", 0, small)
	bounds := []int{0, 1, 2, 2, 4097, 4098, 9000, small}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("bits_s%d.pulse", i)
		mgWriteBitsCohort(t, serial, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	if _, err := serial.CreateShardArchive(context.Background(), "bits_archive.pulse", shards); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}
	decoders := map[int]*pulse.Pulse{}
	for _, dw := range []int{2, 3, 7} {
		decoders[dw] = f.engine(t, osFs, 1, dw)
	}
	sharders := map[[2]int]*pulse.Pulse{}
	for _, sw := range []int{1, 2, 3, 4} {
		for _, dw := range []int{1, 2, 3, 7} {
			sharders[[2]int{sw, dw}] = f.engine(t, osFs, sw, dw)
		}
	}
	for _, c := range mgCases() {
		if !mgMergeable(&types.Request{Groups: c.groups}) {
			continue
		}
		for _, weighted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/weighted=%v", c.name, weighted), func(t *testing.T) {
				want := mgRunBits(t, serial, f, mgBitsRequest("big.pulse", c.groups, weighted))
				if hasW := len(want.Aggs) > 0 && want.Aggs[1].SumWeights != nil; weighted != hasW {
					t.Fatalf("weighted=%v run carries weighted floor keys: %v", weighted, hasW)
				}
				for _, dw := range []int{2, 3, 7} {
					got := mgRunBits(t, decoders[dw], f, mgBitsRequest("big.pulse", c.groups, weighted))
					if got.Arm != observe.ArmParallelDecode {
						t.Fatalf("DecodeWorkers=%d ran on arm %q", dw, got.Arm)
					}
					mgSameBits(t, fmt.Sprintf("DecodeWorkers=%d", dw), got, want)
				}
				twin := mgRunBits(t, serial, f, mgBitsRequest("small.pulse", c.groups, weighted))
				for _, sw := range []int{1, 2, 3, 4} {
					for _, dw := range []int{1, 2, 3, 7} {
						got := mgRunBits(t, sharders[[2]int{sw, dw}], f, mgBitsRequest("bits_archive.pulse", c.groups, weighted))
						if sw > 1 && got.Arm != observe.ArmShardParallel {
							t.Fatalf("ShardWorkers=%d ran on arm %q", sw, got.Arm)
						}
						mgSameBits(t, fmt.Sprintf("archive ShardWorkers=%d DecodeWorkers=%d", sw, dw), got, twin)
					}
				}
			})
		}
	}
}
