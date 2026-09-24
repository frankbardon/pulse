package io

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
)

// widePipeCells builds `rows` pipe-delimited cells drawn from a
// `tokens`-sized alphabet (T0..T{tokens-1}), rotating two tokens per
// cell. Every token appears at least once as long as rows >= tokens,
// and the cells themselves repeat long before that, so the categorical
// "all cells unique" unbounded guard never fires.
func widePipeCells(tokens, rows int) []string {
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		out = append(out, "T"+itoa((2*i)%tokens)+"|"+"T"+itoa((2*i+1)%tokens))
	}
	return out
}

// wideSetReader wraps widePipeCells into a two-column reader whose
// "issuers" column carries the delimited cells. `rows` is kept below
// defaultSampleRows so the whole vocabulary lands inside the sample.
func wideSetReader(tokens, rows int) *mockReader {
	cells := widePipeCells(tokens, rows)
	categories := []string{"A", "B", "C", "D"}
	out := make([][]string, 0, len(cells))
	for i, c := range cells {
		out = append(out, []string{categories[i%len(categories)], c})
	}
	return newMockReader([]string{"id", "issuers"}, out)
}

// countingReader records how many times the inference pass walked the
// source. The ladder must reach its widest rung inside ONE pass — a
// restart would mean re-reading the source, which several adapters
// (stdin-backed NDJSON, a streamed upload) cannot do at all.
type countingReader struct {
	*mockReader
	headerCalls int
	rowCalls    int
}

func (c *countingReader) ReadHeader() ([]string, error) {
	c.headerCalls++
	return c.mockReader.ReadHeader()
}

func (c *countingReader) ReadRows(ctx context.Context, fn func(row []string) error) error {
	c.rowCalls++
	return c.mockReader.ReadRows(ctx, fn)
}

// TestSetWidth_LadderBoundaries pins every rung boundary of the
// smallest-fitting-width ladder, including the two wide rungs. The
// ladder is the only thing inference is allowed to produce; a
// mis-coded comparison (>= instead of >) silently costs a record
// either capacity or bytes on every row of every import.
func TestSetWidth_LadderBoundaries(t *testing.T) {
	cases := []struct {
		unique int
		want   encoding.FieldType
	}{
		{1, encoding.FieldTypeSetU8},
		{8, encoding.FieldTypeSetU8},
		{9, encoding.FieldTypeSetU16},
		{16, encoding.FieldTypeSetU16},
		{17, encoding.FieldTypeSetU32},
		{32, encoding.FieldTypeSetU32},
		{33, encoding.FieldTypeSetU64},
		{64, encoding.FieldTypeSetU64},
		{65, encoding.FieldTypeSetU128},
		{128, encoding.FieldTypeSetU128},
		{129, encoding.FieldTypeSetU256},
		{256, encoding.FieldTypeSetU256},
	}
	for _, c := range cases {
		got := setWidth(c.unique)
		if got != c.want {
			t.Errorf("setWidth(%d) = %s, want %s", c.unique, got, c.want)
		}
		if int(got.MaxSetEntries()) < c.unique {
			t.Errorf("setWidth(%d) = %s holds only %d entries",
				c.unique, got, got.MaxSetEntries())
		}
	}
	// Above the ceiling setWidth must not hand back a set type at
	// all — the caller gate is expected to have filtered it, and a
	// set rung here would be one that cannot hold the dictionary.
	if got := setWidth(257); got.IsSet() {
		t.Errorf("setWidth(257) = %s, want a non-set fallback", got)
	}
}

// TestProbeSetClassification_LadderBoundaries pins the probe's own
// cardinality gate at the moved ceiling: 256 tokens classify, 257 do
// not.
func TestProbeSetClassification_LadderBoundaries(t *testing.T) {
	cases := []struct {
		tokens  int
		want    encoding.FieldType
		wantOK  bool
		comment string
	}{
		{64, encoding.FieldTypeSetU64, true, "last narrow rung"},
		{65, encoding.FieldTypeSetU128, true, "first wide rung"},
		{128, encoding.FieldTypeSetU128, true, "set_u128 exactly full"},
		{129, encoding.FieldTypeSetU256, true, "widest rung"},
		{256, encoding.FieldTypeSetU256, true, "set_u256 exactly full"},
		{257, 0, false, "past the ceiling — falls back"},
	}
	for _, c := range cases {
		values := widePipeCells(c.tokens, 480)
		got, delim, ok := probeSetClassification(values, defaultSetInferenceMinPct)
		if ok != c.wantOK {
			t.Errorf("probeSetClassification(%d tokens) ok = %v, want %v (%s)",
				c.tokens, ok, c.wantOK, c.comment)
			continue
		}
		if !c.wantOK {
			continue
		}
		if got != c.want {
			t.Errorf("probeSetClassification(%d tokens) = %s, want %s (%s)",
				c.tokens, got, c.want, c.comment)
		}
		if delim != "|" {
			t.Errorf("probeSetClassification(%d tokens) delim = %q, want |", c.tokens, delim)
		}
	}
}

