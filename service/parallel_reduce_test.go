package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// parallel_reduce_test.go exercises the E3-S3 per-worker partial-state
// reducer (service.reduceParallelBuffered) end-to-end against a
// mergeable Request. The tests build a cohort above the parallel-
// decode threshold on a real OsFs (so the mmap probe engages), then
// invoke reduceParallelBuffered with worker counts in {1, 2, 4,
// runtime.NumCPU()} and compare every result against the canonical
// serial Process baseline.
//
// Float-equal policy mirrors shard_reduce.go (and the broader
// shard-parity test surface):
//
//   - Integer aggregators (AGG_COUNT) — byte-equal.
//   - Mass-summable aggregators on integer-typed fields (AGG_SUM of u8
//     dictionary indices) — byte-equal.
//   - Welford-Pébaÿ aggregators (AGG_AVERAGE, AGG_STDDEV,
//     AGG_VARIANCE) on f64 fields — equivalent to within 1 ULP.
//
// The same policy lands in TestShardParity_SingleFileVsArchiveVsAnchor
// already; the routes are different but the math is the same.

// TestParallelDecode_MergeableByteEqualVsSerial is the load-bearing
// equivalence harness for E3-S3: integer/count aggregators must produce
// byte-equal output across every supported worker count. Any drift here
// signals a segment-boundary bug, a per-worker race on the shared mmap
// region, or a merge-fold ordering error in mergeShardPartials.
func TestParallelDecode_MergeableByteEqualVsSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel reduce equivalence in -short mode")
	}

	const rowCount = parallelDecodeRecordThreshold + 4096

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_reduce_eq.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
				{Type: types.AGG_SUM, Field: "weight", Label: "total"},
				{Type: types.AGG_MIN, Field: "weight", Label: "lo"},
				{Type: types.AGG_MAX, Field: "weight", Label: "hi"},
			},
		}
	}

	// Serial baseline: run the standard streaming Process path with
	// DecodeWorkers=1 so no parallel-decode dispatch could engage even
	// if a future story wires it at the Process level. This is the
	// "single-threaded path" the brief calls out as the byte-equal
	// reference.
	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(context.Background(), mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected aggregator row in Data")
	}
	wantRow := serialResp.Data[0]

	workerCounts := []int{1, 2, 4, runtime.NumCPU()}
	for _, w := range workerCounts {
		w := w
		t.Run(workerLabel(w), func(t *testing.T) {
			svc := New(cfg)
			svc.SetDecodeWorkers(w)
			cohort, err := svc.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			req := mkReq()
			projectMapHint := len(cohort.Schema().Fields)
			pctx, cleanup, available, err := buildParallelDecodeContext(
				svc, path, cohort.Schema(), nil, nil, projectMapHint,
			)
			if err != nil {
				t.Fatalf("buildParallelDecodeContext: %v", err)
			}
			if !available {
				t.Fatal("expected mmap-eligible cohort for OsFs above threshold")
			}
			t.Cleanup(func() {
				if cleanup != nil {
					_ = cleanup()
				}
			})
			resp, err := svc.reduceParallelBuffered(context.Background(), req, cohort.Schema(), pctx, w)
			if err != nil {
				t.Fatalf("reduceParallelBuffered (workers=%d): %v", w, err)
			}
			if len(resp.Data) == 0 {
				t.Fatalf("workers=%d: expected aggregator row", w)
			}
			gotRow := resp.Data[0]

			// AGG_COUNT — integer counter. Byte-equal across every
			// worker count is non-negotiable.
			if !reflect.DeepEqual(gotRow["n"], wantRow["n"]) {
				t.Errorf("workers=%d: AGG_COUNT differs: got=%v want=%v", w, gotRow["n"], wantRow["n"])
			}
			// AGG_MIN / AGG_MAX — sentinel scans. Byte-equal.
			if !reflect.DeepEqual(gotRow["lo"], wantRow["lo"]) {
				t.Errorf("workers=%d: AGG_MIN differs: got=%v want=%v", w, gotRow["lo"], wantRow["lo"])
			}
			if !reflect.DeepEqual(gotRow["hi"], wantRow["hi"]) {
				t.Errorf("workers=%d: AGG_MAX differs: got=%v want=%v", w, gotRow["hi"], wantRow["hi"])
			}
			// AGG_SUM on f64 — associative+commutative as a real-valued
			// reduction but the worker-partitioned summation order
			// drifts from the serial path's left-fold; rounding can
			// shift within 1 ULP. Match the shard_reduce policy.
			if !floatWithinULP(toFloat(gotRow["total"]), toFloat(wantRow["total"]), 1) {
				t.Errorf("workers=%d: AGG_SUM beyond 1 ULP: got=%v want=%v",
					w, gotRow["total"], wantRow["total"])
			}
			// Metadata: TotalRows and FilteredRows must match exactly.
			if resp.Metadata == nil {
				t.Fatalf("workers=%d: expected metadata", w)
			}
			if got, want := resp.Metadata.TotalRows, serialResp.Metadata.TotalRows; got != want {
				t.Errorf("workers=%d: TotalRows: got=%d want=%d", w, got, want)
			}
			if got, want := resp.Metadata.FilteredRows, serialResp.Metadata.FilteredRows; got != want {
				t.Errorf("workers=%d: FilteredRows: got=%d want=%d", w, got, want)
			}
		})
	}
}

