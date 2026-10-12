package processing

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// setSimSchema: a and b are set_u256 over T0..T255 (one shared
// dictionary layout), c is a set_u8 whose dictionary orders the same
// labels differently, and ma / mb / mw are dictionary-less sets (raw
// masks: two narrow, one wide).
func setSimSchema() *encoding.Schema {
	c := encoding.NewDictionary()
	for _, l := range []string{"T255", "X", "T0"} {
		_, _ = c.Add(l)
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeSetU256, Nullable: true, Dictionary: exprTestDict("T", 256)},
		{Name: "b", Type: encoding.FieldTypeSetU256, Nullable: true, Dictionary: exprTestDict("T", 256)},
		{Name: "c", Type: encoding.FieldTypeSetU8, Nullable: true, Dictionary: c},
		{Name: "ma", Type: encoding.FieldTypeSetU64, Nullable: true},
		{Name: "mb", Type: encoding.FieldTypeSetU64, Nullable: true},
		{Name: "mw", Type: encoding.FieldTypeSetU256, Nullable: true},
	}}
}

// setSimRow is one encoded row; a nil entry is a null field.
type setSimRow map[string]*encoding.SetMask

func bitsMask(bits ...int) *encoding.SetMask {
	m := maskWithBits(bits...)
	return &m
}

func fullMask(n int) *encoding.SetMask {
	var m encoding.SetMask
	for i := range n {
		m = m.WithBit(i)
	}
	return &m
}

func encodeSetSimRows(t *testing.T, s *encoding.Schema, rows []setSimRow) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, row := range rows {
		bm := make([]byte, s.BitmapByteSize())
		for i, f := range s.Fields {
			m := row[f.Name]
			if m == nil {
				encoding.BitmapSetNull(bm, i)
				m = &encoding.SetMask{}
			}
			var err error
			if f.Type.IsWideSet() {
				err = encoding.WriteSetMask(&buf, f.Type, *m)
			} else {
				w, _ := m.Uint64()
				err = encoding.WriteFieldValue(&buf, f.Type, w)
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		buf.Write(bm)
	}
	return buf.Bytes()
}

// TestSetSimilarity_DecodePaths runs every set-similarity built-in over
// real decoded rows through each record-producing path: the set_u256
// 256-member boundary, cross-dictionary label matching, mask-only
// pairs, empty sets and null sets.
func TestSetSimilarity_DecodePaths(t *testing.T) {
	s := setSimSchema()
	nan := math.NaN()
	rows := []setSimRow{
		// Row 0: a full (256 members), b only T255, c only T255 (its
		// dictionary index 0), ma {1,2}, mb {0,1}, mw {255}.
		{"a": fullMask(256), "b": bitsMask(255), "c": bitsMask(0), "ma": bitsMask(1, 2), "mb": bitsMask(0, 1), "mw": bitsMask(255)},
		// Row 1: everything an empty set (not null).
		{"a": bitsMask(), "b": bitsMask(), "c": bitsMask(), "ma": bitsMask(), "mb": bitsMask(), "mw": bitsMask()},
		// Row 2: a, c, ma null; b holds T0 and T255; mb holds bit 3.
		{"b": bitsMask(0, 255), "mb": bitsMask(3), "mw": bitsMask(3)},
	}
	raw := encodeSetSimRows(t, s, rows)
	cases := []struct {
		src  string
		want [3]float64
	}{
		{`jaccard(a, b)`, [3]float64{1.0 / 256, nan, nan}},
		{`dice(a, b)`, [3]float64{2.0 / 257, nan, nan}},
		{`hamming(a, b)`, [3]float64{255, 0, nan}},
		{`overlap(a, b)`, [3]float64{1, nan, nan}},
		{`jaccard(a, a)`, [3]float64{1, nan, nan}},
		{`hamming(b, b)`, [3]float64{0, 0, 0}},
		// Cross-dictionary: c's T255 is index 0, b's is index 255.
		{`jaccard(b, c)`, [3]float64{1, nan, nan}},
		{`hamming(c, b)`, [3]float64{0, 0, nan}},
		// Literal label lists against a set field.
		{`jaccard(b, ["T255", "T0"])`, [3]float64{0.5, 0, 1}},
		{`overlap(["T0"], b)`, [3]float64{0, nan, 1}},
		{`hamming(a, ["T0", "T255", "zzz"])`, [3]float64{255, 3, nan}},
		// Dictionary-less pairs compare by mask bit.
		{`jaccard(ma, mb)`, [3]float64{1.0 / 3, nan, nan}},
		{`hamming(ma, mb)`, [3]float64{2, 0, nan}},
		{`dice(mb, mw)`, [3]float64{0, nan, 1}},
		{`overlap(mw, mw)`, [3]float64{1, nan, 1}},
	}
	for _, tc := range cases {
		prog, err := newExprProgram(tc.src, "formula", s, nil, nil, setExprOptions())
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.src, err)
		}
		row := 0
		lastPath := ""
		exprDecodePaths(t, s, raw, func(path string, r *Record) {
			if path != lastPath {
				row, lastPath = 0, path
			}
			defer func() { row++ }()
			out, _, err := prog.runRecord(r)
			if err != nil {
				t.Errorf("%s %s row %d: %v", path, tc.src, row, err)
				return
			}
			got := toFloat(out)
			if want := tc.want[row]; !(closeTo(got, want) || math.IsNaN(got) && math.IsNaN(want)) {
				t.Errorf("%s %s row %d = %v, want %v", path, tc.src, row, out, want)
			}
		})
	}
}

