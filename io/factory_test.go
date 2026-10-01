package io_test

import (
	"bytes"
	"context"
	stderrors "errors"
	"reflect"
	"testing"

	perrors "github.com/frankbardon/pulse/errors"
	parrow "github.com/frankbardon/pulse/internal/io/arrow"
	"github.com/frankbardon/pulse/internal/io/csv"
	"github.com/frankbardon/pulse/internal/io/excel"
	"github.com/frankbardon/pulse/internal/io/jsonarray"
	"github.com/frankbardon/pulse/internal/io/ndjson"
	"github.com/frankbardon/pulse/internal/io/parquet"
	"github.com/frankbardon/pulse/internal/io/spss"
	"github.com/frankbardon/pulse/internal/io/tsv"
	"github.com/frankbardon/pulse/internal/spsstest"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// TestFromExt_Matrix pins the extension → Format dispatch FormatFromPath
// owns (it replaced io/format.FromExt and keeps the gate's name). Every
// row is present so an edit to the switch cannot quietly drop a mapping
// while adding one; `.xls → Excel` is the historical mapping and stays.
func TestFromExt_Matrix(t *testing.T) {
	tests := []struct {
		path string
		want pio.Format
	}{
		{"data.csv", pio.FormatCSV},
		{"data.CSV", pio.FormatCSV},
		{"data.tsv", pio.FormatTSV},
		{"data.ndjson", pio.FormatNDJSON},
		{"data.jsonl", pio.FormatNDJSON},
		{"data.json", pio.FormatJSONArray},
		{"data.JSON", pio.FormatJSONArray},
		{"data.parquet", pio.FormatParquet},
		{"data.pq", pio.FormatParquet},
		{"data.arrow", pio.FormatArrow},
		{"data.feather", pio.FormatArrow},
		{"data.FEATHER", pio.FormatArrow},
		{"data.xlsx", pio.FormatExcel},
		{"data.xls", pio.FormatExcel},
		{"survey.sav", pio.FormatSPSS},
		{"survey.zsav", pio.FormatSPSS},
		{"SURVEY.SAV", pio.FormatSPSS},
		{"Survey.ZSav", pio.FormatSPSS},
		{"/abs/dir/survey.sav", pio.FormatSPSS},
		{"data.pulse", pio.FormatPulse},
		{"data.unknown", ""},
		{"noext", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := pio.FormatFromPath(tt.path); got != tt.want {
			t.Errorf("FormatFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// TestFormats_ListAndDirections pins the advertised set and the two
// direction predicates. Formats() is what documentation and CLI help
// enumerate; a format listed there that a factory cannot build would be a
// runtime "unsupported" from a list the engine itself published.
func TestFormats_ListAndDirections(t *testing.T) {
	want := []pio.Format{
		pio.FormatCSV, pio.FormatTSV, pio.FormatNDJSON, pio.FormatJSONArray,
		pio.FormatParquet, pio.FormatArrow, pio.FormatExcel, pio.FormatSPSS,
	}
	got := pio.Formats()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Formats() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if pio.Formats()[0] != pio.FormatCSV {
		t.Error("Formats() returned shared backing storage; a caller mutated the registry")
	}
	for _, f := range want {
		if !f.CanRead() || !f.CanWrite() {
			t.Errorf("%s: CanRead=%v CanWrite=%v, want both true", f, f.CanRead(), f.CanWrite())
		}
		if f.String() != string(f) {
			t.Errorf("String() = %q, want %q", f.String(), string(f))
		}
	}
	for _, f := range []pio.Format{pio.FormatPulse, "", "xlsb"} {
		if f.CanRead() || f.CanWrite() {
			t.Errorf("%q: CanRead=%v CanWrite=%v, want both false", f, f.CanRead(), f.CanWrite())
		}
	}
}

// TestFactory_EveryAdvertisedFormatConstructs closes the loop between
// CanRead / CanWrite and the four constructors, in every direction.
func TestFactory_EveryAdvertisedFormatConstructs(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, f := range pio.Formats() {
		if r, err := pio.NewReader(f, fs, "in", pio.ReaderOptions{}); err != nil || r == nil {
			t.Errorf("NewReader(%s) = %v, %v", f, r, err)
		}
		if r, err := pio.NewReaderFromBytes(f, nil, pio.ReaderOptions{}); err != nil || r == nil {
			t.Errorf("NewReaderFromBytes(%s) = %v, %v", f, r, err)
		}
		if w, err := pio.NewWriter(f, fs, "out", pio.WriterOptions{}); err != nil || w == nil {
			t.Errorf("NewWriter(%s) = %v, %v", f, w, err)
		}
		if w, err := pio.NewWriterToBuffer(f, pio.WriterOptions{}); err != nil || w == nil {
			t.Errorf("NewWriterToBuffer(%s) = %v, %v", f, w, err)
		}
	}
}

// TestFactory_UnsupportedFormatIsCoded pins the refusal arm in every
// direction: the empty Format (a FormatFromPath miss), the native pulse
// format, and an unknown identifier all fail with their OWN coded error —
// never a plain error a CLI envelope would flatten to a placeholder.
func TestFactory_UnsupportedFormatIsCoded(t *testing.T) {
	fs := afero.NewMemMapFs()
	ctors := map[string]func(pio.Format) (any, error){
		"NewReader": func(f pio.Format) (any, error) { return pio.NewReader(f, fs, "x", pio.ReaderOptions{}) },
		"NewReaderFromBytes": func(f pio.Format) (any, error) {
			return pio.NewReaderFromBytes(f, []byte("a\n1\n"), pio.ReaderOptions{})
		},
		"NewWriter":         func(f pio.Format) (any, error) { return pio.NewWriter(f, fs, "x", pio.WriterOptions{}) },
		"NewWriterToBuffer": func(f pio.Format) (any, error) { return pio.NewWriterToBuffer(f, pio.WriterOptions{}) },
	}
	wantDirection := map[string]string{
		"NewReader": "read", "NewReaderFromBytes": "read",
		"NewWriter": "write", "NewWriterToBuffer": "write",
	}
	for name, ctor := range ctors {
		for _, f := range []pio.Format{"", pio.FormatPulse, "xlsb", "CSV"} {
			v, err := ctor(f)
			if err == nil {
				t.Errorf("%s(%q) succeeded", name, f)
				continue
			}
			if v != nil && !reflect.ValueOf(v).IsNil() {
				t.Errorf("%s(%q) returned a value with its error", name, f)
			}
			var ce *perrors.CodedError
			if !stderrors.As(err, &ce) {
				t.Fatalf("%s(%q) error %T %v is not a *CodedError", name, f, err, err)
			}
			if ce.Code != perrors.PULSE_IO_FORMAT_UNSUPPORTED {
				t.Errorf("%s(%q) code = %s, want PULSE_IO_FORMAT_UNSUPPORTED", name, f, ce.Code)
			}
			if ce.Details["format"] != string(f) || ce.Details["direction"] != wantDirection[name] {
				t.Errorf("%s(%q) details = %v", name, f, ce.Details)
			}
		}
	}
}

// interfaceChecks is the optional-interface catalogue the shared import /
// export / convert paths discover by type assertion.
var interfaceChecks = map[string]func(any) bool{
	"ResetReader":           func(v any) bool { _, ok := v.(pio.ResetReader); return ok },
	"SchemaAwareReader":     func(v any) bool { _, ok := v.(pio.SchemaAwareReader); return ok },
	"NullAwareReader":       func(v any) bool { _, ok := v.(pio.NullAwareReader); return ok },
	"SourceWarningEmitter":  func(v any) bool { _, ok := v.(pio.SourceWarningEmitter); return ok },
	"SidecarEmitter":        func(v any) bool { _, ok := v.(pio.SidecarEmitter); return ok },
	"DiscardableWriter":     func(v any) bool { _, ok := v.(pio.DiscardableWriter); return ok },
	"SchemaAwareWriter":     func(v any) bool { _, ok := v.(pio.SchemaAwareWriter); return ok },
	"NullAwareWriter":       func(v any) bool { _, ok := v.(pio.NullAwareWriter); return ok },
	"OverlayAwareWriter":    func(v any) bool { _, ok := v.(pio.OverlayAwareWriter); return ok },
	"OverlayWarningEmitter": func(v any) bool { _, ok := v.(pio.OverlayWarningEmitter); return ok },
	"TargetWarningEmitter":  func(v any) bool { _, ok := v.(pio.TargetWarningEmitter); return ok },
	"CohortWriter":          func(v any) bool { _, ok := v.(pio.CohortWriter); return ok },
	"CohortValidator":       func(v any) bool { _, ok := v.(pio.CohortValidator); return ok },
	"SourceAwareWriter":     func(v any) bool { _, ok := v.(pio.SourceAwareWriter); return ok },
}

// TestFactory_ReturnsAdapterUnwrapped asserts the factory hands back the
// adapter pointer itself. A wrapper struct would satisfy Reader / Writer
// and silently lose every optional interface the jobs key off — a Writer
// without CohortWriter would encode a `.sav` from rendered label text. So
// each value must have the adapter's concrete type, every interface in the
// catalogue must answer exactly as the directly-built adapter does, and
// the load-bearing ones are named outright.
func TestFactory_ReturnsAdapterUnwrapped(t *testing.T) {
	fs := afero.NewMemMapFs()
	type row struct {
		f            pio.Format
		directReader any
		directWriter any
		mustReader   []string
		mustWriter   []string
	}
	rows := []row{
		{pio.FormatCSV, csv.NewReader(fs, "x"), csv.NewWriter(fs, "x"),
			[]string{"ResetReader"}, []string{"OverlayAwareWriter"}},
		{pio.FormatTSV, tsv.NewReader(fs, "x"), tsv.NewWriter(fs, "x"),
			[]string{"ResetReader"}, nil},
		{pio.FormatNDJSON, ndjson.NewReader(fs, "x"), ndjson.NewWriter(fs, "x"),
			[]string{"ResetReader", "NullAwareReader"}, []string{"NullAwareWriter", "OverlayAwareWriter"}},
		{pio.FormatJSONArray, jsonarray.NewReader(fs, "x"), jsonarray.NewWriter(fs, "x"),
			[]string{"ResetReader", "NullAwareReader"}, []string{"NullAwareWriter"}},
		{pio.FormatParquet, parquet.NewReader(fs, "x"), parquet.NewWriter(fs, "x"),
			[]string{"ResetReader", "NullAwareReader"}, []string{"SchemaAwareWriter", "NullAwareWriter", "OverlayAwareWriter"}},
		{pio.FormatArrow, parrow.NewReader(fs, "x"), parrow.NewWriter(fs, "x"),
			[]string{"ResetReader", "NullAwareReader"}, []string{"SchemaAwareWriter", "NullAwareWriter", "OverlayAwareWriter"}},
		{pio.FormatExcel, excel.NewReader(fs, "x"), excel.NewWriter(fs, "x"),
			[]string{"ResetReader"}, []string{"SchemaAwareWriter", "DiscardableWriter", "OverlayAwareWriter"}},
		{pio.FormatSPSS, spss.NewReader(fs, "x"), spss.NewWriter(fs, "x", spss.WriterOptions{}),
			[]string{"ResetReader", "SchemaAwareReader", "SourceWarningEmitter", "SidecarEmitter"},
			[]string{"SchemaAwareWriter", "CohortWriter", "CohortValidator", "TargetWarningEmitter", "SourceAwareWriter", "OverlayAwareWriter", "OverlayWarningEmitter"}},
	}
	for _, rw := range rows {
		readers := map[string]any{}
		writers := map[string]any{}
		var err error
		if readers["NewReader"], err = pio.NewReader(rw.f, fs, "x", pio.ReaderOptions{}); err != nil {
			t.Fatal(err)
		}
		if readers["NewReaderFromBytes"], err = pio.NewReaderFromBytes(rw.f, nil, pio.ReaderOptions{}); err != nil {
			t.Fatal(err)
		}
		if writers["NewWriter"], err = pio.NewWriter(rw.f, fs, "x", pio.WriterOptions{}); err != nil {
			t.Fatal(err)
		}
		if writers["NewWriterToBuffer"], err = pio.NewWriterToBuffer(rw.f, pio.WriterOptions{}); err != nil {
			t.Fatal(err)
		}
		check := func(kind string, got, direct any, must []string) {
			if reflect.TypeOf(got) != reflect.TypeOf(direct) {
				t.Errorf("%s %s returned %T, want the adapter %T unwrapped", rw.f, kind, got, direct)
			}
			for name, has := range interfaceChecks {
				if has(got) != has(direct) {
					t.Errorf("%s %s: %s = %v through the factory, %v on the adapter", rw.f, kind, name, has(got), has(direct))
				}
			}
			for _, name := range must {
				if !interfaceChecks[name](got) {
					t.Errorf("%s %s does not implement %s", rw.f, kind, name)
				}
			}
		}
		for kind, r := range readers {
			check(kind, r, rw.directReader, rw.mustReader)
		}
		for kind, w := range writers {
			check(kind, w, rw.directWriter, rw.mustWriter)
		}
	}
}

func writeRows(t *testing.T, w pio.Writer, header []string, rows [][]string) {
	t.Helper()
	if err := w.WriteHeader(header); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	for _, r := range rows {
		vals := make([]any, len(r))
		for i, c := range r {
			vals[i] = c
		}
		if err := w.WriteRow(vals); err != nil {
			t.Fatalf("WriteRow: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func readRows(t *testing.T, r pio.Reader) ([]string, [][]string) {
	t.Helper()
	defer func() { _ = r.Close() }()
	header, err := r.ReadHeader()
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	var rows [][]string
	if err := r.ReadRows(context.Background(), func(row []string) error {
		rows = append(rows, append([]string(nil), row...))
		return nil
	}); err != nil {
		t.Fatalf("ReadRows: %v", err)
	}
	return header, rows
}

// TestFactory_RoundTripEveryFormat writes the same table through each
// format's two writer constructors and reads it back through the matching
// reader constructors: to-buffer → from-bytes, and file → file on a
// MemMapFs. The bytes must agree between the two writers too, so the
// buffer path is the same encoder, not a second one.
func TestFactory_RoundTripEveryFormat(t *testing.T) {
	header := []string{"city", "tier"}
	rows := [][]string{{"Oslo", "gold"}, {"Lima", "silver"}, {"Pune", "gold"}}
	for _, f := range pio.Formats() {
		t.Run(f.String(), func(t *testing.T) {
			bw, err := pio.NewWriterToBuffer(f, pio.WriterOptions{})
			if err != nil {
				t.Fatalf("NewWriterToBuffer: %v", err)
			}
			writeRows(t, bw, header, rows)
			data := bw.Bytes()
			if len(data) == 0 {
				t.Fatal("Bytes() is empty after Close")
			}
			br, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
			if err != nil {
				t.Fatalf("NewReaderFromBytes: %v", err)
			}
			gotHeader, gotRows := readRows(t, br)
			if !reflect.DeepEqual(gotHeader, header) || !reflect.DeepEqual(gotRows, rows) {
				t.Errorf("buffer round trip = %v %v, want %v %v", gotHeader, gotRows, header, rows)
			}

			fs := afero.NewMemMapFs()
			fw, err := pio.NewWriter(f, fs, "out", pio.WriterOptions{})
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			writeRows(t, fw, header, rows)
			onDisk, err := afero.ReadFile(fs, "out")
			if err != nil {
				t.Fatalf("NewWriter wrote nothing: %v", err)
			}
			if f != pio.FormatExcel && !bytes.Equal(onDisk, data) {
				// Excel stamps the workbook with a creation time, so
				// its two encodings legitimately differ by that field.
				t.Errorf("NewWriter and NewWriterToBuffer encoded the same table differently (%d vs %d bytes)", len(onDisk), len(data))
			}
			fr, err := pio.NewReader(f, fs, "out", pio.ReaderOptions{})
			if err != nil {
				t.Fatalf("NewReader: %v", err)
			}
			gotHeader, gotRows = readRows(t, fr)
			if !reflect.DeepEqual(gotHeader, header) || !reflect.DeepEqual(gotRows, rows) {
				t.Errorf("file round trip = %v %v, want %v %v", gotHeader, gotRows, header, rows)
			}
		})
	}
}

// TestFactory_WrongFormatOptionsIgnored pins the sub-struct rule: a knob
// for one format is inert for every other, including a value its own
// format would refuse. The CLI builds ONE options value per leaf and hands
// it to whichever format the path resolved to, so an error here would make
// `--spss-missing` break `pulse import csv`.
func TestFactory_WrongFormatOptionsIgnored(t *testing.T) {
	fs := afero.NewMemMapFs()
	ropts := pio.ReaderOptions{
		Excel: pio.ExcelReaderOptions{Sheet: "NoSuchSheet"},
		SPSS:  pio.SPSSReaderOptions{Charset: "windows-1252", MissingMode: "nonsense"},
	}
	wopts := pio.WriterOptions{SPSS: pio.SPSSWriterOptions{
		IgnoreSidecar: true, Uncompressed: true, Charset: "cp1252", SanitizeNames: true,
	}}
	header := []string{"a"}
	rows := [][]string{{"x"}, {"y"}}
	for _, f := range pio.Formats() {
		if f == pio.FormatSPSS {
			continue
		}
		if _, err := pio.NewReader(f, fs, "in", ropts); err != nil {
			t.Errorf("NewReader(%s) with foreign options: %v", f, err)
		}
		if _, err := pio.NewReaderFromBytes(f, nil, ropts); err != nil {
			t.Errorf("NewReaderFromBytes(%s) with foreign options: %v", f, err)
		}
		if _, err := pio.NewWriter(f, fs, "out", wopts); err != nil {
			t.Errorf("NewWriter(%s) with foreign options: %v", f, err)
		}
		if f == pio.FormatExcel {
			continue // the workbook carries a creation timestamp
		}
		plain, _ := pio.NewWriterToBuffer(f, pio.WriterOptions{})
		foreign, err := pio.NewWriterToBuffer(f, wopts)
		if err != nil {
			t.Fatalf("NewWriterToBuffer(%s) with foreign options: %v", f, err)
		}
		writeRows(t, plain, header, rows)
		writeRows(t, foreign, header, rows)
		if !bytes.Equal(plain.Bytes(), foreign.Bytes()) {
			t.Errorf("%s: SPSS writer options changed a %s encoding", f, f)
		}
	}
	// The Excel sheet knob is inert for csv at READ time too, where a
	// missing sheet would otherwise fail.
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, []byte("a\nx\n"), ropts)
	if err != nil {
		t.Fatal(err)
	}
	if h, got := readRows(t, r); !reflect.DeepEqual(h, header) || len(got) != 1 {
		t.Errorf("csv read with foreign options = %v %v", h, got)
	}
}

// TestFactory_SPSSMissingMode covers the one option that can be REJECTED,
// and it must be rejected at construction: the adapter's option has no
// error channel, so this is the last place a typo can fail loudly instead
// of producing a schema the caller did not ask for.
func TestFactory_SPSSMissingMode(t *testing.T) {
	fs := afero.NewMemMapFs()
	for _, mode := range []pio.SPSSMissingMode{"", pio.SPSSMissingAuto, pio.SPSSMissingNull} {
		opts := pio.ReaderOptions{SPSS: pio.SPSSReaderOptions{MissingMode: mode}}
		if r, err := pio.NewReader(pio.FormatSPSS, fs, "s.sav", opts); err != nil || r == nil {
			t.Errorf("NewReader(MissingMode=%q) = %v, %v", mode, r, err)
		}
		if r, err := pio.NewReaderFromBytes(pio.FormatSPSS, nil, opts); err != nil || r == nil {
			t.Errorf("NewReaderFromBytes(MissingMode=%q) = %v, %v", mode, r, err)
		}
	}
	bad := pio.ReaderOptions{SPSS: pio.SPSSReaderOptions{MissingMode: "nul"}}
	for name, build := range map[string]func() (pio.Reader, error){
		"NewReader":          func() (pio.Reader, error) { return pio.NewReader(pio.FormatSPSS, fs, "s.sav", bad) },
		"NewReaderFromBytes": func() (pio.Reader, error) { return pio.NewReaderFromBytes(pio.FormatSPSS, nil, bad) },
	} {
		r, err := build()
		if r != nil {
			t.Errorf("%s returned a reader for a bad mode", name)
		}
		var ce *perrors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != perrors.PULSE_SPSS_MISSING_MODE_INVALID {
			t.Errorf("%s error = %v, want PULSE_SPSS_MISSING_MODE_INVALID", name, err)
		}
	}
}

// TestFactory_SPSSCharsetReachesReader is behavioural rather than
// structural: a `.sav` carrying an 8-bit byte and declaring no encoding
// fails under the strict UTF-8 default and decodes under the override,
// so the option was consulted, not merely stored.
func TestFactory_SPSSCharsetReachesReader(t *testing.T) {
	raw, err := spsstest.Build(spsstest.Spec{
		Vars:  []spsstest.Var{{Name: "A", Label: "cafX"}},
		Cases: [][]spsstest.Value{{spsstest.Num(1)}},
	})
	if err != nil {
		t.Fatalf("spsstest.Build: %v", err)
	}
	at := bytes.Index(raw, []byte("cafX"))
	if at < 0 {
		t.Fatal("the label is not in the emitted bytes")
	}
	raw[at+3] = 0xE9 // "café" in windows-1252; not valid UTF-8 alone

	plain, err := pio.NewReaderFromBytes(pio.FormatSPSS, raw, pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.ReadHeader(); err == nil {
		t.Fatal("an undeclared 8-bit file read without an override; the default is strict UTF-8")
	}
	overridden, err := pio.NewReaderFromBytes(pio.FormatSPSS, raw,
		pio.ReaderOptions{SPSS: pio.SPSSReaderOptions{Charset: "windows-1252"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := overridden.ReadHeader(); err != nil {
		t.Fatalf("SPSSReaderOptions.Charset did not reach the reader: %v", err)
	}
}

// TestFactory_ExcelSheetReachesReader proves the Sheet knob is consulted:
// a workbook whose only sheet is "Data" reads under Sheet "Data" and fails
// under a sheet it does not have.
func TestFactory_ExcelSheetReachesReader(t *testing.T) {
	w := excel.NewWriterToBuffer(excel.WithSheet("Data"))
	writeRows(t, w, []string{"a"}, [][]string{{"x"}})

	named, err := pio.NewReaderFromBytes(pio.FormatExcel, w.Bytes(),
		pio.ReaderOptions{Excel: pio.ExcelReaderOptions{Sheet: "Data"}})
	if err != nil {
		t.Fatal(err)
	}
	if h, rows := readRows(t, named); !reflect.DeepEqual(h, []string{"a"}) || len(rows) != 1 {
		t.Errorf("Sheet=Data read = %v %v", h, rows)
	}
	missing, err := pio.NewReaderFromBytes(pio.FormatExcel, w.Bytes(),
		pio.ReaderOptions{Excel: pio.ExcelReaderOptions{Sheet: "Nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.ReadHeader(); err == nil {
		t.Error("Sheet=Nope read a workbook with no such sheet; the option was not consulted")
	}
}

// TestFactory_SPSSWriterOptionsReachWriter proves the writer sub-struct is
// consulted: Uncompressed changes the encoding of the same table, and the
// result still reads back identically.
func TestFactory_SPSSWriterOptionsReachWriter(t *testing.T) {
	header := []string{"n"}
	rows := [][]string{{"alpha"}, {"beta"}}
	encode := func(o pio.SPSSWriterOptions) []byte {
		w, err := pio.NewWriterToBuffer(pio.FormatSPSS, pio.WriterOptions{SPSS: o})
		if err != nil {
			t.Fatal(err)
		}
		writeRows(t, w, header, rows)
		return w.Bytes()
	}
	compressed := encode(pio.SPSSWriterOptions{})
	flat := encode(pio.SPSSWriterOptions{Uncompressed: true})
	if bytes.Equal(compressed, flat) {
		t.Fatal("Uncompressed did not change the .sav encoding; the option never reached the writer")
	}
	r, err := pio.NewReaderFromBytes(pio.FormatSPSS, flat, pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if h, got := readRows(t, r); !reflect.DeepEqual(h, header) || !reflect.DeepEqual(got, rows) {
		t.Errorf("uncompressed read back = %v %v", h, got)
	}
}