// TestParallelDecode_MergeableULPEquivalent pins the Welford-Pébaÿ
// merge ULP-tolerance contract. AGG_AVERAGE / AGG_STDDEV / AGG_VARIANCE
// use the Chan-Welford parallel merge formula (already exercised by
// the shard-reduce path); the per-worker partition + merge in this
// path runs the same code so the per-cohort answer is equivalent to
// within 1 ULP of the serial single-pass answer.
func TestParallelDecode_MergeableULPEquivalent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel reduce ULP equivalence in -short mode")
	}

	const rowCount = parallelDecodeRecordThreshold + 2048

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_reduce_ulp.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_AVERAGE, Field: "weight", Label: "mean"},
				{Type: types.AGG_STDDEV, Field: "weight", Label: "sd"},
				{Type: types.AGG_VARIANCE, Field: "weight", Label: "var"},
			},
		}
	}

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(context.Background(), mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected aggregator row")
	}
	wantRow := serialResp.Data[0]

	workerCounts := []int{1, 2, 4, runtime.NumCPU()}
	for _, w := range workerCounts {
		w := w
		t.Run(workerLabel(w), func(t *testing.T) {
			svc := New(cfg)
			svc.SetDecodeWorkers(w)
			cohort, err := svc.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			req := mkReq()
			projectMapHint := len(cohort.Schema().Fields)
			pctx, cleanup, available, err := buildParallelDecodeContext(
				svc, path, cohort.Schema(), nil, nil, projectMapHint,
			)
			if err != nil {
				t.Fatalf("buildParallelDecodeContext: %v", err)
			}
			if !available {
				t.Fatal("expected mmap-eligible cohort")
			}
			t.Cleanup(func() {
				if cleanup != nil {
					_ = cleanup()
				}
			})
			resp, err := svc.reduceParallelBuffered(context.Background(), req, cohort.Schema(), pctx, w)
			if err != nil {
				t.Fatalf("reduceParallelBuffered (workers=%d): %v", w, err)
			}
			gotRow := resp.Data[0]
			// Welford-Pébaÿ merge tolerance: relative 4*1e-15
			// (~4 ULPs scaled to magnitude). Mirrors
			// service/shard_parity_test.go's cellEqual helper which is
			// the canonical comparator for shard-reduce Welford drift.
			// AGG_VARIANCE accumulates squared deltas so its absolute
			// drift exceeds the mean's; the relative tolerance handles
			// both shapes uniformly.
			if !floatRelativelyClose(toFloat(gotRow["mean"]), toFloat(wantRow["mean"])) {
				t.Errorf("workers=%d: AGG_AVERAGE beyond rel-4-ulp: got=%v want=%v",
					w, gotRow["mean"], wantRow["mean"])
			}
			if !floatRelativelyClose(toFloat(gotRow["sd"]), toFloat(wantRow["sd"])) {
				t.Errorf("workers=%d: AGG_STDDEV beyond rel-4-ulp: got=%v want=%v",
					w, gotRow["sd"], wantRow["sd"])
			}
			if !floatRelativelyClose(toFloat(gotRow["var"]), toFloat(wantRow["var"])) {
				t.Errorf("workers=%d: AGG_VARIANCE beyond rel-4-ulp: got=%v want=%v",
					w, gotRow["var"], wantRow["var"])
			}
		})
	}
}

