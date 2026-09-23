package io

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// setCellState is one set cell's storage-level state: the null bit from
// the per-record bitmap plus the membership mask. The two together are
// the THREE states a set column can hold — null, empty mask, non-empty
// mask — and this whole file exists to prove all three survive a round
// trip as distinct values.
type setCellState struct {
	Null bool
	Mask encoding.SetMask
}

func (s setCellState) String() string {
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

// readSetStates walks a cohort's record region by hand and returns the
// per-record state of one set column. Deliberately byte-level: the test
// must observe the null bitmap and the mask independently, which every
// higher-level decode helper already fuses.
func readSetStates(t *testing.T, fs afero.Fs, path, field string) []setCellState {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
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
	if stride == 0 {
		t.Fatal("zero stride")
	}
	payload := blob[len(blob)-r.Len():]

	off, width, idx := 0, 0, -1
	for i, f := range schema.Fields {
		if f.Name == field {
			idx = i
			width = f.Type.ByteSize()
			break
		}
		if f.Type.IsBitPacked() {
			off++
			continue
		}
		off += f.Type.ByteSize()
	}
	if idx < 0 || width == 0 {
		t.Fatalf("field %q not found or not a fixed-width set", field)
	}
	ft := schema.Fields[idx].Type
	bmSize := schema.BitmapByteSize()

	out := make([]setCellState, 0, len(payload)/stride)
	for i := 0; i+stride <= len(payload); i += stride {
		cell := payload[i+off : i+off+width]
		var m encoding.SetMask
		if ft.IsWideSet() {
			var err error
			if m, err = encoding.SetMaskFromBytes(ft, cell); err != nil {
				t.Fatalf("SetMaskFromBytes at record %d: %v", i/stride, err)
			}
		} else {
			// The narrow rungs ride the uint64 value API: little-endian
			// over the field's own width.
			var low uint64
			for b := width - 1; b >= 0; b-- {
				low = low<<8 | uint64(cell[b])
			}
			m = encoding.SetMaskFromUint64(low)
		}
		st := setCellState{Mask: m}
		if bmSize > 0 {
			bitmap := payload[i+stride-bmSize : i+stride]
			st.Null = encoding.BitmapIsNull(bitmap, idx)
		}
		out = append(out, st)
	}
	return out
}

// tristateSchema builds a two-column explicit schema whose second field
// is a nullable set at the requested rung, pre-seeded with tokens
// dictionary entries so the wide rungs carry a genuinely wide mask.
func tristateSchema(t *testing.T, ft encoding.FieldType, tokens int) *encoding.Schema {
	t.Helper()
	dict := encoding.NewDictionary()
	for i := 0; i < tokens; i++ {
		if _, err := dict.AddWithLimit(setToken(i), ft.MaxSetEntries()); err != nil {
			t.Fatalf("seeding dictionary (%s, %d tokens): %v", ft, tokens, err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "sel", Type: ft, Nullable: true, CsvColumnIdx: 1, Dictionary: dict},
	}}
}

func setToken(i int) string {
	return "T" + string(rune('A'+i/26)) + string(rune('A'+i%26))
}

// tristateRungs is the narrow rung AND the wide rung every case in this
// file runs: set_u8 rides the uint64 value API, set_u128 leaves it.
var tristateRungs = []struct {
	Type   encoding.FieldType
	Tokens int
}{
	{encoding.FieldTypeSetU8, 4},
	{encoding.FieldTypeSetU128, 70},
}

// tristateCells returns the three source cells for a rung: a non-empty
// selection, an empty selection, and a null.
func tristateCells(tokens int) (rows [][]string, wantSel []string) {
	nonEmpty := setToken(0) + DefaultSetDelimiter + setToken(tokens-1)
	return [][]string{
			{"1", nonEmpty},
			{"2", EmptySetCell},
			{"3", ""},
		},
		[]string{nonEmpty, EmptySetCell, ""}
}

// importTristate writes a three-record cohort carrying all three states
// under an explicit schema and returns the filesystem holding it.
func importTristate(t *testing.T, ft encoding.FieldType, tokens int, rows [][]string) afero.Fs {
	t.Helper()
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"id", "sel"}, rows), "tri.pulse")
	job.FS = fs
	job.Schema = tristateSchema(t, ft, tokens)
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("%s import: %v", ft, err)
	}
	if rep.RowsImported != len(rows) {
		t.Fatalf("%s import: RowsImported = %d, want %d (row errors: %v)",
			ft, rep.RowsImported, len(rows), rep.RowErrors)
	}
	return fs
}

