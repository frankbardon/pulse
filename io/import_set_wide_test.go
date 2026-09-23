package io

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// wideSetCohort imports a `tokens`-wide pipe-delimited column over
// `rows` rows into an in-memory cohort and returns the import report
// plus the filesystem holding it. Inference picks the rung, so the
// caller gets whatever the ladder chose for that vocabulary.
func wideSetCohort(t *testing.T, tokens, rows int) (*ImportReport, afero.Fs) {
	t.Helper()
	r := wideSetReader(tokens, rows)
	fs := afero.NewMemMapFs()
	job := NewImportJob(r, "wide.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run(%d tokens): %v", tokens, err)
	}
	return rep, fs
}

// TestImportJob_WideSetImportsEveryRow is the regression test for the
// data-loss hole the wide rungs opened: inference produces set_u128 /
// set_u256, the row pass must be able to CONVERT them. Before this
// story every row of such a column failed with "unsupported field
// type: set_u256" and the import "succeeded" with RowsImported == 0.
func TestImportJob_WideSetImportsEveryRow(t *testing.T) {
	cases := []struct {
		tokens int
		want   encoding.FieldType
	}{
		{100, encoding.FieldTypeSetU128},
		{206, encoding.FieldTypeSetU256},
	}
	const rows = 480
	for _, c := range cases {
		rep, _ := wideSetCohort(t, c.tokens, rows)
		if got := rep.Schema.Field("issuers").Type; got != c.want {
			t.Fatalf("%d-token column type = %s, want %s", c.tokens, got, c.want)
		}
		if len(rep.RowErrors) != 0 {
			t.Errorf("%d tokens: %d row errors, want 0; first = %v",
				c.tokens, len(rep.RowErrors), rep.RowErrors[0].Err)
		}
		if rep.RowsImported != rows {
			t.Errorf("%d tokens: RowsImported = %d, want %d",
				c.tokens, rep.RowsImported, rows)
		}
	}
}

// TestImportJob_WideSetBitsAboveSixtyFour walks the cohort back off
// the wire and asserts the bit a token landed on is the bit its
// dictionary index names — specifically across the word boundaries at
// 64 and 128, and at bit 200 in the top word. A mask assembled with a
// uint64 shift would silently drop every one of these.
func TestImportJob_WideSetBitsAboveSixtyFour(t *testing.T) {
	const tokens, rows = 206, 480
	rep, fs := wideSetCohort(t, tokens, rows)
	f := rep.Schema.Field("issuers")
	if f.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("issuers type = %s, want set_u256", f.Type)
	}

	// Dictionary index of each probe token. The probes straddle the
	// 64/128/192 word boundaries the four-word mask is made of.
	idx := map[string]int{}
	for i, v := range f.Dictionary.Values() {
		idx[v] = i
	}
	probes := []string{}
	for _, tok := range f.Dictionary.Values() {
		if i := idx[tok]; i == 63 || i == 64 || i == 65 || i == 127 || i == 128 || i == 200 {
			probes = append(probes, tok)
		}
	}
	if len(probes) != 6 {
		t.Fatalf("probe tokens = %d, want 6 (dictionary has %d entries)",
			len(probes), len(f.Dictionary.Values()))
	}

	masks := readWideSetColumn(t, fs, "wide.pulse", "issuers")
	if len(masks) != rows {
		t.Fatalf("decoded %d records, want %d", len(masks), rows)
	}

	// Every probe token must be selected on at least one row, and the
	// bit set there must be exactly its dictionary index.
	for _, tok := range probes {
		bit := idx[tok]
		seen := false
		for _, m := range masks {
			if m.Has(bit) {
				seen = true
				break
			}
		}
		if !seen {
			t.Errorf("token %q (dictionary index %d) is selected on no row — "+
				"its bit was dropped during mask assembly", tok, bit)
		}
	}

	// And the union across all rows must carry every dictionary bit,
	// including the ones at or above 64.
	var union encoding.SetMask
	for _, m := range masks {
		union = union.Union(m)
	}
	if got := union.PopCount(); got != tokens {
		t.Errorf("union population = %d, want %d (the full dictionary)", got, tokens)
	}
	if hi := union.HighestBit(); hi != tokens-1 {
		t.Errorf("union highest bit = %d, want %d", hi, tokens-1)
	}
}