// TestSetSimilarity_MixedMaskAndLabelsRefused: a dictionary-less mask
// has no labels, so pairing it with a label set is a coded refusal,
// never a silent bit-vs-label comparison.
func TestSetSimilarity_MixedMaskAndLabelsRefused(t *testing.T) {
	s := setSimSchema()
	raw := encodeSetSimRows(t, s, []setSimRow{
		{"a": bitsMask(1), "b": bitsMask(1), "c": bitsMask(0), "ma": bitsMask(1), "mb": bitsMask(1), "mw": bitsMask(1)},
	})
	for _, src := range []string{`jaccard(a, ma)`, `dice(mw, b)`, `hamming(ma, ["T1"])`, `overlap([], mw)`} {
		prog, err := newExprProgram(src, "formula", s, nil, nil, setExprOptions())
		if err != nil {
			t.Fatalf("%s: compile: %v", src, err)
		}
		exprDecodePaths(t, s, raw, func(path string, r *Record) {
			if _, _, err := prog.runRecord(r); !perr.HasCode(err, perr.PROCESSING_RUNTIME) {
				t.Errorf("%s %s: err %v, want PROCESSING_RUNTIME", path, src, err)
			}
		})
	}
}

// TestSetSimilarity_Impls pins the closures directly: nil (null) → NaN
// for all four, hamming an int, argument refusals coded.
func TestSetSimilarity_Impls(t *testing.T) {
	for _, fn := range []string{"jaccard", "dice", "hamming", "overlap"} {
		impl := exprBuiltinImpls[fn]
		for _, args := range [][]any{{nil, []string{"a"}}, {[]string{}, nil}, {nil, nil}} {
			got, err := impl(args...)
			if err != nil || !math.IsNaN(toFloat(got)) {
				t.Errorf("%s(%v) = %v, %v; want NaN (null set)", fn, args, got, err)
			}
		}
		for _, args := range [][]any{{[]string{"a"}, 3.0}, {[]any{"a", 1}, []string{}}, {"a", []string{"a"}}} {
			if _, err := impl(args...); !perr.HasCode(err, perr.PROCESSING_RUNTIME) {
				t.Errorf("%s(%v): err %v, want PROCESSING_RUNTIME", fn, args, err)
			}
		}
		if _, err := impl([]string{"a"}); !perr.HasCode(err, perr.PROCESSING_RUNTIME) {
			t.Errorf("%s with one argument: err %v, want arity refusal", fn, err)
		}
	}
	if got, _ := exprBuiltinImpls["hamming"]([]string{"a", "b"}, []any{"b", "b", "c"}); got != 2 {
		t.Errorf("hamming = %#v, want int 2", got)
	}
	if got, _ := exprBuiltinImpls["jaccard"](uint64(0b11), encoding.SetMaskFromUint64(0b110)); !closeTo(got.(float64), 1.0/3) {
		t.Errorf("jaccard over narrow + wide masks = %v, want 1/3", got)
	}
}

// TestSetSimilarity_NullSetIsNaN: through a whole run, a null set makes
// the formula value NaN (undefined, null on the wire) — the same as
// every undefined vector figure — while an empty set gives a number.
func TestSetSimilarity_NullSetIsNaN(t *testing.T) {
	s := setSimSchema()
	recs := []*Record{
		NewRecordWithWide(s, map[string]float64{}, nil, map[string]any{"a": *bitsMask(1), "b": *bitsMask(1, 2)}),
		NewRecordWithWide(s, map[string]float64{}, map[string]bool{"a": true}, map[string]any{"b": *bitsMask(1)}),
		NewRecordWithWide(s, map[string]float64{}, nil, map[string]any{"a": *bitsMask(), "b": *bitsMask(1)}),
	}
	proc := NewProcessorWithExtensions(s, &ExtensionRegistry{})
	resp, err := proc.Process(t.Context(), &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "b", Expression: `jaccard(a, b)`, Label: "j"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "j", Label: "s"}},
	}, NewSliceIterator(recs[:1]))
	if err != nil {
		t.Fatal(err)
	}
	if s := toFloat(resp.Data[0]["s"]); !closeTo(s, 0.5) {
		t.Errorf("sum over a full row = %v, want 0.5", s)
	}
	resp, err = proc.Process(t.Context(), &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "b", Expression: `jaccard(a, b)`, Label: "j"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "j", Label: "s"}},
	}, NewSliceIterator(recs[1:2]))
	if err != nil {
		t.Fatal(err)
	}
	if s := toFloat(resp.Data[0]["s"]); !math.IsNaN(s) {
		t.Errorf("null set: sum = %v, want NaN", s)
	}
	resp, err = proc.Process(t.Context(), &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_FORMULA, Field: "b", Expression: `jaccard(a, b)`, Label: "j"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "j", Label: "s"}},
	}, NewSliceIterator(recs[2:]))
	if err != nil {
		t.Fatal(err)
	}
	if s := toFloat(resp.Data[0]["s"]); s != 0 {
		t.Errorf("empty vs non-empty set: sum = %v, want 0 (an empty set is not null)", s)
	}
}
