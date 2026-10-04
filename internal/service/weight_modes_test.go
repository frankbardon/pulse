package service

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/fs"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Weighted core aggregators across the parallel reducers
// (weighting-descriptive E1-S3): the per-shard (ShardWorkers) and
// per-segment (DecodeWorkers) arms build their aggregators off the
// stamped spec, fold the weighted floor and the invalid-row tally per
// partition and merge them, so a weighted request answers the same —
// figures within the Welford-merge ULP bound, counts exactly, one
// merged PULSE_WEIGHT_INVALID_ROWS warning — whatever the worker count.

func weightModesSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 4, CsvColumnIdx: 1, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, ByteOffset: 12, CsvColumnIdx: 2, Nullable: true},
	}}
}

// weightModesRows: score null on every 7th row; weight null on every
// 11th, negative on every 13th, NaN on every 17th, zero on every 19th,
// otherwise 0.5 + (i%5)·0.75.
func weightModesRows(n, offset int) ([][]uint64, func(r, f int) bool) {
	recs := make([][]uint64, n)
	for i := range recs {
		g := offset + i
		w := 0.5 + float64(g%5)*0.75
		switch {
		case g%13 == 0:
			w = -1
		case g%17 == 0:
			w = math.NaN()
		case g%19 == 0:
			w = 0
		}
		recs[i] = []uint64{uint64(g), math.Float64bits(float64(g%23)*1.25 + 0.1), math.Float64bits(w)}
	}
	return recs, func(r, f int) bool {
		g := offset + r
		return (f == 1 && g%7 == 0) || (f == 2 && g%11 == 0)
	}
}

func weightModesRequest(path string) *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: path},
		Weight: &types.WeightSpec{Field: "w"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "sum"},
			{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"},
			{Type: types.AGG_VARIANCE, Field: "score", Label: "var"},
			{Type: types.AGG_STDDEV, Field: "score", Label: "sd"},
			{Type: types.AGG_COUNT, Field: "score", Label: "base", Weight: types.NullSlotWeight()},
		},
	}
}

func weightModesArchive(t testing.TB, shardRows []int) []byte {
	t.Helper()
	schema := weightModesSchema()
	var total uint64
	var doc, buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	payloads := make([][]byte, len(shardRows))
	offset := 0
	for i, n := range shardRows {
		recs, nullAt := weightModesRows(n, offset)
		payloads[i] = writeNullablePulse(t, schema, recs, nullAt)
		total += uint64(n)
		offset += n
	}
	if err := encx.WriteSchemaDoc(&doc, schema, total, uint16(len(shardRows))); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	write(encx.ReservedSchemaName, doc.Bytes())
	for i := range payloads {
		write(fmt.Sprintf("s%d.pulse", i), payloads[i])
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// assertWeightedAgree compares a parallel weighted response to the
// serial one: scalars within 1e-12 relative, raw counts and the
// invalid-row figures exactly, and exactly one merged warning.
func assertWeightedAgree(t *testing.T, got, want *types.Response) {
	t.Helper()
	close := func(a, b float64) bool {
		if b == 0 {
			return math.Abs(a) <= 1e-12
		}
		return math.Abs(a-b) <= 1e-12*math.Abs(b)
	}
	for k, wv := range want.Data[0] {
		if !close(got.Data[0][k].(float64), wv.(float64)) {
			t.Errorf("data[%s] = %v, serial %v", k, got.Data[0][k], wv)
		}
	}
	ga, wa := got.Components.Aggregations, want.Components.Aggregations
	for i := range wa {
		if ga[i].N != wa[i].N || ga[i].NNull != wa[i].NNull {
			t.Errorf("slot %d floor %d/%d, serial %d/%d", i, ga[i].N, ga[i].NNull, wa[i].N, wa[i].NNull)
		}
		if (wa[i].SumWeights == nil) != (ga[i].SumWeights == nil) {
			t.Fatalf("slot %d: weighted floor presence differs", i)
		}
		if wa[i].SumWeights == nil {
			continue
		}
		if !close(*ga[i].SumWeights, *wa[i].SumWeights) || !close(*ga[i].NEff, *wa[i].NEff) || *ga[i].NWeightInvalid != *wa[i].NWeightInvalid {
			t.Errorf("slot %d weighted floor %v/%v/%v, serial %v/%v/%v", i,
				*ga[i].SumWeights, *ga[i].NEff, *ga[i].NWeightInvalid, *wa[i].SumWeights, *wa[i].NEff, *wa[i].NWeightInvalid)
		}
	}
	var gw, ww []*types.ResponseWarning
	for _, w := range got.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_INVALID_ROWS) {
			gw = append(gw, w)
		}
	}
	for _, w := range want.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_INVALID_ROWS) {
			ww = append(ww, w)
		}
	}
	if len(gw) != 1 || len(ww) != 1 {
		t.Fatalf("invalid-row warnings: parallel %d, serial %d, want 1 each", len(gw), len(ww))
	}
	if fmt.Sprint(gw[0].Details) != fmt.Sprint(ww[0].Details) {
		t.Errorf("warning details: parallel %v, serial %v", gw[0].Details, ww[0].Details)
	}
}

