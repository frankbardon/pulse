package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// BenchmarkCrosstabWideCohort_Buffered runs the same crosstab workload
// as BenchmarkCrosstabWideCohort but with fusion forced off so the
// buffered RunCrosstab path takes over. Provides the baseline against
// which the fused path's speedup / allocation reduction is measured.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabWideCohort_Buffered -benchmem -run=^$
func BenchmarkCrosstabWideCohort_Buffered(b *testing.B) {
	svc, req, ctx := setupCrosstabWideBench(b)
	svc.SetDisableCrosstabFusion(true)

	b.ReportAllocs()
	for b.Loop() {
		_, err := svc.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// BenchmarkCrosstabWideCohort_Fused runs the wide-cohort crosstab with
// the fused streaming path engaged. Compare to
// BenchmarkCrosstabWideCohort_Buffered for the speedup / allocation
// reduction the fused path delivers on a 200-field × 10K-row cohort
// with three referenced fields.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabWideCohort_Fused -benchmem -run=^$
func BenchmarkCrosstabWideCohort_Fused(b *testing.B) {
	svc, req, ctx := setupCrosstabWideBench(b)
	// Fusion enabled by default; assert here for clarity.
	svc.SetDisableCrosstabFusion(false)

	b.ReportAllocs()
	for b.Loop() {
		_, err := svc.Process(ctx, req)
		if err != nil {
			b.Fatalf("Process: %v", err)
		}
	}
}

// setupCrosstabWideBench builds the same wide-cohort fixture as
// BenchmarkCrosstabWideCohort. Extracted so the fused / buffered
// benchmarks share the cohort + request without duplicating the
// boilerplate.
func setupCrosstabWideBench(b *testing.B) (*Service, *types.Request, context.Context) {
	b.Helper()
	const pad = 197 // 200 total fields including region, segment, value
	const rows = 10000

	schema := buildBenchWideSchema(pad)
	recs := buildBenchWideRecords(rows, pad)

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range recs {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				b.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
			}
		}
	}
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "ct.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	svc := New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "ct.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "value", Label: "n"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	return svc, req, context.Background()
}

// ---------------------------------------------------------------------
// E3-S1 — both-paths memory benchmark for the combined case.
//
// The effort's whole claim is that a crosstab carrying BOTH a
// GROUP_SET_PER_ELEMENT axis (E2) and overlays (E1) no longer
// materialises the record slice. The downstream evidence was peak RSS
// against a real 196 MiB / 1.4M-record cohort, which is not reproducible
// in-repo; the in-repo equivalent is -benchmem across both execution
// paths over a synthetic cohort.
//
// The distinguishing signal is NOT "fused allocates somewhat less" — it
// is that the reduction SCALES with record count. A constant delta would
// mean the fused arm merely allocates a little less per call; a delta
// that grows with rows means the per-record materialisation is gone.
// Hence the record-count parameterisation: read B/op down the size axis,
// not just across the path axis.
//
// No threshold is asserted. Machine variance makes an absolute bound
// flaky, and a bench is not a gate — the recorded comparison is the
// deliverable.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabSetFanoutOverlay -benchmem -run=^$
// ---------------------------------------------------------------------

// setFanoutBenchRows is the record-count axis. At least two sizes so the
// scaling of the fused/buffered B/op gap is readable straight off the
// benchmark output.
var setFanoutBenchRows = []int{25_000, 100_000, 400_000}

// setFanoutBenchMasks are the set_u8 masks cycled across the synthetic
// cohort. Every mask has popcount 3 or 4 (mean 3.5), so the
// GROUP_SET_PER_ELEMENT fan actually fires on every record — a
// popcount-1 cohort would degenerate to a single-key axis and benchmark
// the wrong thing entirely.
var setFanoutBenchMasks = []uint64{
	0b0000_0111, // 3
	0b0001_1100, // 3
	0b1010_1010, // 4
	0b0101_0101, // 4
	0b1100_0011, // 4
	0b0011_1001, // 4
	0b1000_1101, // 4
	0b0110_0100, // 3
}

