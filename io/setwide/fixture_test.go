package setwide

// The shared fixture: a cohort whose only interesting column is a
// 206-member set_u256, carrying the three states in a fixed order.
//
// Everything here is format-INDEPENDENT. The per-adapter files own the
// wiring and the expectations; this file owns the cohort they all start
// from and the byte-level observation they all end at. A fault in this
// file fails all nine tests, which is the property that makes sharing it
// safe — unlike a matrix row, it cannot go missing for one adapter.

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	pfs "github.com/frankbardon/pulse/fs"
	pio "github.com/frankbardon/pulse/io"
	"github.com/spf13/afero"
)

// members is the dictionary size under test: 206 labels, which is past
// set_u128's ceiling and inside set_u256's.
const members = 206

// wideType is the rung 206 members land on.
const wideType = encoding.FieldTypeSetU256

// selectedBits are the members the fixture's first record selects.
//
// They straddle every boundary a partial implementation stops at: bit 63
// / 64 is the uint64 word edge and the narrow-rung ceiling, 127 / 128 is
// set_u128's ceiling and the second word edge, and 200 / 205 sit in the
// fourth word, past anything a two-word mask addresses.
var selectedBits = []int{0, 63, 64, 127, 128, 200, 205}

// clearBits are members the same record does NOT select, chosen as the
// immediate neighbours of the selected ones. They are what separates
// "the mask survived" from "the mask is one bit off": a shift of one in
// either direction moves a selected bit onto one of these.
var clearBits = []int{1, 62, 65, 126, 129, 199, 201, 204}

// fixtureRecords is the number of records every adapter must carry —
// one per state. No adapter may swallow one.
const fixtureRecords = 3

// memberName is the label of dictionary entry i. Zero-padded so the
// label order and the bit order are the same order under any sort.
func memberName(i int) string {
	s := strconv.Itoa(i)
	return "M" + strings.Repeat("0", 3-len(s)) + s
}

// wantMask is the mask the first record must hold, on both sides of
// every round trip.
func wantMask() encoding.SetMask {
	var m encoding.SetMask
	for _, b := range selectedBits {
		m = m.WithBit(b)
	}
	return m
}

// selectionCell is the external, flat-text form of that mask: the
// selected labels joined by the standard delimiter, in bit order.
func selectionCell() string {
	parts := make([]string, 0, len(selectedBits))
	for _, b := range selectedBits {
		parts = append(parts, memberName(b))
	}
	return strings.Join(parts, pio.DefaultSetDelimiter)
}

// wideSchema is the two-column explicit schema the fixture imports
// under: a plain id and the nullable wide set, its dictionary
// pre-seeded with all 206 labels so the column is genuinely wide rather
// than a narrow one wearing a wide type byte.
func wideSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < members; i++ {
		if _, err := dict.AddWithLimit(memberName(i), wideType.MaxSetEntries()); err != nil {
			t.Fatalf("seeding dictionary entry %d: %v", i, err)
		}
	}
	if got := len(dict.Values()); got != members {
		t.Fatalf("fixture dictionary has %d entries, want %d", got, members)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "sel", Type: wideType, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
}

// sourceRows are the three states, in the order every assertion indexes
// them: a wide selection, an empty selection, a null.
func sourceRows() [][]string {
	return [][]string{
		{"1", selectionCell()},
		{"2", pio.EmptySetCell},
		{"3", ""},
	}
}

// mockReader is a minimal pio.Reader / ResetReader over in-memory rows,
// used to build the source cohort without going through any adapter.
type mockReader struct {
	columns []string
	rows    [][]string
	pos     int
}