// TestConvertValueWide_SetMaskBits pins the token -> mask conversion
// directly at each word boundary, independent of what inference
// happened to assign.
func TestConvertValueWide_SetMaskBits(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		max := int(ft.MaxSetEntries())
		dict := encoding.NewDictionary()
		// Seed the dictionary so token Tn lands on bit n.
		for i := 0; i < max; i++ {
			if _, err := dict.AddWithLimit("T"+itoa(i), ft.MaxSetEntries()); err != nil {
				t.Fatalf("%s: seeding dictionary at %d: %v", ft, i, err)
			}
		}
		field := encoding.Field{Name: "s", Type: ft}

		probes := []int{0, 1, 63, 64, 65, 127}
		if max > 128 {
			probes = append(probes, 128, 191, 192, 200, 255)
		}
		for _, bit := range probes {
			raw := "T" + itoa(bit)
			wb, err := convertValueWide(raw, field, dict, DefaultSetDelimiter)
			if err != nil {
				t.Fatalf("%s: convertValueWide(%q): %v", ft, raw, err)
			}
			m, err := encoding.SetMaskFromBytes(ft, wb[:ft.ByteSize()])
			if err != nil {
				t.Fatalf("%s: SetMaskFromBytes: %v", ft, err)
			}
			if m.PopCount() != 1 || !m.Has(bit) {
				t.Errorf("%s: token %q produced mask with bits %v, want only bit %d",
					ft, raw, maskBits(m), bit)
			}
		}

		// A multi-token cell spanning three words.
		if max > 128 {
			wb, err := convertValueWide("T3|T64|T200", field, dict, DefaultSetDelimiter)
			if err != nil {
				t.Fatalf("%s: convertValueWide(multi): %v", ft, err)
			}
			m, err := encoding.SetMaskFromBytes(ft, wb[:ft.ByteSize()])
			if err != nil {
				t.Fatalf("%s: SetMaskFromBytes: %v", ft, err)
			}
			want := []int{3, 64, 200}
			if got := maskBits(m); len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
				t.Errorf("%s: multi-word cell bits = %v, want %v", ft, got, want)
			}
		}
	}
}

// TestConvertValueWide_EmptyCellIsEmptyMask pins the settled semantic:
// a present cell carrying no token is an EMPTY selection, not a null,
// at 128 and 256 bits exactly as at 8. Null rides the record bitmap.
func TestConvertValueWide_EmptyCellIsEmptyMask(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		dict := encoding.NewDictionary()
		if _, err := dict.AddWithLimit("A", ft.MaxSetEntries()); err != nil {
			t.Fatal(err)
		}
		field := encoding.Field{Name: "s", Type: ft}
		// "|" is not a null token (isNullToken only matches "", null,
		// na, n/a), so it reaches convertValueWide as a present cell
		// that splits into zero tokens.
		wb, err := convertValueWide("|", field, dict, DefaultSetDelimiter)
		if err != nil {
			t.Fatalf("%s: convertValueWide(%q): %v", ft, "|", err)
		}
		m, err := encoding.SetMaskFromBytes(ft, wb[:ft.ByteSize()])
		if err != nil {
			t.Fatalf("%s: SetMaskFromBytes: %v", ft, err)
		}
		if !m.IsEmpty() {
			t.Errorf("%s: empty cell produced bits %v, want an empty mask", ft, maskBits(m))
		}
		// The dictionary must not have grown an entry for the empty
		// token — that would burn a bit on nothing.
		if got := len(dict.Values()); got != 1 {
			t.Errorf("%s: dictionary grew to %d entries on an empty cell", ft, got)
		}
	}
}

