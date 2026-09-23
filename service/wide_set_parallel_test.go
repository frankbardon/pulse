package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Service-level parity for a WIDE set column under the two concurrency
// knobs. E1-S3 could only reach the encoding layer — service/
// parallel_decode.go decodes into processing.Record, and both packages
// sat outside its scope — so the decode-worker and shard-worker arms
// were never exercised end to end against a set_u128 / set_u256 cohort.
//
// BOTH ARMS MUST ACTUALLY FAN OUT. The eligibility gates are silent
// bails by design: a cohort under parallelDecodeRecordThreshold, or one
// on an fs without RealPather (which fs.NewMemMap is), drops to the
// serial path and a parity assertion then compares serial against
// serial and proves nothing. Each arm below asserts its own gate opened
// before it compares anything.

const wideSetParityDictSize = 206

func wideSetParityLabel(i int) string { return fmt.Sprintf("P%03d", i) }

// wideSetParityMask is the deterministic per-row selection. Bits are
// placed one per 64-bit word plus a tail bit so every word of the mask
// carries signal: a decode that drops a word, or a reducer that merges
// only the low word, changes the answer.
func wideSetParityMask(r int) encoding.SetMask {
	var m encoding.SetMask
	m = m.WithBit(r % 8)
	m = m.WithBit(64 + r%8)
	m = m.WithBit(128 + r%8)
	m = m.WithBit(192 + r%13)
	return m
}

// wideSetParityCohort builds a two-field cohort — a set_u256 column with
// a 206-entry dictionary plus an f64 weight — and returns the schema
// with the raw record payload.
func wideSetParityCohort(t testing.TB, rowCount int) (*encoding.Schema, []byte) {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < wideSetParityDictSize; i++ {
		if _, err := dict.Add(wideSetParityLabel(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU256, Dictionary: dict, ByteOffset: 0, CsvColumnIdx: 0},
			{Name: "weight", Type: encoding.FieldTypeF64, ByteOffset: 32, CsvColumnIdx: 1},
		},
	}
	stride := schema.RecordByteSize()
	out := bytes.NewBuffer(make([]byte, 0, rowCount*stride))
	for r := 0; r < rowCount; r++ {
		if err := encoding.WriteSetMask(out, encoding.FieldTypeSetU256, wideSetParityMask(r)); err != nil {
			t.Fatalf("write tags(%d): %v", r, err)
		}
		w := float64((r*37)%509) + 0.125
		if err := encoding.WriteFieldValue(out, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
			t.Fatalf("write weight(%d): %v", r, err)
		}
	}
	return schema, out.Bytes()
}

// writeWideSetCohort lays a header + schema + payload down at path on
// the supplied fs.
func writeWideSetCohort(t testing.TB, fsys afero.Fs, path string, schema *encoding.Schema, payload []byte) {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	buf.Write(payload)
	if err := afero.WriteFile(fsys, path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

// wideSetParityRequest is the mergeable set-aggregation request both
// arms run. Every slot is a set aggregator with a MergeOnline, which is
// what makes the request eligible for either parallel path.
func wideSetParityRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "tags", Label: "n"},
			{Type: types.AGG_SET_UNION, Field: "tags", Label: "union"},
			{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "freq"},
			{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "card_sum"},
			{Type: types.AGG_SET_CARDINALITY_AVG, Field: "tags", Label: "card_avg"},
			{Type: types.AGG_SET_DISTINCT_VALUES, Field: "tags", Label: "distinct"},
		},
	}
}

// assertWideSetRowNonDegenerate fails when the aggregation row could
// have been produced by a low-64-bit-only fold. Without this the parity
// assertion would happily pass on two identically truncated answers.
func assertWideSetRowNonDegenerate(t *testing.T, row map[string]any) {
	t.Helper()
	labels, ok := row["union"].([]string)
	if !ok {
		t.Fatalf("union rich value is %T, want []string", row["union"])
	}
	if len(labels) <= 8 {
		t.Fatalf("union resolved %d labels; a 256-bit fold must resolve more than the low word's 8",
			len(labels))
	}
	// At least one selected member must live at or above bit 64.
	sawHigh := false
	for i := 64; i < wideSetParityDictSize; i++ {
		for _, l := range labels {
			if l == wideSetParityLabel(i) {
				sawHigh = true
				break
			}
		}
		if sawHigh {
			break
		}
	}
	if !sawHigh {
		t.Fatal("union resolved no member at or above bit 64 — the fold truncated to the low word")
	}
}

