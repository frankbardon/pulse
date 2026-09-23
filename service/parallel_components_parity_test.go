package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// componentsParitySchema is a three-field cohort.
//
//   - `score` is NULLABLE, so the universal floor's n_null leg carries
//     signal. A fixture with no nulls compares 0 against 0 on both
//     arms and proves nothing about n_null.
//   - `picks` is a SET column. It is here because the floor must be
//     tallied through processing.FieldPresent, not through
//     Record.NumericValue: a set column has no numeric value (the
//     accessor refuses it outright) but every row that carries a mask
//     — the empty mask included — has answered. Without a set column
//     in the fixture, swapping FieldPresent for NumericValue in the
//     reducers is invisible, which is exactly what the first draft of
//     this test failed to catch under falsification.
func componentsParitySchema() *encoding.Schema {
	dict := encoding.NewDictionary()
	for _, v := range []string{"a", "b", "c", "d"} {
		_, _ = dict.Add(v)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		{Name: "picks", Type: encoding.FieldTypeSetU8, ByteOffset: 12, CsvColumnIdx: 2, Dictionary: dict},
	}}
}

// writeNullablePulse writes a single-file `.pulse` whose records carry
// the trailing per-record null bitmap. nullAt reports whether field
// index f is null on record r.
func writeNullablePulse(t testing.TB, schema *encoding.Schema, records [][]uint64, nullAt func(r, f int) bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	bmSize := schema.BitmapByteSize()
	for ri, rec := range records {
		for fi, field := range schema.Fields {
			if err := encoding.WriteFieldValue(&buf, field.Type, rec[fi]); err != nil {
				t.Fatalf("WriteFieldValue: %v", err)
			}
		}
		if bmSize == 0 {
			continue
		}
		bitmap := make([]byte, bmSize)
		for fi := range schema.Fields {
			if nullAt != nil && nullAt(ri, fi) {
				encoding.BitmapSetNull(bitmap, fi)
			}
		}
		if err := encoding.WriteBitmap(&buf, bitmap); err != nil {
			t.Fatalf("WriteBitmap: %v", err)
		}
	}
	return buf.Bytes()
}

// componentsParityRows builds `n` records where every third row's
// score is null, so n and n_null are both non-zero and unequal.
func componentsParityRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs := make([][]uint64, n)
	for i := range recs {
		// picks: a 4-bit mask that is EMPTY on every fifth row. An
		// empty mask is a valid "answered, selected nothing" and must
		// count toward n, never n_null.
		mask := uint64(1<<uint((offset+i)%4) | 1)
		if (offset+i)%5 == 0 {
			mask = 0
		}
		recs[i] = []uint64{uint64(offset + i), math.Float64bits(float64(offset+i) * 1.5), mask}
	}
	// field index 1 == "score"
	return recs, func(r, f int) bool { return f == 1 && r%3 == 0 }
}

// componentsParityRequest exercises four aggregator slots against the
// nullable column plus one against a never-null column, so a floor
// that is merely copied from Run (rather than tracked per slot) shows
// up as a mismatch.
func componentsParityRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"},
			{Type: types.AGG_MIN, Field: "score", Label: "min"},
			{Type: types.AGG_SUM, Field: "id", Label: "id_sum"},
			// Presence over a SET column. Tallied through
			// NumericValue instead of FieldPresent, this slot reads
			// every row as null and n collapses to 0.
			{Type: types.AGG_COUNT, Field: "picks", Label: "picks_n"},
		},
	}
}

// assertAggregationComponentsUseful fails when the entries are empty or
// degenerate. Without it a parity assertion between two paths that both
// emit nothing would pass and mean nothing.
func assertAggregationComponentsUseful(t *testing.T, entries []types.AggregationComponents) {
	t.Helper()
	if len(entries) != 6 {
		t.Fatalf("expected one AggregationComponents entry per aggregator slot, got %d: %+v",
			len(entries), entries)
	}
	// The four score slots must see nulls; the id slot must see none.
	for i := 0; i < 4; i++ {
		if entries[i].NNull == 0 {
			t.Errorf("slot %d (%s): n_null = 0 over a column that is null on every third row",
				i, entries[i].Label)
		}
		if entries[i].N == 0 {
			t.Errorf("slot %d (%s): n = 0", i, entries[i].Label)
		}
	}
	if entries[4].NNull != 0 {
		t.Errorf("slot 4 (id_sum): n_null = %d over a non-nullable column", entries[4].NNull)
	}
	// The set slot: every row carries a mask (some empty), so n is the
	// full post-filter count and n_null is zero. A floor tallied
	// through NumericValue inverts both.
	if entries[5].N == 0 || entries[5].NNull != 0 {
		t.Errorf("slot 5 (picks_n): n=%d n_null=%d — a set column has no numeric value but every row has presence; the floor must use FieldPresent",
			entries[5].N, entries[5].NNull)
	}
}

