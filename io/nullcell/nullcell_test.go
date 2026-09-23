package nullcell

import (
	"context"
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

// The three fixture rows, in order: an ordinary value, the EMPTY STRING
// as a value, and a null. Rows 1 and 2 are the pair the whole package
// exists for — they are byte-identical as cell TEXT and differ only in
// the channel beside it.
const (
	rowValue = 0
	rowEmpty = 1
	rowNull  = 2
)

const fixtureRecords = 3

// schemaFixture builds the explicit two-column schema. The categorical
// dictionary is pre-seeded with BOTH the ordinary value and the empty
// string, so "" has a real dictionary ID (1) rather than depending on
// first-seen insertion order.
func schemaFixture(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for _, v := range []string{"red", ""} {
		if _, err := dict.AddWithLimit(v, encoding.FieldTypeCategoricalU8.MaxCategoricalEntries()); err != nil {
			t.Fatalf("seeding dictionary: %v", err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
}

// nullAwareMock is a pio.Reader that also implements pio.NullAwareReader,
// which is the only way to author a source in which the empty string is
// a VALUE. It is the read-side fixture and simultaneously the direct
// test of the interface.
type nullAwareMock struct {
	columns []string
	rows    [][]string
	nulls   [][]bool
	cur     []bool
	pos     int
}

func (m *nullAwareMock) ReadHeader() ([]string, error) { return m.columns, nil }
func (m *nullAwareMock) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		m.cur = m.nulls[m.pos]
		if err := fn(m.rows[m.pos]); err != nil {
			return err
		}
		m.pos++
	}
	return nil
}
func (m *nullAwareMock) Close() error     { return nil }
func (m *nullAwareMock) Reset() error     { m.pos = 0; return nil }
func (m *nullAwareMock) RowNulls() []bool { return m.cur }
func (m *nullAwareMock) rowCount() int    { return len(m.rows) }
func newSource() *nullAwareMock {
	return &nullAwareMock{
		columns: []string{"id", "c"},
		rows:    [][]string{{"1", "red"}, {"2", ""}, {"3", ""}},
		nulls:   [][]bool{{false, false}, {false, false}, {false, true}},
	}
}

var _ pio.NullAwareReader = (*nullAwareMock)(nil)

// buildCohort writes "src.pulse" carrying all three states.
func buildCohort(t *testing.T) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	src := newSource()
	job := pio.NewImportJob(src, "src.pulse")
	job.FS = fs
	job.Schema = schemaFixture(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("building cohort: %v", err)
	}
	if rep.RowsImported != src.rowCount() {
		t.Fatalf("imported %d rows, want %d (errors %v)",
			rep.RowsImported, src.rowCount(), rep.RowErrors)
	}
	return fs
}

// ---------------------------------------------------------------------
// Storage-level observation
// ---------------------------------------------------------------------

// cellState is one stored categorical cell, with the null bitmap read
// INDEPENDENTLY of the dictionary ID — every higher-level decode helper
// fuses the two, and that fusion is the thing under test.
type cellState struct {
	Null bool
	Text string
}

func (s cellState) String() string {
	if s.Null {
		return "null"
	}
	return "value(" + s.Text + ")"
}

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
	dict := schema.Fields[idx].Dictionary
	bmSize := schema.BitmapByteSize()

	out := make([]cellState, 0, len(payload)/stride)
	for i := 0; i+stride <= len(payload); i += stride {
		cell := payload[i+off : i+off+width]
		var id uint64
		for b := width - 1; b >= 0; b-- {
			id = id<<8 | uint64(cell[b])
		}
		st := cellState{}
		if dict != nil {
			st.Text = dict.Resolve(uint32(id))
		}
		if bmSize > 0 {
			st.Null = encoding.BitmapIsNull(payload[i+stride-bmSize:i+stride], idx)
		}
		out = append(out, st)
	}
	return out
}

func exactly(t *testing.T, what string, xs []cellState) []cellState {
	t.Helper()
	if len(xs) != fixtureRecords {
		t.Fatalf("%s: %d records, want exactly %d", what, len(xs), fixtureRecords)
	}
	return xs
}

// ---------------------------------------------------------------------
// The source fixture itself
// ---------------------------------------------------------------------

// TestNullAwareSource_EmptyStringIsAValue is the import-side half on its
// own: a source that declares its own nulls stores the empty-string cell
// as a dictionary VALUE and only the declared cell as null. Without the
// NullAwareReader channel both rows land on isNullToken("") and the
// cohort carries two nulls.
func TestNullAwareSource_EmptyStringIsAValue(t *testing.T) {
	fs := buildCohort(t)
	got := exactly(t, "source cohort", readStates(t, fs, "src.pulse", "c"))

	if got[rowValue].Null || got[rowValue].Text != "red" {
		t.Errorf("ordinary value stored as %s, want value(red)", got[rowValue])
	}
	if got[rowEmpty].Null {
		t.Errorf("empty-string VALUE stored as %s, want a non-null empty value", got[rowEmpty])
	}
	if got[rowEmpty].Text != "" {
		t.Errorf("empty-string value stored as %q, want the empty string", got[rowEmpty].Text)
	}
	if !got[rowNull].Null {
		t.Errorf("declared null stored as %s, want null", got[rowNull])
	}
}

