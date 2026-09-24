package settristate

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pio "github.com/frankbardon/pulse/io"
	parrow "github.com/frankbardon/pulse/io/arrow"
	pcsv "github.com/frankbardon/pulse/io/csv"
	pexcel "github.com/frankbardon/pulse/io/excel"
	pjsonarray "github.com/frankbardon/pulse/io/jsonarray"
	pndjson "github.com/frankbardon/pulse/io/ndjson"
	pparquet "github.com/frankbardon/pulse/io/parquet"
	ptsv "github.com/frankbardon/pulse/io/tsv"
	"github.com/spf13/afero"
)

// ---------------------------------------------------------------------
// Fixture
// ---------------------------------------------------------------------

// rung is one point on the set ladder the matrix runs: a narrow rung
// (mask rides the uint64 value API) and a wide rung (mask leaves it).
type rung struct {
	Type   encoding.FieldType
	Tokens int
}

var rungs = []rung{
	{encoding.FieldTypeSetU8, 4},
	{encoding.FieldTypeSetU128, 70},
}

func token(i int) string {
	return "T" + string(rune('A'+i/26)) + string(rune('A'+i%26))
}

// schemaFor builds the two-column explicit schema the fixture uses. The
// set field is nullable (so the cohort carries a null bitmap) and its
// dictionary is pre-seeded, which keeps the wide rung genuinely wide.
func schemaFor(t *testing.T, r rung) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < r.Tokens; i++ {
		if _, err := dict.AddWithLimit(token(i), r.Type.MaxSetEntries()); err != nil {
			t.Fatalf("seeding dictionary for %s: %v", r.Type, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "sel", Type: r.Type, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
}

// sourceRows returns the fixture: the three states under test — a
// non-empty selection, an empty selection, and a null, in that order.
//
// This used to carry a leading filler record for io/ndjson alone, whose
// Reader derived the column names from the FIRST object and then
// resumed the row pass after it, losing that record. The filler is gone
// with the bug, so every adapter now sees exactly the three states and
// every assertion below indexes them directly.
func sourceRows(r rung) [][]string {
	return [][]string{
		{"1", token(0) + pio.DefaultSetDelimiter + token(r.Tokens-1)},
		{"2", pio.EmptySetCell},
		{"3", ""},
	}
}

// fixtureRecords is len(sourceRows): one record per state, and every
// adapter must carry all of them.
const fixtureRecords = 3

// exactly asserts a per-record slice carries one entry per fixture
// state and returns it. No adapter may swallow a record.
func exactly[T any](t *testing.T, what string, xs []T) []T {
	t.Helper()
	if len(xs) != fixtureRecords {
		t.Fatalf("%s: %d records, want exactly %d", what, len(xs), fixtureRecords)
	}
	return xs
}

// mockReader is a minimal pio.Reader / ResetReader over in-memory rows.
type mockReader struct {
	columns []string
	rows    [][]string
	pos     int
}

func (m *mockReader) ReadHeader() ([]string, error) { return m.columns, nil }
func (m *mockReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		if err := fn(m.rows[m.pos]); err != nil {
			return err
		}
		m.pos++
	}
	return nil
}
func (m *mockReader) Close() error { return nil }
func (m *mockReader) Reset() error { m.pos = 0; return nil }

// buildCohort writes "src.pulse" carrying all three states and returns
// the in-memory filesystem holding it.
func buildCohort(t *testing.T, r rung) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(&mockReader{columns: []string{"id", "sel"}, rows: sourceRows(r)}, "src.pulse")
	job.FS = fs
	job.Schema = schemaFor(t, r)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s: building cohort: %v", r.Type, err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("%s: imported %d rows, want %d (errors %v)",
			r.Type, rep.RowsImported, fixtureRecords, rep.RowErrors)
	}
	return fs
}

// ---------------------------------------------------------------------
// Storage-level observation
// ---------------------------------------------------------------------

type cellState struct {
	Null bool
	Mask encoding.SetMask
}

func (s cellState) String() string {
	if s.Null {
		return "null"
	}
	if s.Mask.IsEmpty() {
		return "empty-mask"
	}
	bits := make([]string, 0, s.Mask.PopCount())
	for b := range s.Mask.Bits() {
		bits = append(bits, strconv.Itoa(b))
	}
	return "mask{" + strings.Join(bits, ",") + "}"
}