// TestImportJob_SetThreeStatesAreDistinctOnTheWire pins the storage
// floor the rest of this file depends on: the empty-mask cell and the
// null cell are NOT the same record.
func TestImportJob_SetThreeStatesAreDistinctOnTheWire(t *testing.T) {
	for _, rung := range tristateRungs {
		rows, _ := tristateCells(rung.Tokens)
		fs := importTristate(t, rung.Type, rung.Tokens, rows)
		got := readSetStates(t, fs, "tri.pulse", "sel")
		if len(got) != 3 {
			t.Fatalf("%s: %d records, want 3", rung.Type, len(got))
		}
		if got[0].Null || got[0].Mask.IsEmpty() {
			t.Errorf("%s: non-empty selection stored as %s", rung.Type, got[0])
		}
		if got[1].Null || !got[1].Mask.IsEmpty() {
			t.Errorf("%s: empty selection stored as %s, want empty-mask", rung.Type, got[1])
		}
		if !got[2].Null {
			t.Errorf("%s: null stored as %s, want null", rung.Type, got[2])
		}
	}
}

// TestExportJob_SetEmptyMaskIsNotNull is the story's core assertion on
// the export side. Before this story a bitmap-null cell and a present
// empty selection both left ExportJob.Run as "", and re-import read
// both as null — "ticked none of these" became "skipped the question".
func TestExportJob_SetEmptyMaskIsNotNull(t *testing.T) {
	for _, rung := range tristateRungs {
		rows, wantSel := tristateCells(rung.Tokens)
		fs := importTristate(t, rung.Type, rung.Tokens, rows)

		w := &collectWriter{}
		ex := NewExportJob("tri.pulse", w)
		ex.FS = fs
		if _, err := ex.Run(context.Background()); err != nil {
			t.Fatalf("%s export: %v", rung.Type, err)
		}
		if len(w.rows) != 3 {
			t.Fatalf("%s: exported %d rows, want 3", rung.Type, len(w.rows))
		}
		for i, want := range wantSel {
			got, ok := w.rows[i][1].(string)
			if !ok {
				t.Fatalf("%s row %d: exported cell is %T, want string", rung.Type, i, w.rows[i][1])
			}
			if got != want {
				t.Errorf("%s row %d: exported %q, want %q", rung.Type, i, got, want)
			}
		}
		// The load-bearing inequality, stated on its own so a future
		// convention change cannot quietly re-collapse the two.
		if w.rows[1][1] == w.rows[2][1] {
			t.Errorf("%s: empty-mask and null both exported as %q", rung.Type, w.rows[1][1])
		}
	}
}

// TestExportJob_SetThreeStatesRoundTrip closes the loop through the
// shared import/export path: cohort -> cells -> cohort reproduces the
// same three storage states.
func TestExportJob_SetThreeStatesRoundTrip(t *testing.T) {
	for _, rung := range tristateRungs {
		rows, _ := tristateCells(rung.Tokens)
		fs := importTristate(t, rung.Type, rung.Tokens, rows)
		before := readSetStates(t, fs, "tri.pulse", "sel")

		w := &collectWriter{}
		ex := NewExportJob("tri.pulse", w)
		ex.FS = fs
		if _, err := ex.Run(context.Background()); err != nil {
			t.Fatalf("%s export: %v", rung.Type, err)
		}
		back := make([][]string, 0, len(w.rows))
		for _, r := range w.rows {
			back = append(back, []string{r[0].(string), r[1].(string)})
		}

		fs2 := importTristate(t, rung.Type, rung.Tokens, back)
		after := readSetStates(t, fs2, "tri.pulse", "sel")
		if len(after) != len(before) {
			t.Fatalf("%s: round-tripped %d records, want %d", rung.Type, len(after), len(before))
		}
		for i := range before {
			if after[i].Null != before[i].Null || !after[i].Mask.Equal(before[i].Mask) {
				t.Errorf("%s row %d: round-tripped %s, want %s",
					rung.Type, i, after[i], before[i])
			}
		}
	}
}

