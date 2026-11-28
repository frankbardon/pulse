package io

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perrors "github.com/frankbardon/pulse/errors"
	"github.com/spf13/afero"
)

// setWidenSample is the inference sample every set-widen test imports
// under: the fixture's later rows outgrow the rungs it infers.
const setWidenSample = 50

func optSetSample(j *ImportJob) { j.SampleRows = setWidenSample }

// setWidenFixture is a source whose 50-row sample under-sizes two set
// columns, around fields that never widen (so re-striding moves a
// prefix and a suffix):
//
//	tags   pipe-delimited; the sample holds 5 tokens (→ set_u8, nullable:
//	       row 10 is null). From row 51 every row brings one new token,
//	       `tokens` in all, so the column crosses set_u16 (9th token,
//	       row 54), set_u32 (17th, row 62), set_u64 (33rd, row 78),
//	       set_u128 (65th, row 110 — the narrow-to-wide step) and, with
//	       tokens > 128, set_u256 (129th, row 174). A null cell past the
//	       sample (row 200) rides the bitmap at the widened rung.
//	tag2   semicolon-delimited; 3 tokens in the sample (→ set_u8), 12 over
//	       the file (→ set_u16 at row 106, the 9th token).
func setWidenFixture(tokens int) ([]string, [][]string) {
	cols := []string{"id", "tags", "score", "tag2", "name"}
	var rows [][]string
	next := 5 // the next token no row has carried yet
	for i := 0; i < 300; i++ {
		row := i + 1
		var tags string
		switch {
		case row == 10 || row == 200:
			tags = ""
		case row <= setWidenSample:
			tags = fmt.Sprintf("t%03d|t%03d", i%5, (i+1)%5)
		case next < tokens:
			tags = fmt.Sprintf("t%03d|t%03d", next, i%5)
			next++
		default:
			tags = fmt.Sprintf("t%03d|t%03d", i%tokens, (i+7)%tokens)
		}
		tag2 := fmt.Sprintf("a%02d;a%02d", i%3, (i+1)%3)
		if row > 100 && row <= 109 {
			tag2 = fmt.Sprintf("a%02d;a00", row-98) // a03 .. a11
		}
		rows = append(rows, []string{
			fmt.Sprint(i % 40),
			tags,
			fmt.Sprintf("%d.25", i%9),
			tag2,
			fmt.Sprintf("n%d", i%4),
		})
	}
	return cols, rows
}

// setColumn decodes one set column (narrow or wide rung) of a cohort by
// walking the record region by hand — the field's offset and width from
// the schema, the null bit from the trailing bitmap — so the test leans
// on the byte layout, not on a decoder.
func setColumn(t *testing.T, fs afero.Fs, path, field string) (*encoding.Schema, []encoding.SetMask, []bool) {
	t.Helper()
	blob, err := afero.ReadFile(fs, path)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(blob)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	payload := blob[len(blob)-r.Len():]
	stride := schema.RecordByteSize()
	fi, off := -1, 0
	for i := range schema.Fields {
		if schema.Fields[i].Name == field {
			fi = i
			break
		}
		off += schema.FieldWidth(i)
	}
	if fi < 0 {
		t.Fatalf("no field %q", field)
	}
	ft, w := schema.Fields[fi].Type, schema.FieldWidth(fi)
	bmOff := stride - schema.BitmapByteSize()
	var masks []encoding.SetMask
	var nulls []bool
	for k := 0; k+stride <= len(payload); k += stride {
		cell := payload[k+off : k+off+w]
		var m encoding.SetMask
		if ft.IsWideSet() {
			if m, err = encoding.SetMaskFromBytes(ft, cell); err != nil {
				t.Fatal(err)
			}
		} else {
			var word [8]byte
			copy(word[:], cell)
			m = encoding.SetMaskFromUint64(binary.LittleEndian.Uint64(word[:]))
		}
		masks = append(masks, m)
		nulls = append(nulls, schema.BitmapByteSize() > 0 && encoding.BitmapIsNull(payload[k+bmOff:k+stride], fi))
	}
	return schema, masks, nulls
}

// TestImportWiden_SetRungsPromote: a set column whose tokens outgrow
// the rung its sample inferred climbs the set ladder — set_u8 to
// set_u256 across the uint64/wide boundary — and every row imports, with
// one PULSE_IMPORT_WIDTH_PROMOTED per field naming the inferred rung,
// the written rung and the first row that forced a step. A
// non-default token delimiter promotes the same way.
func TestImportWiden_SetRungsPromote(t *testing.T) {
	t.Parallel()

	cols, rows := setWidenFixture(200)
	rep, _ := importRaw(t, cols, rows, optSetSample)
	if rep.RowsImported != len(rows) || len(rep.RowErrors) != 0 {
		t.Fatalf("imported %d rows with %d row errors, want %d / 0 (first: %v)", rep.RowsImported, len(rep.RowErrors), len(rows), rep.RowErrors)
	}
	want := map[string][3]any{
		"tags": {"set_u8", "set_u256", 54},
		"tag2": {"set_u8", "set_u16", 106},
	}
	if len(rep.WidthWarnings) != len(want) {
		t.Fatalf("width warnings = %v, want one per field in %v", rep.WidthWarnings, want)
	}
	for field, w := range want {
		got := warningFor(t, rep.WidthWarnings, field)
		if got.Details["from"] != w[0] || got.Details["to"] != w[1] || got.Details["source_row"] != w[2] {
			t.Errorf("%s: details %v, want from %v to %v source_row %v", field, got.Details, w[0], w[1], w[2])
		}
		if f := fieldByName(rep.Schema, field); f.Type.String() != w[1] {
			t.Errorf("%s: written type %s, want %v", field, f.Type, w[1])
		}
	}
	if n := fieldByName(rep.Schema, "tags").Dictionary.Count(); n != 200 {
		t.Errorf("tags dictionary holds %d tokens, want 200", n)
	}
}

