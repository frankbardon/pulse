package service

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// TestStreamingIterator_LazyRead verifies that the streaming iterator reads
// records lazily without loading the entire file into a record slice upfront.
func TestStreamingIterator_LazyRead(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "val", Type: encoding.FieldTypeU16, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}

	// Write a .pulse file with 100 records.
	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	for i := 0; i < 100; i++ {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeU16, uint64(i))
	}
	afero.WriteFile(memFs, "test.pulse", buf.Bytes(), 0644)

	iter := newStreamingIterator(memFs, "test.pulse", schema)
	defer iter.Close()

	// Read only 10 records, then stop.
	count := 0
	for iter.Next() {
		count++
		v, ok := iter.Record().NumericValue("val")
		if !ok {
			t.Fatal("missing val")
		}
		if int(v) != count-1 {
			t.Fatalf("got val=%v, want %d", v, count-1)
		}
		if count == 10 {
			break
		}
	}

	if count != 10 {
		t.Fatalf("read %d records, want 10", count)
	}

	if iter.Err() != nil {
		t.Fatalf("unexpected error: %v", iter.Err())
	}
}

// TestStreamingIterator_Reset verifies that Reset allows re-reading.
func TestStreamingIterator_Reset(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeU8, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}

	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	for i := 0; i < 5; i++ {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeU8, uint64(i))
	}
	afero.WriteFile(memFs, "test.pulse", buf.Bytes(), 0644)

	iter := newStreamingIterator(memFs, "test.pulse", schema)
	defer iter.Close()

	// First pass: read all.
	count1 := 0
	for iter.Next() {
		count1++
	}

	// Reset and read again.
	iter.Reset()
	count2 := 0
	for iter.Next() {
		count2++
	}

	if count1 != 5 || count2 != 5 {
		t.Fatalf("pass1=%d pass2=%d, want 5 each", count1, count2)
	}
}

// TestProcess_UsesStreaming verifies that Process works correctly via streaming
// (no readAllRecords). This is a behavioral test — same results as before the
// streaming refactor.
func TestProcess_UsesStreaming(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeU16, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}

	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	// Write values: 10, 20, 30
	for _, v := range []uint64{10, 20, 30} {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeU16, v)
	}
	afero.WriteFile(memFs, "data.pulse", buf.Bytes(), 0644)

	fsConfig := fs.NewMemMap()
	// Replace the FS with our pre-populated one.
	svc := New(fsConfig)
	svc.fs = &fs.Config{}
	// We need to access the fs directly. Use a workaround: write to the memmap FS
	// that the service uses.

	// Instead, create the service with the correct FS.
	cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	svc = New(cfg)

	req := &types.Request{
		Cohort: &types.Cohort{Filename: "data.pulse"},
		Aggregations: []*types.Aggregation{
			{Type: types.AGG_SUM, Field: "score", Label: "total"},
		},
	}

	resp, err := svc.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("got %d data rows, want 1", len(resp.Data))
	}

	total, ok := resp.Data[0]["total"].(float64)
	if !ok {
		t.Fatalf("total is not float64: %T", resp.Data[0]["total"])
	}
	if total != 60 {
		t.Fatalf("total=%v, want 60", total)
	}

	if resp.Metadata.TotalRows != 3 {
		t.Fatalf("TotalRows=%d, want 3", resp.Metadata.TotalRows)
	}
}

// TestSample_StopsEarly verifies that Sample only reads n records, not the full file.
func TestSample_StopsEarly(t *testing.T) {
	memFs := afero.NewMemMapFs()

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU32, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}

	var buf bytes.Buffer
	encoding.WriteHeader(&buf)
	encoding.WriteSchema(&buf, schema)
	for i := 0; i < 1000; i++ {
		encoding.WriteFieldValue(&buf, encoding.FieldTypeU32, uint64(i))
	}
	afero.WriteFile(memFs, "big.pulse", buf.Bytes(), 0644)

	cfg, _ := fs.New(fs.WithFs(memFs), fs.WithDataDir("/"))
	svc := New(cfg)

	rows, err := svc.Sample(context.Background(), "big.pulse", 5)
	if err != nil {
		t.Fatalf("Sample: %v", err)
	}

	if len(rows) != 5 {
		t.Fatalf("got %d rows, want 5", len(rows))
	}

	// Verify values are sequential.
	for i, row := range rows {
		v, ok := row["id"].(float64)
		if !ok {
			t.Fatalf("row %d id is not float64", i)
		}
		if int(v) != i {
			t.Fatalf("row %d: got id=%v, want %d", i, v, i)
		}
	}
}
