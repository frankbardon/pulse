package io

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// elideFixture is a synthetic 600-row source with every constancy case:
//
//   - source  categorical, "batch-a" on every row      → value constant
//   - flag    numeric 7 on every row                    → value constant
//   - blank   empty on every row                        → NULL constant
//   - maybe   3 on every row except some nulls          → NOT constant
//   - late    constant for the first 550 rows (past the
//     500-row inference sample), then varies            → NOT constant
//   - id / score vary
func elideFixture(n int) ([]string, [][]string) {
	cols := []string{"id", "source", "score", "flag", "blank", "maybe", "late"}
	var rows [][]string
	for i := 0; i < n; i++ {
		maybe := "3"
		if i%9 == 4 {
			maybe = ""
		}
		late := "42"
		if i >= 550 {
			late = fmt.Sprint(40 + i%5)
		}
		rows = append(rows, []string{fmt.Sprint(i + 1), "batch-a", fmt.Sprint((i * 7) % 11), "7", "", maybe, late})
	}
	return cols, rows
}

func runElideImport(t *testing.T, cols []string, rows [][]string, elide bool, schema ...*encoding.Schema) (*ImportReport, []byte, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader(cols, rows), "out.pulse")
	job.FS = fs
	job.ElideConstants = elide
	if len(schema) > 0 {
		job.Schema = schema[0]
	}
	report, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run(elide=%v): %v", elide, err)
	}
	raw, err := afero.ReadFile(fs, "out.pulse")
	if err != nil {
		t.Fatal(err)
	}
	return report, raw, fs
}

// TestImportJob_ElideConstants: with the option on, exactly the fields
// constant over the FULL row pass are stored once in a constant group —
// a value constant, a categorical constant and a null constant — while
// a field constant except for nulls and a field constant only within the
// inference sample stay in the row. The cohort decodes to exactly the
// values and nulls of the un-elided import.
func TestImportJob_ElideConstants(t *testing.T) {
	cols, rows := elideFixture(600)
	flatRep, flatRaw, flatFS := runElideImport(t, cols, rows, false)
	rep, raw, fs := runElideImport(t, cols, rows, true)

	if flatRaw[encoding.HeaderSize-1] != encoding.FormatVersion || len(flatRep.ElidedConstants) != 0 {
		t.Fatalf("elision off: version 0x%02x, elided %v; want 0x01 and none", flatRaw[encoding.HeaderSize-1], flatRep.ElidedConstants)
	}
	if raw[encoding.HeaderSize-1] != encoding.FormatVersionV2 {
		t.Fatalf("elision on: version 0x%02x, want 0x02", raw[encoding.HeaderSize-1])
	}
	if want := []string{"source", "flag", "blank"}; !reflect.DeepEqual(rep.ElidedConstants, want) {
		t.Fatalf("ElidedConstants = %v, want %v", rep.ElidedConstants, want)
	}

	schema, vals, nulls := decodeAll(t, fs, "out.pulse")
	flatSchema, flatVals, flatNulls := decodeAll(t, flatFS, "out.pulse")
	if len(schema.Groups) != 1 || schema.Groups[0].Kind != encoding.GroupKindConstant {
		t.Fatalf("groups = %+v, want one constant group", schema.Groups)
	}
	if !reflect.DeepEqual(schema.Fields, flatSchema.Fields) || !reflect.DeepEqual(rep.Schema.Fields, flatRep.Schema.Fields) {
		t.Fatal("elided cohort's logical fields (dictionaries included) differ from the flat import's")
	}
	if len(vals) != 600 || !reflect.DeepEqual(vals, flatVals) || !reflect.DeepEqual(nulls, flatNulls) {
		t.Fatalf("decoded %d records; values/nulls must equal the flat import's %d", len(vals), len(flatVals))
	}
	if !nulls[0]["blank"] || !nulls[599]["blank"] {
		t.Fatal("the null constant must decode as null")
	}
	if rep.Schema.RecordByteSize() >= flatRep.Schema.RecordByteSize() || len(raw) >= len(flatRaw) {
		t.Fatalf("stride %d / file %d bytes not smaller than flat %d / %d",
			rep.Schema.RecordByteSize(), len(raw), flatRep.Schema.RecordByteSize(), len(flatRaw))
	}
	if n, _, _ := schema.RecordCountForPayload(int64(payloadLen(t, raw))); n != 600 {
		t.Fatalf("RecordCountForPayload = %d, want 600", n)
	}
}

// payloadLen is the record-region length of a cohort.
func payloadLen(t *testing.T, raw []byte) int {
	t.Helper()
	r := bytes.NewReader(raw)
	if _, _, err := encx.ReadPreamble(r); err != nil {
		t.Fatal(err)
	}
	return r.Len()
}

// TestImportJob_ElideConstants_NothingToElide: a single-row source
// (every field trivially constant), an empty source and a source with no
// constant field all fall through to the 0x01 cohort, byte-identical to
// the import with elision off.
func TestImportJob_ElideConstants_NothingToElide(t *testing.T) {
	cols, rows := elideFixture(600)
	var varying [][]string
	for i, r := range rows {
		varying = append(varying, []string{r[0], fmt.Sprint(i % 3)})
	}
	// An empty source cannot be inferred; it takes an explicit schema.
	explicit := func() *encoding.Schema {
		return &encoding.Schema{Fields: []encoding.Field{
			{Name: "id", Type: encoding.FieldTypeU16, CsvColumnIdx: 0},
			{Name: "k", Type: encoding.FieldTypeU8, CsvColumnIdx: 1, Nullable: true},
		}}
	}
	for _, tc := range []struct {
		name   string
		cols   []string
		rows   [][]string
		schema func() *encoding.Schema
	}{
		{"single_row", cols, rows[:1], nil},
		{"empty", []string{"id", "k"}, nil, explicit},
		{"no_constant", []string{"id", "k"}, varying, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var off, on []byte
			var rep *ImportReport
			if tc.schema != nil {
				_, off, _ = runElideImport(t, tc.cols, tc.rows, false, tc.schema())
				rep, on, _ = runElideImport(t, tc.cols, tc.rows, true, tc.schema())
			} else {
				_, off, _ = runElideImport(t, tc.cols, tc.rows, false)
				rep, on, _ = runElideImport(t, tc.cols, tc.rows, true)
			}
			if !bytes.Equal(on, off) {
				t.Fatalf("elision changed a cohort it had nothing to elide from (%d vs %d bytes)", len(on), len(off))
			}
			if len(rep.ElidedConstants) != 0 {
				t.Fatalf("ElidedConstants = %v, want none", rep.ElidedConstants)
			}
		})
	}
}