// TestInfer_206TokensInfersSetU256 is the story's named case: a
// dictionary that outgrows set_u128 lands on set_u256 and wastes the
// difference, rather than being demoted to a categorical (which would
// silently collapse each multi-select cell into one opaque string).
func TestInfer_206TokensInfersSetU256(t *testing.T) {
	r := wideSetReader(206, 480)
	res, err := InferSchemaWithOptions(r, InferOptions{})
	if err != nil {
		t.Fatalf("InferSchemaWithOptions: %v", err)
	}
	got := res.Schema.Field("issuers").Type
	if got != encoding.FieldTypeSetU256 {
		t.Errorf("206-token column type = %s, want set_u256", got)
	}
	if d := res.Delimiters["issuers"]; d != "|" {
		t.Errorf("delimiter = %q, want |", d)
	}
}

// TestInfer_SetLadderWidensWithinOnePass covers the mid-pass widening
// criterion: as the observed vocabulary grows the chosen rung climbs
// set_u64 -> set_u128 -> set_u256, and the decision is reached inside
// a SINGLE walk of the source (one ReadHeader, one ReadRows). A
// restart-to-widen design would break every non-rewindable source.
func TestInfer_SetLadderWidensWithinOnePass(t *testing.T) {
	cases := []struct {
		tokens int
		want   encoding.FieldType
	}{
		{64, encoding.FieldTypeSetU64},
		{100, encoding.FieldTypeSetU128},
		{206, encoding.FieldTypeSetU256},
	}
	for _, c := range cases {
		cr := &countingReader{mockReader: wideSetReader(c.tokens, 480)}
		res, err := InferSchemaWithOptions(cr, InferOptions{})
		if err != nil {
			t.Fatalf("InferSchemaWithOptions(%d tokens): %v", c.tokens, err)
		}
		if got := res.Schema.Field("issuers").Type; got != c.want {
			t.Errorf("%d-token column type = %s, want %s", c.tokens, got, c.want)
		}
		if cr.rowCalls != 1 || cr.headerCalls != 1 {
			t.Errorf("%d tokens: %d ReadHeader / %d ReadRows calls, want 1 / 1 — "+
				"widening must not restart the pass",
				c.tokens, cr.headerCalls, cr.rowCalls)
		}
	}
}

// TestInfer_SetAbove256FallsBackToCategorical pins the moved ceiling
// from the other side. 256 is hard: above it the column is not a set.
func TestInfer_SetAbove256FallsBackToCategorical(t *testing.T) {
	r := wideSetReader(258, 480)
	res, err := InferSchemaWithOptions(r, InferOptions{})
	if err != nil {
		t.Fatalf("InferSchemaWithOptions: %v", err)
	}
	got := res.Schema.Field("issuers").Type
	if got.IsSet() {
		t.Errorf("258-token column classified as %s; want categorical_* fallback", got)
	}
	// A declined set must not leave a delimiter behind: the delimiter
	// map is what the row pass consults to split cells, so an entry
	// here for a categorical column is a silent split of data the
	// schema says is one opaque value.
	if d, ok := res.Delimiters["issuers"]; ok {
		t.Errorf("non-set column recorded set delimiter %q", d)
	}
}

