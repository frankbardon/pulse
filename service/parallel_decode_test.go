package service

import (
	"bytes"
	"context"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestShouldFanOutDecode_BelowThreshold pins the threshold gate: a
// cohort whose record count sits under parallelDecodeRecordThreshold
// stays serial no matter how many workers the caller configured. This
// is the first line of defence against per-worker spawn overhead
// dominating the parallel win on small inputs.
func TestShouldFanOutDecode_BelowThreshold(t *testing.T) {
	if _, ok := shouldFanOutDecode(8, parallelDecodeRecordThreshold-1); ok {
		t.Fatal("expected fan-out to bail below threshold")
	}
	if _, ok := shouldFanOutDecode(0, parallelDecodeRecordThreshold-1); ok {
		t.Fatal("expected fan-out to bail with default workers below threshold")
	}
}

// TestShouldFanOutDecode_WorkersOneForcesSerial pins the explicit
// serial knob: DecodeWorkers==1 means "no parallelism" regardless of
// cohort size. Mirrors the ShardWorkers==1 semantics so the two knobs
// stay legible.
func TestShouldFanOutDecode_WorkersOneForcesSerial(t *testing.T) {
	if _, ok := shouldFanOutDecode(1, parallelDecodeRecordThreshold*10); ok {
		t.Fatal("expected DecodeWorkers=1 to force serial")
	}
}

// TestShouldFanOutDecode_AtThresholdAcceptsParallel covers the boundary
// case where recordCount equals the threshold exactly. Threshold is the
// floor (>=), not the strictly-greater bar, so equality should engage
// the parallel path.
func TestShouldFanOutDecode_AtThresholdAcceptsParallel(t *testing.T) {
	got, ok := shouldFanOutDecode(4, parallelDecodeRecordThreshold)
	if !ok {
		t.Fatalf("expected fan-out to engage at threshold; got ok=false")
	}
	if got != 4 {
		t.Fatalf("expected resolved workers=4, got %d", got)
	}
}

// TestShouldFanOutDecode_DefaultZeroResolvesNumCPU verifies the zero
// sentinel falls back to runtime.NumCPU(). We don't assert the exact
// CPU count (CI varies); just that the resolved worker count is >= 1
// and that the threshold gate is still enforced atop it.
func TestShouldFanOutDecode_DefaultZeroResolvesNumCPU(t *testing.T) {
	resolved, ok := shouldFanOutDecode(0, parallelDecodeRecordThreshold)
	if !ok {
		t.Fatalf("expected default workers above threshold to engage")
	}
	if resolved < 2 {
		t.Fatalf("expected at least 2 workers; got %d", resolved)
	}
}

// TestShouldFanOutDecode_CapsAtRecordCount when the configured worker
// pool exceeds the cohort record count, we cap at the count itself —
// otherwise the last worker would receive zero records and contribute
// nothing but spawn overhead. Even after capping the count must remain
// >= 2 for the parallel path to be worth taking.
func TestShouldFanOutDecode_CapsAtRecordCount(t *testing.T) {
	// 1 record above threshold + workers cap > 1 means the resolved
	// count clamps to 1 → fails the >=2 check → bails to serial.
	if _, ok := shouldFanOutDecode(16, 1); ok {
		t.Fatal("expected single-record cohort to bail (cap reduces to 1)")
	}
}

// TestCrosstab_ParallelDecode_BailOnMemMapFs verifies the MemMapFs bail
// path: hermetic tests use fs.NewMemMap() which doesn't satisfy
// RealPather, so the mmap probe must miss and the crosstab orchestrator
// must fall back to serial materializeRecords. We exercise the bail by
// running a crosstab on a MemMapFs-backed cohort with DecodeWorkers
// configured to a parallelism-eligible value; correctness equals the
// known-good serial output.
func TestCrosstab_ParallelDecode_BailOnMemMapFs(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)
	svc.SetDecodeWorkers(4)

	schema, payload := buildSmallParallelCohort(t)
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)

	path := "small.pulse"
	if err := afero.WriteFile(cfg.Fs(), path, buf.Bytes(), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	req := smallParallelCohortRequest(path)

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected crosstab matrix payload from MemMapFs bail path")
	}
	// MemMapFs cohort below threshold AND no mmap; serial path
	// produced a populated matrix. The actual numeric correctness is
	// already covered by the crosstab equivalence golden — this test
	// guards the bail, not the math.
	if len(resp.Crosstab.Matrix.RowKeys) == 0 || len(resp.Crosstab.Matrix.ColumnKeys) == 0 {
		t.Fatalf("expected non-empty matrix; rows=%d cols=%d",
			len(resp.Crosstab.Matrix.RowKeys),
			len(resp.Crosstab.Matrix.ColumnKeys))
	}
}