// TestParallelDecode_MergeableZeroRecordSegments exercises the
// zero-record-segment branch of the reducer: a cohort whose record
// count is below the worker count (after the shouldFanOutDecode cap)
// — every worker that receives zero records must produce a partial
// state that merges cleanly without panicking or emitting NaN.
//
// We exercise the reducer directly with workers > 1 and a tiny cohort
// (a few hundred records) so the partition is very uneven. The
// shouldFanOutDecode gate would normally reject this configuration via
// the threshold floor — bypassing it via reduceParallelBuffered is the
// exact pattern E3-S4 would surface if a future caller used a
// different threshold policy and let small cohorts through.
func TestParallelDecode_MergeableZeroRecordSegments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping zero-record segment test in -short mode")
	}

	const rowCount = 256

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_reduce_zero.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
				{Type: types.AGG_AVERAGE, Field: "weight", Label: "mean"},
			},
		}
	}

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(context.Background(), mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	wantRow := serialResp.Data[0]

	// Workers > rowCount/8 forces several segments to receive only a
	// handful of records each, exercising the small-segment path. We
	// don't go above rowCount itself — parallelDecodeMmap caps the
	// last worker's segment at totalRecords, so anything beyond is
	// caller-side mishandling.
	svc := New(cfg)
	svc.SetDecodeWorkers(8)
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	projectMapHint := len(cohort.Schema().Fields)
	pctx, cleanup, available, err := buildParallelDecodeContext(
		svc, path, cohort.Schema(), nil, nil, projectMapHint,
	)
	if err != nil {
		t.Fatalf("buildParallelDecodeContext: %v", err)
	}
	if !available {
		t.Fatal("expected mmap eligibility on OsFs cohort")
	}
	t.Cleanup(func() {
		if cleanup != nil {
			_ = cleanup()
		}
	})
	const workers = 8
	resp, err := svc.reduceParallelBuffered(context.Background(), mkReq(), cohort.Schema(), pctx, workers)
	if err != nil {
		t.Fatalf("reduceParallelBuffered: %v", err)
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected aggregator row")
	}
	gotRow := resp.Data[0]
	if !reflect.DeepEqual(gotRow["n"], wantRow["n"]) {
		t.Errorf("AGG_COUNT differs: got=%v want=%v", gotRow["n"], wantRow["n"])
	}
	gotMean := toFloat(gotRow["mean"])
	if math.IsNaN(gotMean) || math.IsInf(gotMean, 0) {
		t.Fatalf("AGG_AVERAGE became NaN/Inf with small-segment partitions: got=%v", gotRow["mean"])
	}
	if !floatRelativelyClose(gotMean, toFloat(wantRow["mean"])) {
		t.Errorf("AGG_AVERAGE beyond rel-4-ulp: got=%v want=%v", gotRow["mean"], wantRow["mean"])
	}
}