func (m *mockReader) ReadHeader() ([]string, error) { return m.columns, nil }
func (m *mockReader) ReadRows(_ context.Context, fn func(row []string) error) error {
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

// sourceCohort writes src.pulse onto a hermetic in-memory filesystem and
// returns both. It asserts the stored states before handing the cohort
// over, so a per-adapter failure is never the fixture's.
func sourceCohort(t *testing.T) (afero.Fs, string) {
	t.Helper()
	fs := pfs.NewMemMap().Fs()

	job := pio.NewImportJob(&mockReader{columns: []string{"id", "sel"}, rows: sourceRows()}, "src.pulse")
	job.FS = fs
	job.Schema = wideSchema(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("building the source cohort: %v", err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("the source cohort imported %d row(s), want %d (errors %v)",
			rep.RowsImported, fixtureRecords, rep.RowErrors)
	}

	states := readStates(t, fs, "src.pulse", "sel")
	if len(states) != fixtureRecords {
		t.Fatalf("the source cohort holds %d record(s), want %d", len(states), fixtureRecords)
	}
	if states[0].Null || !states[0].Mask.Equal(wantMask()) {
		t.Fatalf("the source cohort's selection is %s, want %v — the fixture itself is wrong",
			states[0], selectedBits)
	}
	if states[1].Null || !states[1].Mask.IsEmpty() || !states[2].Null {
		t.Fatalf("the source cohort's empty/null states are %s and %s, want empty-mask and null",
			states[1], states[2])
	}
	return fs, "src.pulse"
}

// ---------------------------------------------------------------------
// Observation
// ---------------------------------------------------------------------

// cellState is one stored set cell, with the null bitmap and the
// membership mask observed SEPARATELY — every higher-level decode helper
// fuses them into one value, and the fusion is what hides a lost state.
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
	return "mask" + bitsOf(s.Mask)
}

// bitsOf renders a mask's set bits, so a failure names the bits rather
// than 32 bytes of hex.
func bitsOf(m encoding.SetMask) string {
	out := make([]string, 0, m.PopCount())
	for b := range m.Bits() {
		out = append(out, strconv.Itoa(b))
	}
	return "{" + strings.Join(out, ",") + "}"
}

// readStates walks a cohort's record region by hand and returns one
// cellState per record.
func readStates(t *testing.T, fs afero.Fs, path, field string) []cellState {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	r := strings.NewReader(string(blob))
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("%s: ReadHeader: %v", path, err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("%s: ReadSchema: %v", path, err)
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
		t.Fatalf("%s: field %q is absent or not fixed-width", path, field)
	}
	ft := schema.Fields[idx].Type
	bmSize := schema.BitmapByteSize()

	out := make([]cellState, 0, len(payload)/stride)
	for i := 0; i+stride <= len(payload); i += stride {
		cell := payload[i+off : i+off+width]
		var m encoding.SetMask
		if ft.IsWideSet() {
			if m, err = encoding.SetMaskFromBytes(ft, cell); err != nil {
				t.Fatalf("%s: SetMaskFromBytes record %d: %v", path, i/stride, err)
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

// wideField returns a cohort's set column, so a test can state what the
// re-imported schema says about its type and its dictionary.
func wideField(t *testing.T, fs afero.Fs, path, field string) *encoding.Field {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	r := strings.NewReader(string(blob))
	if err := encoding.ReadHeader(r); err != nil {
		t.Fatalf("%s: ReadHeader: %v", path, err)
	}
	schema, err := encoding.ReadSchema(r)
	if err != nil {
		t.Fatalf("%s: ReadSchema: %v", path, err)
	}
	f := schema.Field(field)
	if f == nil {
		t.Fatalf("%s: no %q column in the schema", path, field)
	}
	return f
}

// ---------------------------------------------------------------------
// Plumbing
// ---------------------------------------------------------------------

// exportThrough runs the export of src.pulse into the given Writer and
// fails unless every record came out.
//
// The row count is an assertion and not a formality: an arrow or parquet
// export of a set column used to return a clean report carrying
// RowsExported 0 and one RowError per row, which is the precise failure
// this package exists to make impossible.
func exportThrough(t *testing.T, name string, fs afero.Fs, cohort string, w pio.Writer) {
	t.Helper()
	job := pio.NewExportJob(cohort, w)
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s: export: %v", name, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("%s: writer Close: %v", name, err)
	}
	if rep.RowsExported != fixtureRecords {
		t.Fatalf("%s: exported %d row(s), want %d — a report of success over zero rows is the failure mode (errors %v)",
			name, rep.RowsExported, fixtureRecords, rep.RowErrors)
	}
}

// cellsFrom reads the exported file back through the adapter's own
// Reader and returns its "sel" cells, one per record.
func cellsFrom(t *testing.T, name string, rd pio.Reader) []string {
	t.Helper()
	header, err := rd.ReadHeader()
	if err != nil {
		t.Fatalf("%s: ReadHeader: %v", name, err)
	}
	col := -1
	for i, h := range header {
		if h == "sel" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("%s: no sel column in %v", name, header)
	}
	var cells []string
	if err := rd.ReadRows(context.Background(), func(row []string) error {
		cells = append(cells, row[col])
		return nil
	}); err != nil {
		t.Fatalf("%s: ReadRows: %v", name, err)
	}
	if err := rd.Close(); err != nil {
		t.Fatalf("%s: reader Close: %v", name, err)
	}
	if len(cells) != fixtureRecords {
		t.Fatalf("%s: read back %d record(s), want %d", name, len(cells), fixtureRecords)
	}
	return cells
}

// reimportThrough imports the exported file back into a second cohort
// under the same explicit schema and returns its stored states.
func reimportThrough(t *testing.T, name string, fs afero.Fs, rd pio.Reader, dst string) []cellState {
	t.Helper()
	job := pio.NewImportJob(rd, dst)
	job.FS = fs
	job.Schema = wideSchema(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s: re-import: %v", name, err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("%s: re-imported %d row(s), want %d (errors %v)",
			name, rep.RowsImported, fixtureRecords, rep.RowErrors)
	}
	states := readStates(t, fs, dst, "sel")
	if len(states) != fixtureRecords {
		t.Fatalf("%s: the round-tripped cohort holds %d record(s), want %d",
			name, len(states), fixtureRecords)
	}
	return states
}

// ---------------------------------------------------------------------
// The narrow control
// ---------------------------------------------------------------------

// narrowSelectionCell is the flat-text selection of the narrow-rung
// fixture: the first and last of four members.
const narrowSelectionCell = "N0" + pio.DefaultSetDelimiter + "N3"

// narrowSchema is the same two columns at the BOTTOM of the ladder — a
// four-member set_u8, whose mask rides the uint64 value API.
//
// It exists because the arrow / parquet zero-rows failure was not
// width-specific: it hit the narrow rungs that predate wide sets just as
// hard. A fix verified only at set_u256 would leave the longest-standing
// case unproven.
func narrowSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < 4; i++ {
		if _, err := dict.AddWithLimit("N"+strconv.Itoa(i), encoding.FieldTypeSetU8.MaxSetEntries()); err != nil {
			t.Fatalf("seeding narrow dictionary entry %d: %v", i, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "sel", Type: encoding.FieldTypeSetU8, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
}

// narrowSetCohort writes narrow.pulse carrying the same three states at
// the narrow rung, and returns the filesystem, the path and the mask the
// selection must equal.
func narrowSetCohort(t *testing.T) (afero.Fs, string, encoding.SetMask) {
	t.Helper()
	fs := pfs.NewMemMap().Fs()
	rows := [][]string{
		{"1", narrowSelectionCell},
		{"2", pio.EmptySetCell},
		{"3", ""},
	}
	job := pio.NewImportJob(&mockReader{columns: []string{"id", "sel"}, rows: rows}, "narrow.pulse")
	job.FS = fs
	job.Schema = narrowSchema(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("building the narrow cohort: %v", err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("the narrow cohort imported %d row(s), want %d (errors %v)",
			rep.RowsImported, fixtureRecords, rep.RowErrors)
	}
	want := encoding.SetMask{}.WithBit(0).WithBit(3)
	if states := readStates(t, fs, "narrow.pulse", "sel"); !states[0].Mask.Equal(want) {
		t.Fatalf("the narrow cohort's selection is %s, want %s — the fixture itself is wrong",
			states[0], bitsOf(want))
	}
	return fs, "narrow.pulse", want
}

// reimportNarrow is reimportThrough at the narrow rung.
func reimportNarrow(t *testing.T, name string, fs afero.Fs, rd pio.Reader, dst string) []cellState {
	t.Helper()
	job := pio.NewImportJob(rd, dst)
	job.FS = fs
	job.Schema = narrowSchema(t)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s: re-import: %v", name, err)
	}
	if rep.RowsImported != fixtureRecords {
		t.Fatalf("%s: re-imported %d row(s), want %d (errors %v)",
			name, rep.RowsImported, fixtureRecords, rep.RowErrors)
	}
	states := readStates(t, fs, dst, "sel")
	if len(states) != fixtureRecords {
		t.Fatalf("%s: the round-tripped cohort holds %d record(s), want %d",
			name, len(states), fixtureRecords)
	}
	return states
}
