//go:build heavy

package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

func TestParallelDecode_MergeableByteEqualVsSerial(t *testing.T) {
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
			// AGG_SUM on f64 — the binned reproducible sum
			// (internal/reprosum) is a function of the multiset of
			// values, so every worker count is bit-identical to serial.
			if !reflect.DeepEqual(gotRow["total"], wantRow["total"]) {
				t.Errorf("workers=%d: AGG_SUM differs: got=%v want=%v",
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
			// internal/service/shard_parity_test.go's cellEqual helper which is
			// the canonical comparator for shard-reduce Welford drift.
			// AGG_VARIANCE accumulates squared deltas so its absolute
			// drift exceeds the mean's; the relative tolerance handles
			// both shapes uniformly. AGG_AVERAGE is a binned sum over
			// an exact integer n: bit-identical, no tolerance.
			if !reflect.DeepEqual(gotRow["mean"], wantRow["mean"]) {
				t.Errorf("workers=%d: AGG_AVERAGE differs: got=%v want=%v",
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

func TestParallelDecode_MergeableZeroRecordSegments(t *testing.T) {
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
	if !reflect.DeepEqual(gotRow["mean"], wantRow["mean"]) {
		t.Errorf("AGG_AVERAGE differs: got=%v want=%v", gotRow["mean"], wantRow["mean"])
	}
}

// TestParallelDecode_MergeableGroupedByteEqualVsSerial pins the
// grouped variant: when req.Groups carries a single streaming-
// compatible grouper (GROUP_CATEGORY), the per-worker partial
// allocates per-key buckets and the merger unions them in worker-index
// order. We assert one row per dictionary value matching the serial
// Process output.
func TestParallelDecode_MergeableGroupedByteEqualVsSerial(t *testing.T) {
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
			// per-row. Counts and binned sums bit-identical.
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
				if !reflect.DeepEqual(got["total"], want["total"]) {
					t.Errorf("workers=%d row %d: AGG_SUM differs: got=%v want=%v",
						w, i, got["total"], want["total"])
				}
			}
		})
	}
}
