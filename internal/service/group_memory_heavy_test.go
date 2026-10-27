//go:build heavy

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
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
// the measured peak heap of the whole Process (accumulation AND the
// finalize output: Data rows and per-group Components), unweighted and
// weighted (the instance default reaching every slot), and stays
// within the case's maxFactor of it. Before the recalibration the bucket term
// (512 + 256 x aggs) was half the measured peak here: an under-estimate
// a MaxEstimatedMemory cap could not trust.
func TestGroupMemory_EstimateBoundsPeak(t *testing.T) {
	// serial: measures process-wide heap (runtime.ReadMemStats / GC), which concurrent tests inflate

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
	// maxFactor re-pinned at U51 E5-S7 (per-aggregator state, the
	// components-off output): components on measures 1.46 unweighted and
	// 1.32 weighted (AGG_SUM / AGG_AVERAGE weighted entries are 576 B, the
	// old flat 512 under them), so 2.0; components off 2.47 / 2.05 — the
	// 16 MiB run base is a fifth of a 35 MB peak's estimate (the bucket
	// terms alone are 2.0x) — so 3.0.
	for _, c := range []struct {
		name      string
		weight    *types.WeightSpec
		off       bool
		maxFactor float64
	}{
		{"unweighted", nil, false, 2.0}, {"weighted", &types.WeightSpec{Field: "y"}, false, 2.0},
		// Components off (U51 E5-S7): the output term is the row alone.
		{"unweighted components off", nil, true, 3.0}, {"weighted components off", &types.WeightSpec{Field: "y"}, true, 3.0},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc := New(cfg)
			svc.SetDefaultWeight(c.weight)
			svc.SetDisableComponents(c.off)
			estimate := float64(archiveMemoryEstimate(t, svc, req()))
			peak := -1.0
			for range 3 {
				got, err := measurePeakHeap(func() error {
					_, err := svc.Process(context.Background(), req())
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				peak = max(peak, got)
			}
			t.Logf("estimate %.1f MB, peak %.1f MB (max of 3), estimate / peak %.2f", estimate/(1<<20), peak/(1<<20), estimate/peak)
			if estimate < peak {
				t.Errorf("estimate %.0f under the measured peak %.0f", estimate, peak)
			}
			if estimate > c.maxFactor*peak {
				t.Errorf("estimate %.0f over %.1fx the measured peak %.0f", estimate, c.maxFactor, peak)
			}
		})
	}
}
