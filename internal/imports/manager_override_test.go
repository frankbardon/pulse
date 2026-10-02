package imports

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// overrideFixtureCSV is the cross-adapter source: n holds 1..4 (inference
// alone narrows it to u4), big holds one 300, d holds pre-1970 dates and
// label is free text.
func overrideFixtureCSV() string {
	var b strings.Builder
	b.WriteString("n,big,d,label\n")
	for i := 0; i < 60; i++ {
		big := fmt.Sprint(i%4 + 1)
		if i == 2 {
			big = "300"
		}
		d := "1950-06-01"
		if i%2 == 1 {
			d = "1969-12-31"
		}
		fmt.Fprintf(&b, "%d,%s,%s,lbl%d\n", i%4+1, big, d, i%3)
	}
	return b.String()
}

// overrideAdapters is every text-inference import adapter, by the file
// extension the manager dispatches on.
var overrideAdapters = []struct {
	format pio.Format
	ext    string
}{
	{pio.FormatCSV, "csv"},
	{pio.FormatTSV, "tsv"},
	{pio.FormatNDJSON, "ndjson"},
	{pio.FormatJSONArray, "json"},
	{pio.FormatExcel, "xlsx"},
	{pio.FormatArrow, "arrow"},
	{pio.FormatParquet, "parquet"},
}

// writeOverrideSource materialises the fixture in format f at path: the
// CSV text verbatim, every other format through a cohort export (so the
// reader sees the format's own native encoding of the same values).
func writeOverrideSource(t *testing.T, afs afero.Fs, f pio.Format, path string) {
	t.Helper()
	if err := afero.WriteFile(afs, "fixture.csv", []byte(overrideFixtureCSV()), 0o644); err != nil {
		t.Fatal(err)
	}
	if f == pio.FormatCSV {
		if err := afero.WriteFile(afs, path, []byte(overrideFixtureCSV()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	r, err := pio.NewReader(pio.FormatCSV, afs, "fixture.csv", pio.ReaderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ij := pio.NewImportJob(r, "fixture.pulse")
	ij.FS = afs
	ij.ColumnTypeOverrides = map[string]encoding.FieldType{"label": encoding.FieldTypeCategoricalU8}
	if _, err := ij.Run(context.Background()); err != nil {
		t.Fatalf("fixture import: %v", err)
	}
	_ = r.Close()
	w, err := pio.NewWriter(f, afs, path, pio.WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ej := pio.NewExportJob("fixture.pulse", w)
	ej.FS = afs
	if _, err := ej.Run(context.Background()); err != nil {
		t.Fatalf("fixture export to %s: %v", f, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func overrideDetails(t *testing.T, err error) map[string]any {
	t.Helper()
	var ce *perr.CodedError
	if !stderrors.As(err, &ce) || ce.Code != perr.PULSE_IMPORT_OVERRIDE_INVALID {
		t.Fatalf("err = %v, want PULSE_IMPORT_OVERRIDE_INVALID", err)
	}
	return ce.Details
}

// TestManager_Open_OverrideHonouredEveryAdapter: through ImportSpec, every
// import adapter agrees — the override wins exactly (n forced to u8 is u8,
// not the u4 inference picks; d forced to date accepts pre-1970 days), a
// value the forced type cannot hold refuses the import naming column,
// value and type, and an unknown column is refused.
func TestManager_Open_OverrideHonouredEveryAdapter(t *testing.T) {
	for _, a := range overrideAdapters {
		t.Run(string(a.format), func(t *testing.T) {
			m, afs, _ := newTestManager(t)
			src := "src." + a.ext
			writeOverrideSource(t, afs, a.format, src)

			res, err := m.Open(context.Background(), Spec{SourcePath: src, Handle: "ok",
				ColumnTypeOverrides: map[string]string{"n": "u8", "d": "date", "big": "u16"}})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			for name, want := range map[string]encoding.FieldType{
				"n": encoding.FieldTypeU8, "d": encoding.FieldTypeDate, "big": encoding.FieldTypeU16,
			} {
				if f := res.Schema.Field(name); f == nil || f.Type != want {
					t.Errorf("%s = %v, want %s (forced)", name, f, want)
				}
			}

			_, err = m.Open(context.Background(), Spec{SourcePath: src, Handle: "overflow",
				ColumnTypeOverrides: map[string]string{"big": "u8"}})
			d := overrideDetails(t, err)
			if d["column"] != "big" || d["type"] != "u8" || d["value"] != "300" || d["row"] != 3 {
				t.Errorf("overflow details = %v, want column big, type u8, value 300, row 3", d)
			}

			_, err = m.Open(context.Background(), Spec{SourcePath: src, Handle: "nondate",
				ColumnTypeOverrides: map[string]string{"label": "date"}})
			if d := overrideDetails(t, err); d["column"] != "label" || d["type"] != "date" {
				t.Errorf("non-date details = %v, want column label, type date", d)
			}

			_, err = m.Open(context.Background(), Spec{SourcePath: src, Handle: "unknown",
				ColumnTypeOverrides: map[string]string{"nope": "u8"}})
			if d := overrideDetails(t, err); d["column"] != "nope" {
				t.Errorf("unknown-column details = %v, want column nope", d)
			}
		})
	}
}

// TestManager_Open_OverrideRefusedForSPSS: an SPSS source carries an
// authoritative schema, so an override is refused rather than silently
// ignored.
func TestManager_Open_OverrideRefusedForSPSS(t *testing.T) {
	m, afs, _ := newTestManager(t)
	writeOverrideSource(t, afs, pio.FormatSPSS, "src.sav")
	if _, err := m.Open(context.Background(), Spec{SourcePath: "src.sav", Handle: "plain"}); err != nil {
		t.Fatalf("Open without overrides: %v", err)
	}
	_, err := m.Open(context.Background(), Spec{SourcePath: "src.sav", Handle: "forced",
		ColumnTypeOverrides: map[string]string{"n": "u8"}})
	overrideDetails(t, err)
}