// TestWideSet_DecodeWorkersParity pins Options.DecodeWorkers on a
// single-file wide-set cohort. The fixture is deliberately larger than
// parallelDecodeRecordThreshold and lives on the real OS filesystem,
// because the eligibility predicate needs BOTH (record count >=
// threshold, and mmap via RealPather) and bails silently on either.
func TestWideSet_DecodeWorkersParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping wide-set decode-worker parity in -short mode")
	}

	const rowCount = parallelDecodeRecordThreshold + 4096

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/wide_set_parity.pulse"

	schema, payload := wideSetParityCohort(t, rowCount)
	writeWideSetCohort(t, osFs, path, schema, payload)

	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	if !processing.CanMergeRequest(wideSetParityRequest(path), schema) {
		t.Fatal("wide-set aggregation request is not mergeable; the parallel decode path would never engage")
	}

	// Gate check: the parallel decode path must actually be available
	// for this cohort. A silent bail here is exactly the failure mode
	// this test exists to rule out.
	svc := New(cfg)
	svc.SetDecodeWorkers(4)
	cohort, err := svc.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pctx, cleanup, available, err := buildParallelDecodeContext(
		svc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields),
	)
	if err != nil {
		t.Fatalf("buildParallelDecodeContext: %v", err)
	}
	t.Cleanup(func() {
		if cleanup != nil {
			_ = cleanup()
		}
	})
	if !available {
		t.Fatal("parallel decode unavailable for an OsFs cohort above threshold — the parity check below would compare serial against serial")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}
	_ = pctx

	// Serial baseline: DecodeWorkers == 1 forces the serial path.
	serialSvc := New(cfg)
	serialSvc.SetDecodeWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), wideSetParityRequest(path))
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected an aggregation row")
	}
	wantRow := serialResp.Data[0]
	assertWideSetRowNonDegenerate(t, wantRow)

	for _, workers := range []int{2, 4, runtime.NumCPU()} {
		workers := workers
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetDecodeWorkers(workers)
			resp, err := parSvc.Process(context.Background(), wideSetParityRequest(path))
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if len(resp.Data) == 0 {
				t.Fatal("parallel: expected an aggregation row")
			}
			gotRow := resp.Data[0]
			for _, key := range []string{"n", "union", "freq", "card_sum", "card_avg", "distinct"} {
				if !reflect.DeepEqual(gotRow[key], wantRow[key]) {
					t.Errorf("workers=%d: %q differs\n  parallel: %v\n  serial:   %v",
						workers, key, gotRow[key], wantRow[key])
				}
			}
			// Run components must agree. PER-SLOT
			// Components.Aggregations is deliberately NOT asserted
			// here: neither parallel reducer emits it on any
			// aggregator, set or numeric — shardPartial carries a
			// single primary-field nullRecords counter and no per-slot
			// {n, n_null} floor, so finalizeMergedPartial can only
			// populate Components.Run. That gap predates wide sets and
			// closing it is a per-slot floor-tracking change across
			// both arms and their mergers, not a set-type change.
			if resp.Components == nil || serialResp.Components == nil {
				t.Fatal("expected Components on both arms")
			}
			if !reflect.DeepEqual(resp.Components.Run, serialResp.Components.Run) {
				t.Errorf("workers=%d: Components.Run differs\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Run, serialResp.Components.Run)
			}
		})
	}
}

// TestWideSet_ShardWorkersParity pins Options.ShardWorkers on a wide-set
// SHARD ARCHIVE. The shard path has no record-count threshold and no
// mmap requirement — its gate is "archive-backed, mergeable, and
// ShardWorkers != 1" — so a small in-memory fixture is enough, but the
// gate is asserted here too rather than assumed.
func TestWideSet_ShardWorkersParity(t *testing.T) {
	cfg := fs.NewMemMap()
	svc := New(cfg)

	const shardCount = 3
	const rowsPerShard = 400

	schema, _ := wideSetParityCohort(t, 0)
	shardPaths := make([]string, 0, shardCount)
	for s := 0; s < shardCount; s++ {
		// Each shard gets a distinct row window so the per-shard
		// partials differ and a merge that drops a shard is visible.
		var payload bytes.Buffer
		for r := 0; r < rowsPerShard; r++ {
			row := s*rowsPerShard + r
			if err := encoding.WriteSetMask(&payload, encoding.FieldTypeSetU256, wideSetParityMask(row)); err != nil {
				t.Fatalf("write tags: %v", err)
			}
			w := float64((row*37)%509) + 0.125
			if err := encoding.WriteFieldValue(&payload, encoding.FieldTypeF64, math.Float64bits(w)); err != nil {
				t.Fatalf("write weight: %v", err)
			}
		}
		p := fmt.Sprintf("shard_%d.pulse", s)
		writeWideSetCohort(t, cfg.Fs(), p, schema, payload.Bytes())
		shardPaths = append(shardPaths, p)
	}

	const archive = "wide_set_archive.pulse"
	if err := svc.CreateShardArchive(context.Background(), archive, shardPaths); err != nil {
		t.Fatalf("CreateShardArchive: %v", err)
	}

	// Gate check: the archive must really fan out at >1 worker.
	probe := New(cfg)
	probe.SetShardWorkers(4)
	cohort, err := probe.Open(context.Background(), archive)
	if err != nil {
		t.Fatalf("Open archive: %v", err)
	}
	if got := len(cohort.Shards()); got != shardCount {
		t.Fatalf("archive holds %d shards, want %d", got, shardCount)
	}
	if _, ok := probe.shouldFanOut(wideSetParityRequest(archive), cohort); !ok {
		t.Fatal("shouldFanOut refused the wide-set archive — the parity check below would compare serial against serial")
	}

	// Serial baseline: ShardWorkers == 1 forces the serial shardIter.
	serialSvc := New(cfg)
	serialSvc.SetShardWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), wideSetParityRequest(archive))
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if len(serialResp.Data) == 0 {
		t.Fatal("serial: expected an aggregation row")
	}
	wantRow := serialResp.Data[0]
	assertWideSetRowNonDegenerate(t, wantRow)

	for _, workers := range []int{2, 3, 8} {
		workers := workers
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetShardWorkers(workers)
			resp, err := parSvc.Process(context.Background(), wideSetParityRequest(archive))
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if len(resp.Data) == 0 {
				t.Fatal("parallel: expected an aggregation row")
			}
			gotRow := resp.Data[0]
			for _, key := range []string{"n", "union", "freq", "card_sum", "card_avg", "distinct"} {
				if !reflect.DeepEqual(gotRow[key], wantRow[key]) {
					t.Errorf("shardWorkers=%d: %q differs\n  parallel: %v\n  serial:   %v",
						workers, key, gotRow[key], wantRow[key])
				}
			}
		})
	}
}