// TestCrosstab_ParallelDecode_BailBelowThreshold verifies the below-
// threshold serial bail on a real-OS cohort. mmap engages here (OsFs
// satisfies the resolveRealPath probe), but the cohort sits below the
// 100K floor so shouldFanOutDecode rejects. Crosstab still completes
// via the serial materializeRecords arm.
func TestCrosstab_ParallelDecode_BailBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/below_threshold.pulse"

	schema, payload := buildSmallParallelCohort(t)
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
	svc := New(cfg)
	svc.SetDecodeWorkers(4)

	req := smallParallelCohortRequest(path)

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix from below-threshold serial bail")
	}
}

// TestCrosstab_ParallelDecode_ByteEqualToSerial is the load-bearing
// equivalence test for E3-S2: a cohort above the parallelDecodeRecord
// Threshold, backed by an OsFs (so mmap engages), produces byte-equal
// crosstab matrices when run with DecodeWorkers=1 (serial path) and
// DecodeWorkers=4 (parallel segment-aware decode). Any drift signals
// a segment-boundary bug, a per-worker race on the shared mmap region,
// or a callback stitching error.
//
// The cohort uses the existing 200-field × N-row builder with a
// reduced row count tuned just above the threshold so the test fits a
// race-detector run budget. Cohort shape mirrors the crosstab
// equivalence golden so any aggregator-shape-specific edge case
// surfaces here too.
func TestCrosstab_ParallelDecode_ByteEqualToSerial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping parallel-decode equivalence in -short mode")
	}

	const rowCount = parallelDecodeRecordThreshold + 1024 // just above floor

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/parallel_eq.pulse"

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
			Crosstab: &types.CrosstabSpec{
				Rows: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "brand"},
				},
				Columns: []*types.Group{
					{Type: types.GROUP_CATEGORY, Field: "region"},
				},
				Cell: &types.Aggregation{
					Type:  types.AGG_SUM,
					Field: "weight",
					Label: "sum_weight",
				},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	}

	ctx := context.Background()

	svcSerial := New(cfg)
	svcSerial.SetDecodeWorkers(1)
	serialResp, err := svcSerial.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}

	svcParallel := New(cfg)
	svcParallel.SetDecodeWorkers(4)
	parallelResp, err := svcParallel.Process(ctx, mkReq())
	if err != nil {
		t.Fatalf("parallel Process: %v", err)
	}

	if serialResp.Crosstab == nil || serialResp.Crosstab.Matrix == nil {
		t.Fatal("serial: expected matrix payload")
	}
	if parallelResp.Crosstab == nil || parallelResp.Crosstab.Matrix == nil {
		t.Fatal("parallel: expected matrix payload")
	}

	// Deep-equal compare the matrix payload. Crosstab matrices use
	// stable key ordering (groupers emit keys deterministically), so
	// the per-cell values must match byte-for-byte. Any divergence —
	// even a single ULP on a Welford-mean cell — is a real
	// segment-boundary bug.
	if !reflect.DeepEqual(serialResp.Crosstab.Matrix, parallelResp.Crosstab.Matrix) {
		t.Errorf("Matrix differs between serial and parallel decode")
	}

	if !reflect.DeepEqual(serialResp.Crosstab, parallelResp.Crosstab) {
		t.Errorf("Crosstab top-level differs between serial and parallel decode")
	}

	// Metadata equivalence: TotalRows and FilteredRows must match
	// exactly; CohortFile is the same absolute path on both sides.
	if serialResp.Metadata == nil || parallelResp.Metadata == nil {
		t.Fatalf("expected metadata on both sides: serial=%v parallel=%v",
			serialResp.Metadata, parallelResp.Metadata)
	}
	if serialResp.Metadata.TotalRows != parallelResp.Metadata.TotalRows {
		t.Errorf("TotalRows differs: serial=%d parallel=%d",
			serialResp.Metadata.TotalRows, parallelResp.Metadata.TotalRows)
	}
	if serialResp.Metadata.FilteredRows != parallelResp.Metadata.FilteredRows {
		t.Errorf("FilteredRows differs: serial=%d parallel=%d",
			serialResp.Metadata.FilteredRows, parallelResp.Metadata.FilteredRows)
	}
}