// TestSetOverflowFixup_NamesTheMovedCeiling asserts the coded error's
// prose tracks the ladder. A fixup that still says "64" or "wait for
// set_u128" would send the reader to denormalize a column that now
// imports natively.
func TestSetOverflowFixup_NamesTheMovedCeiling(t *testing.T) {
	md, ok := errors.MetadataFor(errors.PULSE_IMPORT_SET_OVERFLOW)
	if !ok {
		t.Fatal("no codeMetadata for PULSE_IMPORT_SET_OVERFLOW")
	}
	if len(md.Fixups) == 0 {
		t.Fatal("PULSE_IMPORT_SET_OVERFLOW must keep at least one fixup")
	}
	blob := md.Message
	for _, f := range md.Fixups {
		blob += " " + f.Hint
	}
	if !strings.Contains(blob, "set_u256") || !strings.Contains(blob, "256") {
		t.Errorf("overflow prose must name the set_u256 / 256 ceiling, got %q", blob)
	}
	if strings.Contains(blob, "set_u64 holds at most 64") {
		t.Errorf("overflow prose still names the old 64-entry ceiling: %q", blob)
	}
	if strings.Contains(blob, "wait for set_u128") {
		t.Errorf("overflow prose still tells the reader to wait for set_u128: %q", blob)
	}
}

// TestInfer_SetInferenceMinPctUnchangedAtWideWidths confirms the
// threshold knob is orthogonal to the width choice: a wide vocabulary
// below the threshold is still not a set, and lowering the threshold
// still yields the wide rung.
func TestInfer_SetInferenceMinPctUnchangedAtWideWidths(t *testing.T) {
	// 480 rows; only 48 (10%) carry the delimiter, the rest are
	// single tokens from the same 206-token alphabet.
	build := func() *mockReader {
		rows := make([][]string, 0, 480)
		categories := []string{"A", "B", "C", "D"}
		for i := 0; i < 480; i++ {
			cell := "T" + itoa((2*i)%206)
			if i%10 == 0 {
				cell += "|T" + itoa((2*i+1)%206)
			}
			rows = append(rows, []string{categories[i%len(categories)], cell})
		}
		return newMockReader([]string{"id", "issuers"}, rows)
	}

	res, err := InferSchemaWithOptions(build(), InferOptions{})
	if err != nil {
		t.Fatalf("InferSchemaWithOptions (default pct): %v", err)
	}
	if got := res.Schema.Field("issuers").Type; got.IsSet() {
		t.Errorf("10%% delimited cells classified as %s under the default 30%% threshold", got)
	}

	res, err = InferSchemaWithOptions(build(), InferOptions{SetInferenceMinPct: 5})
	if err != nil {
		t.Fatalf("InferSchemaWithOptions (pct=5): %v", err)
	}
	if got := res.Schema.Field("issuers").Type; !got.IsSet() {
		t.Errorf("10%% delimited cells under a 5%% threshold = %s, want a set rung", got)
	}
}

// TestSetDelimiterProbing_UnchangedAtWideWidths pins pickSetDelimiter
// priority and splitSetTokens trimming against a wide vocabulary —
// both are width-agnostic and must stay so.
func TestSetDelimiterProbing_UnchangedAtWideWidths(t *testing.T) {
	semi := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		semi = append(semi, "T"+itoa((2*i)%206)+"; T"+itoa((2*i+1)%206))
	}
	if d := pickSetDelimiter(semi); d != ";" {
		t.Errorf("pickSetDelimiter = %q, want ;", d)
	}
	toks := splitSetTokens(" T205 ;  T17 ; ", ";")
	if len(toks) != 2 || toks[0] != "T205" || toks[1] != "T17" {
		t.Errorf("splitSetTokens = %#v, want [T205 T17]", toks)
	}

	got, delim, ok := probeSetClassification(semi, defaultSetInferenceMinPct)
	if !ok || delim != ";" || got != encoding.FieldTypeSetU256 {
		t.Errorf("probeSetClassification(semicolon, 206 tokens) = (%s, %q, %v), want (set_u256, \";\", true)",
			got, delim, ok)
	}
}

// TestInfer_ColumnTypeOverrideWideSetRungs pins the force-type escape
// hatch at the new rungs — the path a caller uses when the sample
// under-counts the vocabulary.
func TestInfer_ColumnTypeOverrideWideSetRungs(t *testing.T) {
	for _, want := range []encoding.FieldType{
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		r := wideSetReader(12, 200)
		res, err := InferSchemaWithOptions(r, InferOptions{
			ColumnTypeOverrides: map[string]encoding.FieldType{"issuers": want},
		})
		if err != nil {
			t.Fatalf("InferSchemaWithOptions(%s): %v", want, err)
		}
		if got := res.Schema.Field("issuers").Type; got != want {
			t.Errorf("override %s produced %s", want, got)
		}
		if d := res.Delimiters["issuers"]; d != "|" {
			t.Errorf("override %s delimiter = %q, want |", want, d)
		}
	}
}
