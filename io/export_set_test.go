package io

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/spf13/afero"
)

// exportSetColumn imports a pipe-delimited set column, exports it back
// through the canonical string path and returns (source cells, exported
// cells, the inferred field). Everything the set export contract says
// is checkable off those three.
func exportSetColumn(t *testing.T, tokens, rows int) ([]string, []string, encoding.Field) {
	t.Helper()
	cells := widePipeCells(tokens, rows)
	categories := []string{"A", "B", "C", "D"}
	raw := make([][]string, 0, rows)
	for i, c := range cells {
		raw = append(raw, []string{categories[i%len(categories)], c})
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"id", "issuers"}, raw), "sets.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("import(%d tokens): %v", tokens, err)
	}
	if rep.RowsImported != rows {
		t.Fatalf("import(%d tokens): RowsImported = %d, want %d", tokens, rep.RowsImported, rows)
	}

	w := &collectWriter{}
	ex := NewExportJob("sets.pulse", w)
	ex.FS = fs
	erep, err := ex.Run(context.Background())
	if err != nil {
		t.Fatalf("export(%d tokens): %v", tokens, err)
	}
	if erep.RowsExported != rows {
		t.Fatalf("export(%d tokens): RowsExported = %d, want %d", tokens, erep.RowsExported, rows)
	}

	col := -1
	for i, h := range w.header {
		if h == "issuers" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("issuers column missing from exported header %v", w.header)
	}
	got := make([]string, 0, rows)
	for _, r := range w.rows {
		s, ok := r[col].(string)
		if !ok {
			t.Fatalf("exported set cell is %T, want string", r[col])
		}
		got = append(got, s)
	}
	return cells, got, *rep.Schema.Field("issuers")
}

// TestExportJob_SetEmitsLabelsAtEveryWidth pins the set export
// contract across the whole ladder: a set cell exports as the
// delimiter-joined LABELS of its selected bits, in ascending bit
// order — never as the numeric mask. The wide rungs additionally
// exercise the raw-bytes read path, which the uint64 ReadFieldValue
// refuses outright.
func TestExportJob_SetEmitsLabelsAtEveryWidth(t *testing.T) {
	cases := []struct {
		tokens int
		want   encoding.FieldType
	}{
		{6, encoding.FieldTypeSetU8},
		{40, encoding.FieldTypeSetU64},
		{100, encoding.FieldTypeSetU128},
		{206, encoding.FieldTypeSetU256},
	}
	const rows = 480
	for _, c := range cases {
		src, got, field := exportSetColumn(t, c.tokens, rows)
		if field.Type != c.want {
			t.Fatalf("%d tokens: inferred %s, want %s", c.tokens, field.Type, c.want)
		}
		// widePipeCells emits tokens in ascending first-seen order, so
		// the source cell is already in dictionary-index order and the
		// export must reproduce it verbatim.
		for i := range src {
			if got[i] != src[i] {
				t.Fatalf("%s row %d: exported %q, want %q",
					field.Type, i, got[i], src[i])
			}
		}
	}
}

// TestExportJob_SetNeverEmitsRawMask is the falsifiable half of the
// above: before this story a set column exported as the decimal
// bitmask ("3" for the first two dictionary entries), which re-imports
// as a categorical string and destroys the column.
func TestExportJob_SetNeverEmitsRawMask(t *testing.T) {
	_, got, field := exportSetColumn(t, 6, 200)
	if field.Type != encoding.FieldTypeSetU8 {
		t.Fatalf("inferred %s, want set_u8", field.Type)
	}
	for i, cell := range got {
		if !strings.Contains(cell, DefaultSetDelimiter) {
			t.Fatalf("row %d exported %q — a two-token set cell must carry the %q delimiter, "+
				"not a numeric mask", i, cell, DefaultSetDelimiter)
		}
		for _, tok := range splitSetTokens(cell, DefaultSetDelimiter) {
			if !strings.HasPrefix(tok, "T") {
				t.Fatalf("row %d exported token %q — want a dictionary label", i, tok)
			}
		}
	}
}