// buildNullableShardArchive assembles an N-shard archive whose shards
// all carry the nullable schema.
func buildNullableShardArchive(t testing.TB, schema *encoding.Schema, shardRows []int) []byte {
	t.Helper()
	var total uint64
	payloads := make([][]byte, len(shardRows))
	offset := 0
	for i, n := range shardRows {
		recs, nullAt := componentsParityRows(n, offset)
		payloads[i] = writeNullablePulse(t, schema, recs, nullAt)
		total += uint64(n)
		offset += n
	}
	var doc bytes.Buffer
	if err := encoding.WriteSchemaDoc(&doc, schema, total, uint16(len(shardRows))); err != nil {
		t.Fatalf("WriteSchemaDoc: %v", err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip.CreateHeader(%q): %v", name, err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	write(encoding.ReservedSchemaName, doc.Bytes())
	for i := range payloads {
		write(fmt.Sprintf("s%d.pulse", i), payloads[i])
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}
	return buf.Bytes()
}

// Response.Components is a documented contract, not a best-effort
// decoration: every aggregator slot carries the universal floor
// {n, n_null}. Turning on a CONCURRENCY KNOB must not change it.
//
// The ShardWorkers arm merged operator state but carried only a single
// primary-field nullRecords counter, so Components.Aggregations came
// back nil for every aggregator while the serial path emitted a full
// per-slot floor — the same request answering two different shapes
// depending on worker count.
func TestShardWorkers_AggregationComponentsParity(t *testing.T) {
	schema := componentsParitySchema()
	archive := buildNullableShardArchive(t, schema, []int{40, 37, 41})

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "parity.pulse", archive, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := componentsParityRequest("parity.pulse")
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("fixture request is not mergeable; the shard reducer would never fan out")
	}

	serialSvc := New(cfg)
	serialSvc.SetShardWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if serialResp.Components == nil {
		t.Fatal("serial run emitted no Components at all — the fixture cannot prove parity")
	}
	want := serialResp.Components.Aggregations
	assertAggregationComponentsUseful(t, want)

	for _, workers := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetShardWorkers(workers)
			cohort, err := parSvc.Open(context.Background(), "parity.pulse")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, ok := parSvc.shouldFanOut(req, cohort); !ok {
				t.Fatalf("shouldFanOut refused a %d-worker fan-out; this would compare serial against serial", workers)
			}
			resp, err := parSvc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatal("parallel run emitted no Components")
			}
			got := resp.Components.Aggregations
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Components.Aggregations differ under ShardWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, got, want)
			}
		})
	}
}

// Same contract, other knob. The DecodeWorkers arm shares
// shardPartial / mergeShardPartials / finalizeMergedPartial with the
// shard reducer, so it inherited the same hole — plus one of its own:
// its per-record callback never incremented nullRecords at all, so
// Run.NullRecords came back 0 where serial reported the real count.
func TestDecodeWorkers_ComponentsParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping decode-worker components parity in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096

	schema := componentsParitySchema()
	recs, nullAt := componentsParityRows(rowCount, 0)
	payload := writeNullablePulse(t, schema, recs, nullAt)

	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/components_parity.pulse"
	if err := afero.WriteFile(osFs, path, payload, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}

	req := componentsParityRequest(path)
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("fixture request is not mergeable; the parallel decode reducer would never engage")
	}
	if _, ok := shouldFanOutDecode(4, rowCount); !ok {
		t.Fatal("shouldFanOutDecode refused a 4-worker fan-out above threshold")
	}

	serialSvc := New(cfg)
	serialSvc.SetDecodeWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if serialResp.Components == nil {
		t.Fatal("serial run emitted no Components at all")
	}
	want := serialResp.Components.Aggregations
	assertAggregationComponentsUseful(t, want)
	wantRun := serialResp.Components.Run
	if wantRun == nil || wantRun.NullRecords == 0 {
		t.Fatalf("serial Run components carry no null count: %+v", wantRun)
	}

	for _, workers := range []int{2, 4} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetDecodeWorkers(workers)

			// Gate check: a silent bail would compare serial to serial.
			cohort, oerr := parSvc.Open(context.Background(), path)
			if oerr != nil {
				t.Fatalf("Open: %v", oerr)
			}
			_, cleanup, available, berr := buildParallelDecodeContext(
				parSvc, path, cohort.Schema(), nil, nil, len(cohort.Schema().Fields))
			if berr != nil {
				t.Fatalf("buildParallelDecodeContext: %v", berr)
			}
			if cleanup != nil {
				defer func() { _ = cleanup() }()
			}
			if !available {
				t.Fatal("parallel decode unavailable for an OsFs cohort above threshold")
			}

			resp, err := parSvc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatal("parallel run emitted no Components")
			}
			if !reflect.DeepEqual(resp.Components.Aggregations, want) {
				t.Errorf("Components.Aggregations differ under DecodeWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Aggregations, want)
			}
			if !reflect.DeepEqual(resp.Components.Run, wantRun) {
				t.Errorf("Components.Run differs under DecodeWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Run, wantRun)
			}
		})
	}
}

