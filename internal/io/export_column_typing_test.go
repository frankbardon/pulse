package io

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	pq "github.com/apache/arrow-go/v18/parquet"
	"github.com/apache/arrow-go/v18/parquet/pqarrow"
	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"

	"github.com/frankbardon/pulse/encoding"
	parrow "github.com/frankbardon/pulse/internal/io/arrow"
	"github.com/frankbardon/pulse/internal/io/excel"
	"github.com/frankbardon/pulse/internal/io/parquet"
	"github.com/frankbardon/pulse/types"
)

// The typed columnar writers (Excel, Arrow, Parquet) type each output
// column from the export's explicit column plan, never by lining the
// header up against the schema's field list by position. Projection
// (--include), a `_label` augment sibling and vector expansion all move a
// field away from its schema index; a positional lookup then typed a
// column with ANOTHER field's type — silently, whenever the value
// happened to parse.

// typingSchema mixes every typing class the three writers distinguish: a
// plain integer, a decimal (the one type Excel renders natively), a
// categorical (the augment target), a float, a labelled vector and a
// date — so any shift of one position changes a column's type.
func typingSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "qty", Type: encoding.FieldTypeU32},
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 10, Scale: 2},
		{Name: "color", Type: encoding.FieldTypeCategoricalU8},
		{Name: "score", Type: encoding.FieldTypeF64},
		{Name: "emb", Type: encoding.FieldTypeVecF64},
		{Name: "day", Type: encoding.FieldTypeDate},
	}, Vectors: []encoding.VectorField{
		{FieldIndex: 4, Dim: 2, Labels: []string{"x", "y"}},
	}}
}

func typingRows() [][]any {
	return [][]any{
		{uint64(7), encoding.NewDecimal128FromInt(1234), "red", 0.5, []float64{1.5, -2}, int32(19000)},
		{uint64(9), encoding.NewDecimal128FromInt(5), "blue", 2.25, []float64{3, 4}, int32(19001)},
	}
}

// typingExpected is each possible output column's per-row text, keyed by
// column name — what the source field holds, independent of position.
var typingExpected = map[string][]string{
	"qty":         {"7", "9"},
	"amount":      {"12.34", "0.05"},
	"color":       {"red", "blue"},
	"color_label": {"Rouge", "Bleu"},
	"score":       {"0.5", "2.25"},
	"emb_x":       {"1.5", "3"},
	"emb_y":       {"-2", "4"},
	"day":         {"2022-01-08", "2022-01-09"},
}

type typingCase struct {
	name     string
	includes []string
	augment  bool
	// replace binds a label REPLACE on the integer qty: its cells become
	// label text, so the column must be typed text, not uint32 — a fact
	// only the column plan carries (the schema still says u32).
	replace bool
}

// typingCases: (a) projection that reorders and subsets mixed types,
// (b) a `_label` augment, (c) both together with the vector projected in.
var typingCases = []typingCase{
	{name: "include_reordered_subset", includes: []string{"day", "score", "amount"}},
	{name: "label_augment", augment: true},
	{name: "include_augment_vector", includes: []string{"day", "emb", "color", "amount"}, augment: true},
	{name: "replace_numeric", includes: []string{"amount", "qty", "score"}, replace: true},
}

// replacedQty is qty's cells under the replace_numeric binding.
var replacedQty = []string{"seven", "nine"}

// expectedText is column col's row r text for case tc.
func (tc typingCase) expectedText(col string, r int) string {
	if tc.replace && col == "qty" {
		return replacedQty[r]
	}
	return typingExpected[col][r]
}

func (tc typingCase) job(fs afero.Fs, target Writer) *ExportJob {
	job := NewExportJob("t.pulse", target)
	job.FS = fs
	job.Includes = tc.includes
	if tc.augment {
		job.LabelResolver = newFakeResolver("color", types.LabelModeAugment,
			map[string]string{"red": "Rouge", "blue": "Bleu"})
	}
	if tc.replace {
		job.LabelResolver = newFakeResolver("qty", types.LabelModeReplace,
			map[string]string{"7": replacedQty[0], "9": replacedQty[1]})
	}
	return job
}