// BenchmarkCrosstabSetFanoutOverlay drives the combined case — a
// GROUP_SET_PER_ELEMENT row axis, a categorical column axis, an
// AGG_WEIGHTED_MEAN cell and an OVERLAY_PAIRWISE_PROP_Z layer — down
// both execution paths at several record counts.
//
// The cell is a weighted mean of a 0/1 indicator, i.e. a genuine
// weighted proportion, which is what PAIRWISE_PROP_Z's p_source expects;
// the overlay therefore does real work rather than skipping every pair.
//
// Reading the output — three metrics, and they do not all point the same
// way:
//
//   - peak-heap-MB is the headline. Buffered grows linearly with rows
//     (it holds every decoded record live until Finalize); fused stays
//     near flat. This is the materialisation claim.
//   - B/op favours fused by roughly a third, and the ABSOLUTE gap grows
//     with rows. It is a smaller ratio than peak heap because B/op is
//     cumulative: both paths decode every record, so both pay the
//     transient per-record decode allocation. Only the buffered path
//     also pays to keep them.
//   - allocs/op is HIGHER on the fused path, and that is expected, not a
//     regression. The fan-out axis keyer builds a small key tuple and
//     resolves mask labels per record per fanned key; the buffered path
//     amortises some of that over its materialised slice. Fused trades
//     more small short-lived allocations for far fewer retained bytes
//     and a large wall-time win.
func BenchmarkCrosstabSetFanoutOverlay(b *testing.B) {
	for _, rows := range setFanoutBenchRows {
		cfg, schema := buildSetFanoutOverlayCohort(b, rows)
		ctx := context.Background()

		// Non-vacuity: if the gate rejects this request the "fused" arm
		// below would silently run buffered and the benchmark would
		// compare a path against itself.
		probe := New(cfg)
		if ok, reason := processing.CanFuseCrosstab(setFanoutOverlayRequest(), schema, probe.Extensions()); !ok {
			b.Fatalf("CanFuseCrosstab rejected the combined fan-out + overlay request: %s", reason)
		}

		for _, path := range []struct {
			name    string
			disable bool
		}{
			{name: "fused", disable: false},
			{name: "buffered", disable: true},
		} {
			b.Run(fmt.Sprintf("rows=%d/%s", rows, path.name), func(b *testing.B) {
				// Fresh service per sub-case so neither path inherits
				// decode-plan cache state from the other.
				svc := New(cfg)
				svc.SetDisableCrosstabFusion(path.disable)
				// Warm-up outside the timed loop: a request failure
				// surfaces as a real error rather than a timed hang,
				// and lazily-built caches are not charged to iteration
				// one.
				if _, err := svc.Process(ctx, setFanoutOverlayRequest()); err != nil {
					b.Fatalf("warmup Process (%s): %v", path.name, err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := svc.Process(ctx, setFanoutOverlayRequest()); err != nil {
						b.Fatalf("Process (%s): %v", path.name, err)
					}
				}
				// Freeze the -benchmem counters before the untimed
				// peak-heap pass so its allocations are not charged to
				// B/op.
				b.StopTimer()
				reportPeakHeap(b, func() error {
					_, err := svc.Process(ctx, setFanoutOverlayRequest())
					return err
				})
			})
		}
	}
}

// setFanoutOverlayRequest is the combined-case request. Rebuilt per call
// because Process normalises the request in place (smart defaults, label
// binding), and a shared instance would let iteration one's mutations
// leak into iteration two.
func setFanoutOverlayRequest() *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "fanout_overlay.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "value",
				Label:  "wmean",
				Params: json.RawMessage(`{"weight_field":"weight"}`),
			},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
		Overlays: []types.OverlaySpec{{
			Name:  "pz",
			Kind:  types.OverlayKindPairwisePropZ,
			Scope: types.OverlayScopeRow,
		}},
	}
}

// buildSetFanoutOverlayCohort writes a hermetic in-memory cohort of the
// requested row count: region (categorical_u8, 4 labels), tags (set_u8,
// 8 labels, popcount 3–4 per row), value (f64, a 0/1 indicator) and
// weight (f64). 18 bytes per record.
func buildSetFanoutOverlayCohort(b *testing.B, rows int) (*fs.Config, *encoding.Schema) {
	b.Helper()

	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east", "west"} {
		if _, err := regionDict.Add(r); err != nil {
			b.Fatalf("region dict.Add: %v", err)
		}
	}
	tagsDict := encoding.NewDictionary()
	for i := 0; i < 8; i++ {
		if _, err := tagsDict.Add(fmt.Sprintf("TAG%d", i)); err != nil {
			b.Fatalf("tags dict.Add: %v", err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tagsDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 10, CsvColumnIdx: 3},
		},
	}

	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		b.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		b.Fatalf("WriteSchema: %v", err)
	}
	for i := 0; i < rows; i++ {
		// Strides co-prime with the table lengths so region and mask
		// decorrelate instead of marching in lockstep.
		rec := []uint64{
			uint64(i % 4),
			setFanoutBenchMasks[(i*3)%len(setFanoutBenchMasks)],
			math.Float64bits(float64(i % 2)),         // 0/1 indicator
			math.Float64bits(0.5 + float64(i%7)/4.0), // 0.5 .. 2.0
		}
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				b.Fatalf("WriteFieldValue record[%d] field[%d]: %v", i, fi, err)
			}
		}
	}

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "fanout_overlay.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	return cfg, schema
}

// reportPeakHeap runs fn once, untimed, with a sampler goroutine polling
// runtime.ReadMemStats, and reports the peak HeapAlloc observed above the
// pre-call baseline as a custom benchmark metric.
//
// Why this exists alongside -benchmem: B/op is CUMULATIVE bytes
// allocated. It cannot separate "allocated and immediately dropped" from
// "allocated and held live until the pass ends", and both paths decode
// every record, so both pay the same transient decode allocation. The
// claim this effort makes is about the second thing — the buffered path
// materialises []*Record and holds every record live until Finalize,
// while the fused path folds each record into cell state and drops it.
// Peak LIVE heap is the in-repo analogue of the downstream peak-RSS
// figure, and it is where the materialisation shows up.
//
// GC is deliberately left at its default setting: the whole signal is
// that the fused path's records are collectable mid-pass and the
// buffered path's are not. Sampled, so it is an estimate — reported,
// never asserted.
func reportPeakHeap(b *testing.B, fn func() error) {
	b.Helper()

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)

	var peak atomic.Uint64
	peak.Store(base.HeapAlloc)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
			}
			runtime.ReadMemStats(&ms)
			for {
				cur := peak.Load()
				if ms.HeapAlloc <= cur || peak.CompareAndSwap(cur, ms.HeapAlloc) {
					break
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	err := fn()
	close(done)
	wg.Wait()
	if err != nil {
		b.Fatalf("peak-heap Process: %v", err)
	}

	delta := float64(peak.Load()) - float64(base.HeapAlloc)
	if delta < 0 {
		delta = 0
	}
	b.ReportMetric(delta/(1024*1024), "peak-heap-MB")
}
