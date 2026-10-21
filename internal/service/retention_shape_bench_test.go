package service

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/frankbardon/pulse/internal/limits"
	"github.com/frankbardon/pulse/types"
)

// BenchmarkRetentionShape (U51 E5-S4): the retail retention chain's
// per-customer stage against the spend chain's at the same scale — a
// 12-shard archive of 2.52M order lines (one shard a month of 2025)
// over 1.2M customer ids, 4 shard workers. Retention's stage 0 carries
// 13 row-local attributes (ATTR_DATE_PART + 12 ATTR_FORMULA month
// flags) and 15 aggregations (AGG_SUM, AGG_DISTINCT_COUNT, AGG_MIN, 12
// AGG_MAX) per customer; spend's carries 3 aggregations and no
// attribute; retention-budget is retention under a MaxEstimatedMemory
// that shrinks the merge window. The archive is cached under
// $PULSE_BENCH_DIR. Run one
// shape per process under /usr/bin/time -l for RSS; figures in
// docs/internals/BENCHMARKS_BASELINE.md. Never run in CI.
func BenchmarkRetentionShape(b *testing.B) {
	dir := matrixScaleDir(b)
	cfg := matrixScaleOSConfig(b, dir)
	path := retentionShapeArchive(b, dir, 12, 210_000, 1_200_000)
	for _, shape := range []string{"retention", "spend", "retention-budget"} {
		b.Run(shape, func(b *testing.B) {
			svc := New(cfg)
			svc.SetProjectBufferedFields(true)
			svc.SetShardWorkers(4)
			svc.SetDecodeWorkers(4)
			svc.SetDisableComponents(true)
			if shape == "retention-budget" {
				// A MaxEstimatedMemory between the one-partial (10.55 GB)
				// and full-window (13.56 GB) estimates of stage 0 (U51
				// E5-S7; 33.4 / 39.6 GB before): the merge window shrinks
				// from 8 to 2 (U51 E5-S4 (c)).
				l := limits.Defaults()
				l.MaxEstimatedMemory = 11_200_000_000
				svc.SetLimits(l)
			}
			ctx := context.Background()
			run := func() error {
				_, err := svc.ProcessChain(ctx, retentionShapeChain(path, shape != "spend"))
				return err
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := run(); err != nil {
					b.Fatalf("ProcessChain: %v", err)
				}
			}
			b.StopTimer()
			reportPeakHeap(b, run)
		})
	}
}

// retentionShapeMonthStarts are the epoch days of 2025-01-01 …
// 2026-01-01, the month bounds the retention flags test.
var retentionShapeMonthStarts = [13]int32{20089, 20120, 20148, 20179, 20209, 20240, 20270, 20301, 20332, 20362, 20393, 20423, 20454}

// retentionShapeSchema: the order-line fields the two chains read —
// line_id u64, order_id u64, customer_id u32, order_date date,
// net_amount f64, loyalty_member u8 (33 bytes a row).
func retentionShapeSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "line_id", Type: encoding.FieldTypeU64, ByteOffset: 0, CsvColumnIdx: 0},
		{Name: "order_id", Type: encoding.FieldTypeU64, ByteOffset: 8, CsvColumnIdx: 1},
		{Name: "customer_id", Type: encoding.FieldTypeU32, ByteOffset: 16, CsvColumnIdx: 2},
		{Name: "order_date", Type: encoding.FieldTypeDate, ByteOffset: 20, CsvColumnIdx: 3},
		{Name: "net_amount", Type: encoding.FieldTypeF64, ByteOffset: 24, CsvColumnIdx: 4},
		{Name: "loyalty_member", Type: encoding.FieldTypeU8, ByteOffset: 32, CsvColumnIdx: 5},
	}}
}

// retentionShapeArchive returns the cached archive: shards months of
// perShard rows, customer ids uniform over [50001, 50001+customers),
// each row's date uniform within its shard's month, its order id the
// customer and day, its amount a hash-scaled non-dyadic figure.
func retentionShapeArchive(tb testing.TB, dir string, shards, perShard, customers int) string {
	tb.Helper()
	path := filepath.Join(dir, fmt.Sprintf("retention_shape_v1_s%d_r%d_c%d.pulse", shards, perShard, customers))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		tb.Fatalf("create %s: %v", path, err)
	}
	defer os.Remove(tmp.Name())
	bw := bufio.NewWriterSize(tmp, 4<<20)
	schema := retentionShapeSchema()
	zw := zip.NewWriter(bw)
	entry := func(name string) (*bufio.Writer, error) {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			return nil, err
		}
		return bufio.NewWriterSize(w, 4<<20), nil
	}
	var w *bufio.Writer
	if w, err = entry(encx.ReservedSchemaName); err == nil {
		if err = encx.WriteSchemaDoc(w, schema, uint64(shards*perShard), uint16(shards)); err == nil {
			err = w.Flush()
		}
	}
	for m := 0; err == nil && m < shards; m++ {
		if w, err = entry(fmt.Sprintf("orders-2025-%02d.pulse", m+1)); err != nil {
			break
		}
		if err = encoding.WriteHeader(w); err == nil {
			err = encoding.WriteSchema(w, schema)
		}
		if err == nil {
			err = writeRetentionShapeRows(w, m, perShard, customers)
		}
		if err == nil {
			err = w.Flush()
		}
	}
	if err == nil {
		err = zw.Close()
	}
	if err == nil {
		err = bw.Flush()
	}
	if err == nil {
		err = tmp.Close()
	}
	if err != nil {
		tb.Fatalf("generate %s: %v", path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		tb.Fatalf("rename %s: %v", path, err)
	}
	return path
}