// TestImportWiden_SetByteIdenticalToFullSample: the promoted cohort is
// byte-for-byte the one an import whose sample saw every row writes —
// the token -> bit assignment is first-seen at every rung and the narrow
// rows already held zero-extend into the wide rung. Each stop on the
// ladder is a case.
func TestImportWiden_SetByteIdenticalToFullSample(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		tokens int
		want   encoding.FieldType
	}{
		{12, encoding.FieldTypeSetU16},
		{30, encoding.FieldTypeSetU32},
		{60, encoding.FieldTypeSetU64},
		{100, encoding.FieldTypeSetU128},
		{200, encoding.FieldTypeSetU256},
	} {
		cols, rows := setWidenFixture(c.tokens)
		rep, promoted := importRaw(t, cols, rows, optSetSample)
		if got := fieldByName(rep.Schema, "tags").Type; got != c.want {
			t.Fatalf("%d tokens: tags = %s, want %s", c.tokens, got, c.want)
		}
		full, fullRaw := importRaw(t, cols, rows, func(j *ImportJob) { j.SampleRows = len(rows) })
		if len(full.WidthWarnings) != 0 {
			t.Fatalf("%d tokens: full-sample import widened: %v", c.tokens, full.WidthWarnings)
		}
		if !bytes.Equal(promoted, fullRaw) {
			t.Fatalf("%d tokens: promoted import (%d bytes) differs from the full-sample import (%d bytes)", c.tokens, len(promoted), len(fullRaw))
		}
	}
}

// TestImportWiden_SetValuesRoundTrip: every row decodes to the tokens
// the source wrote, on both sides of each promotion, and the two null
// cells (one inside the sample, one past it) stay null.
func TestImportWiden_SetValuesRoundTrip(t *testing.T) {
	t.Parallel()

	cols, rows := setWidenFixture(200)
	if _, _, fs, err := runGroupImport(t, newMockReader(cols, rows), nil, optSetSample); err != nil {
		t.Fatal(err)
	} else {
		for col, field := range map[int]string{1: "tags", 3: "tag2"} {
			delim := map[string]string{"tags": "|", "tag2": ";"}[field]
			schema, masks, nulls := setColumn(t, fs, "out.pulse", field)
			if len(masks) != len(rows) {
				t.Fatalf("%s: decoded %d records, want %d", field, len(masks), len(rows))
			}
			dict := fieldByName(schema, field).Dictionary
			for k, src := range rows {
				if src[col] == "" {
					if !nulls[k] {
						t.Fatalf("%s record %d: source null decodes non-null", field, k)
					}
					continue
				}
				if nulls[k] {
					t.Fatalf("%s record %d: decodes null, source %q", field, k, src[col])
				}
				var got []string
				for b := range masks[k].Bits() {
					got = append(got, dict.Resolve(uint32(b)))
				}
				toks := strings.Split(src[col], delim)
				if len(got) != len(toks) {
					t.Fatalf("%s record %d = %v, want %q", field, k, got, src[col])
				}
				for _, tok := range toks {
					if id, ok := dict.IDFor(tok); !ok || !masks[k].Has(int(id)) {
						t.Fatalf("%s record %d = %v, want %q", field, k, got, src[col])
					}
				}
			}
		}
	}
}

