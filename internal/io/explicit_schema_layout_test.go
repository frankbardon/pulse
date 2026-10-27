package io

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	encx "github.com/frankbardon/pulse/internal/encoding"
	"github.com/spf13/afero"
)

// layoutHostileSchema carries caller-supplied ByteOffset / BitPosition
// values that disagree with the canonical layout on every field: a
// bit-packed field at a nonzero bit, offsets out of order and past the
// stride.
func layoutHostileSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "flag", Type: encoding.FieldTypePackedBool, ByteOffset: 7, BitPosition: 3},
		{Name: "level", Type: encoding.FieldTypeU4, ByteOffset: 0, BitPosition: 4},
		{Name: "n", Type: encoding.FieldTypeU32, ByteOffset: 99},
		{Name: "country", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: encoding.NewDictionary()},
	}}
}

type fieldPos struct{ off, bit int }

func positions(s *encoding.Schema) []fieldPos {
	out := make([]fieldPos, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = fieldPos{f.ByteOffset, f.BitPosition}
	}
	return out
}

// TestResolveExplicitSchema_CanonicalLayout pins that the resolver's copy
// is laid out by encx.LayoutFields whatever offsets the caller supplied —
// a bit-packed field owns a whole byte at bit 0 — on both the resolved and
// the empty-source path, and that the caller's schema is left alone.
func TestResolveExplicitSchema_CanonicalLayout(t *testing.T) {
	t.Parallel()

	want := []fieldPos{{0, 0}, {1, 0}, {2, 0}, {6, 0}}
	for _, src := range []struct {
		name   string
		header []string
	}{
		{"resolved", []string{"flag", "level", "n", "country"}},
		{"empty source", nil},
	} {
		t.Run(src.name, func(t *testing.T) {
			in := layoutHostileSchema()
			before := positions(in)
			got, err := resolveExplicitSchema(&stringRowsReader{header: src.header}, in, false)
			if err != nil {
				t.Fatal(err)
			}
			if p := positions(got); !reflect.DeepEqual(p, want) {
				t.Errorf("resolved positions = %v, want %v", p, want)
			}
			if !reflect.DeepEqual(positions(in), before) {
				t.Error("resolver mutated the caller's offsets")
			}
		})
	}
}

// TestImportRun_ExplicitBitPositionIgnored is the decode-visible half:
// the writer always puts a bit-packed value in bit 0 / the low nibble,
// while the engine decoder (encx.RecordReader) reads the stored
// BitPosition. A caller-supplied nonzero BitPosition stored verbatim
// read every packed_bool back false and every u4 back 0 through the
// engine — while export, which reads the whole byte, still showed the
// right value.
func TestImportRun_ExplicitBitPositionIgnored(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	job := &ImportJob{
		Source: &stringRowsReader{
			header: []string{"flag", "level", "n", "country"},
			rows:   [][]string{{"true", "7", "42", "CA"}, {"false", "3", "9", "US"}},
		},
		Target: "o.pulse",
		FS:     fs,
		Schema: layoutHostileSchema(),
	}
	if _, err := job.Run(t.Context()); err != nil {
		t.Fatalf("import: %v", err)
	}
	raw, err := afero.ReadFile(fs, "o.pulse")
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(raw)
	ver, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := encoding.ReadSchema(r, ver)
	if err != nil {
		t.Fatal(err)
	}
	rr := encx.NewRecordReader(r, schema)
	want := []map[string]float64{
		{"flag": 1, "level": 7, "n": 42, "country": 0},
		{"flag": 0, "level": 3, "n": 9, "country": 1},
	}
	for i, w := range want {
		vals := map[string]float64{}
		if err := rr.ReadRecord(vals, map[string]bool{}); err != nil {
			t.Fatalf("ReadRecord %d: %v", i, err)
		}
		if !reflect.DeepEqual(vals, w) {
			t.Errorf("row %d = %v, want %v", i, vals, w)
		}
	}
}
