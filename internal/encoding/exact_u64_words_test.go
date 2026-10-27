package encoding

import (
	"bytes"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// exactSink records every SetExactU64At it receives.
type exactSink map[int]uint64

func (s exactSink) SetExactU64At(idx int, v uint64) { s[idx] = v }

// exactWordsFixture: x (u32), k (nullable u64), j (u64), f (f64). Rows:
// k = 2^53+1, j = MaxUint64-1; then k null, j = 2^53+1.
func exactWordsFixture(t *testing.T) (*encoding.Schema, []byte) {
	t.Helper()
	s := &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeU32, ByteOffset: 0},
		{Name: "k", Type: encoding.FieldTypeU64, ByteOffset: 4, Nullable: true},
		{Name: "j", Type: encoding.FieldTypeU64, ByteOffset: 12},
		{Name: "f", Type: encoding.FieldTypeF64, ByteOffset: 20},
	}}
	var buf bytes.Buffer
	rows := [][]uint64{{1, 1<<53 + 1, math.MaxUint64 - 1, math.Float64bits(0.5)}, {2, 0, 1<<53 + 1, math.Float64bits(1.5)}}
	for ri, row := range rows {
		for fi, f := range s.Fields {
			if err := encoding.WriteFieldValue(&buf, f.Type, row[fi]); err != nil {
				t.Fatal(err)
			}
		}
		bm := make([]byte, s.BitmapByteSize())
		if ri == 1 {
			encoding.BitmapSetNull(bm, 1)
		}
		if err := encoding.WriteBitmap(&buf, bm); err != nil {
			t.Fatal(err)
		}
	}
	return s, buf.Bytes()
}

// TestExactU64Words_MapDecoders: both map decoders (per-field and plan)
// capture the exact word of every retained non-null u64 field — never a
// projected-out or null one — and reset per record.
func TestExactU64Words_MapDecoders(t *testing.T) {
	t.Parallel()

	s, data := exactWordsFixture(t)
	want := []map[int]uint64{
		{1: 1<<53 + 1, 2: math.MaxUint64 - 1},
		{2: 1<<53 + 1}, // k null on row 2: no word
	}
	keepKJ := func(n string) bool { return n == "k" || n == "j" }
	keepK := func(n string) bool { return n == "k" }
	plan, err := BuildDecodePlan(s, []string{"k", "j"})
	if err != nil {
		t.Fatal(err)
	}
	planK, err := BuildDecodePlan(s, []string{"k"})
	if err != nil {
		t.Fatal(err)
	}
	type decode func(rr *RecordReader, v map[string]float64, n map[string]bool, w map[string]any) error
	cases := []struct {
		name  string
		read  decode
		onlyK bool
	}{
		{"full", func(rr *RecordReader, v map[string]float64, n map[string]bool, w map[string]any) error {
			return rr.ReadRecordWithWide(v, n, w)
		}, false},
		{"projected", func(rr *RecordReader, v map[string]float64, n map[string]bool, w map[string]any) error {
			return rr.ReadRecordWithWideProjected(v, n, w, keepK)
		}, true},
		{"plan", func(rr *RecordReader, v map[string]float64, n map[string]bool, w map[string]any) error {
			return rr.ReadRecordWithWidePlan(v, n, w, keepKJ, plan)
		}, false},
		{"plan_k", func(rr *RecordReader, v map[string]float64, n map[string]bool, w map[string]any) error {
			return rr.ReadRecordWithWidePlan(v, n, w, keepK, planK)
		}, true},
	}
	for _, c := range cases {
		rr := NewRecordReader(bytes.NewReader(data), s)
		words := NewExactU64Words(s)
		rr.CaptureExactU64(words)
		v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
		for r := range want {
			if err := c.read(rr, v, n, w); err != nil {
				t.Fatalf("%s row %d: %v", c.name, r, err)
			}
			got := exactSink{}
			words.ApplyTo(got)
			exp := map[int]uint64{}
			for fi, word := range want[r] {
				if !c.onlyK || fi == 1 {
					exp[fi] = word
				}
			}
			if len(got) != len(exp) {
				t.Fatalf("%s row %d: captured %v, want %v", c.name, r, got, exp)
			}
			for fi, word := range exp {
				if got[fi] != word {
					t.Errorf("%s row %d field %d: word %d, want %d", c.name, r, fi, got[fi], word)
				}
			}
		}
	}
}

// TestExactU64Words_ReadRecordAtExact: the point-read variant captures too.
func TestExactU64Words_ReadRecordAtExact(t *testing.T) {
	t.Parallel()

	s, data := exactWordsFixture(t)
	loc := &encoding.RecordLocator{Schema: s, Stride: int64(s.RecordByteSize()), TotalRecords: 2}
	words := NewExactU64Words(s)
	v, n, w := map[string]float64{}, map[string]bool{}, map[string]any{}
	if err := ReadRecordAtExact(loc, bytes.NewReader(data), 0, v, n, w, nil, nil, words); err != nil {
		t.Fatal(err)
	}
	got := exactSink{}
	words.ApplyTo(got)
	if got[1] != 1<<53+1 || got[2] != math.MaxUint64-1 || len(got) != 2 {
		t.Fatalf("captured %v, want {1: 2^53+1, 2: MaxUint64-1}", got)
	}
}

// TestNewExactU64Words_NilWithoutU64: no u64 field, no capture (and a
// nil capture's ApplyTo is a no-op).
func TestNewExactU64Words_NilWithoutU64(t *testing.T) {
	t.Parallel()

	s := &encoding.Schema{Fields: []encoding.Field{{Name: "x", Type: encoding.FieldTypeU32}}}
	if w := NewExactU64Words(s); w != nil {
		t.Fatalf("NewExactU64Words = %v, want nil", w)
	}
	var w *ExactU64Words
	w.ApplyTo(exactSink{})
}
