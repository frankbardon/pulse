package excel

import (
	"archive/zip"
	"bytes"
	"context"
	stderrors "errors"
	"io/fs"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"github.com/xuri/excelize/v2"

	"github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/internal/io"
)

func requireCode(t *testing.T, err error, code errors.Code) *errors.CodedError {
	t.Helper()
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return ce
}

func excelSimpleWorkbook(t *testing.T) []byte {
	t.Helper()
	return excelFuzzWorkbook(t, func(f *excelize.File, sheet string) {
		for ref, v := range map[string]any{"A1": "name", "B1": "age", "A2": "alice", "B2": 30} {
			if err := f.SetCellValue(sheet, ref, v); err != nil {
				t.Fatal(err)
			}
		}
	})
}

// A panic inside the third-party workbook parser surfaces as DATA_FILE
// naming the format and the recovered value — from the reader and
// through the import an embedder runs — never as a crash.
func TestExcelReader_ParserPanicIsCodedError(t *testing.T) {
	t.Parallel()
	panicking := func([]byte) (*excelize.File, error) { panic("xlsx parser exploded") }
	check := func(err error) {
		t.Helper()
		ce := requireCode(t, err, errors.DATA_FILE)
		if ce.Details["format"] != "excel" || ce.Details["panic"] != "xlsx parser exploded" {
			t.Fatalf("want details format excel + the panic value, got %v", ce.Details)
		}
	}

	r := NewReaderFromBytes(excelSimpleWorkbook(t))
	r.open = panicking
	_, err := r.ReadHeader()
	check(err)

	src := NewReaderFromBytes(excelSimpleWorkbook(t))
	src.open = panicking
	job := pio.NewImportJob(src, "c.pulse")
	job.FS = afero.NewMemMapFs()
	_, err = job.Run(context.Background())
	check(err)
}

// The workbook parser sizes each part's buffer from the length the zip
// entry DECLARES. A part claiming more than the source could ever
// decode to (DEFLATE expands at most 1032:1) is refused before it is
// allocated; the claim never becomes a buffer.
func TestExcelReader_DeclaredPartSizeIsCapped(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateRaw(&zip.FileHeader{
		Name:               "xl/styles.xml",
		Method:             zip.Store,
		CompressedSize64:   5,
		UncompressedSize64: 1 << 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("<a/>\n")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err = NewReaderFromBytes(buf.Bytes()).ReadHeader()
	runtime.ReadMemStats(&after)
	ce := requireCode(t, err, errors.DATA_FILE)
	if !strings.Contains(ce.Message, "unzip size exceeds") {
		t.Fatalf("want the unzip-size refusal, got %v", err)
	}
	if got := after.TotalAlloc - before.TotalAlloc; got > 64<<20 {
		t.Fatalf("refusing the claim allocated %d bytes", got)
	}
}

// An unreadable path, a missing source and bytes that are not a
// workbook are DATA_FILE, the library or fs error kept as Cause.
func TestExcelReader_OpenFaultsAreDataFile(t *testing.T) {
	t.Parallel()
	_, err := NewReader(afero.NewMemMapFs(), "missing.xlsx").ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if !stderrors.Is(err, fs.ErrNotExist) || ce.Details["path"] != "missing.xlsx" {
		t.Fatalf("want the fs cause and details.path, got %v / %v", err, ce.Details)
	}
	_, err = (&Reader{}).ReadHeader()
	requireCode(t, err, errors.DATA_FILE)

	good := excelSimpleWorkbook(t)
	for _, in := range [][]byte{[]byte("not a workbook"), good[:len(good)/2]} {
		_, err := NewReaderFromBytes(in).ReadHeader()
		if ce := requireCode(t, err, errors.DATA_FILE); ce.Cause == nil {
			t.Fatalf("%q: want the parser error kept as Cause", in)
		}
	}
	missing := NewReaderFromBytes(good, WithSheet("no such sheet"))
	_, err = missing.ReadHeader()
	requireCode(t, err, errors.DATA_FILE)
	if missing.file != nil {
		t.Fatal("a failed init must close the workbook it opened")
	}
}

// A workbook of a few kilobytes whose header reaches column XFD and
// whose one data cell sits on row 2000 asks the import for 32 million
// cells (the last probe of this shape, row 100000, ran 1m48s and
// allocated 158 GB). A grid that much sparser than the cells the sheet
// stores is refused before a row is padded.
func TestExcelReader_UnbackedGridIsRefused(t *testing.T) {
	t.Parallel()
	wb := excelFuzzWorkbook(t, func(f *excelize.File, sheet string) {
		for ref, v := range map[string]any{"A1": "h", "XFD1": "z", "A2000": 1} {
			if err := f.SetCellValue(sheet, ref, v); err != nil {
				t.Fatal(err)
			}
		}
	})
	_, err := NewReaderFromBytes(wb).ReadHeader()
	ce := requireCode(t, err, errors.DATA_FILE)
	if ce.Details["rows"] != int64(1999) || ce.Details["columns"] != int64(16384) || ce.Details["stored_cells"] != int64(16385) {
		t.Fatalf("want the grid refusal details, got %v", ce.Details)
	}
}

func TestExcelCheckGrid_Bounds(t *testing.T) {
	t.Parallel()
	rows := func(width, data, filled int) [][]string {
		out := make([][]string, 1, data+1)
		out[0] = make([]string, width)
		row := make([]string, filled)
		for i := 0; i < data; i++ {
			out = append(out, row)
		}
		return out
	}
	for _, tc := range []struct {
		name               string
		width, data, cells int
		refused            bool
	}{
		{"empty sheet", 0, 0, 0, false},
		{"header only", 16384, 0, 0, false},
		{"grid at the floor, all blank", 1, gridFloorCells, 0, false},
		{"grid one past the floor, all blank", 1, gridFloorCells + 1, 0, true},
		{"large dense grid", 8, gridFloorCells, 8, false},
		{"large grid exactly 64x sparse", 64, gridFloorCells / 8, 1, false},
		{"large grid past 64x sparse", 128, gridFloorCells / 8, 1, true},
	} {
		err := checkGrid(rows(tc.width, tc.data, tc.cells))
		if (err != nil) != tc.refused {
			t.Fatalf("%s: refused=%v, want %v (%v)", tc.name, err != nil, tc.refused, err)
		}
		if err != nil {
			requireCode(t, err, errors.DATA_FILE)
		}
	}
}