// TestConvertValueWide_UnknownTokenBehavesAsAtNarrowWidths pins the
// settled decision that the wide rungs add no new tolerance and no new
// error: an unseen token is interned exactly as it is at set_u8, and
// overflowing the rung's capacity is still PULSE_IMPORT_SET_OVERFLOW.
func TestConvertValueWide_UnknownTokenBehavesAsAtNarrowWidths(t *testing.T) {
	ft := encoding.FieldTypeSetU128
	dict := encoding.NewDictionary()
	field := encoding.Field{Name: "s", Type: ft}

	// Unseen token: interned, gets bit 0, no error.
	wb, err := convertValueWide("NEW", field, dict, DefaultSetDelimiter)
	if err != nil {
		t.Fatalf("convertValueWide(unseen): %v", err)
	}
	m, err := encoding.SetMaskFromBytes(ft, wb[:ft.ByteSize()])
	if err != nil {
		t.Fatal(err)
	}
	if !m.Has(0) || m.PopCount() != 1 {
		t.Errorf("unseen token bits = %v, want only bit 0", maskBits(m))
	}

	// Fill to capacity then overflow.
	for i := 1; i < int(ft.MaxSetEntries()); i++ {
		if _, err := convertValueWide("T"+itoa(i), field, dict, DefaultSetDelimiter); err != nil {
			t.Fatalf("filling dictionary at %d: %v", i, err)
		}
	}
	if _, err := convertValueWide("OVERFLOW", field, dict, DefaultSetDelimiter); err == nil {
		t.Errorf("token %d did not overflow set_u128", ft.MaxSetEntries()+1)
	} else if !strings.Contains(err.Error(), "set dictionary overflowed") {
		t.Errorf("overflow error = %v, want the set-overflow message", err)
	}
}

// TestImportJob_WideSetNullCellRidesTheBitmap confirms a null cell in a
// wide-set column is recorded on the per-record null bitmap and writes
// a zero-filled payload of the field's FULL width — not the 16 bytes
// the decimal128 wide path used to hardcode. A short write here shifts
// every subsequent column of every subsequent record.
func TestImportJob_WideSetNullCellRidesTheBitmap(t *testing.T) {
	const tokens, rows = 206, 480
	cells := widePipeCells(tokens, rows)
	cells[7] = "" // out-of-sample null promotes the field
	categories := []string{"A", "B", "C", "D"}
	raw := make([][]string, 0, rows)
	for i, c := range cells {
		raw = append(raw, []string{categories[i%len(categories)], c})
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"id", "issuers"}, raw), "wide.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.RowsImported != rows {
		t.Fatalf("RowsImported = %d, want %d", rep.RowsImported, rows)
	}
	f := rep.Schema.Field("issuers")
	if f.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("issuers type = %s, want set_u256", f.Type)
	}
	if !f.Nullable {
		t.Fatalf("issuers was not promoted to nullable by the out-of-sample null")
	}

	masks := readWideSetColumn(t, fs, "wide.pulse", "issuers")
	if len(masks) != rows {
		t.Fatalf("decoded %d records, want %d — the null row wrote the wrong width", len(masks), rows)
	}
	if !masks[7].IsEmpty() {
		t.Errorf("null row payload = %v, want all zero", maskBits(masks[7]))
	}
	// Records after the null must still decode their real selections;
	// a short null write would desynchronize the stride.
	if masks[8].IsEmpty() {
		t.Errorf("record after the null decoded empty — the stride desynchronized")
	}
}

