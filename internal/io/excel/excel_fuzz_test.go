package excel

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"

	pio "github.com/frankbardon/pulse/internal/io"
	"github.com/frankbardon/pulse/internal/testutil/fuzzcontract"
)

// excelFuzzWorkbook serialises a one-sheet workbook whose cells set
// writes.
func excelFuzzWorkbook(tb testing.TB, set func(f *excelize.File, sheet string)) []byte {
	tb.Helper()
	f := excelize.NewFile()
	defer f.Close()
	set(f, f.GetSheetName(0))
	buf, err := f.WriteToBuffer()
	if err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

// excelFuzzStored re-packs a workbook with every part STORED rather
// than deflated, so the fuzzer's mutations land on the XML itself
// instead of breaking a deflate stream.
func excelFuzzStored(tb testing.TB, wb []byte) []byte {
	tb.Helper()
	zr, err := zip.NewReader(bytes.NewReader(wb), int64(len(wb)))
	if err != nil {
		tb.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			tb.Fatal(err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.Name, Method: zip.Store})
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			tb.Fatal(err)
		}
		_ = rc.Close()
	}
	if err := zw.Close(); err != nil {
		tb.Fatal(err)
	}
	return out.Bytes()
}

// excelFuzzSeeds are small valid workbooks across the reader's cell
// branches — strings, numbers, booleans, a blank cell, a ragged row, a
// header-only sheet, an empty sheet, the first again with its parts
// stored uncompressed — plus truncations of the first.
func excelFuzzSeeds(tb testing.TB) [][]byte {
	tb.Helper()
	cells := func(m map[string]any) func(*excelize.File, string) {
		return func(f *excelize.File, sheet string) {
			for ref, v := range m {
				if err := f.SetCellValue(sheet, ref, v); err != nil {
					tb.Fatal(err)
				}
			}
		}
	}
	simple := excelFuzzWorkbook(tb, cells(map[string]any{
		"A1": "name", "B1": "age", "C1": "score", "D1": "ok",
		"A2": "alice", "B2": 30, "C2": 95.5, "D2": true,
		"A3": "bob", "B3": 25, "D3": false,
		"A4": "carol", "B4": 35, "C4": 72.25, "D4": true, "E4": "extra",
	}))
	headerOnly := excelFuzzWorkbook(tb, cells(map[string]any{"A1": "a", "B1": "b"}))
	empty := excelFuzzWorkbook(tb, func(*excelize.File, string) {})

	seeds := [][]byte{simple, headerOnly, empty, excelFuzzStored(tb, simple), excelFuzzStored(tb, headerOnly), nil, []byte("PK\x03\x04")}
	for _, cut := range []int{30, len(simple) / 2, len(simple) - 22, len(simple) - 1} {
		seeds = append(seeds, simple[:cut])
	}
	return seeds
}

// FuzzExcelImport holds the Excel importer — ImportJob.Predict, then
// ImportJob.Run with schema inference into a hermetic fs, then the
// sheet listing — to the fuzz contract: no panic (the xlsx parser is
// third-party code; its panics must surface as coded errors), a
// bounded run (no allocation sized from a declared zip length), every
// error a registered *errors.CodedError.
func FuzzExcelImport(f *testing.F) {
	for _, s := range excelFuzzSeeds(f) {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzImportContract.Run(t, in, excelFuzzImport)
	})
}

// fuzzImportContract caps one import's total heap allocation at 1 GiB:
// far above what an honest input of at most fuzzcontract.DefaultMaxInput
// bytes needs, far below what a decoder trusting a declared length would
// take.
var fuzzImportContract = fuzzcontract.Contract{MaxAlloc: 1 << 30}

func excelFuzzImport(in []byte) error {
	ctx := context.Background()
	if _, err := pio.NewImportJob(NewReaderFromBytes(in), "p.pulse").Predict(ctx); err != nil {
		return err
	}
	job := pio.NewImportJob(NewReaderFromBytes(in), "in.pulse")
	job.FS = afero.NewMemMapFs()
	if _, err := job.Run(ctx); err != nil {
		return err
	}
	r := NewReaderFromBytes(in)
	defer r.Close()
	_, err := r.SheetNames()
	return err
}