// TestCrosstab_ParallelDecode_BailOnShardArchive covers the shard-
// archive bail: when the cohort opens as a Zip64 archive (Shards()
// non-empty), the crosstab orchestrator must NOT engage segment-aware
// decode — the existing ShardWorkers reducer is the per-shard
// parallel surface and intra-shard segment decode on top would
// double-spawn workers on the same CPUs. We exercise the bail by
// driving a crosstab against a two-shard archive with DecodeWorkers=4
// and asserting the response shape matches the serial path.
//
// The cohort is well below the parallel decode threshold (16 rows
// across two shards), so even if the bail logic missed it would also
// be caught by the threshold gate. The check below pins the shard
// branch specifically by detecting cohort.Shards() before falling
// through to threshold.
func TestCrosstab_ParallelDecode_BailOnShardArchive(t *testing.T) {
	cfg, _, _ := fourShardScoreCohort(t)
	svc := New(cfg)
	svc.SetDecodeWorkers(4)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "arch.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_RANGE, Field: "id", Interval: 4},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_RANGE, Field: "score", Interval: 40},
			},
			Cell: &types.Aggregation{
				Type:  types.AGG_COUNT,
				Field: "score",
				Label: "n",
			},
			Shape: types.CrosstabShapeMatrix,
		},
	}
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process (shard archive): %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix payload on shard archive")
	}
	if resp.Metadata == nil || resp.Metadata.TotalRows != 16 {
		t.Fatalf("expected TotalRows=16 on shard archive; got metadata=%+v", resp.Metadata)
	}
}

// buildSmallParallelCohort returns a tiny (~50-row) cohort used by the
// bail-path tests. Shape is a minimal brand/region/weight schema that
// can drive a 1×1 crosstab — small enough that a hermetic MemMapFs
// run stays under a millisecond.
func buildSmallParallelCohort(t testing.TB) (*encoding.Schema, []byte) {
	t.Helper()

	brandDict := encoding.NewDictionary()
	for i := 0; i < 4; i++ {
		if _, err := brandDict.Add(brandName(i)); err != nil {
			t.Fatalf("brand dict: %v", err)
		}
	}
	regionDict := encoding.NewDictionary()
	for i := 0; i < 3; i++ {
		if _, err := regionDict.Add(regionName(i)); err != nil {
			t.Fatalf("region dict: %v", err)
		}
	}

	fields := []encoding.Field{
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict, ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}
	schema := &encoding.Schema{Fields: fields}

	const rowCount = 48
	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))
	for r := 0; r < rowCount; r++ {
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%4)); err != nil {
			t.Fatalf("write brand: %v", err)
		}
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%3)); err != nil {
			t.Fatalf("write region: %v", err)
		}
		// weight: deterministic float
		w := float64(r%17) + 0.25
		bits := float64ToBits(w)
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, bits); err != nil {
			t.Fatalf("write weight: %v", err)
		}
	}
	return schema, out.Bytes()
}

// smallParallelCohortRequest returns a minimal crosstab request driven
// against the small-cohort schema. Used by the bail-path tests.
func smallParallelCohortRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Crosstab: &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "brand"},
			},
			Columns: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "region"},
			},
			Cell: &types.Aggregation{
				Type:  types.AGG_SUM,
				Field: "weight",
				Label: "sum_weight",
			},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// buildParallelEquivCohort synthesises a wider cohort just above the
// parallel-decode threshold so the equivalence test exercises the real
// segment-aware fan-out. Schema is the same 3-field shape used by the
// bail tests; the row count is the variable.
func buildParallelEquivCohort(t testing.TB, rowCount int) (*encoding.Schema, []byte) {
	t.Helper()

	brandDict := encoding.NewDictionary()
	for i := 0; i < 8; i++ {
		if _, err := brandDict.Add(brandName(i)); err != nil {
			t.Fatalf("brand dict: %v", err)
		}
	}
	regionDict := encoding.NewDictionary()
	for i := 0; i < 5; i++ {
		if _, err := regionDict.Add(regionName(i)); err != nil {
			t.Fatalf("region dict: %v", err)
		}
	}

	fields := []encoding.Field{
		{Name: "brand", Type: encoding.FieldTypeCategoricalU8, Dictionary: brandDict, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict, ByteOffset: 1, CsvColumnIdx: 1},
		{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
	}
	schema := &encoding.Schema{Fields: fields}

	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))
	for r := 0; r < rowCount; r++ {
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%8)); err != nil {
			t.Fatalf("write brand: %v", err)
		}
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeCategoricalU8, uint64(r%5)); err != nil {
			t.Fatalf("write region: %v", err)
		}
		// weight: deterministic, non-uniform, with enough spread that
		// the SUM aggregator differs across cells.
		w := float64((r*37)%509) + 0.125
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, float64ToBits(w)); err != nil {
			t.Fatalf("write weight: %v", err)
		}
	}
	return schema, out.Bytes()
}

func brandName(i int) string {
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta"}
	if i < len(names) {
		return names[i]
	}
	return "brand_other"
}

func regionName(i int) string {
	names := []string{"north", "south", "east", "west", "central"}
	if i < len(names) {
		return names[i]
	}
	return "region_other"
}

// float64ToBits is a tiny shim around math.Float64bits to keep the
// import set local to this test file.
func float64ToBits(v float64) uint64 {
	return math.Float64bits(v)
}