// readStates walks the cohort's record region by hand so the null bitmap
// and the membership mask are observed independently — every
// higher-level decode helper already fuses them into one value, which is
// precisely the fusion under test.
func readStates(t *testing.T, fs afero.Fs, path, field string) []cellState {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	r := strings.NewReader(string(blob))
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("ReadSchema: %v", err)
	}
	stride := schema.RecordByteSize()
	payload := blob[len(blob)-r.Len():]

	off, width, idx := 0, 0, -1
	for i, f := range schema.Fields {
		if f.Name == field {
			idx, width = i, f.Type.ByteSize()
			break
		}
		if f.Type.IsBitPacked() {
			off++
			continue
		}
		off += f.Type.ByteSize()
	}
	if idx < 0 || width == 0 || stride == 0 {
		t.Fatalf("field %q not found or not fixed-width in %s", field, path)
	}
	ft := schema.Fields[idx].Type
	bmSize := schema.BitmapByteSize()

	out := make([]cellState, 0, len(payload)/stride)
	for i := 0; i+stride <= len(payload); i += stride {
		cell := payload[i+off : i+off+width]
		var m encoding.SetMask
		if ft.IsWideSet() {
			if m, err = encoding.SetMaskFromBytes(ft, cell); err != nil {
				t.Fatalf("SetMaskFromBytes record %d: %v", i/stride, err)
			}
		} else {
			var low uint64
			for b := width - 1; b >= 0; b-- {
				low = low<<8 | uint64(cell[b])
			}
			m = encoding.SetMaskFromUint64(low)
		}
		st := cellState{Mask: m}
		if bmSize > 0 {
			st.Null = encoding.BitmapIsNull(payload[i+stride-bmSize:i+stride], idx)
		}
		out = append(out, st)
	}
	return out
}

// ---------------------------------------------------------------------
// The adapter matrix
// ---------------------------------------------------------------------

// adapter is one row of the matrix. Two arms are represented and both
// must reach the same answer:
//
//   - FLAT text — csv, tsv, excel. No native null / empty-list channel;
//     the three states ride the io.EmptySetCell convention alone.
//   - STRUCTURED — ndjson, jsonarray, arrow, parquet. A native null
//     exists (JSON null, the Arrow / Parquet validity bit) and a native
//     empty list exists; the adapters use them, and the convention is
//     what the shared []string reader/writer interface carries between.
type adapter struct {
	Name   string
	Path   string
	Writer func(afero.Fs, string) pio.Writer
	Reader func(afero.Fs, string) pio.Reader
}

var adapters = []adapter{
	{"csv", "out.csv",
		func(fs afero.Fs, p string) pio.Writer { return pcsv.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pcsv.NewReader(fs, p) }},
	{"tsv", "out.tsv",
		func(fs afero.Fs, p string) pio.Writer { return ptsv.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return ptsv.NewReader(fs, p) }},
	{"excel", "out.xlsx",
		func(fs afero.Fs, p string) pio.Writer { return pexcel.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pexcel.NewReader(fs, p) }},
	{"ndjson", "out.ndjson",
		func(fs afero.Fs, p string) pio.Writer { return pndjson.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pndjson.NewReader(fs, p) }},
	{"jsonarray", "out.json",
		func(fs afero.Fs, p string) pio.Writer { return pjsonarray.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pjsonarray.NewReader(fs, p) }},
	{"arrow", "out.arrow",
		func(fs afero.Fs, p string) pio.Writer { return parrow.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return parrow.NewReader(fs, p) }},
	{"parquet", "out.parquet",
		func(fs afero.Fs, p string) pio.Writer { return pparquet.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pparquet.NewReader(fs, p) }},
}

// exportThrough exports the fixture cohort through one adapter and
// returns the filesystem plus the "sel" cells as the adapter's own
// Reader yields them.
func exportThrough(t *testing.T, a adapter, r rung) (afero.Fs, []string) {
	t.Helper()
	fs := buildCohort(t, r)

	w := a.Writer(fs, a.Path)
	job := pio.NewExportJob("src.pulse", w)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s/%s export: %v", a.Name, r.Type, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("%s/%s writer Close: %v", a.Name, r.Type, err)
	}
	if rep.RowsExported != fixtureRecords {
		t.Fatalf("%s/%s: exported %d rows, want %d (errors %v)",
			a.Name, r.Type, rep.RowsExported, fixtureRecords, rep.RowErrors)
	}

	rd := a.Reader(fs, a.Path)
	header, err := rd.ReadHeader()
	if err != nil {
		t.Fatalf("%s/%s ReadHeader: %v", a.Name, r.Type, err)
	}
	col := -1
	for i, h := range header {
		if h == "sel" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("%s/%s: no sel column in %v", a.Name, r.Type, header)
	}
	var cells []string
	if err := rd.ReadRows(context.Background(), func(row []string) error {
		cells = append(cells, row[col])
		return nil
	}); err != nil {
		t.Fatalf("%s/%s ReadRows: %v", a.Name, r.Type, err)
	}
	if err := rd.Close(); err != nil {
		t.Fatalf("%s/%s reader Close: %v", a.Name, r.Type, err)
	}
	return fs, exactly(t, a.Name+"/"+r.Type.String(), cells)
}