// writeRetentionShapeRows writes month m's n rows (a function of the
// month and row index alone).
func writeRetentionShapeRows(w io.Writer, m, n, customers int) error {
	row := make([]byte, 33)
	lo, days := retentionShapeMonthStarts[m%12], retentionShapeMonthStarts[m%12+1]-retentionShapeMonthStarts[m%12]
	for r := range n {
		g := uint64(m*n + r)
		h := matrixScaleHash(g, 7)
		cust := uint32(50001 + h%uint64(customers))
		day := lo + int32((h>>32)%uint64(days))
		amount := math.Round(float64(matrixScaleUnit(matrixScaleHash(g, 8))+1)*25000) / 100
		binary.LittleEndian.PutUint64(row[0:], uint64(m+1)*100_000_000+uint64(r)+1)
		binary.LittleEndian.PutUint64(row[8:], uint64(cust)*100_000+uint64(day))
		binary.LittleEndian.PutUint32(row[16:], cust)
		binary.LittleEndian.PutUint32(row[20:], uint32(day))
		binary.LittleEndian.PutUint64(row[24:], math.Float64bits(amount))
		row[32] = byte(h>>61) & 1
		if _, err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// retentionShapeCohort is the single-file twin: month 0's generator
// over n rows.
func retentionShapeCohort(tb testing.TB, dir string, n, customers int) string {
	tb.Helper()
	path := filepath.Join(dir, fmt.Sprintf("retention_shape_v1_single_r%d_c%d.pulse", n, customers))
	if _, err := os.Stat(path); err == nil {
		return path
	}
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	bw := bufio.NewWriterSize(f, 4<<20)
	if err = encoding.WriteHeader(bw); err == nil {
		err = encoding.WriteSchema(bw, retentionShapeSchema())
	}
	if err == nil {
		err = writeRetentionShapeRows(bw, 0, n, customers)
	}
	if err == nil {
		err = bw.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		tb.Fatalf("generate %s: %v", path, err)
	}
	return path
}

// retentionShapeChain is the showcase's retention chain (retention) or
// spend-band chain over the archive at path. Rebuilt per call: a chain
// normalises its requests in place.
func retentionShapeChain(path string, retention bool) *types.ChainRequest {
	cohort := &types.Cohort{Filename: path}
	byCustomer := []*types.Group{{Type: types.GROUP_CATEGORY, Field: "customer_id"}}
	if !retention {
		return &types.ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{
			{Name: "per_customer", Request: &types.Request{Groups: byCustomer, Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "net_amount", Label: "spend"},
				{Type: types.AGG_DISTINCT_COUNT, Field: "order_id", Label: "orders"},
				{Type: types.AGG_MAX, Field: "loyalty_member", Label: "is_member"},
			}}},
			{Name: "by_spend_band", Request: &types.Request{
				Attributes: []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "spend", Label: "spend_band",
					Expression: "spend < 50 ? 1 : spend < 100 ? 2 : spend < 250 ? 3 : spend < 500 ? 4 : spend < 1000 ? 5 : spend < 2500 ? 6 : 7"}},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "spend_band"}},
				Aggregations: []*types.Aggregation{
					{Type: types.AGG_SUM, Field: "spend", Label: "spend"},
					{Type: types.AGG_COUNT, Field: "spend", Label: "customers"},
					{Type: types.AGG_AVERAGE, Field: "orders", Label: "avg_orders"},
					{Type: types.AGG_AVERAGE, Field: "is_member", Label: "member_share"},
				},
				Sort: []types.OrderKey{{Field: "spend_band"}},
			}},
		}}
	}
	stage0 := &types.Request{Groups: byCustomer,
		Attributes: []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "order_date", Label: "month",
			Params: json.RawMessage(`{"part": "year_month"}`)}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "net_amount", Label: "spend"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "order_id", Label: "orders"},
			{Type: types.AGG_MIN, Field: "month", Label: "first_month"},
		}}
	stage1 := &types.Request{Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "first_month"}},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "spend", Label: "customers"},
			{Type: types.AGG_AVERAGE, Field: "spend", Label: "avg_spend"},
			{Type: types.AGG_AVERAGE, Field: "orders", Label: "avg_orders"},
		},
		Sort: []types.OrderKey{{Field: "first_month"}}}
	for m := range 12 {
		flag := fmt.Sprintf("in_m%02d", m+1)
		active := fmt.Sprintf("active_m%02d", m+1)
		stage0.Attributes = append(stage0.Attributes, &types.Attribute{Type: types.ATTR_FORMULA, Field: "order_date", Label: flag,
			Expression: fmt.Sprintf("order_date >= %d && order_date < %d ? 1 : 0", retentionShapeMonthStarts[m], retentionShapeMonthStarts[m+1])})
		stage0.Aggregations = append(stage0.Aggregations, &types.Aggregation{Type: types.AGG_MAX, Field: flag, Label: active})
		stage1.Aggregations = append(stage1.Aggregations, &types.Aggregation{Type: types.AGG_AVERAGE, Field: active, Label: fmt.Sprintf("m%02d", m+1)})
	}
	return &types.ChainRequest{Cohort: cohort, Stages: []*types.ChainStage{
		{Name: "per_customer", Request: stage0},
		{Name: "by_first_month", Request: stage1},
	}}
}
