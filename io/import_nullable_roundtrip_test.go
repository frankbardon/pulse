package io

import (
	"bytes"
	"context"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// TestImport_NullableBool_RoundTripsThroughRecordReader is the
// regression test for a separate known-broken area surfaced during the
// bit-packed-numeric audit.
//
// Three writers disagree on the nullable_bool on-wire format:
//   - io/import.go encodes a 3-state byte: 0 = null, 1 = false, 2 = true.
//   - synth/writer.go encodes a 2-bit byte: bit0 = value, bit1 = null.
//   - io/arrow + io/parquet route through arrow.Boolean.
//
// encoding/reader.go reads with ReadBit at field.BitPosition, which
// only sees one bit. Across the importer's 3-state convention this
// inverts false/true and silently drops nulls.
//
// The test is skipped because fixing it requires picking a canonical
// wire format and updating every writer in lockstep — out of scope for
// the bit-packed numeric acceptance fix. Unskip after a unified writer
// pass lands.
func TestImport_NullableBool_RoundTripsThroughRecordReader(t *testing.T) {
	t.Skip("known broken: writers disagree on nullable_bool wire format; see test comment")

	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "flag", Type: encoding.FieldTypeNullableBool, BitPosition: 0, CsvColumnIdx: 0, Description: "Survey opt-in flag"},
		},
	}
	src := &stringRowsReader{
		header: []string{"flag"},
		rows: [][]string{
			{"true"},
			{"false"},
			{""},
		},
	}
	fs := afero.NewMemMapFs()
	job := &ImportJob{Source: src, Target: "test.pulse", Schema: schema, FS: fs}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsImported != 3 {
		t.Fatalf("rows imported = %d, want 3", rep.RowsImported)
	}

	data, err := afero.ReadFile(fs, "test.pulse")
	if err != nil {
		t.Fatalf("read pulse: %v", err)
	}

	rdr := bytes.NewReader(data)
	if err := encoding.ReadHeader(rdr); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	gotSchema, err := encoding.ReadSchema(rdr)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	rr := encoding.NewRecordReader(rdr, gotSchema)

	expected := []struct {
		val  float64
		null bool
	}{
		{1, false}, // true
		{0, false}, // false
		{0, true},  // null
	}
	for i, want := range expected {
		values := map[string]float64{}
		nulls := map[string]bool{}
		if err := rr.ReadRecord(values, nulls); err != nil {
			t.Fatalf("row %d: ReadRecord: %v", i, err)
		}
		if got := values["flag"]; got != want.val {
			t.Errorf("row %d: value = %v, want %v", i, got, want.val)
		}
		if got := nulls["flag"]; got != want.null {
			t.Errorf("row %d: null = %v, want %v", i, got, want.null)
		}
	}
}

// TestImport_NullableU4_RoundTripsThroughRecordReader confirms the
// post-fix reader treats the importer's 0x0F null-sentinel byte as null
// and decodes the 0..14 ordinal range untouched.
func TestImport_NullableU4_RoundTripsThroughRecordReader(t *testing.T) {
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "score", Type: encoding.FieldTypeNullableU4, BitPosition: 0, CsvColumnIdx: 0, Description: "Likert score 0..14 with empty as null"},
		},
	}
	src := &stringRowsReader{
		header: []string{"score"},
		rows: [][]string{
			{"0"},
			{"7"},
			{"14"},
			{""},
		},
	}
	fs := afero.NewMemMapFs()
	job := &ImportJob{Source: src, Target: "test.pulse", Schema: schema, FS: fs}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsImported != 4 {
		t.Fatalf("rows imported = %d, want 4", rep.RowsImported)
	}

	data, err := afero.ReadFile(fs, "test.pulse")
	if err != nil {
		t.Fatalf("read pulse: %v", err)
	}
	rdr := bytes.NewReader(data)
	if err := encoding.ReadHeader(rdr); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	gotSchema, err := encoding.ReadSchema(rdr)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	rr := encoding.NewRecordReader(rdr, gotSchema)

	expected := []struct {
		val  float64
		null bool
	}{
		{0, false},
		{7, false},
		{14, false},
		{0, true},
	}
	for i, want := range expected {
		values := map[string]float64{}
		nulls := map[string]bool{}
		if err := rr.ReadRecord(values, nulls); err != nil {
			t.Fatalf("row %d: ReadRecord: %v", i, err)
		}
		if got := values["score"]; got != want.val {
			t.Errorf("row %d: value = %v, want %v", i, got, want.val)
		}
		if got := nulls["score"]; got != want.null {
			t.Errorf("row %d: null = %v, want %v", i, got, want.null)
		}
	}
}