// TestFormatSetMask_DictionaryLessCellIsStillPresent covers the one
// arm no end-to-end path reaches: a set field with no dictionary cannot
// resolve a label, but the cell is still PRESENT — null is applied by
// the export loop from the bitmap — so it must take the empty-selection
// form rather than impersonate a null.
func TestFormatSetMask_DictionaryLessCellIsStillPresent(t *testing.T) {
	if got := formatSetMask(encoding.SetMask{}.WithBit(0), nil); got != EmptySetCell {
		t.Errorf("formatSetMask(mask, nil) = %q, want %q", got, EmptySetCell)
	}
}

// TestIsNullToken_UnchangedBySetConvention pins the constraint the
// convention had to respect: the empty-mask cell is carried by a token
// the null-sentinel set does not recognise, so no other field type's
// null handling moves.
func TestIsNullToken_UnchangedBySetConvention(t *testing.T) {
	null := []string{"", "null", "NULL", "Null", "na", "NA", "n/a", "N/A"}
	for _, s := range null {
		if !isNullToken(s) {
			t.Errorf("isNullToken(%q) = false, want true", s)
		}
	}
	notNull := []string{EmptySetCell, "|a", "a|", "nan", "nil", "-", "0", " "}
	for _, s := range notNull {
		if isNullToken(s) {
			t.Errorf("isNullToken(%q) = true, want false", s)
		}
	}
}

// TestEmptySetCell_LeavesCategoricalAlone is the other half of that
// constraint: the marker is a set convention only. A categorical cell
// holding the marker text is an ordinary category, not a null and not
// an empty anything.
func TestEmptySetCell_LeavesCategoricalAlone(t *testing.T) {
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"id", "cat"}, [][]string{
		{"1", "red"}, {"2", EmptySetCell}, {"3", ""},
	}), "cat.pulse")
	job.FS = fs
	job.Schema = &encoding.Schema{Fields: []encoding.Field{
		{Name: "id", Type: encoding.FieldTypeU8, CsvColumnIdx: 0},
		{Name: "cat", Type: encoding.FieldTypeCategoricalU8, Nullable: true,
			CsvColumnIdx: 1, Dictionary: encoding.NewDictionary()},
	}}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	vals := rep.Schema.Field("cat").Dictionary.Values()
	if len(vals) != 2 || vals[0] != "red" || vals[1] != EmptySetCell {
		t.Fatalf("categorical dictionary = %v, want [red %q]", vals, EmptySetCell)
	}
}

// TestProbeSetClassification_EmptySelectionCellsDoNotBlockInference
// guards the inference side of the convention. Empty-selection cells
// now reach inference as the marker rather than as nulls, so they enter
// the sample; counting them against the "average cardinality > 1" gate
// would let a column of mostly-unanswered multi-selects re-import as a
// categorical of literal "|" strings.
func TestProbeSetClassification_EmptySelectionCellsDoNotBlockInference(t *testing.T) {
	values := []string{"a|b", "b|c", "a|c"}
	for i := 0; i < 9; i++ {
		values = append(values, EmptySetCell)
	}
	ft, delim, ok := probeSetClassification(values, 50)
	if !ok {
		t.Fatalf("probeSetClassification did not fire; a column of three real "+
			"multi-selects and nine empty selections is a set (values=%v)", values)
	}
	if !ft.IsSet() {
		t.Errorf("probeSetClassification returned %s, want a set rung", ft)
	}
	if delim != DefaultSetDelimiter {
		t.Errorf("delimiter = %q, want %q", delim, DefaultSetDelimiter)
	}
}

// TestProbeSetClassification_OccasionalPipeStillNotASet pins the gate
// the fix above must not dissolve: a free-text categorical that happens
// to carry a pipe in a minority of cells is still not a set.
func TestProbeSetClassification_OccasionalPipeStillNotASet(t *testing.T) {
	values := []string{"a|b", "plain", "plain", "other", "other", "more"}
	if ft, _, ok := probeSetClassification(values, 50); ok {
		t.Errorf("probeSetClassification fired (%s) on an occasional-pipe categorical", ft)
	}
}