// assertWeightedUseful guards the serial baseline: the fixture must
// exercise invalid weights and a weighted floor, or agreement proves
// nothing.
func assertWeightedUseful(t *testing.T, resp *types.Response) {
	t.Helper()
	a := resp.Components.Aggregations
	if a[0].SumWeights == nil || *a[0].NWeightInvalid == 0 || a[5].SumWeights != nil {
		t.Fatalf("serial baseline not weighted as designed: %+v", a)
	}
	if resp.Data[0]["n"].(float64) == resp.Data[0]["base"].(float64) {
		t.Fatal("weighted count equals the raw base — the weight did not apply")
	}
}

func TestShardWorkers_WeightedParity(t *testing.T) {
	archive := weightModesArchive(t, []int{90, 77, 101})
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "w.pulse", archive, 0o644); err != nil {
		t.Fatal(err)
	}
	req := weightModesRequest("w.pulse")
	if !processing.CanMergeRequest(processing.StampWeights(req, nil), weightModesSchema()) {
		t.Fatal("weighted fixture request is not mergeable")
	}
	serial := New(cfg)
	serial.SetShardWorkers(1)
	want, err := serial.Process(context.Background(), weightModesRequest("w.pulse"))
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedUseful(t, want)
	for _, workers := range []int{2, 3} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			par := New(cfg)
			par.SetShardWorkers(workers)
			cohort, err := par.Open(context.Background(), "w.pulse")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := par.shouldFanOut(req, cohort); !ok {
				t.Fatal("shouldFanOut refused; the test would compare serial to serial")
			}
			got, err := par.Process(context.Background(), weightModesRequest("w.pulse"))
			if err != nil {
				t.Fatal(err)
			}
			assertWeightedAgree(t, got, want)
		})
	}

	// Strict: the merged tally is an error, not a warning.
	strict := New(cfg)
	strict.SetShardWorkers(2)
	strict.SetStrict(true)
	if _, err := strict.Process(context.Background(), weightModesRequest("w.pulse")); !errors.HasCode(err, errors.PULSE_WEIGHT_INVALID_ROWS) {
		t.Fatalf("strict shard-parallel: err = %v, want PULSE_WEIGHT_INVALID_ROWS", err)
	}

	// Options.DefaultWeight reaches the per-shard workers.
	def := New(cfg)
	def.SetShardWorkers(2)
	def.SetDefaultWeight(&types.WeightSpec{Field: "w"})
	noReqWeight := weightModesRequest("w.pulse")
	noReqWeight.Weight = nil
	got, err := def.Process(context.Background(), noReqWeight)
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedAgree(t, got, want)
}

func TestDecodeWorkers_WeightedParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping decode-worker weighted parity in -short mode")
	}
	const rowCount = parallelDecodeRecordThreshold + 4096
	recs, nullAt := weightModesRows(rowCount, 0)
	payload := writeNullablePulse(t, weightModesSchema(), recs, nullAt)
	dir := t.TempDir()
	osFs := afero.NewOsFs()
	path := dir + "/weighted.pulse"
	if err := afero.WriteFile(osFs, path, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := fs.New(fs.WithFs(osFs), fs.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	serial := New(cfg)
	serial.SetDecodeWorkers(1)
	want, err := serial.Process(context.Background(), weightModesRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedUseful(t, want)

	par := New(cfg)
	par.SetDecodeWorkers(4)
	cohort, err := par.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, perr := par.processSingleFileParallelMaybe(context.Background(), processing.StampWeights(weightModesRequest(path), nil), cohort, path); perr != nil || !ok {
		t.Fatalf("parallel decode did not engage (ok=%v err=%v); the test would compare serial to serial", ok, perr)
	}
	got, err := par.Process(context.Background(), weightModesRequest(path))
	if err != nil {
		t.Fatal(err)
	}
	assertWeightedAgree(t, got, want)
}
