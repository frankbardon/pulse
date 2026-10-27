//go:build heavy

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// TestBitExact_RetentionShapeChain (U51 E5-S4): the retail retention
// chain — 13 row-local attributes (ATTR_DATE_PART + 12 ATTR_FORMULA
// month flags) feeding AGG_MIN / 12 AGG_MAX per customer, then a
// per-first-month rollup — answers byte-identically on every arm and
// with the run's memory work toggled off: serial vs per-shard parallel
// over an archive, serial vs parallel decode over a single file, each
// with and without the attribute slots (attributeSlotsOff).
func TestBitExact_RetentionShapeChain(t *testing.T) {
	// serial: swaps the package-level service.attributeSlotsOff test hook

	dir := t.TempDir()
	archive := retentionShapeArchive(t, dir, 12, 1_500, 800)
	single := retentionShapeCohort(t, dir, 110_000, 4_000)
	run := func(path string, shards, decode int, slotsOff bool) []byte {
		t.Helper()
		prev := attributeSlotsOff
		attributeSlotsOff = slotsOff
		defer func() { attributeSlotsOff = prev }()
		svc := New(matrixScaleOSConfig(t, dir))
		svc.SetShardWorkers(shards)
		svc.SetDecodeWorkers(decode)
		resp, err := svc.ProcessChain(context.Background(), retentionShapeChain(path, true))
		if err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, c := range []struct {
		name   string
		path   string
		shards int
		decode int
	}{
		{"archive", archive, 4, 1},
		{"single", single, 1, 4},
	} {
		t.Run(c.name, func(t *testing.T) {
			probe := New(matrixScaleOSConfig(t, dir))
			cohort := mustOpen(t, probe, c.path)
			stage0 := retentionShapeChain(c.path, true).Stages[0].Request
			if c.shards > 1 {
				probe.SetShardWorkers(c.shards)
				if _, ok := probe.shouldFanOut(stage0, cohort); !ok {
					t.Fatal("stage 0 does not take the per-shard parallel arm")
				}
			} else if ok, why := probe.canParallelDecode(stage0, cohort.Schema(), cohort, c.decode, 110_000); !ok {
				t.Fatalf("stage 0 does not take the parallel-decode arm: %s", why)
			}
			want := run(c.path, 1, 1, true)
			if !bytes.Contains(want, []byte(`"active_m01"`)) {
				t.Fatalf("chain answered no retention flags: %.300s", want)
			}
			for _, arm := range []struct {
				name           string
				shards, decode int
				slotsOff       bool
			}{
				{"serial", 1, 1, false},
				{"parallel", c.shards, c.decode, false},
				{"parallel-slots-off", c.shards, c.decode, true},
			} {
				if got := run(c.path, arm.shards, arm.decode, arm.slotsOff); !bytes.Equal(got, want) {
					t.Errorf("%s: chain answer differs from serial without slots", arm.name)
				}
			}
		})
	}
}