// TestPlainSource_EmptyStringIsStillNull pins the compatibility half:
// a source that does NOT implement NullAwareReader is read exactly as
// before — "" is the null token, whatever the field type.
func TestPlainSource_EmptyStringIsStillNull(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := pio.NewImportJob(&plainMock{columns: []string{"id", "c"},
		rows: [][]string{{"1", "red"}, {"2", ""}, {"3", ""}}}, "plain.pulse")
	job.FS = fs
	job.Schema = schemaFixture(t)
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("import: %v", err)
	}
	got := exactly(t, "plain source", readStates(t, fs, "plain.pulse", "c"))
	if !got[rowEmpty].Null || !got[rowNull].Null {
		t.Errorf("plain text source: states = %v, want both empty cells null", got)
	}
}

type plainMock struct {
	columns []string
	rows    [][]string
	pos     int
}

func (m *plainMock) ReadHeader() ([]string, error) { return m.columns, nil }
func (m *plainMock) ReadRows(ctx context.Context, fn func(row []string) error) error {
	for m.pos < len(m.rows) {
		if err := fn(m.rows[m.pos]); err != nil {
			return err
		}
		m.pos++
	}
	return nil
}
func (m *plainMock) Close() error { return nil }
func (m *plainMock) Reset() error { m.pos = 0; return nil }

// ---------------------------------------------------------------------
// The adapter matrix
// ---------------------------------------------------------------------

// adapter is one row of the matrix. CarriesNull records whether the
// format has a channel for the distinction — the honest-gap flag, kept
// in the table so a change of answer has to be a change of table.
type adapter struct {
	Name        string
	Path        string
	CarriesNull bool
	Writer      func(afero.Fs, string) pio.Writer
	Reader      func(afero.Fs, string) pio.Reader
}

var adapters = []adapter{
	{"csv", "out.csv", false,
		func(fs afero.Fs, p string) pio.Writer { return pcsv.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pcsv.NewReader(fs, p) }},
	{"tsv", "out.tsv", false,
		func(fs afero.Fs, p string) pio.Writer { return ptsv.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return ptsv.NewReader(fs, p) }},
	{"excel", "out.xlsx", false,
		func(fs afero.Fs, p string) pio.Writer { return pexcel.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pexcel.NewReader(fs, p) }},
	{"ndjson", "out.ndjson", true,
		func(fs afero.Fs, p string) pio.Writer { return pndjson.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pndjson.NewReader(fs, p) }},
	{"jsonarray", "out.json", true,
		func(fs afero.Fs, p string) pio.Writer { return pjsonarray.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pjsonarray.NewReader(fs, p) }},
	{"arrow", "out.arrow", true,
		func(fs afero.Fs, p string) pio.Writer { return parrow.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return parrow.NewReader(fs, p) }},
	{"parquet", "out.parquet", true,
		func(fs afero.Fs, p string) pio.Writer { return pparquet.NewWriter(fs, p) },
		func(fs afero.Fs, p string) pio.Reader { return pparquet.NewReader(fs, p) }},
}

// exportThrough exports the fixture cohort through one adapter and
// returns the filesystem it was written into.
func exportThrough(t *testing.T, a adapter) afero.Fs {
	t.Helper()
	fs := buildCohort(t)

	w := a.Writer(fs, a.Path)
	job := pio.NewExportJob("src.pulse", w)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s export: %v", a.Name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("%s writer Close: %v", a.Name, err)
	}
	if rep.RowsExported != fixtureRecords {
		t.Fatalf("%s: exported %d rows, want %d (errors %v)",
			a.Name, rep.RowsExported, fixtureRecords, rep.RowErrors)
	}
	return fs
}

