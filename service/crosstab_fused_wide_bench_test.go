package service

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E2-S3 — fused crosstab over a WIDE set axis, measured on peak heap.
//
// House rule: quote peak heap, never B/op. B/op is cumulative bytes
// allocated and cannot separate "allocated and dropped" from "allocated
// and held live to the end of the pass"; both arms decode every record,
// so both pay the transient decode cost and only the buffered arm pays
// to retain it. The materialisation claim lives in peak LIVE heap, which
// reportPeakHeap samples.
//
// The wide rung adds a second question B/op also cannot answer: a
// set_u256 column is 32 bytes on the wire and lands on the Record as a
// 32-byte encoding.SetMask VALUE rather than the narrow rungs' uint64.
// Under a 206-member fan-out that value is copied per fanned key, so the
// interesting number is whether the fused arm still avoids RETAINING it.
//
// No threshold is asserted — machine variance makes an absolute bound
// flaky and a benchmark is not a gate. The recorded comparison is the
// deliverable.
//
// Run with:
//
//	go test ./service/ -bench BenchmarkCrosstabWideSetFanout -benchmem -run=^$

// wideSetBenchRows is the record-count axis. Two sizes at minimum so the
// scaling of the gap is readable straight off the output: a constant
// delta would mean the fused arm merely allocates less per call, while a
// delta that grows with rows means the per-record retention is gone.
var wideSetBenchRows = []int{25_000, 100_000}

// wideSetBenchBitSets are the selections cycled across the cohort. Every
// entry has popcount 4 and touches all four words of the set_u256 mask,
// so the fan really fires and no word can be dropped without the axis
// losing keys.
var wideSetBenchBitSets = [][]int{
	{3, 70, 133, 200},
	{11, 64, 140, 205},
	{3, 99, 133, 201},
	{27, 70, 188, 204},
	{11, 88, 140, 200},
	{3, 64, 190, 205},
	{27, 99, 133, 204},
	{11, 70, 188, 201},
}

// buildWideSetFanoutCohort writes a set_u256 cohort with a 206-member
// dictionary, a 4-way categorical column axis and an f64 cell target.
func buildWideSetFanoutCohort(b *testing.B, rows int) (*fs.Config, *encoding.Schema) {
	b.Helper()

	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south", "east", "west"} {
		if _, err := regionDict.Add(r); err != nil {
			b.Fatalf("region dict.Add: %v", err)
		}
	}
	tagsDict := encoding.NewDictionary()
	for i := 0; i < 206; i++ {
		if _, err := tagsDict.Add(fmt.Sprintf("L%03d", i)); err != nil {
			b.Fatalf("tags dict.Add: %v", err)
		}
	}
	setBytes := encoding.FieldTypeSetU256.ByteSize()
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "tags", Type: encoding.FieldTypeSetU256, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tagsDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 1 + setBytes, CsvColumnIdx: 2},
		},
	}

	masks := make([]encoding.SetMask, len(wideSetBenchBitSets))
	for i, bits := range wideSetBenchBitSets {
		var m encoding.SetMask
		for _, bit := range bits {
			m = m.WithBit(bit)
		}
		masks[i] = m
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
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeCategoricalU8, uint64(i%4)); err != nil {
			b.Fatalf("WriteFieldValue region: %v", err)
		}
		if err := encoding.WriteSetMask(&buf, encoding.FieldTypeSetU256, masks[(i*3)%len(masks)]); err != nil {
			b.Fatalf("WriteSetMask: %v", err)
		}
		if err := encoding.WriteFieldValue(&buf, encoding.FieldTypeF64, math.Float64bits(float64(i%17))); err != nil {
			b.Fatalf("WriteFieldValue value: %v", err)
		}
	}

	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), "wide_fanout.pulse", buf.Bytes(), 0644); err != nil {
		b.Fatalf("WriteFile: %v", err)
	}
	return cfg, schema
}

// wideSetFanoutRequest is rebuilt per call: Process normalises the
// request in place (smart defaults, label binding), so a shared instance
// would let iteration one's mutations leak into iteration two.
func wideSetFanoutRequest() *types.Request {
	return &types.Request{
		Cohort: &types.Cohort{Filename: "wide_fanout.pulse"},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
}

// BenchmarkCrosstabWideSetFanout drives a 206-member set_u256 fan-out
// row axis down both execution arms and reports peak-heap-MB alongside
// the -benchmem counters.
func BenchmarkCrosstabWideSetFanout(b *testing.B) {
	for _, rows := range wideSetBenchRows {
		cfg, schema := buildWideSetFanoutCohort(b, rows)
		ctx := context.Background()

		// Non-vacuity: a request the gate rejects would run buffered on
		// BOTH arms and the benchmark would compare a path with itself.
		probe := New(cfg)
		if ok, reason := processing.CanFuseCrosstab(wideSetFanoutRequest(), schema, probe.Extensions()); !ok {
			b.Fatalf("CanFuseCrosstab rejected the wide-set fan-out request: %s", reason)
		}

		for _, path := range []struct {
			name    string
			disable bool
		}{
			{name: "fused", disable: false},
			{name: "buffered", disable: true},
		} {
			b.Run(fmt.Sprintf("rows=%d/%s", rows, path.name), func(b *testing.B) {
				svc := New(cfg)
				svc.SetDisableCrosstabFusion(path.disable)
				if _, err := svc.Process(ctx, wideSetFanoutRequest()); err != nil {
					b.Fatalf("warmup Process (%s): %v", path.name, err)
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := svc.Process(ctx, wideSetFanoutRequest()); err != nil {
						b.Fatalf("Process (%s): %v", path.name, err)
					}
				}
				// Freeze the -benchmem counters before the untimed
				// peak-heap pass so its allocations are not charged to
				// B/op.
				b.StopTimer()
				reportPeakHeap(b, func() error {
					_, err := svc.Process(ctx, wideSetFanoutRequest())
					return err
				})
			})
		}
	}
}