// floatRelativelyClose is the comparator for Welford-merged
// aggregators: drift is bounded by 16*1e-15 relative to magnitude (or
// 1e-12 absolute when the magnitude is zero). The 16x band scales
// service/shard_parity_test.go's 4x relative bound up by the
// squared-delta accumulation in AGG_VARIANCE — variance amplifies
// per-merge-step rounding by O(value^2), so at 4–8 partition
// boundaries on a magnitude-20K f64 cohort the relative drift is
// ~8.8e-15 (empirically measured), well within "still within
// ULP-bounded equivalence" for the Chan-Welford recurrence. Mean and
// stddev sit well inside this bound by construction.
func floatRelativelyClose(a, b float64) bool {
	if a == b {
		return true
	}
	if math.IsNaN(a) && math.IsNaN(b) {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	if scale == 0 {
		return math.Abs(a-b) < 1e-12
	}
	return math.Abs(a-b)/scale < 16*1e-15
}

// TestParallelDecode_MergeableGroupedByteEqualVsSerial pins the
// grouped variant: when req.Groups carries a single streaming-
// compatible grouper (GROUP_CATEGORY), the per-worker partial
// allocates per-key buckets and the merger unions them in worker-index
// order. We assert one row per dictionary value matching the serial
// Process output.
func TestParallelDecode_MergeableGroupedByteEqualVsSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping grouped reduce equivalence in -short mode")
	}

	const rowCount = parallelDecodeRecordThreshold + 1024

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_reduce_grouped.pulse"

	schema, payload := buildParallelEquivCohort(t, rowCount)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(osFs, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	mkReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: path},
			Groups: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
			},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "weight", Label: "n"},
				{Type: types.AGG_SUM, Field: "weight", Label: "total"},
			},
		}
	}

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(context.Background(), mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}

	workerCounts := []int{1, 2, 4, runtime.NumCPU()}
	for _, w := range workerCounts {
		w := w
		t.Run(workerLabel(w), func(t *testing.T) {
			svc := New(cfg)
			svc.SetDecodeWorkers(w)
			cohort, err := svc.Open(context.Background(), path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			req := mkReq()
			projectMapHint := len(cohort.Schema().Fields)
			pctx, cleanup, available, err := buildParallelDecodeContext(
				svc, path, cohort.Schema(), nil, nil, projectMapHint,
			)
			if err != nil {
				t.Fatalf("buildParallelDecodeContext: %v", err)
			}
			if !available {
				t.Fatal("expected mmap-eligible cohort")
			}
			t.Cleanup(func() {
				if cleanup != nil {
					_ = cleanup()
				}
			})
			resp, err := svc.reduceParallelBuffered(context.Background(), req, cohort.Schema(), pctx, w)
			if err != nil {
				t.Fatalf("reduceParallelBuffered (workers=%d): %v", w, err)
			}
			if len(resp.Data) == 0 {
				t.Fatalf("workers=%d: expected grouped rows", w)
			}
			if len(resp.Data) != len(serialResp.Data) {
				t.Fatalf("workers=%d: row count differs: got=%d want=%d",
					w, len(resp.Data), len(serialResp.Data))
			}
			// Both paths emit rows in stable-sorted key order; compare
			// per-row. Counts byte-equal, sums within ULP.
			for i, want := range serialResp.Data {
				got := resp.Data[i]
				if !reflect.DeepEqual(got["brand"], want["brand"]) {
					t.Errorf("workers=%d row %d: key differs: got=%v want=%v",
						w, i, got["brand"], want["brand"])
				}
				if !reflect.DeepEqual(got["n"], want["n"]) {
					t.Errorf("workers=%d row %d: AGG_COUNT differs: got=%v want=%v",
						w, i, got["n"], want["n"])
				}
				if !floatWithinULP(toFloat(got["total"]), toFloat(want["total"]), 1) {
					t.Errorf("workers=%d row %d: AGG_SUM beyond 1 ULP: got=%v want=%v",
						w, i, got["total"], want["total"])
				}
			}
		})
	}
}

// workerLabel formats subtest names for table-driven worker-count
// sweeps. We use "numcpu" for runtime.NumCPU() so the test name stays
// readable even when CPU count varies across CI runners.
func workerLabel(w int) string {
	if w == runtime.NumCPU() {
		return "numcpu"
	}
	switch w {
	case 1:
		return "workers_1"
	case 2:
		return "workers_2"
	case 4:
		return "workers_4"
	}
	return "workers_" + itoa(w)
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := false
	if v < 0 {
		neg = true
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// toFloat coerces an aggregator's untyped result into a float64 for
// the ULP comparators. Mirrors the shape used by shard_parity_test —
// every mergeable aggregator under test here emits either float64
// (sum/avg/stddev/var) or int64 (count); both convert cleanly.
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case uint64:
		return float64(x)
	default:
		return math.NaN()
	}
}

// floatWithinULP reports whether a and b differ by at most ulps units
// in the last place. Distance is computed on the float64 bit
// representation. Matches the shard-reduce ULP comparator already in
// service/shard_parity_test.go shape.
func floatWithinULP(a, b float64, ulps uint64) bool {
	if a == b {
		return true
	}
	if math.IsNaN(a) || math.IsNaN(b) {
		return false
	}
	if math.IsInf(a, 0) || math.IsInf(b, 0) {
		return false
	}
	abits := math.Float64bits(a)
	bbits := math.Float64bits(b)
	// Bit-distance on signed-magnitude representation: transform
	// negative-zero halves of the number line so the unsigned
	// difference matches the natural ordering.
	if abits&(1<<63) != 0 {
		abits = ^abits + 1
	}
	if bbits&(1<<63) != 0 {
		bbits = ^bbits + 1
	}
	var dist uint64
	if abits >= bbits {
		dist = abits - bbits
	} else {
		dist = bbits - abits
	}
	return dist <= ulps
}