func writeTypingCohort(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	b, err := NewCohortBuild(context.Background(), fs, "t.pulse", typingSchema(), CohortBuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for k, row := range typingRows() {
		if err := b.Append(row); err != nil {
			t.Fatalf("row %d: %v", k, err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
	return fs
}

// runTypingExport runs Predict then Run against two fresh writers from
// mk, closes the Run target and returns its bytes plus predict's columns.
func runTypingExport(t *testing.T, tc typingCase, mk func() Writer, bytesOf func(Writer) []byte) ([]byte, []string) {
	t.Helper()
	fs := writeTypingCohort(t)
	pred, err := tc.job(fs, mk()).Predict(context.Background())
	if err != nil {
		t.Fatalf("predict: %v", err)
	}
	w := mk()
	rep, err := tc.job(fs, w).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.RowsExported != 2 || len(rep.RowErrors) != 0 {
		t.Fatalf("exported %d rows, row errors %v", rep.RowsExported, rep.RowErrors)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return bytesOf(w), pred.Columns
}

// arrowTypeFor is the Arrow type an output column must carry: the source
// field's own mapping, text for a label sibling, FixedSizeList for a
// native vector column.
func arrowTypeFor(t *testing.T, tc typingCase, col string) arrow.DataType {
	t.Helper()
	if col == "color_label" || (tc.replace && col == "qty") {
		return arrow.BinaryTypes.String
	}
	s := typingSchema()
	for i, f := range s.Fields {
		if f.Name != col {
			continue
		}
		if f.Type.IsVector() {
			return arrow.FixedSizeListOf(int32(s.VectorDim(i)), arrow.PrimitiveTypes.Float64)
		}
		return parrow.FieldFromPulse(f).Type
	}
	t.Fatalf("no source for column %q", col)
	return nil
}

// checkArrowTable asserts each column's name list, physical type and
// values against its source field.
func checkArrowTable(t *testing.T, tc typingCase, sc *arrow.Schema, cols []arrow.Array, predicted []string) {
	t.Helper()
	var header []string
	for _, f := range sc.Fields() {
		header = append(header, f.Name)
	}
	if !reflect.DeepEqual(header, predicted) {
		t.Errorf("predict columns %v != written header %v", predicted, header)
	}
	for c, f := range sc.Fields() {
		if want := arrowTypeFor(t, tc, f.Name); !arrow.TypeEqual(f.Type, want) {
			t.Errorf("column %q: type %s, want %s", f.Name, f.Type, want)
		}
		arr := cols[c]
		for r := 0; r < arr.Len(); r++ {
			got := arr.ValueStr(r)
			if f.Name == "emb" {
				want := "[" + typingExpected["emb_x"][r] + "," + typingExpected["emb_y"][r] + "]"
				if got != want {
					t.Errorf("column emb row %d: %q, want %q", r, got, want)
				}
				continue
			}
			if want := tc.expectedText(f.Name, r); got != want {
				t.Errorf("column %q row %d: %q, want %q", f.Name, r, got, want)
			}
		}
	}
}

func TestExport_ArrowColumnTypingFollowsColumnPlan(t *testing.T) {
	for _, tc := range typingCases {
		t.Run(tc.name, func(t *testing.T) {
			data, predicted := runTypingExport(t, tc,
				func() Writer { return parrow.NewWriterToBuffer() },
				func(w Writer) []byte { return w.(*parrow.Writer).Bytes() })
			rdr, err := ipc.NewFileReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer rdr.Close()
			rec, err := rdr.RecordBatch(0)
			if err != nil {
				t.Fatal(err)
			}
			checkArrowTable(t, tc, rdr.Schema(), rec.Columns(), predicted)
		})
	}
}

func TestExport_ParquetColumnTypingFollowsColumnPlan(t *testing.T) {
	for _, tc := range typingCases {
		t.Run(tc.name, func(t *testing.T) {
			data, predicted := runTypingExport(t, tc,
				func() Writer { return parquet.NewWriterToBuffer() },
				func(w Writer) []byte { return w.(*parquet.Writer).Bytes() })
			tbl, err := pqarrow.ReadTable(context.Background(), bytes.NewReader(data),
				pq.NewReaderProperties(memory.DefaultAllocator), pqarrow.ArrowReadProperties{}, memory.DefaultAllocator)
			if err != nil {
				t.Fatal(err)
			}
			defer tbl.Release()
			cols := make([]arrow.Array, tbl.NumCols())
			for c := range cols {
				chunks := tbl.Column(c).Data().Chunks()
				if len(chunks) != 1 {
					t.Fatalf("column %d: %d chunks", c, len(chunks))
				}
				cols[c] = chunks[0]
			}
			checkArrowTable(t, tc, tbl.Schema(), cols, predicted)
		})
	}
}

// Excel renders decimal128 as a NUMBER cell under a scale-driven format
// and every other value as text, so a column's physical type is right
// exactly when it is numeric iff its source is the decimal.
func TestExport_ExcelColumnTypingFollowsColumnPlan(t *testing.T) {
	for _, tc := range typingCases {
		t.Run(tc.name, func(t *testing.T) {
			data, predicted := runTypingExport(t, tc,
				func() Writer { return excel.NewWriterToBuffer() },
				func(w Writer) []byte { return w.(*excel.Writer).Bytes() })
			f, err := excelize.OpenReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			sheet := f.GetSheetList()[0]
			rows, err := f.GetRows(sheet)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 3 {
				t.Fatalf("got %d rows, want header + 2", len(rows))
			}
			header := rows[0]
			if !reflect.DeepEqual(header, predicted) {
				t.Errorf("predict columns %v != written header %v", predicted, header)
			}
			for c, name := range header {
				for r := 0; r < 2; r++ {
					if got, want := rows[r+1][c], tc.expectedText(name, r); got != want {
						t.Errorf("column %q row %d: %q, want %q", name, r, got, want)
					}
					cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
					ct, err := f.GetCellType(sheet, cell)
					if err != nil {
						t.Fatal(err)
					}
					// A stream-written number cell carries no `t` attribute (Unset);
					// every text cell carries one.
					numeric := ct == excelize.CellTypeNumber || ct == excelize.CellTypeUnset
					if want := name == "amount"; numeric != want {
						t.Errorf("column %q row %d: cell type %v, numeric want %v", name, r, ct, want)
					}
				}
			}
		})
	}
}