// TestImportJob_MixedWideFieldWidths guards the raw-bytes scratch
// slice now that it carries THREE different widths: decimal128 and
// set_u128 at 16 bytes, set_u256 at 32. Every one must be written at
// its own width — a fixed-width write pads the narrower ones and
// shifts every column after them on every record, which decodes as
// plausible-looking wrong values rather than as an error. The decimal
// column sits between the two set columns so a pad on either side of
// it is visible.
func TestImportJob_MixedWideFieldWidths(t *testing.T) {
	setDict := func(n int) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for i := 0; i < n; i++ {
			if _, err := d.AddWithLimit("T"+itoa(i), 256); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "narrow", Type: encoding.FieldTypeSetU128, CsvColumnIdx: 0, Dictionary: setDict(70)},
			{Name: "amount", Type: encoding.FieldTypeDecimal128, Precision: 20, Scale: 2, CsvColumnIdx: 1},
			{Name: "wide", Type: encoding.FieldTypeSetU256, CsvColumnIdx: 2, Dictionary: setDict(206)},
			{Name: "tail", Type: encoding.FieldTypeU32, CsvColumnIdx: 3},
		},
	}
	rows := [][]string{
		{"T0|T69", "12.34", "T5|T200", "7"},
		{"T64", "-0.01", "T128|T205", "9"},
		{"T1|T2|T63", "999.99", "T0", "11"},
	}
	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: &stringRowsReader{header: []string{"narrow", "amount", "wide", "tail"}, rows: rows},
		Target: "mixed.pulse",
		Schema: schema,
		FS:     fs,
	}
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.RowErrors) != 0 {
		t.Fatalf("%d row errors, want 0; first = %v", len(rep.RowErrors), rep.RowErrors[0].Err)
	}
	if rep.RowsImported != len(rows) {
		t.Fatalf("RowsImported = %d, want %d", rep.RowsImported, len(rows))
	}

	w := &collectWriter{}
	ex := NewExportJob("mixed.pulse", w)
	ex.FS = fs
	erep, err := ex.Run(context.Background())
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if erep.RowsExported != len(rows) {
		t.Fatalf("RowsExported = %d, want %d — the stride desynchronized",
			erep.RowsExported, len(rows))
	}
	for i, want := range rows {
		got := w.rows[i]
		if len(got) != len(want) {
			t.Fatalf("row %d: %d cells, want %d", i, len(got), len(want))
		}
		for k := range want {
			if s, _ := got[k].(string); s != want[k] {
				t.Errorf("row %d column %q: exported %q, want %q",
					i, schema.Fields[k].Name, s, want[k])
			}
		}
	}
}

// maskBits materializes a mask's set bits for readable failure output.
func maskBits(m encoding.SetMask) []int {
	out := []int{}
	for b := range m.Bits() {
		out = append(out, b)
	}
	return out
}

// readWideSetColumn decodes one wide-set column out of a cohort file,
// walking the record region by hand so the test depends on the byte
// layout rather than on any decode helper it is also exercising.
func readWideSetColumn(t *testing.T, fs afero.Fs, path, field string) []encoding.SetMask {
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
	consumed := len(blob) - r.Len()
	payload := blob[consumed:]
	if stride == 0 {
		t.Fatal("zero stride")
	}

	// Byte offset of the target field inside a record.
	off, width := 0, 0
	for _, f := range schema.Fields {
		if f.Type.IsBitPacked() {
			if f.Name == field {
				t.Fatalf("field %q is bit-packed, not a wide set", field)
			}
			off++
			continue
		}
		if f.Name == field {
			width = f.Type.ByteSize()
			break
		}
		off += f.Type.ByteSize()
	}
	if width == 0 {
		t.Fatalf("field %q not found or zero width", field)
	}
	ft := schema.Field(field).Type

	out := make([]encoding.SetMask, 0, len(payload)/stride)
	for i := 0; i+stride <= len(payload); i += stride {
		m, err := encoding.SetMaskFromBytes(ft, payload[i+off:i+off+width])
		if err != nil {
			t.Fatalf("SetMaskFromBytes at record %d: %v", i/stride, err)
		}
		out = append(out, m)
	}
	return out
}
