package service

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

func TestFilterToFile_WritesMatchingRecords(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 25.0")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 3 {
		t.Fatalf("written = %d, want 3", written)
	}

	exists, err := afero.Exists(cfg.Fs(), "dst.pulse")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("dst.pulse not written")
	}

	// Re-open and verify schema parity + filtered row count.
	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	if got := len(cohort.Schema().Fields); got != 2 {
		t.Errorf("dst schema fields = %d, want 2", got)
	}
	count, err := cohort.RecordCount()
	if err != nil {
		t.Fatalf("RecordCount: %v", err)
	}
	if count != 3 {
		t.Errorf("dst record count = %d, want 3", count)
	}

	// Verify the surviving records are 30/40/50 in order.
	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")

	// Header+schema must be byte-identical: dst length = src length minus 2 records.
	recordSize := 0
	for _, f := range schema.Fields {
		recordSize += f.Type.ByteSize()
	}
	wantDstLen := len(srcBytes) - 2*recordSize
	if len(dstBytes) != wantDstLen {
		t.Errorf("dst byte length = %d, want %d", len(dstBytes), wantDstLen)
	}
	// Header + schema prefix byte-equal.
	headerSchemaLen := len(srcBytes) - 5*recordSize
	for i := range headerSchemaLen {
		if srcBytes[i] != dstBytes[i] {
			t.Fatalf("header/schema diverges at byte %d", i)
		}
	}
}

func TestFilterToFile_ZeroMatches(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score > 999")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}
	cohort, err := svc.Open(context.Background(), "dst.pulse")
	if err != nil {
		t.Fatalf("Open dst: %v", err)
	}
	count, _ := cohort.RecordCount()
	if count != 0 {
		t.Errorf("dst record count = %d, want 0", count)
	}
}

func TestFilterToFile_AllMatch(t *testing.T) {
	schema := testSchema()
	cfg := setupTestFS(t, "src.pulse", schema, testRecords())
	svc := New(cfg)

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "id >= 1")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 5 {
		t.Fatalf("written = %d, want 5", written)
	}

	// Byte-equal to source (header + schema + all records preserved).
	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	if len(srcBytes) != len(dstBytes) {
		t.Fatalf("dst length = %d, want %d", len(dstBytes), len(srcBytes))
	}
	for i := range srcBytes {
		if srcBytes[i] != dstBytes[i] {
			t.Fatalf("byte mismatch at %d: %#x vs %#x", i, srcBytes[i], dstBytes[i])
		}
	}
}

func TestFilterToFile_RejectsEmptyPaths(t *testing.T) {
	cfg := setupTestFS(t, "src.pulse", testSchema(), testRecords())
	svc := New(cfg)

	if _, err := svc.FilterToFile(context.Background(), "", "dst.pulse", "id > 0"); err == nil {
		t.Error("expected error for empty src")
	}
	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "", "id > 0"); err == nil {
		t.Error("expected error for empty dst")
	}
	if _, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", ""); err == nil {
		t.Error("expected error for empty filter")
	}
}

func TestFilterToFile_BadExpression(t *testing.T) {
	cfg := setupTestFS(t, "src.pulse", testSchema(), testRecords())
	svc := New(cfg)

	// Non-bool expression should error.
	_, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "score + 1")
	if err == nil {
		t.Fatal("expected error for non-bool expression")
	}
}

func TestFilterToFile_PreservesNumericPrecision(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64, ByteOffset: 0, CsvColumnIdx: 0},
		},
	}
	records := [][]uint64{
		{math.Float64bits(math.Pi)},
		{math.Float64bits(math.E)},
		{math.Float64bits(1.0)},
	}
	cfg := setupTestFS(t, "src.pulse", schema, records)
	svc := New(cfg)

	written, err := svc.FilterToFile(context.Background(), "src.pulse", "dst.pulse", "x > 2.0")
	if err != nil {
		t.Fatalf("FilterToFile: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}

	// Verify exact bit pattern preserved for pi and e.
	dstBytes, _ := afero.ReadFile(cfg.Fs(), "dst.pulse")
	srcBytes, _ := afero.ReadFile(cfg.Fs(), "src.pulse")
	headerSchemaLen := len(srcBytes) - 3*8
	got := dstBytes[headerSchemaLen:]
	if len(got) != 16 {
		t.Fatalf("record bytes = %d, want 16", len(got))
	}
	if !bytesEqual(got[:8], srcBytes[headerSchemaLen:headerSchemaLen+8]) {
		t.Error("pi record bytes diverged")
	}
	if !bytesEqual(got[8:16], srcBytes[headerSchemaLen+8:headerSchemaLen+16]) {
		t.Error("e record bytes diverged")
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
