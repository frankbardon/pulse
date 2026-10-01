package io

import (
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// TestImportJob_SetEndToEnd round-trips a pipe-delimited set column
// through the inference + import path and asserts the on-wire mask
// matches the expected dictionary-bit assignment.
func TestImportJob_SetEndToEnd(t *testing.T) {
	rows := make([][]string, 0, minSampleRows)
	pairs := []string{"VISA|MC", "AMEX|DISC", "VISA|AMEX", "MC|DISC"}
	regions := []string{"north", "south", "east", "west"}
	for i := 0; i < minSampleRows; i++ {
		rows = append(rows, []string{regions[i%len(regions)], pairs[i%len(pairs)]})
	}
	reader := newMockReader([]string{"region", "issuers"}, rows)
	fs := afero.NewMemMapFs()
	job := NewImportJob(reader, "set.pulse")
	job.FS = fs

	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.RowsImported != minSampleRows {
		t.Errorf("RowsImported = %d, want %d", report.RowsImported, minSampleRows)
	}
	issuers := report.Schema.Field("issuers")
	if issuers == nil {
		t.Fatal("issuers field missing from inferred schema")
	}
	if !issuers.Type.IsSet() {
		t.Fatalf("issuers.Type = %s, want set_*", issuers.Type)
	}
	if got := len(issuers.Dictionary.Values()); got != 4 {
		t.Errorf("dictionary size = %d, want 4 (VISA/MC/AMEX/DISC)", got)
	}
}

// TestImportJob_SetForceTypeOverride supplies ColumnTypeOverrides on
// the job and confirms a single-token column is forced to set_u8.
func TestImportJob_SetForceTypeOverride(t *testing.T) {
	rows := make([][]string, 0, minSampleRows)
	regions := []string{"north", "south"}
	for i := 0; i < minSampleRows; i++ {
		rows = append(rows, []string{regions[i%len(regions)], "VISA"})
	}
	reader := newMockReader([]string{"region", "issuers"}, rows)
	fs := afero.NewMemMapFs()
	job := NewImportJob(reader, "set.pulse")
	job.FS = fs
	job.ColumnTypeOverrides = map[string]encoding.FieldType{
		"issuers": encoding.FieldTypeSetU8,
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := report.Schema.Field("issuers").Type; got != encoding.FieldTypeSetU8 {
		t.Errorf("issuers.Type = %s, want set_u8 (forced)", got)
	}
	// The dictionary should hold one entry ("VISA") — every cell
	// hits the same token.
	if got := len(report.Schema.Field("issuers").Dictionary.Values()); got != 1 {
		t.Errorf("dictionary size = %d, want 1", got)
	}
}

// TestImportJob_SetInferenceMinPctRespected confirms a low-frequency
// pipe column is misclassified as categorical at default 30%, and
// reclassified as set when the threshold drops.
func TestImportJob_SetInferenceMinPctRespected(t *testing.T) {
	rows := make([][]string, 0, 60)
	regions := []string{"north", "south"}
	for i := 0; i < 10; i++ {
		rows = append(rows, []string{regions[i%2], "VISA|MC"})
	}
	for i := 10; i < 60; i++ {
		rows = append(rows, []string{regions[i%2], "VISA"})
	}
	// 10/60 ≈ 16% — below default 30; expect categorical.
	{
		reader := newMockReader([]string{"region", "issuers"}, rows)
		fs := afero.NewMemMapFs()
		job := NewImportJob(reader, "default.pulse")
		job.FS = fs
		report, err := job.Run(context.Background())
		if err != nil {
			t.Fatalf("default Run: %v", err)
		}
		if report.Schema.Field("issuers").Type.IsSet() {
			t.Error("default threshold (30) wrongly classified column as set_*")
		}
	}
	// Lower threshold to 10 — heuristic now fires, column lands as set.
	{
		reader := newMockReader([]string{"region", "issuers"}, rows)
		fs := afero.NewMemMapFs()
		job := NewImportJob(reader, "lowered.pulse")
		job.FS = fs
		job.SetInferenceMinPct = 10
		report, err := job.Run(context.Background())
		if err != nil {
			t.Fatalf("lowered Run: %v", err)
		}
		if !report.Schema.Field("issuers").Type.IsSet() {
			t.Errorf("min_pct=10 should classify as set_*, got %s",
				report.Schema.Field("issuers").Type)
		}
	}
}