// TestAdapters_ThreeSetStatesAreDistinctOnTheWay_Out asserts that every
// adapter's own Reader can still tell the three states apart after the
// export — the null cell reads back as a null token, the empty
// selection as io.EmptySetCell, and the non-empty selection as its
// tokens.
func TestAdapters_ThreeSetStatesAreDistinctOnTheWayOut(t *testing.T) {
	for _, a := range adapters {
		for _, r := range rungs {
			t.Run(a.Name+"/"+r.Type.String(), func(t *testing.T) {
				_, cells := exportThrough(t, a, r)
				want := token(0) + pio.DefaultSetDelimiter + token(r.Tokens-1)

				if cells[0] != want {
					t.Errorf("non-empty selection read back as %q, want %q", cells[0], want)
				}
				if cells[1] != pio.EmptySetCell {
					t.Errorf("empty selection read back as %q, want %q (the empty-selection marker)",
						cells[1], pio.EmptySetCell)
				}
				if cells[2] != "" {
					t.Errorf("null read back as %q, want the empty string", cells[2])
				}
				if cells[1] == cells[2] {
					t.Errorf("empty selection and null both read back as %q", cells[1])
				}
			})
		}
	}
}

// TestAdapters_ThreeSetStatesRoundTrip is the acceptance criterion
// itself: cohort -> format -> cohort reproduces the same three storage
// states, at a narrow rung and a wide rung, through every adapter.
func TestAdapters_ThreeSetStatesRoundTrip(t *testing.T) {
	for _, a := range adapters {
		for _, r := range rungs {
			t.Run(a.Name+"/"+r.Type.String(), func(t *testing.T) {
				fs, _ := exportThrough(t, a, r)
				before := exactly(t, "source", readStates(t, fs, "src.pulse", "sel"))

				job := pio.NewImportJob(a.Reader(fs, a.Path), "rt.pulse")
				job.FS = fs
				job.Schema = schemaFor(t, r)
				rep, err := job.Run(context.Background())
				if err != nil {
					t.Fatalf("re-import: %v", err)
				}
				if rep.RowsImported != fixtureRecords {
					t.Fatalf("re-imported %d rows, want %d (errors %v)",
						rep.RowsImported, fixtureRecords, rep.RowErrors)
				}

				after := exactly(t, "round-tripped", readStates(t, fs, "rt.pulse", "sel"))
				for i := range before {
					if after[i].Null != before[i].Null || !after[i].Mask.Equal(before[i].Mask) {
						t.Errorf("row %d: round-tripped %s, want %s", i, after[i], before[i])
					}
				}
			})
		}
	}
}

// TestJSONAdapters_NativeEmptyArrayIsAnEmptySelection covers the other
// direction for the structured JSON adapters: a hand-authored document
// that spells the three states the JSON way — a token array, `[]` and
// `null` — imports to the same three states. Pulse writes the
// io.EmptySetCell marker so a set column stays one JSON type, but a
// producer that does not know the convention and simply emits `[]` must
// not have its empty selections read as nulls.
func TestJSONAdapters_NativeEmptyArrayIsAnEmptySelection(t *testing.T) {
	for _, r := range rungs {
		t.Run(r.Type.String(), func(t *testing.T) {
			hi := token(r.Tokens - 1)
			doc := "" +
				`{"id":1,"sel":["` + token(0) + `","` + hi + `"]}` + "\n" +
				`{"id":2,"sel":[]}` + "\n" +
				`{"id":3,"sel":null}` + "\n"

			fs := afero.NewMemMapFs()
			if err := afero.WriteFile(fs, "native.ndjson", []byte(doc), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			job := pio.NewImportJob(pndjson.NewReader(fs, "native.ndjson"), "native.pulse")
			job.FS = fs
			job.Schema = schemaFor(t, r)
			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if rep.RowsImported != fixtureRecords {
				t.Fatalf("imported %d rows, want %d (errors %v)",
					rep.RowsImported, fixtureRecords, rep.RowErrors)
			}

			got := exactly(t, "native ndjson", readStates(t, fs, "native.pulse", "sel"))
			if got[0].Null || got[0].Mask.PopCount() != 2 {
				t.Errorf("token array stored as %s, want a two-bit mask", got[0])
			}
			if got[1].Null || !got[1].Mask.IsEmpty() {
				t.Errorf("JSON [] stored as %s, want empty-mask", got[1])
			}
			if !got[2].Null {
				t.Errorf("JSON null stored as %s, want null", got[2])
			}
		})
	}
}

// TestAdapters_NonEmptySetCellUnchanged pins the compatibility half of
// the convention: introducing an empty-selection marker must not move a
// NON-EMPTY set cell's external form by a single byte. The expectation
// is spelled literally rather than derived, so a future formatter
// change cannot satisfy it by moving with the code.
func TestAdapters_NonEmptySetCellUnchanged(t *testing.T) {
	for _, a := range adapters {
		t.Run(a.Name, func(t *testing.T) {
			_, cells := exportThrough(t, a, rungs[0])
			if cells[0] != "TAA|TAD" {
				t.Errorf("non-empty set cell exported as %q, want %q", cells[0], "TAA|TAD")
			}
		})
	}
}