// TestImportWiden_SetPastU256StaysRowError: a set_u256 has nowhere to
// go — a token past its 256 bits is still the coded row error
// (PULSE_IMPORT_ROW_ERROR, reason PULSE_IMPORT_SET_OVERFLOW) and only
// those rows drop.
func TestImportWiden_SetPastU256StaysRowError(t *testing.T) {
	t.Parallel()

	// Rows 51..250 bring 199 new tokens (204 in all); from row 251 each
	// row brings two more, so rows 251..276 fill set_u256's 256 bits and
	// rows 277..300 each carry a token past them.
	cols, rows := setWidenFixture(300)
	for k := 250; k < 300; k++ {
		rows[k][1] = fmt.Sprintf("x%03d|x%03d", 2*(k-250), 2*(k-250)+1)
	}
	rep, _ := importRaw(t, cols, rows, optSetSample)
	if got := fieldByName(rep.Schema, "tags").Type; got != encoding.FieldTypeSetU256 {
		t.Fatalf("tags = %s, want set_u256", got)
	}
	if fieldByName(rep.Schema, "tags").Dictionary.Count() != 256 {
		t.Fatalf("dictionary holds %d tokens, want 256", fieldByName(rep.Schema, "tags").Dictionary.Count())
	}
	if len(rep.RowErrors) == 0 {
		t.Fatal("no row errors; a token past set_u256 must drop its row")
	}
	for _, re := range rep.RowErrors {
		ce, ok := re.Err.(*perrors.CodedError)
		if !ok || ce.Code != perrors.PULSE_IMPORT_ROW_ERROR {
			t.Fatalf("row %d: %v, want PULSE_IMPORT_ROW_ERROR", re.Row, re.Err)
		}
		if reason, _ := ce.Details["reason"].(string); !strings.Contains(reason, string(perrors.PULSE_IMPORT_SET_OVERFLOW)) || !strings.Contains(reason, "set_u256") {
			t.Errorf("row %d reason %q, want the set_u256 PULSE_IMPORT_SET_OVERFLOW", re.Row, ce.Details["reason"])
		}
	}
	if len(rep.RowErrors) != 24 || rep.RowErrors[0].Row != 277 || rep.RowsImported != len(rows)-24 {
		t.Errorf("imported %d with %d row errors (first at row %d), want %d / 24 from row 277", rep.RowsImported, len(rep.RowErrors), rep.RowErrors[0].Row, len(rows)-24)
	}
}

// TestImportWiden_DeclaredSetNeverPromotes: a set rung the caller fixed
// is a contract — a ColumnTypeOverrides set_u8 refuses the import at the
// first cell past it, and an explicit schema's set_u8 drops the row.
func TestImportWiden_DeclaredSetNeverPromotes(t *testing.T) {
	t.Parallel()

	cols, rows := setWidenFixture(200)
	_, _, _, err := runGroupImport(t, newMockReader(cols, rows), nil, optSetSample, func(j *ImportJob) {
		j.ColumnTypeOverrides = map[string]encoding.FieldType{"tags": encoding.FieldTypeSetU8}
	})
	if d := requireOverrideRefusal(t, err); d["column"] != "tags" || d["row"] != 54 {
		t.Errorf("override refusal details %v, want column tags, row 54", d)
	}

	inferred, _, err := InferSchema(newMockReader(cols, rows), setWidenSample)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := importRaw(t, cols, rows, func(j *ImportJob) {
		j.Schema = inferred
		j.SetDelimiters = map[string]string{"tag2": ";"}
	})
	if len(rep.WidthWarnings) != 0 || fieldByName(rep.Schema, "tags").Type != encoding.FieldTypeSetU8 {
		t.Fatalf("explicit schema widened: %s, %v", fieldByName(rep.Schema, "tags").Type, rep.WidthWarnings)
	}
	if len(rep.RowErrors) == 0 || rep.RowErrors[0].Row != 54 {
		t.Fatalf("explicit set_u8: row errors %v, want the first at row 54", rep.RowErrors)
	}
}

// TestImportPredictWiden_Set: the measured import predict reports the
// rungs the import writes, with the same warnings (predict ≡ runtime).
func TestImportPredictWiden_Set(t *testing.T) {
	t.Parallel()

	cols, rows := setWidenFixture(200)
	imp, _ := importRaw(t, cols, rows, optSetSample)
	rep, err := predictJob(t, newMockReader(cols, rows), optSetSample, func(j *ImportJob) { j.ElideConstants = true })
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range imp.Schema.Fields {
		if got := fieldByName(rep.Schema, f.Name).Type; got != f.Type {
			t.Errorf("%s: predicted %s, import wrote %s", f.Name, got, f.Type)
		}
	}
	sameWidthWarnings(t, rep.WidthWarnings, imp.WidthWarnings)
}

// TestConvertWiden_Set: convert promotes a set rung by the import's
// rule — the same final types, offsets and warnings, the source text
// passed through — and its kept cohort is the import's, the inferred
// semicolon delimiter included.
func TestConvertWiden_Set(t *testing.T) {
	t.Parallel()

	cols, rows := setWidenFixture(200)
	imp, want := importRaw(t, cols, rows, optSetSample)
	rep, target, kept := runConvert(t, newMockReader(cols, rows), true, func(j *ConvertJob) { j.SampleRows = setWidenSample })
	for _, f := range imp.Schema.Fields {
		if got := fieldByName(rep.Schema, f.Name); got.Type != f.Type || got.ByteOffset != f.ByteOffset {
			t.Errorf("%s: convert %s@%d, import %s@%d", f.Name, got.Type, got.ByteOffset, f.Type, f.ByteOffset)
		}
	}
	sameWidthWarnings(t, rep.WidthWarnings, imp.WidthWarnings)
	targetRowsAreSource(t, target, rows)
	if !bytes.Equal(kept, want) {
		t.Fatalf("kept cohort (%d bytes) differs from the import (%d bytes)", len(kept), len(want))
	}
}
