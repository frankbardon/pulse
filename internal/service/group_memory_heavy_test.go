//go:build heavy

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestGroupMemory_EstimateBoundsPeak (U51 E2-S4, FR-26): on a
// high-cardinality grouped run — 50,000 one-row groups, the gated
// request, components on and (U51 E5-S7) off — MaxEstimatedMemory's estimate (the
// pre-flight's own figure, read back as a refusal's observed) bounds
// the live peak heap of the whole Process (peakLiveAbove, exact under
// the stop-the-world collector rerunUnderSTWGC runs it under;
// accumulation AND the finalize output: Data rows and per-group
// Components), unweighted and weighted (the instance default reaching
// every slot), and the estimate / peak factor stays within
// calibrationBand of the case's measured one. Before the recalibration the bucket term
// (512 + 256 x aggs) was half the measured peak here: an under-estimate
// a MaxEstimatedMemory cap could not trust.
func TestGroupMemory_EstimateBoundsPeak(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate
	if rerunUnderSTWGC(t) {
		return
	}

	const keys = 50_000
	mem := afero.NewMemMapFs()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, groupedSumSchema()); err != nil {
		t.Fatal(err)
	}
	row := make([]byte, 20)
	for k := range keys {
		binary.LittleEndian.PutUint32(row, uint32(k))
		binary.LittleEndian.PutUint64(row[4:], math.Float64bits(1.5+float64(k%7)))
		binary.LittleEndian.PutUint64(row[12:], math.Float64bits(2.25+float64(k%5)))
		buf.Write(row)
	}
	if err := afero.WriteFile(mem, "g.pulse", buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(mem))
	if err != nil {
		t.Fatal(err)
	}
	req := func() *types.Request {
		r := groupMemoryRequest()
		r.Cohort = &types.Cohort{Filename: "g.pulse"}
		return r
	}
	// factor is the measured estimate / live peak (darwin/arm64, go1.26,
	// E7-S4: peakLiveAbove under the stop-the-world collector; GOMAXPROCS
	// 2, 4 and 10 agree within 0.02), banded by calibrationBand both
	// ways. On the sampled peak (garbage included) the factors were
	// 1.61 / 1.48 / 2.51 / 1.93 against a ceiling of 2.0 / 3.0; the live
	// peak holds no garbage, so the 16 MiB run base and the estimate's
	// GC headroom weigh more — most where components are off (the
	// bucket output is the row alone).
	for _, c := range []struct {
		name   string
		weight *types.WeightSpec
		off    bool
		factor float64
	}{
		{"unweighted", nil, false, 2.02}, {"weighted", &types.WeightSpec{Field: "y"}, false, 2.63},
		// Components off (U51 E5-S7): the output term is the row alone.
		{"unweighted components off", nil, true, 3.61}, {"weighted components off", &types.WeightSpec{Field: "y"}, true, 2.50},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDefaultWeight(c.weight)
			svc.SetDisableComponents(c.off)
			estimate := float64(archiveMemoryEstimate(t, svc, req()))
			peak, err := peakLiveAbove(func() error {
				_, err := svc.Process(context.Background(), req())
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("estimate %.1f MB, peak %.1f MB, estimate / peak %.2f", estimate/(1<<20), peak/(1<<20), estimate/peak)
			if estimate < peak {
				t.Errorf("estimate %.0f under the measured live peak %.0f", estimate, peak)
			}
			if lo, hi := c.factor/calibrationBand, c.factor*calibrationBand; estimate < lo*peak || estimate > hi*peak {
				t.Errorf("estimate / live peak %.2f, want within [%.2f, %.2f] (%.2f measured, x/÷ %.2f)", estimate/peak, lo, hi, c.factor, calibrationBand)
			}
		})
	}
}

// TestGroupMemory_MergedBytesPerGroup: the merged state of four
// partitions — each folding a disjoint quarter of the 800,000 keys plus
// one key every partition shares — stays under the same ceiling once
// the absorbed partitions are released: a bucket moves into the merged
// map whole, and nothing of the source partition outlives its merge.
// The merged figure must also stay within mergedBytesSlack of ONE
// partition holding every key, measured alongside. E7-S3 audit: under
// the ceiling alone, a merge that kept every absorbed partition alive
// (their maps still pointing at the moved buckets) held 474 B a group
// against 422 and passed.
func TestGroupMemory_MergedBytesPerGroup(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

	svc := New(nil)
	single := func() float64 {
		before := liveHeap()
		whole, fold := groupMemoryPartition(t, svc)
		fold(0, groupMemoryKeys)
		after := liveHeap()
		runtime.KeepAlive(whole)
		return float64(after-before) / groupMemoryKeys
	}()
	before := liveHeap()
	m := partialMerger{req: groupMemoryRequest()}
	const parts = 4
	for i := range parts {
		p, fold := groupMemoryPartition(t, svc)
		fold(i*groupMemoryKeys/parts, (i+1)*groupMemoryKeys/parts)
		fold(0, 1) // the shared key: a slot-wise merge, not a move
		if err := m.add(p); err != nil {
			t.Fatal(err)
		}
	}
	merged := m.result()
	after := liveHeap()
	if len(merged.groups) != groupMemoryKeys {
		t.Fatalf("%d merged groups, want %d", len(merged.groups), groupMemoryKeys)
	}
	if n := merged.groups["0"].Floors[0].N(); n != parts+1 {
		t.Fatalf("shared key's floor n = %d, want %d (one row per partition + its own)", n, parts+1)
	}
	perGroup := float64(after-before) / groupMemoryKeys
	t.Logf("merged: %.1f bytes/group at %d keys over %d partitions (one partition: %.1f)", perGroup, groupMemoryKeys, parts, single)
	if perGroup > groupMemoryCeiling {
		t.Errorf("merged state holds %.1f bytes/group, ceiling %d", perGroup, groupMemoryCeiling)
	}
	if perGroup > single+mergedBytesSlack {
		t.Errorf("merged state holds %.1f bytes/group, one partition %.1f: an absorbed partition outlived its merge", perGroup, single)
	}
	runtime.KeepAlive(merged)
}

// mergedBytesSlack is how far the merged state may sit above one
// partition holding every key: the merged map grows through the same
// size classes, so the two measure alike (422 B a group, arm64 go1.26);
// a retained absorbed partition adds its map, ~52 B a group.
const mergedBytesSlack = 16