// TestAdapters_NullVsEmptyCategoricalRoundTrip is the acceptance
// criterion: cohort -> format -> cohort. An adapter with a null channel
// reproduces all three states; one without collapses the empty-string
// value into NULL — never the other way, which would invent data.
func TestAdapters_NullVsEmptyCategoricalRoundTrip(t *testing.T) {
	for _, a := range adapters {
		t.Run(a.Name, func(t *testing.T) {
			fs := exportThrough(t, a)

			job := pio.NewImportJob(a.Reader(fs, a.Path), "rt.pulse")
			job.FS = fs
			job.Schema = schemaFixture(t)
			rep, err := job.Run(context.Background())
			if err != nil {
				t.Fatalf("re-import: %v", err)
			}
			if rep.RowsImported != fixtureRecords {
				t.Fatalf("re-imported %d rows, want %d (errors %v)",
					rep.RowsImported, fixtureRecords, rep.RowErrors)
			}

			got := exactly(t, "round-tripped", readStates(t, fs, "rt.pulse", "c"))

			if got[rowValue].Null || got[rowValue].Text != "red" {
				t.Errorf("ordinary value round-tripped as %s, want value(red)", got[rowValue])
			}
			if !got[rowNull].Null {
				t.Errorf("null round-tripped as %s, want null", got[rowNull])
			}

			if a.CarriesNull {
				if got[rowEmpty].Null {
					t.Errorf("empty-string value round-tripped as %s, want a non-null empty value "+
						"— %s has a null channel and must use it", got[rowEmpty], a.Name)
				}
				if got[rowEmpty].Text != "" {
					t.Errorf("empty-string value round-tripped as %q, want the empty string",
						got[rowEmpty].Text)
				}
				return
			}

			// The documented gap. Asserted, not tolerated: the collapse
			// must be toward null, and it must still be a collapse —
			// if a future change gives this adapter a channel, this
			// line fails and the matrix entry has to move with it.
			if !got[rowEmpty].Null {
				t.Errorf("%s: empty-string value round-tripped as %s — the matrix says this "+
					"adapter cannot carry the distinction; update adapter.CarriesNull and "+
					"io.NullAwareReader's matrix together", a.Name, got[rowEmpty])
			}
		})
	}
}

// TestJSONAdapters_EmittedDocumentSpellsTheTwoStates reads the EMITTED
// BYTES rather than a re-import.
//
// This is the assertion shape the set work learned the hard way: a
// round-trip test can pass vacuously when the exported spelling is
// wrong in a way the importer un-wrongs — "" re-imports as null, so an
// export that emitted "" for a null still round-tripped a null. Only
// looking at the document catches it. Here the two rows must be
// LITERALLY different JSON: `null` for the null cell, `""` for the
// empty-string value.
func TestJSONAdapters_EmittedDocumentSpellsTheTwoStates(t *testing.T) {
	for _, a := range adapters {
		if a.Name != "ndjson" && a.Name != "jsonarray" {
			continue
		}
		t.Run(a.Name, func(t *testing.T) {
			fs := exportThrough(t, a)
			blob, err := afero.ReadFile(fs, a.Path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			doc := string(blob)

			if !strings.Contains(doc, `"c":""`) {
				t.Errorf(`emitted document has no `+"`"+`"c":""`+"`"+` — the empty-string VALUE `+
					"was not spelled as an empty JSON string.\n%s", doc)
			}
			if !strings.Contains(doc, `"c":null`) {
				t.Errorf(`emitted document has no `+"`"+`"c":null`+"`"+` — the null cell was not `+
					"spelled as JSON null.\n%s", doc)
			}
		})
	}
}

// TestColumnarAdapters_ValidityBitCarriesTheNull is the same
// document-level assertion for Arrow and Parquet, whose null channel is
// the validity bit rather than a token: the null row must read back
// declared-null and the empty-string row declared-present, both with
// the same "" cell text.
func TestColumnarAdapters_ValidityBitCarriesTheNull(t *testing.T) {
	for _, a := range adapters {
		if !a.CarriesNull {
			continue
		}
		t.Run(a.Name, func(t *testing.T) {
			fs := exportThrough(t, a)
			rd := a.Reader(fs, a.Path)
			header, err := rd.ReadHeader()
			if err != nil {
				t.Fatalf("ReadHeader: %v", err)
			}
			col := -1
			for i, h := range header {
				if h == "c" {
					col = i
				}
			}
			if col < 0 {
				t.Fatalf("no c column in %v", header)
			}
			nr, ok := rd.(pio.NullAwareReader)
			if !ok {
				t.Fatalf("%s Reader does not implement pio.NullAwareReader", a.Name)
			}

			type obs struct {
				text string
				null bool
			}
			var seen []obs
			if err := rd.ReadRows(context.Background(), func(row []string) error {
				nulls := nr.RowNulls()
				if len(nulls) != len(row) {
					t.Fatalf("RowNulls length %d, row length %d", len(nulls), len(row))
				}
				seen = append(seen, obs{text: row[col], null: nulls[col]})
				return nil
			}); err != nil {
				t.Fatalf("ReadRows: %v", err)
			}
			if err := rd.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if len(seen) != fixtureRecords {
				t.Fatalf("%d rows, want %d", len(seen), fixtureRecords)
			}

			if seen[rowEmpty].null {
				t.Errorf("empty-string value reported null by the source channel")
			}
			if !seen[rowNull].null {
				t.Errorf("null cell reported present by the source channel")
			}
			if seen[rowEmpty].text != seen[rowNull].text {
				t.Errorf("cell TEXT already differs (%q vs %q) — this test would then pass "+
					"without the null channel doing any work",
					seen[rowEmpty].text, seen[rowNull].text)
			}
		})
	}
}