// The opt-out must keep working on the parallel arms: with components
// disabled the wire form stays byte-identical to the pre-Components
// baseline, i.e. Components is nil, not a floor-only shell.
func TestShardWorkers_AggregationComponentsRespectOptOut(t *testing.T) {
	schema := componentsParitySchema()
	archive := buildNullableShardArchive(t, schema, []int{20, 21, 19})

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "optout.pulse", archive, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := componentsParityRequest("optout.pulse")
	disable := true
	req.DisableComponents = &disable

	svc := New(cfg)
	svc.SetShardWorkers(3)
	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Components != nil {
		t.Errorf("DisableComponents did not suppress Components on the shard arm: %+v", resp.Components)
	}
}

// Components.Filterers is the same contract as Components.Aggregations
// and had the same hole: the parallel reducers ran the filter chain
// with a bare loop that kept no counters, so a filtered request came
// back with no per-slot {n_in, n_out, n_null_input} triple while the
// serial path emitted one entry per Request.Filterers slot.
//
// Two filterers, so the n_in invariant is load-bearing: slot 1's n_in
// must equal slot 0's n_out, not the cohort total.
func TestShardWorkers_FiltererComponentsParity(t *testing.T) {
	schema := componentsParitySchema()
	archive := buildNullableShardArchive(t, schema, []int{40, 37, 41})

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "filters.pulse", archive, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	req := componentsParityRequest("filters.pulse")
	req.Filterers = []*types.Filterer{
		{Type: types.FILTER_RANGE, Field: "id", Values: []string{"10", "100"}},
		{Type: types.FILTER_RANGE, Field: "score", Values: []string{"30", "120"}},
	}
	if !processing.CanMergeRequest(req, schema) {
		t.Fatal("filtered fixture request is not mergeable; the shard reducer would never fan out")
	}

	serialSvc := New(cfg)
	serialSvc.SetShardWorkers(1)
	serialResp, err := serialSvc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("serial Process: %v", err)
	}
	if serialResp.Components == nil || len(serialResp.Components.Filterers) != 2 {
		t.Fatalf("serial run emitted no per-slot FiltererComponents: %+v", serialResp.Components)
	}
	want := serialResp.Components.Filterers
	if want[0].NIn == 0 || want[0].NOut == 0 || want[0].NOut == want[0].NIn {
		t.Fatalf("filter slot 0 is degenerate (n_in=%d n_out=%d); it must actually reject rows",
			want[0].NIn, want[0].NOut)
	}
	if want[1].NIn != want[0].NOut {
		t.Fatalf("serial baseline violates the n_in invariant: slot1.n_in=%d slot0.n_out=%d",
			want[1].NIn, want[0].NOut)
	}
	if want[1].NNullInput == 0 {
		t.Fatalf("filter slot 1 saw no null input over a nullable column: %+v", want[1])
	}

	for _, workers := range []int{2, 3} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			parSvc := New(cfg)
			parSvc.SetShardWorkers(workers)
			cohort, oerr := parSvc.Open(context.Background(), "filters.pulse")
			if oerr != nil {
				t.Fatalf("Open: %v", oerr)
			}
			if _, ok := parSvc.shouldFanOut(req, cohort); !ok {
				t.Fatalf("shouldFanOut refused a %d-worker fan-out", workers)
			}
			resp, err := parSvc.Process(context.Background(), req)
			if err != nil {
				t.Fatalf("parallel Process: %v", err)
			}
			if resp.Components == nil {
				t.Fatal("parallel run emitted no Components")
			}
			if !reflect.DeepEqual(resp.Components.Filterers, want) {
				t.Errorf("Components.Filterers differ under ShardWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Filterers, want)
			}
			if !reflect.DeepEqual(resp.Components.Aggregations, serialResp.Components.Aggregations) {
				t.Errorf("Components.Aggregations differ under a FILTERED request, ShardWorkers=%d\n  parallel: %+v\n  serial:   %+v",
					workers, resp.Components.Aggregations, serialResp.Components.Aggregations)
			}
		})
	}
}