// TestExportJob_WideSetRoundTripsThroughText is the end-to-end
// contract: cohort -> text cells -> cohort reproduces the same
// selections. Re-inference sees the same vocabulary and picks the same
// rung, and each row's selected label set is preserved.
func TestExportJob_WideSetRoundTripsThroughText(t *testing.T) {
	const tokens, rows = 206, 480
	src, exported, field := exportSetColumn(t, tokens, rows)
	if field.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("inferred %s, want set_u256", field.Type)
	}

	categories := []string{"A", "B", "C", "D"}
	raw := make([][]string, 0, rows)
	for i, c := range exported {
		raw = append(raw, []string{categories[i%len(categories)], c})
	}
	fs := afero.NewMemMapFs()
	job := NewImportJob(newMockReader([]string{"id", "issuers"}, raw), "round.pulse")
	job.FS = fs
	rep, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if rep.RowsImported != rows {
		t.Fatalf("re-import RowsImported = %d, want %d", rep.RowsImported, rows)
	}
	rt := rep.Schema.Field("issuers")
	if rt.Type != encoding.FieldTypeSetU256 {
		t.Fatalf("re-import inferred %s, want set_u256", rt.Type)
	}
	if got := len(rt.Dictionary.Values()); got != tokens {
		t.Errorf("re-imported dictionary = %d entries, want %d", got, tokens)
	}

	masks := readWideSetColumn(t, fs, "round.pulse", "issuers")
	if len(masks) != rows {
		t.Fatalf("re-imported %d records, want %d", len(masks), rows)
	}
	for i := range src {
		want := splitSetTokens(src[i], DefaultSetDelimiter)
		got := masks[i].Labels(rt.Dictionary)
		if len(got) != len(want) {
			t.Fatalf("row %d: round-tripped %v, want %v", i, got, want)
		}
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("row %d: round-tripped %v, want %v", i, got, want)
			}
		}
	}
}

// TestFormatSetMask_BitOrderAndEmpty pins the formatter directly: bits
// emit in ascending order regardless of the order they were set, an
// empty mask formats as EmptySetCell (a present, empty selection —
// null is applied separately from the record bitmap and spells ""),
// and a bit past the dictionary is skipped rather than resolved or
// fatal.
//
// The empty-mask expectation was "" until the three-state convention
// landed; see EmptySetCell for why the empty string could not carry it.
func TestFormatSetMask_BitOrderAndEmpty(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, s := range []string{"alpha", "beta", "gamma"} {
		if _, err := dict.AddWithLimit(s, 256); err != nil {
			t.Fatal(err)
		}
	}

	var m encoding.SetMask
	m = m.WithBit(2).WithBit(0)
	if got := formatSetMask(m, dict); got != "alpha|gamma" {
		t.Errorf("formatSetMask = %q, want %q", got, "alpha|gamma")
	}

	if got := formatSetMask(encoding.SetMask{}, dict); got != EmptySetCell {
		t.Errorf("empty mask formatted as %q, want %q (the empty-selection marker, "+
			"not the null cell)", got, EmptySetCell)
	}

	// A bit beyond the dictionary is a corrupt or mid-remap payload.
	beyond := encoding.SetMask{}.WithBit(1).WithBit(200)
	if got := formatSetMask(beyond, dict); got != "beta" {
		t.Errorf("out-of-dictionary bit leaked: %q, want %q", got, "beta")
	}
}

// TestFormatFieldValue_NarrowSetUsesLabels pins that the narrow rungs
// reach the same formatter through the uint64 value API.
func TestFormatFieldValue_NarrowSetUsesLabels(t *testing.T) {
	dict := encoding.NewDictionary()
	for _, s := range []string{"VISA", "MC", "AMEX"} {
		if _, err := dict.AddWithLimit(s, 64); err != nil {
			t.Fatal(err)
		}
	}
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU16,
		encoding.FieldTypeSetU32, encoding.FieldTypeSetU64,
	} {
		if got := formatFieldValue(ft, 0b101, dict); got != "VISA|AMEX" {
			t.Errorf("%s: formatFieldValue(0b101) = %q, want %q", ft, got, "VISA|AMEX")
		}
		// Also corrected by the three-state convention: an all-zero
		// narrow mask is an empty SELECTION, and must not leave the
		// exporter wearing the null cell's spelling.
		if got := formatFieldValue(ft, 0, dict); got != EmptySetCell {
			t.Errorf("%s: empty mask = %q, want %q", ft, got, EmptySetCell)
		}
	}
}
