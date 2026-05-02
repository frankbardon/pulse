package excel

import (
	"bytes"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"
)

func TestExcel_DecimalGeoTypedCells(t *testing.T) {
	fs := afero.NewMemMapFs()
	w := NewWriter(fs, "out.xlsx")
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2},
		{Name: "loc", Type: encoding.FieldTypePointF64},
		{Name: "cell", Type: encoding.FieldTypeH3Cell, H3Resolution: 9},
	}}
	w.SetPulseSchema(schema)
	if err := w.WriteHeader([]string{"amount", "loc", "cell"}); err != nil {
		t.Fatal(err)
	}
	d, _, _ := encoding.ParseDecimal128("123.45")
	if err := w.WriteRow([]any{
		d,
		encoding.PointF64{Lat: 37.775, Lon: -122.418},
		encoding.H3Cell(0x89283082803ffff),
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := afero.ReadFile(fs, "out.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	cell, err := f.GetCellValue(sheet, "A2")
	if err != nil {
		t.Fatal(err)
	}
	if cell != "123.45" {
		t.Errorf("amount cell = %q, want 123.45", cell)
	}
	pt, err := f.GetCellValue(sheet, "B2")
	if err != nil {
		t.Fatal(err)
	}
	if pt != "-122.418, 37.775" {
		t.Errorf("loc cell = %q, want -122.418, 37.775", pt)
	}
	h3, err := f.GetCellValue(sheet, "C2")
	if err != nil {
		t.Fatal(err)
	}
	if h3 != "89283082803ffff" {
		t.Errorf("h3 cell = %q, want 89283082803ffff", h3)
	}
	// Check that the decimal cell carries a scale-driven number format.
	styleID, err := f.GetCellStyle(sheet, "A2")
	if err != nil {
		t.Fatal(err)
	}
	style, err := f.GetStyle(styleID)
	if err != nil {
		t.Fatal(err)
	}
	if style.CustomNumFmt == nil || *style.CustomNumFmt != "0.00" {
		t.Errorf("decimal format = %v, want 0.00", style.CustomNumFmt)
	}
}
